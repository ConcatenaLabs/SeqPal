package main

import (
	"database/sql"
	"time"
)

// M7 persistence: the INVESTOR-side payout mandates and the distribution engine's
// runs + per-holder payment records. The distribution_payments row is the M7
// idempotency anchor, the exact analogue of the M5/M6 settlement record: exactly
// one row per (run, holder), its txid recorded before broadcast, so an ambiguous
// timeout is reconciled by a chain scan (escrowFindSend on the per-holder marker
// comment) before any retry, never a double-pay.

// --- investor payout mandates ------------------------------------------------

// InvestorMandate is one investor's BIP340-signed instruction to pay their
// distributions to an ordinary address on a given chain. It is DISTINCT from the
// issuer PayoutMandate (money_store.go): that one is keyed by (issuance, chain)
// and receives escrow release; this one is keyed by (investor_aid, chain) and
// receives distribution payouts. An enclave key-path (2-of-2) address is rejected
// at registration; no ordinary wallet scans an enclave address, so paying one
// would strand the funds.
type InvestorMandate struct {
	InvestorAID string `json:"investor_aid"`
	Chain       string `json:"chain"` // sequentia (bitcoin cut for M7)
	Asset       string `json:"asset,omitempty"`
	Address     string `json:"address"`
	Signature   string `json:"signature,omitempty"`
	SignerXOnly string `json:"signer_xonly,omitempty"`
	// What checks this signature: the key for a tagged one, or the address an
	// ordinary signed message verifies for. A record with neither is a signature
	// nobody can check.
	SignerAddress string `json:"signer_address,omitempty"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

func (s *Store) UpsertInvestorMandate(m *InvestorMandate) error {
	now := time.Now().Unix()
	if m.CreatedAt == 0 {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	_, err := s.db.Exec(
		`INSERT INTO investor_mandates (investor_aid, chain, asset, address, signature, signer_xonly, signer_address, created_at, updated_at)
         VALUES (?,?,?,?,?,?,?,?,?)
         ON CONFLICT(investor_aid, chain) DO UPDATE SET
            asset=excluded.asset, address=excluded.address, signature=excluded.signature,
            signer_xonly=excluded.signer_xonly, signer_address=excluded.signer_address,
            updated_at=excluded.updated_at`,
		m.InvestorAID, m.Chain, m.Asset, m.Address, m.Signature, m.SignerXOnly,
		m.SignerAddress, m.CreatedAt, m.UpdatedAt)
	return err
}

func (s *Store) InvestorMandateFor(investorAID, chain string) (*InvestorMandate, error) {
	var m InvestorMandate
	err := s.db.QueryRow(
		`SELECT investor_aid, chain, asset, address, signature, signer_xonly, signer_address, created_at, updated_at
         FROM investor_mandates WHERE investor_aid = ? AND chain = ?`, investorAID, chain).
		Scan(&m.InvestorAID, &m.Chain, &m.Asset, &m.Address, &m.Signature, &m.SignerXOnly, &m.SignerAddress, &m.CreatedAt, &m.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// --- distribution runs -------------------------------------------------------

// Distribution is one servicing run for an issuance's asset, USDX only. State
// machine: awaiting_funding -> funded -> snapshotted -> paying -> complete. The
// run BLOCKS (pays nothing) until the servicing deposit confirms and covers the
// pool. The snapshot (holder set + per-holder atoms at a Sequentia block height)
// is immutable once taken.
type Distribution struct {
	ID              string `json:"id"`
	IssuanceID      string `json:"issuance_id"`
	AssetID         string `json:"asset_id"`
	PoolAtoms       uint64 `json:"pool_atoms"`
	Memo            string `json:"memo,omitempty"`
	State           string `json:"state"`
	DepositAddress  string `json:"deposit_address,omitempty"`
	FundedAtoms     uint64 `json:"funded_atoms"`
	FundedTxid      string `json:"funded_txid,omitempty"`
	SnapshotHeight  int64  `json:"snapshot_height"`
	TotalHeld       uint64 `json:"total_held"`
	DustAtoms       uint64 `json:"dust_atoms"`
	GrossTotal      uint64 `json:"gross_total"`
	WithheldTotal   uint64 `json:"withheld_total"`
	NetTotal        uint64 `json:"net_total"`
	DividendEquiv   bool   `json:"dividend_equivalent"`
	StatementHash   string `json:"statement_hash,omitempty"`
	WithholdingHash string `json:"withholding_hash,omitempty"`
	CRSHash         string `json:"crs_hash,omitempty"`
	AnchorTxid      string `json:"anchor_txid,omitempty"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
}

const distCols = `id, issuance_id, asset_id, pool_atoms, memo, state, deposit_address, funded_atoms,
    funded_txid, snapshot_height, total_held, dust_atoms, gross_total, withheld_total, net_total,
    dividend_equiv, statement_hash, withholding_hash, crs_hash, anchor_txid, created_at, updated_at`

func (s *Store) InsertDistribution(d *Distribution) error {
	now := time.Now().Unix()
	d.CreatedAt, d.UpdatedAt = now, now
	_, err := s.db.Exec(
		`INSERT INTO distributions (`+distCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.IssuanceID, d.AssetID, d.PoolAtoms, d.Memo, d.State, d.DepositAddress, d.FundedAtoms,
		d.FundedTxid, d.SnapshotHeight, d.TotalHeld, d.DustAtoms, d.GrossTotal, d.WithheldTotal, d.NetTotal,
		boolInt(d.DividendEquiv), d.StatementHash, d.WithholdingHash, d.CRSHash, d.AnchorTxid, d.CreatedAt, d.UpdatedAt)
	return err
}

