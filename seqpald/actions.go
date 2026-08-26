package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

// Corporate actions (W-3): dividends and votes against a live asset's on-chain
// holder set. The record-date snapshot is the chain UTXO set carrying the asset
// (walked via electrs at the first watcher pass at or after the record height;
// see actions_snapshot.go). A holder participates by PROVING key control over
// snapshot outpoints: a BIP340 TAGGED signature (holdingProofTag) whose derived
// script must equal the snapshot script. Identity never leaves the claim row;
// the public surfaces publish outpoints, atoms, and choices only.
//
// Dividends reuse the existing distribution fund-safety machinery end to end:
// issuer funds FIRST (a per-action deposit address watched by moneywatch),
// per-claim marker comments ("seqpal-act-<action>-<claim>"), intent persisted
// before broadcast, escrowFindSend reconcile before any retry, payment out of
// seqpal-escrow, withholding via withholdingFor with the claimant's claims
// record.

// holdingProofTag is the BIP340 tag for W-3 holding proofs. TAGGED signer,
// never raw: the wallet signs taggedHash(holdingProofTag, sha256(canonical
// claim statement)).
const holdingProofTag = "seqpal-holding-proof-v1"

// actMarker is the per-claim settlement-scoped comment that makes a lost-write
// dividend payout reconcilable (escrowFindSend) instead of re-broadcast.
func actMarker(actionID, claimID string) string { return "seqpal-act-" + actionID + "-" + claimID }

// --- create ------------------------------------------------------------------

type createActionReq struct {
	Kind         string `json:"kind"` // dividend | vote
	RecordHeight int64  `json:"record_height"`
	Dividend     *struct {
		Asset      string `json:"asset"`
		TotalAtoms uint64 `json:"total_atoms"`
	} `json:"dividend"`
	Vote *struct {
		Question     string   `json:"question"`
		Choices      []string `json:"choices"`
		ClosesHeight int64    `json:"closes_height"`
	} `json:"vote"`
}

// handleCreateAction is POST /api/issuances/{id}/actions (owner).
func (s *server) handleCreateAction(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	refuse := func(code int, reason string) {
		s.st.Audit(acct.AID, "action.create.refused", map[string]any{
			"issuance_id": iss.ID, "status": code, "reason": reason,
		})
		writeErr(w, code, "%s", reason)
	}
	if iss.Status != "live" || iss.AssetID == "" {
		refuse(409, "this issuance is not live on chain")
		return
	}
	var req createActionReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind != "dividend" && kind != "vote" {
		refuse(400, `kind must be "dividend" or "vote"`)
		return
	}
	if req.RecordHeight <= 0 {
		refuse(400, "a positive record_height is required")
		return
	}

	a := &CorporateAction{
		ID: mustID(), IssuanceID: iss.ID, AssetID: iss.AssetID, Kind: kind,
		State: "awaiting_snapshot", RecordHeight: req.RecordHeight,
	}
	switch kind {
	case "dividend":
		if req.Dividend == nil || req.Dividend.TotalAtoms == 0 {
			refuse(400, "a dividend needs dividend.asset and a positive dividend.total_atoms")
			return
		}
		asset := strings.ToLower(strings.TrimSpace(req.Dividend.Asset))
		if strings.EqualFold(asset, "usdx") {
			asset = s.cfg.usdxAsset
		}
		if !isHex64(asset) {
			refuse(400, "dividend.asset must be a 64-hex Sequentia asset id (or USDX)")
			return
		}
		addr, err := s.escrowNewAddress("seqpal-action-" + a.ID)
		if err != nil {
			refuse(502, "could not derive the action deposit address: "+err.Error())
			return
		}
		a.DivAsset = asset
		a.DivTotalAtoms = req.Dividend.TotalAtoms
		a.DepositAddress = addr
	case "vote":
		if req.Vote == nil || strings.TrimSpace(req.Vote.Question) == "" {
			refuse(400, "a vote needs vote.question")
			return
		}
		if len(req.Vote.Choices) < 2 {
			refuse(400, "a vote needs at least two vote.choices")
			return
		}
		if req.Vote.ClosesHeight <= req.RecordHeight {
			refuse(400, "vote.closes_height must be after the record height")
			return
		}
		choices, _ := json.Marshal(req.Vote.Choices)
		a.Question = strings.TrimSpace(req.Vote.Question)
		a.Choices = string(choices)
		a.ClosesHeight = req.Vote.ClosesHeight
	}
	if err := s.st.InsertAction(a); err != nil {
		writeErr(w, 500, "could not create the action")
		return
	}
	s.st.Audit(acct.AID, "action.create", map[string]any{
		"issuance_id": iss.ID, "action": a.ID, "kind": kind, "record_height": a.RecordHeight,
		"div_asset": a.DivAsset, "div_total_atoms": a.DivTotalAtoms, "closes_height": a.ClosesHeight,
	})
	resp := map[string]any{"action": s.actionView(a, false)}
	if kind == "dividend" {
		resp["deposit_address"] = a.DepositAddress
		resp["pay_amount"] = a.DivTotalAtoms
		resp["note"] = "fund this deposit with the dividend asset covering total_atoms; no claim pays until it confirms (fund-first)"
	}
	writeJSON(w, 200, resp)
}

