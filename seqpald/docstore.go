package main

import (
	"database/sql"
	"time"
)

// --- documents ---------------------------------------------------------------

// StoredDoc is one content-addressed artifact at rest. Body is the canonical
// HTML the doc_hash is over; pdf is the lazily rendered, cached PDF (nil until a
// PDF is requested and soffice is available). AlwaysPublic marks documents that
// are never offer-gated (rules amendments, for instance).
type StoredDoc struct {
	DocHash      string
	IssuanceID   string
	Kind         string
	Title        string
	ContentType  string
	AlwaysPublic bool
	Body         []byte
	PDF          []byte
	CreatedAt    int64
}

// PutDocument inserts a document, or leaves the existing row untouched when the
// same content (same doc_hash) is stored again. Content-addressing makes this
// idempotent: identical bytes always carry the identical hash.
func (s *Store) PutDocument(d *StoredDoc) error {
	if d.ContentType == "" {
		d.ContentType = "text/html; charset=utf-8"
	}
	_, err := s.db.Exec(
		`INSERT INTO documents (doc_hash, issuance_id, kind, title, content_type, always_public, body, created_at)
         VALUES (?,?,?,?,?,?,?,?)
         ON CONFLICT(doc_hash) DO NOTHING`,
		d.DocHash, d.IssuanceID, d.Kind, d.Title, d.ContentType, boolInt(d.AlwaysPublic), d.Body, d.CreatedAt)
	return err
}

