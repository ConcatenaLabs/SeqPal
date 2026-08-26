package main

import (
	"sync"
	"testing"
)

// Closing IS the delivery: it moves tokens to the investor and the payment to
// the issuer. The settlement state machine is built to survive a crash
// mid-close -- intent persisted before broadcast, reconciled against the chain
// on retry -- but that is about RESUMING a close, and each settlement still
// reads its state and then acts on it. Two closes at once are not a resume.
func TestOneIssuanceClosesOnceUnderConcurrentCloses(t *testing.T) {
	h := newM5Harness(t, m5opts{escrowConfs: 2})
	fx := setupM6USDX(t, h, 1.0, 100)

	// Sign once, the way the browser does, then present the signed close from
	// several callers at the same time.
	pre := h.do("POST", "/api/issuances/"+fx.issID+"/close", fx.issuerSession, map[string]any{})
	if pre.code != 200 {
		t.Fatalf("close (sign_this): %d %s", pre.code, pre.errMsg())
	}
	signThis, _ := pre.body["sign_this"].(string)
	tag, _ := pre.body["tag"].(string)
	sig := signCanonical(t, fx.issuerPriv, tag, signThis)
	body := map[string]any{"signature": sig, "signer_xonly": fx.issuerXOnly}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.do("POST", "/api/issuances/"+fx.issID+"/close", fx.issuerSession, body)
		}()
	}
	wg.Wait()

	if got := h.oa.transfers(); got != 1 {
		t.Fatalf("the investor was delivered %d times, want 1", got)
	}
	if got := h.seq.sendCount(); got > 1 {
		t.Fatalf("the issuer was paid %d times, want at most 1", got)
	}
	set, _ := h.s.st.SettlementByID(fx.subID)
	if set == nil || set.State != "settled" {
		t.Fatalf("settlement = %+v, want settled", set)
	}
}
