package main

import (
	"testing"
)

// M6 (atomic delivery-versus-payment) tests. The tests-owner deliverable.
//
// COVERAGE (closing v2, seqpald side):
//   - Atomic DvP: a USDX subscription settles as ONE multi-asset transaction
//     (token delivery escrow-enclave -> investor AND escrow USDX -> the issuer's
//     mandate, net of the escrow fee, in the same tx). Asserted end to end against
//     an openampd stub that speaks the OA-4 payment protocol (a build carrying a
//     "payment" leg returns the assembled tx hex + the payment input indices; the
//     escrow node wallet partially signs its own USDX inputs; complete co-signs
//     and returns one txid used for BOTH the delivery and release records).
//   - The v1/v2 SELECTOR, both branches: atomic disabled (cfg.atomicClose=false)
//     and the runtime OA-4-absent fallback (the policy server rejects the payment
//     field), each of which must settle via the M5 two-transaction close without
//     ever double-delivering across the fallback boundary.
//   - Fund-safety invariants (path-independent): settle exactly once on a replayed
//     close (never double-deliver/double-release), and reconcile an ambiguous prior
//     delivery by a balance scan before any retry.
//   - BTC reorged-deposit handling (M6-CONTRACT section 5): a credited BTC deposit
//     reorged out AFTER delivery triggers a GLOBAL freeze on the investor AID, a
//     re-confirmation unfreezes, and both are idempotent (exercises reorg.go
//     watchBtcReorgs). The BTC rail stays registrar-style (never atomic).
//
// The OA-4/OA-7 openampd work itself lives in ~/openamp (a separate repo/working
// tree) and is covered by that repo's own Go tests; seqpald talks to openampd over
// HTTP, stubbed here. See the M6 report for the reviewed-but-not-owned findings.

// ---------------------------------------------------------------------------
// fakeNode reorg helper (M6): a credited deposit can be unwound by a reorg, which
// the wallet reports as a drop in confirmations (0 = back in the mempool, negative
// = conflicted). setConfs drives that transition; the node's gettransaction stub
// (in m5_test.go) reads it back for the reorg watcher.
// ---------------------------------------------------------------------------

func (n *fakeNode) setConfs(address string, confs int64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if d, ok := n.deposits[address]; ok {
		d.confs = confs
		n.deposits[address] = d
	}
}

// jsonAtoms reads an atom count from a decoded JSON value (numbers decode to
// float64 in a map[string]any). Atom counts here are well under 2^53, so the
// float round-trip is exact.
func jsonAtoms(v any) uint64 {
	switch x := v.(type) {
	case float64:
		return uint64(x)
	case int:
		return uint64(x)
	case uint64:
		return x
	default:
		return 0
	}
}

// ---------------------------------------------------------------------------
// shared setup: a live USDX offering with one in_escrow USDX subscription and a
// registered Sequentia payout mandate, ready to close.
// ---------------------------------------------------------------------------

type m6usdxFixture struct {
	issuerSession, issuerXOnly, issuerPriv string
	issID, assetID, escrowAID              string
	invSession, invAID                     string
	subID                                  string
	depositAtoms                           uint64
}

func setupM6USDX(t *testing.T, h *m5h, price float64, tokens int) m6usdxFixture {
	t.Helper()
	fx := m6usdxFixture{}
	fx.issuerPriv = genPriv(t)
	fx.issuerSession, _, fx.issuerXOnly = h.register(fx.issuerPriv, "Issuer", "HN")
	fx.issID, fx.assetID, fx.escrowAID = h.deployLivePrivate(fx.issuerSession, "M6UX", "HN", price)

	fx.invSession, fx.invAID, _ = h.verifiedInvestor(genPriv(t), "Investor", "HN", "ret")

	sub := h.do("POST", "/api/issuances/"+fx.issID+"/subscribe", fx.invSession, map[string]any{
		"rail": "usdx", "amount": tokens, "refund_address": "seq-refund-addr",
	})
	if sub.code != 200 {
		t.Fatalf("subscribe usdx: %d %s", sub.code, sub.errMsg())
	}
	depAddr, _ := sub.body["deposit_address"].(string)
	subObj, _ := sub.body["subscription"].(map[string]any)
	fx.subID, _ = subObj["id"].(string)
	if depAddr == "" || fx.subID == "" {
		t.Fatalf("subscribe returned no deposit address / id: %s", sub.raw)
	}

	// Deposit the full pay amount and confirm it to in_escrow.
	fx.depositAtoms = uint64(tokens) * uint64(price) * 1e8
	h.seq.credit(depAddr, fx.depositAtoms, 2, m5USDX)
	h.s.watchDeposits()
	got, _ := h.s.st.SubscriptionByID(fx.subID)
	if got == nil || got.State != "in_escrow" {
		t.Fatalf("subscription not in_escrow before close: %+v", got)
	}

	if err := h.s.st.UpsertMandate(&PayoutMandate{IssuanceID: fx.issID, Chain: "sequentia", Address: "seq-issuer-payout"}); err != nil {
		t.Fatalf("UpsertMandate: %v", err)
	}
	return fx
}

