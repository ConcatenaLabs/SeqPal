import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, dirname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const SRC = join(dirname(fileURLToPath(import.meta.url)), '..', 'src')
const CODE = new Set(['.js', '.jsx', '.mjs'])
function walkSrc(dir = SRC, out = []) {
  for (const entry of readdirSync(dir)) {
    const p = join(dir, entry)
    if (statSync(p).isDirectory()) walkSrc(p, out)
    else if (CODE.has(p.slice(p.lastIndexOf('.')))) out.push(p)
  }
  return out
}

import { computeSetupCost } from '../src/data/pricing.js'
import { STATUS, OFF_PLATFORM_STEPS, offPlatformSteps } from '../src/lib/lifecycle.js'
import { isEligible, tierFor } from '../src/lib/policy.js'
import {
  parseMoney,
  fmtAmount,
  ownershipDenominator,
  ownershipPct,
  escrowSettlementFee,
} from '../src/lib/economics.js'
import { slugify } from '../src/lib/util.js'
import { toTerms, view } from '../src/lib/issuance.js'
import { canonicalJSON, termsHash } from '../src/lib/openamp.js'
import {
  generateEnclaveKey,
  xonlyOf,
  taggedHash,
  signChallenge,
} from '../scripts/e2e/lib/wallet-signer.mjs'
import { computeAID, isAid, isXonly } from '../src/lib/statements.js'
import { schnorr } from '@noble/curves/secp256k1'
import { bytesToHex, hexToBytes } from '@noble/curves/abstract/utils'
import { sha256 } from '@noble/hashes/sha256'

// ── Conformance vectors ──────────────────────────────────────────────────
// The same fixed values seqpald asserts in seqpald/vectors_test.go. The AIDs are
// ground truth from openampd's own store.AID (openamp repo,
// openampd/internal/store/store.go): they are the one crypto seam between this
// browser code and the policy server, so they are pinned, not recomputed.
const VEC = {
  priv: 'b7e151628aed2a6abf7158809cf4f3c762e7160f38b4da56a784d9045190cfef',
  xonly: 'dff1d77f2a671c5f36183726db2341be58feae1da2deced843240f7b502ba659',
  aid: '51738ef8815e1590eba576eef1ac714f9e969d52',
  priv2: 'c90fdaa22168c234c4c6628b80dc1cd129024e088a67cc74020bbea63b14e5c9',
  xonly2: 'dd308afec5777e13121fa72b9cc1b7cc0139715309b086c960e18fd969774eb8',
  aid2: '29dae194e3b97260d4797713ab11f9a9f9b3e54d',
  aidSet: '309533c797997e92f0a73caa81093cab6192a17a',
  challenge: '2f0ee2f6a1cfbd0c4a3f1f8a7a4bbf6d5e2c19d84a0fbb1c7d6e5f4a3b2c1d0e',
  taggedDigest: 'c516dcf501fdf1271f950104a6663a53b303be25f604b601185521d150a54e25',
  challengeSig:
    '41c1aebb8689dba894f4cdc3d2c093929246f15a3e3ecfbc724f81997eb9411f' +
    'ab4cbae14aac412f1ff1c724d708ab4da2a336cc0717df13c9206cc02e04be5d',
  rawDigest: 'd84c1c8b63b45e8a91de7d506cdeda0656c4c166e6d1e5f832b899f2fcb66dff',
  termsHash: '995980cacb0fa2b0a2e27639d962f1666fab212c982895a5036a2741c3fdfe64',
  termsCanonical:
    '{"clawback":true,"jurisdiction":"HN-PROSPERA","note":"Terms & conditions <v1>",' +
    '"raise":{"amount":5000000,"unit":"USD"},"structure":"native-equity",' +
    '"transfer_restrictions":{"accredited_only":true,"blocked":["KP","IR"],"lockup_days":365}}',
}

const TERMS = {
  structure: 'native-equity',
  jurisdiction: 'HN-PROSPERA',
  transfer_restrictions: { lockup_days: 365, accredited_only: true, blocked: ['KP', 'IR'] },
  raise: { amount: 5000000, unit: 'USD' },
  clawback: true,
  note: 'Terms & conditions <v1>',
}

