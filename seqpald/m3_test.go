package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// M3 truthful chain surfaces: the chain watcher (broadcast -> confirmed ->
// anchored, and reorg regression), registry publication, price seeding, the
// owner-scoped holders/log proxies, and the SSE fan-out. Bitcoin anchoring is
// supreme, so the reorg test is the acceptance artifact a live Bitcoin reorg
// cannot stand in for: a confirmed txid that a reorg unwinds must regress and
// emit an event, and nothing is ever labeled final at 0-conf.

// ============================================================================
// mock Sequentia node (JSON-RPC)
// ============================================================================

// mockNode answers the read-only JSON-RPC calls the watcher makes
// (getblockcount, getanchorstatus, getrawtransaction, getblockheader). Its view
// is a single mutable state the test advances phase by phase; the watcher never
// asks it to sign or move anything.
type mockNode struct {
	srv *httptest.Server
	mu  sync.Mutex

	blockCount   int64
	tipAnchor    int64  // most recent Bitcoin height the tip has anchored to
	anchorStatus string // getanchorstatus.anchorstatus for the tip

	blockHash       string // confirming block for the issuance tx ("" = still in mempool)
	confirmations   int64  // getblockheader.confirmations (< 0 once reorged out)
	blockHeight     int64
	blockAnchor     int64 // Bitcoin height THIS block anchors to
	blockAnchorHash string

	calls map[string]int
}

func newMockNode(t *testing.T) *mockNode {
	t.Helper()
	n := &mockNode{
		blockCount:   200000,
		tipAnchor:    800000,
		anchorStatus: "ok",
		calls:        map[string]int{},
	}
	n.srv = httptest.NewServer(http.HandlerFunc(n.handle))
	t.Cleanup(n.srv.Close)
	return n
}

func (n *mockNode) handle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	n.mu.Lock()
	n.calls[req.Method]++
	var result any
	switch req.Method {
	case "getblockcount":
		result = n.blockCount
	case "getanchorstatus":
		result = map[string]any{
			"validateanchor": true,
			"tipheight":      n.blockCount,
			"anchorheight":   n.tipAnchor,
			"anchorhash":     "aa" + strings.Repeat("0", 62),
			"anchorstatus":   n.anchorStatus,
		}
	case "getrawtransaction":
		tx := map[string]any{
			"txid":          trimQuotes(req.Params[0]),
			"confirmations": n.confirmations,
		}
		if n.blockHash != "" {
			tx["blockhash"] = n.blockHash
		}
		result = tx
	case "getblockheader":
		result = map[string]any{
			"hash":          trimQuotes(req.Params[0]),
			"confirmations": n.confirmations,
			"height":        n.blockHeight,
			"anchorheight":  n.blockAnchor,
			"anchorhash":    n.blockAnchorHash,
		}
	}
	n.mu.Unlock()
	w.Header().Set("content-type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"result": result})
}

func trimQuotes(raw json.RawMessage) string {
	return strings.Trim(string(raw), `"`)
}

// phase setters ---------------------------------------------------------------

func (n *mockNode) setBroadcast() {
	n.mu.Lock()
	n.blockHash, n.confirmations = "", 0
	n.mu.Unlock()
}

// setConfirmed makes the tx confirm in a block whose Bitcoin anchor equals the
// current tip anchor, so anchor_depth is 0 (confirmed, not yet anchored).
func (n *mockNode) setConfirmed(conf int64) {
	n.mu.Lock()
	n.blockHash = "bb" + strings.Repeat("1", 62)
	n.confirmations = conf
	n.blockHeight = 199950
	n.blockAnchor = n.tipAnchor
	n.blockAnchorHash = "cc" + strings.Repeat("2", 62)
	n.mu.Unlock()
}

// setAnchored advances the tip's Bitcoin anchor by depth beyond the confirming
// block's anchor, so anchor_depth = depth (>= 1 => anchored).
func (n *mockNode) setAnchored(conf, depth int64) {
	n.mu.Lock()
	n.confirmations = conf
	n.tipAnchor = n.blockAnchor + depth
	n.mu.Unlock()
}