// ===========================================================================
// 1. Atomic close vs the v1 fallback selector.
//
// M6-CONTRACT sections 3-4: closing settles a USDX subscription as ONE
// multi-asset transaction (tokens to the investor enclave AND escrow USDX to the
// issuer in the same tx) via the OA-4 payment leg, with the M5 two-transaction
// path kept as the fallback if OA-4 is unavailable.
// ===========================================================================

// TestM6_UsdxClose_AtomicOrFallback_SettlesOneCombinedOrTwoTx enables the atomic
// selector and closes a USDX subscription, then asserts the tx SHAPE for whichever
// path ran:
//   - ATOMIC (payment leg present): exactly ONE hosted build carrying the payment
//     leg, and NO separate escrow release send (delivery + payment in one tx).
//   - FALLBACK (no payment leg): the M5 shape, one delivery build plus one
//     separate release send (two transactions).
//
// Today the atomic path is not wired, so the fallback branch runs and passes; when
// closing v2 lands and consumes cfg.atomicClose, the atomic branch runs and this
// same test validates the single-combined-tx requirement.
func TestM6_UsdxClose_AtomicOrFallback_SettlesOneCombinedOrTwoTx(t *testing.T) {
	h := newM5Harness(t, m5opts{escrowConfs: 2})
	h.s.cfg.atomicClose = true // request the atomic path (a no-op until v2 wires it)
	fx := setupM6USDX(t, h, 1.0, 100)

	h.closeAs(fx.issuerSession, "", fx.issuerXOnly, fx.issID, fx.issuerPriv)

	// Path-independent: the subscription settled exactly once with a delivery.
	got, _ := h.s.st.SubscriptionByID(fx.subID)
	if got.State != "settled" {
		t.Fatalf("subscription state after close = %q, want settled", got.State)
	}
	set, _ := h.s.st.SettlementByID(fx.subID)
	if set == nil || set.State != "settled" || set.DeliveryTxid == "" {
		t.Fatalf("settlement = %+v, want settled with a delivery txid", set)
	}
	if h.oa.transfers() != 1 {
		t.Fatalf("completed hosted transfers = %d, want 1", h.oa.transfers())
	}

	if h.oa.anyTransferCarriedPayment() {
		// ATOMIC single-tx DvP: the USDX payment rides in the delivery build, so
		// there is exactly one build and NO separate escrow release send.
		if got := h.oa.transferBuildCount(); got != 1 {
			t.Fatalf("atomic path: transfer builds = %d, want 1 combined build", got)
		}
		if got := h.seq.sendCount(); got != 0 {
			t.Fatalf("atomic path: escrow release sends = %d, want 0 (payment is in the delivery tx)", got)
		}
		// Delivery and release are the SAME transaction: one txid recorded for both.
		if set.DeliveryTxid == "" || set.DeliveryTxid != set.ReleaseTxid {
			t.Fatalf("atomic path: delivery_txid %q != release_txid %q (must be one tx)", set.DeliveryTxid, set.ReleaseTxid)
		}
		// The combined build carried the USDX payment leg to the issuer's mandate,
		// net of the escrow fee, in the correct asset.
		pay := h.oa.lastPaymentLeg()
		if pay == nil {
			t.Fatalf("atomic path: no payment leg captured")
		}
		fee := fx.depositAtoms * 50 / 10000
		wantNet := fx.depositAtoms - fee
		if asset, _ := pay["asset"].(string); asset != m5USDX {
			t.Fatalf("atomic payment asset = %v, want USDX %s", pay["asset"], m5USDX)
		}
		if to, _ := pay["to_address"].(string); to != "seq-issuer-payout" {
			t.Fatalf("atomic payment to_address = %v, want seq-issuer-payout", pay["to_address"])
		}
		if atoms := jsonAtoms(pay["atoms"]); atoms != wantNet {
			t.Fatalf("atomic payment atoms = %d, want net %d (deposit %d - fee %d)", atoms, wantNet, fx.depositAtoms, fee)
		}
		t.Logf("atomic DvP path exercised: one combined transaction, payment net %d to mandate", wantNet)
	} else {
		// FALLBACK (reachable today): delivery build + a SEPARATE release send.
		if got := h.oa.transferBuildCount(); got != 1 {
			t.Fatalf("fallback path: transfer builds = %d, want 1", got)
		}
		if got := h.seq.sendCount(); got != 1 {
			t.Fatalf("fallback path: escrow release sends = %d, want 1 (separate release)", got)
		}
		// The separate release paid the mandate net of the escrow fee (v1 shape).
		send, _ := h.seq.lastSend()
		fee := fx.depositAtoms * 50 / 10000
		if send.address != "seq-issuer-payout" || send.atoms != fx.depositAtoms-fee {
			t.Fatalf("fallback release send = %+v, want addr seq-issuer-payout net %d", send, fx.depositAtoms-fee)
		}
		t.Logf("v1 fallback path exercised: delivery + separate release (atomic close not yet wired)")
	}
}

