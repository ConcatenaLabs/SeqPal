package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Holder-list and frozen-coin changes on a network-enforced asset (M13).
//
// This is the same console the bearer (freely tradable) asset has, aimed at a
// different mechanism. For a bearer asset one balance is frozen by putting a
// court-ordered record on chain. For a network-enforced asset the rules live in a
// PUBLISHED POLICY that the chain reads on every transfer, so the two things an
// issuer can do are:
//
//   - change who may hold the token, by publishing the next holder list; and
//   - stop one specific coin, by publishing it on the frozen-coin list.
//
// Both take effect when the updated list is PUBLISHED and the on-chain rules
// output has moved onto it, not the moment the issuer presses the button. That is
// a property of the model, not a limitation of this platform, and every surface
// says so rather than implying instant effect.
//
// # WHAT THIS PLATFORM DOES AND DOES NOT HOLD
//
// It holds no key. Exactly as with a bearer freeze, the platform builds the
// change, the ISSUER signs it in their own browser with their own key, and only a
// signed change is published. The signature is over the next policy document, so
// the published policy is attributable to the issuer and to nobody else.
//
// # WHY A CHANGE TAKES TWO ATTEMPTS
//
// The on-chain rules program is compiled per policy, outside this platform and
// outside the policy server (neither is a compiler). So the first attempt at
// completing a change refuses with the document the issuer's registrar compiles
// against and names the commands to run; the second attempt carries the registrar
// values back and publishes. This is the same handoff a network deploy has, and
// the operation row makes it resumable rather than restartable.

// dampPolicyRegistrarMissing is the exact refusal an issuer sees when the
// registrar values are absent. It names what to run so the message is actionable.
const dampPolicyRegistrarMissing = "this change is built and signed but not published: your registrar must compile the updated rules against the document in this response and return the program identity and the finished rules transaction, which you then submit with this change again. Nothing has been published and no coin has moved."

// --- the branch every downstream flow has to take -----------------------------
//
// EVERY flow written against a platform-held holding account and a platform
// co-signature misbehaves for a network-enforced token, because neither exists:
// the units sit at the holder's own on-chain address and the chain checks the
// published lists without asking this platform anything. Left alone, those flows
// do not fail cleanly, they fail obscurely: an escrow delivery would read an
// empty balance, a transfer build would ask for a signature nobody can give.
//
// So each one branches EXPLICITLY here, and the ones that cannot work say why in
// a sentence an issuer understands rather than returning a store error. The
// decisions, and their reasons:
//
//	subscriptions   REFUSED. A subscription ends in a delivery, and delivery of
//	                this token goes to the investor's own holding address, which
//	                only the issuer's own wallet can sign. A primary sale for this
//	                model is not built.
//	closing         REFUSED, for the same reason: closing IS the delivery.
//	transfers       REFUSED, and pointed at the truth: the holder transfers from
//	                their own wallet against the published lists, with no platform
//	                involvement and no platform approval.
//	distributions   WORK. The register is derivable from the chain (the published
//	                holder list names the addresses; the policy server scans
//	                them), and a payout is an ordinary payment in another asset,
//	                so nothing about it needs a co-signature. A holder with no
//	                payout address on file is skipped with that reason, exactly as
//	                for any other token.
//	listings        WORK, and the eligibility read reflects the PUBLISHED HOLDER
//	                LIST rather than a category stamp, because that list is what
//	                actually decides who can hold this token.
//	mandates        WORK. An investor's payout address is still checked against
//	                their holding address for this token, because pasting the one
//	                into the other strands funds exactly as it would elsewhere.
//	receipt         programme REFUSED. It mints into a platform-held account and
//	                relies on transfer rules this platform checks; neither exists.

// networkEnforced reports whether an issuance's rules are enforced by the network
// rather than by this platform.
func networkEnforced(iss *Issuance) bool { return iss != nil && iss.Enforcement == "network" }

