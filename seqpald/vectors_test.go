package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

// Conformance vectors for the three crypto seams SeqPal shares with other
// implementations: the AID (with openampd), the tagged login challenge (with the
// browser signer), and the canonical terms hash (with the browser and the asset
// contract). The AID values below were produced by running openampd's own
// store.AID (openamp repo, openampd/internal/store/store.go) over these keys;
// the challenge and terms values were produced by the browser's signer and
// hasher. If a change here forces a vector to move, the wire format changed and
// every other implementation is now wrong.
const (
	vecPriv  = "b7e151628aed2a6abf7158809cf4f3c762e7160f38b4da56a784d9045190cfef"
	vecXOnly = "dff1d77f2a671c5f36183726db2341be58feae1da2deced843240f7b502ba659"
	vecAID   = "51738ef8815e1590eba576eef1ac714f9e969d52"

	vecPriv2  = "c90fdaa22168c234c4c6628b80dc1cd129024e088a67cc74020bbea63b14e5c9"
	vecXOnly2 = "dd308afec5777e13121fa72b9cc1b7cc0139715309b086c960e18fd969774eb8"
	vecAID2   = "29dae194e3b97260d4797713ab11f9a9f9b3e54d"
	vecAIDSet = "309533c797997e92f0a73caa81093cab6192a17a" // AID of the {key1, key2} SET

	vecChallenge    = "2f0ee2f6a1cfbd0c4a3f1f8a7a4bbf6d5e2c19d84a0fbb1c7d6e5f4a3b2c1d0e"
	vecTaggedDigest = "c516dcf501fdf1271f950104a6663a53b303be25f604b601185521d150a54e25"
	vecChallengeSig = "41c1aebb8689dba894f4cdc3d2c093929246f15a3e3ecfbc724f81997eb9411f" +
		"ab4cbae14aac412f1ff1c724d708ab4da2a336cc0717df13c9206cc02e04be5d"
	// A BIP340 signature by the SAME key over the UNTAGGED sha256 of the same
	// challenge. It must never be accepted: accepting untagged digests turns the
	// enclave key into a signing oracle over transfer sighashes (openamp
	// spec/venue-wallet-integration.md 0.4).
	vecRawDigest = "d84c1c8b63b45e8a91de7d506cdeda0656c4c166e6d1e5f832b899f2fcb66dff"
	vecRawSig    = "03aaf4efdb63605d854633017da8f3a6def8cfc3f5d034b17d908619fe1c72117" +
		"2389208e6c48f19933ff206f54d1772400f188b12fd1910638637147408e627"

	vecTermsHash      = "995980cacb0fa2b0a2e27639d962f1666fab212c982895a5036a2741c3fdfe64"
	vecTermsCanonical = `{"clawback":true,"jurisdiction":"HN-PROSPERA","note":"Terms & conditions <v1>",` +
		`"raise":{"amount":5000000,"unit":"USD"},"structure":"native-equity",` +
		`"transfer_restrictions":{"accredited_only":true,"blocked":["KP","IR"],"lockup_days":365}}`
)

// vecTermsShuffled is the same terms object with every object's keys in a
// different order and whitespace throughout: canonicalization must erase both.
const vecTermsShuffled = `{
  "note": "Terms & conditions <v1>",
  "clawback": true,
  "raise": { "unit": "USD", "amount": 5000000 },
  "transfer_restrictions": {
      "blocked": ["KP", "IR"],
      "accredited_only": true,
      "lockup_days": 365
  },
  "jurisdiction": "HN-PROSPERA",
  "structure": "native-equity"
}`

func TestAIDGoldenVector(t *testing.T) {
	if got := aidFor([]string{vecXOnly}); got != vecAID {
		t.Fatalf("aidFor(key1) = %s, want %s (openampd store.AID ground truth)", got, vecAID)
	}
	if got := aidFor([]string{vecXOnly2}); got != vecAID2 {
		t.Fatalf("aidFor(key2) = %s, want %s", got, vecAID2)
	}
	// The AID hashes the SORTED key set, so presentation order cannot change it.
	if got := aidFor([]string{vecXOnly, vecXOnly2}); got != vecAIDSet {
		t.Fatalf("aidFor(key1,key2) = %s, want %s", got, vecAIDSet)
	}
	if got := aidFor([]string{vecXOnly2, vecXOnly}); got != vecAIDSet {
		t.Fatalf("aidFor(key2,key1) = %s, want %s (AID must be order-independent)", got, vecAIDSet)
	}
	// A different key set is a different account.
	if vecAID == vecAID2 || vecAID == vecAIDSet {
		t.Fatal("distinct key sets must produce distinct AIDs")
	}
}

