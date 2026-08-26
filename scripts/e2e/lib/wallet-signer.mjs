// A stand-in for the holder's Sequentia wallet, for the conformance tests.
//
// SeqPal itself signs nothing: a SeqPal ID is the enclave key of a wallet the
// holder already has, and every signature is produced there (src/lib/wallet.js).
// The bytes that get signed are still SeqPal's business, though, and seqpald
// verifies them exactly, so the suites below keep asserting the signed-bytes
// contract. This module plays the wallet's part: it holds a key and reproduces
// what a wallet does with it, against the message constructions that ship in
// src/lib/statements.js.
//
// The acceptance driver in scripts/e2e uses it for the same reason: it plays a
// holder, so it needs a wallet. Nothing here is imported by the application, and
// test/logic.test.js fails if anything ever is.
import { schnorr, secp256k1 } from '@noble/curves/secp256k1'
import { bytesToHex, hexToBytes } from '@noble/curves/abstract/utils'
import { sha256 } from '@noble/hashes/sha256'
import {
  BEARER_ATTESTATION_TAG,
  CLOSE_TAG,
  DOCUMENT_TAG,
  HOLDING_PROOF_TAG,
  MANDATE_TAG,
  bearerAttestationDigest,
  holdingProofDigest,
} from '../../../src/lib/statements.js'

const enc = new TextEncoder()
// Deterministic, so a signature is a stable cross-implementation test vector.
const NO_AUX = new Uint8Array(32)

export function generateEnclaveKey() {
  const priv = schnorr.utils.randomPrivateKey()
  return { priv: bytesToHex(priv), xonly: bytesToHex(schnorr.getPublicKey(priv)) }
}
export const generateRecoveryKey = generateEnclaveKey

export function xonlyOf(privHex) {
  return bytesToHex(schnorr.getPublicKey(hexToBytes(privHex)))
}

// BIP340's tagged hash: sha256(sha256(tag) || sha256(tag) || msg).
export function taggedHash(tag, msg) {
  const t = sha256(enc.encode(tag))
  const h = sha256.create()
  h.update(t)
  h.update(t)
  h.update(msg)
  return h.digest()
}

// What a wallet's openampSignTagged does: sign the tagged hash of a message.
export function signTaggedBytes(privHex, tag, msg) {
  return bytesToHex(schnorr.sign(taggedHash(tag, msg), hexToBytes(privHex), NO_AUX))
}
export function signStatement(privHex, tag, statement) {
  return signTaggedBytes(privHex, tag, enc.encode(statement))
}
export function signTaggedDigest(privHex, tag, digestHex) {
  return signTaggedBytes(privHex, tag, hexToBytes(digestHex))
}

export const signChallenge = (priv, challenge) =>
  signStatement(priv, 'openamp-challenge-v1', challenge)
export const signDocument = (priv, docHashHex) => signTaggedDigest(priv, DOCUMENT_TAG, docHashHex)
export const signMandate = (priv, statement) => signStatement(priv, MANDATE_TAG, statement)
export const signClosing = (priv, statement) => signStatement(priv, CLOSE_TAG, statement)
export const signBearerAttestation = (priv, fields) =>
  signTaggedDigest(priv, BEARER_ATTESTATION_TAG, bearerAttestationDigest(fields))
export const signHoldingProof = (priv, fields) =>
  signTaggedDigest(priv, HOLDING_PROOF_TAG, holdingProofDigest(fields))

// The raw-digest signers. A wallet only ever produces these for a spend it
// decoded and a sighash it recomputed itself, which is the whole reason SeqPal
// hands it the transaction rather than the digest.
function signRawDigest(privHex, digestHex, what) {
  const digest = hexToBytes(digestHex)
  if (digest.length !== 32) throw new Error(`A ${what} must be a 32-byte digest.`)
  return bytesToHex(schnorr.sign(digest, hexToBytes(privHex), NO_AUX))
}
export const signSighash = (priv, h) => signRawDigest(priv, h, 'transfer sighash')
export const signClawbackSighash = (priv, h) => signRawDigest(priv, h, 'clawback sighash')
export const signSupervisionMessage = (priv, h) => signRawDigest(priv, h, 'supervision message')

// The account id the policy server derives from a set of x-only keys:
// sha256("openamp-aid-v1" || sorted-pubkey-hex), first 20 bytes.
export function computeAID(xonlyHexes) {
  const h = sha256.create()
  h.update(enc.encode('openamp-aid-v1'))
  for (const pk of [...xonlyHexes].sort()) h.update(enc.encode(pk))
  return bytesToHex(h.digest().slice(0, 20))
}

// ── the OTHER kind of wallet ─────────────────────────────────────────────────
//
// A SeqPal ID that is only a wallet has no OpenAMP key and signs nothing under
// SeqPal's tags. It signs the way every wallet has always been able to: an
// ordinary message, with the private key of one of its addresses, in the format
// `verifymessage` checks. The node is the authority on that format; this is the
// same construction, so the drill can play that holder instead of describing it.
//
// The magic string is Bitcoin's, unchanged in this node (`MESSAGE_MAGIC` in
// src/util/message.cpp), and the digest is its double-sha256. The signature is
// the 65-byte compact recoverable form, base64, with the recovery id offset by
// 27 (+4 when the address is for a compressed key, which every modern one is).

function varint(n) {
  if (n < 0xfd) return Uint8Array.of(n)
  if (n <= 0xffff) return Uint8Array.of(0xfd, n & 0xff, (n >> 8) & 0xff)
  throw new Error('message too long for this drill')
}

function concat(...parts) {
  const total = parts.reduce((n, p) => n + p.length, 0)
  const out = new Uint8Array(total)
  let i = 0
  for (const p of parts) {
    out.set(p, i)
    i += p.length
  }
  return out
}

export function messageDigest(message) {
  const magic = enc.encode('Bitcoin Signed Message:\n')
  const body = enc.encode(message)
  return sha256(sha256(concat(varint(magic.length), magic, varint(body.length), body)))
}

// Sign as an ordinary wallet does. Returns the base64 signature `verifymessage`
// takes, for the address of the COMPRESSED public key of this private key.
export function signWalletMessage(privHex, message) {
  const sig = secp256k1.sign(messageDigest(message), hexToBytes(privHex))
  const compact = sig.toCompactRawBytes()
  const out = new Uint8Array(65)
  out[0] = 27 + 4 + sig.recovery
  out.set(compact, 1)
  return Buffer.from(out).toString('base64')
}

// The compressed public key, which is what a pkh(...) descriptor is built from.
export function compressedOf(privHex) {
  return bytesToHex(secp256k1.getPublicKey(hexToBytes(privHex), true))
}
