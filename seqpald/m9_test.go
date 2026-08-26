package main

import (
	"strings"
	"testing"
)

// M9 (external issuer key + two-phase clawback) seqpald orchestration tests, the
// tests-owner deliverable. They reuse the M5-M8 harness (the openampd stub h.oa,
// extended additively with the two-phase clawback build/complete endpoints and an
// externalAssets flag; the escrow node stub h.seq; register/deploy helpers).
//
// COVERAGE
//  1. Deploy: an external deploy passes the entity's OWN browser x-only as
//     issuer_pubkey (the issuer of record), persists issuer_external, and refuses a
//     foreign key; a legacy deploy sends no key and stays byte-identical.
//  2. Console clawback path selection: an external-issuer asset builds the sweep
//     (reason logged, nothing broadcast), surfaces the L_claw sighashes + a
//     complete_url, and sweeps ONLY after the issuer's signature completes it; a
//     legacy asset sweeps in one call. Completion is idempotent (no double-sweep).
//  3. Stranded-key runbook: an external-issuer asset pauses at
//     awaiting_clawback_sig with the sighashes surfaced, and resumes to a made-whole
//     re-delivery once the issuer's clawback_sigs arrive; a legacy asset sweeps
//     server-side in one pass (M7 behaviour, unchanged).
//
// FUND-SAFETY invariant (reused from M5-M8): exactly one sweep per (asset, holder);
// the reason is logged before broadcast; a replay never double-sweeps.

// deployLiveExternal deploys a private-placement offering whose enclave issuer half
// is the deploying account's OWN browser key (the M9 external path), and marks the
// asset external at the policy-server stub so its clawback is two-phase. It returns
// the issuance id, asset id, escrow AID, and the issuer x-only.
func (h *m5h) deployLiveExternal(session, xonly, ticker, jurisdiction string) (issID, assetID, escrowAID string) {
	h.t.Helper()
	terms := map[string]any{
		"jurisdictions": map[string]any{jurisdiction: map[string]any{"access": "standard"}},
		"price":         1.0,
	}
	issID = h.createIssuance(session, ticker+" Co", ticker, terms)
	dep := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000000, "precision": 2, "issuer_pubkey": xonly,
	})
	if dep.code != 200 {
		h.t.Fatalf("external deploy: %d %s", dep.code, dep.errMsg())
	}
	if dep.body["issuer_external"] != true {
		h.t.Fatalf("external deploy response must flag issuer_external: %s", dep.raw)
	}
	if dep.body["issuer_pubkey"] != xonly {
		h.t.Fatalf("external deploy must echo the entity key %s, got %v", xonly, dep.body["issuer_pubkey"])
	}
	assetID, _ = dep.body["asset"].(string)
	escrowAID, _ = dep.body["escrow_aid"].(string)
	if assetID == "" || escrowAID == "" {
		h.t.Fatalf("external deploy did not return asset/escrow: %s", dep.raw)
	}
	h.oa.markExternal(assetID, xonly)
	return
}

// ===========================================================================
// 1. Deploy wiring.
// ===========================================================================

// TestM9Deploy_ExternalKeyIsOwnBrowserKey proves deploy accepts the entity's own
// x-only as issuer_pubkey (persisted issuer_external + issuer_pubkey), while a
// legacy deploy without the field stays non-external, and a FOREIGN key is refused
// (a platform-held or third-party key could re-introduce unilateral custody).
func TestM9Deploy_ExternalKeyIsOwnBrowserKey(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	session, _, xonly := h.register(genPriv(t), "Issuer", "HN")

	// External deploy: the account's own key.
	issID, assetID, _ := h.deployLiveExternal(session, xonly, "EXTA", "HN")
	iss, err := h.s.st.IssuanceByID(issID)
	if err != nil || iss == nil {
		t.Fatalf("issuance lookup: %v", err)
	}
	if !iss.IssuerExternal || !strings.EqualFold(iss.IssuerPubkey, xonly) {
		t.Fatalf("external issuance must persist issuer_external + the entity key: %#v", iss)
	}
	_ = assetID

	// A foreign key (not this account's own) is refused.
	otherIssID := h.createIssuance(session, "FRGN Co", "FRGN", map[string]any{
		"jurisdictions": map[string]any{"HN": map[string]any{"access": "standard"}}, "price": 1.0,
	})
	_, _, foreign := h.register(genPriv(t), "Someone Else", "HN")
	bad := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": otherIssID, "supply": 1000000, "precision": 2, "issuer_pubkey": foreign,
	})
	if bad.code != 400 {
		t.Fatalf("a foreign issuer_pubkey must be refused with 400, got %d: %s", bad.code, bad.raw)
	}

	// A deploy without an explicit key still uses this account's own key: every asset
	// is external, so the platform never holds a key that can move a holder's position.
	extIss, extAsset, _ := h.deployLivePrivate(session, "LEGA", "HN", 1.0)
	li, _ := h.s.st.IssuanceByID(extIss)
	if li == nil || !li.IssuerExternal {
		t.Fatalf("every deploy must be external (the issuer holds the clawback key): %#v", li)
	}
	_ = extAsset
}

