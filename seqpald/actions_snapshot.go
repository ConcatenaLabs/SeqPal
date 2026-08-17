package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

// The record-date snapshot (W-3): the chain watcher, on each tick, snapshots
// any action whose record_height has been reached. The snapshot enumerates ALL
// UTXOs carrying the asset via electrs (verified against the box explorer,
// which serves the liquid-mode asset index):
//
//	GET /asset/<id>/txs/chain[/<last_txid>]  -- pages the asset's confirmed txs
//	GET /tx/<txid>/outspend/<vout>           -- confirms a candidate is unspent
//
// When the asset index is unsupported, it falls back to walking the issuance
// transaction forward via GET /tx/<txid>/outspends from the known issuance
// txid, which seqpald stores. (The fallback cannot see units minted by a later
// reissuance, because those enter through the reissuance token's chain, not the
// issuance tx's; the asset-index path covers them.)
//
// DISCLOSURE (stamped on the action): the snapshot is taken at the FIRST
// watcher pass at or after the record height, so it reflects the UTXO set at
// that pass's tip, not at the record height itself.

// snapUTXO is one snapshot candidate: the outpoint, its script, its atoms.
type snapUTXO struct {
	Outpoint string
	Script   string
	Atoms    uint64
}

// actionSnapshotTick runs once per watcher tick: take the snapshot for every
// action whose record height is reached and that is still awaiting one.
func (s *server) actionSnapshotTick() {
	if s.cfg.electrsURL == "" {
		return
	}
	tip := s.tipHeight()
	if tip <= 0 {
		return
	}
	actions, err := s.st.ActionsByState("awaiting_snapshot")
	if err != nil {
		return
	}
	for _, a := range actions {
		if a.RecordHeight > tip {
			continue
		}
		if err := s.snapshotAction(a, tip); err != nil {
			log.Printf("action snapshot %s: %v", a.ID, err)
			s.st.Audit("", "action.snapshot.deferred", map[string]any{"action": a.ID, "error": err.Error()})
		}
	}
}

// snapshotAction freezes the snapshot rows for one action, computes and anchors
// the summary, and advances the state. Crash-resumable like the distribution
// snapshot: existing frozen rows are recomputed from, never re-walked, so a
// crash between the row inserts and the state write cannot fork the summary
// from what claims will read.
func (s *server) snapshotAction(a *CorporateAction, tip int64) error {
	rows, err := s.st.SnapshotRows(a.ID)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		iss, err := s.st.IssuanceByID(a.IssuanceID)
		if err != nil || iss == nil {
			return fmt.Errorf("issuance missing")
		}
		utxos, err := s.assetUTXOs(a.AssetID, iss.Txid)
		if err != nil {
			return err
		}
		if len(utxos) == 0 {
			return fmt.Errorf("no UTXOs carrying the asset were found; retrying next tick")
		}
		for _, u := range utxos {
			if err := s.st.InsertSnapshotRowIfAbsent(&SnapshotRow{
				ActionID: a.ID, Outpoint: u.Outpoint, Script: u.Script, Atoms: u.Atoms,
			}); err != nil {
				return err
			}
		}
		rows, err = s.st.SnapshotRows(a.ID)
		if err != nil {
			return err
		}
	}

	var total uint64
	summary := make([]map[string]any, 0, len(rows))
	sort.Slice(rows, func(i, j int) bool { return rows[i].Outpoint < rows[j].Outpoint })
	for _, r := range rows {
		total += r.Atoms
		summary = append(summary, map[string]any{
			"outpoint": r.Outpoint, "script": r.Script, "atoms": r.Atoms,
		})
	}
	canon, err := canonicalJSON(map[string]any{
		"action_id": a.ID, "asset": a.AssetID, "record_height": a.RecordHeight,
		"snapshot_height": tip, "utxos": summary,
	})
	if err != nil {
		return err
	}
	hash := sha256Hex(canon)
	anchorTxid := s.anchorArtifact("action-snapshot", hash)
	note := fmt.Sprintf(
		"snapshot taken at the first watcher pass at or after the record height: record height %d, snapshot at Sequentia block %d",
		a.RecordHeight, tip)
	if err := s.st.UpdateActionFields(a.ID, map[string]any{
		"state": "snapshotted", "snapshot_height": tip, "snapshot_total": total,
		"snapshot_count": len(rows), "snapshot_hash": hash, "snapshot_note": note,
		"anchor_txid": anchorTxid,
	}); err != nil {
		return err
	}
	s.st.Audit("", "action.snapshot", map[string]any{
		"action": a.ID, "asset": a.AssetID, "record_height": a.RecordHeight,
		"snapshot_height": tip, "utxos": len(rows), "total_atoms": total,
		"snapshot_hash": hash, "anchor_txid": anchorTxid,
	})
	return nil
}

// --- electrs walkers ---------------------------------------------------------

