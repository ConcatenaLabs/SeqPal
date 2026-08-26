package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

// M10 tests: enforcement elections + bearer (supervised) deploy, the
// supervision freeze/unfreeze console, corporate actions (holding proofs,
// dividends, votes), the W-6 deposit-time fee accrual, and the W-7 fail-closed
// characterization. They extend the M5-M9 harness: the openampd stub h.oa, the
// JSON-RPC node stub h.seq (extended here, via fakeNode.extra, with the raw
// issuance + supervision surface), and a new electrs stub for the snapshot
// walker.

// ---------------------------------------------------------------------------
// node stub extension: raw issuance + supervision (symbolic transactions)
// ---------------------------------------------------------------------------

// supTx is a symbolic transaction: seqpald treats tx hex opaquely, so the stub
// hands out handles instead of real serializations and tracks structure.
type supTx struct {
	txid  string
	vins  []map[string]any
	vouts []supVout
}

type supVout struct {
	kind   string // data | addr | fee | change | decl | asset | token | record
	script string
}

type supNode struct {
	n           *fakeNode
	handles     map[string]*supTx
	hn          int
	fundN       int
	assets      map[string]map[string]string // asset -> {op, rec, pause}
	frozen      map[string]map[string]int    // asset -> targethash -> records
	broadcasts  int                          // sendrawtransaction calls
	submits     int                          // submitsupervisionrecord calls
	failPrivate bool
	recHashN    int // getsupervisionrecordhash calls
}

func newSupNode(n *fakeNode) *supNode {
	sn := &supNode{
		n: n, handles: map[string]*supTx{},
		assets: map[string]map[string]string{},
		frozen: map[string]map[string]int{},
	}
	n.mu.Lock()
	n.extra = sn.dispatch
	n.mu.Unlock()
	return sn
}

func (sn *supNode) newHandle(tx *supTx) string {
	sn.hn++
	h := fmt.Sprintf("tx-handle-%d", sn.hn)
	if tx.txid == "" {
		tx.txid = sha256Hex([]byte("txid|" + h))
	}
	sn.handles[h] = tx
	return h
}

func (sn *supNode) tx(handle string) *supTx { return sn.handles[handle] }

func supTargetHash(target string) string { return sha256Hex([]byte("target|" + target)) }

func supAssetFor(txid string, vout int64, op, rec, contract string, pause bool) string {
	return sha256Hex([]byte(fmt.Sprintf("supasset|%s|%d|%s|%s|%s|%v", txid, vout, op, rec, contract, pause)))
}

func supDeclScript(asset string) string { return "6a5350" + asset }

// dispatch runs under n.mu (called from fakeNode.dispatch's default branch).
func (sn *supNode) dispatch(method string, params []json.RawMessage) (any, int, string, bool) {
	str := func(i int) string {
		var s string
		if i < len(params) {
			json.Unmarshal(params[i], &s)
		}
		return s
	}
	num := func(i int) int64 {
		var v int64
		if i < len(params) {
			json.Unmarshal(params[i], &v)
		}
		return v
	}
	boolean := func(i int) bool {
		var v bool
		if i < len(params) {
			json.Unmarshal(params[i], &v)
		}
		return v
	}
	switch method {
	case "createrawtransaction":
		var inputs []map[string]any
		json.Unmarshal(params[0], &inputs)
		var outputs map[string]json.RawMessage
		json.Unmarshal(params[1], &outputs)
		tx := &supTx{}
		tx.vins = append(tx.vins, inputs...)
		// data/addr outputs first, fee LAST (as consensus requires).
		for k := range outputs {
			switch k {
			case "data":
				tx.vouts = append(tx.vouts, supVout{kind: "data"})
			case "fee":
			default:
				tx.vouts = append(tx.vouts, supVout{kind: "addr", script: "addr:" + k})
			}
		}
		if _, ok := outputs["fee"]; ok {
			tx.vouts = append(tx.vouts, supVout{kind: "fee"})
		}
		return sn.newHandle(tx), 0, "", true
	case "fundrawtransaction":
		base := sn.tx(str(0))
		if base == nil {
			return nil, -8, "unknown tx handle", true
		}
		sn.fundN++
		tx := &supTx{vins: append([]map[string]any{}, base.vins...)}
		tx.vins = append(tx.vins, map[string]any{"txid": fmt.Sprintf("%064x", 0xf00000+sn.fundN), "vout": 0})
		tx.vouts = append(tx.vouts, base.vouts...)
		tx.vouts = append(tx.vouts, supVout{kind: "change"}, supVout{kind: "fee"})
		return map[string]any{"hex": sn.newHandle(tx), "fee": 0.0001, "changepos": 1}, 0, "", true
	case "decoderawtransaction":
		tx := sn.tx(str(0))
		if tx == nil {
			return nil, -22, "TX decode failed", true
		}
		vouts := []map[string]any{}
		for _, v := range tx.vouts {
			vouts = append(vouts, map[string]any{"scriptPubKey": map[string]any{"hex": v.script}})
		}
		return map[string]any{"txid": tx.txid, "vin": tx.vins, "vout": vouts}, 0, "", true
	case "dumpassetlabels":
		return map[string]any{"bitcoin": strings.Repeat("c8", 32)}, 0, "", true
	case "getsupervisedassetid":
		asset := supAssetFor(str(0), num(1), str(2), str(3), str(4), boolean(5))
		return map[string]any{
			"asset": asset, "token": sha256Hex([]byte("tok|" + asset)),
			"entropy": sha256Hex([]byte("ent|" + asset)), "declarationscript": supDeclScript(asset),
		}, 0, "", true
	case "rawissueasset":
		base := sn.tx(str(0))
		if base == nil {
			return nil, -22, "TX decode failed", true
		}
		if len(base.vouts) == 0 || base.vouts[len(base.vouts)-1].kind != "fee" {
			return nil, -8, "Last transaction output must be fee.", true
		}
		var issuances []struct {
			ContractHash string `json:"contract_hash"`
			Supervision  *struct {
				OperationalKey string `json:"operationalkey"`
				RecoveryKey    string `json:"recoverykey"`
				Pause          bool   `json:"pause"`
			} `json:"supervision"`
			Blind       bool        `json:"blind"`
			TokenAmount json.Number `json:"token_amount"`
		}
		json.Unmarshal(params[1], &issuances)
		if len(issuances) == 0 {
			return nil, -8, "no issuances", true
		}
		iss := issuances[0]
		if iss.Supervision == nil {
			return nil, -8, "test stub only issues supervised assets", true
		}
		if iss.Blind {
			return nil, -8, "a supervised asset cannot be blinded", true
		}
		if atomsFromNumber(iss.TokenAmount) == 0 {
			return nil, -8, "a supervised issuance must create reissuance tokens", true
		}
		var vin0Txid string
		var vin0Vout int64
		if len(base.vins) > 0 {
			vin0Txid, _ = base.vins[0]["txid"].(string)
			if f, ok := base.vins[0]["vout"].(float64); ok {
				vin0Vout = int64(f)
			}
		}
		asset := supAssetFor(vin0Txid, vin0Vout, iss.Supervision.OperationalKey, iss.Supervision.RecoveryKey, iss.ContractHash, iss.Supervision.Pause)
		sn.assets[asset] = map[string]string{
			"op": iss.Supervision.OperationalKey, "rec": iss.Supervision.RecoveryKey,
			"pause": fmt.Sprintf("%v", iss.Supervision.Pause),
		}
		tx := &supTx{vins: base.vins}
		tx.vouts = append(tx.vouts, supVout{kind: "decl", script: supDeclScript(asset)})
		tx.vouts = append(tx.vouts, base.vouts[:len(base.vouts)-1]...)
		tx.vouts = append(tx.vouts, supVout{kind: "asset"}, supVout{kind: "token"}, supVout{kind: "fee"})
		return []map[string]any{{
			"hex": sn.newHandle(tx), "vin": 0, "asset": asset,
			"token": sha256Hex([]byte("tok|" + asset)), "entropy": sha256Hex([]byte("ent|" + asset)),
		}}, 0, "", true
	case "signrawtransactionwithwallet":
		base := sn.tx(str(0))
		if base == nil {
			return nil, -22, "TX decode failed", true
		}
		signed := &supTx{txid: base.txid, vins: base.vins, vouts: base.vouts}
		return map[string]any{"hex": sn.newHandle(signed), "complete": true}, 0, "", true
	case "testmempoolaccept":
		var hexes []string
		json.Unmarshal(params[0], &hexes)
		out := []map[string]any{}
		for _, h := range hexes {
			tx := sn.tx(h)
			if tx == nil {
				out = append(out, map[string]any{"allowed": false, "reject-reason": "decode failed"})
				continue
			}
			out = append(out, map[string]any{"txid": tx.txid, "allowed": true})
		}
		return out, 0, "", true
	case "sendrawtransaction":
		tx := sn.tx(str(0))
		if tx == nil {
			return nil, -22, "TX decode failed", true
		}
		sn.broadcasts++
		sn.applyRecordEffects(tx)
		return tx.txid, 0, "", true
	case "submitsupervisionrecord":
		if sn.failPrivate {
			return nil, -1, "private channel unavailable", true
		}
		tx := sn.tx(str(0))
		if tx == nil {
			return nil, -22, "TX decode failed", true
		}
		hasRecord := false
		for _, v := range tx.vouts {
			if v.kind == "record" {
				hasRecord = true
			}
		}
		if !hasRecord {
			return nil, -8, "this channel carries supervision records only", true
		}
		sn.submits++
		sn.applyRecordEffects(tx)
		return map[string]any{"txid": tx.txid, "queued": sn.submits}, 0, "", true
	case "lockunspent":
		return true, 0, "", true
	case "getsupervisionrecordhash":
		sn.recHashN++
		return map[string]any{
			"sighash":  sha256Hex([]byte(fmt.Sprintf("recmsg|%s|%s|%s|%s|%d", str(0), str(1), str(2), str(4), num(5)))),
			"signwith": "operational",
		}, 0, "", true
	case "buildsupervisionrecord":
		sig := str(4)
		if len(sig) != 128 {
			return nil, -8, "signature must be a 64-byte BIP340 signature in hex", true
		}
		th := supTargetHash(str(2))
		return map[string]any{"script": "6a5245" + str(1) + th, "targethash": th}, 0, "", true
	case "addsupervisionrecordoutput":
		base := sn.tx(str(0))
		if base == nil {
			return nil, -22, "TX decode failed", true
		}
		script, asset := str(1), str(2)
		_ = asset
		tx := &supTx{vins: base.vins}
		// insert before any trailing fee outputs
		at := len(base.vouts)
		for at > 0 && base.vouts[at-1].kind == "fee" {
			at--
		}
		tx.vouts = append(tx.vouts, base.vouts[:at]...)
		tx.vouts = append(tx.vouts, supVout{kind: "record", script: script})
		tx.vouts = append(tx.vouts, base.vouts[at:]...)
		return sn.newHandle(tx), 0, "", true
	case "getsupervisionunfreezehash":
		return sha256Hex([]byte(fmt.Sprintf("unfreeze|%s|%d|%s|%s", str(0), num(1), str(2), str(3)))), 0, "", true
	case "setsupervisionunfreezesig":
		base := sn.tx(str(0))
		if base == nil {
			return nil, -22, "TX decode failed", true
		}
		if len(str(2)) != 128 {
			return nil, -8, "signature must be a 64-byte BIP340 signature in hex", true
		}
		tx := &supTx{txid: base.txid, vins: base.vins, vouts: base.vouts}
		return sn.newHandle(tx), 0, "", true
	case "isassetfrozen":
		asset, target := str(0), str(1)
		th := supTargetHash(target)
		frozen := sn.frozen[asset][th] > 0
		return map[string]any{"frozen": frozen, "paused": false, "freezable": true, "targethash": th}, 0, "", true
	case "getassetfreezes":
		asset := str(0)
		out := []map[string]any{}
		for th, n := range sn.frozen[asset] {
			if n > 0 {
				out = append(out, map[string]any{"targethash": th, "records": n})
			}
		}
		return out, 0, "", true
	case "getsupervisedassets":
		out := []map[string]any{}
		for asset, d := range sn.assets {
			out = append(out, map[string]any{
				"asset": asset, "featurebits": 0,
				"issuedoperationalkey": d["op"], "issuedrecoverykey": d["rec"],
				"operationalkey": d["op"], "recoverykey": d["rec"],
				"pauseallowed": d["pause"] == "true", "paused": false,
				"frozen": len(sn.frozen[asset]),
			})
		}
		return out, 0, "", true
	}
	return nil, 0, "", false
}

