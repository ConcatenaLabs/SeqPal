package main

import (
	"database/sql"
	"time"
)

// A holder's request to be admitted to one network-enforced asset's whitelist.
//
// An OpenDAMP whitelist is a list of holding KEYS the issuer publishes, and
// nothing puts a holder on one automatically. Without a way to ask, a verified
// SeqPal ID was a credential with nowhere to present itself: the holder had to
// find the issuer out of band and hope. This is that missing step, and it is
// per ASSET, because a whitelist is per asset -- being admitted to one says
// nothing about any other.
type WhitelistRequest struct {
	ID         string `json:"id"`
	IssuanceID string `json:"issuance_id"`
	AssetID    string `json:"asset_id"`
	// AID is the requesting SeqPal account, which is who the issuer is admitting.
	AID string `json:"aid"`
	// HoldingKey is the x-only key that goes on the whitelist. It is the thing
	// consensus checks, so control of it is proven before the request is filed.
	HoldingKey string `json:"holding_key"`
	// How that control was proven, recorded so an issuer can see it and an audit
	// can retrace it: "descriptor" (it derives from the account's own registered
	// wallet) or "signature" (a signed message from that key).
	Proof string `json:"proof"`
	Note  string `json:"note,omitempty"`
	// pending | approved | refused | included | withdrawn
	State        string `json:"state"`
	DecidedBy    string `json:"decided_by,omitempty"`
	DecidedAt    int64  `json:"decided_at,omitempty"`
	DecisionNote string `json:"decision_note,omitempty"`
	// PolicyOpID is the policy change that actually put the key on the published
	// list. Approved is a decision; included is a fact on chain.
	PolicyOpID string `json:"policy_op_id,omitempty"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

const whitelistReqCols = `id, issuance_id, asset_id, aid, holding_key, proof, note, state,
    decided_by, decided_at, decision_note, policy_op_id, created_at, updated_at`

func (s *Store) InsertWhitelistRequest(r *WhitelistRequest) error {
	now := time.Now().Unix()
	r.CreatedAt, r.UpdatedAt = now, now
	_, err := s.db.Exec(
		`INSERT INTO whitelist_requests (`+whitelistReqCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.IssuanceID, r.AssetID, r.AID, r.HoldingKey, r.Proof, r.Note, r.State,
		r.DecidedBy, r.DecidedAt, r.DecisionNote, r.PolicyOpID, r.CreatedAt, r.UpdatedAt)
	return err
}

func (s *Store) WhitelistRequestByID(id string) (*WhitelistRequest, error) {
	return scanWhitelistRequest(s.db.QueryRow(
		`SELECT `+whitelistReqCols+` FROM whitelist_requests WHERE id = ?`, id))
}

// OpenWhitelistRequest finds a live request for this holder and key, so asking
// twice updates one request rather than filing a second.
func (s *Store) OpenWhitelistRequest(issuanceID, holdingKey string) (*WhitelistRequest, error) {
	return scanWhitelistRequest(s.db.QueryRow(
		`SELECT `+whitelistReqCols+` FROM whitelist_requests
		 WHERE issuance_id = ? AND holding_key = ? AND state IN ('pending','approved')
		 ORDER BY created_at DESC LIMIT 1`, issuanceID, holdingKey))
}

// RecentWhitelistRefusal is the last refusal this holder had on this asset. A
// refused holder may ask again -- circumstances change, and an issuer's "not
// yet" is not "never" -- but not immediately and not repeatedly, because every
// request puts a notice in front of the issuer.
func (s *Store) RecentWhitelistRefusal(issuanceID, aid string, since int64) (*WhitelistRequest, error) {
	return scanWhitelistRequest(s.db.QueryRow(
		`SELECT `+whitelistReqCols+` FROM whitelist_requests
		 WHERE issuance_id = ? AND aid = ? AND state = 'refused' AND decided_at >= ?
		 ORDER BY decided_at DESC LIMIT 1`, issuanceID, aid, since))
}

func (s *Store) WhitelistRequestsByIssuance(issuanceID string) ([]*WhitelistRequest, error) {
	rows, err := s.db.Query(
		`SELECT `+whitelistReqCols+` FROM whitelist_requests WHERE issuance_id = ? ORDER BY created_at DESC`,
		issuanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectWhitelistRequests(rows)
}

func (s *Store) WhitelistRequestsByAID(aid string) ([]*WhitelistRequest, error) {
	rows, err := s.db.Query(
		`SELECT `+whitelistReqCols+` FROM whitelist_requests WHERE aid = ? ORDER BY created_at DESC`, aid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectWhitelistRequests(rows)
}

// ApprovedWhitelistKeys is what an issuer has agreed to admit but has not yet
// published: the keys a policy change still has to carry.
func (s *Store) ApprovedWhitelistKeys(issuanceID string) ([]*WhitelistRequest, error) {
	rows, err := s.db.Query(
		`SELECT `+whitelistReqCols+` FROM whitelist_requests
		 WHERE issuance_id = ? AND state = 'approved' ORDER BY decided_at`, issuanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectWhitelistRequests(rows)
}

func (s *Store) UpdateWhitelistRequestFields(id string, fields map[string]any) error {
	return s.updateFields("whitelist_requests", "id", id, fields)
}

// MarkWhitelistRequestsIncluded records that a published policy change carried
// these keys. Approved is a decision the issuer made; included is a fact about
// the chain, and the two are not the same thing.
func (s *Store) MarkWhitelistRequestsIncluded(issuanceID, policyOpID string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	now := time.Now().Unix()
	for _, k := range keys {
		if _, err := s.db.Exec(
			`UPDATE whitelist_requests SET state = 'included', policy_op_id = ?, updated_at = ?
			 WHERE issuance_id = ? AND holding_key = ? AND state = 'approved'`,
			policyOpID, now, issuanceID, k); err != nil {
			return err
		}
	}
	return nil
}

func collectWhitelistRequests(rows *sql.Rows) ([]*WhitelistRequest, error) {
	out := []*WhitelistRequest{}
	for rows.Next() {
		var r WhitelistRequest
		if err := rows.Scan(&r.ID, &r.IssuanceID, &r.AssetID, &r.AID, &r.HoldingKey, &r.Proof,
			&r.Note, &r.State, &r.DecidedBy, &r.DecidedAt, &r.DecisionNote, &r.PolicyOpID,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

func scanWhitelistRequest(row *sql.Row) (*WhitelistRequest, error) {
	var r WhitelistRequest
	err := row.Scan(&r.ID, &r.IssuanceID, &r.AssetID, &r.AID, &r.HoldingKey, &r.Proof, &r.Note,
		&r.State, &r.DecidedBy, &r.DecidedAt, &r.DecisionNote, &r.PolicyOpID, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}
