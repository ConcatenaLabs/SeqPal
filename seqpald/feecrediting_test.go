package main

import (
	"sync"
	"testing"
)

// A fee is a gate, so it opens when what was owed has arrived and not before.
func TestAFeeIsPaidOnlyWhenTheAmountOwedHasArrived(t *testing.T) {
	q := FeeQuote{Address: "addr", Amount: 2_500_000_000, Ccy: "USDX"}
	for _, c := range []struct {
		atoms uint64
		want  bool
		why   string
	}{
		{1, false, "a single atom must not open a gate priced in thousands"},
		{2_499_999_999, false, "one atom short is short"},
		{2_500_000_000, true, "the amount owed pays it"},
		{9_000_000_000, true, "an overpayment pays it"},
	} {
		if got := q.covers(c.atoms); got != c.want {
			t.Errorf("covers(%d) = %v, want %v: %s", c.atoms, got, c.want, c.why)
		}
	}
	// An invoice written before rails were quoted separately has no amount on
	// the quote, and must keep behaving as it did rather than never settling.
	if !(FeeQuote{Address: "addr"}).covers(1) {
		t.Errorf("an unquoted amount must stay covered by any deposit")
	}
}

// Every rail an invoice was ever quoted on stays watched, including the one it
// is no longer showing.
func TestEveryQuotedRailStaysWatched(t *testing.T) {
	inv := &FeeInvoice{
		Rail: "btc", Address: "btc-addr", Amount: 40, Ccy: "BTC",
		Quotes: map[string]FeeQuote{
			"usdx": {Address: "usdx-addr", Amount: 2_500_000_000, Ccy: "USDX"},
			"btc":  {Address: "btc-addr", Amount: 40, Ccy: "BTC"},
		},
	}
	got := feeQuotesOf(inv)
	if len(got) != 2 || got["usdx"].Address != "usdx-addr" {
		t.Fatalf("both rails must stay watched, got %+v", got)
	}
	// An invoice from before the change carries its single quote on its own
	// columns, and is watched from there.
	old := &FeeInvoice{Rail: "usdx", Address: "old-addr", Amount: 7, Ccy: "USDX"}
	if q := feeQuotesOf(old)["usdx"]; q.Address != "old-addr" || q.Amount != 7 {
		t.Fatalf("an invoice written before quotes must still be watched, got %+v", feeQuotesOf(old))
	}
	if feeQuotesOf(&FeeInvoice{}) != nil {
		t.Fatalf("an invoice with nothing to watch must yield nothing")
	}
}

// Quoting the same rail twice must hand back the SAME address. Burning a fresh
// one forgot the previous, so a payer who had already sent to it was never
// credited.
func TestQuotingARailTwiceKeepsTheSameAddress(t *testing.T) {
	h := newHarness(t)
	inv := &FeeInvoice{ID: mustID(), AID: "acct-1", Kind: "kyc", AmountUSD: 25, State: "unpaid"}
	if err := h.s.st.InsertFeeInvoice(inv); err != nil {
		t.Fatal(err)
	}
	derived := 0
	derive := func() (string, error) {
		derived++
		return "derived-address", nil
	}
	first, err := h.s.feeQuoteFor(inv, "usdx", 2_500_000_000, "USDX", derive)
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.s.feeQuoteFor(inv, "usdx", 2_500_000_000, "USDX", derive)
	if err != nil {
		t.Fatal(err)
	}
	if derived != 1 || first.Address != second.Address {
		t.Fatalf("the same rail must reuse its address (derived %d times, %q then %q)",
			derived, first.Address, second.Address)
	}

	// Switching rails keeps the first one's quote, so what was sent to it still
	// credits.
	if _, err := h.s.feeQuoteFor(inv, "btc", 40, "BTC", func() (string, error) {
		return "btc-address", nil
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := h.s.st.FeeInvoiceByID(inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Quotes["usdx"].Address != "derived-address" || stored.Quotes["btc"].Address != "btc-address" {
		t.Fatalf("choosing a second rail must not strand the first, got %+v", stored.Quotes)
	}
	if stored.Rail != "btc" || stored.Address != "btc-address" {
		t.Fatalf("the invoice must show the rail last chosen, got %s/%s", stored.Rail, stored.Address)
	}
	// A payer acting on the earlier, cheaper quote is not left short when the
	// price moves under them.
	if _, err := h.s.feeQuoteFor(inv, "btc", 99, "BTC", func() (string, error) {
		return "another-address", nil
	}); err != nil {
		t.Fatal(err)
	}
	stored, _ = h.s.st.FeeInvoiceByID(inv.ID)
	if stored.Quotes["btc"].Amount != 40 {
		t.Fatalf("a re-quote must not raise what an earlier quote asked for, got %d",
			stored.Quotes["btc"].Amount)
	}
}

// The page that quotes a verification fee polls, and several cards poll at
// once. Two raises must not become two invoices, or the holder pays one while
// the gate reads the other.
func TestAnAccountFeeIsRaisedOnlyOnce(t *testing.T) {
	h := newHarness(t)
	var wg sync.WaitGroup
	ids := make([]string, 8)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if inv, err := h.s.ensureVerificationFee("acct-1", "kyc", ""); err == nil && inv != nil {
				ids[i] = inv.ID
			}
		}(i)
	}
	wg.Wait()
	for i, id := range ids {
		if id == "" {
			t.Fatalf("raise %d returned no invoice", i)
		}
		if id != ids[0] {
			t.Fatalf("concurrent raises produced different invoices: %q and %q", ids[0], id)
		}
	}
	var n int
	if err := h.s.st.db.QueryRow(
		`SELECT count(*) FROM fee_invoices WHERE aid = 'acct-1' AND kind = 'kyc'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("one fee, one invoice: got %d", n)
	}
}
