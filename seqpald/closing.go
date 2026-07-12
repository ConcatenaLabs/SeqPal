package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Closing v1: two transactions per subscription (delivery + release), not atomic
// DvP (that is M6). The issuer signs a single closing authorization; then for
// each in_escrow subscription seqpald runs:
//
//  1. DELIVERY: a policy-co-signed transfer of the purchased token amount from the
//     per-offering escrow ENCLAVE (holder = escrow AID, a primary AID so lockups
//     do not block it) to the investor's enclave. seqpald signs the holder side
//     with the custodied escrow key; openampd co-signs. US-tranche purchasers'
//     AIDs are stamped with a rules.Vesting entry (close height + ~12 months).
//  2. RELEASE: the escrowed payment is paid to the issuer's mandate address, minus
//     the accrued escrow fee.
//
// IDEMPOTENCY + RECONCILIATION: exactly one settlement record per subscription;
// each txid is recorded before it can be double-spent; an ambiguous state is
// reconciled by a chain/balance scan before any retry, never a double-deliver or
// double-release. A failed close auto-refunds the payment to the captured refund
// address.

const closeTag = "seqpal-close-v1"

type closeReq struct {
	Signature   string `json:"signature"`
	SignerXOnly string `json:"signer_xonly"`
}

func (s *server) handleClose(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	if iss.Status != "live" || iss.AssetID == "" {
		writeErr(w, 409, "this issuance is not live on chain")
		return
	}
	var req closeReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}

	// The issuer signs ONE closing authorization over the canonical statement.
	closeHeight := s.tipHeight()
	stmt, _ := canonicalJSON(map[string]any{"issuance_id": iss.ID, "action": "close"})
	if strings.TrimSpace(req.Signature) == "" {
		writeJSON(w, 200, map[string]any{"sign_this": string(stmt), "tag": closeTag,
			"note": "sign these canonical bytes with your SeqPal ID key, then resubmit with signature to authorize closing"})
		return
	}
	signer := strings.TrimSpace(req.SignerXOnly)
	if signer == "" {
		signer = acct.XOnly
	}
	if signer != acct.XOnly {
		writeErr(w, 403, "the closing authorization must be signed by the issuer's own key")
		return
	}
	if err := verifyTaggedByKey(signer, closeTag, stmt, req.Signature); err != nil {
		writeErr(w, 400, "the closing authorization signature does not verify")
		return
	}

	// The setup fee must be paid before closing pays anything out.
	if paid, err := s.setupFeePaid(iss.ID); err != nil {
		writeErr(w, 500, "store error")
		return
	} else if !paid {
		writeErr(w, 402, "the platform setup fee is unpaid; pay it before closing")
		return
	}

	// Serialize closing per issuance so vesting stamping and release accounting do
	// not race.
	unlock := s.closeMu.lock(iss.ID)
	defer unlock()

	escrow, err := s.st.EnclaveKeyByRef(enclaveOfferingEscrow, iss.ID)
	if err != nil || escrow == nil {
		writeErr(w, 500, "the offering escrow enclave is missing")
		return
	}

	subs, err := s.st.SubscriptionsByIssuanceState(iss.ID, "in_escrow")
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}

	// Close the offer window (document preimages become public) at close height.
	_ = s.st.CloseOffer(iss.ID, closeHeight)
	s.st.Audit(acct.AID, "close.begin", map[string]any{"issuance_id": iss.ID, "close_height": closeHeight, "subscriptions": len(subs)})

	results := make([]map[string]any, 0, len(subs))
	for _, sub := range subs {
		res := s.settleOne(iss, escrow, sub, closeHeight)
		results = append(results, res)
	}
	s.st.Audit(acct.AID, "close.done", map[string]any{"issuance_id": iss.ID, "settled": len(results)})
	writeJSON(w, 200, map[string]any{"issuance_id": iss.ID, "close_height": closeHeight, "results": results})
}

