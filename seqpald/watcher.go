package main

import (
	"log"
	"time"
)

// The chain watcher is seqpald's authoritative source for every truthful chain
// surface. It tracks each live issuance's issue txid through the states the M3
// contract names: broadcast -> confirmed (N Sequentia confirmations) -> anchored
// (the confirming block carries a Bitcoin anchor and Bitcoin blocks have
// accumulated on top of it). Bitcoin anchoring is supreme: when a reorg unwinds
// a previously-confirmed block, the issuance regresses and an event is emitted,
// because a node discarding Sequentia blocks whose Bitcoin anchor was orphaned is
// correct, never a bug to paper over.
//
// Confirmation / anchor-depth method (which RPC fields):
//   - getanchorstatus -> tipheight, anchorheight (the most recent Bitcoin height
//     the chain tip has anchored to), anchorstatus. This is the parent-chain
//     reference the depth is measured against, read from the Sequentia node
//     alone (no separate Bitcoin RPC).
//   - getrawtransaction <txid> true -> blockhash (which Sequentia block confirms
//     the issuance) and confirmations. Discovery needs -txindex, which the box
//     runs for its explorer.
//   - getblockheader <blockhash> true -> confirmations (< 0 once the block is
//     reorged out of the active chain, which is how a regression is detected),
//     height, and anchorheight (the Bitcoin height THIS block anchors to).
//   - anchor_depth = tip anchorheight - block anchorheight (>= 0), i.e. how many
//     Bitcoin blocks have anchored on top of the confirming block's anchor.

const (
	statusBroadcast = "broadcast"
	statusConfirmed = "confirmed"
	statusAnchored  = "anchored"
	statusRegressed = "regressed"
)

// runWatcher is the background loop. It reconciles watch rows from the issuances
// table (so a restart resumes from chain, and any issuance minted while the
// watcher was down is picked up) and then advances each row's state.
func (s *server) runWatcher(interval time.Duration) {
	s.reconcileWatches()
	s.watchTickAll()
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		s.reconcileWatches()
		s.watchTickAll()
	}
}

// reconcileWatches ensures every live, on-chain issuance has a watch row. The
// deploy path also seeds one immediately, but reconciling from the issuances
// table is what makes the watcher self-healing across a restart or a lost watch
// table.
func (s *server) reconcileWatches() {
	issuances, err := s.st.LiveIssuances()
	if err != nil {
		log.Printf("watcher: list live issuances: %v", err)
		return
	}
	for _, iss := range issuances {
		if iss.AssetID == "" || iss.Txid == "" {
			continue
		}
		existing, err := s.st.WatchByIssuance(iss.ID)
		if err != nil {
			continue
		}
		if existing != nil {
			continue
		}
		w := &WatchState{
			IssuanceID: iss.ID,
			OwnerAID:   iss.OwnerAID,
			AssetID:    iss.AssetID,
			Txid:       iss.Txid,
			Status:     statusBroadcast,
			// The canonical contract is not reconstructed here; registry
			// publication fetches the exact on-chain bytes from openampd when it
			// runs, so a reconciled row publishes correctly without it.
		}
		if err := s.st.UpsertWatch(w); err != nil {
			log.Printf("watcher: seed watch for %s: %v", iss.ID, err)
		}
	}
}

func (s *server) watchTickAll() {
	watches, err := s.st.AllWatches()
	if err != nil {
		log.Printf("watcher: list watches: %v", err)
		return
	}
	for _, w := range watches {
		s.watchTickOne(w)
	}
}

// watchTickOne advances one issuance's chain state, persists and emits on a
// material change, and drives the best-effort deploy-time side effects (price
// seeding, registry publication) that depend on chain state.
func (s *server) watchTickOne(w *WatchState) {
	if s.cfg.nodeURL == "" {
		return // no node configured: nothing authoritative to read
	}
	changed := s.computeWatch(w)
	if changed {
		if err := s.st.UpsertWatch(w); err != nil {
			log.Printf("watcher: persist %s: %v", w.IssuanceID, err)
			return
		}
		s.bus().publish(w.OwnerAID, eventFrom(w))
		s.st.Audit(w.OwnerAID, "watch.update", map[string]any{
			"issuance_id": w.IssuanceID, "asset": w.AssetID, "status": w.Status,
			"confirmations": w.Confirmations, "anchor_depth": w.AnchorDepth,
		})
	}
	// Side effects that need chain state. Price seeding does not need
	// confirmation (it is a data feed); registry publication does (electrs must
	// have indexed the issuance). Both are best-effort and idempotent.
	s.maybeSeedPrice(w)
	if w.Confirmations >= 1 {
		s.maybePublishRegistry(w)
	}
}

