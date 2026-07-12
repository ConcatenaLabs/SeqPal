package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
)

// marketAbuseTag is the BIP340 tag for the optional signed market-abuse
// acknowledgment (distinct from the mandate and claims tags).
const marketAbuseTag = "seqpal-market-abuse-ack-v1"

// M8 secondary market: policy-co-signed holder-to-holder transfers of a
// restricted asset between two SeqPal identities (the hosted transfer path, the
// exact M5/M6 delivery mechanism but holder-to-holder). A browser-key holder
// builds+signs in the SPA: seqpald builds the transfer at openampd (returning the
// sighashes to sign), the SPA signs with the holder's enclave key, and seqpald
// completes it. A wallet-linked holder transfers in the live wallet; the /v1/log
// poller (walletpoll.go) captures that transfer and joins it to identities.
//
// THE REFUSAL PATH IS A FIRST-CLASS RESULT, not an error: an ineligible
// recipient, a resale inside the lockup window, and a Reg S distribution-
// compliance window each make openampd's checkTransfer return a real 403 with the
// reason at /complete. seqpald records the refusal as a terminal state and returns
// that reason to the SPA; openampd also writes the refusal to its public /v1/log.
//
// TRAVEL RULE: every P2P record stores the originator and beneficiary AIDs, both
// resolved to registered platform identities, with their captured name/residence.
//
// FUND-SAFETY: the openampd pending build is consume-once, so a completed transfer
// cannot be re-broadcast; a lost complete write is reconciled by scanning
// openampd's /v1/log for the settled transfer before any retry, never a
// double-transfer (the M5/M6 invariant).

const marketAbuseAckVersion = 1

// marketAbuseAckStatement builds the canonical bytes an investor acknowledges.
func marketAbuseAckStatement(aid string) []byte {
	stmt, _ := canonicalJSON(map[string]any{
		"role": "market-abuse-ack", "aid": aid, "version": marketAbuseAckVersion,
	})
	return stmt
}

// --- market-abuse / insider-dealing acknowledgment ---------------------------

type marketAbuseAckReq struct {
	Signature   string `json:"signature"`
	SignerXOnly string `json:"signer_xonly"`
}

// handleMarketAbuseAck is POST /api/id/market-abuse-ack (session): record the
// once-per-investor market-abuse / insider-dealing acknowledgment that gates the
// platform's transfer surfaces. A signature is optional; when supplied it is a
// tagged acknowledgment by the caller's own SeqPal ID key. Disclosed honestly as a
// PLATFORM-LAYER control: it is never enforced at the policy co-signature, so it
// blocks the SeqPal UI, not a chain transfer.
func (s *server) handleMarketAbuseAck(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	var req marketAbuseAckReq
	_ = readJSON(r, &req)
	statement := marketAbuseAckStatement(acct.AID)
	ack := &MarketAbuseAck{AID: acct.AID, Version: marketAbuseAckVersion, AckHash: sha256Hex(statement)}
	if sig := strings.TrimSpace(req.Signature); sig != "" {
		signer := strings.TrimSpace(req.SignerXOnly)
		if signer == "" {
			signer = acct.XOnly
		}
		if signer != acct.XOnly {
			writeErr(w, 403, "the acknowledgment must be signed by your own SeqPal ID key")
			return
		}
		if err := verifyTaggedByKey(signer, marketAbuseTag, statement, sig); err != nil {
			writeErr(w, 400, "the acknowledgment signature does not verify for your key")
			return
		}
		ack.Signature, ack.SignerXOnly = sig, signer
	}
	if err := s.st.UpsertMarketAbuseAck(ack); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	s.st.Audit(acct.AID, "market_abuse.ack", map[string]any{"version": marketAbuseAckVersion, "signed": ack.Signature != ""})
	writeJSON(w, 200, map[string]any{
		"acknowledged": true, "version": marketAbuseAckVersion, "at": ack.CreatedAt,
		"note": "platform-layer control; recorded once and disclosed as such, not enforced at the policy co-signature",
	})
}

