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
