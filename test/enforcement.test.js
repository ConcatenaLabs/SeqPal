import { test } from 'node:test'
import assert from 'node:assert/strict'

import {
  generateEnclaveKey,
  generateRecoveryKey,
  taggedHash,
  encryptKey,
  decryptKey,
  BEARER_ATTESTATION_TAG,
  HOLDING_PROOF_TAG,
  signBearerAttestation,
  signHoldingProof,
  signSupervisionMessage,
  signChallenge,
} from '../src/lib/keys.js'
import { canonicalJSON } from '../src/lib/openamp.js'
import { toTerms, view } from '../src/lib/issuance.js'
import { schnorr } from '@noble/curves/secp256k1'
import { sha256 } from '@noble/hashes/sha256'
import { hexToBytes } from '@noble/curves/abstract/utils'

// The enforcement-election browser primitives: the bearer attestation and the
// corporate-action holding proof are TAGGED signatures over sha256 of a
// canonical JSON object (so seqpald can rebuild the exact message from the
// posted fields), and the supervision freeze signer is a deliberate raw-digest
// signer with the same guards as the clawback signer. These tests pin the
// constructions so the server side has fixed vectors to verify against.

const enc = new TextEncoder()
const verifyCanonical = (sigHex, tag, obj, xonly) => {
  const msg = taggedHash(tag, sha256(enc.encode(canonicalJSON(obj))))
  return schnorr.verify(hexToBytes(sigHex), msg, hexToBytes(xonly))
}

test('bearer attestation: tagged signature over sha256 of the canonical JSON', () => {
  const { priv, xonly } = generateEnclaveKey()
  const fields = { issuance_id: 'iss_1', no_us_nexus: true, risk_accepted: true, aid: 'a'.repeat(40) }
  const sig = signBearerAttestation(priv, fields)
  assert.ok(
    verifyCanonical(sig, BEARER_ATTESTATION_TAG, fields, xonly),
    'the attestation must verify over taggedHash(tag, sha256(canonicalJSON(fields)))'
  )
  // Key order in the input object must not matter: canonical JSON sorts keys.
  const reordered = { aid: fields.aid, risk_accepted: true, no_us_nexus: true, issuance_id: 'iss_1' }
  assert.equal(signBearerAttestation(priv, reordered), sig, 'canonicalization must ignore key order')
  // Flipping either statement invalidates the signature.
  assert.ok(
    !verifyCanonical(sig, BEARER_ATTESTATION_TAG, { ...fields, no_us_nexus: false }, xonly),
    'a flipped attestation checkbox must invalidate the signature'
  )
})

test('bearer attestation: NOT a raw digest signer, and domain-separated from other tags', () => {
  const { priv, xonly } = generateEnclaveKey()
  const fields = { issuance_id: 'iss_1', no_us_nexus: true, risk_accepted: true, aid: 'a'.repeat(40) }
  const sig = signBearerAttestation(priv, fields)
  const rawDigest = sha256(enc.encode(canonicalJSON({
    aid: fields.aid, issuance_id: fields.issuance_id, no_us_nexus: true, risk_accepted: true,
  })))
  assert.ok(
    !schnorr.verify(hexToBytes(sig), rawDigest, hexToBytes(xonly)),
    'the attestation signature must never verify over the raw digest (oracle guard)'
  )
  assert.ok(
    !verifyCanonical(sig, HOLDING_PROOF_TAG, fields, xonly),
    'an attestation signature must not verify under the holding-proof tag'
  )
})

