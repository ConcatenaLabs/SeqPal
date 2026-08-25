package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Supervision operations (M10, bearer assets only). The node holds no keys and
// signs nothing: seqpald asks the node what must be signed, the issuer's
// browser signs it with the OPERATIONAL key (the session key), and seqpald
// assembles and submits the result.
//
// RAW-SIGNATURE EXCEPTION (documented beside signSighash's warning in
// taxonomy.go): the freeze and unfreeze signatures are BIP340 over RAW 32-byte
// NODE-PRODUCED messages (getsupervisionrecordhash / getsupervisionunfreezehash),
// NOT tagged hashes. This is the operational key's legitimate purpose, exactly
// like the SPA's signClawbackSighash: the message binds the funding
// transaction's first input prevout (freeze) or the record outpoint (unfreeze),
// so it can never collide with a challenge, a mandate, or a spend of anything
// else. Every other new signature in M10 is TAGGED.
//
// Front-running: a freeze that sat in the public mempool would be a chase the
// issuer loses, so the funding input is selected and LOCKED before the message
// is produced (the message binds that prevout), and the finished record goes to
// the producer over submitsupervisionrecord, the private issuer-to-producer
// channel. sendrawtransaction is only the fallback when that channel errors.
// An UNFREEZE spends a record rather than creating one, which the private
// channel refuses by design, so unfreezes broadcast normally.

// supervisionFeeAtoms is the flat fee a supervision transaction pays on the
// testnet (well above the any-asset floor for a small tx).
const supervisionFeeAtoms uint64 = 5000

// bearerIssuance loads an owned issuance and requires it to be a live
// bearer-enforcement asset; supervision controls exist for no other kind.
func (s *server) bearerIssuance(w http.ResponseWriter, acct *Account, id string) *Issuance {
	iss := s.ownedIssuance(w, acct, id)
	if iss == nil {
		return nil
	}
	if iss.Enforcement != "bearer" || iss.Status != "live" || iss.AssetID == "" {
		s.st.Audit(acct.AID, "supervision.refused", map[string]any{
			"issuance_id": id, "reason": "not a live bearer-enforcement asset",
		})
		writeErr(w, 409, "supervision controls apply only to a live bearer-enforcement asset")
		return nil
	}
	return iss
}

type supervisionReq struct {
	TargetAddress string `json:"target_address"`
	TargetScript  string `json:"target_script"`
	Reason        string `json:"reason"`
	OrderHash     string `json:"order_hash"`
	FreezeID      string `json:"freeze_id"` // unfreeze: the freeze op to lift (optional when target_* given)
}

func (r *supervisionReq) target() string {
	if t := strings.TrimSpace(r.TargetAddress); t != "" {
		return t
	}
	return strings.TrimSpace(r.TargetScript)
}

