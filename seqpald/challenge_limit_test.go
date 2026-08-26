package main

import (
	"testing"
	"time"
)

// Asking for a challenge is unauthenticated by necessity and costs a row and
// two node calls, and a descriptor is public, so anyone can ask against anyone
// else's wallet. Both budgets are asserted, and so is the map not growing
// forever, which is the quiet way a long-running process dies.
func TestChallengeBudgets(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := newWindowLimiter(3, 5, time.Hour)
	l.nowFunc = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if ok, _ := l.allow("wallet-a"); !ok {
			t.Fatalf("attempt %d must be allowed", i)
		}
	}
	ok, why := l.allow("wallet-a")
	if ok {
		t.Fatal("one wallet must not be able to mint endlessly")
	}
	if why == "" {
		t.Fatal("a refusal must say why")
	}

	// A different wallet has its own budget, up to the global one.
	if ok, _ := l.allow("wallet-b"); !ok {
		t.Fatal("another wallet must not be punished for the first")
	}
	if ok, _ := l.allow("wallet-c"); !ok {
		t.Fatal("still within the global budget")
	}
	if ok, _ := l.allow("wallet-d"); ok {
		t.Fatal("the global budget must hold whatever the spread of keys")
	}

	// An hour later everything is forgotten, including the keys themselves.
	now = now.Add(time.Hour + time.Second)
	if ok, _ := l.allow("wallet-a"); !ok {
		t.Fatal("the window must expire")
	}
	if len(l.hits) != 1 {
		t.Fatalf("keys that went quiet must be dropped, %d left", len(l.hits))
	}
}
