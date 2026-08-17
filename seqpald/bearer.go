package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// Bearer enforcement (M10): the asset is issued as a Sequentia SUPERVISED
// asset through the node's raw issuance flow, with no openampd involvement.
// Enforcement is consensus itself: the supervision descriptor {operational key
// = the issuer's session key, recovery key, pause bit} is committed as a third
// merkle leaf in the asset entropy, so it is part of the asset id and can never
// be added, removed, or altered afterwards. Freezes bind single-owner scripts
// only, are signed by the operational key in the issuer's browser (a RAW
// 32-byte node-produced message, see supervision.go), and are submitted over
// the producer's private channel so they cannot be front-run.

// bearerAttestationTag is the BIP340 tag for the annually-refreshable bearer
// attestation. TAGGED signer, never raw: the browser signs
// taggedHash(bearerAttestationTag, sha256(canonicalJSON(statement))).
const bearerAttestationTag = "seqpal-bearer-attestation-v1"

// bearerAttestationValidity: an attestation is refreshable annually.
const bearerAttestationValidity = 365 * 24 * time.Hour

// SUPERVISION_FEATURE_PAUSE, as consensus defines it (bit 1).
const supervisionFeaturePause = 1 << 1

// bearerAttestationStatement builds the canonical statement the session key
// signs: sha256 of these bytes is the tagged-signature message.
func bearerAttestationStatement(issuanceID, aid string) []byte {
	stmt, _ := canonicalJSON(map[string]any{
		"issuance_id":   issuanceID,
		"no_us_nexus":   true,
		"risk_accepted": true,
		"aid":           aid,
	})
	return stmt
}

