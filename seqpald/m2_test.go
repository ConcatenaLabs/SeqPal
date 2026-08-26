package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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
		chalRL: newWindowLimiter(challengesPerKeyPerHour, challengesGlobalPerHour, time.Hour),
		catMu:  newKeyedMutex(),
		idv:    &testIDV{},
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
	if got, _ := r.body["status"].(string); got != "submitted" {
		t.Fatalf("status = %q, want submitted", got)
	}
	h.adjudicate(aid, idvClear)
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

// --- what a provider's decision does -----------------------------------------

// A provider refusal is final and binding: the identity is refused, the OpenAMP
// account frozen, and nothing left stamped. There is no SeqPal reviewer to
// appeal to, which is the point -- adjudication belongs to the provider.
func TestAProviderRefusalFreezesAndRefuses(t *testing.T) {
	h := newHarness(t)
	session, aid, _ := h.register(vecPriv, "Refused Rachel")
	if v := h.do("POST", "/api/id/verify", session, map[string]any{
		"residence": "DE", "base_eligibility": "ret",
	}); v.code != 200 {
		t.Fatalf("verify: %d %s", v.code, v.raw)
	}
	h.adjudicate(aid, idvReject)

	claims, _ := h.s.st.ClaimsByAID(aid)
	if claims == nil || claims.Status != "refused" {
		t.Fatalf("claims status = %v, want refused", claims)
	}
	if eligibilityLive(claims, time.Now().Unix()) {
		t.Fatal("a refused identity must not be eligible for anything")
	}
	// And the policy server was actually told. This assertion is the point of the
	// test's name and was missing: the stub had no freeze route, so the call
	// errored every time and the error was swallowed.
	if frozen, _ := h.oa.frozen.Load(aid); frozen != true {
		t.Fatalf("a refused identity must be frozen at the policy server, got %v", frozen)
	}
	if cats, _ := h.oa.categories.Load(aid); len(cats.([]string)) != 0 {
		t.Fatalf("a refused identity must be left carrying nothing, got %v", cats)
	}
}

// A refusal the policy server never heard is a refusal it does not enforce, so
// the check stays undecided until it has been. Anything else leaves a holder who
// was verified before this check still carrying live categories there.
func TestARefusalThePolicyServerNeverHeardIsNotFinished(t *testing.T) {
	h := newHarness(t)
	session, aid, _ := h.register(vecPriv, "Refused Rachel")
	if v := h.do("POST", "/api/id/verify", session, map[string]any{
		"residence": "DE", "base_eligibility": "ret",
	}); v.code != 200 {
		t.Fatalf("verify: %d %s", v.code, v.raw)
	}
	h.oa.freezeFail.Store(true)

	check, _ := h.s.st.LatestVerificationCheck(aid)
	if err := h.s.applyAdjudication(check, idvReject, ""); err == nil {
		t.Fatal("a refusal that could not be enforced must not report success")
	}
	if c, _ := h.s.st.ClaimsByAID(aid); c.Status != "refused" {
		t.Fatalf("the claims refuse regardless, since they can only restrict, got %v", c.Status)
	}
	if again, _ := h.s.st.LatestVerificationCheck(aid); again.Status != "submitted" {
		t.Fatalf("the check must stay open for another attempt, got %v", again.Status)
	}

	// Brought back, the reconciler finishes it.
	h.oa.freezeFail.Store(false)
	h.s.cfg.idvGrace = 0
	h.s.idv = &rejectingIDV{}
	h.s.reconcileVerifications()
	if frozen, _ := h.oa.frozen.Load(aid); frozen != true {
		t.Fatalf("the retry must reach the policy server, got %v", frozen)
	}
	if done, _ := h.s.st.LatestVerificationCheck(aid); done.Status != "complete" || done.Result != string(idvReject) {
		t.Fatalf("the check must finish as the refusal it was, got %+v", done)
	}
}

// rejectingIDV answers every poll with the refusal the provider had made.
type rejectingIDV struct{ testIDV }

func (p *rejectingIDV) PollCheck(*VerificationCheck) (idvDecision, string, bool, error) {
	return idvReject, "", true, nil
}

