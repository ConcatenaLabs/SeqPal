package main

import (
	"net/http"
	"sort"
	"strings"
)

// Closing an offering whose subscriptions are in the escrow covenant.
//
// The order is what keeps a subscriber from ever holding neither the tokens
// nor the money:
//
//  1. Nothing moves before the close (CLOSE) has passed, and nothing moves at
//     all unless the release that would follow pays the issuer at least the
//     minimum. Both are checked first, by a dry run of the release.
//  2. The tokens are delivered, subscription by subscription, from the
//     offering's escrow enclave -- a transfer this platform signs with the
//     escrow enclave key and the policy server co-signs, exactly as for every
//     other delivery. The money stays in escrow throughout.
//  3. ONE release spends the coins of every delivered subscription, output 0
//     the raise to the issuer, and it goes to the issuer to sign. Until the
//     issuer signs, the money is still in escrow -- and from the deadline any
//     subscriber can take it back -- so the risk of not signing is the
//     issuer's, never the subscriber's.
//  4. A subscription whose delivery failed is not released: it goes into a
//     cancel, which returns its money and which the issuer signs too; if the
//     issuer never does, the deadline returns it by the keyless refund.

// splitCovenant separates subscriptions held by the covenant from the rest.
func splitCovenant(subs []*Subscription) (covenant, other []*Subscription) {
	for _, sub := range subs {
		if isCovenantSub(sub) {
			covenant = append(covenant, sub)
		} else {
			other = append(other, sub)
		}
	}
	return
}

// prepareEscrowClose checks, before anything moves, that the covenant
// subscriptions can be released: the close has passed, and a release of all of
// them pays the issuer at least the minimum. It writes the refusal itself and
// returns nil when the close must not proceed.
func (s *server) prepareEscrowClose(w http.ResponseWriter, iss *Issuance, covenant []*Subscription) *EscrowTerms {
	t, err := s.st.EscrowTermsByIssuance(iss.ID)
	if err != nil || t == nil {
		writeErr(w, 500, "the offering's escrow terms are missing")
		return nil
	}
	tip, mtp, err := s.chainTip()
	if err != nil {
		writeErr(w, 502, "could not read the chain tip: %v", err)
		return nil
	}
	if !lockReached(t.CloseLock, tip, mtp) {
		writeErr(w, 409, "the escrow can be released from %s and the chain is at block %d; nothing was "+
			"delivered or released", describeLock(t.CloseLock), tip)
		return nil
	}
	// From the deadline anyone may refund a subscriber, so a delivery then (or
	// within a day of it) could leave the issuer with the tokens gone and the
	// money refunded before its release is mined. Too late to close: the
	// subscriptions are refunded instead.
	if lockReached(s.escrowLastCloseLock(t), tip, mtp) {
		writeErr(w, 409, "the escrow's deadline (%s) is too near to deliver and release safely; nothing was "+
			"delivered or released, and every undelivered subscription is refunded from the deadline",
			describeLock(t.DeadlineLock))
		return nil
	}
	var toRelease []*Subscription
	for _, sub := range covenant {
		st, _ := s.st.SettlementByID(sub.ID)
		if st != nil && (st.State == "delivery_failed" || st.State == "awaiting_cancel" || st.State == "refunded") {
			continue
		}
		toRelease = append(toRelease, sub)
	}
	if len(toRelease) > 0 {
		if _, _, err := s.buildReleaseSpend(iss, t, toRelease, false); err != nil {
			writeErr(w, 409, "this offering cannot be released: %v. Nothing was delivered or released. "+
				"The subscriptions stay in escrow: cancel them to return the money now, or from %s anyone "+
				"can refund them", err, describeLock(t.DeadlineLock))
			return nil
		}
	}
	return t
}