func (s *Store) DocumentByHash(hash string) (*StoredDoc, error) {
	var d StoredDoc
	var alwaysPublic int
	var pdf []byte
	err := s.db.QueryRow(
		`SELECT doc_hash, issuance_id, kind, title, content_type, always_public, body, pdf, created_at FROM documents WHERE doc_hash = ?`,
		hash).Scan(&d.DocHash, &d.IssuanceID, &d.Kind, &d.Title, &d.ContentType, &alwaysPublic, &d.Body, &pdf, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.AlwaysPublic = alwaysPublic != 0
	d.PDF = pdf
	return &d, nil
}

// SetDocumentPDF caches a rendered PDF against a document.
func (s *Store) SetDocumentPDF(hash string, pdf []byte) error {
	_, err := s.db.Exec(`UPDATE documents SET pdf = ? WHERE doc_hash = ?`, pdf, hash)
	return err
}

// --- terms_docs (the terms->manifest binding, keyed by terms_hash) ----------

type TermsDoc struct {
	TermsHash      string `json:"terms_hash"`
	IssuanceID     string `json:"issuance_id"`
	CanonicalTerms string `json:"-"`
	ManifestHash   string `json:"manifest_hash"`
	Manifest       string `json:"-"`
	CreatedAt      int64  `json:"created_at"`
}

// PutTermsDoc records the exact canonical terms bytes that hash to terms_hash,
// with the document manifest, so GET /api/terms/{hash} can serve the manifest
// and an auditor can recompute the commitment.
func (s *Store) PutTermsDoc(t *TermsDoc) error {
	_, err := s.db.Exec(
		`INSERT INTO terms_docs (terms_hash, issuance_id, canonical_terms, manifest_hash, manifest, created_at)
         VALUES (?,?,?,?,?,?)
         ON CONFLICT(terms_hash) DO UPDATE SET
            issuance_id=excluded.issuance_id, canonical_terms=excluded.canonical_terms,
            manifest_hash=excluded.manifest_hash, manifest=excluded.manifest`,
		t.TermsHash, t.IssuanceID, t.CanonicalTerms, t.ManifestHash, t.Manifest, time.Now().Unix())
	return err
}

func (s *Store) TermsDocByHash(hash string) (*TermsDoc, error) {
	var t TermsDoc
	err := s.db.QueryRow(
		`SELECT terms_hash, issuance_id, canonical_terms, manifest_hash, manifest, created_at FROM terms_docs WHERE terms_hash = ?`,
		hash).Scan(&t.TermsHash, &t.IssuanceID, &t.CanonicalTerms, &t.ManifestHash, &t.Manifest, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// --- offerings (offer-window state) -----------------------------------------

type Offering struct {
	IssuanceID  string `json:"issuance_id"`
	OfferOpen   bool   `json:"offer_open"`
	CloseHeight int64  `json:"close_height"`
	ClosedAt    int64  `json:"closed_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

// EnsureOffering creates an offer-window row (open) on first use and returns it.
func (s *Store) EnsureOffering(issuanceID string) (*Offering, error) {
	if _, err := s.db.Exec(
		`INSERT INTO offerings (issuance_id, offer_open, close_height, closed_at, updated_at)
         VALUES (?,1,0,0,?) ON CONFLICT(issuance_id) DO NOTHING`,
		issuanceID, time.Now().Unix()); err != nil {
		return nil, err
	}
	return s.OfferingByIssuance(issuanceID)
}

func (s *Store) OfferingByIssuance(issuanceID string) (*Offering, error) {
	var o Offering
	var open int
	err := s.db.QueryRow(
		`SELECT issuance_id, offer_open, close_height, closed_at, updated_at FROM offerings WHERE issuance_id = ?`,
		issuanceID).Scan(&o.IssuanceID, &open, &o.CloseHeight, &o.ClosedAt, &o.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	o.OfferOpen = open != 0
	return &o, nil
}

// CloseOffer flips an offering's window closed at a stated height, after which
// document preimages publish ungated.
func (s *Store) CloseOffer(issuanceID string, closeHeight int64) error {
	now := time.Now().Unix()
	if _, err := s.EnsureOffering(issuanceID); err != nil {
		return err
	}
	_, err := s.db.Exec(
		`UPDATE offerings SET offer_open = 0, close_height = ?, closed_at = ?, updated_at = ? WHERE issuance_id = ?`,
		closeHeight, now, now, issuanceID)
	return err
}

// --- filings (the RFSA simulated registry) ----------------------------------

type Filing struct {
	FilingNumber    string `json:"filing_number"`
	Issuer          string `json:"issuer"`
	IssuerAID       string `json:"issuer_aid,omitempty"`
	IssuanceID      string `json:"issuance_id,omitempty"`
	Structure       string `json:"structure"`
	DocManifestHash string `json:"doc_manifest_hash"`
	TermsHash       string `json:"terms_hash"`
	FilingHash      string `json:"filing_hash"`
	AnchorTxid      string `json:"anchor_txid,omitempty"`
	CreatedAt       int64  `json:"created_at"`
}

func (s *Store) InsertFiling(f *Filing) error {
	_, err := s.db.Exec(
		`INSERT INTO filings (filing_number, issuer, issuer_aid, issuance_id, structure, doc_manifest_hash, terms_hash, filing_hash, anchor_txid, created_at)
         VALUES (?,?,?,?,?,?,?,?,?,?)`,
		f.FilingNumber, f.Issuer, f.IssuerAID, f.IssuanceID, f.Structure, f.DocManifestHash, f.TermsHash, f.FilingHash, f.AnchorTxid, f.CreatedAt)
	return err
}

func (s *Store) SetFilingAnchor(number, txid string) error {
	_, err := s.db.Exec(`UPDATE filings SET anchor_txid = ? WHERE filing_number = ?`, txid, number)
	return err
}

func (s *Store) FilingByNumber(number string) (*Filing, error) {
	return scanFiling(s.db.QueryRow(filingSelect+` WHERE filing_number = ?`, number))
}

// FilingByTermsHash returns the most recent filing bound to a terms_hash, which
// is how the deploy gate matches a public offering to its filing.
func (s *Store) FilingByTermsHash(termsHash string) (*Filing, error) {
	return scanFiling(s.db.QueryRow(filingSelect+` WHERE terms_hash = ? ORDER BY created_at DESC LIMIT 1`, termsHash))
}

const filingSelect = `SELECT filing_number, issuer, issuer_aid, issuance_id, structure, doc_manifest_hash, terms_hash, filing_hash, anchor_txid, created_at FROM filings`

func scanFiling(row *sql.Row) (*Filing, error) {
	var f Filing
	err := row.Scan(&f.FilingNumber, &f.Issuer, &f.IssuerAID, &f.IssuanceID, &f.Structure,
		&f.DocManifestHash, &f.TermsHash, &f.FilingHash, &f.AnchorTxid, &f.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *Store) FilingCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM filings`).Scan(&n)
	return n, err
}

// --- amendments (the rules-amendment chain) ---------------------------------

type Amendment struct {
	AmendHash       string `json:"amend_hash"`
	IssuanceID      string `json:"issuance_id"`
	AssetID         string `json:"asset_id,omitempty"`
	Ord             int64  `json:"ord"`
	PriorRulesHash  string `json:"prior_rules_hash"`
	NewRulesHash    string `json:"new_rules_hash"`
	Basis           string `json:"basis"`
	EffectiveHeight int64  `json:"effective_height"`
	AnchorTxid      string `json:"anchor_txid,omitempty"`
	CreatedAt       int64  `json:"created_at"`
}

func (s *Store) InsertAmendment(a *Amendment) error {
	_, err := s.db.Exec(
		`INSERT INTO amendments (amend_hash, issuance_id, asset_id, ord, prior_rules_hash, new_rules_hash, basis, effective_height, anchor_txid, created_at)
         VALUES (?,?,?,?,?,?,?,?,?,?)`,
		a.AmendHash, a.IssuanceID, a.AssetID, a.Ord, a.PriorRulesHash, a.NewRulesHash, a.Basis, a.EffectiveHeight, a.AnchorTxid, a.CreatedAt)
	return err
}

// AmendmentsByIssuance returns the ordered amendment chain for an issuance.
func (s *Store) AmendmentsByIssuance(issuanceID string) ([]*Amendment, error) {
	rows, err := s.db.Query(
		`SELECT amend_hash, issuance_id, asset_id, ord, prior_rules_hash, new_rules_hash, basis, effective_height, anchor_txid, created_at
         FROM amendments WHERE issuance_id = ? ORDER BY ord`, issuanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Amendment{}
	for rows.Next() {
		var a Amendment
		if err := rows.Scan(&a.AmendHash, &a.IssuanceID, &a.AssetID, &a.Ord, &a.PriorRulesHash,
			&a.NewRulesHash, &a.Basis, &a.EffectiveHeight, &a.AnchorTxid, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

func (s *Store) AmendmentCount(issuanceID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM amendments WHERE issuance_id = ?`, issuanceID).Scan(&n)
	return n, err
}

// --- document signatures -----------------------------------------------------

type DocSignature struct {
	DocHash    string `json:"doc_hash"`
	SignerAID  string `json:"signer_aid"`
	XOnly      string `json:"xonly,omitempty"`
	Sig        string `json:"sig"`
	AnchorTxid string `json:"anchor_txid,omitempty"`
	CreatedAt  int64  `json:"created_at"`
}

func (s *Store) InsertDocSignature(sig *DocSignature) error {
	_, err := s.db.Exec(
		`INSERT INTO doc_signatures (doc_hash, signer_aid, xonly, sig, anchor_txid, created_at)
         VALUES (?,?,?,?,?,?)
         ON CONFLICT(doc_hash, signer_aid) DO UPDATE SET
            xonly=excluded.xonly, sig=excluded.sig, anchor_txid=excluded.anchor_txid, created_at=excluded.created_at`,
		sig.DocHash, sig.SignerAID, sig.XOnly, sig.Sig, sig.AnchorTxid, sig.CreatedAt)
	return err
}

func (s *Store) SignaturesByDoc(docHash string) ([]*DocSignature, error) {
	rows, err := s.db.Query(
		`SELECT doc_hash, signer_aid, xonly, sig, anchor_txid, created_at FROM doc_signatures WHERE doc_hash = ? ORDER BY created_at`, docHash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*DocSignature{}
	for rows.Next() {
		var d DocSignature
		if err := rows.Scan(&d.DocHash, &d.SignerAID, &d.XOnly, &d.Sig, &d.AnchorTxid, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &d)
	}
	return out, rows.Err()
}

func (s *Store) IsSigner(docHash, aid string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM doc_signatures WHERE doc_hash = ? AND signer_aid = ?`, docHash, aid).Scan(&n)
	return n > 0, err
}
