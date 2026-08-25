import { test } from 'node:test'
import assert from 'node:assert/strict'

import {
  generateEnclaveKey,
  xonlyOf,
  taggedHash,
  signSighash,
  signChallenge,
  signDocument,
} from '../scripts/e2e/lib/wallet-signer.mjs'
import { schnorr } from '@noble/curves/secp256k1'
import { bytesToHex, hexToBytes } from '@noble/curves/abstract/utils'

// M9 (external issuer key + two-phase clawback) browser-side conformance. For a
// NEW asset the enclave issuer half is the entity's own SeqPal ID key, so a
// clawback needs the ISSUER's browser signature over the REAL L_claw script-path
// spend sighash openampd returns in the two-phase build. openampd's
// handleClawbackComplete verifies each signature with
// schnorr ParseSignature + sig.Verify(sighash, issuerPubkey) before it adds the
// policy signature and broadcasts. The browser primitive that produces that
// signature is signSighash (schnorr over the raw 32-byte digest), the ONE
// non-tagged signer, distinct from the tagged challenge/document signers whose
// domain tag is the signing-oracle guard. These tests pin that exactly: a clawback
// sighash signed in the browser must verify as openampd verifies it, and must be
// unusable as (and not confusable with) a tagged challenge/document signature.

// A stand-in for a real 32-byte L_claw taproot script-path spend sighash. openampd
// computes this with elements.TaprootSighash over the L_claw leaf; the browser
// treats it as an opaque digest to sign, exactly like the M5/M6 transfer sighash.
const CLAW_SIGHASH = 'a1b2c3d4e5f6071829303a4b5c6d7e8fa1b2c3d4e5f6071829303a4b5c6d7e8f'

test('M9 clawback: a browser sighash signature verifies exactly as openampd verifies it', () => {
  // The entity's SeqPal ID key is the enclave issuer half for a new asset.
  const { priv, xonly } = generateEnclaveKey()
  const sig = signSighash(priv, CLAW_SIGHASH)

  // openampd: schnorr.ParseSignature(sig) then sig.Verify(sighash, issuerPubkey).
  // The browser must sign the RAW sighash bytes (not a tagged hash), under the
  // issuer x-only, for the completion to be accepted and the sweep broadcast.
  assert.ok(
    schnorr.verify(hexToBytes(sig), hexToBytes(CLAW_SIGHASH), hexToBytes(xonly)),
    'the clawback sighash signature must verify under the issuer x-only over the raw digest'
  )
})

test('M9 clawback: the issuer key alone can complete (a different enclave key is rejected)', () => {
  // A clawback signature from any key other than the asset's issuer key must fail
  // verification, so a sweep cannot be completed without the genuine issuer.
  const issuer = generateEnclaveKey()
  const other = generateEnclaveKey()
  const sig = signSighash(other.priv, CLAW_SIGHASH)
  assert.ok(
    !schnorr.verify(hexToBytes(sig), hexToBytes(CLAW_SIGHASH), hexToBytes(issuer.xonly)),
    'a foreign key must not complete the issuer clawback'
  )
})

test('M9 clawback: the spend signer is NOT the tagged challenge/document signer (oracle guard holds)', () => {
  // The clawback spend signs the raw sighash; the challenge/document signers sign a
  // BIP340 tagged hash of their input. Signing the SAME 32 bytes both ways yields
  // different signatures, and neither verifies against the other's message, so a
  // clawback sighash can never be produced by tricking the tagged signers, and a
  // clawback signature can never stand in for a login/e-signature.
  const { priv, xonly } = generateEnclaveKey()

  const spendSig = signSighash(priv, CLAW_SIGHASH)
  const challengeSig = signChallenge(priv, CLAW_SIGHASH) // treats it as a challenge string
  const documentSig = signDocument(priv, CLAW_SIGHASH) // treats it as a doc content hash

  assert.notEqual(spendSig, challengeSig, 'the spend signer must differ from the challenge signer')
  assert.notEqual(spendSig, documentSig, 'the spend signer must differ from the document signer')

  // The spend signature verifies over the raw sighash only, never over the tagged
  // messages the challenge/document signers used.
  const challengeMsg = taggedHash('openamp-challenge-v1', new TextEncoder().encode(CLAW_SIGHASH))
  const documentMsg = taggedHash('openamp-document-v1', hexToBytes(CLAW_SIGHASH))
  assert.ok(
    !schnorr.verify(hexToBytes(spendSig), challengeMsg, hexToBytes(xonly)),
    'a clawback spend signature must not verify as a login challenge'
  )
  assert.ok(
    !schnorr.verify(hexToBytes(spendSig), documentMsg, hexToBytes(xonly)),
    'a clawback spend signature must not verify as a document e-signature'
  )
  // And a challenge signature over the same 32 bytes does NOT verify as the raw
  // spend openampd checks, so the tagged signers are not a clawback oracle.
  assert.ok(
    !schnorr.verify(hexToBytes(challengeSig), hexToBytes(CLAW_SIGHASH), hexToBytes(xonly)),
    'a tagged challenge signature must not authorize a raw clawback spend'
  )
})

test('M9 clawback: a tampered sighash invalidates the issuer signature', () => {
  // The sweep is bound to the exact sighash openampd built; any change to the
  // seizure transaction changes the sighash and voids the signature.
  const { priv, xonly } = generateEnclaveKey()
  const sig = signSighash(priv, CLAW_SIGHASH)
  const tampered = 'ff' + CLAW_SIGHASH.slice(2)
  assert.ok(
    !schnorr.verify(hexToBytes(sig), hexToBytes(tampered), hexToBytes(xonly)),
    'a signature over one L_claw sighash must not verify for another'
  )
})

test('M9 clawback: the raw-sighash signer refuses a non-32-byte digest', () => {
  // signSighash only signs a 32-byte digest; a wrong-length input is refused, so a
  // malformed to_sign entry cannot be blind-signed.
  const { priv } = generateEnclaveKey()
  assert.throws(() => signSighash(priv, 'deadbeef'), /32-byte digest/)
})

test('M9 clawback: input-index keying matches openampd (sigs keyed by decimal input index)', () => {
  // A multi-UTXO clawback returns one to_sign entry per enclave input; the browser
  // signs each sighash under the issuer key and posts a { "<input>": sig } map,
  // exactly the shape openampd's complete endpoint consumes. This pins the keying
  // so a two-input sweep is assembled correctly.
  const { priv, xonly } = generateEnclaveKey()
  const toSign = [
    { input: 0, sighash: CLAW_SIGHASH, pubkey: xonly },
    { input: 1, sighash: 'b'.repeat(64), pubkey: xonly },
  ]
  const sigs = {}
  for (const ts of toSign) {
    assert.equal(ts.pubkey, xonly, 'every to_sign entry must name the issuer key before signing')
    sigs[String(ts.input)] = signSighash(priv, ts.sighash)
  }
  assert.deepEqual(Object.keys(sigs).sort(), ['0', '1'])
  for (const ts of toSign) {
    assert.ok(
      schnorr.verify(hexToBytes(sigs[String(ts.input)]), hexToBytes(ts.sighash), hexToBytes(xonly)),
      `input ${ts.input} signature must verify under the issuer key`
    )
  }
  void bytesToHex
})