// applyRecordEffects registers a broadcast/submitted record output as a live
// freeze in the stub's registry (script = 6a5245 || asset || targethash).
func (sn *supNode) applyRecordEffects(tx *supTx) {
	for _, v := range tx.vouts {
		if v.kind == "record" && strings.HasPrefix(v.script, "6a5245") && len(v.script) == 6+64+64 {
			asset, th := v.script[6:70], v.script[70:]
			if sn.frozen[asset] == nil {
				sn.frozen[asset] = map[string]int{}
			}
			sn.frozen[asset][th]++
		}
	}
}

func (sn *supNode) counts() (broadcasts, submits, recHash int) {
	sn.n.mu.Lock()
	defer sn.n.mu.Unlock()
	return sn.broadcasts, sn.submits, sn.recHashN
}

func (sn *supNode) setFailPrivate(v bool) {
	sn.n.mu.Lock()
	defer sn.n.mu.Unlock()
	sn.failPrivate = v
}

// ---------------------------------------------------------------------------
// electrs stub (the snapshot walker's surface)
// ---------------------------------------------------------------------------

type elVout struct {
	Asset  string
	Script string
	Value  uint64
}

type elTx struct {
	Txid  string
	Vouts []elVout
}

type fakeElectrs struct {
	srv          *httptest.Server
	mu           sync.Mutex
	byAsset      map[string][]elTx // asset index pages (all in one page)
	byTxid       map[string]elTx
	spent        map[string]string // "txid:vout" -> spending txid ("" = spent, spender unknown)
	disableIndex bool
}

func newFakeElectrs(t *testing.T) *fakeElectrs {
	t.Helper()
	f := &fakeElectrs{byAsset: map[string][]elTx{}, byTxid: map[string]elTx{}, spent: map[string]string{}}
	mux := http.NewServeMux()
	writeTx := func(tx elTx) map[string]any {
		vouts := []map[string]any{}
		for _, v := range tx.Vouts {
			vouts = append(vouts, map[string]any{"scriptpubkey": v.Script, "asset": v.Asset, "value": v.Value})
		}
		return map[string]any{
			"txid": tx.Txid, "status": map[string]any{"confirmed": true, "block_height": 100},
			"vout": vouts,
		}
	}
	pages := func(w http.ResponseWriter, asset, last string) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.disableIndex {
			writeJSON(w, 404, map[string]any{"error": "no asset index"})
			return
		}
		txs := f.byAsset[asset]
		out := []map[string]any{}
		if last == "" {
			for _, tx := range txs {
				out = append(out, writeTx(tx))
			}
		}
		writeJSON(w, 200, out)
	}
	mux.HandleFunc("GET /asset/{id}/txs/chain", func(w http.ResponseWriter, r *http.Request) {
		pages(w, r.PathValue("id"), "")
	})
	mux.HandleFunc("GET /asset/{id}/txs/chain/{last}", func(w http.ResponseWriter, r *http.Request) {
		pages(w, r.PathValue("id"), r.PathValue("last"))
	})
	mux.HandleFunc("GET /tx/{txid}/outspend/{v}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if spender, ok := f.spent[r.PathValue("txid")+":"+r.PathValue("v")]; ok {
			writeJSON(w, 200, map[string]any{"spent": true, "txid": spender})
			return
		}
		writeJSON(w, 200, map[string]any{"spent": false})
	})
	mux.HandleFunc("GET /tx/{txid}/outspends", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		tx, ok := f.byTxid[r.PathValue("txid")]
		if !ok {
			writeJSON(w, 404, map[string]any{"error": "unknown tx"})
			return
		}
		out := []map[string]any{}
		for i := range tx.Vouts {
			if spender, sp := f.spent[fmt.Sprintf("%s:%d", tx.Txid, i)]; sp {
				out = append(out, map[string]any{"spent": true, "txid": spender})
			} else {
				out = append(out, map[string]any{"spent": false})
			}
		}
		writeJSON(w, 200, out)
	})
	mux.HandleFunc("GET /tx/{txid}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		tx, ok := f.byTxid[r.PathValue("txid")]
		if !ok {
			writeJSON(w, 404, map[string]any{"error": "unknown tx"})
			return
		}
		writeJSON(w, 200, writeTx(tx))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeElectrs) addTx(asset string, tx elTx) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byAsset[asset] = append(f.byAsset[asset], tx)
	f.byTxid[tx.Txid] = tx
}