// The same, for a SeqPal ID that is only a wallet. A refusal used to freeze the
// OpenAMP account FIRST and return on any error from that call -- and such an ID
// has no account there, so the call failed every time and the function returned
// before it refused the claims. The identity stayed verified. A provider-driven
// refusal would have failed open in exactly the same way.
func TestAProviderRefusalBindsAWalletBackedID(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	session, aid := walletSession(t, h, testPKH)
	h.verifyIdentity(session, aid, map[string]any{
		"residence": "AE", "base_eligibility": "ret",
	})
	if c, _ := h.s.st.ClaimsByAID(aid); c == nil || c.Status != "verified" {
		t.Fatalf("the holder must start verified, got %v", c)
	}

	h.adjudicate(aid, idvReject)

	c, _ := h.s.st.ClaimsByAID(aid)
	if c == nil || c.Status != "refused" {
		t.Fatalf("a refusal must refuse the identity, got %v", c)
	}
	if eligibilityLive(c, time.Now().Unix()) {
		t.Fatal("a refused identity must not be eligible for anything")
	}
}

// The claims record carries this platform's signature over what it attests. A
// verification it could not sign is not recorded as one.
func TestAClearedVerificationIsAlwaysSigned(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	session, aid := walletSession(t, h, testPKH)
	h.verifyIdentity(session, aid, map[string]any{
		"residence": "AE", "base_eligibility": "ret",
	})
	c, _ := h.s.st.ClaimsByAID(aid)
	if c == nil || c.Status != "verified" {
		t.Fatalf("expected a verified record, got %v", c)
	}
	if c.ClaimsSig == "" {
		t.Fatal("a verification recorded with no signature attests nothing")
	}
}

// A refusal cannot be submitted away. Verification runs on details the holder
// declares, so without this an identity the provider refused could simply submit
// again under a different name and be cleared.
func TestARefusalCannotBeSubmittedAway(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	session, aid := walletSession(t, h, testPKH)
	if v := h.do("POST", "/api/id/verify", session, map[string]any{
		"residence": "AE", "base_eligibility": "ret",
	}); v.code != 200 {
		t.Fatalf("verify: %d %s", v.code, v.raw)
	}

	// While it is with the provider, submitting again is refused too: it is not a
	// decision to re-run, it is one that has not arrived.
	if again := h.do("POST", "/api/id/verify", session, map[string]any{
		"residence": "AE", "screening_name": "Someone Else Entirely",
	}); again.code != 409 {
		t.Fatalf("re-submitting a check in flight must be refused, got %d %s", again.code, again.raw)
	}

	h.adjudicate(aid, idvReject)
	third := h.do("POST", "/api/id/verify", session, map[string]any{
		"residence": "AE", "screening_name": "Someone Else Entirely",
	})
	if third.code != 409 {
		t.Fatalf("re-submitting after a refusal must be refused, got %d %s", third.code, third.raw)
	}
	if c, _ := h.s.st.ClaimsByAID(aid); c.Status != "refused" {
		t.Fatalf("the identity must stay refused, got %v", c)
	}
}

func TestVerifyingACompanyIsAComplianceDecision(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	session, aid := walletSession(t, h, testPKH)

	ent := h.do("POST", "/api/entities", session, map[string]any{
		"name": "Listed Holdings SA", "jurisdiction": "HN",
	})
	if ent.code != 200 {
		t.Fatalf("create entity: %d %s", ent.code, ent.raw)
	}
	entity, _ := ent.body["entity"].(map[string]any)
	entityID, _ := entity["id"].(string)

	// An unverified controller cannot vouch for a company.
	res := h.do("POST", "/api/id/entities/"+entityID+"/verify", session, map[string]any{})
	if res.code != 403 {
		t.Fatalf("an unverified controller must be refused, got %d %s", res.code, res.raw)
	}

	h.verifyIdentity(session, aid, map[string]any{
		"residence": "AE", "screening_name": "Wallet Wendy", "base_eligibility": "ret",
	})

	// A verified controller submits the company, and it goes to the provider like
	// everything else: nothing about it counts as verified until they decide.
	res = h.do("POST", "/api/id/entities/"+entityID+"/verify", session, map[string]any{})
	if res.code != 200 {
		t.Fatalf("a verified controller may submit a company: %d %s", res.code, res.raw)
	}
	if got, _ := res.body["status"].(string); got != "submitted" {
		t.Fatalf("entity verification status = %q, want submitted", got)
	}
	check, _ := h.s.st.LatestVerificationCheck(aid)
	if check == nil || check.Kind != "business" || check.EntityID != entityID {
		t.Fatalf("the company's check is %+v, want a business check for %s", check, entityID)
	}

	// And a company the provider refuses does not become verified.
	h.adjudicate(aid, idvReject)
	done, _ := h.s.st.LatestVerificationCheck(aid)
	if done.Status != "complete" || done.Result != string(idvReject) {
		t.Fatalf("the refusal was not recorded on the check: %+v", done)
	}
}

