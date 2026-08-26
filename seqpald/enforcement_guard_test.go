package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func seedIssuanceOfKind(t *testing.T, s *server, ownerAID, kind string) *Issuance {
	t.Helper()
	id, _ := randHex(12)
	now := time.Now().Unix()
	if err := s.st.CreateIssuance(&Issuance{
		ID: id, OwnerAID: ownerAID, Name: kind + " Fund", Ticker: strings.ToUpper(kind[:3]),
		StructureID: "native-equity", Status: "draft", Terms: json.RawMessage(`{}`),
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.st.UpdateIssuanceFields(id, map[string]any{
		"status": "live", "enforcement": kind, "asset_id": strings.Repeat("c", 64),
	}); err != nil {
		t.Fatal(err)
	}
	out, _ := s.st.IssuanceByID(id)
	return out
}

// A freely-tradable token has no escrow enclave to deliver from. Taking a
// subscription for one promises a delivery that can never be made, and the
// holder finds out at closing, with their money already in escrow.
func TestSubscribingToABearerTokenIsRefused(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.screen = newScreener("")
	session, _, _ := m3SeedAccount(t, h.s, vecPriv, "Investor Ivy")
	_, ownerAID, _ := m3SeedAccount(t, h.s, vecPriv2, "Issuer Ida")
	iss := seedIssuanceOfKind(t, h.s, ownerAID, "bearer")

	res := h.do("POST", "/api/issuances/"+iss.ID+"/subscribe", session,
		map[string]any{"rail": "usdx", "amount": 10})
	if res.code != 409 {
		t.Fatalf("a bearer subscription must be refused, got %d %s", res.code, res.raw)
	}
	msg, _ := res.body["error"].(string)
	if !strings.Contains(msg, "freely tradable") {
		t.Fatalf("the refusal must say what kind of token this is: %q", msg)
	}
}

// The refusal must be about the TOKEN before it is about the investor: an
// investor with no OpenAMP account asking about a bearer token has not made the
// mistake the enclave message describes.
func TestTheRefusalNamesTheTokenNotTheInvestor(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.screen = newScreener("")
	session, _ := walletSession(t, h, testPKH) // no enclave at all
	_, ownerAID, _ := m3SeedAccount(t, h.s, vecPriv, "Issuer Ida")
	iss := seedIssuanceOfKind(t, h.s, ownerAID, "bearer")

	res := h.do("POST", "/api/issuances/"+iss.ID+"/subscribe", session,
		map[string]any{"rail": "usdx", "amount": 10})
	msg, _ := res.body["error"].(string)
	if strings.Contains(msg, "OpenAMP account") {
		t.Fatalf("the token's kind is the reason, not the investor's wallet: %q", msg)
	}
	if !strings.Contains(msg, "freely tradable") {
		t.Fatalf("unexpected refusal: %q", msg)
	}
}

// A distribution reads a register the transfer agent keeps. Neither of the other
// kinds has one, and starting a run against them fails somewhere further in.
func TestDistributionsAreForServicedTokensOnly(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	ownerSession, ownerAID, _ := m3SeedAccount(t, h.s, vecPriv, "Issuer Ida")

	for _, kind := range []string{"bearer", "network"} {
		iss := seedIssuanceOfKind(t, h.s, ownerAID, kind)
		res := h.do("POST", "/api/issuances/"+iss.ID+"/distributions", ownerSession,
			map[string]any{"pool_usd": 100})
		if res.code != 409 {
			t.Fatalf("%s: a distribution must be refused, got %d %s", kind, res.code, res.raw)
		}
	}
}

// Closing releases from an escrow enclave a freely-tradable token never had.
func TestClosingABearerIssuanceIsRefused(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	ownerSession, ownerAID, _ := m3SeedAccount(t, h.s, vecPriv, "Issuer Ida")
	iss := seedIssuanceOfKind(t, h.s, ownerAID, "bearer")

	res := h.do("POST", "/api/issuances/"+iss.ID+"/close", ownerSession, map[string]any{})
	if res.code != 409 {
		t.Fatalf("closing a bearer issuance must be refused, got %d %s", res.code, res.raw)
	}
	if msg, _ := res.body["error"].(string); strings.Contains(msg, "escrow enclave is missing") {
		t.Fatalf("the refusal must describe the token, not the missing machinery: %q", msg)
	}
}

// A freely-tradable token is supervised by a key that signs its freezes, as a
// BIP340 signature over the message the node computes. An ordinary wallet signs
// classic messages and cannot produce one, so an ID with no OpenAMP account has
// nothing to put in the asset id -- which commits to that key at issuance.
// Unrefused, the deploy reached the node and failed there, about a malformed
// operational key, after the terms were already written.
func TestAWalletBackedIDCannotIssueASupervisedToken(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.screen = newScreener("")
	session, aid := walletSession(t, h, testPKH)

	iss := seedIssuanceOfKind(t, h.s, aid, "bearer")
	if err := h.s.st.UpdateIssuanceFields(iss.ID, map[string]any{"status": "draft"}); err != nil {
		t.Fatal(err)
	}

	for _, kind := range []string{"bearer", "serviced"} {
		res := h.do("POST", "/api/deploy", session, map[string]any{
			"issuance_id": iss.ID, "enforcement": kind,
			"supply": 1000, "precision": 0,
		})
		if res.code != 403 {
			t.Fatalf("%s: must be refused for an ID with no OpenAMP account, got %d %s",
				kind, res.code, res.raw)
		}
		msg, _ := res.body["error"].(string)
		if !strings.Contains(msg, "no OpenAMP account attached") {
			t.Fatalf("%s: the refusal must name what is missing: %q", kind, msg)
		}
		// And it must point at the option that does work.
		if !strings.Contains(msg, "network-enforced") {
			t.Fatalf("%s: the refusal must name the option that needs nothing: %q", kind, msg)
		}
	}
}

// A network-enforced mint lands at a holding key, and that coin is spent by
// signing with it. A SeqPal ID that is only a wallet has no OpenAMP account to
// supply one, so it names a key of its own -- and naming none has to be a
// refusal that says so, not a 500 about a key the account was never going to
// have. Before this, network was the one enforcement such an ID was told it
// could use, and it failed on every attempt.
func TestANetworkDeployNamesAHoldingKey(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.damp = true
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.screen = newScreener("")
	session, aid := walletSession(t, h, testPKH)

	iss := seedIssuanceOfKind(t, h.s, aid, "network")
	if err := h.s.st.UpdateIssuanceFields(iss.ID, map[string]any{"status": "draft"}); err != nil {
		t.Fatal(err)
	}
	deploy := func(body map[string]any) resp {
		body["issuance_id"] = iss.ID
		body["enforcement"] = "network"
		body["supply"] = 1000
		body["precision"] = 0
		return h.do("POST", "/api/deploy", session, body)
	}

	res := deploy(map[string]any{})
	if res.code != 400 {
		t.Fatalf("naming no holding key must be refused, got %d %s", res.code, res.raw)
	}
	if msg, _ := res.body["error"].(string); !strings.Contains(msg, "Name a holding key") {
		t.Fatalf("the refusal must say what to do: %q", msg)
	}

	res = deploy(map[string]any{"holder_key": "not a key"})
	if res.code != 400 {
		t.Fatalf("a malformed holding key must be refused, got %d %s", res.code, res.raw)
	}

	// A well-formed key the account cannot sign with would mint the whole supply
	// somewhere it can never be moved from, so it is refused before anything is
	// minted rather than reported afterwards.
	h.s.cfg.nodeURL = newKeyEchoNode(t).URL
	res = deploy(map[string]any{"holder_key": strings.Repeat("ab", 32)})
	if res.code != 403 {
		t.Fatalf("a holding key from no linked wallet must be refused 403, got %d %s", res.code, res.raw)
	}
	if msg, _ := res.body["error"].(string); !strings.Contains(msg, "does not derive") {
		t.Fatalf("the refusal must name the reason: %q", msg)
	}
}

// newKeyEchoNode is a node whose derived address is a function of the descriptor
// it was asked about, so two different keys never share an address. The shared
// wallet stub answers every pkh descriptor with one address, which is enough for
// the flows that only ask "does this verify" and not enough to tell one key from
// another.
func newKeyEchoNode(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		reply := func(v any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"result": v, "error": nil, "id": "seqpald"})
		}
		switch req.Method {
		case "getdescriptorinfo":
			var p []string
			_ = json.Unmarshal(req.Params, &p)
			d := p[0]
			if i := strings.Index(d, "#"); i >= 0 {
				d = d[:i]
			}
			reply(map[string]any{"descriptor": d, "checksum": "0wcatm2p", "hasprivatekeys": false})
		case "deriveaddresses":
			var p []any
			_ = json.Unmarshal(req.Params, &p)
			d, _ := p[0].(string)
			reply([]string{"addr:" + d})
		case "verifymessage":
			reply(false)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"result": nil,
				"error": map[string]any{"code": -32601, "message": "no such method: " + req.Method}})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}
