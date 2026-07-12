package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// The freeze / clawback console (M7 section 4). Issuer-facing and scoped to the
// entity's own issuances (challenge-auth session, like close and mandate), over
// openampd's existing POST /v1/issuer/freeze and POST /v1/issuer/clawback. A
// REASON is required on both; it becomes part of the public transparency log.
// Clawback is a FULL SWEEP: openampd seizes ALL of a holder's confirmed enclave
// UTXOs for the asset into the issuer enclave, disclosed in the response. Every
// clawback is idempotent and reconciled before retry, so a sweep is never
// broadcast twice.

// --- freeze ------------------------------------------------------------------

type consoleFreezeReq struct {
	HolderAID string `json:"holder_aid"`
	Frozen    bool   `json:"frozen"`
	Reason    string `json:"reason"`
}

// handleConsoleFreeze is POST /api/issuances/{id}/freeze (owner). It freezes or
// unfreezes a holder at the policy server. A freeze is a global account attribute
// at openampd (it gates every transfer the AID is party to), disclosed here.
func (s *server) handleConsoleFreeze(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	if iss.AssetID == "" {
		writeErr(w, 409, "this issuance is not live on chain")
		return
	}
	var req consoleFreezeReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	holder := strings.TrimSpace(req.HolderAID)
	reason := strings.TrimSpace(req.Reason)
	if holder == "" {
		writeErr(w, 400, "holder_aid is required")
		return
	}
	if reason == "" {
		writeErr(w, 400, "a reason is required; it is recorded in the audit log")
		return
	}
	if err := s.callOpenAMP("POST", "/v1/issuer/freeze", s.cfg.issuerToken,
		map[string]any{"aid": holder, "frozen": req.Frozen}, nil); err != nil {
		writeErr(w, 502, "the policy server rejected the freeze: %v", err)
		return
	}
	action := "console.freeze"
	notice := "A servicing freeze was applied to your account for a SeqPal-managed asset. Contact the issuer's transfer agent."
	if !req.Frozen {
		action = "console.unfreeze"
		notice = "A servicing freeze on your account was lifted."
	}
	s.st.Audit(acct.AID, action, map[string]any{
		"issuance_id": iss.ID, "asset": iss.AssetID, "holder": holder, "frozen": req.Frozen, "reason": reason,
	})
	_ = s.st.InsertNotice(holder, "servicing", notice)
	writeJSON(w, 200, map[string]any{
		"holder_aid": holder, "frozen": req.Frozen, "reason": reason,
		"log_url": "/openamp/v1/log",
		"note":    "a freeze is a global policy-server account attribute; it gates every transfer the account is party to, not this asset alone",
	})
}

// --- clawback ----------------------------------------------------------------

type consoleClawbackReq struct {
	HolderAID string `json:"holder_aid"`
	Reason    string `json:"reason"`
}

