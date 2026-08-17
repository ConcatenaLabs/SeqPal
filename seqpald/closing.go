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

	// --- ATOMIC DvP (closing v2) ---------------------------------------------
	// A USDX subscription against a transparent restricted asset settles as ONE
	// multi-asset transaction: the token delivery and the USDX release ride in the
	// same tx (M6 atomic delivery-versus-payment). It preserves the M5 idempotency +
	// reconciliation invariant (one tx per subscription, intent persisted before
	// broadcast, reconciled by chain scan before any retry), and it transparently
	// falls back to the v1 two-transaction close when the policy server has no
	// payment leg (OA-4 absent) or atomic close is disabled.
	//
	// Two distinct entry conditions, only ever from a state with no closing-v1
	// movement (DeliveryTxid/ReleaseTxid both empty):
	//
	//   - INFLIGHT reconcile: st.State == "settling" is written ONLY by
	//     settleOneAtomic, so it always means an atomic tx may already be broadcast.
	//     It MUST be reconciled through the atomic path regardless of the current
	//     atomicClose flag. Flipping the flag OFF mid-settlement and then running the
	//     v1 release would double-pay the issuer (the atomic tx already paid them),
	//     so the flag is deliberately NOT consulted here.
	//   - FRESH start: st.State == "pending" consults atomicEligible (which includes
	//     the flag) to decide whether to open an atomic close at all.
	//
	// A settlement that already reached a v1 progress state ("delivering"/"delivered"
	// /"releasing"/"released") is neither, so it keeps running v1: settleOneAtomic
	// only reconciles "settling", so routing a mid-"delivering" v1 close (tokens on
	// chain but the delivery_txid write lost) into the atomic path would build and
	// broadcast a SECOND delivery+payment tx (an atomic-enable-after-v1-crash
	// double-settle). Both are gated out here.
	inflightAtomic := st.State == "settling"
	freshAtomic := st.State == "pending" && s.atomicEligible(iss, sub)
	if st.DeliveryTxid == "" && st.ReleaseTxid == "" && (inflightAtomic || freshAtomic) {
		if res, ok := s.settleOneAtomic(iss, escrow, sub, st, investor, closeHeight); ok {
			return res
		}
		// settleOneAtomic returned (nil,false): the policy server has no payment leg
		// and NOTHING was broadcast this attempt.
		if inflightAtomic {
			// A prior atomic attempt is in flight (its tx may still be in the mempool).
			// Do NOT fall to the v1 release: if that atomic tx confirms, a v1 release
			// double-pays the issuer. Leave it "settling" to reconcile on a later retry
			// once the tx confirms or the payment leg is restored.
			return settleResult(sub, "settling", "atomic settlement in flight; awaiting confirmation or policy payment leg")
		}
		// Fresh settlement, capability absent: fall through to closing v1 cleanly.
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
		fee, net, feeAccrued := s.escrowFeeFor(sub)
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
			// W-6: when the fee accrued at deposit confirmation, its ledger row
			// already exists; writing a second one here would double-count the
			// books. Only the legacy (pre-accrual, e.g. fiat) path records it now.
			if fee > 0 && !feeAccrued {
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

// escrowFeeSplit divides a deposited amount into the escrow fee (bps) and the
// net paid out to the issuer. It is defensive: a fee that would meet or exceed
// the deposit is dropped so the release is never negative.
func escrowFeeSplit(sub *Subscription, bps int64) (fee, net uint64) {
	fee = sub.DepositedAtoms * uint64(bps) / 10000
	net = sub.DepositedAtoms
	if net > fee {
		net -= fee
	} else {
		fee, net = 0, sub.DepositedAtoms
	}
	return fee, net
}

// escrowFeeFor is the W-6 fee source of truth at settlement time: the fee that
// ACCRUED when the deposit confirmed (moneywatch wrote it to the ledger), never
// a recomputation, so a bps change between deposit and close cannot move an
// already-due fee. accrued=false means no accrual row exists (a fiat-funded
// subscription, or one deposited before W-6), and the legacy split computes it.
func (s *server) escrowFeeFor(sub *Subscription) (fee, net uint64, accrued bool) {
	if f, ok, err := s.st.AccruedEscrowFee(sub.ID); err == nil && ok {
		net = sub.DepositedAtoms
		if net > f {
			return f, net - f, true
		}
		// Defensive, like escrowFeeSplit: never a negative release.
		return 0, sub.DepositedAtoms, true
	}
	f, n := escrowFeeSplit(sub, s.cfg.escrowFeeBps)
	return f, n, false
}

// atomicEligible reports whether a subscription can settle as one atomic DvP tx:
// the USDX rail (a Sequentia payment asset that can share a tx with the restricted
// delivery), atomic close enabled, and a funded escrow deposit at a known
// from_address. The atomic path builds a fully explicit transaction, which is
// always true for a closing: confidentiality is a per-transfer choice, and a
// primary delivery is a transparent transfer. BTC keeps the registrar close; fiat
// keeps the SIMULATED release.
func (s *server) atomicEligible(iss *Issuance, sub *Subscription) bool {
	return s.cfg.atomicClose && sub.Rail == "usdx" &&
		s.cfg.usdxAsset != "" && sub.DepositAddress != "" && sub.DepositedAtoms > 0
}

// settleOneAtomic settles a subscription with a single multi-asset transaction
// (token delivery + USDX release). It returns (result, true) when it owns the
// settlement (settled, reconciled, refunded, or a hard configuration failure), and
// (nil, false) ONLY when the policy server has no payment leg and nothing was
// broadcast, so settleOne can fall back to closing v1 cleanly.
func (s *server) settleOneAtomic(iss *Issuance, escrow *EnclaveKey, sub *Subscription, st *Settlement, investor *Account, closeHeight int64) (map[string]any, bool) {
	fee, net, feeAccrued := s.escrowFeeFor(sub)

	// Reconcile a lost write: a prior attempt may have broadcast the atomic tx
	// (state "settling", no recorded txid). Because delivery and release are the
	// SAME transaction, a chain-backed balance scan showing the tokens landed proves
	// BOTH legs settled; never rebroadcast in that case.
	if st.State == "settling" && s.deliveryAlreadyDone(iss, sub) {
		s.recordAtomicSettled(iss, sub, st, investor, "reconciled", fee, net, closeHeight, true, feeAccrued)
		return settleResult(sub, "settled", ""), true
	}

	// The issuer's registered Sequentia payout mandate is the USDX payee (never an
	// enclave). A missing mandate is a hard configuration error, exactly as in the
	// v1 release; do not fall back to v1 (it would fail the same way).
	m, err := s.st.MandateFor(iss.ID, "sequentia")
	if err != nil || m == nil || m.Address == "" {
		s.failSettlement(sub, st, "atomic close: no Sequentia payout mandate is registered")
		return settleResult(sub, "failed", "no Sequentia payout mandate is registered"), true
	}

	// Build the atomic tx. This build is the OA-4 capability probe: a policy server
	// without the payment leg refuses the request (or produces no payment inputs),
	// and we fall back to v1 without having broadcast anything.
	built, capable, berr := s.buildAtomicTransfer(escrow, sub, iss.AssetID, m.Address, net)
	if !capable {
		return nil, false
	}
	if berr != nil {
		// Build failed for a real reason with nothing broadcast: reconcile once (in
		// case an earlier attempt landed), else refund the full deposit.
		if s.deliveryAlreadyDone(iss, sub) {
			s.recordAtomicSettled(iss, sub, st, investor, "reconciled", fee, net, closeHeight, true, feeAccrued)
			return settleResult(sub, "settled", ""), true
		}
		return s.refundSubscription(iss, sub, st, "atomic close build failed: "+berr.Error()), true
	}

	// Persist the intent BEFORE broadcasting so a lost write after broadcast is
	// reconcilable (the M5 fund-safety invariant, here covering both legs at once).
	_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"state": "settling"})

	txid, cerr := s.completeAtomicTransfer(escrow, built)
	if cerr != nil {
		if s.deliveryAlreadyDone(iss, sub) {
			s.recordAtomicSettled(iss, sub, st, investor, "reconciled", fee, net, closeHeight, true, feeAccrued)
			return settleResult(sub, "settled", ""), true
		}
		return s.refundSubscription(iss, sub, st, "atomic close failed: "+cerr.Error()), true
	}
	s.recordAtomicSettled(iss, sub, st, investor, txid, fee, net, closeHeight, false, feeAccrued)
	return settleResult(sub, "settled", ""), true
}

