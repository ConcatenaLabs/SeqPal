package main

import (
	"database/sql"
	"encoding/json"
	"time"
)

// M5 money-engine persistence. Every real-money fact (a subscription, an escrow
// deposit, a settlement, a fiat payment, a platform-fee invoice, a payout
// mandate, an offeree count) lives here, never in the browser. The settlement
// record is the idempotency anchor: exactly one row per subscription, its txids
// recorded pre-broadcast so an ambiguous timeout is reconciled by a chain scan
// before any retry, never a double-deliver or a double-release.

// --- subscription ------------------------------------------------------------

// Subscription is one investor's commitment to an offering on one rail. State
// machine: created -> in_escrow (deposit confirmed at N confs / fiat settled) ->
// settled (delivered at close) | refunded | expired. Nothing is settled until
// close. Amounts: TokenAtoms is the purchased amount in asset atoms; PayAmount is
// the expected payment in the rail's base unit (USDX atoms, tBTC sats, or fiat
// minor units); FundsSimulated flags a fiat-funded row on every derived surface.
type Subscription struct {
	ID             string  `json:"id"`
	IssuanceID     string  `json:"issuance_id"`
	InvestorAID    string  `json:"investor_aid"`
	Rail           string  `json:"rail"` // usdx | btc | card | bank
	TokenAtoms     uint64  `json:"token_atoms"`
	PayAmount      uint64  `json:"pay_amount"`
	PayCcy         string  `json:"pay_ccy"`
	DepositAddress string  `json:"deposit_address,omitempty"`
	RefundAddress  string  `json:"refund_address,omitempty"`
	State          string  `json:"state"`
	FundsSimulated bool    `json:"funds_simulated"`
	DepositTxid    string  `json:"deposit_txid,omitempty"`
	Confirmations  int64   `json:"confirmations"`
	DepositedAtoms uint64  `json:"deposited_atoms,omitempty"`
	USDRate        float64 `json:"usd_rate,omitempty"` // captured USD/BTC at confirmation (btc rail)
	CreatedAt      int64   `json:"created_at"`
	UpdatedAt      int64   `json:"updated_at"`
}

const subCols = `id, issuance_id, investor_aid, rail, token_atoms, pay_amount, pay_ccy,
    deposit_address, refund_address, state, funds_simulated, deposit_txid, confirmations,
    deposited_atoms, usd_rate, created_at, updated_at`

func (s *Store) InsertSubscription(sub *Subscription) error {
	now := time.Now().Unix()
	sub.CreatedAt, sub.UpdatedAt = now, now
	_, err := s.db.Exec(
		`INSERT INTO subscriptions (`+subCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sub.ID, sub.IssuanceID, sub.InvestorAID, sub.Rail, sub.TokenAtoms, sub.PayAmount, sub.PayCcy,
		sub.DepositAddress, sub.RefundAddress, sub.State, boolInt(sub.FundsSimulated), sub.DepositTxid,
		sub.Confirmations, sub.DepositedAtoms, sub.USDRate, sub.CreatedAt, sub.UpdatedAt)
	return err
}

func (s *Store) UpdateSubscriptionFields(id string, fields map[string]any) error {
	return s.updateFields("subscriptions", "id", id, fields)
}

func (s *Store) SubscriptionByID(id string) (*Subscription, error) {
	return scanSub(s.db.QueryRow(`SELECT `+subCols+` FROM subscriptions WHERE id = ?`, id))
}

// SubscriptionsByState returns every subscription in a given state and rail set
// (the deposit watcher scans created usdx/btc rows; the closing engine scans
// in_escrow rows for an issuance).
func (s *Store) SubscriptionsByState(state string) ([]*Subscription, error) {
	return s.querySubs(`SELECT `+subCols+` FROM subscriptions WHERE state = ? ORDER BY created_at`, state)
}

