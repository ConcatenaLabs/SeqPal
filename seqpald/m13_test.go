package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// M13: the holder-list and frozen-coin console for a network-enforced token, and
// the branch every OTHER flow has to take for one.
//
// The second half matters as much as the first. A flow written against a
// platform-held account and a platform co-signature does not fail cleanly for a
// network-enforced token, it fails obscurely, so each of these asserts a REFUSAL
// WITH A REASON rather than merely a non-200.

// --- upstream stub -----------------------------------------------------------

// dampUpstream is the policy server's network-enforcement surface: the published
// policy document, and the two-phase policy update. It records what it was asked
// so a test can prove a refusal reached it (or never did).
type dampUpstream struct {
	asset string

	// holders is the published holder list in its two document shapes: a bare
	// hex string for a holder with no height bounds, an object for one with a
	// lockup or a receive window. Both shapes exist so that a document written
	// before bounds existed still hashes and verifies exactly as it did.
	holders []any
	frozen  []string // published frozen-coin fingerprints
	seq     uint64
	pi      string

	prepares  int
	completes int
	lastBody  map[string]any
	published bool

	// failComplete, when set, is the status and message the completion answers.
	failComplete    int
	failCompleteMsg string
}

func (u *dampUpstream) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/snapshots", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("asset") != u.asset {
			writeJSON(w, 404, map[string]any{"error": "no such snapshot"})
			return
		}
		writeJSON(w, 200, map[string]any{
			"asset": u.asset, "seq": u.seq, "pi": u.pi, "hash": strings.Repeat("dd", 32),
			"snapshot": map[string]any{
				"predicates": map[string]any{
					"whitelist": map[string]any{"root": strings.Repeat("11", 32), "entries": u.holders},
					"blacklist": map[string]any{"root": strings.Repeat("22", 32), "entries": u.frozen},
				},
			},
		})
	})
	mux.HandleFunc("POST /v1/issuer/damp-policy", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-issuer-token" {
			writeJSON(w, 401, map[string]any{"error": "bad issuer token"})
			return
		}
		u.prepares++
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		u.lastBody = body
		if body["reason"] == "" || body["reason"] == nil {
			writeJSON(w, 400, map[string]any{"error": "a reason is required"})
			return
		}
		writeJSON(w, 200, map[string]any{
			"policy_id": fmt.Sprintf("pol-%d", u.prepares),
			"asset":     u.asset,
			"seq":       u.seq + 1,
			"prev_pi":   u.pi,
			"pi_next":   strings.Repeat("ab", 32),
			"whitelist": u.holders,
			"blacklist": u.frozen,
			"change": map[string]any{
				"removed_holders": body["remove_whitelist"],
				"added_holders":   body["add_whitelist"],
			},
			"to_sign":         strings.Repeat("5e", 32),
			"derive_snapshot": map[string]any{"v": 1, "asset": u.asset, "seq": u.seq + 1},
		})
	})
	mux.HandleFunc("POST /v1/issuer/damp-policy/{id}/complete", func(w http.ResponseWriter, r *http.Request) {
		u.completes++
		if u.failComplete != 0 {
			writeJSON(w, u.failComplete, map[string]any{"error": u.failCompleteMsg})
			return
		}
		u.published = true
		writeJSON(w, 200, map[string]any{
			"policy_id": r.PathValue("id"), "txid": strings.Repeat("7f", 32),
			"seq": u.seq + 1, "pi": strings.Repeat("ab", 32),
		})
	})
	return mux
}

// --- fixture -----------------------------------------------------------------

type networkFixture struct {
	h       *harness
	up      *dampUpstream
	session string
	aid     string
	xonly   string
	issID   string
	asset   string
}