// handleConsoleClawback is POST /api/issuances/{id}/clawback (owner). It performs
// the full-sweep seizure and surfaces the resulting txid, the reason, and a link
// to the public transparency log entry that records both.
func (s *server) handleConsoleClawback(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	if iss.AssetID == "" {
		writeErr(w, 409, "this issuance is not live on chain")
		return
	}
	var req consoleClawbackReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	holder := strings.TrimSpace(req.HolderAID)
	reason := strings.TrimSpace(req.Reason)
	if holder == "" {
		writeErr(w, 400, "holder_aid is required")
		return
	}
	if reason == "" {
		writeErr(w, 400, "a reason is required; it becomes part of the public transparency log")
		return
	}

	// M9 two-phase path: an external-issuer asset cannot be swept server-side. Build
	// the L_claw sweep (the reason is logged now, but nothing is swept), surface the
	// sighashes, and wait for the issuer's browser signature to complete it.
	if iss.IssuerExternal {
		c, toSign, err := s.doClawbackBuild(iss, holder, reason, acct.AID, "console")
		if err != nil {
			writeErr(w, 502, "the clawback could not be built: %v", err)
			return
		}
		switch c.State {
		case "empty":
			writeJSON(w, 200, map[string]any{
				"clawback": c, "log_url": "/openamp/v1/log",
				"note": "the holder had no confirmed enclave balance for this asset; nothing was swept",
			})
		case "swept":
			writeJSON(w, 200, map[string]any{
				"clawback": c, "txid": c.Txid, "atoms": c.Atoms, "reason": reason, "log_url": "/openamp/v1/log",
				"note": "this holder was already swept for this asset for an earlier request",
			})
		default:
			writeJSON(w, 200, map[string]any{
				"clawback": c, "clawback_id": c.ID, "to_sign": toSign, "atoms": c.Atoms,
				"pubkey": iss.IssuerPubkey, "two_phase": true, "reason": reason, "log_url": "/openamp/v1/log",
				"complete_url": "/api/issuances/" + iss.ID + "/clawback/" + c.ID + "/complete",
				"note": "external issuer key: the reason is already in the public transparency log, but nothing is swept " +
					"until the issuer signs these sighashes with the SeqPal ID key and posts them to complete_url; nothing is final at 0-conf",
			})
		}
		return
	}

	c, err := s.doClawback(iss, holder, reason, acct.AID, "console")
	if err != nil {
		writeErr(w, 502, "the clawback could not be completed: %v", err)
		return
	}
	if c.State == "empty" {
		writeJSON(w, 200, map[string]any{
			"clawback": c, "log_url": "/openamp/v1/log",
			"note": "the holder had no confirmed enclave balance for this asset; nothing was swept",
		})
		return
	}
	_ = s.st.InsertNotice(holder, "servicing",
		fmt.Sprintf("A clawback seized your %s holdings into the issuer enclave (txid %s). Reason: %s.", iss.Ticker, c.Txid, reason))
	writeJSON(w, 200, map[string]any{
		"clawback": c, "txid": c.Txid, "atoms": c.Atoms, "reason": reason, "log_url": "/openamp/v1/log",
		"note": "full sweep: every confirmed enclave UTXO of this holder for this asset was seized into the issuer enclave; the reason is recorded in the public transparency log",
	})
}

// --- two-phase clawback (M9, external issuer key) ----------------------------

type consoleClawbackCompleteReq struct {
	Sigs map[string]string `json:"sigs"`
}

// handleConsoleClawbackComplete is POST /api/issuances/{id}/clawback/{cid}/complete
// (owner). It finishes a two-phase (external-issuer) clawback: the issuer's browser
// signatures over the L_claw sighashes are handed to openampd, which adds the
// policy signature and broadcasts. Idempotent: a completed sweep returns the same
// txid without a second broadcast.
func (s *server) handleConsoleClawbackComplete(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	c, err := s.st.ClawbackByID(r.PathValue("cid"))
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if c == nil || c.IssuanceID != iss.ID {
		writeErr(w, 404, "unknown clawback for this issuance")
		return
	}
	if c.State == "swept" {
		writeJSON(w, 200, map[string]any{
			"clawback": c, "txid": c.Txid, "atoms": c.Atoms, "state": "swept", "log_url": "/openamp/v1/log",
			"note": "this clawback was already completed; the same seizure txid is returned",
		})
		return
	}
	var req consoleClawbackCompleteReq
	if err := readJSON(r, &req); err != nil || len(req.Sigs) == 0 {
		writeErr(w, 400, "sigs are required (issuer signatures keyed by input index)")
		return
	}
	c, err = s.doClawbackComplete(iss, c, req.Sigs, acct.AID)
	if err != nil {
		writeErr(w, 502, "the clawback could not be completed: %v", err)
		return
	}
	_ = s.st.InsertNotice(c.HolderAID, "servicing",
		fmt.Sprintf("A clawback seized your %s holdings into the issuer enclave (txid %s). Reason: %s.", iss.Ticker, c.Txid, c.Reason))
	writeJSON(w, 200, map[string]any{
		"clawback": c, "txid": c.Txid, "atoms": c.Atoms, "reason": c.Reason, "log_url": "/openamp/v1/log",
		"note": "full sweep completed by the issuer's signature; every confirmed enclave UTXO of this holder for this asset " +
			"was seized into the issuer enclave, and the reason is recorded in the public transparency log",
	})
}

