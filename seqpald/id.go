package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	identityValidity   = 365 * 24 * time.Hour // a verified identity is good for a year
	accreditedValidity = 90 * 24 * time.Hour  // 506(c) accreditation staleness
	expiryWarnWindow   = 14 * 24 * time.Hour  // notify this long before expiry
)

// --- POST /id/verify ---------------------------------------------------------

type verifyReq struct {
	Residence       string          `json:"residence"`
	BaseEligibility string          `json:"base_eligibility"` // ret | pro
	Accredited      bool            `json:"accredited"`
	AccredArtifact  string          `json:"accred_artifact"` // sha256 hex of the (simulated) artifact
	USPerson        *bool           `json:"us_person"`       // explicit determination; derived if absent
	Citizenship     string          `json:"citizenship"`
	GBHNW           bool            `json:"gb_hnw"`
	GBSoph          bool            `json:"gb_soph"`
	TaxResidencies  json.RawMessage `json:"tax_residencies"` // CRS/FATCA self-certification
	ScreeningName   string          `json:"screening_name"`  // optional override of the registered name
}

func (s *server) handleIDVerify(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	var req verifyReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	res := normalizeResidence(req.Residence)
	if res == "" {
		writeErr(w, 400, "residence must be a two-letter country code (or HN-PRO)")
		return
	}
	name := strings.TrimSpace(req.ScreeningName)
	if name == "" {
		name = acct.DisplayName
	}
	if name == "" {
		writeErr(w, 400, "a registered name is required to screen this identity")
		return
	}

	// The investor must be a registered openampd user before any category can be
	// stamped; the server-recomputed AID must equal the local AID or we would be
	// stamping an account we do not control.
	//
	// A wallet-backed ID has no enclave key to register and no enclave account to
	// stamp, and does not need one: what it is verified FOR -- freely-tradable
	// stocks, network-enforced assets, the distributions attached to them -- is
	// not gated on the policy server. Its eligibility is recorded here and
	// pushed the moment an enclave is attached (handleAttachEnclave).
	aid := acct.AID
	if acct.HasEnclave() {
		registered, err := s.registerUser(acct.XOnly)
		if err != nil {
			writeErr(w, 502, "register with the policy server: %v", err)
			return
		}
		if registered != acct.AID {
			writeErr(w, 502, "the policy server returned an unexpected account id for this key")
			return
		}
		aid = registered
	}

	// Real sanctions screening against all four public lists.
	results := s.screen.Screen(name)
	var hits []ScreeningResult
	for _, rslt := range results {
		if err := s.st.UpsertScreening(aid, rslt); err != nil {
			writeErr(w, 500, "store error")
			return
		}
		if rslt.Matched {
			hits = append(hits, rslt)
		}
	}

	now := time.Now()
	usPerson := res == "US" || strings.EqualFold(strings.TrimSpace(req.Citizenship), "US")
	if req.USPerson != nil {
		usPerson = *req.USPerson
	}
	base := req.BaseEligibility
	if base != "pro" {
		base = "ret"
	}
	accredValid := int64(0)
	if req.Accredited && req.AccredArtifact != "" {
		accredValid = now.Add(accreditedValidity).Unix()
	}
	claims := &Claims{
		AID:              aid,
		Residence:        res,
		BaseEligibility:  base,
		Accredited:       req.Accredited,
		AccredArtifact:   strings.TrimSpace(req.AccredArtifact),
		AccredValidUntil: accredValid,
		USPerson:         usPerson,
		GBHNW:            req.GBHNW && res == "GB",
		GBSoph:           req.GBSoph && res == "GB",
		TaxResidencies:   req.TaxResidencies,
		ValidUntil:       now.Add(identityValidity).Unix(),
		VocabVersion:     vocabVersion,
	}

	// A sanctions hit does NOT auto-refuse: it parks the ID in review and stamps
	// nothing. The SIMULATED auto-reviewer (or a human on the admin page) resolves it.
	if len(hits) > 0 {
		claims.Status = "pending_review"
		if err := s.st.UpsertClaims(claims); err != nil {
			writeErr(w, 500, "store error")
			return
		}
		for _, h := range hits {
			id, _ := randHex(12)
			_ = s.st.InsertReview(&ReviewItem{
				ID: id, AID: aid, List: h.List, MatchedEntry: h.MatchedEntry,
				State: "pending", Disposition: s.screen.disposition(h.List, h.MatchedEntry),
				CreatedAt: now.Unix(),
			})
		}
		s.st.Audit(aid, "id.verify.pending_review", map[string]any{"lists": listsOf(hits)})
		writeJSON(w, 200, map[string]any{
			"status": "pending_review", "aid": aid, "screening": results,
			"message": "a sanctions-list name match was found; the identity is in manual review and no eligibility has been granted",
		})
		return
	}

	// No sanctions hit: run the labeled-simulated document/selfie review.
	switch personaDisposition(name) {
	case "reject":
		claims.Status = "refused"
		_ = s.st.UpsertClaims(claims)
		s.st.Audit(aid, "id.verify.refused", map[string]any{"reason": "document review rejected (SIMULATED)"})
		writeJSON(w, 200, map[string]any{"status": "refused", "aid": aid,
			"message": "document review was rejected (SIMULATED review)"})
		return
	case "needs_info":
		claims.Status = "needs_info"
		_ = s.st.UpsertClaims(claims)
		s.st.Audit(aid, "id.verify.needs_info", map[string]any{})
		writeJSON(w, 200, map[string]any{"status": "needs_info", "aid": aid,
			"message": "additional information is required to complete the review (SIMULATED review)"})
		return
	}

	claims.Status = "verified"
	claims.VerifiedAt = now.Unix()
	sig, err := s.signClaims(claims)
	if err != nil {
		writeErr(w, 500, "could not sign the claims record")
		return
	}
	claims.ClaimsSig = sig
	if err := s.st.UpsertClaims(claims); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	var cats []string
	if acct.HasEnclave() {
		cats, err = s.writeCategories(aid)
		if err != nil {
			s.st.Audit(aid, "id.verify.stamp_failed", map[string]any{"error": err.Error()})
			writeErr(w, 502, "categories could not be stamped on the policy server: %v", err)
			return
		}
	} else {
		// Projected the same way, just not pushed anywhere yet.
		cats = projectCategories(claims, time.Now().Unix())
		if cats == nil {
			cats = []string{}
		}
	}
	s.st.Audit(aid, "id.verify.approved", map[string]any{"categories": cats})
	writeJSON(w, 200, map[string]any{
		"status": "verified", "aid": aid, "categories": cats,
		"valid_until": claims.ValidUntil, "screening": results,
	})
}

