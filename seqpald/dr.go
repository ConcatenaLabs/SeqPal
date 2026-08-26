package main

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"
)

// M8 Depository-Receipt programme (contract section 6). A DR programme mints and
// redeems real supply of a restricted asset:
//
//   - MINT = OA-6 reissuance (POST /v1/issuer/reissue): more units are issued into
//     the DR custodian enclave. openampd re-blinds the reissuance token first (the
//     phantom-tx hazard), so a mint may return 202 "reblinding"; seqpald retries
//     the same request_id until it broadcasts. The request_id is the idempotency
//     key, so a crash-then-retry never double-mints.
//   - REDEEM = OA-5 burn (POST /v1/issuer/burn): units held in a custodied enclave
//     are sent to a provably-unspendable output, reducing supply for real. seqpald
//     signs the holder side with the custodied enclave key and completes the burn.
//     A lost complete write is reconciled from openampd's log, never double-burned.
//   - CIRCULATING SUPPLY is chain-derived (GET /v1/supply), never a stored counter:
//     a burn lowers it, a reissue raises it, as soon as the change confirms.
//   - A SIMULATED custodian produces an anchored attestation on each mint/redeem
//     (labeled simulated custodian; the math and the artifact hash are real).
//   - US-PERSON EXCLUSION is enforced as a REAL category rule at the policy server:
//     a permanent j:US category deny, applied through the amendment-chain
//     chokepoint, so openampd refuses any secondary transfer to a US-person holder.

const drReblindAttempts = 5

// --- enable ------------------------------------------------------------------

// handleDREnable is POST /api/issuances/{id}/dr/enable (owner): mark the issuance
// a DR programme and enforce the US-person exclusion as a real policy-server
// category rule (a permanent j:US deny), applied through the amendment chain.
// Idempotent: re-enabling refreshes the exclusion without stacking denies.
func (s *server) handleDREnable(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	if iss.Status != "live" || iss.AssetID == "" {
		writeErr(w, 409, "this issuance is not live on chain")
		return
	}
	// The programme rests on two things a network-enforced token does not have: a
	// platform-held account to hold the receipts in, and transfer rules this
	// platform checks when it approves a transfer (the US-person exclusion below is
	// exactly such a rule). Enabling it would record a programme that could never
	// mint, redeem, or exclude anyone.
	if s.refuseForNetwork(w, acct.AID, iss, "dr.enable",
		"A receipt programme needs this platform to hold the receipts and to check each transfer against your rules. This token's rules are enforced by the network instead, and its units sit in holders' own addresses, so there is nothing here to hold or to check and the programme cannot be enabled for it.") {
		return
	}
	if s.refuseForBearer(w, acct.AID, iss, "dr.enable",
		"This token is freely tradable and already moves without a depository: a receipt programme exists to carry a restricted token where it cannot go itself.") {
		return
	}
	if !s.hasEnclave(acct) {
		writeErr(w, 403, "a receipt programme is issued from an OpenAMP account and this SeqPal ID has none attached")
		return
	}
	height, mutationID, err := s.applyUSExclusion(iss)
	if err != nil {
		writeErr(w, 502, "could not enforce the US-person exclusion category rule: %v", err)
		return
	}
	prog := &DRProgram{IssuanceID: iss.ID, AssetID: iss.AssetID, Enabled: true, USExclusionHeight: height, MutationID: mutationID}
	if err := s.st.UpsertDRProgram(prog); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	s.st.Audit(acct.AID, "dr.enable", map[string]any{
		"issuance_id": iss.ID, "asset": iss.AssetID, "us_exclusion_height": height, "mutation": mutationID,
	})
	writeJSON(w, 200, map[string]any{
		"program": prog,
		"note":    "US persons (17 CFR 230.902(k)) are excluded by a real policy-server j:US category deny, not a display string; the simulated custodian attestations are labeled and anchored",
	})
}