// TestM6_FallbackSelector_ForcedV1_UsesTwoTx pins the explicit fallback branch:
// with the atomic selector OFF (OA-4 unavailable / capability off), closing MUST
// fully settle via the M5 two-transaction path and MUST NOT attach a payment leg.
func TestM6_FallbackSelector_ForcedV1_UsesTwoTx(t *testing.T) {
	h := newM5Harness(t, m5opts{escrowConfs: 2})
	h.s.cfg.atomicClose = false // force the v1 fallback
	fx := setupM6USDX(t, h, 1.0, 100)

	h.closeAs(fx.issuerSession, "", fx.issuerXOnly, fx.issID, fx.issuerPriv)

	set, _ := h.s.st.SettlementByID(fx.subID)
	if set == nil || set.State != "settled" || set.DeliveryTxid == "" || set.ReleaseTxid == "" {
		t.Fatalf("forced-fallback close did not settle with both txids: %+v", set)
	}
	if h.oa.anyTransferCarriedPayment() {
		t.Fatalf("forced fallback must not carry an OA-4 payment leg")
	}
	if got := h.oa.transferBuildCount(); got != 1 {
		t.Fatalf("transfer builds = %d, want 1 (fallback delivery)", got)
	}
	if got := h.seq.sendCount(); got != 1 {
		t.Fatalf("release sends = %d, want 1 (fallback release)", got)
	}
}

// TestM6_AtomicRequested_PolicyServerLacksPaymentLeg_FallsBackToV1 covers the
// RUNTIME capability fallback: atomic close is enabled, but the policy server
// rejects the payment leg (a pre-OA-4 openampd). settleOne must fall through to
// the v1 two-transaction close WITHOUT having broadcast a delivery-only tx, and
// still fully settle (delivery + a separate release, with distinct txids).
func TestM6_AtomicRequested_PolicyServerLacksPaymentLeg_FallsBackToV1(t *testing.T) {
	h := newM5Harness(t, m5opts{escrowConfs: 2})
	h.s.cfg.atomicClose = true     // atomic requested ...
	h.oa.rejectPaymentField = true // ... but the policy server has no payment leg
	fx := setupM6USDX(t, h, 1.0, 100)

	h.closeAs(fx.issuerSession, "", fx.issuerXOnly, fx.issID, fx.issuerPriv)

	set, _ := h.s.st.SettlementByID(fx.subID)
	if set == nil || set.State != "settled" {
		t.Fatalf("capability-fallback close did not settle: %+v", set)
	}
	// Two DISTINCT transactions (v1): a delivery and a separate release.
	if set.DeliveryTxid == "" || set.ReleaseTxid == "" || set.DeliveryTxid == set.ReleaseTxid {
		t.Fatalf("fallback must record two distinct txids, got delivery=%q release=%q", set.DeliveryTxid, set.ReleaseTxid)
	}
	if got := h.seq.sendCount(); got != 1 {
		t.Fatalf("escrow release sends = %d, want 1 (separate v1 release after fallback)", got)
	}
	// The rejected atomic probe plus the v1 delivery build = 2 builds; exactly one
	// delivery completed (never a double-deliver across the fallback boundary).
	if got := h.oa.transfers(); got != 1 {
		t.Fatalf("completed deliveries = %d, want exactly 1 (no double-deliver on fallback)", got)
	}
	// The fee output stays last: the release paid the mandate net of the escrow fee.
	send, _ := h.seq.lastSend()
	fee := fx.depositAtoms * 50 / 10000
	if send.address != "seq-issuer-payout" || send.atoms != fx.depositAtoms-fee {
		t.Fatalf("fallback release send = %+v, want addr seq-issuer-payout net %d", send, fx.depositAtoms-fee)
	}
}