// personaDisposition is the SIMULATED document-review outcome, made deterministic
// by name so demos and tests run unattended. The default is approval; the refusal
// personas exist so the rejected/needs-info states are demonstrable.
func personaDisposition(name string) string {
	u := strings.ToUpper(name)
	switch {
	case strings.Contains(u, "REVIEW-REJECT"):
		return "reject"
	case strings.Contains(u, "REVIEW-NEEDSINFO"):
		return "needs_info"
	default:
		return "approve"
	}
}

// signClaims produces the seqpald-signed claims record. The signature is over the
// canonical {aid, residence, eligibility, verifiedAt, valid_until} tuple that
// drives category assignment, using the platform claims-signing key.
func (s *server) signClaims(c *Claims) (string, error) {
	pk, err := s.st.PlatformKey("claims-signer")
	if err != nil {
		return "", err
	}
	body, err := canonicalJSON(map[string]any{
		"aid": c.AID, "residence": c.Residence, "eligibility": c.BaseEligibility,
		"accredited": c.Accredited, "us_person": c.USPerson,
		"verified_at": c.VerifiedAt, "valid_until": c.ValidUntil, "vocab_version": c.VocabVersion,
	})
	if err != nil {
		return "", err
	}
	return signTagged(pk.Priv, claimsTag, body)
}

// --- GET /id/passport --------------------------------------------------------

