package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
)

// This file is the tests-owner deliverable for M5 (real money legs, offer-side
// gates, closing v1, wallet-first custody). It stands up its own instrumented
// stubs for every external surface the money engine touches: an openampd stub
// that answers the hosted-transfer/cosign flow and tracks balances, a JSON-RPC
// stub that plays both the Sequentia escrow node and the testnet4 Bitcoin node
// (deposits + sends), and a price stub. No live box is required.

// ---------------------------------------------------------------------------
// openampd stub (extends the M2 shape with transfers / balances / rules)
// ---------------------------------------------------------------------------

type m5User struct {
	AID        string
	Pubkeys    []string
	Categories []string
	Frozen     bool
}

type m5pendingTransfer struct {
	asset       string
	sender      string
	recipient   string
	atoms       uint64
	senderXOnly string
	burn        bool // M8: an OA-5 burn build (send to a provably-unspendable output)
}

type m5Stub struct {
	srv *httptest.Server
	mu  sync.Mutex

	users    map[string]*m5User
	assets   map[string]map[string]any
	balances map[string]map[string]uint64 // aid -> asset -> atoms
	nAsset   int

	pending       map[string]*m5pendingTransfer
	transferCount int // successful /complete calls (deliveries)
	deliveries    []m5pendingTransfer
	rulesWritten  map[string]json.RawMessage // asset -> last rules posted

	// M6 instrumentation (additive; unused by M5 tests): the raw body of every
	// POST /v1/transfers build (so a test can assert whether seqpald attached an
	// OA-4 "payment" leg) and a count of POST /v1/issuer/freeze calls (so a test
	// can assert whether a reorg triggered the global account freeze).
	transferBodies []map[string]any
	freezeCalls    int

	failTransfers bool // /v1/transfers returns 500
	failComplete  bool // /v1/transfers/{id}/complete returns 500

	// M6: simulate a pre-OA-4 policy server that rejects the unknown "payment"
	// field (DisallowUnknownFields), the runtime capability signal that makes
	// closing fall back to the v1 two-transaction path.
	rejectPaymentField bool

	// M7 instrumentation (additive; all inert by default so the M5/M6 suite is
	// unaffected). holdersHeight is the Sequentia block height GET /v1/issuer/
	// holders reports; clawbackLog is the public transparency log the clawback
	// reconciler scans; clawbacks counts POST /v1/issuer/clawback calls. gateAsset
	// + gateCat model a real policy-server refusal: when set, a transfer to a
	// recipient lacking gateCat for gateAsset is rejected (the accreditation-expiry
	// enforcement point).
	holdersHeight int64
	clawbackLog   []map[string]any
	clawbacks     int
	gateAsset     string
	gateCat       string

	// M8 instrumentation (additive; all inert by default so the M5/M6/M7 suite is
	// byte-identical). reissues maps a DR-mint request_id to its reissue txid, so a
	// retry is idempotent; reblindLeft makes POST /v1/issuer/reissue answer 202
	// "reblinding" a set number of times before it broadcasts (the reissuance-token
	// re-blinding retry loop). completeRefuse maps a pending transfer's recipient AID
	// to a policy-refusal reason so POST /v1/transfers/{id}/complete returns a real
	// 403 at co-sign (the first-class P2P refusal path). broadcastThenFailOnce makes
	// the next /complete settle on chain (deliver + append the transparency-log
	// entry) but then return 500, so a lost-write reconciliation is exercised. txLog
	// is the settled transfer / burn transparency log the P2P + DR reconcilers scan;
	// logSeq numbers those entries for the wallet-transfer poller cursor.
	reissues              map[string]string
	reblindLeft           map[string]int
	completeRefuse        map[string]string
	broadcastThenFailOnce bool
	txLog                 []map[string]any
	logSeq                uint64

	// M9 instrumentation (additive; all inert by default so the M5-M8 suite is
	// byte-identical). externalAssets marks an asset whose enclave issuer half is
	// the entity's own browser key: its clawback is two-phase (build returns the
	// L_claw sighashes, /complete adds the issuer signature and broadcasts), and
	// assetIssuerPub is the issuer x-only each to_sign entry must name. pendingClaw
	// holds a built-but-unbroadcast sweep, clawDone maps a consumed build id to its
	// txid so a replay of /complete returns the same txid and never re-sweeps.
	externalAssets map[string]bool
	assetIssuerPub map[string]string
	pendingClaw    map[string]*m5pendingClaw
	clawDone       map[string]string

	// M12 instrumentation (additive; inert by default). The two-phase
	// network-enforced issuance: dampPrepareBodies and dampCompleteBodies are the
	// raw forwarded bodies so a test can assert exactly what seqpald sent, and
	// dampPrepared is the live prepare keyed by prepare_id so /complete can only
	// finish a preparation that happened. dampCompleteErr makes /complete answer
	// with a policy-server refusal instead.
	dampPrepareBodies  []map[string]any
	dampCompleteBodies []map[string]any
	dampPrepared       map[string]map[string]any
	dampCompleteErr    string
}

// m5pendingClaw is a two-phase clawback build the stub returned but has not yet
// broadcast (it broadcasts only when /complete supplies the issuer signature).
type m5pendingClaw struct {
	asset, holder, reason, txid string
	atoms                       uint64
}

