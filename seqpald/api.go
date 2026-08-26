package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"
)

func (s *server) handleMe(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	entities, err := s.st.EntitiesByOwner(acct.AID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	issuances, err := s.st.IssuancesByOwner(acct.AID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	writeJSON(w, 200, map[string]any{
		"account":  acct,
		"entities": entities,
		// Whether this ID can hold OpenAMP restricted assets, computed from the
		// wallets it has linked. The browser used to infer this from the kind the
		// account was FOUNDED with, which is a different question and drifts from
		// this one the moment a wallet is linked.
		"has_enclave": s.hasEnclave(acct),
		"issuances":   issuances,
	})
}

type entityReq struct {
	Name          string          `json:"name"`
	Jurisdiction  string          `json:"jurisdiction"`
	StructureNote string          `json:"structure_note"`
	Profile       json.RawMessage `json:"profile"`
}

func (s *server) handleCreateEntity(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	var req entityReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErr(w, 400, "name is required")
		return
	}
	profile := req.Profile
	if len(profile) == 0 {
		profile, _ = json.Marshal(map[string]any{"structure_note": req.StructureNote})
	}
	id, err := randHex(16)
	if err != nil {
		writeErr(w, 500, "id generation failed")
		return
	}
	e := &Entity{
		ID:           id,
		OwnerAID:     acct.AID,
		Name:         req.Name,
		Jurisdiction: strings.TrimSpace(req.Jurisdiction),
		Profile:      profile,
		CreatedAt:    time.Now().Unix(),
	}
	if err := s.st.CreateEntity(e); err != nil {
		writeErr(w, 500, "could not create the entity")
		return
	}
	s.st.Audit(acct.AID, "entity.create", map[string]any{"entity_id": e.ID, "name": e.Name, "jurisdiction": e.Jurisdiction})
	writeJSON(w, 200, map[string]any{"entity": e})
}

func (s *server) handleListIssuances(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	issuances, err := s.st.IssuancesByOwner(acct.AID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	writeJSON(w, 200, map[string]any{"issuances": issuances})
}

type issuanceReq struct {
	Name        string          `json:"name"`
	Ticker      string          `json:"ticker"`
	StructureID string          `json:"structure_id"`
	EntityID    string          `json:"entity_id"`
	Terms       json.RawMessage `json:"terms"`
}

func (s *server) handleCreateIssuance(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	var req issuanceReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Ticker = strings.ToUpper(strings.TrimSpace(req.Ticker))
	if req.Name == "" {
		writeErr(w, 400, "name is required")
		return
	}
	if err := validateTicker(req.Ticker); err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	if req.EntityID != "" {
		e, err := s.st.EntityByID(req.EntityID)
		if err != nil {
			writeErr(w, 500, "store error")
			return
		}
		if e == nil {
			writeErr(w, 404, "unknown entity")
			return
		}
		if e.OwnerAID != acct.AID {
			s.st.Audit(acct.AID, "issuance.create.refused", map[string]any{"reason": "entity not owned", "entity_id": req.EntityID})
			writeErr(w, 403, "that entity belongs to another account")
			return
		}
	}
	id, err := randHex(16)
	if err != nil {
		writeErr(w, 500, "id generation failed")
		return
	}
	now := time.Now().Unix()
	i := &Issuance{
		ID:          id,
		OwnerAID:    acct.AID,
		EntityID:    req.EntityID,
		Name:        req.Name,
		Ticker:      req.Ticker,
		StructureID: strings.TrimSpace(req.StructureID),
		Status:      "draft",
		Terms:       rawOrEmpty(req.Terms),
		Clawback:    true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.st.CreateIssuance(i); err != nil {
		writeErr(w, 500, "could not create the issuance")
		return
	}
	s.st.Audit(acct.AID, "issuance.create", map[string]any{"issuance_id": i.ID, "ticker": i.Ticker, "name": i.Name})
	writeJSON(w, 200, map[string]any{"issuance": i})
}

// patchIssuanceReq is a partial update: only non-nil fields are applied, and
// only fields a user may set (asset_id, txid, status and the rest of the
// deployment record are written by the deploy path alone).
type patchIssuanceReq struct {
	Name        *string          `json:"name"`
	Ticker      *string          `json:"ticker"`
	StructureID *string          `json:"structure_id"`
	EntityID    *string          `json:"entity_id"`
	Terms       *json.RawMessage `json:"terms"`
	Supply      *uint64          `json:"supply"`
	Precision   *int             `json:"precision"`
	Clawback    *bool            `json:"clawback"`
	// No confidential field: confidentiality is a per-transfer choice, not an
	// issuance property. A legacy body carrying it is ignored, not refused.
}

func (s *server) handlePatchIssuance(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	id := r.PathValue("id")
	iss := s.ownedIssuance(w, acct, id)
	if iss == nil {
		return
	}
	if iss.Status == "live" {
		writeErr(w, 409, "this issuance is deployed on chain and can no longer be edited")
		return
	}
	var req patchIssuanceReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	fields := map[string]any{}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			writeErr(w, 400, "name cannot be empty")
			return
		}
		fields["name"] = name
	}
	if req.Ticker != nil {
		ticker := strings.ToUpper(strings.TrimSpace(*req.Ticker))
		if err := validateTicker(ticker); err != nil {
			writeErr(w, 400, "%v", err)
			return
		}
		fields["ticker"] = ticker
	}
	if req.StructureID != nil {
		fields["structure_id"] = strings.TrimSpace(*req.StructureID)
	}
	if req.EntityID != nil {
		if *req.EntityID == "" {
			fields["entity_id"] = nil
		} else {
			e, err := s.st.EntityByID(*req.EntityID)
			if err != nil {
				writeErr(w, 500, "store error")
				return
			}
			if e == nil {
				writeErr(w, 404, "unknown entity")
				return
			}
			if e.OwnerAID != acct.AID {
				writeErr(w, 403, "that entity belongs to another account")
				return
			}
			fields["entity_id"] = e.ID
		}
	}
	if req.Terms != nil {
		fields["terms"] = string(rawOrEmpty(*req.Terms))
	}
	if req.Supply != nil {
		if *req.Supply < 1 {
			writeErr(w, 400, "supply must be at least 1")
			return
		}
		fields["supply"] = *req.Supply
	}
	if req.Precision != nil {
		// 0 is a valid integer-only asset (nDenomination 0); only nil means "unchanged".
		if *req.Precision < 0 || *req.Precision > 8 {
			writeErr(w, 400, "precision must be between 0 and 8")
			return
		}
		fields["precision"] = *req.Precision
	}
	if req.Clawback != nil {
		fields["clawback"] = boolInt(*req.Clawback)
	}
	if err := s.st.UpdateIssuanceFields(id, fields); err != nil {
		writeErr(w, 500, "could not update the issuance")
		return
	}
	updated, err := s.st.IssuanceByID(id)
	if err != nil || updated == nil {
		writeErr(w, 500, "store error")
		return
	}
	s.st.Audit(acct.AID, "issuance.update", map[string]any{"issuance_id": id, "fields": sortedKeys(fields)})
	writeJSON(w, 200, map[string]any{"issuance": updated})
}

// ownedIssuance loads an issuance and enforces that the session's AID owns it.
// It writes the error response itself and returns nil when the caller must stop.
func (s *server) ownedIssuance(w http.ResponseWriter, acct *Account, id string) *Issuance {
	iss, err := s.st.IssuanceByID(id)
	if err != nil {
		writeErr(w, 500, "store error")
		return nil
	}
	if iss == nil {
		writeErr(w, 404, "unknown issuance")
		return nil
	}
	if iss.OwnerAID != acct.AID {
		s.st.Audit(acct.AID, "issuance.access.refused", map[string]any{"issuance_id": id, "reason": "not owner"})
		writeErr(w, 403, "that issuance belongs to another account")
		return nil
	}
	return iss
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// The token probe is what makes this truthful: an unauthenticated reachability
	// check would report ok while every deployment would fail at the mint.
	openampOK := false
	if resp, err := s.http.Get(s.cfg.openampURL + "/v1/assets"); err == nil {
		openampOK = resp.StatusCode < 500
		resp.Body.Close()
	}
	issuerOK := false
	if s.cfg.issuerToken != "" && openampOK {
		req, err := http.NewRequest("GET", s.cfg.openampURL+"/v1/issuer/holders?asset=health-probe", nil)
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+s.cfg.issuerToken)
			if resp, err := s.http.Do(req); err == nil {
				// The probe asset does not exist, so a 404 means the token passed
				// issuer auth; 401 or 403 means it did not.
				issuerOK = resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden && resp.StatusCode < 500
				resp.Body.Close()
			}
		}
	}
	writeJSON(w, 200, map[string]any{
		"ok":      openampOK && issuerOK,
		"network": s.cfg.network,
		// The per-transfer confidentiality capability of THIS deployment: whether
		// a transfer with confidential:true would be accepted here rather than
		// refused with a 501. Deploys are always transparent mints.
		"confidential": s.cfg.confidential,
		"damp":         s.cfg.damp,
		// The setup fee is a real gate: an unpaid one refuses the deploy with a
		// 402. What it costs is configuration, and a screen that asserts an amount
		// (or asserts there is none) is wrong on any deployment configured
		// differently, which is how the onboarding step came to say the fee was
		// simulated while the README said it blocked the deploy. Both were reading
		// their own deployment.
		"setup_fee_usd":   s.cfg.setupFeeUSD,
		"openamp_ok":      openampOK,
		"issuer_token_ok": issuerOK,
	})
}
