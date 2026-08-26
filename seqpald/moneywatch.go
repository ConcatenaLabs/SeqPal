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
		// The escrow fee is charged for TIME -- 0.25% a month, accrued daily --
		// so its AMOUNT is not knowable here; what starts here is the clock.
		// Recorded before the state flip, so a crash between the two is healed
		// by the next tick with the same instant rather than a later one.
		s.startEscrowClock(sub, fields)
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
		for rail, q := range feeQuotesOf(inv) {
			if q.Address == "" {
				continue // a fiat rail has no address to watch
			}
			var dep deposit
			var ok bool
			switch rail {
			case "usdx":
				if s.cfg.nodeURL == "" {
					continue
				}
				dep, ok, err = s.usdxDeposit(q.Address)
			case "btc":
				if s.cfg.btcURL == "" {
					continue
				}
				dep, ok, err = s.btcDeposit(q.Address)
			default:
				continue
			}
			if err != nil || !ok || dep.Confirmations < s.cfg.escrowConfs {
				continue
			}
			// A fee is a GATE, so it is paid when what was owed has arrived and
			// not before. Crediting on any confirmed amount let a single atom
			// open a gate priced in thousands -- the deploy gate included. The
			// deposit is the running total at the address, so paying in parts
			// still gets there; until it does, the invoice is simply unpaid,
			// which is what the payer's own screen already says.
			if !q.covers(dep.Atoms) {
				continue
			}
			s.onFeeDepositConfirmed(inv, rail, q, dep.Txid, dep.Atoms)
			break
		}
	}
}

// feeQuotesOf is every rail this invoice can be paid on. An invoice written
// before quotes were kept per rail carries exactly one, on its own columns.
func feeQuotesOf(inv *FeeInvoice) map[string]FeeQuote {
	if len(inv.Quotes) > 0 {
		return inv.Quotes
	}
	if inv.Address == "" || inv.Rail == "" {
		return nil
	}
	return map[string]FeeQuote{inv.Rail: {Address: inv.Address, Amount: inv.Amount, Ccy: inv.Ccy}}
}

// startEscrowClock records when a subscription's funds entered escrow, which is
// what the fee is charged for. It is written once: a deposit confirms once, and
// a later tick must not restart the clock and undercharge for the time already
// held.
func (s *server) startEscrowClock(sub *Subscription, fields map[string]any) {
	if sub.EscrowFrom > 0 {
		return
	}
	now := time.Now().Unix()
	fields["escrow_from"] = now
	sub.EscrowFrom = now
	s.st.Audit(sub.InvestorAID, "escrow.clock_started", map[string]any{
		"issuance_id": sub.IssuanceID, "sub": sub.ID, "from": now,
		"note": "the escrow fee accrues daily from here and is due regardless of outcome",
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