// setReorg unwinds the confirming block: getblockheader now reports a negative
// confirmation count, which is exactly how a Bitcoin-driven regression surfaces.
func (n *mockNode) setReorg() {
	n.mu.Lock()
	n.confirmations = -1
	n.mu.Unlock()
}

// ============================================================================
// mock openampd, registry, price feed
// ============================================================================

type m3OA struct {
	srv        *httptest.Server
	mu         sync.Mutex
	issues     int
	holders    string   // JSON body returned by GET /v1/issuer/holders
	logLines   []string // NDJSON returned by GET /v1/log
	anchorTxid string
}

func newM3OA(t *testing.T) *m3OA {
	t.Helper()
	f := &m3OA{
		holders:    `{"asset":"","holders":[]}`,
		anchorTxid: "an" + strings.Repeat("c", 62),
	}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/users", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Pubkeys []string `json:"pubkeys"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		writeJSON(w, 200, map[string]any{"aid": aidFor(req.Pubkeys)})
	})
	mux.HandleFunc("GET /v1/assets", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"assets": []any{}})
	})
	mux.HandleFunc("GET /v1/users/{aid}/address", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"address": "tb1p" + strings.Repeat("q", 58)})
	})
	// Issue echoes entity_domain into the returned contract exactly as OA-1
	// specifies, so a test can prove the domain flows deploy -> contract ->
	// registry rather than being hardcoded by the stub.
	mux.HandleFunc("POST /v1/issuer/assets", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-issuer-token" {
			writeJSON(w, 401, map[string]any{"error": "bad issuer token"})
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.issues++
		n := f.issues
		f.mu.Unlock()
		contract := map[string]any{"ticker": body["ticker"], "version": 1}
		if d, _ := body["entity_domain"].(string); d != "" {
			entity := map[string]any{"domain": d}
			if name, _ := body["entity_name"].(string); name != "" {
				entity["issuer"] = name
			}
			contract["entity"] = entity
		}
		contractBytes, _ := json.Marshal(contract)
		writeJSON(w, 200, map[string]any{
			"asset":         fmt.Sprintf("%064x", n),
			"txid":          fmt.Sprintf("%064x", 1000+n),
			"contract_hash": fmt.Sprintf("%064x", 2000+n),
			"contract":      json.RawMessage(contractBytes),
		})
	})
	mux.HandleFunc("GET /v1/issuer/holders", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-issuer-token" {
			writeJSON(w, 401, map[string]any{"error": "bad issuer token"})
			return
		}
		f.mu.Lock()
		body := f.holders
		f.mu.Unlock()
		w.Header().Set("content-type", "application/json")
		w.Write([]byte(body))
	})
	mux.HandleFunc("GET /v1/log", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		lines := f.logLines
		f.mu.Unlock()
		w.Header().Set("content-type", "application/x-ndjson")
		for _, ln := range lines {
			fmt.Fprintln(w, ln)
		}
	})
	mux.HandleFunc("POST /v1/issuer/anchor", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		txid := f.anchorTxid
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"txid": txid, "seq": 42, "head": "he" + strings.Repeat("a", 62)})
	})

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

type m3Registry struct {
	srv *httptest.Server
	mu  sync.Mutex
	got []map[string]json.RawMessage // recorded publish bodies
}

func newM3Registry(t *testing.T) *m3Registry {
	t.Helper()
	r := &m3Registry{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]json.RawMessage
		json.NewDecoder(req.Body).Decode(&body)
		r.mu.Lock()
		r.got = append(r.got, body)
		r.mu.Unlock()
		writeJSON(w, 200, map[string]any{"verified": false, "id": trimQuotes(body["asset_id"])})
	})
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	return r
}

func (r *m3Registry) posts() []map[string]json.RawMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]json.RawMessage(nil), r.got...)
}

