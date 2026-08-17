package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// Network-enforced deploys (M12).
//
// A network-enforced asset is not policed by the policy server at all. Its
// holdings live in on-chain programs, and every transfer is checked by the
// network against a policy commitment the issuer publishes. What that means
// concretely for this file:
//
//   - There is NO escrow enclave. There is no enclave at all, so the mint lands
//     directly in the issuer's own holding address rather than in a platform
//     custodied account. Nothing here provisions a key.
//   - There is NO rules compilation. The Step 5 matrix is not turned into a
//     policy-server rule set, because the on-chain programs ARE the rules. What
//     the issuer supplies instead is the list of holder keys allowed to receive
//     the asset.
//   - The platform never signs a transfer of the asset, and never can.
//
// # Why a network deploy takes two attempts
//
// The on-chain programs are compiled by the issuer's registrar, outside this
// platform and outside the policy server: neither is a compiler. Worse, the
// values needed to compile them depend on the asset id, which depends on the
// funding the policy server picks, so they cannot be produced in advance either.
//
// So the first deploy attempt PREPARES: the policy server fixes the asset id and
// the policy commitment, and hands back a document. seqpald records that handoff
// and refuses with it, naming the command to run. The issuer's registrar runs it
// and returns two values, and the second attempt COMPLETES the deploy. The
// handoff is stored per issuance, so a retry resumes rather than preparing (and
// minting a second verifier asset) again.
//
// This file is deliberately the only place that knows the policy server calls
// the election "damp" while SeqPal calls it "network"; the translation happens
// in one place, at the request body.

type dampDeployParams struct {
	precision      int
	atoms          uint64
	supply         uint64
	termsHash      string
	canonicalTerms []byte
	idem           string

	whitelist      []string
	verifierAmount uint64

	userCMR     string
	verifierCMR string
	issuerCMR   string
	pi          string
}

// dampPrepareResponse is what the policy server returns from phase 1.
type dampPrepareResponse struct {
	PrepareID          string          `json:"prepare_id"`
	Asset              string          `json:"asset"`
	Contract           json.RawMessage `json:"contract"`
	ContractHash       string          `json:"contract_hash"`
	VerifierAsset      string          `json:"verifier_asset"`
	VerifierAmount     uint64          `json:"verifier_amount"`
	VerifierIssueTxid  string          `json:"verifier_issue_txid"`
	Pi                 string          `json:"pi"`
	WhitelistRoot      string          `json:"whitelist_root"`
	Snapshot           json.RawMessage `json:"snapshot"`
	SnapshotSigMessage string          `json:"snapshot_sig_message"`
	DeriveSnapshot     json.RawMessage `json:"derive_snapshot"`
}

// dampCompleteResponse is what the policy server returns from phase 2.
type dampCompleteResponse struct {
	Asset         string            `json:"asset"`
	VerifierAsset string            `json:"verifier_asset"`
	Txids         map[string]string `json:"txids"`
	Pi            string            `json:"pi"`
	WhitelistRoot string            `json:"whitelist_root"`
	UserAddress   string            `json:"user_covenant_address"`
	UserSPK       string            `json:"user_covenant_spk"`
	RulesAddress  string            `json:"verifier_covenant_address"`
	RulesSPK      string            `json:"verifier_covenant_spk"`
	Contract      json.RawMessage   `json:"contract"`
	ContractHash  string            `json:"contract_hash"`
}

// dampParamsMissing is the exact refusal an issuer sees when the two registrar
// values are absent. It names the command so the message is actionable rather
// than merely correct.
const dampParamsMissing = "this deployment is prepared but not minted: your registrar must run `opendamp derive` against the document in this response and return its program identities and policy commitment, which you then submit with this deploy again. Nothing has been minted."

