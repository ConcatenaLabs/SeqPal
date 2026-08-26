package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Signing in with a WALLET rather than an enclave key.
//
// The exchange is deliberately the plainest thing that proves control: SeqPal
// names an address derived from the holder's own descriptor, the holder signs
// the challenge with it in whatever wallet they already use, and the node
// checks the signature. Nothing here is SeqPal-specific, which is the point --
// a hardware wallet or a node wallet can do it today, and those are exactly the
// wallets that have no enclave key and were shut out before.

type walletChallengeReq struct {
	Descriptor string `json:"descriptor"`
}

// handleWalletChallenge is POST /api/auth/wallet/challenge. It answers with the
// address to sign with and the challenge to sign, plus the account id this
// descriptor maps to so the caller can tell registration from sign-in.
func (s *server) handleWalletChallenge(w http.ResponseWriter, r *http.Request) {
	var req walletChallengeReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	desc, verifyDesc, err := s.walletDescriptors(req.Descriptor)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	// Two addresses for one key: the one the holder's wallet shows them, which
	// is what they have to recognise and select, and the legacy form the node
	// needs, which they never see and should not have to.
	address, err := s.walletAddressAt(desc, walletProofIndex)
	if err != nil {
		writeErr(w, 502, "%v", err)
		return
	}
	id := walletIDFor(verifyDesc)
	// The challenge is keyed by the account id, not by a key: a wallet account
	// has no enclave key to key it on.
	challenge, exp, err := s.st.CreateChallenge(id, challengeTTL)
	if err != nil {
		writeErr(w, 500, "could not issue a challenge")
		return
	}
	existing, err := s.st.AccountByAID(id)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	writeJSON(w, 200, map[string]any{
		"account_id": id,
		"descriptor": desc,
		"address":    address,
		"index":      walletProofIndex,
		"challenge":  challenge,
		"expires_at": exp,
		"registered": existing != nil,
		"note": "sign the challenge with this address in your wallet, then post the signature " +
			"back. The address is address " + fmt.Sprint(walletProofIndex) + " of the descriptor " +
			"you gave, derived from public data alone; SeqPal never sees a key",
	})
}

type walletAuthReq struct {
	Descriptor  string          `json:"descriptor"`
	Challenge   string          `json:"challenge"`
	Sig         string          `json:"sig"`
	Kind        string          `json:"kind"`
	DisplayName string          `json:"display_name"`
	Residence   string          `json:"residence"`
	IDNumber    string          `json:"id_number"`
	Profile     json.RawMessage `json:"profile"`
}

// authenticateWallet consumes the challenge and checks the signed message.
// Returns the canonical descriptor, the account id and the address that signed.
func (s *server) authenticateWallet(req *walletAuthReq) (desc, id, address string, err error) {
	desc, verifyDesc, err := s.walletDescriptors(req.Descriptor)
	if err != nil {
		return "", "", "", err
	}
	id = walletIDFor(verifyDesc)
	if err = s.st.ConsumeChallenge(req.Challenge, id); err != nil {
		return "", "", "", err
	}
	// The signature is checked against the LEGACY form of the same key, because
	// that is the only form verifymessage accepts. The holder signed with the
	// address their wallet showed them; this is the same key, written the way
	// the verifier needs.
	address, err = s.walletAddressAt(verifyDesc, walletProofIndex)
	if err != nil {
		return "", "", "", err
	}
	if err = s.verifyWalletMessage(address, req.Sig, req.Challenge); err != nil {
		return "", "", "", err
	}
	return desc, id, address, nil
}

