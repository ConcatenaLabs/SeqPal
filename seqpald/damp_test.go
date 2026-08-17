package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// M12: network-enforced deploys.
//
// The two things worth proving here are what seqpald FORWARDS (the policy
// server's request shape is a contract, and getting the holder list or the
// election name wrong is silent and expensive) and what it PERSISTS (a
// network-enforced issuance's facts are different facts: no escrow, no rules,
// a policy commitment and two on-chain addresses instead).

func newDampHarness(t *testing.T) *m5h {
	t.Helper()
	h := newM5Harness(t, m5opts{})
	h.s.cfg.damp = true
	return h
}

func dampIssuance(t *testing.T, h *m5h, session string) string {
	t.Helper()
	return h.createIssuance(session, "Net Co", "NETX", map[string]any{
		"jurisdictions": map[string]any{"HN": map[string]any{"access": "standard"}}, "price": 1.0,
	})
}

// TestDampDeployPreparesThenCompletes is the whole path: the first attempt
// prepares and refuses with the registrar document, the second completes, and
// the persisted row carries the network-enforcement facts.
func TestDampDeployPreparesThenCompletes(t *testing.T) {
	h := newDampHarness(t)
	session, _, xonly := h.register(genPriv(t), "Issuer", "HN")
	issID := dampIssuance(t, h, session)

	// --- attempt 1: prepare, then refuse with what the registrar must run -----
	first := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000, "precision": 2, "enforcement": "network",
	})
	if first.code != 409 {
		t.Fatalf("first attempt = %d %s, want 409 (prepared, awaiting the registrar)", first.code, first.raw)
	}
	if !strings.Contains(first.errMsg(), "opendamp derive") {
		t.Fatalf("the refusal must name the command to run: %q", first.errMsg())
	}
	if !strings.Contains(first.errMsg(), "Nothing has been minted") {
		t.Fatalf("the refusal must say nothing was minted: %q", first.errMsg())
	}
	pi, _ := first.body["policy_commitment"].(string)
	if pi == "" || first.body["registrar_document"] == nil || first.body["asset"] == "" {
		t.Fatalf("the refusal must carry everything the registrar needs: %s", first.raw)
	}
	// The holder list defaulted to exactly the issuer's own key.
	holders, _ := first.body["holders"].([]any)
	if len(holders) != 1 || holders[0] != xonly {
		t.Fatalf("holder list = %v, want exactly the issuer's own key %s", holders, xonly)
	}

	// What was forwarded to the policy server.
	h.oa.mu.Lock()
	preps := append([]map[string]any(nil), h.oa.dampPrepareBodies...)
	h.oa.mu.Unlock()
	if len(preps) != 1 {
		t.Fatalf("want exactly one prepare, got %d", len(preps))
	}
	p := preps[0]
	if p["holder_pubkey"] != xonly {
		t.Fatalf("holder_pubkey = %v, want the session key %s", p["holder_pubkey"], xonly)
	}
	if p["issuer_update_key"] != xonly {
		t.Fatalf("issuer_update_key = %v, want the session key", p["issuer_update_key"])
	}
	if p["ticker"] != "NETX" || p["name"] != "Net Co" {
		t.Fatalf("forwarded identity is wrong: %v", p)
	}
	if p["network"] != "testnet" {
		t.Fatalf("network = %v, want the chain name the derivation tool takes", p["network"])
	}
	if p["burn_allowed"] != false {
		t.Fatalf("burn_allowed = %v, want false", p["burn_allowed"])
	}
	if p["verifier_amount"] != float64(1) {
		t.Fatalf("verifier_amount = %v, want the default 1", p["verifier_amount"])
	}
	if th, _ := p["terms_hash"].(string); len(th) != 64 {
		t.Fatalf("the committed terms hash must ride along: %v", p["terms_hash"])
	}
	// A network deploy must NOT send a policy-server rule set or a holder AID:
	// there is no enclave and there are no server-side rules.
	for _, forbidden := range []string{"rules", "holder_aid", "issuer_aid", "clawback"} {
		if _, has := p[forbidden]; has {
			t.Fatalf("a network deploy must not forward %q: %v", forbidden, p)
		}
	}
	// And it provisioned no escrow enclave.
	if k, _ := h.s.st.EnclaveKeyByRef(enclaveOfferingEscrow, issID); k != nil {
		t.Fatal("a network deploy must not provision an escrow enclave")
	}
	// Still a draft.
	iss, _ := h.s.st.IssuanceByID(issID)
	if iss.Status == "live" {
		t.Fatal("a prepared-but-unminted issuance must not be live")
	}
	if iss.Enforcement != "network" {
		t.Fatalf("the election was not persisted: %q", iss.Enforcement)
	}

	// --- attempt 2: with the registrar's values, it completes -----------------
	second := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000, "precision": 2, "enforcement": "network",
		"user_cmr": strings.Repeat("1a", 32), "verifier_cmr": strings.Repeat("2b", 32),
		"issuer_cmr": strings.Repeat("3c", 32), "pi": pi,
	})
	if second.code != 200 {
		t.Fatalf("second attempt = %d %s", second.code, second.raw)
	}
	asset, _ := second.body["asset"].(string)
	if asset == "" || second.body["enforcement"] != "network" {
		t.Fatalf("deploy response is wrong: %s", second.raw)
	}
	if second.body["holder_covenant_address"] == "" || second.body["verifier_covenant_address"] == "" {
		t.Fatalf("the deploy must return both on-chain addresses: %s", second.raw)
	}

	// It prepared exactly ONCE: the second attempt resumed the stored handoff.
	h.oa.mu.Lock()
	nPrep, completes := len(h.oa.dampPrepareBodies), append([]map[string]any(nil), h.oa.dampCompleteBodies...)
	h.oa.mu.Unlock()
	if nPrep != 1 {
		t.Fatalf("the second attempt prepared again (%d preparations); a retry must resume", nPrep)
	}
	if len(completes) != 1 {
		t.Fatalf("want exactly one completion, got %d", len(completes))
	}
	c := completes[0]
	if c["user_cmr"] != strings.Repeat("1a", 32) || c["verifier_cmr"] != strings.Repeat("2b", 32) ||
		c["issuer_cmr"] != strings.Repeat("3c", 32) || c["pi"] != pi {
		t.Fatalf("the registrar's values were not forwarded verbatim: %v", c)
	}

	// The persisted row.
	iss, _ = h.s.st.IssuanceByID(issID)
	if iss.Status != "live" || iss.AssetID != asset {
		t.Fatalf("issuance did not go live: %+v", iss)
	}
	if iss.Enforcement != "network" {
		t.Fatalf("enforcement = %q", iss.Enforcement)
	}
	if iss.PolicyCommitment != pi || iss.WhitelistRoot == "" || iss.VerifierAsset == "" || iss.VerifierAmount != 1 {
		t.Fatalf("the network-enforcement binding was not persisted: %+v", iss)
	}
	if iss.HolderCovenantAddress == "" || iss.VerifierCovenantAddress == "" {
		t.Fatalf("the two on-chain addresses were not persisted: %+v", iss)
	}
	if iss.Clawback {
		t.Fatal("a network-enforced asset has no clawback path; the row must not claim one")
	}
	if iss.IssuerPubkey != xonly || !iss.IssuerExternal {
		t.Fatalf("the issuer of record must be the session key: %+v", iss)
	}
	// The handoff was consumed, so a later retry hits deploy idempotency rather
	// than preparing a second verifier asset.
	if prep, err := h.s.st.DampPrepare(issID); err != nil || prep != nil {
		t.Fatalf("the handoff must be consumed once the issuance is live (%v, %v)", prep, err)
	}
}

