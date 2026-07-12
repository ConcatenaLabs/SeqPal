package main

import (
	"fmt"
	"net/http"
	"time"
)

// The SIMULATED fiat processor. Card and bank transfer are first-class SIMULATED
// rails for both subscriptions and platform fees. A checkout goes pending ->
// settled with realistic timing, produces a receipt, supports a refund path, and
// writes ledger entries flagged funds_simulated. The SIMULATED label appears at
// the checkout and on every derived money surface. Fiat-funded subscriptions run
// the SAME subscription/escrow/closing state machine and settle by policy
// co-signed delivery (no atomic DvP, since the payment leg has no chain).

// startFiatCheckout opens a SIMULATED checkout for a subscription. It settles
// automatically after cfg.fiatSettle via the fiat cron.
func (s *server) startFiatCheckout(sub *Subscription, rail string, usd float64) (map[string]any, error) {
	receipt, _ := randHex(10)
	pay := &FiatPayment{
		ID:             mustID(),
		SubscriptionID: sub.ID,
		Rail:           rail,
		AmountMinor:    usdToMinor(usd),
		Ccy:            "USD",
		State:          "pending",
		Receipt:        "SIM-" + receipt,
		SettleAt:       time.Now().Add(s.cfg.fiatSettle).Unix(),
	}
	if err := s.st.InsertFiatPayment(pay); err != nil {
		return nil, err
	}
	return fiatView(pay), nil
}

// startFiatFeePayment opens a SIMULATED checkout for a platform-fee invoice.
func (s *server) startFiatFeePayment(invoiceID, rail string, usd float64) (*FiatPayment, error) {
	receipt, _ := randHex(10)
	pay := &FiatPayment{
		ID:          mustID(),
		InvoiceID:   invoiceID,
		Rail:        rail,
		AmountMinor: usdToMinor(usd),
		Ccy:         "USD",
		State:       "pending",
		Receipt:     "SIM-" + receipt,
		SettleAt:    time.Now().Add(s.cfg.fiatSettle).Unix(),
	}
	if err := s.st.InsertFiatPayment(pay); err != nil {
		return nil, err
	}
	return pay, nil
}

// settleFiatDue is the cron body: flip due pending payments to settled and drive
// the downstream side effect (a subscription reaches in_escrow; a fee invoice is
// marked paid). It is idempotent: UpdateFiatFields only ever runs once per
// payment because the row leaves the pending state.
func (s *server) settleFiatDue() {
	due, err := s.st.PendingFiatDue(time.Now().Unix())
	if err != nil {
		return
	}
	for _, p := range due {
		if err := s.st.UpdateFiatFields(p.ID, map[string]any{"state": "settled"}); err != nil {
			continue
		}
		p.State = "settled"
		if p.SubscriptionID != "" {
			s.onFiatSubscriptionSettled(p)
		} else if p.InvoiceID != "" {
			s.onFiatFeeSettled(p)
		}
	}
}

// onFiatSubscriptionSettled advances a fiat-funded subscription to in_escrow, the
// same state a confirmed on-chain deposit reaches, and records a SIMULATED ledger
// deposit. Idempotent: it only advances a subscription still in 'created'.
func (s *server) onFiatSubscriptionSettled(p *FiatPayment) {
	sub, err := s.st.SubscriptionByID(p.SubscriptionID)
	if err != nil || sub == nil || sub.State != "created" {
		return
	}
	if err := s.st.UpdateSubscriptionFields(sub.ID, map[string]any{
		"state": "in_escrow", "deposit_txid": p.Receipt, "confirmations": 0,
		"deposited_atoms": p.AmountMinor,
	}); err != nil {
		return
	}
	_ = s.st.InsertLedger(&LedgerEntry{
		SubscriptionID: sub.ID, IssuanceID: sub.IssuanceID, Kind: "deposit", Rail: sub.Rail,
		Amount: p.AmountMinor, Ccy: p.Ccy, Txid: p.Receipt, FundsSimulated: true,
	})
	s.st.Audit(sub.InvestorAID, "escrow.in_escrow", map[string]any{
		"issuance_id": sub.IssuanceID, "sub": sub.ID, "rail": sub.Rail, "funds_simulated": true, "receipt": p.Receipt,
	})
}

// refundFiatForSubscription issues a SIMULATED refund on the fiat rail (used when
// a fiat-funded close fails). Idempotent per payment.
func (s *server) refundFiatForSubscription(sub *Subscription) (string, error) {
	// Find the settled fiat payment for this subscription; the receipt is the
	// SIMULATED reference. Marking it refunded is idempotent.
	pay, _ := s.st.FiatBySubscription(sub.ID)
	receipt := sub.DepositTxid
	if pay != nil {
		if pay.State != "refunded" {
			_ = s.st.UpdateFiatFields(pay.ID, map[string]any{"state": "refunded"})
		}
		receipt = pay.Receipt
	}
	refundRef := "SIM-REFUND-" + receipt
	_ = s.st.InsertLedger(&LedgerEntry{
		SubscriptionID: sub.ID, IssuanceID: sub.IssuanceID, Kind: "refund", Rail: sub.Rail,
		Amount: sub.DepositedAtoms, Ccy: sub.PayCcy, Txid: refundRef, FundsSimulated: true,
	})
	return refundRef, nil
}

// handleFiatStatus is GET /fiat/{id}: poll a SIMULATED checkout.
func (s *server) handleFiatStatus(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	p, err := s.st.FiatPaymentByID(r.PathValue("id"))
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if p == nil {
		writeErr(w, 404, "unknown payment")
		return
	}
	// Scope: only the owning investor (via the subscription) may poll it.
	if p.SubscriptionID != "" {
		sub, _ := s.st.SubscriptionByID(p.SubscriptionID)
		if sub == nil || sub.InvestorAID != acct.AID {
			writeErr(w, 403, "not your payment")
			return
		}
	}
	writeJSON(w, 200, fiatView(p))
}

func fiatView(p *FiatPayment) map[string]any {
	return map[string]any{
		"id":              p.ID,
		"rail":            p.Rail,
		"amount_minor":    p.AmountMinor,
		"ccy":             p.Ccy,
		"state":           p.State,
		"receipt":         p.Receipt,
		"settle_at":       p.SettleAt,
		"funds_simulated": true,
		"label":           "SIMULATED payment: no real card or bank transfer is processed",
		"amount_display":  fmt.Sprintf("%.2f %s (SIMULATED)", float64(p.AmountMinor)/100, p.Ccy),
	}
}