test('vector: AID matches openampd store.AID', () => {
  assert.equal(computeAID([VEC.xonly]), VEC.aid)
  assert.equal(computeAID([VEC.xonly2]), VEC.aid2)
  // The AID hashes the SORTED key set: presentation order cannot change it, and
  // a second key makes a different account.
  assert.equal(computeAID([VEC.xonly, VEC.xonly2]), VEC.aidSet)
  assert.equal(computeAID([VEC.xonly2, VEC.xonly]), VEC.aidSet)
  assert.notEqual(VEC.aidSet, VEC.aid)

  // The keys themselves are pinned too, so a change in the curve library that
  // moved the pubkey encoding could not silently move every AID.
  assert.equal(xonlyOf(VEC.priv), VEC.xonly)
  assert.equal(xonlyOf(VEC.priv2), VEC.xonly2)

  // And it is a real derivation, not a lookup.
  const k = generateEnclaveKey()
  assert.match(k.priv, /^[0-9a-f]{64}$/)
  assert.equal(xonlyOf(k.priv), k.xonly)
  assert.match(computeAID([k.xonly]), /^[0-9a-f]{40}$/)
  assert.equal(computeAID([k.xonly]), computeAID([k.xonly]))
})

test('vector: the login challenge is signed TAGGED, never raw', () => {
  const digest = taggedHash('openamp-challenge-v1', new TextEncoder().encode(VEC.challenge))
  assert.equal(bytesToHex(digest), VEC.taggedDigest)

  // signChallenge is deterministic (zero aux-rand), so the signature is a fixed
  // vector the Go verifier accepts (seqpald/vectors_test.go asserts the same
  // bytes).
  const sig = signChallenge(VEC.priv, VEC.challenge)
  assert.equal(sig, VEC.challengeSig)
  assert.ok(schnorr.verify(hexToBytes(sig), digest, hexToBytes(VEC.xonly)))

  // The anti-oracle property: what the enclave key signs is the tagged digest,
  // and that is NOT the raw hash of the challenge the server handed us. If these
  // two were ever equal, a hostile "challenge" could be a spendable sighash.
  const raw = sha256(new TextEncoder().encode(VEC.challenge))
  assert.equal(bytesToHex(raw), VEC.rawDigest)
  assert.notEqual(bytesToHex(raw), VEC.taggedDigest)
  assert.ok(!schnorr.verify(hexToBytes(sig), raw, hexToBytes(VEC.xonly)))

  // A different challenge, or a different key, produces a different signature.
  assert.notEqual(signChallenge(VEC.priv, VEC.challenge.slice(0, 63) + 'f'), sig)
  assert.notEqual(signChallenge(VEC.priv2, VEC.challenge), sig)
})

test('vector: terms_hash canonicalization ignores key order and whitespace', async () => {
  assert.equal(canonicalJSON(TERMS), VEC.termsCanonical)
  assert.equal(await termsHash(TERMS), VEC.termsHash)

  // Same data, every object's keys reordered and nested values shuffled: the
  // hash the browser cross-checks against seqpald must not move.
  const shuffled = {
    note: 'Terms & conditions <v1>',
    clawback: true,
    raise: { unit: 'USD', amount: 5000000 },
    transfer_restrictions: {
      blocked: ['KP', 'IR'],
      accredited_only: true,
      lockup_days: 365,
    },
    jurisdiction: 'HN-PROSPERA',
    structure: 'native-equity',
  }
  assert.equal(canonicalJSON(shuffled), VEC.termsCanonical)
  assert.equal(await termsHash(shuffled), VEC.termsHash)

  // Array order IS data (a blocked-jurisdiction list is ordered on the wire),
  // and any change of value changes the hash.
  const reversedArray = {
    ...TERMS,
    transfer_restrictions: { ...TERMS.transfer_restrictions, blocked: ['IR', 'KP'] },
  }
  assert.notEqual(await termsHash(reversedArray), VEC.termsHash)
  assert.notEqual(await termsHash({ ...TERMS, clawback: false }), VEC.termsHash)
})

// ── Encrypted key envelope ───────────────────────────────────────────────

// SeqPal is not a wallet: a SeqPal ID is the enclave key of a Sequentia wallet
// the holder already has, so the shipped application must contain no signer, no
// key generator and no key-at-rest format. This is the architectural rule that
// replaced the passphrase-encrypted key envelope, and it is worth a test because
// it is one convenient import away from being broken by accident.
test('the shipped app holds no key material and signs nothing itself', () => {
  const BANNED = [
    { name: 'schnorr.sign', re: /schnorr\s*\.\s*sign\b/ },
    { name: 'randomPrivateKey', re: /randomPrivateKey\b/ },
    { name: 'getPublicKey', re: /schnorr\s*\.\s*getPublicKey\b/ },
    { name: 'deriveKey / AES envelope', re: /\bderiveKey\b|AES-GCM/ },
    { name: 'privHex parameter', re: /\bprivHex\b/ },
  ]
  const offenders = []
  for (const file of walkSrc()) {
    const src = readFileSync(file, 'utf8')
    for (const b of BANNED) {
      if (b.re.test(src)) offenders.push(`${relative(SRC, file)}: ${b.name}`)
    }
  }
  assert.deepEqual(
    offenders,
    [],
    'the SPA must never sign or store key material; that belongs to the holder\'s wallet:\n  ' +
      offenders.join('\n  ')
  )
})