// newNetworkFixture registers an owner and puts a LIVE network-enforced issuance
// in the store. It builds the issuance directly rather than deploying, because a
// real network deploy needs a registrar run this test cannot do and the subject
// here is what happens AFTER a token is live.
func newNetworkFixture(t *testing.T) *networkFixture {
	t.Helper()
	h := newHarness(t)
	session, aid, xonly := h.register(strings.Repeat("31", 32), "Network Issuer")
	asset := strings.Repeat("c6", 32)

	iss := &Issuance{
		ID: "iss-network", OwnerAID: aid, Name: "Network Bond", Ticker: "NBND",
		StructureID: "native-equity", Status: "live", Supply: 1000, Precision: 0,
		CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(),
	}
	if err := h.s.st.CreateIssuance(iss); err != nil {
		t.Fatal(err)
	}
	if err := h.s.st.UpdateIssuanceFields(iss.ID, map[string]any{
		"asset_id": asset, "enforcement": "network", "issuer_pubkey": xonly,
		"issuer_external": 1, "policy_commitment": strings.Repeat("cd", 32),
		"holder_covenant_address": "tb1pholder", "verifier_covenant_address": "tb1prules",
	}); err != nil {
		t.Fatal(err)
	}

	// The published list: the owner as a bare key, plus one holder carrying a
	// lockup, so every read is exercised against both document shapes.
	up := &dampUpstream{
		asset: asset, seq: 0, pi: strings.Repeat("cd", 32),
		holders: []any{
			xonly,
			map[string]any{"key": strings.Repeat("7a", 32), "send_after": 95200, "recv_after": 0},
		},
		frozen: []string{},
	}
	h.oa.extra.Store(up.handler())

	return &networkFixture{h: h, up: up, session: session, aid: aid, xonly: xonly,
		issID: iss.ID, asset: asset}
}

// --- the console -------------------------------------------------------------

func TestM13_PolicyStatusReadsThePublishedList(t *testing.T) {
	f := newNetworkFixture(t)
	r := f.h.do("GET", "/api/issuances/"+f.issID+"/policy", f.session, nil)
	if r.code != 200 {
		t.Fatalf("policy status: %d %s", r.code, r.errMsg())
	}
	if r.body["network_enforced"] != true {
		t.Fatalf("a network-enforced issuance must say so: %s", r.raw)
	}
	// The two-coin bound is on every issuer-facing surface, including this one.
	if fmt.Sprint(r.body["max_coins_per_transfer"]) != "2" {
		t.Fatalf("the console must state the per-transfer coin bound: %s", r.raw)
	}
	pub, _ := r.body["published"].(map[string]any)
	if pub == nil {
		t.Fatalf("the published list must be surfaced: %s", r.raw)
	}
	holders, _ := pub["holders"].([]any)
	if len(holders) != 2 {
		t.Fatalf("both published holders must appear: %s", r.raw)
	}
	// The bounded holder's lockup survives the read. Dropping it would show an
	// issuer a holder who can trade today when the network says otherwise.
	bounded := false
	for _, raw := range holders {
		row, _ := raw.(map[string]any)
		if fmt.Sprint(row["can_send_from_block"]) == "95200" {
			bounded = true
		}
	}
	if !bounded {
		t.Fatalf("a holder's lockup must survive the read: %s", r.raw)
	}
}