// settleOne runs the delivery+release state machine for one subscription with
// full idempotency. It is safe to call repeatedly: the settlement record is the
// single source of truth for what has already happened.
func (s *server) settleOne(iss *Issuance, escrow *EnclaveKey, sub *Subscription, closeHeight int64) map[string]any {
	// The idempotency anchor: create the settlement row if absent. If it already
	// exists we resume from its recorded state rather than re-running.
	if _, err := s.st.CreateSettlementIfAbsent(sub.ID, iss.ID); err != nil {
		return settleResult(sub, "error", err.Error())
	}
	st, err := s.st.SettlementByID(sub.ID)
	if err != nil || st == nil {
		return settleResult(sub, "error", "settlement record missing")
	}
	if st.State == "settled" {
		return settleResult(sub, "settled", "")
	}
	if st.State == "refunded" || st.State == "failed" {
		return settleResult(sub, st.State, st.Error)
	}

	investor, err := s.st.AccountByAID(sub.InvestorAID)
	if err != nil || investor == nil {
		s.failSettlement(sub, st, "investor account missing")
		return settleResult(sub, "failed", "investor account missing")
	}

	// --- DELIVERY -------------------------------------------------------------
	if st.DeliveryTxid == "" {
		// Reconcile: if a prior attempt already delivered (state "delivering" but no
		// recorded txid), a chain-backed balance scan detects it and we do NOT
		// re-deliver. Otherwise attempt delivery.
		if st.State == "delivering" && s.deliveryAlreadyDone(iss, sub) {
			_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"delivery_txid": "reconciled", "state": "delivered"})
			st.DeliveryTxid = "reconciled"
		} else {
			_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"state": "delivering"})
			txid, derr := s.deliverFromEscrow(escrow, sub.InvestorAID, iss.AssetID, sub.TokenAtoms)
			if derr != nil {
				// Delivery refused or failed: reconcile once, then refund if truly
				// undelivered (never leave funds captured on a failed close).
				if s.deliveryAlreadyDone(iss, sub) {
					_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"delivery_txid": "reconciled", "state": "delivered"})
					st.DeliveryTxid = "reconciled"
				} else {
					return s.refundSubscription(iss, sub, st, "delivery failed: "+derr.Error())
				}
			} else {
				_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"delivery_txid": txid, "state": "delivered"})
				st.DeliveryTxid = txid
				_ = s.st.InsertLedger(&LedgerEntry{
					SubscriptionID: sub.ID, IssuanceID: iss.ID, Kind: "delivery", Rail: sub.Rail,
					Amount: sub.TokenAtoms, Ccy: iss.Ticker, Txid: txid, FundsSimulated: sub.FundsSimulated,
				})
			}
		}
		// US-tranche Rule 144 vesting stamp (idempotent per AID) once delivered.
		s.maybeStampVesting(iss, sub, investor, closeHeight)
	}

	// --- RELEASE --------------------------------------------------------------
	if st.ReleaseTxid == "" {
		fee := sub.DepositedAtoms * uint64(s.cfg.escrowFeeBps) / 10000
		net := sub.DepositedAtoms
		if net > fee {
			net -= fee
		} else {
			fee, net = 0, sub.DepositedAtoms
		}
		relMarker := "seqpal-rel-" + sub.ID
		// Reconcile before any retry (mirrors delivery): if a prior attempt already
		// broadcast the release (state "releasing" but no recorded txid), find it by
		// its settlement-scoped comment and record it rather than re-sending from the
		// commingled escrow wallet.
		reconciled, recTxid := false, ""
		if st.State == "releasing" {
			if txid, found := s.escrowFindSend(sub.Rail, relMarker); found {
				reconciled, recTxid = true, txid
			}
		}
		if !reconciled {
			// Persist the intent BEFORE broadcasting so a lost write is reconcilable.
			_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"state": "releasing"})
			txid, rerr := s.releasePayment(iss, sub, net, relMarker)
			if rerr != nil {
				// Delivery already happened; a release failure is operational, not a
				// reason to claw back the delivered tokens. Record and surface it.
				s.failSettlement(sub, st, "release failed: "+rerr.Error())
				return settleResult(sub, "delivered_release_failed", rerr.Error())
			}
			recTxid = txid
		}
		_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"release_txid": recTxid, "fee_atoms": fee, "state": "released"})
		st.ReleaseTxid = recTxid
		if !reconciled {
			if fee > 0 {
				_ = s.st.InsertLedger(&LedgerEntry{
					SubscriptionID: sub.ID, IssuanceID: iss.ID, Kind: "escrow_fee", Rail: sub.Rail,
					Amount: fee, Ccy: sub.PayCcy, FundsSimulated: sub.FundsSimulated,
				})
			}
			_ = s.st.InsertLedger(&LedgerEntry{
				SubscriptionID: sub.ID, IssuanceID: iss.ID, Kind: "release", Rail: sub.Rail,
				Amount: net, Ccy: sub.PayCcy, Txid: recTxid, FundsSimulated: sub.FundsSimulated,
			})
		}
	}

	_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"state": "settled"})
	_ = s.st.UpdateSubscriptionFields(sub.ID, map[string]any{"state": "settled"})
	s.st.Audit(sub.InvestorAID, "settle.done", map[string]any{
		"issuance_id": iss.ID, "sub": sub.ID, "delivery_txid": st.DeliveryTxid, "release_txid": st.ReleaseTxid,
	})
	return settleResult(sub, "settled", "")
}

