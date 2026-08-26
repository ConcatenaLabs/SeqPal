package main

import (
	"database/sql"
	"encoding/json"
	"time"
)

// M8 persistence: the secondary-market travel-rule record, the market-abuse
// acknowledgment, venue listings, and the Depository-Receipt programme (mint =
// OA-6 reissuance, redeem = OA-5 burn). Every fund/supply movement keeps the
// M5/M6 idempotency shape: intent persisted before broadcast, reconciled by a
// chain/log scan before any retry, never a double-transfer / double-mint /
// double-burn.

// --- lookups shared by the secondary surfaces --------------------------------

// IssuanceByAsset returns the SeqPal-managed issuance for an on-chain asset id,
// or nil. It is how a P2P transfer, the wallet-transfer poller, and the listings
// read resolve an asset back to the platform record that governs it.
func (s *Store) IssuanceByAsset(assetID string) (*Issuance, error) {
	if assetID == "" {
		return nil, nil
	}
	return scanIssuance(s.db.QueryRow(`SELECT `+issuanceCols+` FROM issuances WHERE asset_id = ? LIMIT 1`, assetID))
}

// EnclaveKeyByAID returns the custodied enclave key whose AID matches, or nil.
// It tells the DR redeem path and the wallet poller whether an AID is one of
// seqpald's own primary enclaves (escrow / entity treasury), which seqpald can
// sign for and which must never be counted as a secondary counterparty.
func (s *Store) EnclaveKeyByAID(aid string) (*EnclaveKey, error) {
	var k EnclaveKey
	err := s.db.QueryRow(
		`SELECT aid, kind, ref_id, xonly, priv, created_at FROM enclave_keys WHERE aid = ?`, aid).
		Scan(&k.AID, &k.Kind, &k.RefID, &k.XOnly, &k.Priv, &k.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &k, nil
}

// --- P2P transfer (travel-rule record) ---------------------------------------

// P2PTransfer is one policy-co-signed holder-to-holder transfer. It is the
// travel-rule record: both AIDs and their captured identity fields are stored.
// State machine: awaiting_signature -> completing -> settled | refused | failed.
// A refusal is terminal and carries the real policy-server reason (also present
// in openampd /v1/log). source is "browser" (built+signed in the SPA) or
// "wallet" (captured by the /v1/log poller and joined server-side).
type P2PTransfer struct {
	ID              string `json:"id"`
	OaID            string `json:"oa_id,omitempty"`
	IssuanceID      string `json:"issuance_id,omitempty"`
	AssetID         string `json:"asset_id"`
	OriginatorAID   string `json:"originator_aid"`
	BeneficiaryAID  string `json:"beneficiary_aid,omitempty"`
	Atoms           uint64 `json:"atoms"`
	State           string `json:"state"`
	Source          string `json:"source"`
	Txid            string `json:"txid,omitempty"`
	Reason          string `json:"reason,omitempty"`
	OrigName        string `json:"originator_name,omitempty"`
	OrigResidence   string `json:"originator_residence,omitempty"`
	BenefName       string `json:"beneficiary_name,omitempty"`
	BenefResidence  string `json:"beneficiary_residence,omitempty"`
	BenefRegistered bool   `json:"beneficiary_registered"`
	// Confidential notes the per-transfer confidentiality election on the
	// travel-rule record: the transfer's amount and asset are blinded from
	// outside observers, while the registrar keeps full sight through the policy
	// server's blinding keys.
	Confidential bool  `json:"confidential"`
	CreatedAt    int64 `json:"created_at"`
	UpdatedAt    int64 `json:"updated_at"`
}

const p2pCols = `id, oa_id, issuance_id, asset_id, originator_aid, beneficiary_aid, atoms, state,
    source, txid, reason, orig_name, orig_residence, benef_name, benef_residence, benef_registered,
    confidential, created_at, updated_at`

func (s *Store) InsertP2PTransfer(t *P2PTransfer) error {
	now := time.Now().Unix()
	t.CreatedAt, t.UpdatedAt = now, now
	_, err := s.db.Exec(
		`INSERT INTO p2p_transfers (`+p2pCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.OaID, t.IssuanceID, t.AssetID, t.OriginatorAID, t.BeneficiaryAID, t.Atoms, t.State,
		t.Source, t.Txid, t.Reason, t.OrigName, t.OrigResidence, t.BenefName, t.BenefResidence,
		boolInt(t.BenefRegistered), boolInt(t.Confidential), t.CreatedAt, t.UpdatedAt)
	return err
}

func (s *Store) UpdateP2PTransferFields(id string, fields map[string]any) error {
	return s.updateFields("p2p_transfers", "id", id, fields)
}

func (s *Store) P2PTransferByID(id string) (*P2PTransfer, error) {
	return scanP2P(s.db.QueryRow(`SELECT `+p2pCols+` FROM p2p_transfers WHERE id = ?`, id))
}

// P2PTransferByTxid returns the transfer that recorded a given txid, or nil. It
// makes wallet-transfer capture idempotent (the poller skips a txid already
// recorded) and is the reconciliation anchor for a lost complete write.
func (s *Store) P2PTransferByTxid(txid string) (*P2PTransfer, error) {
	if txid == "" {
		return nil, nil
	}
	return scanP2P(s.db.QueryRow(`SELECT `+p2pCols+` FROM p2p_transfers WHERE txid = ?`, txid))
}

// P2PBrowserExistsForMove reports whether a browser-initiated transfer already
// records this exact move (asset, originator, atoms). The wallet-transfer poller
// uses it (with the txid dedup) to avoid double-capturing a transfer seqpald built
// itself, closing the race between openampd appending the transfer to its log and
// seqpald recording the settled txid on the browser row.
func (s *Store) P2PBrowserExistsForMove(assetID, originatorAID string, atoms uint64) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(1) FROM p2p_transfers WHERE source = 'browser' AND asset_id = ? AND originator_aid = ? AND atoms = ?
         AND state IN ('awaiting_signature','completing','settled')`,
		assetID, originatorAID, atoms).Scan(&n)
	return n > 0, err
}

func (s *Store) P2PTransfersByOriginator(aid string) ([]*P2PTransfer, error) {
	return s.queryP2P(`SELECT `+p2pCols+` FROM p2p_transfers WHERE originator_aid = ? ORDER BY created_at DESC`, aid)
}

func (s *Store) P2PTransfersByAsset(assetID string) ([]*P2PTransfer, error) {
	return s.queryP2P(`SELECT `+p2pCols+` FROM p2p_transfers WHERE asset_id = ? ORDER BY created_at DESC`, assetID)
}

func (s *Store) queryP2P(q string, args ...any) ([]*P2PTransfer, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*P2PTransfer{}
	for rows.Next() {
		t, err := scanP2PInto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func scanP2P(row *sql.Row) (*P2PTransfer, error) {
	t, err := scanP2PInto(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

func scanP2PInto(sc scanner) (*P2PTransfer, error) {
	var t P2PTransfer
	var reg, conf int
	err := sc.Scan(&t.ID, &t.OaID, &t.IssuanceID, &t.AssetID, &t.OriginatorAID, &t.BeneficiaryAID,
		&t.Atoms, &t.State, &t.Source, &t.Txid, &t.Reason, &t.OrigName, &t.OrigResidence,
		&t.BenefName, &t.BenefResidence, &reg, &conf, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	t.BenefRegistered = reg != 0
	t.Confidential = conf != 0
	return &t, nil
}

// --- market-abuse acknowledgment ---------------------------------------------

// MarketAbuseAck is the once-per-investor insider-dealing / market-abuse
// acknowledgment recorded before the platform enables its transfer surfaces. It
// is a platform-layer control, disclosed as such: it is NOT enforced at the
// policy co-signature, so it never blocks a chain transfer, only the SeqPal UI.
type MarketAbuseAck struct {
	AID         string `json:"aid"`
	Version     int    `json:"version"`
	AckHash     string `json:"ack_hash,omitempty"`
	Signature   string `json:"signature,omitempty"`
	SignerXOnly string `json:"signer_xonly,omitempty"`
	// What checks this signature: the key for a tagged one, or the address an
	// ordinary signed message verifies for. A record with neither is a signature
	// nobody can check.
	SignerAddress string `json:"signer_address,omitempty"`
	CreatedAt     int64  `json:"created_at"`
}

// UpsertMarketAbuseAck records the acknowledgment. It preserves the first
// created_at on a repeat (ON CONFLICT keeps the original timestamp) so "recorded
// once" reads truthfully.
func (s *Store) UpsertMarketAbuseAck(a *MarketAbuseAck) error {
	if a.CreatedAt == 0 {
		a.CreatedAt = time.Now().Unix()
	}
	_, err := s.db.Exec(
		`INSERT INTO market_abuse_acks (aid, version, ack_hash, signature, signer_xonly, signer_address, created_at)
         VALUES (?,?,?,?,?,?,?)
         ON CONFLICT(aid) DO UPDATE SET
            version=excluded.version, ack_hash=excluded.ack_hash, signature=excluded.signature,
            signer_xonly=excluded.signer_xonly, signer_address=excluded.signer_address`,
		a.AID, a.Version, a.AckHash, a.Signature, a.SignerXOnly, a.SignerAddress, a.CreatedAt)
	return err
}

func (s *Store) MarketAbuseAckByAID(aid string) (*MarketAbuseAck, error) {
	var a MarketAbuseAck
	err := s.db.QueryRow(
		`SELECT aid, version, ack_hash, signature, signer_xonly, signer_address, created_at FROM market_abuse_acks WHERE aid = ?`, aid).
		Scan(&a.AID, &a.Version, &a.AckHash, &a.Signature, &a.SignerXOnly, &a.SignerAddress, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// --- venue listings ----------------------------------------------------------

// Listing is an issuer-granted listing authorization for an asset. A venue reads
// it to learn which assets an issuer authorized for listing; it can never GRANT
// eligibility (that is stamped by SeqPal ID alone). venues is a JSON array of
// venue names; an empty array means "any venue that honors SeqPal ID".
type Listing struct {
	AssetID     string          `json:"asset"`
	IssuanceID  string          `json:"issuance_id,omitempty"`
	IssuerAID   string          `json:"issuer_aid,omitempty"`
	Ticker      string          `json:"ticker,omitempty"`
	Name        string          `json:"name,omitempty"`
	Authorized  bool            `json:"authorized"`
	Venues      json.RawMessage `json:"venues"`
	Signature   string          `json:"signature,omitempty"`
	SignerXOnly string          `json:"signer_xonly,omitempty"`
	// What checks this signature: the key for a tagged one, or the address an
	// ordinary signed message verifies for. A record with neither is a signature
	// nobody can check.
	SignerAddress string `json:"signer_address,omitempty"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

const listingCols = `asset_id, issuance_id, issuer_aid, ticker, name, authorized, venues, signature, signer_xonly, signer_address, created_at, updated_at`

func (s *Store) UpsertListing(l *Listing) error {
	now := time.Now().Unix()
	if l.CreatedAt == 0 {
		l.CreatedAt = now
	}
	l.UpdatedAt = now
	venues := string(rawOrEmpty(l.Venues))
	if venues == "" {
		venues = "[]"
	}
	_, err := s.db.Exec(
		`INSERT INTO listings (`+listingCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
         ON CONFLICT(asset_id) DO UPDATE SET
            issuance_id=excluded.issuance_id, issuer_aid=excluded.issuer_aid, ticker=excluded.ticker,
            name=excluded.name, authorized=excluded.authorized, venues=excluded.venues,
            signature=excluded.signature, signer_xonly=excluded.signer_xonly,
            signer_address=excluded.signer_address, updated_at=excluded.updated_at`,
		l.AssetID, l.IssuanceID, l.IssuerAID, l.Ticker, l.Name, boolInt(l.Authorized), venues,
		l.Signature, l.SignerXOnly, l.SignerAddress, l.CreatedAt, l.UpdatedAt)
	return err
}

func (s *Store) ListingByAsset(assetID string) (*Listing, error) {
	return scanListing(s.db.QueryRow(`SELECT `+listingCols+` FROM listings WHERE asset_id = ?`, assetID))
}

// AuthorizedListings returns the listings a venue may act on, optionally filtered
// by issuer AID. Only authorized rows are returned (a revoked authorization is
// withheld), so a venue never lists an asset the issuer has not authorized.
func (s *Store) AuthorizedListings(issuerAID string) ([]*Listing, error) {
	q := `SELECT ` + listingCols + ` FROM listings WHERE authorized = 1`
	args := []any{}
	if issuerAID != "" {
		q += ` AND issuer_aid = ?`
		args = append(args, issuerAID)
	}
	q += ` ORDER BY ticker`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Listing{}
	for rows.Next() {
		l, err := scanListingInto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func scanListing(row *sql.Row) (*Listing, error) {
	l, err := scanListingInto(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return l, err
}

func scanListingInto(sc scanner) (*Listing, error) {
	var l Listing
	var auth int
	var venues string
	err := sc.Scan(&l.AssetID, &l.IssuanceID, &l.IssuerAID, &l.Ticker, &l.Name, &auth, &venues,
		&l.Signature, &l.SignerXOnly, &l.SignerAddress, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		return nil, err
	}
	l.Authorized = auth != 0
	l.Venues = json.RawMessage(venues)
	return &l, nil
}

// --- DR programme + ops ------------------------------------------------------

// DRProgram marks an issuance as a running Depository-Receipt programme and
// records the height of the enforced US-person exclusion category rule (a real
// policy-server rule, not a display string) and the amendment-chain mutation that
// applied it.
type DRProgram struct {
	IssuanceID        string `json:"issuance_id"`
	AssetID           string `json:"asset_id"`
	Enabled           bool   `json:"enabled"`
	USExclusionHeight int64  `json:"us_exclusion_height"`
	MutationID        string `json:"mutation_id,omitempty"`
	CreatedAt         int64  `json:"created_at"`
	UpdatedAt         int64  `json:"updated_at"`
}

func (s *Store) UpsertDRProgram(p *DRProgram) error {
	now := time.Now().Unix()
	if p.CreatedAt == 0 {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	_, err := s.db.Exec(
		`INSERT INTO dr_programs (issuance_id, asset_id, enabled, us_exclusion_height, mutation_id, created_at, updated_at)
         VALUES (?,?,?,?,?,?,?)
         ON CONFLICT(issuance_id) DO UPDATE SET
            asset_id=excluded.asset_id, enabled=excluded.enabled, us_exclusion_height=excluded.us_exclusion_height,
            mutation_id=excluded.mutation_id, updated_at=excluded.updated_at`,
		p.IssuanceID, p.AssetID, boolInt(p.Enabled), p.USExclusionHeight, p.MutationID, p.CreatedAt, p.UpdatedAt)
	return err
}

func (s *Store) DRProgramByIssuance(issuanceID string) (*DRProgram, error) {
	var p DRProgram
	var enabled int
	err := s.db.QueryRow(
		`SELECT issuance_id, asset_id, enabled, us_exclusion_height, mutation_id, created_at, updated_at
         FROM dr_programs WHERE issuance_id = ?`, issuanceID).
		Scan(&p.IssuanceID, &p.AssetID, &enabled, &p.USExclusionHeight, &p.MutationID, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.Enabled = enabled != 0
	return &p, nil
}

// DROp is one DR supply operation: a mint (OA-6 reissuance) or a redeem (OA-5
// burn). request_id is the mandatory idempotency key; a retry with the same key
// resolves to the same txid, so a crash-then-retry never double-mints or
// double-burns. State machine: pending -> reblinding (mint only) -> broadcast |
// failed.
type DROp struct {
	ID              string `json:"id"`
	IssuanceID      string `json:"issuance_id"`
	AssetID         string `json:"asset_id"`
	Kind            string `json:"kind"` // mint | redeem
	RequestID       string `json:"request_id"`
	TargetAID       string `json:"target_aid,omitempty"`
	HolderAID       string `json:"holder_aid,omitempty"`
	Atoms           uint64 `json:"atoms"`
	OaID            string `json:"oa_id,omitempty"`
	Txid            string `json:"txid,omitempty"`
	State           string `json:"state"`
	AttestationHash string `json:"attestation_hash,omitempty"`
	AnchorTxid      string `json:"anchor_txid,omitempty"`
	Error           string `json:"error,omitempty"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
}

const drOpCols = `id, issuance_id, asset_id, kind, request_id, target_aid, holder_aid, atoms, oa_id,
    txid, state, attestation_hash, anchor_txid, error, created_at, updated_at`

// InsertDROpIfAbsent inserts the op for a request_id and reports whether it
// created it (true) or one already existed (false). The unique request_id makes
// a DR mint/redeem idempotent even under a concurrent double-submit.
func (s *Store) InsertDROpIfAbsent(op *DROp) (bool, error) {
	now := time.Now().Unix()
	op.CreatedAt, op.UpdatedAt = now, now
	res, err := s.db.Exec(
		`INSERT INTO dr_ops (`+drOpCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
         ON CONFLICT(request_id) DO NOTHING`,
		op.ID, op.IssuanceID, op.AssetID, op.Kind, op.RequestID, op.TargetAID, op.HolderAID, op.Atoms,
		op.OaID, op.Txid, op.State, op.AttestationHash, op.AnchorTxid, op.Error, op.CreatedAt, op.UpdatedAt)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) DROpByRequestID(requestID string) (*DROp, error) {
	return scanDROp(s.db.QueryRow(`SELECT `+drOpCols+` FROM dr_ops WHERE request_id = ?`, requestID))
}

func (s *Store) UpdateDROpFields(id string, fields map[string]any) error {
	return s.updateFields("dr_ops", "id", id, fields)
}

func (s *Store) DROpsByIssuance(issuanceID string) ([]*DROp, error) {
	rows, err := s.db.Query(`SELECT `+drOpCols+` FROM dr_ops WHERE issuance_id = ? ORDER BY created_at DESC`, issuanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*DROp{}
	for rows.Next() {
		op, err := scanDROpInto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

func scanDROp(row *sql.Row) (*DROp, error) {
	op, err := scanDROpInto(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return op, err
}

func scanDROpInto(sc scanner) (*DROp, error) {
	var op DROp
	err := sc.Scan(&op.ID, &op.IssuanceID, &op.AssetID, &op.Kind, &op.RequestID, &op.TargetAID,
		&op.HolderAID, &op.Atoms, &op.OaID, &op.Txid, &op.State, &op.AttestationHash, &op.AnchorTxid,
		&op.Error, &op.CreatedAt, &op.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &op, nil
}

// --- kv cursor (wallet-transfer poller) --------------------------------------

func (s *Store) KVGet(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM kv WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *Store) KVSet(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO kv (key, value, updated_at) VALUES (?,?,?)
         ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, time.Now().Unix())
	return err
}