func TestM13_FreezeBuildsSignsAndPublishes(t *testing.T) {
	f := newNetworkFixture(t)
	order := strings.Repeat("0a", 32)
	frozenKey := strings.Repeat("ee", 32)

	// A reason and an order fingerprint are both required, exactly as for a
	// court-ordered freeze on a freely tradable token.
	r := f.h.do("POST", "/api/issuances/"+f.issID+"/policy/freeze", f.session, map[string]any{
		"holders": []string{frozenKey}, "order_hash": order,
	})
	if r.code != 400 || !strings.Contains(r.errMsg(), "a reason is required") {
		t.Fatalf("a change without a reason must be refused: %d %s", r.code, r.errMsg())
	}
	r = f.h.do("POST", "/api/issuances/"+f.issID+"/policy/freeze", f.session, map[string]any{
		"holders": []string{frozenKey}, "reason": "court order 2026-1188",
	})
	if r.code != 400 || !strings.Contains(r.errMsg(), "order_hash") {
		t.Fatalf("a change without an order fingerprint must be refused: %d %s", r.code, r.errMsg())
	}
	if f.up.prepares != 0 {
		t.Fatal("a refused change must never reach the policy server")
	}

	// The real build.
	r = f.h.do("POST", "/api/issuances/"+f.issID+"/policy/freeze", f.session, map[string]any{
		"holders": []string{frozenKey}, "reason": "court order 2026-1188", "order_hash": order,
	})
	if r.code != 200 {
		t.Fatalf("freeze build: %d %s", r.code, r.errMsg())
	}
	opID, _ := r.body["op_id"].(string)
	if opID == "" || r.body["to_sign"] != strings.Repeat("5e", 32) {
		t.Fatalf("the build must return the message the issuer signs: %s", r.raw)
	}
	if r.body["sign_with"] != "issuer" {
		t.Fatalf("the signature is the issuer's own, not the platform's: %s", r.raw)
	}
	// The note must not imply the freeze is already in force.
	if !strings.Contains(fmt.Sprint(r.body["note"]), "published") {
		t.Fatalf("the build must say when the change takes effect: %s", r.raw)
	}

	// Replaying the SAME order against the SAME holder resumes the operation. A
	// second prepare would open a second sequence number, and only one of the two
	// could ever be published.
	again := f.h.do("POST", "/api/issuances/"+f.issID+"/policy/freeze", f.session, map[string]any{
		"holders": []string{frozenKey}, "reason": "court order 2026-1188", "order_hash": order,
	})
	if again.code != 200 || again.body["op_id"] != opID {
		t.Fatalf("a replayed build must resume the same change: %d %s", again.code, again.raw)
	}
	if f.up.prepares != 1 {
		t.Fatalf("a replayed build must not prepare twice, got %d", f.up.prepares)
	}

	// Completing without the registrar's values is a 409 that HANDS BACK the
	// document to compile against, not a dead end.
	c := f.h.do("POST", "/api/issuances/"+f.issID+"/policy/"+opID+"/complete", f.session,
		map[string]any{"sig": strings.Repeat("aa", 64)})
	if c.code != 409 || !strings.Contains(c.errMsg(), "not published") {
		t.Fatalf("a completion without registrar values must be refused: %d %s", c.code, c.raw)
	}
	if c.body["registrar_document"] == nil || c.body["registrar_commands"] == nil {
		t.Fatalf("the refusal must name what to run: %s", c.raw)
	}
	if f.up.completes != 0 {
		t.Fatal("nothing may be published before the registrar values arrive")
	}

	// A signature that is not a signature is refused before anything upstream.
	bad := f.h.do("POST", "/api/issuances/"+f.issID+"/policy/"+opID+"/complete", f.session, map[string]any{
		"sig": "nope", "verifier_program": strings.Repeat("bb", 32), "rules_tx": "0200",
	})
	if bad.code != 400 || f.up.completes != 0 {
		t.Fatalf("a malformed signature must be refused locally: %d %s", bad.code, bad.raw)
	}

	// The real completion.
	done := f.h.do("POST", "/api/issuances/"+f.issID+"/policy/"+opID+"/complete", f.session, map[string]any{
		"sig": strings.Repeat("aa", 64), "verifier_program": strings.Repeat("bb", 32),
		"verifier_address": "tb1pnewrules", "rules_tx": "0200beef",
	})
	if done.code != 200 || done.body["state"] != "published" {
		t.Fatalf("completion: %d %s", done.code, done.raw)
	}
	if done.body["txid"] != strings.Repeat("7f", 32) {
		t.Fatalf("the completion must carry the rules transaction: %s", done.raw)
	}
	// Replaying the completion never publishes twice.
	replay := f.h.do("POST", "/api/issuances/"+f.issID+"/policy/"+opID+"/complete", f.session, map[string]any{
		"sig": strings.Repeat("aa", 64), "verifier_program": strings.Repeat("bb", 32), "rules_tx": "0200beef",
	})
	if replay.code != 200 || replay.body["idempotent"] != true || replay.body["txid"] != done.body["txid"] {
		t.Fatalf("a replayed completion must return the same result: %d %s", replay.code, replay.raw)
	}
	if f.up.completes != 1 {
		t.Fatalf("a replayed completion must not publish twice, got %d", f.up.completes)
	}

	// The change is on the operation history with its reason and order fingerprint,
	// which is what makes the console an audit surface rather than a button.
	st := f.h.do("GET", "/api/issuances/"+f.issID+"/policy", f.session, nil)
	ops, _ := st.body["ops"].([]any)
	if len(ops) != 1 {
		t.Fatalf("the change must be on the history: %s", st.raw)
	}
	op, _ := ops[0].(map[string]any)
	if op["reason"] != "court order 2026-1188" || op["order_hash"] != order || op["state"] != "published" {
		t.Fatalf("the history must carry the reason and the order fingerprint: %s", st.raw)
	}
}