func (f *fakeElectrs) markSpent(outpoint, spender string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spent[outpoint] = spender
}

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

type m10h struct {
	*m5h
	sup *supNode
	el  *fakeElectrs
}

func newM10Harness(t *testing.T) *m10h {
	t.Helper()
	h := newM7Harness(t, m5opts{})
	sup := newSupNode(h.seq)
	el := newFakeElectrs(t)
	h.s.cfg.electrsURL = el.srv.URL
	h.s.cfg.policyFeeSats = 1000
	return &m10h{m5h: h, sup: sup, el: el}
}

// attest runs the two-phase bearer attestation for an issuance with the
// session's key.
func (h *m10h) attest(t *testing.T, session, priv, issID string) {
	t.Helper()
	first := h.do("POST", "/api/issuances/"+issID+"/bearer-attestation", session, map[string]any{
		"no_us_nexus": true, "risk_accepted": true,
	})
	if first.code != 200 {
		t.Fatalf("attestation phase 1: %d %s", first.code, first.errMsg())
	}
	signThis, _ := first.body["sign_this"].(string)
	if signThis == "" {
		t.Fatalf("attestation returned no sign_this: %s", first.raw)
	}
	digest := sha256.Sum256([]byte(signThis))
	sig, err := signTagged(priv, bearerAttestationTag, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	second := h.do("POST", "/api/issuances/"+issID+"/bearer-attestation", session, map[string]any{
		"no_us_nexus": true, "risk_accepted": true, "statement_sig": sig,
	})
	if second.code != 200 {
		t.Fatalf("attestation phase 2: %d %s", second.code, second.errMsg())
	}
}

// deployBearerLive drafts + attests + bearer-deploys, returning the issuance id
// and asset id.
func (h *m10h) deployBearerLive(t *testing.T, session, priv, ticker, recovery string) (issID, assetID string) {
	t.Helper()
	issID = h.createIssuance(session, ticker+" Co", ticker, map[string]any{
		"jurisdictions": map[string]any{"HN": map[string]any{"access": "standard"}}, "price": 1.0,
	})
	h.attest(t, session, priv, issID)
	dep := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000000, "precision": 2,
		"enforcement": "bearer", "recovery_pubkey": recovery,
	})
	if dep.code != 200 {
		t.Fatalf("bearer deploy: %d %s", dep.code, dep.errMsg())
	}
	assetID, _ = dep.body["asset"].(string)
	if assetID == "" {
		t.Fatalf("bearer deploy returned no asset: %s", dep.raw)
	}
	return
}

func (f *m5Stub) assetCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nAsset
}

// ===========================================================================
// health + enforcement election
// ===========================================================================

func TestM10HealthReportsDampAndNetworkIsRefused(t *testing.T) {
	h := newM10Harness(t)

	hr := h.do("GET", "/api/health", "", nil)
	if hr.code != 200 || hr.body["damp"] != false {
		t.Fatalf("/health damp = %v, want false: %s", hr.body["damp"], hr.raw)
	}
	h.s.cfg.damp = true
	if hr2 := h.do("GET", "/api/health", "", nil); hr2.body["damp"] != true {
		t.Fatalf("/health damp with SEQPALD_DAMP = %v, want true", hr2.body["damp"])
	}
	h.s.cfg.damp = false

	session, aid, _ := h.register(genPriv(t), "Issuer", "HN")
	issID := h.createIssuance(session, "Net Co", "NETX", map[string]any{
		"jurisdictions": map[string]any{"HN": map[string]any{"access": "standard"}},
	})

	// enforcement=network: refused 501, the ELECTION still persisted.
	r := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000, "precision": 2, "enforcement": "network",
	})
	if r.code != 501 || !strings.Contains(r.errMsg(), "network enforcement is not available") {
		t.Fatalf("network deploy = %d %q, want 501", r.code, r.errMsg())
	}
	iss, _ := h.s.st.IssuanceByID(issID)
	if iss.Enforcement != "network" {
		t.Fatalf("the network election was not persisted: enforcement = %q", iss.Enforcement)
	}
	assertAuditedM10(t, h.m5h, aid, "deploy.refused")

	// A bogus enforcement value is refused outright.
	if r := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000, "precision": 2, "enforcement": "vibes",
	}); r.code != 400 {
		t.Fatalf("bogus enforcement = %d, want 400", r.code)
	}
	if h.oa.assetCount() != 0 || func() int { b, _, _ := h.sup.counts(); return b }() != 0 {
		t.Fatal("a refused election minted or broadcast something")
	}
}

func assertAuditedM10(t *testing.T, h *m5h, actor, action string) {
	t.Helper()
	rows, err := h.s.st.db.Query(`SELECT actor_aid, action FROM audit_log`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var a, act string
		rows.Scan(&a, &act)
		if a == actor && act == action {
			return
		}
	}
	t.Fatalf("no audit row %q for %s", action, actor)
}

// ===========================================================================
// bearer deploy gate sequence
// ===========================================================================

