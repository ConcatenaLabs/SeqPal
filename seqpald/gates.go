package main

import (
	"encoding/json"
	"strings"
	"time"
)

// Offer-side gates. No real money moves without them. Two layers:
//
//   - The PROMOTION gate (view): the offering page is served filtered on the
//     terms' promotable_jurisdictions facet; an anonymous or ungated visitor gets
//     a fixed teaser, never price/raise/returns/mechanics. A UK resident must
//     record the SI 2024/301 statutory statement before the offering renders. The
//     grant is the EU offeree-counting event: distinct non-qualified persons per
//     member state, warned at 149, blocked at the 150th.
//   - The SUBSCRIBE gates (money): an appropriateness questionnaire + KID surface
//     for retail-lift jurisdictions, a source-of-funds questionnaire above a
//     USD 10k-equivalent subscription, and US 506(c) artifact-gated accreditation.

const offereeWarnAt = 148 // warn approaching the sub-150 Prospectus Regulation exemption
const offereeCapAt = 149  // admit at most 149; block the 150th distinct non-qualified offeree per member state
const sofThresholdUSD = 10000

// The EU member-state set used for offeree counting and the retail lift is the
// canonical euMemberStates map defined in characterization.go (the 27 members).

// requirement is one unsatisfied gate the investor must clear, returned to the
// SPA so it can render the exact next step.
type requirement struct {
	Kind    string `json:"kind"` // uk_statement | appropriateness | sof | accreditation | jurisdiction | offeree_cap
	Message string `json:"message"`
	Blocked bool   `json:"blocked"` // true = a hard block the investor cannot self-clear
}

// promotableJurisdictions reads the terms facet (committed in terms_hash). Empty
// means the offering is not promotable to anyone (fail closed): a public offering
// must name its promotable jurisdictions explicitly.
func promotableJurisdictions(terms json.RawMessage) map[string]bool {
	set := map[string]bool{}
	if len(terms) == 0 {
		return set
	}
	var m map[string]any
	if err := json.Unmarshal(terms, &m); err != nil {
		return set
	}
	if pj, ok := m["promotable_jurisdictions"].([]any); ok {
		for _, v := range pj {
			if s, ok := v.(string); ok {
				if code := normalizeResidence(s); code != "" {
					set[code] = true
				}
			}
		}
	}
	return set
}

// isQualified reports whether the investor is a qualified/professional investor,
// who is not counted against the EU sub-150 non-qualified-offeree exemption and
// is exempt from the retail appropriateness lift.
func isQualified(c *Claims) bool {
	if c == nil {
		return false
	}
	if c.BaseEligibility == "pro" {
		return true
	}
	return false
}

// investorResidence returns the investor's verified residence code, or "".
func investorResidence(c *Claims) string {
	if c == nil {
		return ""
	}
	return normalizeResidence(c.Residence)
}

// hasValidGate reports whether a non-expired gate record of the given kind exists.
func (s *server) hasValidGate(aid, issuanceID, kind string) bool {
	g, err := s.st.GateRecord(aid, issuanceID, kind)
	if err != nil || g == nil {
		return false
	}
	if g.ValidUntil > 0 && time.Now().Unix() >= g.ValidUntil {
		return false
	}
	return true
}

// promotionGate evaluates whether the full offering may render for this investor.
// It never mutates state; recordOffereeGrant applies the counting side effect
// only when access is actually granted. Returns (granted, requirements, count,
// warn).
func (s *server) promotionGate(iss *Issuance, terms json.RawMessage, acct *Account, claims *Claims) (bool, []requirement, int, bool) {
	var reqs []requirement
	res := investorResidence(claims)
	if !eligibilityLive(claims, time.Now().Unix()) || res == "" {
		return false, []requirement{{Kind: "jurisdiction", Message: "a verified SeqPal ID is required to view this offering", Blocked: false}}, 0, false
	}

	// Jurisdiction filter: promotable_jurisdictions must include the investor's
	// residence (US persons are matched on US as well).
	pj := promotableJurisdictions(terms)
	allowed := pj[res] || (claims.USPerson && pj["US"])
	if len(pj) > 0 && !allowed {
		return false, []requirement{{Kind: "jurisdiction",
			Message: "this offering is not available in your jurisdiction", Blocked: true}}, 0, false
	}

	// UK statutory statement (SI 2024/301) before the offering renders, valid 12
	// months, recorded on the ID.
	if res == "GB" && !isQualified(claims) && !s.hasValidGate(acct.AID, "", "uk_statement") {
		reqs = append(reqs, requirement{Kind: "uk_statement",
			Message: "record the SI 2024/301 high-net-worth or self-certified sophisticated investor statement to view this offering"})
	}

	// EU offeree counting: block the 150th distinct non-qualified offeree per
	// member state. An already-counted person is not blocked at the cap.
	count := 0
	warn := false
	if euMemberStates[res] && !isQualified(claims) {
		c, _ := s.st.OffereeCount(iss.ID, res)
		counted, _ := s.st.OffereeCounted(iss.ID, res, acct.AID)
		count = c
		if !counted && c >= offereeCapAt {
			reqs = append(reqs, requirement{Kind: "offeree_cap",
				Message: "this offering has reached the maximum number of offerees permitted in your member state", Blocked: true})
		} else if !counted && c+1 >= offereeWarnAt {
			warn = true
		}
	}

	granted := len(reqs) == 0
	return granted, reqs, count, warn
}