// ===========================================================================
// 2. Idempotency + reconciliation (M6-CONTRACT section 4: "one atomic tx per
//    subscription ... reconciled by chain scan before any retry"). These are
//    path-independent fund-safety invariants.
// ===========================================================================

// TestM6_ReplayedClose_SettlesExactlyOnce: a second close of the same offering
// must not re-deliver or re-release. Exactly one delivery and one release survive
// the replay (never double-settle).
func TestM6_ReplayedClose_SettlesExactlyOnce(t *testing.T) {
	h := newM5Harness(t, m5opts{escrowConfs: 2})
	h.s.cfg.atomicClose = true
	fx := setupM6USDX(t, h, 1.0, 100)

	h.closeAs(fx.issuerSession, "", fx.issuerXOnly, fx.issID, fx.issuerPriv)
	builds1, delivered1, sends1 := h.oa.transferBuildCount(), h.oa.transfers(), h.seq.sendCount()
	if delivered1 != 1 {
		t.Fatalf("after first close: completed deliveries = %d, want 1", delivered1)
	}

	// Replay. The subscription is "settled" (not in_escrow) so it is not re-scanned,
	// and the settlement's "settled" state short-circuits both legs anyway.
	h.closeAs(fx.issuerSession, "", fx.issuerXOnly, fx.issID, fx.issuerPriv)

	if got := h.oa.transfers(); got != delivered1 {
		t.Fatalf("completed deliveries after replay = %d, want %d (no double-deliver)", got, delivered1)
	}
	if got := h.oa.transferBuildCount(); got != builds1 {
		t.Fatalf("transfer builds after replay = %d, want %d (no re-build)", got, builds1)
	}
	if got := h.seq.sendCount(); got != sends1 {
		t.Fatalf("release sends after replay = %d, want %d (no double-release)", got, sends1)
	}
	set, _ := h.s.st.SettlementByID(fx.subID)
	if set == nil || set.State != "settled" {
		t.Fatalf("settlement after replay = %+v, want a single settled record", set)
	}
}

// TestM6_ReconcilesDeliveryBeforeRetry: an ambiguous prior delivery (settlement in
// "delivering" with no recorded txid, but the investor enclave already holds the
// tokens) must be reconciled by a balance scan, NOT re-delivered. No new transfer
// build is issued for the delivery leg.
func TestM6_ReconcilesDeliveryBeforeRetry(t *testing.T) {
	h := newM5Harness(t, m5opts{escrowConfs: 2})
	fx := setupM6USDX(t, h, 1.0, 100)

	sub, _ := h.s.st.SubscriptionByID(fx.subID)
	if sub == nil {
		t.Fatalf("subscription missing")
	}
	// Simulate a crashed prior attempt: the settlement row exists in "delivering"
	// with no txid, and the delivery actually landed (investor holds the tokens).
	if _, err := h.s.st.CreateSettlementIfAbsent(fx.subID, fx.issID); err != nil {
		t.Fatalf("CreateSettlementIfAbsent: %v", err)
	}
	if err := h.s.st.UpdateSettlementFields(fx.subID, map[string]any{"state": "delivering"}); err != nil {
		t.Fatalf("UpdateSettlementFields: %v", err)
	}
	h.oa.setBalance(fx.invAID, fx.assetID, sub.TokenAtoms)

	h.closeAs(fx.issuerSession, "", fx.issuerXOnly, fx.issID, fx.issuerPriv)

	// Reconciliation must have detected the prior delivery: NO new build, no re-deliver.
	if got := h.oa.transferBuildCount(); got != 0 {
		t.Fatalf("transfer builds = %d, want 0 (delivery reconciled, not re-attempted)", got)
	}
	if got := h.oa.transfers(); got != 0 {
		t.Fatalf("completed deliveries = %d, want 0 (no re-delivery)", got)
	}
	set, _ := h.s.st.SettlementByID(fx.subID)
	if set == nil || set.State != "settled" || set.DeliveryTxid != "reconciled" {
		t.Fatalf("settlement = %+v, want settled with delivery_txid=reconciled", set)
	}
}

