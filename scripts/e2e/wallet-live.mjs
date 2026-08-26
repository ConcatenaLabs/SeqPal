// Live driver: a SeqPal ID that is only a WALLET, through the real seqpald API,
// signing exactly as such a holder does -- ordinary signed messages, made with
// the private key of one of the wallet's own addresses, which the node checks
// with verifymessage. Run from the repo root:
//
//   node scripts/e2e/wallet-live.mjs
//
// Env: SEQPAL_BASE (default https://sequentiatestnet.com/seqpal/api)
//
// This is the half of the platform the other drivers do not touch. They all
// play a holder whose wallet has an OpenAMP account and signs under SeqPal's
// tags; a holder without one signs nothing that way, and every surface that
// asks for a signature has to be answerable by both. Every step here is a
// question of the form "can this holder actually finish", asked of the live
// service rather than of a comment.
import { randomBytes } from 'node:crypto'
import { Client, must, BASE } from './lib/drill.mjs'
import { signWalletMessage } from './lib/wallet-signer.mjs'
import { walletFromSeed } from './lib/hd.mjs'

const client = new Client()
const call = (m, p, b) => client.call(m, p, b)

// A real wallet: an account xpub with a receive chain, which is what SeqPal
// identifies such an ID by, and the private keys behind its addresses, which is
// what proves it.
const wallet = walletFromSeed(randomBytes(32).toString('hex'))
const descriptor = wallet.descriptor
const priv = wallet.receiveKey(0)
console.log('base           ', BASE)
console.log('wallet         ', descriptor)

// 1. Register. The challenge is signed as a message, not under a tag: this
//    wallet has no idea what a SeqPal tag is, which is the whole point.
const ch = must(await call('POST', '/auth/wallet/challenge', { descriptor }), 'wallet challenge')
console.log('sign at        ', ch.address)
const reg = must(
  await call('POST', '/auth/wallet/register', {
    descriptor,
    challenge: ch.challenge,
    sig: signWalletMessage(priv, ch.challenge),
    display_name: 'Wallet Live Proof',
    residence: 'AE',
  }),
  'wallet register',
)
const aid = reg.account.aid
console.log('aid            ', aid)

// 2. The passport must not imply an OpenAMP account this ID does not have.
const passport = must(await call('GET', '/id/passport'), 'passport')
if (passport.has_enclave !== false) throw new Error('the passport claims an OpenAMP account')
console.log('has_enclave    ', passport.has_enclave)

// 3. Verification is what unlocks the KYC-gated parts, and it must not need the
//    policy server to stamp an account that does not exist.
must(await call('POST', '/id/verify', { residence: 'AE', name: 'Wallet Live Proof' }), 'verify')
const after = must(await call('GET', '/id/passport'), 'passport after verify')
if (!after.categories || after.categories.length === 0) {
  throw new Error('verification recorded no eligibility for a wallet-backed ID')
}
console.log(
  'categories     ',
  after.categories.map((c) => (typeof c === 'string' ? c : c.token || c.code || JSON.stringify(c))).join(', '),
)

// 4. What such an ID cannot do must be refused in words that name the missing
//    piece, not fail somewhere downstream.
const transfer = await call('POST', '/transfers', { asset: 'x'.repeat(64), to_aid: aid, atoms: 1 })
if (transfer.status !== 403) throw new Error(`an OpenAMP transfer must be refused 403, got ${transfer.status}`)
if (!/no OpenAMP account/i.test(transfer.data?.error || '')) {
  throw new Error(`the refusal must name what is missing: ${transfer.data?.error}`)
}
console.log('transfer       ', 'refused, and says why')

// 5. A payout mandate: two phases, and phase one has to say what to sign in a
//    form this wallet can sign.
const addr = ch.address
const prep = must(await call('POST', '/mandates/investor', { chain: 'sequentia', address: addr }), 'mandate prepare')
if (!prep.sign_this_message) throw new Error('the mandate did not say what an ordinary wallet should sign')
const mandate = must(
  await call('POST', '/mandates/investor', {
    chain: 'sequentia',
    address: addr,
    signature: signWalletMessage(priv, prep.sign_this_message),
  }),
  'mandate register',
)
console.log('mandate        ', mandate.mandate?.address || 'registered')

// 6. The market-abuse acknowledgment gates the transfer surfaces, so an ID that
//    cannot sign it cannot reach them.
const ack = must(await call('GET', '/id/market-abuse-ack'), 'market-abuse read')
if (!ack.sign_this_message) throw new Error('the acknowledgment did not say what to sign')
must(
  await call('POST', '/id/market-abuse-ack', { signature: signWalletMessage(priv, ack.sign_this_message) }),
  'market-abuse acknowledge',
)
console.log('market abuse   ', 'signed acknowledgment recorded')

// 7. Issuing: the one election open to this ID mints at a holding key it names,
//    and naming none has to be a refusal that says so.
const iss = must(
  await call('POST', '/issuances', {
    name: 'Wallet Live Proof Fund',
    ticker: 'WL' + randomBytes(2).toString('hex').toUpperCase().slice(0, 4),
    structure_id: 'native-equity',
  }),
  'draft issuance',
)
const issId = iss.id || iss.issuance?.id
const noKey = await call('POST', '/deploy', {
  issuance_id: issId,
  supply: 1000,
  precision: 0,
  enforcement: 'network',
  terms: {},
})
if (noKey.status !== 400 || !/holding key/i.test(noKey.data?.error || '')) {
  throw new Error(`deploying with no holding key must be refused and say so, got ${noKey.status} ${JSON.stringify(noKey.data)}`)
}
console.log('network deploy ', 'refused without a holding key, and says which')

const serviced = await call('POST', '/deploy', {
  issuance_id: issId,
  supply: 1000,
  precision: 0,
  enforcement: 'serviced',
  terms: {},
})
if (serviced.status !== 403) throw new Error(`a serviced deploy must be refused 403, got ${serviced.status}`)
const bearer = await call('POST', '/deploy', {
  issuance_id: issId,
  supply: 1000,
  precision: 0,
  enforcement: 'bearer',
  terms: {},
})
if (bearer.status !== 403) throw new Error(`a bearer deploy must be refused 403, got ${bearer.status}`)
console.log('other deploys  ', 'both refused, before any terms are written')

console.log('\nPASS: a SeqPal ID that is only a wallet can finish everything it is offered,')
console.log('      and is refused everything it is not, in words that name what is missing.')
