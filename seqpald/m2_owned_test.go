package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// This file is the tests-owner deliverable for M2 (compiler matrices, the
// serialized per-AID category write queue, the /id/verify -> /eligibility flow
// including sanctions, and the per-offering escrow enclave). It is self-contained:
// it stands up its own instrumented openampd stub so it does not depend on the
// backend's m2_test.go fixtures, and asserts the OA-3 wire shape exactly.

// --- an instrumented, capturing openampd stub --------------------------------

type owUser struct {
	AID        string   `json:"aid"`
	Pubkeys    []string `json:"pubkeys"`
	Categories []string `json:"categories"`
	Frozen     bool     `json:"frozen"`
}

// capturedIssue records exactly what seqpald POSTs to /v1/issuer/assets, so the
// escrow test can prove the mint's holder is the escrow enclave (not the issuer's
// personal AID) and that rules.primary_aids carries the escrow + treasury AIDs.
type capturedIssue struct {
	HolderAID string        `json:"holder_aid"`
	IssuerAID string        `json:"issuer_aid"`
	Rules     CompiledRules `json:"rules"`
}

type owStub struct {
	srv    *httptest.Server
	mu     sync.Mutex
	users  map[string]*owUser
	assets map[string]map[string]any
	issues []capturedIssue
	nAsset int

	// per-AID concurrency instrumentation for the category-write door.
	catSleep  time.Duration
	inflight  map[string]int
	maxConc   map[string]int
	catWrites map[string]int // per-AID successful category writes seen upstream
}