test('holding proof: mirrors the server statement and binds every claim field', () => {
  const { priv, xonly } = generateEnclaveKey()
  const outpoints = ['b'.repeat(64) + ':1', 'a'.repeat(64) + ':0'] // deliberately unsorted
  const base = {
    action_id: 'act_1',
    asset: 'c'.repeat(64),
    record_height: 97000,
    outpoints,
    aid: 'd'.repeat(40),
  }
  const dividend = { ...base, purpose: 'dividend', payout_address: 'tex1qexample' }
  const sig = signHoldingProof(priv, dividend)
  // The signed statement is seqpald's holdingProofStatement byte-for-byte: v,
  // action_id, asset, record_height (a number), the SORTED outpoints, purpose,
  // aid, and the payout address for a dividend. The claimant key is bound by
  // script derivation, not by the message.
  const serverStatement = {
    v: 1,
    action_id: 'act_1',
    asset: 'c'.repeat(64),
    record_height: 97000,
    outpoints: [...outpoints].sort(),
    purpose: 'dividend',
    aid: 'd'.repeat(40),
    payout_address: 'tex1qexample',
  }
  assert.ok(
    verifyCanonical(sig, HOLDING_PROOF_TAG, serverStatement, xonly),
    'a dividend claim must verify over the exact server-side statement'
  )
  // Outpoint order in the input must not matter: the signer sorts.
  assert.equal(sig, signHoldingProof(priv, { ...dividend, outpoints: [...outpoints].sort() }))
  // A different payout address, outpoint set, height, or session is a
  // different message.
  assert.notEqual(sig, signHoldingProof(priv, { ...dividend, payout_address: 'tex1qother' }))
  assert.notEqual(sig, signHoldingProof(priv, { ...dividend, outpoints: outpoints.slice(0, 1) }))
  assert.notEqual(sig, signHoldingProof(priv, { ...dividend, record_height: 97001 }))
  assert.notEqual(sig, signHoldingProof(priv, { ...dividend, aid: 'e'.repeat(40) }))
  const vote = { ...base, purpose: 'vote', choice: 'For' }
  assert.notEqual(sig, signHoldingProof(priv, vote), 'a vote and a dividend claim never share a message')
  const voteSig = signHoldingProof(priv, vote)
  assert.ok(
    verifyCanonical(
      voteSig,
      HOLDING_PROOF_TAG,
      {
        v: 1,
        action_id: 'act_1',
        asset: 'c'.repeat(64),
        record_height: 97000,
        outpoints: [...outpoints].sort(),
        purpose: 'vote',
        aid: 'd'.repeat(40),
        choice: 'For',
      },
      xonly
    ),
    'a vote claim binds the ballot choice in place of the payout address'
  )
})

test('supervision: raw 32-byte signer with the clawback-style guards', () => {
  const { priv, xonly } = generateEnclaveKey()
  const message = 'c'.repeat(64)
  const sig = signSupervisionMessage(priv, message)
  // The chain verifies the raw digest under the issuer key.
  assert.ok(schnorr.verify(hexToBytes(sig), hexToBytes(message), hexToBytes(xonly)))
  // Wrong-length digests are refused, so a malformed to_sign cannot be blind-signed.
  assert.throws(() => signSupervisionMessage(priv, 'deadbeef'), /32-byte digest/)
  // The tagged signers can never produce a supervision authorization.
  const challengeSig = signChallenge(priv, message)
  assert.ok(
    !schnorr.verify(hexToBytes(challengeSig), hexToBytes(message), hexToBytes(xonly)),
    'a tagged challenge signature must not authorize a freeze'
  )
})

test('recovery key: an independent keypair with the same envelope round-trip', async () => {
  const key = generateRecoveryKey()
  const main = generateEnclaveKey()
  assert.notEqual(key.xonly, main.xonly, 'the recovery key must be independent of the main key')
  const envelope = await encryptKey(key.priv, 'a strong passphrase 1234')
  envelope.xonly = key.xonly
  assert.ok(envelope.ct && envelope.salt && envelope.iv, 'the backup is the AES-GCM envelope, never plaintext')
  assert.equal(await decryptKey(envelope, 'a strong passphrase 1234'), key.priv)
  await assert.rejects(() => decryptKey(envelope, 'wrong'), /Wrong passphrase/)
})

// ── the live-drill drivers' primitives (scripts/e2e) ────────────────────────
//
// The action drill derives the holder's P2TR key-path address and an ordinary
// P2WPKH payout address with its own bech32/bech32m encoder (the repo carries
// no bech32 dependency), and signs the server's exact holding-proof statement
// bytes. Both are pinned here: the encoder against address vectors taken from
// the Sequentia node's own src/test/data/key_io_valid.json (chain "test"), and
// the statement signer against the SPA's field-based signHoldingProof.