// ── Issuance record shape ────────────────────────────────────────────────

test('issuance — terms round-trip through the shape seqpald stores', () => {
  const draft = {
    structureId: 'native-equity',
    isPublic: true,
    unit: 'BTC',
    entityName: 'Aurora Holdings Ltd',
    raise: '5,000,000',
    fields: { premoney: '20,000,000' },
    policy: { US: 'restricted' },
    principal: { type: 'individual' },
    mintTarget: 'enclave',
  }
  const terms = toTerms(draft)
  assert.equal(terms.structure_id, 'native-equity')
  assert.equal(terms.is_public, true)
  assert.equal(terms.unit, 'BTC')

  // view() reads back what the server stored, and the chain fields come from the
  // server's record alone.
  const v = view({
    id: 'abc',
    owner_aid: VEC.aid,
    name: 'Aurora Ventures Fund I',
    ticker: 'AURA',
    structure_id: 'native-equity',
    status: 'live',
    terms,
    asset_id: 'aa'.repeat(32),
    txid: 'bb'.repeat(32),
  })
  assert.equal(v.structureId, 'native-equity')
  assert.equal(v.entityName, 'Aurora Holdings Ltd')
  assert.equal(v.unit, 'BTC')
  assert.equal(v.assetId, 'aa'.repeat(32))
  assert.equal(v.live, true)
  assert.equal(view({ id: 'x', status: 'draft', terms: {} }).live, false)
  assert.equal(view(null), null)
})

// ── Pricing, policy, economics, lifecycle ────────────────────────────────

test('computeSetupCost — Native Equity tiers, surcharge, secured, DR', () => {
  // standard private placement
  let c = computeSetupCost('native-equity', false, { raise: '5,000,000' })
  assert.equal(c.base, 12500)
  assert.equal(c.simple, false)
  assert.equal(c.total, 12500)

  // Simple Native Equity tier (raise <= $500K)
  c = computeSetupCost('native-equity', false, { raise: '400,000' })
  assert.equal(c.base, 7500)
  assert.equal(c.simple, true)

  // BTC-denominated raise: the $500K threshold is USD, so a 100-BTC raise (~$6M)
  // must NOT trigger the Simple tier just because 100 < 500000.
  c = computeSetupCost('native-equity', false, { raise: '₿100', unit: 'BTC' })
  assert.equal(c.simple, false)
  assert.equal(c.base, 12500)
  // a genuinely small BTC raise (~$317K) does qualify
  c = computeSetupCost('native-equity', false, { raise: '5', unit: 'BTC' })
  assert.equal(c.simple, true)
  assert.equal(c.base, 7500)

  // public offering surcharge
  c = computeSetupCost('native-equity', true, { raise: '5,000,000' })
  assert.equal(c.surcharge, 12500)
  assert.equal(c.total, 25000)

  // Equity SPV
  assert.equal(computeSetupCost('equity-spv', false, {}).base, 17500)

  // Debt unsecured vs secured add-on
  assert.equal(computeSetupCost('debt-yield', false, { collateral: 'Unsecured' }).secured, 0)
  const secured = computeSetupCost('debt-yield', false, { collateral: 'BTC multi-sig' })
  assert.equal(secured.secured, 10000)
  assert.equal(secured.total, 30000)

  // DR: always public but excluded from the public surcharge
  const dr = computeSetupCost('depository-receipt', true, {})
  assert.equal(dr.base, 22500)
  assert.equal(dr.surcharge, 0)
  assert.equal(dr.total, 22500)
})

test('lifecycle — only the two statuses seqpald actually records', () => {
  // The browser no longer advances an issuance through invented states: there is
  // draft, and there is what the chain says.
  assert.deepEqual(Object.keys(STATUS).sort(), ['draft', 'live'])
  assert.equal(STATUS.live.label, 'Deployed')

  // Off-platform preparation is a checklist, not progress, and DR adds custody.
  assert.equal(offPlatformSteps('native-equity').length, OFF_PLATFORM_STEPS.length)
  const dr = offPlatformSteps('depository-receipt')
  assert.equal(dr.length, OFF_PLATFORM_STEPS.length + 1)
  assert.ok(dr.some((s) => s.key === 'custody'))
})