// deliverFromEscrow runs the hosted policy-co-signed transfer from the escrow
// enclave to the investor enclave. seqpald signs the holder side with the
// custodied escrow key; openampd co-signs and broadcasts, returning the txid.
func (s *server) deliverFromEscrow(escrow *EnclaveKey, investorAID, assetID string, atoms uint64) (string, error) {
	var built struct {
		ID     string `json:"id"`
		ToSign []struct {
			Input   int    `json:"input"`
			Sighash string `json:"sighash"`
			Pubkey  string `json:"pubkey"`
		} `json:"to_sign"`
	}
	body := map[string]any{
		"asset": assetID, "sender_aid": escrow.AID, "recipient_aid": investorAID,
		"atoms": atoms, "fee_mode": "sponsor",
	}
	if err := s.callOpenAMP("POST", "/v1/transfers", "", body, &built); err != nil {
		return "", err
	}
	if built.ID == "" {
		return "", fmt.Errorf("transfer build returned no id")
	}
	sigs := map[string]string{}
	for _, ts := range built.ToSign {
		if ts.Pubkey != "" && ts.Pubkey != escrow.XOnly {
			return "", fmt.Errorf("transfer wants a signature from a key the escrow does not hold")
		}
		sig, err := signSighash(escrow.Priv, ts.Sighash)
		if err != nil {
			return "", err
		}
		sigs[fmt.Sprintf("%d", ts.Input)] = sig
	}
	var done struct {
		Txid string `json:"txid"`
	}
	if err := s.callOpenAMP("POST", "/v1/transfers/"+built.ID+"/complete", "", map[string]any{"sigs": sigs}, &done); err != nil {
		return "", err
	}
	if done.Txid == "" {
		return "", fmt.Errorf("transfer complete returned no txid")
	}
	return done.Txid, nil
}

// deliveryAlreadyDone reconciles an ambiguous delivery by reading the investor
// enclave's on-chain balance for the asset: if it already holds at least the
// purchased amount, a prior attempt delivered and we must not re-deliver.
func (s *server) deliveryAlreadyDone(iss *Issuance, sub *Subscription) bool {
	var bal struct {
		Atoms uint64 `json:"atoms"`
	}
	if err := s.callOpenAMP("GET", "/v1/users/"+sub.InvestorAID+"/balance?asset="+iss.AssetID, "", nil, &bal); err != nil {
		return false
	}
	return bal.Atoms >= sub.TokenAtoms
}