func TestM10BearerDeployGateSequence(t *testing.T) {
	h := newM10Harness(t)
	priv := genPriv(t)
	session, _, xonly := h.register(priv, "Bearer Issuer", "HN")
	issID := h.createIssuance(session, "Bearer Co", "BEAR", map[string]any{
		"jurisdictions": map[string]any{"HN": map[string]any{"access": "standard"}}, "price": 1.0,
	})
	recovery := xonlyHex(t, genPriv(t))

	deploy := func(extra map[string]any) resp {
		body := map[string]any{"issuance_id": issID, "supply": 1000000, "precision": 2, "enforcement": "bearer"}
		for k, v := range extra {
			body[k] = v
		}
		return h.do("POST", "/api/deploy", session, body)
	}

	// 1. recovery key gates: missing, malformed, equal to the session key.
	if r := deploy(nil); r.code != 400 || !strings.Contains(r.errMsg(), "recovery_pubkey") {
		t.Fatalf("bearer deploy without recovery_pubkey = %d %q, want 400", r.code, r.errMsg())
	}
	if r := deploy(map[string]any{"recovery_pubkey": "zz"}); r.code != 400 {
		t.Fatalf("malformed recovery_pubkey = %d, want 400", r.code)
	}
	if r := deploy(map[string]any{"recovery_pubkey": xonly}); r.code != 400 || !strings.Contains(r.errMsg(), "distinct") {
		t.Fatalf("recovery == session key = %d %q, want 400 distinct refusal", r.code, r.errMsg())
	}

	// 2. attestation gate: nothing stored -> 403.
	if r := deploy(map[string]any{"recovery_pubkey": recovery}); r.code != 403 || !strings.Contains(r.errMsg(), "attestation") {
		t.Fatalf("bearer deploy without attestation = %d %q, want 403", r.code, r.errMsg())
	}

	// 3. a bad attestation signature is refused and stores nothing.
	bad := h.do("POST", "/api/issuances/"+issID+"/bearer-attestation", session, map[string]any{
		"no_us_nexus": true, "risk_accepted": true, "statement_sig": strings.Repeat("ab", 64),
	})
	if bad.code != 400 {
		t.Fatalf("bad attestation sig = %d, want 400", bad.code)
	}
	if att, _ := h.s.st.BearerAttestation(issID); att != nil {
		t.Fatal("a refused attestation was stored")
	}
	// ... and both affirmations are required.
	if r := h.do("POST", "/api/issuances/"+issID+"/bearer-attestation", session, map[string]any{
		"no_us_nexus": true, "risk_accepted": false,
	}); r.code != 400 {
		t.Fatalf("half-affirmed attestation = %d, want 400", r.code)
	}

	// 4. real attestation, then the deploy succeeds through the raw node flow.
	// (The old bearer+confidential 400 is moot: deploys no longer carry a
	// confidential election at all; confidentiality is per transfer.)
	h.attest(t, session, priv, issID)
	ok := deploy(map[string]any{"recovery_pubkey": recovery})
	if ok.code != 200 {
		t.Fatalf("bearer deploy = %d %s", ok.code, ok.errMsg())
	}
	asset, _ := ok.body["asset"].(string)
	txid, _ := ok.body["txid"].(string)
	if asset == "" || txid == "" {
		t.Fatalf("bearer deploy returned no asset/txid: %s", ok.raw)
	}
	supBlock, _ := ok.body["supervision"].(map[string]any)
	if supBlock["operational_key"] != xonly || supBlock["recovery_key"] != recovery {
		t.Fatalf("deploy response supervision block wrong: %v", supBlock)
	}

	// Persisted election + descriptor.
	iss, _ := h.s.st.IssuanceByID(issID)
	if iss.Enforcement != "bearer" || iss.RecoveryPubkey != recovery ||
		!strings.EqualFold(iss.IssuerPubkey, xonly) || !iss.IssuerExternal ||
		iss.Status != "live" || iss.AssetID != asset {
		t.Fatalf("bearer issuance row wrong: %#v", iss)
	}

	// No openampd involvement; exactly one node broadcast.
	if h.oa.assetCount() != 0 {
		t.Fatal("a bearer deploy reached the openampd mint")
	}
	broadcasts, _, _ := h.sup.counts()
	if broadcasts != 1 {
		t.Fatalf("bearer deploy broadcast %d times, want 1", broadcasts)
	}

	// Replay: same terms -> same asset, nothing re-broadcast, no second mint.
	replay := deploy(map[string]any{"recovery_pubkey": recovery})
	if replay.code != 200 {
		t.Fatalf("bearer replay = %d %s", replay.code, replay.errMsg())
	}
	if got, _ := replay.body["asset"].(string); got != asset {
		t.Fatalf("replay minted a second asset: %s, want %s", got, asset)
	}
	if b2, _, _ := h.sup.counts(); b2 != 1 {
		t.Fatalf("replay re-broadcast: %d broadcasts, want 1", b2)
	}

	// Watcher handoff: the watch row exists with the supervision contract (a
	// supervision block, the terms_hash binding, and NO openamp block).
	watch, _ := h.s.st.WatchByIssuance(issID)
	if watch == nil || watch.Txid != txid {
		t.Fatalf("no watch row for the bearer mint: %#v", watch)
	}
	var contract map[string]any
	if err := json.Unmarshal([]byte(watch.Contract), &contract); err != nil {
		t.Fatalf("watch contract is not JSON: %v", err)
	}
	if _, hasOpenamp := contract["openamp"]; hasOpenamp {
		t.Fatal("the bearer contract carries an openamp block")
	}
	sb, _ := contract["supervision"].(map[string]any)
	if sb["operational_key"] != xonly || sb["recovery_key"] != recovery {
		t.Fatalf("contract supervision block wrong: %v", contract)
	}
	if th, _ := contract["terms_hash"].(string); len(th) != 64 {
		t.Fatalf("the bearer contract does not commit terms_hash: %v", contract)
	}
}

// ===========================================================================
// supervision freeze / unfreeze
// ===========================================================================

func TestM10SupervisionFreezeAndUnfreezeIdempotency(t *testing.T) {
	h := newM10Harness(t)
	priv := genPriv(t)
	session, _, _ := h.register(priv, "Bearer Issuer", "HN")
	issID, _ := h.deployBearerLive(t, session, priv, "FRZ", xonlyHex(t, genPriv(t)))

	// Escrow funding UTXOs for the supervision transactions.
	h.seq.credit("fund-addr-1", 1_000_000, 3, "")
	h.seq.credit("fund-addr-2", 1_000_000, 3, "")

	orderHash := strings.Repeat("11", 32)
	target := "tb1qfrozenholderaddressxxxxxxxxxxxxxxxxxx"

	// Gates: order_hash required; reason required; bearer-only.
	if r := h.do("POST", "/api/issuances/"+issID+"/supervision/freeze", session, map[string]any{
		"target_address": target, "reason": "court order",
	}); r.code != 400 || !strings.Contains(r.errMsg(), "order_hash") {
		t.Fatalf("freeze without order_hash = %d %q, want 400", r.code, r.errMsg())
	}
	if r := h.do("POST", "/api/issuances/"+issID+"/supervision/freeze", session, map[string]any{
		"target_address": target, "order_hash": orderHash,
	}); r.code != 400 {
		t.Fatalf("freeze without reason = %d, want 400", r.code)
	}
	servicedIss, _, _ := h.deployLivePrivate(session, "SRVD", "HN", 1.0)
	if r := h.do("POST", "/api/issuances/"+servicedIss+"/supervision/freeze", session, map[string]any{
		"target_address": target, "reason": "x", "order_hash": orderHash,
	}); r.code != 409 {
		t.Fatalf("freeze on a serviced asset = %d, want 409", r.code)
	}

	// BUILD: locked prevout + raw 32-byte message; idempotent per (target, order).
	build := h.do("POST", "/api/issuances/"+issID+"/supervision/freeze", session, map[string]any{
		"target_address": target, "reason": "court order 12", "order_hash": orderHash,
	})
	if build.code != 200 {
		t.Fatalf("freeze build: %d %s", build.code, build.errMsg())
	}
	fid, _ := build.body["freeze_id"].(string)
	toSign, _ := build.body["to_sign"].(string)
	if fid == "" || len(toSign) != 64 {
		t.Fatalf("freeze build must return freeze_id + a 32-byte message: %s", build.raw)
	}
	if build.body["freezable"] != true {
		t.Fatalf("freeze build must surface freezable: %s", build.raw)
	}
	_, _, rh1 := h.sup.counts()
	rebuild := h.do("POST", "/api/issuances/"+issID+"/supervision/freeze", session, map[string]any{
		"target_address": target, "reason": "court order 12", "order_hash": orderHash,
	})
	if rebuild.code != 200 {
		t.Fatalf("freeze rebuild: %d %s", rebuild.code, rebuild.errMsg())
	}
	if got, _ := rebuild.body["freeze_id"].(string); got != fid {
		t.Fatalf("a replayed build made a second op: %s, want %s", got, fid)
	}
	if _, _, rh2 := h.sup.counts(); rh2 != rh1 {
		t.Fatal("a replayed build asked the node for a second record message")
	}

	// COMPLETE: bad sig refused; good sig assembles + submits privately once.
	if r := h.do("POST", "/api/issuances/"+issID+"/supervision/freeze/"+fid+"/complete", session, map[string]any{
		"sig": "beef",
	}); r.code != 400 {
		t.Fatalf("short freeze sig = %d, want 400", r.code)
	}
	comp := h.do("POST", "/api/issuances/"+issID+"/supervision/freeze/"+fid+"/complete", session, map[string]any{
		"sig": strings.Repeat("ab", 64),
	})
	if comp.code != 200 {
		t.Fatalf("freeze complete: %d %s", comp.code, comp.errMsg())
	}
	txid, _ := comp.body["txid"].(string)
	if txid == "" || comp.body["channel"] != "private" {
		t.Fatalf("freeze complete must return a txid via the private channel: %s", comp.raw)
	}
	_, submits1, _ := h.sup.counts()
	if submits1 != 1 {
		t.Fatalf("freeze submitted %d times, want 1", submits1)
	}

	// IDEMPOTENT: a replayed complete returns the same txid and submits nothing.
	replay := h.do("POST", "/api/issuances/"+issID+"/supervision/freeze/"+fid+"/complete", session, map[string]any{
		"sig": strings.Repeat("ab", 64),
	})
	if replay.code != 200 {
		t.Fatalf("freeze complete replay: %d %s", replay.code, replay.errMsg())
	}
	if got, _ := replay.body["txid"].(string); got != txid {
		t.Fatalf("replayed complete returned txid %s, want %s", got, txid)
	}
	if _, submits2, _ := h.sup.counts(); submits2 != 1 {
		t.Fatal("a replayed complete re-submitted the record")
	}

	// GET supervision: supervised, keys, the freeze with its txid.
	status := h.do("GET", "/api/issuances/"+issID+"/supervision", session, nil)
	if status.code != 200 || status.body["supervised"] != true {
		t.Fatalf("supervision status: %d %s", status.code, status.raw)
	}
	freezes, _ := status.body["freezes"].([]any)
	if len(freezes) != 1 {
		t.Fatalf("supervision status lists %d freezes, want 1: %s", len(freezes), status.raw)
	}
	fr, _ := freezes[0].(map[string]any)
	if txids, _ := fr["txids"].([]any); len(txids) != 1 || txids[0] != txid {
		t.Fatalf("freeze row missing its txid: %v", fr)
	}

	// Private-channel failure falls back to the mempool.
	h.sup.setFailPrivate(true)
	order2 := strings.Repeat("22", 32)
	target2 := "tb1qsecondfrozenaddressyyyyyyyyyyyyyyyyyy"
	b2 := h.do("POST", "/api/issuances/"+issID+"/supervision/freeze", session, map[string]any{
		"target_address": target2, "reason": "court order 13", "order_hash": order2,
	})
	if b2.code != 200 {
		t.Fatalf("second freeze build: %d %s", b2.code, b2.errMsg())
	}
	f2, _ := b2.body["freeze_id"].(string)
	c2 := h.do("POST", "/api/issuances/"+issID+"/supervision/freeze/"+f2+"/complete", session, map[string]any{
		"sig": strings.Repeat("cd", 64),
	})
	if c2.code != 200 || c2.body["channel"] != "mempool" {
		t.Fatalf("freeze with a dead private channel must fall back to the mempool: %d %s", c2.code, c2.raw)
	}
	h.sup.setFailPrivate(false)

	// UNFREEZE: build (raw message), complete (sendrawtransaction), idempotent.
	unfOrder := strings.Repeat("33", 32)
	ub := h.do("POST", "/api/issuances/"+issID+"/supervision/unfreeze", session, map[string]any{
		"freeze_id": fid, "reason": "order lifted", "order_hash": unfOrder,
	})
	if ub.code != 200 {
		t.Fatalf("unfreeze build: %d %s", ub.code, ub.errMsg())
	}
	uid, _ := ub.body["unfreeze_id"].(string)
	if s, _ := ub.body["to_sign"].(string); uid == "" || len(s) != 64 {
		t.Fatalf("unfreeze build must return unfreeze_id + a 32-byte message: %s", ub.raw)
	}
	bBefore, _, _ := h.sup.counts()
	uc := h.do("POST", "/api/issuances/"+issID+"/supervision/unfreeze/"+uid+"/complete", session, map[string]any{
		"sig": strings.Repeat("ef", 64),
	})
	if uc.code != 200 {
		t.Fatalf("unfreeze complete: %d %s", uc.code, uc.errMsg())
	}
	utxid, _ := uc.body["txid"].(string)
	if utxid == "" {
		t.Fatalf("unfreeze complete returned no txid: %s", uc.raw)
	}
	bAfter, _, _ := h.sup.counts()
	if bAfter != bBefore+1 {
		t.Fatalf("unfreeze broadcast %d times, want exactly 1 more", bAfter-bBefore)
	}
	ur := h.do("POST", "/api/issuances/"+issID+"/supervision/unfreeze/"+uid+"/complete", session, map[string]any{
		"sig": strings.Repeat("ef", 64),
	})
	if ur.code != 200 {
		t.Fatalf("unfreeze replay: %d %s", ur.code, ur.errMsg())
	}
	if got, _ := ur.body["txid"].(string); got != utxid {
		t.Fatalf("replayed unfreeze returned txid %s, want %s", got, utxid)
	}
	if b3, _, _ := h.sup.counts(); b3 != bAfter {
		t.Fatal("a replayed unfreeze re-broadcast")
	}
}

