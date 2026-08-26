package main

import (
	"strings"
	"testing"
)

// The provider bills SeqPal per check, cleared or refused, so the check is not
// submitted until it is paid for. Money before submission is the whole point: a
// fee asked for afterwards is a fee the platform has already spent and can be
// refused.
func TestAVerificationIsNotSubmittedUntilItIsPaidFor(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.cfg.kycFeeUSD = 25
	session, aid := walletSession(t, h, testPKH)

	body := map[string]any{"residence": "AE", "screening_name": "Wallet Wendy", "base_eligibility": "ret"}
	v := h.do("POST", "/api/id/verify", session, body)
	if v.code != 402 {
		t.Fatalf("verify with an unpaid fee = %d, want 402 (%s)", v.code, v.raw)
	}
	// Nothing was written and nothing was submitted: an unpaid caller leaves
	// exactly as they arrived, so paying later is a first attempt, not a retry
	// blocked by the "already with the provider" guard.
	if check, _ := h.s.st.LatestVerificationCheck(aid); check != nil {
		t.Fatalf("an unpaid verification must not reach the provider, got %+v", check)
	}
	if c, _ := h.s.st.ClaimsByAID(aid); c != nil {
		t.Fatalf("an unpaid verification must record no claims, got %+v", c)
	}

	// The invoice is quoted with the refusal, so the SPA has something to pay.
	inv := h.do("GET", "/api/id/fees", session, nil)
	if inv.code != 200 {
		t.Fatalf("fees: %d %s", inv.code, inv.raw)
	}
	identity, _ := inv.body["identity"].(map[string]any)
	if identity == nil || identity["state"] != "unpaid" || identity["amount_usd"].(float64) != 25 {
		t.Fatalf("the identity fee must be quoted unpaid at the configured price, got %v", inv.body["identity"])
	}

	pay := h.do("POST", "/api/id/fees/pay", session, map[string]any{"kind": "identity", "rail": "card"})
	if pay.code != 200 {
		t.Fatalf("fees/pay: %d %s", pay.code, pay.errMsg())
	}
	h.s.settleFiatDue()

	// Paid, and recorded as what was actually charged. The invoice used to keep
	// its own zero, so a fee paid on a fiat rail was booked at nothing.
	settled := h.do("GET", "/api/id/fees", session, nil)
	paid, _ := settled.body["identity"].(map[string]any)
	if paid == nil || paid["state"] != "paid" || paid["rail"] != "card" {
		t.Fatalf("the paid fee must be recorded on the invoice, got %v", settled.body["identity"])
	}
	invoiceID, _ := paid["id"].(string)
	stored, err := h.s.st.FeeInvoiceByID(invoiceID)
	if err != nil || stored == nil {
		t.Fatalf("read the invoice back: %v", err)
	}
	if stored.Amount != 2500 || stored.Ccy != "USD" {
		t.Fatalf("the invoice must record the amount charged, got %d %s", stored.Amount, stored.Ccy)
	}

	if v := h.do("POST", "/api/id/verify", session, body); v.code != 200 {
		t.Fatalf("verify after paying = %d, want 200 (%s)", v.code, v.raw)
	}
	check, _ := h.s.st.LatestVerificationCheck(aid)
	if check == nil {
		t.Fatalf("a paid verification must reach the provider")
	}

	// The fee bought THAT check and no other: it is spent, and the next one
	// would be quoted again.
	if spent, _ := h.s.st.FeeInvoiceByID(invoiceID); spent.CheckID != check.ID {
		t.Fatalf("the invoice must record the check it bought, got %q", spent.CheckID)
	}
	after := h.do("GET", "/api/id/fees", session, nil)
	if q, _ := after.body["identity"].(map[string]any); q["state"] != "unpaid" {
		t.Fatalf("a spent fee must not keep reading as paid, got %v", after.body["identity"])
	}

	// It stays bought for the check it bought, though: the provider asking for a
	// better photo is the same check continuing, not a second applicant.
	h.adjudicate(aid, idvResubmit)
	if v := h.do("POST", "/api/id/verify", session, body); v.code != 200 {
		t.Fatalf("resubmitting after a resubmission request = %d, want 200 (%s)", v.code, v.raw)
	}

	// A verification fee belongs to a person and to no offering, so it is not in
	// an offering's escrow ledger.
	if entries, err := h.s.st.LedgerByIssuance(""); err != nil {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Fatalf("a verification fee must not land in an offering's ledger, got %+v", entries)
	}
}

