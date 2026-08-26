package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

// --- upstream stub -------------------------------------------------------

// fakeOpenAMP stands in for openampd. It counts issue calls, which is how the
// idempotency test proves that a replayed deploy mints nothing.
type fakeOpenAMP struct {
	srv          *httptest.Server
	issues       atomic.Int32
	issueFail    atomic.Bool
	existingTick atomic.Value // string: a ticker already live upstream
	aidOverride  atomic.Value // string: AID to answer POST /v1/users with
	// extra, when set to an http.Handler, answers any path the routes below do
	// not, so a milestone's own test file can add the upstream surface it needs
	// without this stub having to know about it.
	extra atomic.Value
	// Which accounts have actually been registered, so a probe about an account
	// nobody registered answers the way the real policy server does.
	registered sync.Map
	// aid -> []string, the categories the policy server holds for that account.
	categories sync.Map
	// aid -> bool, whether the policy server has the account frozen. Without
	// this the stub answered no route for a freeze at all, so every freeze
	// errored and a test could not tell an account that was frozen from one
	// where the call never landed.
	frozen sync.Map
	// When set, the freeze route fails, standing in for a policy server that
	// cannot be reached.
	freezeFail atomic.Bool
}

func newFakeOpenAMP(t *testing.T) *fakeOpenAMP {
	t.Helper()
	f := &fakeOpenAMP{}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/assets", func(w http.ResponseWriter, r *http.Request) {
		assets := []map[string]any{}
		if tk, _ := f.existingTick.Load().(string); tk != "" {
			assets = append(assets, map[string]any{"asset": strings.Repeat("a", 64), "ticker": tk})
		}
		writeJSON(w, 200, map[string]any{"assets": assets})
	})

	mux.HandleFunc("POST /v1/users", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Pubkeys []string `json:"pubkeys"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		aid := aidFor(req.Pubkeys)
		if o, _ := f.aidOverride.Load().(string); o != "" {
			aid = o
		}
		f.registered.Store(aid, true)
		writeJSON(w, 200, map[string]any{"aid": aid})
	})

	mux.HandleFunc("POST /v1/issuer/assets", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-issuer-token" {
			writeJSON(w, 401, map[string]any{"error": "bad issuer token"})
			return
		}
		if f.issueFail.Load() {
			writeJSON(w, 500, map[string]any{"error": "upstream mint failed"})
			return
		}
		n := f.issues.Add(1)
		writeJSON(w, 200, map[string]any{
			"asset":         fmt.Sprintf("%064x", n),
			"txid":          fmt.Sprintf("%064x", 1000+n),
			"contract_hash": fmt.Sprintf("%064x", 2000+n),
		})
	})

	// The categories a registered account carries, which is what the policy
	// server is authoritative for. Without these two the stub 404s every read of
	// an account and stores no write, so a test could not tell a category that
	// was written to the wrong account id from one that was never written.
	mux.HandleFunc("GET /v1/users/{aid}", func(w http.ResponseWriter, r *http.Request) {
		aid := r.PathValue("aid")
		if _, ok := f.registered.Load(aid); !ok {
			writeJSON(w, 404, map[string]any{"error": "no such user"})
			return
		}
		cats := []string{}
		if v, ok := f.categories.Load(aid); ok {
			cats = append(cats, v.([]string)...)
		}
		isFrozen, _ := f.frozen.Load(aid)
		writeJSON(w, 200, map[string]any{"aid": aid, "categories": cats, "frozen": isFrozen == true})
	})

	mux.HandleFunc("POST /v1/issuer/freeze", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-issuer-token" {
			writeJSON(w, 401, map[string]any{"error": "bad issuer token"})
			return
		}
		if f.freezeFail.Load() {
			writeJSON(w, 503, map[string]any{"error": "the policy server is unreachable"})
			return
		}
		var req struct {
			AID    string `json:"aid"`
			Frozen bool   `json:"frozen"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		f.frozen.Store(req.AID, req.Frozen)
		writeJSON(w, 200, map[string]any{"ok": true})
	})

	mux.HandleFunc("POST /v1/issuer/categories", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-issuer-token" {
			writeJSON(w, 401, map[string]any{"error": "bad issuer token"})
			return
		}
		var req struct {
			AID        string   `json:"aid"`
			Categories []string `json:"categories"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if _, ok := f.registered.Load(req.AID); !ok {
			writeJSON(w, 404, map[string]any{"error": "no such user"})
			return
		}
		f.categories.Store(req.AID, append([]string{}, req.Categories...))
		writeJSON(w, 200, map[string]any{"ok": true})
	})

	// An account the policy server has never been told about has no address, and
	// says so. Answering every aid was the reason a check that fails closed on an
	// unresolvable address looked like it worked.
	mux.HandleFunc("GET /v1/users/{aid}/address", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := f.registered.Load(r.PathValue("aid")); !ok {
			writeJSON(w, 404, map[string]any{"error": "no such user"})
			return
		}
		writeJSON(w, 200, map[string]any{"address": "tb1p" + strings.Repeat("q", 58)})
	})

	mux.HandleFunc("GET /v1/issuer/holders", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-issuer-token" {
			writeJSON(w, 401, map[string]any{"error": "bad issuer token"})
			return
		}
		writeJSON(w, 404, map[string]any{"error": "no such asset"})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if h, _ := f.extra.Load().(http.Handler); h != nil {
			h.ServeHTTP(w, r)
			return
		}
		writeJSON(w, 404, map[string]any{"error": "no such upstream route: " + r.URL.Path})
	})

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// --- test harness --------------------------------------------------------

type harness struct {
	t  *testing.T
	s  *server
	h  http.Handler
	oa *fakeOpenAMP
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	oa := newFakeOpenAMP(t)
	st, err := openStore(filepath.Join(t.TempDir(), "seqpald.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := &server{
		cfg: config{
			openampURL:  oa.srv.URL,
			issuerToken: "test-issuer-token",
			network:     "sequentia-testnet",
		},
		st:     st,
		http:   &http.Client{Timeout: 5 * time.Second},
		rl:     newRateLimiter(),
		chalRL: newWindowLimiter(challengesPerKeyPerHour, challengesGlobalPerHour, time.Hour),
		// As production builds it. Without this a category write panics on a nil
		// mutex, which is a way for a test suite to never reach the write at all.
		catMu: newKeyedMutex(),
		// A provider that records the check and waits to be told. Tests drive the
		// adjudication through applyAdjudication, which is where a real callback
		// lands, so they exercise the decision path without depending on a
		// goroutine and a loopback HTTP round trip to settle first. The callback
		// endpoint itself has its own test.
		idv: &testIDV{},
	}
	return &harness{t: t, s: s, h: s.handler(), oa: oa}
}

type resp struct {
	code int
	body map[string]any
	raw  string
	set  []*http.Cookie
}

// do drives the real router, through the /seqpal prefix the proxy uses, with the
// session cookie carried by hand (the cookie is Secure, so no jar over plain
// http in tests).
// doWithHeader is do with extra request headers, for the callers that are not a
// browser session -- the verification provider's callback carries what proves it
// is theirs.
func (h *harness) doWithHeader(method, path, session string, hdr map[string]string, body any) resp {
	h.t.Helper()
	return h.doRaw(method, path, session, hdr, body)
}

func (h *harness) do(method, path, session string, body any) resp {
	h.t.Helper()
	return h.doRaw(method, path, session, nil, body)
}

func (h *harness) doRaw(method, path, session string, hdr map[string]string, body any) resp {
	h.t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, "/seqpal"+path, rdr)
	req.Header.Set("content-type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if session != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	res := rec.Result()
	out := resp{code: rec.Code, raw: rec.Body.String(), set: res.Cookies()}
	json.Unmarshal(rec.Body.Bytes(), &out.body)
	return out
}

func (r resp) errMsg() string {
	if e, ok := r.body["error"].(string); ok {
		return e
	}
	return r.raw
}

func (r resp) session() string {
	for _, c := range r.set {
		if c.Name == sessionCookie && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

func (h *harness) challenge(xonly string) string {
	h.t.Helper()
	r := h.do("POST", "/api/auth/challenge", "", map[string]any{"xonly": xonly})
	if r.code != 200 {
		h.t.Fatalf("challenge: %d %s", r.code, r.errMsg())
	}
	c, _ := r.body["challenge"].(string)
	if len(c) != 64 {
		h.t.Fatalf("challenge is not 32 bytes of hex: %q", c)
	}
	return c
}

// register creates an account for a private key and returns its session token.
func (h *harness) register(priv, name string) (session, aid, xonly string) {
	h.t.Helper()
	xonly = xonlyHex(h.t, priv)
	ch := h.challenge(xonly)
	r := h.do("POST", "/api/auth/register", "", map[string]any{
		"xonly": xonly, "challenge": ch, "sig": signChallengeHex(h.t, priv, ch),
		"display_name": name, "residence": "HN",
	})
	if r.code != 200 {
		h.t.Fatalf("register: %d %s", r.code, r.errMsg())
	}
	acct, _ := r.body["account"].(map[string]any)
	aid, _ = acct["aid"].(string)
	if aid != aidFor([]string{xonly}) {
		h.t.Fatalf("register returned aid %s, want %s", aid, aidFor([]string{xonly}))
	}
	if s := r.session(); s != "" {
		return s, aid, xonly
	}
	h.t.Fatal("register did not set a session cookie")
	return "", "", ""
}

func (h *harness) draftIssuance(session, name, ticker string, terms map[string]any) string {
	h.t.Helper()
	r := h.do("POST", "/api/issuances", session, map[string]any{
		"name": name, "ticker": ticker, "structure_id": "native-equity", "terms": terms,
	})
	if r.code != 200 {
		h.t.Fatalf("create issuance: %d %s", r.code, r.errMsg())
	}
	iss, _ := r.body["issuance"].(map[string]any)
	if got, _ := iss["status"].(string); got != "draft" {
		h.t.Fatalf("new issuance status = %q, want draft", got)
	}
	id, _ := iss["id"].(string)
	return id
}

// --- session cookie shape ------------------------------------------------

func TestSessionCookieAttributes(t *testing.T) {
	h := newHarness(t)
	xonly := xonlyHex(t, vecPriv)
	ch := h.challenge(xonly)
	r := h.do("POST", "/api/auth/register", "", map[string]any{
		"xonly": xonly, "challenge": ch, "sig": signChallengeHex(t, vecPriv, ch), "display_name": "Ida",
	})
	if r.code != 200 {
		t.Fatalf("register: %d %s", r.code, r.errMsg())
	}
	var c *http.Cookie
	for _, ck := range r.set {
		if ck.Name == sessionCookie {
			c = ck
		}
	}
	if c == nil {
		t.Fatal("no session cookie")
	}
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != sessionPath {
		t.Fatalf("session cookie is not HttpOnly/Secure/SameSite=Strict/Path=%s: %+v", sessionPath, c)
	}
}

// --- auth lifecycle ------------------------------------------------------

func TestAuthLifecycle(t *testing.T) {
	h := newHarness(t)
	session, aid, xonly := h.register(vecPriv, "Issuer Ida")

	// /me is the signed-in principal's whole world, and it is empty at first.
	me := h.do("GET", "/api/me", session, nil)
	if me.code != 200 {
		t.Fatalf("/me: %d %s", me.code, me.errMsg())
	}
	acct, _ := me.body["account"].(map[string]any)
	if got, _ := acct["aid"].(string); got != aid {
		t.Fatalf("/me aid = %s, want %s", got, aid)
	}
	if got, _ := acct["xonly"].(string); got != xonly {
		t.Fatalf("/me xonly = %s, want %s", got, xonly)
	}

	// No session, no access.
	if r := h.do("GET", "/api/me", "", nil); r.code != 401 {
		t.Fatalf("/me without a session = %d, want 401", r.code)
	}
	if r := h.do("GET", "/api/me", "deadbeef", nil); r.code != 401 {
		t.Fatalf("/me with a forged token = %d, want 401", r.code)
	}

	// Registering the same key twice is a 409, not a second account.
	ch := h.challenge(xonly)
	dup := h.do("POST", "/api/auth/register", "", map[string]any{
		"xonly": xonly, "challenge": ch, "sig": signChallengeHex(t, vecPriv, ch),
	})
	if dup.code != 409 {
		t.Fatalf("re-register = %d, want 409 (%s)", dup.code, dup.errMsg())
	}

	// Login with a fresh challenge works and yields a new session.
	ch = h.challenge(xonly)
	li := h.do("POST", "/api/auth/login", "", map[string]any{
		"xonly": xonly, "challenge": ch, "sig": signChallengeHex(t, vecPriv, ch),
	})
	if li.code != 200 || li.session() == "" {
		t.Fatalf("login = %d %s", li.code, li.errMsg())
	}

	// Logout revokes the row: the old token stops working.
	if r := h.do("POST", "/api/auth/logout", session, nil); r.code != 200 {
		t.Fatalf("logout = %d %s", r.code, r.errMsg())
	}
	if r := h.do("GET", "/api/me", session, nil); r.code != 401 {
		t.Fatalf("/me after logout = %d, want 401", r.code)
	}
}

func TestLoginRefusals(t *testing.T) {
	h := newHarness(t)
	_, _, xonly := h.register(vecPriv, "Issuer Ida")

	// A challenge is single use: replaying it is refused even with a good signature.
	ch := h.challenge(xonly)
	sig := signChallengeHex(t, vecPriv, ch)
	if r := h.do("POST", "/api/auth/login", "", map[string]any{"xonly": xonly, "challenge": ch, "sig": sig}); r.code != 200 {
		t.Fatalf("first login = %d %s", r.code, r.errMsg())
	}
	replay := h.do("POST", "/api/auth/login", "", map[string]any{"xonly": xonly, "challenge": ch, "sig": sig})
	if replay.code != 401 {
		t.Fatalf("replayed challenge = %d, want 401", replay.code)
	}

	// A stale challenge is refused.
	stale, _, err := h.s.st.CreateChallenge(xonly, -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	old := h.do("POST", "/api/auth/login", "", map[string]any{
		"xonly": xonly, "challenge": stale, "sig": signChallengeHex(t, vecPriv, stale),
	})
	if old.code != 401 {
		t.Fatalf("expired challenge = %d, want 401 (%s)", old.code, old.errMsg())
	}

	// A signature by the wrong key is refused (key2 signs, key1 is claimed).
	ch = h.challenge(xonly)
	wrong := h.do("POST", "/api/auth/login", "", map[string]any{
		"xonly": xonly, "challenge": ch, "sig": signChallengeHex(t, vecPriv2, ch),
	})
	if wrong.code != 401 {
		t.Fatalf("wrong-key signature = %d, want 401", wrong.code)
	}

	// A RAW (untagged) signature over the challenge digest is refused: the enclave
	// key must never be usable as a signing oracle over supplied digests.
	rawCh := h.challenge(vecXOnly)
	rawSig := rawSignHex(t, vecPriv, sha256.Sum256([]byte(rawCh)))
	raw := h.do("POST", "/api/auth/login", "", map[string]any{
		"xonly": vecXOnly, "challenge": rawCh, "sig": rawSig,
	})
	if raw.code != 401 {
		t.Fatalf("raw-signed digest accepted (%d): the challenge is a signing oracle", raw.code)
	}

	// A challenge issued for one key cannot be used by another.
	other := xonlyHex(t, vecPriv2)
	chForOther := h.challenge(other)
	cross := h.do("POST", "/api/auth/login", "", map[string]any{
		"xonly": xonly, "challenge": chForOther, "sig": signChallengeHex(t, vecPriv, chForOther),
	})
	if cross.code != 401 {
		t.Fatalf("challenge bound to another key was accepted (%d)", cross.code)
	}

	// An unknown key that signs correctly is a 404, not a session.
	ch2 := h.challenge(other)
	unknown := h.do("POST", "/api/auth/login", "", map[string]any{
		"xonly": other, "challenge": ch2, "sig": signChallengeHex(t, vecPriv2, ch2),
	})
	if unknown.code != 404 {
		t.Fatalf("login for an unregistered key = %d, want 404", unknown.code)
	}
}

// --- ownership -----------------------------------------------------------

func TestOwnershipIsDerivedFromTheSession(t *testing.T) {
	h := newHarness(t)
	ida, _, _ := h.register(vecPriv, "Issuer Ida")
	bob, bobAID, _ := h.register(vecPriv2, "Bob")

	id := h.draftIssuance(ida, "Aurora Ventures Fund I", "AURA", map[string]any{"structure": "native-equity"})

	// Bob sees none of Ida's issuances.
	list := h.do("GET", "/api/issuances", bob, nil)
	if list.code != 200 {
		t.Fatalf("/issuances: %d", list.code)
	}
	if items, _ := list.body["issuances"].([]any); len(items) != 0 {
		t.Fatalf("another account's issuance list has %d rows, want 0", len(items))
	}

	// Bob cannot edit or deploy it.
	if r := h.do("PATCH", "/api/issuances/"+id, bob, map[string]any{"name": "Bob's now"}); r.code != 403 {
		t.Fatalf("cross-account PATCH = %d, want 403", r.code)
	}
	dep := h.do("POST", "/api/deploy", bob, map[string]any{"issuance_id": id, "supply": 1000, "precision": 2})
	if dep.code != 403 {
		t.Fatalf("cross-account deploy = %d, want 403", dep.code)
	}
	if h.oa.issues.Load() != 0 {
		t.Fatal("a cross-account deploy reached the mint")
	}
	// The refusal is on the record, against the account that tried.
	assertAudited(t, h, bobAID, "issuance.access.refused")

	// Ida can, and an unknown id is a 404 for everybody.
	if r := h.do("PATCH", "/api/issuances/"+id, ida, map[string]any{"name": "Aurora Ventures Fund II"}); r.code != 200 {
		t.Fatalf("owner PATCH = %d %s", r.code, r.errMsg())
	}
	if r := h.do("PATCH", "/api/issuances/nosuchid", ida, map[string]any{"name": "x"}); r.code != 404 {
		t.Fatalf("unknown issuance = %d, want 404", r.code)
	}

	// An entity belonging to Ida cannot be attached to Bob's issuance.
	ent := h.do("POST", "/api/entities", ida, map[string]any{"name": "Aurora Holdings Ltd", "jurisdiction": "HN-PROSPERA"})
	if ent.code != 200 {
		t.Fatalf("create entity = %d %s", ent.code, ent.errMsg())
	}
	e, _ := ent.body["entity"].(map[string]any)
	entID, _ := e["id"].(string)
	steal := h.do("POST", "/api/issuances", bob, map[string]any{
		"name": "Bob Co", "ticker": "BOBC", "entity_id": entID,
	})
	if steal.code != 403 {
		t.Fatalf("issuance against another account's entity = %d, want 403", steal.code)
	}
}

// --- deploy validation ---------------------------------------------------

func TestDeployValidationMatrix(t *testing.T) {
	h := newHarness(t)
	ida, _, _ := h.register(vecPriv, "Issuer Ida")
	id := h.draftIssuance(ida, "Aurora Ventures Fund I", "AURA", map[string]any{"structure": "native-equity"})

	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"supply 0", map[string]any{"issuance_id": id, "supply": 0, "precision": 2}, 400},
		{"precision omitted", map[string]any{"issuance_id": id, "supply": 1000}, 400},
		{"precision 9", map[string]any{"issuance_id": id, "supply": 1000, "precision": 9}, 400},
		{"precision negative", map[string]any{"issuance_id": id, "supply": 1000, "precision": -1}, 400},
		{"supply overflows atoms", map[string]any{"issuance_id": id, "supply": uint64(1) << 62, "precision": 8}, 400},
		{"unknown issuance", map[string]any{"issuance_id": "nope", "supply": 10, "precision": 2}, 404},
		{"terms_hash mismatch", map[string]any{
			"issuance_id": id, "supply": 1000, "precision": 2,
			"terms": map[string]any{"structure": "native-equity"}, "terms_hash": strings.Repeat("0", 64)}, 400},
	}
	for _, c := range cases {
		r := h.do("POST", "/api/deploy", ida, c.body)
		if r.code != c.want {
			t.Errorf("%s: %d, want %d (%s)", c.name, r.code, c.want, r.errMsg())
		}
		if msg := r.errMsg(); msg == "" || strings.HasPrefix(msg, "{") {
			t.Errorf("%s: refusal carries no usable message: %q", c.name, msg)
		}
	}

	// Ticker rules bite at issuance creation, so a bad ticker cannot reach deploy
	// through the API at all.
	for _, bad := range []string{"a", "TOOLONGTICKER", "AU-RA", "AU RA", ""} {
		r := h.do("POST", "/api/issuances", ida, map[string]any{"name": "X", "ticker": bad})
		if r.code != 400 {
			t.Errorf("create issuance with ticker %q = %d, want 400", bad, r.code)
		}
	}
	// Case is normalized, not rejected.
	norm := h.do("POST", "/api/issuances", ida, map[string]any{"name": "X", "ticker": " lower "})
	if norm.code != 200 {
		t.Errorf("create issuance with a lowercase ticker = %d, want 200 (normalized)", norm.code)
	} else if iss, _ := norm.body["issuance"].(map[string]any); iss["ticker"] != "LOWER" {
		t.Errorf("lowercase ticker stored as %v, want LOWER", iss["ticker"])
	}
	for _, reserved := range []string{"SEQ", "TSEQ", "BTC", "TBTC", "USDX", "EURX", "GOLD", "SILVR", "OILX"} {
		r := h.do("POST", "/api/issuances", ida, map[string]any{"name": "X", "ticker": reserved})
		if r.code != 400 || !strings.Contains(r.errMsg(), "reserved") {
			t.Errorf("create issuance with reserved ticker %s = %d (%s), want 400", reserved, r.code, r.errMsg())
		}
	}

	// And they bite again at deploy, for a record that reached the store some other
	// way: a reserved or malformed ticker never mints.
	for _, bad := range []string{"SEQ", "aura"} {
		if err := h.s.st.UpdateIssuanceFields(id, map[string]any{"ticker": bad}); err != nil {
			t.Fatal(err)
		}
		r := h.do("POST", "/api/deploy", ida, map[string]any{"issuance_id": id, "supply": 1000, "precision": 2})
		if r.code != 400 {
			t.Errorf("deploy with stored ticker %q = %d, want 400 (%s)", bad, r.code, r.errMsg())
		}
	}
	if h.oa.issues.Load() != 0 {
		t.Fatalf("a refused deploy still minted (%d issue calls)", h.oa.issues.Load())
	}

	// Precision 0 is a REAL value (an integer-only asset), not "unset": it must
	// deploy, never be refused (the repo's documented trap). Only an OMITTED
	// precision is refused, which the matrix above already proves.
	//
	// The legacy "confidential" deploy field rides along here to prove it is
	// IGNORED for backward compatibility: confidentiality is a per-transfer
	// choice now, so a deploy carrying it still mints an ordinary transparent
	// asset (200), never a 501.
	zid := h.draftIssuance(ida, "Integer Fund", "INTF", map[string]any{"structure": "native-equity"})
	if r := h.do("POST", "/api/deploy", ida, map[string]any{
		"issuance_id": zid, "supply": 1000, "precision": 0, "confidential": true,
	}); r.code != 200 {
		t.Fatalf("precision-0 deploy (with a legacy confidential field) = %d, want 200 (%s)", r.code, r.errMsg())
	}
}

func TestDeployTickerCollision(t *testing.T) {
	h := newHarness(t)
	h.oa.existingTick.Store("aura") // upstream match is case-insensitive
	ida, _, _ := h.register(vecPriv, "Issuer Ida")
	id := h.draftIssuance(ida, "Aurora Ventures Fund I", "AURA", map[string]any{"structure": "native-equity"})

	r := h.do("POST", "/api/deploy", ida, map[string]any{"issuance_id": id, "supply": 1000, "precision": 2})
	if r.code != 409 {
		t.Fatalf("deploy against a live ticker = %d, want 409 (%s)", r.code, r.errMsg())
	}
	if h.oa.issues.Load() != 0 {
		t.Fatal("a colliding ticker minted anyway")
	}
}

func TestDeployRefusedWhenUpstreamAIDDisagrees(t *testing.T) {
	h := newHarness(t)
	h.oa.aidOverride.Store(strings.Repeat("f", 40)) // policy server answers with someone else's account
	ida, _, _ := h.register(vecPriv, "Issuer Ida")
	id := h.draftIssuance(ida, "Aurora Ventures Fund I", "AURA", map[string]any{"structure": "native-equity"})

	r := h.do("POST", "/api/deploy", ida, map[string]any{"issuance_id": id, "supply": 1000, "precision": 2})
	if r.code != 502 {
		t.Fatalf("deploy with a mismatched upstream AID = %d, want 502 (%s)", r.code, r.errMsg())
	}
	if h.oa.issues.Load() != 0 {
		t.Fatal("minted into an account whose AID did not match the signed-in key")
	}
}

// --- deploy success and idempotency --------------------------------------

func TestDeploySuccessAndIdempotency(t *testing.T) {
	h := newHarness(t)
	ida, aid, _ := h.register(vecPriv, "Issuer Ida")
	terms := map[string]any{
		"structure":             "native-equity",
		"jurisdiction":          "HN-PROSPERA",
		"transfer_restrictions": map[string]any{"lockup_days": 365, "accredited_only": true},
	}
	id := h.draftIssuance(ida, "Aurora Ventures Fund I", "AURA", terms)

	body := map[string]any{"issuance_id": id, "supply": 1000000, "precision": 2, "terms": terms}
	first := h.do("POST", "/api/deploy", ida, body)
	if first.code != 200 {
		t.Fatalf("deploy = %d %s", first.code, first.errMsg())
	}
	asset, _ := first.body["asset"].(string)
	txid, _ := first.body["txid"].(string)
	if asset == "" || txid == "" {
		t.Fatalf("deploy returned no asset/txid: %s", first.raw)
	}
	if got, _ := first.body["aid"].(string); got != aid {
		t.Fatalf("deploy minted into aid %s, want the session's %s", got, aid)
	}

	// The issuance is now live, and it carries the on-chain facts.
	me := h.do("GET", "/api/me", ida, nil)
	items, _ := me.body["issuances"].([]any)
	if len(items) != 1 {
		t.Fatalf("/me lists %d issuances, want 1", len(items))
	}
	iss, _ := items[0].(map[string]any)
	if got, _ := iss["status"].(string); got != "live" {
		t.Fatalf("issuance status = %q, want live", got)
	}
	if got, _ := iss["asset_id"].(string); got != asset {
		t.Fatalf("issuance asset_id = %q, want %q", got, asset)
	}

	// Replay: same key, same terms. One mint, same answer.
	second := h.do("POST", "/api/deploy", ida, body)
	if second.code != 200 {
		t.Fatalf("replayed deploy = %d %s", second.code, second.errMsg())
	}
	if got, _ := second.body["asset"].(string); got != asset {
		t.Fatalf("replay returned asset %s, want the original %s", got, asset)
	}
	if got, _ := second.body["txid"].(string); got != txid {
		t.Fatalf("replay returned txid %s, want the original %s", got, txid)
	}

	// The same terms with every key reordered is the same terms: still one mint.
	reordered := map[string]any{
		"transfer_restrictions": map[string]any{"accredited_only": true, "lockup_days": 365},
		"jurisdiction":          "HN-PROSPERA",
		"structure":             "native-equity",
	}
	third := h.do("POST", "/api/deploy", ida, map[string]any{
		"issuance_id": id, "supply": 1000000, "precision": 2, "terms": reordered,
	})
	if third.code != 200 {
		t.Fatalf("reordered-terms deploy = %d %s", third.code, third.errMsg())
	}
	if got, _ := third.body["asset"].(string); got != asset {
		t.Fatalf("reordered terms minted a second asset (%s, want %s)", got, asset)
	}

	if n := h.oa.issues.Load(); n != 1 {
		t.Fatalf("upstream saw %d issue calls, want exactly 1", n)
	}
	assertAudited(t, h, aid, "deploy.success")
	assertAudited(t, h, aid, "deploy.replay")
}

// Two distinct issuances by one account with equal (default, empty) terms must mint
// two distinct assets. The idempotency key therefore has to include the issuance id;
// keyed only on (xonly, terms_hash) the second deploy would return the first's asset
// and strand the second draft forever while telling the user it deployed.
func TestDeployIdempotencyIsScopedToTheIssuance(t *testing.T) {
	h := newHarness(t)
	ida, _, _ := h.register(vecPriv, "Issuer Ida")

	// Two drafts, distinct tickers, identical default terms.
	idGamma := h.draftIssuance(ida, "Fund Gamma", "GAMMA", map[string]any{})
	idDelta := h.draftIssuance(ida, "Fund Delta", "DELTA", map[string]any{})

	first := h.do("POST", "/api/deploy", ida, map[string]any{
		"issuance_id": idGamma, "supply": 1000000, "precision": 2, "terms": map[string]any{},
	})
	if first.code != 200 {
		t.Fatalf("gamma deploy = %d %s", first.code, first.errMsg())
	}
	gammaAsset, _ := first.body["asset"].(string)

	second := h.do("POST", "/api/deploy", ida, map[string]any{
		"issuance_id": idDelta, "supply": 1000000, "precision": 2, "terms": map[string]any{},
	})
	if second.code != 200 {
		t.Fatalf("delta deploy = %d %s", second.code, second.errMsg())
	}
	deltaAsset, _ := second.body["asset"].(string)

	if deltaAsset == "" || deltaAsset == gammaAsset {
		t.Fatalf("delta returned asset %q, want a distinct asset from gamma's %q", deltaAsset, gammaAsset)
	}
	if got, _ := second.body["issuance_id"].(string); got != idDelta {
		t.Fatalf("delta deploy reported issuance_id %q, want %q", got, idDelta)
	}
	if n := h.oa.issues.Load(); n != 2 {
		t.Fatalf("upstream saw %d issue calls for two distinct issuances, want 2", n)
	}
}

func TestDeployRateLimit(t *testing.T) {
	h := newHarness(t)
	ida, aid, _ := h.register(vecPriv, "Issuer Ida")

	for i := 0; i < deploysPerAccountPerHour; i++ {
		id := h.draftIssuance(ida, fmt.Sprintf("Fund %d", i), fmt.Sprintf("FUND%d", i),
			map[string]any{"n": i})
		r := h.do("POST", "/api/deploy", ida, map[string]any{
			"issuance_id": id, "supply": 100, "precision": 2, "terms": map[string]any{"n": i},
		})
		if r.code != 200 {
			t.Fatalf("deploy %d = %d %s", i, r.code, r.errMsg())
		}
	}
	id := h.draftIssuance(ida, "Fund over", "OVER", map[string]any{"n": 99})
	over := h.do("POST", "/api/deploy", ida, map[string]any{
		"issuance_id": id, "supply": 100, "precision": 2, "terms": map[string]any{"n": 99},
	})
	if over.code != 429 {
		t.Fatalf("deploy past the account budget = %d, want 429 (%s)", over.code, over.errMsg())
	}
	if n := h.oa.issues.Load(); n != int32(deploysPerAccountPerHour) {
		t.Fatalf("upstream saw %d issue calls, want %d", n, deploysPerAccountPerHour)
	}
	assertAudited(t, h, aid, "deploy.refused")

	// The refusal that was audited is the rate-limit one, with its status.
	rows := auditRows(t, h)
	found := false
	for _, r := range rows {
		if r.Action == "deploy.refused" && strings.Contains(r.Detail, `"status":429`) {
			found = true
		}
	}
	if !found {
		t.Fatal("the rate-limit refusal is not in the audit log with status 429")
	}

	// The budget is per account: another account still deploys.
	bob, _, _ := h.register(vecPriv2, "Bob")
	bobIss := h.draftIssuance(bob, "Bob Fund", "BOBF", map[string]any{"n": 1})
	if r := h.do("POST", "/api/deploy", bob, map[string]any{
		"issuance_id": bobIss, "supply": 100, "precision": 2, "terms": map[string]any{"n": 1},
	}); r.code != 200 {
		t.Fatalf("another account's deploy = %d, want 200 (the limit must be per account)", r.code)
	}
}

func TestDeployWithoutIssuerTokenIsRefusedBeforeMinting(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.issuerToken = ""
	ida, _, _ := h.register(vecPriv, "Issuer Ida")
	id := h.draftIssuance(ida, "Aurora Ventures Fund I", "AURA", map[string]any{"structure": "native-equity"})

	r := h.do("POST", "/api/deploy", ida, map[string]any{"issuance_id": id, "supply": 100, "precision": 2})
	if r.code != 503 {
		t.Fatalf("deploy with no issuer token = %d, want 503 (%s)", r.code, r.errMsg())
	}
	if h.oa.issues.Load() != 0 {
		t.Fatal("minted without an issuer token")
	}
}

func TestDeployUpstreamMintFailureLeavesNothingBehind(t *testing.T) {
	h := newHarness(t)
	h.oa.issueFail.Store(true)
	ida, _, _ := h.register(vecPriv, "Issuer Ida")
	terms := map[string]any{"structure": "native-equity"}
	id := h.draftIssuance(ida, "Aurora Ventures Fund I", "AURA", terms)
	body := map[string]any{"issuance_id": id, "supply": 100, "precision": 2, "terms": terms}

	if r := h.do("POST", "/api/deploy", ida, body); r.code != 502 {
		t.Fatalf("failed upstream mint = %d, want 502 (%s)", r.code, r.errMsg())
	}
	iss, err := h.s.st.IssuanceByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if iss.Status != "draft" || iss.AssetID != "" {
		t.Fatalf("a failed mint left the issuance as %s/%s, want draft and no asset", iss.Status, iss.AssetID)
	}

	// No deploy row was written, so the user can retry once the upstream recovers.
	h.oa.issueFail.Store(false)
	r := h.do("POST", "/api/deploy", ida, body)
	if r.code != 200 {
		t.Fatalf("retry after a failed mint = %d %s", r.code, r.errMsg())
	}
	if n := h.oa.issues.Load(); n != 1 {
		t.Fatalf("upstream saw %d successful issue calls, want 1", n)
	}
}

// --- health --------------------------------------------------------------

func TestHealthProbesTheIssuerToken(t *testing.T) {
	h := newHarness(t)
	ok := h.do("GET", "/api/health", "", nil)
	if ok.code != 200 {
		t.Fatalf("/health = %d", ok.code)
	}
	if ok.body["openamp_ok"] != true || ok.body["issuer_token_ok"] != true || ok.body["ok"] != true {
		t.Fatalf("/health with a good token = %s, want all true", ok.raw)
	}
	if got, _ := ok.body["network"].(string); got != "sequentia-testnet" {
		t.Fatalf("/health network = %q", got)
	}

	// A bad token must be reported, not hidden: the UI has to be able to say
	// deployment is unavailable before a user reaches the mint.
	h.s.cfg.issuerToken = "wrong"
	bad := h.do("GET", "/api/health", "", nil)
	if bad.body["issuer_token_ok"] != false || bad.body["ok"] != false {
		t.Fatalf("/health with a bad token = %s, want issuer_token_ok false", bad.raw)
	}
	if bad.body["openamp_ok"] != true {
		t.Fatalf("/health should still see the policy server: %s", bad.raw)
	}
}

// --- static SPA ----------------------------------------------------------

func TestStaticFallbackAndTraversal(t *testing.T) {
	h := newHarness(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<!doctype html>SPA"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(root), "secret.txt"), []byte("issuer token"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.s.cfg.webroot = root
	h.h = h.s.handler()

	// A client route falls back to index.html.
	if r := h.do("GET", "/id/register", "", nil); r.code != 200 || !strings.Contains(r.raw, "SPA") {
		t.Fatalf("SPA fallback = %d %q", r.code, r.raw)
	}
	// Nothing above the webroot is reachable, however the path is spelled.
	for _, p := range []string{"/../secret.txt", "/..%2fsecret.txt", "/assets/../../secret.txt"} {
		req := httptest.NewRequest("GET", "/seqpal"+p, nil)
		rec := httptest.NewRecorder()
		h.h.ServeHTTP(rec, req)
		if strings.Contains(rec.Body.String(), "issuer token") {
			t.Fatalf("path traversal served a file outside the webroot: %s", p)
		}
	}
	// The API still answers with JSON, not the SPA.
	if r := h.do("GET", "/api/me", "", nil); r.code != 401 {
		t.Fatalf("/api/me under a webroot = %d, want 401", r.code)
	}
	if r := h.do("GET", "/api/nope", "", nil); r.code != 404 || r.errMsg() != "no such endpoint" {
		t.Fatalf("unknown API route = %d %q", r.code, r.errMsg())
	}
}

// --- audit chain ---------------------------------------------------------

type auditRow struct {
	Seq      int64
	Ts       int64
	Actor    string
	Action   string
	Detail   string
	PrevHash string
	Hash     string
}

func auditRows(t *testing.T, h *harness) []auditRow {
	t.Helper()
	rows, err := h.s.st.db.Query(`SELECT seq, ts, actor_aid, action, detail, prev_hash, hash FROM audit_log ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		if err := rows.Scan(&r.Seq, &r.Ts, &r.Actor, &r.Action, &r.Detail, &r.PrevHash, &r.Hash); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func assertAudited(t *testing.T, h *harness, actor, action string) {
	t.Helper()
	for _, r := range auditRows(t, h) {
		if r.Action == action && r.Actor == actor {
			return
		}
	}
	t.Fatalf("no audit row %q for %s", action, actor)
}

// TestAuditChainIntegrity recomputes the chain independently of the writer:
// hash = sha256(prev_hash || ts || actor_aid || action || canonical(detail)).
func TestAuditChainIntegrity(t *testing.T) {
	h := newHarness(t)
	ida, idaAID, _ := h.register(vecPriv, "Issuer Ida")
	bob, _, _ := h.register(vecPriv2, "Bob")
	id := h.draftIssuance(ida, "Aurora Ventures Fund I", "AURA", map[string]any{"structure": "native-equity"})
	h.do("POST", "/api/issuances/"+id, ida, nil)                                  // 404 route, no row
	h.do("PATCH", "/api/issuances/"+id, ida, map[string]any{"name": "Aurora II"}) // issuance.update
	h.do("PATCH", "/api/issuances/"+id, bob, map[string]any{"name": "mine now"})  // issuance.access.refused
	h.do("POST", "/api/entities", ida, map[string]any{"name": "Aurora Holdings Ltd"})
	h.do("POST", "/api/deploy", ida, map[string]any{"issuance_id": id, "supply": 0, "precision": 2}) // deploy.refused
	h.do("POST", "/api/deploy", ida, map[string]any{
		"issuance_id": id, "supply": 100, "precision": 2, "terms": map[string]any{"structure": "native-equity"},
	}) // deploy.attempt + deploy.success
	ch := h.challenge(vecXOnly)
	h.do("POST", "/api/auth/login", "", map[string]any{"xonly": vecXOnly, "challenge": ch, "sig": strings.Repeat("0", 128)}) // auth.login.refused
	h.do("POST", "/api/auth/logout", ida, nil)

	rows := auditRows(t, h)
	if len(rows) < 8 {
		t.Fatalf("audit log has %d rows, want the whole privileged sequence", len(rows))
	}
	prev := ""
	for i, r := range rows {
		if r.PrevHash != prev {
			t.Fatalf("row %d (%s): prev_hash = %q, want %q (chain is broken)", r.Seq, r.Action, r.PrevHash, prev)
		}
		hsh := sha256.New()
		hsh.Write([]byte(r.PrevHash))
		hsh.Write([]byte(strconv.FormatInt(r.Ts, 10)))
		hsh.Write([]byte(r.Actor))
		hsh.Write([]byte(r.Action))
		hsh.Write([]byte(r.Detail))
		want := hex.EncodeToString(hsh.Sum(nil))
		if r.Hash != want {
			t.Fatalf("row %d (%s): hash = %s, recomputed %s", r.Seq, r.Action, r.Hash, want)
		}
		if int64(i+1) != r.Seq {
			t.Fatalf("audit seq is not dense: row %d has seq %d", i, r.Seq)
		}
		prev = r.Hash
	}

	// Every privileged action in the sequence left a row.
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Action] = true
	}
	for _, want := range []string{
		"auth.register", "auth.login.refused", "auth.logout", "entity.create",
		"issuance.create", "issuance.update", "issuance.access.refused",
		"deploy.refused", "deploy.attempt", "deploy.success",
	} {
		if !seen[want] {
			t.Errorf("no %q row in the audit log", want)
		}
	}
	assertAudited(t, h, idaAID, "deploy.success")

	// Tampering with one row's detail must break the recomputation: the chain is
	// only worth anything if it detects that.
	if _, err := h.s.st.db.Exec(`UPDATE audit_log SET detail = '{"tampered":true}' WHERE seq = 1`); err != nil {
		t.Fatal(err)
	}
	tampered := auditRows(t, h)[0]
	hsh := sha256.New()
	hsh.Write([]byte(tampered.PrevHash))
	hsh.Write([]byte(strconv.FormatInt(tampered.Ts, 10)))
	hsh.Write([]byte(tampered.Actor))
	hsh.Write([]byte(tampered.Action))
	hsh.Write([]byte(tampered.Detail))
	if hex.EncodeToString(hsh.Sum(nil)) == tampered.Hash {
		t.Fatal("an edited audit row still matches its hash")
	}
}