// refuseForNetwork writes the explicit refusal for a platform flow that cannot
// apply to a network-enforced token, records it, and reports whether it did. The
// reason is the whole point: a caller must never be left to infer why.
func (s *server) refuseForNetwork(w http.ResponseWriter, aid string, iss *Issuance, flow, reason string) bool {
	if !networkEnforced(iss) {
		return false
	}
	s.st.Audit(aid, flow+".refused", map[string]any{
		"issuance_id": iss.ID, "asset": iss.AssetID, "enforcement": "network", "reason": reason,
	})
	writeJSON(w, 409, map[string]any{
		"error": reason, "enforcement": "network", "flow": flow,
	})
	return true
}

// refuseForBearer is refuseForNetwork's counterpart for freely-tradable assets.
// A bearer token is an ordinary on-chain holding: there is no enclave escrow to
// deliver from, no policy server to co-sign, and no transfer agent. Flows built
// on those must say so, rather than proceeding to fail somewhere further in
// where the message is about a missing escrow enclave and the reader has to
// work out that the asset was never that kind in the first place.
func (s *server) refuseForBearer(w http.ResponseWriter, aid string, iss *Issuance, flow, reason string) bool {
	if iss == nil || iss.Enforcement != "bearer" {
		return false
	}
	s.st.Audit(aid, flow+".refused", map[string]any{
		"issuance_id": iss.ID, "asset": iss.AssetID, "enforcement": "bearer", "reason": reason,
	})
	writeJSON(w, 409, map[string]any{"error": reason, "enforcement": "bearer", "flow": flow})
	return true
}

// networkIssuance loads an owned issuance and requires it to be a live
// network-enforced asset. Holder-list controls exist for no other kind: a
// serviced asset is policed by the platform's own transfer checks, and a bearer
// asset by the court-order freeze console.
func (s *server) networkIssuance(w http.ResponseWriter, acct *Account, id string) *Issuance {
	iss := s.ownedIssuance(w, acct, id)
	if iss == nil {
		return nil
	}
	if iss.Enforcement != "network" || iss.Status != "live" || iss.AssetID == "" {
		s.st.Audit(acct.AID, "policy.refused", map[string]any{
			"issuance_id": id, "reason": "not a live network-enforced asset", "enforcement": iss.Enforcement,
		})
		writeErr(w, 409, "holder-list controls apply only to a live token whose rules the network enforces")
		return nil
	}
	return iss
}

// --- read --------------------------------------------------------------------

// policyHolder is one entry of a published holder list: the holding key, plus the
// block heights from which that holder may send and may be paid. Zero means no
// bound.
//
// The two shapes are not this platform's choice. A holder with no bounds appears
// in the published document as a BARE KEY, and one with a bound as an object, so
// that every document written before bounds existed still hashes and verifies
// exactly as it did. Both are accepted here, because reading only the first shape
// would silently drop the bounds that the network actually enforces.
type policyHolder struct {
	Key       string `json:"key"`
	SendAfter uint32 `json:"send_after"`
	RecvAfter uint32 `json:"recv_after"`
}

func (h *policyHolder) UnmarshalJSON(b []byte) error {
	var key string
	if err := json.Unmarshal(b, &key); err == nil {
		*h = policyHolder{Key: key}
		return nil
	}
	type bounded policyHolder
	var out bounded
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	*h = policyHolder(out)
	return nil
}

// publishedPolicy is the policy server's public snapshot document for an asset:
// who may hold it, which coins are frozen, the sequence number, and the
// commitment the chain holds.
type publishedPolicy struct {
	Asset    string `json:"asset"`
	Seq      uint64 `json:"seq"`
	Pi       string `json:"pi"`
	Hash     string `json:"hash"`
	Snapshot struct {
		Predicates struct {
			Whitelist struct {
				Root    string         `json:"root"`
				Entries []policyHolder `json:"entries"`
			} `json:"whitelist"`
			Blacklist struct {
				Root    string         `json:"root"`
				Entries []policyHolder `json:"entries"`
			} `json:"blacklist"`
		} `json:"predicates"`
	} `json:"snapshot"`
}

