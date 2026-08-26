package main

import (
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Proving a statement when the signer has no enclave key.
//
// Every application statement SeqPal records is signed under a domain tag, and
// until now that meant a BIP340 signature from an OpenAMP enclave key. A SeqPal
// ID that is an ordinary wallet has no such key and no way to make one: its
// wallet signs classic messages, the kind verifymessage checks. So every one of
// these statements -- an e-signature on an offering document, a payout mandate,
// a bearer attestation, a holding proof on a dividend -- was unreachable for
// exactly the holders the wallet-backed ID exists to serve.
//
// Both forms are accepted now, and both prove the same thing: control of a key.
// What the tag is for survives either way. In the tagged form it separates the
// domain by construction. In the classic form the tag is written INTO the signed
// bytes, as
//
//	<tag>\n<statement>
//
// so a signature over one kind of statement still cannot be presented as another
// even where the statement text alone would not have said which it was.
//
// What is NOT accepted is a classic signature over the bare statement. That is
// the whole reason the tag is prefixed rather than merely checked alongside.

// classicStatementMessage is the text a wallet signs when it cannot sign tagged.
//
// Some of these statements are text a person can read; others are a 32-byte
// digest, which nobody can paste into a signing box. A digest is written as
// hex behind a marker, so both are printable and neither can be mistaken for
// the other: "hex:" cannot begin a canonical JSON statement, and a statement
// cannot be read as a digest.
func classicStatementMessage(tag string, msg []byte) string {
	if printableStatement(msg) {
		return tag + "\n" + string(msg)
	}
	return tag + "\nhex:" + hex.EncodeToString(msg)
}

// printableStatement reports whether a statement is text a person could read and
// retype: valid UTF-8, no control characters beyond the whitespace that formats
// it. Anything else is a digest and is written as hex.
func printableStatement(msg []byte) bool {
	if !utf8.Valid(msg) {
		return false
	}
	for _, r := range string(msg) {
		if r == '\n' || r == '\t' || r == '\r' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// looksLikeTaggedSig reports whether a signature is in the BIP340 form. A
// classic signed message is base64 and 88 characters; a BIP340 signature is 128
// hex. Nothing is decided on this alone -- it only picks which check to run
// first, and both must pass on their own terms.
func looksLikeTaggedSig(sig string) bool {
	s := strings.TrimSpace(sig)
	return len(s) == 128 && isHexStr(strings.ToLower(s))
}

// verifyKeyStatement checks a statement signed by one specific key, in either
// form. The address for the classic form is derived from the key rather than
// supplied, so a signature from some other key the holder controls will not do.
func (s *server) verifyKeyStatement(key, tag string, msg []byte, sig string) error {
	if looksLikeTaggedSig(sig) {
		return verifyTaggedByKey(key, tag, msg, sig)
	}
	if !validXOnly(key) {
		return fmt.Errorf("that signature needs a 32-byte x-only key to check against")
	}
	return s.verifyHoldingKeySignature(key, sig, classicStatementMessage(tag, msg))
}

// verifyAccountStatement checks a statement signed by the ACCOUNT.
//
// An enclave account signs it tagged with its enclave key, as it always has. An
// ID with no enclave signs it as an ordinary message from an address of one of
// the wallets it has linked -- the same address it signs in with -- and any of
// them will do, because they are all wallets this ID has proven.
func (s *server) verifyAccountStatement(acct *Account, tag string, msg []byte, sig string) error {
	if acct == nil {
		return fmt.Errorf("no account to check this signature against")
	}
	if acct.XOnly != "" && looksLikeTaggedSig(sig) {
		return verifyTaggedByKey(acct.XOnly, tag, msg, sig)
	}
	wallets, err := s.st.DescriptorWallets(acct.AID)
	if err != nil {
		return fmt.Errorf("could not read this ID's wallets: %v", err)
	}
	if len(wallets) == 0 {
		if acct.XOnly == "" {
			return fmt.Errorf("this SeqPal ID has no wallet able to sign this")
		}
		return verifyTaggedByKey(acct.XOnly, tag, msg, sig)
	}
	message := classicStatementMessage(tag, msg)
	var last error
	for _, wl := range wallets {
		addr, err := s.walletAddressAt(toPKH(wl.Descriptor), walletProofIndex)
		if err != nil {
			last = err
			continue
		}
		if err := s.verifyWalletMessage(addr, sig, message); err == nil {
			return nil
		} else {
			last = err
		}
	}
	if last == nil {
		last = fmt.Errorf("that signature does not verify for any wallet on this SeqPal ID")
	}
	return fmt.Errorf("the signature does not verify for this SeqPal ID: %v", last)
}

// accountSigningAddresses is what a holder has to be TOLD when they cannot sign
// tagged: which address of theirs the statement must be signed with.
func (s *server) accountSigningAddresses(acct *Account) []string {
	wallets, err := s.st.DescriptorWallets(acct.AID)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(wallets))
	for _, wl := range wallets {
		if addr, err := s.walletAddressAt(toPKH(wl.Descriptor), walletProofIndex); err == nil {
			out = append(out, addr)
		}
	}
	return out
}
