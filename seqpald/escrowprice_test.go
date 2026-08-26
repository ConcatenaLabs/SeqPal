package main

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The escrow schedule, read out of the page that publishes it.
func TestTheEscrowFeeIsThePublishedSchedule(t *testing.T) {
	src := readPricingSource(t)
	// "0.25% / month on escrowed funds · $5K min · 3% cap"
	amount := regexp.MustCompile(`item: 'Escrow & Settlement Fee'[\s\S]*?amount:\s*\n?\s*'([^']+)'`).FindStringSubmatch(src)
	if amount == nil {
		t.Fatalf("no published amount for the escrow fee; if that row was renamed, this test has to follow it")
	}
	published := amount[1]

	rate := regexp.MustCompile(`([0-9.]+)% ?/ ?month`).FindStringSubmatch(published)
	if rate == nil {
		t.Fatalf("the published escrow amount does not state a monthly rate: %q", published)
	}
	pct, err := strconv.ParseFloat(rate[1], 64)
	if err != nil {
		t.Fatal(err)
	}
	if got := pct * 100; got != escrowRatePerMonthBps {
		t.Errorf("the rate is published at %v%%/month (%v bps) and charged at %v bps", pct, got, escrowRatePerMonthBps)
	}
	if !strings.Contains(published, "$5K min") {
		t.Errorf("the published amount no longer states a $5K minimum: %q", published)
	}
	if escrowMinimumUSD != 5000 {
		t.Errorf("the minimum is published as $5K and charged at $%v", escrowMinimumUSD)
	}
	if !strings.Contains(published, "3% cap") {
		t.Errorf("the published amount no longer states a 3%% cap: %q", published)
	}
	if escrowCapBps != 300 {
		t.Errorf("the cap is published at 3%% and charged at %v bps", escrowCapBps)
	}
}