// holderRows renders the published holder list for a console: the key, and the
// height bounds in plain terms when there are any.
func holderRows(entries []policyHolder) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		row := map[string]any{"key": e.Key}
		if e.SendAfter != 0 {
			row["can_send_from_block"] = e.SendAfter
		}
		if e.RecvAfter != 0 {
			row["can_receive_from_block"] = e.RecvAfter
		}
		out = append(out, row)
	}
	return out
}

// handlePolicyStatus is GET /api/issuances/{id}/policy (owner): the published
// holder list and frozen-coin list, the sequence number and the commitment the
// chain holds, plus this platform's own change history so a pending change is
// visible beside the published one.
func (s *server) handlePolicyStatus(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	if iss.Enforcement != "network" || iss.AssetID == "" {
		writeJSON(w, 200, map[string]any{"network_enforced": false, "enforcement": iss.Enforcement})
		return
	}
	ops, _ := s.st.DampPolicyOpsByIssuance(iss.ID)
	out := map[string]any{
		"network_enforced": true,
		"enforcement":      "network",
		"asset":            iss.AssetID,
		"ops":              ops,
		// The bound at the top of every issuer-facing surface: the rules program a
		// transfer runs is sized, so a holder can combine at most two of their coins
		// of this token in one transfer.
		"max_coins_per_transfer": maxCoinsPerTransfer,
	}
	// The published policy is a PUBLIC read: no issuer token, and the issuer can
	// verify the same document independently.
	var pol publishedPolicy
	if err := s.callOpenAMP("GET", "/v1/snapshots?asset="+iss.AssetID, "", nil, &pol); err != nil {
		out["published"] = nil
		out["error"] = "the published rules could not be read right now"
		writeJSON(w, 200, out)
		return
	}
	// A frozen coin is published as a fingerprint of the coin, never as the coin
	// itself, so the published list cannot be turned back into transaction ids.
	// The count is what the published document supports; WHICH coins were frozen
	// comes from this platform's own change history below, and a coin frozen
	// somewhere else stays a number here rather than a guess.
	prints := make([]string, 0, len(pol.Snapshot.Predicates.Blacklist.Entries))
	for _, e := range pol.Snapshot.Predicates.Blacklist.Entries {
		prints = append(prints, e.Key)
	}
	out["published"] = map[string]any{
		"seq":                pol.Seq,
		"commitment":         pol.Pi,
		"document_hash":      pol.Hash,
		"holders":            holderRows(pol.Snapshot.Predicates.Whitelist.Entries),
		"holder_list_root":   pol.Snapshot.Predicates.Whitelist.Root,
		"frozen_coin_count":  len(prints),
		"frozen_coin_prints": prints,
	}
	writeJSON(w, 200, out)
}

