package main

import (
	"net/http"
	"strings"
	"time"
)

// Linking more wallets to one SeqPal ID.
//
// Descriptor wallets are unlimited: they are just places a holder keeps keys,
// and recognising more of them only ever helps -- it is how a holding key can be
// admitted to a whitelist without a separate signature, and how a holder can
// sign in from whichever wallet is in front of them.
//
// An ENCLAVE wallet is limited to one, and the limit is not arbitrary. The
// enclave account is where restricted assets settle; a second one would make
// "which account do this ID's restricted assets live in" a question with two
// answers, and every transfer, clawback and register entry would have to pick.
// One account, one enclave.

type linkWalletReq struct {
	// A descriptor wallet.
	Descriptor string `json:"descriptor"`
	Challenge  string `json:"challenge"`
	Sig        string `json:"sig"`
	// An enclave wallet: the same tagged challenge an OpenAMP account signs in with.
	XOnly string `json:"xonly"`
	Label string `json:"label"`
}

// handleAccountWallets is GET /api/account/wallets: every wallet this ID is held
// in, and whether it can reach restricted assets.
func (s *server) handleAccountWallets(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	wallets, err := s.st.AccountWallets(acct.AID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	writeJSON(w, 200, map[string]any{
		"wallets":     wallets,
		"has_enclave": s.hasEnclave(acct),
	})
}

// handleLinkWallet is POST /api/account/wallets: prove another wallet and add it.
func (s *server) handleLinkWallet(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	var req linkWalletReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	label := strings.TrimSpace(req.Label)
	if len(label) > 64 {
		label = label[:64]
	}

	switch {
	case strings.TrimSpace(req.Descriptor) != "":
		s.linkDescriptorWallet(w, acct, &req, label)
	case strings.TrimSpace(req.XOnly) != "":
		s.linkEnclaveWallet(w, acct, &req, label)
	default:
		writeErr(w, 400, "send either a wallet descriptor or the x-only key of an OpenAMP account")
	}
}

func (s *server) linkDescriptorWallet(w http.ResponseWriter, acct *Account, req *linkWalletReq, label string) {
	desc, verifyDesc, err := s.walletDescriptors(req.Descriptor)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	// Already linked here, or somewhere else. Saying which matters: one is a
	// no-op and the other is a conflict the holder has to resolve.
	if owner, err := s.st.AccountByDescriptor(desc); err != nil {
		writeErr(w, 500, "store error")
		return
	} else if owner != nil {
		if owner.AID == acct.AID {
			writeJSON(w, 200, map[string]any{"note": "this wallet is already linked to your SeqPal ID"})
			return
		}
		writeErr(w, 409, "that wallet is already linked to a different SeqPal ID")
		return
	}
	// A wallet nobody has linked but which IS a SeqPal ID of its own would be two
	// identities for one wallet; the holder has to decide which one they want.
	if other, err := s.st.AccountByAID(walletIDFor(verifyDesc)); err != nil {
		writeErr(w, 500, "store error")
		return
	} else if other != nil && other.AID != acct.AID {
		writeErr(w, 409, "that wallet already has a SeqPal ID of its own; sign in with it instead")
		return
	}

	address, err := s.walletAddressAt(verifyDesc, walletProofIndex)
	if err != nil {
		writeErr(w, 502, "%v", err)
		return
	}
	// Proof, in two steps like every other wallet exchange: ask for a challenge,
	// come back with the signature.
	if strings.TrimSpace(req.Sig) == "" {
		if ok, why := s.chalRL.allow(walletIDFor(verifyDesc)); !ok {
			writeErr(w, 429, "%s", why)
			return
		}
		challenge, exp, err := s.st.CreateChallenge(walletIDFor(verifyDesc), walletChallengeTTL)
		if err != nil {
			writeErr(w, 500, "could not issue a challenge")
			return
		}
		showAddr, _ := s.walletAddressAt(desc, walletProofIndex)
		if showAddr == "" {
			showAddr = address
		}
		writeJSON(w, 200, map[string]any{
			"descriptor": desc, "address": showAddr, "index": walletProofIndex,
			"challenge": challenge, "expires_at": exp,
			"note": "sign the challenge with this address in that wallet, then send it back as sig",
		})
		return
	}
	if err := s.st.PeekChallenge(req.Challenge, walletIDFor(verifyDesc)); err != nil {
		writeErr(w, 401, "%v", err)
		return
	}
	if err := s.verifyWalletMessage(address, req.Sig, req.Challenge); err != nil {
		writeErr(w, 401, "%v", err)
		return
	}
	if err := s.st.ConsumeChallenge(req.Challenge, walletIDFor(verifyDesc)); err != nil {
		writeErr(w, 401, "%v", err)
		return
	}
	wl := &AccountWallet{
		ID: mustID(), AID: acct.AID, Kind: "descriptor", Descriptor: desc,
		Label: label, Proof: "signature",
	}
	if err := s.st.InsertAccountWallet(wl); err != nil {
		writeErr(w, 409, "could not link that wallet: %v", err)
		return
	}
	s.st.Audit(acct.AID, "wallet.link", map[string]any{"kind": "descriptor", "address": address})
	writeJSON(w, 200, map[string]any{"wallet": wl})
}

func (s *server) linkEnclaveWallet(w http.ResponseWriter, acct *Account, req *linkWalletReq, label string) {
	if s.hasEnclave(acct) {
		writeErr(w, 409, "this SeqPal ID already holds an OpenAMP account. Restricted assets settle "+
			"in one account, so a second would leave no answer to which one they settle in")
		return
	}
	auth := &authReq{XOnly: strings.ToLower(strings.TrimSpace(req.XOnly)), Challenge: req.Challenge, Sig: req.Sig}
	if !validXOnly(auth.XOnly) {
		writeErr(w, 400, "xonly must be a valid 32-byte x-only public key in lowercase hex")
		return
	}
	if strings.TrimSpace(req.Sig) == "" {
		if ok, why := s.chalRL.allow(auth.XOnly); !ok {
			writeErr(w, 429, "%s", why)
			return
		}
		challenge, exp, err := s.st.CreateChallenge(auth.XOnly, walletChallengeTTL)
		if err != nil {
			writeErr(w, 500, "could not issue a challenge")
			return
		}
		writeJSON(w, 200, map[string]any{
			"xonly": auth.XOnly, "challenge": challenge, "expires_at": exp, "tag": challengeTag,
			"note": "sign this challenge TAGGED with your enclave key, then send it back as sig",
		})
		return
	}
	enclaveAID, err := s.authenticate(auth)
	if err != nil {
		writeErr(w, 401, "%v", err)
		return
	}
	if owner, err := s.st.AccountByEnclaveKey(auth.XOnly); err != nil {
		writeErr(w, 500, "store error")
		return
	} else if owner != nil {
		if owner.AID == acct.AID {
			writeJSON(w, 200, map[string]any{"note": "that OpenAMP account is already linked"})
			return
		}
		writeErr(w, 409, "that OpenAMP account is already linked to a different SeqPal ID")
		return
	}
	if other, err := s.st.AccountByAID(enclaveAID); err != nil {
		writeErr(w, 500, "store error")
		return
	} else if other != nil && other.AID != acct.AID {
		writeErr(w, 409, "that OpenAMP account is a SeqPal ID of its own; sign in with it instead")
		return
	}
	if other, err := s.st.AccountByXOnly(auth.XOnly); err != nil {
		writeErr(w, 500, "store error")
		return
	} else if other != nil && other.AID != acct.AID {
		writeErr(w, 409, "that enclave key already belongs to another account")
		return
	}

	wl := &AccountWallet{
		ID: mustID(), AID: acct.AID, Kind: "enclave", XOnly: auth.XOnly,
		EnclaveAID: enclaveAID, Label: label, Proof: "tagged-challenge",
	}
	if err := s.st.InsertAccountWallet(wl); err != nil {
		// The database holds the one-enclave rule where it cannot be raced. Losing
		// that race is the same situation the check above describes, so it reads
		// the same way rather than leaking a constraint name.
		if strings.Contains(err.Error(), "UNIQUE") {
			writeErr(w, 409, "this SeqPal ID already holds an OpenAMP account. Restricted assets "+
				"settle in one account, so a second would leave no answer to which one they settle in")
			return
		}
		writeErr(w, 409, "could not link that OpenAMP account: %v", err)
		return
	}
	// accounts.xonly stays the ID's single OpenAMP identity, which everything
	// downstream reads; linking the first enclave is what sets it.
	if acct.XOnly == "" {
		_ = s.st.AttachEnclave(acct.AID, auth.XOnly)
	}
	s.st.Audit(acct.AID, "wallet.link", map[string]any{"kind": "enclave", "enclave_aid": enclaveAID})
	s.catchUpPolicyServer(acct, auth.XOnly, enclaveAID)

	updated, _ := s.st.AccountByAID(acct.AID)
	writeJSON(w, 200, map[string]any{"wallet": wl, "account": updated, "enclave_aid": enclaveAID})
}

// handleUnlinkWallet is DELETE /api/account/wallets/{wid}.
func (s *server) handleUnlinkWallet(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	wl, err := s.st.AccountWalletByID(r.PathValue("wid"))
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if wl == nil || wl.AID != acct.AID {
		writeErr(w, 404, "no such wallet on this SeqPal ID")
		return
	}
	all, err := s.st.AccountWallets(acct.AID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if len(all) <= 1 {
		writeErr(w, 409, "this is the only wallet on this SeqPal ID; unlinking it would leave no "+
			"way to sign in. Link another first")
		return
	}
	// Unlinking the enclave would strand whatever it holds: the assets stay in
	// that account, and this ID would no longer be able to reach them.
	if wl.Kind == "enclave" {
		writeErr(w, 409, "an OpenAMP account cannot be unlinked: restricted assets settle in it, "+
			"and unlinking would leave them held by an account this ID can no longer act for")
		return
	}
	if err := s.st.DeleteAccountWallet(wl.ID, acct.AID); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	s.st.Audit(acct.AID, "wallet.unlink", map[string]any{"kind": wl.Kind, "wallet_id": wl.ID})
	writeJSON(w, 200, map[string]any{"unlinked": wl.ID})
}

// hasEnclave reports whether this ID holds an OpenAMP account, from the wallets
// it has linked. The account row's own identity is the fallback for a row that
// predates linked wallets and has not been read since.
func (s *server) hasEnclave(acct *Account) bool {
	if acct == nil {
		return false
	}
	if ok, err := s.st.HasEnclaveWallet(acct.AID); err == nil && ok {
		return true
	}
	return acct.HasEnclave() && acct.XOnly != ""
}

// catchUpPolicyServer registers a newly linked enclave and stamps the
// eligibility this ID already has, so verifying before linking is not punished
// by having to verify again. Neither step is fatal: the wallet IS linked.
func (s *server) catchUpPolicyServer(acct *Account, xonly, enclaveAID string) {
	var problem string
	if registered, err := s.registerUser(xonly); err != nil {
		problem = "register: " + err.Error()
	} else if registered != enclaveAID {
		problem = "the policy server returned an unexpected account id"
	} else if _, err := s.writeCategoriesFor(acct.AID, enclaveAID); err != nil {
		problem = "stamp categories: " + err.Error()
	}
	if problem != "" {
		s.st.Audit(acct.AID, "wallet.link.catchup_failed", map[string]any{
			"enclave_aid": enclaveAID, "error": problem, "at": time.Now().Unix(),
		})
	}
}

// enclaveAIDOf is the account id the POLICY SERVER knows this SeqPal ID by.
//
// For an ID founded on an OpenAMP account the two are the same id, which is why
// passing acct.AID to openampd worked everywhere for as long as that was the
// only kind of ID there was. For an ID founded as a wallet that later attached
// one they are different: the SeqPal account id is the one it was created with
// and never changes, while the policy server knows only the account derived from
// the enclave key. Asking openampd about the SeqPal id then asks about an
// account it has never heard of, and the answer -- no categories, not
// registered, no address -- is indistinguishable from a holder who has nothing.
//
// Returns "" when there is no OpenAMP account at all, which callers must treat
// as "there is nothing to ask about" rather than as an id.
func (s *server) enclaveAIDOf(acct *Account) string {
	if acct == nil {
		return ""
	}
	if wallets, err := s.st.AccountWallets(acct.AID); err == nil {
		for _, wl := range wallets {
			if wl.Kind == "enclave" && wl.EnclaveAID != "" {
				return wl.EnclaveAID
			}
		}
	}
	// An account from before linked wallets: its own id IS the enclave id.
	if acct.HasEnclave() && acct.XOnly != "" {
		return acct.AID
	}
	return ""
}

// enclaveAIDFor is enclaveAIDOf by account id, for the paths that have one.
func (s *server) enclaveAIDFor(aid string) string {
	acct, err := s.st.AccountByAID(aid)
	if err != nil || acct == nil {
		return ""
	}
	return s.enclaveAIDOf(acct)
}

// openampAIDFor turns whatever account id a caller has into the one the policy
// server answers about.
//
// A SeqPal account id maps to the enclave account that ID has linked, which is a
// different id whenever the ID was founded as a wallet. An id that is not a
// SeqPal account is already an openampd id (a venue quoting one, an escrow
// enclave, a counterparty) and passes through unchanged. An ID with no OpenAMP
// account maps to "", which every caller reads as "there is nothing there",
// because that is exactly what it is.
func (s *server) openampAIDFor(aid string) string {
	if aid == "" {
		return ""
	}
	acct, err := s.st.AccountByAID(aid)
	if err != nil || acct == nil {
		return aid
	}
	return s.enclaveAIDOf(acct)
}
