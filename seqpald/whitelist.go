package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Asking to be admitted to a network-enforced asset's whitelist.
//
// An OpenDAMP whitelist is a list of holding keys the issuer publishes, and the
// chain enforces it on both sides of every regulated transfer. Nothing adds a
// holder automatically, which left a verified SeqPal ID with nowhere to present
// itself: the credential existed, and the only way to use it was to find the
// issuer out of band. This is the missing step.
//
// It is per ASSET, because a whitelist is per asset. Being admitted to one says
// nothing about any other, and an issuer admitting a holder is a decision about
// their own asset only.
//
// The key a holder asks to have admitted must be one they control, or the
// credential launders eligibility rather than proving it: a verified holder
// could otherwise get somebody else's key -- an unverified party's -- onto a
// list that exists precisely to keep it off. Control is proven two ways, and
// both are recorded on the request:
//
//	"descriptor"  the key derives from the wallet this account signed in with,
//	              which was proven at sign-in. Nothing further to do.
//	"signature"   a signed message from that key, checked by the node, for an
//	              account that registered no descriptor.

// How far down a wallet's receive chain to look for a key it claims. A holder
// who has handed out many addresses is ordinary; one past this is not, and the
// refusal tells them to sign instead rather than silently failing.
const whitelistScanWindow = 200

const whitelistRequestTag = "seqpal-whitelist-request-v1"

// whitelistRequestStatement is the exact text a holder signs to prove the key
// is theirs. It names the asset and the account, so a signature for one asset
// cannot be replayed to get the same key admitted to another.
func whitelistRequestStatement(issuanceID, assetID, aid, holdingKey string) string {
	stmt, _ := canonicalJSON(map[string]any{
		"v": 1, "purpose": "whitelist-request", "issuance_id": issuanceID,
		"asset": assetID, "aid": aid, "holding_key": holdingKey,
	})
	return string(stmt)
}

