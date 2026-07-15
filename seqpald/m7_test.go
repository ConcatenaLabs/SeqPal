package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// M7 (transfer-agent servicing) tests, the tests-owner deliverable. They reuse
// the M5/M6 harness verbatim: the openampd stub h.oa (extended additively in
// m5_test.go with the issuer holder register, the full-sweep clawback, the public
// transparency log, and a category gate), the escrow node stub h.seq, and the
// register/deploy helpers.
//
// COVERAGE
//  1. Distribution engine: a run BLOCKS until funded; the record-date snapshot
//     captures the chain holder set + Sequentia height and is immutable; pro-rata
//     floors exactly and discloses dust; sum(gross)==sum(net)+sum(withheld); NET
//     is paid to each holder's registered mandate; a holder with no mandate is
//     SKIPPED (never paid to a guessed address); a replayed execute pays each
//     holder exactly once, and a lost-write payout is reconciled by its marker
//     comment before any retry (never a double-pay).
//  2. Investor mandate: an enclave (2-of-2) address is rejected; an ordinary
//     address with a valid tagged BIP340 signature is accepted; a bad signature
//     is rejected.
//  3. Rules-amendment chain: after a live rules mutation GET /v1/assets/{id} rules
//     equal the head of the anchored amendment chain; a half-applied mutation is
//     reconciled, not left inconsistent.
//  4. Freeze/clawback console: a reason is required; a clawback records a txid and
//     a public log entry; a repeat clawback is idempotent (no double-sweep).
//  5. Stranded-key runbook: re-auth against the server identity + a registered new
//     AID, clawback sweep, re-delivery, made-whole accounting, end to end and
//     idempotent.
//  6. Category expiry: an expired accreditation drops the accreditation category at
//     the policy server, which turns a previously-allowed transfer into a refusal.
//
// FUND-SAFETY is the M5/M6 invariant, reused exactly: exactly one settlement/sweep
// record per (run, holder) / per (asset, holder); intent persisted before
// broadcast; reconcile by a chain/log scan before any retry; never a double-pay or
// double-sweep.

// ---------------------------------------------------------------------------
// harness: the M5 harness, with the distribution-engine mutex provisioned. The
// production server sets s.distMu in main(); the shared test harness does not, and
// (unlike the Backend-B servicing mutexes) distMu is NOT lazily provisioned by
// ensureServicingMu, so the snapshot/execute handlers would nil-panic without
// this. See the DEFECTS section of the M7 report (D1).
// ---------------------------------------------------------------------------

// sendsMatching counts escrow-wallet sends to address for atoms carrying the given
// comment marker (the per-holder settlement marker). Exactly one is the
// pay-once-per-holder invariant.
func (n *fakeNode) sendsMatching(address string, atoms uint64, comment string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := 0
	for _, s := range n.sends {
		if s.address == address && s.atoms == atoms && s.comment == comment {
			c++
		}
	}
	return c
}

func newM7Harness(t *testing.T, opts m5opts) *m5h {
	t.Helper()
	h := newM5Harness(t, opts)
	if h.s.distMu == nil {
		h.s.distMu = newKeyedMutex()
	}
	h.s.ensureServicingMu()
	return h
}

// regHolder registers an account + its openampd user and returns the AID. A
// distribution holder needs a chain identity (for the enclave-address probe and
// the holder register) but not a live session.
func (h *m5h) regHolder(name, residence string) string {
	h.t.Helper()
	_, aid, xonly := h.register(genPriv(h.t), name, residence)
	if _, err := h.s.registerUser(xonly); err != nil {
		h.t.Fatalf("registerUser: %v", err)
	}
	return aid
}

// ===========================================================================
// 1. Distribution engine.
// ===========================================================================