// The arithmetic. A dollar of USDX is 1e8 atoms, so these are readable as money.
func TestWhatEscrowCostsOverTime(t *testing.T) {
	const dollar = 100_000_000
	h := newHarness(t)
	h.s.cfg.escrowFeeOverrideBps = -1 // charge the published schedule

	// A raise big enough that the $5,000 minimum never binds: $4,000,000 held.
	// 0.25% a month for 120 days (four months) is 1% of the raise, which is what
	// the pricing page says it typically comes to.
	funds := uint64(4_000_000) * dollar
	from := time.Now().Add(-120 * 24 * time.Hour).Unix()
	issID := seedEscrow(t, h, map[string]uint64{"sub-a": funds}, "USDX", from)
	plan, err := h.s.publishedEscrowFees(issID, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	want := funds / 100 // 1%
	if got := plan["sub-a"].Fee; got != want {
		t.Errorf("four months on a $4M raise charged %d, want %d (1%% of the raise)", got, want)
	}

	// The cap: two years would accrue 6%, and 3% is the most it can be.
	h2 := newHarness(t)
	h2.s.cfg.escrowFeeOverrideBps = -1
	issID2 := seedEscrow(t, h2, map[string]uint64{"sub-b": funds},
		"USDX", time.Now().Add(-720*24*time.Hour).Unix())
	plan2, err := h2.s.publishedEscrowFees(issID2, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := plan2["sub-b"].Fee, funds*3/100; got != want {
		t.Errorf("two years charged %d, want the 3%% cap of %d", got, want)
	}

	// The minimum: a raise closed the day it funded still owes $5,000.
	h3 := newHarness(t)
	h3.s.cfg.escrowFeeOverrideBps = -1
	issID3 := seedEscrow(t, h3, map[string]uint64{"sub-c": uint64(1_000_000) * dollar},
		"USDX", time.Now().Unix())
	plan3, err := h3.s.publishedEscrowFees(issID3, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := plan3["sub-c"].Fee, uint64(escrowMinimumUSD)*dollar; got != want {
		t.Errorf("a same-day close charged %d, want the $5,000 minimum of %d", got, want)
	}
}

// The minimum and the cap belong to the OFFERING, so two investors in one raise
// share one fee between them rather than owing it each.
func TestTheEscrowMinimumIsPerOfferingNotPerInvestor(t *testing.T) {
	const dollar = 100_000_000
	h := newHarness(t)
	h.s.cfg.escrowFeeOverrideBps = -1
	issID := seedEscrow(t, h, map[string]uint64{
		"sub-a": uint64(300_000) * dollar,
		"sub-b": uint64(100_000) * dollar,
	}, "USDX", time.Now().Unix())

	plan, err := h.s.publishedEscrowFees(issID, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	total := plan["sub-a"].Fee + plan["sub-b"].Fee
	if want := uint64(escrowMinimumUSD) * dollar; total != want {
		t.Errorf("two investors in one raise were charged %d between them, want the one $5,000 minimum of %d", total, want)
	}
	// Split by what each put in, since neither has been held any longer.
	if plan["sub-a"].Fee <= plan["sub-b"].Fee {
		t.Errorf("the larger subscription must carry the larger share: %d vs %d",
			plan["sub-a"].Fee, plan["sub-b"].Fee)
	}
}

// A fee cannot take more than the funds it comes out of, and what it could not
// take is recorded rather than forgotten.
func TestAFeeLargerThanTheFundsIsTakenInFullAndTheRestIsOwed(t *testing.T) {
	const dollar = 100_000_000
	h := newHarness(t)
	h.s.cfg.escrowFeeOverrideBps = -1
	// $50 held, which is what a demo raise looks like. The published minimum is
	// $5,000, so the fee owed is a hundred times the money there is.
	issID := seedEscrow(t, h, map[string]uint64{"sub-a": 50 * dollar}, "USDX", time.Now().Unix())
	plan, err := h.s.publishedEscrowFees(issID, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	share := plan["sub-a"]
	if share.Fee != 50*dollar {
		t.Errorf("took %d of a $50 escrow, want all of it", share.Fee)
	}
	if share.Owed != uint64(escrowMinimumUSD)*dollar {
		t.Errorf("owed is recorded as %d, want the published $5,000", share.Owed)
	}
	if share.Owed <= share.Fee {
		t.Errorf("a shortfall this large must be visible: owed %d, taken %d", share.Owed, share.Fee)
	}
}

// The minimum is a dollar figure, so a BTC escrow is charged the rate and the
// cap and no minimum, exactly as the published helper does it.
func TestABitcoinEscrowHasNoDollarMinimum(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.escrowFeeOverrideBps = -1
	// 1 BTC, held a day: far too little to reach $5,000 of anything.
	issID := seedEscrow(t, h, map[string]uint64{"sub-a": 100_000_000},
		"BTC", time.Now().Add(-24*time.Hour).Unix())
	plan, err := h.s.publishedEscrowFees(issID, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	want := mulDiv(100_000_000, escrowRatePerMonthBps*1, 10000*escrowDaysPerMonth)
	if got := plan["sub-a"].Fee; got != want {
		t.Errorf("a day of BTC escrow charged %d sats, want %d (the rate, with no dollar minimum)", got, want)
	}
}

// seedEscrow puts funded subscriptions in escrow from a given moment, and
// returns the issuance they belong to.
func seedEscrow(t *testing.T, h *harness, deposits map[string]uint64, ccy string, from int64) string {
	t.Helper()
	issID := mustID()
	for id, atoms := range deposits {
		sub := &Subscription{
			ID: id, IssuanceID: issID, InvestorAID: "investor-" + id,
			Rail: strings.ToLower(ccy), PayCcy: ccy, State: "in_escrow",
			PayAmount: atoms, DepositedAtoms: atoms, EscrowFrom: from,
		}
		if err := h.s.st.InsertSubscription(sub); err != nil {
			t.Fatal(err)
		}
	}
	return issID
}