// The OFAC- and FATF-aligned sanctions floor is a compliance control the Legal
// and Licensing page states absolutely: "it can never be admitted". It was drawn
// in the browser's jurisdiction data and enforced by the wizard that renders it,
// so a matrix posted to the API directly compiled j:IR:ret into an asset's
// allowed categories and a resident of Iran carried it.
func TestTheSanctionsFloorIsEnforcedWhereTheRulesAreMade(t *testing.T) {
	// The compiler admits nothing on the floor, whatever the matrix says.
	terms := json.RawMessage(`{"jurisdictions":{
		"IR":{"access":"standard"},
		"KP":{"access":"restricted"},
		"DE":{"access":"standard"}
	}}`)
	rules, err := compileRules(terms, compileEnv{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rules.AllowedCategories {
		if strings.HasPrefix(c, "j:IR:") || strings.HasPrefix(c, "j:KP:") {
			t.Fatalf("the compiler admitted a floor jurisdiction: %v", rules.AllowedCategories)
		}
	}
	var sawDE bool
	for _, c := range rules.AllowedCategories {
		if strings.HasPrefix(c, "j:DE:") {
			sawDE = true
		}
	}
	if !sawDE {
		t.Fatalf("an ordinary jurisdiction was dropped with the floor: %v", rules.AllowedCategories)
	}

	// And the matrix is named back to the issuer rather than quietly narrowed.
	blocked := admittedFloorJurisdictions(terms)
	if len(blocked) != 2 || blocked[0] != "IR" || blocked[1] != "KP" {
		t.Fatalf("the floor jurisdictions this matrix admits are %v, want [IR KP]", blocked)
	}
	if len(admittedFloorJurisdictions(json.RawMessage(`{"jurisdictions":{"DE":{"access":"standard"}}}`))) != 0 {
		t.Fatal("an ordinary matrix was reported as admitting a floor jurisdiction")
	}
	// Naming one without admitting it is not admitting it.
	if len(admittedFloorJurisdictions(json.RawMessage(`{"jurisdictions":{"IR":{"access":"excluded"}}}`))) != 0 {
		t.Fatal("excluding a floor jurisdiction was read as admitting it")
	}
}

// The floor read through the DEPLOY endpoint, with the matrix in the request.
//
// The first version of this check read the issuance's stored terms, which for a
// fresh draft are empty, so it never fired on the path anyone actually uses. The
// unit test above did not catch that because it called the function directly.
// This one posts a deploy.
func TestADeployIsRefusedWhenItsMatrixAdmitsAFloorJurisdiction(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.damp = true
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	session, aid := walletSession(t, h, testPKH)
	iss := seedIssuanceOfKind(t, h.s, aid, "network")
	if err := h.s.st.UpdateIssuanceFields(iss.ID, map[string]any{"status": "draft"}); err != nil {
		t.Fatal(err)
	}

	res := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": iss.ID, "enforcement": "network", "supply": 1000, "precision": 0,
		"holder_key": strings.Repeat("d", 64),
		"terms": map[string]any{"jurisdictions": map[string]any{
			"IR": map[string]any{"access": "standard"},
			"DE": map[string]any{"access": "standard"},
		}},
	})
	if res.code != 422 {
		t.Fatalf("a matrix admitting a floor jurisdiction must be refused, got %d %s", res.code, res.raw)
	}
	msg, _ := res.body["error"].(string)
	if !strings.Contains(msg, "IR") {
		t.Fatalf("the refusal must name which: %q", msg)
	}
	if !strings.Contains(msg, "nothing was minted") {
		t.Fatalf("the refusal must say nothing happened: %q", msg)
	}

	// Excluding it is what a careful matrix does, and gets past this check.
	res = h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": iss.ID, "enforcement": "network", "supply": 1000, "precision": 0,
		"holder_key": strings.Repeat("d", 64),
		"terms": map[string]any{"jurisdictions": map[string]any{
			"IR": map[string]any{"access": "excluded"},
			"DE": map[string]any{"access": "standard"},
		}},
	})
	if res.code == 422 {
		t.Fatalf("excluding a floor jurisdiction was refused as admitting it: %s", res.raw)
	}
}

func dup(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}

// adjudicate delivers a provider decision for this harness.
func (h *m2Harness) adjudicate(aid string, decision idvDecision) {
	h.t.Helper()
	check, err := h.s.st.LatestVerificationCheck(aid)
	if err != nil || check == nil {
		h.t.Fatalf("no verification check for %s: %v", aid, err)
	}
	if err := h.s.applyAdjudication(check, decision, ""); err != nil {
		h.t.Fatalf("adjudicate %s as %s: %v", aid, decision, err)
	}
}