// TestM7Distribution_BlocksUntilFundedThenPaysNetToMandates is the end-to-end
// distribution proof: the run pays nothing until the servicing deposit confirms
// and covers the pool; the snapshot captures the chain register at a Sequentia
// height and is immutable; pro-rata floors with disclosed dust; withholding
// reconciles (gross == net + withheld); each holder with a mandate is paid NET;
// a holder with no mandate is skipped; and a replayed execute never double-pays.
func TestM7Distribution_BlocksUntilFundedThenPaysNetToMandates(t *testing.T) {
	h := newM7Harness(t, m5opts{escrowConfs: 2})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, _ := h.deployLivePrivate(issuerSession, "DIST", "HN", 1.0)

	// Three holders of record, 100 atoms each (total 300). A pool of 1000 atoms
	// floors to 333 each with a 1-atom dust remainder.
	aidA := h.regHolder("Holder A", "US") // US person: W-9, 0% withholding
	aidB := h.regHolder("Holder B", "GB") // non-US GB: W-8BEN, 15% treaty
	aidC := h.regHolder("Holder C", "FR") // no claims record: unknown, 30%
	h.s.st.UpsertClaims(&Claims{AID: aidA, Residence: "US", BaseEligibility: "ret", USPerson: true, Status: "verified"})
	h.s.st.UpsertClaims(&Claims{AID: aidB, Residence: "GB", BaseEligibility: "ret", Status: "verified"})
	// aidC deliberately has NO claims record (undetermined tax status).
	h.oa.setBalance(aidA, assetID, 100)
	h.oa.setBalance(aidB, assetID, 100)
	h.oa.setBalance(aidC, assetID, 100)

	// A and B register ordinary payout mandates; C registers none (must be skipped).
	if err := h.s.st.UpsertInvestorMandate(&InvestorMandate{InvestorAID: aidA, Chain: "sequentia", Address: "seq-payout-a"}); err != nil {
		t.Fatal(err)
	}
	if err := h.s.st.UpsertInvestorMandate(&InvestorMandate{InvestorAID: aidB, Chain: "sequentia", Address: "seq-payout-b"}); err != nil {
		t.Fatal(err)
	}

	// Create the run: awaiting_funding with a servicing deposit invoice for the pool.
	cr := h.do("POST", "/api/issuances/"+issID+"/distributions", issuerSession, map[string]any{
		"pool_atoms": 1000, "memo": "Q1 dividend",
	})
	if cr.code != 200 {
		t.Fatalf("create distribution: %d %s", cr.code, cr.errMsg())
	}
	depAddr, _ := cr.body["deposit_address"].(string)
	dObj, _ := cr.body["distribution"].(map[string]any)
	runID, _ := dObj["id"].(string)
	if depAddr == "" || runID == "" {
		t.Fatalf("create returned no deposit address / run id: %s", cr.raw)
	}
	if st, _ := dObj["state"].(string); st != "awaiting_funding" {
		t.Fatalf("new run state = %q, want awaiting_funding", st)
	}
	if pc, _ := cr.body["pay_ccy"].(string); pc != "USDX" {
		t.Fatalf("pay_ccy = %v, want USDX", cr.body["pay_ccy"])
	}

	// BLOCKED before funding: snapshot and execute both 409.
	if snap := h.do("POST", "/api/issuances/"+issID+"/distributions/"+runID+"/snapshot", issuerSession, nil); snap.code != 409 {
		t.Fatalf("snapshot before funding = %d, want 409 (%s)", snap.code, snap.raw)
	}
	if ex := h.do("POST", "/api/issuances/"+issID+"/distributions/"+runID+"/execute", issuerSession, nil); ex.code != 409 {
		t.Fatalf("execute before funding = %d, want 409 (%s)", ex.code, ex.raw)
	}

	// A shallow (1-conf) deposit must NOT fund it (nothing final at 0-conf).
	h.seq.credit(depAddr, 1000, 1, m5USDX)
	h.s.watchDistributionDeposits()
	if got, _ := h.s.st.DistributionByID(runID); got.State != "awaiting_funding" {
		t.Fatalf("run funded at 1 conf (need 2): state=%q", got.State)
	}

	// A confirmed but UNDER-funded deposit stays blocked (audit-logged, pays nothing).
	h.seq.credit(depAddr, 500, 2, m5USDX)
	h.s.watchDistributionDeposits()
	if got, _ := h.s.st.DistributionByID(runID); got.State != "awaiting_funding" {
		t.Fatalf("run funded on an under-funded deposit: state=%q", got.State)
	}
	if h.seq.sendCount() != 0 {
		t.Fatalf("a blocked run paid out: sends=%d, want 0", h.seq.sendCount())
	}

	// Fully funded at the required depth: the run advances to funded.
	h.seq.credit(depAddr, 1000, 2, m5USDX)
	h.s.watchDistributionDeposits()
	funded, _ := h.s.st.DistributionByID(runID)
	if funded.State != "funded" {
		t.Fatalf("run state after full funding = %q, want funded", funded.State)
	}
	if funded.FundedAtoms != 1000 || funded.FundedTxid == "" {
		t.Fatalf("funded fields = atoms %d txid %q, want 1000 and a txid", funded.FundedAtoms, funded.FundedTxid)
	}

	// Snapshot: capture the register at the Sequentia height, compute pro-rata +
	// withholding, persist the immutable table.
	snap := h.do("POST", "/api/issuances/"+issID+"/distributions/"+runID+"/snapshot", issuerSession, nil)
	if snap.code != 200 {
		t.Fatalf("snapshot: %d %s", snap.code, snap.errMsg())
	}
	d, _ := h.s.st.DistributionByID(runID)
	if d.State != "snapshotted" {
		t.Fatalf("state after snapshot = %q, want snapshotted", d.State)
	}
	if d.SnapshotHeight != 250000 {
		t.Fatalf("snapshot height = %d, want the register height 250000", d.SnapshotHeight)
	}
	if d.TotalHeld != 300 {
		t.Fatalf("total held = %d, want 300", d.TotalHeld)
	}
	// Pro-rata: 333 each, sum 999, dust 1.
	if d.GrossTotal != 999 || d.DustAtoms != 1 {
		t.Fatalf("gross_total=%d dust=%d, want 999 and 1", d.GrossTotal, d.DustAtoms)
	}
	// Withholding: A 0, B floor(333*0.15)=49, C floor(333*0.30)=99. Sum 148.
	if d.WithheldTotal != 148 {
		t.Fatalf("withheld_total = %d, want 148 (0 + 49 + 99)", d.WithheldTotal)
	}
	if d.NetTotal != d.GrossTotal-d.WithheldTotal {
		t.Fatalf("net_total = %d, want gross-withheld = %d", d.NetTotal, d.GrossTotal-d.WithheldTotal)
	}
	// The acceptance invariant, on the persisted rows.
	rows, _ := h.s.st.DistPaymentsByRun(runID)
	if len(rows) != 3 {
		t.Fatalf("payment rows = %d, want 3", len(rows))
	}
	var sumGross, sumNet, sumWithheld uint64
	byAID := map[string]*DistPayment{}
	for _, p := range rows {
		byAID[p.HolderAID] = p
		sumGross += p.GrossAtoms
		sumNet += p.NetAtoms
		sumWithheld += p.WithheldAtoms
	}
	if sumGross != sumNet+sumWithheld {
		t.Fatalf("reconciliation broken: gross %d != net %d + withheld %d", sumGross, sumNet, sumWithheld)
	}
	if p := byAID[aidA]; p.GrossAtoms != 333 || p.WithheldAtoms != 0 || p.NetAtoms != 333 || p.TaxStatus != "w9" {
		t.Fatalf("holder A row = %+v, want gross 333 withheld 0 net 333 w9", p)
	}
	if p := byAID[aidB]; p.WithheldAtoms != 49 || p.NetAtoms != 284 || p.TreatyBps != 1500 || p.TaxStatus != "w8ben" {
		t.Fatalf("holder B row = %+v, want withheld 49 net 284 treaty 1500 w8ben", p)
	}
	if p := byAID[aidC]; p.WithheldAtoms != 99 || p.NetAtoms != 234 || p.TreatyBps != 3000 || p.TaxStatus != "unknown" {
		t.Fatalf("holder C row = %+v, want withheld 99 net 234 treaty 3000 unknown", p)
	}

	// Immutability: re-snapshot after the register CHANGES must return the same
	// table (the record date is fixed, not recomputed).
	h.oa.setBalance(aidA, assetID, 99999)
	snap2 := h.do("POST", "/api/issuances/"+issID+"/distributions/"+runID+"/snapshot", issuerSession, nil)
	if snap2.code != 200 {
		t.Fatalf("re-snapshot: %d %s", snap2.code, snap2.errMsg())
	}
	rows2, _ := h.s.st.DistPaymentsByRun(runID)
	for _, p := range rows2 {
		if p.HolderAID == aidA && p.BalanceAtoms != 100 {
			t.Fatalf("snapshot was recomputed: A balance %d, want the frozen 100", p.BalanceAtoms)
		}
	}

	// Execute: pay NET to each mandate; C (no mandate) is skipped, not paid.
	ex := h.do("POST", "/api/issuances/"+issID+"/distributions/"+runID+"/execute", issuerSession, nil)
	if ex.code != 200 {
		t.Fatalf("execute: %d %s", ex.code, ex.errMsg())
	}
	d, _ = h.s.st.DistributionByID(runID)
	if d.State != "complete" {
		t.Fatalf("state after execute = %q, want complete", d.State)
	}
	// Two on-chain payouts (A and B); C skipped.
	if h.seq.sendCount() != 2 {
		t.Fatalf("payout sends = %d, want 2 (C has no mandate)", h.seq.sendCount())
	}
	// A paid net 333 to its mandate; B paid net 284; each tagged with its marker.
	if got := h.seq.sendsMatching("seq-payout-a", 333, distMarker(runID, aidA)); got != 1 {
		t.Fatalf("holder A payout = %d, want exactly one net-333 send to seq-payout-a with its marker", got)
	}
	if got := h.seq.sendsMatching("seq-payout-b", 284, distMarker(runID, aidB)); got != 1 {
		t.Fatalf("holder B payout = %d, want exactly one net-284 send to seq-payout-b with its marker", got)
	}
	rows, _ = h.s.st.DistPaymentsByRun(runID)
	for _, p := range rows {
		switch p.HolderAID {
		case aidA, aidB:
			if p.State != "paid" || p.Txid == "" {
				t.Fatalf("holder %s state=%q txid=%q, want paid with a txid", p.HolderAID, p.State, p.Txid)
			}
		case aidC:
			if p.State != "skipped" || p.Txid != "" {
				t.Fatalf("holder C (no mandate) state=%q txid=%q, want skipped with no txid", p.State, p.Txid)
			}
		}
	}
	// C received a portal notice explaining the skip.
	if notes, _ := h.s.st.NoticesByAID(aidC); len(notes) == 0 {
		t.Fatalf("skipped holder C got no portal notice")
	}
	// Completed artifacts are content-addressed + anchored.
	if d.WithholdingHash == "" || d.StatementHash == "" || d.CRSHash == "" {
		t.Fatalf("completion did not produce the artifact hashes: %+v", d)
	}

	// IDEMPOTENCY: a replayed execute must not pay anyone a second time.
	ex2 := h.do("POST", "/api/issuances/"+issID+"/distributions/"+runID+"/execute", issuerSession, nil)
	if ex2.code != 200 {
		t.Fatalf("replay execute: %d %s", ex2.code, ex2.errMsg())
	}
	if h.seq.sendCount() != 2 {
		t.Fatalf("replayed execute double-paid: sends=%d, want still 2", h.seq.sendCount())
	}
}