func (s *Store) SubscriptionsByIssuanceState(issuanceID, state string) ([]*Subscription, error) {
	return s.querySubs(`SELECT `+subCols+` FROM subscriptions WHERE issuance_id = ? AND state = ? ORDER BY created_at`, issuanceID, state)
}

func (s *Store) SubscriptionsByIssuance(issuanceID string) ([]*Subscription, error) {
	return s.querySubs(`SELECT `+subCols+` FROM subscriptions WHERE issuance_id = ? ORDER BY created_at`, issuanceID)
}

func (s *Store) SubscriptionsByInvestor(aid string) ([]*Subscription, error) {
	return s.querySubs(`SELECT `+subCols+` FROM subscriptions WHERE investor_aid = ? ORDER BY created_at DESC`, aid)
}

func (s *Store) querySubs(q string, args ...any) ([]*Subscription, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Subscription{}
	for rows.Next() {
		sub, err := scanSubInto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

func scanSub(row *sql.Row) (*Subscription, error) {
	sub, err := scanSubInto(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return sub, err
}

func scanSubInto(sc scanner) (*Subscription, error) {
	var sub Subscription
	var simulated int
	err := sc.Scan(&sub.ID, &sub.IssuanceID, &sub.InvestorAID, &sub.Rail, &sub.TokenAtoms, &sub.PayAmount,
		&sub.PayCcy, &sub.DepositAddress, &sub.RefundAddress, &sub.State, &simulated, &sub.DepositTxid,
		&sub.Confirmations, &sub.DepositedAtoms, &sub.USDRate, &sub.CreatedAt, &sub.UpdatedAt)
	if err != nil {
		return nil, err
	}
	sub.FundsSimulated = simulated != 0
	return &sub, nil
}

// --- settlement (idempotency anchor) -----------------------------------------

// Settlement is the exactly-one-per-subscription closing record. It is created
// (CreateSettlementIfAbsent) before any money moves, so a replayed close finds
// the existing record and never re-delivers or re-releases. Each txid is written
// BEFORE broadcast; an ambiguous timeout is reconciled by a chain scan against
// the recorded txid before any retry.
type Settlement struct {
	SubscriptionID string `json:"subscription_id"`
	IssuanceID     string `json:"issuance_id"`
	State          string `json:"state"` // pending|delivered|released|settled|refunded|failed
	DeliveryTxid   string `json:"delivery_txid,omitempty"`
	ReleaseTxid    string `json:"release_txid,omitempty"`
	RefundTxid     string `json:"refund_txid,omitempty"`
	FeeAtoms       uint64 `json:"fee_atoms"`
	Error          string `json:"error,omitempty"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

// CreateSettlementIfAbsent inserts the pending settlement row for a subscription
// and reports whether it created it (true) or one already existed (false). The
// INSERT ... ON CONFLICT DO NOTHING is the atomic guard that makes close
// idempotent even under a concurrent double-submit.
func (s *Store) CreateSettlementIfAbsent(subID, issuanceID string) (bool, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(
		`INSERT INTO settlements (subscription_id, issuance_id, state, created_at, updated_at)
         VALUES (?,?,'pending',?,?) ON CONFLICT(subscription_id) DO NOTHING`,
		subID, issuanceID, now, now)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) SettlementByID(subID string) (*Settlement, error) {
	var st Settlement
	err := s.db.QueryRow(
		`SELECT subscription_id, issuance_id, state, delivery_txid, release_txid, refund_txid,
                fee_atoms, error, created_at, updated_at FROM settlements WHERE subscription_id = ?`, subID).
		Scan(&st.SubscriptionID, &st.IssuanceID, &st.State, &st.DeliveryTxid, &st.ReleaseTxid,
			&st.RefundTxid, &st.FeeAtoms, &st.Error, &st.CreatedAt, &st.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *Store) UpdateSettlementFields(subID string, fields map[string]any) error {
	return s.updateFields("settlements", "subscription_id", subID, fields)
}

// --- escrow ledger -----------------------------------------------------------

// LedgerEntry is an append-only money movement. Every deposit, release, refund
// and fee is recorded here; a fiat-derived entry carries funds_simulated=1 so
// every derived surface can render the SIMULATED label.
type LedgerEntry struct {
	ID             string `json:"id"`
	SubscriptionID string `json:"subscription_id,omitempty"`
	IssuanceID     string `json:"issuance_id,omitempty"`
	Kind           string `json:"kind"` // deposit|release|refund|delivery|setup_fee|escrow_fee
	Rail           string `json:"rail,omitempty"`
	Amount         uint64 `json:"amount"`
	Ccy            string `json:"ccy,omitempty"`
	Txid           string `json:"txid,omitempty"`
	FundsSimulated bool   `json:"funds_simulated"`
	CreatedAt      int64  `json:"created_at"`
}

func (s *Store) InsertLedger(e *LedgerEntry) error {
	if e.ID == "" {
		e.ID, _ = randHex(12)
	}
	e.CreatedAt = time.Now().Unix()
	_, err := s.db.Exec(
		`INSERT INTO escrow_ledger (id, subscription_id, issuance_id, kind, rail, amount, ccy, txid, funds_simulated, created_at)
         VALUES (?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.SubscriptionID, e.IssuanceID, e.Kind, e.Rail, e.Amount, e.Ccy, e.Txid,
		boolInt(e.FundsSimulated), e.CreatedAt)
	return err
}

func (s *Store) LedgerByIssuance(issuanceID string) ([]*LedgerEntry, error) {
	rows, err := s.db.Query(
		`SELECT id, subscription_id, issuance_id, kind, rail, amount, ccy, txid, funds_simulated, created_at
         FROM escrow_ledger WHERE issuance_id = ? ORDER BY created_at`, issuanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*LedgerEntry{}
	for rows.Next() {
		var e LedgerEntry
		var sim int
		if err := rows.Scan(&e.ID, &e.SubscriptionID, &e.IssuanceID, &e.Kind, &e.Rail, &e.Amount,
			&e.Ccy, &e.Txid, &sim, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.FundsSimulated = sim != 0
		out = append(out, &e)
	}
	return out, rows.Err()
}

// --- fiat simulated payments -------------------------------------------------

// FiatPayment is one SIMULATED card/bank checkout. It carries the same
// pending->settled lifecycle a real processor would, with a receipt and a refund
// path, and is always labeled simulated. It funds either a subscription or a
// platform-fee invoice.
type FiatPayment struct {
	ID             string `json:"id"`
	SubscriptionID string `json:"subscription_id,omitempty"`
	InvoiceID      string `json:"invoice_id,omitempty"`
	Rail           string `json:"rail"` // card | bank
	AmountMinor    uint64 `json:"amount_minor"`
	Ccy            string `json:"ccy"`
	State          string `json:"state"` // pending | settled | refunded
	Receipt        string `json:"receipt,omitempty"`
	CreatedAt      int64  `json:"created_at"`
	SettleAt       int64  `json:"settle_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

func (s *Store) InsertFiatPayment(p *FiatPayment) error {
	now := time.Now().Unix()
	p.CreatedAt, p.UpdatedAt = now, now
	_, err := s.db.Exec(
		`INSERT INTO fiat_payments (id, subscription_id, invoice_id, rail, amount_minor, ccy, state, receipt, created_at, settle_at, updated_at)
         VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.SubscriptionID, p.InvoiceID, p.Rail, p.AmountMinor, p.Ccy, p.State, p.Receipt,
		p.CreatedAt, p.SettleAt, p.UpdatedAt)
	return err
}

func (s *Store) FiatPaymentByID(id string) (*FiatPayment, error) {
	return scanFiat(s.db.QueryRow(
		`SELECT id, subscription_id, invoice_id, rail, amount_minor, ccy, state, receipt, created_at, settle_at, updated_at
         FROM fiat_payments WHERE id = ?`, id))
}

// PendingFiatDue returns SIMULATED payments whose settle_at has arrived (the fiat
// settlement cron flips them to settled).
func (s *Store) PendingFiatDue(now int64) ([]*FiatPayment, error) {
	rows, err := s.db.Query(
		`SELECT id, subscription_id, invoice_id, rail, amount_minor, ccy, state, receipt, created_at, settle_at, updated_at
         FROM fiat_payments WHERE state = 'pending' AND settle_at <= ? ORDER BY settle_at`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*FiatPayment{}
	for rows.Next() {
		p, err := scanFiatInto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UpdateFiatFields(id string, fields map[string]any) error {
	return s.updateFields("fiat_payments", "id", id, fields)
}

// FiatBySubscription returns the most recent fiat payment for a subscription, or
// nil (used by the fiat refund path).
func (s *Store) FiatBySubscription(subID string) (*FiatPayment, error) {
	return scanFiat(s.db.QueryRow(
		`SELECT id, subscription_id, invoice_id, rail, amount_minor, ccy, state, receipt, created_at, settle_at, updated_at
         FROM fiat_payments WHERE subscription_id = ? ORDER BY created_at DESC LIMIT 1`, subID))
}

func scanFiat(row *sql.Row) (*FiatPayment, error) {
	p, err := scanFiatInto(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

func scanFiatInto(sc scanner) (*FiatPayment, error) {
	var p FiatPayment
	err := sc.Scan(&p.ID, &p.SubscriptionID, &p.InvoiceID, &p.Rail, &p.AmountMinor, &p.Ccy, &p.State,
		&p.Receipt, &p.CreatedAt, &p.SettleAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// --- platform fee invoices ---------------------------------------------------

// FeeInvoice is a SeqPal platform fee owed by an issuer. The setup fee (kind
// "setup") must be paid before deploy; the escrow fee (kind "escrow") accrues on
// real balances and is deducted at release. Rail is the issuer's choice.
type FeeInvoice struct {
	ID             string  `json:"id"`
	IssuanceID     string  `json:"issuance_id"`
	Kind           string  `json:"kind"` // setup | escrow
	Rail           string  `json:"rail,omitempty"`
	AmountUSD      float64 `json:"amount_usd"`
	Amount         uint64  `json:"amount,omitempty"` // in rail base units once a rail is chosen
	Ccy            string  `json:"ccy,omitempty"`
	State          string  `json:"state"` // unpaid | paid
	Txid           string  `json:"txid,omitempty"`
	Address        string  `json:"address,omitempty"` // on-chain fee deposit address
	FundsSimulated bool    `json:"funds_simulated"`
	CreatedAt      int64   `json:"created_at"`
	PaidAt         int64   `json:"paid_at,omitempty"`
}

func (s *Store) InsertFeeInvoice(f *FeeInvoice) error {
	f.CreatedAt = time.Now().Unix()
	_, err := s.db.Exec(
		`INSERT INTO fee_invoices (id, issuance_id, kind, rail, amount_usd, amount, ccy, state, txid, address, funds_simulated, created_at, paid_at)
         VALUES (?,?,?,?,?,?,?,?,?,?,?,?,0)`,
		f.ID, f.IssuanceID, f.Kind, f.Rail, f.AmountUSD, f.Amount, f.Ccy, f.State, f.Txid, f.Address,
		boolInt(f.FundsSimulated), f.CreatedAt)
	return err
}

func (s *Store) UpdateFeeInvoiceFields(id string, fields map[string]any) error {
	return s.updateFields("fee_invoices", "id", id, fields)
}

func (s *Store) FeeInvoiceByID(id string) (*FeeInvoice, error) {
	return scanFeeInvoice(s.db.QueryRow(feeInvoiceSelect+` WHERE id = ?`, id))
}

// SetupFeeForIssuance returns the setup invoice for an issuance (there is at most
// one), or nil.
func (s *Store) SetupFeeForIssuance(issuanceID string) (*FeeInvoice, error) {
	return scanFeeInvoice(s.db.QueryRow(feeInvoiceSelect+` WHERE issuance_id = ? AND kind = 'setup' LIMIT 1`, issuanceID))
}

func (s *Store) FeeInvoicesByIssuance(issuanceID string) ([]*FeeInvoice, error) {
	rows, err := s.db.Query(feeInvoiceSelect+` WHERE issuance_id = ? ORDER BY created_at`, issuanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*FeeInvoice{}
	for rows.Next() {
		f, err := scanFeeInvoiceInto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// UnpaidOnchainFees returns unpaid fee invoices with an on-chain deposit address
// (the deposit watcher credits them when a payment confirms).
func (s *Store) UnpaidOnchainFees() ([]*FeeInvoice, error) {
	rows, err := s.db.Query(feeInvoiceSelect + ` WHERE state = 'unpaid' AND address != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*FeeInvoice{}
	for rows.Next() {
		f, err := scanFeeInvoiceInto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

const feeInvoiceSelect = `SELECT id, issuance_id, kind, rail, amount_usd, amount, ccy, state, txid, address, funds_simulated, created_at, paid_at FROM fee_invoices`

func scanFeeInvoice(row *sql.Row) (*FeeInvoice, error) {
	f, err := scanFeeInvoiceInto(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return f, err
}

func scanFeeInvoiceInto(sc scanner) (*FeeInvoice, error) {
	var f FeeInvoice
	var sim int
	err := sc.Scan(&f.ID, &f.IssuanceID, &f.Kind, &f.Rail, &f.AmountUSD, &f.Amount, &f.Ccy, &f.State,
		&f.Txid, &f.Address, &sim, &f.CreatedAt, &f.PaidAt)
	if err != nil {
		return nil, err
	}
	f.FundsSimulated = sim != 0
	return &f, nil
}

// --- payout mandates ---------------------------------------------------------

// PayoutMandate is the issuer's BIP340-signed instruction to pay escrow release
// to an ordinary address on a given chain. An enclave key-path address is
// rejected (no wallet scans it). One mandate per (issuance, chain).
type PayoutMandate struct {
	IssuanceID  string `json:"issuance_id"`
	Chain       string `json:"chain"` // sequentia | bitcoin
	Asset       string `json:"asset,omitempty"`
	Address     string `json:"address"`
	Signature   string `json:"signature,omitempty"`
	SignerXOnly string `json:"signer_xonly,omitempty"`
	// What checks this signature: the key for a tagged one, or the address an
	// ordinary signed message verifies for. A record with neither is a signature
	// nobody can check.
	SignerAddress string `json:"signer_address,omitempty"`
	CreatedAt     int64  `json:"created_at"`
}

func (s *Store) UpsertMandate(m *PayoutMandate) error {
	m.CreatedAt = time.Now().Unix()
	_, err := s.db.Exec(
		`INSERT INTO payout_mandates (issuance_id, chain, asset, address, signature, signer_xonly, signer_address, created_at)
         VALUES (?,?,?,?,?,?,?,?)
         ON CONFLICT(issuance_id, chain) DO UPDATE SET
            asset=excluded.asset, address=excluded.address, signature=excluded.signature,
            signer_xonly=excluded.signer_xonly, signer_address=excluded.signer_address,
            created_at=excluded.created_at`,
		m.IssuanceID, m.Chain, m.Asset, m.Address, m.Signature, m.SignerXOnly,
		m.SignerAddress, m.CreatedAt)
	return err
}

func (s *Store) MandateFor(issuanceID, chain string) (*PayoutMandate, error) {
	var m PayoutMandate
	err := s.db.QueryRow(
		`SELECT issuance_id, chain, asset, address, signature, signer_xonly, signer_address, created_at
         FROM payout_mandates WHERE issuance_id = ? AND chain = ?`, issuanceID, chain).
		Scan(&m.IssuanceID, &m.Chain, &m.Asset, &m.Address, &m.Signature, &m.SignerXOnly, &m.SignerAddress, &m.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) MandatesByIssuance(issuanceID string) ([]*PayoutMandate, error) {
	rows, err := s.db.Query(
		`SELECT issuance_id, chain, asset, address, signature, signer_xonly, signer_address, created_at
         FROM payout_mandates WHERE issuance_id = ? ORDER BY chain`, issuanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*PayoutMandate{}
	for rows.Next() {
		var m PayoutMandate
		if err := rows.Scan(&m.IssuanceID, &m.Chain, &m.Asset, &m.Address, &m.Signature, &m.SignerXOnly, &m.SignerAddress, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// --- EU offeree counting -----------------------------------------------------

// RecordOfferee records that a distinct non-qualified person (aid) in a member
// state has had the offer made available (gate passage). Idempotent per
// (issuance, member_state, aid): re-passing the gate never double-counts. Returns
// the resulting distinct count for that member state.
func (s *Store) RecordOfferee(issuanceID, memberState, aid string) (int, error) {
	_, err := s.db.Exec(
		`INSERT INTO offerees (issuance_id, member_state, aid, created_at) VALUES (?,?,?,?)
         ON CONFLICT(issuance_id, member_state, aid) DO NOTHING`,
		issuanceID, memberState, aid, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return s.OffereeCount(issuanceID, memberState)
}

func (s *Store) OffereeCount(issuanceID, memberState string) (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(1) FROM offerees WHERE issuance_id = ? AND member_state = ?`, issuanceID, memberState).Scan(&n)
	return n, err
}

// OffereeCounted reports whether this aid is already counted for the state, so
// the gate can pass an already-counted person even at the cap.
func (s *Store) OffereeCounted(issuanceID, memberState, aid string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(1) FROM offerees WHERE issuance_id = ? AND member_state = ? AND aid = ?`,
		issuanceID, memberState, aid).Scan(&n)
	return n > 0, err
}

// --- gate records (statements + questionnaires recorded on the ID) -----------

// GateRecord is a captured offer-side gate artifact recorded on an ID: a UK
// SI 2024/301 statement (valid 12 months), an appropriateness questionnaire, or
// a source-of-funds declaration. Keyed by (aid, issuance_id, kind); a UK
// statement uses an empty issuance_id since it is personal and offering-agnostic.
type GateRecord struct {
	AID        string          `json:"aid"`
	IssuanceID string          `json:"issuance_id"`
	Kind       string          `json:"kind"` // uk_statement | appropriateness | sof
	Detail     json.RawMessage `json:"detail"`
	ValidUntil int64           `json:"valid_until,omitempty"`
	CreatedAt  int64           `json:"created_at"`
}

func (s *Store) UpsertGateRecord(g *GateRecord) error {
	g.CreatedAt = time.Now().Unix()
	_, err := s.db.Exec(
		`INSERT INTO gate_records (aid, issuance_id, kind, detail, valid_until, created_at)
         VALUES (?,?,?,?,?,?)
         ON CONFLICT(aid, issuance_id, kind) DO UPDATE SET
            detail=excluded.detail, valid_until=excluded.valid_until, created_at=excluded.created_at`,
		g.AID, g.IssuanceID, g.Kind, string(rawOrEmpty(g.Detail)), g.ValidUntil, g.CreatedAt)
	return err
}

// GateRecord returns the record for (aid, issuance_id, kind), or nil.
func (s *Store) GateRecord(aid, issuanceID, kind string) (*GateRecord, error) {
	var g GateRecord
	var detail string
	err := s.db.QueryRow(
		`SELECT aid, issuance_id, kind, detail, valid_until, created_at
         FROM gate_records WHERE aid = ? AND issuance_id = ? AND kind = ?`, aid, issuanceID, kind).
		Scan(&g.AID, &g.IssuanceID, &g.Kind, &detail, &g.ValidUntil, &g.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	g.Detail = json.RawMessage(detail)
	return &g, nil
}

// --- reorged-BTC-deposit holds (M6) ------------------------------------------

// ReorgHold records a global account freeze applied because a settled native-BTC
// subscription's deposit was reorged out post-delivery. state: active (frozen) |
// cleared (re-confirmed, unfrozen) | clawed (resolved by clawback).
type ReorgHold struct {
	SubscriptionID string `json:"subscription_id"`
	IssuanceID     string `json:"issuance_id"`
	InvestorAID    string `json:"investor_aid"`
	DepositTxid    string `json:"deposit_txid"`
	State          string `json:"state"`
	Reason         string `json:"reason,omitempty"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

// UpsertReorgHold inserts or updates the hold for a subscription, refreshing its
// state, reason and updated_at while preserving created_at on the first write.
func (s *Store) UpsertReorgHold(h *ReorgHold) error {
	now := time.Now().Unix()
	if h.CreatedAt == 0 {
		h.CreatedAt = now
	}
	_, err := s.db.Exec(
		`INSERT INTO reorg_holds (subscription_id, issuance_id, investor_aid, deposit_txid, state, reason, created_at, updated_at)
         VALUES (?,?,?,?,?,?,?,?)
         ON CONFLICT(subscription_id) DO UPDATE SET
            state=excluded.state, reason=excluded.reason, deposit_txid=excluded.deposit_txid, updated_at=excluded.updated_at`,
		h.SubscriptionID, h.IssuanceID, h.InvestorAID, h.DepositTxid, h.State, h.Reason, h.CreatedAt, now)
	return err
}

func (s *Store) ReorgHoldBySub(subID string) (*ReorgHold, error) {
	var h ReorgHold
	err := s.db.QueryRow(
		`SELECT subscription_id, issuance_id, investor_aid, deposit_txid, state, reason, created_at, updated_at
         FROM reorg_holds WHERE subscription_id = ?`, subID).
		Scan(&h.SubscriptionID, &h.IssuanceID, &h.InvestorAID, &h.DepositTxid, &h.State, &h.Reason, &h.CreatedAt, &h.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

func (s *Store) ActiveReorgHolds() ([]*ReorgHold, error) {
	rows, err := s.db.Query(
		`SELECT subscription_id, issuance_id, investor_aid, deposit_txid, state, reason, created_at, updated_at
         FROM reorg_holds WHERE state = 'active' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ReorgHold{}
	for rows.Next() {
		var h ReorgHold
		if err := rows.Scan(&h.SubscriptionID, &h.IssuanceID, &h.InvestorAID, &h.DepositTxid,
			&h.State, &h.Reason, &h.CreatedAt, &h.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &h)
	}
	return out, rows.Err()
}

func (s *Store) UpdateReorgHoldFields(subID string, fields map[string]any) error {
	return s.updateFields("reorg_holds", "subscription_id", subID, fields)
}

// --- generic partial update (shared by the M5 tables) ------------------------

// updateFields applies a partial update to one row of a table by a key column.
// It is the M5 analogue of UpdateIssuanceFields, used by the subscription,
// settlement, fiat and fee state machines. Column names are caller-supplied
// constants, never client input.
func (s *Store) updateFields(table, keyCol, keyVal string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	cols := make([]string, 0, len(fields))
	for k := range fields {
		cols = append(cols, k)
	}
	set := ""
	args := make([]any, 0, len(cols)+2)
	for _, c := range cols {
		if set != "" {
			set += ", "
		}
		set += c + " = ?"
		args = append(args, fields[c])
	}
	set += ", updated_at = ?"
	args = append(args, time.Now().Unix(), keyVal)
	_, err := s.db.Exec(`UPDATE `+table+` SET `+set+` WHERE `+keyCol+` = ?`, args...)
	return err
}
