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
		writeErr(w, 400, "a registered name is required to verify this identity")
		return
	}

	// A decision already made is not re-run. A refusal is the provider's, and
	// there is no reviewer here to appeal it to; a check already with the
	// provider is simply not finished. Only needs_info invites another
	// submission, which is what a provider asking for one means.
	prior, err := s.st.ClaimsByAID(acct.AID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if prior != nil {
		switch prior.Status {
		case "submitted":
			s.st.Audit(acct.AID, "id.verify.blocked", map[string]any{"status": prior.Status})
			writeErr(w, 409, "this identity is already with the verification provider. This page "+
				"shows the outcome when they decide")
			return
		case "refused":
			s.st.Audit(acct.AID, "id.verify.blocked", map[string]any{"status": prior.Status})
			writeErr(w, 409, "verification of this identity was refused by the verification "+
				"provider, and submitting again does not change that")
			return
		}
	}

	// Paid before submitted, because the provider bills for the check the moment
	// it is created. Nothing has been written yet at this point, so an unpaid
	// caller leaves exactly as they arrived. Answering a provider who asked for
	// more is the same check continuing, and costs nothing further.
	priorCheck, err := s.st.LatestVerificationCheck(acct.AID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	invoice, ok := s.requireVerificationFee(w, acct.AID, "identity", "",
		continuesAnOpenCheck(priorCheck))
	if !ok {
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
	if s.hasEnclave(acct) {
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
	// Recorded now, granted nothing yet: the claims exist so the provider's
	// decision has something to land on, and they carry no eligibility until it
	// does. projectCategories yields nothing for any status but "verified", so
	// every gate on this platform already refuses a submitted identity.
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
		Status:           "submitted",
	}
	if err := s.st.UpsertClaims(claims); err != nil {
		writeErr(w, 500, "store error")
		return
	}

	check, err := s.submitVerification(aid, "identity", name, "")
	if err != nil {
		// Nothing reached the provider, so nothing is with them. Left as
		// "submitted" this account would be stuck for good: submitting again is
		// refused as already open, and there is no check for the reconciler to
		// chase. Put back exactly what was there -- including a verification
		// this attempt would otherwise have thrown away.
		if prior != nil {
			_ = s.st.UpsertClaims(prior)
		} else {
			_ = s.st.DeleteClaims(aid)
		}
		s.st.Audit(aid, "id.verify.submit_failed", map[string]any{"error": err.Error()})
		writeErr(w, 502, "the verification provider could not be reached, and nothing was "+
			"submitted. Try again: %v", err)
		return
	}
	s.spendVerificationFee(invoice, check)
	s.st.Audit(aid, "id.verify.submitted", map[string]any{
		"check": check.ID, "provider": check.Provider,
	})
	writeJSON(w, 200, map[string]any{
		"status": "submitted", "aid": aid, "check_id": check.ID, "provider": check.Provider,
		"message": "your details are with the verification provider. Nothing is granted until " +
			"they decide, and this page shows the outcome when they do",
	})
}

// submitVerification records a check and hands it to the provider. The row is
// written BEFORE the provider is called, because a provider fast enough to
// answer first still needs something to answer about.
func (s *server) submitVerification(aid, kind, name, entityID string) (*VerificationCheck, error) {
	check := &VerificationCheck{
		ID: mustID(), AID: aid, Kind: kind, SubjectName: name, EntityID: entityID,
		Provider: s.idv.Name(), Status: "submitted", CreatedAt: time.Now().Unix(),
	}
	if err := s.st.InsertVerificationCheck(check); err != nil {
		return nil, err
	}
	ref, err := s.idv.CreateCheck(check)
	if err != nil {
		return nil, err
	}
	check.ProviderRef = ref
	if err := s.st.SetVerificationCheckRef(check.ID, ref); err != nil {
		return nil, err
	}
	return check, nil
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
	// Where the categories come from depends on what this account IS. An enclave
	// account carries them on the policy server, which is authoritative for it.
	// A wallet-backed one has no account there at all, so reading openampd would
	// report zero categories for a holder who is verified -- which is how the
	// passport came to show "Verified" and "Categories carried: 0" at once.
	var user openampUser
	if oaid := s.enclaveAIDOf(acct); oaid != "" {
		_ = s.callOpenAMP("GET", "/v1/users/"+oaid, "", nil, &user)
	} else {
		user.Categories = projectCategories(claims, time.Now().Unix())
	}
	latestCheck, _ := s.st.LatestVerificationCheck(acct.AID)

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
		}
		// Verified is the provider's word, not ours. The treasury key and the UBO
		// link both exist from the moment the check is SUBMITTED -- the control
		// statement names the treasury, so it cannot be signed before it exists --
		// and calling that verified would show a business as cleared while its
		// check is still with the provider, or after they refused it.
		check, _ := s.st.LatestVerificationCheckForEntity(e.ID)
		ent["verification"] = verificationView(check)
		ent["verified"] = check != nil && check.Result == string(idvClear)
		linked = append(linked, ent)
	}

	// Where the ID is accepted: which SeqPal-managed assets this holder is
	// eligible for right now (advisory), and which venues honor the credential.
	height := s.tipHeight()
	// These are OpenAMP restricted assets: eligibility for them is real and stays
	// real, but receiving one needs an enclave account. An ID without one clears
	// the rules and still cannot hold any of them, so the list says so rather
	// than reading as access it does not have.
	eligibleAssets := s.eligibleAssetsFor(user.Categories, user.Frozen, height)

	writeJSON(w, 200, map[string]any{
		// What kind of account this is, so the passport can stop calling a
		// wallet-backed id an enclave account id.
		"identity":    acct.Identity,
		"has_enclave": s.hasEnclave(acct),
		"aid":         acct.AID,
		// The account the POLICY SERVER knows this ID by. The same id for an ID
		// founded on an OpenAMP account, and a different one for an ID founded as
		// a wallet that attached one later -- so a holder quoting "their AID" to a
		// venue needs to be shown which is which, not one labelled as the other.
		"enclave_aid": s.enclaveAIDOf(acct),
		"enclave_key": acct.XOnly,
		"status":      status,
		"categories":  cats,
		"valid_until": validUntil,
		// Where this identity's verification stands, and who decided it. The
		// platform used to report which sanctions lists IT had screened against;
		// it screens against none, because that is the provider's work.
		"verification": verificationView(latestCheck),
		"frozen":       user.Frozen,
		"entities":     linked,
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

// ownedEntity resolves one of the caller's own businesses, writing the refusal
// itself if it is missing or somebody else's.
func (s *server) ownedEntity(w http.ResponseWriter, acct *Account, id string) *Entity {
	e, err := s.st.EntityByID(id)
	if err != nil {
		writeErr(w, 500, "store error")
		return nil
	}
	if e == nil {
		writeErr(w, 404, "unknown entity")
		return nil
	}
	if e.OwnerAID != acct.AID {
		s.st.Audit(acct.AID, "entity.access.refused", map[string]any{"entity_id": id, "reason": "not owner"})
		writeErr(w, 403, "that entity belongs to another account")
		return nil
	}
	return e
}

func (s *server) handleEntityVerify(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	e := s.ownedEntity(w, acct, r.PathValue("id"))
	if e == nil {
		return
	}
	id := e.ID
	var req entityVerifyReq
	_ = readJSON(r, &req)

	// The person doing this is the entity's controller, and approving a KYB
	// provisions a treasury enclave. An identity that is parked for review or
	// refused could otherwise route straight around its own refusal: make a
	// company, verify the company, hold assets in the company's treasury.
	controller, err := s.st.ClaimsByAID(acct.AID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if !eligibilityLive(controller, time.Now().Unix()) {
		s.st.Audit(acct.AID, "entity.verify.refused", map[string]any{
			"entity_id": id, "reason": "the controlling identity is not verified",
		})
		writeErr(w, 403, "an entity is verified by the person who controls it, and this SeqPal ID "+
			"is not verified. Verify your own identity first")
		return
	}

	// Where this entity's own check stands decides what this call is. A refusal
	// is the provider's and it is final, exactly as for a person. A check still
	// open is not re-sent: this endpoint doubles as where the UBO signature is
	// recorded, and signing it must not buy a second check.
	priorCheck, err := s.st.LatestVerificationCheckForEntity(e.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if priorCheck != nil && priorCheck.Status == "complete" && priorCheck.Result == string(idvReject) {
		s.st.Audit(acct.AID, "entity.verify.blocked", map[string]any{
			"entity_id": id, "reason": "refused by the provider",
		})
		writeErr(w, 409, "verification of this business was refused by the verification "+
			"provider, and submitting again does not change that")
		return
	}

	// Paid before anything is provisioned, for the same reason as an identity
	// check: the provider bills per business.
	invoice, ok := s.requireVerificationFee(w, acct.AID, "business", e.ID,
		priorCheck != nil && (priorCheck.Status == "submitted" || continuesAnOpenCheck(priorCheck)))
	if !ok {
		return
	}

	// The entity treasury: its own key (custodied by seqpald) registered with
	// openampd, and the AID that goes into rules.primary_aids for the entity's
	// offerings. Provisioned now rather than on approval, because the UBO
	// statement below NAMES it and cannot be signed before it exists. A key is
	// not an approval: the entity counts as verified only when the provider says
	// so, and an entity they refuse is left holding nothing but an unused key.
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
		if err := s.verifyAccountStatement(acct, "seqpal-ubo-v1", []byte(statement), link.Sig); err != nil {
			writeErr(w, 400, "the UBO signature does not verify for this identity's key")
			return
		}
	}
	if err := s.st.UpsertUBOLink(link); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	// Business verification is the provider's too, and asynchronous for the same
	// reason: they run the checks and tell us. Nothing about this entity counts
	// as verified until they do.
	// A check already with the provider is left with them: this call is here to
	// record the UBO signature, not to buy another check.
	if priorCheck != nil && priorCheck.Status == "submitted" {
		s.st.Audit(acct.AID, "entity.ubo.recorded", map[string]any{
			"entity_id": e.ID, "check": priorCheck.ID, "ubo_signed": link.Sig != "",
		})
		writeJSON(w, 200, map[string]any{
			"entity": e, "treasury_aid": treasury.AID, "ubo_link": link,
			"status": "submitted", "check_id": priorCheck.ID, "provider": priorCheck.Provider,
			"message": "this entity is already with the verification provider; nothing was " +
				"submitted again and nothing further was charged",
		})
		return
	}

	check, err := s.submitVerification(acct.AID, "business", e.Name, e.ID)
	if err != nil {
		s.st.Audit(acct.AID, "entity.verify.submit_failed", map[string]any{
			"entity_id": e.ID, "error": err.Error(),
		})
		writeErr(w, 502, "the verification provider could not be reached: %v", err)
		return
	}
	s.spendVerificationFee(invoice, check)
	s.st.Audit(acct.AID, "entity.verify.submitted", map[string]any{
		"entity_id": e.ID, "check": check.ID, "provider": check.Provider,
		"treasury_aid": treasury.AID, "ubo_signed": link.Sig != "",
	})
	writeJSON(w, 200, map[string]any{
		"entity": e, "treasury_aid": treasury.AID, "ubo_link": link,
		"status": "submitted", "check_id": check.ID, "provider": check.Provider,
		"message": "this entity is with the verification provider. Its treasury key exists so the " +
			"control statement could be signed, but nothing is verified until they decide",
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
	if err := s.callOpenAMP("GET", "/v1/users/"+s.openampAIDFor(aid), "", nil, &user); err != nil {
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