// TestDampDeployRefusesWithoutCovenantParams: the refusal is stable across
// retries and never prepares twice, so an issuer who submits again before
// running the derivation does not mint a second verifier asset.
func TestDampDeployRefusesWithoutCovenantParams(t *testing.T) {
	h := newDampHarness(t)
	session, aid, _ := h.register(genPriv(t), "Issuer", "HN")
	issID := dampIssuance(t, h, session)

	body := map[string]any{"issuance_id": issID, "supply": 1000, "precision": 2, "enforcement": "network"}
	first := h.do("POST", "/api/deploy", session, body)
	second := h.do("POST", "/api/deploy", session, body)
	if first.code != 409 || second.code != 409 {
		t.Fatalf("both attempts must refuse: %d, %d", first.code, second.code)
	}
	if first.body["policy_commitment"] != second.body["policy_commitment"] {
		t.Fatal("the policy commitment must be stable across retries; a shifting one invalidates a registrar run")
	}
	h.oa.mu.Lock()
	nPrep := len(h.oa.dampPrepareBodies)
	h.oa.mu.Unlock()
	if nPrep != 1 {
		t.Fatalf("retrying prepared again (%d preparations)", nPrep)
	}
	assertAuditedM10(t, h, aid, "deploy.refused")

	// A malformed value from the registrar is a 400, not a mint attempt.
	bad := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000, "precision": 2, "enforcement": "network",
		"user_cmr": "nothex", "verifier_cmr": strings.Repeat("2b", 32),
		"pi": first.body["policy_commitment"],
	})
	if bad.code != 400 {
		t.Fatalf("a malformed program identity = %d, want 400: %s", bad.code, bad.raw)
	}
	h.oa.mu.Lock()
	nComplete := len(h.oa.dampCompleteBodies)
	h.oa.mu.Unlock()
	if nComplete != 0 {
		t.Fatalf("a malformed value reached the policy server (%d completions)", nComplete)
	}
}

