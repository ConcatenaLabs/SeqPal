import { test } from 'node:test'
import assert from 'node:assert/strict'

import { computeSetupCost } from '../src/data/pricing.js'
import { completedCount, milestonesFor, nextStatus, MILESTONES } from '../src/lib/lifecycle.js'
import { isEligible, tierFor } from '../src/lib/policy.js'
import {
  parseMoney,
  fmtAmount,
  ownershipDenominator,
  ownershipPct,
  escrowSettlementFee,
} from '../src/lib/economics.js'
import {
  slugify,
  addBusinessDays,
  fakeGaid,
  fakeHex,
  fakeIdNumber,
} from '../src/lib/util.js'
import { ownsIssuance, ownedIssuances } from '../src/lib/account.js'

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

  // BTC-denominated raise: the $500K threshold is USD — a 100-BTC raise (~$6M)
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

test('lifecycle — completedCount, milestones, nextStatus', () => {
  assert.equal(completedCount('awaiting_incorporation'), 1)
  assert.equal(completedCount('deploying'), 2)
  assert.equal(completedCount('live', 'native-equity'), MILESTONES.length)

  // DR has an extra brokerage-custody milestone
  const drMs = milestonesFor('depository-receipt')
  assert.equal(drMs.length, MILESTONES.length + 1)
  assert.ok(drMs.some((m) => m.key === 'custody'))
  assert.equal(completedCount('live', 'depository-receipt'), drMs.length)

  assert.equal(nextStatus('awaiting_incorporation'), 'deploying')
  assert.equal(nextStatus('deploying'), 'live')
  assert.equal(nextStatus('live'), 'live')
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

  // Escrow & Settlement Fee (plan v0.72): 0.25%/mo on escrowed funds, accrued
  // over the holding period; $5K minimum; capped at 3% of funds held.
  // Plan's worked example: $5M raise over the typical ~4 months ≈ $50K (~1%).
  assert.equal(escrowSettlementFee(5000000), 50000)
  assert.equal(escrowSettlementFee(750000), 7500) // ≈1% typical window
  assert.equal(escrowSettlementFee(100000), 5000) // $5K minimum binds
  assert.equal(escrowSettlementFee(0), 5000)
  // long holding periods hit the 3% cap: 20 months on $1M accrues $50K → capped $30K
  assert.equal(escrowSettlementFee(1000000, 20), 30000)
  // BTC-denominated raises: the US$5,000 minimum is a dollar-equivalent and is
  // not netted against the ₿ amount — fee is the raw accrual within the cap.
  assert.equal(escrowSettlementFee(100, 4, 'BTC'), 1) // ₿1 ≈ 1% of ₿100
  assert.equal(escrowSettlementFee(100, 20, 'BTC'), 3) // capped at 3% = ₿3

  // unit-of-account formatting (USD default; BTC election)
  assert.equal(fmtAmount(7500), '$7,500')
  assert.equal(fmtAmount(7500, 'USD'), '$7,500')
  assert.equal(fmtAmount(12, 'BTC'), '₿12')
})

test('account — issuance ownership by SeqPal ID number', () => {
  const acct = {
    individual: { name: 'Issuer Ida', idNumber: 'SQID-I-AAA' },
    corporates: [{ entity: 'Acme Holdings Ltd', idNumber: 'SQID-C-XYZ' }],
  }
  const asIndividual = { principal: { type: 'individual', idNumber: 'SQID-I-AAA' } }
  const asCorporate = { principal: { type: 'corporate', idNumber: 'SQID-C-XYZ' } }
  const someoneElse = { principal: { type: 'individual', idNumber: 'SQID-I-BBB' } }

  assert.equal(ownsIssuance(acct, asIndividual), true)
  assert.equal(ownsIssuance(acct, asCorporate), true)
  assert.equal(ownsIssuance(acct, someoneElse), false)
  assert.equal(ownsIssuance(acct, { principal: {} }), false)
  assert.equal(ownsIssuance({ individual: null, corporates: [] }, asIndividual), false)
  assert.equal(ownsIssuance(null, asIndividual), false)

  assert.deepEqual(ownedIssuances(acct, [asIndividual, someoneElse, asCorporate]), [
    asIndividual,
    asCorporate,
  ])
  assert.deepEqual(ownedIssuances(acct, undefined), [])
})

test('util — slugify, addBusinessDays, id/hash formats', () => {
  assert.equal(slugify('Aurora Ventures Fund I'), 'aurora-ventures-fund-i')
  assert.equal(slugify('  Helvetia Digital AG!! '), 'helvetia-digital-ag')
  assert.equal(slugify('a'.repeat(40)).length, 24) // capped
  assert.equal(slugify(''), '')

  // Fri 2026-06-05 + 1 business day = Mon 2026-06-08 (skips the weekend)
  const fri = new Date('2026-06-05T12:00:00Z')
  const next = addBusinessDays(fri, 1)
  assert.equal(next.getUTCDay(), 1) // Monday
  // 5 business days never lands on a weekend
  const d = addBusinessDays(new Date('2026-06-01T12:00:00Z'), 5)
  assert.ok(d.getUTCDay() !== 0 && d.getUTCDay() !== 6)

  assert.equal(fakeHex(64).length, 64)
  assert.match(fakeHex(64), /^[0-9a-f]{64}$/)
  assert.equal(fakeGaid().length, 40)
  assert.match(fakeGaid(), /^[A-Za-z0-9]{40}$/)
  assert.match(fakeIdNumber('SQID-I'), /^SQID-I-[A-Z0-9]+-[A-Z0-9]+$/)
})