// handleSupervisionFreeze is POST /api/issuances/{id}/supervision/freeze
// (owner, bearer only): select and lock a funding UTXO, ask the node for the
// record message bound to it, persist the pending op (the idempotency anchor,
// including the chosen prevout), and return the 32-byte message the issuer's
// operational key must sign. Idempotent per (issuance, target, order_hash): a
// replayed build resumes the same op and prevout.
func (s *server) handleSupervisionFreeze(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.bearerIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	var req supervisionReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	refuse := func(code int, reason string) {
		s.st.Audit(acct.AID, "supervision.freeze.refused", map[string]any{
			"issuance_id": iss.ID, "status": code, "reason": reason,
		})
		writeErr(w, code, "%s", reason)
	}
	target := req.target()
	if target == "" {
		refuse(400, "target_address or target_script is required")
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		refuse(400, "a reason is required for every freeze")
		return
	}
	orderHash := strings.ToLower(strings.TrimSpace(req.OrderHash))
	if !isHex64(orderHash) {
		refuse(400, "order_hash (64 hex characters, the hash of the legal order this freeze executes) is required")
		return
	}

	s.ensureServicingMu()
	unlock := s.supMu.lock(iss.ID)
	defer unlock()

	// The consensus supervision registry only learns an asset when its
	// issuance CONFIRMS (records are validated against the registry through
	// the previous block), so a freeze built against an unconfirmed mint
	// would broadcast a record the chain rejects as naming an unsupervised
	// asset. Refuse with the real reason instead.
	if known, err := s.supervisedAssetKnown(iss.AssetID); err != nil {
		refuse(502, "getsupervisedassets: "+err.Error())
		return
	} else if !known {
		refuse(409, "the issuance has not confirmed yet; a freeze can be built once the asset is in the chain's supervision registry (wait for one confirmation)")
		return
	}

	// Probe first: whether the target is even freezable (single-owner) and
	// whether it is already frozen; both are surfaced, neither is guessed.
	probe, err := s.isAssetFrozen(iss.AssetID, target)
	if err != nil {
		refuse(502, "isassetfrozen: "+err.Error())
		return
	}
	if !probe.Freezable {
		refuse(400, "the target script is not freezable: a freeze binds only single-owner scripts (P2PKH, P2WPKH, P2TR key-path, P2SH-P2WPKH)")
		return
	}

	// Idempotent build: the same order against the same target resumes the
	// existing op (and its locked prevout) rather than locking a second UTXO.
	if prev, err := s.st.SupervisionOpFor(iss.ID, "freeze", probe.TargetHash, orderHash); err != nil {
		writeErr(w, 500, "store error")
		return
	} else if prev != nil {
		writeJSON(w, 200, s.freezeBuildResponse(prev, probe))
		return
	}

	// Select and LOCK the funding UTXO BEFORE producing the message: the record
	// hash binds this prevout, so the tx that carries the record is fixed now.
	fund, err := s.lockSupervisionFunding()
	if err != nil {
		refuse(503, "no spendable escrow funding UTXO for the freeze transaction: "+err.Error())
		return
	}

	res, err := s.nodeRPC("getsupervisionrecordhash", "freeze", iss.AssetID, target, nil, fund.Txid, fund.Vout)
	if err != nil {
		refuse(502, "getsupervisionrecordhash: "+err.Error())
		return
	}
	var rh struct {
		Sighash  string `json:"sighash"`
		SignWith string `json:"signwith"`
	}
	if err := json.Unmarshal(res, &rh); err != nil || rh.Sighash == "" {
		refuse(502, "getsupervisionrecordhash returned no message")
		return
	}

	op := &SupervisionOp{
		ID: mustID(), IssuanceID: iss.ID, AssetID: iss.AssetID, Kind: "freeze",
		Target: target, TargetHash: probe.TargetHash, Reason: strings.TrimSpace(req.Reason),
		OrderHash: orderHash, FundTxid: fund.Txid, FundVout: fund.Vout, FundAtoms: fund.Atoms,
		Sighash: rh.Sighash, State: "pending",
	}
	if err := s.st.InsertSupervisionOp(op); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	s.st.Audit(acct.AID, "supervision.freeze.build", map[string]any{
		"issuance_id": iss.ID, "asset": iss.AssetID, "freeze_id": op.ID,
		"target_hash": probe.TargetHash, "order_hash": orderHash, "reason": op.Reason,
		"fund_prevout": fund.Txid + ":" + fmt.Sprint(fund.Vout),
	})
	writeJSON(w, 200, s.freezeBuildResponse(op, probe))
}

func (s *server) freezeBuildResponse(op *SupervisionOp, probe *frozenProbe) map[string]any {
	resp := map[string]any{
		"freeze_id": op.ID,
		"to_sign":   op.Sighash, // the node-produced message, for a signer that already knows this layout
		"sign_with": "operational",
		"state":     op.State,
		// The FIELDS the message commits to, so the issuer's wallet can rebuild
		// it instead of being handed a digest. The node's message is a BIP340
		// tagged hash over exactly these, and a wallet that cannot check what it
		// signs must refuse to sign: the operational key is also half of the
		// 2-of-2 every restricted asset that issuer holds sits behind.
		"record": map[string]any{
			"kind":   "freeze",
			"asset":  op.AssetID,
			"target": op.Target,
			"txid":   op.FundTxid,
			"vout":   op.FundVout,
		},
		"note": "the issuer's wallet rebuilds this record from the fields in `record` and signs it with the " +
			"operational key, then POSTs the signature to /supervision/freeze/" + op.ID + "/complete; the record " +
			"is submitted over the private producer channel so it cannot be front-run",
	}
	if probe != nil {
		resp["freezable"] = probe.Freezable
		resp["frozen"] = probe.Frozen
		resp["paused"] = probe.Paused
		resp["targethash"] = probe.TargetHash
	}
	if op.Txid != "" {
		resp["txid"] = op.Txid
	}
	return resp
}