// --- reads (public, no PII) --------------------------------------------------

// handleListActions is GET /api/issuances/{id}/actions (public): the actions
// with per-action state and tallies/totals. No identities.
func (s *server) handleListActions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	iss, err := s.st.IssuanceByID(id)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if iss == nil {
		writeErr(w, 404, "unknown issuance")
		return
	}
	actions, err := s.st.ActionsByIssuance(iss.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	out := make([]map[string]any, 0, len(actions))
	for _, a := range actions {
		out = append(out, s.actionView(a, false))
	}
	writeJSON(w, 200, map[string]any{"issuance_id": iss.ID, "actions": out})
}

// handleGetAction is GET /api/actions/{aid2} (public): the detail, including
// the snapshot summary once taken and, for votes, the running tally plus the
// per-outpoint proof list (txid:vout, atoms, choice) so anyone can re-verify.
// Never identities.
func (s *server) handleGetAction(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.ActionByID(r.PathValue("aid2"))
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if a == nil {
		writeErr(w, 404, "unknown action")
		return
	}
	writeJSON(w, 200, map[string]any{"action": s.actionView(a, true)})
}

// actionView renders the public view of an action. detail adds the snapshot
// summary and the per-outpoint proof list.
func (s *server) actionView(a *CorporateAction, detail bool) map[string]any {
	v := map[string]any{
		"id": a.ID, "issuance_id": a.IssuanceID, "asset": a.AssetID, "kind": a.Kind,
		"state": a.State, "record_height": a.RecordHeight, "created_at": a.CreatedAt,
	}
	if a.State == "snapshotted" {
		v["snapshot"] = map[string]any{
			"height":       a.SnapshotHeight,
			"holder_count": a.SnapshotCount,
			"total_atoms":  a.SnapshotTotal,
			"hash":         a.SnapshotHash,
			"anchor_txid":  a.AnchorTxid,
			"note":         a.SnapshotNote,
		}
	}
	claims, _ := s.st.ActionClaimsByAction(a.ID)
	switch a.Kind {
	case "dividend":
		var claimedAtoms, grossTotal, withheldTotal, netTotal, paid uint64
		for _, c := range claims {
			claimedAtoms += c.Atoms
			grossTotal += c.GrossAtoms
			withheldTotal += c.WithheldAtoms
			netTotal += c.NetAtoms
			if c.State == "paid" {
				paid++
			}
		}
		v["dividend"] = map[string]any{
			"asset": a.DivAsset, "total_atoms": a.DivTotalAtoms,
			"funded": a.Funded, "funded_atoms": a.FundedAtoms,
			"claims": len(claims), "claimed_atoms": claimedAtoms,
			"gross_total": grossTotal, "withheld_total": withheldTotal,
			"net_total": netTotal, "paid_claims": paid,
		}
	case "vote":
		tally := map[string]uint64{}
		for _, ch := range a.ChoiceList() {
			tally[ch] = 0
		}
		proofs := []map[string]any{}
		for _, c := range claims {
			tally[c.Choice] += c.Atoms
			if detail {
				ops := c.OutpointList()
				for _, op := range ops {
					row := map[string]any{"outpoint": op, "choice": c.Choice}
					if sr, _ := s.st.SnapshotRow(a.ID, op); sr != nil {
						row["atoms"] = sr.Atoms
					}
					proofs = append(proofs, row)
				}
			}
		}
		vote := map[string]any{
			"question": a.Question, "choices": a.ChoiceList(), "closes_height": a.ClosesHeight,
			"tally": tally, "ballots": len(claims),
		}
		if detail {
			sort.Slice(proofs, func(i, j int) bool {
				return proofs[i]["outpoint"].(string) < proofs[j]["outpoint"].(string)
			})
			vote["proofs"] = proofs
		}
		v["vote"] = vote
	}
	return v
}

