package main

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// keyedMutex serializes work per string key. Entries are never reclaimed, which
// is fine at testnet scale: one lock per AID that has ever had a category write.
type keyedMutex struct {
	mu sync.Mutex
	m  map[string]*sync.Mutex
}

func newKeyedMutex() *keyedMutex { return &keyedMutex{m: map[string]*sync.Mutex{}} }

func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()
	l, ok := k.m[key]
	if !ok {
		l = &sync.Mutex{}
		k.m[key] = l
	}
	k.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// openampUser is the shape of GET /v1/users/{aid} (openampd store.User).
type openampUser struct {
	AID        string   `json:"aid"`
	Pubkeys    []string `json:"pubkeys"`
	Categories []string `json:"categories"`
	Frozen     bool     `json:"frozen"`
}

// writeCategories is the ONE door through which every openampd category write
// passes: the verification flow, the expiry cron, and the sanctions/freeze path
// all call it. openampd category writes replace the whole list, so two
// concurrent writers on one AID would race; the per-AID mutex here makes that
// impossible. The sequence is: read the claims record, project the full list,
// POST it, then VERIFY by re-reading GET /v1/users/{aid}, and audit-log the
// pre/post lists and vocabulary version. A verification mismatch is an error,
// never a silent success.
func (s *server) writeCategories(aid string) ([]string, error) {
	unlock := s.catMu.lock(aid)
	defer unlock()

	claims, err := s.st.ClaimsByAID(aid)
	if err != nil {
		return nil, fmt.Errorf("read claims: %w", err)
	}
	newList := projectCategories(claims, time.Now().Unix())
	if newList == nil {
		newList = []string{}
	}

	// Pre-image: the current openampd list (for the audit record).
	before := []string{}
	var cur openampUser
	if err := s.callOpenAMP("GET", "/v1/users/"+aid, "", nil, &cur); err == nil {
		before = append(before, cur.Categories...)
	}

	if err := s.callOpenAMP("POST", "/v1/issuer/categories", s.cfg.issuerToken,
		map[string]any{"aid": aid, "categories": newList}, nil); err != nil {
		return nil, fmt.Errorf("write categories: %w", err)
	}

	// Verify by re-reading. The write is not considered done until the policy
	// server confirms it stuck.
	var after openampUser
	if err := s.callOpenAMP("GET", "/v1/users/"+aid, "", nil, &after); err != nil {
		return nil, fmt.Errorf("verify categories: %w", err)
	}
	if !sameStringSet(after.Categories, newList) {
		return nil, fmt.Errorf("category write did not verify: policy server has %v, expected %v", after.Categories, newList)
	}

	s.st.Audit(aid, "categories.write", map[string]any{
		"aid": aid, "before": before, "after": newList, "vocab_version": vocabVersion,
	})
	return newList, nil
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}
