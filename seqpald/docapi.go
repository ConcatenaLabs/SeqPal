package main

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const documentTag = "openamp-document-v1"

// sessionAccountFromRequest resolves the account behind the session cookie, or
// nil when there is none. Unlike requireSession it never writes an error, so a
// public endpoint can offer gated content to a signed-in caller and a public
// fallback to everyone else.
func (s *server) sessionAccountFromRequest(r *http.Request) *Account {
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

// --- GET /api/terms/{hash} (public: the manifest) ---------------------------

func (s *server) handleTerms(w http.ResponseWriter, r *http.Request) {
	hash := strings.ToLower(strings.TrimSpace(r.PathValue("hash")))
	if !isHex64(hash) {
		writeErr(w, 400, "terms hash must be 64 hex characters")
		return
	}
	td, err := s.st.TermsDocByHash(hash)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if td == nil {
		writeErr(w, 404, "no terms document is published for this hash")
		return
	}
	var manifest any
	if td.Manifest != "" {
		_ = json.Unmarshal([]byte(td.Manifest), &manifest)
	}
	var termsObj any
	_ = json.Unmarshal([]byte(td.CanonicalTerms), &termsObj)

	offerOpen := true
	closeHeight := int64(0)
	if o, _ := s.st.OfferingByIssuance(td.IssuanceID); o != nil {
		offerOpen = o.OfferOpen
		closeHeight = o.CloseHeight
	}
	resp := map[string]any{
		"terms_hash":    td.TermsHash,
		"manifest_hash": td.ManifestHash,
		"manifest":      manifest,
		"terms":         termsObj,
		"access": map[string]any{
			"model":        "The hash commitment is verifiable immediately; document preimages are gate-passers-only during the offer window and public at offer close.",
			"offer_open":   offerOpen,
			"close_height": closeHeight,
		},
		"verify": map[string]any{
			"steps": []string{
				"canonicalize the terms object (sorted keys, no whitespace) and sha256 it to reproduce terms_hash",
				"the documents manifest inside terms binds the document set; sha256 the canonical manifest set to reproduce manifest_hash",
				"terms_hash equals the terms_hash committed inside the on-chain contract (contract_hash)",
			},
		},
	}
	// Issuance facts, best-effort (the manifest is public even pre-deploy).
	if iss, _ := s.st.IssuanceByID(td.IssuanceID); iss != nil {
		resp["issuance"] = map[string]any{
			"ticker": iss.Ticker, "name": iss.Name, "asset_id": iss.AssetID,
			"contract_hash": iss.ContractHash, "structure": iss.StructureID, "status": iss.Status,
		}
	}
	writeJSON(w, 200, resp)
}

// --- GET /api/doc/{hash} (public with offer-window gating) ------------------

func (s *server) handleDoc(w http.ResponseWriter, r *http.Request) {
	hash := strings.ToLower(strings.TrimSpace(r.PathValue("hash")))
	if !isHex64(hash) {
		writeErr(w, 400, "document hash must be 64 hex characters")
		return
	}
	doc, err := s.st.DocumentByHash(hash)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if doc == nil {
		writeErr(w, 404, "no such document")
		return
	}

	// Offer-window gating. A document that is always public (a rules amendment)
	// or whose offering has closed is served to anyone. During the offer window a
	// preimage is served only to a session that passed the promotion gate, is the
	// issuer, is a signer, or is a holder. An anonymous request during the window
	// returns a non-200, which is the standing probe.
	if !doc.AlwaysPublic {
		offerOpen := true
		if o, _ := s.st.OfferingByIssuance(doc.IssuanceID); o != nil {
			offerOpen = o.OfferOpen
		}
		if offerOpen {
			acct := s.sessionAccountFromRequest(r)
			if acct == nil {
				writeErr(w, 401, "this offering is in its offer window; document previews require a verified SeqPal ID that has passed the promotion gate")
				return
			}
			if !s.docAccessAllowed(acct, doc) {
				writeErr(w, 403, "your SeqPal ID has not passed the promotion gate for this offering")
				return
			}
		}
	}

	if strings.EqualFold(r.URL.Query().Get("format"), "pdf") {
		if pdf, ok := s.documentPDF(doc); ok {
			w.Header().Set("Content-Type", "application/pdf")
			w.Header().Set("Content-Disposition", "inline; filename=\""+doc.Kind+".pdf\"")
			w.WriteHeader(200)
			w.Write(pdf)
			return
		}
		// No PDF toolchain: fall through to HTML with a documented note header.
		w.Header().Set("X-PDF-Unavailable", "soffice --headless --convert-to pdf not available on this deployment; serving canonical HTML")
	}
	w.Header().Set("Content-Type", doc.ContentType)
	w.WriteHeader(200)
	w.Write(doc.Body)
}

// docAccessAllowed decides offer-window access for a signed-in caller. Issuer,
// signer, gate-passer (a verified identity), and holder all retain access.
func (s *server) docAccessAllowed(acct *Account, doc *StoredDoc) bool {
	iss, _ := s.st.IssuanceByID(doc.IssuanceID)
	if iss != nil && iss.OwnerAID == acct.AID {
		return true
	}
	if ok, _ := s.st.IsSigner(doc.DocHash, acct.AID); ok {
		return true
	}
	if s.passedPromotionGate(acct.AID) {
		return true
	}
	if iss != nil && iss.AssetID != "" && s.isHolder(acct.AID, iss.AssetID) {
		return true
	}
	return false
}

// passedPromotionGate reports whether an AID holds a verified SeqPal ID, which is
// the eligible/attested state the gate requires.
func (s *server) passedPromotionGate(aid string) bool {
	c, err := s.st.ClaimsByAID(aid)
	return err == nil && c != nil && c.Status == "verified"
}

// isHolder is a best-effort check that an AID appears in the asset's holder
// register (holders always retain document access). It tolerates any error by
// returning false; the other access paths still apply.
func (s *server) isHolder(aid, asset string) bool {
	if s.cfg.issuerToken == "" {
		return false
	}
	var raw json.RawMessage
	if err := s.callOpenAMP("GET", "/v1/issuer/holders?asset="+asset, s.cfg.issuerToken, nil, &raw); err != nil {
		return false
	}
	return strings.Contains(string(raw), aid)
}

// documentPDF returns the cached PDF, rendering it lazily with the house soffice
// toolchain on first request. The content address is over the HTML, never the
// PDF, so lazy rendering never changes a hash.
func (s *server) documentPDF(doc *StoredDoc) ([]byte, bool) {
	if len(doc.PDF) > 0 {
		return doc.PDF, true
	}
	pdf, ok := renderHTMLToPDF(doc.Body)
	if !ok {
		return nil, false
	}
	_ = s.st.SetDocumentPDF(doc.DocHash, pdf)
	return pdf, true
}

// renderHTMLToPDF invokes `soffice --headless --convert-to pdf` when it is on the
// PATH. It returns ok=false when the toolchain is absent, in which case callers
// serve canonical HTML and document the PDF step.
func renderHTMLToPDF(htmlBytes []byte) ([]byte, bool) {
	bin, err := exec.LookPath("soffice")
	if err != nil {
		return nil, false
	}
	dir, err := os.MkdirTemp("", "seqpal-doc-")
	if err != nil {
		return nil, false
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "doc.html")
	if err := os.WriteFile(in, htmlBytes, 0o600); err != nil {
		return nil, false
	}
	cmd := exec.Command(bin, "--headless", "--convert-to", "pdf", "--outdir", dir, in)
	cmd.Env = append(os.Environ(), "HOME="+dir)
	if err := cmd.Run(); err != nil {
		return nil, false
	}
	out, err := os.ReadFile(filepath.Join(dir, "doc.pdf"))
	if err != nil || len(out) == 0 {
		return nil, false
	}
	return out, true
}

// --- POST /api/issuances/{id}/documents (session, owner) --------------------

func (s *server) handleGenerateDocuments(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	bound, man, err := s.ensureDocuments(iss, iss.Terms)
	if err != nil {
		writeErr(w, 400, "documents could not be generated: %v", err)
		return
	}
	var termsObj any
	if err := json.Unmarshal(bound, &termsObj); err != nil {
		writeErr(w, 500, "internal error")
		return
	}
	canonical, err := canonicalJSON(termsObj)
	if err != nil {
		writeErr(w, 500, "internal error")
		return
	}
	termsHash := sha256Hex(canonical)
	manifestJSON := string(jsonCompact(man))
	if err := s.st.PutTermsDoc(&TermsDoc{
		TermsHash: termsHash, IssuanceID: iss.ID, CanonicalTerms: string(canonical),
		ManifestHash: man.ManifestHash, Manifest: manifestJSON,
	}); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if _, err := s.st.EnsureOffering(iss.ID); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	// Bind the generated manifest into the stored draft terms so a later deploy
	// recomputes the identical terms_hash. A deployed issuance's terms are frozen
	// on chain, so this only rewrites drafts.
	if iss.Status != "live" {
		_ = s.st.UpdateIssuanceFields(iss.ID, map[string]any{"terms": string(canonical)})
	}
	ch := characterize(structureName(iss, iss.Terms))
	s.st.Audit(acct.AID, "documents.generate", map[string]any{
		"issuance_id": iss.ID, "terms_hash": termsHash, "manifest_hash": man.ManifestHash, "docs": len(man.Set),
	})
	writeJSON(w, 200, map[string]any{
		"issuance_id":      iss.ID,
		"terms_hash":       termsHash,
		"manifest_hash":    man.ManifestHash,
		"documents":        man.Set,
		"characterization": ch,
		"offer_open":       true,
		"e_signature_tag":  documentTag,
		"note":             "terms_hash now commits to the document manifest. Deploy with these exact terms; the browser hash is a cross-check only.",
	})
}

// --- POST /api/issuances/{id}/offer-close (session, owner) -------------------

func (s *server) handleOfferClose(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	height := s.tipHeight()
	if err := s.st.CloseOffer(iss.ID, height); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	s.st.Audit(acct.AID, "offer.close", map[string]any{"issuance_id": iss.ID, "close_height": height})
	writeJSON(w, 200, map[string]any{
		"issuance_id": iss.ID, "offer_open": false, "close_height": height,
		"note": "document preimages are now public for this offering",
	})
}

// --- GET /api/characterization?structure= (public) --------------------------

func (s *server) handleCharacterization(w http.ResponseWriter, r *http.Request) {
	structure := strings.TrimSpace(r.URL.Query().Get("structure"))
	if structure == "" {
		// Enumerate the four canonical structures.
		out := []Characterization{}
		for _, s := range []string{"equity", "equity-spv", "debt-yield", "depositary-receipt"} {
			out = append(out, characterize(s))
		}
		writeJSON(w, 200, map[string]any{"structures": out})
		return
	}
	writeJSON(w, 200, map[string]any{"characterization": characterize(structure)})
}

// --- POST /api/documents/{hash}/sign (session) ------------------------------

type signDocReq struct {
	Sig string `json:"sig"`
}

func (s *server) handleSignDocument(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	hash := strings.ToLower(strings.TrimSpace(r.PathValue("hash")))
	if !isHex64(hash) {
		writeErr(w, 400, "document hash must be 64 hex characters")
		return
	}
	doc, err := s.st.DocumentByHash(hash)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if doc == nil {
		writeErr(w, 404, "no such document")
		return
	}
	var req signDocReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	msg, err := hex.DecodeString(hash)
	if err != nil || len(msg) != 32 {
		writeErr(w, 400, "document hash must decode to 32 bytes")
		return
	}
	// The e-signature is a real BIP340 signature over the tagged document hash by
	// the signer's enclave key. The e-signature PROVIDER is a labeled simulation;
	// the signature is cryptographically real.
	if err := verifyTaggedByKey(acct.XOnly, documentTag, msg, strings.TrimSpace(req.Sig)); err != nil {
		s.st.Audit(acct.AID, "document.sign.refused", map[string]any{"doc_hash": hash, "reason": err.Error()})
		writeErr(w, 400, "the document signature does not verify for this identity's key")
		return
	}
	anchorTxid := s.anchorArtifact("document-signature", hash)
	if err := s.st.InsertDocSignature(&DocSignature{
		DocHash: hash, SignerAID: acct.AID, XOnly: acct.XOnly, Sig: strings.TrimSpace(req.Sig),
		AnchorTxid: anchorTxid, CreatedAt: time.Now().Unix(),
	}); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	s.st.Audit(acct.AID, "document.sign", map[string]any{"doc_hash": hash, "kind": doc.Kind, "anchor_txid": anchorTxid})
	writeJSON(w, 200, map[string]any{
		"doc_hash": hash, "signer_aid": acct.AID, "tag": documentTag, "anchor_txid": anchorTxid,
		"note": "cryptographically real signature; provider-grade e-signature simulated",
	})
}

// --- GET /api/documents/{hash}/signatures (public) --------------------------

func (s *server) handleDocSignatures(w http.ResponseWriter, r *http.Request) {
	hash := strings.ToLower(strings.TrimSpace(r.PathValue("hash")))
	if !isHex64(hash) {
		writeErr(w, 400, "document hash must be 64 hex characters")
		return
	}
	sigs, err := s.st.SignaturesByDoc(hash)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	writeJSON(w, 200, map[string]any{"doc_hash": hash, "tag": documentTag, "signatures": sigs})
}

// --- GET /api/issuances/{id}/amendments (session, owner) --------------------
//
// The rendering data for the Verify explainer: the genesis terms_hash plus the
// anchored amendment chain.

func (s *server) handleAmendments(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	amends, err := s.st.AmendmentsByIssuance(iss.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	resp := map[string]any{
		"issuance_id":        iss.ID,
		"asset_id":           iss.AssetID,
		"genesis_terms_hash": genesisTermsHash(iss),
		"contract_hash":      iss.ContractHash,
		"amendments":         amends,
		"note":               "the on-chain commitment is the genesis terms_hash plus this anchored amendment chain",
	}
	// The M7 acceptance-proof invariant: whether the live on-chain rules equal the
	// head of the anchored amendment chain. Only meaningful once the asset is
	// deployed; a draft has no rules to compare.
	if iss.AssetID != "" {
		resp["head"] = s.amendmentHeadStatus(iss)
	}
	writeJSON(w, 200, resp)
}

// genesisTermsHash reproduces the terms_hash committed at issue from the stored
// canonical terms, so the explainer shows the same value the contract commits.
func genesisTermsHash(iss *Issuance) string {
	var termsObj any
	if err := json.Unmarshal(rawOrEmpty(iss.Terms), &termsObj); err != nil {
		return ""
	}
	canonical, err := canonicalJSON(termsObj)
	if err != nil {
		return ""
	}
	return sha256Hex(canonical)
}

// --- POST /api/issuances/{id}/amendments (session, owner): generator ---------
//
// M4 exposes the amendment-document GENERATOR: it produces and anchors the
// content-addressed artifact and appends it to the chain. It does NOT call
// openampd's POST /v1/issuer/rules; the live rules mutation is M7. This endpoint
// gives the Verify explainer real chain data and lets the generator be exercised.

type amendReq struct {
	NewRules        *CompiledRules `json:"new_rules"`
	NewRulesHash    string         `json:"new_rules_hash"`
	Basis           string         `json:"basis"`
	EffectiveHeight int64          `json:"effective_height"`
}

func (s *server) handleGenerateAmendment(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	if iss.AssetID == "" {
		writeErr(w, 409, "this issuance is not deployed; there are no rules to amend yet")
		return
	}
	var req amendReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	if strings.TrimSpace(req.Basis) == "" {
		writeErr(w, 400, "an amendment basis is required")
		return
	}
	// The prior rules hash is the head of the chain, or the on-chain rules at
	// genesis. Fetch the current on-chain rules as the prior hash baseline.
	priorRulesHash := ""
	if rules, ok, err := s.assetRules(iss.AssetID); err == nil && ok {
		priorRulesHash, _ = rulesHash(rules)
	}
	if amends, _ := s.st.AmendmentsByIssuance(iss.ID); len(amends) > 0 {
		priorRulesHash = amends[len(amends)-1].NewRulesHash
	}
	// M7: when a full new_rules object is supplied, this is a LIVE mutation. It
	// flows through applyRulesMutation, the single chokepoint that posts the rules
	// to openampd and records the anchored amendment, so GET /v1/assets/{id} rules
	// stay equal to the head of the amendment chain. When only a 64-hex hash is
	// supplied, the artifact-only generator runs (unchanged from M4): it records an
	// amendment artifact without mutating the on-chain rules, for explainer data.
	if req.NewRules != nil {
		m, err := s.applyRulesMutation(iss, *req.NewRules, strings.TrimSpace(req.Basis), req.EffectiveHeight)
		if err != nil {
			writeErr(w, 502, "could not apply the rules mutation: %v", err)
			return
		}
		var a *Amendment
		if amends, _ := s.st.AmendmentsByIssuance(iss.ID); len(amends) > 0 && m.AmendHash != "" {
			for _, x := range amends {
				if x.AmendHash == m.AmendHash {
					a = x
				}
			}
		}
		writeJSON(w, 200, map[string]any{
			"amendment": a, "mutation": m, "head": s.amendmentHeadStatus(iss),
			"note": "rules posted to the policy server and recorded in the anchored amendment chain; GET /v1/assets rules now equal the chain head",
		})
		return
	}
	newRulesHash := strings.TrimSpace(req.NewRulesHash)
	if !isHex64(newRulesHash) {
		writeErr(w, 400, "a new_rules object or a 64-hex new_rules_hash is required")
		return
	}
	a, err := s.generateAmendment(iss, priorRulesHash, newRulesHash, strings.TrimSpace(req.Basis), req.EffectiveHeight)
	if err != nil {
		writeErr(w, 500, "could not generate the amendment artifact: %v", err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"amendment": a,
		"note":      "amendment artifact generated and anchored; supply a full new_rules object to perform the live policy-rules mutation",
	})
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32
}
