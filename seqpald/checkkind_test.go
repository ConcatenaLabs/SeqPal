package main

import "testing"

// A person's verification is theirs. Their company's check is filed under the
// same account id -- they are who asked for it -- so "the latest check for this
// account" is not the same question as "where does this person's identity
// stand", and answering the second with the first shows a holder their
// company's decision as their own and lets a company's resubmission pay for
// their identity check.
func TestACompanysCheckIsNotThePersonsVerification(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.cfg.kybFeeUSD = 150
	session, aid := walletSession(t, h, testPKH)
	// Verified while checks were free, so what follows is about the SECOND one.
	h.verifyIdentity(session, aid, map[string]any{
		"residence": "AE", "screening_name": "Wallet Wendy", "base_eligibility": "ret",
	})
	h.s.cfg.kycFeeUSD = 20

	r := h.do("POST", "/api/entities", session, map[string]any{"name": "First Holdings", "jurisdiction": "AE"})
	entity := r.body["entity"].(map[string]any)["id"].(string)
	if p := h.do("POST", "/api/id/fees/pay", session, map[string]any{
		"kind": "business", "entity_id": entity, "rail": "card",
	}); p.code != 200 {
		t.Fatalf("fees/pay: %d %s", p.code, p.errMsg())
	}
	h.s.settleFiatDue()
	if v := h.do("POST", "/api/id/entities/"+entity+"/verify", session, map[string]any{}); v.code != 200 {
		t.Fatalf("verify the business: %d %s", v.code, v.raw)
	}
	// The provider wants more from the COMPANY.
	businessCheck, _ := h.s.st.LatestVerificationCheckForEntity(entity)
	if err := h.s.applyAdjudication(businessCheck, idvResubmit, "clearer filing please"); err != nil {
		t.Fatal(err)
	}

	// The passport still reports the person's own verification, not the company's.
	p := h.do("GET", "/api/id/passport", session, nil)
	v, _ := p.body["verification"].(map[string]any)
	if v == nil || v["kind"] != "identity" {
		t.Fatalf("the passport must report this person's own check, got %v", p.body["verification"])
	}

	// And the company's resubmission does not pay for a fresh identity check.
	fees := h.do("GET", "/api/id/fees", session, nil)
	if q, _ := fees.body["identity"].(map[string]any); q["due"] != true {
		t.Fatalf("a fresh identity check is owed for, got %v", fees.body["identity"])
	}
	again := h.do("POST", "/api/id/verify", session, map[string]any{
		"residence": "AE", "screening_name": "Wallet Wendy", "base_eligibility": "ret",
	})
	if again.code != 402 {
		t.Fatalf("re-verifying on a company's resubmission = %d, want 402 (%s)", again.code, again.raw)
	}
}