// --- claim -------------------------------------------------------------------

type actionClaimReq struct {
	Pubkey        string   `json:"pubkey"`    // 66-hex compressed or 64-hex x-only
	Outpoints     []string `json:"outpoints"` // "txid:vout"
	PayoutAddress string   `json:"payout_address"`
	Choice        string   `json:"choice"`
	Sig           string   `json:"sig"`
}

// holdingProofStatement builds the canonical bytes whose sha256 the claimant
// signs under holdingProofTag. The purpose and payout/choice fields bind the
// proof to exactly this use, so a dividend proof can never be replayed as a
// ballot (or vice versa), and the aid binds it to the presenting session.
func holdingProofStatement(a *CorporateAction, outpoints []string, payoutAddress, choice, aid string) []byte {
	m := map[string]any{
		"v":             1,
		"action_id":     a.ID,
		"asset":         a.AssetID,
		"record_height": a.RecordHeight,
		"outpoints":     outpoints, // sorted by the caller
		"purpose":       a.Kind,
		"aid":           aid,
	}
	if a.Kind == "dividend" {
		m["payout_address"] = payoutAddress
	} else {
		m["choice"] = choice
	}
	stmt, _ := canonicalJSON(m)
	return stmt
}

// scriptForClaimKey derives the scriptPubKey a claim key must control:
// P2WPKH (0014 || hash160(pubkey)) from a 66-hex compressed key, or P2TR
// key-path (5120 || key) from a 64-hex x-only output key. Returns the script
// hex and the x-only key the BIP340 proof verifies against.
func scriptForClaimKey(pubkey string) (script, xonly string, err error) {
	pk := strings.ToLower(strings.TrimSpace(pubkey))
	switch len(pk) {
	case 66:
		raw, derr := hex.DecodeString(pk)
		if derr != nil {
			return "", "", derr
		}
		if _, perr := btcec.ParsePubKey(raw); perr != nil {
			return "", "", perr
		}
		return "0014" + hex.EncodeToString(hash160(raw)), pk[2:], nil
	case 64:
		raw, derr := hex.DecodeString(pk)
		if derr != nil {
			return "", "", derr
		}
		if _, perr := schnorr.ParsePubKey(raw); perr != nil {
			return "", "", perr
		}
		return "5120" + pk, pk, nil
	default:
		return "", "", errBadClaimKey
	}
}

var errBadClaimKey = jsonErr("pubkey must be a 66-hex compressed or 64-hex x-only public key")

type jsonErr string

func (e jsonErr) Error() string { return string(e) }