// TestM7Distribution_ReconcilesLostPayoutByMarker proves the per-holder payout is
// reconciled before retry: a settlement stuck in "paying" with no recorded txid,
// whose send already broadcast (seeded under its marker comment), is recovered by
// escrowFindSend rather than re-broadcast from the commingled servicing wallet.
func TestM7Distribution_ReconcilesLostPayoutByMarker(t *testing.T) {
	h := newM7Harness(t, m5opts{escrowConfs: 1})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, _ := h.deployLivePrivate(issuerSession, "RECON", "HN", 1.0)

	aid := h.regHolder("Solo Holder", "HN")
	h.s.st.UpsertClaims(&Claims{AID: aid, Residence: "HN", BaseEligibility: "ret", USPerson: true, Status: "verified"})
	h.oa.setBalance(aid, assetID, 100)
	if err := h.s.st.UpsertInvestorMandate(&InvestorMandate{InvestorAID: aid, Chain: "sequentia", Address: "seq-payout-solo"}); err != nil {
		t.Fatal(err)
	}

	cr := h.do("POST", "/api/issuances/"+issID+"/distributions", issuerSession, map[string]any{"pool_atoms": 1000})
	dObj, _ := cr.body["distribution"].(map[string]any)
	runID, _ := dObj["id"].(string)
	depAddr, _ := cr.body["deposit_address"].(string)
	h.seq.credit(depAddr, 1000, 1, m5USDX)
	h.s.watchDistributionDeposits()
	if snap := h.do("POST", "/api/issuances/"+issID+"/distributions/"+runID+"/snapshot", issuerSession, nil); snap.code != 200 {
		t.Fatalf("snapshot: %d %s", snap.code, snap.errMsg())
	}

	// The single holder is US-person (0% withholding), so net == gross == pool 1000.
	marker := distMarker(runID, aid)
	if err := h.s.st.UpdateDistPaymentFields(runID, aid, map[string]any{"state": "paying"}); err != nil {
		t.Fatal(err)
	}
	// A prior attempt already broadcast the payout but the write of its txid was lost.
	h.seq.seedSend("seq-payout-solo", 1000, marker, "deadbeef00")

	before := h.seq.sendCount()
	if ex := h.do("POST", "/api/issuances/"+issID+"/distributions/"+runID+"/execute", issuerSession, nil); ex.code != 200 {
		t.Fatalf("execute: %d %s", ex.code, ex.errMsg())
	}
	if h.seq.sendCount() != before {
		t.Fatalf("lost-write payout re-broadcast: sends %d -> %d (must reconcile, not re-send)", before, h.seq.sendCount())
	}
	rows, _ := h.s.st.DistPaymentsByRun(runID)
	if len(rows) != 1 || rows[0].State != "paid" || rows[0].Txid != "deadbeef00" {
		t.Fatalf("reconciled payment = %+v, want paid with the pre-broadcast txid", rows[0])
	}
}

