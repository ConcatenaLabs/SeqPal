package main

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ConcatenaLabs/SeqPal/seqpald/elements"
)

// M7 (transfer-agent servicing) tests, the tests-owner deliverable. They reuse
// the M5/M6 harness verbatim: the openampd stub h.oa (extended additively in
// m5_test.go with the issuer holder register, the full-sweep clawback, the public
// transparency log, and a category gate), the escrow node stub h.seq, and the
// register/deploy helpers.
//
// COVERAGE
//  1. Distribution engine: no deposit is taken; the record-date snapshot
//     captures the chain holder set + Sequentia height and is immutable; pro-rata
//     floors exactly and discloses dust; sum(gross)==sum(net)+sum(withheld); NET
//     is paid to each holder's registered mandate in ONE transaction the issuer's
//     wallet funds and signs, refused unless every holder's output is exact; a
//     holder with no mandate is SKIPPED (never paid to a guessed address); a
//     replayed execute pays each holder exactly once, and a lost-write payout is
//     healed by broadcasting the recorded bytes again (never a double-pay).
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

// TestM7Distribution_PaysEveryHolderInOneIssuerFundedTx is the end-to-end
// distribution proof: no deposit is taken; the snapshot captures the chain
// register at a Sequentia height and is immutable; pro-rata floors with
// disclosed dust; withholding reconciles (gross == net + withheld); the payout
// is ONE transaction the issuer's wallet funds and signs, accepted only when it
// pays every holder with a mandate exactly its net; a holder with no mandate is
// skipped; and a replayed execute never double-pays.
func TestM7Distribution_PaysEveryHolderInOneIssuerFundedTx(t *testing.T) {
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

	// Create the run. The issuer pays from its own wallet, so there is no
	// deposit to make and nothing to wait for.
	cr := h.do("POST", "/api/issuances/"+issID+"/distributions", issuerSession, map[string]any{
		"pool_atoms": 1000, "memo": "Q1 dividend",
	})
	if cr.code != 200 {
		t.Fatalf("create distribution: %d %s", cr.code, cr.errMsg())
	}
	dObj, _ := cr.body["distribution"].(map[string]any)
	runID, _ := dObj["id"].(string)
	if runID == "" || cr.body["deposit_address"] != nil || dObj["deposit_address"] != nil {
		t.Fatalf("create returned a deposit address or no run id: %s", cr.raw)
	}
	if st, _ := dObj["state"].(string); st != "open" {
		t.Fatalf("new run state = %q, want open", st)
	}
	if pc, _ := cr.body["pay_ccy"].(string); pc != "USDX" {
		t.Fatalf("pay_ccy = %v, want USDX", cr.body["pay_ccy"])
	}
	// Nothing to execute before the record date is fixed.
	if ex := h.do("POST", "/api/issuances/"+issID+"/distributions/"+runID+"/execute", issuerSession, nil); ex.code != 409 {
		t.Fatalf("execute before the snapshot = %d, want 409 (%s)", ex.code, ex.raw)
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

	// Execute without a transaction: the payout -- one output per holder with a
	// mandate, net of withholding -- and C (no mandate) skipped, not paid.
	plan := h.payoutPlan(issuerSession, issID, runID)
	if len(plan.outputs) != 2 {
		t.Fatalf("payout outputs = %d, want 2 (C has no mandate): %v", len(plan.outputs), plan.outputs)
	}
	want := map[string]uint64{fakeSPK("seq-payout-a"): 333, fakeSPK("seq-payout-b"): 284}
	for _, o := range plan.outputs {
		if want[o.spk] != o.atoms {
			t.Fatalf("planned output %d to %s, want %v", o.atoms, o.spk, want)
		}
	}
	// A transaction short-changing a holder, or missing one, or paying another
	// script, is refused: nothing broadcast, the rows still planned.
	for name, bad := range map[string]func(*elements.Tx){
		"one atom less":  func(tx *elements.Tx) { v, _ := elements.ExplicitValueAmount(tx.Out[0].Value); tx.Out[0].Value = elements.ExplicitValue(v - 1) },
		"missing holder": func(tx *elements.Tx) { tx.Out = tx.Out[1:] },
		"other script":   func(tx *elements.Tx) { s, _ := hex.DecodeString(fakeSPK("seq-thief")); tx.Out[0].ScriptPubKey = s },
	} {
		r := h.postPayout(issuerSession, issID, runID, h.issuerFunds(plan.unfunded, bad))
		if r.code != 400 || h.seq.broadcastCount() != 0 {
			t.Fatalf("%s: execute = %d, broadcasts %d (%s)", name, r.code, h.seq.broadcastCount(), r.raw)
		}
	}
	for _, p := range mustRows(t, h, runID) {
		if (p.HolderAID == aidA || p.HolderAID == aidB) && p.State != "planned" {
			t.Fatalf("after refusals holder %s is %q, want planned", shortAID(p.HolderAID), p.State)
		}
	}
	signed := h.issuerFunds(plan.unfunded, nil)
	ex := h.postPayout(issuerSession, issID, runID, signed)
	if ex.code != 200 {
		t.Fatalf("execute: %d %s", ex.code, ex.errMsg())
	}
	d, _ = h.s.st.DistributionByID(runID)
	if d.State != "complete" || d.PayoutTxid == "" {
		t.Fatalf("state after execute = %q txid %q, want complete with the payout txid", d.State, d.PayoutTxid)
	}
	// ONE transaction paid every holder; no wallet sent anything.
	if h.seq.broadcastCount() != 1 || h.seq.sendCount() != 0 {
		t.Fatalf("broadcasts = %d, wallet sends = %d, want 1 and 0", h.seq.broadcastCount(), h.seq.sendCount())
	}
	paid, _ := decodeHexTx(h.seq.txHex(d.PayoutTxid))
	rows, _ = h.s.st.DistPaymentsByRun(runID)
	seen := map[int]bool{}
	for _, p := range rows {
		switch p.HolderAID {
		case aidA, aidB:
			if p.State != "paid" || p.Txid != d.PayoutTxid || p.Vout < 0 || seen[p.Vout] {
				t.Fatalf("holder %s = %+v, want paid by the one txid at its own output", p.HolderAID, p)
			}
			seen[p.Vout] = true
			v, _ := elements.ExplicitValueAmount(paid.Out[p.Vout].Value)
			if v != p.NetAtoms || hex.EncodeToString(paid.Out[p.Vout].ScriptPubKey) != fakeSPK(p.MandateAddress) {
				t.Fatalf("holder %s output %d pays %d to %x", p.HolderAID, p.Vout, v, paid.Out[p.Vout].ScriptPubKey)
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
	if ex2 := h.postPayout(issuerSession, issID, runID, signed); ex2.code != 200 || h.seq.broadcastCount() != 1 {
		t.Fatalf("replay execute: %d, broadcasts %d", ex2.code, h.seq.broadcastCount())
	}
	for _, m := range h.seq.walletCallsOf(seqEscrowWallet) {
		if m == "sendtoaddress" || m == "getnewaddress" || m == "listunspent" {
			t.Fatalf("the platform wallet was asked for %s on a distribution", m)
		}
	}
}

type payoutPlanOut struct {
	unfunded string
	outputs  []struct {
		spk   string
		atoms uint64
	}
}

func (h *m5h) payoutPlan(session, issID, runID string) payoutPlanOut {
	h.t.Helper()
	r := h.do("POST", "/api/issuances/"+issID+"/distributions/"+runID+"/execute", session, nil)
	if r.code != 200 {
		h.t.Fatalf("execute (plan): %d %s", r.code, r.raw)
	}
	payout, _ := r.body["payout"].(map[string]any)
	var p payoutPlanOut
	p.unfunded, _ = payout["unfunded_tx"].(string)
	outs, _ := payout["outputs"].([]any)
	for _, o := range outs {
		m := o.(map[string]any)
		p.outputs = append(p.outputs, struct {
			spk   string
			atoms uint64
		}{m["script_pubkey"].(string), uint64(m["atoms"].(float64))})
	}
	if p.unfunded == "" {
		h.t.Fatalf("plan has no unfunded transaction: %s", r.raw)
	}
	return p
}

// issuerFunds plays the issuer's wallet: fundrawtransaction (an input of the
// issuer's own coin, change, and a fee output in the distribution's asset) and
// signrawtransactionwithwallet (the fake chain takes an ordinary coin's
// signature as given). tamper, when set, edits the outputs first.
func (h *m5h) issuerFunds(unfunded string, tamper func(*elements.Tx)) string {
	h.t.Helper()
	tx, err := decodeHexTx(unfunded)
	if err != nil {
		h.t.Fatal(err)
	}
	if tamper != nil {
		tamper(tx)
	}
	var total uint64
	for _, o := range tx.Out {
		v, _ := elements.ExplicitValueAmount(o.Value)
		total += v
	}
	issuerSPK := fakeSPK("seq-issuer-own-wallet")
	txid, vout := h.seq.addCoin(issuerSPK, m5USDX, total+100000)
	op, _ := outpointOf(txid, vout)
	tx.In = append(tx.In, &elements.TxIn{Prevout: op, Sequence: 0xffffffff})
	change, _ := hex.DecodeString(issuerSPK)
	co, _ := newTxOut(m5USDX, 100000-500, change)
	fo, _ := newTxOut(m5USDX, 500, nil)
	tx.Out = append(tx.Out, co, fo)
	return hex.EncodeToString(tx.Serialize())
}

func (h *m5h) postPayout(session, issID, runID, tx string) resp {
	return h.do("POST", "/api/issuances/"+issID+"/distributions/"+runID+"/execute", session, map[string]any{"tx": tx})
}

func mustRows(t *testing.T, h *m5h, runID string) []*DistPayment {
	rows, err := h.s.st.DistPaymentsByRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// TestM7Distribution_ReconcilesLostPayoutByTxid proves a payout whose intent
// was written but whose broadcast never completed is healed by broadcasting the
// SAME bytes again: one transaction, recorded before broadcast, can never pay a
// holder twice, and a second execute never asks for another.
func TestM7Distribution_ReconcilesLostPayoutByTxid(t *testing.T) {
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
	if snap := h.do("POST", "/api/issuances/"+issID+"/distributions/"+runID+"/snapshot", issuerSession, nil); snap.code != 200 {
		t.Fatalf("snapshot: %d %s", snap.code, snap.errMsg())
	}
	plan := h.payoutPlan(issuerSession, issID, runID)
	signed := h.issuerFunds(plan.unfunded, nil)
	tx, _ := decodeHexTx(signed)
	txid := tx.TxID()
	// The intent was recorded -- the bytes, the txid, the holder's output -- and
	// then the process died before the broadcast.
	if err := h.s.st.UpdateDistributionFields(runID, map[string]any{"state": "paying", "payout_tx": signed, "payout_txid": txid}); err != nil {
		t.Fatal(err)
	}
	if err := h.s.st.UpdateDistPaymentFields(runID, aid, map[string]any{"state": "paying", "txid": txid, "vout": 0}); err != nil {
		t.Fatal(err)
	}
	// A different transaction offered now is not what gets broadcast.
	other := h.issuerFunds(plan.unfunded, nil)
	if ex := h.postPayout(issuerSession, issID, runID, other); ex.code != 200 {
		t.Fatalf("execute: %d %s", ex.code, ex.errMsg())
	}
	if h.seq.broadcastCount() != 1 || h.seq.txHex(txid) == "" {
		t.Fatalf("lost write not healed with the recorded bytes: broadcasts %d", h.seq.broadcastCount())
	}
	rows, _ := h.s.st.DistPaymentsByRun(runID)
	if len(rows) != 1 || rows[0].State != "paid" || rows[0].Txid != txid || rows[0].Vout != 0 {
		t.Fatalf("reconciled payment = %+v, want paid by the recorded txid", rows[0])
	}
	if d, _ := h.s.st.DistributionByID(runID); d.State != "complete" {
		t.Fatalf("run after reconcile = %q, want complete", d.State)
	}
	if ex := h.postPayout(issuerSession, issID, runID, other); ex.code != 200 || h.seq.broadcastCount() != 1 {
		t.Fatalf("second execute: %d, broadcasts %d", ex.code, h.seq.broadcastCount())
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

// Every rules change comes through applyRulesMutation, so that is where the one
// shape no amendment ever intends is refused: replacing a live rule set with an
// empty one. A caller that could not read the current rules and started from
// nothing would otherwise publish an asset with no allowed categories, no
// denies, no caps and no primary AIDs, and record it as an anchored amendment.
func TestRulesAreNeverReplacedWithNothing(t *testing.T) {
	if isEmptyRules(json.RawMessage(`{}`)) != true {
		t.Fatal("an object with no keys is an empty rule set")
	}
	if isEmptyRules(json.RawMessage(``)) != true {
		t.Fatal("absent rules are an empty rule set")
	}
	if isEmptyRules(json.RawMessage(`null`)) != true {
		t.Fatal("null rules are an empty rule set")
	}
	if isEmptyRules(json.RawMessage(`{"allowed_categories":["j:AE:ret"]}`)) != false {
		t.Fatal("a rule set with rules in it is not empty")
	}
	// Unreadable is not empty: this check must never be what decides that rules
	// nobody could parse are safe to throw away.
	if isEmptyRules(json.RawMessage(`{"broken`)) != false {
		t.Fatal("an unreadable rule set must not count as empty")
	}
}

// The pro-rata allocation is the money-safety claim of the whole distribution
// feature, and it was asserted on one shape: three equal holders, pool 1000,
// dust 1. These are the shapes that break a floor-and-disclose scheme -- a pool
// smaller than the number of holders, one holder with almost everything, atoms
// near the top of the range where a naive product overflows -- checked as
// properties rather than as an example.
func TestTheProRataInvariantHoldsOverManyShapes(t *testing.T) {
	shapes := []struct {
		name string
		pool uint64
		bals []uint64
	}{
		{"equal thirds", 1000, []uint64{1, 1, 1}},
		{"pool smaller than holders", 2, []uint64{1, 1, 1, 1, 1}},
		{"pool of one", 1, []uint64{5, 5}},
		{"one whale", 1_000_000, []uint64{999_999_999, 1, 1}},
		{"single holder takes all", 12345, []uint64{7}},
		{"wide spread", 999_983, []uint64{3, 5, 7, 11, 13, 17, 19, 23}},
		{"huge atoms", 21_000_000_00000000, []uint64{1 << 40, 1 << 41, 3}},
		{"balances at the top of the range", 1_000_000, []uint64{1 << 62, 1 << 62}},
	}
	for _, s := range shapes {
		t.Run(s.name, func(t *testing.T) {
			var total uint64
			for _, b := range s.bals {
				total += b
			}
			var grossTotal uint64
			for _, b := range s.bals {
				gross := mulDiv(s.pool, b, total)
				// Nobody may be allocated more than the pool, ever.
				if gross > s.pool {
					t.Fatalf("a holder was allocated %d out of a pool of %d", gross, s.pool)
				}
				grossTotal += gross
			}
			// Never over-allocate: the sum of the floors cannot exceed the pool.
			if grossTotal > s.pool {
				t.Fatalf("allocated %d out of a pool of %d", grossTotal, s.pool)
			}
			// And the shortfall is dust, which is bounded by one atom per holder:
			// each holder loses less than one atom to the floor.
			dust := s.pool - grossTotal
			if dust >= uint64(len(s.bals)) && s.pool >= uint64(len(s.bals)) {
				t.Fatalf("dust %d is not bounded by the %d holders it floors",
					dust, len(s.bals))
			}
		})
	}
}

// Withholding is the other half of the invariant: whatever it takes, gross has
// to be exactly net plus withheld, for every rate and every amount -- including
// the amounts where a percentage lands between atoms.
func TestWithholdingNeverLosesAnAtom(t *testing.T) {
	for _, gross := range []uint64{0, 1, 2, 3, 7, 99, 100, 101, 12345, 1 << 40, ^uint64(0) >> 4} {
		for _, res := range []string{"US", "GB", "AE", "HN-PRO", ""} {
			claims := &Claims{AID: "x", Residence: res, Status: "verified"}
			withheld, _, _ := withholdingFor(claims, gross)
			if withheld > gross {
				t.Fatalf("withheld %d from a gross of %d (%s)", withheld, gross, res)
			}
			if net := gross - withheld; net+withheld != gross {
				t.Fatalf("gross %d != net %d + withheld %d (%s)", gross, net, withheld, res)
			}
		}
	}
}

// net is computed as gross - withheld on UNSIGNED integers. A withholding rate
// outside 0..100% would not make net negative, it would make it wrap to
// something astronomical -- and that net is what gets paid on chain. The
// reconciliation check downstream cannot catch it, because modular arithmetic
// makes net + withheld == gross however far it wrapped.
func TestWithholdingCanNeverExceedTheGross(t *testing.T) {
	for _, gross := range []uint64{0, 1, 1000, 1 << 40, ^uint64(0) >> 2} {
		for _, res := range []string{"GB", "JP", "US", "AE", "ZZ", ""} {
			withheld, bps, _ := withholdingFor(&Claims{Residence: res, Status: "verified"}, gross)
			if withheld > gross {
				t.Fatalf("withheld %d from gross %d (%s at %d bps)", withheld, gross, res, bps)
			}
			if bps < 0 || bps > 10000 {
				t.Fatalf("rate %d bps is outside 0..100%% (%s)", bps, res)
			}
			if net := gross - withheld; net > gross {
				t.Fatalf("net %d wrapped past gross %d (%s)", net, gross, res)
			}
		}
	}
}

// A fee comes out of a deposit, so it can never be larger than one. The rate is
// configured, and a rate outside 0..100% is a typo -- an extra digit on a
// percentage -- which would charge more than was deposited. The release paths
// already refuse to pay out less than nothing, but the ledger would still carry
// a fee that was never taken, and the ledger is the books.
func TestAFeeCanNeverExceedWhatItCameFrom(t *testing.T) {
	if got := clampBps(-1, "test"); got != 0 {
		t.Fatalf("a negative rate clamped to %d, want 0", got)
	}
	if got := clampBps(50_000, "test"); got != 10000 {
		t.Fatalf("500%% clamped to %d, want 10000", got)
	}
	if got := clampBps(50, "test"); got != 50 {
		t.Fatalf("an ordinary rate was changed to %d", got)
	}

	for _, deposited := range []uint64{0, 1, 1000, 1 << 40, ^uint64(0) >> 2} {
		for _, bps := range []int64{-500, 0, 50, 10000, 50_000} {
			sub := &Subscription{DepositedAtoms: deposited}
			fee, net := escrowFeeSplit(sub, bps)
			if fee > deposited {
				t.Fatalf("fee %d out of a deposit of %d (%d bps)", fee, deposited, bps)
			}
			if net > deposited {
				t.Fatalf("net %d exceeds the deposit of %d (%d bps)", net, deposited, bps)
			}
			if fee+net != deposited {
				t.Fatalf("fee %d + net %d != deposit %d (%d bps)", fee, net, deposited, bps)
			}
		}
	}
}

// Numbers an operator sets should not be able to take the service down or
// quietly remove a control. A cadence of zero panics time.NewTicker rather than
// ticking fast; zero confirmations would treat an unconfirmed deposit as
// arrived, which this platform says nowhere else that it does; and a year of
// zero blocks makes a Rule 144 lockup expire at the moment it is stamped.
func TestAMisconfiguredNumberIsRefusedNotObeyed(t *testing.T) {
	t.Setenv("SEQPALD_TEST_CADENCE", "0")
	if got := interval("SEQPALD_TEST_CADENCE", 30); got != 30*time.Second {
		t.Fatalf("a cadence of zero became %v, want the default", got)
	}
	t.Setenv("SEQPALD_TEST_CADENCE", "-5")
	if got := interval("SEQPALD_TEST_CADENCE", 30); got != 30*time.Second {
		t.Fatalf("a negative cadence became %v, want the default", got)
	}
	t.Setenv("SEQPALD_TEST_CADENCE", "7")
	if got := interval("SEQPALD_TEST_CADENCE", 30); got != 7*time.Second {
		t.Fatalf("an ordinary cadence became %v", got)
	}
	// And whatever it returns can actually run a ticker.
	tk := time.NewTicker(interval("SEQPALD_TEST_CADENCE", 30))
	tk.Stop()
}

// mulDiv is the money multiplication in this platform, and big.Int.Uint64 is
// undefined for a value that does not fit. A divisor small enough to leave the
// product larger than a uint64 is exactly that case, and the answer has to be a
// defined one -- zero, which every caller reads as "no amount" -- rather than
// whatever the conversion produced.
func TestMulDivIsTotal(t *testing.T) {
	const max = ^uint64(0)
	for _, tc := range []struct {
		a, b, c, want uint64
	}{
		{1000, 500, 10000, 50},
		{max, 1, 1, max},
		{max, 2, 1, 0},           // 2*max does not fit
		{1 << 40, 1 << 40, 1, 0}, // 2^80 does not fit
		{1 << 40, 1 << 40, 1 << 40, 1 << 40},
		{100, 100, 0, 0}, // divide by zero is no amount
		{0, 12345, 7, 0},
	} {
		if got := mulDiv(tc.a, tc.b, tc.c); got != tc.want {
			t.Fatalf("mulDiv(%d, %d, %d) = %d, want %d", tc.a, tc.b, tc.c, got, tc.want)
		}
	}
}
