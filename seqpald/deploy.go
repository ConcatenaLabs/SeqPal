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
//
// Confidentiality is not an asset property: every deploy is a transparent mint,
// and any holder may later move the asset confidentially per transfer (see
// handleP2PInitiate). An incoming legacy "confidential" field is ignored for
// backward compatibility rather than refused; readJSON drops unknown fields.
type deployReq struct {
	IssuanceID string `json:"issuance_id"`
	Supply     uint64 `json:"supply"`
	// Pointer so an OMITTED precision (nil) is distinct from an explicit 0. Precision 0
	// is a valid integer-only asset; a plain int would let an omitted field masquerade
	// as 0 (or, as before, forced 1..8 to reject 0 entirely and make 0dp assets unissuable).
	Precision      *int            `json:"precision"`
	Clawback       *bool           `json:"clawback"`
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

	// Enforcement (M10) is the issuer's election: "serviced" (the co-signed
	// OpenAMP path; the default, also selected by an empty string), "network"
	// (OpenDAMP; refused 501 unless SEQPALD_DAMP is enabled, the election is
	// still recorded), or "bearer" (a consensus-supervised asset issued through
	// the node's raw path, no openampd involvement).
	Enforcement string `json:"enforcement"`
	// RecoveryPubkey (bearer only) is the supervision recovery key: a 64-hex
	// x-only key DISTINCT from the session key, whose only power is rotating the
	// supervision keys. Required for enforcement=bearer.
	RecoveryPubkey string `json:"recovery_pubkey"`
	// Pause (bearer only) elects the asset-wide pause capability. Permanent
	// either way: it is committed in the asset id.
	Pause bool `json:"pause"`

	// --- network enforcement only (M12) ---
	//
	// Whitelist is the set of x-only holder keys permitted to RECEIVE the asset
	// under the genesis policy. It must contain this account's own key, which is
	// the initial holder; omitted, it defaults to exactly that one key.
	Whitelist []string `json:"whitelist"`
	// VerifierAmount is the fixed amount of the verifier asset a valid rules
	// output carries. Defaults to 1.
	VerifierAmount uint64 `json:"verifier_amount"`
	// HolderKey names where the mint lands: an x-only key this account can sign
	// with. An account with an OpenAMP account has one already and may leave this
	// empty; a SeqPal ID that is only a wallet must name one of its own, which
	// the server proves derives from a wallet the account has linked.
	HolderKey string `json:"holder_key"`
	// The two values the issuer's registrar produces for this policy, and which
	// nothing in this platform can compute: the compiled program identities and
	// the policy commitment they were compiled against. Absent on the first
	// attempt (which prepares and refuses with the document to run); required on
	// the second (which completes the deploy).
	UserCMR     string `json:"user_cmr"`
	VerifierCMR string `json:"verifier_cmr"`
	IssuerCMR   string `json:"issuer_cmr"`
	Pi          string `json:"pi"`
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
	if req.Precision == nil {
		refuse(400, "precision is required")
		return
	}
	precision := *req.Precision
	if precision < 0 || precision > 8 {
		refuse(400, "precision must be between 0 and 8")
		return
	}
	if err := validateTicker(iss.Ticker); err != nil {
		refuse(400, err.Error())
		return
	}
	// atoms = supply * 10^precision, guarded against uint64 overflow.
	atoms, ok := atomsFor(req.Supply, precision)
	if !ok {
		refuse(400, "supply is too large for the chosen precision")
		return
	}
	// M10 enforcement election. The election is persisted on the (draft)
	// issuance row even when the deploy is then refused, so the record shows
	// what the issuer chose.
	enforcement := strings.ToLower(strings.TrimSpace(req.Enforcement))
	if enforcement == "" {
		enforcement = "serviced"
	}
	switch enforcement {
	case "serviced", "network", "bearer":
	default:
		refuse(400, `enforcement must be "serviced", "network", or "bearer"`)
		return
	}
	if iss.Status != "live" {
		_ = s.st.UpdateIssuanceFields(iss.ID, map[string]any{"enforcement": enforcement})
	}
	// The in-memory issuance carries the election too, so the document
	// generation below selects the bearer charter set even when the supplied
	// terms object does not name the election itself.
	iss.Enforcement = enforcement
	// A serviced issuance mints into an OpenAMP enclave, which a wallet-backed
	// account does not have. The other two elections do not need one: a bearer
	// asset is an ordinary on-chain holding and a network-enforced one is
	// governed by the chain's own rules, so both stay open to a plain wallet.
	if enforcement == "serviced" && !s.hasEnclave(acct) {
		refuse(403, "a policy-co-signed (serviced) issuance mints into an OpenAMP enclave, and this "+
			"SeqPal ID is a wallet with no OpenAMP account attached. Attach one, or issue this as "+
			"network-enforced, which does not need one")
		return
	}
	// A freely-tradable token is supervised by an OPERATIONAL key, and the asset
	// id commits to it at issuance: it is the key that will sign a court-ordered
	// freeze, as a BIP340 signature over the message the node computes. An
	// ordinary wallet cannot make one -- it signs classic messages -- so an ID
	// with no OpenAMP account has no key to put there. Refused here, because the
	// alternative is an unreadable failure from the node about a malformed
	// operational key, after the terms are already written.
	if enforcement == "bearer" && !s.hasEnclave(acct) {
		refuse(403, "a freely-tradable token is supervised by a key that signs its freezes, and "+
			"this SeqPal ID is a wallet with no OpenAMP account attached to supply one. Attach "+
			"one, or issue this as network-enforced, whose rules the chain enforces without a "+
			"supervision key")
		return
	}
	if enforcement == "network" && !s.cfg.damp {
		refuse(501, "network enforcement is not available on this deployment")
		return
	}
	var recoveryKey string
	if enforcement == "bearer" {
		recoveryKey = strings.ToLower(strings.TrimSpace(req.RecoveryPubkey))
		if !validXOnly(recoveryKey) {
			refuse(400, "bearer enforcement requires recovery_pubkey: a valid 32-byte x-only public key in hex")
			return
		}
		if strings.EqualFold(recoveryKey, acct.XOnly) {
			refuse(400, "recovery_pubkey must be distinct from the session key: the recovery key is the cold key that survives an operational-key compromise")
			return
		}
		if s.cfg.nodeURL == "" {
			refuse(503, "bearer issuance requires a Sequentia node RPC (SEQPALD_NODE_URL)")
			return
		}
	}

	// The enclave issuer half is ALWAYS the issuing entity's own key: this account's
	// own x-only, whose private half stays in the browser. So clawback runs two-phase
	// (the issuer signs the L_claw sweep) and the platform never holds a key that can
	// move a holder's position. This is the only path. If the SPA supplies the key it
	// must equal this account's; otherwise it defaults to it (this account is the
	// issuer of record and the browser key that later signs the sweep).
	issuerPubkey := strings.TrimSpace(req.IssuerPubkey)
	if issuerPubkey != "" && !strings.EqualFold(issuerPubkey, acct.XOnly) {
		refuse(400, "issuer_pubkey must be this account's own key (the issuer of record); the browser signs clawbacks with it")
		return
	}
	issuerPubkey = acct.XOnly
	issuerExternal := true
	iss.IssuerExternal = issuerExternal

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
	// The sanctions floor, read from the terms being DEPLOYED rather than the
	// draft's -- a deploy carries its own matrix, and a draft's is usually empty.
	// Refused rather than quietly narrowed: the terms hash commits to the matrix
	// as submitted, so compiling something else would leave the published terms
	// and the live rules saying different things.
	if blocked := admittedFloorJurisdictions(rawOrEmpty(terms)); len(blocked) > 0 {
		refuse(422, "this offering admits "+strings.Join(blocked, ", ")+", which the "+
			"OFAC- and FATF-aligned sanctions floor does not allow any offering to admit. "+
			"Remove them from the jurisdiction matrix and deploy again; nothing was minted")
		return
	}
	// Fail closed on the structure (W-7): an unrecognized name refuses here
	// instead of silently characterizing as equity. An EMPTY structure remains
	// the documented equity default.
	if _, cerr := characterize(structureName(iss, rawOrEmpty(terms))); cerr != nil {
		refuse(400, "unrecognized structure: "+cerr.Error())
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
	// The cross-check proves the client hashed the terms it sent: it is a body
	// integrity check, not a prediction of the server's document binding. Two
	// client shapes are both correct, so both are accepted: one hashes the terms
	// as sent (it cannot know the manifest the server is about to bind), and one
	// that already called the documents endpoint hashes the document-bound terms
	// and reproduces the server's hash exactly. Comparing ONLY against the bound
	// hash rejects every deploy from a client that did not pre-fetch its own
	// manifest, which is the ordinary wizard path.
	if req.TermsHash != "" && !strings.EqualFold(req.TermsHash, termsHash) {
		var sentObj any
		sentOK := false
		if err := json.Unmarshal(rawOrEmpty(terms), &sentObj); err == nil {
			if sentCanonical, cerr := canonicalJSON(sentObj); cerr == nil {
				sentOK = strings.EqualFold(req.TermsHash, sha256Hex(sentCanonical))
			}
		}
		if !sentOK {
			refuse(400, "terms_hash mismatch: the terms the browser hashed are not the terms it sent")
			return
		}
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

	// Bearer gate: a stored, unexpired bearer attestation by this account must
	// exist BEFORE the deploy (POST /api/issuances/{id}/bearer-attestation).
	if enforcement == "bearer" {
		att, aerr := s.st.BearerAttestation(iss.ID)
		if aerr != nil {
			writeErr(w, 500, "store error")
			return
		}
		if att == nil {
			refuse(403, "bearer enforcement requires a stored bearer attestation; sign one at POST /api/issuances/"+iss.ID+"/bearer-attestation first")
			return
		}
		if att.AID != acct.AID {
			refuse(403, "the stored bearer attestation was signed by a different account")
			return
		}
		if att.ValidUntil <= time.Now().Unix() {
			refuse(403, "the bearer attestation has expired (it is valid for one year); refresh it at POST /api/issuances/"+iss.ID+"/bearer-attestation")
			return
		}
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
	// not silently spend the caller's deploy budget. A bearer deploy never talks
	// to the openampd issuer endpoint, so it needs no token.
	if enforcement != "bearer" && s.cfg.issuerToken == "" {
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
		"precision": precision,
		"clawback":  clawback, "terms_hash": termsHash, "idem_key": idem,
		"issuer_external": issuerExternal, "enforcement": enforcement,
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

	// Bearer path: a consensus-supervised asset minted through the node's raw
	// issuance flow. No openampd involvement, no rules compilation, no escrow
	// enclave: the mint lands in the seqpal-escrow node wallet for primary sales.
	if enforcement == "bearer" {
		s.deployBearer(w, acct, iss, bearerDeployParams{
			precision: precision, atoms: atoms, supply: req.Supply,
			termsHash: termsHash, canonicalTerms: canonical,
			recoveryKey: recoveryKey, pause: req.Pause, idem: idem,
		})
		return
	}

	// Network path: the rules are compiled into on-chain programs and enforced by
	// the network, so there is no escrow enclave to provision (there is no
	// enclave) and no rule set to compile for the policy server (the programs are
	// the rules). The mint lands directly in the issuer's own holding address.
	if enforcement == "network" {
		s.deployNetwork(w, acct, iss, dampDeployParams{
			precision: precision, atoms: atoms, supply: req.Supply,
			termsHash: termsHash, canonicalTerms: canonical, idem: idem,
			whitelist: req.Whitelist, verifierAmount: req.VerifierAmount,
			holderKey: req.HolderKey,
			userCMR:   req.UserCMR, verifierCMR: req.VerifierCMR, issuerCMR: req.IssuerCMR,
			pi: req.Pi,
		})
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
		"precision":    precision,
		"atoms":        atoms,
		"holder_aid":   escrow.AID,
		"issuer_aid":   aid,
		"clawback":     clawback,
		"burn_allowed": false,
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
		"precision":       precision,
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