// doClawbackBuild is the external-issuer analogue of doClawback's first half: it
// resolves the sweep at openampd (which logs the reason and returns the L_claw
// sighashes but broadcasts NOTHING) and persists it as an 'awaiting_signature'
// clawback. It is idempotent and reconciled: a holder with nothing to sweep yields
// an 'empty' (or prior 'swept') row, and a build already awaiting a signature is
// resumed rather than re-assembled, so it never drives a second sweep. The
// returned toSign is nil for the empty/swept short-circuits.
func (s *server) doClawbackBuild(iss *Issuance, holderAID, reason, byAID, context string) (*Clawback, []map[string]any, error) {
	s.ensureServicingMu()
	key := iss.AssetID + ":" + holderAID
	unlock := s.clawMu.lock(key)
	defer unlock()

	// Nothing to sweep: return the prior completed sweep if one exists, else record
	// an idempotent empty result (matches doClawback's zero-balance handling).
	if bal, _ := s.enclaveBalance(holderAID, iss.AssetID); bal == 0 {
		if last, _ := s.st.LastSweptClawback(iss.AssetID, holderAID); last != nil {
			return last, nil, nil
		}
		c := &Clawback{
			ID: mustID(), IssuanceID: iss.ID, AssetID: iss.AssetID, HolderAID: holderAID,
			Reason: reason, State: "empty", ByAID: byAID, Context: context,
		}
		if err := s.st.InsertClawback(c); err != nil {
			return nil, nil, err
		}
		return c, nil, nil
	}

	// Resume an in-flight build rather than assembling a second sweep of the same
	// UTXOs. openampd's pending build survives the wait; re-surfacing its sighashes
	// keeps the issuer signing one canonical sweep.
	if pend, _ := s.st.AwaitingClawback(iss.AssetID, holderAID); pend != nil {
		if toSign, err := decodeToSign(pend.ToSign); err == nil && len(toSign) > 0 {
			return pend, toSign, nil
		}
	}

	var built struct {
		ID     string `json:"id"`
		Tx     string `json:"tx"`
		Atoms  uint64 `json:"atoms"`
		ToSign []struct {
			Input   int    `json:"input"`
			Sighash string `json:"sighash"`
			Pubkey  string `json:"pubkey"`
		} `json:"to_sign"`
	}
	if err := s.callOpenAMP("POST", "/v1/issuer/clawback", s.cfg.issuerToken,
		map[string]any{"asset": iss.AssetID, "holder_aid": holderAID, "reason": reason}, &built); err != nil {
		return nil, nil, err
	}
	if built.ID == "" {
		return nil, nil, fmt.Errorf("the clawback build returned no id")
	}
	// The issuer signs only its own enclave inputs; refuse a build that asks for any
	// other key, as the escrow and P2P transfer paths do.
	toSign := make([]map[string]any, 0, len(built.ToSign))
	for _, ts := range built.ToSign {
		if ts.Pubkey != "" && !strings.EqualFold(ts.Pubkey, iss.IssuerPubkey) {
			return nil, nil, fmt.Errorf("the clawback build wants a signature from a key the issuer does not hold")
		}
		toSign = append(toSign, map[string]any{"input": ts.Input, "sighash": ts.Sighash, "pubkey": ts.Pubkey})
	}
	tsJSON, _ := json.Marshal(toSign)
	c := &Clawback{
		ID: mustID(), IssuanceID: iss.ID, AssetID: iss.AssetID, HolderAID: holderAID,
		Reason: reason, State: "awaiting_signature", Atoms: built.Atoms, ByAID: byAID, Context: context,
		OaID: built.ID, ToSign: string(tsJSON),
	}
	if err := s.st.InsertClawback(c); err != nil {
		return nil, nil, err
	}
	s.st.Audit(byAID, "clawback.build", map[string]any{
		"issuance_id": iss.ID, "asset": iss.AssetID, "holder": holderAID, "atoms": built.Atoms,
		"clawback_id": c.ID, "oa_id": built.ID, "reason": reason, "context": context,
	})
	return c, toSign, nil
}