// closeCovenant runs steps 2 to 4 and returns the per-subscription results and
// the spends the issuer is to sign.
func (s *server) closeCovenant(iss *Issuance, escrow *EnclaveKey, t *EscrowTerms, covenant []*Subscription, closeHeight int64) ([]map[string]any, []map[string]any) {
	results := make([]map[string]any, 0, len(covenant))
	failedOnce := false
	for i, sub := range covenant {
		// After a delivery has failed, the rest is delivered only while the
		// subscriptions still deliverable can be released at the minimum: a
		// delivery the release cannot follow leaves the issuer holding neither
		// its tokens nor the money.
		if failedOnce {
			var rest []*Subscription
			for _, other := range covenant[:i] {
				if st, _ := s.st.SettlementByID(other.ID); st != nil && st.State == "awaiting_release" {
					rest = append(rest, other)
				}
			}
			rest = append(rest, covenant[i:]...)
			if _, _, err := s.buildReleaseSpend(iss, t, rest, false); err != nil {
				for _, left := range covenant[i:] {
					results = append(results, settleResult(left, "not_delivered",
						"not delivered: after a failed delivery the rest could no longer be released ("+err.Error()+")"))
				}
				break
			}
		}
		res := s.deliverForEscrow(iss, escrow, sub, closeHeight)
		if st, _ := res["state"].(string); st == "delivery_failed" {
			failedOnce = true
		}
		results = append(results, res)
	}
	var deliver, failed []*Subscription
	for _, sub := range covenant {
		st, _ := s.st.SettlementByID(sub.ID)
		if st == nil {
			continue
		}
		switch st.State {
		case "awaiting_release":
			deliver = append(deliver, sub)
		case "delivery_failed", "awaiting_cancel":
			failed = append(failed, sub)
		}
	}
	var spends []map[string]any
	if sp := s.currentOrNewSpend("release", iss, t, deliver); sp != nil {
		spends = append(spends, s.escrowSpendView(t, sp))
	}
	if sp := s.currentOrNewSpend("cancel", iss, t, failed); sp != nil {
		for _, sub := range failed {
			_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"state": "awaiting_cancel"})
		}
		spends = append(spends, s.escrowSpendView(t, sp))
	}
	return results, spends
}

// currentOrNewSpend returns the built spend of a kind that already covers
// exactly these subscriptions -- so a repeated close hands the issuer the same
// transaction, and a signature already made over it still applies -- or
// builds a new one.
func (s *server) currentOrNewSpend(kind string, iss *Issuance, t *EscrowTerms, subs []*Subscription) *EscrowSpend {
	if len(subs) == 0 {
		return nil
	}
	want := subIDs(subs)
	sort.Strings(want)
	spends, _ := s.st.EscrowSpendsByIssuance(iss.ID)
	for i := len(spends) - 1; i >= 0; i-- {
		sp := spends[i]
		if sp.Kind != kind || (sp.State != "built" && sp.State != "broadcasting" && sp.State != "broadcast") {
			continue
		}
		var have []string
		if list, ok := sp.Detail["subscriptions"].([]any); ok {
			for _, v := range list {
				if id, ok := v.(string); ok {
					have = append(have, id)
				}
			}
		}
		sort.Strings(have)
		if strings.Join(have, ",") == strings.Join(want, ",") {
			return sp
		}
	}
	var sp *EscrowSpend
	var err error
	if kind == "release" {
		sp, _, err = s.buildReleaseSpend(iss, t, subs, true)
	} else {
		sp, err = s.buildReturnSpend(kind, iss, t, subs, true)
	}
	if err != nil {
		s.st.Audit("", "escrow."+kind+"_unbuildable", map[string]any{"issuance_id": iss.ID, "error": err.Error()})
		return nil
	}
	return sp
}

