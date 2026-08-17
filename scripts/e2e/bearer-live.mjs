// Live driver: a freely-tradable (bearer, supervised) issuance through the
// REAL seqpald API path, signing exactly as the browser does. Run from the
// repo root: node scripts/e2e/bearer-live.mjs
// Env: SEQPAL_BASE (default https://sequentiatestnet.com/seqpal/api)
import {
  generateEnclaveKey,
  generateRecoveryKey,
  signChallenge,
  signBearerAttestation,
  signSupervisionMessage,
} from '../../src/lib/keys.js'
import { createHash, randomBytes } from 'node:crypto'

const BASE = process.env.SEQPAL_BASE || 'https://sequentiatestnet.com/seqpal/api'
let cookie = ''

async function call(method, path, body) {
  const res = await fetch(BASE + path, {
    method,
    headers: { 'content-type': 'application/json', ...(cookie ? { cookie } : {}) },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const setc = res.headers.get('set-cookie')
  if (setc) cookie = setc.split(';')[0]
  let data = null
  try {
    data = await res.json()
  } catch {
    /* empty body */
  }
  return { status: res.status, data }
}

function must(r, what, okStatuses = [200]) {
  if (!okStatuses.includes(r.status)) {
    console.error(`FAIL ${what}: HTTP ${r.status}`, JSON.stringify(r.data))
    process.exit(1)
  }
  console.log(`ok   ${what}${r.data && r.data.error ? ' ' + r.data.error : ''}`)
  return r.data
}

const key = generateEnclaveKey()
const recovery = generateRecoveryKey()
console.log('issuer xonly   ', key.xonly)
console.log('recovery xonly ', recovery.xonly)

// 1. Register a fresh SeqPal ID.
const ch = must(await call('POST', '/auth/challenge', { xonly: key.xonly }), 'challenge')
const sig = signChallenge(key.priv, ch.challenge)
const reg = must(
  await call('POST', '/auth/register', {
    xonly: key.xonly,
    challenge: ch.challenge,
    sig,
    name: 'Bearer Live Proof',
  }),
  'register',
)
const me = must(await call('GET', '/me'), 'me')
const aid = me.aid || (me.account && me.account.aid) || reg.aid
console.log('aid            ', aid)

// 2. Verify the identity (Prospera resident, auto path, no sanctions hit).
must(
  await call('POST', '/id/verify', {
    residence: 'HN-PRO',
    base_eligibility: 'ret',
    screening_name: 'Bearer Live Proof Issuer',
  }),
  'id verify',
)

// 3. Create the issuance draft.
const iss = must(
  await call('POST', '/issuances', {
    name: 'Bearer Equity Live Proof',
    ticker: 'BL' + randomBytes(2).toString('hex').toUpperCase().slice(0, 4),
    structure_id: 'native-equity',
  }),
  'create issuance',
)
const issId = iss.id || (iss.issuance && iss.issuance.id)
console.log('issuance       ', issId)

// 4. Bearer attestation, signed with the session key.
const fields = { issuance_id: issId, no_us_nexus: true, risk_accepted: true, aid }
const attSig = signBearerAttestation(key.priv, fields)
must(
  await call('POST', `/issuances/${issId}/bearer-attestation`, {
    ...fields,
    statement_sig: attSig,
  }),
  'bearer attestation',
)

// 5. Deploy as a freely-tradable supervised asset.
const dep = await call('POST', '/deploy', {
  issuance_id: issId,
  supply: 1000000,
  precision: 0,
  enforcement: 'bearer',
  clawback: false,
  recovery_pubkey: recovery.xonly,
  pause: true,
  fee_convert_atoms: 0,
  terms: { structure: 'native-equity' },
})
if (dep.status === 402) {
  console.log('setup fee due, paying via the simulated rail')
  must(await call('POST', `/issuances/${issId}/fees/pay`, { rail: 'card' }), 'pay setup fee', [200, 202])
  const dep2 = must(await call('POST', '/deploy', {
    issuance_id: issId,
    supply: 1000000,
    precision: 0,
    enforcement: 'bearer',
    clawback: false,
    recovery_pubkey: recovery.xonly,
    pause: true,
    fee_convert_atoms: 0,
    terms: { structure: 'native-equity' },
  }), 'deploy (after fee)')
  console.log(JSON.stringify(dep2, null, 1))
} else {
  const d = must(dep, 'deploy')
  console.log(JSON.stringify(d, null, 1))

  // 6. Wait for the mint to confirm: the chain's supervision registry only
  // learns the asset at its first confirmation, and a freeze cannot be built
  // before that.
  let known = null
  for (let i = 0; i < 40; i++) {
    known = (await call('GET', `/issuances/${issId}/supervision`)).data
    if (known && known.supervised) break
    await new Promise((r) => setTimeout(r, 4000))
  }
  console.log('supervised     ', known && known.supervised)

  // 7. Court-order freeze drill: freeze the treasury address, verify the
  // consensus register shows it, then lift it. The operational key is this
  // session's own key by construction.
  const target = d.treasury_address
  const orderHash = createHash('sha256').update('SIMULATED court order: freeze ' + target).digest('hex')
  const fz = must(
    await call('POST', `/issuances/${issId}/supervision/freeze`, {
      target_address: target,
      reason: 'live drill: simulated asset-freeze order',
      order_hash: orderHash,
    }),
    'freeze build',
  )
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
    await new Promise((r) => setTimeout(r, 4000))
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
      await new Promise((r) => setTimeout(r, 4000))
    }
    console.log('register after ', JSON.stringify(sup && sup.freezes))
  }
}
