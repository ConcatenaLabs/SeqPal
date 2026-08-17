// Live driver: a freely-tradable (bearer, supervised) issuance through the
// REAL seqpald API path, signing exactly as the browser does. Run from the
// repo root: node scripts/e2e/bearer-live.mjs
// Env: SEQPAL_BASE (default https://sequentiatestnet.com/seqpal/api)
import {
  generateEnclaveKey,
  generateRecoveryKey,
  signChallenge,
  signBearerAttestation,
} from '../../src/lib/keys.js'

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
    ticker: 'BLPRF',
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
}
