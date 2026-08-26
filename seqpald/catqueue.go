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
// writeCategories stamps an account's eligibility on the policy server, where
// the SeqPal account id and the OpenAMP account id are the same thing.
func (s *server) writeCategories(aid string) ([]string, error) {
	return s.writeCategoriesFor(aid, aid)
}

// writeCategoriesFor separates the two ids, which stop being the same the moment
// a wallet-backed SeqPal ID attaches an enclave: the claims are filed under the
// SeqPal account id it has always had, while the policy server knows only the
// account derived from the enclave key.
func (s *server) writeCategoriesFor(claimsAID, enclaveAID string) ([]string, error) {
	aid := enclaveAID
	unlock := s.catMu.lock(aid)
	defer unlock()

	claims, err := s.st.ClaimsByAID(claimsAID)
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

// stampCategories writes a SeqPal ID's projected categories wherever they are
// kept. For an ID with an OpenAMP account that is the policy server, as it has
// always been. For an ID that is only a wallet there is no account there to
// stamp, and none is needed: what such an ID is verified for is not gated on
// the policy server, and every read of its eligibility comes from the claims
// record this platform keeps. It projects the same list, and returns it.
//
// The distinction is not cosmetic. Every caller of the old function treated a
// write failure as a reason to stop, so a compliance action against a
// wallet-backed ID failed on an account the policy server has never heard of --
// and the action did not happen.
func (s *server) stampCategories(aid string) ([]string, error) {
	acct, err := s.st.AccountByAID(aid)
	if err != nil {
		return nil, fmt.Errorf("read account: %w", err)
	}
	if acct == nil || !s.hasEnclave(acct) {
		claims, err := s.st.ClaimsByAID(aid)
		if err != nil {
			return nil, fmt.Errorf("read claims: %w", err)
		}
		list := projectCategories(claims, time.Now().Unix())
		if list == nil {
			list = []string{}
		}
		return list, nil
	}
	return s.writeCategories(aid)
}

// freezeAtPolicyServer freezes an OpenAMP account, and reports whether there was
// one to freeze. An ID that is only a wallet holds nothing the policy server
// gates, so there is nothing there to freeze -- and treating that as a failed
// freeze stopped a sanctions confirmation before it refused the claims, which
// left the identity verified and eligible.
func (s *server) freezeAtPolicyServer(aid string) (bool, error) {
	acct, err := s.st.AccountByAID(aid)
	if err != nil {
		return false, fmt.Errorf("read account: %w", err)
	}
	if acct == nil || !s.hasEnclave(acct) {
		return false, nil
	}
	if err := s.callOpenAMP("POST", "/v1/issuer/freeze", s.cfg.issuerToken,
		map[string]any{"aid": aid, "frozen": true}, nil); err != nil {
		return false, err
	}
	return true, nil
}
