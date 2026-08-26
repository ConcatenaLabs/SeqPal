package main

import (
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// The rules-amendment chain wiring (M7 section 3). EVERY policy-rules mutation,
// whatever its origin (a corporate action, a category migration, the closing
// vesting stamp), flows through the single chokepoint applyRulesMutation, which:
//
//	(a) persists the intent to rules_mutations BEFORE any external call, so a
//	    half-applied mutation is always detectable;
//	(b) calls openampd POST /v1/issuer/rules;
//	(c) reads the rules BACK from openampd and takes their canonical hash as the
//	    new_rules_hash, so the amendment records exactly what the policy server now
//	    enforces, not what we hoped it would;
//	(d) emits and anchors generateAmendment(prior, new, basis, effective_height).
//
// The invariant, an M7 acceptance proof: GET /v1/assets/{id} rules ALWAYS equals
// the head of the anchored amendment chain (genesis rules hash plus each anchored
// amendment's new_rules_hash). Because the new_rules_hash is computed from the
// read-back, the head and the on-chain rules agree by construction. A crash
// between (b) and (d) leaves a rules_mutations row short of complete; the
// reconcile cron finishes it, so the chain is never left inconsistent.

// canonicalRulesHash is the content address of a rules object as the policy server
// represents it: sha256 over its canonical JSON. Empty input (an asset with no
// rules) hashes to the empty string, the genesis baseline before any rules exist.
func canonicalRulesHash(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	if v == nil {
		return "", nil
	}
	canon, err := canonicalJSON(v)
	if err != nil {
		return "", err
	}
	return sha256Hex(canon), nil
}

// isEmptyRules reports whether a serialized rule set carries no rules at all:
// absent, null, or an object with no keys.
func isEmptyRules(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		// Unreadable is not empty, and this check must not be the thing that
		// decides an unreadable rule set is safe to replace.
		return false
	}
	return len(obj) == 0
}

// onchainRulesRaw returns the asset's rules exactly as openampd serializes them.
func (s *server) onchainRulesRaw(assetID string) (json.RawMessage, error) {
	var out struct {
		ID    string          `json:"id"`
		Rules json.RawMessage `json:"rules"`
	}
	if err := s.callOpenAMP("GET", "/v1/assets/"+assetID, "", nil, &out); err != nil {
		return nil, err
	}
	return out.Rules, nil
}

// onchainRulesHash returns the canonical hash of the asset's current on-chain
// rules. It is the prior-rules baseline for the next mutation and the value the
// amendment-head invariant compares against.
func (s *server) onchainRulesHash(assetID string) (string, error) {
	raw, err := s.onchainRulesRaw(assetID)
	if err != nil {
		return "", err
	}
	return canonicalRulesHash(raw)
}

// applyRulesMutation is the ONE door every policy-rules change passes through. It
// returns the durable mutation record. The openampd rules POST is the critical
// step; the amendment generation and anchor are best-effort here and, if they
// fail, are finished by reconcileRulesMutations, so the caller sees success as
// soon as the on-chain rules are in place while the chain still converges.
func (s *server) applyRulesMutation(iss *Issuance, newRules any, basis string, effectiveHeight int64) (*RulesMutation, error) {
	if iss.AssetID == "" {
		return nil, fmt.Errorf("issuance is not deployed; there are no rules to mutate")
	}
	s.ensureServicingMu()
	unlock := s.rulesMu.lock(iss.ID)
	defer unlock()

	rulesJSON, err := json.Marshal(newRules)
	if err != nil {
		return nil, fmt.Errorf("marshal new rules: %w", err)
	}
	// Every rules change in this platform comes through here, so this is where to
	// refuse the one shape that is never intended: replacing a live rule set with
	// an empty one. A caller that could not read the current rules and started
	// from nothing would otherwise publish an asset with no allowed categories,
	// no denies, no caps and no primary AIDs -- and record it as an amendment.
	// Removing rules deliberately is a different thing and still allowed; what is
	// refused is removing ALL of them.
	if isEmptyRules(rulesJSON) {
		if current, err := s.onchainRulesRaw(iss.AssetID); err == nil && !isEmptyRules(current) {
			return nil, fmt.Errorf("this would replace the asset's entire rule set with an empty " +
				"one, which no amendment intends; nothing was published")
		}
	}
	prior, _ := s.onchainRulesHash(iss.AssetID)

	m := &RulesMutation{
		ID: mustID(), IssuanceID: iss.ID, AssetID: iss.AssetID, PriorRulesHash: prior,
		Basis: basis, EffectiveHeight: effectiveHeight, RulesJSON: string(rulesJSON), State: "pending",
	}
	if err := s.st.InsertRulesMutation(m); err != nil {
		return nil, fmt.Errorf("persist mutation intent: %w", err)
	}

	// (b) the critical step: post the rules. A failure here leaves the record
	// 'pending'; the reconcile cron re-posts the stored rules bytes idempotently.
	if err := s.callOpenAMP("POST", "/v1/issuer/rules", s.cfg.issuerToken,
		map[string]any{"asset": iss.AssetID, "rules": json.RawMessage(rulesJSON)}, nil); err != nil {
		_ = s.st.UpdateRulesMutationFields(m.ID, map[string]any{"error": err.Error()})
		m.Error = err.Error()
		return m, fmt.Errorf("post rules: %w", err)
	}
	_ = s.st.UpdateRulesMutationFields(m.ID, map[string]any{"state": "rules_posted", "error": ""})
	m.State = "rules_posted"

	// (c) + (d): read back, then anchor the amendment against what is now enforced.
	if err := s.finishRulesMutation(iss, m); err != nil {
		// Non-fatal: the rules are live; the reconcile cron completes the amendment.
		log.Printf("applyRulesMutation: amendment for %s deferred to reconcile: %v", iss.ID, err)
	}
	return m, nil
}

