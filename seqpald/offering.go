package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// The promotion-gated offering surface. GET /issuances/{id}/offering serves a
// fixed teaser to anonymous or ungated visitors and the full offering (price,
// mechanics, document manifest) only once the promotion gate passes; the grant is
// the EU offeree-counting event. POST /issuances/{id}/gate captures the statutory
// statement / questionnaires that clear the gates.

const ukHNWIncomeGBP = 100000    // SI 2024/301 high-net-worth income threshold
const ukHNWNetAssetsGBP = 250000 // SI 2024/301 high-net-worth net-assets threshold
const ukStatementValidity = 365 * 24 * time.Hour

// optionalPrincipal resolves the session account if the request carries a valid
// session cookie, else nil. Unlike requireSession it never writes an error: the
// offering teaser is public.
func (s *server) optionalPrincipal(r *http.Request) *Account {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	acct, err := s.st.SessionAccount(c.Value)
	if err != nil {
		return nil
	}
	return acct
}

// handleOffering is GET /issuances/{id}/offering. Public: the teaser renders with
// no session. With a verified session that passes the promotion gate, the full
// offering renders and the offeree count is recorded.
func (s *server) handleOffering(w http.ResponseWriter, r *http.Request) {
	iss, err := s.st.IssuanceByID(r.PathValue("id"))
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if iss == nil {
		writeErr(w, 404, "unknown issuance")
		return
	}
	terms := iss.Terms

	teaser := offeringTeaser(iss, terms)
	acct := s.optionalPrincipal(r)
	if acct == nil {
		writeJSON(w, 200, map[string]any{
			"issuance_id": iss.ID, "gated": true, "teaser": teaser,
			"cta": "verification required to view offering details",
		})
		return
	}
	claims, _ := s.st.ClaimsByAID(acct.AID)
	granted, reqs, _, warn := s.promotionGate(iss, terms, acct, claims)
	if !granted {
		writeJSON(w, 200, map[string]any{
			"issuance_id": iss.ID, "gated": true, "teaser": teaser,
			"requirements": reqs, "cta": "complete the steps below to view offering details",
		})
		return
	}

	// Access granted: record this non-qualified EU offeree (idempotent), then
	// render the full offering.
	s.recordOffereeGrant(iss, acct, claims)
	price, quote, _ := offeringPrice(terms)
	offering := map[string]any{
		"issuance_id":   iss.ID,
		"gated":         false,
		"name":          iss.Name,
		"ticker":        iss.Ticker,
		"asset_id":      iss.AssetID,
		"status":        iss.Status,
		"price":         price,
		"quote":         quote,
		"terms":         terms,
		"contract_hash": iss.ContractHash,
		"rails":         s.availableRails(iss),
		"teaser":        teaser,
	}
	if warn {
		offering["offeree_warning"] = "this offering is approaching the maximum number of offerees in your member state"
	}
	if m, _ := s.st.TermsDocByHash(iss.ContractHash); m != nil {
		offering["manifest_hash"] = m.ManifestHash
	}
	writeJSON(w, 200, offering)
}

// availableRails lists the funding rails the payer may choose. Rail choice is
// ALWAYS the payer's; the BTC rail is present only when the testnet4 node is
// configured. Fiat is a first-class SIMULATED rail.
func (s *server) availableRails(iss *Issuance) []map[string]any {
	rails := []map[string]any{
		{"rail": "usdx", "label": "USDX (Sequentia)", "kind": "onchain", "simulated": false, "final_at_confs": s.cfg.escrowConfs},
	}
	if s.cfg.btcURL != "" {
		rails = append(rails, map[string]any{
			"rail": "btc", "label": "Bitcoin (testnet4, native)", "kind": "onchain-cross-chain",
			"simulated": false, "registrar_model": true, "final_at_confs": s.cfg.escrowConfs,
			"note": "cross-chain registrar settlement: payment confirms on Bitcoin testnet4, then delivery is co-signed on Sequentia; not atomic",
		})
	}
	rails = append(rails,
		map[string]any{"rail": "card", "label": "Card (SIMULATED)", "kind": "fiat", "simulated": true},
		map[string]any{"rail": "bank", "label": "Bank transfer (SIMULATED)", "kind": "fiat", "simulated": true},
	)
	return rails
}

// offeringTeaser is the fixed template shown to anonymous or ungated visitors:
// issuer name, sector, structure, jurisdiction-availability, and a
// verification-required CTA. It carries NO price, raise size, returns language, or
// subscription mechanics, so the ungated page stays below the EU public-offer and
// UK financial-promotion lines.
func offeringTeaser(iss *Issuance, terms json.RawMessage) map[string]any {
	sector := termsString(terms, "sector")
	structure := structureName(iss, terms)
	pj := promotableJurisdictions(terms)
	jurisdictions := make([]string, 0, len(pj))
	for code := range pj {
		jurisdictions = append(jurisdictions, code)
	}
	return map[string]any{
		"issuer":                    iss.Name,
		"sector":                    sector,
		"structure":                 structure,
		"available_in":              jurisdictions,
		"verification_required":     true,
		"jurisdiction_availability": "availability depends on your verified jurisdiction",
	}
}

