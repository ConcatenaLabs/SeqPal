package main

// Regression tests for the fund-safety review of the escrow covenant: each one
// pins a case where the platform's own flow could move value from the party it
// protects.

import (
	"encoding/hex"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ConcatenaLabs/SeqPal/seqpald/elements"
)

func (e *m14env) subscribeOnly(t *testing.T, sess, refund string, tokens int) *Subscription {
	t.Helper()
	res := e.h.do("POST", "/api/issuances/"+e.issID+"/subscribe", sess, map[string]any{
		"rail": "usdx", "amount": tokens, "refund_address": refund,
	})
	if res.code != 200 {
		t.Fatalf("subscribe: %d %s", res.code, res.errMsg())
	}
	subObj, _ := res.body["subscription"].(map[string]any)
	sub, _ := e.h.s.st.SubscriptionByID(subObj["id"].(string))
	return sub
}

func ledgerCount(t *testing.T, h *m5h, issID, subID, kind string) int {
	rows, err := h.s.st.LedgerByIssuance(issID)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range rows {
		if r.Kind == kind && (subID == "" || r.SubscriptionID == subID) {
			n++
		}
	}
	return n
}


// A refused delivery is a failed delivery, even for an investor who already
// holds the token: it is cancelled, never released.
func TestM14RefusedDeliveryIsNeverReleasedOnAPriorBalance(t *testing.T) {
	e := newM14(t, 1)
	h := e.h
	a, aidA := e.subscribePaid(t, "A", "seq-refund-a", 10)
	h.oa.setBalance(h.s.openampAIDFor(aidA), e.assetID, a.TokenAtoms) // held before, from elsewhere
	h.oa.setRefusal(aidA, "recipient not eligible")
	h.seq.setHeight(int64(e.terms.CloseLock))
	r := e.close(t)
	if r.code != 200 {
		t.Fatalf("close: %d %s", r.code, r.raw)
	}
	st, _ := h.s.st.SettlementByID(a.ID)
	sp := spendsOf(t, r)
	if st.State != "awaiting_cancel" || st.DeliveryTxid != "" || sp["release"] != nil || sp["cancel"] == nil {
		t.Fatalf("refused delivery: settlement %+v, spends %v; want awaiting_cancel, no release, a cancel", st, sp)
	}
}

// What a subscriber paid beyond their subscription goes back to them in the release.
func TestM14OverpaymentIsReturnedInTheRelease(t *testing.T) {
	e := newM14(t, 1)
	h := e.h
	a, _ := e.subscribePaid(t, "A", "seq-refund-a", 10)
	h.seq.addCoin(a.EscrowSPK, m5USDX, a.PayAmount) // paid twice
	h.seq.mine(2)
	h.seq.setHeight(int64(e.terms.CloseLock))
	rel := spendsOf(t, e.close(t))["release"]
	if rel == nil {
		t.Fatal("no release")
	}
	d := rel["detail"].(map[string]any)
	back := uint64(0)
	for _, o := range d["outputs"].([]any) {
		m := o.(map[string]any)
		if m["pays"] == "subscriber" && m["subscription_id"] == a.ID {
			back += uint64(m["atoms"].(float64))
		}
	}
	if back != a.PayAmount || uint64(d["to_issuer_atoms"].(float64)) >= a.PayAmount {
		t.Fatalf("overpayment: returned %d (want %d), to the issuer %v", back, a.PayAmount, d["to_issuer_atoms"])
	}
	if res := e.postSigs(rel, e.signSpend(t, rel, e.releasePriv)); res.code != 200 {
		t.Fatalf("the release with the return output did not verify: %d %s", res.code, res.raw)
	}
}

// A close too near the deadline delivers nothing.
func TestM14CloseNearTheDeadlineDeliversNothing(t *testing.T) {
	e := newM14(t, 1)
	h := e.h
	e.subscribePaid(t, "A", "seq-refund-a", 10)
	h.seq.setHeight(int64(e.terms.DeadlineLock))
	r := e.close(t)
	if r.code != 409 || h.oa.transfers() != 0 {
		t.Fatalf("close at the deadline: %d, deliveries %d; want 409 and none", r.code, h.oa.transfers())
	}
}