// handleActionClaim is POST /api/actions/{aid2}/claim (investor session): the
// holding-proof claim. Endpoint-KYC gate: the claimant's SeqPal ID claims
// record must be status verified.
func (s *server) handleActionClaim(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	a, err := s.st.ActionByID(r.PathValue("aid2"))
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if a == nil {
		writeErr(w, 404, "unknown action")
		return
	}
	refuse := func(code int, reason string) {
		s.st.Audit(acct.AID, "action.claim.refused", map[string]any{
			"action": a.ID, "status": code, "reason": reason,
		})
		writeErr(w, code, "%s", reason)
	}
	var req actionClaimReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	if a.State != "snapshotted" {
		refuse(409, "the record-date snapshot has not been taken yet; claims open once it is")
		return
	}
	if a.Kind == "vote" && a.ClosesHeight > 0 && s.tipHeight() >= a.ClosesHeight {
		refuse(409, "voting closed at Sequentia block "+strconv.FormatInt(a.ClosesHeight, 10))
		return
	}

	// Endpoint-KYC: the claimant must hold a verified SeqPal ID. This is the
	// gate that keeps a bearer asset's servicing surface identified even though
	// the chain itself never asked.
	claims, err := s.st.ClaimsByAID(acct.AID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if claims == nil || claims.Status != "verified" {
		refuse(403, "claiming requires a verified SeqPal ID; verify at POST /api/id/verify first")
		return
	}

	// Key and script derivation.
	script, xonly, kerr := scriptForClaimKey(req.Pubkey)
	if kerr != nil {
		refuse(400, kerr.Error())
		return
	}

	// Outpoints: sorted, deduplicated, each present in the snapshot with a
	// script this key derives.
	if len(req.Outpoints) == 0 {
		refuse(400, "at least one snapshot outpoint is required")
		return
	}
	seen := map[string]bool{}
	outpoints := make([]string, 0, len(req.Outpoints))
	for _, op := range req.Outpoints {
		op = strings.ToLower(strings.TrimSpace(op))
		if op == "" || seen[op] {
			continue
		}
		seen[op] = true
		outpoints = append(outpoints, op)
	}
	sort.Strings(outpoints)
	outAtoms := map[string]uint64{}
	var claimedAtoms uint64
	for _, op := range outpoints {
		row, err := s.st.SnapshotRow(a.ID, op)
		if err != nil {
			writeErr(w, 500, "store error")
			return
		}
		if row == nil {
			refuse(400, "outpoint "+op+" is not in the record-date snapshot")
			return
		}
		if !strings.EqualFold(row.Script, script) {
			refuse(403, "outpoint "+op+" is not controlled by the supplied key (its snapshot script does not derive from it)")
			return
		}
		outAtoms[op] = row.Atoms
		claimedAtoms += row.Atoms
	}

	// Purpose-specific fields.
	payoutAddress := strings.TrimSpace(req.PayoutAddress)
	choice := strings.TrimSpace(req.Choice)
	switch a.Kind {
	case "dividend":
		if payoutAddress == "" {
			refuse(400, "a payout_address is required to claim a dividend")
			return
		}
		// Reuse the investor-mandate address discipline: valid on this network,
		// never blinded, never one of the claimant's enclave key-path addresses.
		if err := s.validateOrdinaryPayoutAddress(acct.AID, payoutAddress); err != nil {
			refuse(400, "payout_address: "+err.Error())
			return
		}
	case "vote":
		valid := false
		for _, c := range a.ChoiceList() {
			if c == choice {
				valid = true
				break
			}
		}
		if !valid {
			refuse(400, "choice must be one of the vote's configured choices")
			return
		}
	}

	// The holding proof: BIP340 under holdingProofTag over sha256 of the
	// canonical statement, by the key that derives the snapshot script.
	stmt := holdingProofStatement(a, outpoints, payoutAddress, choice, acct.AID)
	digest := sha256.Sum256(stmt)
	if strings.TrimSpace(req.Sig) == "" {
		writeJSON(w, 200, map[string]any{
			"sign_this": string(stmt), "tag": holdingProofTag,
			"note": "sign sha256 of these canonical bytes under the tag with the key controlling the outpoints, then resubmit as sig",
		})
		return
	}
	if err := s.verifyKeyStatement(xonly, holdingProofTag, digest[:], req.Sig); err != nil {
		refuse(400, "the holding-proof signature does not verify for the supplied key")
		return
	}

	// Withholding (dividends): the claimant's own claims record decides.
	c := &ActionClaim{
		ID: mustID(), ActionID: a.ID, AID: acct.AID, Pubkey: strings.ToLower(strings.TrimSpace(req.Pubkey)),
		Atoms: claimedAtoms, Sig: strings.TrimSpace(req.Sig), State: "registered",
	}
	opsJSON, _ := json.Marshal(outpoints)
	c.Outpoints = string(opsJSON)
	if a.Kind == "dividend" {
		gross := mulDiv(a.DivTotalAtoms, claimedAtoms, a.SnapshotTotal)
		withheld, treatyBps, status := withholdingFor(claims, gross)
		c.PayoutAddress = payoutAddress
		c.GrossAtoms = gross
		c.WithheldAtoms = withheld
		c.NetAtoms = gross - withheld
		c.TreatyBps = treatyBps
		c.TaxStatus = status
		if !a.Funded {
			c.State = "awaiting_funding"
		}
	} else {
		c.Choice = choice
	}

	// One claim per (action, outpoint): the outpoint table's primary key.
	ok, err := s.st.InsertActionClaim(c, outAtoms)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if !ok {
		refuse(409, "one or more of these outpoints has already been claimed for this action")
		return
	}
	s.st.Audit(acct.AID, "action.claim", map[string]any{
		"action": a.ID, "claim": c.ID, "kind": a.Kind, "atoms": claimedAtoms,
		"outpoints": outpoints, "gross": c.GrossAtoms, "withheld": c.WithheldAtoms,
		"net": c.NetAtoms, "choice": c.Choice,
	})

	// A funded dividend pays immediately through the fund-safety machinery; an
	// unfunded one waits for the moneywatch funding pass.
	if a.Kind == "dividend" && a.Funded {
		s.payActionClaim(a, c)
		c, _ = s.st.ActionClaimByID(c.ID)
	}
	writeJSON(w, 200, map[string]any{"claim": c})
}

// validateOrdinaryPayoutAddress applies the investor-mandate address checks to
// a dividend payout address: network-valid, not a blinded (confidential)
// address, and not one of the claimant's enclave key-path addresses (paying an
// enclave would strand the funds; the check fails closed).
func (s *server) validateOrdinaryPayoutAddress(aid, addr string) error {
	if err := s.validateSeqAddress(addr); err != nil {
		return err
	}
	// Blinded (blech32) addresses carry the confidential HRPs; a dividend must
	// pay a plain, wallet-scanned address.
	lower := strings.ToLower(addr)
	for _, hrp := range []string{"tsqb1", "sqb1", "el1", "lq1", "ert1"} {
		if strings.HasPrefix(lower, hrp) {
			return jsonErr("blinded (confidential) addresses are not accepted; provide the unblinded address")
		}
	}
	if isEnclave, err := s.isInvestorEnclaveAddress(aid, addr); err != nil {
		return jsonErr("the address could not be verified right now; please retry")
	} else if isEnclave {
		return jsonErr("this is one of your enclave key-path addresses; provide an ordinary wallet address a wallet can scan")
	}
	return nil
}

// --- dividend payment (fund-safety machinery) --------------------------------

// payActionClaim pays one registered dividend claim with the M5/M7 fund-safety
// discipline: intent persisted before broadcast, marker-scoped reconcile before
// any retry, payment from seqpal-escrow, never a double-pay.
func (s *server) payActionClaim(a *CorporateAction, c *ActionClaim) {
	if c.State == "paid" || c.State == "no_payment" {
		return
	}
	if c.NetAtoms == 0 {
		_ = s.st.UpdateActionClaimFields(c.ID, map[string]any{
			"state": "no_payment", "reason": "net zero after withholding",
		})
		return
	}
	marker := actMarker(a.ID, c.ID)

	// Reconcile a lost write: a prior attempt may already have broadcast.
	if c.State == "paying" && c.Txid == "" {
		if txid, found := s.escrowFindSend("usdx", marker); found {
			_ = s.st.UpdateActionClaimFields(c.ID, map[string]any{"state": "paid", "txid": txid, "reason": ""})
			s.st.Audit(c.AID, "action.pay.reconciled", map[string]any{"action": a.ID, "claim": c.ID, "txid": txid})
			return
		}
	}

	// Persist the intent BEFORE broadcasting so a lost write is reconcilable.
	_ = s.st.UpdateActionClaimFields(c.ID, map[string]any{"state": "paying"})
	txid, err := s.releaseAsset(c.PayoutAddress, c.NetAtoms, marker, a.DivAsset)
	if err != nil {
		_ = s.st.UpdateActionClaimFields(c.ID, map[string]any{
			"state": "failed", "reason": "payout failed: " + err.Error(),
		})
		s.st.Audit(c.AID, "action.pay.failed", map[string]any{"action": a.ID, "claim": c.ID, "error": err.Error()})
		return
	}
	_ = s.st.UpdateActionClaimFields(c.ID, map[string]any{"state": "paid", "txid": txid, "reason": ""})
	_ = s.st.InsertLedger(&LedgerEntry{
		IssuanceID: a.IssuanceID, Kind: "action_dividend", Rail: "usdx",
		Amount: c.NetAtoms, Ccy: a.DivAsset, Txid: txid, FundsSimulated: false,
	})
	s.st.Audit(c.AID, "action.pay", map[string]any{
		"action": a.ID, "claim": c.ID, "txid": txid,
		"net_atoms": c.NetAtoms, "withheld_atoms": c.WithheldAtoms,
	})
}

// watchActionDeposits advances a dividend action to funded once the issuer's
// deposit confirms at escrowConfs AND covers the pool (fund-first: nothing pays
// before this). Idempotent: it only flips funded once.
func (s *server) watchActionDeposits() {
	if s.cfg.nodeURL == "" {
		return
	}
	actions, err := s.st.UnfundedDividendActions()
	if err != nil {
		return
	}
	for _, a := range actions {
		dep, ok, err := s.assetDeposit(a.DepositAddress, a.DivAsset)
		if err != nil || !ok || dep.Confirmations < s.cfg.escrowConfs {
			continue
		}
		if dep.Atoms < a.DivTotalAtoms {
			s.st.Audit("", "action.underfunded", map[string]any{
				"action": a.ID, "deposited": dep.Atoms, "required": a.DivTotalAtoms,
			})
			continue
		}
		if err := s.st.UpdateActionFields(a.ID, map[string]any{
			"funded": 1, "funded_atoms": dep.Atoms, "funded_txid": dep.Txid,
		}); err != nil {
			continue
		}
		_ = s.st.InsertLedger(&LedgerEntry{
			IssuanceID: a.IssuanceID, Kind: "action_funding", Rail: "usdx",
			Amount: dep.Atoms, Ccy: a.DivAsset, Txid: dep.Txid, FundsSimulated: false,
		})
		s.st.Audit("", "action.funded", map[string]any{
			"action": a.ID, "issuance_id": a.IssuanceID, "txid": dep.Txid, "atoms": dep.Atoms,
		})
	}
}

// payDueActionClaims retries registered/awaiting/failed claims of funded
// dividend actions (the resumable half of the fund-first pattern).
func (s *server) payDueActionClaims() {
	if s.cfg.nodeURL == "" {
		return
	}
	actions, err := s.st.FundedDividendActions()
	if err != nil {
		return
	}
	for _, a := range actions {
		claims, err := s.st.ActionClaimsByAction(a.ID)
		if err != nil {
			continue
		}
		for _, c := range claims {
			switch c.State {
			case "registered", "awaiting_funding", "paying", "failed":
				s.payActionClaim(a, c)
			}
		}
	}
}