// deliverForEscrow delivers one covenant subscription's tokens, with the same
// idempotency and reconciliation as every delivery: one settlement row, the
// intent written before the transfer, a balance scan before any retry. It
// never touches the money.
func (s *server) deliverForEscrow(iss *Issuance, escrow *EnclaveKey, sub *Subscription, closeHeight int64) map[string]any {
	if _, err := s.st.CreateSettlementIfAbsent(sub.ID, iss.ID); err != nil {
		return settleResult(sub, "error", err.Error())
	}
	st, err := s.st.SettlementByID(sub.ID)
	if err != nil || st == nil {
		return settleResult(sub, "error", "settlement record missing")
	}
	switch st.State {
	case "awaiting_release", "settled", "refunded", "delivery_failed", "awaiting_cancel":
		return settleResult(sub, st.State, st.Error)
	}
	investor, err := s.st.AccountByAID(sub.InvestorAID)
	if err != nil || investor == nil {
		_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"state": "delivery_failed", "error": "investor account missing"})
		return settleResult(sub, "delivery_failed", "investor account missing")
	}
	if st.DeliveryTxid == "" {
		if st.State == "delivering" {
			// A delivery was under way when the process stopped. Whether it landed
			// is read from the investor's balance, which must cover every
			// delivery to them under this offering -- this one included -- or it
			// proves nothing. A holder who also bought elsewhere still fools it,
			// so an ambiguous case is left delivering, for a person to reconcile:
			// it is neither released nor refunded until then.
			if !s.escrowDeliveryLanded(iss, sub) {
				s.st.Audit(sub.InvestorAID, "settle.delivery_ambiguous", map[string]any{"issuance_id": iss.ID, "sub": sub.ID})
				return settleResult(sub, "delivering", "a delivery was interrupted and is not visible on the "+
					"investor's balance; reconcile it before this subscription is released or refunded")
			}
			_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"delivery_txid": "reconciled"})
		} else {
			_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"state": "delivering"})
			txid, derr := s.deliverFromEscrow(escrow, sub.InvestorAID, iss.AssetID, sub.TokenAtoms)
			switch {
			case derr == nil:
				_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"delivery_txid": txid})
				_ = s.st.InsertLedger(&LedgerEntry{
					SubscriptionID: sub.ID, IssuanceID: iss.ID, Kind: "delivery", Rail: sub.Rail,
					Amount: sub.TokenAtoms, Ccy: iss.Ticker, Txid: txid,
				})
			default:
				// A refused or failed delivery is a failed delivery. It is never
				// reconciled from a balance here: an investor who already holds the
				// token would look delivered, and their money would be released.
				msg := "delivery failed: " + derr.Error()
				_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"state": "delivery_failed", "error": msg})
				s.st.Audit(sub.InvestorAID, "settle.delivery_failed", map[string]any{"issuance_id": iss.ID, "sub": sub.ID, "error": msg})
				return settleResult(sub, "delivery_failed", msg)
			}
		}
		s.maybeStampVesting(iss, sub, investor, closeHeight)
	}
	_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"state": "awaiting_release", "error": ""})
	s.st.Audit(sub.InvestorAID, "settle.delivered", map[string]any{"issuance_id": iss.ID, "sub": sub.ID, "escrow": true})
	return settleResult(sub, "awaiting_release", "")
}

// escrowLastCloseLock is the last lock at which a close may still deliver:
// one day before the deadline.
func (s *server) escrowLastCloseLock(t *EscrowTerms) uint32 {
	margin := uint32(86400)
	if t.DeadlineLock < locktimeThreshold {
		margin = uint32(s.cfg.blocksPerDay)
	}
	if t.DeadlineLock <= margin {
		return 0
	}
	return t.DeadlineLock - margin
}

// escrowDeliveryLanded is the balance reconciliation of an interrupted
// delivery: the investor's balance must cover every token this offering has
// delivered or is delivering to them, this subscription included.
func (s *server) escrowDeliveryLanded(iss *Issuance, sub *Subscription) bool {
	subs, err := s.st.SubscriptionsByIssuance(iss.ID)
	if err != nil {
		return false
	}
	var owed uint64
	for _, other := range subs {
		if other.InvestorAID != sub.InvestorAID {
			continue
		}
		st, _ := s.st.SettlementByID(other.ID)
		if other.ID == sub.ID || (st != nil && (st.DeliveryTxid != "" || st.State == "delivering")) {
			owed += other.TokenAtoms
		}
	}
	var bal struct {
		Atoms uint64 `json:"atoms"`
	}
	if err := s.callOpenAMP("GET", "/v1/users/"+s.openampAIDFor(sub.InvestorAID)+"/balance?asset="+iss.AssetID, "", nil, &bal); err != nil {
		return false
	}
	return bal.Atoms >= owed
}
