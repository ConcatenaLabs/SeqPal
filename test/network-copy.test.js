// The two facts about a network-enforced token that an issuer must NOT discover
// at the moment a transfer fails:
//
//   1. a holder can combine at most TWO of their coins in one transfer, and that
//      bound is fixed when the token is created; and
//   2. a change to the holder list takes effect when the updated list is
//      PUBLISHED, not the moment the issuer makes it.
//
// Both follow from the mechanism rather than from anything this platform chose,
// which is exactly why they have to be stated on the surfaces where an issuer
// makes the choice and where they act on it. This test pins them there, in plain
// business language, and pins the protocol-level explanation to /docs, which is
// the only surface allowed to use protocol names.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const SRC = join(dirname(fileURLToPath(import.meta.url)), '..', 'src')
const read = (rel) => readFileSync(join(SRC, rel), 'utf8')

// Each assertion below matches a SENTENCE rather than a word, so a stray
// occurrence of "two" somewhere else on the page cannot satisfy it.
test('the wizard states the per-transfer coin bound where the issuer picks the model', () => {
  const src = read('pages/onboarding/Steps.jsx')
  const network = src.slice(src.indexOf("id: 'network'"), src.indexOf("id: 'bearer'"))
  assert.ok(network.length > 0, 'the network enforcement model card must exist')
  assert.match(
    network,
    /at most two of their coins of this token in a single transfer/i,
    'the network card must state that a holder can combine at most two of their coins in one transfer',
  )
  assert.match(
    network,
    /cannot be raised later/i,
    'it must say the bound is fixed at creation, because an issuer would otherwise plan around raising it',
  )
  assert.match(
    network,
    /take effect when the updated list is published/i,
    'the network card must say a rule change is not instant',
  )
})

test('the issuance detail states both limits for a live network-enforced token', () => {
  const src = read('pages/IssuanceDetail.jsx')
  assert.match(
    src,
    /at most two of their coins of this token in\s*\n?\s*a single transfer/i,
    'the network note on the issuance detail must state the per-transfer coin bound',
  )
  assert.match(
    src,
    /takes effect when the updated list is published/i,
    'the network note must say a policy change is not instant',
  )
})