// handleMarketAbuseAckGet is GET /api/id/market-abuse-ack (session): whether the
// caller has recorded the acknowledgment, and the canonical bytes to sign.
func (s *server) handleMarketAbuseAckGet(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	ack, err := s.st.MarketAbuseAckByAID(acct.AID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	out := map[string]any{
		"acknowledged": ack != nil, "version": marketAbuseAckVersion,
		"sign_this": string(marketAbuseAckStatement(acct.AID)), "tag": marketAbuseTag,
	}
	if ack != nil {
		out["at"] = ack.CreatedAt
	}
	writeJSON(w, 200, out)
}

// --- P2P transfer initiate (build) -------------------------------------------

type p2pInitiateReq struct {
	Asset      string `json:"asset"`
	ToAID      string `json:"to_aid"`
	Atoms      uint64 `json:"atoms"`
	IssuanceID string `json:"issuance_id"` // optional; asset is authoritative
}

// handleP2PInitiate is POST /api/transfers (session): build a policy-co-signed
// holder-to-holder transfer for the caller (the originator) to sign in the SPA.
// It resolves the asset to a SeqPal-managed issuance, enforces the platform-layer
// market-abuse acknowledgment, requires the beneficiary to be a registered
// platform identity (the travel-rule join), previews recipient eligibility
// (advisory), and asks openampd to assemble the transfer, returning the sighashes.
func (s *server) handleP2PInitiate(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	var req p2pInitiateReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	asset := strings.TrimSpace(req.Asset)
	toAID := strings.TrimSpace(req.ToAID)
	if asset == "" || toAID == "" || req.Atoms == 0 {
		writeErr(w, 400, "asset, to_aid and a positive atoms are required")
		return
	}
	if toAID == acct.AID {
		writeErr(w, 400, "the beneficiary must be a different identity")
		return
	}

	iss, err := s.st.IssuanceByAsset(asset)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if iss == nil || iss.AssetID == "" {
		writeErr(w, 404, "not a SeqPal-managed asset")
		return
	}

	// Platform-layer market-abuse gate: the transfer surfaces are enabled only
	// after the once-per-investor acknowledgment is on record.
	if ack, _ := s.st.MarketAbuseAckByAID(acct.AID); ack == nil {
		s.st.Audit(acct.AID, "p2p.blocked.no_ack", map[string]any{"asset": asset})
		writeJSON(w, 403, map[string]any{
			"error":       "record the market-abuse / insider-dealing acknowledgment before using the transfer surfaces",
			"requirement": "market_abuse_ack",
			"note":        "platform-layer control, disclosed as such; it is not enforced at the policy co-signature",
		})
		return
	}

	// Travel-rule join: the beneficiary must be a registered platform identity.
	benef, err := s.st.AccountByAID(toAID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if benef == nil {
		writeErr(w, 404, "the beneficiary is not a registered SeqPal identity; a P2P transfer captures both counterparties")
		return
	}

	// Advisory recipient-eligibility preflight. The authoritative decision is the
	// policy server's at /complete; this only lets the SPA warn before signing.
	preflight := s.recipientPreflight(toAID, asset)

	// Build the transfer at openampd (sender = the originator's own AID). This
	// returns the enclave sighashes the SPA signs with the holder's browser key.
	var built struct {
		ID     string `json:"id"`
		Tx     string `json:"tx"`
		ToSign []struct {
			Input   int    `json:"input"`
			Sighash string `json:"sighash"`
			Pubkey  string `json:"pubkey"`
		} `json:"to_sign"`
	}
	body := map[string]any{
		"asset": asset, "sender_aid": acct.AID, "recipient_aid": toAID,
		"atoms": req.Atoms, "fee_mode": "sponsor",
	}
	if code, err := s.callOpenAMPStatus("POST", "/v1/transfers", "", body, &built); err != nil {
		writeErr(w, statusForOpenAMP(code), "could not build the transfer: %v", err)
		return
	}
	if built.ID == "" {
		writeErr(w, 502, "the transfer build returned no id")
		return
	}
	// The originator signs only their own enclave inputs; refuse a build that asks
	// for any other key, as the escrow delivery path does.
	toSign := make([]map[string]any, 0, len(built.ToSign))
	for _, ts := range built.ToSign {
		if ts.Pubkey != "" && ts.Pubkey != acct.XOnly {
			writeErr(w, 502, "the transfer build wants a signature from a key you do not hold")
			return
		}
		toSign = append(toSign, map[string]any{"input": ts.Input, "sighash": ts.Sighash, "pubkey": ts.Pubkey})
	}

	origClaims, _ := s.st.ClaimsByAID(acct.AID)
	benefClaims, _ := s.st.ClaimsByAID(toAID)
	t := &P2PTransfer{
		ID: mustID(), OaID: built.ID, IssuanceID: iss.ID, AssetID: asset,
		OriginatorAID: acct.AID, BeneficiaryAID: toAID, Atoms: req.Atoms,
		State: "awaiting_signature", Source: "browser",
		OrigName: acct.DisplayName, OrigResidence: claimsResidence(origClaims),
		BenefName: benef.DisplayName, BenefResidence: claimsResidence(benefClaims), BenefRegistered: true,
	}
	if err := s.st.InsertP2PTransfer(t); err != nil {
		writeErr(w, 500, "could not record the transfer")
		return
	}
	s.st.Audit(acct.AID, "p2p.initiate", map[string]any{
		"transfer": t.ID, "asset": asset, "beneficiary": toAID, "atoms": req.Atoms,
	})
	writeJSON(w, 200, map[string]any{
		"transfer_id": t.ID, "oa_id": built.ID, "tx": built.Tx, "to_sign": toSign,
		"recipient_preflight": preflight,
		"travel_rule":         travelRuleView(t),
		"note":                "sign the sighashes with your SeqPal ID key, then POST them to /api/transfers/{id}/complete; nothing is final at 0-conf",
	})
}

// --- P2P transfer complete (co-sign + broadcast, or first-class refusal) ------

type p2pCompleteReq struct {
	Sigs map[string]string `json:"sigs"`
}

// handleP2PComplete is POST /api/transfers/{id}/complete (session): hand the
// originator's signatures to openampd, which re-derives every policy claim and
// either co-signs and broadcasts, or REFUSES with a real 403 and a reason. A
// refusal is recorded as a terminal state and returned honestly (not a 500). A
// success records the txid with the M5/M6 idempotency shape.
func (s *server) handleP2PComplete(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	t, err := s.st.P2PTransferByID(r.PathValue("id"))
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if t == nil {
		writeErr(w, 404, "unknown transfer")
		return
	}
	if t.OriginatorAID != acct.AID {
		writeErr(w, 403, "that transfer belongs to another account")
		return
	}
	if t.State == "settled" {
		writeJSON(w, 200, map[string]any{"transfer_id": t.ID, "txid": t.Txid, "state": "settled"})
		return
	}
	if t.State == "refused" {
		writeJSON(w, 403, map[string]any{"transfer_id": t.ID, "state": "refused", "refused": true, "reason": t.Reason})
		return
	}
	var req p2pCompleteReq
	if err := readJSON(r, &req); err != nil || len(req.Sigs) == 0 {
		writeErr(w, 400, "sigs are required")
		return
	}

	// Persist the intent BEFORE the co-sign call so a lost write after broadcast is
	// reconcilable from openampd's log rather than re-broadcast.
	_ = s.st.UpdateP2PTransferFields(t.ID, map[string]any{"state": "completing"})

	var done struct {
		Txid string `json:"txid"`
	}
	code, cerr := s.callOpenAMPStatus("POST", "/v1/transfers/"+t.OaID+"/complete", "",
		map[string]any{"sigs": req.Sigs}, &done)
	if cerr != nil {
		// A 403 is a first-class policy REFUSAL (ineligible recipient, lockup window,
		// Reg S window). Record it, surface the reason, and let the SPA render it as a
		// headline result. openampd also logged it to its public /v1/log.
		if code == http.StatusForbidden {
			_ = s.st.UpdateP2PTransferFields(t.ID, map[string]any{"state": "refused", "reason": cerr.Error()})
			s.st.Audit(acct.AID, "p2p.refused", map[string]any{
				"transfer": t.ID, "asset": t.AssetID, "beneficiary": t.BeneficiaryAID, "reason": cerr.Error(),
			})
			writeJSON(w, 403, map[string]any{
				"transfer_id": t.ID, "state": "refused", "refused": true, "reason": cerr.Error(),
				"log_url": "/openamp/v1/log",
				"note":    "the policy server refused this transfer; the reason is recorded here and in the public transparency log",
			})
			return
		}
		// Not a refusal: an operational error. openampd consumes the pending build on
		// every complete attempt, so this oa_id cannot be re-completed; the build may
		// also have broadcast before the write was lost. Reconcile against openampd's
		// log first (never assume it did not settle); otherwise the transfer is failed
		// and the SPA rebuilds a fresh one.
		if txid, found := s.findTransferInLog(t.AssetID, t.OriginatorAID, t.Atoms); found && s.txidUnclaimed(txid, t.ID) {
			s.recordP2PSettled(t, txid, true)
			writeJSON(w, 200, map[string]any{"transfer_id": t.ID, "txid": txid, "state": "settled", "reconciled": true})
			return
		}
		_ = s.st.UpdateP2PTransferFields(t.ID, map[string]any{"state": "failed", "reason": cerr.Error()})
		writeErr(w, statusForOpenAMP(code), "the transfer could not be completed (rebuild to retry): %v", cerr)
		return
	}
	if done.Txid == "" {
		writeErr(w, 502, "the transfer complete returned no txid")
		return
	}
	s.recordP2PSettled(t, done.Txid, false)
	writeJSON(w, 200, map[string]any{"transfer_id": t.ID, "txid": done.Txid, "state": "settled"})
}

// recordP2PSettled writes the terminal settled state for a P2P transfer and its
// ledger row. On a reconciled settle (a prior attempt already broadcast) the
// ledger entry is skipped so a replay never double-writes the books.
func (s *server) recordP2PSettled(t *P2PTransfer, txid string, reconciled bool) {
	_ = s.st.UpdateP2PTransferFields(t.ID, map[string]any{"state": "settled", "txid": txid, "reason": ""})
	if !reconciled {
		_ = s.st.InsertLedger(&LedgerEntry{
			IssuanceID: t.IssuanceID, Kind: "p2p_transfer", Rail: "asset",
			Amount: t.Atoms, Ccy: assetTicker(s, t.AssetID), Txid: txid,
		})
	}
	s.st.Audit(t.OriginatorAID, "p2p.settle", map[string]any{
		"transfer": t.ID, "asset": t.AssetID, "beneficiary": t.BeneficiaryAID,
		"atoms": t.Atoms, "txid": txid, "reconciled": reconciled,
	})
}

// --- reads -------------------------------------------------------------------

// handleListMyTransfers is GET /api/transfers (session): the caller's P2P
// transfers as originator, each with its travel-rule counterparties and state
// (including a first-class refusal reason).
func (s *server) handleListMyTransfers(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	ts, err := s.st.P2PTransfersByOriginator(acct.AID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	out := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		out = append(out, map[string]any{"transfer": t, "travel_rule": travelRuleView(t)})
	}
	writeJSON(w, 200, map[string]any{"transfers": out, "log_url": "/openamp/v1/log"})
}

// --- helpers -----------------------------------------------------------------

// recipientPreflight is the advisory recipient-side eligibility check (the same
// evaluation /api/eligibility runs). It never blocks; the policy server's 403 at
// /complete is authoritative.
func (s *server) recipientPreflight(aid, asset string) map[string]any {
	var user openampUser
	if err := s.callOpenAMP("GET", "/v1/users/"+aid, "", nil, &user); err != nil {
		return map[string]any{"eligible": false, "reasons": []string{"the beneficiary is not registered with the policy server"}}
	}
	rules, ok, err := s.assetRules(asset)
	if err != nil || !ok {
		return map[string]any{"eligible": false, "reasons": []string{"unknown asset rules"}}
	}
	eligible, reasons := evalEligibility(user.Categories, user.Frozen, rules, s.tipHeight())
	return map[string]any{"eligible": eligible, "reasons": reasons}
}

// findTransferInLog scans openampd's public transparency log for a settled
// "transfer" entry naming this asset and sender with this atom count, and returns
// its txid. openampd appends the entry as part of the broadcast, so this is the
// transfer analogue of escrowFindSend: it recovers a settle whose seqpald write
// was lost, so a retry never re-broadcasts. It takes the newest match.
func (s *server) findTransferInLog(assetID, senderAID string, atoms uint64) (string, bool) {
	return s.logFindMoved("transfer", assetID, senderAID, atoms)
}

// logFindMoved scans openampd's /v1/log for the newest settled entry of an action
// ("transfer" or "burn") that moved a given atom count of an asset from a sender,
// returning its txid. It is the shared reconciliation probe for the P2P transfer
// and DR burn paths, so a lost seqpald write is recovered rather than re-broadcast.
func (s *server) logFindMoved(action, assetID, senderAID string, atoms uint64) (string, bool) {
	req, err := http.NewRequest("GET", s.cfg.openampURL+"/v1/log", nil)
	if err != nil {
		return "", false
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	var txid string
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
				Asset   string `json:"asset"`
				Sender  string `json:"sender"`
				Atoms   uint64 `json:"atoms"`
				Txid    string `json:"txid"`
				Refused bool   `json:"refused"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if e.Action == action && !e.Data.Refused && e.Data.Asset == assetID &&
			e.Data.Sender == senderAID && e.Data.Atoms == atoms && e.Data.Txid != "" {
			txid, found = e.Data.Txid, true
		}
	}
	return txid, found
}

// txidUnclaimed reports whether a txid discovered in the log is not already
// recorded on a different P2P transfer, so reconciliation never attributes one
// on-chain transfer to two records.
func (s *server) txidUnclaimed(txid, selfID string) bool {
	other, err := s.st.P2PTransferByTxid(txid)
	if err != nil {
		return false
	}
	return other == nil || other.ID == selfID
}

// statusForOpenAMP maps a policy-server status onto the status seqpald returns.
// A refusal (403) is handled by the caller; other 4xx are passed through so the
// SPA sees the true failure class (409 insufficient balance, 404 unknown), and
// anything else degrades to 502 (bad upstream).
func statusForOpenAMP(code int) int {
	switch code {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusForbidden:
		return code
	default:
		return http.StatusBadGateway
	}
}

func travelRuleView(t *P2PTransfer) map[string]any {
	return map[string]any{
		"originator":   map[string]any{"aid": t.OriginatorAID, "name": t.OrigName, "residence": t.OrigResidence},
		"beneficiary":  map[string]any{"aid": t.BeneficiaryAID, "name": t.BenefName, "residence": t.BenefResidence, "registered": t.BenefRegistered},
		"captured_via": t.Source,
	}
}

func claimsResidence(c *Claims) string {
	if c == nil {
		return ""
	}
	return c.Residence
}

// assetTicker resolves a ticker for a ledger row from the managing issuance, or
// falls back to a short asset id.
func assetTicker(s *server, assetID string) string {
	if iss, _ := s.st.IssuanceByAsset(assetID); iss != nil && iss.Ticker != "" {
		return iss.Ticker
	}
	if len(assetID) > 8 {
		return assetID[:8]
	}
	return assetID
}
