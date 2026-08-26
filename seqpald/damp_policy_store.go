package main

import (
	"database/sql"
	"encoding/json"
	"time"
)

// Persistence for holder-list and frozen-coin changes on a network-enforced
// asset (M13). It mirrors the supervision-op shape deliberately: the row is
// created BEFORE the issuer signs anything, records everything a resumed attempt
// needs, and is the reason a replayed build cannot open a second sequence number.

// DampPolicyOp is one holder-list or frozen-coin change. States:
//
//	pending   built at the policy server; the issuer's signature is outstanding
//	prepared  the registrar's program identity is still outstanding
//	published the change is broadcast and the new policy is published
//	failed    a terminal error, with the reason recorded
//
// RegistrarDocument is what the issuer's registrar compiles against. It is not a
// secret and not a key; it is the next policy in its canonical form.
type DampPolicyOp struct {
	ID         string `json:"id"`
	IssuanceID string `json:"issuance_id"`
	AssetID    string `json:"asset_id"`
	Kind       string `json:"kind"` // freeze | unfreeze
	// PolicyID is the policy server's own operation id, which is what makes its
	// completion idempotent as well as this one.
	PolicyID string `json:"policy_id,omitempty"`
	Seq      int64  `json:"seq"`
	PrevPi   string `json:"prev_pi,omitempty"`
	PiNext   string `json:"pi_next,omitempty"`

	Targets        json.RawMessage `json:"targets,omitempty"`
	Holders        json.RawMessage `json:"holders,omitempty"`
	HoldersAdded   json.RawMessage `json:"holders_added,omitempty"`
	HoldersRemoved json.RawMessage `json:"holders_removed,omitempty"`
	CoinsFrozen    json.RawMessage `json:"coins_frozen,omitempty"`
	CoinsUnfrozen  json.RawMessage `json:"coins_unfrozen,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	OrderHash      string          `json:"order_hash,omitempty"`
	ToSign         string          `json:"to_sign,omitempty"`
	// SnapshotHash is what ToSign is the TAGGED hash of, under the policy
	// server's OpenDAMP/snapshot/v1 tag. The issuer's wallet is given this rather
	// than ToSign, so it applies the tag itself and never signs a digest it was
	// simply handed.
	SnapshotHash    string          `json:"snapshot_hash,omitempty"`
	RegistrarDoc    json.RawMessage `json:"registrar_document,omitempty"`
	VerifierProgram string          `json:"verifier_program,omitempty"`
	Txid            string          `json:"txid,omitempty"`
	State           string          `json:"state"`
	Error           string          `json:"error,omitempty"`
	CreatedAt       int64           `json:"created_at"`
	UpdatedAt       int64           `json:"updated_at"`
}

const dampPolicyOpCols = `id, issuance_id, asset_id, kind, policy_id, seq, prev_pi, pi_next, targets,
    holders, holders_added, holders_removed, coins_frozen, coins_unfrozen, reason, order_hash, to_sign,
    registrar_document, verifier_cmr, txid, snapshot_hash, state, error, created_at, updated_at`

func (s *Store) InsertDampPolicyOp(o *DampPolicyOp) error {
	now := time.Now().Unix()
	o.CreatedAt, o.UpdatedAt = now, now
	j := func(raw json.RawMessage, empty string) string {
		if len(raw) == 0 {
			return empty
		}
		return string(raw)
	}
	_, err := s.db.Exec(
		`INSERT INTO damp_policy_ops (`+dampPolicyOpCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.ID, o.IssuanceID, o.AssetID, o.Kind, o.PolicyID, o.Seq, o.PrevPi, o.PiNext, j(o.Targets, "{}"),
		j(o.Holders, "[]"), j(o.HoldersAdded, "[]"), j(o.HoldersRemoved, "[]"),
		j(o.CoinsFrozen, "[]"), j(o.CoinsUnfrozen, "[]"),
		o.Reason, o.OrderHash, o.ToSign, j(o.RegistrarDoc, "{}"), o.VerifierProgram, o.Txid,
		o.SnapshotHash, o.State, o.Error, o.CreatedAt, o.UpdatedAt)
	return err
}

func (s *Store) UpdateDampPolicyOpFields(id string, fields map[string]any) error {
	return s.updateFields("damp_policy_ops", "id", id, fields)
}

func (s *Store) DampPolicyOpByID(id string) (*DampPolicyOp, error) {
	return scanDampPolicyOp(s.db.QueryRow(`SELECT `+dampPolicyOpCols+` FROM damp_policy_ops WHERE id = ?`, id))
}

// DampPolicyOpFor finds an existing operation for (issuance, kind, order hash,
// targets), which is what makes a replayed build idempotent: the same order
// against the same holders and coins resumes the same operation rather than
// opening a second sequence number at the policy server.
func (s *Store) DampPolicyOpFor(issuanceID, kind, orderHash, targets string) (*DampPolicyOp, error) {
	return scanDampPolicyOp(s.db.QueryRow(
		`SELECT `+dampPolicyOpCols+` FROM damp_policy_ops
         WHERE issuance_id = ? AND kind = ? AND order_hash = ? AND targets = ?
         ORDER BY created_at DESC LIMIT 1`, issuanceID, kind, orderHash, targets))
}

func (s *Store) DampPolicyOpsByIssuance(issuanceID string) ([]*DampPolicyOp, error) {
	rows, err := s.db.Query(
		`SELECT `+dampPolicyOpCols+` FROM damp_policy_ops WHERE issuance_id = ? ORDER BY created_at`, issuanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*DampPolicyOp{}
	for rows.Next() {
		o, err := scanDampPolicyOpInto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func scanDampPolicyOp(row *sql.Row) (*DampPolicyOp, error) {
	o, err := scanDampPolicyOpInto(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return o, err
}

func scanDampPolicyOpInto(sc scanner) (*DampPolicyOp, error) {
	var o DampPolicyOp
	var targets, holders, added, removed, frozen, unfrozen, doc string
	if err := sc.Scan(&o.ID, &o.IssuanceID, &o.AssetID, &o.Kind, &o.PolicyID, &o.Seq, &o.PrevPi, &o.PiNext,
		&targets, &holders, &added, &removed, &frozen, &unfrozen, &o.Reason, &o.OrderHash, &o.ToSign,
		&doc, &o.VerifierProgram, &o.Txid, &o.SnapshotHash, &o.State, &o.Error, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return nil, err
	}
	o.Targets = json.RawMessage(targets)
	o.Holders = json.RawMessage(holders)
	o.HoldersAdded = json.RawMessage(added)
	o.HoldersRemoved = json.RawMessage(removed)
	o.CoinsFrozen = json.RawMessage(frozen)
	o.CoinsUnfrozen = json.RawMessage(unfrozen)
	o.RegistrarDoc = json.RawMessage(doc)
	return &o, nil
}
