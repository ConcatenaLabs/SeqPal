package main

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

// vocabVersion is the platform-owned category vocabulary version. Every user's
// openampd category list is a projection of that user's claims record AT THIS
// version. When the vocabulary changes (for example splitting a jurisdiction
// code), bump this, migrate claims by rule, and re-stamp every affected user
// through the serialized write queue (Section 4.1). Assets and users never mix
// tokens from two versions.
const vocabVersion = 1

// Category tokens are compound: j:<ISO2>:<elig>, elig in {ret, acc, pro} plus
// {hnw, soph} for GB. The EU is per-member-state. A sanctions hit is NOT a
// category (it triggers freeze); staleness is a category removal (an expired
// claim projects to no categories).

var eligTiers = map[string]bool{"ret": true, "acc": true, "pro": true, "hnw": true, "soph": true}

// projectCategories computes the FULL category list for a claims record at the
// current vocabulary version, as of unix time now. It is pure: the openampd
// write queue calls it, the expiry cron calls it, and both get the same answer
// for the same record. An unverified, refused, or expired record projects to no
// categories, which is a real loss of eligibility once the list is written.
// eligibilityLive reports whether an identity is verified AND still inside its
// validity window.
//
// "verified" is a status a verification set and nothing later clears: the
// identity window passing does not change it, because what expiry does is empty
// the categories, which projectCategories has always done correctly. Every gate
// that asked only for the status therefore kept letting a lapsed identity
// through -- subscribing, claiming a corporate action, passing an offering gate
// -- while the categories those same gates rely on had already gone to nothing.
//
// Anything deciding what a holder may do asks this, not the status.
func eligibilityLive(c *Claims, now int64) bool {
	if c == nil || c.Status != "verified" {
		return false
	}
	return c.ValidUntil == 0 || now < c.ValidUntil
}

func projectCategories(c *Claims, now int64) []string {
	if c == nil || c.Status != "verified" {
		return nil
	}
	if c.ValidUntil > 0 && now >= c.ValidUntil {
		return nil // stale identity: eligibility removed everywhere
	}
	res := normalizeResidence(c.Residence)
	if res == "" {
		return nil
	}
	set := map[string]bool{}
	base := c.BaseEligibility
	if base != "pro" {
		base = "ret"
	}
	addToken(set, res, base)
	// Accreditation is an additive attribute, gated by a non-stale artifact.
	if c.Accredited && c.AccredArtifact != "" && (c.AccredValidUntil == 0 || now < c.AccredValidUntil) {
		addToken(set, res, "acc")
	}
	// GB statutory investor statements.
	if res == "GB" {
		if c.GBHNW {
			addToken(set, res, "hnw")
		}
		if c.GBSoph {
			addToken(set, res, "soph")
		}
	}
	// US-person capture (17 CFR 230.902(k)) is broader than residence: a US
	// person by determination carries j:US tokens independently of residence, so
	// the Reg S category_denies "j:US" window and US-admission rules bind them.
	if c.USPerson && res != "US" {
		addToken(set, "US", "ret")
		if c.Accredited && c.AccredArtifact != "" && (c.AccredValidUntil == 0 || now < c.AccredValidUntil) {
			addToken(set, "US", "acc")
		}
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func addToken(set map[string]bool, iso2, elig string) {
	if !eligTiers[elig] {
		return
	}
	set["j:"+iso2+":"+elig] = true
}

// normalizeResidence uppercases and validates a residence code. It accepts
// two-letter ISO codes and the Próspera special code HN-PRO.
func normalizeResidence(r string) string {
	r = strings.ToUpper(strings.TrimSpace(r))
	if r == "HN-PRO" {
		return r
	}
	if len(r) == 2 && isAlpha(r) {
		return r
	}
	return ""
}

func isAlpha(s string) bool {
	for _, c := range s {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

// --- enclave / platform key generation ---------------------------------------

// newEnclaveKey generates a fresh secp256k1 keypair and returns (privHex,
// xonlyHex). seqpald custodies the private key for the per-offering escrow
// enclaves, the per-entity treasury enclaves, and the platform claims-signing
// key; the private key is written only to the box-local SQLite store and is
// never logged, returned to a client, or committed.
func newEnclaveKey() (privHex, xonlyHex string, err error) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		return "", "", err
	}
	privHex = hex.EncodeToString(priv.Serialize())
	xonlyHex = hex.EncodeToString(schnorr.SerializePubKey(priv.PubKey()))
	return privHex, xonlyHex, nil
}

// signTagged produces a BIP340 signature over a tagged hash of msg with a stored
// private key. Tagging keeps a platform signature from ever colliding with a
// spendable transfer sighash.
func signTagged(privHex, tag string, msg []byte) (string, error) {
	pb, err := hex.DecodeString(privHex)
	if err != nil {
		return "", err
	}
	priv, _ := btcec.PrivKeyFromBytes(pb)
	digest := taggedHash(tag, msg)
	sig, err := schnorr.Sign(priv, digest[:])
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sig.Serialize()), nil
}

// signSighash produces a BIP340 signature over a RAW 32-byte transfer sighash
// (NOT a tagged hash) with a custodied enclave private key. This is the escrow
// enclave's holder-side signature in the hosted transfer flow: openampd's
// /v1/transfers/{id}/complete verifies sig.Verify(sighash, pubkey), so the digest
// must be signed exactly as delivered, unlike the tagged challenge/claims paths.
func signSighash(privHex, sighashHex string) (string, error) {
	pb, err := hex.DecodeString(privHex)
	if err != nil {
		return "", err
	}
	digest, err := hex.DecodeString(sighashHex)
	if err != nil || len(digest) != 32 {
		return "", fmt.Errorf("sighash must be a 32-byte hex digest")
	}
	priv, _ := btcec.PrivKeyFromBytes(pb)
	sig, err := schnorr.Sign(priv, digest)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sig.Serialize()), nil
}

