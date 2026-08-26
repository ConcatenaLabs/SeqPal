package main

import (
	"encoding/json"
	"log"
	"time"
)

// BTC reorged-deposit handling (M6 section 5). The native-BTC rail is registrar-
// style: the payment confirms on Bitcoin testnet4, then delivery is co-signed on
// Sequentia (two chains, NOT atomic). That leaves a residual window a USDX atomic
// close does not have: a settled BTC deposit can be reorged out AFTER the tokens
// were delivered on Sequentia. Bitcoin anchoring is supreme, so a reorg on the
// parent chain is authoritative and must be reflected, never papered over.
//
// This watcher keeps tracking each settled BTC subscription's deposit txid on
// testnet4 after delivery. If the deposit is reorged out (its confirmations drop
// to zero or negative), seqpald applies a GLOBAL account freeze on the investor's
// AID via the policy server, recording the cross-asset blast radius (the freeze
// affects every asset the investor holds, not only this offering). On a re-
// confirmation the freeze this hold caused is lifted; a persistent reorg is
// resolved out of band by clawback-with-reason (offerings on the BTC rail should
// enable clawback, else the residual reorg risk stands, disclosed at subscribe).

// runBtcReorgWatcher is the background loop. It only runs when the testnet4 RPC is
// configured (the BTC rail is enabled).
func (s *server) runBtcReorgWatcher(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		s.watchBtcReorgs()
	}
}

// watchBtcReorgs scans every settled native-BTC subscription's deposit and every
// active hold once. It acts only on positive evidence: a transient RPC failure
// leaves the prior state untouched, so a node hiccup never fabricates a freeze.
func (s *server) watchBtcReorgs() {
	if s.cfg.btcURL == "" {
		return
	}
	settled, err := s.st.SubscriptionsByState("settled")
	if err != nil {
		return
	}
	for _, sub := range settled {
		if sub.Rail != "btc" || sub.DepositTxid == "" {
			continue
		}
		confs, ok := s.btcTxConfirmations(sub.DepositTxid)
		if !ok {
			continue // no definitive answer this tick
		}
		hold, _ := s.st.ReorgHoldBySub(sub.ID)
		reorgedOut := confs <= 0 // back in the mempool (0) or conflicted (<0): no longer in a block
		switch {
		case reorgedOut && (hold == nil || hold.State != "active"):
			s.freezeForReorg(sub, confs)
		case !reorgedOut && confs >= s.cfg.escrowConfs && hold != nil && hold.State == "active":
			s.clearReorgHold(sub, confs)
		}
	}
}

// freezeForReorg applies the global account freeze and records the hold. The audit
// discloses the blast radius: freezing an AID freezes it across every asset it
// holds under the single-operator policy model, not only this offering.
func (s *server) freezeForReorg(sub *Subscription, confs int64) {
	reason := "native-BTC deposit reorged out post-delivery"
	if err := s.callOpenAMP("POST", "/v1/issuer/freeze", s.cfg.issuerToken,
		map[string]any{"aid": s.openampAIDFor(sub.InvestorAID), "frozen": true}, nil); err != nil {
		log.Printf("reorg watch: freeze %s: %v", sub.InvestorAID, err)
		return
	}
	_ = s.st.UpsertReorgHold(&ReorgHold{
		SubscriptionID: sub.ID, IssuanceID: sub.IssuanceID, InvestorAID: sub.InvestorAID,
		DepositTxid: sub.DepositTxid, State: "active", Reason: reason,
	})
	s.st.Audit(sub.InvestorAID, "reorg.freeze", map[string]any{
		"issuance_id": sub.IssuanceID, "sub": sub.ID, "deposit_txid": sub.DepositTxid,
		"confirmations": confs, "reason": reason,
		"blast_radius": "global: this freezes the investor AID across every asset they hold, not only this offering",
		"resolution":   "unfreeze on re-confirmation, or clawback-with-reason if the deposit does not re-confirm",
	})
	log.Printf("reorg watch: froze %s (deposit %s reorged out, confs=%d)", sub.InvestorAID, sub.DepositTxid, confs)
}

// clearReorgHold lifts the freeze this hold caused once the deposit re-confirms. It
// never lifts a sanctions freeze: if the investor's claims are refused, the freeze
// stays and the hold is only marked cleared so it stops being re-evaluated.
func (s *server) clearReorgHold(sub *Subscription, confs int64) {
	if claims, _ := s.st.ClaimsByAID(sub.InvestorAID); claims != nil && claims.Status == "refused" {
		_ = s.st.UpdateReorgHoldFields(sub.ID, map[string]any{"state": "cleared", "reason": "deposit re-confirmed; account remains frozen for another reason"})
		s.st.Audit(sub.InvestorAID, "reorg.hold_cleared_frozen", map[string]any{
			"issuance_id": sub.IssuanceID, "sub": sub.ID, "confirmations": confs,
			"note": "deposit re-confirmed but the account stays frozen (claims refused); not auto-unfrozen",
		})
		return
	}
	if err := s.callOpenAMP("POST", "/v1/issuer/freeze", s.cfg.issuerToken,
		map[string]any{"aid": s.openampAIDFor(sub.InvestorAID), "frozen": false}, nil); err != nil {
		log.Printf("reorg watch: unfreeze %s: %v", sub.InvestorAID, err)
		return
	}
	_ = s.st.UpdateReorgHoldFields(sub.ID, map[string]any{"state": "cleared", "reason": "deposit re-confirmed"})
	s.st.Audit(sub.InvestorAID, "reorg.unfreeze", map[string]any{
		"issuance_id": sub.IssuanceID, "sub": sub.ID, "deposit_txid": sub.DepositTxid, "confirmations": confs,
	})
	log.Printf("reorg watch: unfroze %s (deposit %s re-confirmed, confs=%d)", sub.InvestorAID, sub.DepositTxid, confs)
}

// btcTxConfirmations reads a deposit tx's confirmation count from the testnet4
// escrow wallet. ok is false on a transient failure (no action taken). A reorged
// deposit stays in the wallet with confirmations 0 (back in the mempool) or
// negative (conflicted by a replacement), which is how the reorg is detected.
func (s *server) btcTxConfirmations(txid string) (int64, bool) {
	if err := s.ensureBTCEscrowWallet(); err != nil {
		return 0, false
	}
	res, err := s.btcRPC(btcEscrowWallet, "gettransaction", txid)
	if err != nil {
		return 0, false
	}
	var out struct {
		Confirmations int64 `json:"confirmations"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return 0, false
	}
	return out.Confirmations, true
}
