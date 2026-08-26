package main

import (
	"sync"
	"time"
)

// How fast challenges can be minted.
//
// Asking for a challenge is unauthenticated by necessity: it is the step before
// anyone can prove who they are. It also costs something -- a row, and for a
// wallet challenge two calls to the node to canonicalise a descriptor and derive
// an address -- and a descriptor is public, so anyone can ask for a challenge
// against anyone else's wallet.
//
// Two budgets, neither of which needs to know where a request came from.
// seqpald listens on loopback behind a proxy, so the remote address is always
// the proxy and a forwarded header would only be as trustworthy as whatever set
// it; caps that do not depend on identifying the caller avoid the question.
//
//	per key   one wallet or one enclave key cannot be made to mint endlessly
//	global    the node and the database are protected whatever the spread
//
// The budgets are generous enough that a person retrying will never see them.
const (
	challengesPerKeyPerHour = 30
	challengesGlobalPerHour = 600
)

type windowLimiter struct {
	mu      sync.Mutex
	hits    map[string][]time.Time
	global  []time.Time
	perKey  int
	perAll  int
	window  time.Duration
	nowFunc func() time.Time
}

func newWindowLimiter(perKey, perAll int, window time.Duration) *windowLimiter {
	return &windowLimiter{
		hits: map[string][]time.Time{}, perKey: perKey, perAll: perAll,
		window: window, nowFunc: time.Now,
	}
}

// allow records an attempt and reports whether it is within both budgets.
func (l *windowLimiter) allow(key string) (bool, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.nowFunc()
	cutoff := now.Add(-l.window)

	// Keys that have gone quiet are dropped, or the map grows for as long as the
	// process runs and every distinct key ever seen is remembered forever.
	for k, ts := range l.hits {
		if kept := prune(ts, cutoff); len(kept) == 0 {
			delete(l.hits, k)
		} else {
			l.hits[k] = kept
		}
	}
	l.global = prune(l.global, cutoff)

	if len(l.hits[key]) >= l.perKey {
		return false, "too many challenges for this wallet in the last hour; wait a little and try again"
	}
	if len(l.global) >= l.perAll {
		return false, "this platform is issuing too many challenges right now; try again shortly"
	}
	l.hits[key] = append(l.hits[key], now)
	l.global = append(l.global, now)
	return true, ""
}
