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
import { computeAID, signChallenge, signWalletMessage, xonlyOf } from './lib/wallet-signer.mjs'
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

// 8. One person, two wallets, one identity. Signing in with either has to land
//    in the same SeqPal ID, or a holder who owns a web wallet and an extension
//    is two people to this platform and verified as neither.
const second = walletFromSeed(randomBytes(32).toString('hex'))
const linkChallenge = must(
  await call('POST', '/account/wallets', { descriptor: second.descriptor, label: 'second wallet' }),
  'link challenge',
)
must(
  await call('POST', '/account/wallets', {
    descriptor: second.descriptor,
    label: 'second wallet',
    challenge: linkChallenge.challenge,
    sig: signWalletMessage(second.receiveKey(0), linkChallenge.challenge),
  }),
  'link second wallet',
)
const wallets = must(await call('GET', '/account/wallets'), 'list wallets')
if ((wallets.wallets || []).length !== 2) {
  throw new Error(`expected two wallets on this ID, got ${(wallets.wallets || []).length}`)
}

const other = new Client()
const otherChallenge = must(
  await other.call('POST', '/auth/wallet/challenge', { descriptor: second.descriptor }),
  'second wallet challenge',
)
const login = must(
  await other.call('POST', '/auth/wallet/login', {
    descriptor: second.descriptor,
    challenge: otherChallenge.challenge,
    sig: signWalletMessage(second.receiveKey(0), otherChallenge.challenge),
  }),
  'sign in with the second wallet',
)
if (login.account?.aid !== aid) {
  throw new Error(`the second wallet signed in as a different identity: ${login.account?.aid}`)
}
console.log('second wallet  ', 'linked, and signs in as the same identity')

// 9. One wallet, two descriptor forms. A wallet shows its holder the wpkh
//    addresses it hands out; the node checks a signed message against the pkh
//    address of the same key. Both name the same wallet, and both have to reach
//    the same identity -- otherwise registering under one form and signing in
//    under the other quietly makes a second person out of one.
const twoForms = walletFromSeed(randomBytes(32).toString('hex'))
const asWpkh = twoForms.descriptor.replace(/^pkh\(/, 'wpkh(')
const formClient = new Client()
const formChallenge = must(
  await formClient.call('POST', '/auth/wallet/challenge', { descriptor: asWpkh }),
  'challenge in the wpkh form',
)
// What the holder is asked to sign with must be an address their wallet shows
// them. A wpkh wallet shows tb1 addresses and has never displayed a legacy one.
if (!/^(tb1|ert1)/.test(formChallenge.address)) {
  throw new Error(`a wpkh wallet was asked to sign with ${formChallenge.address}`)
}
const formAccount = must(
  await formClient.call('POST', '/auth/wallet/register', {
    descriptor: asWpkh,
    challenge: formChallenge.challenge,
    sig: signWalletMessage(twoForms.receiveKey(0), formChallenge.challenge),
    display_name: 'Two Forms',
    residence: 'AE',
  }),
  'register in the wpkh form',
)
const backAsPkh = new Client()
const pkhChallenge = must(
  await backAsPkh.call('POST', '/auth/wallet/challenge', { descriptor: twoForms.descriptor }),
  'challenge in the pkh form',
)
const pkhLogin = must(
  await backAsPkh.call('POST', '/auth/wallet/login', {
    descriptor: twoForms.descriptor,
    challenge: pkhChallenge.challenge,
    sig: signWalletMessage(twoForms.receiveKey(0), pkhChallenge.challenge),
  }),
  'sign in in the pkh form',
)
if (pkhLogin.account?.aid !== formAccount.account.aid) {
  throw new Error('the same wallet in its other form reached a different identity')
}
console.log('both forms     ', 'one wallet, one identity, and the address shown is one it displays')

// 10. Attaching an OpenAMP account to an ID that started as a wallet. The
//     credential it already earned has to survive that, and the two account ids
//     it now has are different strings: the SeqPal one it was founded with, and
//     the one the policy server derives from the enclave key. Everything asked
//     of the policy server about this holder has to name the second.
const enclavePriv = randomBytes(32).toString('hex')
const enclaveXonly = xonlyOf(enclavePriv)
const derivedAID = computeAID([enclaveXonly])
const enclaveChallenge = must(
  await call('POST', '/auth/challenge', { xonly: enclaveXonly }),
  'enclave challenge',
)
must(
  await call('POST', '/auth/attach-enclave', {
    xonly: enclaveXonly,
    challenge: enclaveChallenge.challenge,
    sig: signChallenge(enclavePriv, enclaveChallenge.challenge),
  }),
  'attach an OpenAMP account',
)
const attachedPassport = must(await call('GET', '/id/passport'), 'passport after attaching')
if (!attachedPassport.has_enclave) {
  throw new Error('the OpenAMP account did not attach')
}
if ((attachedPassport.categories || []).length !== (after.categories || []).length) {
  throw new Error(
    `attaching an OpenAMP account lost the eligibility this ID already had: ` +
      `${(after.categories || []).length} -> ${(attachedPassport.categories || []).length}`,
  )
}
if (attachedPassport.enclave_aid !== derivedAID) {
  throw new Error(`the passport reports OpenAMP account ${attachedPassport.enclave_aid}, want ${derivedAID}`)
}
if (attachedPassport.aid !== aid) {
  throw new Error('the SeqPal account id changed when an OpenAMP account was attached')
}
console.log('both ids       ', 'attached, credential kept, and each id reported as itself')

console.log('\nPASS: a SeqPal ID that is only a wallet can finish everything it is offered,')
console.log('      and is refused everything it is not, in words that name what is missing.')
