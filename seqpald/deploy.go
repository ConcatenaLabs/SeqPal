package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	deploysPerAccountPerHour = 5
	deploysGlobalPerHour     = 20

	// defaultFeeConvertAtoms is the safe fallback when neither the issuer nor the
	// price server supplies a fee-conversion figure. It is used only when a live
	// price is unavailable; the normal path derives the value from /prices.
	defaultFeeConvertAtoms = 100
)

// Reserved tickers: the Sequence token and the tickers already carried by the
// live Sequentia testnet assets and the parent chain's unit.
var reservedTickers = map[string]bool{
	"SEQ": true, "TSEQ": true, "BTC": true, "TBTC": true,
	"USDX": true, "EURX": true, "GOLD": true, "SILVR": true, "OILX": true,
}

var tickerRE = regexp.MustCompile(`^[A-Z0-9]{2,8}$`)

func validateTicker(ticker string) error {
	if !tickerRE.MatchString(ticker) {
		return fmt.Errorf("ticker must be 2 to 8 characters, uppercase letters and digits only")
	}
	if reservedTickers[ticker] {
		return fmt.Errorf("ticker %s is reserved", ticker)
	}
	return nil
}

// rateLimiter is an in-memory sliding window over deploy attempts. M1 runs a
// single seqpald process, so process-local counters are the whole population;
// refusals are recorded in the audit log either way.
type rateLimiter struct {
	mu      sync.Mutex
	perAID  map[string][]time.Time
	global  []time.Time
	nowFunc func() time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{perAID: map[string][]time.Time{}, nowFunc: time.Now}
}

// allow records an attempt and reports whether it is within both budgets. The
// window is one hour.
func (rl *rateLimiter) allow(aid string) (bool, string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.nowFunc()
	cutoff := now.Add(-time.Hour)
	mine := prune(rl.perAID[aid], cutoff)
	rl.global = prune(rl.global, cutoff)
	if len(mine) >= deploysPerAccountPerHour {
		rl.perAID[aid] = mine
		return false, "account deploy rate limit reached (5 per hour)"
	}
	if len(rl.global) >= deploysGlobalPerHour {
		rl.perAID[aid] = mine
		return false, "platform deploy rate limit reached (20 per hour)"
	}
	rl.perAID[aid] = append(mine, now)
	rl.global = append(rl.global, now)
	return true, ""
}