func newOWStub(t *testing.T) *owStub {
	t.Helper()
	f := &owStub{
		users:     map[string]*owUser{},
		assets:    map[string]map[string]any{},
		inflight:  map[string]int{},
		maxConc:   map[string]int{},
		catWrites: map[string]int{},
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
			f.users[aid] = &owUser{AID: aid, Pubkeys: req.Pubkeys, Categories: []string{}}
		}
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"aid": aid})
	})

	mux.HandleFunc("GET /v1/users/{aid}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		u, ok := f.users[r.PathValue("aid")]
		var cp owUser
		if ok {
			cp = *u
		}
		f.mu.Unlock()
		if !ok {
			writeJSON(w, 404, map[string]any{"error": "unknown aid"})
			return
		}
		if cp.Categories == nil {
			cp.Categories = []string{}
		}
		writeJSON(w, 200, cp)
	})

	mux.HandleFunc("GET /v1/users/{aid}/address", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"address": "tb1p" + r.PathValue("aid")})
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

		// Enter the per-AID critical section and record the peak overlap. If the
		// seqpald per-AID mutex works, this peak is 1 for any single AID; a lost
		// mutex would let two writers overlap here.
		f.mu.Lock()
		f.inflight[req.AID]++
		if f.inflight[req.AID] > f.maxConc[req.AID] {
			f.maxConc[req.AID] = f.inflight[req.AID]
		}
		f.mu.Unlock()

		if f.catSleep > 0 {
			time.Sleep(f.catSleep)
		}

		f.mu.Lock()
		if u, ok := f.users[req.AID]; ok {
			u.Categories = append([]string{}, req.Categories...)
		}
		f.catWrites[req.AID]++
		f.inflight[req.AID]--
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"ok": true})
	})

	mux.HandleFunc("POST /v1/issuer/freeze", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-issuer-token" {
			writeJSON(w, 401, map[string]any{"error": "bad token"})
			return
		}
		var req struct {
			AID    string `json:"aid"`
			Frozen bool   `json:"frozen"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
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
		var body capturedIssue
		raw, _ := readAllLimited(r.Body, 1<<20)
		json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.nAsset++
		id := pad64(byte('a'), f.nAsset)
		f.assets[id] = map[string]any{"id": id, "ticker": "X", "name": "X", "rules": body.Rules}
		f.issues = append(f.issues, body)
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{
			"asset":         id,
			"txid":          pad64(byte('b'), f.nAsset),
			"contract_hash": pad64(byte('c'), f.nAsset),
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

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *owStub) putAsset(id, ticker string, rules CompiledRules) {
	f.mu.Lock()
	f.assets[id] = map[string]any{"id": id, "ticker": ticker, "name": ticker, "rules": rules}
	f.mu.Unlock()
}

func (f *owStub) userCategories(aid string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.users[aid]; ok {
		return append([]string{}, u.Categories...)
	}
	return nil
}

func (f *owStub) frozen(aid string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.users[aid]; ok {
		return u.Frozen
	}
	return false
}

func (f *owStub) peakConcurrency(aid string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxConc[aid]
}

func pad64(fill byte, n int) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = fill
	}
	// stamp a small index so distinct assets get distinct ids
	hexd := "0123456789abcdef"
	b[63] = hexd[n&0xf]
	b[62] = hexd[(n>>4)&0xf]
	return string(b)
}

// --- owned harness -----------------------------------------------------------

type owHarness struct {
	t  *testing.T
	s  *server
	h  http.Handler
	oa *owStub
}

func newOWHarness(t *testing.T) *owHarness {
	t.Helper()
	oa := newOWStub(t)
	st, err := openStore(filepath.Join(t.TempDir(), "seqpald.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := &server{
		cfg: config{
			openampURL:   oa.srv.URL,
			issuerToken:  "test-issuer-token",
			network:      "sequentia-testnet",
			blocksPerDay: 144,
			assumedTip:   100000,
			adminAIDs:    map[string]bool{},
		},
		st:       st,
		http:     &http.Client{Timeout: 5 * time.Second},
		rl:       newRateLimiter(),
		chalRL:   newWindowLimiter(challengesPerKeyPerHour, challengesGlobalPerHour, time.Hour),
		catMu:    newKeyedMutex(),
		verifyMu: newKeyedMutex(),
		idv:      &testIDV{},
	}
	return &owHarness{t: t, s: s, h: s.handler(), oa: oa}
}

func (h *owHarness) do(method, path, session string, body any) resp {
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

func (h *owHarness) challenge(xonly string) string {
	h.t.Helper()
	r := h.do("POST", "/api/auth/challenge", "", map[string]any{"xonly": xonly})
	c, _ := r.body["challenge"].(string)
	if len(c) != 64 {
		h.t.Fatalf("challenge: %d %s", r.code, r.errMsg())
	}
	return c
}

func (h *owHarness) register(priv, name, residence string) (session, aid, xonly string) {
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

// ============================================================================
// Deliverable 1: the Step 5 matrix -> openampd rules compiler.
// ============================================================================

// asRules turns the CompiledRules back through JSON so tests assert the exact
// wire object openampd will receive, not just the Go struct.
func rulesJSON(t *testing.T, r CompiledRules) string {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func eqStrSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}

func TestOwnedCompiler_AccessTiers(t *testing.T) {
	env := compileEnv{TipHeight: 100000, BlocksPerDay: 144}

	// standard, no explicit selection -> admits ret|acc|pro.
	std, err := compileRules(json.RawMessage(`{"jurisdictions":{"DE":{"access":"standard"}}}`), env)
	if err != nil {
		t.Fatal(err)
	}
	if !eqStrSet(std.AllowedCategories, []string{"j:DE:ret", "j:DE:acc", "j:DE:pro"}) {
		t.Fatalf("standard admits = %v, want ret|acc|pro", std.AllowedCategories)
	}

	// restricted, no explicit selection -> admits acc|pro ONLY (never ret).
	restr, err := compileRules(json.RawMessage(`{"jurisdictions":{"DE":{"access":"restricted"}}}`), env)
	if err != nil {
		t.Fatal(err)
	}
	if !eqStrSet(restr.AllowedCategories, []string{"j:DE:acc", "j:DE:pro"}) {
		t.Fatalf("restricted admits = %v, want acc|pro only", restr.AllowedCategories)
	}
	for _, c := range restr.AllowedCategories {
		if c == "j:DE:ret" {
			t.Fatalf("restricted must not admit retail: %v", restr.AllowedCategories)
		}
	}

	// restricted cannot be widened by asking for ret: access clamps the selection.
	clamp, _ := compileRules(json.RawMessage(`{"jurisdictions":{"DE":{"access":"restricted","elig_categories":["ret","acc","pro"]}}}`), env)
	if !eqStrSet(clamp.AllowedCategories, []string{"j:DE:acc", "j:DE:pro"}) {
		t.Fatalf("restricted+ret selection = %v, want acc|pro (ret clamped out)", clamp.AllowedCategories)
	}

	// excluded -> admits nothing; the field is omitted entirely.
	excl, _ := compileRules(json.RawMessage(`{"jurisdictions":{"DE":{"access":"excluded"}}}`), env)
	if len(excl.AllowedCategories) != 0 {
		t.Fatalf("excluded admits = %v, want nothing", excl.AllowedCategories)
	}

	// catch-all: a jurisdiction absent from the matrix contributes nothing, so a
	// resident of an unlisted country matches no allowed category (fail closed).
	catchAll, _ := compileRules(json.RawMessage(`{"jurisdictions":{"DE":{"access":"standard"}}}`), env)
	for _, c := range catchAll.AllowedCategories {
		if c == "j:FR:ret" || c == "j:US:ret" {
			t.Fatalf("catch-all leaked a token for an unlisted jurisdiction: %v", catchAll.AllowedCategories)
		}
	}
	// An entirely empty matrix admits nobody and serializes with no allowed_categories.
	empty, _ := compileRules(json.RawMessage(`{}`), env)
	if len(empty.AllowedCategories) != 0 {
		t.Fatalf("empty matrix admits = %v, want nothing", empty.AllowedCategories)
	}
	// unknown access value fails closed (treated as excluded).
	unknown, _ := compileRules(json.RawMessage(`{"jurisdictions":{"DE":{"access":"vip"}}}`), env)
	if len(unknown.AllowedCategories) != 0 {
		t.Fatalf("unknown access admits = %v, want nothing", unknown.AllowedCategories)
	}
}

func TestOwnedCompiler_LockupRegSCapsPrimary(t *testing.T) {
	env := compileEnv{TipHeight: 100000, BlocksPerDay: 144, FeeConvert: 7,
		PrimaryAIDs: []string{"escrowAID", "treasuryAID"}}

	// Absolute lockup height passes through; days convert against the tip.
	abs, _ := compileRules(json.RawMessage(`{"lockup_height":250000}`), env)
	if abs.LockinUntilHeight != 250000 {
		t.Fatalf("lockup_height -> %d, want 250000", abs.LockinUntilHeight)
	}
	rel, _ := compileRules(json.RawMessage(`{"lockup_days":30}`), env)
	if rel.LockinUntilHeight != 100000+30*144 {
		t.Fatalf("lockup_days -> %d, want %d", rel.LockinUntilHeight, 100000+30*144)
	}
	// Absolute wins when both are present.
	both, _ := compileRules(json.RawMessage(`{"lockup_height":250000,"lockup_days":30}`), env)
	if both.LockinUntilHeight != 250000 {
		t.Fatalf("lockup_height+days -> %d, want the absolute 250000", both.LockinUntilHeight)
	}

	// Reg S with an explicit prefix + absolute height.
	rs, _ := compileRules(json.RawMessage(`{"reg_s":{"prefix":"j:US","until_height":300000}}`), env)
	if len(rs.CategoryDenies) != 1 || rs.CategoryDenies[0].Prefix != "j:US" || rs.CategoryDenies[0].UntilHeight != 300000 {
		t.Fatalf("reg_s -> %+v, want [{j:US 300000}]", rs.CategoryDenies)
	}
	// Reg S default prefix is j:US when omitted; days convert.
	rsd, _ := compileRules(json.RawMessage(`{"reg_s":{"days":40}}`), env)
	if len(rsd.CategoryDenies) != 1 || rsd.CategoryDenies[0].Prefix != "j:US" || rsd.CategoryDenies[0].UntilHeight != 100000+40*144 {
		t.Fatalf("reg_s default -> %+v, want [{j:US %d}]", rsd.CategoryDenies, 100000+40*144)
	}

	// EU per-member-state caps: a bare state maps to that state's retail token; an
	// explicit token key is used verbatim.
	caps, _ := compileRules(json.RawMessage(`{"eu_caps":{"DE":149,"j:FR:acc":10,"IT":0}}`), env)
	if caps.HolderCapsByCategory["j:DE:ret"] != 149 {
		t.Fatalf("eu_caps bare DE -> %+v, want j:DE:ret=149", caps.HolderCapsByCategory)
	}
	if caps.HolderCapsByCategory["j:FR:acc"] != 10 {
		t.Fatalf("eu_caps explicit token -> %+v, want j:FR:acc=10", caps.HolderCapsByCategory)
	}
	if _, ok := caps.HolderCapsByCategory["j:IT:ret"]; ok {
		t.Fatalf("a non-positive cap must be dropped, got %+v", caps.HolderCapsByCategory)
	}

	// primary_aids passthrough and fee-convert.
	if !eqStrSet(rs.PrimaryAIDs, []string{"escrowAID", "treasuryAID"}) {
		t.Fatalf("primary_aids = %v, want escrow+treasury", rs.PrimaryAIDs)
	}
	if rs.FeeConvertAtoms != 7 {
		t.Fatalf("fee_convert = %d, want 7", rs.FeeConvertAtoms)
	}

	// Debt-yield structure sets a velocity window; equity leaves it unset.
	debt, _ := compileRules(json.RawMessage(`{"structure":"debt-yield"}`), env)
	if debt.VelocityWindowBlocks != 144 {
		t.Fatalf("debt-yield velocity window = %d, want 144", debt.VelocityWindowBlocks)
	}
	eq, _ := compileRules(json.RawMessage(`{"structure":"native-equity"}`), env)
	if eq.VelocityWindowBlocks != 0 {
		t.Fatalf("equity velocity window = %d, want 0", eq.VelocityWindowBlocks)
	}
}

// TestOwnedCompiler_Adversarial is the required adversarial case: a matrix with
// reordered top-level keys, reordered jurisdiction map, reordered elig arrays,
// reordered eu_caps, injected unknown keys, and whitespace must compile to the
// byte-identical rules of the clean input. The compiler must be order- and
// noise-insensitive or two issuers who wrote the same policy would get different
// on-chain rules.
func TestOwnedCompiler_Adversarial(t *testing.T) {
	env := compileEnv{TipHeight: 100000, BlocksPerDay: 144, FeeConvert: 5,
		PrimaryAIDs: []string{"escrowAID", "treasuryAID"}}

	clean := json.RawMessage(`{
      "jurisdictions": {
        "DE": {"access":"standard","elig_categories":["ret","acc","pro"]},
        "US": {"access":"restricted","elig_categories":["acc","pro"]},
        "FR": {"access":"excluded"}
      },
      "lockup_days": 30,
      "reg_s": {"prefix":"j:US","days":40},
      "eu_caps": {"DE": 149, "FR": 100},
      "structure": "debt-yield",
      "holder_cap": 2000
    }`)

	// Same policy, every collection reordered, extra unknown keys, and whitespace.
	shuffled := json.RawMessage(`{
      "holder_cap": 2000,
      "documents": {"prospectus":"ignored"},
      "eu_caps": {"FR": 100, "DE": 149},
      "structure": "debt-yield",
      "reg_s": {"until_height": 0, "days": 40, "prefix": "j:US"},
      "promotion": ["ignored","facets"],
      "jurisdictions": {
        "FR": {"access":"excluded","elig_categories":[]},
        "US": {"elig_categories":["pro","acc"],"access":"restricted"},
        "DE": {"elig_categories":["pro","ret","acc"],"access":"standard"}
      },
      "lockup_height": 0,
      "lockup_days": 30
    }`)

	rc, err := compileRules(clean, env)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := compileRules(shuffled, env)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rc, rs) {
		t.Fatalf("reordered/nested matrix compiled differently:\n clean    = %s\n shuffled = %s",
			rulesJSON(t, rc), rulesJSON(t, rs))
	}
	// And the concrete expected shape, so a change to BOTH inputs cannot hide a regression.
	if !eqStrSet(rc.AllowedCategories, []string{"j:DE:ret", "j:DE:acc", "j:DE:pro", "j:US:acc", "j:US:pro"}) {
		t.Fatalf("allowed = %v", rc.AllowedCategories)
	}
	if rc.LockinUntilHeight != 100000+30*144 {
		t.Fatalf("lockin = %d", rc.LockinUntilHeight)
	}
	if len(rc.CategoryDenies) != 1 || rc.CategoryDenies[0] != (CategoryDeny{Prefix: "j:US", UntilHeight: 100000 + 40*144}) {
		t.Fatalf("category_denies = %+v", rc.CategoryDenies)
	}
	if rc.HolderCapsByCategory["j:DE:ret"] != 149 || rc.HolderCapsByCategory["j:FR:ret"] != 100 {
		t.Fatalf("holder_caps_by_category = %+v", rc.HolderCapsByCategory)
	}
	if rc.HolderCap != 2000 || rc.VelocityWindowBlocks != 144 {
		t.Fatalf("holder_cap=%d velocity=%d", rc.HolderCap, rc.VelocityWindowBlocks)
	}
	if !eqStrSet(rc.PrimaryAIDs, []string{"escrowAID", "treasuryAID"}) {
		t.Fatalf("primary_aids = %v", rc.PrimaryAIDs)
	}
}

// ============================================================================
// Deliverable 2: the serialized per-AID category write queue.
// ============================================================================

// TestOwnedWriteQueueSerialization proves that concurrent category writes for one
// AID never interleave (the instrumented stub sees peak per-AID concurrency of 1),
// that the final list is a correct projection of the claims record rather than a
// lost update, and that the audit log records every write.
func TestOwnedWriteQueueSerialization(t *testing.T) {
	h := newOWHarness(t)
	h.oa.catSleep = 3 * time.Millisecond // widen the interleave window

	_, aid, xonly := h.register(vecPriv, "Grace Hopper", "DE")

	// Register the AID upstream (so the write-queue's verify GET has a user) and
	// seed a verified claims record that projects exactly [j:DE:ret].
	if _, err := h.s.registerUser(xonly); err != nil {
		t.Fatalf("registerUser: %v", err)
	}
	now := time.Now().Unix()
	if err := h.s.st.UpsertClaims(&Claims{
		AID: aid, Residence: "DE", BaseEligibility: "ret", Status: "verified",
		ValidUntil: now + 3600, VocabVersion: vocabVersion,
	}); err != nil {
		t.Fatalf("UpsertClaims: %v", err)
	}
	want := []string{"j:DE:ret"}

	// A second AID writing concurrently must be free to overlap the first (the lock
	// is per-AID, not global) while never overlapping itself.
	_, aid2, xonly2 := h.register(vecPriv2, "Ada Byron", "FR")
	if _, err := h.s.registerUser(xonly2); err != nil {
		t.Fatalf("registerUser2: %v", err)
	}
	if err := h.s.st.UpsertClaims(&Claims{
		AID: aid2, Residence: "FR", BaseEligibility: "ret", Status: "verified",
		ValidUntil: now + 3600, VocabVersion: vocabVersion,
	}); err != nil {
		t.Fatalf("UpsertClaims2: %v", err)
	}

	const n = 24
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			got, err := h.s.writeCategories(aid)
			if err != nil {
				errs <- err
				return
			}
			if !eqStrSet(got, want) {
				errs <- fmt.Errorf("aid1 write projected %v, want %v", got, want)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := h.s.writeCategories(aid2); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent writeCategories: %v", err)
	}

	// The mutex is per-AID: neither AID's category write ever overlapped itself.
	if peak := h.oa.peakConcurrency(aid); peak != 1 {
		t.Fatalf("aid1 peak concurrent category writes = %d, want 1 (writes interleaved -> the per-AID lock failed)", peak)
	}
	if peak := h.oa.peakConcurrency(aid2); peak != 1 {
		t.Fatalf("aid2 peak concurrent category writes = %d, want 1", peak)
	}

	// The end state is the correct projection, not a torn/lost update.
	if got := h.oa.userCategories(aid); !eqStrSet(got, want) {
		t.Fatalf("final categories = %v, want %v", got, want)
	}

	// Every write is recorded in the append-only audit log.
	if got := auditCount(t, h.s, aid, "categories.write"); got != n {
		t.Fatalf("audit rows for aid1 categories.write = %d, want %d", got, n)
	}
	if got := auditCount(t, h.s, aid2, "categories.write"); got != n {
		t.Fatalf("audit rows for aid2 categories.write = %d, want %d", got, n)
	}
}

func auditCount(t *testing.T, s *server, aid, action string) int {
	t.Helper()
	var n int
	if err := s.st.db.QueryRow(
		`SELECT COUNT(1) FROM audit_log WHERE actor_aid = ? AND action = ?`, aid, action).Scan(&n); err != nil {
		t.Fatalf("audit query: %v", err)
	}
	return n
}

// ============================================================================
// Deliverable 3: /id/verify -> categories -> /eligibility, including sanctions.
// ============================================================================

func TestOwnedVerifyEligibilityAndRefusal(t *testing.T) {
	h := newOWHarness(t)

	// A clean DE retail identity is submitted, granted nothing yet, and stamped
	// exactly [j:DE:ret] once the provider clears it.
	session, aid, _ := h.register(vecPriv, "Katherine Johnson", "DE")
	v := h.do("POST", "/api/id/verify", session, map[string]any{"residence": "DE", "base_eligibility": "ret"})
	if v.code != 200 {
		t.Fatalf("verify: %d %s", v.code, v.errMsg())
	}
	if got, _ := v.body["status"].(string); got != "submitted" {
		t.Fatalf("status = %q, want submitted", got)
	}
	if cats := h.oa.userCategories(aid); len(cats) != 0 {
		t.Fatalf("nothing is granted before the provider decides, got %v", cats)
	}
	h.adjudicate(aid, idvClear)
	if cats := h.oa.userCategories(aid); !eqStrSet(cats, []string{"j:DE:ret"}) {
		t.Fatalf("stamped categories = %v, want [j:DE:ret]", cats)
	}

	// A matching asset admits the holder; a non-matching one refuses with a reason.
	h.oa.putAsset(pad64('d', 1), "DEBOND", CompiledRules{AllowedCategories: []string{"j:DE:ret", "j:DE:acc"}})
	h.oa.putAsset(pad64('e', 2), "USONLY", CompiledRules{AllowedCategories: []string{"j:US:acc"}})

	ok := h.do("GET", "/api/eligibility?aid="+aid+"&asset="+pad64('d', 1), "", nil)
	if eligible, _ := ok.body["eligible"].(bool); !eligible {
		t.Fatalf("DE asset should be eligible for a DE retail holder: %s", ok.raw)
	}
	no := h.do("GET", "/api/eligibility?aid="+aid+"&asset="+pad64('e', 2), "", nil)
	if eligible, _ := no.body["eligible"].(bool); eligible {
		t.Fatalf("US-only asset should be ineligible for a DE retail holder: %s", no.raw)
	}
	if reasons, _ := no.body["reasons"].([]any); len(reasons) == 0 {
		t.Fatalf("an ineligible verdict must carry a reason: %s", no.raw)
	}

	// An identity the provider REFUSES is frozen and refused, and now fails
	// eligibility for the very asset the cleared holder passed. There is no
	// SeqPal reviewer to appeal to: the provider's decision is the decision.
	sSession, sAID, _ := h.register(vecPriv2, "Somebody Else", "DE")
	sv := h.do("POST", "/api/id/verify", sSession, map[string]any{"residence": "DE", "base_eligibility": "ret"})
	if got, _ := sv.body["status"].(string); got != "submitted" {
		t.Fatalf("verify status = %q, want submitted (%s)", got, sv.raw)
	}
	if cats := h.oa.userCategories(sAID); len(cats) != 0 {
		t.Fatalf("a submitted identity must have nothing stamped, got %v", cats)
	}
	pass := h.do("GET", "/api/id/passport", sSession, nil)
	if got, _ := pass.body["status"].(string); got != "submitted" {
		t.Fatalf("passport status = %q, want submitted", got)
	}

	h.adjudicate(sAID, idvReject)

	if !h.oa.frozen(sAID) {
		t.Fatalf("a refusal must freeze the AID")
	}
	claims, _ := h.s.st.ClaimsByAID(sAID)
	if claims == nil || claims.Status != "refused" {
		t.Fatalf("claims status = %v, want refused", claims)
	}
	// A frozen account is ineligible for everything, with the freeze called out.
	frz := h.do("GET", "/api/eligibility?aid="+sAID+"&asset="+pad64('d', 1), "", nil)
	if eligible, _ := frz.body["eligible"].(bool); eligible {
		t.Fatalf("a frozen AID must be ineligible: %s", frz.raw)
	}
	if !containsReason(frz.body["reasons"], "frozen") {
		t.Fatalf("a frozen verdict must say so: %s", frz.raw)
	}
}

// A provider that asks for more rather than deciding leaves the identity able to
// try again -- the one not-verified state that is not final, and the reason the
// re-submission guard lets it through.
func TestOwnedProviderCanAskForMore(t *testing.T) {
	h := newOWHarness(t)
	session, aid, _ := h.register(vecPriv, "Needs More Documents", "FR")

	if v := h.do("POST", "/api/id/verify", session, map[string]any{
		"residence": "FR", "base_eligibility": "ret",
	}); v.code != 200 {
		t.Fatalf("verify: %d %s", v.code, v.errMsg())
	}
	h.adjudicate(aid, idvResubmit)

	claims, _ := h.s.st.ClaimsByAID(aid)
	if claims == nil || claims.Status != "needs_info" {
		t.Fatalf("claims status = %v, want needs_info", claims)
	}
	if cats := h.oa.userCategories(aid); len(cats) != 0 {
		t.Fatalf("an unfinished verification grants nothing, got %v", cats)
	}

	// And submitting again is allowed, unlike after a refusal.
	if again := h.do("POST", "/api/id/verify", session, map[string]any{
		"residence": "FR", "base_eligibility": "ret",
	}); again.code != 200 {
		t.Fatalf("a holder asked for more must be able to submit again: %d %s", again.code, again.errMsg())
	}
	h.adjudicate(aid, idvClear)
	if cats := h.oa.userCategories(aid); len(cats) == 0 {
		t.Fatalf("a cleared resubmission must stamp categories")
	}
}

func containsReason(v any, needle string) bool {
	arr, _ := v.([]any)
	for _, r := range arr {
		if s, ok := r.(string); ok && strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// ============================================================================
// Deliverable 4: the per-offering escrow enclave.
// ============================================================================

// TestOwnedEscrowEnclave proves the mint lands in a per-offering escrow AID (not
// the issuer's personal AID), and that the escrow AID plus the entity treasury AID
// are exactly the asset's rules.primary_aids. It asserts on the ACTUAL body
// seqpald POSTs to /v1/issuer/assets, so a regression that minted into the issuer
// AID or dropped a primary AID would fail here.
func TestOwnedEscrowEnclave(t *testing.T) {
	h := newOWHarness(t)
	session, issuerAID, _ := h.register(vecPriv2, "Escrow Issuer Co", "HN")

	// The controller verifies first: an entity is verified by the person who
	// controls it, and an unverified one cannot vouch for a company.
	if v := h.do("POST", "/api/id/verify", session, map[string]any{
		"residence": "HN-PRO", "screening_name": "Escrow Issuer Co", "base_eligibility": "ret",
	}); v.code != 200 {
		t.Fatalf("controller verify: %d %s", v.code, v.errMsg())
	}
	h.adjudicate(issuerAID, idvClear)

	// A corporate entity, then KYB verify to provision its treasury enclave.
	ent := h.do("POST", "/api/entities", session, map[string]any{"name": "Acme Holdings", "jurisdiction": "HN"})
	if ent.code != 200 {
		t.Fatalf("create entity: %d %s", ent.code, ent.errMsg())
	}
	entObj, _ := ent.body["entity"].(map[string]any)
	entityID, _ := entObj["id"].(string)
	ev := h.do("POST", "/api/id/entities/"+entityID+"/verify", session, map[string]any{})
	if ev.code != 200 {
		t.Fatalf("entity verify: %d %s", ev.code, ev.errMsg())
	}
	treasuryAID, _ := ev.body["treasury_aid"].(string)
	if treasuryAID == "" {
		t.Fatalf("entity verify did not provision a treasury AID: %s", ev.raw)
	}

	// An entity-backed issuance with a real jurisdiction matrix.
	terms := map[string]any{
		"jurisdictions": map[string]any{"HN": map[string]any{"access": "standard"}},
		"lockup_days":   90,
	}
	iss := h.do("POST", "/api/issuances", session, map[string]any{
		"name": "Acme Series A", "ticker": "ESCROW", "structure_id": "native-equity",
		"entity_id": entityID, "terms": terms,
	})
	if iss.code != 200 {
		t.Fatalf("create issuance: %d %s", iss.code, iss.errMsg())
	}
	issObj, _ := iss.body["issuance"].(map[string]any)
	issID, _ := issObj["id"].(string)

	dep := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000000, "precision": 2,
	})
	if dep.code != 200 {
		t.Fatalf("deploy: %d %s", dep.code, dep.errMsg())
	}

	// The escrow and treasury AIDs seqpald custodies for this offering.
	escrowKey, err := h.s.st.EnclaveKeyByRef(enclaveOfferingEscrow, issID)
	if err != nil || escrowKey == nil {
		t.Fatalf("no offering escrow enclave was created: %v", err)
	}
	treasuryKey, err := h.s.st.EnclaveKeyByRef(enclaveEntityTreasury, entityID)
	if err != nil || treasuryKey == nil {
		t.Fatalf("no entity treasury enclave: %v", err)
	}
	if escrowKey.AID == issuerAID {
		t.Fatalf("the escrow AID must differ from the issuer's personal AID (%s)", issuerAID)
	}
	if escrowKey.AID != treasuryKey.AID && treasuryKey.AID == issuerAID {
		t.Fatalf("the treasury AID must differ from the issuer's personal AID")
	}

	// What seqpald actually sent to openampd's mint.
	h.oa.mu.Lock()
	issues := append([]capturedIssue(nil), h.oa.issues...)
	h.oa.mu.Unlock()
	if len(issues) != 1 {
		t.Fatalf("expected exactly one mint, got %d", len(issues))
	}
	mint := issues[0]
	if mint.HolderAID != escrowKey.AID {
		t.Fatalf("mint holder = %s, want the escrow AID %s (tokens must NOT land in the issuer AID)", mint.HolderAID, escrowKey.AID)
	}
	if mint.HolderAID == issuerAID {
		t.Fatalf("mint landed in the issuer's personal AID %s", issuerAID)
	}
	if mint.IssuerAID != issuerAID {
		t.Fatalf("issuer of record = %s, want %s", mint.IssuerAID, issuerAID)
	}
	if !eqStrSet(mint.Rules.PrimaryAIDs, []string{escrowKey.AID, treasuryKey.AID}) {
		t.Fatalf("rules.primary_aids = %v, want {escrow %s, treasury %s}", mint.Rules.PrimaryAIDs, escrowKey.AID, treasuryKey.AID)
	}
	// The compiled matrix rode along: HN standard admits ret|acc|pro and the lockup converted.
	if !eqStrSet(mint.Rules.AllowedCategories, []string{"j:HN:ret", "j:HN:acc", "j:HN:pro"}) {
		t.Fatalf("minted allowed_categories = %v", mint.Rules.AllowedCategories)
	}
	if mint.Rules.LockinUntilHeight != 100000+90*144 {
		t.Fatalf("minted lockin = %d, want %d", mint.Rules.LockinUntilHeight, 100000+90*144)
	}

	// The deploy response mirrors the split: aid = issuer of record; escrow/holder = escrow AID.
	if got, _ := dep.body["aid"].(string); got != issuerAID {
		t.Fatalf("deploy response aid = %s, want the issuer of record %s", got, issuerAID)
	}
	if got, _ := dep.body["escrow_aid"].(string); got != escrowKey.AID {
		t.Fatalf("deploy response escrow_aid = %s, want %s", got, escrowKey.AID)
	}
	if got, _ := dep.body["holder_aid"].(string); got != escrowKey.AID {
		t.Fatalf("deploy response holder_aid = %s, want %s", got, escrowKey.AID)
	}
}

// adjudicate delivers a provider decision for this harness, the way the callback
// would.
func (h *owHarness) adjudicate(aid string, decision idvDecision) {
	h.t.Helper()
	check, err := h.s.st.LatestVerificationCheck(aid, "identity")
	if err != nil || check == nil {
		h.t.Fatalf("no verification check for %s: %v", aid, err)
	}
	if err := h.s.applyAdjudication(check, decision, ""); err != nil {
		h.t.Fatalf("adjudicate %s as %s: %v", aid, decision, err)
	}
}