// The watcher healing a broadcast and the sign handler broadcasting it apply
// the books once.
func TestM14WatcherAndSignHandlerApplyTheBooksOnce(t *testing.T) {
	e := newM14(t, 1)
	h := e.h
	a, _ := e.subscribePaid(t, "A", "seq-refund-a", 10)
	h.seq.setHeight(int64(e.terms.CloseLock))
	rel := spendsOf(t, e.close(t))["release"]
	sigs := e.signSpend(t, rel, e.releasePriv)
	var slow atomic.Bool
	slow.Store(true)
	h.seq.extra = func(method string, params []json.RawMessage) (any, int, string, bool) {
		if method == "testmempoolaccept" && slow.Load() {
			slow.Store(false)
			time.Sleep(400 * time.Millisecond)
		}
		return nil, 0, "", false
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(100 * time.Millisecond)
		h.s.watchEscrowSpends()
	}()
	res := e.postSigs(rel, sigs)
	wg.Wait()
	if res.code != 200 || ledgerCount(t, h, e.issID, a.ID, "release") != 1 || ledgerCount(t, h, e.issID, "", "network_fee") != 1 {
		t.Fatalf("sign %d; release rows %d, network_fee rows %d; want 200, 1, 1", res.code,
			ledgerCount(t, h, e.issID, a.ID, "release"), ledgerCount(t, h, e.issID, "", "network_fee"))
	}
}

// A payout the issuer's own wallet already broadcast is recorded as paid, and
// a second transaction is never taken.
func TestM14PayoutAlreadyBroadcastIsPaidOnce(t *testing.T) {
	h := newM7Harness(t, m5opts{escrowConfs: 1})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, _ := h.deployLivePrivate(issuerSession, "ONCE", "HN", 1.0)
	aid := h.regHolder("Solo Holder", "HN")
	h.s.st.UpsertClaims(&Claims{AID: aid, Residence: "HN", BaseEligibility: "ret", USPerson: true, Status: "verified"})
	h.oa.setBalance(aid, assetID, 100)
	h.s.st.UpsertInvestorMandate(&InvestorMandate{InvestorAID: aid, Chain: "sequentia", Address: "seq-payout-solo"})
	cr := h.do("POST", "/api/issuances/"+issID+"/distributions", issuerSession, map[string]any{"pool_atoms": 1000})
	runID := cr.body["distribution"].(map[string]any)["id"].(string)
	if s := h.do("POST", "/api/issuances/"+issID+"/distributions/"+runID+"/snapshot", issuerSession, nil); s.code != 200 {
		t.Fatalf("snapshot %d %s", s.code, s.raw)
	}
	plan := h.payoutPlan(issuerSession, issID, runID)
	t1 := h.issuerFunds(plan.unfunded, nil)
	if _, err := h.s.nodeRPC("sendrawtransaction", t1); err != nil {
		t.Fatal(err)
	}
	if r1 := h.postPayout(issuerSession, issID, runID, t1); r1.code != 200 {
		t.Fatalf("already-broadcast payout: %d %s", r1.code, r1.raw)
	}
	h.postPayout(issuerSession, issID, runID, h.issuerFunds(plan.unfunded, nil))
	if n := h.seq.broadcastCount(); n != 1 {
		t.Fatalf("%d payout transactions on chain, want 1", n)
	}
}

// An interrupted delivery is neither refunded nor cancelled.
func TestM14DeliveringIsNotRefundedAtTheDeadline(t *testing.T) {
	e := newM14(t, 1)
	h := e.h
	a, aidA := e.subscribePaid(t, "A", "seq-refund-a", 10)
	h.s.st.CreateSettlementIfAbsent(a.ID, e.issID)
	h.s.st.UpdateSettlementFields(a.ID, map[string]any{"state": "delivering"})
	h.oa.setBalance(h.s.openampAIDFor(aidA), e.assetID, a.TokenAtoms)
	h.seq.setHeight(int64(e.terms.DeadlineLock))
	h.s.watchDeposits()
	if got, _ := h.s.st.SubscriptionByID(a.ID); got.State == "refunded" || h.seq.broadcastCount() != 0 {
		t.Fatalf("a delivering subscription was refunded: %q, broadcasts %d", got.State, h.seq.broadcastCount())
	}
}

// A refund the node refuses is built again at the current rate.
func TestM14RefusedRefundIsRebuilt(t *testing.T) {
	e := newM14(t, 1)
	h := e.h
	a, _ := e.subscribePaid(t, "A", "seq-refund-a", 10)
	h.seq.setHeight(int64(e.terms.DeadlineLock))
	h.seq.mu.Lock()
	h.seq.chain().rejectAll = "min relay fee not met"
	h.seq.mu.Unlock()
	h.s.watchDeposits()
	h.seq.mu.Lock()
	h.seq.chain().rejectAll = ""
	h.seq.mu.Unlock()
	h.s.watchDeposits()
	if got, _ := h.s.st.SubscriptionByID(a.ID); got.State != "refunded" {
		t.Fatalf("after the refusal cleared: subscription %q, want refunded", got.State)
	}
}

var _ = hex.EncodeToString
var _ = elements.ExplicitValue
