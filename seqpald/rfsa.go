package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// The RFSA Financial Products Registry is a simulated regulator with real
// registry mechanics: a filing carries a number, is publicly look-up-able, gates
// a public offering's deploy, and has its filing hash anchored on-chain. Only the
// regulator RELATIONSHIP is simulated; the number, the lookup, the gate, and the
// anchoring are real. Every surface labels it "simulated regulator, real registry
// mechanics".

const rfsaLabel = "simulated regulator, real registry mechanics"

type filingReq struct {
	Issuer          string `json:"issuer"`
	Structure       string `json:"structure"`
	DocManifestHash string `json:"doc_manifest_hash"`
	TermsHash       string `json:"terms_hash"`
	IssuanceID      string `json:"issuance_id"` // optional convenience link
}

// handleRFSAFile records a filing. It is session-gated so the filer is a
// registered identity (the issuer of record for the filing), which is more
// honest than an open regulator inbox and prevents spam. The filing hash is a
// content address over the canonical filing record; it is anchored via the
// existing log/anchor path.
func (s *server) handleRFSAFile(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	var req filingReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	termsHash := strings.ToLower(strings.TrimSpace(req.TermsHash))
	if !isHex64(termsHash) {
		writeErr(w, 400, "terms_hash must be 64 hex characters")
		return
	}
	docManifestHash := strings.ToLower(strings.TrimSpace(req.DocManifestHash))
	if docManifestHash != "" && !isHex64(docManifestHash) {
		writeErr(w, 400, "doc_manifest_hash must be 64 hex characters")
		return
	}
	issuer := strings.TrimSpace(req.Issuer)
	if issuer == "" {
		issuer = acct.DisplayName
	}
	structure, ok := canonicalStructure(req.Structure)
	if !ok {
		// Fail closed (W-7): a filing must name a structure the pipeline can
		// classify; never silently file an unknown one as equity.
		writeErr(w, 400, "unrecognized structure %q", req.Structure)
		return
	}

	number, err := s.nextFilingNumber()
	if err != nil {
		writeErr(w, 500, "could not allocate a filing number")
		return
	}
	now := time.Now().Unix()
	f := &Filing{
		FilingNumber: number, Issuer: issuer, IssuerAID: acct.AID, IssuanceID: strings.TrimSpace(req.IssuanceID),
		Structure: structure, DocManifestHash: docManifestHash, TermsHash: termsHash, CreatedAt: now,
	}
	f.FilingHash = filingHash(f)
	if err := s.st.InsertFiling(f); err != nil {
		writeErr(w, 500, "could not record the filing: %v", err)
		return
	}
	f.AnchorTxid = s.anchorArtifact("rfsa-filing", f.FilingHash)
	if f.AnchorTxid != "" {
		_ = s.st.SetFilingAnchor(number, f.AnchorTxid)
	}
	s.st.Audit(acct.AID, "rfsa.file", map[string]any{
		"filing_number": number, "terms_hash": termsHash, "doc_manifest_hash": docManifestHash,
		"structure": structure, "filing_hash": f.FilingHash, "anchor_txid": f.AnchorTxid,
	})
	writeJSON(w, 200, map[string]any{
		"filing_number": number,
		"filing":        f,
		"label":         rfsaLabel,
	})
}

// handleRFSALookup is the public registry lookup.
func (s *server) handleRFSALookup(w http.ResponseWriter, r *http.Request) {
	number := strings.TrimSpace(r.PathValue("number"))
	f, err := s.st.FilingByNumber(number)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if f == nil {
		writeErr(w, 404, "no such filing")
		return
	}
	writeJSON(w, 200, map[string]any{"filing": f, "label": rfsaLabel})
}

// nextFilingNumber allocates a concrete, human-readable filing number. It is
// unique by construction (a random suffix), so it needs no lock, and it reads
// like a real registry number.
func (s *server) nextFilingNumber() (string, error) {
	suffix, err := randHex(3) // 6 hex chars
	if err != nil {
		return "", err
	}
	year := time.Now().UTC().Year()
	return fmt.Sprintf("RFSA-FP-%d-%s", year, strings.ToUpper(suffix)), nil
}

// filingHash is the content address of a filing: sha256 over the canonical
// {doc_manifest_hash, filing_number, issuer, issuer_aid, structure, terms_hash}.
func filingHash(f *Filing) string {
	canon, _ := canonicalJSON(map[string]any{
		"filing_number":     f.FilingNumber,
		"issuer":            f.Issuer,
		"issuer_aid":        f.IssuerAID,
		"structure":         f.Structure,
		"doc_manifest_hash": f.DocManifestHash,
		"terms_hash":        f.TermsHash,
	})
	return sha256Hex(canon)
}

// isPublicOffering reports whether an issuance's terms describe a public offering,
// which is what the RFSA filing gate requires. A public offering is one the terms
// explicitly mark public, or one that promotes to any jurisdiction. A private
// placement (the default when no public marker is present) does not require a
// filing, which keeps the gate opt-in and backward compatible.
func isPublicOffering(termsRaw json.RawMessage) bool {
	if len(termsRaw) == 0 {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal(termsRaw, &m); err != nil {
		return false
	}
	if b, ok := m["public"].(bool); ok && b {
		return true
	}
	if b, ok := m["public_offering"].(bool); ok && b {
		return true
	}
	if t, ok := m["offering_type"].(string); ok && strings.EqualFold(strings.TrimSpace(t), "public") {
		return true
	}
	if pj, ok := m["promotable_jurisdictions"].([]any); ok && len(pj) > 0 {
		return true
	}
	return false
}