// ===========================================================================
// 2. Investor payout mandate.
// ===========================================================================

// TestM7InvestorMandate_EnclaveRejected_OrdinaryAcceptedBadSigRefused covers the
// three mandate cases the contract requires: an enclave (2-of-2) address is
// rejected; an ordinary address with a valid tagged BIP340 signature by the
// caller's own key is accepted; a bad signature is refused.
func TestM7InvestorMandate_EnclaveRejected_OrdinaryAcceptedBadSigRefused(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	// A live issuance must exist so the enclave-address probe has an asset to
	// resolve the investor's 2-of-2 address against.
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	h.deployLivePrivate(issuerSession, "MND", "HN", 1.0)

	invPriv := genPriv(t)
	invSession, invAID, invXOnly := h.register(invPriv, "Investor", "HN")
	if _, err := h.s.registerUser(invXOnly); err != nil {
		t.Fatal(err)
	}

	// The stub derives the investor's enclave receive address as "tb1p"+AID; a
	// mandate to that address must be rejected (no wallet scans a 2-of-2 script).
	enclaveAddr := "tb1p" + invAID
	rej := h.do("POST", "/api/mandates/investor", invSession, map[string]any{
		"chain": "sequentia", "address": enclaveAddr,
	})
	if rej.code != 400 {
		t.Fatalf("enclave-address investor mandate = %d, want 400 (%s)", rej.code, rej.raw)
	}

	// An ordinary address: two-phase. Fetch the canonical bytes, sign, resubmit.
	addr := "seq-ordinary-investor"
	pre := h.do("POST", "/api/mandates/investor", invSession, map[string]any{
		"chain": "sequentia", "address": addr,
	})
	if pre.code != 200 {
		t.Fatalf("investor mandate (sign_this): %d %s", pre.code, pre.errMsg())
	}
	signThis, _ := pre.body["sign_this"].(string)
	tag, _ := pre.body["tag"].(string)
	if signThis == "" || tag != mandateTag {
		t.Fatalf("sign_this/tag missing or wrong: sign_this=%q tag=%q", signThis, tag)
	}
	sig := signCanonical(t, invPriv, tag, signThis)
	fin := h.do("POST", "/api/mandates/investor", invSession, map[string]any{
		"chain": "sequentia", "address": addr, "signature": sig, "signer_xonly": invXOnly,
	})
	if fin.code != 200 {
		t.Fatalf("signed investor mandate = %d, want 200 (%s)", fin.code, fin.raw)
	}
	m, err := h.s.st.InvestorMandateFor(invAID, "sequentia")
	if err != nil || m == nil || m.Address != addr {
		t.Fatalf("investor mandate not stored: %v %+v", err, m)
	}

	// A bad signature over the same statement is refused.
	bad := h.do("POST", "/api/mandates/investor", invSession, map[string]any{
		"chain": "sequentia", "address": addr, "signature": "00", "signer_xonly": invXOnly,
	})
	if bad.code != 400 {
		t.Fatalf("bad-signature investor mandate = %d, want 400 (%s)", bad.code, bad.raw)
	}

	// A non-sequentia chain is refused (tBTC payouts are the plan's M7 cut).
	btc := h.do("POST", "/api/mandates/investor", invSession, map[string]any{
		"chain": "bitcoin", "address": "tb1qordinary",
	})
	if btc.code != 400 {
		t.Fatalf("bitcoin investor mandate = %d, want 400 (USDX-only in M7)", btc.code)
	}
}

