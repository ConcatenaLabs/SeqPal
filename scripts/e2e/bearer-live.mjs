// Live driver: a freely-tradable (bearer, supervised) issuance through the
// REAL seqpald API path, signing exactly as the browser does. Run from the
// repo root: node scripts/e2e/bearer-live.mjs
// Env: SEQPAL_BASE (default https://sequentiatestnet.com/seqpal/api)
//
// The shared client, registration, and bearer-deploy flow live in
// lib/drill.mjs, which scripts/e2e/action-drill.mjs (the corporate-action
// drill) also uses. This driver keeps the freeze/unfreeze half.
import { signSupervisionMessage } from '../../src/lib/keys.js'
import { createHash, randomBytes } from 'node:crypto'
import {
  Client,
  must,
  sleep,
  signIn,
  verifyIdentity,
  deployBearer,
  generateEnclaveKey,
  generateRecoveryKey,
} from './lib/drill.mjs'

const client = new Client()
const call = (method, path, body) => client.call(method, path, body)

const key = generateEnclaveKey()
const recovery = generateRecoveryKey()
console.log('issuer xonly   ', key.xonly)
console.log('recovery xonly ', recovery.xonly)

// 1 + 2. Register a fresh SeqPal ID and verify it (Prospera resident, auto
// path, no sanctions hit).
const aid = await signIn(client, key, 'Bearer Live Proof')
console.log('aid            ', aid)
await verifyIdentity(client, { residence: 'HN-PRO', name: 'Bearer Live Proof Issuer' })

// 3-5. Draft, attest, and deploy as a freely-tradable supervised asset (the
// shared flow pays the setup fee over the simulated rail when it is due).
const { issId, deploy: d } = await deployBearer(client, key, recovery, aid, {
  name: 'Bearer Equity Live Proof',
  ticker: 'BL' + randomBytes(2).toString('hex').toUpperCase().slice(0, 4),
  supply: 1000000,
  precision: 0,
  pause: true,
})
console.log('issuance       ', issId)
console.log(JSON.stringify(d, null, 1))

// 6. Wait for the mint to confirm: the chain's supervision registry only
// learns the asset at its first confirmation, and a freeze cannot be built
// before that.
let known = null
for (let i = 0; i < 40; i++) {
  known = (await call('GET', `/issuances/${issId}/supervision`)).data
  if (known && known.supervised) break
  await sleep(4000)
}
console.log('supervised     ', known && known.supervised)

// 7. Court-order freeze drill: freeze the treasury address, verify the
// consensus register shows it, then lift it. The operational key is this
// session's own key by construction.
const target = d.treasury_address
const orderHash = createHash('sha256').update('SIMULATED court order: freeze ' + target).digest('hex')
let fzr = null
for (let i = 0; i < 40; i++) {
  fzr = await call('POST', `/issuances/${issId}/supervision/freeze`, {
    target_address: target,
    reason: 'live drill: simulated asset-freeze order',
    order_hash: orderHash,
  })
  if (fzr.status !== 409) break
  await sleep(4000)
}
const fz = must(fzr, 'freeze build')
console.log('freeze to_sign ', fz.to_sign, 'freezable:', fz.freezable)
const fsig = signSupervisionMessage(key.priv, fz.to_sign)
const fdone = must(
  await call('POST', `/issuances/${issId}/supervision/freeze/${fz.freeze_id}/complete`, { sig: fsig }),
  'freeze complete',
)
console.log('freeze txid    ', fdone.txid, 'channel:', fdone.channel)

// Wait for the record to confirm, then check the register.
let sup = null
for (let i = 0; i < 30; i++) {
  sup = (await call('GET', `/issuances/${issId}/supervision`)).data
  if (sup && (sup.freezes || []).length > 0) break
  await sleep(4000)
}
console.log('register       ', JSON.stringify(sup && sup.freezes))

if (sup && (sup.freezes || []).length > 0) {
  const uf = must(
    await call('POST', `/issuances/${issId}/supervision/unfreeze`, {
      target_address: target,
      reason: 'live drill: order lifted',
      order_hash: orderHash,
    }),
    'unfreeze build',
  )
  const usig = signSupervisionMessage(key.priv, uf.to_sign)
  const udone = must(
    await call('POST', `/issuances/${issId}/supervision/unfreeze/${uf.unfreeze_id}/complete`, { sig: usig }),
    'unfreeze complete',
  )
  console.log('unfreeze txid  ', udone.txid)
  for (let i = 0; i < 30; i++) {
    sup = (await call('GET', `/issuances/${issId}/supervision`)).data
    if (sup && (sup.freezes || []).length === 0) break
    await sleep(4000)
  }
  console.log('register after ', JSON.stringify(sup && sup.freezes))
}
