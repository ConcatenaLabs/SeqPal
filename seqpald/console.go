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