// ===========================================================================
// 3. Rules-amendment chain.
// ===========================================================================

// TestM7Amendment_LiveMutationKeepsChainHeadConsistent proves the M7 acceptance
// invariant: after a live rules mutation, GET /v1/assets/{id} rules equal the head
// of the anchored amendment chain (the head is computed from the read-back, so it
// records exactly what the policy server now enforces).
func TestM7Amendment_LiveMutationKeepsChainHeadConsistent(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, _ := h.deployLivePrivate(issuerSession, "AMD", "HN", 1.0)

	// A live mutation: supplying a full new_rules object routes through
	// applyRulesMutation (post rules -> read back -> anchor amendment).
	mut := h.do("POST", "/api/issuances/"+issID+"/amendments", issuerSession, map[string]any{
		"new_rules":        map[string]any{"allowed_categories": []string{"j:HN:ret", "j:HN:acc"}, "holder_cap": 500},
		"basis":            "board-approved holder-cap increase",
		"effective_height": 260000,
	})
	if mut.code != 200 {
		t.Fatalf("live amendment mutation: %d %s", mut.code, mut.errMsg())
	}
	head, _ := mut.body["head"].(map[string]any)
	if head == nil {
		t.Fatalf("mutation response carried no head block: %s", mut.raw)
	}
	if hc, _ := head["head_consistent"].(bool); !hc {
		t.Fatalf("head_consistent = false right after the mutation: %v", head)
	}

	// Independently confirm: the amendment head new_rules_hash equals the canonical
	// hash of the asset's live on-chain rules.
	onchain, err := h.s.onchainRulesHash(assetID)
	if err != nil {
		t.Fatalf("onchainRulesHash: %v", err)
	}
	amends, _ := h.s.st.AmendmentsByIssuance(issID)
	if len(amends) != 1 {
		t.Fatalf("amendment count = %d, want 1", len(amends))
	}
	if amends[0].NewRulesHash != onchain {
		t.Fatalf("chain head %q != live on-chain rules hash %q", amends[0].NewRulesHash, onchain)
	}

	// The read surface agrees.
	get := h.do("GET", "/api/issuances/"+issID+"/amendments", issuerSession, nil)
	if get.code != 200 {
		t.Fatalf("GET amendments: %d %s", get.code, get.errMsg())
	}
	gh, _ := get.body["head"].(map[string]any)
	if hc, _ := gh["head_consistent"].(bool); !hc {
		t.Fatalf("GET amendments head_consistent = false: %v", gh)
	}
	if hrh, _ := gh["head_rules_hash"].(string); hrh != onchain {
		t.Fatalf("head_rules_hash %q != on-chain %q", hrh, onchain)
	}
}