// ===========================================================================
// corporate actions: holding proofs + vote tally
// ===========================================================================

// claimKeys builds a P2WPKH claimant (compressed key) and a P2TR claimant
// (x-only key) with their snapshot scripts.
func claimKeys(t *testing.T) (privA, pubA, scriptA, privB, pubB, scriptB string) {
	t.Helper()
	kA, _ := btcec.NewPrivateKey()
	privA = hex.EncodeToString(kA.Serialize())
	compA := kA.PubKey().SerializeCompressed()
	pubA = hex.EncodeToString(compA)
	scriptA = "0014" + hex.EncodeToString(hash160(compA))
	kB, _ := btcec.NewPrivateKey()
	privB = hex.EncodeToString(kB.Serialize())
	pubB = hex.EncodeToString(schnorr.SerializePubKey(kB.PubKey()))
	scriptB = "5120" + pubB
	return
}

// claimTwoPhase runs the two-phase claim: fetch sign_this, sign sha256 of it
// tagged, resubmit.
func (h *m10h) claimTwoPhase(t *testing.T, session, actionID, priv string, body map[string]any) resp {
	t.Helper()
	first := h.do("POST", "/api/actions/"+actionID+"/claim", session, body)
	if first.code != 200 {
		return first
	}
	signThis, _ := first.body["sign_this"].(string)
	if signThis == "" {
		t.Fatalf("claim phase 1 returned no sign_this: %s", first.raw)
	}
	digest := sha256.Sum256([]byte(signThis))
	sig, err := signTagged(priv, holdingProofTag, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	body["sig"] = sig
	return h.do("POST", "/api/actions/"+actionID+"/claim", session, body)
}

func TestM10HoldingProofAndVoteTally(t *testing.T) {
	h := newM10Harness(t)
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, _ := h.deployLivePrivate(issuerSession, "VOTE", "HN", 1.0)

	privA, pubA, scriptA, privB, pubB, scriptB := claimKeys(t)
	h.el.addTx(assetID, elTx{Txid: strings.Repeat("aa", 32), Vouts: []elVout{
		{Asset: assetID, Script: scriptA, Value: 600},
		{Asset: strings.Repeat("ff", 32), Script: scriptA, Value: 999}, // foreign asset: excluded
	}})
	h.el.addTx(assetID, elTx{Txid: strings.Repeat("bb", 32), Vouts: []elVout{
		{Asset: assetID, Script: scriptB, Value: 400},
		{Asset: assetID, Script: scriptB, Value: 123}, // spent: excluded
	}})
	h.el.markSpent(strings.Repeat("bb", 32)+":1", strings.Repeat("cc", 32))
	opA := strings.Repeat("aa", 32) + ":0"
	opB := strings.Repeat("bb", 32) + ":0"

	// Create the vote, snapshot on the watcher pass.
	create := h.do("POST", "/api/issuances/"+issID+"/actions", issuerSession, map[string]any{
		"kind": "vote", "record_height": 100,
		"vote": map[string]any{"question": "Approve the merger?", "choices": []string{"yes", "no"}, "closes_height": 200000},
	})
	if create.code != 200 {
		t.Fatalf("create vote: %d %s", create.code, create.errMsg())
	}
	action, _ := create.body["action"].(map[string]any)
	actionID, _ := action["id"].(string)

	// Claiming before the snapshot is refused.
	sessA, aidA, _ := h.register(genPriv(t), "Holder A", "US")
	h.verifiedClaims(aidA, &Claims{Residence: "US", BaseEligibility: "ret", USPerson: true})
	if r := h.do("POST", "/api/actions/"+actionID+"/claim", sessA, map[string]any{
		"pubkey": pubA, "outpoints": []string{opA}, "choice": "yes",
	}); r.code != 409 {
		t.Fatalf("claim before snapshot = %d, want 409", r.code)
	}

	h.s.actionSnapshotTick()
	a, _ := h.s.st.ActionByID(actionID)
	if a.State != "snapshotted" || a.SnapshotTotal != 1000 || a.SnapshotCount != 2 {
		t.Fatalf("snapshot wrong: state=%s total=%d count=%d", a.State, a.SnapshotTotal, a.SnapshotCount)
	}
	if a.SnapshotHash == "" || !strings.Contains(a.SnapshotNote, "first watcher pass") {
		t.Fatalf("snapshot must be hashed + disclosed: %#v", a)
	}
	assertAuditedM10(t, h.m5h, "", "action.snapshot")

	// Endpoint-KYC: an unverified session cannot claim.
	sessNoKYC, _, _ := h.register(genPriv(t), "No KYC", "HN")
	if r := h.claimTwoPhase(t, sessNoKYC, actionID, privA, map[string]any{
		"pubkey": pubA, "outpoints": []string{opA}, "choice": "yes",
	}); r.code != 403 || !strings.Contains(r.errMsg(), "verified") {
		t.Fatalf("unverified claim = %d %q, want 403", r.code, r.errMsg())
	}

	// Wrong key: key A cannot claim B's outpoint.
	if r := h.claimTwoPhase(t, sessA, actionID, privA, map[string]any{
		"pubkey": pubA, "outpoints": []string{opB}, "choice": "yes",
	}); r.code != 403 {
		t.Fatalf("wrong-key claim = %d, want 403", r.code)
	}

	// A bad signature is refused.
	if r := h.do("POST", "/api/actions/"+actionID+"/claim", sessA, map[string]any{
		"pubkey": pubA, "outpoints": []string{opA}, "choice": "yes", "sig": strings.Repeat("ab", 64),
	}); r.code != 400 {
		t.Fatalf("bad holding-proof sig = %d, want 400", r.code)
	}

	// Valid P2WPKH claim: yes, weighted 600.
	if r := h.claimTwoPhase(t, sessA, actionID, privA, map[string]any{
		"pubkey": pubA, "outpoints": []string{opA}, "choice": "yes",
	}); r.code != 200 {
		t.Fatalf("claim A = %d %s", r.code, r.errMsg())
	}

	// Double-claim of opA (another verified session, same key) is refused.
	sessA2, aidA2, _ := h.register(genPriv(t), "Holder A2", "HN")
	h.verifiedClaims(aidA2, &Claims{Residence: "HN", BaseEligibility: "ret"})
	if r := h.claimTwoPhase(t, sessA2, actionID, privA, map[string]any{
		"pubkey": pubA, "outpoints": []string{opA}, "choice": "no",
	}); r.code != 409 {
		t.Fatalf("double-claim = %d, want 409", r.code)
	}

	// Valid P2TR claim: no, weighted 400.
	sessB, aidB, _ := h.register(genPriv(t), "Holder B", "GB")
	h.verifiedClaims(aidB, &Claims{Residence: "GB", BaseEligibility: "ret"})
	if r := h.claimTwoPhase(t, sessB, actionID, privB, map[string]any{
		"pubkey": pubB, "outpoints": []string{opB}, "choice": "no",
	}); r.code != 200 {
		t.Fatalf("claim B = %d %s", r.code, r.errMsg())
	}

	// Tally math: weighted by snapshot atoms; the public detail carries the
	// per-outpoint proof list and never an AID.
	detail := h.do("GET", "/api/actions/"+actionID, "", nil)
	if detail.code != 200 {
		t.Fatalf("action detail: %d", detail.code)
	}
	av, _ := detail.body["action"].(map[string]any)
	vote, _ := av["vote"].(map[string]any)
	tally, _ := vote["tally"].(map[string]any)
	if jsonAtoms(tally["yes"]) != 600 || jsonAtoms(tally["no"]) != 400 {
		t.Fatalf("tally = %v, want yes 600 / no 400", tally)
	}
	proofs, _ := vote["proofs"].([]any)
	if len(proofs) != 2 {
		t.Fatalf("proof list has %d rows, want 2: %s", len(proofs), detail.raw)
	}
	if strings.Contains(detail.raw, aidA) || strings.Contains(detail.raw, aidB) {
		t.Fatal("the public action detail leaks a claimant AID")
	}

	// A closed vote refuses ballots (tip 100000 >= closes 100000).
	closedCreate := h.do("POST", "/api/issuances/"+issID+"/actions", issuerSession, map[string]any{
		"kind": "vote", "record_height": 50,
		"vote": map[string]any{"question": "Late?", "choices": []string{"a", "b"}, "closes_height": 100000},
	})
	closedAction, _ := closedCreate.body["action"].(map[string]any)
	closedID, _ := closedAction["id"].(string)
	h.s.actionSnapshotTick()
	if r := h.claimTwoPhase(t, sessB, closedID, privB, map[string]any{
		"pubkey": pubB, "outpoints": []string{opB}, "choice": "a",
	}); r.code != 409 || !strings.Contains(r.errMsg(), "closed") {
		t.Fatalf("ballot after close = %d %q, want 409", r.code, r.errMsg())
	}
}

// ===========================================================================
// corporate actions: dividend claims + payment idempotency
// ===========================================================================

func TestM10DividendClaimPaymentIdempotency(t *testing.T) {
	h := newM10Harness(t)
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, _ := h.deployLivePrivate(issuerSession, "DIVI", "HN", 1.0)

	privA, pubA, scriptA, privB, pubB, scriptB := claimKeys(t)
	h.el.addTx(assetID, elTx{Txid: strings.Repeat("aa", 32), Vouts: []elVout{
		{Asset: assetID, Script: scriptA, Value: 600},
	}})
	h.el.addTx(assetID, elTx{Txid: strings.Repeat("bb", 32), Vouts: []elVout{
		{Asset: assetID, Script: scriptB, Value: 400},
	}})
	opA := strings.Repeat("aa", 32) + ":0"
	opB := strings.Repeat("bb", 32) + ":0"

	create := h.do("POST", "/api/issuances/"+issID+"/actions", issuerSession, map[string]any{
		"kind": "dividend", "record_height": 100,
		"dividend": map[string]any{"asset": "USDX", "total_atoms": 1000},
	})
	if create.code != 200 {
		t.Fatalf("create dividend: %d %s", create.code, create.errMsg())
	}
	action, _ := create.body["action"].(map[string]any)
	actionID, _ := action["id"].(string)
	depositAddr, _ := create.body["deposit_address"].(string)
	if depositAddr == "" {
		t.Fatalf("dividend must return a fund-first deposit address: %s", create.raw)
	}
	h.s.actionSnapshotTick()

	// Claim BEFORE funding: registered, nothing paid (fund-first).
	sessA, aidA, _ := h.register(genPriv(t), "Holder A", "US")
	h.verifiedClaims(aidA, &Claims{Residence: "US", BaseEligibility: "ret", USPerson: true})
	sendsBefore := h.seq.sendCount()
	rA := h.claimTwoPhase(t, sessA, actionID, privA, map[string]any{
		"pubkey": pubA, "outpoints": []string{opA}, "payout_address": "payout-addr-a",
	})
	if rA.code != 200 {
		t.Fatalf("claim A = %d %s", rA.code, rA.errMsg())
	}
	claimA, _ := rA.body["claim"].(map[string]any)
	claimAID, _ := claimA["id"].(string)
	if st, _ := claimA["state"].(string); st != "awaiting_funding" {
		t.Fatalf("pre-funding claim state = %q, want awaiting_funding", st)
	}
	if h.seq.sendCount() != sendsBefore {
		t.Fatal("an unfunded dividend paid out")
	}

	// Fund the action; the watcher pays the registered claim. Holder A is a US
	// person (W-9): 0 withholding, gross = 1000*600/1000 = 600.
	h.seq.credit(depositAddr, 1000, 2, m5USDX)
	h.s.watchDeposits()
	a, _ := h.s.st.ActionByID(actionID)
	if !a.Funded {
		t.Fatalf("the action did not fund: %#v", a)
	}
	ca, _ := h.s.st.ActionClaimByID(claimAID)
	if ca.State != "paid" || ca.Txid == "" || ca.GrossAtoms != 600 || ca.WithheldAtoms != 0 || ca.NetAtoms != 600 {
		t.Fatalf("claim A after funding = %#v, want paid 600 net", ca)
	}
	if n := h.seq.sendsMatching("payout-addr-a", 600, actMarker(actionID, claimAID)); n != 1 {
		t.Fatalf("claim A paid %d times, want exactly 1", n)
	}

	// A replayed watcher pass never double-pays.
	h.s.watchDeposits()
	h.s.watchDeposits()
	if n := h.seq.sendsMatching("payout-addr-a", 600, actMarker(actionID, claimAID)); n != 1 {
		t.Fatalf("replayed watcher passes double-paid claim A (%d sends)", n)
	}

	// Withholding: holder B (GB, W-8BEN) is withheld 15%: gross 400 -> net 340.
	sessB, aidB, _ := h.register(genPriv(t), "Holder B", "GB")
	h.verifiedClaims(aidB, &Claims{Residence: "GB", BaseEligibility: "ret"})
	rB := h.claimTwoPhase(t, sessB, actionID, privB, map[string]any{
		"pubkey": pubB, "outpoints": []string{opB}, "payout_address": "payout-addr-b",
	})
	if rB.code != 200 {
		t.Fatalf("claim B = %d %s", rB.code, rB.errMsg())
	}
	claimB, _ := rB.body["claim"].(map[string]any)
	claimBID, _ := claimB["id"].(string)
	cb, _ := h.s.st.ActionClaimByID(claimBID)
	if cb.GrossAtoms != 400 || cb.WithheldAtoms != 60 || cb.NetAtoms != 340 || cb.State != "paid" {
		t.Fatalf("claim B withholding wrong: %#v", cb)
	}

	// Lost-write reconciliation: a claim stuck 'paying' with no recorded txid is
	// matched to the broadcast it already made by its marker comment, never
	// re-sent (the escrowFindSend discipline).
	origTxid := cb.Txid
	_ = h.s.st.UpdateActionClaimFields(claimBID, map[string]any{"state": "paying", "txid": ""})
	before := h.seq.sendCount()
	h.s.payDueActionClaims()
	cb2, _ := h.s.st.ActionClaimByID(claimBID)
	if cb2.State != "paid" || cb2.Txid != origTxid {
		t.Fatalf("reconcile did not re-adopt the broadcast txid %s: %#v", origTxid, cb2)
	}
	if h.seq.sendCount() != before {
		t.Fatal("reconciliation re-broadcast instead of adopting the marker match")
	}

	// Double-claim of a paid outpoint stays refused.
	if r := h.claimTwoPhase(t, sessA, actionID, privA, map[string]any{
		"pubkey": pubA, "outpoints": []string{opA}, "payout_address": "payout-addr-a",
	}); r.code != 409 {
		t.Fatalf("double dividend claim = %d, want 409", r.code)
	}

	// An enclave payout address is refused (the mandate check reused).
	sessC, aidC, xonlyC := h.register(genPriv(t), "Holder C", "HN")
	h.verifiedClaims(aidC, &Claims{Residence: "HN", BaseEligibility: "ret"})
	if _, err := h.s.registerUser(xonlyC); err != nil {
		t.Fatal(err)
	}
	enclaveAddr := "tb1p" + aidC // the openampd stub's deterministic enclave address
	if r := h.do("POST", "/api/actions/"+actionID+"/claim", sessC, map[string]any{
		"pubkey": pubA, "outpoints": []string{opA}, "payout_address": enclaveAddr,
	}); r.code != 400 || !strings.Contains(r.errMsg(), "enclave") {
		t.Fatalf("enclave payout address = %d %q, want 400", r.code, r.errMsg())
	}
}

// ===========================================================================
// W-6: escrow fee accrual at deposit confirmation
// ===========================================================================

func TestM10EscrowFeeAccrual(t *testing.T) {
	h := newM10Harness(t) // escrowFeeBps 50 (0.5%)
	issuerSession, _, issuerX := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, escrowAID := h.deployLivePrivate(issuerSession, "FEEA", "HN", 1.0)
	_ = assetID
	_ = escrowAID

	invSession, invAID, invX := h.register(genPriv(t), "Investor", "HN")
	_ = invSession
	if _, err := h.s.registerUser(invX); err != nil {
		t.Fatal(err)
	}
	h.verifiedClaims(invAID, &Claims{Residence: "HN", BaseEligibility: "ret"})

	// A created USDX subscription whose deposit confirms.
	sub := &Subscription{
		ID: mustID(), IssuanceID: issID, InvestorAID: invAID, Rail: "usdx",
		TokenAtoms: 10000, PayAmount: 100000, PayCcy: "USDX",
		DepositAddress: "dep-addr-1", RefundAddress: "refund-addr-1", State: "created",
	}
	if err := h.s.st.InsertSubscription(sub); err != nil {
		t.Fatal(err)
	}
	h.seq.credit("dep-addr-1", 100000, 2, m5USDX)
	h.s.watchDeposits()

	got, _ := h.s.st.SubscriptionByID(sub.ID)
	if got.State != "in_escrow" {
		t.Fatalf("subscription state = %q, want in_escrow", got.State)
	}
	// Accrued exactly once: 100000 * 50 / 10000 = 500.
	fee, ok, err := h.s.st.AccruedEscrowFee(sub.ID)
	if err != nil || !ok || fee != 500 {
		t.Fatalf("accrued fee = %d ok=%v err=%v, want 500 at deposit confirmation", fee, ok, err)
	}
	h.s.watchDeposits()
	h.s.watchDeposits()
	if n := countLedgerRows(t, h.m5h, sub.ID, "escrow_fee"); n != 1 {
		t.Fatalf("accrual rows = %d, want exactly 1 after replayed ticks", n)
	}
	assertAuditedM10(t, h.m5h, invAID, "escrow.fee_accrued")

	// CLOSE consumes the ACCRUED figure, not a recomputation: change the bps
	// after accrual; the release must still deduct 500.
	h.s.cfg.escrowFeeBps = 500 // would be 5000 atoms if recomputed
	if err := h.s.st.UpsertMandate(&PayoutMandate{IssuanceID: issID, Chain: "sequentia", Address: "mandate-addr-1"}); err != nil {
		t.Fatal(err)
	}
	escrow, _ := h.s.st.EnclaveKeyByRef(enclaveOfferingEscrow, issID)
	iss, _ := h.s.st.IssuanceByID(issID)
	res := h.s.settleOne(iss, escrow, got, 100000)
	if res["state"] != "settled" {
		t.Fatalf("settleOne = %v", res)
	}
	st, _ := h.s.st.SettlementByID(sub.ID)
	if st.FeeAtoms != 500 {
		t.Fatalf("settlement fee_atoms = %d, want the accrued 500 (never recomputed)", st.FeeAtoms)
	}
	if n := h.seq.sendsMatching("mandate-addr-1", 99500, "seqpal-rel-"+sub.ID); n != 1 {
		t.Fatalf("release did not pay deposited-minus-accrued: %d matching sends", n)
	}
	if n := countLedgerRows(t, h.m5h, sub.ID, "escrow_fee"); n != 1 {
		t.Fatalf("close double-recorded the fee: %d escrow_fee rows, want 1", n)
	}

	// REFUND withholds the accrued fee. A second subscription, accrued, then
	// refunded: the refund pays deposited - fee.
	sub2 := &Subscription{
		ID: mustID(), IssuanceID: issID, InvestorAID: invAID, Rail: "usdx",
		TokenAtoms: 10000, PayAmount: 200000, PayCcy: "USDX",
		DepositAddress: "dep-addr-2", RefundAddress: "refund-addr-2", State: "created",
	}
	if err := h.s.st.InsertSubscription(sub2); err != nil {
		t.Fatal(err)
	}
	h.s.cfg.escrowFeeBps = 50
	h.seq.credit("dep-addr-2", 200000, 2, m5USDX)
	h.s.watchDeposits()
	fee2, ok2, _ := h.s.st.AccruedEscrowFee(sub2.ID)
	if !ok2 || fee2 != 1000 {
		t.Fatalf("sub2 accrued = %d ok=%v, want 1000", fee2, ok2)
	}
	got2, _ := h.s.st.SubscriptionByID(sub2.ID)
	if _, err := h.s.st.CreateSettlementIfAbsent(sub2.ID, issID); err != nil {
		t.Fatal(err)
	}
	st2, _ := h.s.st.SettlementByID(sub2.ID)
	out := h.s.refundSubscription(iss, got2, st2, "test refund")
	if out["state"] != "refunded" {
		t.Fatalf("refund = %v", out)
	}
	if n := h.seq.sendsMatching("refund-addr-2", 199000, "seqpal-ref-"+sub2.ID); n != 1 {
		t.Fatalf("refund did not withhold the accrued fee (want 199000 sent): sends=%d", n)
	}
	st2b, _ := h.s.st.SettlementByID(sub2.ID)
	if st2b.FeeAtoms != 1000 {
		t.Fatalf("refund settlement fee_atoms = %d, want 1000 withheld", st2b.FeeAtoms)
	}

	_ = issuerX
}

func countLedgerRows(t *testing.T, h *m5h, subID, kind string) int {
	t.Helper()
	var n int
	if err := h.s.st.db.QueryRow(
		`SELECT COUNT(1) FROM escrow_ledger WHERE subscription_id = ? AND kind = ?`, subID, kind).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// ===========================================================================
// W-7: fail-closed characterization + structure id unification
// ===========================================================================

func TestM10FailClosedCharacterizationAndStructureID(t *testing.T) {
	h := newM10Harness(t)
	session, _, _ := h.register(genPriv(t), "Issuer", "HN")

	// Deploy with an unknown structure name fails closed with 400, and nothing
	// mints.
	issID := h.createIssuance(session, "Mystery Co", "MYST", map[string]any{
		"structure":     "mystery-instrument",
		"jurisdictions": map[string]any{"HN": map[string]any{"access": "standard"}},
	})
	r := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000, "precision": 2,
	})
	if r.code != 400 || !strings.Contains(r.errMsg(), "unrecognized structure") {
		t.Fatalf("unknown structure deploy = %d %q, want 400 unrecognized structure", r.code, r.errMsg())
	}
	if h.oa.assetCount() != 0 {
		t.Fatal("an unrecognized structure minted anyway")
	}

	// The characterization endpoint fails closed too...
	if r := h.do("GET", "/api/characterization?structure=mystery-instrument", "", nil); r.code != 400 {
		t.Fatalf("characterization of an unknown structure = %d, want 400", r.code)
	}
	// ...while the canonical id is depository-receipt with the old spelling as
	// an accepted alias.
	for _, alias := range []string{"depository-receipt", "depositary-receipt", "dr"} {
		cr := h.do("GET", "/api/characterization?structure="+alias, "", nil)
		if cr.code != 200 {
			t.Fatalf("characterization of %q = %d, want 200", alias, cr.code)
		}
		ch, _ := cr.body["characterization"].(map[string]any)
		if got, _ := ch["structure"].(string); got != "depository-receipt" {
			t.Fatalf("structure id for %q = %q, want depository-receipt", alias, got)
		}
	}
	list := h.do("GET", "/api/characterization", "", nil)
	if !strings.Contains(list.raw, `"depository-receipt"`) || strings.Contains(list.raw, `"depositary-receipt"`) {
		t.Fatalf("the enumerated structures must use the unified id: %s", list.raw)
	}

	// Unit seams: empty stays the equity default; the DR predicate accepts the
	// alias; an unknown name is not ok.
	if cs, ok := canonicalStructure(""); !ok || cs != "equity" {
		t.Fatalf(`canonicalStructure("") = %q/%v, want equity default`, cs, ok)
	}
	if _, ok := canonicalStructure("mystery-instrument"); ok {
		t.Fatal("canonicalStructure accepted an unknown name")
	}
	if !isDepositoryReceipt("depositary-receipt") || !isDepositoryReceipt("depository-receipt") {
		t.Fatal("the DR predicate must accept both spellings")
	}

	// W-7 (3): fee_convert_atoms == 0 from the wizard takes the price-derived
	// path (SEQ 0.1, offering price 1.0, policy fee 1000 sats -> 1 atom), never
	// the 100-atom fallback.
	okIss := h.createIssuance(session, "Price Co", "PRCE", map[string]any{
		"jurisdictions": map[string]any{"HN": map[string]any{"access": "standard"}}, "price": 1.0,
	})
	dep := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": okIss, "supply": 1000, "precision": 2, "fee_convert_atoms": 0,
	})
	if dep.code != 200 {
		t.Fatalf("priced deploy = %d %s", dep.code, dep.errMsg())
	}
	rules, _ := dep.body["rules"].(map[string]any)
	if fc := jsonAtoms(rules["fee_convert_atoms"]); fc != 1 {
		t.Fatalf("fee_convert_atoms = %d, want the price-derived 1 (not the 100 fallback)", fc)
	}
}

