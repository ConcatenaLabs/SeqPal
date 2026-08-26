package main

import (
	"fmt"
	"strings"
	"time"
)

// The Escrow and Settlement Fee, as published: 0.25% a month on the subscription
// funds held, accrued daily, with a $5,000 minimum and a cap of 3% of the funds.
// Over a typical four-month subscription window that is about 1% of the raise,
// which is what the pricing page says it comes to.
//
// It is charged for TIME, so unlike every other fee here its amount is not known
// when the money arrives -- only when it leaves. What IS fixed at arrival is the
// schedule; what varies is how long the funds sat. Once charged it is written
// down and never recomputed, so a retry cannot move it.
//
// The minimum is a DOLLAR figure, so it applies only where the escrow is
// denominated in dollars. A BTC escrow is charged the rate and the cap, which
// are proportions and need no currency, exactly as the published helper does it.
const (
	escrowRatePerMonthBps = 25  // 0.25% a month
	escrowDaysPerMonth    = 30  // what "a month" means when accruing daily
	escrowCapBps          = 300 // never more than 3% of the funds held
	escrowMinimumUSD      = 5000
)

// escrowUnitsPerUSD is how many of a rail's smallest units make a dollar, and
// whether a dollar minimum applies to it at all.
func escrowUnitsPerUSD(payCcy string) (units uint64, dollarDenominated bool) {
	switch strings.ToUpper(strings.TrimSpace(payCcy)) {
	case "USDX":
		return 100_000_000, true // USDX is a dollar at eight decimals
	case "USD":
		return 100, true // the simulated fiat rail settles in cents
	default:
		return 0, false // BTC, and anything else priced in its own units
	}
}

// escrowFeeShare is what one subscription owes of its issuance's escrow fee.
type escrowFeeShare struct {
	Fee uint64 // what is taken, never more than the deposit it comes out of
	// Owed is the published fee before that clamp. It differs from Fee only when
	// the funds held are smaller than the fee they owe, which the $5,000 minimum
	// makes possible on a small raise; the difference is a shortfall, and the
	// books say so rather than pretending the fee was smaller.
	Owed uint64
	Days int64
}

// publishedEscrowFees is what each subscription of an issuance owes right now.
//
// The minimum and the cap are properties of the OFFERING, not of one investor's
// cheque, so they are applied to the funds held together and then apportioned:
// in proportion to what each subscription accrued, or -- when nothing has
// accrued yet, which is a raise that closed the day it funded -- in proportion
// to what each put in.
func (s *server) publishedEscrowFees(issuanceID string, at int64) (map[string]escrowFeeShare, error) {
	subs, err := s.st.SubscriptionsByIssuance(issuanceID)
	if err != nil {
		return nil, err
	}
	type held struct {
		sub     *Subscription
		days    int64
		accrued uint64
	}
	groups := map[string][]held{}
	for _, sub := range subs {
		if sub.DepositedAtoms == 0 {
			continue
		}
		days := int64(0)
		if sub.EscrowFrom > 0 && at > sub.EscrowFrom {
			days = (at - sub.EscrowFrom) / int64(24*time.Hour/time.Second)
		}
		accrued := mulDiv(sub.DepositedAtoms, uint64(escrowRatePerMonthBps*days), 10000*escrowDaysPerMonth)
		groups[strings.ToUpper(strings.TrimSpace(sub.PayCcy))] = append(
			groups[strings.ToUpper(strings.TrimSpace(sub.PayCcy))], held{sub, days, accrued})
	}

	out := map[string]escrowFeeShare{}
	for ccy, group := range groups {
		var funds, accrued uint64
		for _, h := range group {
			funds += h.sub.DepositedAtoms
			accrued += h.accrued
		}
		if funds == 0 {
			continue
		}
		total := accrued
		if cap := mulDiv(funds, escrowCapBps, 10000); total > cap {
			total = cap
		}
		if units, dollars := escrowUnitsPerUSD(ccy); dollars {
			if min := escrowMinimumUSD * units; total < min {
				total = min
			}
		}

		// Apportion. Time-weighted while anything has accrued, so an investor
		// whose money sat twice as long pays twice as much of it.
		for _, h := range group {
			var owed uint64
			switch {
			case accrued > 0:
				owed = mulDiv(total, h.accrued, accrued)
			default:
				owed = mulDiv(total, h.sub.DepositedAtoms, funds)
			}
			fee := owed
			if fee > h.sub.DepositedAtoms {
				fee = h.sub.DepositedAtoms
			}
			out[h.sub.ID] = escrowFeeShare{Fee: fee, Owed: owed, Days: h.days}
		}
	}
	return out, nil
}

// publishedEscrowFeeFor is one subscription's share, computed fresh.
func (s *server) publishedEscrowFeeFor(sub *Subscription, at int64) (escrowFeeShare, error) {
	plan, err := s.publishedEscrowFees(sub.IssuanceID, at)
	if err != nil {
		return escrowFeeShare{}, err
	}
	share, ok := plan[sub.ID]
	if !ok {
		return escrowFeeShare{}, fmt.Errorf("no escrow fee share for subscription %s", sub.ID)
	}
	return share, nil
}
