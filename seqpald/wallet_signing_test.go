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

// An anchored e-signature exists so anyone can check it later, and the record
// kept only the signer's x-only key. A SeqPal ID that is only a wallet has no
// such key: it signs an ordinary message, checked against an ADDRESS. Recorded
// with an empty key and no address, the signature was a string nobody could
// verify -- the one thing an anchored signature must not be.
func TestARecordedSignatureCanBeChecked(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	session, aid := walletSession(t, h, testPKH)
	iss := seedIssuanceOfKind(t, h.s, aid, "serviced")

	doc := h.do("POST", "/api/issuances/"+iss.ID+"/documents", session, map[string]any{
		"kind": "terms", "text": "the terms of this offering",
	})
	if doc.code != 200 {
		t.Fatalf("upload: %d %s", doc.code, doc.raw)
	}
	docs, _ := doc.body["documents"].([]any)
	if len(docs) == 0 {
		t.Fatalf("no documents in %s", doc.raw)
	}
	first, _ := docs[0].(map[string]any)
	hash, _ := first["hash"].(string)
	if hash == "" {
		t.Fatalf("no document hash in %s", doc.raw)
	}

	// Phase one says what to sign, in the form this wallet can sign.
	prep := h.do("POST", "/api/documents/"+hash+"/sign", session, map[string]any{"sig": ""})
	if prep.code != 200 {
		t.Fatalf("prepare: %d %s", prep.code, prep.raw)
	}
	if msg, _ := prep.body["sign_this_message"].(string); msg == "" {
		t.Fatalf("nothing said what to sign: %s", prep.raw)
	}

	res := h.do("POST", "/api/documents/"+hash+"/sign", session, map[string]any{
		"sig": "H1uL0Y2ZwOaKf3wRZ5NnwF0oJp0V1sV+Xu3QW6mV2mYbTGYr9k1J2bV0wq1mM4pV",
	})
	if res.code != 200 {
		t.Fatalf("sign: %d %s", res.code, res.raw)
	}

	sigs, err := h.s.st.SignaturesByDoc(hash)
	if err != nil || len(sigs) != 1 {
		t.Fatalf("signatures: %v %v", sigs, err)
	}
	if sigs[0].XOnly == "" && sigs[0].Address == "" {
		t.Fatal("the record says neither what key nor what address this verifies for")
	}
	if sigs[0].Address == "" {
		t.Fatalf("a wallet signature must record the address it verifies for: %+v", sigs[0])
	}
}

// Every record that keeps a signature has to keep what checks it. A signature
// by an OpenAMP key is checked against that key; one by a wallet is checked
// against an address, and a record with neither is a signature nobody can
// verify. The listing authorization is the one that matters most: it is public,
// and a venue relies on it.
func TestEveryStoredSignatureSaysWhatChecksIt(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	session, aid := walletSession(t, h, testPKH)
	const walletSig = "H1uL0Y2ZwOaKf3wRZ5NnwF0oJp0V1sV+Xu3QW6mV2mYbTGYr9k1J2bV0wq1mM4pV"

	// The acknowledgment, signed.
	if res := h.do("POST", "/api/id/market-abuse-ack", session,
		map[string]any{"signature": walletSig}); res.code != 200 {
		t.Fatalf("acknowledge: %d %s", res.code, res.raw)
	}
	ack, err := h.s.st.MarketAbuseAckByAID(aid)
	if err != nil || ack == nil {
		t.Fatalf("ack: %v %v", ack, err)
	}
	if ack.SignerXOnly == "" && ack.SignerAddress == "" {
		t.Fatal("the acknowledgment keeps a signature and nothing that checks it")
	}

	// The investor mandate, signed.
	addr := "tb1qnzten2u3ayqmnqtdul7z00v3uvapet7dv2789z"
	if res := h.do("POST", "/api/mandates/investor", session, map[string]any{
		"chain": "sequentia", "address": addr, "signature": walletSig,
	}); res.code != 200 {
		t.Fatalf("mandate: %d %s", res.code, res.raw)
	}
	m, err := h.s.st.InvestorMandateFor(aid, "sequentia")
	if err != nil || m == nil {
		t.Fatalf("mandate: %v %v", m, err)
	}
	if m.SignerXOnly == "" && m.SignerAddress == "" {
		t.Fatal("the mandate keeps a signature and nothing that checks it")
	}
}