// applyUSExclusion merges a permanent j:US category deny into the asset's current
// on-chain rules and applies it through the amendment-chain chokepoint. It returns
// the deny height and the mutation id. The deny binds every non-primary (secondary)
// sender, so a US-person recipient is refused on the secondary market.
func (s *server) applyUSExclusion(iss *Issuance) (int64, string, error) {
	raw, err := s.onchainRulesRaw(iss.AssetID)
	if err != nil {
		return 0, "", err
	}
	var rules CompiledRules
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &rules); err != nil {
			return 0, "", fmt.Errorf("parse current rules: %w", err)
		}
	}
	// A permanent exclusion: a century of blocks past the tip. blocksPerDay is a
	// positive config constant, so this is always well past any realistic horizon.
	far := s.tipHeight() + s.cfg.blocksPerDay*365*100
	found := false
	for i, d := range rules.CategoryDenies {
		if d.Prefix == "j:US" {
			if rules.CategoryDenies[i].UntilHeight < far {
				rules.CategoryDenies[i].UntilHeight = far
			}
			far = rules.CategoryDenies[i].UntilHeight
			found = true
		}
	}
	if !found {
		rules.CategoryDenies = append(rules.CategoryDenies, CategoryDeny{Prefix: "j:US", UntilHeight: far})
	}
	basis := "Depository-Receipt US-person exclusion: a permanent j:US category deny (17 CFR 230.902(k)) enforced at the policy server for the life of the programme"
	m, err := s.applyRulesMutation(iss, rules, basis, far)
	if err != nil {
		return 0, "", err
	}
	return far, m.ID, nil
}

// --- mint (OA-6 reissuance) --------------------------------------------------

type drMintReq struct {
	Atoms     uint64 `json:"atoms"`
	TargetAID string `json:"target_aid"` // optional; defaults to the offering escrow (DR custodian) enclave
	RequestID string `json:"request_id"` // idempotency key; a retry returns the same reissue txid
}