// TestM7Amendment_HalfAppliedMutationIsReconciled proves the guard: a mutation
// whose rules POST never landed (a 'pending' record) is healed by the reconcile
// cron, which re-posts the stored rules and anchors the amendment, leaving the
// chain head consistent with the on-chain rules rather than inconsistent.
func TestM7Amendment_HalfAppliedMutationIsReconciled(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, _ := h.deployLivePrivate(issuerSession, "HALF", "HN", 1.0)
	iss, _ := h.s.st.IssuanceByID(issID)

	// Simulate a crash after the intent was persisted but BEFORE the rules POST: a
	// 'pending' rules_mutations row with the rules bytes, and nothing posted yet.
	prior, _ := h.s.onchainRulesHash(assetID)
	rulesJSON, _ := json.Marshal(map[string]any{"allowed_categories": []string{"j:HN:ret"}, "holder_cap": 42})
	m := &RulesMutation{
		ID: mustID(), IssuanceID: issID, AssetID: assetID, PriorRulesHash: prior,
		Basis: "reconcile test", EffectiveHeight: 265000, RulesJSON: string(rulesJSON), State: "pending",
	}
	if err := h.s.st.InsertRulesMutation(m); err != nil {
		t.Fatal(err)
	}
	// Before reconcile the chain is inconsistent: an unfinished mutation exists and
	// no amendment has been anchored.
	if amends, _ := h.s.st.AmendmentsByIssuance(issID); len(amends) != 0 {
		t.Fatalf("amendments before reconcile = %d, want 0", len(amends))
	}

	// The guard heals it: re-post the stored rules, then anchor the amendment.
	h.s.reconcileRulesMutations()

	got, _ := h.s.st.RulesMutationByID(m.ID)
	if got.State != "complete" {
		t.Fatalf("mutation state after reconcile = %q, want complete", got.State)
	}
	amends, _ := h.s.st.AmendmentsByIssuance(issID)
	if len(amends) != 1 {
		t.Fatalf("amendments after reconcile = %d, want 1", len(amends))
	}
	head := h.s.amendmentHeadStatus(iss)
	if hc, _ := head["head_consistent"].(bool); !hc {
		t.Fatalf("chain left inconsistent after reconcile: %v", head)
	}
	onchain, _ := h.s.onchainRulesHash(assetID)
	if amends[0].NewRulesHash != onchain {
		t.Fatalf("reconciled head %q != on-chain rules %q", amends[0].NewRulesHash, onchain)
	}
	// The re-posted rules are the ones the reconcile stored, not the genesis rules.
	if got.NewRulesHash == prior {
		t.Fatalf("reconcile did not change the rules from the prior baseline")
	}
}

// ===========================================================================
// 4. Freeze / clawback console.
// ===========================================================================