func (s *server) handleIDPassport(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	claims, err := s.st.ClaimsByAID(acct.AID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	var user openampUser
	_ = s.callOpenAMP("GET", "/v1/users/"+acct.AID, "", nil, &user)
	screening, _ := s.st.ScreeningByAID(acct.AID)

	now := time.Now().Unix()
	validUntil := int64(0)
	status := "unverified"
	if claims != nil {
		validUntil = claims.ValidUntil
		status = claims.Status
	}
	cats := make([]map[string]any, 0, len(user.Categories))
	for _, c := range user.Categories {
		// An accreditation category (j:*:acc) carries the accreditation validity
		// (shorter, 90 days), not the identity validity, so the passport never
		// overstates how long an accredited-only eligibility remains valid. This
		// mirrors projectCategories, which drops the acc token once it lapses.
		cv := validUntil
		if claims != nil && claims.AccredValidUntil > 0 && strings.HasSuffix(c, ":acc") {
			cv = claims.AccredValidUntil
		}
		expiring := cv > 0 && cv-now < int64(expiryWarnWindow.Seconds()) && cv > now
		cats = append(cats, map[string]any{"token": c, "valid_until": cv, "expiring_soon": expiring})
	}

	// Linked corporate entities and their treasury AIDs.
	entities, _ := s.st.EntitiesByOwner(acct.AID)
	linked := make([]map[string]any, 0, len(entities))
	for _, e := range entities {
		ent := map[string]any{"id": e.ID, "name": e.Name, "jurisdiction": e.Jurisdiction}
		if link, _ := s.st.UBOLinkByEntity(e.ID); link != nil {
			ent["treasury_aid"] = link.TreasuryAID
			ent["ubo_signed"] = link.Sig != ""
			ent["verified"] = true
		}
		linked = append(linked, ent)
	}

	// Where the ID is accepted: which SeqPal-managed assets this holder is
	// eligible for right now (advisory), and which venues honor the credential.
	height := s.tipHeight()
	eligibleAssets := s.eligibleAssetsFor(user.Categories, user.Frozen, height)

	writeJSON(w, 200, map[string]any{
		"aid":            acct.AID,
		"enclave_key":    acct.XOnly,
		"status":         status,
		"categories":     cats,
		"valid_until":    validUntil,
		"screening":      screening,
		"lists_screened": sanctionsLists,
		"frozen":         user.Frozen,
		"entities":       linked,
		"accepted": map[string]any{
			"assets": eligibleAssets,
			"venues": honoringVenues(),
		},
	})
}

// honoringVenues names the Sequentia surfaces that consume SeqPal ID. A venue
// can only CHECK what SeqPal stamped; it can never grant eligibility.
func honoringVenues() []map[string]string {
	return []map[string]string{
		{"name": "Sequentia web wallet", "role": "checks eligibility on receive and transfer"},
		{"name": "SeqDEX", "role": "checks eligibility before it lists or fills SeqPal assets"},
	}
}

// --- POST /id/entities/{id}/verify -------------------------------------------

type entityVerifyReq struct {
	UBOSig     string          `json:"ubo_sig"`     // optional BIP340 signature by the personal enclave key
	KYBProfile json.RawMessage `json:"kyb_profile"` // simulated KYB questionnaire payload
}

func (s *server) handleEntityVerify(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	id := r.PathValue("id")
	e, err := s.st.EntityByID(id)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if e == nil {
		writeErr(w, 404, "unknown entity")
		return
	}
	if e.OwnerAID != acct.AID {
		s.st.Audit(acct.AID, "entity.verify.refused", map[string]any{"entity_id": id, "reason": "not owner"})
		writeErr(w, 403, "that entity belongs to another account")
		return
	}
	var req entityVerifyReq
	_ = readJSON(r, &req)

	// KYB approval mints the entity a treasury enclave: its own key (custodied by
	// seqpald) registered with openampd. This is the entity treasury AID that goes
	// into rules.primary_aids for the entity's offerings.
	treasury, err := s.createEnclave(enclaveEntityTreasury, e.ID)
	if err != nil {
		writeErr(w, 502, "provision the entity treasury: %v", err)
		return
	}

	// The UBO link binds the corporate entity to the controlling personal ID.
	// The statement is signed by the personal enclave key in the browser; the
	// signature is optional at this step and recorded when present.
	statement := fmt.Sprintf("SeqPal KYB: entity %s (treasury %s) is controlled by SeqPal ID %s", e.ID, treasury.AID, acct.AID)
	link := &UBOLink{
		EntityID: e.ID, UBOAID: acct.AID, TreasuryAID: treasury.AID,
		Statement: statement, Sig: strings.TrimSpace(req.UBOSig), CreatedAt: time.Now().Unix(),
	}
	if link.Sig != "" {
		if err := verifyTaggedByKey(acct.XOnly, "seqpal-ubo-v1", []byte(statement), link.Sig); err != nil {
			writeErr(w, 400, "the UBO signature does not verify for this identity's key")
			return
		}
	}
	if err := s.st.UpsertUBOLink(link); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	s.st.Audit(acct.AID, "entity.verify.approved", map[string]any{
		"entity_id": e.ID, "treasury_aid": treasury.AID, "ubo_signed": link.Sig != "",
	})
	writeJSON(w, 200, map[string]any{
		"entity": e, "treasury_aid": treasury.AID, "ubo_link": link,
		"message": "entity verified (SIMULATED KYB); a treasury enclave was provisioned",
	})
}

// --- GET /eligibility (public) -----------------------------------------------

func (s *server) handleEligibility(w http.ResponseWriter, r *http.Request) {
	aid := strings.TrimSpace(r.URL.Query().Get("aid"))
	asset := strings.TrimSpace(r.URL.Query().Get("asset"))
	if aid == "" || asset == "" {
		writeErr(w, 400, "aid and asset query parameters are required")
		return
	}
	// A token whose rules the network enforces has no category stamps to evaluate:
	// who may hold it is the PUBLISHED HOLDER LIST, which the chain itself checks on
	// every transfer. Answering from the category rules here would report an
	// eligibility that no transfer respects, in either direction, so this branch
	// reads the published list instead.
	if iss, _ := s.st.IssuanceByAsset(asset); networkEnforced(iss) {
		s.eligibilityFromPublishedList(w, aid, asset)
		return
	}
	var user openampUser
	if err := s.callOpenAMP("GET", "/v1/users/"+aid, "", nil, &user); err != nil {
		writeJSON(w, 200, map[string]any{"aid": aid, "asset": asset, "eligible": false,
			"reasons": []string{"this AID is not registered with the policy server"}})
		return
	}
	rules, ok, err := s.assetRules(asset)
	if err != nil {
		writeErr(w, 502, "could not read the asset rules: %v", err)
		return
	}
	if !ok {
		writeJSON(w, 200, map[string]any{"aid": aid, "asset": asset, "eligible": false,
			"reasons": []string{"unknown asset"}})
		return
	}
	eligible, reasons := evalEligibility(user.Categories, user.Frozen, rules, s.tipHeight())
	writeJSON(w, 200, map[string]any{"aid": aid, "asset": asset, "eligible": eligible, "reasons": reasons})
}

// --- POST /issuances/{id}/compile --------------------------------------------

type compileReq struct {
	Terms json.RawMessage `json:"terms"` // optional preview override of the stored matrix
}

func (s *server) handleCompile(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	id := r.PathValue("id")
	iss := s.ownedIssuance(w, acct, id)
	if iss == nil {
		return
	}
	var req compileReq
	_ = readJSON(r, &req)
	terms := iss.Terms
	if len(req.Terms) > 0 {
		terms = req.Terms
	}
	rules, err := s.compileForIssuance(iss, terms)
	if err != nil {
		writeErr(w, 400, "the matrix could not be compiled: %v", err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"rules": rules, "tip_height": s.tipHeight(), "blocks_per_day": s.cfg.blocksPerDay,
	})
}

// compileForIssuance builds the compile env (chain tip, primary AIDs already
// provisioned for this offering) and runs the pure compiler.
func (s *server) compileForIssuance(iss *Issuance, terms json.RawMessage) (CompiledRules, error) {
	// Fail closed (W-7): an unrecognized structure cannot be compiled, because
	// the characterization decides the marketing clamp the rules encode.
	ch, err := characterize(structureName(iss, terms))
	if err != nil {
		return CompiledRules{}, err
	}
	env := compileEnv{
		TipHeight:        s.tipHeight(),
		BlocksPerDay:     s.cfg.blocksPerDay,
		PrimaryAIDs:      s.primaryAIDsFor(iss),
		Characterization: ch,
	}
	return compileRules(terms, env)
}

// structureName resolves the structure to classify: terms.structure takes
// precedence (it is what the compiler already reads for velocity), falling back
// to the issuance's StructureID. An empty result characterizes as plain equity.
func structureName(iss *Issuance, terms json.RawMessage) string {
	if name := termsStructureName(terms); name != "" {
		return name
	}
	if iss != nil {
		return iss.StructureID
	}
	return ""
}

// termsStructureName pulls terms.structure whether it is a bare string or an
// object with a name field.
func termsStructureName(terms json.RawMessage) string {
	if len(terms) == 0 {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(terms, &m); err != nil {
		return ""
	}
	raw, ok := m["structure"]
	if !ok {
		return ""
	}
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		return name
	}
	var obj struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj.Name
	}
	return ""
}