type m3Price struct {
	srv    *httptest.Server
	mu     sync.Mutex
	prices map[string]float64
	seeds  []map[string]any
	paths  []string
}

func newM3Price(t *testing.T) *m3Price {
	t.Helper()
	p := &m3Price{prices: map[string]float64{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /prices", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.paths = append(p.paths, "/prices")
		out := map[string]any{}
		for k, v := range p.prices {
			out[k] = map[string]any{"price": v}
		}
		p.mu.Unlock()
		writeJSON(w, 200, out)
	})
	mux.HandleFunc("POST /seed", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		p.mu.Lock()
		p.paths = append(p.paths, "/seed")
		p.seeds = append(p.seeds, body)
		p.mu.Unlock()
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *m3Price) hitPaths() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.paths...)
}

// ============================================================================
// m3 server / seeding helpers
// ============================================================================

func newM3Server(t *testing.T, oaURL string) *server {
	t.Helper()
	st, err := openStore(filepath.Join(t.TempDir(), "seqpald.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &server{
		cfg: config{
			openampURL:    oaURL,
			issuerToken:   "test-issuer-token",
			network:       "sequentia-testnet",
			blocksPerDay:  144,
			assumedTip:    200000,
			entityDomain:  "sequentiatestnet.com",
			policyFeeSats: 1000,
			adminAIDs:     map[string]bool{},
		},
		st:     st,
		http:   &http.Client{Timeout: 5 * time.Second},
		rl:     newRateLimiter(),
		catMu:  newKeyedMutex(),
		screen: newScreener(""),
	}
}

func m3SeedAccount(t *testing.T, s *server, priv, name string) (session, aid, xonly string) {
	t.Helper()
	xonly = xonlyHex(t, priv)
	aid = aidFor([]string{xonly})
	if err := s.st.CreateAccount(&Account{
		AID: aid, Kind: "person", XOnly: xonly, DisplayName: name, CreatedAt: time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	tok, _, err := s.st.CreateSession(aid, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return tok, aid, xonly
}

// m3SeedLive creates a live (on-chain) issuance directly in the store, the state
// the watcher and the owner-scoped reads operate on.
func m3SeedLive(t *testing.T, s *server, ownerAID, ticker, assetID, txid string, terms string) string {
	t.Helper()
	id, _ := randHex(12)
	now := time.Now().Unix()
	if err := s.st.CreateIssuance(&Issuance{
		ID: id, OwnerAID: ownerAID, Name: ticker + " Fund", Ticker: ticker,
		StructureID: "native-equity", Status: "draft", Terms: json.RawMessage(terms),
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.st.UpdateIssuanceFields(id, map[string]any{
		"status": "live", "asset_id": assetID, "txid": txid,
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func m3req(t *testing.T, h http.Handler, method, path, session string, body any) resp {
	t.Helper()
	var rdr *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = strings.NewReader(string(b))
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, "/seqpal"+path, rdr)
	req.Header.Set("content-type", "application/json")
	if session != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := resp{code: rec.Code, raw: rec.Body.String(), set: rec.Result().Cookies()}
	json.Unmarshal(rec.Body.Bytes(), &out.body)
	return out
}

func recvEvent(t *testing.T, ch chan watchEvent, want string) watchEvent {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Status == want {
				return ev
			}
			// Ignore intermediate events (e.g. a broadcast snapshot) and keep waiting.
		case <-deadline:
			t.Fatalf("timed out waiting for a %q event", want)
		}
	}
}

// ============================================================================
// Deliverable 1: the chain watcher, incl. the reorg regression acceptance proof
// ============================================================================

func TestWatcherAdvancesAndRegressesOnReorg(t *testing.T) {
	oa := newM3OA(t)
	node := newMockNode(t)
	s := newM3Server(t, oa.srv.URL)
	s.cfg.nodeURL = node.srv.URL
	// Registry and price seeding are exercised elsewhere; keep them off so the
	// watcher tick under test has no side effects.
	s.cfg.registryURL, s.cfg.priceURL = "", ""

	_, ownerAID, _ := m3SeedAccount(t, s, vecPriv, "Issuer Ida")
	assetID := strings.Repeat("a", 64)
	txid := strings.Repeat("b", 64)
	issID := m3SeedLive(t, s, ownerAID, "AURA", assetID, txid, `{}`)

	// Subscribe to the owner's SSE fan-out so each transition is asserted on the
	// stream, not just in the store.
	_, ch := s.bus().subscribe(ownerAID)

	w := &WatchState{
		IssuanceID: issID, OwnerAID: ownerAID, AssetID: assetID, Txid: txid, Status: statusBroadcast,
	}
	if err := s.st.UpsertWatch(w); err != nil {
		t.Fatal(err)
	}

	// Phase 0: broadcast. The tx is in the mempool, no confirming block yet.
	node.setBroadcast()
	s.watchTickOne(w)
	if w.Status != statusBroadcast {
		t.Fatalf("phase 0 status = %q, want broadcast (never final at 0-conf)", w.Status)
	}

	// Phase 1: confirmed, anchor depth 0. One Sequentia confirmation, the block's
	// Bitcoin anchor equals the tip anchor, so nothing has anchored on top yet.
	node.setConfirmed(1)
	s.watchTickOne(w)
	ev := recvEvent(t, ch, statusConfirmed)
	if ev.Confirmations != 1 || ev.AnchorDepth != 0 {
		t.Fatalf("confirmed event = conf %d depth %d, want 1/0", ev.Confirmations, ev.AnchorDepth)
	}
	if w.Status != statusConfirmed {
		t.Fatalf("phase 1 status = %q, want confirmed", w.Status)
	}

	// Phase 2: anchored. The tip's Bitcoin anchor is now 3 ahead of the confirming
	// block's anchor, so anchor_depth is 3.
	node.setAnchored(4, 3)
	s.watchTickOne(w)
	ev = recvEvent(t, ch, statusAnchored)
	if ev.AnchorDepth != 3 {
		t.Fatalf("anchored event depth = %d, want 3", ev.AnchorDepth)
	}
	if w.Status != statusAnchored || w.AnchorDepth != 3 {
		t.Fatalf("phase 2 = %q depth %d, want anchored/3", w.Status, w.AnchorDepth)
	}

	// Phase 3: REORG. Bitcoin reorged out the confirming block; anchoring is
	// supreme, so the previously-anchored issuance must regress and emit an event.
	node.setReorg()
	s.watchTickOne(w)
	ev = recvEvent(t, ch, statusRegressed)
	if ev.Status != statusRegressed {
		t.Fatalf("reorg event status = %q, want regressed", ev.Status)
	}
	if w.Status != statusRegressed || w.AnchorDepth != 0 {
		t.Fatalf("phase 3 = %q depth %d, want regressed/0", w.Status, w.AnchorDepth)
	}
	if w.BlockHash != "" {
		t.Fatal("a regressed watch kept its stale confirming block; re-confirmation cannot be rediscovered")
	}

	// The persisted row reflects the regression, so a restart does not paper over it.
	got, err := s.st.WatchByIssuance(issID)
	if err != nil || got == nil {
		t.Fatalf("watch row missing after regression: %v", err)
	}
	if got.Status != statusRegressed {
		t.Fatalf("persisted status = %q, want regressed", got.Status)
	}
}

// A transient node error must NOT be read as a reorg: the watcher only regresses
// on positive evidence (a stored block reporting confirmations < 0).
func TestWatcherDoesNotFabricateRegressionOnRPCError(t *testing.T) {
	oa := newM3OA(t)
	node := newMockNode(t)
	s := newM3Server(t, oa.srv.URL)
	s.cfg.nodeURL = node.srv.URL
	s.cfg.registryURL, s.cfg.priceURL = "", ""

	_, ownerAID, _ := m3SeedAccount(t, s, vecPriv, "Issuer Ida")
	assetID, txid := strings.Repeat("a", 64), strings.Repeat("b", 64)
	issID := m3SeedLive(t, s, ownerAID, "AURA", assetID, txid, `{}`)
	w := &WatchState{IssuanceID: issID, OwnerAID: ownerAID, AssetID: assetID, Txid: txid, Status: statusBroadcast}
	s.st.UpsertWatch(w)

	node.setConfirmed(2)
	s.watchTickOne(w)
	if w.Status != statusConfirmed {
		t.Fatalf("status = %q, want confirmed", w.Status)
	}

	// The node goes away (all RPC fails); the watcher must keep the prior state.
	node.srv.Close()
	s.watchTickOne(w)
	if w.Status != statusConfirmed {
		t.Fatalf("an RPC outage regressed the state to %q; only positive evidence may regress", w.Status)
	}
}

// ============================================================================
// Deliverable 2: registry publication
// ============================================================================

func TestRegistryPublicationPostsContractWithEntityDomain(t *testing.T) {
	oa := newM3OA(t)
	node := newMockNode(t)
	reg := newM3Registry(t)
	s := newM3Server(t, oa.srv.URL)
	s.cfg.nodeURL = node.srv.URL
	s.cfg.registryURL = reg.srv.URL
	s.cfg.priceURL = "" // isolate registry from price seeding

	session, ownerAID, _ := m3SeedAccount(t, s, vecPriv, "Issuer Ida")
	issID := m3SeedDraft(t, s, ownerAID, "AURA")

	// Node starts in broadcast so onDeployed's async tick is a harmless no-op.
	node.setBroadcast()
	dep := m3req(t, s.handler(), "POST", "/api/deploy", session,
		map[string]any{"issuance_id": issID, "supply": 1000, "precision": 2, "terms": map[string]any{}})
	if dep.code != 200 {
		t.Fatalf("deploy = %d %s", dep.code, dep.errMsg())
	}
	assetID, _ := dep.body["asset"].(string)
	if assetID == "" {
		t.Fatalf("deploy returned no asset: %s", dep.raw)
	}

	// The issuance is confirmed on chain; the watcher publishes to the registry.
	node.setConfirmed(1)
	w, err := s.st.WatchByIssuance(issID)
	if err != nil || w == nil {
		t.Fatalf("watch row missing after deploy: %v", err)
	}
	s.watchTickOne(w)

	posts := reg.posts()
	if len(posts) == 0 {
		t.Fatal("nothing was published to the registry")
	}
	last := posts[len(posts)-1]
	if got := trimQuotes(last["asset_id"]); got != assetID {
		t.Fatalf("registry asset_id = %q, want %q", got, assetID)
	}
	contract := string(last["contract"])
	if !strings.Contains(contract, `"domain":"sequentiatestnet.com"`) {
		t.Fatalf("published contract has no entity.domain: %s", contract)
	}

	// The resolvable registry URL is recorded on the issuance.
	got, _ := s.st.WatchByIssuance(issID)
	wantURL := strings.TrimRight(reg.srv.URL, "/") + "/" + assetID
	if got.RegistryURL != wantURL {
		t.Fatalf("recorded registry_url = %q, want %q", got.RegistryURL, wantURL)
	}
}

// Registry publication is best-effort: an unreachable registry must not fail the
// deploy nor wedge the watcher.
func TestDeploySucceedsWhenRegistryIsDown(t *testing.T) {
	oa := newM3OA(t)
	node := newMockNode(t)
	reg := newM3Registry(t)
	down := reg.srv.URL
	reg.srv.Close() // the registry is now unreachable

	s := newM3Server(t, oa.srv.URL)
	s.cfg.nodeURL = node.srv.URL
	s.cfg.registryURL = down
	s.cfg.priceURL = ""

	session, ownerAID, _ := m3SeedAccount(t, s, vecPriv, "Issuer Ida")
	issID := m3SeedDraft(t, s, ownerAID, "AURA")

	node.setBroadcast()
	dep := m3req(t, s.handler(), "POST", "/api/deploy", session,
		map[string]any{"issuance_id": issID, "supply": 1000, "precision": 2, "terms": map[string]any{}})
	if dep.code != 200 {
		t.Fatalf("deploy with a down registry = %d %s, want 200", dep.code, dep.errMsg())
	}

	node.setConfirmed(1)
	w, _ := s.st.WatchByIssuance(issID)
	s.watchTickOne(w) // must not panic
	got, _ := s.st.WatchByIssuance(issID)
	if got.RegistryURL != "" {
		t.Fatalf("registry_url = %q, want empty (nothing was published)", got.RegistryURL)
	}
	if got.Status != statusConfirmed {
		t.Fatalf("a down registry wedged the watcher: status = %q, want confirmed", got.Status)
	}
}

// ============================================================================
// Deliverable 3: price seeding (data-only; never the rate calculation)
// ============================================================================

func TestPriceSeedIsDataOnly(t *testing.T) {
	oa := newM3OA(t)
	price := newM3Price(t)
	s := newM3Server(t, oa.srv.URL)
	s.cfg.priceURL = price.srv.URL

	_, ownerAID, _ := m3SeedAccount(t, s, vecPriv, "Issuer Ida")
	terms := `{"price":2.5,"quote":"USD"}`
	assetID, txid := strings.Repeat("a", 64), strings.Repeat("b", 64)
	issID := m3SeedLive(t, s, ownerAID, "AURA", assetID, txid, terms)
	// The ticker for the seed comes from the issuance record, not the watch row.
	w := &WatchState{IssuanceID: issID, OwnerAID: ownerAID, AssetID: assetID, Txid: txid}

	s.maybeSeedPrice(w)

	price.mu.Lock()
	seeds := append([]map[string]any(nil), price.seeds...)
	price.mu.Unlock()
	if len(seeds) != 1 {
		t.Fatalf("price seed count = %d, want 1", len(seeds))
	}
	seed := seeds[0]
	if seed["kind"] != "offering-reference" {
		t.Fatalf("seed kind = %v, want offering-reference (data-only feed datum)", seed["kind"])
	}
	if seed["ticker"] != "AURA" || seed["asset_id"] != assetID {
		t.Fatalf("seed ticker/asset = %v/%v, want AURA/%s", seed["ticker"], seed["asset_id"], assetID)
	}
	if p, _ := seed["price"].(float64); p != 2.5 {
		t.Fatalf("seed price = %v, want 2.5", seed["price"])
	}
	if seed["quote"] != "USD" {
		t.Fatalf("seed quote = %v, want USD", seed["quote"])
	}
	// Data-only: the seed carries no field that would alter the price server's
	// rate calculation, and the only endpoint the seed path touches is POST /seed.
	for _, k := range []string{"rate", "rates", "exchange_rate", "seq_price"} {
		if _, ok := seed[k]; ok {
			t.Fatalf("seed carries rate-calculation field %q; it must be data-only", k)
		}
	}
	for _, p := range price.hitPaths() {
		if p != "/seed" {
			t.Fatalf("price seeding touched %q; only POST /seed is data-only", p)
		}
	}
	if !w.PriceSeeded {
		t.Fatal("price_seeded flag not set; the seed would be re-POSTed every tick")
	}

	// Idempotent: a second call does not re-seed.
	s.maybeSeedPrice(w)
	price.mu.Lock()
	n := len(price.seeds)
	price.mu.Unlock()
	if n != 1 {
		t.Fatalf("price re-seeded (%d POSTs), want 1", n)
	}
}

// fee_convert_atoms derives from /prices with the canonical, value-preserving
// relation: a MORE valuable asset pays FEWER atoms for the same fee value. This
// is the standing "any-asset fee rates are correct, never inverted" invariant.
func TestFeeConvertIsNotInverted(t *testing.T) {
	oa := newM3OA(t)
	price := newM3Price(t)
	price.prices = map[string]float64{"SEQ": 1.0, "TSEQ": 1.0}
	s := newM3Server(t, oa.srv.URL)
	s.cfg.priceURL = price.srv.URL
	s.cfg.policyFeeSats = 100000000 // 1e8 SEQ-sats, so the arithmetic is legible

	valuable, ok1 := s.feeConvertAtoms("VALU", 2.0) // rate = 2e8 -> atoms = ceil(1e8/2e8) = 1
	cheap, ok2 := s.feeConvertAtoms("CHEAP", 0.5)   // rate = 5e7 -> atoms = ceil(1e8/5e7) = 2
	if !ok1 || !ok2 {
		t.Fatalf("feeConvertAtoms not derivable from /prices: %v %v", ok1, ok2)
	}
	if valuable != 1 || cheap != 2 {
		t.Fatalf("fee atoms valuable/cheap = %d/%d, want 1/2", valuable, cheap)
	}
	if valuable > cheap {
		t.Fatal("more valuable asset paid MORE atoms; the rate was inverted")
	}
}

// ============================================================================
// Deliverable 4: owner-scoped holders and log proxies
// ============================================================================

func TestHoldersAndLogAreOwnerScoped(t *testing.T) {
	oa := newM3OA(t)
	s := newM3Server(t, oa.srv.URL)

	owner, ownerAID, _ := m3SeedAccount(t, s, vecPriv, "Issuer Ida")
	other, _, _ := m3SeedAccount(t, s, vecPriv2, "Bob")
	assetID, txid := strings.Repeat("a", 64), strings.Repeat("b", 64)
	issID := m3SeedLive(t, s, ownerAID, "AURA", assetID, txid, `{}`)

	// The cap table, in atoms, from openampd's holder register.
	oa.mu.Lock()
	oa.holders = fmt.Sprintf(`{"asset":%q,"holders":[{"aid":"%s","atoms":1000000}]}`, assetID, strings.Repeat("e", 40))
	oa.mu.Unlock()

	h := s.handler()

	// Owner sees the register in atoms.
	r := m3req(t, h, "GET", "/api/issuances/"+issID+"/holders", owner, nil)
	if r.code != 200 {
		t.Fatalf("owner holders = %d %s", r.code, r.errMsg())
	}
	holders, _ := r.body["holders"].([]any)
	if len(holders) != 1 {
		t.Fatalf("holders = %d rows, want 1", len(holders))
	}
	h0, _ := holders[0].(map[string]any)
	if atoms, _ := h0["atoms"].(float64); atoms != 1000000 {
		t.Fatalf("holder atoms = %v, want 1000000 (must be in atoms)", h0["atoms"])
	}

	// A non-owner is refused.
	if r := m3req(t, h, "GET", "/api/issuances/"+issID+"/holders", other, nil); r.code != 403 {
		t.Fatalf("non-owner holders = %d, want 403", r.code)
	}
	// No session, no access.
	if r := m3req(t, h, "GET", "/api/issuances/"+issID+"/holders", "", nil); r.code != 401 {
		t.Fatalf("holders without a session = %d, want 401", r.code)
	}

	// The log slice: entries naming the asset or its txid, plus anchor entries;
	// an unrelated asset's entry is excluded.
	oa.mu.Lock()
	oa.logLines = []string{
		fmt.Sprintf(`{"seq":1,"prev":"","time":"t","action":"issue","data":{"asset":%q},"hash":"h1"}`, assetID),
		fmt.Sprintf(`{"seq":2,"prev":"h1","time":"t","action":"transfer","data":{"txid":%q},"hash":"h2"}`, txid),
		`{"seq":3,"prev":"h2","time":"t","action":"anchor","data":{"txid":"deadbeef"},"hash":"h3"}`,
		`{"seq":4,"prev":"h3","time":"t","action":"issue","data":{"asset":"ffff"},"hash":"h4"}`,
	}
	oa.mu.Unlock()

	lr := m3req(t, h, "GET", "/api/issuances/"+issID+"/log", owner, nil)
	if lr.code != 200 {
		t.Fatalf("owner log = %d %s", lr.code, lr.errMsg())
	}
	entries, _ := lr.body["entries"].([]any)
	if len(entries) != 3 {
		t.Fatalf("log slice = %d entries, want 3 (asset + txid + anchor, not the unrelated asset)", len(entries))
	}
	if lr.body["log_url"] != "/openamp/v1/log" {
		t.Fatalf("log_url = %v, want /openamp/v1/log (full chain for independent verification)", lr.body["log_url"])
	}
	// The anchor entry is surfaced, so the on-chain log-head txids are auditable.
	sawAnchor := false
	for _, e := range entries {
		if m, _ := e.(map[string]any); m["action"] == "anchor" {
			sawAnchor = true
		}
	}
	if !sawAnchor {
		t.Fatal("the anchor entry was not surfaced in the log slice")
	}

	// A non-owner cannot read the log either.
	if r := m3req(t, h, "GET", "/api/issuances/"+issID+"/log", other, nil); r.code != 403 {
		t.Fatalf("non-owner log = %d, want 403", r.code)
	}
}

// ============================================================================
// Deliverable 5: the SSE fan-out
// ============================================================================

func TestSSEStreamsStateChangesScopedToOwner(t *testing.T) {
	oa := newM3OA(t)
	node := newMockNode(t)
	s := newM3Server(t, oa.srv.URL)
	s.cfg.nodeURL = node.srv.URL
	s.cfg.registryURL, s.cfg.priceURL = "", ""

	owner, ownerAID, _ := m3SeedAccount(t, s, vecPriv, "Issuer Ida")
	assetID, txid := strings.Repeat("a", 64), strings.Repeat("b", 64)
	issID := m3SeedLive(t, s, ownerAID, "AURA", assetID, txid, `{}`)
	w := &WatchState{IssuanceID: issID, OwnerAID: ownerAID, AssetID: assetID, Txid: txid, Status: statusBroadcast}
	s.st.UpsertWatch(w)

	// Another account must never observe this issuance's chain state.
	_, otherAID, _ := m3SeedAccount(t, s, vecPriv2, "Bob")
	_, otherCh := s.bus().subscribe(otherAID)

	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/seqpal/api/events", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: owner})
	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("events stream = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("events content-type = %q, want text/event-stream", ct)
	}

	events := make(chan watchEvent, 8)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "data: ") {
				var ev watchEvent
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev) == nil {
					events <- ev
				}
			}
		}
	}()

	// Snapshot on connect: the owner's current world (broadcast).
	snap := waitEvent(t, events)
	if snap.IssuanceID != issID || snap.Status != statusBroadcast {
		t.Fatalf("snapshot event = %s/%s, want %s/broadcast", snap.IssuanceID, snap.Status, issID)
	}

	// A state change fans out as a new event.
	node.setConfirmed(1)
	s.watchTickOne(w)
	changed := waitEvent(t, events)
	if changed.Status != statusConfirmed || changed.IssuanceID != issID {
		t.Fatalf("change event = %s/%s, want %s/confirmed", changed.IssuanceID, changed.Status, issID)
	}

	// The other account's subscriber saw nothing.
	select {
	case ev := <-otherCh:
		t.Fatalf("a non-owner SSE subscriber received event %s/%s", ev.IssuanceID, ev.Status)
	default:
	}
}

func waitEvent(t *testing.T, ch chan watchEvent) watchEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for an SSE event")
		return watchEvent{}
	}
}

// m3SeedDraft creates a draft issuance the deploy flow then mints.
func m3SeedDraft(t *testing.T, s *server, ownerAID, ticker string) string {
	t.Helper()
	id, _ := randHex(12)
	now := time.Now().Unix()
	if err := s.st.CreateIssuance(&Issuance{
		ID: id, OwnerAID: ownerAID, Name: ticker + " Fund", Ticker: ticker,
		StructureID: "native-equity", Status: "draft", Terms: json.RawMessage(`{}`),
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	return id
}