func TestTaggedChallengeVector(t *testing.T) {
	digest := taggedHash(challengeTag, []byte(vecChallenge))
	if got := hex.EncodeToString(digest[:]); got != vecTaggedDigest {
		t.Fatalf("tagged digest = %s, want %s (the browser signs this exact digest)", got, vecTaggedDigest)
	}

	// The signature the browser's signChallenge produced over that digest.
	if err := verifyChallengeSig(vecXOnly, vecChallenge, vecChallengeSig); err != nil {
		t.Fatalf("Go verifier rejected the browser's tagged signature: %v", err)
	}

	// The anti-oracle rule: a valid BIP340 signature by the same key over the
	// RAW (untagged) challenge digest is not a login.
	if sha256Hex([]byte(vecChallenge)) != vecRawDigest {
		t.Fatal("raw-digest vector no longer matches sha256(challenge)")
	}
	if err := verifyChallengeSig(vecXOnly, vecChallenge, vecRawSig); err == nil {
		t.Fatal("a raw-signed digest was accepted where a tagged signature is required (signing-oracle hole)")
	}

	// Wrong key, and a signature bound to a different challenge, both fail.
	if err := verifyChallengeSig(vecXOnly2, vecChallenge, vecChallengeSig); err == nil {
		t.Fatal("a signature verified under the wrong public key")
	}
	other := vecChallenge[:63] + "f"
	if err := verifyChallengeSig(vecXOnly, other, vecChallengeSig); err == nil {
		t.Fatal("a signature over one challenge was accepted for another")
	}

	// A freshly made Go signature over the tagged digest verifies too, so the two
	// signers are interchangeable.
	if err := verifyChallengeSig(vecXOnly, vecChallenge, signChallengeHex(t, vecPriv, vecChallenge)); err != nil {
		t.Fatalf("Go-produced tagged signature rejected: %v", err)
	}
}

func TestTermsHashCanonicalization(t *testing.T) {
	var obj any
	if err := json.Unmarshal([]byte(vecTermsShuffled), &obj); err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalJSON(obj)
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != vecTermsCanonical {
		t.Fatalf("canonical form =\n%s\nwant\n%s", canonical, vecTermsCanonical)
	}
	if got := sha256Hex(canonical); got != vecTermsHash {
		t.Fatalf("terms_hash = %s, want %s (the browser hashes the same bytes)", got, vecTermsHash)
	}
	// Reordering keys and adding whitespace must not move the hash; changing a
	// single value must.
	var again any
	if err := json.Unmarshal([]byte(vecTermsCanonical), &again); err != nil {
		t.Fatal(err)
	}
	c2, err := canonicalJSON(again)
	if err != nil {
		t.Fatal(err)
	}
	if sha256Hex(c2) != vecTermsHash {
		t.Fatal("re-canonicalizing the canonical form changed the hash")
	}
	var changed map[string]any
	if err := json.Unmarshal([]byte(vecTermsCanonical), &changed); err != nil {
		t.Fatal(err)
	}
	changed["clawback"] = false
	c3, err := canonicalJSON(changed)
	if err != nil {
		t.Fatal(err)
	}
	if sha256Hex(c3) == vecTermsHash {
		t.Fatal("a changed term produced the same terms_hash")
	}
}