// recordAtomicSettled writes the terminal settlement state for an atomic close:
// the single txid is BOTH the delivery and the release txid (one transaction). It
// stamps US-tranche vesting and marks the subscription settled. On a reconciled
// close (a prior attempt already landed the tx) the ledger entries are skipped, so
// a replay never double-writes the books, exactly like the v1 reconcile paths.
func (s *server) recordAtomicSettled(iss *Issuance, sub *Subscription, st *Settlement, investor *Account, txid string, fee, net uint64, closeHeight int64, reconciled, feeAccrued bool) {
	_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{
		"delivery_txid": txid, "release_txid": txid, "fee_atoms": fee, "state": "settled"})
	st.DeliveryTxid, st.ReleaseTxid = txid, txid
	if !reconciled {
		_ = s.st.InsertLedger(&LedgerEntry{
			SubscriptionID: sub.ID, IssuanceID: iss.ID, Kind: "delivery", Rail: sub.Rail,
			Amount: sub.TokenAtoms, Ccy: iss.Ticker, Txid: txid, FundsSimulated: sub.FundsSimulated,
		})
		// W-6: an accrued fee is already on the ledger; do not double-write it.
		if fee > 0 && !feeAccrued {
			_ = s.st.InsertLedger(&LedgerEntry{
				SubscriptionID: sub.ID, IssuanceID: iss.ID, Kind: "escrow_fee", Rail: sub.Rail,
				Amount: fee, Ccy: sub.PayCcy, FundsSimulated: sub.FundsSimulated,
			})
		}
		_ = s.st.InsertLedger(&LedgerEntry{
			SubscriptionID: sub.ID, IssuanceID: iss.ID, Kind: "release", Rail: sub.Rail,
			Amount: net, Ccy: sub.PayCcy, Txid: txid, FundsSimulated: sub.FundsSimulated,
		})
	}
	s.maybeStampVesting(iss, sub, investor, closeHeight)
	_ = s.st.UpdateSubscriptionFields(sub.ID, map[string]any{"state": "settled"})
	s.st.Audit(sub.InvestorAID, "settle.done", map[string]any{
		"issuance_id": iss.ID, "sub": sub.ID, "atomic": true, "txid": txid, "reconciled": reconciled,
	})
}