// releasePayment pays the escrowed payment out to the issuer's registered mandate
// (USDX or tBTC on-chain), or records a SIMULATED release for a fiat-funded
// subscription (whose payment leg has no chain). net is the amount after the
// escrow fee.
func (s *server) releasePayment(iss *Issuance, sub *Subscription, net uint64, marker string) (string, error) {
	switch sub.Rail {
	case "usdx":
		m, err := s.st.MandateFor(iss.ID, "sequentia")
		if err != nil || m == nil {
			return "", fmt.Errorf("no Sequentia payout mandate is registered")
		}
		return s.releaseUSDX(m.Address, net, marker)
	case "btc":
		m, err := s.st.MandateFor(iss.ID, "bitcoin")
		if err != nil || m == nil {
			return "", fmt.Errorf("no Bitcoin payout mandate is registered")
		}
		return s.sendBTC(m.Address, net, marker)
	case "card", "bank":
		// Fiat funds have no chain: the release to the issuer is a SIMULATED payout,
		// labeled on the ledger. No mandate address is required.
		return "SIM-RELEASE-" + sub.ID, nil
	default:
		return "", fmt.Errorf("unknown rail %q", sub.Rail)
	}
}

// maybeStampVesting stamps a US-tranche purchaser's AID with a rules.Vesting entry
// (close height + ~12 months, the Rule 144 approximation). It reads the asset's
// current rules, appends/updates the entry for this AID, and writes them back. It
// is idempotent per AID: a re-run replaces that AID's entry rather than stacking.
func (s *server) maybeStampVesting(iss *Issuance, sub *Subscription, investor *Account, closeHeight int64) {
	claims, _ := s.st.ClaimsByAID(sub.InvestorAID)
	if claims == nil || !claims.USPerson {
		return
	}
	if !offeringRequiresUSAccredited(iss.Terms) && claims.Residence != "US" {
		// Only US-tranche purchasers carry the restriction.
		return
	}
	var asset struct {
		Rules json.RawMessage `json:"rules"`
	}
	if err := s.callOpenAMP("GET", "/v1/assets/"+iss.AssetID, "", nil, &asset); err != nil {
		return
	}
	var rules map[string]any
	if len(asset.Rules) > 0 {
		_ = json.Unmarshal(asset.Rules, &rules)
	}
	if rules == nil {
		rules = map[string]any{}
	}
	until := closeHeight + s.cfg.blocksPerDay*365 // ~12 months
	var vesting []map[string]any
	if raw, ok := rules["vesting"]; ok {
		if arr, ok := raw.([]any); ok {
			for _, e := range arr {
				if m, ok := e.(map[string]any); ok {
					if aid, _ := m["aid"].(string); aid == sub.InvestorAID {
						continue // replace this AID's entry
					}
					vesting = append(vesting, m)
				}
			}
		}
	}
	vesting = append(vesting, map[string]any{"aid": sub.InvestorAID, "atoms": sub.TokenAtoms, "until_height": until})
	rules["vesting"] = vesting
	if err := s.callOpenAMP("POST", "/v1/issuer/rules", s.cfg.issuerToken, map[string]any{"asset": iss.AssetID, "rules": rules}, nil); err != nil {
		s.st.Audit(sub.InvestorAID, "vesting.stamp_failed", map[string]any{"issuance_id": iss.ID, "error": err.Error()})
		return
	}
	s.st.Audit(sub.InvestorAID, "vesting.stamp", map[string]any{
		"issuance_id": iss.ID, "aid": sub.InvestorAID, "atoms": sub.TokenAtoms, "until_height": until,
	})
}