// finishRulesMutation performs the read-back + amendment-and-anchor step for a
// mutation whose rules POST has already landed. It is idempotent: a completed
// record is left untouched, so the reconcile cron can call it freely.
func (s *server) finishRulesMutation(iss *Issuance, m *RulesMutation) error {
	if m.State == "complete" {
		return nil
	}
	newHash, err := s.onchainRulesHash(iss.AssetID)
	if err != nil {
		return fmt.Errorf("read back rules: %w", err)
	}
	a, err := s.generateAmendment(iss, m.PriorRulesHash, newHash, m.Basis, m.EffectiveHeight)
	if err != nil {
		_ = s.st.UpdateRulesMutationFields(m.ID, map[string]any{"new_rules_hash": newHash, "error": err.Error()})
		return err
	}
	_ = s.st.UpdateRulesMutationFields(m.ID, map[string]any{
		"state": "complete", "new_rules_hash": newHash, "amend_hash": a.AmendHash,
		"anchor_txid": a.AnchorTxid, "error": "",
	})
	m.State = "complete"
	m.NewRulesHash = newHash
	m.AmendHash = a.AmendHash
	m.AnchorTxid = a.AnchorTxid
	return nil
}

// reconcileRulesMutations is the guard that keeps the amendment chain and the
// on-chain rules consistent after a crash. It re-posts the stored rules of a
// 'pending' record (idempotent: openampd just re-sets the rules) and finishes the
// amendment of a 'rules_posted' record. A record already reflected on-chain but
// missing its amendment is completed without a second POST.
func (s *server) reconcileRulesMutations() {
	s.ensureServicingMu()
	muts, err := s.st.RulesMutationsUnfinished()
	if err != nil {
		return
	}
	for _, m := range muts {
		iss, err := s.st.IssuanceByID(m.IssuanceID)
		if err != nil || iss == nil || iss.AssetID == "" {
			continue
		}
		unlock := s.rulesMu.lock(iss.ID)
		if m.State == "pending" {
			// Re-post the stored rules; safe to repeat, then fall through to finish.
			if err := s.callOpenAMP("POST", "/v1/issuer/rules", s.cfg.issuerToken,
				map[string]any{"asset": iss.AssetID, "rules": json.RawMessage(m.RulesJSON)}, nil); err != nil {
				unlock()
				continue
			}
			_ = s.st.UpdateRulesMutationFields(m.ID, map[string]any{"state": "rules_posted", "error": ""})
			m.State = "rules_posted"
		}
		if err := s.finishRulesMutation(iss, m); err != nil {
			log.Printf("reconcileRulesMutations: %s: %v", m.ID, err)
		}
		unlock()
	}
}

// runRulesReconcileCron periodically heals any half-applied rules mutation.
func (s *server) runRulesReconcileCron(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		s.reconcileRulesMutations()
	}
}

// amendmentHeadStatus computes the M7 acceptance-proof invariant for an issuance:
// whether GET /v1/assets/{id} rules equal the head of the anchored amendment
// chain. When there are no amendments the head is the genesis rules hash and the
// invariant holds trivially; otherwise the on-chain rules hash must equal the last
// amendment's new_rules_hash.
func (s *server) amendmentHeadStatus(iss *Issuance) map[string]any {
	onchain, _ := s.onchainRulesHash(iss.AssetID)
	amends, _ := s.st.AmendmentsByIssuance(iss.ID)
	head := ""
	if len(amends) > 0 {
		head = amends[len(amends)-1].NewRulesHash
	}
	consistent := true
	if len(amends) > 0 {
		consistent = head == onchain
	}
	return map[string]any{
		"onchain_rules_hash": onchain,
		"head_rules_hash":    head,
		"amendment_count":    len(amends),
		"head_consistent":    consistent,
	}
}