// TestM6_V1DeliveringRollover_DoesNotDoubleDeliver is the fund-safety regression
// for the atomic-enable ROLLOVER window. A settlement can be mid-closing-v1 in
// state "delivering" with the tokens already on-chain but the delivery_txid write
// lost (a crash between broadcast and record). Both DeliveryTxid and ReleaseTxid
// are empty in that window. If atomic close is then enabled (the default; e.g. a
// pre-OA-4 v1 fallback ran, then OA-4 went live) a naive gate keyed only on the
// empty txids would route this into settleOneAtomic, which reconciles only its own
// "settling" state, and would build+broadcast a SECOND delivery+payment tx =
// double-settle. The gate must keep a v1-progress settlement on v1: it must
// RECONCILE the prior delivery by balance scan, issue NO atomic build, and never
// attach an atomic payment leg.
func TestM6_V1DeliveringRollover_DoesNotDoubleDeliver(t *testing.T) {
	h := newM5Harness(t, m5opts{escrowConfs: 2})
	h.s.cfg.atomicClose = true // atomic enabled AFTER a v1 delivery already began
	fx := setupM6USDX(t, h, 1.0, 100)

	sub, _ := h.s.st.SubscriptionByID(fx.subID)
	if sub == nil {
		t.Fatalf("subscription missing")
	}
	// A crashed closing-v1 attempt: settlement in "delivering", no txid, but the
	// tokens actually landed in the investor enclave.
	if _, err := h.s.st.CreateSettlementIfAbsent(fx.subID, fx.issID); err != nil {
		t.Fatalf("CreateSettlementIfAbsent: %v", err)
	}
	if err := h.s.st.UpdateSettlementFields(fx.subID, map[string]any{"state": "delivering"}); err != nil {
		t.Fatalf("UpdateSettlementFields: %v", err)
	}
	h.oa.setBalance(fx.invAID, fx.assetID, sub.TokenAtoms)

	h.closeAs(fx.issuerSession, "", fx.issuerXOnly, fx.issID, fx.issuerPriv)

	// The prior delivery must be reconciled, NOT re-attempted: no atomic (or v1)
	// build, and no completed delivery. Against the pre-fix gate this fails because
	// settleOneAtomic builds and completes a fresh combined tx.
	if got := h.oa.transferBuildCount(); got != 0 {
		t.Fatalf("transfer builds = %d, want 0 (v1 delivery reconciled, atomic must not re-build)", got)
	}
	if got := h.oa.transfers(); got != 0 {
		t.Fatalf("completed deliveries = %d, want 0 (no double-delivery on the atomic rollover)", got)
	}
	if h.oa.anyTransferCarriedPayment() {
		t.Fatalf("a v1-progress settlement must not be routed into the atomic payment leg")
	}
	set, _ := h.s.st.SettlementByID(fx.subID)
	if set == nil || set.State != "settled" || set.DeliveryTxid != "reconciled" {
		t.Fatalf("settlement = %+v, want settled with delivery_txid=reconciled (v1 reconcile)", set)
	}
}