// primaryAIDsFor returns the escrow and entity-treasury AIDs for an issuance, if
// they have been provisioned (they are at deploy). For a pre-deploy preview the
// list may be empty.
func (s *server) primaryAIDsFor(iss *Issuance) []string {
	var out []string
	if k, _ := s.st.EnclaveKeyByRef(enclaveOfferingEscrow, iss.ID); k != nil {
		out = append(out, k.AID)
	}
	if iss.EntityID != "" {
		if k, _ := s.st.EnclaveKeyByRef(enclaveEntityTreasury, iss.EntityID); k != nil {
			out = append(out, k.AID)
		}
	}
	return out
}

// --- eligibility evaluation --------------------------------------------------

// evalEligibility is the advisory preflight: recipient-side rules only (the ones
// that decide whether an AID may HOLD the asset). Holder caps and lockups depend
// on chain state and the sender and are not decided here; category gating and
// the Reg S receive-window are.
func evalEligibility(cats []string, frozen bool, rules CompiledRules, height int64) (bool, []string) {
	reasons := []string{}
	if frozen {
		reasons = append(reasons, "the account is frozen")
	}
	if len(rules.AllowedCategories) > 0 && !anyIn(cats, rules.AllowedCategories) {
		reasons = append(reasons, "holds no eligibility category permitted by this asset")
	}
	for _, d := range rules.CategoryDenies {
		if height >= d.UntilHeight {
			continue
		}
		for _, c := range cats {
			if strings.HasPrefix(c, d.Prefix) {
				reasons = append(reasons, fmt.Sprintf("category %s is in a distribution-compliance window until Sequentia block %d", c, d.UntilHeight))
			}
		}
	}
	return len(reasons) == 0, reasons
}

