package main

import (
	"strings"
	"testing"
)

// Every surface that asks for a signature has to be answerable by every kind of
// SeqPal ID. An ID that is only a wallet signs an ordinary message somewhere
// else and pastes it back, so each of these has to say what to sign in that
// form, and accept it. Before this they all assumed the account had an OpenAMP
// key: the button was disabled, and the endpoint compared the signature against
// a key the account did not have.
func TestAWalletBackedIDCanSignWhatItIsAsked(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.screen = newScreener("")
	session, _ := walletSession(t, h, testPKH)

	// Phase one: what to sign. The tagged form is for a wallet that knows
	// SeqPal's tags; sign_this_message is the same commitment as characters an
	// ordinary wallet can sign.
	prep := h.do("POST", "/api/mandates/investor", session, map[string]any{
		"chain": "sequentia", "address": "tb1qnzten2u3ayqmnqtdul7z00v3uvapet7dv2789z",
	})
	if prep.code != 200 {
		t.Fatalf("prepare: %d %s", prep.code, prep.raw)
	}
	stmt, _ := prep.body["sign_this"].(string)
	msg, _ := prep.body["sign_this_message"].(string)
	if stmt == "" || msg == "" {
		t.Fatalf("both forms must be offered: %s", prep.raw)
	}
	if !strings.HasPrefix(msg, mandateTag+"\n") || !strings.Contains(msg, stmt) {
		t.Fatalf("the message form must be the tag and the statement together: %q", msg)
	}

	// Phase two: an ordinary signed message, checked against the addresses of
	// the wallets this ID has linked.
	res := h.do("POST", "/api/mandates/investor", session, map[string]any{
		"chain": "sequentia", "address": "tb1qnzten2u3ayqmnqtdul7z00v3uvapet7dv2789z",
		"signature": "H1uL0Y2ZwOaKf3wRZ5NnwF0oJp0V1sV+Xu3QW6mV2mYbTGYr9k1J2bV0wq1mM4pV",
	})
	if res.code != 200 {
		t.Fatalf("a signed message from a linked wallet must be accepted: %d %s", res.code, res.raw)
	}

	// Naming a key this ID does not have is still refused: the signature would
	// be checked against the account either way, and claiming otherwise on the
	// record would be a lie about who signed.
	res = h.do("POST", "/api/mandates/investor", session, map[string]any{
		"chain": "sequentia", "address": "tb1qnzten2u3ayqmnqtdul7z00v3uvapet7dv2789z",
		"signature":    "H1uL0Y2ZwOaKf3wRZ5NnwF0oJp0V1sV+Xu3QW6mV2mYbTGYr9k1J2bV0wq1mM4pV",
		"signer_xonly": strings.Repeat("ab", 32),
	})
	if res.code != 403 {
		t.Fatalf("naming somebody else's key must be refused, got %d %s", res.code, res.raw)
	}
}

// The market-abuse acknowledgment is the same shape, and it gates the transfer
// surfaces: an ID that could not sign it could not reach them.
func TestTheMarketAbuseAckOffersTheMessageForm(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.screen = newScreener("")
	session, _ := walletSession(t, h, testPKH)

	get := h.do("GET", "/api/id/market-abuse-ack", session, nil)
	if get.code != 200 {
		t.Fatalf("read: %d %s", get.code, get.raw)
	}
	msg, _ := get.body["sign_this_message"].(string)
	if !strings.HasPrefix(msg, marketAbuseTag+"\n") {
		t.Fatalf("the message form must carry the tag: %q", msg)
	}
	res := h.do("POST", "/api/id/market-abuse-ack", session, map[string]any{
		"signature": "H1uL0Y2ZwOaKf3wRZ5NnwF0oJp0V1sV+Xu3QW6mV2mYbTGYr9k1J2bV0wq1mM4pV",
	})
	if res.code != 200 {
		t.Fatalf("a signed acknowledgment from a linked wallet must be accepted: %d %s", res.code, res.raw)
	}
}
