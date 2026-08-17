package main

import (
	"fmt"
	"net/http"
	"strings"
)

// Investor payout mandates (M7 section 2). A distribution pays ONLY a registered
// ordinary wallet address captured via a BIP340-signed mandate in the portal:
// {asset_or_chain, address, signature, signer_xonly}. The endpoint validates that
// the signature is a tagged mandate signature by the investor's OWN registered
// key (the M5 mandate tag + verifyTaggedByKey), that the address is a valid
// ordinary Sequentia address, and it REJECTS an enclave key-path / 2-of-2 address
// (no wallet scans an enclave address; paying one would strand the funds). For
// M7, USDX distributions are Sequentia-only; the tBTC payout leg is the plan's
// explicit cut, so the only chain accepted here is sequentia.

type investorMandateReq struct {
	Chain       string `json:"chain"` // sequentia
	Asset       string `json:"asset"`
	Address     string `json:"address"`
	Signature   string `json:"signature"`
	SignerXOnly string `json:"signer_xonly"`
}

// investorMandateStatement builds the canonical bytes the investor signs. The
// role and investor_aid discriminators keep an issuer-side payout mandate
// signature (same tag) from ever being replayed as an investor mandate.
func investorMandateStatement(investorAID, chain, asset, addr string) []byte {
	stmt, _ := canonicalJSON(map[string]any{
		"role": "investor", "investor_aid": investorAID, "chain": chain, "asset": asset, "address": addr,
	})
	return stmt
}

// handleInvestorMandate is POST /api/mandates/investor (session): register the
// caller's own BIP340-signed payout mandate. Two-phase like the issuer mandate:
// with no signature it returns the exact bytes to sign; with a signature it
// verifies and stores. The address is network-validated and enclave-rejected.
func (s *server) handleInvestorMandate(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	var req investorMandateReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	chain := strings.ToLower(strings.TrimSpace(req.Chain))
	if chain == "" {
		chain = "sequentia"
	}
	if chain != "sequentia" {
		// tBTC payouts are the plan's explicit M7 cut; distributions are USDX-only.
		writeErr(w, 400, "only the sequentia chain is supported for payout mandates in this milestone (USDX distributions)")
		return
	}
	addr := strings.TrimSpace(req.Address)
	if err := s.validateSeqAddress(addr); err != nil {
		writeErr(w, 400, "address is not a valid Sequentia address: %v", err)
		return
	}

	// Reject an enclave key-path address: a distribution must pay an ordinary
	// wallet-scanned address, never a 2-of-2 enclave script no wallet monitors.
	// Resolve the investor's own enclave addresses across the SeqPal-managed assets
	// and refuse a mandate address matching any of them.
	if isEnclave, err := s.isInvestorEnclaveAddress(acct.AID, addr); err != nil {
		// Fail closed: if we cannot confirm the address is NOT an enclave address,
		// refuse rather than risk registering a payout address that strands funds.
		writeErr(w, 503, "cannot verify the payout address right now; please retry")
		return
	} else if isEnclave {
		writeErr(w, 400, "the payout address is one of your enclave key-path addresses; provide an ordinary wallet address a wallet can scan")
		return
	}

	signer := strings.TrimSpace(req.SignerXOnly)
	if signer == "" {
		signer = acct.XOnly
	}
	if signer != acct.XOnly {
		writeErr(w, 403, "the mandate must be signed by your own SeqPal ID key")
		return
	}
	statement := investorMandateStatement(acct.AID, chain, req.Asset, addr)
	if strings.TrimSpace(req.Signature) == "" {
		writeJSON(w, 200, map[string]any{
			"sign_this": string(statement), "tag": mandateTag,
			"note": "sign these canonical bytes with your SeqPal ID key, then resubmit with signature",
		})
		return
	}
	if err := verifyTaggedByKey(signer, mandateTag, statement, req.Signature); err != nil {
		writeErr(w, 400, "the mandate signature does not verify for your key")
		return
	}
	m := &InvestorMandate{
		InvestorAID: acct.AID, Chain: chain, Asset: strings.TrimSpace(req.Asset), Address: addr,
		Signature: req.Signature, SignerXOnly: signer,
	}
	if err := s.st.UpsertInvestorMandate(m); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	s.st.Audit(acct.AID, "investor.mandate.register", map[string]any{"chain": chain, "address": addr})
	writeJSON(w, 200, map[string]any{"mandate": m})
}

// handleInvestorMandateGet is GET /api/mandates/investor?chain=sequentia
// (session): the caller's current mandate for a chain, or null.
func (s *server) handleInvestorMandateGet(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	chain := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("chain")))
	if chain == "" {
		chain = "sequentia"
	}
	m, err := s.st.InvestorMandateFor(acct.AID, chain)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	writeJSON(w, 200, map[string]any{"chain": chain, "mandate": m})
}

// isInvestorEnclaveAddress reports whether addr is one of this investor's enclave
// key-path addresses. openampd derives a per-asset 2-of-2 enclave address from the
// investor's registered key; we resolve it for each SeqPal-managed live issuance
// asset and match. This is the "resolve the investor's enclave scriptPubKey and
// refuse an address matching it" check the contract requires; it catches the exact
// stranding scenario, an investor pasting the enclave address where their
// restricted tokens live as the payout address.
//
// It FAILS CLOSED: the second return is non-nil when the check could not be
// completed (the store or any per-asset openampd probe was unreachable), so a
// definitive "not an enclave address" (false, nil) is distinguishable from "could
// not determine" (false, err). Callers must refuse the mandate / skip the payout on
// a non-nil error rather than risk stranding funds at an unresolved enclave address.
func (s *server) isInvestorEnclaveAddress(investorAID, addr string) (bool, error) {
	if addr == "" {
		return false, nil
	}
	issuances, err := s.st.LiveIssuances()
	if err != nil {
		return false, fmt.Errorf("enclave check: live issuances: %w", err)
	}
	var probeErr error
	for _, iss := range issuances {
		if iss.AssetID == "" {
			continue
		}
		// A bearer (supervised) asset has no enclave: it is minted through the
		// node's raw path and the policy server never learns it, so there is no
		// enclave address to resolve for it and a probe would fail closed for no
		// reason, refusing every payout address while any bearer asset is live.
		if iss.Enforcement == "bearer" {
			continue
		}
		var out struct {
			Address string `json:"address"`
		}
		if err := s.callOpenAMP("GET", "/v1/users/"+investorAID+"/address?asset="+iss.AssetID, "", nil, &out); err != nil {
			// Remember the failure but keep probing: a later asset may still yield a
			// definitive match. If none matches, an unresolved asset means we cannot
			// be sure, so we return the error (fail closed).
			probeErr = fmt.Errorf("enclave check: resolve %s: %w", iss.AssetID, err)
			continue
		}
		if out.Address != "" && out.Address == addr {
			return true, nil
		}
	}
	return false, probeErr
}