// eligibilityFromPublishedList answers GET /api/eligibility for a token whose
// rules the network enforces. Eligibility here is not a stamp this platform
// grants: it is membership of the published holder list, which the chain reads on
// every transfer, so the answer is derived from that list and names it as the
// source. An identity with no holding key on record cannot be on the list, which
// is a definite "no", not an error.
func (s *server) eligibilityFromPublishedList(w http.ResponseWriter, aid, asset string) {
	answer := func(eligible bool, reasons []string, extra map[string]any) {
		out := map[string]any{
			"aid": aid, "asset": asset, "eligible": eligible, "reasons": reasons,
			"enforcement": "network",
			"source":      "the published holder list for this token, which the network itself checks",
		}
		for k, v := range extra {
			out[k] = v
		}
		writeJSON(w, 200, out)
	}
	acct, err := s.st.AccountByAID(aid)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if acct == nil {
		answer(false, []string{"this identity has no holding key on record, so it cannot appear on the published holder list"}, nil)
		return
	}
	keys, err := s.holdingKeysOf(acct)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if len(keys) == 0 {
		answer(false, []string{"this identity has no holding key on record, so it cannot appear on the published holder list"}, nil)
		return
	}
	var pol publishedPolicy
	if err := s.callOpenAMP("GET", "/v1/snapshots?asset="+asset, "", nil, &pol); err != nil {
		// Fail closed and say so: an unreadable list is not an empty list.
		answer(false, []string{"the published holder list for this token could not be read right now, so eligibility cannot be confirmed"}, nil)
		return
	}
	for _, e := range pol.Snapshot.Predicates.Whitelist.Entries {
		if !keys[strings.ToLower(strings.TrimSpace(e.Key))] {
			continue
		}
		// On the list, and the height bounds that bind this holder travel with the
		// answer: a venue that reports "eligible" without them would let a taker try a
		// trade the network will refuse until the holder's window opens.
		extra := map[string]any{"list_version": pol.Seq, "commitment": pol.Pi}
		if e.SendAfter != 0 {
			extra["can_send_from_block"] = e.SendAfter
		}
		if e.RecvAfter != 0 {
			extra["can_receive_from_block"] = e.RecvAfter
		}
		answer(true, []string{}, extra)
		return
	}
	answer(false, []string{"this identity is not on the published holder list for this token"},
		map[string]any{"list_version": pol.Seq, "commitment": pol.Pi})
}

// --- build -------------------------------------------------------------------

type policyChangeReq struct {
	// Holders are x-only holding keys. On a freeze they are removed from the
	// holder list; on an unfreeze they are added back.
	Holders []string `json:"holders"`
	// Coins are the specific coins to stop or release, as txid + output index.
	Coins []struct {
		Txid string `json:"txid"`
		Vout uint32 `json:"vout"`
	} `json:"coins"`
	Reason    string `json:"reason"`
	OrderHash string `json:"order_hash"`
}

// handlePolicyFreeze is POST /api/issuances/{id}/policy/freeze (owner): remove
// holders from the published list and/or publish specific coins as frozen.
func (s *server) handlePolicyFreeze(w http.ResponseWriter, r *http.Request) {
	s.policyChange(w, r, "freeze")
}

// handlePolicyUnfreeze is POST /api/issuances/{id}/policy/unfreeze (owner): put
// holders back on the published list and/or release specific coins.
func (s *server) handlePolicyUnfreeze(w http.ResponseWriter, r *http.Request) {
	s.policyChange(w, r, "unfreeze")
}