func (s *server) handleWalletRegister(w http.ResponseWriter, r *http.Request) {
	var req walletAuthReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	desc, id, address, err := s.authenticateWallet(&req)
	if err != nil {
		s.st.Audit("", "auth.wallet.register.refused", map[string]any{"reason": err.Error()})
		writeErr(w, 401, "%v", err)
		return
	}
	if existing, err := s.st.AccountByAID(id); err != nil {
		writeErr(w, 500, "store error")
		return
	} else if existing != nil {
		writeErr(w, 409, "an account already exists for this wallet; sign in instead")
		return
	}
	kind := req.Kind
	if kind != "individual" && kind != "corporate" {
		kind = "individual"
	}
	profile := req.Profile
	if len(profile) == 0 {
		profile, _ = json.Marshal(map[string]any{"residence": req.Residence})
	}
	acct := &Account{
		AID:         id,
		Kind:        kind,
		XOnly:       "", // no enclave key: that is what identity "xpub" means
		DisplayName: strings.TrimSpace(req.DisplayName),
		IDNumber:    strings.TrimSpace(req.IDNumber),
		Profile:     profile,
		CreatedAt:   time.Now().Unix(),
		Identity:    "xpub",
		Descriptor:  desc,
	}
	if err := s.st.CreateAccount(acct); err != nil {
		writeErr(w, 409, "could not create the account: %v", err)
		return
	}
	s.st.Audit(id, "auth.wallet.register", map[string]any{
		"kind": acct.Kind, "display_name": acct.DisplayName, "address": address,
	})
	s.openSession(w, acct)
	writeJSON(w, 200, map[string]any{"account": acct})
}

func (s *server) handleWalletLogin(w http.ResponseWriter, r *http.Request) {
	var req walletAuthReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	_, id, _, err := s.authenticateWallet(&req)
	if err != nil {
		s.st.Audit("", "auth.wallet.login.refused", map[string]any{"reason": err.Error()})
		writeErr(w, 401, "%v", err)
		return
	}
	acct, err := s.st.AccountByAID(id)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if acct == nil {
		writeErr(w, 404, "no account exists for this wallet; register first")
		return
	}
	s.st.Audit(id, "auth.wallet.login", nil)
	s.openSession(w, acct)
	writeJSON(w, 200, map[string]any{"account": acct})
}

// handleAttachEnclave is POST /api/auth/attach-enclave (authenticated). It binds
// an OpenAMP enclave key to a wallet-backed account, which is what lifts the
// restriction on restricted assets. The proof is the same tagged challenge an
// enclave account signs in to with, so nothing new is trusted here.
func (s *server) handleAttachEnclave(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	var req authReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	if acct.HasEnclave() {
		writeErr(w, 409, "this account already has an OpenAMP account attached")
		return
	}
	enclaveAID, err := s.authenticate(&req)
	if err != nil {
		s.st.Audit(acct.AID, "auth.attach_enclave.refused", map[string]any{"reason": err.Error()})
		writeErr(w, 401, "%v", err)
		return
	}
	// The enclave key must not already be somebody else's account.
	if other, err := s.st.AccountByAID(enclaveAID); err != nil {
		writeErr(w, 500, "store error")
		return
	} else if other != nil {
		writeErr(w, 409, "that OpenAMP account is already a SeqPal ID of its own; sign in with it instead")
		return
	}
	if other, err := s.st.AccountByXOnly(req.XOnly); err != nil {
		writeErr(w, 500, "store error")
		return
	} else if other != nil {
		writeErr(w, 409, "that enclave key is already attached to another account")
		return
	}
	if err := s.st.AttachEnclave(acct.AID, req.XOnly); err != nil {
		writeErr(w, 500, "could not attach the OpenAMP account: %v", err)
		return
	}
	s.st.Audit(acct.AID, "auth.attach_enclave", map[string]any{"xonly": req.XOnly, "enclave_aid": enclaveAID})
	updated, _ := s.st.AccountByAID(acct.AID)
	writeJSON(w, 200, map[string]any{"account": updated, "enclave_aid": enclaveAID})
}

// requireEnclave guards the paths that only an OpenAMP enclave account can
// take. A wallet-backed account is refused here rather than deeper down, where
// the failure would surface as a missing AID or an unregistered key and read
// like a fault instead of a boundary.
//
// What is NOT behind this gate is the point of the whole arrangement: supervised
// (freely-tradable) stocks, network-enforced OpenDAMP assets, corporate actions
// and their claims, documents, entities and verification are all reachable by a
// wallet that has no enclave at all.
func (s *server) requireEnclave(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if acct := principal(r); acct != nil && !acct.HasEnclave() {
			writeErr(w, 403, "this SeqPal ID is a wallet with no OpenAMP account attached, so it "+
				"cannot hold or move OpenAMP restricted assets. Attach an OpenAMP account to it "+
				"and this works; freely-tradable stocks and network-enforced assets do not need one")
			return
		}
		next(w, r)
	}
}
