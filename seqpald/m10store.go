package main

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// M10 persistence: bearer attestations, supervision operations, and the
// corporate-action engine (actions, snapshots, claims). Every money- or
// authority-moving row here follows the house idempotency discipline: the row
// is created BEFORE anything is broadcast and carries enough state (funding
// prevout, raw tx, txid) that an ambiguous submit is resumed, never re-built.

// --- bearer attestations -----------------------------------------------------

// BearerAttestation is the annually-refreshable no-US-nexus / risk attestation
// a bearer (supervised) deploy requires, BIP340-signed by the issuer's session
// key under the tag "seqpal-bearer-attestation-v1" over sha256 of the canonical
// statement.
type BearerAttestation struct {
	IssuanceID   string `json:"issuance_id"`
	AID          string `json:"aid"`
	NoUSNexus    bool   `json:"no_us_nexus"`
	RiskAccepted bool   `json:"risk_accepted"`
	Statement    string `json:"statement"`
	Sig          string `json:"sig,omitempty"`
	CreatedAt    int64  `json:"created_at"`
	ValidUntil   int64  `json:"valid_until"`
}

// UpsertBearerAttestation stores (or annually refreshes) the attestation for an
// issuance. One live attestation per issuance; a re-POST replaces it with a new
// validity window.
func (s *Store) UpsertBearerAttestation(a *BearerAttestation) error {
	a.CreatedAt = time.Now().Unix()
	_, err := s.db.Exec(
		`INSERT INTO bearer_attestations (issuance_id, aid, no_us_nexus, risk_accepted, statement, sig, created_at, valid_until)
         VALUES (?,?,?,?,?,?,?,?)
         ON CONFLICT(issuance_id) DO UPDATE SET
            aid=excluded.aid, no_us_nexus=excluded.no_us_nexus, risk_accepted=excluded.risk_accepted,
            statement=excluded.statement, sig=excluded.sig, created_at=excluded.created_at,
            valid_until=excluded.valid_until`,
		a.IssuanceID, a.AID, boolInt(a.NoUSNexus), boolInt(a.RiskAccepted), a.Statement, a.Sig,
		a.CreatedAt, a.ValidUntil)
	return err
}

func (s *Store) BearerAttestation(issuanceID string) (*BearerAttestation, error) {
	var a BearerAttestation
	var nexus, risk int
	err := s.db.QueryRow(
		`SELECT issuance_id, aid, no_us_nexus, risk_accepted, statement, sig, created_at, valid_until
         FROM bearer_attestations WHERE issuance_id = ?`, issuanceID).
		Scan(&a.IssuanceID, &a.AID, &nexus, &risk, &a.Statement, &a.Sig, &a.CreatedAt, &a.ValidUntil)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.NoUSNexus = nexus != 0
	a.RiskAccepted = risk != 0
	return &a, nil
}

// --- supervision operations --------------------------------------------------

