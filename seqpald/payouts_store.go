package main

import (
	"database/sql"
	"strings"
	"time"
)

// Persistence for payouts to many wallets at once (payouts.go): one row per
// payout, one row per holder or subscriber in it.

// Payout is one payment that pays many wallets at once: a distribution run's
// holders, or a refund to an offering's subscribers. The issuer's own wallet
// makes it; this record is what the platform planned and what it has seen
// happen since.
//
// State: planned (the list is settled, nothing paid) -> paying (the issuer's
// wallet submitted it and the reference names exactly the list) -> paid (it
// went out: every listed wallet's allocation is its own) -> complete (no
// allocation is waiting to be collected: each was collected or came back).
type Payout struct {
	ID             string `json:"id"`
	IssuanceID     string `json:"issuance_id"`
	Kind           string `json:"kind"` // distribution | refund
	SourceID       string `json:"source_id,omitempty"`
	Asset          string `json:"asset"`
	State          string `json:"state"`
	Reference      string `json:"reference,omitempty"`
	TotalAtoms     uint64 `json:"total_atoms"`
	Holders        int    `json:"holders"`
	LeftOut        int    `json:"left_out"`
	FeeBound       uint64 `json:"fee_bound"`
	RoundTxid      string `json:"round_txid,omitempty"`
	Expiry         int64  `json:"expiry,omitempty"`
	Deadline       int64  `json:"deadline,omitempty"`
	CollectedAtoms uint64 `json:"collected_atoms"`
	ReturnedAtoms  uint64 `json:"returned_atoms"`
	ReturnTxids    string `json:"return_txids,omitempty"`
	Memo           string `json:"memo,omitempty"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

// PayoutRow is one wallet in a payout: who, under which key, how much, and
// where its allocation stands.
//
// State: planned | left_out (not in the payment; Reason says why) | pending
// (in the submitted payment, not yet out) | paid (its own, to be collected by
// its wallet) | collected | returned (not collected by the deadline, paid back
// to the issuer in ReturnedIn).
type PayoutRow struct {
	PayoutID       string `json:"payout_id"`
	HolderAID      string `json:"holder_aid"`
	SubscriptionID string `json:"subscription_id,omitempty"`
	Key            string `json:"key,omitempty"`
	Atoms          uint64 `json:"atoms"`
	State          string `json:"state"`
	Reason         string `json:"reason,omitempty"`
	ClaimIndex     int    `json:"claim_index"`
	LeafID         string `json:"leaf_id,omitempty"`
	ReturnedIn     string `json:"returned_in,omitempty"`
}

const payoutCols = `id, issuance_id, kind, source_id, asset, state, reference, total_atoms, holders, left_out,
    fee_bound, round_txid, expiry, deadline, collected_atoms, returned_atoms, return_txids, memo, created_at, updated_at`

const payoutRowCols = `payout_id, holder_aid, subscription_id, named_key, atoms, state, reason, claim_index, leaf_id, returned_in`

func (s *Store) InsertPayout(p *Payout, rows []*PayoutRow) error {
	now := time.Now().Unix()
	p.CreatedAt, p.UpdatedAt = now, now
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO payouts (`+payoutCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.IssuanceID, p.Kind, p.SourceID, p.Asset, p.State, p.Reference, p.TotalAtoms, p.Holders, p.LeftOut,
		p.FeeBound, p.RoundTxid, p.Expiry, p.Deadline, p.CollectedAtoms, p.ReturnedAtoms, p.ReturnTxids, p.Memo,
		p.CreatedAt, p.UpdatedAt); err != nil {
		tx.Rollback()
		return err
	}
	for _, r := range rows {
		r.PayoutID = p.ID
		if _, err := tx.Exec(`INSERT INTO payout_rows (`+payoutRowCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			r.PayoutID, r.HolderAID, r.SubscriptionID, r.Key, r.Atoms, r.State, r.Reason, r.ClaimIndex, r.LeafID,
			r.ReturnedIn); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpdatePayoutFields(id string, fields map[string]any) error {
	return s.updateFields("payouts", "id", id, fields)
}

func (s *Store) UpdatePayoutRowFields(payoutID, holderAID string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	var sets []string
	var args []any
	for k, v := range fields {
		sets = append(sets, k+" = ?")
		args = append(args, v)
	}
	args = append(args, payoutID, holderAID)
	_, err := s.db.Exec(`UPDATE payout_rows SET `+strings.Join(sets, ", ")+` WHERE payout_id = ? AND holder_aid = ?`, args...)
	return err
}

func scanPayout(sc scanner) (*Payout, error) {
	var p Payout
	err := sc.Scan(&p.ID, &p.IssuanceID, &p.Kind, &p.SourceID, &p.Asset, &p.State, &p.Reference, &p.TotalAtoms,
		&p.Holders, &p.LeftOut, &p.FeeBound, &p.RoundTxid, &p.Expiry, &p.Deadline, &p.CollectedAtoms, &p.ReturnedAtoms,
		&p.ReturnTxids, &p.Memo, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) PayoutByID(id string) (*Payout, error) {
	p, err := scanPayout(s.db.QueryRow(`SELECT `+payoutCols+` FROM payouts WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// PayoutBySource is the payout of one distribution run, if it has one.
func (s *Store) PayoutBySource(kind, sourceID string) (*Payout, error) {
	p, err := scanPayout(s.db.QueryRow(`SELECT `+payoutCols+` FROM payouts WHERE kind = ? AND source_id = ?`, kind, sourceID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

func (s *Store) payoutsWhere(where string, args ...any) ([]*Payout, error) {
	rows, err := s.db.Query(`SELECT `+payoutCols+` FROM payouts WHERE `+where+` ORDER BY created_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Payout{}
	for rows.Next() {
		p, err := scanPayout(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) PayoutsByIssuance(issuanceID string) ([]*Payout, error) {
	return s.payoutsWhere(`issuance_id = ?`, issuanceID)
}

// PayoutsFollowed are the payouts whose allocations can still change: those
// submitted and not yet complete.
func (s *Store) PayoutsFollowed() ([]*Payout, error) {
	return s.payoutsWhere(`state IN ('paying', 'paid')`)
}

func (s *Store) PayoutRows(payoutID string) ([]*PayoutRow, error) {
	rows, err := s.db.Query(`SELECT `+payoutRowCols+` FROM payout_rows WHERE payout_id = ? ORDER BY claim_index, holder_aid`, payoutID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*PayoutRow{}
	for rows.Next() {
		var r PayoutRow
		if err := rows.Scan(&r.PayoutID, &r.HolderAID, &r.SubscriptionID, &r.Key, &r.Atoms, &r.State, &r.Reason,
			&r.ClaimIndex, &r.LeafID, &r.ReturnedIn); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

// EnclaveKeyOfAID is the one enclave key a SeqPal ID holds for the OpenAMP
// account `enclaveAID`, or "" when no ID here holds it.
func (s *Store) EnclaveKeyOfAID(enclaveAID string) (string, error) {
	rows, err := s.db.Query(`SELECT xonly FROM account_wallets WHERE kind = 'enclave' AND enclave_aid = ?`, enclaveAID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return "", err
		}
		keys = append(keys, strings.ToLower(k))
	}
	if len(keys) != 1 {
		return "", rows.Err()
	}
	return keys[0], rows.Err()
}