// handleDRMint is POST /api/issuances/{id}/dr/mint (owner): reissue more units of
// the DR asset into the custodian enclave, handling openampd's token re-blinding
// (retrying the same request_id) and recording the reissue txid. Idempotent by
// request_id; supply stays chain-derived.
func (s *server) handleDRMint(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	// A mint issues new units into an account this platform holds and signs for.
	// There is no such account for a network-enforced token: new units go to an
	// address the issuer controls, from the issuer's own wallet.
	if s.refuseForNetwork(w, acct.AID, iss, "dr.mint",
		"New units of this token are issued by you, into an address you control, from your own wallet. This platform holds no account for it and cannot issue them.") {
		return
	}
	prog, _ := s.st.DRProgramByIssuance(iss.ID)
	if prog == nil || !prog.Enabled {
		writeErr(w, 409, "enable the DR programme first (POST /api/issuances/%s/dr/enable)", iss.ID)
		return
	}
	var req drMintReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	if req.Atoms == 0 {
		writeErr(w, 400, "a positive atoms is required")
		return
	}
	target := strings.TrimSpace(req.TargetAID)
	if target == "" {
		escrow, err := s.st.EnclaveKeyByRef(enclaveOfferingEscrow, iss.ID)
		if err != nil || escrow == nil {
			writeErr(w, 500, "the DR custodian (offering escrow) enclave is missing")
			return
		}
		target = escrow.AID
	}
	requestID := strings.TrimSpace(req.RequestID)
	if requestID == "" {
		requestID = "dr-mint-" + mustID()
	}

	s.ensureServicingMu()
	unlock := s.drMu.lock(iss.ID)
	defer unlock()

	op := &DROp{
		ID: mustID(), IssuanceID: iss.ID, AssetID: iss.AssetID, Kind: "mint",
		RequestID: requestID, TargetAID: target, Atoms: req.Atoms, State: "pending",
	}
	created, err := s.st.InsertDROpIfAbsent(op)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if !created {
		op, _ = s.st.DROpByRequestID(requestID)
		if op != nil && op.State == "broadcast" {
			s.writeDRMintResult(w, iss, op)
			return
		}
	}

	// Reissue, retrying the re-blinding step (0-conf chaining from the wallet's own
	// new blinded token is fine; nothing is final at 0-conf).
	var lastReblind string
	for attempt := 0; attempt < drReblindAttempts; attempt++ {
		var out struct {
			ReissueTxid string `json:"reissue_txid"`
			Status      string `json:"status"`
			ReblindTxid string `json:"reblind_txid"`
			Idempotent  bool   `json:"idempotent"`
		}
		code, err := s.callOpenAMPStatus("POST", "/v1/issuer/reissue", s.cfg.issuerToken,
			map[string]any{"asset": iss.AssetID, "target_aid": s.openampAIDFor(target), "atoms": req.Atoms, "request_id": requestID}, &out)
		if err != nil {
			if code == http.StatusNotImplemented {
				_ = s.st.UpdateDROpFields(op.ID, map[string]any{"state": "failed", "error": err.Error()})
				writeErr(w, 501, "DR mint is unavailable: %v", err)
				return
			}
			_ = s.st.UpdateDROpFields(op.ID, map[string]any{"state": "failed", "error": err.Error()})
			writeErr(w, statusForOpenAMP(code), "reissue failed: %v", err)
			return
		}
		if out.Status == "reblinding" {
			lastReblind = out.ReblindTxid
			_ = s.st.UpdateDROpFields(op.ID, map[string]any{"state": "reblinding"})
			time.Sleep(2 * time.Second)
			continue
		}
		// broadcast (fresh) or done (idempotent replay): a reissue txid is present.
		if out.ReissueTxid == "" {
			_ = s.st.UpdateDROpFields(op.ID, map[string]any{"state": "failed", "error": "reissue returned no txid"})
			writeErr(w, 502, "reissue returned no txid")
			return
		}
		_ = s.st.UpdateDROpFields(op.ID, map[string]any{"state": "broadcast", "txid": out.ReissueTxid, "error": ""})
		op.Txid, op.State = out.ReissueTxid, "broadcast"
		s.finalizeDRAttestation(iss, op)
		op, _ = s.st.DROpByRequestID(requestID)
		s.st.Audit(acct.AID, "dr.mint", map[string]any{
			"issuance_id": iss.ID, "asset": iss.AssetID, "target": target, "atoms": req.Atoms, "txid": op.Txid,
		})
		s.writeDRMintResult(w, iss, op)
		return
	}
	// Still re-blinding after the retry budget: safe to leave; the caller retries
	// the same request_id once the re-blind confirms.
	_ = s.st.UpdateDROpFields(op.ID, map[string]any{"state": "reblinding"})
	writeJSON(w, 202, map[string]any{
		"state": "reblinding", "request_id": requestID, "reblind_txid": lastReblind,
		"note": "the reissuance token is being re-blinded; retry this mint with the same request_id shortly (nothing is final at 0-conf)",
	})
}

func (s *server) writeDRMintResult(w http.ResponseWriter, iss *Issuance, op *DROp) {
	circ, height, _ := s.drSupply(iss.AssetID)
	writeJSON(w, 200, map[string]any{
		"op": op, "reissue_txid": op.Txid, "state": op.State,
		"circulating_atoms": circ, "height": height, "supply_source": "chain-derived",
	})
}

// --- redeem (OA-5 burn) ------------------------------------------------------

type drRedeemReq struct {
	Atoms     uint64 `json:"atoms"`
	HolderAID string `json:"holder_aid"` // optional; defaults to the offering escrow (DR custodian) enclave
	RequestID string `json:"request_id"`
}