func (s *server) deployNetwork(w http.ResponseWriter, acct *Account, iss *Issuance, p dampDeployParams) {
	refuse := func(code int, reason string) {
		s.st.Audit(acct.AID, "deploy.refused", map[string]any{
			"issuance_id": iss.ID, "status": code, "reason": reason, "enforcement": "network",
		})
		writeErr(w, code, "%s", reason)
	}

	// The session key is the initial holder: the mint lands where the issuer can
	// already spend from, with no platform-held key anywhere in the path.
	holder := strings.ToLower(strings.TrimSpace(acct.XOnly))
	if !validXOnly(holder) {
		refuse(500, "this account has no usable holding key")
		return
	}
	whitelist, err := normalizeWhitelist(p.whitelist, holder)
	if err != nil {
		refuse(400, err.Error())
		return
	}
	q := p.verifierAmount
	if q == 0 {
		q = 1
	}

	// Resume or prepare. One handoff row per issuance: a retry must never mint a
	// second verifier asset, and must never shift the asset id out from under a
	// registrar run the issuer has already paid for.
	prep, err := s.st.DampPrepare(iss.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if prep != nil && prep.WhitelistRoot != "" && !sameWhitelist(prep.Whitelist, whitelist) {
		refuse(409, "this deployment was already prepared with a different holder list, and the policy commitment covers that list. Deploy with the prepared list, or start a new issuance.")
		return
	}
	if prep == nil {
		prepared, perr := s.dampPrepare(iss, p, holder, whitelist, q)
		if perr != nil {
			refuse(502, "prepare the network-enforced issuance: "+perr.Error())
			return
		}
		wlJSON, _ := json.Marshal(whitelist)
		prep = &DampPrepare{
			IssuanceID: iss.ID, PrepareID: prepared.PrepareID, AssetID: prepared.Asset,
			VerifierAsset: prepared.VerifierAsset, VerifierAmount: prepared.VerifierAmount,
			PolicyCommitment: prepared.Pi, WhitelistRoot: prepared.WhitelistRoot,
			Whitelist: wlJSON, DeriveDocument: prepared.DeriveSnapshot,
			SnapshotSigMessage: prepared.SnapshotSigMessage, CreatedAt: time.Now().Unix(),
		}
		if err := s.st.PutDampPrepare(prep); err != nil {
			// The policy server has prepared and cannot be un-prepared, so losing the
			// row would strand the preparation. Report rather than swallow.
			s.st.Audit(acct.AID, "deploy.network.prepare_record_failed", map[string]any{
				"issuance_id": iss.ID, "asset": prepared.Asset, "error": err.Error(),
			})
			writeErr(w, 500, "the deployment was prepared (%s) but the record could not be stored; do not retry, contact support", prepared.Asset)
			return
		}
		s.st.Audit(acct.AID, "deploy.network.prepared", map[string]any{
			"issuance_id": iss.ID, "asset": prepared.Asset, "verifier_asset": prepared.VerifierAsset,
			"policy_commitment": prepared.Pi, "whitelist_root": prepared.WhitelistRoot,
			"holders": len(whitelist),
		})
	}

	// Phase 2 needs the two values only the registrar can produce.
	userCMR := strings.ToLower(strings.TrimSpace(p.userCMR))
	verifierCMR := strings.ToLower(strings.TrimSpace(p.verifierCMR))
	issuerCMR := strings.ToLower(strings.TrimSpace(p.issuerCMR))
	pi := strings.ToLower(strings.TrimSpace(p.pi))
	if userCMR == "" || verifierCMR == "" || pi == "" {
		s.st.Audit(acct.AID, "deploy.refused", map[string]any{
			"issuance_id": iss.ID, "status": 409, "reason": dampParamsMissing, "enforcement": "network",
		})
		writeJSON(w, 409, map[string]any{
			"error":                dampParamsMissing,
			"issuance_id":          iss.ID,
			"stage":                "prepared",
			"asset":                prep.AssetID,
			"policy_commitment":    prep.PolicyCommitment,
			"holder_list_root":     prep.WhitelistRoot,
			"holders":              json.RawMessage(prep.Whitelist),
			"registrar_document":   json.RawMessage(prep.DeriveDocument),
			"registrar_command":    "opendamp derive --snapshot <registrar_document>",
			"snapshot_sig_message": prep.SnapshotSigMessage,
		})
		return
	}
	if !validHex32(userCMR) || !validHex32(verifierCMR) || (issuerCMR != "" && !validHex32(issuerCMR)) {
		refuse(400, "the registrar's program identities must each be 32 bytes in hex")
		return
	}
	if !validHex32(pi) {
		refuse(400, "the policy commitment must be 32 bytes in hex")
		return
	}
	// The commitment the registrar compiled against must be the one this
	// deployment is prepared with. The policy server checks this too and is the
	// authority; checking here makes the refusal legible and saves a round trip.
	if pi != prep.PolicyCommitment {
		refuse(409, "the policy commitment does not match this deployment: it commits to "+prep.PolicyCommitment+", and your registrar derived against "+pi+". Re-run the derivation against the document this deployment returned. Nothing has been minted.")
		return
	}

	body := map[string]any{
		"user_cmr": userCMR, "verifier_cmr": verifierCMR, "pi": pi,
	}
	if issuerCMR != "" {
		body["issuer_cmr"] = issuerCMR
	}
	var done dampCompleteResponse
	if err := s.callOpenAMP("POST", "/v1/issuer/damp-assets/"+prep.PrepareID+"/complete",
		s.cfg.issuerToken, body, &done); err != nil {
		refuse(502, "mint the network-enforced asset: "+err.Error())
		return
	}
	txid := done.Txids["asset_issue"]

	rec := &DeployRecord{
		IdemKey:      p.idem,
		IssuanceID:   iss.ID,
		AssetID:      done.Asset,
		Txid:         txid,
		ContractHash: done.ContractHash,
		// The issuer of record is this account: for a network-enforced asset it is
		// also the initial holder and the key that may update the policy.
		AID:       acct.AID,
		Address:   done.UserAddress,
		CreatedAt: time.Now().Unix(),
	}
	if err := s.st.InsertDeploy(rec); err != nil {
		s.st.Audit(acct.AID, "deploy.record_failed", map[string]any{
			"issuance_id": iss.ID, "asset": done.Asset, "txid": txid, "error": err.Error(),
		})
		writeErr(w, 500, "the asset was minted (%s) but the record could not be stored; do not retry, contact support", done.Asset)
		return
	}
	if err := s.st.UpdateIssuanceFields(iss.ID, map[string]any{
		"status":        "live",
		"terms":         string(p.canonicalTerms),
		"supply":        p.supply,
		"precision":     p.precision,
		"clawback":      0, // there is no clawback path: the network enforces, the platform cannot seize
		"asset_id":      done.Asset,
		"txid":          txid,
		"contract_hash": done.ContractHash,
		"holder_aid":    acct.AID,
		// enclave_address stays the holder-facing receive address, so every screen
		// that already renders it keeps working; for this election it is the
		// on-chain holding address rather than an enclave.
		"enclave_address":           done.UserAddress,
		"issuer_external":           1,
		"issuer_pubkey":             holder,
		"enforcement":               "network",
		"verifier_asset":            done.VerifierAsset,
		"verifier_amount":           prep.VerifierAmount,
		"policy_commitment":         done.Pi,
		"whitelist_root":            done.WhitelistRoot,
		"holder_covenant_address":   done.UserAddress,
		"verifier_covenant_address": done.RulesAddress,
	}); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	// The handoff has served its purpose; dropping it makes a later retry hit the
	// deploy idempotency record instead of preparing again.
	_ = s.st.DeleteDampPrepare(iss.ID)

	s.st.Audit(acct.AID, "deploy.success", map[string]any{
		"issuance_id": iss.ID, "asset": done.Asset, "txid": txid,
		"contract_hash": done.ContractHash, "atoms": p.atoms, "idem_key": p.idem,
		"enforcement": "network", "verifier_asset": done.VerifierAsset,
		"policy_commitment": done.Pi, "whitelist_root": done.WhitelistRoot,
		"holder_address": done.UserAddress, "rules_address": done.RulesAddress,
		"verifier_issue_txid": done.Txids["verifier_issue"],
	})

	// Watcher handoff, exactly as the other elections do: the canonical contract
	// rides along so the asset registry publication has the committed bytes.
	iss.AssetID = done.Asset
	iss.Txid = txid
	s.onDeployed(iss, string(done.Contract))

	resp := deployResponse(rec)
	resp["enforcement"] = "network"
	resp["issuer_external"] = true
	resp["issuer_pubkey"] = holder
	resp["holder_pubkey"] = holder
	resp["verifier_asset"] = done.VerifierAsset
	resp["verifier_amount"] = prep.VerifierAmount
	resp["policy_commitment"] = done.Pi
	resp["holder_list_root"] = done.WhitelistRoot
	resp["holders"] = json.RawMessage(prep.Whitelist)
	resp["holder_covenant_address"] = done.UserAddress
	resp["verifier_covenant_address"] = done.RulesAddress
	resp["txids"] = done.Txids
	writeJSON(w, 200, resp)
}

// dampPrepare runs phase 1 against the policy server. It translates SeqPal's
// election name ("network") into the policy server's ("damp") and passes the
// issuer identity fields exactly as a serviced deploy does, so the asset
// registers through the same path.
func (s *server) dampPrepare(iss *Issuance, p dampDeployParams, holder string, whitelist []string, q uint64) (*dampPrepareResponse, error) {
	body := map[string]any{
		"name":       iss.Name,
		"ticker":     iss.Ticker,
		"precision":  p.precision,
		"atoms":      p.atoms,
		"terms_hash": p.termsHash,
		// The initial holder, the list of keys allowed to receive, and the key that
		// may later update the policy or halt the asset. All three are the issuer's
		// own key material; none of it is held here.
		"holder_pubkey":     holder,
		"whitelist":         whitelist,
		"issuer_update_key": holder,
		"verifier_amount":   q,
		"burn_allowed":      false,
		// The chain the registrar should encode addresses for. This is NOT
		// cfg.network (a UI label like "sequentia-testnet"): the derivation tool
		// takes "testnet" or "regtest", and nothing in the commitment depends on
		// it, so a wrong value would only produce addresses in the wrong encoding.
		"network": dampDeriveNetwork(s.cfg.network),
	}
	if s.cfg.entityDomain != "" {
		body["entity_domain"] = s.cfg.entityDomain
		if s.cfg.entityName != "" {
			body["entity_name"] = s.cfg.entityName
		}
		if s.cfg.operatorName != "" {
			body["operator_name"] = s.cfg.operatorName
		}
		if s.cfg.operatorRegistration != "" {
			body["operator_registration"] = s.cfg.operatorRegistration
		}
	}
	var out dampPrepareResponse
	if err := s.callOpenAMP("POST", "/v1/issuer/damp-assets", s.cfg.issuerToken, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// normalizeWhitelist lowercases, validates and de-duplicates the holder list,
// and guarantees it contains the initial holder. An empty list defaults to
// exactly the initial holder, which is the sensible starting policy: the issuer
// can receive, nobody else can yet.
func normalizeWhitelist(list []string, holder string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(list)+1)
	for _, k := range list {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "" {
			continue
		}
		if !validXOnly(k) {
			return nil, errors.New("every holder in the list must be a valid 32-byte x-only public key in hex; " + k + " is not")
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	if !seen[holder] {
		// The initial holder must be able to receive, including their own change:
		// the network checks the recipient of every regulated output.
		out = append(out, holder)
	}
	return out, nil
}

// sameWhitelist compares a stored JSON list against a normalized one.
func sameWhitelist(stored json.RawMessage, want []string) bool {
	var have []string
	if err := json.Unmarshal(stored, &have); err != nil {
		return false
	}
	if len(have) != len(want) {
		return false
	}
	for i := range have {
		if have[i] != want[i] {
			return false
		}
	}
	return true
}

// dampDeriveNetwork maps SeqPal's network label onto the chain names the
// derivation tool understands. Address encoding only.
func dampDeriveNetwork(label string) string {
	if strings.Contains(strings.ToLower(label), "regtest") {
		return "regtest"
	}
	return "testnet"
}

// validHex32 reports whether s is exactly 32 bytes of lowercase hex.
func validHex32(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