// TestDampDeployRefusesMismatchedCommitment: a derivation run against the wrong
// policy is refused here, before the policy server is asked to mint.
func TestDampDeployRefusesMismatchedCommitment(t *testing.T) {
	h := newDampHarness(t)
	session, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID := dampIssuance(t, h, session)
	_ = h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000, "precision": 2, "enforcement": "network",
	})

	r := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000, "precision": 2, "enforcement": "network",
		"user_cmr": strings.Repeat("1a", 32), "verifier_cmr": strings.Repeat("2b", 32),
		"pi": strings.Repeat("ff", 32),
	})
	if r.code != 409 || !strings.Contains(r.errMsg(), "policy commitment does not match") {
		t.Fatalf("mismatched commitment = %d %q, want 409", r.code, r.errMsg())
	}
	h.oa.mu.Lock()
	nComplete := len(h.oa.dampCompleteBodies)
	h.oa.mu.Unlock()
	if nComplete != 0 {
		t.Fatalf("a mismatched commitment was sent to the policy server (%d completions)", nComplete)
	}
	if iss, _ := h.s.st.IssuanceByID(issID); iss.Status == "live" {
		t.Fatal("a refused completion went live")
	}
}

// TestDampDeployHolderListForwarded: an explicit holder list is normalized,
// always includes the issuer, and is what the commitment is prepared over.
func TestDampDeployHolderListForwarded(t *testing.T) {
	h := newDampHarness(t)
	session, _, xonly := h.register(genPriv(t), "Issuer", "HN")
	issID := dampIssuance(t, h, session)
	other := xonlyHex(t, genPriv(t))

	r := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000, "precision": 2, "enforcement": "network",
		// Deliberately messy: uppercase, a duplicate, and no issuer key.
		"whitelist": []string{strings.ToUpper(other), other},
	})
	if r.code != 409 {
		t.Fatalf("prepare = %d %s", r.code, r.raw)
	}
	h.oa.mu.Lock()
	p := h.oa.dampPrepareBodies[0]
	h.oa.mu.Unlock()
	wl, _ := p["whitelist"].([]any)
	if len(wl) != 2 || wl[0] != other || wl[1] != xonly {
		t.Fatalf("holder list = %v, want [%s %s] (deduped, lowercased, issuer appended)", wl, other, xonly)
	}

	// Deploying with a DIFFERENT list after preparing is refused: the commitment
	// already covers the prepared list.
	again := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000, "precision": 2, "enforcement": "network",
		"whitelist": []string{xonlyHex(t, genPriv(t))},
	})
	if again.code != 409 || !strings.Contains(again.errMsg(), "different holder list") {
		t.Fatalf("changing the holder list after preparing = %d %q, want 409", again.code, again.errMsg())
	}

	// A malformed key never reaches the policy server.
	h2 := newDampHarness(t)
	s2, _, _ := h2.register(genPriv(t), "Issuer", "HN")
	iss2 := dampIssuance(t, h2, s2)
	bad := h2.do("POST", "/api/deploy", s2, map[string]any{
		"issuance_id": iss2, "supply": 1000, "precision": 2, "enforcement": "network",
		"whitelist": []string{"not-a-key"},
	})
	if bad.code != 400 {
		t.Fatalf("a malformed holder key = %d, want 400: %s", bad.code, bad.raw)
	}
	h2.oa.mu.Lock()
	n := len(h2.oa.dampPrepareBodies)
	h2.oa.mu.Unlock()
	if n != 0 {
		t.Fatal("a malformed holder key reached the policy server")
	}
}