// computeWatch reads the chain and updates w in place, returning whether a
// material field changed. It never invents state: an RPC it cannot complete
// leaves the prior persisted state untouched (so a transient node hiccup does
// not fabricate a regression), and it only regresses on positive evidence that a
// confirming block left the active chain.
func (s *server) computeWatch(w *WatchState) bool {
	var tipAnchorHeight int64
	anchoringOn := false
	if a, err := s.anchorStatus(); err == nil {
		tipAnchorHeight = a.AnchorHeight
		w.AnchorStatus = a.AnchorStatus
		anchoringOn = a.AnchorHeight > 0 || a.AnchorStatus != ""
	}

	// Discover the confirming block if we do not have one yet.
	if w.BlockHash == "" {
		if tx, err := s.rawTransaction(w.Txid); err == nil && tx.BlockHash != "" {
			w.BlockHash = tx.BlockHash
		}
	}
	if w.BlockHash == "" {
		// Still unconfirmed (in the mempool) or not yet discoverable.
		return applyState(w, statusBroadcast, 0, 0, 0, 0, "")
	}

	hdr, err := s.blockHeader(w.BlockHash)
	if err != nil {
		return false // cannot read the header this tick; keep prior state
	}
	if hdr.Confirmations < 0 {
		// The confirming block is no longer in the active chain: a Bitcoin-driven
		// reorg unwound it. Clear the block so a re-confirmation is rediscovered.
		w.BlockHash = ""
		return applyState(w, statusRegressed, 0, 0, 0, 0, "")
	}

	depth := int64(0)
	status := statusConfirmed
	if anchoringOn && hdr.AnchorHeight > 0 {
		depth = tipAnchorHeight - hdr.AnchorHeight
		if depth < 0 {
			depth = 0
		}
		if depth >= 1 {
			status = statusAnchored
		}
	}
	return applyState(w, status, hdr.Confirmations, depth, hdr.Height, hdr.AnchorHeight, hdr.AnchorHash)
}

// applyState writes the computed fields onto w and reports whether any material
// (rendered) field changed, so the watcher emits an event exactly on change.
func applyState(w *WatchState, status string, conf, depth, blockHeight, anchorHeight int64, anchorHash string) bool {
	changed := w.Status != status || w.Confirmations != conf || w.AnchorDepth != depth ||
		w.BlockHeight != blockHeight || w.AnchorHeight != anchorHeight
	w.Status = status
	w.Confirmations = conf
	w.AnchorDepth = depth
	w.BlockHeight = blockHeight
	w.AnchorHeight = anchorHeight
	w.AnchorHash = anchorHash
	return changed
}

// onDeployed is called by the deploy path right after a successful mint. It
// records the watch row (with the canonical contract for later registry
// publication) and kicks an immediate best-effort tick so the SPA sees
// "broadcast" without waiting a whole interval. It never fails the deploy.
func (s *server) onDeployed(iss *Issuance, contract string) {
	w := &WatchState{
		IssuanceID: iss.ID,
		OwnerAID:   iss.OwnerAID,
		AssetID:    iss.AssetID,
		Txid:       iss.Txid,
		Status:     statusBroadcast,
		Contract:   contract,
	}
	if err := s.st.UpsertWatch(w); err != nil {
		log.Printf("deploy: seed watch for %s: %v", iss.ID, err)
		return
	}
	s.bus().publish(w.OwnerAID, eventFrom(w))
	go func() {
		if cur, err := s.st.WatchByIssuance(iss.ID); err == nil && cur != nil {
			cur.Contract = contract
			s.watchTickOne(cur)
		}
	}()
}

// runAnchorCron triggers openampd's log-head anchor daily so the transparency
// log head is committed on-chain (an OP_RETURN an auditor can find on the
// explorer). This is openampd's log anchor, NOT seqpald's own state anchor,
// which is M7. The returned anchor txids are surfaced through the owner-scoped
// log proxy (the "anchor" entries in /v1/log) and audit-logged here.
func (s *server) runAnchorCron(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		s.anchorLogHead()
	}
}

func (s *server) anchorLogHead() {
	txid, seq, err := s.postLogHeadAnchor()
	if err != nil {
		if s.cfg.issuerToken != "" {
			log.Printf("anchor cron: %v", err)
		}
		return
	}
	s.st.Audit("", "log.anchor", map[string]any{"txid": txid, "seq": seq})
	log.Printf("anchor cron: log head anchored, txid=%s seq=%d", txid, seq)
}