// atomicBuild is the OA-4 /v1/transfers response for a transfer carrying a payment
// leg: the transfer id, the assembled (transparent) tx hex, the enclave sighashes
// to sign, and the indices of the ordinary payment inputs seqpald must sign itself.
type atomicBuild struct {
	ID     string `json:"id"`
	Tx     string `json:"tx"`
	ToSign []struct {
		Input   int    `json:"input"`
		Sighash string `json:"sighash"`
		Pubkey  string `json:"pubkey"`
	} `json:"to_sign"`
	PaymentInputs []int `json:"payment_inputs"`
}

// buildAtomicTransfer asks openampd to assemble a single tx delivering the tokens
// escrow-enclave -> investor AND paying net USDX escrow -> the issuer's payout
// address, plus the self-paid network fee. capable is false (with a nil error) only
// when the policy server has no payment leg, so the caller falls back to v1.
func (s *server) buildAtomicTransfer(escrow *EnclaveKey, sub *Subscription, assetID, payTo string, net uint64) (*atomicBuild, bool, error) {
	body := map[string]any{
		"asset": assetID, "sender_aid": escrow.AID, "recipient_aid": sub.InvestorAID,
		"atoms": sub.TokenAtoms, "fee_mode": "sponsor",
		"payment": map[string]any{
			"asset": s.cfg.usdxAsset, "atoms": net,
			"from_address": sub.DepositAddress, "to_address": payTo,
		},
	}
	var built atomicBuild
	if err := s.callOpenAMP("POST", "/v1/transfers", "", body, &built); err != nil {
		if isPaymentLegUnsupported(err) {
			return nil, false, nil // OA-4 absent (unknown field): fall back to v1
		}
		return nil, true, err
	}
	if built.ID == "" {
		return nil, true, fmt.Errorf("atomic transfer build returned no id")
	}
	// A real OA-4 build returns the assembled transaction; when it does, it MUST
	// also carry the payment inputs, otherwise the tx would deliver the tokens and
	// release nothing. (Refuse that; do not broadcast a half tx.)
	if built.Tx != "" && len(built.PaymentInputs) == 0 {
		return nil, true, fmt.Errorf("atomic build returned a transaction with no payment inputs")
	}
	return &built, true, nil
}

// completeAtomicTransfer signs the escrow enclave (restricted) inputs with the
// custodied escrow key and the escrow wallet's USDX (payment) inputs with the
// seqpal-escrow node wallet, then hands openampd the partially-signed payment tx.
// openampd pins the body by txid and lifts only the payment witnesses, co-signs the
// policy inputs, funds and signs its own fee input, and broadcasts, returning the
// single atomic txid.
func (s *server) completeAtomicTransfer(escrow *EnclaveKey, built *atomicBuild) (string, error) {
	sigs := map[string]string{}
	for _, ts := range built.ToSign {
		if ts.Pubkey != "" && ts.Pubkey != escrow.XOnly {
			return "", fmt.Errorf("atomic transfer wants a signature from a key the escrow does not hold")
		}
		sig, err := signSighash(escrow.Priv, ts.Sighash)
		if err != nil {
			return "", err
		}
		sigs[fmt.Sprintf("%d", ts.Input)] = sig
	}
	complete := map[string]any{"sigs": sigs}
	// The escrow's USDX payment inputs are signed with the seqpal-escrow node wallet
	// over the assembled tx. openampd pins the body by txid and lifts only those
	// witnesses. (A build that carries no assembled tx, e.g. the degraded hosted
	// flow, is completed with the enclave sigs alone.)
	if built.Tx != "" && len(built.PaymentInputs) > 0 {
		paymentTx, err := s.escrowSignPaymentTx(built.Tx)
		if err != nil {
			return "", err
		}
		complete["payment_tx"] = paymentTx
	}
	var done struct {
		Txid string `json:"txid"`
	}
	if err := s.callOpenAMP("POST", "/v1/transfers/"+built.ID+"/complete", "",
		complete, &done); err != nil {
		return "", err
	}
	if done.Txid == "" {
		return "", fmt.Errorf("atomic transfer complete returned no txid")
	}
	return done.Txid, nil
}

