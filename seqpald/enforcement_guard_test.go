package main

import (
	"encoding/json"
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
