package main

import (
	"database/sql"
	"encoding/json"
	"time"
)

// --- claims records ----------------------------------------------------------

// Claims is the platform's identity determination for one AID. It is the single
// source of truth from which a user's openampd category list is projected; the
// openampd list itself is never authoritative, only a mirror of this record at
// the current vocabulary version.
type Claims struct {
	AID              string          `json:"aid"`
	Residence        string          `json:"residence"`
	BaseEligibility  string          `json:"base_eligibility"` // ret | pro
	Accredited       bool            `json:"accredited"`
	AccredArtifact   string          `json:"accred_artifact,omitempty"` // sha256 hex of the (simulated) verification artifact
	AccredValidUntil int64           `json:"accred_valid_until,omitempty"`
	USPerson         bool            `json:"us_person"`
	GBHNW            bool            `json:"gb_hnw,omitempty"`
	GBSoph           bool            `json:"gb_soph,omitempty"`
	TaxResidencies   json.RawMessage `json:"tax_residencies"` // CRS/FATCA self-certification (array of {country, tin})
	ValidUntil       int64           `json:"valid_until,omitempty"`
	Status           string          `json:"status"` // unverified | submitted | needs_info | refused | verified
	VocabVersion     int             `json:"vocab_version"`
	ClaimsSig        string          `json:"claims_sig,omitempty"`
	VerifiedAt       int64           `json:"verified_at,omitempty"`
	UpdatedAt        int64           `json:"updated_at"`
}

func (s *Store) UpsertClaims(c *Claims) error {
	c.UpdatedAt = time.Now().Unix()
	_, err := s.db.Exec(
		`INSERT INTO claims
           (aid, residence, base_eligibility, accredited, accred_artifact, accred_valid_until,
            us_person, gb_hnw, gb_soph, tax_residencies, valid_until, status, vocab_version,
            claims_sig, verified_at, updated_at)
         VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
         ON CONFLICT(aid) DO UPDATE SET
            residence=excluded.residence, base_eligibility=excluded.base_eligibility,
            accredited=excluded.accredited, accred_artifact=excluded.accred_artifact,
            accred_valid_until=excluded.accred_valid_until, us_person=excluded.us_person,
            gb_hnw=excluded.gb_hnw, gb_soph=excluded.gb_soph, tax_residencies=excluded.tax_residencies,
            valid_until=excluded.valid_until, status=excluded.status, vocab_version=excluded.vocab_version,
            claims_sig=excluded.claims_sig, verified_at=excluded.verified_at, updated_at=excluded.updated_at`,
		c.AID, c.Residence, c.BaseEligibility, boolInt(c.Accredited), c.AccredArtifact, c.AccredValidUntil,
		boolInt(c.USPerson), boolInt(c.GBHNW), boolInt(c.GBSoph), string(rawArrOrEmpty(c.TaxResidencies)),
		c.ValidUntil, c.Status, c.VocabVersion, c.ClaimsSig, c.VerifiedAt, c.UpdatedAt)
	return err
}

// DeleteClaims removes an account's claims entirely. It undoes a submission
// that never reached the provider, for an account that had no claims before it.
func (s *Store) DeleteClaims(aid string) error {
	_, err := s.db.Exec(`DELETE FROM claims WHERE aid = ?`, aid)
	return err
}