// keyDerivesFromAccount reports whether a holding key is one of the addresses of
// the wallet this account signed in with. The account's descriptor was proven at
// sign-in, so a key inside it needs no further proof.
func (s *server) keyDerivesFromAccount(acct *Account, holdingKey string) (bool, error) {
	wallets, err := s.st.DescriptorWallets(acct.AID)
	if err != nil {
		return false, err
	}
	if len(wallets) == 0 {
		return false, nil
	}
	// The whitelist names an x-only key; the wallet's addresses come from the
	// compressed key, whose parity x-only drops. Both parities are checked,
	// because only one of them is the key the wallet actually holds and the
	// request does not say which.
	for _, prefix := range []string{"02", "03"} {
		desc, err := s.canonicalDescriptor("pkh(" + prefix + holdingKey + ")")
		if err != nil {
			continue
		}
		want, err := s.walletAddressAt(desc, 0)
		if err != nil {
			continue
		}
		for _, wl := range wallets {
			mine, err := s.walletAddressRange(toPKH(wl.Descriptor), whitelistScanWindow)
			if err != nil {
				continue
			}
			for _, a := range mine {
				if a == want {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// verifyHoldingKeySignature checks a signed message from the holding key itself.
// The address is derived from the key rather than supplied, so a holder cannot
// present a signature from some other key they happen to control.
func (s *server) verifyHoldingKeySignature(holdingKey, sig, message string) error {
	var lastErr error
	for _, prefix := range []string{"02", "03"} {
		desc, err := s.canonicalDescriptor("pkh(" + prefix + holdingKey + ")")
		if err != nil {
			lastErr = err
			continue
		}
		addr, err := s.walletAddressAt(desc, 0)
		if err != nil {
			lastErr = err
			continue
		}
		if err := s.verifyWalletMessage(addr, sig, message); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("that signature does not verify for this key")
	}
	return fmt.Errorf("the signature does not prove this key is yours: %v", lastErr)
}

type whitelistRequestReq struct {
	HoldingKey string `json:"holding_key"`
	Sig        string `json:"sig"`
	Note       string `json:"note"`
}

// handleWhitelistRequest is POST /api/issuances/{id}/whitelist-requests.
func (s *server) handleWhitelistRequest(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss, err := s.st.IssuanceByID(r.PathValue("id"))
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if iss == nil || iss.Status != "live" {
		writeErr(w, 404, "no live issuance with that id")
		return
	}
	if iss.Enforcement != "network" {
		writeErr(w, 400, "this asset is not network-enforced, so it has no whitelist to join. "+
			"A policy-co-signed asset admits holders through its transfer agent, and a "+
			"freely-tradable one admits everybody")
		return
	}

	// Eligibility is the whole point of asking: an unverified holder has nothing
	// to present, and the issuer would only be deciding on a name.
	claims, err := s.st.ClaimsByAID(acct.AID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if !eligibilityLive(claims, time.Now().Unix()) {
		writeErr(w, 403, "verify your identity before asking to be admitted: an issuer is "+
			"admitting an eligible holder, and that is what verification establishes")
		return
	}

	var req whitelistRequestReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	key := strings.ToLower(strings.TrimSpace(req.HoldingKey))
	if !validXOnly(key) {
		writeErr(w, 400, "holding_key must be a 32-byte x-only public key in hex: the key that "+
			"will own the tokens, which is what the network checks")
		return
	}

	// Proof of control, or the credential admits keys their holder does not have.
	proof := ""
	derives, err := s.keyDerivesFromAccount(acct, key)
	if err != nil {
		writeErr(w, 502, "could not check that key against your wallet: %v", err)
		return
	}
	if derives {
		proof = "descriptor"
	} else {
		stmt := whitelistRequestStatement(iss.ID, iss.AssetID, acct.AID, key)
		if strings.TrimSpace(req.Sig) == "" {
			writeJSON(w, 200, map[string]any{
				"sign_this": stmt,
				"note": "sign these exact characters as a message with the address of the key you " +
					"want admitted, then send the signature back as sig. That is what proves the " +
					"key is yours rather than one you copied",
			})
			return
		}
		if err := s.verifyHoldingKeySignature(key, req.Sig, stmt); err != nil {
			writeErr(w, 400, "%v", err)
			return
		}
		proof = "signature"
	}

	if existing, err := s.st.OpenWhitelistRequest(iss.ID, key); err != nil {
		writeErr(w, 500, "store error")
		return
	} else if existing != nil {
		if existing.AID != acct.AID {
			writeErr(w, 409, "another SeqPal ID has already asked to have this key admitted")
			return
		}
		writeJSON(w, 200, map[string]any{"request": existing, "note": "this request is already open"})
		return
	}

	wr := &WhitelistRequest{
		ID: mustID(), IssuanceID: iss.ID, AssetID: iss.AssetID, AID: acct.AID,
		HoldingKey: key, Proof: proof, Note: strings.TrimSpace(req.Note), State: "pending",
	}
	if err := s.st.InsertWhitelistRequest(wr); err != nil {
		writeErr(w, 500, "could not record the request: %v", err)
		return
	}
	s.st.Audit(acct.AID, "whitelist.request", map[string]any{
		"issuance_id": iss.ID, "asset": iss.AssetID, "holding_key": key, "proof": proof,
	})
	_ = s.st.InsertNotice(iss.OwnerAID, "whitelist",
		"A verified holder asked to be admitted to "+iss.Ticker+"'s whitelist.")
	writeJSON(w, 200, map[string]any{"request": wr})
}

// handleWhitelistRequests is GET /api/issuances/{id}/whitelist-requests. The
// owner sees every request with what it is worth deciding on; anyone else sees
// only their own, because who else has asked is not their business.
func (s *server) handleWhitelistRequests(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss, err := s.st.IssuanceByID(r.PathValue("id"))
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if iss == nil {
		writeErr(w, 404, "no issuance with that id")
		return
	}
	all, err := s.st.WhitelistRequestsByIssuance(iss.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	owner := iss.OwnerAID == acct.AID
	out := make([]map[string]any, 0, len(all))
	for _, wr := range all {
		if !owner && wr.AID != acct.AID {
			continue
		}
		row := map[string]any{"request": wr}
		if owner {
			// What an issuer needs in order to decide: who is asking, and what
			// their credential actually says.
			if a, _ := s.st.AccountByAID(wr.AID); a != nil {
				row["holder"] = map[string]any{
					"aid": a.AID, "display_name": a.DisplayName, "identity": a.Identity,
				}
			}
			if c, _ := s.st.ClaimsByAID(wr.AID); c != nil {
				row["categories"] = projectCategories(c, time.Now().Unix())
				row["eligibility_valid_until"] = c.ValidUntil
			}
		}
		out = append(out, row)
	}
	writeJSON(w, 200, map[string]any{"requests": out, "is_owner": owner})
}

type whitelistDecisionReq struct {
	Approve bool   `json:"approve"`
	Note    string `json:"note"`
}

// handleWhitelistDecide is POST /api/issuances/{id}/whitelist-requests/{rid}/decide
// (owner). Approving is a decision, not a publication: the key reaches the
// published list when a policy change carries it, which is the same two-phase,
// issuer-signed path every other change to that list takes.
func (s *server) handleWhitelistDecide(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.networkIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	wr, err := s.st.WhitelistRequestByID(r.PathValue("rid"))
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if wr == nil || wr.IssuanceID != iss.ID {
		writeErr(w, 404, "no such request on this issuance")
		return
	}
	if wr.State != "pending" {
		writeErr(w, 409, "this request is already %s", wr.State)
		return
	}
	var req whitelistDecisionReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	state := "refused"
	if req.Approve {
		state = "approved"
	}
	if err := s.st.UpdateWhitelistRequestFields(wr.ID, map[string]any{
		"state": state, "decided_by": acct.AID, "decided_at": time.Now().Unix(),
		"decision_note": strings.TrimSpace(req.Note), "updated_at": time.Now().Unix(),
	}); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	s.st.Audit(acct.AID, "whitelist.decide", map[string]any{
		"issuance_id": iss.ID, "request_id": wr.ID, "holding_key": wr.HoldingKey, "state": state,
	})
	msg := "Your request to hold " + iss.Ticker + " was refused."
	if req.Approve {
		msg = "Your request to hold " + iss.Ticker + " was approved. It takes effect when the " +
			"issuer publishes the updated list."
	}
	_ = s.st.InsertNotice(wr.AID, "whitelist", msg)

	updated, _ := s.st.WhitelistRequestByID(wr.ID)
	resp := map[string]any{"request": updated}
	if req.Approve {
		pending, _ := s.st.ApprovedWhitelistKeys(iss.ID)
		keys := make([]string, 0, len(pending))
		for _, p := range pending {
			keys = append(keys, p.HoldingKey)
		}
		resp["approved_keys"] = keys
		resp["note"] = "approved. Publish these keys with a policy change to put them on the list; " +
			"nothing changes on chain until you do"
	}
	writeJSON(w, 200, resp)
}

// noteWhitelistInclusions records that a published policy change carried some
// approved keys. Called after a policy op completes, so "included" only ever
// means the list actually moved.
func (s *server) noteWhitelistInclusions(issuanceID, policyOpID string, holdersJSON json.RawMessage) {
	var keys []string
	if err := json.Unmarshal(holdersJSON, &keys); err != nil || len(keys) == 0 {
		return
	}
	if err := s.st.MarkWhitelistRequestsIncluded(issuanceID, policyOpID, keys); err != nil {
		s.st.Audit("", "whitelist.include_failed", map[string]any{
			"issuance_id": issuanceID, "policy_op_id": policyOpID, "error": err.Error(),
		})
	}
}