func (s *server) eligibleAssetsFor(cats []string, frozen bool, height int64) []map[string]any {
	var out struct {
		Assets []struct {
			ID     string        `json:"id"`
			Ticker string        `json:"ticker"`
			Name   string        `json:"name"`
			Rules  CompiledRules `json:"rules"`
		} `json:"assets"`
	}
	if err := s.callOpenAMP("GET", "/v1/assets", "", nil, &out); err != nil {
		return []map[string]any{}
	}
	res := []map[string]any{}
	for _, a := range out.Assets {
		eligible, reasons := evalEligibility(cats, frozen, a.Rules, height)
		res = append(res, map[string]any{
			"asset": a.ID, "ticker": a.Ticker, "name": a.Name,
			"eligible": eligible, "reasons": reasons,
		})
	}
	return res
}

// assetRules fetches an asset's compiled rules from openampd.
func (s *server) assetRules(asset string) (CompiledRules, bool, error) {
	var out struct {
		ID    string        `json:"id"`
		Rules CompiledRules `json:"rules"`
	}
	err := s.callOpenAMP("GET", "/v1/assets/"+asset, "", nil, &out)
	if err != nil {
		if strings.Contains(err.Error(), "unknown asset") {
			return CompiledRules{}, false, nil
		}
		return CompiledRules{}, false, err
	}
	if out.ID == "" {
		return CompiledRules{}, false, nil
	}
	return out.Rules, true, nil
}

func anyIn(have, want []string) bool {
	set := map[string]bool{}
	for _, w := range want {
		set[w] = true
	}
	for _, h := range have {
		if set[h] {
			return true
		}
	}
	return false
}

func listsOf(hits []ScreeningResult) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.List)
	}
	return out
}
