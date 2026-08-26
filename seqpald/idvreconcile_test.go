package main

import (
	"errors"
	"testing"
	"time"
)

// A decision is delivered over a network, once. Restart the process between the
// submission and the delivery -- or lose the delivery -- and without a backstop
// the holder sits at "submitted" forever: they cannot submit again, because
// submitting over a check that is with the provider is refused, and they have
// already paid for the check.
func TestACheckWhoseDecisionWasNeverDeliveredIsChased(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	// A provider that answers when asked but never delivers, which is exactly
	// what a dropped callback looks like from here.
	h.s.idv = &silentIDV{}
	h.s.cfg.idvGrace = 0
	session, aid := walletSession(t, h, testPKH)

	if v := h.do("POST", "/api/id/verify", session, map[string]any{
		"residence": "AE", "screening_name": "Ordinary Person", "base_eligibility": "ret",
	}); v.code != 200 {
		t.Fatalf("verify: %d %s", v.code, v.raw)
	}
	if c, _ := h.s.st.ClaimsByAID(aid); c.Status != "submitted" {
		t.Fatalf("the check must be with the provider, got %v", c.Status)
	}
	// And the holder has no way out on their own.
	if again := h.do("POST", "/api/id/verify", session, map[string]any{
		"residence": "AE", "screening_name": "Ordinary Person",
	}); again.code != 409 {
		t.Fatalf("submitting over an open check = %d, want 409", again.code)
	}

	h.s.reconcileVerifications()

	if c, _ := h.s.st.ClaimsByAID(aid); c.Status != "verified" {
		t.Fatalf("a chased check must reach its decision, got %v", c.Status)
	}
	check, _ := h.s.st.LatestVerificationCheck(aid)
	if check.Status != "complete" || check.Result != string(idvClear) {
		t.Fatalf("the check must be recorded as decided, got %+v", check)
	}

	// The decision is recorded once. A callback that turns up late -- the
	// provider retrying what we had already chased -- must not decide again.
	if decided, err := h.s.st.CompleteVerificationCheck(check.ID, string(idvReject), "late", 1); err != nil {
		t.Fatal(err)
	} else if decided {
		t.Fatalf("a second decision must not overwrite the first")
	}
	if c, _ := h.s.st.ClaimsByAID(aid); c.Status != "verified" {
		t.Fatalf("the first decision stands, got %v", c.Status)
	}
}

// A check that is still in flight is not late, and must be left alone.
func TestACheckStillInFlightIsNotChased(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.idv = &silentIDV{}
	h.s.cfg.idvGrace = time.Hour
	session, aid := walletSession(t, h, testPKH)

	h.do("POST", "/api/id/verify", session, map[string]any{
		"residence": "AE", "screening_name": "Ordinary Person", "base_eligibility": "ret",
	})
	h.s.reconcileVerifications()
	if c, _ := h.s.st.ClaimsByAID(aid); c.Status != "submitted" {
		t.Fatalf("a check inside its grace must be left with the provider, got %v", c.Status)
	}
}

// silentIDV accepts checks and never delivers a decision, but answers when it is
// asked -- a provider whose webhook was lost.
type silentIDV struct{}

func (p *silentIDV) Name() string { return "silent" }

func (p *silentIDV) CreateCheck(c *VerificationCheck) (string, error) {
	return "silent-" + c.ID, nil
}

func (p *silentIDV) PollCheck(c *VerificationCheck) (idvDecision, string, bool, error) {
	decision, reason := simulatedDecision(c.SubjectName)
	return decision, reason, true, nil
}

// A submission that never reached the provider must leave nothing behind. Left
// recorded as "submitted" the account is stuck for good: submitting again is
// refused as already open, and there is no check for the reconciler to chase.
func TestASubmissionThatNeverReachedTheProviderCanBeRetried(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	down := &unreachableIDV{}
	h.s.idv = down
	session, aid := walletSession(t, h, testPKH)

	body := map[string]any{"residence": "AE", "screening_name": "Ordinary Person", "base_eligibility": "ret"}
	if v := h.do("POST", "/api/id/verify", session, body); v.code != 502 {
		t.Fatalf("verify with the provider down = %d, want 502 (%s)", v.code, v.raw)
	}
	if c, _ := h.s.st.ClaimsByAID(aid); c != nil {
		t.Fatalf("a submission that never happened must record nothing, got %+v", c)
	}

	// And the holder can simply try again once the provider is back.
	down.up = true
	if v := h.do("POST", "/api/id/verify", session, body); v.code != 200 {
		t.Fatalf("verify after the provider came back = %d, want 200 (%s)", v.code, v.raw)
	}
	h.adjudicate(aid, idvClear)
	if c, _ := h.s.st.ClaimsByAID(aid); c.Status != "verified" {
		t.Fatalf("the retry must verify, got %v", c.Status)
	}

	// A holder who is ALREADY verified must not lose that to a failed attempt.
	down.up = false
	if v := h.do("POST", "/api/id/verify", session, body); v.code != 502 {
		t.Fatalf("re-verify with the provider down = %d, want 502", v.code)
	}
	if c, _ := h.s.st.ClaimsByAID(aid); c.Status != "verified" {
		t.Fatalf("a failed attempt must not throw away a verification, got %v", c.Status)
	}
}

var errProviderDown = errors.New("provider unreachable")

type unreachableIDV struct{ up bool }

func (p *unreachableIDV) Name() string { return "unreachable" }

func (p *unreachableIDV) CreateCheck(c *VerificationCheck) (string, error) {
	if !p.up {
		return "", errProviderDown
	}
	return "back-" + c.ID, nil
}

func (p *unreachableIDV) PollCheck(*VerificationCheck) (idvDecision, string, bool, error) {
	return "", "", false, nil
}