func termsString(terms json.RawMessage, key string) string {
	if len(terms) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(terms, &m); err != nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// --- POST /issuances/{id}/gate -----------------------------------------------

type gateReq struct {
	Kind      string          `json:"kind"`       // uk_statement | appropriateness | sof
	Basis     string          `json:"basis"`      // uk_statement: hnw | soph
	IncomeGBP float64         `json:"income_gbp"` // uk_statement (hnw)
	NetAssets float64         `json:"net_assets"` // uk_statement (hnw)
	KIDAck    bool            `json:"kid_ack"`    // appropriateness: confirms KID read
	Source    string          `json:"source"`     // sof: source-of-funds description
	AmountUSD float64         `json:"amount_usd"` // sof: declared amount
	Answers   json.RawMessage `json:"answers"`
}

func (s *server) handleGate(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss, err := s.st.IssuanceByID(r.PathValue("id"))
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if iss == nil {
		writeErr(w, 404, "unknown issuance")
		return
	}
	var req gateReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	claims, _ := s.st.ClaimsByAID(acct.AID)
	if !eligibilityLive(claims, time.Now().Unix()) {
		writeErr(w, 403, "a verified SeqPal ID is required")
		return
	}
	res := investorResidence(claims)

	switch req.Kind {
	case "uk_statement":
		if res != "GB" {
			writeErr(w, 400, "the SI 2024/301 statement applies to UK residents only")
			return
		}
		basis := strings.ToLower(strings.TrimSpace(req.Basis))
		if basis == "hnw" {
			if req.IncomeGBP < ukHNWIncomeGBP && req.NetAssets < ukHNWNetAssetsGBP {
				writeErr(w, 400, "the high-net-worth statement requires income of at least GBP 100,000 or net assets of at least GBP 250,000")
				return
			}
		} else if basis != "soph" {
			writeErr(w, 400, "basis must be hnw (high net worth) or soph (self-certified sophisticated)")
			return
		}
		detail, _ := json.Marshal(map[string]any{
			"basis": basis, "income_gbp": req.IncomeGBP, "net_assets": req.NetAssets,
			"statement": "SI 2024/301 " + basis + " investor statement",
		})
		valid := time.Now().Add(ukStatementValidity).Unix()
		if err := s.st.UpsertGateRecord(&GateRecord{AID: acct.AID, IssuanceID: "", Kind: "uk_statement",
			Detail: detail, ValidUntil: valid}); err != nil {
			writeErr(w, 500, "store error")
			return
		}
		// Reflect the statement on the ID so restricted-asset eligibility follows.
		if basis == "hnw" {
			claims.GBHNW = true
		} else {
			claims.GBSoph = true
		}
		_ = s.st.UpsertClaims(claims)
		_, _ = s.writeCategories(acct.AID)
		s.st.Audit(acct.AID, "gate.uk_statement", map[string]any{"basis": basis, "valid_until": valid})
		writeJSON(w, 200, map[string]any{"kind": "uk_statement", "recorded": true, "valid_until": valid})

	case "appropriateness":
		if !req.KIDAck {
			writeErr(w, 400, "you must confirm you have read the KID-style summary")
			return
		}
		detail, _ := json.Marshal(map[string]any{"kid_ack": true, "answers": jsonOrNull(req.Answers)})
		if err := s.st.UpsertGateRecord(&GateRecord{AID: acct.AID, IssuanceID: iss.ID, Kind: "appropriateness",
			Detail: detail, ValidUntil: 0}); err != nil {
			writeErr(w, 500, "store error")
			return
		}
		s.st.Audit(acct.AID, "gate.appropriateness", map[string]any{"issuance_id": iss.ID})
		writeJSON(w, 200, map[string]any{"kind": "appropriateness", "recorded": true})

	case "sof":
		if strings.TrimSpace(req.Source) == "" {
			writeErr(w, 400, "a source-of-funds description is required")
			return
		}
		detail, _ := json.Marshal(map[string]any{"source": req.Source, "amount_usd": req.AmountUSD})
		if err := s.st.UpsertGateRecord(&GateRecord{AID: acct.AID, IssuanceID: iss.ID, Kind: "sof",
			Detail: detail, ValidUntil: 0}); err != nil {
			writeErr(w, 500, "store error")
			return
		}
		s.st.Audit(acct.AID, "gate.sof", map[string]any{"issuance_id": iss.ID})
		writeJSON(w, 200, map[string]any{"kind": "sof", "recorded": true})

	default:
		writeErr(w, 400, "unknown gate kind")
	}
}

func jsonOrNull(r json.RawMessage) json.RawMessage {
	if len(r) == 0 {
		return json.RawMessage("null")
	}
	return r
}
