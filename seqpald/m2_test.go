package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// --- in-memory openampd stub (fuller than the M1 fake) -----------------------

type oaUser struct {
	AID        string   `json:"aid"`
	Pubkeys    []string `json:"pubkeys"`
	Categories []string `json:"categories"`
	Frozen     bool     `json:"frozen"`
}

type stubOA struct {
	srv    *httptest.Server
	mu     sync.Mutex
	users  map[string]*oaUser
	assets map[string]map[string]any
}

func newStubOA(t *testing.T) *stubOA {
	t.Helper()
	f := &stubOA{users: map[string]*oaUser{}, assets: map[string]map[string]any{}}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/users", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Pubkeys []string `json:"pubkeys"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		aid := aidFor(req.Pubkeys)
		f.mu.Lock()
		if _, ok := f.users[aid]; !ok {
			f.users[aid] = &oaUser{AID: aid, Pubkeys: req.Pubkeys, Categories: []string{}}
		}
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"aid": aid})
	})
	mux.HandleFunc("GET /v1/users/{aid}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		u, ok := f.users[r.PathValue("aid")]
		var cp oaUser
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
		writeJSON(w, 200, map[string]any{"address": "tb1pstub"})
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
	mux.HandleFunc("GET /v1/assets", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		var out []map[string]any
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

func (f *stubOA) putAsset(id, ticker string, rules CompiledRules) {
	f.mu.Lock()
	f.assets[id] = map[string]any{"id": id, "ticker": ticker, "name": ticker, "rules": rules}
	f.mu.Unlock()
}

func (f *stubOA) userCategories(aid string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.users[aid]; ok {
		return append([]string{}, u.Categories...)
	}
	return nil
}

func (f *stubOA) frozen(aid string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.users[aid]; ok {
		return u.Frozen
	}
	return false
}

// --- M2 harness --------------------------------------------------------------

type m2Harness struct {
	t  *testing.T
	s  *server
	h  http.Handler
	oa *stubOA
}

func newM2Harness(t *testing.T) *m2Harness {
	t.Helper()
	oa := newStubOA(t)
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
		st:     st,
		http:   &http.Client{Timeout: 5 * time.Second},
		rl:     newRateLimiter(),
		catMu:  newKeyedMutex(),
		screen: newScreener(""),
	}
	return &m2Harness{t: t, s: s, h: s.handler(), oa: oa}
}

func (h *m2Harness) do(method, path, session string, body any) resp {
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

// registerPersona creates an account whose display_name is the given name (so it
// is the name screened), and returns its session and AID.
func (h *m2Harness) registerPersona(priv, name, residence string) (session, aid, xonly string) {
	h.t.Helper()
	xonly = xonlyHex(h.t, priv)
	ch := h.challengeM2(xonly)
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

func (h *m2Harness) challengeM2(xonly string) string {
	h.t.Helper()
	r := h.do("POST", "/api/auth/challenge", "", map[string]any{"xonly": xonly})
	c, _ := r.body["challenge"].(string)
	if len(c) != 64 {
		h.t.Fatalf("challenge: %d %s", r.code, r.errMsg())
	}
	return c
}

// --- the pure compiler -------------------------------------------------------

func TestCompileRules_OA3Shape(t *testing.T) {
	terms := json.RawMessage(`{
      "jurisdictions": {
        "DE": {"access":"standard"},
        "US": {"access":"restricted","elig_categories":["acc"]},
        "FR": {"access":"excluded"}
      },
      "lockup_height": 250000,
      "reg_s": {"prefix":"j:US","until_height":300000},
      "eu_caps": {"DE": 149},
      "structure": "debt-yield",
      "holder_cap": 2000
    }`)
	env := compileEnv{TipHeight: 100000, BlocksPerDay: 144, FeeConvert: 5, PrimaryAIDs: []string{"escrow01", "treasury01"}}
	rules, err := compileRules(terms, env)
	if err != nil {
		t.Fatal(err)
	}
	// allowed_categories: DE standard -> ret/acc/pro; US restricted+acc -> j:US:acc; FR excluded -> nothing.
	wantAllowed := map[string]bool{"j:DE:ret": true, "j:DE:acc": true, "j:DE:pro": true, "j:US:acc": true}
	if len(rules.AllowedCategories) != len(wantAllowed) {
		t.Fatalf("allowed = %v", rules.AllowedCategories)
	}
	for _, c := range rules.AllowedCategories {
		if !wantAllowed[c] {
			t.Fatalf("unexpected allowed category %s (full %v)", c, rules.AllowedCategories)
		}
	}
	if rules.LockinUntilHeight != 250000 {
		t.Fatalf("lockin = %d", rules.LockinUntilHeight)
	}
	if len(rules.CategoryDenies) != 1 || rules.CategoryDenies[0].Prefix != "j:US" || rules.CategoryDenies[0].UntilHeight != 300000 {
		t.Fatalf("category_denies = %+v", rules.CategoryDenies)
	}
	if rules.HolderCapsByCategory["j:DE:ret"] != 149 {
		t.Fatalf("holder_caps_by_category = %+v", rules.HolderCapsByCategory)
	}
	if rules.HolderCap != 2000 {
		t.Fatalf("holder_cap = %d", rules.HolderCap)
	}
	if rules.VelocityWindowBlocks != 144 {
		t.Fatalf("velocity window = %d", rules.VelocityWindowBlocks)
	}
	if len(rules.PrimaryAIDs) != 2 {
		t.Fatalf("primary_aids = %v", rules.PrimaryAIDs)
	}
	if rules.FeeConvertAtoms != 5 {
		t.Fatalf("fee_convert = %d", rules.FeeConvertAtoms)
	}

	// Catch-all excluded by default: a jurisdiction absent from the matrix
	// contributes nothing, and lockup days convert against the tip.
	terms2 := json.RawMessage(`{"jurisdictions":{"GB":{"access":"standard","elig_categories":["ret","hnw","soph"]}},"lockup_days":30}`)
	r2, _ := compileRules(terms2, env)
	if r2.LockinUntilHeight != 100000+30*144 {
		t.Fatalf("days lockin = %d", r2.LockinUntilHeight)
	}
	gb := map[string]bool{}
	for _, c := range r2.AllowedCategories {
		gb[c] = true
	}
	if !gb["j:GB:ret"] || !gb["j:GB:hnw"] || !gb["j:GB:soph"] || gb["j:US:ret"] {
		t.Fatalf("gb allowed = %v", r2.AllowedCategories)
	}
}

func TestProjectCategories(t *testing.T) {
	now := time.Now().Unix()
	c := &Claims{Residence: "DE", BaseEligibility: "ret", Status: "verified", ValidUntil: now + 1000}
	if got := projectCategories(c, now); len(got) != 1 || got[0] != "j:DE:ret" {
		t.Fatalf("DE retail -> %v", got)
	}
	// Accredited with a non-stale artifact adds the acc token; US-person adds j:US.
	c2 := &Claims{Residence: "DE", BaseEligibility: "ret", Accredited: true, AccredArtifact: "abcd",
		AccredValidUntil: now + 1000, USPerson: true, Status: "verified", ValidUntil: now + 1000}
	got := projectCategories(c2, now)
	set := map[string]bool{}
	for _, g := range got {
		set[g] = true
	}
	if !set["j:DE:ret"] || !set["j:DE:acc"] || !set["j:US:ret"] || !set["j:US:acc"] {
		t.Fatalf("accredited US-person DE -> %v", got)
	}
	// Expired identity projects to nothing.
	c3 := &Claims{Residence: "DE", Status: "verified", ValidUntil: now - 1}
	if got := projectCategories(c3, now); got != nil {
		t.Fatalf("expired -> %v, want nil", got)
	}
	// Unverified projects to nothing.
	c4 := &Claims{Residence: "DE", Status: "pending_review", ValidUntil: now + 1000}
	if got := projectCategories(c4, now); got != nil {
		t.Fatalf("unverified -> %v, want nil", got)
	}
}

// --- /id/verify stamps categories; /eligibility reflects them ----------------

func TestIDVerifyStampsCategories(t *testing.T) {
	h := newM2Harness(t)
	session, aid, _ := h.registerPersona(vecPriv, "Ada Lovelace", "DE")

	r := h.do("POST", "/api/id/verify", session, map[string]any{
		"residence": "DE", "base_eligibility": "ret",
	})
	if r.code != 200 {
		t.Fatalf("verify: %d %s", r.code, r.errMsg())
	}
	if got, _ := r.body["status"].(string); got != "verified" {
		t.Fatalf("status = %q, want verified", got)
	}
	if cats := h.oa.userCategories(aid); len(cats) != 1 || cats[0] != "j:DE:ret" {
		t.Fatalf("policy server categories = %v, want [j:DE:ret]", cats)
	}

	// An asset admitting DE retail: eligible. An asset admitting only US acc: not.
	h.oa.putAsset("aa"+dup(62), "DEBOND", CompiledRules{AllowedCategories: []string{"j:DE:ret", "j:DE:acc"}})
	h.oa.putAsset("bb"+dup(62), "USONLY", CompiledRules{AllowedCategories: []string{"j:US:acc"}})

	elig := h.do("GET", "/api/eligibility?aid="+aid+"&asset=aa"+dup(62), "", nil)
	if ok, _ := elig.body["eligible"].(bool); !ok {
		t.Fatalf("DE asset eligibility = %v (%s)", elig.body["eligible"], elig.raw)
	}
	nope := h.do("GET", "/api/eligibility?aid="+aid+"&asset=bb"+dup(62), "", nil)
	if ok, _ := nope.body["eligible"].(bool); ok {
		t.Fatalf("US-only asset should be ineligible for a DE retail holder: %s", nope.raw)
	}

	// The passport shows the real categories and where the ID is accepted.
	pass := h.do("GET", "/api/id/passport", session, nil)
	if pass.code != 200 {
		t.Fatalf("passport: %d %s", pass.code, pass.errMsg())
	}
	cats, _ := pass.body["categories"].([]any)
	if len(cats) != 1 {
		t.Fatalf("passport categories = %v", pass.body["categories"])
	}
}

// --- sanctions hit -> pending review -> auto-review confirm -> freeze ---------

func TestSanctionsHitFreezes(t *testing.T) {
	h := newM2Harness(t)
	// The OFAC SDN fixture confirm persona.
	session, aid, _ := h.registerPersona(vecPriv, "OFAC SDN TEST PERSONA CONFIRM", "DE")

	r := h.do("POST", "/api/id/verify", session, map[string]any{"residence": "DE", "base_eligibility": "ret"})
	if r.code != 200 {
		t.Fatalf("verify: %d %s", r.code, r.errMsg())
	}
	if got, _ := r.body["status"].(string); got != "pending_review" {
		t.Fatalf("status = %q, want pending_review", got)
	}
	if len(h.oa.userCategories(aid)) != 0 {
		t.Fatalf("no categories should be stamped on a flagged ID")
	}
	// The SIMULATED auto-reviewer confirms the deterministic persona (delay 0).
	h.s.runAutoReview(0)
	if !h.oa.frozen(aid) {
		t.Fatalf("confirmed sanctions hit must freeze the AID")
	}
	claims, _ := h.s.st.ClaimsByAID(aid)
	if claims == nil || claims.Status != "refused" {
		t.Fatalf("claims status = %v, want refused", claims)
	}
}

func TestSanctionsFalsePositiveClears(t *testing.T) {
	h := newM2Harness(t)
	session, aid, _ := h.registerPersona(vecPriv, "SEQPAL EU TEST FALSE POSITIVE CLEAR", "FR")

	r := h.do("POST", "/api/id/verify", session, map[string]any{"residence": "FR", "base_eligibility": "ret"})
	if got, _ := r.body["status"].(string); got != "pending_review" {
		t.Fatalf("status = %q, want pending_review (%s)", got, r.raw)
	}
	h.s.runAutoReview(0)
	if h.oa.frozen(aid) {
		t.Fatalf("a cleared false positive must not freeze")
	}
	if cats := h.oa.userCategories(aid); len(cats) != 1 || cats[0] != "j:FR:ret" {
		t.Fatalf("cleared identity categories = %v, want [j:FR:ret]", cats)
	}
	claims, _ := h.s.st.ClaimsByAID(aid)
	if claims == nil || claims.Status != "verified" {
		t.Fatalf("claims status = %v, want verified", claims)
	}
}

func dup(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