test('the holder-list console states both limits, and asks for a reason and an order', () => {
  const src = read('components/PolicyConsole.jsx')
  assert.match(src, /at most\{' '\}/, 'the console must state the per-transfer coin bound')
  assert.match(src, /two of their coins/i)
  assert.match(src, /cannot be raised later/i)
  assert.match(
    src,
    /when the updated list is published/i,
    'the console must say a change is not instant',
  )
  // Same court-order discipline as the freely-tradable console: the order
  // document is hashed in the browser and only the fingerprint leaves it.
  assert.match(src, /crypto\.subtle\.digest\('SHA-256'/, 'the order document is hashed in the browser')
  assert.match(src, /only this fingerprint is/i, 'only the fingerprint is sent and published')
  assert.match(src, /A reason is required/i, 'a change without a reason is refused in the browser too')
})

test('the console speaks plain business language: no protocol names on it', () => {
  // It is not a wizard flow surface, so the copy gate does not cover it, but the
  // same rule applies: an issuer acting on a court order should not have to read
  // about covenants to do it.
  const src = read('components/PolicyConsole.jsx')
  for (const [name, re] of [
    ['OpenDAMP', /open\s?damp/i],
    ['covenant', /covenant/i],
    ['enclave', /enclave/i],
    ['whitelist', /whitelist/i],
    ['blacklist', /blacklist/i],
    ['pi', /\bpolicy commitment\b/i],
  ]) {
    assert.ok(!re.test(src), `the holder-list console must not say "${name}"`)
  }
})

test('/docs carries the protocol-level explanation, including what is not enforced', () => {
  const src = read('pages/Docs.jsx')
  assert.match(src, /OpenDAMP/, 'the docs surface names the protocol')
  assert.match(src, /C_V\(pi\)/, 'the docs explain the verifier covenant')
  assert.match(src, /send_after/, 'the docs explain the per-holder height bounds in the leaf')
  assert.match(
    src,
    /at most 4 inputs and 6 outputs/i,
    'the docs give the real bound the two-coin limit follows from',
  )
  assert.match(
    src,
    /at most TWO coins/i,
    'the docs state the same limit the issuer surfaces state in plain language',
  )
  assert.match(
    src,
    /issuer path G\(I\)/,
    'the docs explain that a policy update is a publication plus a respend',
  )
  assert.match(
    src,
    /STATUS\.md section 2/,
    'the docs point at the authoritative list of what is NOT enforced rather than implying completeness',
  )
})

test('the holder-list console takes a signature the browser could not make', () => {
  // The key that authorises a change is the holding key the token was issued
  // at, and for an issuer whose SeqPal ID is a wallet that key is not in any
  // browser. Requiring a connected wallet to sign made the console unusable for
  // exactly the issuers who can deploy this kind of token.
  const src = read('components/PolicyConsole.jsx')
  assert.match(src, /setPaste\(true\)/, 'no wallet here to sign means offering the paste path')
  assert.match(src, /complete\(undefined, sigPaste\)/, 'a pasted signature publishes the change')
  assert.ok(
    !/disabled=\{[^}]*!hasKey/.test(src),
    'no button on this console is disabled merely because no wallet is connected',
  )
})

test('the election says a network-enforced token needs its own software to move', () => {
  const src = read('pages/onboarding/Steps.jsx')
  assert.match(
    src,
    /does not sit in an ordinary wallet balance/,
    'the election states that no ordinary wallet sends this token',
  )
})

test('a surface says WHICH wallet it needs, or that one declined', () => {
  // "Connect your Sequentia wallet" is wrong twice over: on a surface a holder
  // reached by proving a wallet, and on a path where a wallet IS connected and
  // simply returned nothing. Where an extension is genuinely required -- an
  // enclave spend, whose signer must verify the transaction it signs -- the copy
  // names it; everywhere else the holder is told their wallet declined.
  for (const f of [
    'components/TransferConsole.jsx',
    'components/FreezeClawbackConsole.jsx',
    'components/SupervisionConsole.jsx',
    'components/PayoutMandateCard.jsx',
    'components/ClosingCard.jsx',
    'components/ListingCard.jsx',
    'components/MarketAbuseGate.jsx',
    'components/PolicyConsole.jsx',
    'components/DataRoom.jsx',
    'pages/ActionClaim.jsx',
    'components/InvestorMandateCard.jsx',
    'pages/onboarding/Steps.jsx',
  ]) {
    const src = read(f)
    assert.ok(
      !/Connect your Sequentia wallet to sign/.test(src),
      `${f} still tells a holder who has a wallet to connect a wallet`,
    )
  }
})

test('the listing authorization can be signed by a wallet that is not in the browser', () => {
  const src = read('components/ListingCard.jsx')
  assert.match(src, /setPrep\(\{ sign_this_message/, 'no wallet here means showing what to sign')
  assert.match(src, /OfflineSignature/, 'and taking the signature back as a paste')
})

test('the election says the deploy takes a registrar the issuer runs', () => {
  // Three values a network-enforced deploy needs cannot be computed by the
  // issuer or by SeqPal; they come from a registrar the issuer runs, between
  // two halves of the deploy. The handoff panel explains it well -- but it
  // appears at the moment of deploying, which is the worst place to learn that
  // you need a tool you do not have.
  const src = read('pages/onboarding/Steps.jsx')
  assert.match(src, /Issuing it takes two rounds/, 'the election names the two-round deploy')
  assert.match(src, /registrar is yours to run/, 'and says whose tool it is')
})

test('the front page does not promise one enforcement model for all three', () => {
  // Eligibility is checked on every transfer for a SERVICED token. A
  // network-enforced one is checked against a published holder list; a
  // freely-tradable one is not checked at all. Promising the first of the three
  // as though it were all of them contradicts the next sentence on the same
  // card, which offers the other two.
  const src = read('pages/Home.jsx')
  assert.ok(
    !/Investor eligibility is checked on every transfer:/.test(src),
    'the compliance card does not claim every transfer is checked',
  )
  assert.ok(
    !/it whitelists the holder for every SeqPal-managed asset/.test(src),
    'verification does not whitelist a holder for a network-enforced asset: they ask, and the issuer decides',
  )
})