test('policy.isEligible — standard / restricted / excluded / blocked', () => {
  const policy = { SV: 'standard', US: 'restricted', FR: 'excluded', KP: 'blocked' }
  assert.equal(isEligible(policy, 'SV', false), true) // standard admits regardless
  assert.equal(isEligible(policy, 'SV', true), true)
  assert.equal(isEligible(policy, 'US', true), true) // restricted needs accredited
  assert.equal(isEligible(policy, 'US', false), false)
  assert.equal(isEligible(policy, 'FR', true), false) // excluded
  assert.equal(isEligible(policy, 'KP', true), false) // blocked
  assert.equal(isEligible(policy, 'ZZ', true), false) // unknown jurisdiction
  assert.equal(isEligible(null, 'US', true), false)
  assert.equal(tierFor(policy, 'US'), 'restricted')
})

test('economics — money, post-money ownership, platform fee', () => {
  assert.equal(parseMoney('5,000,000'), 5000000)
  assert.equal(parseMoney('$1,250'), 1250)
  assert.equal(parseMoney(''), 0)
  assert.equal(parseMoney(undefined), 0)

  // Native Equity: investment / post-money (pre-money + raise)
  const fields = { premoney: '20,000,000' }
  assert.equal(ownershipDenominator('native-equity', fields, 5000000), 25000000)
  assert.equal(ownershipPct('native-equity', fields, 5000000, 250000).toFixed(2), '1.00')

  // Debt: investment / raise (no post-money)
  assert.equal(ownershipDenominator('debt-yield', {}, 5000000), 5000000)
  assert.equal(ownershipPct('debt-yield', {}, 5000000, 250000).toFixed(2), '5.00')

  // Escrow and settlement fee: 0.25%/mo on escrowed funds, accrued over the
  // holding period; $5K minimum; capped at 3% of funds held.
  assert.equal(escrowSettlementFee(5000000), 50000)
  assert.equal(escrowSettlementFee(750000), 7500)
  assert.equal(escrowSettlementFee(100000), 5000) // $5K minimum binds
  assert.equal(escrowSettlementFee(0), 5000)
  // long holding periods hit the 3% cap: 20 months on $1M accrues $50K, capped $30K
  assert.equal(escrowSettlementFee(1000000, 20), 30000)
  // BTC-denominated raises: the US$5,000 minimum is a dollar-equivalent and is
  // not netted against the ₿ amount.
  assert.equal(escrowSettlementFee(100, 4, 'BTC'), 1)
  assert.equal(escrowSettlementFee(100, 20, 'BTC'), 3)

  // unit-of-account formatting (USD default; BTC election)
  assert.equal(fmtAmount(7500), '$7,500')
  assert.equal(fmtAmount(7500, 'USD'), '$7,500')
  assert.equal(fmtAmount(12, 'BTC'), '₿12')
})

test('util — slugify', () => {
  assert.equal(slugify('Aurora Ventures Fund I'), 'aurora-ventures-fund-i')
  assert.equal(slugify('  Helvetia Digital AG!! '), 'helvetia-digital-ag')
  assert.equal(slugify('a'.repeat(40)).length, 24) // capped
  assert.equal(slugify(''), '')
})

// A holder pastes whatever their wallet shows them, and wallets show the
// account id far more prominently than the key it comes from. Both are
// accepted, and an account id is only trusted once the key returned for it
// re-derives to the same id.
test('a linked wallet is named by its account id or its account key', () => {
  const k = generateEnclaveKey()
  const aid = computeAID([k.xonly])

  assert.ok(isXonly(k.xonly), 'an account key is 64 hex')
  assert.ok(isAid(aid), 'an account id is 40 hex')
  assert.ok(!isAid(k.xonly), 'a key is not mistaken for an id')
  assert.ok(!isXonly(aid), 'an id is not mistaken for a key')

  // Case and surrounding whitespace are what a copy-paste actually delivers.
  assert.ok(isAid('  ' + aid.toUpperCase() + '  '))
  assert.ok(isXonly(' ' + k.xonly.toUpperCase() + ' '))

  // Neither shape: the form has to say so rather than sit disabled.
  for (const bad of ['', 'abc', aid.slice(0, 39), k.xonly.slice(0, 63), 'z'.repeat(40)]) {
    assert.ok(!isAid(bad) && !isXonly(bad), `refused: ${bad.slice(0, 12)}`)
  }

  // The check that makes accepting an account id safe: the key the policy
  // server returns must re-derive to the id that was pasted.
  const impostor = generateEnclaveKey()
  assert.notEqual(computeAID([impostor.xonly]), aid)
})