func TestM13_UnfreezeIsASeparateOperation(t *testing.T) {
	f := newNetworkFixture(t)
	key := strings.Repeat("ee", 32)
	body := map[string]any{"holders": []string{key}, "reason": "order discharged", "order_hash": strings.Repeat("0b", 32)}
	r := f.h.do("POST", "/api/issuances/"+f.issID+"/policy/unfreeze", f.session, body)
	if r.code != 200 || r.body["kind"] != "unfreeze" {
		t.Fatalf("unfreeze build: %d %s", r.code, r.raw)
	}
	// An unfreeze asks the policy server to ADD the holder back, never to remove.
	if f.up.lastBody["add_whitelist"] == nil || f.up.lastBody["remove_whitelist"] != nil {
		t.Fatalf("an unfreeze must restore the holder, not remove one: %v", f.up.lastBody)
	}
	// And a freeze with the same order is a DIFFERENT operation, so lifting and
	// re-imposing under one order number cannot collide.
	fr := f.h.do("POST", "/api/issuances/"+f.issID+"/policy/freeze", f.session, body)
	if fr.code != 200 || fr.body["op_id"] == r.body["op_id"] {
		t.Fatalf("a freeze and an unfreeze must be distinct operations: %d %s", fr.code, fr.raw)
	}
}

func TestM13_PolicyConsoleIsForNetworkTokensOnly(t *testing.T) {
	f := newNetworkFixture(t)
	// A serviced token has no published list to change: it is policed by the
	// platform's own transfer checks.
	if err := f.h.s.st.UpdateIssuanceFields(f.issID, map[string]any{"enforcement": "serviced"}); err != nil {
		t.Fatal(err)
	}
	r := f.h.do("POST", "/api/issuances/"+f.issID+"/policy/freeze", f.session, map[string]any{
		"holders": []string{strings.Repeat("ee", 32)}, "reason": "x", "order_hash": strings.Repeat("0a", 32),
	})
	if r.code != 409 || !strings.Contains(r.errMsg(), "network enforces") {
		t.Fatalf("want a 409 naming the election, got %d %s", r.code, r.errMsg())
	}
	st := f.h.do("GET", "/api/issuances/"+f.issID+"/policy", f.session, nil)
	if st.code != 200 || st.body["network_enforced"] != false {
		t.Fatalf("the read must answer honestly for another election: %d %s", st.code, st.raw)
	}
}

// --- the downstream branches --------------------------------------------------

// TestM13_FlowsThatCannotWorkRefuseWithAReason covers every platform flow that
// is written against a platform-held account and a platform co-signature. None
// of them can work for a network-enforced token, and each must SAY WHY.
func TestM13_FlowsThatCannotWorkRefuseWithAReason(t *testing.T) {
	f := newNetworkFixture(t)
	cases := []struct {
		name, method, path string
		body               any
		want               string
	}{
		{"subscribe", "POST", "/api/issuances/" + f.issID + "/subscribe",
			map[string]any{"rail": "usdx", "amount": 10, "refund_address": "tb1qrefund"},
			"delivered to each investor's own holding address"},
		{"close", "POST", "/api/issuances/" + f.issID + "/close",
			map[string]any{}, "cannot deliver it"},
		{"receipt programme", "POST", "/api/issuances/" + f.issID + "/dr/enable",
			map[string]any{}, "nothing here to hold or to check"},
		{"receipt mint", "POST", "/api/issuances/" + f.issID + "/dr/mint",
			map[string]any{"atoms": 10}, "from your own wallet"},
		{"receipt redeem", "POST", "/api/issuances/" + f.issID + "/dr/redeem",
			map[string]any{"atoms": 10}, "from their own wallet"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := f.h.do(tc.method, tc.path, f.session, tc.body)
			if r.code != 409 {
				t.Fatalf("want 409, got %d %s", r.code, r.raw)
			}
			if r.body["enforcement"] != "network" {
				t.Fatalf("the refusal must name the election: %s", r.raw)
			}
			if !strings.Contains(r.errMsg(), tc.want) {
				t.Fatalf("the refusal must explain why:\n got  %s\n want it to contain %q", r.errMsg(), tc.want)
			}
		})
	}
}