// assetUTXOs enumerates every unspent output carrying the asset.
//
// The electrs asset index is authoritative for ISSUANCES ONLY: it records the
// issuance (and reissuance) transactions under the asset but does not index
// later transfers of it. Verified live on the box: an asset whose units had
// already moved twice still listed exactly one transaction. So the index alone
// can never see a holder who received units in an ordinary transfer, and the
// forward graph walk from the issuance is not a fallback for a broken index,
// it is the mechanism that finds holders at all. Both run, and their results
// are unioned by outpoint: the index contributes reissuances the walk cannot
// reach, the walk contributes every transfer.
func (s *server) assetUTXOs(asset, issuanceTxid string) ([]snapUTXO, error) {
	byOutpoint := map[string]snapUTXO{}
	idxUTXOs, idxErr := s.assetUTXOsViaIndex(asset)
	for _, u := range idxUTXOs {
		byOutpoint[u.Outpoint] = u
	}
	var walkErr error
	if issuanceTxid != "" {
		walkUTXOs, werr := s.assetUTXOsViaWalk(asset, issuanceTxid)
		walkErr = werr
		for _, u := range walkUTXOs {
			byOutpoint[u.Outpoint] = u
		}
	}
	// Refuse only when NEITHER source could speak. A snapshot that silently
	// omitted a holder would misallocate a distribution, so a hard error beats
	// a partial answer here.
	if idxErr != nil && (issuanceTxid == "" || walkErr != nil) {
		if issuanceTxid == "" {
			return nil, idxErr
		}
		return nil, fmt.Errorf("asset index: %v; issuance walk: %v", idxErr, walkErr)
	}
	out := make([]snapUTXO, 0, len(byOutpoint))
	for _, u := range byOutpoint {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Outpoint < out[j].Outpoint })
	return out, nil
}

// electrsTx is the subset of an esplora tx the walkers read.
type electrsTx struct {
	Txid   string `json:"txid"`
	Status struct {
		Confirmed   bool  `json:"confirmed"`
		BlockHeight int64 `json:"block_height"`
	} `json:"status"`
	Vout []struct {
		ScriptPubKey string `json:"scriptpubkey"`
		Asset        string `json:"asset"`
		Value        uint64 `json:"value"`
	} `json:"vout"`
}

type electrsOutspend struct {
	Spent bool   `json:"spent"`
	Txid  string `json:"txid"`
}

// assetUTXOsViaIndex pages GET /asset/<id>/txs/chain[/<last>] collecting every
// confirmed output carrying the asset, then keeps the ones
// /tx/<txid>/outspend/<vout> reports unspent.
func (s *server) assetUTXOsViaIndex(asset string) ([]snapUTXO, error) {
	candidates := []snapUTXO{}
	last := ""
	for page := 0; page < 400; page++ {
		path := "/asset/" + asset + "/txs/chain"
		if last != "" {
			path += "/" + last
		}
		raw, err := s.electrsGET(path)
		if err != nil {
			return nil, err
		}
		var txs []electrsTx
		if err := json.Unmarshal(raw, &txs); err != nil {
			return nil, err
		}
		if len(txs) == 0 {
			break
		}
		for _, tx := range txs {
			if !tx.Status.Confirmed {
				continue
			}
			for i, v := range tx.Vout {
				if strings.EqualFold(v.Asset, asset) && v.Value > 0 && v.ScriptPubKey != "" {
					candidates = append(candidates, snapUTXO{
						Outpoint: fmt.Sprintf("%s:%d", tx.Txid, i),
						Script:   strings.ToLower(v.ScriptPubKey),
						Atoms:    v.Value,
					})
				}
			}
		}
		last = txs[len(txs)-1].Txid
	}
	out := []snapUTXO{}
	for _, c := range candidates {
		parts := strings.SplitN(c.Outpoint, ":", 2)
		raw, err := s.electrsGET("/tx/" + parts[0] + "/outspend/" + parts[1])
		if err != nil {
			return nil, err
		}
		var osp electrsOutspend
		if err := json.Unmarshal(raw, &osp); err != nil {
			return nil, err
		}
		if !osp.Spent {
			out = append(out, c)
		}
	}
	return out, nil
}

// assetUTXOsViaWalk walks the asset forward from the issuance tx: every output
// carrying the asset is either unspent (a snapshot row) or spent (its spending
// tx joins the frontier). Bounded so a pathological chain cannot spin forever.
func (s *server) assetUTXOsViaWalk(asset, issuanceTxid string) ([]snapUTXO, error) {
	out := []snapUTXO{}
	frontier := []string{issuanceTxid}
	seen := map[string]bool{}
	for steps := 0; len(frontier) > 0 && steps < 5000; steps++ {
		txid := frontier[0]
		frontier = frontier[1:]
		if seen[txid] {
			continue
		}
		seen[txid] = true
		rawTx, err := s.electrsGET("/tx/" + txid)
		if err != nil {
			return nil, err
		}
		var tx electrsTx
		if err := json.Unmarshal(rawTx, &tx); err != nil {
			return nil, err
		}
		rawSpends, err := s.electrsGET("/tx/" + txid + "/outspends")
		if err != nil {
			return nil, err
		}
		var spends []electrsOutspend
		if err := json.Unmarshal(rawSpends, &spends); err != nil {
			return nil, err
		}
		for i, v := range tx.Vout {
			if !strings.EqualFold(v.Asset, asset) || v.Value == 0 || v.ScriptPubKey == "" {
				continue
			}
			if i < len(spends) && spends[i].Spent {
				if spends[i].Txid != "" && !seen[spends[i].Txid] {
					frontier = append(frontier, spends[i].Txid)
				}
				continue
			}
			out = append(out, snapUTXO{
				Outpoint: fmt.Sprintf("%s:%d", tx.Txid, i),
				Script:   strings.ToLower(v.ScriptPubKey),
				Atoms:    v.Value,
			})
		}
	}
	return out, nil
}

func (s *server) electrsGET(path string) ([]byte, error) {
	if s.cfg.electrsURL == "" {
		return nil, fmt.Errorf("no electrs url configured")
	}
	req, err := http.NewRequest("GET", strings.TrimRight(s.cfg.electrsURL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("electrs %s -> HTTP %d", path, resp.StatusCode)
	}
	return readAllLimited(resp.Body, 16<<20)
}