// doClawbackComplete finishes an 'awaiting_signature' clawback with the external
// issuer's signatures. openampd verifies them, adds the policy signature, and
// broadcasts; it never re-drives a fresh sweep, and a replay returns the same
// txid, so completing twice cannot double-sweep. A seqpald write lost after a
// successful complete is reconciled by re-calling complete (openampd returns the
// same txid idempotently).
func (s *server) doClawbackComplete(iss *Issuance, c *Clawback, sigs map[string]string, byAID string) (*Clawback, error) {
	s.ensureServicingMu()
	key := iss.AssetID + ":" + c.HolderAID
	unlock := s.clawMu.lock(key)
	defer unlock()

	if c.State == "swept" {
		return c, nil
	}
	if c.State != "awaiting_signature" || c.OaID == "" {
		return nil, fmt.Errorf("this clawback is %s, not awaiting a signature", c.State)
	}

	var out struct {
		Txid  string `json:"txid"`
		Atoms uint64 `json:"atoms"`
	}
	if err := s.callOpenAMP("POST", "/v1/issuer/clawback/"+c.OaID+"/complete", s.cfg.issuerToken,
		map[string]any{"sigs": sigs}, &out); err != nil {
		_ = s.st.UpdateClawbackFields(c.ID, map[string]any{"error": err.Error()})
		c.Error = err.Error()
		return c, err
	}
	if out.Txid == "" {
		return c, fmt.Errorf("the clawback complete returned no txid")
	}
	atoms := out.Atoms
	if atoms == 0 {
		atoms = c.Atoms
	}
	_ = s.st.UpdateClawbackFields(c.ID, map[string]any{"state": "swept", "txid": out.Txid, "atoms": atoms, "error": ""})
	c.State, c.Txid, c.Atoms, c.Error = "swept", out.Txid, atoms, ""
	_ = s.st.InsertLedger(&LedgerEntry{
		IssuanceID: iss.ID, Kind: "clawback", Rail: "asset", Amount: atoms, Ccy: iss.Ticker, Txid: out.Txid,
	})
	s.st.Audit(byAID, "clawback.sweep", map[string]any{
		"issuance_id": iss.ID, "asset": iss.AssetID, "holder": c.HolderAID, "atoms": atoms,
		"txid": out.Txid, "reason": c.Reason, "context": c.Context, "clawback_id": c.ID, "external": true,
	})
	return c, nil
}

