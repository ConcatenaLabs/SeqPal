package main

import (
	"strings"
	"testing"
	"time"
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

// The payout-address check asks the policy server for each of the holder's
// enclave addresses, to refuse a mandate that would pay one. A SeqPal ID with
// no OpenAMP account has none, and the policy server has never heard of it, so
// every probe failed -- and the check fails closed, which turned "we cannot be
// sure" into "no payout address, ever" for exactly the holders whose claim on a
// distribution is the point of having the ID.
func TestAWalletBackedIDCanNameAPayoutAddress(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.screen = newScreener("")
	session, aid := walletSession(t, h, testPKH)

	// A live serviced issuance exists, so the probe has something to ask about.
	seedIssuanceOfKind(t, h.s, aid, "serviced")

	addr := "tb1qnzten2u3ayqmnqtdul7z00v3uvapet7dv2789z"
	prep := h.do("POST", "/api/mandates/investor", session,
		map[string]any{"chain": "sequentia", "address": addr})
	if prep.code != 200 {
		t.Fatalf("prepare: %d %s", prep.code, prep.raw)
	}
	res := h.do("POST", "/api/mandates/investor", session, map[string]any{
		"chain": "sequentia", "address": addr,
		"signature": "H1uL0Y2ZwOaKf3wRZ5NnwF0oJp0V1sV+Xu3QW6mV2mYbTGYr9k1J2bV0wq1mM4pV",
	})
	if res.code != 200 {
		t.Fatalf("registering a payout address must not depend on an OpenAMP account: %d %s",
			res.code, res.raw)
	}
}

// The two payout-address checks -- one for an investor, one for an issuer --
// have the same job and the same consequence when they get it wrong: money sent
// to a 2-of-2 script no wallet scans. One failed closed on an address it could
// not resolve and one failed open, which meant the same outage answered "no,
// carry on" on the issuer's side and "we cannot be sure, stop" on the
// investor's.
func TestBothPayoutChecksFailClosed(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.screen = newScreener("")
	session, aid := walletSession(t, h, testPKH)
	iss := seedIssuanceOfKind(t, h.s, aid, "serviced")

	// An escrow enclave the policy server cannot resolve: registered here, never
	// registered there, which is what an outage looks like from this side.
	if err := h.s.st.InsertEnclaveKey(&EnclaveKey{
		AID: strings.Repeat("f", 40), Kind: enclaveOfferingEscrow, RefID: iss.ID,
		XOnly: strings.Repeat("ab", 32), Priv: strings.Repeat("cd", 32),
		CreatedAt: time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	res := h.do("POST", "/api/issuances/"+iss.ID+"/mandate", session, map[string]any{
		"chain": "sequentia", "asset": strings.Repeat("c", 64),
		"address": "tb1qnzten2u3ayqmnqtdul7z00v3uvapet7dv2789z",
	})
	if res.code != 503 {
		t.Fatalf("an address that cannot be confirmed safe must stop the mandate, got %d %s",
			res.code, res.raw)
	}
}