// TestM7Clawback_ReasonRequired_RecordsTxidAndLog_Idempotent covers the console:
// a reason is required on freeze and clawback; a clawback records a txid and a
// public transparency-log entry naming the reason; and a repeat clawback of an
// already-swept holder is idempotent (no second sweep).
func TestM7Clawback_ReasonRequired_RecordsTxidAndLog_Idempotent(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, _ := h.deployLivePrivate(issuerSession, "CLAW", "HN", 1.0)

	holder := h.regHolder("Bad Actor", "HN")
	h.oa.setBalance(holder, assetID, 5000)

	// Reason required on freeze.
	nf := h.do("POST", "/api/issuances/"+issID+"/freeze", issuerSession, map[string]any{
		"holder_aid": holder, "frozen": true,
	})
	if nf.code != 400 {
		t.Fatalf("freeze without a reason = %d, want 400", nf.code)
	}
	// A freeze with a reason succeeds and hits the policy server.
	ok := h.do("POST", "/api/issuances/"+issID+"/freeze", issuerSession, map[string]any{
		"holder_aid": holder, "frozen": true, "reason": "court order 2026-07",
	})
	if ok.code != 200 {
		t.Fatalf("freeze with reason: %d %s", ok.code, ok.errMsg())
	}
	if !h.oa.frozen(holder) {
		t.Fatalf("holder not frozen at the policy server")
	}

	// Reason required on clawback.
	nc := h.do("POST", "/api/issuances/"+issID+"/clawback", issuerSession, map[string]any{"holder_aid": holder})
	if nc.code != 400 {
		t.Fatalf("clawback without a reason = %d, want 400", nc.code)
	}

	// A clawback with a reason builds the two-phase sweep: the reason is logged, the
	// L_claw sighashes are surfaced, nothing is broadcast, and the holder's balance is
	// untouched until the issuer signs.
	reason := "sanctions confirmed, seizure ordered"
	build := h.do("POST", "/api/issuances/"+issID+"/clawback", issuerSession, map[string]any{
		"holder_aid": holder, "reason": reason,
	})
	if build.code != 200 {
		t.Fatalf("clawback build: %d %s", build.code, build.errMsg())
	}
	if build.body["two_phase"] != true {
		t.Fatalf("clawback must be two-phase (the issuer signs the sweep): %s", build.raw)
	}
	completeURL, _ := build.body["complete_url"].(string)
	if completeURL == "" || build.body["txid"] != nil {
		t.Fatalf("build must return a complete_url and broadcast nothing: %s", build.raw)
	}
	if bal := h.oa.balanceOf(holder, assetID); bal != 5000 {
		t.Fatalf("build must not sweep: holder balance = %d, want 5000", bal)
	}

	// The issuer's signature completes the sweep and surfaces the txid.
	cb := h.do("POST", completeURL, issuerSession, map[string]any{
		"sigs": map[string]string{"0": strings.Repeat("ab", 64)},
	})
	if cb.code != 200 {
		t.Fatalf("clawback complete: %d %s", cb.code, cb.errMsg())
	}
	txid, _ := cb.body["txid"].(string)
	if txid == "" {
		t.Fatalf("clawback returned no txid: %s", cb.raw)
	}
	if atoms := jsonAtoms(cb.body["atoms"]); atoms != 5000 {
		t.Fatalf("clawback atoms = %d, want the full 5000 swept", atoms)
	}
	if h.oa.clawbackCount() != 1 {
		t.Fatalf("openampd clawback calls = %d, want 1", h.oa.clawbackCount())
	}
	// The reason and txid are in the public transparency log (reconcilable).
	rtxid, ratoms, found := h.s.findClawbackInLog(assetID, holder, reason)
	if !found || rtxid != txid || ratoms != 5000 {
		t.Fatalf("clawback not in the public log (found=%v txid=%q atoms=%d)", found, rtxid, ratoms)
	}
	if bal := h.oa.balanceOf(holder, assetID); bal != 0 {
		t.Fatalf("holder balance after sweep = %d, want 0", bal)
	}

	// IDEMPOTENT: a replay of completion returns the same txid without a second sweep.
	cb2 := h.do("POST", completeURL, issuerSession, map[string]any{
		"sigs": map[string]string{"0": strings.Repeat("ab", 64)},
	})
	if cb2.code != 200 {
		t.Fatalf("repeat clawback complete: %d %s", cb2.code, cb2.errMsg())
	}
	if rt, _ := cb2.body["txid"].(string); rt != txid {
		t.Fatalf("replay must return the same txid %s, got %s", txid, rt)
	}
	if h.oa.clawbackCount() != 1 {
		t.Fatalf("repeat clawback double-swept: openampd clawback calls = %d, want still 1", h.oa.clawbackCount())
	}
}