// recordOffereeGrant applies the counting side effect when access is granted to a
// non-qualified EU offeree. Idempotent per (issuance, state, aid).
func (s *server) recordOffereeGrant(iss *Issuance, acct *Account, claims *Claims) {
	res := investorResidence(claims)
	if !euMemberStates[res] || isQualified(claims) {
		return
	}
	n, err := s.st.RecordOfferee(iss.ID, res, acct.AID)
	if err != nil {
		return
	}
	s.st.Audit(acct.AID, "offeree.count", map[string]any{
		"issuance_id": iss.ID, "member_state": res, "count": n,
	})
}

// subscribeGates evaluates the money-movement gates for a subscription of a given
// USD size. It runs after eligibility and the promotion gate. Returns the
// unsatisfied requirements (empty = clear to fund).
func (s *server) subscribeGates(iss *Issuance, terms json.RawMessage, acct *Account, claims *Claims, subUSD float64) []requirement {
	var reqs []requirement
	res := investorResidence(claims)

	// Appropriateness + KID for retail-lift jurisdictions (EU/EEA + UK) when the
	// investor is not a qualified/professional investor.
	if (euMemberStates[res] || res == "GB") && !isQualified(claims) {
		if !s.hasValidGate(acct.AID, iss.ID, "appropriateness") {
			reqs = append(reqs, requirement{Kind: "appropriateness",
				Message: "complete the appropriateness questionnaire and confirm you have read the KID before subscribing"})
		}
	}

	// Source-of-funds above USD 10k-equivalent.
	if subUSD >= sofThresholdUSD && !s.hasValidGate(acct.AID, iss.ID, "sof") {
		reqs = append(reqs, requirement{Kind: "sof",
			Message: "complete the source-of-funds declaration for subscriptions of USD 10,000 or more"})
	}

	// US 506(c): a US person subscribing to an offering that admits only accredited
	// US investors must have current (non-stale, <=90 day) accredited status with a
	// verification artifact on file.
	if claims != nil && claims.USPerson && offeringRequiresUSAccredited(terms) {
		now := time.Now().Unix()
		accCurrent := claims.Accredited && claims.AccredArtifact != "" &&
			(claims.AccredValidUntil == 0 || now < claims.AccredValidUntil)
		if !accCurrent {
			reqs = append(reqs, requirement{Kind: "accreditation", Blocked: true,
				Message: "US investors in this offering must have current verified-accredited status (Rule 506(c), re-verified within 90 days)"})
		}
	}
	return reqs
}

// offeringRequiresUSAccredited reports whether the terms admit US persons only on
// an accredited basis (j:US access "restricted", or an explicit us_506c marker).
func offeringRequiresUSAccredited(terms json.RawMessage) bool {
	if len(terms) == 0 {
		return false
	}
	var mt struct {
		Jurisdictions map[string]jurisdictionTerm `json:"jurisdictions"`
		US506c        bool                        `json:"us_506c"`
	}
	if err := json.Unmarshal(terms, &mt); err != nil {
		return false
	}
	if mt.US506c {
		return true
	}
	if jt, ok := mt.Jurisdictions["US"]; ok {
		return strings.EqualFold(strings.TrimSpace(jt.Access), "restricted")
	}
	return false
}