// SupervisionOp is one pending or submitted freeze/unfreeze. States:
// pending (built, funding prevout locked, awaiting the operational-key
// signature) -> submitting (raw tx assembled + txid recorded, broadcast may or
// may not have landed) -> submitted (broadcast confirmed sent). The row is the
// idempotency anchor: a replayed complete resumes from the recorded raw tx and
// txid rather than assembling a second record.
type SupervisionOp struct {
	ID           string `json:"id"`
	IssuanceID   string `json:"issuance_id"`
	AssetID      string `json:"asset_id"`
	Kind         string `json:"kind"` // freeze | unfreeze
	Target       string `json:"target,omitempty"`
	TargetHash   string `json:"target_hash,omitempty"`
	Reason       string `json:"reason,omitempty"`
	OrderHash    string `json:"order_hash,omitempty"`
	RefID        string `json:"ref_id,omitempty"` // unfreeze: the freeze op it lifts
	FundTxid     string `json:"fund_txid,omitempty"`
	FundVout     int64  `json:"fund_vout"`
	FundAtoms    uint64 `json:"fund_atoms"`
	Sighash      string `json:"sighash,omitempty"`
	RecordScript string `json:"record_script,omitempty"`
	RecordVout   int64  `json:"record_vout"`
	RawTx        string `json:"-"`
	Txid         string `json:"txid,omitempty"`
	State        string `json:"state"`
	Channel      string `json:"channel,omitempty"` // private | mempool
	Error        string `json:"error,omitempty"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

const supervisionOpCols = `id, issuance_id, asset_id, kind, target, target_hash, reason, order_hash, ref_id,
    fund_txid, fund_vout, fund_atoms, sighash, record_script, record_vout, rawtx, txid, state, channel, error,
    created_at, updated_at`

func (s *Store) InsertSupervisionOp(o *SupervisionOp) error {
	now := time.Now().Unix()
	o.CreatedAt, o.UpdatedAt = now, now
	_, err := s.db.Exec(
		`INSERT INTO supervision_ops (`+supervisionOpCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.ID, o.IssuanceID, o.AssetID, o.Kind, o.Target, o.TargetHash, o.Reason, o.OrderHash, o.RefID,
		o.FundTxid, o.FundVout, o.FundAtoms, o.Sighash, o.RecordScript, o.RecordVout, o.RawTx, o.Txid,
		o.State, o.Channel, o.Error, o.CreatedAt, o.UpdatedAt)
	return err
}

func (s *Store) UpdateSupervisionOpFields(id string, fields map[string]any) error {
	return s.updateFields("supervision_ops", "id", id, fields)
}

func (s *Store) SupervisionOpByID(id string) (*SupervisionOp, error) {
	return scanSupervisionOp(s.db.QueryRow(`SELECT `+supervisionOpCols+` FROM supervision_ops WHERE id = ?`, id))
}

// SupervisionOpFor finds an existing op for (issuance, kind, target hash,
// order hash), which is what makes a replayed build idempotent: the same legal
// order against the same target resumes the same op (and its locked funding
// prevout) rather than locking a second UTXO.
func (s *Store) SupervisionOpFor(issuanceID, kind, targetHash, orderHash string) (*SupervisionOp, error) {
	return scanSupervisionOp(s.db.QueryRow(
		`SELECT `+supervisionOpCols+` FROM supervision_ops
         WHERE issuance_id = ? AND kind = ? AND target_hash = ? AND order_hash = ?
         ORDER BY created_at DESC LIMIT 1`, issuanceID, kind, targetHash, orderHash))
}

func (s *Store) SupervisionOpsByIssuance(issuanceID string) ([]*SupervisionOp, error) {
	rows, err := s.db.Query(
		`SELECT `+supervisionOpCols+` FROM supervision_ops WHERE issuance_id = ? ORDER BY created_at`, issuanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SupervisionOp{}
	for rows.Next() {
		o, err := scanSupervisionOpInto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func scanSupervisionOp(row *sql.Row) (*SupervisionOp, error) {
	o, err := scanSupervisionOpInto(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return o, err
}

func scanSupervisionOpInto(sc scanner) (*SupervisionOp, error) {
	var o SupervisionOp
	err := sc.Scan(&o.ID, &o.IssuanceID, &o.AssetID, &o.Kind, &o.Target, &o.TargetHash, &o.Reason,
		&o.OrderHash, &o.RefID, &o.FundTxid, &o.FundVout, &o.FundAtoms, &o.Sighash, &o.RecordScript,
		&o.RecordVout, &o.RawTx, &o.Txid, &o.State, &o.Channel, &o.Error, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// --- corporate actions -------------------------------------------------------

// CorporateAction is one W-3 action (a dividend or a vote) against a live
// asset. State machine: awaiting_snapshot -> snapshotted; a dividend
// additionally carries a fund-first deposit (Funded flips when the issuer's
// deposit confirms and covers the pool; nothing pays before that).
type CorporateAction struct {
	ID             string `json:"id"`
	IssuanceID     string `json:"issuance_id"`
	AssetID        string `json:"asset_id"`
	Kind           string `json:"kind"` // dividend | vote
	State          string `json:"state"`
	RecordHeight   int64  `json:"record_height"`
	DivAsset       string `json:"div_asset,omitempty"`
	DivTotalAtoms  uint64 `json:"div_total_atoms,omitempty"`
	DepositAddress string `json:"deposit_address,omitempty"`
	Funded         bool   `json:"funded"`
	FundedAtoms    uint64 `json:"funded_atoms,omitempty"`
	FundedTxid     string `json:"funded_txid,omitempty"`
	Question       string `json:"question,omitempty"`
	Choices        string `json:"-"` // JSON array of strings
	ClosesHeight   int64  `json:"closes_height,omitempty"`
	SnapshotHeight int64  `json:"snapshot_height,omitempty"`
	SnapshotTotal  uint64 `json:"snapshot_total,omitempty"`
	SnapshotCount  int64  `json:"snapshot_count,omitempty"`
	SnapshotHash   string `json:"snapshot_hash,omitempty"`
	SnapshotNote   string `json:"snapshot_note,omitempty"`
	AnchorTxid     string `json:"anchor_txid,omitempty"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

// ChoiceList decodes the stored choices JSON.
func (a *CorporateAction) ChoiceList() []string {
	var out []string
	_ = json.Unmarshal([]byte(a.Choices), &out)
	return out
}

const actionCols = `id, issuance_id, asset_id, kind, state, record_height, div_asset, div_total_atoms,
    deposit_address, funded, funded_atoms, funded_txid, question, choices, closes_height,
    snapshot_height, snapshot_total, snapshot_count, snapshot_hash, snapshot_note, anchor_txid,
    created_at, updated_at`

func (s *Store) InsertAction(a *CorporateAction) error {
	now := time.Now().Unix()
	a.CreatedAt, a.UpdatedAt = now, now
	if a.Choices == "" {
		a.Choices = "[]"
	}
	_, err := s.db.Exec(
		`INSERT INTO corporate_actions (`+actionCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.IssuanceID, a.AssetID, a.Kind, a.State, a.RecordHeight, a.DivAsset, a.DivTotalAtoms,
		a.DepositAddress, boolInt(a.Funded), a.FundedAtoms, a.FundedTxid, a.Question, a.Choices,
		a.ClosesHeight, a.SnapshotHeight, a.SnapshotTotal, a.SnapshotCount, a.SnapshotHash,
		a.SnapshotNote, a.AnchorTxid, a.CreatedAt, a.UpdatedAt)
	return err
}

func (s *Store) UpdateActionFields(id string, fields map[string]any) error {
	return s.updateFields("corporate_actions", "id", id, fields)
}

func (s *Store) ActionByID(id string) (*CorporateAction, error) {
	return scanAction(s.db.QueryRow(`SELECT `+actionCols+` FROM corporate_actions WHERE id = ?`, id))
}

func (s *Store) ActionsByIssuance(issuanceID string) ([]*CorporateAction, error) {
	return s.queryActions(`SELECT `+actionCols+` FROM corporate_actions WHERE issuance_id = ? ORDER BY created_at`, issuanceID)
}

func (s *Store) ActionsByState(state string) ([]*CorporateAction, error) {
	return s.queryActions(`SELECT `+actionCols+` FROM corporate_actions WHERE state = ? ORDER BY created_at`, state)
}

// UnfundedDividendActions returns dividend actions still awaiting their issuer
// deposit (the moneywatch fund-first surface).
func (s *Store) UnfundedDividendActions() ([]*CorporateAction, error) {
	return s.queryActions(
		`SELECT ` + actionCols + ` FROM corporate_actions
         WHERE kind = 'dividend' AND funded = 0 AND deposit_address != '' ORDER BY created_at`)
}

// FundedDividendActions returns funded dividend actions (whose registered
// claims are payable).
func (s *Store) FundedDividendActions() ([]*CorporateAction, error) {
	return s.queryActions(
		`SELECT ` + actionCols + ` FROM corporate_actions
         WHERE kind = 'dividend' AND funded = 1 ORDER BY created_at`)
}

func (s *Store) queryActions(q string, args ...any) ([]*CorporateAction, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*CorporateAction{}
	for rows.Next() {
		a, err := scanActionInto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func scanAction(row *sql.Row) (*CorporateAction, error) {
	a, err := scanActionInto(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return a, err
}

func scanActionInto(sc scanner) (*CorporateAction, error) {
	var a CorporateAction
	var funded int
	err := sc.Scan(&a.ID, &a.IssuanceID, &a.AssetID, &a.Kind, &a.State, &a.RecordHeight, &a.DivAsset,
		&a.DivTotalAtoms, &a.DepositAddress, &funded, &a.FundedAtoms, &a.FundedTxid, &a.Question,
		&a.Choices, &a.ClosesHeight, &a.SnapshotHeight, &a.SnapshotTotal, &a.SnapshotCount,
		&a.SnapshotHash, &a.SnapshotNote, &a.AnchorTxid, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	a.Funded = funded != 0
	return &a, nil
}

// --- action snapshots --------------------------------------------------------

// SnapshotRow is one UTXO of the record-date snapshot: the outpoint, its
// scriptPubKey (what a holding proof must derive), and its asset atoms.
type SnapshotRow struct {
	ActionID string `json:"-"`
	Outpoint string `json:"outpoint"` // txid:vout
	Script   string `json:"script"`
	Atoms    uint64 `json:"atoms"`
}

// InsertSnapshotRowIfAbsent freezes one snapshot row. ON CONFLICT DO NOTHING
// makes a crashed-and-resumed snapshot idempotent, exactly like the
// distribution engine's per-holder rows.
func (s *Store) InsertSnapshotRowIfAbsent(r *SnapshotRow) error {
	_, err := s.db.Exec(
		`INSERT INTO action_snapshots (action_id, outpoint, script, atoms) VALUES (?,?,?,?)
         ON CONFLICT(action_id, outpoint) DO NOTHING`,
		r.ActionID, r.Outpoint, r.Script, r.Atoms)
	return err
}

func (s *Store) SnapshotRows(actionID string) ([]*SnapshotRow, error) {
	rows, err := s.db.Query(
		`SELECT action_id, outpoint, script, atoms FROM action_snapshots WHERE action_id = ? ORDER BY outpoint`, actionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SnapshotRow{}
	for rows.Next() {
		var r SnapshotRow
		if err := rows.Scan(&r.ActionID, &r.Outpoint, &r.Script, &r.Atoms); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

func (s *Store) SnapshotRow(actionID, outpoint string) (*SnapshotRow, error) {
	var r SnapshotRow
	err := s.db.QueryRow(
		`SELECT action_id, outpoint, script, atoms FROM action_snapshots WHERE action_id = ? AND outpoint = ?`,
		actionID, outpoint).Scan(&r.ActionID, &r.Outpoint, &r.Script, &r.Atoms)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// --- action claims -----------------------------------------------------------

// ActionClaim is one holder's verified claim over a set of snapshot outpoints:
// for a dividend it is the payment record (fund-safety anchored: state and the
// marker-scoped txid persist before broadcast); for a vote it is the weighted
// ballot. The public detail surface never exposes the AID.
type ActionClaim struct {
	ID            string `json:"id"`
	ActionID      string `json:"action_id"`
	AID           string `json:"-"` // endpoint-KYC identity; never rendered publicly
	Pubkey        string `json:"pubkey"`
	Outpoints     string `json:"-"` // JSON array of txid:vout
	Atoms         uint64 `json:"atoms"`
	PayoutAddress string `json:"payout_address,omitempty"`
	Choice        string `json:"choice,omitempty"`
	GrossAtoms    uint64 `json:"gross_atoms,omitempty"`
	WithheldAtoms uint64 `json:"withheld_atoms,omitempty"`
	NetAtoms      uint64 `json:"net_atoms,omitempty"`
	TreatyBps     int64  `json:"treaty_bps,omitempty"`
	TaxStatus     string `json:"tax_status,omitempty"`
	Sig           string `json:"-"`
	State         string `json:"state"`
	Txid          string `json:"txid,omitempty"`
	Reason        string `json:"reason,omitempty"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

func (c *ActionClaim) OutpointList() []string {
	var out []string
	_ = json.Unmarshal([]byte(c.Outpoints), &out)
	return out
}

const actionClaimCols = `id, action_id, aid, pubkey, outpoints, atoms, payout_address, choice,
    gross_atoms, withheld_atoms, net_atoms, treaty_bps, tax_status, sig, state, txid, reason,
    created_at, updated_at`

// InsertActionClaim writes the claim AND its per-outpoint rows in one
// transaction. The PRIMARY KEY on action_claim_outpoints(action_id, outpoint)
// is the one-claim-per-outpoint rule: a second claim naming any already-claimed
// outpoint fails the whole insert atomically (no partial claim survives).
// Returns (false, nil) on a double-claim.
func (s *Store) InsertActionClaim(c *ActionClaim, outAtoms map[string]uint64) (bool, error) {
	now := time.Now().Unix()
	c.CreatedAt, c.UpdatedAt = now, now
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(
		`INSERT INTO action_claims (`+actionClaimCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.ActionID, c.AID, c.Pubkey, c.Outpoints, c.Atoms, c.PayoutAddress, c.Choice,
		c.GrossAtoms, c.WithheldAtoms, c.NetAtoms, c.TreatyBps, c.TaxStatus, c.Sig, c.State,
		c.Txid, c.Reason, c.CreatedAt, c.UpdatedAt); err != nil {
		tx.Rollback()
		return false, err
	}
	for _, op := range c.OutpointList() {
		if _, err := tx.Exec(
			`INSERT INTO action_claim_outpoints (action_id, outpoint, claim_id, atoms) VALUES (?,?,?,?)`,
			c.ActionID, op, c.ID, outAtoms[op]); err != nil {
			tx.Rollback()
			if isUniqueViolation(err) {
				return false, nil
			}
			return false, err
		}
	}
	return true, tx.Commit()
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "constraint")
}

func (s *Store) UpdateActionClaimFields(id string, fields map[string]any) error {
	return s.updateFields("action_claims", "id", id, fields)
}

func (s *Store) ActionClaimByID(id string) (*ActionClaim, error) {
	return scanActionClaim(s.db.QueryRow(`SELECT `+actionClaimCols+` FROM action_claims WHERE id = ?`, id))
}

func (s *Store) ActionClaimsByAction(actionID string) ([]*ActionClaim, error) {
	rows, err := s.db.Query(
		`SELECT `+actionClaimCols+` FROM action_claims WHERE action_id = ? ORDER BY created_at`, actionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ActionClaim{}
	for rows.Next() {
		c, err := scanActionClaimInto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanActionClaim(row *sql.Row) (*ActionClaim, error) {
	c, err := scanActionClaimInto(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

func scanActionClaimInto(sc scanner) (*ActionClaim, error) {
	var c ActionClaim
	err := sc.Scan(&c.ID, &c.ActionID, &c.AID, &c.Pubkey, &c.Outpoints, &c.Atoms, &c.PayoutAddress,
		&c.Choice, &c.GrossAtoms, &c.WithheldAtoms, &c.NetAtoms, &c.TreatyBps, &c.TaxStatus, &c.Sig,
		&c.State, &c.Txid, &c.Reason, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// --- escrow fee accrual (W-6) ------------------------------------------------

// AccruedEscrowFee returns the escrow fee already on the books for a
// subscription (the kind='escrow_fee' ledger row, written at deposit
// confirmation since W-6, or at release by the pre-W-6 path). ok=false means no
// fee has been recorded yet, so a closer falls back to computing one.
func (s *Store) AccruedEscrowFee(subID string) (uint64, bool, error) {
	var amount uint64
	err := s.db.QueryRow(
		`SELECT amount FROM escrow_ledger WHERE subscription_id = ? AND kind = 'escrow_fee'
         ORDER BY created_at LIMIT 1`, subID).Scan(&amount)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return amount, true, nil
}