// ===========================================================================
// snapshot fallback walk + RIPEMD-160 vectors
// ===========================================================================

func TestM10SnapshotFallbackWalk(t *testing.T) {
	h := newM10Harness(t)
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, _ := h.deployLivePrivate(issuerSession, "WALK", "HN", 1.0)
	iss, _ := h.s.st.IssuanceByID(issID)

	// No asset index: the walker follows the issuance tx's outspends.
	h.el.mu.Lock()
	h.el.disableIndex = true
	h.el.mu.Unlock()

	issuanceTxid := iss.Txid
	childTxid := strings.Repeat("dd", 32)
	h.el.addTx(assetID, elTx{Txid: issuanceTxid, Vouts: []elVout{
		{Asset: assetID, Script: "0014" + strings.Repeat("11", 20), Value: 700}, // spent -> child
		{Asset: assetID, Script: "0014" + strings.Repeat("22", 20), Value: 300}, // unspent
	}})
	h.el.addTx(assetID, elTx{Txid: childTxid, Vouts: []elVout{
		{Asset: assetID, Script: "0014" + strings.Repeat("33", 20), Value: 700}, // unspent
	}})
	h.el.markSpent(issuanceTxid+":0", childTxid)

	create := h.do("POST", "/api/issuances/"+issID+"/actions", issuerSession, map[string]any{
		"kind": "vote", "record_height": 100,
		"vote": map[string]any{"question": "Q", "choices": []string{"a", "b"}, "closes_height": 200000},
	})
	if create.code != 200 {
		t.Fatalf("create action: %d %s", create.code, create.errMsg())
	}
	action, _ := create.body["action"].(map[string]any)
	actionID, _ := action["id"].(string)
	h.s.actionSnapshotTick()
	a, _ := h.s.st.ActionByID(actionID)
	if a.State != "snapshotted" || a.SnapshotTotal != 1000 || a.SnapshotCount != 2 {
		t.Fatalf("fallback snapshot wrong: state=%s total=%d count=%d", a.State, a.SnapshotTotal, a.SnapshotCount)
	}
	if row, _ := h.s.st.SnapshotRow(actionID, issuanceTxid+":0"); row != nil {
		t.Fatal("a spent output entered the snapshot")
	}
	if row, _ := h.s.st.SnapshotRow(actionID, childTxid+":0"); row == nil || row.Atoms != 700 {
		t.Fatalf("the walked child output is missing: %#v", row)
	}
}

func TestRIPEMD160Vectors(t *testing.T) {
	if got := hex.EncodeToString(ripemd160Sum([]byte(""))); got != "9c1185a5c5e9fc54612808977ee8f548b2258d31" {
		t.Fatalf("ripemd160(\"\") = %s", got)
	}
	if got := hex.EncodeToString(ripemd160Sum([]byte("abc"))); got != "8eb208f7e05d987a9b044a8e98c6b087f15a0bfc" {
		t.Fatalf("ripemd160(\"abc\") = %s", got)
	}
	if got := hex.EncodeToString(ripemd160Sum([]byte("The quick brown fox jumps over the lazy dog"))); got != "37f332f68db77bd9d7edd4969571ad671cf9dd3b" {
		t.Fatalf("ripemd160(fox) = %s", got)
	}
	// hash160 of the generator-point compressed pubkey (a standard vector).
	pk, _ := hex.DecodeString("0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798")
	if got := hex.EncodeToString(hash160(pk)); got != "751e76e8199196d454941c45d1b3a323f1433bd6" {
		t.Fatalf("hash160(G) = %s", got)
	}
}