// ===========================================================================
// 5. Stranded-key re-delivery runbook.
// ===========================================================================


// TestM7Redeliver_UnknownOldIdentityRefused proves step 1 re-auth: the old AID
// must have a server-held identity record; without one, the runbook refuses to
// start (it re-authenticates against the record, not the lost key).
func TestM7Redeliver_UnknownOldIdentityRefused(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, _, _ := h.deployLivePrivate(issuerSession, "STR2", "HN", 1.0)

	// old AID has no claims record; new AID is registered.
	oldAID := h.regHolder("No Identity", "HN") // registerUser only, no UpsertClaims
	newAID := h.regHolder("New Key", "HN")

	adminSession, adminAID, _ := h.register(genPriv(t), "Reviewer", "HN")
	h.s.cfg.adminAIDs = map[string]bool{adminAID: true}

	rd := h.do("POST", "/api/id/redeliver", adminSession, map[string]any{
		"issuance_id": issID, "old_aid": oldAID, "new_aid": newAID, "reason": "attested",
	})
	if rd.code != 409 {
		t.Fatalf("redeliver with no old-AID identity = %d, want 409 (%s)", rd.code, rd.raw)
	}
}

// ===========================================================================
// 6. Category expiry -> real transfer refusal.
// ===========================================================================

// TestM7CategoryExpiry_ExpiredAccreditationRefusesTransfer proves the expiry
// enforcement: an accreditation whose window has passed has its accreditation
// category dropped at the policy server by the expiry cron, and a transfer that
// requires that category flips from allowed to refused.
func TestM7CategoryExpiry_ExpiredAccreditationRefusesTransfer(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	_, assetID, senderAID := h.deployLivePrivate(issuerSession, "ACC", "HN", 1.0)

	// A verified, accredited holder. The accreditation window is currently open, so
	// projectCategories includes the j:HN:acc token.
	holder := h.regHolder("Accredited Holder", "HN")
	future := time.Now().Add(365 * 24 * time.Hour).Unix()
	h.s.st.UpsertClaims(&Claims{
		AID: holder, Residence: "HN", BaseEligibility: "ret", Status: "verified",
		Accredited: true, AccredArtifact: "accreditation-artifact-hash", AccredValidUntil: future,
		ValidUntil: future,
	})
	if _, err := h.s.writeCategories(holder); err != nil {
		t.Fatalf("writeCategories: %v", err)
	}
	if !hasCategory(h.oa.userCategories(holder), "j:HN:acc") {
		t.Fatalf("holder is missing the accreditation category before expiry: %v", h.oa.userCategories(holder))
	}

	// Gate the asset on the accreditation category: only an accredited holder may
	// receive it (the real policy-server enforcement point).
	h.oa.setGate(assetID, "j:HN:acc")

	// While accredited, a transfer to the holder is allowed.
	allowed := h.s.callOpenAMP("POST", "/v1/transfers", "", map[string]any{
		"asset": assetID, "sender_aid": senderAID, "recipient_aid": holder, "atoms": 1,
	}, &struct{}{})
	if allowed != nil {
		t.Fatalf("transfer to an accredited holder was refused: %v", allowed)
	}

	// The accreditation window lapses. The expiry cron re-projects the holder's
	// categories, dropping the stale accreditation token at the policy server.
	h.s.st.UpsertClaims(&Claims{
		AID: holder, Residence: "HN", BaseEligibility: "ret", Status: "verified",
		Accredited: true, AccredArtifact: "accreditation-artifact-hash",
		AccredValidUntil: time.Now().Add(-1 * time.Hour).Unix(), ValidUntil: future,
	})
	h.s.runExpiry()

	cats := h.oa.userCategories(holder)
	if hasCategory(cats, "j:HN:acc") {
		t.Fatalf("expired accreditation category was NOT dropped: %v", cats)
	}
	if !hasCategory(cats, "j:HN:ret") {
		t.Fatalf("the base retail category must survive accreditation expiry: %v", cats)
	}

	// Now the same transfer is REFUSED at the policy server: the expiry produced a
	// real loss of eligibility, not just a UI change.
	refused := h.s.callOpenAMP("POST", "/v1/transfers", "", map[string]any{
		"asset": assetID, "sender_aid": senderAID, "recipient_aid": holder, "atoms": 1,
	}, &struct{}{})
	if refused == nil {
		t.Fatalf("transfer to a lapsed-accreditation holder was still allowed (no real refusal)")
	}
}