// A business check is charged per business, because the provider charges per
// business. One paid company must not verify the next one for free, and paying
// for somebody else's company must not be possible at all.
func TestEachBusinessIsChargedForItsOwnCheck(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.cfg.kybFeeUSD = 150
	session, aid := walletSession(t, h, testPKH)
	h.verifyIdentity(session, aid, map[string]any{
		"residence": "AE", "screening_name": "Wallet Wendy", "base_eligibility": "ret",
	})

	mk := func(name string) string {
		r := h.do("POST", "/api/entities", session, map[string]any{"name": name, "jurisdiction": "AE"})
		if r.code != 200 {
			t.Fatalf("create entity: %d %s", r.code, r.raw)
		}
		return r.body["entity"].(map[string]any)["id"].(string)
	}
	first, second := mk("First Holdings"), mk("Second Holdings")

	if v := h.do("POST", "/api/id/entities/"+first+"/verify", session, map[string]any{}); v.code != 402 {
		t.Fatalf("verify an unpaid business = %d, want 402 (%s)", v.code, v.raw)
	}
	pay := h.do("POST", "/api/id/fees/pay", session, map[string]any{
		"kind": "business", "entity_id": first, "rail": "card",
	})
	if pay.code != 200 {
		t.Fatalf("fees/pay: %d %s", pay.code, pay.errMsg())
	}
	h.s.settleFiatDue()

	if v := h.do("POST", "/api/id/entities/"+first+"/verify", session, map[string]any{}); v.code != 200 {
		t.Fatalf("verify the paid business = %d, want 200 (%s)", v.code, v.raw)
	}
	// The second company was not paid for, and the first one's payment does not
	// carry over to it.
	if v := h.do("POST", "/api/id/entities/"+second+"/verify", session, map[string]any{}); v.code != 402 {
		t.Fatalf("verify a second unpaid business = %d, want 402 (%s)", v.code, v.raw)
	}

	// Submitted is not verified: the treasury key and the UBO link exist from the
	// moment the check is sent, and the provider decides afterwards.
	entityIn := func(id string) map[string]any {
		p := h.do("GET", "/api/id/passport", session, nil)
		for _, e := range p.body["entities"].([]any) {
			m := e.(map[string]any)
			if m["id"] == id {
				return m
			}
		}
		t.Fatalf("entity %s is missing from the passport", id)
		return nil
	}
	if e := entityIn(first); e["verified"] != false || e["treasury_aid"] == nil {
		t.Fatalf("a submitted business must have a treasury and not be verified, got %v", e)
	}
	check, _ := h.s.st.LatestVerificationCheckForEntity(first)
	if check == nil {
		t.Fatalf("the business check must be recorded against the entity")
	}
	if err := h.s.applyAdjudication(check, idvClear, ""); err != nil {
		t.Fatal(err)
	}
	if e := entityIn(first); e["verified"] != true {
		t.Fatalf("a cleared business must read as verified, got %v", e)
	}
	// The other one is untouched by its sibling's decision.
	if e := entityIn(second); e["verified"] != false {
		t.Fatalf("a business with no check must not read as verified, got %v", e)
	}

	// A stranger cannot raise or pay an invoice against a company they do not own.
	other, _ := walletSession(t, h, strings.Replace(testPKH, "78a58319", "99a58319", 1))
	if r := h.do("POST", "/api/id/fees/pay", other, map[string]any{
		"kind": "business", "entity_id": first, "rail": "card",
	}); r.code != 403 {
		t.Fatalf("paying for another account's business = %d, want 403 (%s)", r.code, r.raw)
	}
}

// A business refusal is the provider's and it is final, exactly as for a person.
// And the endpoint that submits a business is also where the UBO signature is
// recorded, so signing must not buy a second check on a fee that bought one.
func TestABusinessCheckIsBoughtOnceAndRefusedFinally(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.cfg.kybFeeUSD = 150
	session, aid := walletSession(t, h, testPKH)
	h.verifyIdentity(session, aid, map[string]any{
		"residence": "AE", "screening_name": "Wallet Wendy", "base_eligibility": "ret",
	})
	r := h.do("POST", "/api/entities", session, map[string]any{"name": "First Holdings", "jurisdiction": "AE"})
	if r.code != 200 {
		t.Fatalf("create entity: %d %s", r.code, r.raw)
	}
	entity := r.body["entity"].(map[string]any)["id"].(string)

	pay := h.do("POST", "/api/id/fees/pay", session, map[string]any{
		"kind": "business", "entity_id": entity, "rail": "card",
	})
	if pay.code != 200 {
		t.Fatalf("fees/pay: %d %s", pay.code, pay.errMsg())
	}
	h.s.settleFiatDue()
	if v := h.do("POST", "/api/id/entities/"+entity+"/verify", session, map[string]any{}); v.code != 200 {
		t.Fatalf("verify the paid business = %d, want 200 (%s)", v.code, v.raw)
	}
	first, _ := h.s.st.LatestVerificationCheckForEntity(entity)

	// Calling again while it is with the provider records the UBO link and
	// nothing else: no second check, and nothing further charged.
	again := h.do("POST", "/api/id/entities/"+entity+"/verify", session, map[string]any{})
	if again.code != 200 {
		t.Fatalf("recording the UBO link = %d, want 200 (%s)", again.code, again.raw)
	}
	if again.body["check_id"] != first.ID {
		t.Fatalf("that must not buy a second check, got %v want %v", again.body["check_id"], first.ID)
	}

	// Refused, and that is the end of it. The refusal is about the COMPANY: the
	// person who controls it asked for the check and is not the subject of it.
	if err := h.s.applyAdjudication(first, idvReject, "no"); err != nil {
		t.Fatal(err)
	}
	if c, _ := h.s.st.ClaimsByAID(aid); c.Status != "verified" {
		t.Fatalf("refusing a company must not refuse the person who controls it, got %v", c.Status)
	}
	refused := h.do("POST", "/api/id/entities/"+entity+"/verify", session, map[string]any{})
	if refused.code != 409 {
		t.Fatalf("submitting over a refused business = %d, want 409 (%s)", refused.code, refused.raw)
	}
	if p := h.do("GET", "/api/id/passport", session, nil); p.code == 200 {
		for _, e := range p.body["entities"].([]any) {
			if m := e.(map[string]any); m["id"] == entity && m["verified"] != false {
				t.Fatalf("a refused business must not read as verified, got %v", m)
			}
		}
	}
}
