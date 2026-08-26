package main

import (
	"strings"
	"testing"
)

// The tagged form still works, and is still the only thing an enclave account's
// signature is checked as.
func TestTaggedStatementStillVerifies(t *testing.T) {
	h := newHarness(t)
	acct := &Account{AID: aidFor([]string{xonlyHex(t, vecPriv)}), XOnly: xonlyHex(t, vecPriv), Identity: "aid"}
	msg := []byte(`{"v":1}`)
	sig, err := signTagged(vecPriv, "seqpal-test-v1", msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.s.verifyAccountStatement(acct, "seqpal-test-v1", msg, sig); err != nil {
		t.Fatalf("a tagged signature from the enclave key must verify: %v", err)
	}
	// A different tag is a different statement.
	if err := h.s.verifyAccountStatement(acct, "seqpal-other-v1", msg, sig); err == nil {
		t.Fatal("a signature under one tag must not verify under another")
	}
}

// The classic form is what a wallet with no enclave can actually produce, and
// the tag has to be inside the signed bytes or it separates nothing.
func TestClassicStatementCarriesTheTag(t *testing.T) {
	msg := []byte(`{"asset":"abc"}`)
	a := classicStatementMessage("seqpal-mandate-v1", msg)
	b := classicStatementMessage("seqpal-close-v1", msg)
	if a == b {
		t.Fatal("two tags over one statement must not produce the same signed text")
	}
	if !strings.HasPrefix(a, "seqpal-mandate-v1\n") {
		t.Fatalf("the tag must lead the signed text, got %q", a)
	}
	if !strings.HasSuffix(a, string(msg)) {
		t.Fatalf("the statement must be signed verbatim, got %q", a)
	}
}

// A wallet-backed ID's statement is checked against its own wallets' addresses,
// so a signature from a key it has not linked is not accepted.
func TestWalletBackedStatementUsesItsOwnWallets(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	session, aid := walletSession(t, h, testPKH)
	_ = session
	acct, _ := h.s.st.AccountByAID(aid)
	if acct.XOnly != "" {
		t.Fatal("a wallet-backed ID has no enclave key")
	}
	// This node verifies any message, standing in for a wallet that really did
	// sign: the point under test is that the account's own wallets are consulted.
	if err := h.s.verifyAccountStatement(acct, "seqpal-test-v1", []byte(`{"v":1}`), "AAAA"); err != nil {
		t.Fatalf("a wallet-backed ID must be able to sign a statement: %v", err)
	}
	// And it is told which addresses those are, or it cannot sign at all.
	if len(h.s.accountSigningAddresses(acct)) == 0 {
		t.Fatal("the holder has to be told which address to sign with")
	}
}

// A signature that verifies for nothing is refused rather than waved through by
// whichever branch happens to run.
func TestAStatementWithNoValidSignatureIsRefused(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNodeNoMatch(t).URL
	session, aid := walletSession(t, h, testPKH)
	_ = session
	acct, _ := h.s.st.AccountByAID(aid)
	err := h.s.verifyAccountStatement(acct, "whitelist-request", []byte(`{"v":1}`), "AAAA")
	if err == nil {
		t.Fatal("a signature that verifies for none of this ID's wallets must be refused")
	}
}

// An account with neither an enclave nor a linked wallet cannot sign anything,
// and must be told so rather than silently passing.
func TestAnAccountWithNoWalletCannotSign(t *testing.T) {
	h := newHarness(t)
	acct := &Account{AID: "deadbeef", XOnly: "", Identity: "xpub"}
	if err := h.s.verifyAccountStatement(acct, "t", []byte("x"), "sig"); err == nil {
		t.Fatal("an ID with no wallet must not verify anything")
	}
}

// A digest is not text. Written raw it could not be pasted into a signing box at
// all, and written bare it could be confused with a statement.
func TestADigestStatementIsWrittenAsHex(t *testing.T) {
	digest := make([]byte, 32)
	for i := range digest {
		digest[i] = byte(i)
	}
	got := classicStatementMessage("openamp-document-v1", digest)
	if !strings.HasPrefix(got, "openamp-document-v1\nhex:") {
		t.Fatalf("a digest must be written as hex behind a marker, got %q", got)
	}
	if !printableStatementText(got) {
		t.Fatalf("the signed text must be printable, got %q", got)
	}
	// A readable statement is left readable.
	text := []byte(`{"asset":"abc","v":1}`)
	if got := classicStatementMessage("t", text); got != "t\n"+string(text) {
		t.Fatalf("a readable statement must be signed verbatim, got %q", got)
	}
	// And the two forms cannot collide: a statement never starts with hex:.
	if strings.HasPrefix(string(text), "hex:") {
		t.Fatal("a canonical statement must not begin with the digest marker")
	}
}

func printableStatementText(s string) bool {
	for _, r := range s {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
