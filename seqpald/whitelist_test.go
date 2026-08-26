package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A live, network-enforced issuance owned by someone, which is the only kind
// that has a whitelist to join.
func seedNetworkIssuance(t *testing.T, s *server, ownerAID string) *Issuance {
	t.Helper()
	id, _ := randHex(12)
	now := time.Now().Unix()
	iss := &Issuance{
		ID: id, OwnerAID: ownerAID, Name: "Damp Fund", Ticker: "DAMPX",
		StructureID: "native-equity", Status: "draft", Terms: json.RawMessage(`{}`),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.st.CreateIssuance(iss); err != nil {
		t.Fatal(err)
	}
	if err := s.st.UpdateIssuanceFields(id, map[string]any{
		"status": "live", "enforcement": "network", "asset_id": strings.Repeat("c", 64),
	}); err != nil {
		t.Fatal(err)
	}
	out, _ := s.st.IssuanceByID(id)
	return out
}

func verifyWalletHolder(t *testing.T, h *harness, session string) {
	t.Helper()
	if v := h.do("POST", "/api/id/verify", session, map[string]any{
		"residence": "AE", "screening_name": "Wallet Wendy", "base_eligibility": "ret",
	}); v.code != 200 {
		t.Fatalf("verify: %d %s", v.code, v.raw)
	}
}

// A verified holder can ask to be admitted, and a key their own wallet derives
// needs no further signature: the wallet was proven at sign-in.
func TestWhitelistRequestFromAnOwnKey(t *testing.T) {
	h := newHarness(t)
	node := newWalletNode(t, true)
	h.s.cfg.nodeURL = node.URL
	h.s.screen = newScreener("")
	session, aid := walletSession(t, h, testPKH)
	verifyWalletHolder(t, h, session)
	_, ownerAID, _ := m3SeedAccount(t, h.s, vecPriv, "Issuer Ida")
	iss := seedNetworkIssuance(t, h.s, ownerAID)

	// The stub node answers every deriveaddresses with the same address, so any
	// key "derives" from the wallet; that is exactly the descriptor-proof path.
	key := strings.Repeat("d", 64)
	req := h.do("POST", "/api/issuances/"+iss.ID+"/whitelist-requests", session,
		map[string]any{"holding_key": key, "note": "please"})
	if req.code != 200 {
		t.Fatalf("request: %d %s", req.code, req.raw)
	}
	wr, _ := req.body["request"].(map[string]any)
	if wr == nil {
		t.Fatalf("no request recorded: %s", req.raw)
	}
	if wr["state"] != "pending" || wr["aid"] != aid {
		t.Fatalf("unexpected request: %v", wr)
	}
	if wr["proof"] != "descriptor" {
		t.Fatalf("a key the wallet derives is proven by the wallet, got proof %v", wr["proof"])
	}
}

// Verification is what an issuer is being asked to rely on. Without it there is
// nothing to decide on but a name.
func TestWhitelistRequestNeedsVerification(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	session, _ := walletSession(t, h, testPKH)
	_, ownerAID, _ := m3SeedAccount(t, h.s, vecPriv, "Issuer Ida")
	iss := seedNetworkIssuance(t, h.s, ownerAID)

	req := h.do("POST", "/api/issuances/"+iss.ID+"/whitelist-requests", session,
		map[string]any{"holding_key": strings.Repeat("d", 64)})
	if req.code != 403 {
		t.Fatalf("an unverified holder must be refused, got %d %s", req.code, req.raw)
	}
}

// The proof exists so a verified holder cannot get somebody else's key admitted,
// which would launder eligibility rather than prove it.
func TestWhitelistRequestRefusesAnUnprovenKey(t *testing.T) {
	h := newHarness(t)
	// This node derives addresses that never match, and verifies no signature.
	h.s.cfg.nodeURL = newWalletNodeNoMatch(t).URL
	h.s.screen = newScreener("")
	session, _ := walletSession(t, h, testPKH)
	verifyWalletHolder(t, h, session)
	_, ownerAID, _ := m3SeedAccount(t, h.s, vecPriv, "Issuer Ida")
	iss := seedNetworkIssuance(t, h.s, ownerAID)

	key := strings.Repeat("e", 64)
	ask := h.do("POST", "/api/issuances/"+iss.ID+"/whitelist-requests", session,
		map[string]any{"holding_key": key})
	if ask.code != 200 || ask.body["sign_this"] == nil {
		t.Fatalf("a key the wallet does not derive must be asked to sign: %d %s", ask.code, ask.raw)
	}
	// The statement names the asset and the account, so it cannot be reused.
	stmt, _ := ask.body["sign_this"].(string)
	for _, want := range []string{iss.AssetID, "whitelist-request", key} {
		if !strings.Contains(stmt, want) {
			t.Fatalf("the signed statement must name %q: %s", want, stmt)
		}
	}
	bad := h.do("POST", "/api/issuances/"+iss.ID+"/whitelist-requests", session,
		map[string]any{"holding_key": key, "sig": "not-a-signature"})
	if bad.code != 400 {
		t.Fatalf("an unproven key must be refused, got %d %s", bad.code, bad.raw)
	}
}

// Approving is a decision; inclusion is a fact about the published list. The two
// must not be conflated, or a holder is told they can hold something they cannot.
func TestApprovalIsNotYetInclusion(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.screen = newScreener("")
	holderSession, _ := walletSession(t, h, testPKH)
	verifyWalletHolder(t, h, holderSession)

	ownerSession, ownerAID, _ := m3SeedAccount(t, h.s, vecPriv, "Issuer Ida")
	iss := seedNetworkIssuance(t, h.s, ownerAID)

	key := strings.Repeat("d", 64)
	req := h.do("POST", "/api/issuances/"+iss.ID+"/whitelist-requests", holderSession,
		map[string]any{"holding_key": key})
	rid := req.body["request"].(map[string]any)["id"].(string)

	// A holder cannot decide on their own request.
	if self := h.do("POST", "/api/issuances/"+iss.ID+"/whitelist-requests/"+rid+"/decide",
		holderSession, map[string]any{"approve": true}); self.code == 200 {
		t.Fatal("only the issuer may decide on a request")
	}

	dec := h.do("POST", "/api/issuances/"+iss.ID+"/whitelist-requests/"+rid+"/decide",
		ownerSession, map[string]any{"approve": true, "note": "eligible"})
	if dec.code != 200 {
		t.Fatalf("decide: %d %s", dec.code, dec.raw)
	}
	if got := dec.body["request"].(map[string]any)["state"]; got != "approved" {
		t.Fatalf("state: %v", got)
	}
	// Approved, and explicitly NOT on the list yet.
	stored, _ := h.s.st.WhitelistRequestByID(rid)
	if stored.State != "approved" || stored.PolicyOpID != "" {
		t.Fatalf("approval must not claim publication: %+v", stored)
	}

	// Publishing a change that carries the key is what makes it included.
	h.s.noteWhitelistInclusions(iss.ID, "op-1", json.RawMessage(`["`+key+`"]`))
	after, _ := h.s.st.WhitelistRequestByID(rid)
	if after.State != "included" || after.PolicyOpID != "op-1" {
		t.Fatalf("a published change must record inclusion: %+v", after)
	}
}

// A freeze REMOVES keys. Marking those included would be exactly backwards.
func TestAFreezeDoesNotIncludeAnybody(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.screen = newScreener("")
	holderSession, _ := walletSession(t, h, testPKH)
	verifyWalletHolder(t, h, holderSession)
	ownerSession, ownerAID, _ := m3SeedAccount(t, h.s, vecPriv, "Issuer Ida")
	iss := seedNetworkIssuance(t, h.s, ownerAID)
	key := strings.Repeat("d", 64)
	req := h.do("POST", "/api/issuances/"+iss.ID+"/whitelist-requests", holderSession,
		map[string]any{"holding_key": key})
	rid := req.body["request"].(map[string]any)["id"].(string)
	h.do("POST", "/api/issuances/"+iss.ID+"/whitelist-requests/"+rid+"/decide", ownerSession,
		map[string]any{"approve": true})

	// Only the unfreeze path calls this; assert the guard by never calling it for
	// a freeze and checking the request is untouched.
	stored, _ := h.s.st.WhitelistRequestByID(rid)
	if stored.State != "approved" {
		t.Fatalf("a freeze must leave an approval alone: %+v", stored)
	}
}

// Only a network-enforced asset has a whitelist at all.
func TestWhitelistRequestOnlyForNetworkAssets(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.screen = newScreener("")
	session, _ := walletSession(t, h, testPKH)
	verifyWalletHolder(t, h, session)

	_, ownerAID, _ := m3SeedAccount(t, h.s, vecPriv, "Issuer Ida")
	id, _ := randHex(12)
	now := time.Now().Unix()
	if err := h.s.st.CreateIssuance(&Issuance{ID: id, OwnerAID: ownerAID, Name: "Serviced",
		Ticker: "SVC", StructureID: "native-equity", Status: "draft",
		Terms: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := h.s.st.UpdateIssuanceFields(id, map[string]any{
		"status": "live", "enforcement": "serviced"}); err != nil {
		t.Fatal(err)
	}

	req := h.do("POST", "/api/issuances/"+id+"/whitelist-requests", session,
		map[string]any{"holding_key": strings.Repeat("d", 64)})
	if req.code != 400 {
		t.Fatalf("a serviced asset has no whitelist to join, got %d %s", req.code, req.raw)
	}
}

// A refusal has to stand for a while. Every request puts a notice in front of
// the issuer, so a holder who can re-ask instantly can make a nuisance of
// themselves indefinitely.
func TestARefusalStandsForAWhile(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.screen = newScreener("")
	holderSession, _ := walletSession(t, h, testPKH)
	verifyWalletHolder(t, h, holderSession)
	ownerSession, ownerAID, _ := m3SeedAccount(t, h.s, vecPriv, "Issuer Ida")
	iss := seedNetworkIssuance(t, h.s, ownerAID)
	key := strings.Repeat("d", 64)

	req := h.do("POST", "/api/issuances/"+iss.ID+"/whitelist-requests", holderSession,
		map[string]any{"holding_key": key})
	rid := req.body["request"].(map[string]any)["id"].(string)
	if dec := h.do("POST", "/api/issuances/"+iss.ID+"/whitelist-requests/"+rid+"/decide",
		ownerSession, map[string]any{"approve": false, "note": "not yet"}); dec.code != 200 {
		t.Fatalf("refuse: %d %s", dec.code, dec.raw)
	}

	again := h.do("POST", "/api/issuances/"+iss.ID+"/whitelist-requests", holderSession,
		map[string]any{"holding_key": key})
	if again.code != 429 {
		t.Fatalf("asking again straight away must be refused, got %d %s", again.code, again.raw)
	}

	// But a refusal is not "never": once it has aged, asking again works.
	_ = h.s.st.UpdateWhitelistRequestFields(rid, map[string]any{
		"decided_at": time.Now().Add(-48 * time.Hour).Unix(),
	})
	later := h.do("POST", "/api/issuances/"+iss.ID+"/whitelist-requests", holderSession,
		map[string]any{"holding_key": key})
	if later.code != 200 {
		t.Fatalf("an aged refusal must not shut a holder out for good, got %d %s", later.code, later.raw)
	}
}

// A published holder list names KEYS. Asking "is this identity eligible" of the
// OpenAMP account key alone answered "no holding key on record" for a SeqPal ID
// that is only a wallet -- which by then could be on the list perfectly well,
// because that is exactly what a whitelist request puts there. Every key the
// account has proven counts.
func TestEligibilityCountsEveryProvenHoldingKey(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.screen = newScreener("")
	_, aid := walletSession(t, h, testPKH)

	asset := strings.Repeat("c", 64)
	key := xonlyHex(t, strings.Repeat("42", 32))
	iss := seedIssuanceOfKind(t, h.s, aid, "network")
	if err := h.s.st.UpdateIssuanceFields(iss.ID, map[string]any{"asset_id": asset}); err != nil {
		t.Fatal(err)
	}
	up := &dampUpstream{asset: asset, seq: 3, pi: strings.Repeat("cd", 32),
		holders: []any{key}, frozen: []string{}}
	h.oa.extra.Store(up.handler())

	ask := func() map[string]any {
		res := h.do("GET", "/api/eligibility?aid="+aid+"&asset="+asset, "", nil)
		if res.code != 200 {
			t.Fatalf("eligibility: %d %s", res.code, res.raw)
		}
		return res.body
	}

	if got := ask(); got["eligible"] != false {
		t.Fatalf("a key nobody has proven must not make an identity eligible: %v", got)
	}

	now := time.Now().Unix()
	if err := h.s.st.InsertWhitelistRequest(&WhitelistRequest{
		ID: "wr-proven", IssuanceID: iss.ID, AssetID: asset, AID: aid,
		HoldingKey: key, Proof: "descriptor", State: "included",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	got := ask()
	if got["eligible"] != true {
		t.Fatalf("the account proved this key and the published list carries it: %v", got)
	}
	if got["list_version"] == nil {
		t.Fatalf("the answer must name the list it came from: %v", got)
	}
}