// TAG REGISTRY. Every platform signature is BIP340 over a TAGGED hash, one tag
// per purpose, so no two signatures can ever be replayed across purposes:
//
//	openamp-challenge-v1            auth challenge (auth.go)
//	seqpal-claims-v1                platform-signed claims record (below)
//	seqpal-ubo-v1                   UBO link statement (id.go)
//	openamp-document-v1             document e-signature (docapi.go)
//	seqpal-payout-mandate-v1        issuer + investor payout mandates (platform.go)
//	seqpal-close-v1                 closing authorization (closing.go)
//	seqpal-market-abuse-ack-v1      market-abuse acknowledgment (secondary.go)
//	seqpal-listing-v1               listing authorization (listings.go)
//	seqpal-bearer-attestation-v1    bearer no-US-nexus attestation (bearer.go)
//	seqpal-holding-proof-v1         corporate-action holding proof (actions.go)
//
// The ONLY raw (untagged) signatures are over 32-byte messages a signer cannot
// choose: real transfer sighashes (signSighash above, and the SPA's
// signClawbackSighash), and the supervision freeze/unfreeze messages the node
// produces (getsupervisionrecordhash / getsupervisionunfreezehash; see
// supervision.go), which bind a specific prevout or record outpoint and so can
// never collide with anything else worth signing.
const claimsTag = "seqpal-claims-v1"

// verifyTaggedByKey checks a BIP340 signature over a tagged hash of msg against
// an x-only public key. Used for the UBO link signature (tag seqpal-ubo-v1).
func verifyTaggedByKey(xonly, tag string, msg []byte, sigHex string) error {
	pkBytes, err := hex.DecodeString(xonly)
	if err != nil || len(pkBytes) != 32 {
		return fmt.Errorf("xonly must be a 32-byte hex key")
	}
	pub, err := schnorr.ParsePubKey(pkBytes)
	if err != nil {
		return fmt.Errorf("invalid x-only public key")
	}
	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil || len(sigBytes) != 64 {
		return fmt.Errorf("sig must be a 64-byte hex BIP340 signature")
	}
	sig, err := schnorr.ParseSignature(sigBytes)
	if err != nil {
		return fmt.Errorf("sig is not a valid BIP340 signature")
	}
	digest := taggedHash(tag, msg)
	if !sig.Verify(digest[:], pub) {
		return fmt.Errorf("signature does not verify")
	}
	return nil
}