// TestDampDeployNeedsTheCapability: the election is recorded either way, and a
// deployment without the capability refuses before anything is prepared.
func TestDampDeployNeedsTheCapability(t *testing.T) {
	h := newM5Harness(t, m5opts{}) // damp OFF
	session, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID := dampIssuance(t, h, session)
	r := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000, "precision": 2, "enforcement": "network",
	})
	if r.code != 501 || !strings.Contains(r.errMsg(), "network enforcement is not available") {
		t.Fatalf("network deploy without the capability = %d %q, want 501", r.code, r.errMsg())
	}
	if iss, _ := h.s.st.IssuanceByID(issID); iss.Enforcement != "network" {
		t.Fatalf("the election must be recorded even when refused: %q", iss.Enforcement)
	}
	h.oa.mu.Lock()
	n := len(h.oa.dampPrepareBodies)
	h.oa.mu.Unlock()
	if n != 0 {
		t.Fatal("a refused election prepared something")
	}
}

// TestDampServicedDeployUnaffected: the co-signed path is untouched. It still
// provisions an escrow enclave, still compiles and forwards rules, and stores no
// network-enforcement fields.
func TestDampServicedDeployUnaffected(t *testing.T) {
	h := newDampHarness(t) // capability ON, but this deploy elects serviced
	session, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, escrowAID := h.deployLivePrivate(session, "SVCD", "HN", 1.0)
	if assetID == "" || escrowAID == "" {
		t.Fatal("the serviced deploy did not produce an asset and an escrow")
	}
	if k, _ := h.s.st.EnclaveKeyByRef(enclaveOfferingEscrow, issID); k == nil {
		t.Fatal("the serviced deploy stopped provisioning an escrow enclave")
	}
	h.oa.mu.Lock()
	rules, _ := h.oa.assets[assetID]["rules"].(json.RawMessage)
	h.oa.mu.Unlock()
	if len(rules) == 0 || string(rules) == "null" {
		t.Fatalf("the serviced deploy stopped forwarding compiled rules: %s", rules)
	}
	iss, _ := h.s.st.IssuanceByID(issID)
	if iss.Enforcement == "network" || iss.PolicyCommitment != "" || iss.VerifierAsset != "" ||
		iss.WhitelistRoot != "" || iss.HolderCovenantAddress != "" || iss.VerifierCovenantAddress != "" {
		t.Fatalf("a serviced issuance gained network-enforcement state: %+v", iss)
	}
	// And it serializes without any of the new keys.
	raw, err := json.Marshal(iss)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"policy_commitment", "verifier_asset", "whitelist_root",
		"holder_covenant_address", "verifier_covenant_address"} {
		if strings.Contains(string(raw), `"`+k+`"`) {
			t.Fatalf("a serviced issuance record must omit %q: %s", k, raw)
		}
	}
	h.oa.mu.Lock()
	n := len(h.oa.dampPrepareBodies)
	h.oa.mu.Unlock()
	if n != 0 {
		t.Fatal("a serviced deploy touched the network-enforcement endpoint")
	}
}
