package main

import (
	"database/sql"
	"time"
)

// M7 (Backend-B) persistence: the durable records behind the rules-amendment
// chokepoint, the freeze/clawback console, the stranded-key runbook, and the
// scheduled ownership snapshots. Every fund-movement record is written BEFORE the
// broadcast it authorizes, so an ambiguous timeout is reconciled by a chain/log
// scan before any retry, never a double-sweep. This mirrors the M5/M6 settlement
// idempotency shape exactly.

// --- rules mutations (the amendment-chain chokepoint) ------------------------

// RulesMutation is the durable intent for one policy-rules change. It is written
// pending BEFORE the openampd rules POST, advanced to rules_posted once the POST
// lands, and to complete once the anchored amendment is generated. The reconcile
// cron finishes any record left short of complete, so a crash between the rules
// POST and the anchor never leaves the amendment chain out of step with the
// on-chain rules. state: pending -> rules_posted -> complete.
type RulesMutation struct {
	ID              string `json:"id"`
	IssuanceID      string `json:"issuance_id"`
	AssetID         string `json:"asset_id"`
	PriorRulesHash  string `json:"prior_rules_hash"`
	NewRulesHash    string `json:"new_rules_hash"`
	Basis           string `json:"basis"`
	EffectiveHeight int64  `json:"effective_height"`
	RulesJSON       string `json:"-"`
	State           string `json:"state"`
	AmendHash       string `json:"amend_hash,omitempty"`
	AnchorTxid      string `json:"anchor_txid,omitempty"`
	Error           string `json:"error,omitempty"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
}

const rulesMutCols = `id, issuance_id, asset_id, prior_rules_hash, new_rules_hash, basis,
    effective_height, rules_json, state, amend_hash, anchor_txid, error, created_at, updated_at`

func (s *Store) InsertRulesMutation(m *RulesMutation) error {
	now := time.Now().Unix()
	m.CreatedAt, m.UpdatedAt = now, now
	_, err := s.db.Exec(
		`INSERT INTO rules_mutations (`+rulesMutCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.IssuanceID, m.AssetID, m.PriorRulesHash, m.NewRulesHash, m.Basis,
		m.EffectiveHeight, m.RulesJSON, m.State, m.AmendHash, m.AnchorTxid, m.Error, m.CreatedAt, m.UpdatedAt)
	return err
}

func (s *Store) UpdateRulesMutationFields(id string, fields map[string]any) error {
	return s.updateFields("rules_mutations", "id", id, fields)
}

func (s *Store) RulesMutationByID(id string) (*RulesMutation, error) {
	return scanRulesMut(s.db.QueryRow(`SELECT `+rulesMutCols+` FROM rules_mutations WHERE id = ?`, id))
}

// RulesMutationsUnfinished returns every mutation not yet in the complete state,
// oldest first, so the reconcile cron heals half-applied mutations in order.
func (s *Store) RulesMutationsUnfinished() ([]*RulesMutation, error) {
	rows, err := s.db.Query(`SELECT ` + rulesMutCols + ` FROM rules_mutations WHERE state != 'complete' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*RulesMutation{}
	for rows.Next() {
		m, err := scanRulesMutInto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func scanRulesMut(row *sql.Row) (*RulesMutation, error) {
	m, err := scanRulesMutInto(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return m, err
}

func scanRulesMutInto(sc scanner) (*RulesMutation, error) {
	var m RulesMutation
	err := sc.Scan(&m.ID, &m.IssuanceID, &m.AssetID, &m.PriorRulesHash, &m.NewRulesHash, &m.Basis,
		&m.EffectiveHeight, &m.RulesJSON, &m.State, &m.AmendHash, &m.AnchorTxid, &m.Error, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// --- clawbacks (the freeze/clawback console + runbook fund-movement anchor) ---

// Clawback is one full-sweep seizure of a holder's enclave UTXOs for an asset.
// The row is written 'sweeping' BEFORE the openampd clawback call; because
// openampd writes its public transparency-log entry BEFORE it signs, a lost write
// is reconciled by scanning that log for the entry naming this asset and holder,
// exactly as escrowFindSend reconciles an escrow send. state: sweeping -> swept
// (txid recorded) | empty (holder had no confirmed balance) | failed (retryable).
type Clawback struct {
	ID         string `json:"id"`
	IssuanceID string `json:"issuance_id"`
	AssetID    string `json:"asset_id"`
	HolderAID  string `json:"holder_aid"`
	Reason     string `json:"reason"`
	State      string `json:"state"`
	Atoms      uint64 `json:"atoms"`
	Txid       string `json:"txid,omitempty"`
	ByAID      string `json:"by_aid,omitempty"`
	Context    string `json:"context"` // console | redeliver
	Error      string `json:"error,omitempty"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

const clawbackCols = `id, issuance_id, asset_id, holder_aid, reason, state, atoms, txid, by_aid, context, error, created_at, updated_at`

func (s *Store) InsertClawback(c *Clawback) error {
	now := time.Now().Unix()
	c.CreatedAt, c.UpdatedAt = now, now
	_, err := s.db.Exec(
		`INSERT INTO clawbacks (`+clawbackCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.IssuanceID, c.AssetID, c.HolderAID, c.Reason, c.State, c.Atoms, c.Txid,
		c.ByAID, c.Context, c.Error, c.CreatedAt, c.UpdatedAt)
	return err
}

func (s *Store) UpdateClawbackFields(id string, fields map[string]any) error {
	return s.updateFields("clawbacks", "id", id, fields)
}

func (s *Store) ClawbackByID(id string) (*Clawback, error) {
	return scanClawback(s.db.QueryRow(`SELECT `+clawbackCols+` FROM clawbacks WHERE id = ?`, id))
}

// InflightClawback returns the most recent not-yet-terminal ('sweeping') clawback
// for (asset, holder), the row a retry must reconcile before starting a new one.
func (s *Store) InflightClawback(assetID, holderAID string) (*Clawback, error) {
	return scanClawback(s.db.QueryRow(
		`SELECT `+clawbackCols+` FROM clawbacks WHERE asset_id = ? AND holder_aid = ? AND state = 'sweeping'
         ORDER BY created_at DESC LIMIT 1`, assetID, holderAID))
}

// LastSweptClawback returns the most recent completed sweep for (asset, holder),
// used to answer a repeat console request idempotently when the holder balance is
// already zero.
func (s *Store) LastSweptClawback(assetID, holderAID string) (*Clawback, error) {
	return scanClawback(s.db.QueryRow(
		`SELECT `+clawbackCols+` FROM clawbacks WHERE asset_id = ? AND holder_aid = ? AND state = 'swept'
         ORDER BY created_at DESC LIMIT 1`, assetID, holderAID))
}

func scanClawback(row *sql.Row) (*Clawback, error) {
	var c Clawback
	err := row.Scan(&c.ID, &c.IssuanceID, &c.AssetID, &c.HolderAID, &c.Reason, &c.State, &c.Atoms,
		&c.Txid, &c.ByAID, &c.Context, &c.Error, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// --- redeliveries (the stranded-key runbook) ---------------------------------

// Redelivery is one end-to-end stranded-key remediation, keyed uniquely by
// (issuance, old_aid, new_aid) so a re-run resumes the same record rather than
// starting a parallel one. state: created -> clawed (old AID swept) -> redelivered
// (new AID made whole) -> complete (accounting reconciled). Each step is
// idempotent and reconciled by an on-chain balance read.
type Redelivery struct {
	ID            string `json:"id"`
	IssuanceID    string `json:"issuance_id"`
	AssetID       string `json:"asset_id"`
	OldAID        string `json:"old_aid"`
	NewAID        string `json:"new_aid"`
	Reason        string `json:"reason"`
	State         string `json:"state"`
	SweptAtoms    uint64 `json:"swept_atoms"`
	ClawbackID    string `json:"clawback_id,omitempty"`
	ClawbackTxid  string `json:"clawback_txid,omitempty"`
	SourceAID     string `json:"source_aid,omitempty"`
	SourceKind    string `json:"source_kind,omitempty"`
	NewBefore     uint64 `json:"new_before"`
	RedeliverTxid string `json:"redeliver_txid,omitempty"`
	ByAID         string `json:"by_aid,omitempty"`
	Error         string `json:"error,omitempty"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

const redeliveryCols = `id, issuance_id, asset_id, old_aid, new_aid, reason, state, swept_atoms,
    clawback_id, clawback_txid, source_aid, source_kind, new_before, redeliver_txid, by_aid, error,
    created_at, updated_at`

func (s *Store) InsertRedelivery(r *Redelivery) error {
	now := time.Now().Unix()
	r.CreatedAt, r.UpdatedAt = now, now
	_, err := s.db.Exec(
		`INSERT INTO redeliveries (`+redeliveryCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.IssuanceID, r.AssetID, r.OldAID, r.NewAID, r.Reason, r.State, r.SweptAtoms,
		r.ClawbackID, r.ClawbackTxid, r.SourceAID, r.SourceKind, r.NewBefore, r.RedeliverTxid,
		r.ByAID, r.Error, r.CreatedAt, r.UpdatedAt)
	return err
}

func (s *Store) UpdateRedeliveryFields(id string, fields map[string]any) error {
	return s.updateFields("redeliveries", "id", id, fields)
}

func (s *Store) RedeliveryByID(id string) (*Redelivery, error) {
	return scanRedelivery(s.db.QueryRow(`SELECT `+redeliveryCols+` FROM redeliveries WHERE id = ?`, id))
}

func (s *Store) RedeliveryByParties(issuanceID, oldAID, newAID string) (*Redelivery, error) {
	return scanRedelivery(s.db.QueryRow(
		`SELECT `+redeliveryCols+` FROM redeliveries WHERE issuance_id = ? AND old_aid = ? AND new_aid = ?`,
		issuanceID, oldAID, newAID))
}

func scanRedelivery(row *sql.Row) (*Redelivery, error) {
	var r Redelivery
	err := row.Scan(&r.ID, &r.IssuanceID, &r.AssetID, &r.OldAID, &r.NewAID, &r.Reason, &r.State,
		&r.SweptAtoms, &r.ClawbackID, &r.ClawbackTxid, &r.SourceAID, &r.SourceKind, &r.NewBefore,
		&r.RedeliverTxid, &r.ByAID, &r.Error, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// --- ownership snapshots + annual reports ------------------------------------

// OwnershipSnapshot is one scheduled register capture (or annual report) for a
// live issuance, anchored via the existing log/anchor path. It is an append-only
// time series: the register at a Sequentia block height, content-addressed.
type OwnershipSnapshot struct {
	ID           string `json:"id"`
	IssuanceID   string `json:"issuance_id"`
	AssetID      string `json:"asset_id"`
	Height       int64  `json:"height"`
	HoldersCount int    `json:"holders_count"`
	TotalAtoms   uint64 `json:"total_atoms"`
	ReportHash   string `json:"report_hash,omitempty"`
	Kind         string `json:"kind"` // snapshot | annual-report
	AnchorTxid   string `json:"anchor_txid,omitempty"`
	CreatedAt    int64  `json:"created_at"`
}

const snapCols = `id, issuance_id, asset_id, height, holders_count, total_atoms, report_hash, kind, anchor_txid, created_at`

func (s *Store) InsertOwnershipSnapshot(o *OwnershipSnapshot) error {
	o.CreatedAt = time.Now().Unix()
	_, err := s.db.Exec(
		`INSERT INTO ownership_snapshots (`+snapCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		o.ID, o.IssuanceID, o.AssetID, o.Height, o.HoldersCount, o.TotalAtoms, o.ReportHash, o.Kind, o.AnchorTxid, o.CreatedAt)
	return err
}

func (s *Store) SnapshotsByIssuance(issuanceID string) ([]*OwnershipSnapshot, error) {
	rows, err := s.db.Query(`SELECT `+snapCols+` FROM ownership_snapshots WHERE issuance_id = ? ORDER BY created_at DESC`, issuanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*OwnershipSnapshot{}
	for rows.Next() {
		var o OwnershipSnapshot
		if err := rows.Scan(&o.ID, &o.IssuanceID, &o.AssetID, &o.Height, &o.HoldersCount, &o.TotalAtoms,
			&o.ReportHash, &o.Kind, &o.AnchorTxid, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &o)
	}
	return out, rows.Err()
}

// LastSnapshotOfKind returns the newest snapshot of a kind for an issuance, so the
// annual-report cron can rate-limit itself to one report per period.
func (s *Store) LastSnapshotOfKind(issuanceID, kind string) (*OwnershipSnapshot, error) {
	var o OwnershipSnapshot
	err := s.db.QueryRow(`SELECT `+snapCols+` FROM ownership_snapshots WHERE issuance_id = ? AND kind = ?
        ORDER BY created_at DESC LIMIT 1`, issuanceID, kind).
		Scan(&o.ID, &o.IssuanceID, &o.AssetID, &o.Height, &o.HoldersCount, &o.TotalAtoms,
			&o.ReportHash, &o.Kind, &o.AnchorTxid, &o.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// --- holder notices inbox ----------------------------------------------------

// Notice is one portal-inbox item for a holder (pre-expiry warnings, skipped
// distributions, annual-report availability, servicing actions taken on their
// account). The inbox read surface (GET /api/id/notices) returns these.
type Notice struct {
	ID        string `json:"id"`
	AID       string `json:"aid"`
	Kind      string `json:"kind"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"created_at"`
}

func (s *Store) NoticesByAID(aid string) ([]*Notice, error) {
	rows, err := s.db.Query(`SELECT id, aid, kind, body, created_at FROM notices WHERE aid = ? ORDER BY created_at DESC`, aid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Notice{}
	for rows.Next() {
		var n Notice
		if err := rows.Scan(&n.ID, &n.AID, &n.Kind, &n.Body, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &n)
	}
	return out, rows.Err()
}