test('drill bech32m: the P2TR key-path address matches the node key_io vector', async () => {
  const { p2trAddress, decodeSegwit } = await import('../scripts/e2e/lib/bech32.mjs')
  // Sequentia src/test/data/key_io_valid.json, chain "test":
  //   tb1ph9v3e8nxct57hknlkhkz75p5pnxnkn05cw8ewpxu6tek56g29xgqydzfu7
  //   = 5120 b9591c9e66c2e9ebda7fb5ec2f50340ccd3b4df4c38f9704dcd2f36a690a2990
  const xonly = 'b9591c9e66c2e9ebda7fb5ec2f50340ccd3b4df4c38f9704dcd2f36a690a2990'
  assert.equal(
    p2trAddress(xonly, 'tb'),
    'tb1ph9v3e8nxct57hknlkhkz75p5pnxnkn05cw8ewpxu6tek56g29xgqydzfu7',
    'the bech32m encoding must match what the node itself decodes'
  )
  // Round trip: the address decodes back to witness v1 over exactly the key,
  // i.e. the 5120||xonly script seqpald's claim derivation expects.
  const dec = decodeSegwit(p2trAddress(xonly, 'tb'))
  assert.equal(dec.hrp, 'tb')
  assert.equal(dec.version, 1)
  assert.equal(dec.program, xonly)
  // A corrupted character must fail the checksum.
  assert.throws(() => decodeSegwit('tb1ph9v3e8nxct57hknlkhkz75p5pnxnkn05cw8ewpxu6tek56g29xgqydzfu8'))
})

test('drill bech32: the P2WPKH payout address matches the node key_io vector and its claim script', async () => {
  const { encodeSegwit, decodeSegwit, p2wpkhAddressFromPriv, compressedPubkey, hash160 } = await import(
    '../scripts/e2e/lib/bech32.mjs'
  )
  const { bytesToHex } = await import('@noble/curves/abstract/utils')
  // Sequentia src/test/data/key_io_valid.json, chain "test":
  //   tb1q74fxwnvhsue0l8wremgq66xzvn48jlc5zthsvz = 0014 f552674d...797f14
  assert.equal(
    encodeSegwit('tb', 0, 'f552674d978732ff9dc3ced00d68c264ea797f14'),
    'tb1q74fxwnvhsue0l8wremgq66xzvn48jlc5zthsvz',
    'the bech32 (v0) encoding must match what the node itself decodes'
  )
  // The payout address derives from the REAL compressed key: its program is
  // hash160(compressed), which is exactly the 0014||hash160 script seqpald's
  // scriptForClaimKey derives for a 66-hex claim key.
  const { priv } = generateEnclaveKey()
  const compressed = compressedPubkey(priv)
  const dec = decodeSegwit(p2wpkhAddressFromPriv(priv, 'tb'))
  assert.equal(dec.version, 0)
  assert.equal(dec.program, bytesToHex(hash160(hexToBytes(compressed))))
})

test('drill holding-proof signer: signing the server statement bytes equals the SPA field signer', async () => {
  const { signHoldingStatement } = await import('../scripts/e2e/lib/drill.mjs')
  const { priv, xonly } = generateEnclaveKey()
  const fields = {
    action_id: 'act_drill',
    asset: 'c'.repeat(64),
    record_height: 97600,
    outpoints: ['b'.repeat(64) + ':1', 'a'.repeat(64) + ':0'],
    purpose: 'dividend',
    payout_address: 'tb1q74fxwnvhsue0l8wremgq66xzvn48jlc5zthsvz',
    aid: 'd'.repeat(40),
  }
  // The server's sign_this bytes are the canonical JSON of its statement
  // (sorted keys, sorted outpoints), which the driver signs verbatim in the
  // two-phase claim. That signature must be byte-identical to the SPA's
  // field-based signer for the same claim, so seqpald verifies both alike.
  const serverStatement = canonicalJSON({
    v: 1,
    action_id: fields.action_id,
    asset: fields.asset,
    record_height: fields.record_height,
    outpoints: [...fields.outpoints].sort(),
    purpose: 'dividend',
    aid: fields.aid,
    payout_address: fields.payout_address,
  })
  assert.equal(signHoldingStatement(priv, serverStatement), signHoldingProof(priv, fields))
  // And the vote shape too.
  const voteFields = { ...fields, purpose: 'vote', choice: 'for' }
  delete voteFields.payout_address
  const voteStatement = canonicalJSON({
    v: 1,
    action_id: fields.action_id,
    asset: fields.asset,
    record_height: fields.record_height,
    outpoints: [...fields.outpoints].sort(),
    purpose: 'vote',
    aid: fields.aid,
    choice: 'for',
  })
  assert.equal(signHoldingStatement(priv, voteStatement), signHoldingProof(priv, voteFields))
  // Sanity: it stays a tagged signer, never a raw digest oracle.
  const raw = sha256(enc.encode(serverStatement))
  assert.ok(!schnorr.verify(hexToBytes(signHoldingStatement(priv, serverStatement)), raw, hexToBytes(xonly)))
})