func (s *server) policyChange(w http.ResponseWriter, r *http.Request, kind string) {
	acct := principal(r)
	iss := s.networkIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	var req policyChangeReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	refuse := func(code int, reason string) {
		s.st.Audit(acct.AID, "policy."+kind+".refused", map[string]any{
			"issuance_id": iss.ID, "status": code, "reason": reason,
		})
		writeErr(w, code, "%s", reason)
	}
	if strings.TrimSpace(req.Reason) == "" {
		refuse(400, "a reason is required for every change; it is published beside the change")
		return
	}
	// The same court-order discipline the freely-tradable console uses: the order
	// document is hashed in the browser and only its fingerprint ever reaches this
	// server, but a change without one is refused.
	orderHash := strings.ToLower(strings.TrimSpace(req.OrderHash))
	if !isHex64(orderHash) {
		refuse(400, "order_hash (64 hex characters, the fingerprint of the legal order this change executes) is required")
		return
	}
	holders := make([]string, 0, len(req.Holders))
	seen := map[string]bool{}
	for _, h := range req.Holders {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || seen[h] {
			continue
		}
		if !validXOnly(h) {
			refuse(400, "every holder must be a valid 32-byte holding key in hex; "+h+" is not")
			return
		}
		seen[h] = true
		holders = append(holders, h)
	}
	coins := make([]map[string]any, 0, len(req.Coins))
	for _, c := range req.Coins {
		txid := strings.ToLower(strings.TrimSpace(c.Txid))
		if !isHex64(txid) {
			refuse(400, "every coin must name a 64-character transaction id and an output index")
			return
		}
		coins = append(coins, map[string]any{"txid": txid, "vout": c.Vout})
	}
	if len(holders) == 0 && len(coins) == 0 {
		refuse(400, "name at least one holder or one coin for this change")
		return
	}

	// Idempotent build: the same order against the same holders and coins resumes
	// the existing operation. Without this a retry would open a second sequence
	// number at the policy server, and only one of the two could ever be published.
	targets, _ := canonicalJSON(map[string]any{"holders": holders, "coins": coins})
	if prev, err := s.st.DampPolicyOpFor(iss.ID, kind, orderHash, string(targets)); err != nil {
		writeErr(w, 500, "store error")
		return
	} else if prev != nil {
		writeJSON(w, 200, policyBuildResponse(prev))
		return
	}

	body := map[string]any{"asset": iss.AssetID, "reason": strings.TrimSpace(req.Reason)}
	if kind == "freeze" {
		if len(holders) > 0 {
			body["remove_whitelist"] = holders
		}
		if len(coins) > 0 {
			body["add_blacklist"] = coins
		}
	} else {
		if len(holders) > 0 {
			body["add_whitelist"] = holders
		}
		if len(coins) > 0 {
			body["remove_blacklist"] = coins
		}
	}
	var prep dampPolicyPrepareResponse
	if code, err := s.callOpenAMPStatus("POST", "/v1/issuer/damp-policy", s.cfg.issuerToken, body, &prep); err != nil {
		// A 400/409 from the policy server is a real, legible refusal (a holder who
		// is not on the list, a coin that is not frozen, a change that changes
		// nothing), so it is passed through rather than flattened to a bad gateway.
		refuse(statusForOpenAMP(code), "the change was refused: "+err.Error())
		return
	}
	if prep.PolicyID == "" {
		refuse(502, "the change was prepared without an operation id")
		return
	}

	op := &DampPolicyOp{
		ID: mustID(), IssuanceID: iss.ID, AssetID: iss.AssetID, Kind: kind,
		PolicyID: prep.PolicyID, Seq: int64(prep.Seq), PrevPi: prep.PrevPi, PiNext: prep.PiNext,
		Targets: json.RawMessage(targets), Holders: rawHolders(prep.Whitelist),
		HoldersAdded: rawList(prep.Change.AddedHolders), HoldersRemoved: rawList(prep.Change.RemovedHolders),
		CoinsFrozen:   rawList(prep.Change.AddedOutpoints),
		CoinsUnfrozen: rawList(prep.Change.RemovedOutpoints),
		Reason:        strings.TrimSpace(req.Reason), OrderHash: orderHash,
		ToSign: prep.ToSign, SnapshotHash: prep.SnapshotHash,
		RegistrarDoc: prep.DeriveSnapshot, State: "pending",
	}
	if err := s.st.InsertDampPolicyOp(op); err != nil {
		// The policy server has prepared and cannot be un-prepared, so losing the row
		// would strand the change. Report rather than swallow.
		s.st.Audit(acct.AID, "policy."+kind+".record_failed", map[string]any{
			"issuance_id": iss.ID, "policy_id": prep.PolicyID, "error": err.Error(),
		})
		writeErr(w, 500, "the change was prepared (%s) but the record could not be stored; do not retry, contact support", prep.PolicyID)
		return
	}
	s.st.Audit(acct.AID, "policy."+kind+".build", map[string]any{
		"issuance_id": iss.ID, "asset": iss.AssetID, "op": op.ID, "policy_id": prep.PolicyID,
		"seq": prep.Seq, "commitment": prep.PiNext, "reason": op.Reason, "order_hash": orderHash,
		"holders": holders, "coins": coins,
	})
	writeJSON(w, 200, policyBuildResponse(op))
}