// signChallengeHex signs a challenge the way the browser must: BIP340 over the
// tagged hash of the challenge string, never over a raw digest.
func signChallengeHex(t *testing.T, privHex, challenge string) string {
	t.Helper()
	pb, err := hex.DecodeString(privHex)
	if err != nil {
		t.Fatal(err)
	}
	priv, _ := btcec.PrivKeyFromBytes(pb)
	digest := taggedHash(challengeTag, []byte(challenge))
	sig, err := schnorr.Sign(priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(sig.Serialize())
}

func xonlyHex(t *testing.T, privHex string) string {
	t.Helper()
	pb, err := hex.DecodeString(privHex)
	if err != nil {
		t.Fatal(err)
	}
	priv, _ := btcec.PrivKeyFromBytes(pb)
	return hex.EncodeToString(schnorr.SerializePubKey(priv.PubKey()))
}

func TestXOnlyVectors(t *testing.T) {
	if got := xonlyHex(t, vecPriv); got != vecXOnly {
		t.Fatalf("xonly(priv1) = %s, want %s", got, vecXOnly)
	}
	if got := xonlyHex(t, vecPriv2); got != vecXOnly2 {
		t.Fatalf("xonly(priv2) = %s, want %s", got, vecXOnly2)
	}
}

// The two digests a wallet actually signs, pinned to fixed bytes on BOTH sides
// of the wire.
//
// The browser builds these from src/lib/statements.js and the holder signs the
// result; seqpald rebuilds them here and checks the signature against it. The
// two constructions have to produce identical bytes, and nothing proved they
// did: a field renamed, reordered or added on one side would change what is
// signed, both sides would still agree with themselves, every test would pass,
// and every signature would stop verifying -- or worse, cover bytes the holder
// was never shown.
//
// The same constants are asserted in test/logic.test.js. Changing a statement
// means changing both, deliberately, which is the point.
func TestTheDigestsAWalletSignsAreFixed(t *testing.T) {
	const (
		issuanceID = "11223344556677889900aabb"
		aid        = "c80cdf9652c0621b4cc70a856c82ed1c99582032"
		asset      = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
		actionID   = "act0123456789abcdef012345"
		payout     = "tb1qnzten2u3ayqmnqtdul7z00v3uvapet7dv2789z"

		wantAttestation  = "dd4f59f9bb5a4996d48639ee5cf88957136dfe8382ccad9df7eae447e2b26867"
		wantHoldingProof = "9dfde23596857df2e4a76be48068939322880b5d134b13e30ddc177029cb61ea"
	)

	att := sha256.Sum256(bearerAttestationStatement(issuanceID, aid))
	if got := hex.EncodeToString(att[:]); got != wantAttestation {
		t.Fatalf("the bearer attestation digest changed:\n got  %s\n want %s\nIf this was deliberate, "+
			"change src/lib/statements.js and test/logic.test.js in the same breath", got, wantAttestation)
	}

	action := &CorporateAction{ID: actionID, AssetID: asset, RecordHeight: 100000, Kind: "dividend"}
	// Deliberately out of order: the construction sorts them, and a signature
	// must not depend on the order a holder happened to select them in.
	outpoints := []string{
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb:1",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:0",
	}
	hp := sha256.Sum256(holdingProofStatement(action, outpoints, payout, "", aid))
	if got := hex.EncodeToString(hp[:]); got != wantHoldingProof {
		t.Fatalf("the holding-proof digest changed:\n got  %s\n want %s\nIf this was deliberate, "+
			"change src/lib/statements.js and test/logic.test.js in the same breath", got, wantHoldingProof)
	}
}

// The canonical form itself, pinned. Everything signed or committed on this
// platform is canonical JSON, and the browser writes it too: the terms hash a
// deploy commits on chain is computed on both sides and compared. So the two
// implementations have to agree over every JSON value, not just the easy ones.
//
// test/logic.test.js asserts the SAME expected string from the browser's
// implementation. The cases are the ones that usually diverge: HTML characters
// (Go escapes them unless told not to), non-BMP characters, control characters,
// the solidus, exponent formatting, integers past 2^53, and negative zero.
func TestOneCanonicalFormInBothLanguages(t *testing.T) {
	const doc = `{"amp":"Smith & Sons <Holdings>","ctl":"tab\there","emo":"a🙂b","sol":"a/b",` +
		`"uni":"Ærø Zürich 東京","big":100000000000000000000,"exp":1e21,"tiny":1e-7,` +
		`"round":9007199254740993,"negzero":-0,"one":1.0}`
	const want = `{"amp":"Smith & Sons <Holdings>","big":100000000000000000000,"ctl":"tab\there",` +
		`"emo":"a🙂b","exp":1e+21,"negzero":0,"one":1,"round":9007199254740992,"sol":"a/b",` +
		`"tiny":1e-7,"uni":"Ærø Zürich 東京"}`

	var v any
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatal(err)
	}
	got, err := canonicalJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("the canonical form changed:\n got  %s\n want %s\nIf this was deliberate, change "+
			"src/lib/openamp.js and test/logic.test.js in the same breath", got, want)
	}
}