func (s *Store) UpdateDistributionFields(id string, fields map[string]any) error {
	return s.updateFields("distributions", "id", id, fields)
}

func (s *Store) DistributionByID(id string) (*Distribution, error) {
	return scanDist(s.db.QueryRow(`SELECT `+distCols+` FROM distributions WHERE id = ?`, id))
}

func (s *Store) DistributionsByIssuance(issuanceID string) ([]*Distribution, error) {
	rows, err := s.db.Query(`SELECT `+distCols+` FROM distributions WHERE issuance_id = ? ORDER BY created_at DESC`, issuanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Distribution{}
	for rows.Next() {
		d, err := scanDistInto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DistributionsByState returns runs in a given state (the funding watcher scans
// awaiting_funding rows).
func (s *Store) DistributionsByState(state string) ([]*Distribution, error) {
	rows, err := s.db.Query(`SELECT `+distCols+` FROM distributions WHERE state = ? ORDER BY created_at`, state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Distribution{}
	for rows.Next() {
		d, err := scanDistInto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func scanDist(row *sql.Row) (*Distribution, error) {
	d, err := scanDistInto(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return d, err
}

func scanDistInto(sc scanner) (*Distribution, error) {
	var d Distribution
	var dividend int
	err := sc.Scan(&d.ID, &d.IssuanceID, &d.AssetID, &d.PoolAtoms, &d.Memo, &d.State, &d.DepositAddress,
		&d.FundedAtoms, &d.FundedTxid, &d.SnapshotHeight, &d.TotalHeld, &d.DustAtoms, &d.GrossTotal,
		&d.WithheldTotal, &d.NetTotal, &dividend, &d.StatementHash, &d.WithholdingHash, &d.CRSHash,
		&d.AnchorTxid, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, err
	}
	d.DividendEquiv = dividend != 0
	return &d, nil
}

// --- per-holder distribution payments (idempotency anchor) -------------------

// DistPayment is the exactly-one-per-(run, holder) payment record, the M7
// analogue of the M5 settlement. It carries the snapshot balance and the
// pro-rata + withholding math, and it is the reconciliation anchor for the
// on-chain payout: state=paying is written before the send so a lost write is
// reconciled by escrowFindSend on the per-holder marker comment. state:
// pending -> paying -> paid | skipped (no mandate) | no_payment (net zero) |
// failed (retryable).
type DistPayment struct {
	RunID          string `json:"run_id"`
	HolderAID      string `json:"holder_aid"`
	BalanceAtoms   uint64 `json:"balance_atoms"`
	GrossAtoms     uint64 `json:"gross_atoms"`
	WithheldAtoms  uint64 `json:"withheld_atoms"`
	NetAtoms       uint64 `json:"net_atoms"`
	TreatyBps      int64  `json:"treaty_bps"`
	TaxStatus      string `json:"tax_status"`
	MandateAddress string `json:"mandate_address,omitempty"`
	State          string `json:"state"`
	Txid           string `json:"txid,omitempty"`
	Reason         string `json:"reason,omitempty"`
	StatementHash  string `json:"statement_hash,omitempty"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

const distPayCols = `run_id, holder_aid, balance_atoms, gross_atoms, withheld_atoms, net_atoms,
    treaty_bps, tax_status, mandate_address, state, txid, reason, statement_hash, created_at, updated_at`

// InsertDistPaymentIfAbsent inserts a snapshot payment row and reports whether it
// created it (true) or one already existed (false). The ON CONFLICT DO NOTHING is
// what makes the snapshot immutable and idempotent per run: a re-run finds the
// existing rows and never recomputes the register.
func (s *Store) InsertDistPaymentIfAbsent(p *DistPayment) (bool, error) {
	now := time.Now().Unix()
	if p.CreatedAt == 0 {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	res, err := s.db.Exec(
		`INSERT INTO distribution_payments (`+distPayCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
         ON CONFLICT(run_id, holder_aid) DO NOTHING`,
		p.RunID, p.HolderAID, p.BalanceAtoms, p.GrossAtoms, p.WithheldAtoms, p.NetAtoms, p.TreatyBps,
		p.TaxStatus, p.MandateAddress, p.State, p.Txid, p.Reason, p.StatementHash, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) UpdateDistPaymentFields(runID, holderAID string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	cols := make([]string, 0, len(fields))
	for k := range fields {
		cols = append(cols, k)
	}
	set := ""
	args := make([]any, 0, len(cols)+3)
	for _, c := range cols {
		if set != "" {
			set += ", "
		}
		set += c + " = ?"
		args = append(args, fields[c])
	}
	set += ", updated_at = ?"
	args = append(args, time.Now().Unix(), runID, holderAID)
	_, err := s.db.Exec(`UPDATE distribution_payments SET `+set+` WHERE run_id = ? AND holder_aid = ?`, args...)
	return err
}

func (s *Store) DistPaymentsByRun(runID string) ([]*DistPayment, error) {
	rows, err := s.db.Query(`SELECT `+distPayCols+` FROM distribution_payments WHERE run_id = ? ORDER BY holder_aid`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*DistPayment{}
	for rows.Next() {
		var p DistPayment
		if err := rows.Scan(&p.RunID, &p.HolderAID, &p.BalanceAtoms, &p.GrossAtoms, &p.WithheldAtoms,
			&p.NetAtoms, &p.TreatyBps, &p.TaxStatus, &p.MandateAddress, &p.State, &p.Txid, &p.Reason,
			&p.StatementHash, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// DistPaymentCount returns how many payment rows exist for a run (the snapshot
// idempotency probe).
func (s *Store) DistPaymentCount(runID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM distribution_payments WHERE run_id = ?`, runID).Scan(&n)
	return n, err
}
