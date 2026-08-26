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

// The setup fee is the deploy gate, and its invoice is raised on demand by a
// page that polls. Raised twice, the issuer pays whichever one they were quoted
// while the gate reads whichever the lookup happens to return, and a paid
// offering stays blocked.
func TestAnIssuanceRaisesOneSetupFee(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.setupFeeOverrideUSD = 500

	var wg sync.WaitGroup
	ids := make([]string, 8)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if inv, err := h.s.ensureSetupInvoice(&Issuance{ID: "iss-1", StructureID: "native-equity"}); err == nil && inv != nil {
				ids[i] = inv.ID
			}
		}(i)
	}
	wg.Wait()

	var n int
	if err := h.s.st.db.QueryRow(
		`SELECT count(*) FROM fee_invoices WHERE issuance_id = 'iss-1' AND kind = 'setup'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("one offering, one setup fee: got %d", n)
	}
	for i, id := range ids {
		if id == "" {
			t.Fatalf("raise %d returned no invoice", i)
		}
		if id != ids[0] {
			t.Fatalf("concurrent raises produced different setup invoices: %q and %q", ids[0], id)
		}
	}
}

// An enclave key is looked up by what it belongs to and created if there is
// none. Two of those at once make two keys for one company treasury, each
// registered with the policy server, and assets then go to a treasury the
// ownership link does not name.
func TestOneReferenceHasOneEnclaveKey(t *testing.T) {
	h := newHarness(t)

	var wg sync.WaitGroup
	aids := make([]string, 6)
	for i := range aids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if k, err := h.s.createEnclave(enclaveEntityTreasury, "entity-1"); err == nil && k != nil {
				aids[i] = k.AID
			}
		}(i)
	}
	wg.Wait()

	var n int
	if err := h.s.st.db.QueryRow(
		`SELECT count(*) FROM enclave_keys WHERE kind = ? AND ref_id = 'entity-1'`,
		enclaveEntityTreasury).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("one treasury, one key: got %d", n)
	}
	for i, aid := range aids {
		if aid == "" {
			t.Fatalf("create %d returned no key", i)
		}
		if aid != aids[0] {
			t.Fatalf("concurrent creates handed out different treasuries: %q and %q", aids[0], aid)
		}
	}
}

// A fee that settled while a pay request was in flight must not be quoted
// again: the watcher only looks at unpaid invoices, so anything sent to an
// address handed out after settlement sits unnoticed.
func TestAPaidFeeIsNotQuotedAgain(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.cfg.kycFeeUSD = 20
	session, _ := walletSession(t, h, testPKH)

	if r := h.do("POST", "/api/id/fees/pay", session, map[string]any{
		"kind": "identity", "rail": "card",
	}); r.code != 200 {
		t.Fatalf("fees/pay: %d %s", r.code, r.errMsg())
	}
	h.s.settleFiatDue()

	again := h.do("POST", "/api/id/fees/pay", session, map[string]any{"kind": "identity", "rail": "usdx"})
	if again.code != 200 {
		t.Fatalf("paying a settled fee = %d, want 200 saying so (%s)", again.code, again.raw)
	}
	if again.body["already_paid"] != true || again.body["deposit_address"] != nil {
		t.Fatalf("a settled fee must be reported paid and quote nothing, got %s", again.raw)
	}
}