// ===========================================================================
// 2. Console clawback path selection.
// ===========================================================================

// TestM9Console_ExternalClawbackIsTwoPhase proves the freeze/clawback console
// routes an external-issuer asset through build -> issuer-sign -> complete: the
// build surfaces the L_claw sighashes and a complete_url but broadcasts nothing
// (the holder's balance is untouched); completion sweeps exactly once; and a replay
// of completion returns the same txid without a second sweep.
func TestM9Console_ExternalClawbackIsTwoPhase(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	session, _, xonly := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, _ := h.deployLiveExternal(session, xonly, "TWOP", "HN")

	holder := h.regHolder("Holder", "HN")
	h.oa.setBalance(holder, assetID, 500)

	// BUILD: two-phase, sighashes surfaced, nothing swept.
	build := h.do("POST", "/api/issuances/"+issID+"/clawback", session, map[string]any{
		"holder_aid": holder, "reason": "court order 7",
	})
	if build.code != 200 {
		t.Fatalf("clawback build: %d %s", build.code, build.errMsg())
	}
	if build.body["two_phase"] != true {
		t.Fatalf("external clawback must be two_phase: %s", build.raw)
	}
	if _, hasTxid := build.body["txid"]; hasTxid {
		t.Fatalf("build must not broadcast a txid: %s", build.raw)
	}
	if build.body["pubkey"] != xonly {
		t.Fatalf("build must name the issuer x-only to sign with, got %v", build.body["pubkey"])
	}
	toSign, _ := build.body["to_sign"].([]any)
	if len(toSign) != 1 {
		t.Fatalf("build must surface the L_claw sighashes, got %v", build.body["to_sign"])
	}
	cid, _ := build.body["clawback_id"].(string)
	completeURL, _ := build.body["complete_url"].(string)
	if cid == "" || completeURL == "" {
		t.Fatalf("build must return a clawback_id and complete_url: %s", build.raw)
	}
	if bal := h.oa.balanceOf(holder, assetID); bal != 500 {
		t.Fatalf("build must not sweep: holder balance = %d, want 500", bal)
	}
	if h.oa.pendingClawCount() != 1 {
		t.Fatalf("build must leave exactly one pending sweep, got %d", h.oa.pendingClawCount())
	}

	// COMPLETE: the issuer's signature finishes the sweep (one broadcast).
	comp := h.do("POST", completeURL, session, map[string]any{
		"sigs": map[string]string{"0": strings.Repeat("ab", 64)},
	})
	if comp.code != 200 {
		t.Fatalf("clawback complete: %d %s", comp.code, comp.errMsg())
	}
	txid, _ := comp.body["txid"].(string)
	if txid == "" {
		t.Fatalf("complete must return a sweep txid: %s", comp.raw)
	}
	if got := jsonAtoms(comp.body["atoms"]); got != 500 {
		t.Fatalf("complete swept %d atoms, want 500", got)
	}
	if bal := h.oa.balanceOf(holder, assetID); bal != 0 {
		t.Fatalf("complete must sweep the holder: balance = %d, want 0", bal)
	}
	if h.oa.pendingClawCount() != 0 {
		t.Fatalf("complete must consume the pending sweep, got %d", h.oa.pendingClawCount())
	}

	// IDEMPOTENT: a replay of complete returns the same txid and never re-sweeps.
	replay := h.do("POST", completeURL, session, map[string]any{
		"sigs": map[string]string{"0": strings.Repeat("ab", 64)},
	})
	if replay.code != 200 {
		t.Fatalf("clawback complete replay: %d %s", replay.code, replay.errMsg())
	}
	if rt, _ := replay.body["txid"].(string); rt != txid {
		t.Fatalf("replay must return the same txid %s, got %s", txid, rt)
	}
	if bal := h.oa.balanceOf(holder, assetID); bal != 0 {
		t.Fatalf("replay must not change the swept balance: %d", bal)
	}
}

// ===========================================================================
// 3. Stranded-key runbook (external issuer key).
// ===========================================================================