// dampPolicyPrepareResponse is the policy server's prepare reply.
type dampPolicyPrepareResponse struct {
	PolicyID string `json:"policy_id"`
	Asset    string `json:"asset"`
	Seq      uint64 `json:"seq"`
	PrevPi   string `json:"prev_pi"`
	PiNext   string `json:"pi_next"`
	// Whitelist is the next policy's holder list, in the same two document shapes
	// the published one uses, so a holder's lockup or receive window is recorded
	// here rather than flattened away.
	Whitelist []policyHolder `json:"whitelist"`
	Blacklist []string       `json:"blacklist"`
	Change    struct {
		AddedHolders     []string `json:"added_holders"`
		RemovedHolders   []string `json:"removed_holders"`
		AddedOutpoints   []string `json:"added_outpoints"`
		RemovedOutpoints []string `json:"removed_outpoints"`
	} `json:"change"`
	ToSign         string          `json:"to_sign"`
	SnapshotHash   string          `json:"snapshot_hash"`
	DeriveSnapshot json.RawMessage `json:"derive_snapshot"`
}

func policyBuildResponse(op *DampPolicyOp) map[string]any {
	resp := map[string]any{
		"op_id":     op.ID,
		"kind":      op.Kind,
		"to_sign":   op.ToSign,
		"sign_with": "issuer",
		// What to_sign is the TAGGED hash of. The issuer's wallet signs under the
		// tag itself, so it never signs bytes it cannot check.
		"snapshot_hash": op.SnapshotHash,
		"snapshot_tag":  "OpenDAMP/snapshot/v1",
		"state":         op.State,
		"seq":           op.Seq,
		"note": "sign the 32-byte message with your own key, then POST it to /policy/" + op.ID +
			"/complete; the change takes effect when the updated list is published and the on-chain rules output has moved onto it, not the moment you sign",
	}
	if op.Txid != "" {
		resp["txid"] = op.Txid
	}
	return resp
}

func rawList(list []string) json.RawMessage {
	if list == nil {
		list = []string{}
	}
	b, _ := json.Marshal(list)
	return b
}

// rawHolders records the holder list an operation publishes, keeping each
// holder's height bounds beside its key. Recording keys alone would leave the
// stored history claiming an unrestricted list where the network enforces a
// lockup.
func rawHolders(list []policyHolder) json.RawMessage {
	rows := holderRows(list)
	b, _ := json.Marshal(rows)
	return b
}

// --- complete ----------------------------------------------------------------

type policyCompleteReq struct {
	// Sig is the issuer's signature over the to_sign the build returned.
	Sig string `json:"sig"`
	// The registrar's values. Absent on the first attempt, which is answered with
	// the document to compile against.
	VerifierProgram string `json:"verifier_program"`
	VerifierAddress string `json:"verifier_address"`
	RulesTx         string `json:"rules_tx"`
}