// TestM13_TransfersPointTheHolderAtTheirOwnWallet: the refusal an investor sees
// has to be actionable, because the transfer they wanted IS possible, just not
// here.
func TestM13_TransfersPointTheHolderAtTheirOwnWallet(t *testing.T) {
	f := newNetworkFixture(t)
	investor, _, _ := f.h.register(strings.Repeat("32", 32), "Investor")
	r := f.h.do("POST", "/api/transfers", investor, map[string]any{
		"asset": f.asset, "to_aid": f.aid, "atoms": 100,
	})
	if r.code != 409 || r.body["enforcement"] != "network" {
		t.Fatalf("want a 409 naming the election, got %d %s", r.code, r.raw)
	}
	msg := r.errMsg()
	for _, want := range []string{"own wallet", "published holder list"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal must tell the holder how to transfer: %q lacks %q", msg, want)
		}
	}
}

// TestM13_EligibilityComesFromThePublishedList: for a network-enforced token,
// "eligible" is membership of the list the chain reads, not a category stamp
// this platform grants. A stamp-based answer would be wrong in both directions.
func TestM13_EligibilityComesFromThePublishedList(t *testing.T) {
	f := newNetworkFixture(t)
	onList := f.h.do("GET", "/api/eligibility?aid="+f.aid+"&asset="+f.asset, "", nil)
	if onList.code != 200 || onList.body["eligible"] != true {
		t.Fatalf("the owner is on the published list: %d %s", onList.code, onList.raw)
	}
	if !strings.Contains(fmt.Sprint(onList.body["source"]), "published holder list") {
		t.Fatalf("the answer must name where it came from: %s", onList.raw)
	}

	// A registered identity that is NOT on the list.
	_, otherAID, _ := f.h.register(strings.Repeat("33", 32), "Outsider")
	off := f.h.do("GET", "/api/eligibility?aid="+otherAID+"&asset="+f.asset, "", nil)
	if off.code != 200 || off.body["eligible"] != false {
		t.Fatalf("an identity off the list is not eligible: %d %s", off.code, off.raw)
	}
	if !strings.Contains(fmt.Sprint(off.body["reasons"]), "not on the published holder list") {
		t.Fatalf("the reason must name the list: %s", off.raw)
	}

	// An unknown identity is a definite no, not an error.
	unknown := f.h.do("GET", "/api/eligibility?aid=nobody&asset="+f.asset, "", nil)
	if unknown.code != 200 || unknown.body["eligible"] != false {
		t.Fatalf("an unknown identity must be a definite no: %d %s", unknown.code, unknown.raw)
	}
}

// TestM13_ListingsReflectTheOnChainPolicy: a venue reading the listing surface
// gets the published list's version and commitment, so it can verify the same
// document itself instead of trusting a stamp.
func TestM13_ListingsReflectTheOnChainPolicy(t *testing.T) {
	f := newNetworkFixture(t)
	g := f.h.do("POST", "/api/issuances/"+f.issID+"/listing", f.session, map[string]any{
		"authorized": true, "venues": []string{"seqdex"},
	})
	if g.code != 200 {
		t.Fatalf("grant: %d %s", g.code, g.errMsg())
	}
	r := f.h.do("GET", "/api/listings?asset="+f.asset, "", nil)
	if r.code != 200 || r.body["authorized"] != true {
		t.Fatalf("listing read: %d %s", r.code, r.raw)
	}
	if r.body["enforcement"] != "network" {
		t.Fatalf("the read must name the election: %s", r.raw)
	}
	if fmt.Sprint(r.body["max_coins_per_transfer"]) != "2" {
		t.Fatalf("a venue must be told the per-transfer coin bound: %s", r.raw)
	}
	pol, _ := r.body["policy"].(map[string]any)
	if pol == nil || fmt.Sprint(pol["holder_count"]) != "2" {
		t.Fatalf("the read must carry the published policy: %s", r.raw)
	}
	if !strings.Contains(fmt.Sprint(r.body["eligibility_source"]), "published holder list") {
		t.Fatalf("the read must name where eligibility comes from: %s", r.raw)
	}
}
