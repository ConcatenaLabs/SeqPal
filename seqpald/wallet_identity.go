package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// A SeqPal ID backed by a WALLET DESCRIPTOR rather than an OpenAMP enclave key.
//
// Not every wallet has an enclave key. A hardware wallet, a node wallet, a
// plain Bitcoin-style wallet: none of them can hold OpenAMP restricted assets,
// and requiring one to get a SeqPal ID shut them out of everything else, which
// is most of what SeqPal does. A supervised (freely-tradable) stock is an
// ordinary on-chain holding, and OpenDAMP identifies holders by KEY rather than
// by account id, so neither needs an enclave account at all.
//
// So an account may instead be identified by a descriptor the holder controls.
// Such an account cannot issue or receive OpenAMP restricted assets until an
// enclave key is attached to it (see handleAttachAID), and every OpenAMP path
// says so rather than failing obscurely.
//
// Proof of possession is an ordinary signed message. The node derives the
// address and checks the signature -- getdescriptorinfo, deriveaddresses and
// verifymessage are pure functions of their arguments and need no wallet, so
// there is no key handling and no bespoke crypto here. It also means the proof
// is one an ordinary wallet can already produce: "sign this message with this
// address" is a button that has existed for fifteen years, which is the whole
// point of accepting these wallets in the first place.

const walletIDTag = "seqpal-wallet-account-v1"

// walletIDFor is the account id of a descriptor-backed account. The tag is
// deliberately NOT openamp-aid-v1: this is not an OpenAMP account id and must
// never be mistakable for one, in this database or in a URL.
func walletIDFor(canonicalDesc string) string {
	h := sha256.New()
	h.Write([]byte(walletIDTag))
	h.Write([]byte(canonicalDesc))
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:20])
}

// An extended PRIVATE key, as a key token rather than as a substring. Base58
// bodies contain "prv" and every other short string by chance, so matching
// loosely would reject almost every honest descriptor; this matches the prefix
// only where a long base58 run follows it, which is what a key looks like.
var extendedPrivateKey = regexp.MustCompile(`(?i)\b[xtuvyz]prv[1-9A-HJ-NP-Za-km-z]{40,}`)

// checkNoPrivateKey refuses a descriptor carrying key material. The node's
// hasprivatekeys is the authority; this is the cheap check that fires before
// the value ever leaves the process, because a holder who has pasted a private
// key into a website is not helped by being told afterwards.
func checkNoPrivateKey(desc string) error {
	if extendedPrivateKey.MatchString(desc) {
		return fmt.Errorf("that descriptor contains a PRIVATE key. SeqPal never wants one: " +
			"paste the public descriptor your wallet exports for watching, the one with an " +
			"extended PUBLIC key. Treat the key you pasted as compromised and move those funds")
	}
	return nil
}

// validWalletDescriptor checks the shape SeqPal can actually work with, before
// asking the node anything.
//
//   - pkh(...) because the node's signmessage signs for key-hash addresses and
//     nothing else, so this is the only form a holder can prove with today.
//   - a ranged, UNHARDENED receive chain, because SeqPal derives addresses from
//     public data alone; a hardened path cannot be derived from an xpub at all.
func validWalletDescriptor(desc string) error {
	d := strings.TrimSpace(desc)
	if d == "" {
		return fmt.Errorf("a wallet descriptor is required")
	}
	if len(d) > 1024 {
		return fmt.Errorf("that descriptor is too long to be one")
	}
	if err := checkNoPrivateKey(d); err != nil {
		return err
	}
	if !strings.HasPrefix(d, "pkh(") {
		return fmt.Errorf("SeqPal needs the pkh(...) descriptor, the legacy one. It is the only " +
			"kind a wallet can sign a message for, which is how you prove the wallet is yours")
	}
	// A hardened step BELOW the account level cannot be derived from public data.
	// Only the path segments are inspected: the key itself is base58, which
	// contains "h" and everything else by chance.
	tail := d
	if i := strings.LastIndex(tail, "]"); i >= 0 {
		tail = tail[i+1:]
	}
	tail = strings.TrimSuffix(tail, ")")
	if i := strings.Index(tail, "#"); i >= 0 {
		tail = tail[:i]
	}
	segs := strings.Split(tail, "/")
	for _, seg := range segs[1:] { // segs[0] is the key
		if strings.ContainsAny(seg, "'h") {
			return fmt.Errorf("that descriptor derives through a hardened step below the account " +
				"level, which cannot be derived from an extended public key")
		}
	}
	if !strings.Contains(d, "/0/*") {
		return fmt.Errorf("that descriptor has no ranged receive chain (.../0/*), so there is no " +
			"first address to prove it with")
	}
	return nil
}

// canonicalWalletDescriptor validates the descriptor at the node and returns it
// in canonical form with its checksum. The node is the authority on what a
// descriptor means, so it is not re-implemented here.
func (s *server) canonicalWalletDescriptor(desc string) (string, error) {
	if err := validWalletDescriptor(desc); err != nil {
		return "", err
	}
	res, err := s.nodeRPC("getdescriptorinfo", strings.TrimSpace(desc))
	if err != nil {
		return "", fmt.Errorf("that is not a descriptor this network understands: %v", err)
	}
	var out struct {
		Descriptor string `json:"descriptor"`
		Checksum   string `json:"checksum"`
		HasPrivate bool   `json:"hasprivatekeys"`
	}
	if err := json.Unmarshal(res, &out); err != nil || out.Descriptor == "" {
		return "", fmt.Errorf("the node did not recognise that descriptor")
	}
	if out.HasPrivate {
		return "", fmt.Errorf("that descriptor contains a PRIVATE key; SeqPal never wants one")
	}
	if strings.Contains(out.Descriptor, "#") {
		return out.Descriptor, nil
	}
	if out.Checksum == "" {
		return "", fmt.Errorf("the node returned no checksum for that descriptor")
	}
	return out.Descriptor + "#" + out.Checksum, nil
}

// walletAddressAt derives one address of a canonical descriptor. Public data
// only: this is the same derivation any watch-only wallet performs.
func (s *server) walletAddressAt(canonicalDesc string, index int) (string, error) {
	res, err := s.nodeRPC("deriveaddresses", canonicalDesc, []int{index, index})
	if err != nil {
		return "", fmt.Errorf("could not derive an address from that descriptor: %v", err)
	}
	var addrs []string
	if err := json.Unmarshal(res, &addrs); err != nil || len(addrs) == 0 {
		return "", fmt.Errorf("the node derived no address from that descriptor")
	}
	return addrs[0], nil
}

// verifyWalletMessage checks an ordinary signed message against an address.
func (s *server) verifyWalletMessage(address, signature, message string) error {
	res, err := s.nodeRPC("verifymessage", address, strings.TrimSpace(signature), message)
	if err != nil {
		return fmt.Errorf("that signature could not be checked: %v", err)
	}
	var ok bool
	if err := json.Unmarshal(res, &ok); err != nil {
		return fmt.Errorf("the node gave no verdict on that signature")
	}
	if !ok {
		return fmt.Errorf("that signature does not verify for this address. Sign the challenge " +
			"exactly as shown, with the address SeqPal named and no other")
	}
	return nil
}
