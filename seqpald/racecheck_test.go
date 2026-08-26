package main

import (
	"sync"
	"testing"
)

// One payment buys one check, including when the submissions arrive together.
// The gate reads the invoice, submits, then records what it bought, so two
// requests could both read the same unspent invoice and both be billed by the
// provider for it.
func TestOnePaymentBuysOneCheckUnderConcurrentSubmits(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.cfg.kycFeeUSD = 20
	session, aid := walletSession(t, h, testPKH)

	if pay := h.do("POST", "/api/id/fees/pay", session, map[string]any{
		"kind": "identity", "rail": "card",
	}); pay.code != 200 {
		t.Fatalf("fees/pay: %d %s", pay.code, pay.errMsg())
	}
	h.s.settleFiatDue()

	body := map[string]any{"residence": "AE", "screening_name": "Ordinary Person", "base_eligibility": "ret"}
	var wg sync.WaitGroup
	codes := make([]int, 6)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = h.do("POST", "/api/id/verify", session, body).code
		}(i)
	}
	wg.Wait()

	var accepted int
	for _, c := range codes {
		if c == 200 {
			accepted++
		}
	}
	var checks int
	if err := h.s.st.db.QueryRow(
		`SELECT count(*) FROM verification_checks WHERE aid = ?`, aid).Scan(&checks); err != nil {
		t.Fatal(err)
	}
	if checks != 1 {
		t.Fatalf("one payment bought %d checks (%d submissions accepted); the provider bills for each",
			checks, accepted)
	}
}

// Quoting two rails at once must not lose one of them. payInvoiceOnRail reads
// the invoice, adds its rail's quote to what it read, and writes the whole set
// back, so two requests that read before either wrote would each write a set
// missing the other's -- stranding whatever was sent to the address that
// vanished, which is the thing keeping quotes per rail exists to prevent.
func TestQuotingTwoRailsAtOnceKeepsBoth(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.cfg.kycFeeUSD = 20
	session, _ := walletSession(t, h, testPKH)

	// Raise the invoice first, so both racing calls are paying the same one.
	if r := h.do("POST", "/api/id/fees/pay", session, map[string]any{
		"kind": "identity", "rail": "card",
	}); r.code != 200 {
		t.Fatalf("fees/pay: %d %s", r.code, r.errMsg())
	}

	var wg sync.WaitGroup
	for _, rail := range []string{"card", "bank"} {
		wg.Add(1)
		go func(rail string) {
			defer wg.Done()
			h.do("POST", "/api/id/fees/pay", session, map[string]any{"kind": "identity", "rail": rail})
		}(rail)
	}
	wg.Wait()

	inv, err := h.s.st.AccountFee("", "kyc", "")
	if err != nil {
		t.Fatal(err)
	}
	if inv == nil {
		var aid string
		if err := h.s.st.db.QueryRow(
			`SELECT aid FROM fee_invoices WHERE kind = 'kyc' LIMIT 1`).Scan(&aid); err != nil {
			t.Fatal(err)
		}
		if inv, err = h.s.st.AccountFee(aid, "kyc", ""); err != nil || inv == nil {
			t.Fatalf("read the invoice back: %v", err)
		}
	}
	if len(inv.Quotes) != 2 {
		t.Fatalf("both rails quoted must survive, got %+v", inv.Quotes)
	}
}