test('terms: the enforcement election is committed, and bearer terms carry no transfer rules', () => {
  const base = {
    structureId: 'native-equity',
    isPublic: false,
    policy: { DE: 'standard' },
    lockup: { mode: 'days', days: '30' },
    holderCap: '100',
    euCaps: { DE: 149 },
    regS: { enabled: true, prefix: 'j:US', mode: 'days', days: '40' },
  }
  const serviced = toTerms({ ...base, enforcement: 'serviced' })
  assert.equal(serviced.enforcement, 'serviced')
  assert.equal(serviced.lockup_days, 30)
  assert.equal(serviced.holder_cap, 100)
  assert.ok(serviced.jurisdictions.DE)

  const dflt = toTerms(base)
  assert.equal(dflt.enforcement, 'serviced', 'serviced is the default election')

  const bearer = toTerms({ ...base, enforcement: 'bearer' })
  assert.equal(bearer.enforcement, 'bearer')
  assert.equal(bearer.lockup_days, undefined, 'a bearer asset has no lockup')
  assert.equal(bearer.holder_cap, undefined, 'a bearer asset has no holder cap')
  assert.equal(bearer.reg_s, undefined, 'a bearer asset has no holding-period window')
  assert.equal(bearer.eu_caps, undefined, 'a bearer asset has no per-country caps')
  assert.deepEqual(bearer.jurisdictions, {}, 'a bearer asset admits everyone')
  assert.deepEqual(bearer.policy, {}, 'a bearer asset commits no category matrix')
})

test('terms: a network election is committed and keeps the full rule matrix', () => {
  // A network-enforced offering still commits the same compliance terms a
  // serviced one does. What changes is WHO enforces them, not what they are, so
  // dropping the matrix here would silently weaken the committed offering.
  const network = toTerms({
    structureId: 'native-equity',
    isPublic: false,
    enforcement: 'network',
    policy: { DE: 'standard' },
    lockup: { mode: 'days', days: '30' },
    holderCap: '100',
  })
  assert.equal(network.enforcement, 'network')
  assert.equal(network.lockup_days, 30)
  assert.equal(network.holder_cap, 100)
  assert.ok(network.jurisdictions.DE)
})

test('view: a network-enforced issuance surfaces its on-chain addresses, others surface none', () => {
  const net = view({
    status: 'live',
    enforcement: 'network',
    terms: { enforcement: 'network' },
    asset_id: 'a'.repeat(64),
    holder_covenant_address: 'tb1pholder',
    verifier_covenant_address: 'tb1prules',
    policy_commitment: 'b'.repeat(64),
    whitelist_root: 'c'.repeat(64),
  })
  assert.equal(net.enforcement, 'network')
  assert.equal(net.holderAddress, 'tb1pholder')
  assert.equal(net.rulesAddress, 'tb1prules')
  assert.equal(net.policyCommitment, 'b'.repeat(64))
  assert.equal(net.holderListRoot, 'c'.repeat(64))

  const serviced = view({ status: 'live', terms: {}, enclave_address: 'tb1penclave' })
  assert.equal(serviced.enforcement, 'serviced')
  assert.equal(serviced.holderAddress, '', 'a serviced issuance has no on-chain rules address')
  assert.equal(serviced.rulesAddress, '')
  assert.equal(serviced.policyCommitment, '')
  assert.equal(serviced.enclaveAddress, 'tb1penclave')
})