// decodeToSign parses the cached L_claw sighash list stored on an
// 'awaiting_signature' clawback row (empty for the legacy single-call path).
func decodeToSign(raw string) ([]map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// doClawback is the shared, idempotent, reconciled full-sweep used by the console
// and the stranded-key runbook. The intent row is written 'sweeping' BEFORE the
// openampd call; because openampd writes its public clawback log entry BEFORE it
// signs, a lost write is reconciled by scanning that log rather than re-sweeping.
// It never double-sweeps a holder.
func (s *server) doClawback(iss *Issuance, holderAID, reason, byAID, context string) (*Clawback, error) {
	s.ensureServicingMu()
	key := iss.AssetID + ":" + holderAID
	unlock := s.clawMu.lock(key)
	defer unlock()

	// Reconcile an in-flight sweep that may have broadcast but lost its write.
	if inflight, _ := s.st.InflightClawback(iss.AssetID, holderAID); inflight != nil {
		if txid, atoms, found := s.findClawbackInLog(iss.AssetID, holderAID, reason); found {
			_ = s.st.UpdateClawbackFields(inflight.ID, map[string]any{"state": "swept", "txid": txid, "atoms": atoms, "error": ""})
			inflight.State, inflight.Txid, inflight.Atoms = "swept", txid, atoms
			return inflight, nil
		}
	}

	// Idempotent no-op: a holder with zero confirmed enclave balance has nothing to
	// sweep (openampd 409s on an empty holder). Return a prior sweep if one exists.
	bal, _ := s.enclaveBalance(holderAID, iss.AssetID)
	if bal == 0 {
		if last, _ := s.st.LastSweptClawback(iss.AssetID, holderAID); last != nil {
			return last, nil
		}
		c := &Clawback{
			ID: mustID(), IssuanceID: iss.ID, AssetID: iss.AssetID, HolderAID: holderAID,
			Reason: reason, State: "empty", ByAID: byAID, Context: context,
		}
		if err := s.st.InsertClawback(c); err != nil {
			return nil, err
		}
		return c, nil
	}

	// Persist the intent BEFORE broadcasting (reusing an in-flight row if present).
	c, _ := s.st.InflightClawback(iss.AssetID, holderAID)
	if c == nil {
		c = &Clawback{
			ID: mustID(), IssuanceID: iss.ID, AssetID: iss.AssetID, HolderAID: holderAID,
			Reason: reason, State: "sweeping", ByAID: byAID, Context: context,
		}
		if err := s.st.InsertClawback(c); err != nil {
			return nil, err
		}
	}

	var out struct {
		Txid  string `json:"txid"`
		Atoms uint64 `json:"atoms"`
	}
	err := s.callOpenAMP("POST", "/v1/issuer/clawback", s.cfg.issuerToken,
		map[string]any{"asset": iss.AssetID, "holder_aid": holderAID, "reason": reason}, &out)
	if err != nil {
		// The public log entry precedes signing, so a post-broadcast failure is still
		// recoverable without re-sweeping.
		if txid, atoms, found := s.findClawbackInLog(iss.AssetID, holderAID, reason); found {
			_ = s.st.UpdateClawbackFields(c.ID, map[string]any{"state": "swept", "txid": txid, "atoms": atoms, "error": ""})
			c.State, c.Txid, c.Atoms = "swept", txid, atoms
			return c, nil
		}
		_ = s.st.UpdateClawbackFields(c.ID, map[string]any{"state": "failed", "error": err.Error()})
		c.State, c.Error = "failed", err.Error()
		return c, err
	}
	_ = s.st.UpdateClawbackFields(c.ID, map[string]any{"state": "swept", "txid": out.Txid, "atoms": out.Atoms, "error": ""})
	c.State, c.Txid, c.Atoms = "swept", out.Txid, out.Atoms
	_ = s.st.InsertLedger(&LedgerEntry{
		IssuanceID: iss.ID, Kind: "clawback", Rail: "asset", Amount: out.Atoms, Ccy: iss.Ticker, Txid: out.Txid,
	})
	s.st.Audit(byAID, "clawback.sweep", map[string]any{
		"issuance_id": iss.ID, "asset": iss.AssetID, "holder": holderAID, "atoms": out.Atoms,
		"txid": out.Txid, "reason": reason, "context": context,
	})
	return c, nil
}

// findClawbackInLog scans openampd's public transparency log for a clawback entry
// naming this asset and holder with this reason, and returns its recorded txid and
// atoms. openampd appends the entry BEFORE it signs, so this is the clawback
// analogue of escrowFindSend: it recovers a sweep whose seqpald write was lost.
func (s *server) findClawbackInLog(assetID, holderAID, reason string) (string, uint64, bool) {
	req, err := http.NewRequest("GET", s.cfg.openampURL+"/v1/log", nil)
	if err != nil {
		return "", 0, false
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return "", 0, false
	}
	defer resp.Body.Close()
	var txid string
	var atoms uint64
	found := false
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e struct {
			Action string `json:"action"`
			Data   struct {
				Asset  string `json:"asset"`
				Holder string `json:"holder"`
				Reason string `json:"reason"`
				Txid   string `json:"txid"`
				Atoms  uint64 `json:"atoms"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if e.Action == "clawback" && e.Data.Asset == assetID && e.Data.Holder == holderAID && e.Data.Reason == reason && e.Data.Txid != "" {
			txid, atoms, found = e.Data.Txid, e.Data.Atoms, true // keep scanning: take the newest match
		}
	}
	return txid, atoms, found
}

// enclaveBalance reads an AID's confirmed enclave balance for an asset from
// openampd. It is the reconciliation probe for clawback (nothing to sweep at zero)
// and for re-delivery (made whole once the new AID's balance rises).
func (s *server) enclaveBalance(aid, assetID string) (uint64, error) {
	var out struct {
		Atoms uint64 `json:"atoms"`
	}
	if err := s.callOpenAMP("GET", "/v1/users/"+aid+"/balance?asset="+assetID, "", nil, &out); err != nil {
		return 0, err
	}
	return out.Atoms, nil
}
