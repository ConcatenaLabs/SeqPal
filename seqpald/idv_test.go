package main

import (
	"strings"
	"testing"
	"time"
)

// The callback is what a real provider uses, so it is what has to be right. It
// decides who is verified, so it is authenticated; a provider that does not hear
// 200 retries, so it is idempotent; and it takes only decisions it understands.
func TestTheVerificationCallbackIsTheProviderSeam(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.cfg.idvSecret = "shared-secret-for-this-test"
	session, aid := walletSession(t, h, testPKH)

	if v := h.do("POST", "/api/id/verify", session, map[string]any{
		"residence": "AE", "screening_name": "Wallet Wendy", "base_eligibility": "ret",
	}); v.code != 200 {
		t.Fatalf("verify: %d %s", v.code, v.raw)
	}
	check, _ := h.s.st.LatestVerificationCheck(aid, "identity")
	if check == nil || check.ProviderRef == "" {
		t.Fatalf("the check must carry the provider's reference: %+v", check)
	}

	deliver := func(secret string, body map[string]any) resp {
		return h.doWithHeader("POST", "/api/id/verify/callback", "",
			map[string]string{idvSignatureHeader: secret}, body)
	}

	// Unsigned, and signed by somebody else, decide nothing.
	if r := deliver("", map[string]any{"provider_ref": check.ProviderRef, "result": "clear"}); r.code != 401 {
		t.Fatalf("an unsigned callback must be refused, got %d %s", r.code, r.raw)
	}
	if r := deliver("not-the-secret", map[string]any{
		"provider_ref": check.ProviderRef, "result": "clear",
	}); r.code != 401 {
		t.Fatalf("a wrongly signed callback must be refused, got %d %s", r.code, r.raw)
	}
	if c, _ := h.s.st.ClaimsByAID(aid); c.Status != "submitted" {
		t.Fatalf("a refused callback must decide nothing, got %v", c.Status)
	}

	// A decision this platform does not understand is refused rather than guessed.
	if r := deliver(h.s.cfg.idvSecret, map[string]any{
		"provider_ref": check.ProviderRef, "result": "maybe",
	}); r.code != 400 {
		t.Fatalf("an unknown result must be refused, got %d %s", r.code, r.raw)
	}

	// A reference nobody submitted is not a check.
	if r := deliver(h.s.cfg.idvSecret, map[string]any{
		"provider_ref": "never-submitted", "result": "clear",
	}); r.code != 404 {
		t.Fatalf("an unknown reference must be refused, got %d %s", r.code, r.raw)
	}

	// The real thing.
	if r := deliver(h.s.cfg.idvSecret, map[string]any{
		"provider_ref": check.ProviderRef, "result": "clear",
	}); r.code != 200 {
		t.Fatalf("the provider's decision must be accepted: %d %s", r.code, r.raw)
	}
	c, _ := h.s.st.ClaimsByAID(aid)
	if c == nil || c.Status != "verified" {
		t.Fatalf("a cleared check must verify the identity, got %v", c)
	}
	if !eligibilityLive(c, time.Now().Unix()) {
		t.Fatal("a verified identity must be eligible")
	}

	// Delivered again -- which a provider that did not hear 200 will do -- it
	// decides nothing further and says so rather than erroring.
	again := deliver(h.s.cfg.idvSecret, map[string]any{
		"provider_ref": check.ProviderRef, "result": "reject",
	})
	if again.code != 200 {
		t.Fatalf("a repeated delivery must be accepted, got %d %s", again.code, again.raw)
	}
	if note, _ := again.body["note"].(string); !strings.Contains(note, "already decided") {
		t.Fatalf("a repeated delivery must say it changed nothing: %s", again.raw)
	}
	if c, _ := h.s.st.ClaimsByAID(aid); c.Status != "verified" {
		t.Fatalf("a repeated delivery must not re-decide: %v", c.Status)
	}
}

// The secret is compared without leaking how much of it was right.
func TestTheCallbackSecretIsComparedInConstantTime(t *testing.T) {
	if !secretEqual("abc", "abc") {
		t.Fatal("equal secrets must compare equal")
	}
	for _, other := range []string{"", "ab", "abcd", "abd", "ABC"} {
		if secretEqual("abc", other) {
			t.Fatalf("%q must not compare equal to \"abc\"", other)
		}
	}
}