// handleSupervisionFreezeComplete is POST
// /api/issuances/{id}/supervision/freeze/{fid}/complete {sig}: assemble the
// funding tx carrying the record output (zero of the asset), sign the funding
// input with the escrow wallet, persist the raw tx and txid BEFORE broadcast,
// and submit over the private channel (sendrawtransaction as the fallback).
// Idempotent: a completed op replays its txid; an ambiguous submit resumes the
// stored raw tx rather than assembling a second record.
func (s *server) handleSupervisionFreezeComplete(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.bearerIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	var req struct {
		Sig string `json:"sig"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	s.ensureServicingMu()
	unlock := s.supMu.lock(iss.ID)
	defer unlock()

	op, err := s.st.SupervisionOpByID(r.PathValue("fid"))
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if op == nil || op.IssuanceID != iss.ID || op.Kind != "freeze" {
		writeErr(w, 404, "unknown freeze for this issuance")
		return
	}
	if op.State == "submitted" {
		writeJSON(w, 200, map[string]any{"freeze_id": op.ID, "txid": op.Txid, "state": op.State, "channel": op.Channel, "idempotent": true})
		return
	}

	if op.RawTx == "" {
		sig := strings.ToLower(strings.TrimSpace(req.Sig))
		if len(sig) != 128 || !isHexStr(sig) {
			writeErr(w, 400, "sig must be a 64-byte BIP340 signature in hex over the freeze message")
			return
		}
		// Assemble the record script from the issuer's signature, then the
		// funding tx: the locked prevout as vin[0] (the message binds it), change
		// back to escrow, an explicit fee output last, and the record output
		// (zero of the asset) inserted before the fee.
		buildRes, err := s.nodeRPC("buildsupervisionrecord", "freeze", op.AssetID, op.Target, nil, sig)
		if err != nil {
			writeErr(w, 502, "buildsupervisionrecord: %v", err)
			return
		}
		var built struct {
			Script     string `json:"script"`
			TargetHash string `json:"targethash"`
		}
		if err := json.Unmarshal(buildRes, &built); err != nil || built.Script == "" {
			writeErr(w, 502, "buildsupervisionrecord returned no script")
			return
		}
		signedHex, txid, recordVout, aerr := s.assembleRecordTx(op, built.Script)
		if aerr != nil {
			writeErr(w, 502, "assemble the freeze transaction: %v", aerr)
			return
		}
		// Persist BEFORE broadcast: the raw tx and its txid are the resume state.
		if err := s.st.UpdateSupervisionOpFields(op.ID, map[string]any{
			"state": "submitting", "rawtx": signedHex, "txid": txid,
			"record_script": built.Script, "record_vout": recordVout,
		}); err != nil {
			writeErr(w, 500, "store error")
			return
		}
		op.RawTx, op.Txid, op.RecordScript, op.RecordVout, op.State = signedHex, txid, built.Script, recordVout, "submitting"
	}

	channel, serr := s.submitSupervisionTx(op.RawTx)
	if serr != nil {
		_ = s.st.UpdateSupervisionOpFields(op.ID, map[string]any{"error": serr.Error()})
		writeErr(w, 502, "submit the freeze transaction: %v", serr)
		return
	}
	_ = s.st.UpdateSupervisionOpFields(op.ID, map[string]any{"state": "submitted", "channel": channel, "error": ""})
	s.st.Audit(acct.AID, "supervision.freeze.submit", map[string]any{
		"issuance_id": iss.ID, "asset": op.AssetID, "freeze_id": op.ID, "txid": op.Txid,
		"target_hash": op.TargetHash, "order_hash": op.OrderHash, "reason": op.Reason, "channel": channel,
	})
	writeJSON(w, 200, map[string]any{"freeze_id": op.ID, "txid": op.Txid, "state": "submitted", "channel": channel, "targethash": op.TargetHash})
}

// handleSupervisionUnfreeze is POST /api/issuances/{id}/supervision/unfreeze:
// build the unfreeze (spend the freeze record). Names the freeze by freeze_id
// or by target; returns the raw 32-byte message the operational key signs.
func (s *server) handleSupervisionUnfreeze(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.bearerIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	var req supervisionReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	refuse := func(code int, reason string) {
		s.st.Audit(acct.AID, "supervision.unfreeze.refused", map[string]any{
			"issuance_id": iss.ID, "status": code, "reason": reason,
		})
		writeErr(w, code, "%s", reason)
	}
	if strings.TrimSpace(req.Reason) == "" {
		refuse(400, "a reason is required for every unfreeze")
		return
	}
	orderHash := strings.ToLower(strings.TrimSpace(req.OrderHash))
	if !isHex64(orderHash) {
		refuse(400, "order_hash (64 hex characters) is required")
		return
	}

	s.ensureServicingMu()
	unlock := s.supMu.lock(iss.ID)
	defer unlock()

	// Locate the submitted freeze this lifts.
	freezeOp, err := s.findFreezeOp(iss, &req)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if freezeOp == nil || freezeOp.State != "submitted" || freezeOp.RecordVout < 0 {
		refuse(404, "no submitted freeze record found to unfreeze; name it by freeze_id or by its target")
		return
	}

	// Idempotent build per (target, order_hash), like the freeze build.
	if prev, err := s.st.SupervisionOpFor(iss.ID, "unfreeze", freezeOp.TargetHash, orderHash); err != nil {
		writeErr(w, 500, "store error")
		return
	} else if prev != nil {
		writeJSON(w, 200, s.unfreezeBuildResponse(prev))
		return
	}

	fund, err := s.lockSupervisionFunding()
	if err != nil {
		refuse(503, "no spendable escrow funding UTXO for the unfreeze transaction: "+err.Error())
		return
	}

	res, err := s.nodeRPC("getsupervisionunfreezehash", freezeOp.Txid, freezeOp.RecordVout, iss.AssetID, freezeOp.TargetHash)
	if err != nil {
		refuse(502, "getsupervisionunfreezehash: "+err.Error())
		return
	}
	var sighash string
	if err := json.Unmarshal(res, &sighash); err != nil || sighash == "" {
		refuse(502, "getsupervisionunfreezehash returned no message")
		return
	}

	op := &SupervisionOp{
		ID: mustID(), IssuanceID: iss.ID, AssetID: iss.AssetID, Kind: "unfreeze",
		Target: freezeOp.Target, TargetHash: freezeOp.TargetHash, Reason: strings.TrimSpace(req.Reason),
		OrderHash: orderHash, RefID: freezeOp.ID,
		FundTxid: fund.Txid, FundVout: fund.Vout, FundAtoms: fund.Atoms,
		Sighash: sighash, State: "pending",
	}
	if err := s.st.InsertSupervisionOp(op); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	s.st.Audit(acct.AID, "supervision.unfreeze.build", map[string]any{
		"issuance_id": iss.ID, "asset": iss.AssetID, "unfreeze_id": op.ID, "freeze_id": freezeOp.ID,
		"target_hash": op.TargetHash, "order_hash": orderHash, "reason": op.Reason,
	})
	writeJSON(w, 200, s.unfreezeBuildResponse(op))
}

func (s *server) unfreezeBuildResponse(op *SupervisionOp) map[string]any {
	resp := map[string]any{
		"unfreeze_id": op.ID,
		"freeze_id":   op.RefID,
		"to_sign":     op.Sighash, // the node-produced message, for a signer that already knows this layout
		"sign_with":   "operational",
		"state":       op.State,
		"targethash":  op.TargetHash,
		"note":        "the issuer's wallet rebuilds this from the fields in `record` and signs it with the operational key, then POSTs the signature to /supervision/unfreeze/" + op.ID + "/complete; spending the record is what lifts the freeze",
	}
	// A lift spends the freeze RECORD, so the outpoint the message binds is that
	// record's own, not a funding input.
	if freezeOp, err := s.st.SupervisionOpByID(op.RefID); err == nil && freezeOp != nil {
		resp["record"] = map[string]any{
			"kind":   "unfreeze",
			"asset":  op.AssetID,
			"target": op.Target,
			"txid":   freezeOp.Txid,
			"vout":   freezeOp.RecordVout,
		}
	}
	if op.Txid != "" {
		resp["txid"] = op.Txid
	}
	return resp
}

// handleSupervisionUnfreezeComplete is POST
// /api/issuances/{id}/supervision/unfreeze/{fid}/complete {sig}: build the tx
// spending the freeze record (input 0) plus the locked funding input, sign the
// funding input with the escrow wallet, place the issuer's signature in the
// record input's scriptSig (setsupervisionunfreezesig), persist before
// broadcast, and send. The private channel carries record CREATION only, so an
// unfreeze always broadcasts through the ordinary mempool.
func (s *server) handleSupervisionUnfreezeComplete(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.bearerIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	var req struct {
		Sig string `json:"sig"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	s.ensureServicingMu()
	unlock := s.supMu.lock(iss.ID)
	defer unlock()

	op, err := s.st.SupervisionOpByID(r.PathValue("fid"))
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if op == nil || op.IssuanceID != iss.ID || op.Kind != "unfreeze" {
		writeErr(w, 404, "unknown unfreeze for this issuance")
		return
	}
	if op.State == "submitted" {
		writeJSON(w, 200, map[string]any{"unfreeze_id": op.ID, "txid": op.Txid, "state": op.State, "idempotent": true})
		return
	}

	if op.RawTx == "" {
		sig := strings.ToLower(strings.TrimSpace(req.Sig))
		if len(sig) != 128 || !isHexStr(sig) {
			writeErr(w, 400, "sig must be a 64-byte BIP340 signature in hex over the unfreeze message")
			return
		}
		freezeOp, ferr := s.st.SupervisionOpByID(op.RefID)
		if ferr != nil || freezeOp == nil {
			writeErr(w, 500, "the freeze record this unfreeze lifts is missing")
			return
		}
		if op.FundAtoms <= supervisionFeeAtoms {
			writeErr(w, 502, "the locked funding UTXO cannot cover the fee")
			return
		}
		changeAddr, aerr := s.escrowNewAddress("seqpal-supervision-change")
		if aerr != nil {
			writeErr(w, 502, "derive a change address: %v", aerr)
			return
		}
		inputs := []map[string]any{
			{"txid": freezeOp.Txid, "vout": freezeOp.RecordVout}, // input 0: the record (scriptSig = the signature)
			{"txid": op.FundTxid, "vout": op.FundVout},           // input 1: fees
		}
		outputs := []map[string]any{
			{changeAddr: amount8(op.FundAtoms - supervisionFeeAtoms)},
			{"fee": amount8(supervisionFeeAtoms)},
		}
		rawRes, rerr := s.walletRPC(seqEscrowWallet, "createrawtransaction", inputs, outputs)
		if rerr != nil {
			writeErr(w, 502, "createrawtransaction: %v", rerr)
			return
		}
		var rawHex string
		if err := json.Unmarshal(rawRes, &rawHex); err != nil || rawHex == "" {
			writeErr(w, 502, "createrawtransaction returned no transaction")
			return
		}
		// The escrow wallet signs its funding input; the record input is not the
		// wallet's to sign, so complete=false is expected here.
		signRes, serr := s.walletRPC(seqEscrowWallet, "signrawtransactionwithwallet", rawHex)
		if serr != nil {
			writeErr(w, 502, "signrawtransactionwithwallet: %v", serr)
			return
		}
		var signed struct {
			Hex string `json:"hex"`
		}
		if err := json.Unmarshal(signRes, &signed); err != nil || signed.Hex == "" {
			writeErr(w, 502, "the wallet returned no signed transaction")
			return
		}
		setRes, uerr := s.nodeRPC("setsupervisionunfreezesig", signed.Hex, 0, sig)
		if uerr != nil {
			writeErr(w, 502, "setsupervisionunfreezesig: %v", uerr)
			return
		}
		var finalHex string
		if err := json.Unmarshal(setRes, &finalHex); err != nil || finalHex == "" {
			writeErr(w, 502, "setsupervisionunfreezesig returned no transaction")
			return
		}
		dec, derr := s.decodeRawTransaction(finalHex)
		if derr != nil || dec.Txid == "" {
			writeErr(w, 502, "the unfreeze transaction could not be decoded")
			return
		}
		if err := s.st.UpdateSupervisionOpFields(op.ID, map[string]any{
			"state": "submitting", "rawtx": finalHex, "txid": dec.Txid,
		}); err != nil {
			writeErr(w, 500, "store error")
			return
		}
		op.RawTx, op.Txid, op.State = finalHex, dec.Txid, "submitting"
	}

	if _, err := s.nodeRPC("sendrawtransaction", op.RawTx); err != nil && !txAlreadyKnown(err) {
		_ = s.st.UpdateSupervisionOpFields(op.ID, map[string]any{"error": err.Error()})
		writeErr(w, 502, "broadcast the unfreeze transaction: %v", err)
		return
	}
	_ = s.st.UpdateSupervisionOpFields(op.ID, map[string]any{"state": "submitted", "channel": "mempool", "error": ""})
	s.st.Audit(acct.AID, "supervision.unfreeze.submit", map[string]any{
		"issuance_id": iss.ID, "asset": op.AssetID, "unfreeze_id": op.ID, "freeze_id": op.RefID,
		"txid": op.Txid, "target_hash": op.TargetHash, "order_hash": op.OrderHash, "reason": op.Reason,
	})
	writeJSON(w, 200, map[string]any{"unfreeze_id": op.ID, "txid": op.Txid, "state": "submitted", "targethash": op.TargetHash})
}

// handleSupervisionStatus is GET /api/issuances/{id}/supervision (owner): the
// asset's supervision terms and current keys from the node's registry, the
// frozen target set, and this platform's own operations joined in for txids.
func (s *server) handleSupervisionStatus(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	if iss.Enforcement != "bearer" || iss.AssetID == "" {
		writeJSON(w, 200, map[string]any{"supervised": false, "enforcement": iss.Enforcement})
		return
	}
	ops, _ := s.st.SupervisionOpsByIssuance(iss.ID)
	// txids per target hash, from our own submitted ops.
	txidsByTarget := map[string][]string{}
	targetByHash := map[string]string{}
	for _, op := range ops {
		if op.State == "submitted" && op.Txid != "" {
			txidsByTarget[op.TargetHash] = append(txidsByTarget[op.TargetHash], op.Txid)
		}
		if op.Target != "" {
			targetByHash[op.TargetHash] = op.Target
		}
	}

	out := map[string]any{
		"supervised":      true,
		"enforcement":     "bearer",
		"asset":           iss.AssetID,
		"operational_key": iss.IssuerPubkey,
		"recovery_key":    iss.RecoveryPubkey,
		"pause":           iss.SupervisionPause,
		"ops":             ops,
	}

	// The node's registry view is authoritative when reachable (it reflects
	// rotations and third-party records this platform never made).
	if res, err := s.nodeRPC("getsupervisedassets"); err == nil {
		var assets []struct {
			Asset          string `json:"asset"`
			FeatureBits    int    `json:"featurebits"`
			OperationalKey string `json:"operationalkey"`
			RecoveryKey    string `json:"recoverykey"`
			PauseAllowed   bool   `json:"pauseallowed"`
			Paused         bool   `json:"paused"`
			Frozen         int    `json:"frozen"`
		}
		if json.Unmarshal(res, &assets) == nil {
			for _, a := range assets {
				if strings.EqualFold(a.Asset, iss.AssetID) {
					out["operational_key"] = a.OperationalKey
					out["recovery_key"] = a.RecoveryKey
					out["pause"] = a.PauseAllowed
					out["paused"] = a.Paused
					out["frozen_targets"] = a.Frozen
				}
			}
		}
	}
	freezes := []map[string]any{}
	if res, err := s.nodeRPC("getassetfreezes", iss.AssetID); err == nil {
		var list []struct {
			TargetHash string `json:"targethash"`
			Records    int    `json:"records"`
		}
		if json.Unmarshal(res, &list) == nil {
			for _, f := range list {
				row := map[string]any{
					"targethash": f.TargetHash,
					"records":    f.Records,
					"txids":      txidsByTarget[f.TargetHash],
				}
				if t := targetByHash[f.TargetHash]; t != "" {
					row["target"] = t
				}
				freezes = append(freezes, row)
			}
		}
	}
	out["freezes"] = freezes
	writeJSON(w, 200, out)
}

// --- shared plumbing ---------------------------------------------------------

type frozenProbe struct {
	Frozen     bool   `json:"frozen"`
	Paused     bool   `json:"paused"`
	Freezable  bool   `json:"freezable"`
	TargetHash string `json:"targethash"`
}

func (s *server) isAssetFrozen(asset, target string) (*frozenProbe, error) {
	res, err := s.nodeRPC("isassetfrozen", asset, target)
	if err != nil {
		return nil, err
	}
	var p frozenProbe
	if err := json.Unmarshal(res, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

type fundingUTXO struct {
	Txid  string
	Vout  int64
	Atoms uint64
}

// lockSupervisionFunding selects a spendable seqpal-escrow UTXO able to carry a
// supervision transaction's fee and change, and LOCKS it so a concurrent build
// cannot pick the same prevout (the record message binds it).
func (s *server) lockSupervisionFunding() (*fundingUTXO, error) {
	if err := s.ensureSeqEscrowWallet(); err != nil {
		return nil, err
	}
	// Only coins of the FEE asset can fund a supervision transaction: the
	// outputs pay change and fee in it, so a coin of any other asset (the
	// minted shares live in this same wallet) can never balance.
	feeAsset, err := s.bearerFeeAsset()
	if err != nil {
		return nil, err
	}
	res, err := s.walletRPC(seqEscrowWallet, "listunspent", 1, 9999999, []string{}, true, map[string]any{"asset": feeAsset})
	if err != nil {
		return nil, err
	}
	var utxos []struct {
		Txid          string      `json:"txid"`
		Vout          int64       `json:"vout"`
		Amount        json.Number `json:"amount"`
		Spendable     bool        `json:"spendable"`
		Confirmations int64       `json:"confirmations"`
	}
	if err := json.Unmarshal(res, &utxos); err != nil {
		return nil, err
	}
	for _, u := range utxos {
		atoms := atomsFromNumber(u.Amount)
		if !u.Spendable || atoms <= supervisionFeeAtoms*2 {
			continue
		}
		// lockunspent(false, ...) LOCKS; a failure means a concurrent build got
		// there first, so try the next candidate.
		if _, err := s.walletRPC(seqEscrowWallet, "lockunspent", false,
			[]map[string]any{{"txid": u.Txid, "vout": u.Vout}}); err != nil {
			continue
		}
		return &fundingUTXO{Txid: u.Txid, Vout: u.Vout, Atoms: atoms}, nil
	}
	return nil, fmt.Errorf("no unlocked UTXO above %d atoms", supervisionFeeAtoms*2)
}

// assembleRecordTx builds and wallet-signs the freeze funding transaction:
// vin[0] = the locked prevout the record message binds, outputs = change back
// to escrow, the record output (ZERO of the asset, inserted before the fee by
// addsupervisionrecordoutput), and the explicit fee output last. Returns the
// signed hex, its txid, and the record output's index.
func (s *server) assembleRecordTx(op *SupervisionOp, recordScript string) (signedHex, txid string, recordVout int64, err error) {
	if op.FundAtoms <= supervisionFeeAtoms {
		return "", "", -1, fmt.Errorf("the locked funding UTXO cannot cover the fee")
	}
	changeAddr, err := s.escrowNewAddress("seqpal-supervision-change")
	if err != nil {
		return "", "", -1, err
	}
	inputs := []map[string]any{{"txid": op.FundTxid, "vout": op.FundVout}}
	outputs := []map[string]any{
		{changeAddr: amount8(op.FundAtoms - supervisionFeeAtoms)},
		{"fee": amount8(supervisionFeeAtoms)},
	}
	rawRes, err := s.walletRPC(seqEscrowWallet, "createrawtransaction", inputs, outputs)
	if err != nil {
		return "", "", -1, fmt.Errorf("createrawtransaction: %w", err)
	}
	var rawHex string
	if err := json.Unmarshal(rawRes, &rawHex); err != nil || rawHex == "" {
		return "", "", -1, fmt.Errorf("createrawtransaction returned no transaction")
	}
	addRes, err := s.nodeRPC("addsupervisionrecordoutput", rawHex, recordScript, op.AssetID)
	if err != nil {
		return "", "", -1, fmt.Errorf("addsupervisionrecordoutput: %w", err)
	}
	var withRecord string
	if err := json.Unmarshal(addRes, &withRecord); err != nil || withRecord == "" {
		return "", "", -1, fmt.Errorf("addsupervisionrecordoutput returned no transaction")
	}
	signRes, err := s.walletRPC(seqEscrowWallet, "signrawtransactionwithwallet", withRecord)
	if err != nil {
		return "", "", -1, fmt.Errorf("signrawtransactionwithwallet: %w", err)
	}
	var signed struct {
		Hex      string `json:"hex"`
		Complete bool   `json:"complete"`
	}
	if err := json.Unmarshal(signRes, &signed); err != nil || signed.Hex == "" {
		return "", "", -1, fmt.Errorf("the wallet returned no signed transaction")
	}
	dec, err := s.decodeRawTransaction(signed.Hex)
	if err != nil || dec.Txid == "" {
		return "", "", -1, fmt.Errorf("the signed transaction could not be decoded")
	}
	recordVout = -1
	for i, v := range dec.Vout {
		if strings.EqualFold(v.ScriptPubKey.Hex, recordScript) {
			recordVout = int64(i)
			break
		}
	}
	if recordVout < 0 {
		return "", "", -1, fmt.Errorf("the record output is missing from the assembled transaction")
	}
	return signed.Hex, dec.Txid, recordVout, nil
}

// submitSupervisionTx submits a freeze over the private producer channel, or
// through the ordinary mempool when that channel errors. Returns which channel
// carried it.
func (s *server) submitSupervisionTx(rawHex string) (string, error) {
	if _, err := s.nodeRPC("submitsupervisionrecord", rawHex); err == nil {
		return "private", nil
	} else if txAlreadyKnown(err) {
		return "private", nil
	}
	if _, err := s.nodeRPC("sendrawtransaction", rawHex); err != nil && !txAlreadyKnown(err) {
		return "", err
	}
	return "mempool", nil
}

// findFreezeOp resolves the freeze a supervision request names, by id or by
// target.
func (s *server) findFreezeOp(iss *Issuance, req *supervisionReq) (*SupervisionOp, error) {
	if id := strings.TrimSpace(req.FreezeID); id != "" {
		op, err := s.st.SupervisionOpByID(id)
		if err != nil {
			return nil, err
		}
		if op == nil || op.IssuanceID != iss.ID || op.Kind != "freeze" {
			return nil, nil
		}
		return op, nil
	}
	target := req.target()
	if target == "" {
		return nil, nil
	}
	probe, err := s.isAssetFrozen(iss.AssetID, target)
	if err != nil {
		return nil, nil
	}
	ops, err := s.st.SupervisionOpsByIssuance(iss.ID)
	if err != nil {
		return nil, err
	}
	for i := len(ops) - 1; i >= 0; i-- {
		if ops[i].Kind == "freeze" && ops[i].TargetHash == probe.TargetHash && ops[i].State == "submitted" {
			return ops[i], nil
		}
	}
	return nil, nil
}

func isHexStr(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return len(s) > 0
}

// supervisedAssetKnown reports whether the chain's supervision registry knows
// this asset, i.e. its supervised issuance has confirmed.
func (s *server) supervisedAssetKnown(assetID string) (bool, error) {
	res, err := s.nodeRPC("getsupervisedassets")
	if err != nil {
		return false, err
	}
	var list []struct {
		Asset string `json:"asset"`
	}
	if err := json.Unmarshal(res, &list); err != nil {
		return false, err
	}
	for _, a := range list {
		if a.Asset == assetID {
			return true, nil
		}
	}
	return false, nil
}