// TestM6_AtomicDisabledMidSettling_DoesNotDoublePay is the fund-safety regression
// for the REVERSE rollover. A settlement can be mid-atomic-close in state "settling"
// (the combined delivery+payment tx broadcast, txids not yet recorded) with the
// tokens already on-chain. "settling" is written ONLY by settleOneAtomic, so it
// always means an atomic tx may already have paid the issuer. If the operator now
// flips atomicClose OFF (a config downgrade during an in-flight settlement), a gate
// that consults the flag would skip the atomic reconcile and drop into the v1
// path, running a SEPARATE v1 USDX release = a second payment to the issuer
// (double-pay). The reconcile of a "settling" record must be MANDATORY and never
// depend on the flag: reconcile the atomic tx by balance scan, issue no new build,
// and run no separate release.
func TestM6_AtomicDisabledMidSettling_DoesNotDoublePay(t *testing.T) {
	h := newM5Harness(t, m5opts{escrowConfs: 2})
	h.s.cfg.atomicClose = true // atomic close was on when the settlement began ...
	fx := setupM6USDX(t, h, 1.0, 100)

	sub, _ := h.s.st.SubscriptionByID(fx.subID)
	if sub == nil {
		t.Fatalf("subscription missing")
	}
	// An in-flight atomic attempt: settlement in "settling", no recorded txid, but the
	// combined tx already landed (the investor enclave holds the tokens and the issuer
	// was already paid in the SAME tx).
	if _, err := h.s.st.CreateSettlementIfAbsent(fx.subID, fx.issID); err != nil {
		t.Fatalf("CreateSettlementIfAbsent: %v", err)
	}
	if err := h.s.st.UpdateSettlementFields(fx.subID, map[string]any{"state": "settling"}); err != nil {
		t.Fatalf("UpdateSettlementFields: %v", err)
	}
	h.oa.setBalance(fx.invAID, fx.assetID, sub.TokenAtoms)

	// The operator downgrades atomic close OFF while the settlement is still "settling".
	h.s.cfg.atomicClose = false

	h.closeAs(fx.issuerSession, "", fx.issuerXOnly, fx.issID, fx.issuerPriv)

	// The atomic tx must be RECONCILED, never re-run through v1. Against the pre-fix
	// gate (which consulted the flag for "settling" too) this fails: the disabled
	// flag routes it into the v1 release, producing a separate escrow send.
	if got := h.seq.sendCount(); got != 0 {
		t.Fatalf("escrow release sends = %d, want 0 (a settling atomic close must not run a v1 release = double-pay)", got)
	}
	if got := h.oa.transferBuildCount(); got != 0 {
		t.Fatalf("transfer builds = %d, want 0 (atomic tx reconciled, not rebuilt)", got)
	}
	if got := h.oa.transfers(); got != 0 {
		t.Fatalf("completed deliveries = %d, want 0 (no re-delivery)", got)
	}
	set, _ := h.s.st.SettlementByID(fx.subID)
	if set == nil || set.State != "settled" || set.DeliveryTxid != "reconciled" || set.ReleaseTxid != "reconciled" {
		t.Fatalf("settlement = %+v, want settled with delivery_txid=release_txid=reconciled (atomic reconcile)", set)
	}
}

// ===========================================================================
// 3. BTC reorged-deposit handling (M6-CONTRACT section 5).
//
// The native-BTC rail is registrar-style (payment confirms on testnet4, THEN
// delivery is co-signed on Sequentia: not atomic), which leaves a window a USDX
// atomic close does not have. A credited BTC deposit reorged out AFTER delivery
// must trigger a GLOBAL account freeze on the investor's AID (POST
// /v1/issuer/freeze) pending investigation, and a re-confirmation must unfreeze.
// The freeze blast radius is cross-asset (the whole AID), which the watcher
// discloses. This exercises the reorg watcher (reorg.go watchBtcReorgs) directly.
// ===========================================================================