// rawSignHex signs a bare 32-byte digest, which is exactly what no conforming
// client may ever do with an enclave key; the tests use it to prove the server
// refuses such signatures.
func rawSignHex(t *testing.T, privHex string, digest [32]byte) string {
	t.Helper()
	pb, err := hex.DecodeString(privHex)
	if err != nil {
		t.Fatal(err)
	}
	priv, _ := btcec.PrivKeyFromBytes(pb)
	sig, err := schnorr.Sign(priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(sig.Serialize())
}

// testIDV is the identity-verification provider a test runs against: it accepts
// a check and decides nothing until the test says so.
type testIDV struct{}

func (p *testIDV) Name() string { return "test" }

func (p *testIDV) CreateCheck(c *VerificationCheck) (string, error) {
	return "test-" + c.ID, nil
}

// The test provider never decides on its own: tests adjudicate explicitly, so
// the reconciler must find nothing to do.
func (p *testIDV) PollCheck(*VerificationCheck) (idvDecision, string, bool, error) {
	return "", "", false, nil
}

// adjudicate delivers a provider decision for an account's most recent check,
// the way the callback would.
func (h *harness) adjudicate(aid string, decision idvDecision) {
	h.t.Helper()
	check, err := h.s.st.LatestVerificationCheck(aid)
	if err != nil || check == nil {
		h.t.Fatalf("no verification check for %s: %v", aid, err)
	}
	if err := h.s.applyAdjudication(check, decision, ""); err != nil {
		h.t.Fatalf("adjudicate %s as %s: %v", aid, decision, err)
	}
}

// verifyIdentity submits a verification and has the provider clear it, which is
// what most tests mean when they say an identity is verified.
func (h *harness) verifyIdentity(session, aid string, body map[string]any) {
	h.t.Helper()
	res := h.do("POST", "/api/id/verify", session, body)
	if res.code != 200 {
		h.t.Fatalf("verify: %d %s", res.code, res.raw)
	}
	h.adjudicate(aid, idvClear)
}