func newM5Stub(t *testing.T) *m5Stub {
	t.Helper()
	f := &m5Stub{
		users:          map[string]*m5User{},
		assets:         map[string]map[string]any{},
		balances:       map[string]map[string]uint64{},
		pending:        map[string]*m5pendingTransfer{},
		rulesWritten:   map[string]json.RawMessage{},
		holdersHeight:  250000,
		reissues:       map[string]string{},
		reblindLeft:    map[string]int{},
		completeRefuse: map[string]string{},
		externalAssets: map[string]bool{},
		assetIssuerPub: map[string]string{},
		pendingClaw:    map[string]*m5pendingClaw{},
		clawDone:       map[string]string{},
		dampPrepared:   map[string]map[string]any{},
	}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/users", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Pubkeys []string `json:"pubkeys"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		aid := aidFor(req.Pubkeys)
		f.mu.Lock()
		if _, ok := f.users[aid]; !ok {
			f.users[aid] = &m5User{AID: aid, Pubkeys: req.Pubkeys, Categories: []string{}}
		}
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"aid": aid})
	})

	mux.HandleFunc("GET /v1/users/{aid}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		u, ok := f.users[r.PathValue("aid")]
		var cats []string
		var frozen bool
		if ok {
			cats = append([]string{}, u.Categories...)
			frozen = u.Frozen
		}
		f.mu.Unlock()
		if !ok {
			writeJSON(w, 404, map[string]any{"error": "unknown aid"})
			return
		}
		if cats == nil {
			cats = []string{}
		}
		writeJSON(w, 200, map[string]any{"aid": r.PathValue("aid"), "categories": cats, "frozen": frozen})
	})

	mux.HandleFunc("GET /v1/users/{aid}/address", func(w http.ResponseWriter, r *http.Request) {
		// Deterministic enclave receive address, so isEnclaveAddress can recognize it.
		writeJSON(w, 200, map[string]any{"address": "tb1p" + r.PathValue("aid")})
	})

	mux.HandleFunc("GET /v1/users/{aid}/balance", func(w http.ResponseWriter, r *http.Request) {
		asset := r.URL.Query().Get("asset")
		f.mu.Lock()
		var atoms uint64
		if b, ok := f.balances[r.PathValue("aid")]; ok {
			atoms = b[asset]
		}
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"atoms": atoms})
	})

	mux.HandleFunc("POST /v1/issuer/categories", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-issuer-token" {
			writeJSON(w, 401, map[string]any{"error": "bad token"})
			return
		}
		var req struct {
			AID        string   `json:"aid"`
			Categories []string `json:"categories"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		if u, ok := f.users[req.AID]; ok {
			u.Categories = append([]string{}, req.Categories...)
		}
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"ok": true})
	})

	mux.HandleFunc("POST /v1/issuer/freeze", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AID    string `json:"aid"`
			Frozen bool   `json:"frozen"`
		}
		raw, _ := readAllLimited(r.Body, 1<<20)
		json.Unmarshal(raw, &req)
		f.mu.Lock()
		f.freezeCalls++
		if u, ok := f.users[req.AID]; ok {
			u.Frozen = req.Frozen
		}
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"ok": true})
	})

	mux.HandleFunc("POST /v1/issuer/assets", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-issuer-token" {
			writeJSON(w, 401, map[string]any{"error": "bad token"})
			return
		}
		var body map[string]any
		raw, _ := readAllLimited(r.Body, 1<<20)
		json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.nAsset++
		id := pad64(byte('a'), f.nAsset)
		rules, _ := json.Marshal(body["rules"])
		f.assets[id] = map[string]any{"id": id, "ticker": "X", "name": "X", "rules": json.RawMessage(rules)}
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{
			"asset": id, "txid": pad64(byte('b'), f.nAsset), "contract_hash": pad64(byte('c'), f.nAsset),
		})
	})

	// M12: the two-phase network-enforced issuance. Phase 1 mints the verifier
	// asset and FIXES the asset id and the policy commitment; phase 2 takes the
	// compiled-program identities and mints. The stub derives a commitment from
	// the whitelist it was given, so a test can prove seqpald forwards the right
	// holder list and refuses a mismatched commitment.
	mux.HandleFunc("POST /v1/issuer/damp-assets", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-issuer-token" {
			writeJSON(w, 401, map[string]any{"error": "bad token"})
			return
		}
		var body map[string]any
		raw, _ := readAllLimited(r.Body, 1<<20)
		json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.dampPrepareBodies = append(f.dampPrepareBodies, body)
		f.nAsset++
		n := f.nAsset
		id := pad64(byte('a'), n)
		vid := pad64(byte('v'), n)
		wl, _ := body["whitelist"].([]any)
		root := sha256Hex([]byte(fmt.Sprint(wl)))
		pi := sha256Hex([]byte(id + root))
		prepareID := pad64(byte('p'), n)
		f.dampPrepared[prepareID] = map[string]any{
			"asset": id, "verifier_asset": vid, "pi": pi, "whitelist_root": root,
		}
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{
			"prepare_id": prepareID, "asset": id, "contract_hash": pad64(byte('c'), n),
			"contract":            json.RawMessage(`{"openamp":{"enforcement":"damp"}}`),
			"verifier_asset":      vid,
			"verifier_amount":     body["verifier_amount"],
			"verifier_issue_txid": pad64(byte('7'), n),
			"pi":                  pi, "whitelist_root": root, "tree": "dmt-v1",
			"snapshot":             json.RawMessage(`{"v":1}`),
			"snapshot_hash":        pad64(byte('8'), n),
			"snapshot_sig_message": pad64(byte('9'), n),
			"derive_snapshot": map[string]any{
				"v": 1, "asset": id, "verifier_asset": vid, "tree": "dmt-v1",
				"issuer_update_key": body["issuer_update_key"], "network": body["network"],
			},
		})
	})

	mux.HandleFunc("POST /v1/issuer/damp-assets/{id}/complete", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-issuer-token" {
			writeJSON(w, 401, map[string]any{"error": "bad token"})
			return
		}
		var body map[string]any
		raw, _ := readAllLimited(r.Body, 1<<20)
		json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.dampCompleteBodies = append(f.dampCompleteBodies, body)
		prep, ok := f.dampPrepared[r.PathValue("id")]
		failWith := f.dampCompleteErr
		f.mu.Unlock()
		if !ok {
			writeJSON(w, 404, map[string]any{"error": "unknown or expired prepared issuance"})
			return
		}
		if failWith != "" {
			writeJSON(w, 409, map[string]any{"error": failWith})
			return
		}
		if body["pi"] != prep["pi"] {
			writeJSON(w, 409, map[string]any{"error": "pi mismatch"})
			return
		}
		id, _ := prep["asset"].(string)
		f.mu.Lock()
		f.assets[id] = map[string]any{"id": id, "ticker": "X", "name": "X", "enforcement": "damp"}
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{
			"asset": id, "verifier_asset": prep["verifier_asset"],
			"txids": map[string]string{
				"verifier_issue": pad64(byte('7'), 1), "asset_issue": pad64(byte('b'), 1),
				"verifier_lock": pad64(byte('b'), 1),
			},
			"pi": prep["pi"], "whitelist_root": prep["whitelist_root"],
			"user_covenant_address":     "sq1holder-" + id[:8],
			"user_covenant_spk":         "5120" + id,
			"verifier_covenant_address": "sq1rules-" + id[:8],
			"verifier_covenant_spk":     "5120" + id,
			"contract":                  json.RawMessage(`{"openamp":{"enforcement":"damp"}}`),
			"contract_hash":             pad64(byte('c'), 1),
			"snapshot_seq":              0, "snapshot_signed": false,
		})
	})

	mux.HandleFunc("GET /v1/assets", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		out := []map[string]any{}
		for _, a := range f.assets {
			out = append(out, a)
		}
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"assets": out})
	})

	mux.HandleFunc("GET /v1/assets/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		a, ok := f.assets[r.PathValue("id")]
		f.mu.Unlock()
		if !ok {
			writeJSON(w, 404, map[string]any{"error": "unknown asset"})
			return
		}
		writeJSON(w, 200, a)
	})

	// Hosted transfer build: return the holder-side sighash to sign, with the
	// pubkey the escrow actually holds (so deliverFromEscrow's key check passes).
	mux.HandleFunc("POST /v1/transfers", func(w http.ResponseWriter, r *http.Request) {
		if f.failTransfers {
			writeJSON(w, 500, map[string]any{"error": "transfer build refused"})
			return
		}
		var body struct {
			Asset        string `json:"asset"`
			SenderAID    string `json:"sender_aid"`
			RecipientAID string `json:"recipient_aid"`
			Atoms        uint64 `json:"atoms"`
		}
		raw, _ := readAllLimited(r.Body, 1<<20)
		json.Unmarshal(raw, &body)
		var rawBody map[string]any
		json.Unmarshal(raw, &rawBody)
		f.mu.Lock()
		f.transferBodies = append(f.transferBodies, rawBody)
		reject := f.rejectPaymentField
		f.mu.Unlock()
		if _, hasPay := rawBody["payment"]; hasPay && reject {
			// A pre-OA-4 policy server rejects the unknown field, exactly as
			// DisallowUnknownFields would; seqpald reads this as "no payment leg".
			writeJSON(w, 400, map[string]any{"error": `json: unknown field "payment"`})
			return
		}
		// M7 category gate (inert unless a test sets gateCat): a real policy-server
		// refusal when the recipient lacks a required category for the asset. This is
		// the enforcement point an expired accreditation trips (the category token is
		// dropped, so the next transfer is refused).
		f.mu.Lock()
		gAsset, gCat := f.gateAsset, f.gateCat
		var recipCats []string
		if u, ok := f.users[body.RecipientAID]; ok {
			recipCats = append([]string{}, u.Categories...)
		}
		f.mu.Unlock()
		if gCat != "" && body.Asset == gAsset && !hasCategory(recipCats, gCat) {
			writeJSON(w, 403, map[string]any{"error": "recipient lacks the required category " + gCat})
			return
		}
		f.mu.Lock()
		senderXOnly := ""
		if u, ok := f.users[body.SenderAID]; ok && len(u.Pubkeys) > 0 {
			senderXOnly = u.Pubkeys[0]
		}
		id, _ := randHex(8)
		f.pending[id] = &m5pendingTransfer{
			asset: body.Asset, sender: body.SenderAID, recipient: body.RecipientAID,
			atoms: body.Atoms, senderXOnly: senderXOnly,
		}
		f.mu.Unlock()
		sighash := strings.Repeat("11", 32)
		out := map[string]any{
			"id": id,
			"to_sign": []map[string]any{
				{"input": 0, "sighash": sighash, "pubkey": senderXOnly},
			},
		}
		// M6 OA-4 payment leg: when the build carries a "payment" object, answer the
		// atomic shape (assembled tx hex + the ordinary payment input indices seqpald
		// signs itself). The enclave input is 0, the payment input is 1, the fee input
		// is 2 (openampd's own). A build WITHOUT a payment object keeps the M5 shape.
		if _, ok := rawBody["payment"]; ok {
			out["tx"] = "0200000000" + id // a non-empty placeholder tx body
			out["payment_inputs"] = []int{1}
		}
		writeJSON(w, 200, out)
	})

	mux.HandleFunc("POST /v1/transfers/{id}/complete", func(w http.ResponseWriter, r *http.Request) {
		if f.failComplete {
			writeJSON(w, 500, map[string]any{"error": "transfer complete refused"})
			return
		}
		id := r.PathValue("id")
		f.mu.Lock()
		pt, ok := f.pending[id]
		if !ok {
			f.mu.Unlock()
			writeJSON(w, 404, map[string]any{"error": "unknown transfer"})
			return
		}
		// M8 first-class refusal at co-sign: an ineligible recipient, a resale inside
		// the lockup window, or a Reg S distribution-compliance window. The pending is
		// consumed (openampd co-signs once) and a real 403 with the reason is returned.
		if reason := f.completeRefuse[pt.recipient]; reason != "" && !pt.burn {
			delete(f.pending, id)
			f.mu.Unlock()
			writeJSON(w, 403, map[string]any{"error": reason})
			return
		}
		delete(f.pending, id)
		f.transferCount++
		f.deliveries = append(f.deliveries, *pt)
		txid, _ := randHex(32)
		f.logSeq++
		if pt.burn {
			// OA-5 burn: the holder's units go to a provably-unspendable output, so the
			// holder balance drops and the chain-derived supply falls with it.
			if m := f.balances[pt.sender]; m != nil && m[pt.asset] >= pt.atoms {
				m[pt.asset] -= pt.atoms
			}
			f.txLog = append(f.txLog, map[string]any{
				"seq": f.logSeq, "action": "burn",
				"data": map[string]any{"asset": pt.asset, "sender": pt.sender, "atoms": pt.atoms, "txid": txid},
			})
		} else {
			if f.balances[pt.recipient] == nil {
				f.balances[pt.recipient] = map[string]uint64{}
			}
			f.balances[pt.recipient][pt.asset] += pt.atoms
			f.txLog = append(f.txLog, map[string]any{
				"seq": f.logSeq, "action": "transfer",
				"data": map[string]any{"asset": pt.asset, "sender": pt.sender, "atoms": pt.atoms, "txid": txid},
			})
		}
		// Broadcast-then-lost-response: the tx settled and was logged, but the caller
		// sees a 500. seqpald must reconcile from the log, never re-broadcast.
		fail := f.broadcastThenFailOnce
		f.broadcastThenFailOnce = false
		f.mu.Unlock()
		if fail {
			writeJSON(w, 500, map[string]any{"error": "connection reset after broadcast"})
			return
		}
		writeJSON(w, 200, map[string]any{"txid": txid})
	})

	mux.HandleFunc("POST /v1/issuer/rules", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-issuer-token" {
			writeJSON(w, 401, map[string]any{"error": "bad token"})
			return
		}
		var body struct {
			Asset string          `json:"asset"`
			Rules json.RawMessage `json:"rules"`
		}
		raw, _ := readAllLimited(r.Body, 1<<20)
		json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.rulesWritten[body.Asset] = append(json.RawMessage(nil), body.Rules...)
		if a, ok := f.assets[body.Asset]; ok {
			a["rules"] = json.RawMessage(append(json.RawMessage(nil), body.Rules...))
		}
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"ok": true})
	})

	// M7: the issuer holder register (confirmed enclave balances per AID at a
	// Sequentia height), built from the stub's balance ledger.
	mux.HandleFunc("GET /v1/issuer/holders", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-issuer-token" {
			writeJSON(w, 401, map[string]any{"error": "bad token"})
			return
		}
		asset := r.URL.Query().Get("asset")
		f.mu.Lock()
		holders := map[string]uint64{}
		var total uint64
		for aid, m := range f.balances {
			if bal := m[asset]; bal > 0 {
				holders[aid] = bal
				total += bal
			}
		}
		h := f.holdersHeight
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"asset": asset, "height": h, "holders": holders, "total_atoms": total})
	})

	// M7: the full-sweep clawback. openampd appends its public transparency-log
	// entry BEFORE it signs, so a lost seqpald write can be reconciled from the log
	// (the clawback analogue of escrowFindSend). It seizes ALL of a holder's
	// confirmed balance for the asset (modeled by zeroing it) into the issuer
	// enclave. An empty holder is a 409, matching openampd.
	mux.HandleFunc("POST /v1/issuer/clawback", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-issuer-token" {
			writeJSON(w, 401, map[string]any{"error": "bad token"})
			return
		}
		var req struct {
			Asset     string `json:"asset"`
			HolderAID string `json:"holder_aid"`
			Reason    string `json:"reason"`
		}
		raw, _ := readAllLimited(r.Body, 1<<20)
		json.Unmarshal(raw, &req)
		f.mu.Lock()
		var atoms uint64
		if m, ok := f.balances[req.HolderAID]; ok {
			atoms = m[req.Asset]
		}
		if atoms == 0 {
			f.mu.Unlock()
			writeJSON(w, 409, map[string]any{"error": "holder has no confirmed enclave balance for this asset"})
			return
		}
		txid := fmt.Sprintf("%064x", 900000+len(f.clawbackLog)+1)
		// The reason is logged BEFORE any signature on BOTH paths (openampd's public
		// transparency-log invariant): the eventual sweep txid is committed now.
		f.clawbackLog = append(f.clawbackLog, map[string]any{
			"action": "clawback",
			"data": map[string]any{
				"asset": req.Asset, "holder": req.HolderAID, "reason": req.Reason, "txid": txid, "atoms": atoms,
			},
		})
		f.clawbacks++
		// M9 two-phase: an external-issuer asset cannot be swept server-side. BUILD the
		// L_claw sweep and return its sighashes (broadcast nothing, keep the balance);
		// /complete adds the issuer's signature and only then sweeps.
		if f.externalAssets[req.Asset] {
			id, _ := randHex(8)
			f.pendingClaw[id] = &m5pendingClaw{asset: req.Asset, holder: req.HolderAID, reason: req.Reason, txid: txid, atoms: atoms}
			pub := f.assetIssuerPub[req.Asset]
			f.mu.Unlock()
			writeJSON(w, 200, map[string]any{
				"id": id, "tx": "", "atoms": atoms,
				"to_sign": []map[string]any{{"input": 0, "sighash": strings.Repeat("33", 32), "pubkey": pub}},
			})
			return
		}
		f.balances[req.HolderAID][req.Asset] = 0
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"txid": txid, "atoms": atoms})
	})

	// M9: complete a two-phase (external-issuer) clawback. The issuer's browser
	// signatures (keyed by input index) arrive here; the stub verifies they are
	// present, sweeps the holder's balance into the issuer enclave, and broadcasts.
	// Idempotent + consume-once: a completed build returns the same txid and never
	// re-sweeps, mirroring openampd's GetClawback short-circuit.
	mux.HandleFunc("POST /v1/issuer/clawback/{id}/complete", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-issuer-token" {
			writeJSON(w, 401, map[string]any{"error": "bad token"})
			return
		}
		id := r.PathValue("id")
		var req struct {
			Sigs map[string]string `json:"sigs"`
		}
		raw, _ := readAllLimited(r.Body, 1<<20)
		json.Unmarshal(raw, &req)
		f.mu.Lock()
		if txid, ok := f.clawDone[id]; ok {
			atoms := uint64(0)
			f.mu.Unlock()
			writeJSON(w, 200, map[string]any{"txid": txid, "atoms": atoms, "idempotent": true})
			return
		}
		pc, ok := f.pendingClaw[id]
		if !ok {
			f.mu.Unlock()
			writeJSON(w, 404, map[string]any{"error": "unknown or expired clawback"})
			return
		}
		if _, has := req.Sigs["0"]; !has {
			f.mu.Unlock()
			writeJSON(w, 400, map[string]any{"error": "missing issuer signature for input 0"})
			return
		}
		if f.balances[pc.holder] != nil {
			f.balances[pc.holder][pc.asset] = 0
		}
		f.clawDone[id] = pc.txid
		delete(f.pendingClaw, id)
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"txid": pc.txid, "atoms": pc.atoms})
	})

	// M7/M8: the public transparency log as newline-delimited JSON, scanned by
	// findClawbackInLog (action "clawback") and the M8 reconcilers / wallet poller
	// (actions "transfer" and "burn"). Every consumer filters by action, so the two
	// families never cross.
	mux.HandleFunc("GET /v1/log", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		entries := append([]map[string]any{}, f.clawbackLog...)
		entries = append(entries, f.txLog...)
		f.mu.Unlock()
		w.WriteHeader(200)
		for _, e := range entries {
			b, _ := json.Marshal(e)
			w.Write(b)
			w.Write([]byte("\n"))
		}
	})

	// M8 OA-6 DR mint (reissuance): idempotent by request_id; optionally answers
	// "reblinding" a set number of times first (the reissuance-token re-blinding
	// retry loop). A broadcast lands the minted units in the target enclave, raising
	// the chain-derived supply.
	mux.HandleFunc("POST /v1/issuer/reissue", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-issuer-token" {
			writeJSON(w, 401, map[string]any{"error": "bad token"})
			return
		}
		var req struct {
			Asset     string `json:"asset"`
			TargetAID string `json:"target_aid"`
			Atoms     uint64 `json:"atoms"`
			RequestID string `json:"request_id"`
		}
		raw, _ := readAllLimited(r.Body, 1<<20)
		json.Unmarshal(raw, &req)
		f.mu.Lock()
		if txid, ok := f.reissues[req.RequestID]; ok {
			f.mu.Unlock()
			writeJSON(w, 200, map[string]any{"reissue_txid": txid, "status": "done", "idempotent": true})
			return
		}
		if n := f.reblindLeft[req.RequestID]; n > 0 {
			f.reblindLeft[req.RequestID] = n - 1
			f.mu.Unlock()
			writeJSON(w, 202, map[string]any{"status": "reblinding", "reblind_txid": fmt.Sprintf("%064x", 800000+n)})
			return
		}
		if f.balances[req.TargetAID] == nil {
			f.balances[req.TargetAID] = map[string]uint64{}
		}
		f.balances[req.TargetAID][req.Asset] += req.Atoms
		txid := fmt.Sprintf("%064x", 810000+len(f.reissues)+1)
		f.reissues[req.RequestID] = txid
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"reissue_txid": txid, "status": "broadcast", "target_aid": req.TargetAID, "atoms": req.Atoms})
	})

	// M8 OA-5 burn build: returns the holder-side sighash to sign (pubkey empty, so
	// the custodian signs with its enclave key). Completion runs through the shared
	// /v1/transfers/{id}/complete path, which recognizes the burn pending.
	mux.HandleFunc("POST /v1/issuer/burn", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-issuer-token" {
			writeJSON(w, 401, map[string]any{"error": "bad token"})
			return
		}
		var req struct {
			Asset     string `json:"asset"`
			HolderAID string `json:"holder_aid"`
			Atoms     uint64 `json:"atoms"`
		}
		raw, _ := readAllLimited(r.Body, 1<<20)
		json.Unmarshal(raw, &req)
		f.mu.Lock()
		id, _ := randHex(8)
		f.pending[id] = &m5pendingTransfer{asset: req.Asset, sender: req.HolderAID, atoms: req.Atoms, burn: true}
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{
			"id": id, "burn_atoms": req.Atoms,
			"to_sign": []map[string]any{{"input": 0, "sighash": strings.Repeat("22", 32), "pubkey": ""}},
		})
	})

	// M8 chain-derived circulating supply: the holders sum (never a stored counter),
	// so a burn lowers it and a reissue raises it as the balances move.
	mux.HandleFunc("GET /v1/supply", func(w http.ResponseWriter, r *http.Request) {
		asset := r.URL.Query().Get("asset")
		f.mu.Lock()
		var total uint64
		for _, m := range f.balances {
			total += m[asset]
		}
		h := f.holdersHeight
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"asset": asset, "circulating_atoms": total, "height": h})
	})

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// hasCategory reports whether cats contains cat (the stub's category-gate probe).
func hasCategory(cats []string, cat string) bool {
	for _, c := range cats {
		if c == cat {
			return true
		}
	}
	return false
}

// --- M7 stub helpers ---------------------------------------------------------

func (f *m5Stub) setGate(asset, cat string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gateAsset, f.gateCat = asset, cat
}

func (f *m5Stub) clawbackCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clawbacks
}

// markExternal (M9) flags an asset as external-issuer at the policy server: its
// clawback becomes two-phase and every to_sign entry names issuerPub (the entity's
// own browser x-only). seqpald independently tracks the same flag from its deploy
// response, so a test sets both sides consistently.
func (f *m5Stub) markExternal(assetID, issuerPub string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.externalAssets[assetID] = true
	f.assetIssuerPub[assetID] = issuerPub
}

// pendingClawCount reports how many two-phase builds are awaiting an issuer
// signature (used to prove a build is resumed, not re-assembled).
func (f *m5Stub) pendingClawCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.pendingClaw)
}

func (f *m5Stub) logLen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.clawbackLog)
}

func (f *m5Stub) userCategories(aid string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.users[aid]; ok {
		return append([]string{}, u.Categories...)
	}
	return nil
}

func (f *m5Stub) balanceOf(aid, asset string) uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := f.balances[aid]; ok {
		return m[asset]
	}
	return 0
}

func (f *m5Stub) setBalance(aid, asset string, atoms uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.balances[aid] == nil {
		f.balances[aid] = map[string]uint64{}
	}
	f.balances[aid][asset] = atoms
}

func (f *m5Stub) transfers() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.transferCount
}

// freezes reports how many POST /v1/issuer/freeze calls the stub has seen (M6:
// a BTC-reorg-out post delivery must trigger the global account freeze).
func (f *m5Stub) freezes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.freezeCalls
}

// frozen reports whether the given AID is currently frozen at the policy server
// (M6 reorg watcher: frozen on reorg-out, unfrozen on re-confirmation).
func (f *m5Stub) frozen(aid string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.users[aid]; ok {
		return u.Frozen
	}
	return false
}

// anyTransferCarriedPayment reports whether ANY POST /v1/transfers build body
// carried an OA-4 "payment" leg (the atomic-DvP marker). If no build ever
// carried one, closing ran the M5 two-transaction v1 path (delivery + a
// separate release send), not atomic single-tx DvP.
func (f *m5Stub) anyTransferCarriedPayment() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.transferBodies {
		if b == nil {
			continue
		}
		if _, ok := b["payment"]; ok {
			return true
		}
	}
	return false
}

// transferBuildCount reports how many POST /v1/transfers builds were requested.
func (f *m5Stub) transferBuildCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.transferBodies)
}

// lastTransferBuildBody returns the raw body of the most recent POST
// /v1/transfers build, or nil if none was requested. The per-transfer
// confidentiality tests use it to assert the confidential flag reached the
// policy server verbatim.
func (f *m5Stub) lastTransferBuildBody() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.transferBodies) == 0 {
		return nil
	}
	return f.transferBodies[len(f.transferBodies)-1]
}

// lastPaymentLeg returns the "payment" object from the most recent transfer build
// that carried one (the OA-4 atomic leg: asset, atoms, from_address, to_address),
// or nil if no build carried a payment leg.
func (f *m5Stub) lastPaymentLeg() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.transferBodies) - 1; i >= 0; i-- {
		b := f.transferBodies[i]
		if b == nil {
			continue
		}
		if p, ok := b["payment"].(map[string]any); ok {
			return p
		}
	}
	return nil
}

func (f *m5Stub) rulesFor(asset string) json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rulesWritten[asset]
}

// --- M8 stub helpers ---------------------------------------------------------

// supplyOf is the chain-derived circulating supply GET /v1/supply reports: the
// sum of every holder's balance for the asset.
func (f *m5Stub) supplyOf(asset string) uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var total uint64
	for _, m := range f.balances {
		total += m[asset]
	}
	return total
}

// setRefusal makes the policy server refuse a co-sign for a recipient AID with a
// given reason (the first-class P2P refusal: ineligible / lockup / Reg S).
func (f *m5Stub) setRefusal(recipientAID, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completeRefuse[recipientAID] = reason
}

// injectWalletTransfer appends a settled, wallet-initiated secondary transfer to
// the public transparency log, as the live wallet's own policy co-signature would.
// The M8 poller captures it and joins it to registered identities server-side.
func (f *m5Stub) injectWalletTransfer(asset, sender string, atoms uint64, txid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logSeq++
	f.txLog = append(f.txLog, map[string]any{
		"seq": f.logSeq, "action": "transfer",
		"data": map[string]any{"asset": asset, "sender": sender, "atoms": atoms, "txid": txid},
	})
}

// burnLogCount counts settled burn entries in the transparency log for an asset,
// so a test can prove a redeem burned exactly once (no double-burn).
func (f *m5Stub) burnLogCount(asset string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, e := range f.txLog {
		if e["action"] == "burn" {
			if d, ok := e["data"].(map[string]any); ok && d["asset"] == asset {
				n++
			}
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// JSON-RPC node stub (plays the Sequentia escrow node AND the testnet4 node)
// ---------------------------------------------------------------------------

type nodeDeposit struct {
	txid  string
	atoms uint64
	confs int64
	asset string
}

type nodeSend struct {
	address string
	atoms   uint64
	asset   string
	comment string
	txid    string
}

type fakeNode struct {
	srv    *httptest.Server
	mu     sync.Mutex
	prefix string
	height int64
	addrN  int
	txN    int

	deposits map[string]nodeDeposit
	invalid  map[string]bool
	sends    []nodeSend

	// extra (M10) lets a test install additional RPC methods (the supervision
	// and raw-issuance surface) without touching the shared dispatch. It runs
	// under n.mu.
	extra func(method string, params []json.RawMessage) (any, int, string, bool)
}

func newFakeNode(t *testing.T, prefix string, height int64) *fakeNode {
	t.Helper()
	n := &fakeNode{
		prefix:   prefix,
		height:   height,
		deposits: map[string]nodeDeposit{},
		invalid:  map[string]bool{},
	}
	n.srv = httptest.NewServer(http.HandlerFunc(n.handle))
	t.Cleanup(n.srv.Close)
	return n
}

func (n *fakeNode) handle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	raw, _ := readAllLimited(r.Body, 1<<20)
	json.Unmarshal(raw, &req)
	res, code, msg := n.dispatch(req.Method, req.Params)
	if msg != "" {
		writeJSON(w, 200, map[string]any{"result": nil, "error": map[string]any{"code": code, "message": msg}})
		return
	}
	writeJSON(w, 200, map[string]any{"result": res})
}

func (n *fakeNode) dispatch(method string, params []json.RawMessage) (any, int, string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	switch method {
	case "getblockcount":
		return n.height, 0, ""
	case "validateaddress":
		var addr string
		if len(params) > 0 {
			json.Unmarshal(params[0], &addr)
		}
		return map[string]any{"isvalid": addr != "" && !n.invalid[addr]}, 0, ""
	case "loadwallet":
		// Succeed idempotently: ensureWalletVia treats a nil error as loaded.
		return map[string]any{"name": n.prefix}, 0, ""
	case "createwallet":
		return map[string]any{"name": n.prefix}, 0, ""
	case "dumpassetlabels":
		// The open fee market has no default fee asset, so every asset send
		// names one; the real node answers this from its label table.
		return map[string]any{"bitcoin": strings.Repeat("c8", 32)}, 0, ""
	case "getnewaddress":
		n.addrN++
		return fmt.Sprintf("%s-addr-%d", n.prefix, n.addrN), 0, ""
	case "listunspent":
		var addrs []string
		if len(params) > 2 {
			json.Unmarshal(params[2], &addrs)
		}
		out := []map[string]any{}
		emit := func(addr string, d nodeDeposit) {
			out = append(out, map[string]any{
				"txid": d.txid, "address": addr, "amount": amount8(d.atoms),
				"asset": d.asset, "confirmations": d.confs, "spendable": true,
			})
		}
		if len(addrs) > 0 {
			for _, a := range addrs {
				if d, ok := n.deposits[a]; ok {
					emit(a, d)
				}
			}
		} else {
			for a, d := range n.deposits {
				emit(a, d)
			}
		}
		return out, 0, ""
	case "sendtoaddress":
		var addr string
		var amt json.Number
		if len(params) > 0 {
			json.Unmarshal(params[0], &addr)
		}
		if len(params) > 1 {
			json.Unmarshal(params[1], &amt)
		}
		asset := ""
		if len(params) > 10 {
			json.Unmarshal(params[10], &asset)
		}
		comment := ""
		if len(params) > 2 {
			json.Unmarshal(params[2], &comment)
		}
		n.txN++
		txid := fmt.Sprintf("%064x", n.txN)
		n.sends = append(n.sends, nodeSend{address: addr, atoms: atomsFromNumber(amt), asset: asset, comment: comment, txid: txid})
		return txid, 0, ""
	case "listtransactions":
		out := []map[string]any{}
		for _, sd := range n.sends {
			out = append(out, map[string]any{
				"category": "send", "address": sd.address, "amount": amount8(sd.atoms),
				"comment": sd.comment, "txid": sd.txid,
			})
		}
		return out, 0, ""
	case "gettransaction":
		// M6 reorg watcher: report a deposit tx's confirmation count. A reorged-out
		// deposit stays in the wallet with confs 0 (back in the mempool) or negative
		// (conflicted). Unknown txid -> RPC error so btcTxConfirmations takes no action.
		var txid string
		if len(params) > 0 {
			json.Unmarshal(params[0], &txid)
		}
		for _, d := range n.deposits {
			if d.txid == txid {
				return map[string]any{"confirmations": d.confs, "txid": txid}, 0, ""
			}
		}
		return nil, -5, "Invalid or non-wallet transaction id"
	case "signrawtransactionwithwallet":
		// M10 extension hook first: the supervision/raw-issuance stub owns this
		// method when installed (its handles are not hex the M6 shape understands).
		if n.extra != nil {
			if res, code, msg, ok := n.extra(method, params); ok {
				return res, code, msg
			}
		}
		// M6 atomic close: the escrow wallet partially signs its own USDX payment
		// inputs over the built tx. The body is returned unchanged (complete=false:
		// the enclave + fee inputs remain for openampd to finish).
		var txHex string
		if len(params) > 0 {
			json.Unmarshal(params[0], &txHex)
		}
		return map[string]any{"hex": txHex, "complete": false}, 0, ""
	default:
		if n.extra != nil {
			if res, code, msg, ok := n.extra(method, params); ok {
				return res, code, msg
			}
		}
		return nil, -32601, "method not found: " + method
	}
}

func (n *fakeNode) credit(address string, atoms uint64, confs int64, asset string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.txN++
	n.deposits[address] = nodeDeposit{txid: fmt.Sprintf("%064x", 1000+n.txN), atoms: atoms, confs: confs, asset: asset}
}

func (n *fakeNode) markInvalid(addr string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.invalid[addr] = true
}

func (n *fakeNode) sendCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.sends)
}

// seedSend injects a prior on-chain send (as if broadcast before a lost write),
// so a reconciliation scan can find it by its comment marker.
func (n *fakeNode) seedSend(address string, atoms uint64, comment, txid string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sends = append(n.sends, nodeSend{address: address, atoms: atoms, comment: comment, txid: txid})
}

func (n *fakeNode) lastSend() (nodeSend, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.sends) == 0 {
		return nodeSend{}, false
	}
	return n.sends[len(n.sends)-1], true
}

// ---------------------------------------------------------------------------
// price stub
// ---------------------------------------------------------------------------

func newPriceStub(t *testing.T, prices map[string]float64) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /prices", func(w http.ResponseWriter, r *http.Request) {
		out := map[string]map[string]float64{}
		for k, v := range prices {
			out[k] = map[string]float64{"price": v}
		}
		writeJSON(w, 200, out)
	})
	mux.HandleFunc("POST /seed", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

const m5USDX = "2a515539da5e6a60caa7766ecd65bac0c10d15717ddd2088844ba58f4d04b9de"

type m5h struct {
	t     *testing.T
	s     *server
	h     http.Handler
	oa    *m5Stub
	seq   *fakeNode
	btc   *fakeNode
	price *httptest.Server
}

type m5opts struct {
	withBTC      bool
	escrowConfs  int64
	setupFeeUSD  float64
	escrowFeeBps int64
}

func newM5Harness(t *testing.T, opts m5opts) *m5h {
	t.Helper()
	oa := newM5Stub(t)
	seq := newFakeNode(t, "seq", 100000)
	price := newPriceStub(t, map[string]float64{"BTC": 100000, "SEQ": 0.1, "USDX": 1})
	st, err := openStore(filepath.Join(t.TempDir(), "seqpald.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	confs := opts.escrowConfs
	if confs == 0 {
		confs = 1
	}
	bps := opts.escrowFeeBps
	if bps == 0 {
		bps = 50
	}
	cfg := config{
		openampURL:   oa.srv.URL,
		issuerToken:  "test-issuer-token",
		network:      "sequentia-testnet",
		blocksPerDay: 144,
		assumedTip:   100000,
		adminAIDs:    map[string]bool{},
		nodeURL:      seq.srv.URL,
		priceURL:     price.URL,
		usdxAsset:    m5USDX,
		escrowConfs:  confs,
		setupFeeUSD:  opts.setupFeeUSD,
		escrowFeeBps: bps,
		fiatSettle:   0,
	}
	h := &m5h{t: t, oa: oa, seq: seq, price: price}
	if opts.withBTC {
		h.btc = newFakeNode(t, "btc", 100000)
		cfg.btcURL = h.btc.srv.URL
	}
	s := &server{
		cfg:     cfg,
		st:      st,
		http:    &http.Client{Timeout: 5 * time.Second},
		rl:      newRateLimiter(),
		catMu:   newKeyedMutex(),
		closeMu: newKeyedMutex(),
		escrow:  newEscrowState(),
		screen:  newScreener(""),
	}
	h.s = s
	h.h = s.handler()
	return h
}

func (h *m5h) do(method, path, session string, body any) resp {
	h.t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, "/seqpal"+path, rdr)
	req.Header.Set("content-type", "application/json")
	if session != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	out := resp{code: rec.Code, raw: rec.Body.String(), set: rec.Result().Cookies()}
	json.Unmarshal(rec.Body.Bytes(), &out.body)
	return out
}

func (h *m5h) challenge(xonly string) string {
	h.t.Helper()
	r := h.do("POST", "/api/auth/challenge", "", map[string]any{"xonly": xonly})
	c, _ := r.body["challenge"].(string)
	if len(c) != 64 {
		h.t.Fatalf("challenge: %d %s", r.code, r.errMsg())
	}
	return c
}

func (h *m5h) register(priv, name, residence string) (session, aid, xonly string) {
	h.t.Helper()
	xonly = xonlyHex(h.t, priv)
	ch := h.challenge(xonly)
	r := h.do("POST", "/api/auth/register", "", map[string]any{
		"xonly": xonly, "challenge": ch, "sig": signChallengeHex(h.t, priv, ch),
		"display_name": name, "residence": residence,
	})
	if r.code != 200 {
		h.t.Fatalf("register: %d %s", r.code, r.errMsg())
	}
	acct, _ := r.body["account"].(map[string]any)
	aid, _ = acct["aid"].(string)
	return r.session(), aid, xonly
}

// verifiedClaims sets a verified SeqPal ID (no openampd categories). Sufficient
// for the offer-side promotion/gate surfaces, which read the ID, not eligibility.
func (h *m5h) verifiedClaims(aid string, c *Claims) {
	h.t.Helper()
	c.AID = aid
	c.Status = "verified"
	if err := h.s.st.UpsertClaims(c); err != nil {
		h.t.Fatalf("UpsertClaims: %v", err)
	}
}

// verifiedInvestor registers an account, an openampd user, verified claims, and
// pushes the projected categories to openampd (so subscribe eligibility passes).
func (h *m5h) verifiedInvestor(priv, name, residence, base string) (session, aid, xonly string) {
	h.t.Helper()
	session, aid, xonly = h.register(priv, name, residence)
	if _, err := h.s.registerUser(xonly); err != nil {
		h.t.Fatalf("registerUser: %v", err)
	}
	h.verifiedClaims(aid, &Claims{Residence: residence, BaseEligibility: base})
	if _, err := h.s.writeCategories(aid); err != nil {
		h.t.Fatalf("writeCategories: %v", err)
	}
	return
}

func (h *m5h) createIssuance(session, name, ticker string, terms map[string]any) string {
	h.t.Helper()
	r := h.do("POST", "/api/issuances", session, map[string]any{
		"name": name, "ticker": ticker, "structure_id": "native-equity", "terms": terms,
	})
	if r.code != 200 {
		h.t.Fatalf("create issuance: %d %s", r.code, r.errMsg())
	}
	iss, _ := r.body["issuance"].(map[string]any)
	id, _ := iss["id"].(string)
	return id
}

// deployLivePrivate creates and deploys a private-placement offering (no
// promotable_jurisdictions, so no RFSA gate) admitting the given jurisdiction.
func (h *m5h) deployLivePrivate(session, ticker, jurisdiction string, price float64) (issID, assetID, escrowAID string) {
	h.t.Helper()
	terms := map[string]any{
		"jurisdictions": map[string]any{jurisdiction: map[string]any{"access": "standard"}},
		"price":         price,
	}
	issID = h.createIssuance(session, ticker+" Co", ticker, terms)
	dep := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000000, "precision": 2,
	})
	if dep.code != 200 {
		h.t.Fatalf("deploy: %d %s", dep.code, dep.errMsg())
	}
	assetID, _ = dep.body["asset"].(string)
	escrowAID, _ = dep.body["escrow_aid"].(string)
	if assetID == "" || escrowAID == "" {
		h.t.Fatalf("deploy did not return asset/escrow: %s", dep.raw)
	}
	// Every asset is external (the issuer holds the clawback key); mirror that on the
	// policy-server stub so a clawback of this asset is two-phase.
	if issuerPub, _ := dep.body["issuer_pubkey"].(string); issuerPub != "" {
		h.oa.markExternal(assetID, issuerPub)
	}
	return
}

func genPriv(t *testing.T) string {
	t.Helper()
	k, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(k.Serialize())
}

// signCanonical produces the tagged BIP340 signature the close/mandate flows want
// over the exact sign_this bytes the server returned.
func signCanonical(t *testing.T, priv, tag, signThis string) string {
	t.Helper()
	sig, err := signTagged(priv, tag, []byte(signThis))
	if err != nil {
		t.Fatalf("signTagged: %v", err)
	}
	return sig
}

// ===========================================================================
// 1. Subscription lifecycle
// ===========================================================================

func TestM5USDXSubscriptionLifecycleToSettled(t *testing.T) {
	h := newM5Harness(t, m5opts{escrowConfs: 2})
	issuerPriv := genPriv(t)
	issuerSession, _, issuerXOnly := h.register(issuerPriv, "Issuer", "HN")
	issID, assetID, escrowAID := h.deployLivePrivate(issuerSession, "USDXL", "HN", 1.0)
	_ = assetID

	invPriv := genPriv(t)
	invSession, invAID, _ := h.verifiedInvestor(invPriv, "Investor", "HN", "ret")

	// Subscribe on the USDX rail: 100 tokens at $1 each.
	sub := h.do("POST", "/api/issuances/"+issID+"/subscribe", invSession, map[string]any{
		"rail": "usdx", "amount": 100, "refund_address": "seq-refund-addr",
	})
	if sub.code != 200 {
		t.Fatalf("subscribe: %d %s", sub.code, sub.errMsg())
	}
	depAddr, _ := sub.body["deposit_address"].(string)
	if depAddr == "" {
		t.Fatalf("no deposit address: %s", sub.raw)
	}
	if got, _ := sub.body["confs_required"].(float64); int64(got) != 2 {
		t.Fatalf("confs_required = %v, want 2", sub.body["confs_required"])
	}
	subObj, _ := sub.body["subscription"].(map[string]any)
	subID, _ := subObj["id"].(string)

	// A deposit below N confirmations must NOT flip in_escrow (anchoring supremacy:
	// nothing is final at shallow depth).
	depositAtoms := uint64(100 * 1e8)
	h.seq.credit(depAddr, depositAtoms, 1, m5USDX)
	h.s.watchDeposits()
	got, _ := h.s.st.SubscriptionByID(subID)
	if got.State != "created" {
		t.Fatalf("subscription flipped to %q at 1 conf (need 2)", got.State)
	}
	if got.Confirmations != 1 {
		t.Fatalf("confirmations = %d, want 1", got.Confirmations)
	}

	// At N confirmations it becomes in_escrow with the deposited amount recorded.
	h.seq.credit(depAddr, depositAtoms, 2, m5USDX)
	h.s.watchDeposits()
	got, _ = h.s.st.SubscriptionByID(subID)
	if got.State != "in_escrow" {
		t.Fatalf("subscription state = %q, want in_escrow at 2 confs", got.State)
	}
	if got.DepositedAtoms != depositAtoms {
		t.Fatalf("deposited_atoms = %d, want %d", got.DepositedAtoms, depositAtoms)
	}

	// Register the issuer's Sequentia payout mandate (release target).
	if err := h.s.st.UpsertMandate(&PayoutMandate{IssuanceID: issID, Chain: "sequentia", Address: "seq-issuer-payout"}); err != nil {
		t.Fatal(err)
	}

	// Close: deliver + release.
	h.closeAs(issuerSession, "", issuerXOnly, issID, issuerPriv)
	_ = escrowAID
	_ = invAID

	got, _ = h.s.st.SubscriptionByID(subID)
	if got.State != "settled" {
		t.Fatalf("subscription state after close = %q, want settled", got.State)
	}
	set, _ := h.s.st.SettlementByID(subID)
	if set == nil || set.State != "settled" {
		t.Fatalf("settlement = %+v, want settled", set)
	}
	if set.DeliveryTxid == "" || set.ReleaseTxid == "" {
		t.Fatalf("settlement missing delivery/release txid: %+v", set)
	}
	if h.oa.transfers() != 1 {
		t.Fatalf("delivery transfers = %d, want 1", h.oa.transfers())
	}
	if h.seq.sendCount() != 1 {
		t.Fatalf("release sends = %d, want 1", h.seq.sendCount())
	}
	// Release paid the mandate address, net of the 50 bps escrow fee.
	send, _ := h.seq.lastSend()
	fee := depositAtoms * 50 / 10000
	if send.address != "seq-issuer-payout" || send.atoms != depositAtoms-fee {
		t.Fatalf("release send = %+v, want addr seq-issuer-payout net %d", send, depositAtoms-fee)
	}
}

// closeAs drives the two-step close (fetch sign_this, sign, resubmit). The signer
// is always the issuer; the extra priv arg keeps the call sites explicit.
func (h *m5h) closeAs(issuerSession, _ignored, issuerXOnly, issID, issuerPriv string) {
	h.t.Helper()
	pre := h.do("POST", "/api/issuances/"+issID+"/close", issuerSession, map[string]any{})
	if pre.code != 200 {
		h.t.Fatalf("close (sign_this): %d %s", pre.code, pre.errMsg())
	}
	signThis, _ := pre.body["sign_this"].(string)
	tag, _ := pre.body["tag"].(string)
	if signThis == "" || tag == "" {
		h.t.Fatalf("close did not return sign_this/tag: %s", pre.raw)
	}
	sig := signCanonical(h.t, issuerPriv, tag, signThis)
	fin := h.do("POST", "/api/issuances/"+issID+"/close", issuerSession, map[string]any{
		"signature": sig, "signer_xonly": issuerXOnly,
	})
	if fin.code != 200 {
		h.t.Fatalf("close: %d %s", fin.code, fin.errMsg())
	}
}

func TestM5BTCDepositCreditsAtCapturedRate(t *testing.T) {
	h := newM5Harness(t, m5opts{withBTC: true, escrowConfs: 1})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, _, _ := h.deployLivePrivate(issuerSession, "BTCL", "HN", 1000.0)

	invSession, _, _ := h.verifiedInvestor(genPriv(t), "Investor", "HN", "ret")
	sub := h.do("POST", "/api/issuances/"+issID+"/subscribe", invSession, map[string]any{
		"rail": "btc", "amount": 1, "refund_address": "btc-refund-addr",
	})
	if sub.code != 200 {
		t.Fatalf("subscribe btc: %d %s", sub.code, sub.errMsg())
	}
	if note, _ := sub.body["registrar_note"].(string); note == "" {
		t.Fatalf("btc subscribe must surface the registrar (non-atomic) note: %s", sub.raw)
	}
	depAddr, _ := sub.body["deposit_address"].(string)
	subObj, _ := sub.body["subscription"].(map[string]any)
	subID, _ := subObj["id"].(string)

	// $1000 at $100000/BTC -> 0.01 BTC = 1_000_000 sats. Credit at 1 conf.
	h.btc.credit(depAddr, 1_000_000, 1, "")
	h.s.watchDeposits()
	got, _ := h.s.st.SubscriptionByID(subID)
	if got.State != "in_escrow" {
		t.Fatalf("btc subscription state = %q, want in_escrow", got.State)
	}
	if got.DepositedAtoms != 1_000_000 {
		t.Fatalf("deposited sats = %d, want 1_000_000", got.DepositedAtoms)
	}
	if got.USDRate <= 0 {
		t.Fatalf("captured USD/BTC rate = %v, want > 0", got.USDRate)
	}
}

func TestM5FiatCheckoutSettlesSimulated(t *testing.T) {
	h := newM5Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, _, _ := h.deployLivePrivate(issuerSession, "FIATL", "HN", 1.0)

	invSession, _, _ := h.verifiedInvestor(genPriv(t), "Investor", "HN", "ret")
	sub := h.do("POST", "/api/issuances/"+issID+"/subscribe", invSession, map[string]any{
		"rail": "card", "amount": 50,
	})
	if sub.code != 200 {
		t.Fatalf("subscribe card: %d %s", sub.code, sub.errMsg())
	}
	if fs, _ := sub.body["funds_simulated"].(bool); !fs {
		t.Fatalf("fiat subscribe must be flagged funds_simulated: %s", sub.raw)
	}
	checkout, _ := sub.body["checkout"].(map[string]any)
	if lbl, _ := checkout["label"].(string); !strings.Contains(lbl, "SIMULATED") {
		t.Fatalf("checkout label must carry SIMULATED: %v", checkout["label"])
	}
	fiatID, _ := checkout["id"].(string)
	subObj, _ := sub.body["subscription"].(map[string]any)
	subID, _ := subObj["id"].(string)

	// The simulated processor settles due payments (fiatSettle=0 -> immediately).
	h.s.settleFiatDue()

	fs := h.do("GET", "/api/fiat/"+fiatID, invSession, nil)
	if got, _ := fs.body["state"].(string); got != "settled" {
		t.Fatalf("fiat state = %q, want settled", got)
	}
	if got, _ := fs.body["funds_simulated"].(bool); !got {
		t.Fatalf("fiat status must stay funds_simulated")
	}
	got, _ := h.s.st.SubscriptionByID(subID)
	if got.State != "in_escrow" {
		t.Fatalf("fiat subscription state = %q, want in_escrow", got.State)
	}
	if !got.FundsSimulated {
		t.Fatalf("fiat-funded subscription must carry FundsSimulated")
	}
	// The escrow ledger deposit entry is flagged simulated.
	ledger, _ := h.s.st.LedgerByIssuance(issID)
	var sawSimDeposit bool
	for _, e := range ledger {
		if e.Kind == "deposit" && e.SubscriptionID == subID {
			if !e.FundsSimulated {
				t.Fatalf("fiat deposit ledger entry must be funds_simulated")
			}
			sawSimDeposit = true
		}
	}
	if !sawSimDeposit {
		t.Fatalf("no simulated deposit ledger entry for the fiat subscription")
	}
}

// ===========================================================================
// 2. Offer-side gates
// ===========================================================================

func TestM5UKStatementGatesOffering(t *testing.T) {
	h := newM5Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "GB")
	issID := h.createIssuance(issuerSession, "UK Offering", "UKGT", map[string]any{
		"promotable_jurisdictions": []string{"GB"},
		"sector":                   "energy",
		"price":                    1.0,
	})

	ukSession, ukAID, _ := h.register(genPriv(t), "UK Investor", "GB")
	h.verifiedClaims(ukAID, &Claims{Residence: "GB", BaseEligibility: "ret"})

	// Without the SI 2024/301 statement the full offering must not render.
	off := h.do("GET", "/api/issuances/"+issID+"/offering", ukSession, nil)
	if off.code != 200 {
		t.Fatalf("offering: %d %s", off.code, off.errMsg())
	}
	if gated, _ := off.body["gated"].(bool); !gated {
		t.Fatalf("UK offering rendered without the statutory statement: %s", off.raw)
	}
	if _, hasPrice := off.body["price"]; hasPrice {
		t.Fatalf("gated offering must not leak price")
	}
	if !hasRequirement(off, "uk_statement") {
		t.Fatalf("expected a uk_statement requirement, got %s", off.raw)
	}

	// Record the self-certified sophisticated statement.
	g := h.do("POST", "/api/issuances/"+issID+"/gate", ukSession, map[string]any{
		"kind": "uk_statement", "basis": "soph",
	})
	if g.code != 200 {
		t.Fatalf("gate uk_statement: %d %s", g.code, g.errMsg())
	}

	// Now the full offering renders.
	off = h.do("GET", "/api/issuances/"+issID+"/offering", ukSession, nil)
	if gated, _ := off.body["gated"].(bool); gated {
		t.Fatalf("offering still gated after the statement: %s", off.raw)
	}
	if _, hasPrice := off.body["price"]; !hasPrice {
		t.Fatalf("granted offering must expose the price")
	}
}

func TestM5HNWStatementRejectsBelowThreshold(t *testing.T) {
	h := newM5Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "GB")
	issID := h.createIssuance(issuerSession, "UK Offering", "UKHN", map[string]any{
		"promotable_jurisdictions": []string{"GB"}, "price": 1.0,
	})
	ukSession, ukAID, _ := h.register(genPriv(t), "UK Investor", "GB")
	h.verifiedClaims(ukAID, &Claims{Residence: "GB", BaseEligibility: "ret"})

	// hnw basis below both GBP 100k income and GBP 250k net assets must be refused.
	g := h.do("POST", "/api/issuances/"+issID+"/gate", ukSession, map[string]any{
		"kind": "uk_statement", "basis": "hnw", "income_gbp": 50000, "net_assets": 100000,
	})
	if g.code != 400 {
		t.Fatalf("sub-threshold hnw statement = %d, want 400 (%s)", g.code, g.raw)
	}
}

// TestM5OffereeCountingMechanism verifies the EU offeree counter as the code
// currently enforces it: distinct-per-member-state dedup, an approaching-cap
// warning, a hard block once the cap is reached, and that an already-counted
// person still passes at the cap. It pins the boundary the code actually
// enforces. NOTE: that boundary is off by one against the Prospectus Regulation
// sub-150 exemption (see TestM5OffereeCapOffByOne_KnownDefect and the report):
// the code admits 150 offerees and blocks the 151st, where "fewer than 150"
// requires blocking the 150th.
func TestM5OffereeCountingMechanism(t *testing.T) {
	h := newM5Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "DE")
	issID := h.createIssuance(issuerSession, "DE Offering", "DEOF", map[string]any{
		"promotable_jurisdictions": []string{"DE"}, "price": 1.0,
	})

	// Seed 148 distinct offerees, then a real DE-retail investor is the 149th:
	// granted, with an approaching-cap warning.
	for i := 0; i < 148; i++ {
		if _, err := h.s.st.RecordOfferee(issID, "DE", fmt.Sprintf("seed-aid-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	s149, a149, _ := h.register(genPriv(t), "DE 149", "DE")
	h.verifiedClaims(a149, &Claims{Residence: "DE", BaseEligibility: "ret"})
	off := h.do("GET", "/api/issuances/"+issID+"/offering", s149, nil)
	if gated, _ := off.body["gated"].(bool); gated {
		t.Fatalf("the 149th DE offeree was blocked: %s", off.raw)
	}
	if _, ok := off.body["offeree_warning"]; !ok {
		t.Fatalf("the 149th DE offeree should carry an approaching-cap warning: %s", off.raw)
	}
	if n, _ := h.s.st.OffereeCount(issID, "DE"); n != 149 {
		t.Fatalf("offeree count after the 149th = %d, want 149", n)
	}

	// Re-viewing as the same person is idempotent (distinct-per-aid dedup).
	off = h.do("GET", "/api/issuances/"+issID+"/offering", s149, nil)
	if gated, _ := off.body["gated"].(bool); gated {
		t.Fatalf("an already-counted offeree must pass: %s", off.raw)
	}
	if n, _ := h.s.st.OffereeCount(issID, "DE"); n != 149 {
		t.Fatalf("re-view double-counted: count = %d, want 149", n)
	}

	// The count is now at the cap (149). The NEXT distinct offeree (the 150th) is
	// hard-blocked with an offeree_cap requirement and is not counted, so at most
	// 149 non-qualified offerees per member state are admitted (Prospectus Reg. Art
	// 1(4)(b): "fewer than 150").
	sNext, aNext, _ := h.register(genPriv(t), "DE next", "DE")
	h.verifiedClaims(aNext, &Claims{Residence: "DE", BaseEligibility: "ret"})
	off = h.do("GET", "/api/issuances/"+issID+"/offering", sNext, nil)
	if gated, _ := off.body["gated"].(bool); !gated {
		t.Fatalf("the 150th distinct DE offeree was not blocked: %s", off.raw)
	}
	if !hasRequirement(off, "offeree_cap") {
		t.Fatalf("a capped offeree must get an offeree_cap block: %s", off.raw)
	}
	if n, _ := h.s.st.OffereeCount(issID, "DE"); n != 149 {
		t.Fatalf("a blocked offeree must not be counted; count = %d, want 149", n)
	}

	// A qualified (professional) investor is exempt from counting entirely.
	sPro, aPro, _ := h.register(genPriv(t), "DE pro", "DE")
	h.verifiedClaims(aPro, &Claims{Residence: "DE", BaseEligibility: "pro"})
	off = h.do("GET", "/api/issuances/"+issID+"/offering", sPro, nil)
	if gated, _ := off.body["gated"].(bool); gated {
		t.Fatalf("a professional investor must not be blocked by the offeree cap: %s", off.raw)
	}
	if n, _ := h.s.st.OffereeCount(issID, "DE"); n != 149 {
		t.Fatalf("a qualified investor must not be counted; count = %d, want 149", n)
	}
}

// TestM5OffereeCap150Blocked pins the regulator-correct boundary: the 150th
// distinct non-qualified offeree per member state is blocked (Prospectus
// Regulation (EU) 2017/1129 Art 1(4)(b): "fewer than 150" = at most 149).
func TestM5OffereeCap150Blocked(t *testing.T) {
	h := newM5Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "DE")
	issID := h.createIssuance(issuerSession, "DE Offering", "DEOF", map[string]any{
		"promotable_jurisdictions": []string{"DE"}, "price": 1.0,
	})
	for i := 0; i < 149; i++ {
		if _, err := h.s.st.RecordOfferee(issID, "DE", fmt.Sprintf("seed-aid-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	s150, a150, _ := h.register(genPriv(t), "DE 150", "DE")
	h.verifiedClaims(a150, &Claims{Residence: "DE", BaseEligibility: "ret"})
	off := h.do("GET", "/api/issuances/"+issID+"/offering", s150, nil)
	if gated, _ := off.body["gated"].(bool); !gated {
		t.Fatalf("the 150th DE offeree must be blocked (fewer than 150): %s", off.raw)
	}
}

func TestM5IneligibleInvestorCannotSubscribe(t *testing.T) {
	h := newM5Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, _, _ := h.deployLivePrivate(issuerSession, "ELIG", "HN", 1.0)

	// A FR investor holds j:FR:ret; the asset admits only j:HN:*. Not eligible.
	frSession, _, _ := h.verifiedInvestor(genPriv(t), "FR Investor", "FR", "ret")
	sub := h.do("POST", "/api/issuances/"+issID+"/subscribe", frSession, map[string]any{
		"rail": "usdx", "amount": 10, "refund_address": "seq-refund",
	})
	if sub.code != 403 {
		t.Fatalf("ineligible subscribe = %d, want 403 (%s)", sub.code, sub.raw)
	}
}

func TestM5SourceOfFundsRequiredAboveThreshold(t *testing.T) {
	h := newM5Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, _, _ := h.deployLivePrivate(issuerSession, "SOFL", "HN", 1.0)

	invSession, _, _ := h.verifiedInvestor(genPriv(t), "Investor", "HN", "ret")

	// $15,000 subscription (>= $10k) requires a source-of-funds declaration.
	sub := h.do("POST", "/api/issuances/"+issID+"/subscribe", invSession, map[string]any{
		"rail": "usdx", "amount": 15000, "refund_address": "seq-refund",
	})
	if sub.code != 403 {
		t.Fatalf("large subscribe without SoF = %d, want 403 (%s)", sub.code, sub.raw)
	}
	if !hasRequirementBody(sub, "sof") {
		t.Fatalf("expected an sof requirement, got %s", sub.raw)
	}

	// Below threshold ($5,000) subscribes without SoF.
	small := h.do("POST", "/api/issuances/"+issID+"/subscribe", invSession, map[string]any{
		"rail": "usdx", "amount": 5000, "refund_address": "seq-refund",
	})
	if small.code != 200 {
		t.Fatalf("sub-threshold subscribe = %d, want 200 (%s)", small.code, small.raw)
	}

	// After recording SoF the large subscription proceeds.
	g := h.do("POST", "/api/issuances/"+issID+"/gate", invSession, map[string]any{
		"kind": "sof", "source": "salary and savings", "amount_usd": 15000,
	})
	if g.code != 200 {
		t.Fatalf("gate sof: %d %s", g.code, g.errMsg())
	}
	sub = h.do("POST", "/api/issuances/"+issID+"/subscribe", invSession, map[string]any{
		"rail": "usdx", "amount": 15000, "refund_address": "seq-refund",
	})
	if sub.code != 200 {
		t.Fatalf("subscribe after SoF = %d, want 200 (%s)", sub.code, sub.raw)
	}
}

// ===========================================================================
// 3. Closing engine
// ===========================================================================

// escrowSub injects an in_escrow subscription directly (bypassing the deposit
// path) so the closing-engine tests focus on delivery/release/refund.
func (h *m5h) escrowSub(issID, investorAID, rail string, tokenAtoms, depositedAtoms uint64, refund string) string {
	h.t.Helper()
	sub := &Subscription{
		ID: mustID(), IssuanceID: issID, InvestorAID: investorAID, Rail: rail,
		TokenAtoms: tokenAtoms, PayAmount: depositedAtoms, PayCcy: "USDX",
		DepositAddress: "dep-" + rail, RefundAddress: refund, State: "in_escrow",
		DepositTxid: "deptxid", Confirmations: 6, DepositedAtoms: depositedAtoms,
	}
	if err := h.s.st.InsertSubscription(sub); err != nil {
		h.t.Fatal(err)
	}
	return sub.ID
}

func (h *m5h) liveEnv(t *testing.T) (issuerSession, issuerXOnly, issuerPriv, issID, assetID, escrowAID string) {
	issuerPriv = genPriv(t)
	issuerSession, _, issuerXOnly = h.register(issuerPriv, "Issuer", "HN")
	issID, assetID, escrowAID = h.deployLivePrivate(issuerSession, "CLOSE", "HN", 1.0)
	if err := h.s.st.UpsertMandate(&PayoutMandate{IssuanceID: issID, Chain: "sequentia", Address: "seq-issuer-payout"}); err != nil {
		t.Fatal(err)
	}
	return
}

func TestM5CloseDeliversAndReleases(t *testing.T) {
	h := newM5Harness(t, m5opts{})
	issuerSession, issuerXOnly, issuerPriv, issID, assetID, _ := h.liveEnv(t)

	invSession, invAID, _ := h.verifiedInvestor(genPriv(t), "Investor", "HN", "ret")
	_ = invSession
	depAtoms := uint64(200 * 1e8)
	subID := h.escrowSub(issID, invAID, "usdx", uint64(200*1e2), depAtoms, "seq-refund")

	h.closeAs(issuerSession, "", issuerXOnly, issID, issuerPriv)

	// Exactly one delivery (policy-co-signed transfer) and one release.
	if h.oa.transfers() != 1 {
		t.Fatalf("deliveries = %d, want 1", h.oa.transfers())
	}
	if h.seq.sendCount() != 1 {
		t.Fatalf("releases = %d, want 1", h.seq.sendCount())
	}
	// Delivery credited the investor enclave the purchased token amount.
	h.oa.mu.Lock()
	delivered := len(h.oa.deliveries) == 1 && h.oa.deliveries[0].recipient == invAID &&
		h.oa.deliveries[0].asset == assetID && h.oa.deliveries[0].atoms == uint64(200*1e2)
	h.oa.mu.Unlock()
	if !delivered {
		t.Fatalf("delivery did not carry (recipient=%s asset=%s atoms=%d): %+v", invAID, assetID, uint64(200*1e2), h.oa.deliveries)
	}
	set, _ := h.s.st.SettlementByID(subID)
	if set.State != "settled" {
		t.Fatalf("settlement state = %q, want settled", set.State)
	}
}

func TestM5CloseIsIdempotent(t *testing.T) {
	h := newM5Harness(t, m5opts{})
	issuerSession, issuerXOnly, issuerPriv, issID, _, escrowAID := h.liveEnv(t)
	_ = issuerSession
	_ = issuerXOnly
	_ = issuerPriv

	_, invAID, _ := h.verifiedInvestor(genPriv(t), "Investor", "HN", "ret")
	depAtoms := uint64(100 * 1e8)
	subID := h.escrowSub(issID, invAID, "usdx", uint64(100*1e2), depAtoms, "seq-refund")

	iss, _ := h.s.st.IssuanceByID(issID)
	escrow, _ := h.s.st.EnclaveKeyByRef(enclaveOfferingEscrow, issID)
	if escrow == nil || escrow.AID != escrowAID {
		t.Fatalf("escrow enclave mismatch")
	}
	sub, _ := h.s.st.SubscriptionByID(subID)

	// Replay settleOne three times: the settlement record is the single source of
	// truth, so exactly one delivery and one release happen.
	for i := 0; i < 3; i++ {
		res := h.s.settleOne(iss, escrow, sub, 100000)
		if st, _ := res["state"].(string); st != "settled" {
			t.Fatalf("settleOne #%d state = %q, want settled", i, st)
		}
	}
	if h.oa.transfers() != 1 {
		t.Fatalf("replayed close delivered %d times, want exactly 1", h.oa.transfers())
	}
	if h.seq.sendCount() != 1 {
		t.Fatalf("replayed close released %d times, want exactly 1", h.seq.sendCount())
	}
	// A double-submit of the HTTP close is also a no-op (subs already settled).
	h.closeAs(issuerSession, "", issuerXOnly, issID, issuerPriv)
	if h.oa.transfers() != 1 || h.seq.sendCount() != 1 {
		t.Fatalf("HTTP re-close double-moved money: transfers=%d sends=%d", h.oa.transfers(), h.seq.sendCount())
	}
}

// TestM5ReleaseReconcilesLostWrite proves the fund-safety fix: if the release send
// broadcast but the DB write of its txid was lost (settlement stuck in "releasing",
// no release_txid), a re-close reconciles the prior send by its comment marker and
// does NOT re-broadcast from the commingled escrow wallet.
func TestM5ReleaseReconcilesLostWrite(t *testing.T) {
	h := newM5Harness(t, m5opts{})
	issuerSession, _, _, issID, _, escrowAID := h.liveEnv(t)
	_ = issuerSession
	_, invAID, _ := h.verifiedInvestor(genPriv(t), "Investor", "HN", "ret")
	depAtoms := uint64(100 * 1e8)
	subID := h.escrowSub(issID, invAID, "usdx", uint64(100*1e2), depAtoms, "seq-refund")

	iss, _ := h.s.st.IssuanceByID(issID)
	escrow, _ := h.s.st.EnclaveKeyByRef(enclaveOfferingEscrow, issID)
	if escrow == nil || escrow.AID != escrowAID {
		t.Fatalf("escrow enclave mismatch")
	}
	sub, _ := h.s.st.SubscriptionByID(subID)

	// Simulate the lost write: delivery already done, and the release send already
	// broadcast (seeded in the wallet with its marker) but state stuck in "releasing"
	// with no recorded release_txid.
	if _, err := h.s.st.CreateSettlementIfAbsent(subID, issID); err != nil {
		t.Fatal(err)
	}
	_ = h.s.st.UpdateSettlementFields(subID, map[string]any{"delivery_txid": "reconciled", "state": "releasing"})
	net := depAtoms - depAtoms*uint64(h.s.cfg.escrowFeeBps)/10000
	h.seq.seedSend("seq-issuer-payout", net, "seqpal-rel-"+subID, "deadbeefcafe")

	before := h.seq.sendCount()
	res := h.s.settleOne(iss, escrow, sub, 100000)
	if st, _ := res["state"].(string); st != "settled" {
		t.Fatalf("reconciled settleOne state = %q, want settled", st)
	}
	if h.seq.sendCount() != before {
		t.Fatalf("release re-broadcast on a lost-write re-close: sends %d -> %d (must reconcile, not re-send)", before, h.seq.sendCount())
	}
	set, _ := h.s.st.SettlementByID(subID)
	if set.ReleaseTxid != "deadbeefcafe" {
		t.Fatalf("reconciled release_txid = %q, want the pre-broadcast txid", set.ReleaseTxid)
	}
}

func TestM5CloseRefundsOnDeliveryFailure(t *testing.T) {
	h := newM5Harness(t, m5opts{})
	issuerSession, issuerXOnly, issuerPriv, issID, _, _ := h.liveEnv(t)

	_, invAID, _ := h.verifiedInvestor(genPriv(t), "Investor", "HN", "ret")
	depAtoms := uint64(100 * 1e8)
	subID := h.escrowSub(issID, invAID, "usdx", uint64(100*1e2), depAtoms, "seq-refund-addr")

	// Delivery is refused upstream; the balance shows no delivery, so the close
	// must refund the full deposit to the captured refund address.
	h.oa.failTransfers = true

	h.closeAs(issuerSession, "", issuerXOnly, issID, issuerPriv)

	if h.oa.transfers() != 0 {
		t.Fatalf("a failed delivery must not count a transfer, got %d", h.oa.transfers())
	}
	// One send: the refund (no release on a failed close).
	if h.seq.sendCount() != 1 {
		t.Fatalf("refund sends = %d, want 1", h.seq.sendCount())
	}
	send, _ := h.seq.lastSend()
	if send.address != "seq-refund-addr" || send.atoms != depAtoms {
		t.Fatalf("refund send = %+v, want full deposit %d to seq-refund-addr", send, depAtoms)
	}
	sub, _ := h.s.st.SubscriptionByID(subID)
	if sub.State != "refunded" {
		t.Fatalf("subscription state = %q, want refunded", sub.State)
	}
	set, _ := h.s.st.SettlementByID(subID)
	if set.State != "refunded" || set.RefundTxid == "" {
		t.Fatalf("settlement = %+v, want refunded with a refund txid", set)
	}
}

func TestM5CloseReconcilesWithoutDoubleBroadcast(t *testing.T) {
	h := newM5Harness(t, m5opts{})
	issuerSession, issuerXOnly, issuerPriv, issID, assetID, _ := h.liveEnv(t)
	_ = issuerSession
	_ = issuerXOnly
	_ = issuerPriv

	_, invAID, _ := h.verifiedInvestor(genPriv(t), "Investor", "HN", "ret")
	tokenAtoms := uint64(100 * 1e2)
	depAtoms := uint64(100 * 1e8)
	subID := h.escrowSub(issID, invAID, "usdx", tokenAtoms, depAtoms, "seq-refund")

	// Simulate a crash mid-delivery: the settlement is 'delivering' with no txid,
	// and the tokens ALREADY landed in the investor enclave (a prior attempt
	// broadcast but the txid was lost). Reconciliation must detect this via the
	// balance scan and NOT re-broadcast the delivery.
	if _, err := h.s.st.CreateSettlementIfAbsent(subID, issID); err != nil {
		t.Fatal(err)
	}
	if err := h.s.st.UpdateSettlementFields(subID, map[string]any{"state": "delivering"}); err != nil {
		t.Fatal(err)
	}
	h.oa.setBalance(invAID, assetID, tokenAtoms)

	iss, _ := h.s.st.IssuanceByID(issID)
	escrow, _ := h.s.st.EnclaveKeyByRef(enclaveOfferingEscrow, issID)
	sub, _ := h.s.st.SubscriptionByID(subID)
	res := h.s.settleOne(iss, escrow, sub, 100000)

	if st, _ := res["state"].(string); st != "settled" {
		t.Fatalf("reconciled settleOne state = %q, want settled", st)
	}
	if h.oa.transfers() != 0 {
		t.Fatalf("reconciliation re-broadcast the delivery (%d transfers), want 0", h.oa.transfers())
	}
	// Release still happens exactly once.
	if h.seq.sendCount() != 1 {
		t.Fatalf("release sends = %d, want 1", h.seq.sendCount())
	}
	set, _ := h.s.st.SettlementByID(subID)
	if set.DeliveryTxid != "reconciled" {
		t.Fatalf("delivery txid = %q, want reconciled", set.DeliveryTxid)
	}
}

func TestM5USTrancheVestingStampedAtClose(t *testing.T) {
	h := newM5Harness(t, m5opts{})
	issuerSession, issuerXOnly, issuerPriv, issID, assetID, _ := h.liveEnv(t)

	// A US-person investor. maybeStampVesting reads the claims and stamps a
	// per-AID Rule 144 vesting window into the asset rules at close.
	_, invAID, invXOnly := h.register(genPriv(t), "US Investor", "US")
	if _, err := h.s.registerUser(invXOnly); err != nil {
		t.Fatal(err)
	}
	h.verifiedClaims(invAID, &Claims{Residence: "US", BaseEligibility: "ret", USPerson: true})

	tokenAtoms := uint64(100 * 1e2)
	h.escrowSub(issID, invAID, "usdx", tokenAtoms, uint64(100*1e8), "seq-refund")

	h.closeAs(issuerSession, "", issuerXOnly, issID, issuerPriv)

	rules := h.oa.rulesFor(assetID)
	if len(rules) == 0 {
		t.Fatalf("no rules.Vesting was written for the US-tranche purchaser")
	}
	var parsed map[string]any
	if err := json.Unmarshal(rules, &parsed); err != nil {
		t.Fatal(err)
	}
	vesting, _ := parsed["vesting"].([]any)
	found := false
	for _, v := range vesting {
		m, _ := v.(map[string]any)
		if aid, _ := m["aid"].(string); aid == invAID {
			found = true
			if uh, _ := m["until_height"].(float64); int64(uh) != 100000+144*365 {
				t.Fatalf("vesting until_height = %v, want %d", uh, 100000+144*365)
			}
		}
	}
	if !found {
		t.Fatalf("no vesting entry for the US investor AID %s in %s", invAID, string(rules))
	}
}

// ===========================================================================
// 4. Payout mandate
// ===========================================================================

func TestM5MandateRejectsEnclaveKeyPathAddress(t *testing.T) {
	h := newM5Harness(t, m5opts{})
	issuerPriv := genPriv(t)
	issuerSession, _, _ := h.register(issuerPriv, "Issuer", "HN")
	issID, _, escrowAID := h.deployLivePrivate(issuerSession, "MAND", "HN", 1.0)

	// The escrow enclave's receive address (the stub returns tb1p+aid) must be
	// rejected as a payout target: no wallet scans a 2-of-2 enclave script.
	enclaveAddr := "tb1p" + escrowAID
	m := h.do("POST", "/api/issuances/"+issID+"/mandate", issuerSession, map[string]any{
		"chain": "sequentia", "address": enclaveAddr,
	})
	if m.code != 400 {
		t.Fatalf("enclave-address mandate = %d, want 400 (%s)", m.code, m.raw)
	}
	if !strings.Contains(strings.ToLower(m.errMsg()), "enclave") {
		t.Fatalf("rejection should name the enclave address: %s", m.raw)
	}
}

func TestM5MandateAcceptsValidBIP340(t *testing.T) {
	h := newM5Harness(t, m5opts{})
	issuerPriv := genPriv(t)
	issuerSession, _, issuerXOnly := h.register(issuerPriv, "Issuer", "HN")
	issID, _, _ := h.deployLivePrivate(issuerSession, "MAND2", "HN", 1.0)

	addr := "seq-ordinary-payout"
	// Step 1: fetch the canonical bytes to sign.
	pre := h.do("POST", "/api/issuances/"+issID+"/mandate", issuerSession, map[string]any{
		"chain": "sequentia", "address": addr,
	})
	if pre.code != 200 {
		t.Fatalf("mandate (sign_this): %d %s", pre.code, pre.errMsg())
	}
	signThis, _ := pre.body["sign_this"].(string)
	tag, _ := pre.body["tag"].(string)
	if signThis == "" || tag == "" {
		t.Fatalf("mandate did not return sign_this/tag: %s", pre.raw)
	}

	// Step 2: sign and resubmit.
	sig := signCanonical(t, issuerPriv, tag, signThis)
	fin := h.do("POST", "/api/issuances/"+issID+"/mandate", issuerSession, map[string]any{
		"chain": "sequentia", "address": addr, "signature": sig, "signer_xonly": issuerXOnly,
	})
	if fin.code != 200 {
		t.Fatalf("signed mandate = %d, want 200 (%s)", fin.code, fin.raw)
	}
	m, err := h.s.st.MandateFor(issID, "sequentia")
	if err != nil || m == nil || m.Address != addr {
		t.Fatalf("mandate not stored: %v %+v", err, m)
	}

	// A bad signature is refused.
	bad := h.do("POST", "/api/issuances/"+issID+"/mandate", issuerSession, map[string]any{
		"chain": "sequentia", "address": addr, "signature": strings.Repeat("00", 64), "signer_xonly": issuerXOnly,
	})
	if bad.code != 400 {
		t.Fatalf("bad-signature mandate = %d, want 400", bad.code)
	}
}

// ===========================================================================
// 5. Platform fee gate
// ===========================================================================

func TestM5UnpaidSetupFeeBlocksDeploy(t *testing.T) {
	h := newM5Harness(t, m5opts{setupFeeUSD: 500})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	terms := map[string]any{
		"jurisdictions": map[string]any{"HN": map[string]any{"access": "standard"}}, "price": 1.0,
	}
	issID := h.createIssuance(issuerSession, "Fee Co", "FEE", terms)

	// Deploy blocked (402) until the setup fee is paid.
	dep := h.do("POST", "/api/deploy", issuerSession, map[string]any{
		"issuance_id": issID, "supply": 1000000, "precision": 2,
	})
	if dep.code != 402 {
		t.Fatalf("deploy with unpaid setup fee = %d, want 402 (%s)", dep.code, dep.raw)
	}

	// Pay the setup fee on the SIMULATED fiat rail; the cron settles it.
	pay := h.do("POST", "/api/issuances/"+issID+"/fees/pay", issuerSession, map[string]any{
		"kind": "setup", "rail": "card",
	})
	if pay.code != 200 {
		t.Fatalf("fees/pay: %d %s", pay.code, pay.errMsg())
	}
	h.s.settleFiatDue()
	if paid, _ := h.s.setupFeePaid(issID); !paid {
		t.Fatalf("setup fee did not settle to paid")
	}

	// Deploy now proceeds.
	dep = h.do("POST", "/api/deploy", issuerSession, map[string]any{
		"issuance_id": issID, "supply": 1000000, "precision": 2,
	})
	if dep.code != 200 {
		t.Fatalf("deploy after paying the setup fee = %d, want 200 (%s)", dep.code, dep.raw)
	}
}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

func hasRequirement(r resp, kind string) bool {
	reqs, _ := r.body["requirements"].([]any)
	for _, x := range reqs {
		m, _ := x.(map[string]any)
		if k, _ := m["kind"].(string); k == kind {
			return true
		}
	}
	return false
}

func hasRequirementBody(r resp, kind string) bool {
	return hasRequirement(r, kind)
}