// TestM9Redeliver_ExternalPausesForIssuerSignatureThenCompletes proves the
// stranded-key runbook for an external-issuer asset pauses at awaiting_clawback_sig
// (surfacing the L_claw sighashes, sweeping nothing), and resumes to a made-whole
// re-delivery once the issuer's clawback_sigs arrive. A replay is idempotent.
func TestM9Redeliver_ExternalPausesForIssuerSignatureThenCompletes(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	issuerSession, _, issuerX := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, escrowAID := h.deployLiveExternal(issuerSession, issuerX, "STREXT", "HN")

	oldAID := h.regHolder("Returning Investor", "HN")
	h.s.st.UpsertClaims(&Claims{AID: oldAID, Residence: "HN", BaseEligibility: "ret", Status: "verified"})
	h.oa.setBalance(oldAID, assetID, 700)
	newAID := h.regHolder("Returning Investor New Key", "HN")
	// The custodied source (offering escrow enclave) funds the re-delivery leg under
	// the disclosed pre-M9 treasury delegation; the seizure itself needs the issuer.
	h.oa.setBalance(escrowAID, assetID, 1000)

	h.s.cfg.adminAIDs = map[string]bool{}
	adminSession, adminAID, _ := h.register(genPriv(t), "Reviewer", "HN")
	h.s.cfg.adminAIDs[adminAID] = true

	// First call: the runbook pauses for the issuer's clawback signature.
	paused := h.do("POST", "/api/id/redeliver", adminSession, map[string]any{
		"issuance_id": issID, "old_aid": oldAID, "new_aid": newAID, "reason": "key loss attested",
	})
	if paused.code != 200 {
		t.Fatalf("redeliver build: %d %s", paused.code, paused.errMsg())
	}
	rdObj, _ := paused.body["redelivery"].(map[string]any)
	if st, _ := rdObj["state"].(string); st != "awaiting_clawback_sig" {
		t.Fatalf("external redeliver must pause at awaiting_clawback_sig, got %q: %s", st, paused.raw)
	}
	if paused.body["two_phase"] != true || paused.body["pubkey"] != issuerX {
		t.Fatalf("paused runbook must surface the issuer key + sighashes: %s", paused.raw)
	}
	if _, hasToSign := paused.body["to_sign"]; !hasToSign {
		t.Fatalf("paused runbook must surface to_sign: %s", paused.raw)
	}
	// Nothing swept, nothing delivered while paused.
	if bal := h.oa.balanceOf(oldAID, assetID); bal != 700 {
		t.Fatalf("paused runbook must sweep nothing: old AID = %d, want 700", bal)
	}
	if bal := h.oa.balanceOf(newAID, assetID); bal != 0 {
		t.Fatalf("paused runbook must deliver nothing: new AID = %d, want 0", bal)
	}

	// Resume with the issuer's signatures: sweep + re-deliver + made-whole.
	done := h.do("POST", "/api/id/redeliver", adminSession, map[string]any{
		"issuance_id": issID, "old_aid": oldAID, "new_aid": newAID, "reason": "key loss attested",
		"clawback_sigs": map[string]string{"0": strings.Repeat("cd", 64)},
	})
	if done.code != 200 {
		t.Fatalf("redeliver resume: %d %s", done.code, done.errMsg())
	}
	doneObj, _ := done.body["redelivery"].(map[string]any)
	if st, _ := doneObj["state"].(string); st != "complete" {
		t.Fatalf("resumed runbook state = %q, want complete: %s", st, done.raw)
	}
	if swept := jsonAtoms(doneObj["swept_atoms"]); swept != 700 {
		t.Fatalf("swept_atoms = %d, want 700", swept)
	}
	if bal := h.oa.balanceOf(oldAID, assetID); bal != 0 {
		t.Fatalf("old AID after sweep = %d, want 0", bal)
	}
	if bal := h.oa.balanceOf(newAID, assetID); bal != 700 {
		t.Fatalf("new AID after re-delivery = %d, want 700", bal)
	}

	// IDEMPOTENT: replaying the completed runbook re-sweeps and re-delivers nothing.
	sweeps := h.oa.clawbackCount()
	deliveries := h.oa.transfers()
	replay := h.do("POST", "/api/id/redeliver", adminSession, map[string]any{
		"issuance_id": issID, "old_aid": oldAID, "new_aid": newAID, "reason": "key loss attested",
		"clawback_sigs": map[string]string{"0": strings.Repeat("cd", 64)},
	})
	if replay.code != 200 {
		t.Fatalf("redeliver replay: %d %s", replay.code, replay.errMsg())
	}
	if h.oa.clawbackCount() != sweeps {
		t.Fatalf("replay re-built a sweep: clawbacks = %d, want %d", h.oa.clawbackCount(), sweeps)
	}
	if h.oa.transfers() != deliveries {
		t.Fatalf("replay re-delivered: transfers = %d, want %d", h.oa.transfers(), deliveries)
	}
	if h.oa.pendingClawCount() != 0 {
		t.Fatalf("no pending sweep must remain after completion, got %d", h.oa.pendingClawCount())
	}
}