// refundSubscription auto-refunds a failed close: it pays the escrowed payment
// back to the captured refund address on the correct chain (or a SIMULATED fiat
// refund) and marks the subscription refunded.
func (s *server) refundSubscription(iss *Issuance, sub *Subscription, st *Settlement, reason string) map[string]any {
	refMarker := "seqpal-ref-" + sub.ID
	// Reconcile before any retry: if a prior attempt already broadcast the refund
	// (state "refunding" but no recorded txid), find it by its comment and record it
	// rather than re-sending from the commingled escrow wallet.
	if st.State == "refunding" && (sub.Rail == "usdx" || sub.Rail == "btc") {
		if txid, found := s.escrowFindSend(sub.Rail, refMarker); found {
			_ = s.st.InsertLedger(&LedgerEntry{
				SubscriptionID: sub.ID, IssuanceID: iss.ID, Kind: "refund", Rail: sub.Rail,
				Amount: sub.DepositedAtoms, Ccy: sub.PayCcy, Txid: txid, FundsSimulated: false,
			})
			_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"state": "refunded", "refund_txid": txid})
			_ = s.st.UpdateSubscriptionFields(sub.ID, map[string]any{"state": "refunded"})
			res := settleResult(sub, "refunded", reason)
			res["refund_txid"] = txid
			return res
		}
	}
	// Persist the intent BEFORE broadcasting so a lost write is reconcilable.
	_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"state": "refunding", "error": reason})
	var txid string
	var err error
	switch sub.Rail {
	case "usdx":
		if sub.DepositedAtoms > 0 && sub.RefundAddress != "" {
			txid, err = s.releaseUSDX(sub.RefundAddress, sub.DepositedAtoms, refMarker)
		}
	case "btc":
		if sub.DepositedAtoms > 0 && sub.RefundAddress != "" {
			txid, err = s.sendBTC(sub.RefundAddress, sub.DepositedAtoms, refMarker)
		}
	case "card", "bank":
		txid, err = s.refundFiatForSubscription(sub)
	}
	if err != nil {
		s.failSettlement(sub, st, "refund failed: "+err.Error())
		return settleResult(sub, "refund_failed", err.Error())
	}
	if txid != "" && !sub.FundsSimulated {
		_ = s.st.InsertLedger(&LedgerEntry{
			SubscriptionID: sub.ID, IssuanceID: iss.ID, Kind: "refund", Rail: sub.Rail,
			Amount: sub.DepositedAtoms, Ccy: sub.PayCcy, Txid: txid, FundsSimulated: false,
		})
	}
	_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"state": "refunded", "refund_txid": txid})
	_ = s.st.UpdateSubscriptionFields(sub.ID, map[string]any{"state": "refunded"})
	s.st.Audit(sub.InvestorAID, "settle.refund", map[string]any{
		"issuance_id": iss.ID, "sub": sub.ID, "refund_txid": txid, "reason": reason,
	})
	res := settleResult(sub, "refunded", reason)
	res["refund_txid"] = txid
	return res
}

func (s *server) failSettlement(sub *Subscription, st *Settlement, reason string) {
	_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"state": "failed", "error": reason})
	s.st.Audit(sub.InvestorAID, "settle.failed", map[string]any{"sub": sub.ID, "reason": reason})
}

func settleResult(sub *Subscription, state, msg string) map[string]any {
	r := map[string]any{"subscription_id": sub.ID, "investor_aid": sub.InvestorAID, "rail": sub.Rail, "state": state}
	if msg != "" {
		r["message"] = msg
	}
	return r
}

// handleSettlements is GET /issuances/{id}/settlements (owner): the settlement
// records for the offering (the audit view of what delivered/released/refunded).
func (s *server) handleSettlements(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	subs, err := s.st.SubscriptionsByIssuance(iss.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	out := make([]map[string]any, 0, len(subs))
	for _, sub := range subs {
		st, _ := s.st.SettlementByID(sub.ID)
		out = append(out, map[string]any{"subscription": sub, "settlement": st})
	}
	writeJSON(w, 200, map[string]any{"issuance_id": iss.ID, "settlements": out})
}