func (s *Store) ClaimsByAID(aid string) (*Claims, error) {
	var c Claims
	var accredited, usPerson, gbHNW, gbSoph int
	var tax string
	err := s.db.QueryRow(
		`SELECT aid, residence, base_eligibility, accredited, accred_artifact, accred_valid_until,
                us_person, gb_hnw, gb_soph, tax_residencies, valid_until, status, vocab_version,
                claims_sig, verified_at, updated_at
         FROM claims WHERE aid = ?`, aid).
		Scan(&c.AID, &c.Residence, &c.BaseEligibility, &accredited, &c.AccredArtifact, &c.AccredValidUntil,
			&usPerson, &gbHNW, &gbSoph, &tax, &c.ValidUntil, &c.Status, &c.VocabVersion,
			&c.ClaimsSig, &c.VerifiedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.Accredited = accredited != 0
	c.USPerson = usPerson != 0
	c.GBHNW = gbHNW != 0
	c.GBSoph = gbSoph != 0
	c.TaxResidencies = json.RawMessage(tax)
	return &c, nil
}

// AllClaims returns every claims record (used by the screening and expiry crons).
func (s *Store) AllClaims() ([]*Claims, error) {
	rows, err := s.db.Query(`SELECT aid FROM claims`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var aids []string
	for rows.Next() {
		var aid string
		if err := rows.Scan(&aid); err != nil {
			return nil, err
		}
		aids = append(aids, aid)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]*Claims, 0, len(aids))
	for _, aid := range aids {
		c, err := s.ClaimsByAID(aid)
		if err != nil {
			return nil, err
		}
		if c != nil {
			out = append(out, c)
		}
	}
	return out, nil
}

// --- enclave keys (per-offering escrow, per-entity treasury) ------------------

type EnclaveKey struct {
	AID       string `json:"aid"`
	Kind      string `json:"kind"` // offering-escrow | entity-treasury
	RefID     string `json:"ref_id"`
	XOnly     string `json:"xonly"`
	Priv      string `json:"-"` // never serialized to any client
	CreatedAt int64  `json:"created_at"`
}

func (s *Store) InsertEnclaveKey(k *EnclaveKey) error {
	_, err := s.db.Exec(
		`INSERT INTO enclave_keys (aid, kind, ref_id, xonly, priv, created_at) VALUES (?,?,?,?,?,?)`,
		k.AID, k.Kind, k.RefID, k.XOnly, k.Priv, k.CreatedAt)
	return err
}

func (s *Store) EnclaveKeyByRef(kind, refID string) (*EnclaveKey, error) {
	var k EnclaveKey
	err := s.db.QueryRow(
		`SELECT aid, kind, ref_id, xonly, priv, created_at FROM enclave_keys WHERE kind = ? AND ref_id = ?`,
		kind, refID).Scan(&k.AID, &k.Kind, &k.RefID, &k.XOnly, &k.Priv, &k.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &k, nil
}

// --- platform claims-signing key ---------------------------------------------

// PlatformKey returns a persistent platform key by name, generating and storing
// one on first use. The private key never leaves the box.
func (s *Store) PlatformKey(name string) (*EnclaveKey, error) {
	var k EnclaveKey
	err := s.db.QueryRow(
		`SELECT ? AS name, '' AS kind, '' AS ref_id, xonly, priv, created_at FROM platform_keys WHERE name = ?`,
		name, name).Scan(&k.AID, &k.Kind, &k.RefID, &k.XOnly, &k.Priv, &k.CreatedAt)
	if err == nil {
		return &k, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	priv, xonly, err := newEnclaveKey()
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	if _, err := s.db.Exec(
		`INSERT INTO platform_keys (name, xonly, priv, created_at) VALUES (?,?,?,?)`,
		name, xonly, priv, now); err != nil {
		return nil, err
	}
	return &EnclaveKey{XOnly: xonly, Priv: priv, CreatedAt: now}, nil
}

// --- UBO links (entity <-> personal SeqPal ID) -------------------------------

type UBOLink struct {
	EntityID    string `json:"entity_id"`
	UBOAID      string `json:"ubo_aid"`
	TreasuryAID string `json:"treasury_aid"`
	Statement   string `json:"statement"`
	Sig         string `json:"sig,omitempty"`
	CreatedAt   int64  `json:"created_at"`
}

func (s *Store) UpsertUBOLink(l *UBOLink) error {
	_, err := s.db.Exec(
		`INSERT INTO ubo_links (entity_id, ubo_aid, treasury_aid, statement, sig, created_at)
         VALUES (?,?,?,?,?,?)
         ON CONFLICT(entity_id) DO UPDATE SET
            ubo_aid=excluded.ubo_aid, treasury_aid=excluded.treasury_aid,
            statement=excluded.statement, sig=excluded.sig`,
		l.EntityID, l.UBOAID, l.TreasuryAID, l.Statement, l.Sig, l.CreatedAt)
	return err
}

func (s *Store) UBOLinkByEntity(entityID string) (*UBOLink, error) {
	var l UBOLink
	err := s.db.QueryRow(
		`SELECT entity_id, ubo_aid, treasury_aid, statement, sig, created_at FROM ubo_links WHERE entity_id = ?`,
		entityID).Scan(&l.EntityID, &l.UBOAID, &l.TreasuryAID, &l.Statement, &l.Sig, &l.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// --- holder notices (pre-expiry etc; the portal inbox proper lands in M7) ----

func (s *Store) InsertNotice(aid, kind, body string) error {
	id, err := randHex(12)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO notices (id, aid, kind, body, created_at) VALUES (?,?,?,?,?)`,
		id, aid, kind, body, time.Now().Unix())
	return err
}

// NoticeExists reports whether a notice of a given kind was already recorded for
// an AID (so the pre-expiry cron notifies exactly once).
func (s *Store) NoticeExists(aid, kind string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM notices WHERE aid = ? AND kind = ?`, aid, kind).Scan(&n)
	return n > 0, err
}

func rawArrOrEmpty(r json.RawMessage) json.RawMessage {
	if len(r) == 0 {
		return json.RawMessage("[]")
	}
	return r
}
