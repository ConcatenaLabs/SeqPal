package main

import (
	"sync"
	"testing"
)

// A deploy MINTS. Its idempotency key exists so that a browser which already
// called it, and did not hear back, can call again without minting twice -- and
// that is exactly the case where two calls are in flight together. The key was
// read, then the asset was minted, then the key was recorded, so both calls
// found no prior deploy and both minted.
func TestOneIssuanceMintsOnceUnderConcurrentDeploys(t *testing.T) {
	h := newM5Harness(t, m5opts{})
	session, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID := h.createIssuance(session, "Race Co", "RACE", map[string]any{
		"jurisdictions": map[string]any{"HN": map[string]any{"access": "standard"}}, "price": 1.0,
	})

	body := map[string]any{"issuance_id": issID, "supply": 1000000, "precision": 2}
	var wg sync.WaitGroup
	assets := make([]string, 5)
	codes := make([]int, 5)
	for i := range assets {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := h.do("POST", "/api/deploy", session, body)
			codes[i] = r.code
			assets[i], _ = r.body["asset"].(string)
		}(i)
	}
	wg.Wait()

	h.oa.mu.Lock()
	minted := h.oa.nAsset
	h.oa.mu.Unlock()
	if minted != 1 {
		t.Fatalf("one issuance minted %d assets (codes %v); the chain keeps every one of them",
			minted, codes)
	}
	// And every caller was told about the same asset, so no browser is left
	// holding an id the register does not know.
	for i, a := range assets {
		if codes[i] == 200 && a != assets[0] && assets[0] != "" {
			t.Fatalf("callers were handed different assets: %q and %q", assets[0], a)
		}
	}
}
