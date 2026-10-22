// The fourth enforcement election: a token whose holdings may also sit in the
// issuer's tree. What an issuer and a holder must read where the choice is made
// and where it acts: the same rules apply in the tree, the holder can always
// take its tokens out to its own account on the chain, and the issuer's powers
// there, in plain words. And the protocol that runs the tree is named on no
// page at all.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, dirname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { toTerms, view } from '../src/lib/issuance.js'

const SRC = join(dirname(fileURLToPath(import.meta.url)), '..', 'src')
const read = (rel) => readFileSync(join(SRC, rel), 'utf8')

test('the wizard offers the tree as a fourth model and states what it adds and what it costs', () => {
  const src = read('pages/onboarding/Steps.jsx')
  const card = src.slice(src.indexOf("id: 'tree'"), src.indexOf("id: 'network'"))
  assert.ok(card.length > 0, 'the tree enforcement model card must exist, before the network card')
  assert.match(card, /settles in an instant after the same check against your rules/i)
  assert.match(card, /always take their tokens out of the tree into their own account on the chain, without your approval/i)
  assert.match(card, /if you keep the clawback option, take back a holding once it is on the chain/i)
  assert.match(card, /You cannot move anyone.s holding without them/i)
  assert.match(src, /m\.id === 'tree' && treeAvailable === false/, 'the card cannot be picked on a deployment without a tree')
})

test('the issuance page shows the election and the issuer powers for a tree token', () => {
  const page = read('pages/IssuanceDetail.jsx')
  assert.match(page, /iss\.enforcement === 'tree' && <TreeCard iss=\{iss\} \/>/)
  assert.match(page, /instant transfers in your tree/)
  const card = read('components/TreeCard.jsx')
  assert.match(card, /always take their tokens out to their own account on the chain, without your approval/i)
  assert.match(card, /You kept the clawback option: you can take back a holding once it is on the chain/)
  assert.match(card, /You gave up the clawback option: nobody can take a holding back/)
  assert.match(card, /You cannot move anyone&rsquo;s holding without them/)
  assert.match(card, /Turn on instant transfers/, 'a tree the deploy could not turn on is asked for again here')
})

test('the investor portal tells a holder their way out of the tree', () => {
  const src = read('pages/portal/InvestorPortal.jsx')
  assert.match(src, /offering\.enforcement === 'tree'/)
  assert.match(src, /always take\s+your tokens out to your own account on the chain, without the issuer&rsquo;s\s+approval/)
  assert.match(src, /it cannot move your holding without you/)
})

function walk(dir) {
  const out = []
  for (const entry of readdirSync(dir)) {
    const p = join(dir, entry)
    if (statSync(p).isDirectory()) out.push(...walk(p))
    else if (/\.(js|jsx|mjs|ts|tsx|html|css|md)$/.test(p)) out.push(p)
  }
  return out
}

test('no page names the protocol that runs the issuer tree', () => {
  const bad = []
  for (const f of walk(SRC)) {
    const lines = readFileSync(f, 'utf8').split('\n')
    lines.forEach((l, i) => {
      if (/\bArca\b/i.test(l) || /\bArk\b/.test(l)) bad.push(`${relative(SRC, f)}:${i + 1}: ${l.trim()}`)
    })
  }
  assert.deepEqual(bad, [], 'public pages say "the issuer\'s tree" and never name the protocol')
})

test('terms: a tree election is committed and keeps the full rule matrix', () => {
  const tree = toTerms({
    structureId: 'native-equity',
    isPublic: false,
    enforcement: 'tree',
    policy: { DE: 'standard' },
    lockup: { mode: 'days', days: '30' },
    holderCap: '100',
  })
  assert.equal(tree.enforcement, 'tree')
  assert.equal(tree.lockup_days, 30)
  assert.equal(tree.holder_cap, 100)
  assert.ok(tree.jurisdictions.DE)
})

test('view: a tree issuance surfaces its operator once its tree is on', () => {
  const on = view({ status: 'live', enforcement: 'tree', terms: { enforcement: 'tree' }, tree_operator: '1'.repeat(64) })
  assert.equal(on.enforcement, 'tree')
  assert.equal(on.treeOperator, '1'.repeat(64))
  const off = view({ status: 'live', enforcement: 'tree', terms: { enforcement: 'tree' } })
  assert.equal(off.treeOperator, '')
})