// handleDRRedeem is POST /api/issuances/{id}/dr/redeem (owner): burn units held in
// a custodied enclave, reducing chain-derived supply. seqpald signs the holder
// side with the custodied enclave key (so the holder must be one of seqpald's own
// enclaves) and completes the burn. Idempotent by request_id; a lost complete
// write is reconciled from openampd's log, never double-burned.
func (s *server) handleDRRedeem(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	if iss.Status != "live" || iss.AssetID == "" {
		writeErr(w, 409, "this issuance is not live on chain")
		return
	}
	// A redemption destroys units out of an account this platform holds and signs
	// for. For a network-enforced token the units are in the holder's own wallet, so
	// only the holder can destroy them, and this platform cannot sign it for them.
	if s.refuseForNetwork(w, acct.AID, iss, "dr.redeem",
		"Units of this token are destroyed by whoever holds them, from their own wallet. This platform holds none of them and cannot destroy them on a holder's behalf.") {
		return
	}
	var req drRedeemReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	if req.Atoms == 0 {
		writeErr(w, 400, "a positive atoms is required")
		return
	}
	// Resolve the custodied enclave that holds the units to burn (default: escrow).
	holderAID := strings.TrimSpace(req.HolderAID)
	if holderAID == "" {
		escrow, err := s.st.EnclaveKeyByRef(enclaveOfferingEscrow, iss.ID)
		if err != nil || escrow == nil {
			writeErr(w, 500, "the DR custodian (offering escrow) enclave is missing")
			return
		}
		holderAID = escrow.AID
	}
	enclave, err := s.st.EnclaveKeyByAID(holderAID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if enclave == nil {
		writeErr(w, 400, "the redeem holder must be a custodied SeqPal enclave; a holder-signed burn goes through the wallet, not the DR console")
		return
	}
	requestID := strings.TrimSpace(req.RequestID)
	if requestID == "" {
		requestID = "dr-redeem-" + mustID()
	}

	s.ensureServicingMu()
	unlock := s.drMu.lock(iss.ID)
	defer unlock()

	op := &DROp{
		ID: mustID(), IssuanceID: iss.ID, AssetID: iss.AssetID, Kind: "redeem",
		RequestID: requestID, HolderAID: holderAID, Atoms: req.Atoms, State: "pending",
	}
	created, err := s.st.InsertDROpIfAbsent(op)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if !created {
		op, _ = s.st.DROpByRequestID(requestID)
		if op != nil && op.State == "broadcast" {
			s.writeDRRedeemResult(w, iss, op)
			return
		}
	}

	// Reconcile a lost write: a prior attempt may have broadcast the burn (an op
	// past "pending" with no txid). A log scan for a settled burn of this holder and
	// amount proves it settled; never re-burn in that case.
	if op != nil && op.State != "pending" && op.Txid == "" {
		if txid, found := s.logFindMoved("burn", iss.AssetID, holderAID, req.Atoms); found {
			_ = s.st.UpdateDROpFields(op.ID, map[string]any{"state": "broadcast", "txid": txid, "error": ""})
			op.Txid, op.State = txid, "broadcast"
			s.finalizeDRAttestation(iss, op)
			op, _ = s.st.DROpByRequestID(requestID)
			s.writeDRRedeemResult(w, iss, op)
			return
		}
	}

	// Build the burn at openampd, then sign the holder side with the custodied key.
	var built struct {
		ID     string `json:"id"`
		ToSign []struct {
			Input   int    `json:"input"`
			Sighash string `json:"sighash"`
			Pubkey  string `json:"pubkey"`
		} `json:"to_sign"`
		BurnAtoms uint64 `json:"burn_atoms"`
	}
	code, berr := s.callOpenAMPStatus("POST", "/v1/issuer/burn", s.cfg.issuerToken,
		map[string]any{"asset": iss.AssetID, "holder_aid": holderAID, "atoms": req.Atoms, "fee_mode": "sponsor"}, &built)
	if berr != nil {
		_ = s.st.UpdateDROpFields(op.ID, map[string]any{"state": "failed", "error": berr.Error()})
		writeErr(w, statusForOpenAMP(code), "burn build failed: %v", berr)
		return
	}
	if built.ID == "" {
		_ = s.st.UpdateDROpFields(op.ID, map[string]any{"state": "failed", "error": "burn build returned no id"})
		writeErr(w, 502, "burn build returned no id")
		return
	}
	sigs := map[string]string{}
	for _, ts := range built.ToSign {
		if ts.Pubkey != "" && ts.Pubkey != enclave.XOnly {
			_ = s.st.UpdateDROpFields(op.ID, map[string]any{"state": "failed", "error": "burn wants a key the custodian does not hold"})
			writeErr(w, 502, "the burn wants a signature from a key the custodian does not hold")
			return
		}
		sig, err := signSighash(enclave.Priv, ts.Sighash)
		if err != nil {
			_ = s.st.UpdateDROpFields(op.ID, map[string]any{"state": "failed", "error": err.Error()})
			writeErr(w, 500, "could not sign the burn")
			return
		}
		sigs[fmt.Sprintf("%d", ts.Input)] = sig
	}

	// Persist the intent (with the openampd build id) BEFORE completing, so a lost
	// write after broadcast is reconcilable from the log.
	_ = s.st.UpdateDROpFields(op.ID, map[string]any{"state": "burning", "oa_id": built.ID})

	var done struct {
		Txid string `json:"txid"`
	}
	ccode, cerr := s.callOpenAMPStatus("POST", "/v1/transfers/"+built.ID+"/complete", "", map[string]any{"sigs": sigs}, &done)
	if cerr != nil {
		if txid, found := s.logFindMoved("burn", iss.AssetID, holderAID, req.Atoms); found {
			_ = s.st.UpdateDROpFields(op.ID, map[string]any{"state": "broadcast", "txid": txid, "error": ""})
			op.Txid, op.State = txid, "broadcast"
			s.finalizeDRAttestation(iss, op)
			op, _ = s.st.DROpByRequestID(requestID)
			s.writeDRRedeemResult(w, iss, op)
			return
		}
		_ = s.st.UpdateDROpFields(op.ID, map[string]any{"state": "failed", "error": cerr.Error()})
		writeErr(w, statusForOpenAMP(ccode), "burn complete failed: %v", cerr)
		return
	}
	if done.Txid == "" {
		writeErr(w, 502, "burn complete returned no txid")
		return
	}
	_ = s.st.UpdateDROpFields(op.ID, map[string]any{"state": "broadcast", "txid": done.Txid, "error": ""})
	op.Txid, op.State = done.Txid, "broadcast"
	_ = s.st.InsertLedger(&LedgerEntry{
		IssuanceID: iss.ID, Kind: "dr_redeem_burn", Rail: "asset", Amount: req.Atoms, Ccy: iss.Ticker, Txid: done.Txid,
	})
	s.finalizeDRAttestation(iss, op)
	op, _ = s.st.DROpByRequestID(requestID)
	s.st.Audit(acct.AID, "dr.redeem", map[string]any{
		"issuance_id": iss.ID, "asset": iss.AssetID, "holder": holderAID, "atoms": req.Atoms, "txid": done.Txid,
	})
	s.writeDRRedeemResult(w, iss, op)
}

func (s *server) writeDRRedeemResult(w http.ResponseWriter, iss *Issuance, op *DROp) {
	circ, height, _ := s.drSupply(iss.AssetID)
	writeJSON(w, 200, map[string]any{
		"op": op, "burn_txid": op.Txid, "state": op.State,
		"circulating_atoms": circ, "height": height, "supply_source": "chain-derived",
	})
}

// --- supply + program reads --------------------------------------------------

// drSupply reads the chain-derived circulating supply for an asset from openampd
// (GET /v1/supply), a holders-sum that a mint raises and a burn lowers. It is
// never a stored counter.
func (s *server) drSupply(assetID string) (uint64, int64, error) {
	var out struct {
		CirculatingAtoms uint64 `json:"circulating_atoms"`
		Height           int64  `json:"height"`
	}
	if err := s.callOpenAMP("GET", "/v1/supply?asset="+assetID, "", nil, &out); err != nil {
		return 0, 0, err
	}
	return out.CirculatingAtoms, out.Height, nil
}

// handleDRSupply is GET /api/issuances/{id}/dr/supply (owner): the chain-derived
// circulating supply of the DR asset.
func (s *server) handleDRSupply(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	if iss.AssetID == "" {
		writeErr(w, 409, "this issuance is not live on chain")
		return
	}
	circ, height, err := s.drSupply(iss.AssetID)
	if err != nil {
		writeErr(w, 502, "could not read the chain-derived supply: %v", err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"asset": iss.AssetID, "circulating_atoms": circ, "height": height,
		"supply_source": "chain-derived", "note": "holders-sum from the policy server; never a stored counter; nothing final at 0-conf",
	})
}

// handleDRProgram is GET /api/issuances/{id}/dr (owner): the DR programme state,
// its ops, and the current chain-derived supply.
func (s *server) handleDRProgram(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	prog, _ := s.st.DRProgramByIssuance(iss.ID)
	ops, _ := s.st.DROpsByIssuance(iss.ID)
	circ, height := uint64(0), int64(0)
	if iss.AssetID != "" {
		circ, height, _ = s.drSupply(iss.AssetID)
	}
	writeJSON(w, 200, map[string]any{
		"program": prog, "ops": ops,
		"circulating_atoms": circ, "height": height, "supply_source": "chain-derived",
		"custodian": "SIMULATED custodian; attestations are labeled simulated with real hashed, anchored artifacts",
	})
}

// --- simulated custodian attestation -----------------------------------------

// finalizeDRAttestation produces the content-addressed, anchored SIMULATED
// custodian attestation for a completed DR op: it states the (simulated) custodian
// holds underlying units backing the now chain-derived circulating supply. The
// math and the artifact hash are real; the remittance/custody is simulated and
// labeled. Idempotent: it only generates when the op has no attestation yet.
func (s *server) finalizeDRAttestation(iss *Issuance, op *DROp) {
	if op.AttestationHash != "" {
		return
	}
	circ, height, _ := s.drSupply(iss.AssetID)
	title, body := drCustodianAttestationDoc(iss, op, circ, height)
	hash := sha256Hex([]byte(body))
	_ = s.st.PutDocument(&StoredDoc{
		DocHash: hash, IssuanceID: iss.ID, Kind: "dr-custodian-attestation", Title: title,
		ContentType: "text/html; charset=utf-8", AlwaysPublic: true, Body: []byte(body),
	})
	anchorTxid := s.anchorArtifact("dr-custodian-attestation", hash)
	_ = s.st.UpdateDROpFields(op.ID, map[string]any{"attestation_hash": hash, "anchor_txid": anchorTxid})
	op.AttestationHash, op.AnchorTxid = hash, anchorTxid
	s.st.Audit("", "dr.attestation", map[string]any{
		"run": op.ID, "issuance_id": iss.ID, "kind": op.Kind, "attestation_hash": hash, "anchor_txid": anchorTxid,
	})
}

func drCustodianAttestationDoc(iss *Issuance, op *DROp, circulating uint64, height int64) (string, string) {
	action := "mint (reissuance)"
	if op.Kind == "redeem" {
		action = "redeem (burn)"
	}
	title := fmt.Sprintf("SIMULATED custodian attestation: %s (%s) DR %s", iss.Name, iss.Ticker, op.Kind)
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString(para("SIMULATED custodian attestation. This artifact demonstrates the attestation a depository-receipt custodian would issue: it states that the custodian holds underlying units backing the depository receipts in circulation. The custodian and its holding are simulated; the depository-receipt supply figure is real and chain-derived (a holders-sum from the policy server), and this artifact's hash is anchored on the Sequentia network, which anchors to Bitcoin."))
	b.WriteString("<table border=\"1\" cellpadding=\"4\"><tbody>")
	b.WriteString(row2("Asset", iss.AssetID))
	b.WriteString(row2("Operation", action))
	b.WriteString(row2("Operation atoms", fmt.Sprintf("%d", op.Atoms)))
	if op.Txid != "" {
		b.WriteString(row2("On-chain txid", op.Txid))
	}
	b.WriteString(row2("Circulating DR supply (atoms, chain-derived)", fmt.Sprintf("%d", circulating)))
	b.WriteString(row2("As of Sequentia block", fmt.Sprintf("%d", height)))
	b.WriteString(row2("Simulated underlying held by custodian (atoms)", fmt.Sprintf("%d", circulating)))
	b.WriteString("</tbody></table>")
	b.WriteString(para("US persons (17 CFR 230.902(k)) are excluded from holding these depository receipts by a real policy-server category rule, not a display string. Nothing on the Sequentia network is final at 0-conf."))
	return title, docPage("dr-custodian-attestation", title, b.String())
}
