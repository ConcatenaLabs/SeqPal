package main

import (
	"database/sql"
	"time"
)

// VerificationCheck is one submission to an identity-verification provider, and
// the decision that came back. It is the record of who decided, on what, and
// when -- which is the thing an auditor asks for and the thing this platform
// cannot answer for itself, because it does not do the deciding.
type VerificationCheck struct {
	ID string `json:"id"`
	// AID is the SeqPal account the check is about.
	AID string `json:"aid"`
	// Kind is "identity" for a person, "business" for an entity.
	Kind string `json:"kind"`
	// SubjectName is the name submitted, kept so a decision can be read back
	// against what was actually checked.
	SubjectName string `json:"subject_name"`
	// EntityID is set for a business check.
	EntityID string `json:"entity_id,omitempty"`
	Provider string `json:"provider"`
	// ProviderRef is the provider's own identifier, and what arrives on the
	// callback.
	ProviderRef string `json:"provider_ref,omitempty"`
	// Status is "submitted" until a decision arrives, then "complete".
	Status string `json:"status"`
	// Result is the decision: clear | reject | resubmission.
	Result    string `json:"result,omitempty"`
	Reason    string `json:"reason,omitempty"`
	CreatedAt int64  `json:"created_at"`
	DecidedAt int64  `json:"decided_at,omitempty"`
}

const verificationCheckCols = `id, aid, kind, subject_name, entity_id, provider, provider_ref,
    status, result, reason, created_at, decided_at`

func (s *Store) InsertVerificationCheck(c *VerificationCheck) error {
	if c.CreatedAt == 0 {
		c.CreatedAt = time.Now().Unix()
	}
	_, err := s.db.Exec(
		`INSERT INTO verification_checks (`+verificationCheckCols+`)
         VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.AID, c.Kind, c.SubjectName, c.EntityID, c.Provider, c.ProviderRef,
		c.Status, c.Result, c.Reason, c.CreatedAt, c.DecidedAt)
	return err
}

// SetVerificationCheckRef records the provider's identifier once it has one. The
// row exists before the provider is called, so a provider that answers before
// this platform has finished writing still finds something to decide.
func (s *Store) SetVerificationCheckRef(id, ref string) error {
	_, err := s.db.Exec(`UPDATE verification_checks SET provider_ref = ? WHERE id = ?`, ref, id)
	return err
}

func (s *Store) VerificationCheckByRef(ref string) (*VerificationCheck, error) {
	return scanVerificationCheck(s.db.QueryRow(
		`SELECT `+verificationCheckCols+` FROM verification_checks WHERE provider_ref = ?`, ref))
}

// LatestVerificationCheck is the most recent check for an account, which is what
// a holder is shown while they wait.
func (s *Store) LatestVerificationCheck(aid string) (*VerificationCheck, error) {
	return scanVerificationCheck(s.db.QueryRow(
		`SELECT `+verificationCheckCols+` FROM verification_checks
         WHERE aid = ? ORDER BY created_at DESC, rowid DESC LIMIT 1`, aid))
}

func (s *Store) CompleteVerificationCheck(id, result, reason string, decidedAt int64) error {
	_, err := s.db.Exec(
		`UPDATE verification_checks SET status = 'complete', result = ?, reason = ?, decided_at = ?
         WHERE id = ?`, result, reason, decidedAt, id)
	return err
}

func scanVerificationCheck(row *sql.Row) (*VerificationCheck, error) {
	var c VerificationCheck
	err := row.Scan(&c.ID, &c.AID, &c.Kind, &c.SubjectName, &c.EntityID, &c.Provider,
		&c.ProviderRef, &c.Status, &c.Result, &c.Reason, &c.CreatedAt, &c.DecidedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}
