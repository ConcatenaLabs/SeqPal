package main

import (
	"log"
	"time"
)

// The M5 deposit watcher extends the M3 chain watcher's model to the escrow
// surfaces: it tracks each created subscription's per-rail deposit address (and
// each unpaid on-chain platform-fee address), and advances a deposit to in_escrow
// (or a fee to paid) only at N confirmations. For a native-BTC deposit it captures
// the price-server USD/BTC rate at confirmation. It is authoritative: nothing is
// in_escrow until the deposit txid has N confirmations on the correct chain.

// runMoneyWatcher is the deposit-watching background loop.
func (s *server) runMoneyWatcher(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		s.watchDeposits()
	}
}

// watchDeposits scans every open deposit surface once.
func (s *server) watchDeposits() {
	s.watchSubscriptionDeposits()
	s.watchFeeDeposits()
	s.watchDistributionDeposits()
	// W-3 corporate actions: fund-first dividend deposits, then any claims the
	// funding just made payable.
	s.watchActionDeposits()
	s.payDueActionClaims()
}

func (s *server) watchSubscriptionDeposits() {
	subs, err := s.st.SubscriptionsByState("created")
	if err != nil {
		return
	}
	for _, sub := range subs {
		switch sub.Rail {
		case "usdx":
			if s.cfg.nodeURL == "" {
				continue
			}
			dep, ok, err := s.usdxDeposit(sub.DepositAddress)
			if err != nil || !ok {
				continue
			}
			s.creditSubscription(sub, dep, 0)
		case "btc":
			if s.cfg.btcURL == "" {
				continue
			}
			dep, ok, err := s.btcDeposit(sub.DepositAddress)
			if err != nil || !ok {
				continue
			}
			rate := btcPrice(s.fetchPrices())
			s.creditSubscription(sub, dep, rate)
		}
	}
}

// creditSubscription records the observed confirmation count and, once the
// deposit reaches N confirmations, advances the subscription to in_escrow with
// the deposited amount, the deposit txid, and (for BTC) the captured USD rate. It
// only advances a subscription still in 'created', so it never double-credits.
func (s *server) creditSubscription(sub *Subscription, dep deposit, btcRate float64) {
	fields := map[string]any{"confirmations": dep.Confirmations, "deposit_txid": dep.Txid, "deposited_atoms": dep.Atoms}
	// Payment-sufficiency gate: a deposit only becomes in_escrow (and thus deliverable
	// at close) once it CONFIRMS and covers the expected pay amount. Advancing an
	// underpayment would deliver the full token amount for less than the price, so a
	// confirmed-but-short deposit stays in 'created' with the partial amount recorded
	// and an audit note; the investor tops up or it is refunded on close abandonment.
	sufficient := dep.Atoms >= sub.PayAmount
	if dep.Confirmations >= s.cfg.escrowConfs && sufficient {
		// W-6: the escrow fee accrues NOW, at deposit confirmation, due
		// regardless of outcome. Written BEFORE the state flip so a crash
		// between the two is healed by the next tick (the sub is still
		// 'created', the accrual insert is idempotent); closing and refund
		// consume this figure instead of recomputing.
		s.accrueEscrowFee(sub, dep)
		fields["state"] = "in_escrow"
		if sub.Rail == "btc" && btcRate > 0 {
			fields["usd_rate"] = btcRate
		}
	}
	if dep.Confirmations >= s.cfg.escrowConfs && !sufficient {
		s.st.Audit(sub.InvestorAID, "escrow.underpaid", map[string]any{
			"issuance_id": sub.IssuanceID, "sub": sub.ID, "deposited": dep.Atoms, "required": sub.PayAmount})
	}
	if err := s.st.UpdateSubscriptionFields(sub.ID, fields); err != nil {
		log.Printf("moneywatch: credit %s: %v", sub.ID, err)
		return
	}
	if dep.Confirmations >= s.cfg.escrowConfs && sufficient {
		_ = s.st.InsertLedger(&LedgerEntry{
			SubscriptionID: sub.ID, IssuanceID: sub.IssuanceID, Kind: "deposit", Rail: sub.Rail,
			Amount: dep.Atoms, Ccy: sub.PayCcy, Txid: dep.Txid, FundsSimulated: false,
		})
		detail := map[string]any{"issuance_id": sub.IssuanceID, "sub": sub.ID, "rail": sub.Rail,
			"txid": dep.Txid, "atoms": dep.Atoms, "confirmations": dep.Confirmations}
		if sub.Rail == "btc" && btcRate > 0 {
			detail["usd_rate"] = btcRate
		}
		s.st.Audit(sub.InvestorAID, "escrow.in_escrow", detail)
	}
}

func (s *server) watchFeeDeposits() {
	invoices, err := s.st.UnpaidOnchainFees()
	if err != nil {
		return
	}
	for _, inv := range invoices {
		if inv.Address == "" {
			continue
		}
		var dep deposit
		var ok bool
		switch inv.Rail {
		case "usdx":
			if s.cfg.nodeURL == "" {
				continue
			}
			dep, ok, err = s.usdxDeposit(inv.Address)
		case "btc":
			if s.cfg.btcURL == "" {
				continue
			}
			dep, ok, err = s.btcDeposit(inv.Address)
		default:
			continue
		}
		if err != nil || !ok || dep.Confirmations < s.cfg.escrowConfs {
			continue
		}
		s.onFeeDepositConfirmed(inv, dep.Txid, dep.Atoms)
	}
}

// accrueEscrowFee writes the W-6 deposit-time fee accrual: bps on the deposited
// amount, one kind='escrow_fee' ledger row per subscription, idempotent (the
// existing row IS the accrual). A zero-bps or zero-fee deposit accrues nothing;
// closing then falls back to computing (the same zero).
func (s *server) accrueEscrowFee(sub *Subscription, dep deposit) {
	if _, ok, err := s.st.AccruedEscrowFee(sub.ID); err != nil || ok {
		return
	}
	fee := mulDiv(dep.Atoms, uint64(clampBps(s.cfg.escrowFeeBps, "escrow fee")), 10000)
	// The ledger is the books, and a fee larger than the deposit it came out of
	// is not a fee. The release paths already refuse to pay out less than
	// nothing; this keeps the record from claiming otherwise.
	if fee > dep.Atoms {
		fee = dep.Atoms
	}
	if fee == 0 {
		return
	}
	if err := s.st.InsertLedger(&LedgerEntry{
		SubscriptionID: sub.ID, IssuanceID: sub.IssuanceID, Kind: "escrow_fee", Rail: sub.Rail,
		Amount: fee, Ccy: sub.PayCcy, FundsSimulated: false,
	}); err != nil {
		log.Printf("moneywatch: accrue escrow fee %s: %v", sub.ID, err)
		return
	}
	s.st.Audit(sub.InvestorAID, "escrow.fee_accrued", map[string]any{
		"issuance_id": sub.IssuanceID, "sub": sub.ID, "fee_atoms": fee,
		"deposited": dep.Atoms, "bps": s.cfg.escrowFeeBps,
		"note": "the escrow fee accrues at deposit confirmation and is due regardless of outcome",
	})
}

// runFiatCron drives the SIMULATED fiat processor's pending->settled transitions.
func (s *server) runFiatCron(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		s.settleFiatDue()
	}
}