// handleBearerAttestation is POST /api/issuances/{id}/bearer-attestation
// (session, owner). Two-phase like the mandate flows: with no statement_sig it
// returns the exact canonical bytes to hash and sign; with one it verifies
// (BIP340 by the session key under bearerAttestationTag over sha256 of the
// canonical statement), persists, and audit-logs. Valid for one year and
// refreshable by re-POSTing.
func (s *server) handleBearerAttestation(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	var req struct {
		NoUSNexus    bool   `json:"no_us_nexus"`
		RiskAccepted bool   `json:"risk_accepted"`
		StatementSig string `json:"statement_sig"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	refuse := func(code int, reason string) {
		s.st.Audit(acct.AID, "bearer.attest.refused", map[string]any{
			"issuance_id": iss.ID, "status": code, "reason": reason,
		})
		writeErr(w, code, "%s", reason)
	}
	if !req.NoUSNexus || !req.RiskAccepted {
		refuse(400, "both no_us_nexus and risk_accepted must be affirmed true")
		return
	}
	stmt := bearerAttestationStatement(iss.ID, acct.AID)
	digest := sha256.Sum256(stmt)
	sig := strings.TrimSpace(req.StatementSig)
	if sig == "" {
		writeJSON(w, 200, map[string]any{
			"sign_this": string(stmt), "tag": bearerAttestationTag,
			"note": "sign sha256 of these canonical bytes with your SeqPal ID key under the tag, then resubmit as statement_sig",
		})
		return
	}
	if err := verifyTaggedByKey(acct.XOnly, bearerAttestationTag, digest[:], sig); err != nil {
		refuse(400, "the attestation signature does not verify for your key")
		return
	}
	att := &BearerAttestation{
		IssuanceID: iss.ID, AID: acct.AID, NoUSNexus: true, RiskAccepted: true,
		Statement: string(stmt), Sig: sig,
		ValidUntil: time.Now().Add(bearerAttestationValidity).Unix(),
	}
	if err := s.st.UpsertBearerAttestation(att); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	s.st.Audit(acct.AID, "bearer.attest", map[string]any{
		"issuance_id": iss.ID, "valid_until": att.ValidUntil,
	})
	writeJSON(w, 200, map[string]any{"attestation": att})
}

// --- bearer deploy -----------------------------------------------------------

type bearerDeployParams struct {
	precision      int
	atoms          uint64
	supply         uint64
	termsHash      string
	canonicalTerms []byte
	recoveryKey    string
	pause          bool
	idem           string
}

// bearerContract assembles the canonical registry contract for a bearer asset:
// openampd's contract shape minus the openamp block, with a "supervision" block
// {operational_key, recovery_key, feature_bits} in its place and the terms_hash
// committed at top level so the on-chain contract still binds the compliance
// terms (the standing SeqPal invariant). contract_hash = sha256 of the
// canonical bytes; the value handed to the node is that digest in display
// (reversed) byte order, exactly as openampd/the registry exchange it.
func (s *server) bearerContract(iss *Issuance, precision int, operationalKey, recoveryKey string, pause bool, termsHash string) (canonical []byte, contractHashDisplay string, err error) {
	featureBits := 0
	if pause {
		featureBits = supervisionFeaturePause
	}
	contract := map[string]any{
		"name":          iss.Name,
		"ticker":        iss.Ticker,
		"precision":     precision,
		"version":       0,
		"issuer_pubkey": operationalKey,
		"terms_hash":    termsHash,
		"supervision": map[string]any{
			"operational_key": operationalKey,
			"recovery_key":    recoveryKey,
			"feature_bits":    featureBits,
		},
	}
	if s.cfg.entityDomain != "" {
		entity := map[string]any{"domain": s.cfg.entityDomain}
		if s.cfg.entityName != "" {
			entity["issuer"] = s.cfg.entityName
		}
		contract["entity"] = entity
	}
	canonical, err = canonicalJSON(contract)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(canonical)
	return canonical, hexReverse(hex.EncodeToString(digest[:])), nil
}

// hexReverse reverses the byte order of a hex string (natural <-> display order
// of a uint256). The node's hash-valued RPC parameters take display order.
func hexReverse(h string) string {
	b, err := hex.DecodeString(h)
	if err != nil {
		return h
	}
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return hex.EncodeToString(b)
}

// deployBearer mints the supervised asset through the raw node flow:
//
//	createrawtransaction (a data placeholder output only)
//	-> fundrawtransaction against seqpal-escrow (fixes vin[0], whose prevout the
//	   asset id commits to; inputs locked against concurrent deploys)
//	-> getsupervisedassetid (derive + cross-check the id the issuance must create)
//	-> rawissueasset with the supervision object (the raw path inserts the
//	   SEQSUP declaration output itself; verified, with addsupervisionrecordoutput
//	   as the fallback)
//	-> signrawtransactionwithwallet -> testmempoolaccept
//	-> persist the deploy record (the idem anchor) BEFORE broadcast
//	-> sendrawtransaction.
//
// blindrawtransaction is deliberately never called: consensus requires a
// supervised issuance to be ALL-EXPLICIT so its outputs are freezable.
func (s *server) deployBearer(w http.ResponseWriter, acct *Account, iss *Issuance, p bearerDeployParams) {
	refuse := func(code int, reason string) {
		s.st.Audit(acct.AID, "deploy.refused", map[string]any{
			"issuance_id": iss.ID, "status": code, "reason": reason, "enforcement": "bearer",
		})
		writeErr(w, code, "%s", reason)
	}
	operationalKey := acct.XOnly

	canonicalContract, contractHash, err := s.bearerContract(iss, p.precision, operationalKey, p.recoveryKey, p.pause, p.termsHash)
	if err != nil {
		refuse(500, "the contract could not be canonicalized: "+err.Error())
		return
	}

	if err := s.ensureSeqEscrowWallet(); err != nil {
		refuse(502, "provision the escrow wallet: "+err.Error())
		return
	}
	assetAddr, err := s.escrowNewAddress("seqpal-bearer-mint")
	if err != nil {
		refuse(502, "derive the mint address: "+err.Error())
		return
	}
	tokenAddr, err := s.escrowNewAddress("seqpal-bearer-treasury")
	if err != nil {
		refuse(502, "derive the treasury (reissuance token) address: "+err.Error())
		return
	}

	// Empty tx (a data placeholder satisfies fundrawtransaction's one-output
	// minimum), then funding: the wallet picks and LOCKS the inputs, adds change
	// and the fee output (which rawissueasset requires to be last).
	rawRes, err := s.walletRPC(seqEscrowWallet, "createrawtransaction", []any{}, []map[string]any{{"data": "00"}})
	if err != nil {
		refuse(502, "createrawtransaction: "+err.Error())
		return
	}
	var rawHex string
	if err := json.Unmarshal(rawRes, &rawHex); err != nil {
		refuse(502, "createrawtransaction returned no transaction")
		return
	}
	feeAsset, err := s.bearerFeeAsset()
	if err != nil {
		refuse(502, "fee asset: "+err.Error())
		return
	}
	fundRes, err := s.walletRPC(seqEscrowWallet, "fundrawtransaction", rawHex, map[string]any{
		"lockUnspents": true, "fee_asset": feeAsset,
		// The placeholder tx funded here roughly triples in size once
		// rawissueasset adds the mint, token, and declaration outputs, and the
		// fee is fixed at funding time. Fund at a generous rate so the final
		// transaction still clears the relay floor; on this chain fees are
		// cheap and the preflight gate refuses anything underfunded.
		"fee_rate": 10,
	})
	if err != nil {
		refuse(502, "fundrawtransaction: "+err.Error())
		return
	}
	var funded struct {
		Hex string `json:"hex"`
	}
	if err := json.Unmarshal(fundRes, &funded); err != nil || funded.Hex == "" {
		refuse(502, "fundrawtransaction returned no transaction")
		return
	}
	dec, err := s.decodeRawTransaction(funded.Hex)
	if err != nil || len(dec.Vin) == 0 {
		refuse(502, "the funded transaction could not be decoded")
		return
	}
	prevTxid, prevVout := dec.Vin[0].Txid, dec.Vin[0].Vout

	// Derive the supervised asset id from vin[0] + the descriptor, so the mint
	// below can be cross-checked against an independent derivation.
	supRes, err := s.nodeRPC("getsupervisedassetid", prevTxid, prevVout, operationalKey, p.recoveryKey, contractHash, p.pause)
	if err != nil {
		refuse(502, "getsupervisedassetid: "+err.Error())
		return
	}
	var sup struct {
		Asset             string `json:"asset"`
		Token             string `json:"token"`
		Entropy           string `json:"entropy"`
		DeclarationScript string `json:"declarationscript"`
	}
	if err := json.Unmarshal(supRes, &sup); err != nil || sup.Asset == "" {
		refuse(502, "getsupervisedassetid returned no asset id")
		return
	}

	// Attach the issuance. The supervision object makes the raw path insert the
	// SEQSUP declaration output itself; token_amount is the consensus-required
	// minimum single reissuance-token atom (freeze-plus-reissue is how seize and
	// burn are answered), landing at the treasury-labeled escrow address.
	issueRes, err := s.nodeRPC("rawissueasset", funded.Hex, []map[string]any{{
		"asset_amount":  amount8(p.atoms),
		"asset_address": assetAddr,
		"token_amount":  amount8(1),
		"token_address": tokenAddr,
		"blind":         false,
		"contract_hash": contractHash,
		"denomination":  p.precision,
		"supervision": map[string]any{
			"operationalkey": operationalKey,
			"recoverykey":    p.recoveryKey,
			"pause":          p.pause,
		},
	}})
	if err != nil {
		refuse(502, "rawissueasset: "+err.Error())
		return
	}
	var issued []struct {
		Hex     string `json:"hex"`
		Vin     int64  `json:"vin"`
		Entropy string `json:"entropy"`
		Asset   string `json:"asset"`
		Token   string `json:"token"`
	}
	if err := json.Unmarshal(issueRes, &issued); err != nil || len(issued) == 0 || issued[len(issued)-1].Hex == "" {
		refuse(502, "rawissueasset returned no transaction")
		return
	}
	mint := issued[len(issued)-1]
	if !strings.EqualFold(mint.Asset, sup.Asset) {
		refuse(502, fmt.Sprintf("asset-id cross-check failed: rawissueasset derived %s, getsupervisedassetid derived %s", mint.Asset, sup.Asset))
		return
	}
	// Belt and braces: the raw path inserts the declaration output when
	// supervision is passed; if a build ever did not, append it explicitly.
	issueHex := mint.Hex
	if dec2, derr := s.decodeRawTransaction(issueHex); derr == nil && sup.DeclarationScript != "" {
		found := false
		for _, v := range dec2.Vout {
			if strings.EqualFold(v.ScriptPubKey.Hex, sup.DeclarationScript) {
				found = true
				break
			}
		}
		if !found {
			addRes, aerr := s.nodeRPC("addsupervisionrecordoutput", issueHex, sup.DeclarationScript, sup.Asset)
			if aerr != nil {
				refuse(502, "addsupervisionrecordoutput: "+aerr.Error())
				return
			}
			if err := json.Unmarshal(addRes, &issueHex); err != nil || issueHex == "" {
				refuse(502, "addsupervisionrecordoutput returned no transaction")
				return
			}
		}
	}

	signRes, err := s.walletRPC(seqEscrowWallet, "signrawtransactionwithwallet", issueHex)
	if err != nil {
		refuse(502, "signrawtransactionwithwallet: "+err.Error())
		return
	}
	var signed struct {
		Hex      string `json:"hex"`
		Complete bool   `json:"complete"`
	}
	if err := json.Unmarshal(signRes, &signed); err != nil || signed.Hex == "" {
		refuse(502, "the wallet returned no signed transaction")
		return
	}
	if !signed.Complete {
		refuse(502, "the issuance transaction could not be fully signed by the escrow wallet")
		return
	}

	// Preflight against the mempool policy before anything is recorded.
	accRes, err := s.nodeRPC("testmempoolaccept", []string{signed.Hex})
	if err != nil {
		refuse(502, "testmempoolaccept: "+err.Error())
		return
	}
	var acc []struct {
		Txid         string `json:"txid"`
		Allowed      bool   `json:"allowed"`
		RejectReason string `json:"reject-reason"`
	}
	if err := json.Unmarshal(accRes, &acc); err != nil || len(acc) == 0 {
		refuse(502, "testmempoolaccept returned no verdict")
		return
	}
	if !acc[0].Allowed {
		refuse(502, "the issuance transaction was rejected in preflight: "+acc[0].RejectReason)
		return
	}
	txid := acc[0].Txid

	// Idempotency anchor BEFORE broadcast: a replayed deploy after an ambiguous
	// send finds this record and never funds or mints a second time.
	rec := &DeployRecord{
		IdemKey:      p.idem,
		IssuanceID:   iss.ID,
		AssetID:      mint.Asset,
		Txid:         txid,
		ContractHash: contractHash,
		AID:          acct.AID,
		Address:      assetAddr,
		CreatedAt:    time.Now().Unix(),
	}
	if err := s.st.InsertDeploy(rec); err != nil {
		refuse(500, "the deploy record could not be stored; nothing was broadcast")
		return
	}

	if _, err := s.nodeRPC("sendrawtransaction", signed.Hex); err != nil && !txAlreadyKnown(err) {
		// The record exists (the anchor), the tx did not observably broadcast.
		// A replayed deploy returns the record; the operator re-broadcasts.
		s.st.Audit(acct.AID, "deploy.bearer.broadcast_failed", map[string]any{
			"issuance_id": iss.ID, "asset": mint.Asset, "txid": txid, "error": err.Error(),
		})
		writeErr(w, 502, "the issuance was signed and recorded (txid %s) but broadcast failed: %v; do not re-fund, re-broadcast the transaction", txid, err)
		return
	}

	if err := s.st.UpdateIssuanceFields(iss.ID, map[string]any{
		"status":            "live",
		"terms":             string(p.canonicalTerms),
		"supply":            p.supply,
		"precision":         p.precision,
		"clawback":          0,
		"asset_id":          mint.Asset,
		"txid":              txid,
		"contract_hash":     contractHash,
		"holder_aid":        "",
		"enclave_address":   assetAddr,
		"issuer_external":   1,
		"issuer_pubkey":     operationalKey,
		"enforcement":       "bearer",
		"recovery_pubkey":   p.recoveryKey,
		"supervision_pause": boolInt(p.pause),
	}); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	s.st.Audit(acct.AID, "deploy.success", map[string]any{
		"issuance_id": iss.ID, "asset": mint.Asset, "txid": txid, "contract_hash": contractHash,
		"atoms": p.atoms, "idem_key": p.idem, "enforcement": "bearer",
		"operational_key": operationalKey, "recovery_key": p.recoveryKey, "pause": p.pause,
		"token": mint.Token, "entropy": mint.Entropy,
	})

	// Watcher handoff, keyed on the mint txid exactly like a serviced deploy;
	// the canonical contract rides along for registry publication.
	iss.AssetID = mint.Asset
	iss.Txid = txid
	s.onDeployed(iss, string(canonicalContract))

	resp := deployResponse(rec)
	resp["enforcement"] = "bearer"
	resp["issuer_external"] = true
	resp["issuer_pubkey"] = operationalKey
	resp["token"] = mint.Token
	resp["entropy"] = mint.Entropy
	resp["treasury_address"] = tokenAddr
	resp["supervision"] = map[string]any{
		"operational_key": operationalKey,
		"recovery_key":    p.recoveryKey,
		"pause":           p.pause,
	}
	writeJSON(w, 200, resp)
}

// txAlreadyKnown recognizes the node telling us the transaction is already in
// the mempool or chain, which for a broadcast is success, not failure.
func txAlreadyKnown(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already in") || strings.Contains(msg, "already known") ||
		strings.Contains(msg, "txn-already") || strings.Contains(msg, "already have")
}

// escrowNewAddress derives a fresh labeled address in the seqpal-escrow wallet.
func (s *server) escrowNewAddress(label string) (string, error) {
	res, err := s.walletRPC(seqEscrowWallet, "getnewaddress", label)
	if err != nil {
		return "", err
	}
	var addr string
	if err := json.Unmarshal(res, &addr); err != nil || addr == "" {
		return "", fmt.Errorf("getnewaddress returned no address")
	}
	return addr, nil
}

// --- decoded raw tx (the subset the raw flows read) ---------------------------

type decodedTx struct {
	Txid string `json:"txid"`
	Vin  []struct {
		Txid string `json:"txid"`
		Vout int64  `json:"vout"`
	} `json:"vin"`
	Vout []struct {
		ScriptPubKey struct {
			Hex string `json:"hex"`
		} `json:"scriptPubKey"`
	} `json:"vout"`
}

func (s *server) decodeRawTransaction(txHex string) (*decodedTx, error) {
	res, err := s.nodeRPC("decoderawtransaction", txHex)
	if err != nil {
		return nil, err
	}
	var d decodedTx
	if err := json.Unmarshal(res, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// bearerFeeAsset names the asset the bearer mint's funding pays its network
// fee in. This chain has an open fee market with no default fee asset, so
// fundrawtransaction must be told one. SEQPALD_FEE_ASSET overrides; the
// fallback is the node's "bitcoin" label (tSEQ on the testnet), which the
// escrow wallet is kept funded in for exactly these fees.
func (s *server) bearerFeeAsset() (string, error) {
	if v := os.Getenv("SEQPALD_FEE_ASSET"); v != "" {
		return v, nil
	}
	res, err := s.nodeRPC("dumpassetlabels")
	if err != nil {
		return "", fmt.Errorf("dumpassetlabels: %w", err)
	}
	var labels map[string]string
	if err := json.Unmarshal(res, &labels); err != nil {
		return "", fmt.Errorf("dumpassetlabels: %w", err)
	}
	if a := labels["bitcoin"]; a != "" {
		return a, nil
	}
	return "", fmt.Errorf("no fee asset: set SEQPALD_FEE_ASSET")
}