func prune(ts []time.Time, cutoff time.Time) []time.Time {
	out := ts[:0]
	for _, t := range ts {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	return out
}

// deployReq is what the SPA sends. The issuance's owner, ticker, and name come
// from the stored record, never from this body: only the mint parameters do.
type deployReq struct {
	IssuanceID     string          `json:"issuance_id"`
	Supply         uint64          `json:"supply"`
	Precision      int             `json:"precision"`
	Clawback       *bool           `json:"clawback"`
	Confidential   bool            `json:"confidential"`
	FeeConvertAtom uint64          `json:"fee_convert_atoms"`
	Terms          json.RawMessage `json:"terms"`
	TermsHash      string          `json:"terms_hash"` // cross-check only; the server computes its own
	// IssuerPubkey (M9) is the entity's own browser key (x-only hex) requesting the
	// external-issuer path: it becomes the enclave issuer half, so clawback needs
	// the issuer's browser signature (two-phase) and the server never holds an
	// issuer key for this asset. It MUST be this account's own key (the issuer of
	// record the browser signs clawbacks with); a mismatch is refused. When absent
	// the deploy is byte-identical to pre-M9 (server-generated key, legacy clawback).
	IssuerPubkey string `json:"issuer_pubkey"`
}

func (s *server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	var req deployReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	iss := s.ownedIssuance(w, acct, req.IssuanceID)
	if iss == nil {
		return
	}

	refuse := func(code int, reason string) {
		s.st.Audit(acct.AID, "deploy.refused", map[string]any{
			"issuance_id": iss.ID, "status": code, "reason": reason,
		})
		writeErr(w, code, "%s", reason)
	}

	if req.Supply < 1 {
		refuse(400, "supply must be at least 1 token")
		return
	}
	if req.Precision < 1 || req.Precision > 8 {
		refuse(400, "precision must be between 1 and 8")
		return
	}
	if err := validateTicker(iss.Ticker); err != nil {
		refuse(400, err.Error())
		return
	}
	// atoms = supply * 10^precision, guarded against uint64 overflow.
	atoms, ok := atomsFor(req.Supply, req.Precision)
	if !ok {
		refuse(400, "supply is too large for the chosen precision")
		return
	}
	if req.Confidential && !s.cfg.confidential {
		refuse(501, "confidential issuance is not available on this deployment; the node is not confidentiality-enabled")
		return
	}

	// M9: external issuer key. When the SPA supplies the entity's own browser key,
	// that key (not a server-generated one) becomes the enclave issuer half, so
	// clawback runs two-phase and the server never holds an issuer key for this
	// asset. The source is the deploying identity itself: this account is the issuer
	// of record (registered as the clawback authority below), and its browser key is
	// the one that can later sign the L_claw sweep, so the supplied key must equal
	// it. A mismatch is refused, like the mandate signer cross-check. Absent = the
	// legacy server-held key, byte-identical to pre-M9.
	issuerExternal := false
	issuerPubkey := strings.TrimSpace(req.IssuerPubkey)
	if issuerPubkey != "" {
		if !strings.EqualFold(issuerPubkey, acct.XOnly) {
			refuse(400, "issuer_pubkey must be this account's own key (the issuer of record); the browser signs clawbacks with it")
			return
		}
		issuerPubkey = acct.XOnly
		issuerExternal = true
	}

	// The canonical terms hash is a server-side fact. The browser's value is only
	// a cross-check: if the two disagree, the terms the user saw are not the terms
	// that would be committed to on chain, so nothing is minted.
	terms := req.Terms
	if len(terms) == 0 {
		terms = iss.Terms
	}
	if err := json.Unmarshal(rawOrEmpty(terms), new(any)); err != nil {
		refuse(400, "terms must be a JSON object")
		return
	}
	// M4: bind the generated document set into terms so terms_hash (and thus the
	// on-chain contract_hash) commits to the exact document bytes. ensureDocuments
	// is deterministic and idempotent, so a browser that already called the
	// documents endpoint recomputes the identical hash and the cross-check below
	// still holds; a deploy that skipped that step still binds real documents.
	boundTerms, manifest, err := s.ensureDocuments(iss, rawOrEmpty(terms))
	if err != nil {
		refuse(400, "documents could not be generated: "+err.Error())
		return
	}
	var termsObj any
	if err := json.Unmarshal(boundTerms, &termsObj); err != nil {
		refuse(400, "terms must be a JSON object")
		return
	}
	canonical, err := canonicalJSON(termsObj)
	if err != nil {
		refuse(400, "terms could not be canonicalized: "+err.Error())
		return
	}
	termsHash := sha256Hex(canonical)
	if req.TermsHash != "" && !strings.EqualFold(req.TermsHash, termsHash) {
		refuse(400, "terms_hash mismatch: the terms the browser hashed are not the terms it sent")
		return
	}

	// Publish the terms document (manifest + canonical bytes) so GET /api/terms
	// resolves it, and open the offer window for preimage gating.
	_ = s.st.PutTermsDoc(&TermsDoc{
		TermsHash: termsHash, IssuanceID: iss.ID, CanonicalTerms: string(canonical),
		ManifestHash: manifest.ManifestHash, Manifest: string(jsonCompact(manifest)),
	})
	_, _ = s.st.EnsureOffering(iss.ID)

	// M4: a public offering cannot deploy without an RFSA filing bound to these
	// exact terms. Private placements (the default when terms carry no public
	// marker) are ungated, which keeps every pre-M4 deploy path working.
	if isPublicOffering(canonical) {
		filing, ferr := s.st.FilingByTermsHash(termsHash)
		if ferr != nil {
			writeErr(w, 500, "store error")
			return
		}
		if filing == nil {
			refuse(403, "a public offering requires an RFSA filing bound to these terms; file at POST /rfsa/filings first")
			return
		}
		s.st.Audit(acct.AID, "deploy.filing_ok", map[string]any{
			"issuance_id": iss.ID, "filing_number": filing.FilingNumber, "terms_hash": termsHash,
		})
	}

	// Idempotency: a given issuance minted under a given key and terms mints exactly
	// once, whatever the network did to the first response. The issuance id MUST be in
	// the key: without it, two distinct drafts by one account with equal terms (the
	// default case, both terms {}) would collide, so deploying the second returns the
	// first's asset and silently strands the second at draft forever.
	idem := sha256Hex([]byte(acct.XOnly + "\x00" + iss.ID + "\x00" + termsHash))
	if prev, err := s.st.DeployByIdem(idem); err != nil {
		writeErr(w, 500, "store error")
		return
	} else if prev != nil {
		s.st.Audit(acct.AID, "deploy.replay", map[string]any{"issuance_id": prev.IssuanceID, "asset": prev.AssetID})
		writeJSON(w, 200, deployResponse(prev))
		return
	}

	// M5: an unpaid SeqPal platform setup fee blocks the deploy. The fee is
	// invoiced lazily here (idempotent) and payable by the issuer's choice of rail
	// via POST /issuances/{id}/fees/pay; a zero configured fee auto-marks paid.
	if paid, ferr := s.setupFeePaid(iss.ID); ferr != nil {
		writeErr(w, 500, "store error")
		return
	} else if !paid {
		refuse(402, "the SeqPal platform setup fee is unpaid; pay it at POST /issuances/"+iss.ID+"/fees/pay before deploying")
		return
	}

	// The token check precedes the rate limit so a platform misconfiguration does
	// not silently spend the caller's deploy budget.
	if s.cfg.issuerToken == "" {
		refuse(503, "the deployment backend is not configured with an issuer token")
		return
	}
	if allowed, reason := s.rl.allow(acct.AID); !allowed {
		refuse(429, reason)
		return
	}

	clawback := true
	if req.Clawback != nil {
		clawback = *req.Clawback
	}
	s.st.Audit(acct.AID, "deploy.attempt", map[string]any{
		"issuance_id": iss.ID, "ticker": iss.Ticker, "supply": req.Supply,
		"precision": req.Precision, "confidential": req.Confidential,
		"clawback": clawback, "terms_hash": termsHash, "idem_key": idem,
		"issuer_external": issuerExternal,
	})

	// Ticker collision against the live assets. The residual race inside openampd
	// (two mints of the same ticker in flight) is disclosed, not closed.
	if taken, err := s.tickerTaken(iss.Ticker); err != nil {
		refuse(502, "could not check the ticker against the live assets: "+err.Error())
		return
	} else if taken {
		refuse(409, "ticker "+iss.Ticker+" is already used by a live asset")
		return
	}

	// 1. Register the issuer's enclave key as an OpenAMP account. The AID is
	//    recomputed locally and asserted: a policy server answering with a
	//    different AID would silently mint into an account we do not control.
	//    This is the issuer of record (clawback authority), not the holder.
	aid, err := s.registerUser(acct.XOnly)
	if err != nil {
		refuse(502, "register account with the policy server: "+err.Error())
		return
	}
	if aid != acct.AID {
		refuse(502, "the policy server returned an unexpected account id for this key")
		return
	}

	// 2. Create the per-offering escrow/distribution enclave: a fresh registered
	//    openampd user whose key seqpald custodies. The mint lands HERE, not in
	//    the issuer's personal AID, so primary distribution flows from a scoped
	//    escrow. It is idempotent per issuance, so a retry reuses the same escrow.
	escrow, err := s.createEnclave(enclaveOfferingEscrow, iss.ID)
	if err != nil {
		refuse(502, "provision the offering escrow enclave: "+err.Error())
		return
	}

	// 3. Compile the Step 5 matrix into openampd rules. The escrow AID and (if the
	//    offering is entity-backed) the entity treasury AID are the primary AIDs:
	//    lock-in and Reg S category denies bind only NON-primary senders, so
	//    escrow-to-investor delivery works during a lockup while allowed_categories
	//    still applies to every recipient.
	compiled, err := s.compileForIssuance(iss, canonical)
	if err != nil {
		refuse(400, "the jurisdiction matrix could not be compiled: "+err.Error())
		return
	}
	primary := []string{escrow.AID}
	if iss.EntityID != "" {
		if k, _ := s.st.EnclaveKeyByRef(enclaveEntityTreasury, iss.EntityID); k != nil {
			primary = append(primary, k.AID)
		}
	}
	compiled.PrimaryAIDs = primary

	// fee_convert_atoms comes from the price server, not a hardcoded constant: it
	// is the count of THIS asset's atoms that carries the network fee's value,
	// derived from the canonical (never inverted) any-asset fee relation. The
	// issuer may override it explicitly; otherwise it is derived from the
	// offering price and /prices, with a safe fallback when a price is missing.
	feeConvert := req.FeeConvertAtom
	if feeConvert == 0 {
		offer, _, _ := offeringPrice(canonical)
		if fc, ok := s.feeConvertAtoms(iss.Ticker, offer); ok {
			feeConvert = fc
		} else {
			feeConvert = defaultFeeConvertAtoms
		}
	}
	compiled.FeeConvertAtoms = feeConvert

	// 4. Mint the restricted asset into the escrow enclave (holder = escrow AID).
	//    The issuer AID remains the issuer of record for clawback authority.
	issueBody := map[string]any{
		"name":         iss.Name,
		"ticker":       iss.Ticker,
		"precision":    req.Precision,
		"atoms":        atoms,
		"holder_aid":   escrow.AID,
		"issuer_aid":   aid,
		"clawback":     clawback,
		"burn_allowed": false,
		"confidential": req.Confidential,
		"terms_hash":   termsHash,
		"rules":        compiled,
	}
	// M9: pass the external issuer key so openampd uses it as the enclave issuer
	// half (the L_claw leaf becomes (policy, this key)) and marks the asset
	// external-issuer. Omitted when empty, keeping a legacy deploy byte-identical.
	if issuerExternal {
		issueBody["issuer_pubkey"] = issuerPubkey
	}
	// Entity/operator identity (OA-1) so the asset can be published to the
	// Sequentia asset registry, which requires entity.domain committed on-chain
	// at issue time. The contract_hash commits to whatever is added here, so the
	// registry can verify the exact bytes. Defaults keep the contract to a
	// domain-only shape the current box registry accepts; the operator identity
	// fields are sent only when configured.
	if s.cfg.entityDomain != "" {
		issueBody["entity_domain"] = s.cfg.entityDomain
		if s.cfg.entityName != "" {
			issueBody["entity_name"] = s.cfg.entityName
		}
		if s.cfg.operatorName != "" {
			issueBody["operator_name"] = s.cfg.operatorName
		}
		if s.cfg.operatorRegistration != "" {
			issueBody["operator_registration"] = s.cfg.operatorRegistration
		}
	}
	var issued struct {
		Asset        string          `json:"asset"`
		Txid         string          `json:"txid"`
		ContractHash string          `json:"contract_hash"`
		Contract     json.RawMessage `json:"contract"`
	}
	if err := s.callOpenAMP("POST", "/v1/issuer/assets", s.cfg.issuerToken, issueBody, &issued); err != nil {
		refuse(502, "issue: "+err.Error())
		return
	}

	// 5. Best-effort: fetch the escrow enclave's receive address for display (the
	//    tokens live there). The response `aid` stays the issuer of record.
	var addr struct {
		Address string `json:"address"`
	}
	_ = s.callOpenAMP("GET", "/v1/users/"+escrow.AID+"/address?asset="+issued.Asset, "", nil, &addr)

	rec := &DeployRecord{
		IdemKey:      idem,
		IssuanceID:   iss.ID,
		AssetID:      issued.Asset,
		Txid:         issued.Txid,
		ContractHash: issued.ContractHash,
		AID:          aid,
		Address:      addr.Address,
		CreatedAt:    time.Now().Unix(),
	}
	if err := s.st.InsertDeploy(rec); err != nil {
		// The asset exists on chain; losing the record would let a retry mint a
		// second one, so the failure is reported rather than swallowed.
		s.st.Audit(acct.AID, "deploy.record_failed", map[string]any{
			"issuance_id": iss.ID, "asset": issued.Asset, "txid": issued.Txid, "error": err.Error(),
		})
		writeErr(w, 500, "the asset was minted (%s) but the record could not be stored; do not retry, contact support", issued.Asset)
		return
	}
	if err := s.st.UpdateIssuanceFields(iss.ID, map[string]any{
		"status":          "live",
		"terms":           string(canonical),
		"supply":          req.Supply,
		"precision":       req.Precision,
		"confidential":    boolInt(req.Confidential),
		"clawback":        boolInt(clawback),
		"asset_id":        issued.Asset,
		"txid":            issued.Txid,
		"contract_hash":   issued.ContractHash,
		"holder_aid":      escrow.AID,
		"enclave_address": addr.Address,
		"issuer_external": boolInt(issuerExternal),
		"issuer_pubkey":   issuerPubkey,
	}); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	s.st.Audit(acct.AID, "deploy.success", map[string]any{
		"issuance_id": iss.ID, "asset": issued.Asset, "txid": issued.Txid,
		"contract_hash": issued.ContractHash, "atoms": atoms, "idem_key": idem,
		"escrow_aid": escrow.AID, "primary_aids": primary,
	})

	// Hand the freshly minted asset to the chain watcher: it records a watch row
	// (with the canonical contract for registry publication), emits the initial
	// "broadcast" event, and drives the price seed. This never fails the deploy.
	iss.AssetID = issued.Asset
	iss.Txid = issued.Txid
	s.onDeployed(iss, string(issued.Contract))
	resp := deployResponse(rec)
	resp["escrow_aid"] = escrow.AID
	resp["holder_aid"] = escrow.AID
	resp["rules"] = compiled
	if issuerExternal {
		// So the SPA can render the two-phase clawback posture (the issuer's own key
		// co-signs seizures; the platform cannot move a holder's position alone).
		resp["issuer_external"] = true
		resp["issuer_pubkey"] = issuerPubkey
	}
	writeJSON(w, 200, resp)
}

func deployResponse(d *DeployRecord) map[string]any {
	return map[string]any{
		"asset":         d.AssetID,
		"txid":          d.Txid,
		"contract_hash": d.ContractHash,
		"aid":           d.AID,
		"address":       d.Address,
		"issuance_id":   d.IssuanceID,
	}
}

// tickerTaken reports whether a live asset already carries this ticker.
func (s *server) tickerTaken(ticker string) (bool, error) {
	var out struct {
		Assets []struct {
			Ticker string `json:"ticker"`
		} `json:"assets"`
	}
	if err := s.callOpenAMP("GET", "/v1/assets", "", nil, &out); err != nil {
		return false, err
	}
	for _, a := range out.Assets {
		if strings.EqualFold(a.Ticker, ticker) {
			return true, nil
		}
	}
	return false, nil
}

func (s *server) registerUser(xonly string) (string, error) {
	var out struct {
		AID string `json:"aid"`
	}
	if err := s.callOpenAMP("POST", "/v1/users", "", map[string]any{"pubkeys": []string{xonly}}, &out); err != nil {
		return "", err
	}
	if out.AID == "" {
		return "", fmt.Errorf("no account id returned")
	}
	return out.AID, nil
}

// atomsFor scales whole tokens to atoms, reporting false rather than wrapping.
func atomsFor(supply uint64, precision int) (uint64, bool) {
	atoms := supply
	for i := 0; i < precision; i++ {
		if atoms > (1<<64-1)/10 {
			return 0, false
		}
		atoms *= 10
	}
	return atoms, true
}