func TestM6_BTCReorgOut_FreezesAndReconfirmUnfreezes(t *testing.T) {
	h := newM5Harness(t, m5opts{withBTC: true, escrowConfs: 1})
	issuerPriv := genPriv(t)
	issuerSession, _, issuerXOnly := h.register(issuerPriv, "Issuer", "HN")
	issID, _, _ := h.deployLivePrivate(issuerSession, "M6BTC", "HN", 1000.0)

	invSession, invAID, _ := h.verifiedInvestor(genPriv(t), "Investor", "HN", "ret")
	sub := h.do("POST", "/api/issuances/"+issID+"/subscribe", invSession, map[string]any{
		"rail": "btc", "amount": 1, "refund_address": "btc-refund-addr",
	})
	if sub.code != 200 {
		t.Fatalf("subscribe btc: %d %s", sub.code, sub.errMsg())
	}
	depAddr, _ := sub.body["deposit_address"].(string)
	subObj, _ := sub.body["subscription"].(map[string]any)
	subID, _ := subObj["id"].(string)

	// $1000 at $100000/BTC = 0.01 BTC = 1_000_000 sats. Credit and confirm.
	h.btc.credit(depAddr, 1_000_000, 1, "")
	h.s.watchDeposits()
	got, _ := h.s.st.SubscriptionByID(subID)
	if got.State != "in_escrow" {
		t.Fatalf("btc subscription state = %q, want in_escrow", got.State)
	}

	// Register the Bitcoin payout mandate and close: registrar-style delivery +
	// release, which delivers the restricted tokens (a BTC rail keeps the two-tx
	// registrar close; it is never atomic).
	if err := h.s.st.UpsertMandate(&PayoutMandate{IssuanceID: issID, Chain: "bitcoin", Address: "btc-issuer-payout"}); err != nil {
		t.Fatalf("UpsertMandate btc: %v", err)
	}
	h.closeAs(issuerSession, "", issuerXOnly, issID, issuerPriv)
	got, _ = h.s.st.SubscriptionByID(subID)
	if got.State != "settled" {
		t.Fatalf("btc subscription state after close = %q, want settled", got.State)
	}
	if h.oa.transfers() != 1 {
		t.Fatalf("delivery transfers = %d, want 1", h.oa.transfers())
	}
	if h.oa.anyTransferCarriedPayment() {
		t.Fatalf("BTC rail must not use the atomic USDX payment leg (registrar model)")
	}

	// A stable post-delivery tick must NOT freeze: the deposit is still confirmed.
	h.s.watchBtcReorgs()
	if h.oa.freezes() != 0 || h.oa.frozen(invAID) {
		t.Fatalf("a still-confirmed deposit triggered a freeze (freezes=%d frozen=%v)", h.oa.freezes(), h.oa.frozen(invAID))
	}

	// The funding deposit is now reorged out AFTER delivery: it falls back to the
	// mempool (confirmations 0). The reorg watcher must apply a GLOBAL freeze on the
	// investor AID.
	h.btc.setConfs(depAddr, 0)
	h.s.watchBtcReorgs()
	if h.oa.freezes() != 1 {
		t.Fatalf("reorg-out freeze calls = %d, want 1", h.oa.freezes())
	}
	if !h.oa.frozen(invAID) {
		t.Fatalf("investor AID not frozen after the deposit reorged out")
	}
	hold, _ := h.s.st.ReorgHoldBySub(subID)
	if hold == nil || hold.State != "active" {
		t.Fatalf("reorg hold = %+v, want an active hold", hold)
	}

	// Idempotent: a second tick while still reorged out must NOT re-freeze.
	h.s.watchBtcReorgs()
	if h.oa.freezes() != 1 {
		t.Fatalf("freeze calls after a second reorged tick = %d, want 1 (no re-freeze)", h.oa.freezes())
	}

	// Re-confirmation lifts the freeze this hold caused.
	h.btc.setConfs(depAddr, 1)
	h.s.watchBtcReorgs()
	if h.oa.frozen(invAID) {
		t.Fatalf("investor AID still frozen after the deposit re-confirmed")
	}
	if h.oa.freezes() != 2 {
		t.Fatalf("freeze endpoint calls = %d, want 2 (one freeze + one unfreeze)", h.oa.freezes())
	}
	hold, _ = h.s.st.ReorgHoldBySub(subID)
	if hold == nil || hold.State != "cleared" {
		t.Fatalf("reorg hold after re-confirmation = %+v, want cleared", hold)
	}

	// Idempotent: a further confirmed tick does not toggle the freeze again.
	h.s.watchBtcReorgs()
	if h.oa.freezes() != 2 {
		t.Fatalf("freeze endpoint calls after a stable tick = %d, want 2 (no churn)", h.oa.freezes())
	}
}