// escrowSignPaymentTx signs the escrow wallet's own USDX payment inputs over the
// built tx with signrawtransactionwithwallet. Only the inputs seqpal-escrow
// controls are signed (the enclave and fee inputs stay unsigned for openampd to
// complete); the returned tx body is identical, so openampd's txid pin holds.
func (s *server) escrowSignPaymentTx(txHex string) (string, error) {
	if err := s.ensureSeqEscrowWallet(); err != nil {
		return "", err
	}
	res, err := s.walletRPC(seqEscrowWallet, "signrawtransactionwithwallet", txHex)
	if err != nil {
		return "", err
	}
	var out struct {
		Hex      string `json:"hex"`
		Complete bool   `json:"complete"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", err
	}
	if out.Hex == "" {
		return "", fmt.Errorf("escrow wallet returned no signed payment tx")
	}
	return out.Hex, nil
}

// isPaymentLegUnsupported detects a policy server without the OA-4 payment leg: it
// rejects the unknown "payment" field (DisallowUnknownFields), which is our clean
// capability signal to fall back to closing v1.
func isPaymentLegUnsupported(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unknown field") && strings.Contains(msg, "payment")
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
	// Every policy-rules mutation flows through the amendment-chain chokepoint (M7
	// section 3): it posts the rules AND records an anchored amendment, so the
	// Rule 144 vesting stamp joins the asset's amendment chain rather than mutating
	// the on-chain rules silently.
	basis := fmt.Sprintf("Rule 144 vesting stamp for US-tranche purchaser %s at close (until Sequentia block %d)", sub.InvestorAID, until)
	if _, err := s.applyRulesMutation(iss, rules, basis, until); err != nil {
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
	// W-6: the escrow fee accrued at deposit confirmation is due regardless of
	// outcome, so it is WITHHELD from the refund. Its ledger row (written at
	// accrual) is the record of the withholding; the refund row carries the net.
	feeWithheld := uint64(0)
	refundAtoms := sub.DepositedAtoms
	if f, ok, err := s.st.AccruedEscrowFee(sub.ID); err == nil && ok && f < refundAtoms {
		feeWithheld = f
		refundAtoms -= f
	}
	// Reconcile before any retry: if a prior attempt already broadcast the refund
	// (state "refunding" but no recorded txid), find it by its comment and record it
	// rather than re-sending from the commingled escrow wallet.
	if st.State == "refunding" && (sub.Rail == "usdx" || sub.Rail == "btc") {
		if txid, found := s.escrowFindSend(sub.Rail, refMarker); found {
			_ = s.st.InsertLedger(&LedgerEntry{
				SubscriptionID: sub.ID, IssuanceID: iss.ID, Kind: "refund", Rail: sub.Rail,
				Amount: refundAtoms, Ccy: sub.PayCcy, Txid: txid, FundsSimulated: false,
			})
			_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"state": "refunded", "refund_txid": txid, "fee_atoms": feeWithheld})
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
		if refundAtoms > 0 && sub.RefundAddress != "" {
			txid, err = s.releaseUSDX(sub.RefundAddress, refundAtoms, refMarker)
		}
	case "btc":
		if refundAtoms > 0 && sub.RefundAddress != "" {
			txid, err = s.sendBTC(sub.RefundAddress, refundAtoms, refMarker)
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
			Amount: refundAtoms, Ccy: sub.PayCcy, Txid: txid, FundsSimulated: false,
		})
	}
	_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"state": "refunded", "refund_txid": txid, "fee_atoms": feeWithheld})
	_ = s.st.UpdateSubscriptionFields(sub.ID, map[string]any{"state": "refunded"})
	s.st.Audit(sub.InvestorAID, "settle.refund", map[string]any{
		"issuance_id": iss.ID, "sub": sub.ID, "refund_txid": txid, "reason": reason,
		"refund_atoms": refundAtoms, "fee_withheld": feeWithheld,
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
