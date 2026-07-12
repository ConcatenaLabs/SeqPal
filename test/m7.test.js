import { test } from 'node:test'
import assert from 'node:assert/strict'

import { canonicalJSON } from '../src/lib/openamp.js'
import { xonlyOf, taggedHash, signMandate, MANDATE_TAG } from '../src/lib/keys.js'
import { schnorr } from '@noble/curves/secp256k1'
import { bytesToHex, hexToBytes } from '@noble/curves/abstract/utils'

// M7 (transfer-agent servicing) browser-side conformance for the INVESTOR payout
// mandate signing seam. seqpald returns the exact canonical `sign_this` bytes for
// an investor mandate (mandate_investor.go investorMandateStatement:
// {address, asset, chain, investor_aid, role} with role="investor"), and the
// browser signs those bytes under the shared tag seqpal-payout-mandate-v1. This
// mirrors the server's verifyTaggedByKey(signer, tag, statement, sig): the browser
// signature must verify against taggedHash(tag, statement) for the caller's own
// x-only key. If either the tag or the canonical bytes drift, a real investor
// mandate would be rejected at registration and the holder could not be paid.

const VEC = {
  priv: 'b7e151628aed2a6abf7158809cf4f3c762e7160f38b4da56a784d9045190cfef',
  investorAID: '51738ef8815e1590eba576eef1ac714f9e969d52',
}

test('M7 investor mandate: tag matches the server constant', () => {
  // The browser tag must equal seqpald's platform.go mandateTag; a drift here
  // silently breaks every mandate signature.
  assert.equal(MANDATE_TAG, 'seqpal-payout-mandate-v1')
})

test('M7 investor mandate: browser signs the exact canonical statement seqpald verifies', () => {
  const xonly = xonlyOf(VEC.priv)
  // The exact statement seqpald builds (canonicalJSON sorts keys, no whitespace).
  const statement = canonicalJSON({
    role: 'investor',
    investor_aid: VEC.investorAID,
    chain: 'sequentia',
    asset: '',
    address: 'seq-ordinary-investor',
  })
  // Canonical form is sorted-key, whitespace-free: what the server hashes.
  assert.equal(
    statement,
    '{"address":"seq-ordinary-investor","asset":"","chain":"sequentia","investor_aid":"' +
      VEC.investorAID +
      '","role":"investor"}'
  )

  const sig = signMandate(VEC.priv, statement)
  // The message the server verifies: taggedHash(MANDATE_TAG, statement bytes).
  const digest = taggedHash(MANDATE_TAG, new TextEncoder().encode(statement))
  assert.ok(
    schnorr.verify(hexToBytes(sig), digest, hexToBytes(xonly)),
    'mandate signature must verify under the payout-mandate tag for the signer key'
  )
})

test('M7 investor mandate: role discriminator stops issuer-mandate signature replay', () => {
  // The investor statement carries role:"investor"; an issuer-side mandate over
  // the same tag omits it, so the two messages (and their signatures) never
  // collide. A signature made over one must NOT verify against the other's digest.
  const investorStmt = canonicalJSON({
    role: 'investor',
    investor_aid: VEC.investorAID,
    chain: 'sequentia',
    asset: '',
    address: 'seq-addr',
  })
  const issuerStmt = canonicalJSON({ chain: 'sequentia', address: 'seq-addr' })
  assert.notEqual(investorStmt, issuerStmt)

  const sig = signMandate(VEC.priv, investorStmt)
  const issuerDigest = taggedHash(MANDATE_TAG, new TextEncoder().encode(issuerStmt))
  assert.ok(
    !schnorr.verify(hexToBytes(sig), issuerDigest, hexToBytes(xonlyOf(VEC.priv))),
    'an investor-mandate signature must not verify as an issuer mandate'
  )
})

test('M7 investor mandate: a tampered address invalidates the signature', () => {
  const xonly = xonlyOf(VEC.priv)
  const statement = canonicalJSON({
    role: 'investor',
    investor_aid: VEC.investorAID,
    chain: 'sequentia',
    asset: '',
    address: 'seq-original',
  })
  const sig = signMandate(VEC.priv, statement)
  const tampered = canonicalJSON({
    role: 'investor',
    investor_aid: VEC.investorAID,
    chain: 'sequentia',
    asset: '',
    address: 'seq-attacker',
  })
  const digest = taggedHash(MANDATE_TAG, new TextEncoder().encode(tampered))
  assert.ok(
    !schnorr.verify(hexToBytes(sig), digest, hexToBytes(xonly)),
    'a signature over one payout address must not verify for another'
  )
})
