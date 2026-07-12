package main

import (
	"database/sql"
	"time"
)

// WatchState is the chain watcher's persistent view of one live issuance. It is
// what every truthful surface renders from: the status chip, the confirmation
// count, and the Bitcoin anchor depth all come from here, never from the
// browser. Bitcoin anchoring is supreme, so a previously-confirmed txid that a
// reorg unwinds regresses to Status "regressed" and an event is emitted.
type WatchState struct {
	IssuanceID    string `json:"issuance_id"`
	OwnerAID      string `json:"owner_aid"`
	AssetID       string `json:"asset_id"`
	Txid          string `json:"txid"`
	Status        string `json:"status"` // broadcast | confirmed | anchored | regressed
	Confirmations int64  `json:"confirmations"`
	AnchorDepth   int64  `json:"anchor_depth"`
	AnchorHeight  int64  `json:"anchor_height"`
	AnchorHash    string `json:"anchor_hash,omitempty"`
	AnchorStatus  string `json:"anchor_status,omitempty"` // node getanchorstatus for the tip
	BlockHash     string `json:"block_hash,omitempty"`
	BlockHeight   int64  `json:"block_height,omitempty"`
	Contract      string `json:"-"` // canonical contract bytes, for registry publication
	RegistryURL   string `json:"registry_url,omitempty"`
	PriceSeeded   bool   `json:"price_seeded"`
	UpdatedAt     int64  `json:"updated_at"`
}

const watchCols = `issuance_id, owner_aid, asset_id, txid, status, confirmations, anchor_depth,
    anchor_height, anchor_hash, anchor_status, block_hash, block_height, contract, registry_url,
    price_seeded, updated_at`

// UpsertWatch inserts or replaces a watch row. The watcher owns every column
// except the ones the deploy path seeds on creation (owner_aid, asset_id, txid,
// contract), which are preserved by ON CONFLICT so a status tick never clears
// the contract needed for later registry publication.
func (s *Store) UpsertWatch(w *WatchState) error {
	w.UpdatedAt = time.Now().Unix()
	_, err := s.db.Exec(
		`INSERT INTO watch (`+watchCols+`)
         VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
         ON CONFLICT(issuance_id) DO UPDATE SET
            owner_aid=excluded.owner_aid, asset_id=excluded.asset_id, txid=excluded.txid,
            status=excluded.status, confirmations=excluded.confirmations, anchor_depth=excluded.anchor_depth,
            anchor_height=excluded.anchor_height, anchor_hash=excluded.anchor_hash,
            anchor_status=excluded.anchor_status, block_hash=excluded.block_hash,
            block_height=excluded.block_height, registry_url=excluded.registry_url,
            price_seeded=excluded.price_seeded, updated_at=excluded.updated_at,
            contract=CASE WHEN excluded.contract != '' THEN excluded.contract ELSE watch.contract END`,
		w.IssuanceID, w.OwnerAID, w.AssetID, w.Txid, w.Status, w.Confirmations, w.AnchorDepth,
		w.AnchorHeight, w.AnchorHash, w.AnchorStatus, w.BlockHash, w.BlockHeight, w.Contract,
		w.RegistryURL, boolInt(w.PriceSeeded), w.UpdatedAt)
	return err
}

func (s *Store) WatchByIssuance(id string) (*WatchState, error) {
	return scanWatch(s.db.QueryRow(`SELECT `+watchCols+` FROM watch WHERE issuance_id = ?`, id))
}

// WatchByAsset finds the watch row for an asset id (the domain-proof route uses
// it to authorize only assets seqpald actually deployed).
func (s *Store) WatchByAsset(assetID string) (*WatchState, error) {
	return scanWatch(s.db.QueryRow(`SELECT `+watchCols+` FROM watch WHERE asset_id = ? LIMIT 1`, assetID))
}

// WatchByOwner returns every watch row an account owns (the SSE snapshot and the
// scope check for owner-scoped reads read from here).
func (s *Store) WatchByOwner(aid string) ([]*WatchState, error) {
	rows, err := s.db.Query(`SELECT `+watchCols+` FROM watch WHERE owner_aid = ? ORDER BY updated_at DESC`, aid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWatchRows(rows)
}

// AllWatches returns every watch row (the watcher iterates them each tick).
func (s *Store) AllWatches() ([]*WatchState, error) {
	rows, err := s.db.Query(`SELECT ` + watchCols + ` FROM watch`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWatchRows(rows)
}

func scanWatchRows(rows *sql.Rows) ([]*WatchState, error) {
	out := []*WatchState{}
	for rows.Next() {
		w, err := scanWatchInto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func scanWatch(row *sql.Row) (*WatchState, error) {
	w, err := scanWatchInto(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return w, err
}

func scanWatchInto(sc scanner) (*WatchState, error) {
	var w WatchState
	var priceSeeded int
	err := sc.Scan(&w.IssuanceID, &w.OwnerAID, &w.AssetID, &w.Txid, &w.Status, &w.Confirmations,
		&w.AnchorDepth, &w.AnchorHeight, &w.AnchorHash, &w.AnchorStatus, &w.BlockHash, &w.BlockHeight,
		&w.Contract, &w.RegistryURL, &priceSeeded, &w.UpdatedAt)
	if err != nil {
		return nil, err
	}
	w.PriceSeeded = priceSeeded != 0
	return &w, nil
}
