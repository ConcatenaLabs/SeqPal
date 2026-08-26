package main

import (
	"strings"
	"testing"
)

// The real descriptor a Sequentia descriptor wallet exports, taken from a node
// (elementsregtest, createwallet with descriptors). The base58 body of a tpub
// contains "h", "prv" and every other short string by chance, which is exactly
// what a loosely-written guard rejects by mistake, so the honest case is pinned
// here first.
const realPKH = "pkh([78a58319/44'/1'/0']tpubDCTudosJmS58rksmdnazbWxbQyCAcxncXqT9cQy5rpg94dyseRE5oNF99AhMxgn1bLxU94UeSxfUj6M2WwPRnxHjHkPaqoTXWkfigM2vcd1/0/*)#0wcatm2p"

func TestWalletDescriptorAcceptsARealOne(t *testing.T) {
	if err := validWalletDescriptor(realPKH); err != nil {
		t.Fatalf("a real pkh descriptor must be accepted, got: %v", err)
	}
	// The wallet's OWN form is the one a holder is most likely to paste, since
	// it is the one labelled "this wallet's addresses".
	if err := validWalletDescriptor(strings.Replace(realPKH, "pkh(", "wpkh(", 1)); err != nil {
		t.Fatalf("a wpkh descriptor must be accepted, got: %v", err)
	}
	// Without the checksum too: the node adds it, holders paste either.
	if err := validWalletDescriptor(strings.Split(realPKH, "#")[0]); err != nil {
		t.Fatalf("unchecksummed descriptor must be accepted, got: %v", err)
	}
}

func TestWalletDescriptorRefusesPrivateKeys(t *testing.T) {
	// An extended private key as a key token, which is the mistake worth
	// catching before it leaves the process.
	// Deliberately not a real key: a placeholder shaped like an extended private
	// key, which is all the guard looks at. A repository nobody should ever be
	// able to find a key in is not the place to keep one, even a spent one.
	priv := "pkh([78a58319/44'/1'/0']tprv" + strings.Repeat("z", 50) + "/0/*)"
	err := validWalletDescriptor(priv)
	if err == nil || !strings.Contains(err.Error(), "PRIVATE key") {
		t.Fatalf("an xprv descriptor must be refused as a private key, got: %v", err)
	}
	// And the refusal must never echo the key back.
	if strings.Contains(err.Error(), "tprv"+strings.Repeat("z", 10)) {
		t.Fatal("the refusal must not echo the private key")
	}
}

func TestWalletDescriptorShapeRules(t *testing.T) {
	cases := []struct{ desc, want string }{
		{"", "required"},
		{"tr([78a58319/86'/1'/0']tpubDCxfK778pheRPiuDBUsDRJydJHexhabABJNoLPyvUaJaw1e3LWJGAy7rW13vWA97AwvF1E6knt4utxmuRHK2ZuMQgRFXT/0/*)", "wpkh(...)"},
		{"pkh([78a58319/44'/1'/0']tpubDCTudosJmS58rksmdnazbWxbQyCAcxncXqT9cQy5rpg94dyseRE5oNF99AhMxgn1bLxU94UeSxfUj6M2WwPRnxHjHkPaqoTXWkfigM2vcd1)", "ranged receive chain"},
		{"pkh([78a58319/44'/1'/0']tpubDCTudosJmS58rksmdnazbWxbQyCAcxncXqT9cQy5rpg94dyseRE5oNF99AhMxgn1bLxU94UeSxfUj6M2WwPRnxHjHkPaqoTXWkfigM2vcd1/0'/0/*)", "hardened"},
	}
	for _, c := range cases {
		err := validWalletDescriptor(c.desc)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("descriptor %.40s: want error containing %q, got %v", c.desc, c.want, err)
		}
	}
}

// The account id must never be mistakable for an OpenAMP account id, because
// one grants access to an enclave and the other does not.
func TestWalletIDIsNotAnAID(t *testing.T) {
	id := walletIDFor(realPKH)
	if len(id) != 40 {
		t.Fatalf("a wallet account id is 20 bytes hex, got %d chars", len(id))
	}
	if id == aidFor([]string{strings.Repeat("ab", 32)}) {
		t.Fatal("wallet ids and AIDs must not collide")
	}
	// Same descriptor, same id: signing in again finds the same account.
	if walletIDFor(realPKH) != id {
		t.Fatal("the wallet id must be stable for a descriptor")
	}
	// A different descriptor is a different account.
	other := strings.Replace(realPKH, "/0/*", "/1/*", 1)
	if walletIDFor(other) == id {
		t.Fatal("different descriptors must not share an account id")
	}
	// The tag differs from the OpenAMP one, so no descriptor can ever hash to
	// the id of an enclave account.
	if walletIDTag == aidTag {
		t.Fatal("the wallet id tag must differ from the OpenAMP AID tag")
	}
}

// One wallet, one account, whichever form of the descriptor is pasted: the key
// is the account, the script type is only how its addresses are written.
func TestPKHNormalisationCollapsesTheForms(t *testing.T) {
	wpkh := strings.Replace(realPKH, "pkh(", "wpkh(", 1)
	if toPKH(wpkh) != toPKH(realPKH) {
		t.Fatalf("wpkh and pkh of one key must normalise to the same descriptor:\n  %s\n  %s",
			toPKH(wpkh), toPKH(realPKH))
	}
	if strings.Contains(toPKH(wpkh), "#") {
		t.Fatal("the checksum belongs to the text it was computed over; it must be dropped")
	}
	if !strings.HasPrefix(toPKH(wpkh), "pkh(") {
		t.Fatalf("normalised form must be pkh, got %.20s", toPKH(wpkh))
	}
}