// handlePolicyComplete is POST /api/issuances/{id}/policy/{opID}/complete
// (owner): hand the issuer's signature and the registrar's values to the policy
// server, which verifies the signature against the issuer's own key, checks the
// rules transaction against the commitment it computed, broadcasts it and
// publishes the updated list.
func (s *server) handlePolicyComplete(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.networkIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	var req policyCompleteReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	op, err := s.st.DampPolicyOpByID(r.PathValue("opID"))
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if op == nil || op.IssuanceID != iss.ID {
		writeErr(w, 404, "unknown change for this token")
		return
	}
	if op.State == "published" {
		writeJSON(w, 200, map[string]any{
			"op_id": op.ID, "txid": op.Txid, "state": op.State, "seq": op.Seq, "idempotent": true,
		})
		return
	}
	sig := strings.ToLower(strings.TrimSpace(req.Sig))
	if len(sig) != 128 || !isHexStr(sig) {
		writeErr(w, 400, "sig must be a 64-byte signature in hex over the change message")
		return
	}
	program := strings.ToLower(strings.TrimSpace(req.VerifierProgram))
	rulesTx := strings.TrimSpace(req.RulesTx)
	if program == "" || rulesTx == "" {
		s.st.Audit(acct.AID, "policy."+op.Kind+".registrar_required", map[string]any{
			"issuance_id": iss.ID, "op": op.ID, "policy_id": op.PolicyID,
		})
		writeJSON(w, 409, map[string]any{
			"error":              dampPolicyRegistrarMissing,
			"op_id":              op.ID,
			"stage":              "signed",
			"seq":                op.Seq,
			"commitment":         op.PiNext,
			"registrar_document": op.RegistrarDoc,
			"registrar_commands": []string{
				"opendamp derive --snapshot <registrar_document>",
				"opendamp issuer-update --snapshot <current> --next-snapshot <registrar_document> --request <fee coin> --issuer-privkey <your key>",
			},
		})
		return
	}
	if !validHex32(program) {
		writeErr(w, 400, "the registrar's program identity must be 32 bytes in hex")
		return
	}

	// Persist the signature and the registrar values BEFORE the publishing call, so
	// an ambiguous outcome is resumed from this row rather than rebuilt (a rebuild
	// would need a fresh signature and would open a second sequence number).
	_ = s.st.UpdateDampPolicyOpFields(op.ID, map[string]any{
		"state": "prepared", "verifier_cmr": program,
	})

	body := map[string]any{"sig": sig, "verifier_cmr": program, "signed_tx": rulesTx}
	if addr := strings.TrimSpace(req.VerifierAddress); addr != "" {
		body["verifier_spk"] = addr
	}
	var done struct {
		Txid string `json:"txid"`
		Seq  uint64 `json:"seq"`
		Pi   string `json:"pi"`
	}
	if code, cerr := s.callOpenAMPStatus("POST", "/v1/issuer/damp-policy/"+op.PolicyID+"/complete",
		s.cfg.issuerToken, body, &done); cerr != nil {
		_ = s.st.UpdateDampPolicyOpFields(op.ID, map[string]any{"error": cerr.Error()})
		s.st.Audit(acct.AID, "policy."+op.Kind+".refused", map[string]any{
			"issuance_id": iss.ID, "op": op.ID, "status": code, "reason": cerr.Error(),
		})
		writeErr(w, statusForOpenAMP(code), "the change could not be published: %v", cerr)
		return
	}
	if done.Txid == "" {
		writeErr(w, 502, "the change was published without a transaction id")
		return
	}
	_ = s.st.UpdateDampPolicyOpFields(op.ID, map[string]any{
		"state": "published", "txid": done.Txid, "error": "",
	})
	s.st.Audit(acct.AID, "policy."+op.Kind+".published", map[string]any{
		"issuance_id": iss.ID, "asset": op.AssetID, "op": op.ID, "policy_id": op.PolicyID,
		"seq": op.Seq, "commitment": op.PiNext, "txid": done.Txid,
		"reason": op.Reason, "order_hash": op.OrderHash,
	})
	// The issuance record follows the published policy, so every screen that shows
	// the commitment shows the one the chain is moving to.
	_ = s.st.UpdateIssuanceFields(iss.ID, map[string]any{"policy_commitment": op.PiNext})

	// A holder whose request this change carried is now admitted in fact, not
	// just in the issuer's intention. Only an unfreeze adds keys; a freeze
	// removes them, and marking those included would be exactly backwards.
	if op.Kind == "unfreeze" {
		s.noteWhitelistInclusions(iss.ID, op.ID, op.HoldersAdded)
	}

	writeJSON(w, 200, map[string]any{
		"op_id": op.ID, "kind": op.Kind, "state": "published", "txid": done.Txid,
		"seq": op.Seq, "commitment": op.PiNext,
		"note": "the updated list is published and the rules transaction is broadcast; it binds transfers once that transaction confirms, and until then holders transfer under the previous list",
	})
}
