// Shared helpers for the bearer live drivers (bearer-live.mjs and
// action-drill.mjs): the cookie-holding API client, identity registration and
// verification, the bearer attestation + deploy flow, electrs reads, and the
// persisted drill state. Everything signs through wallet-signer.mjs, which plays
// the holder's wallet over the message constructions the SPA ships.
import { readFileSync, writeFileSync, chmodSync, existsSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { schnorr } from '@noble/curves/secp256k1'
import { sha256 } from '@noble/hashes/sha256'
import { bytesToHex, hexToBytes } from '@noble/curves/abstract/utils'
import {
  generateEnclaveKey,
  generateRecoveryKey,
  signChallenge,
  signBearerAttestation,
  taggedHash,
} from './wallet-signer.mjs'
import { HOLDING_PROOF_TAG } from '../../../src/lib/statements.js'

export const BASE = process.env.SEQPAL_BASE || 'https://sequentiatestnet.com/seqpal/api'
// The public electrs (esplora) REST base on the box; /blocks/tip/height,
// /address/{addr}/utxo, /tx/{txid} all live here.
export const ELECTRS = process.env.SEQPAL_ELECTRS || 'https://sequentiatestnet.com/api'

// One authenticated API client per identity: seqpald sessions ride an HttpOnly
// cookie, and the drill runs an issuer session and a holder session side by
// side, so each keeps its own jar.
export class Client {
  constructor(base = BASE) {
    this.base = base
    this.cookie = ''
  }

  async call(method, path, body) {
    const res = await fetch(this.base + path, {
      method,
      headers: { 'content-type': 'application/json', ...(this.cookie ? { cookie: this.cookie } : {}) },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    const setc = res.headers.get('set-cookie')
    if (setc) this.cookie = setc.split(';')[0]
    let data = null
    try {
      data = await res.json()
    } catch {
      /* empty body */
    }
    return { status: res.status, data }
  }
}

export function must(r, what, okStatuses = [200]) {
  if (!okStatuses.includes(r.status)) {
    console.error(`FAIL ${what}: HTTP ${r.status}`, JSON.stringify(r.data))
    process.exit(1)
  }
  console.log(`ok   ${what}${r.data && r.data.error ? ' ' + r.data.error : ''}`)
  return r.data
}

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// Poll fn() until it returns a truthy value or the timeout passes.
export async function waitFor(label, fn, { timeoutMs = 600000, intervalMs = 5000 } = {}) {
  const deadline = Date.now() + timeoutMs
  for (;;) {
    const v = await fn()
    if (v) return v
    if (Date.now() > deadline) {
      console.error(`FAIL ${label}: timed out after ${timeoutMs / 1000}s`)
      process.exit(1)
    }
    await sleep(intervalMs)
  }
}

// Register a fresh SeqPal ID (or sign back in with a persisted key) and return
// the session-holding client plus the account id.
export async function signIn(client, key, name) {
  const ch = must(await client.call('POST', '/auth/challenge', { xonly: key.xonly }), `challenge (${name})`)
  const sig = signChallenge(key.priv, ch.challenge)
  const reg = await client.call('POST', '/auth/register', {
    xonly: key.xonly,
    challenge: ch.challenge,
    sig,
    display_name: name,
  })
  if (reg.status === 409) {
    // Already registered (a resumed drill): sign in instead with a new challenge.
    const ch2 = must(await client.call('POST', '/auth/challenge', { xonly: key.xonly }), `challenge 2 (${name})`)
    must(
      await client.call('POST', '/auth/login', {
        xonly: key.xonly,
        challenge: ch2.challenge,
        sig: signChallenge(key.priv, ch2.challenge),
      }),
      `login (${name})`,
    )
  } else {
    must(reg, `register (${name})`)
  }
  const me = must(await client.call('GET', '/me'), `me (${name})`)
  return me.aid || (me.account && me.account.aid)
}

// Verify the identity through the auto path (no sanctions hit, deterministic
// simulated review approval). Idempotent: re-verifying refreshes the record.
export async function verifyIdentity(client, { residence, name }) {
  must(
    await client.call('POST', '/id/verify', {
      residence,
      base_eligibility: 'ret',
      screening_name: name,
    }),
    `id verify (${name}, ${residence})`,
  )
}

// The bearer attestation + deploy flow, as the browser runs it. Returns the
// deploy response (asset, txid, treasury_address, supervision block).
export async function deployBearer(client, key, recovery, aid, { name, ticker, supply, precision, pause }) {
  const iss = must(
    await client.call('POST', '/issuances', {
      name,
      ticker,
      structure_id: 'native-equity',
    }),
    'create issuance',
  )
  const issId = iss.id || (iss.issuance && iss.issuance.id)

  const fields = { issuance_id: issId, no_us_nexus: true, risk_accepted: true, aid }
  const attSig = signBearerAttestation(key.priv, fields)
  must(
    await client.call('POST', `/issuances/${issId}/bearer-attestation`, { ...fields, statement_sig: attSig }),
    'bearer attestation',
  )

  const body = {
    issuance_id: issId,
    supply,
    precision,
    enforcement: 'bearer',
    clawback: false,
    recovery_pubkey: recovery.xonly,
    pause,
    fee_convert_atoms: 0,
    terms: { structure: 'native-equity', enforcement: 'bearer' },
  }
  let dep = await client.call('POST', '/deploy', body)
  if (dep.status === 402) {
    console.log('setup fee due, paying via the simulated rail')
    must(await client.call('POST', `/issuances/${issId}/fees/pay`, { rail: 'card' }), 'pay setup fee', [200, 202])
    dep = await client.call('POST', '/deploy', body)
  }
  const d = must(dep, 'bearer deploy')
  return { issId, deploy: d }
}

export { generateEnclaveKey, generateRecoveryKey }

// The holding-proof signer over the server's exact `sign_this` bytes: BIP340
// tagged (HOLDING_PROOF_TAG) over sha256 of the canonical claim statement.
// This mirrors seqpald's verifyTaggedByKey construction, and signing the
// server's canonical bytes equals the SPA's field-based signHoldingProof for
// the same claim (pinned in test/enforcement.test.js).
const utf8 = new TextEncoder()
const NO_AUX = new Uint8Array(32)
export function signHoldingStatement(privHex, signThis) {
  const msg = taggedHash(HOLDING_PROOF_TAG, sha256(utf8.encode(signThis)))
  return bytesToHex(schnorr.sign(msg, hexToBytes(privHex), NO_AUX))
}

// ── electrs reads (the public box explorer API) ─────────────────────────────

export async function electrs(path) {
  const res = await fetch(ELECTRS + path)
  const text = await res.text()
  if (!res.ok) throw new Error(`electrs ${path}: HTTP ${res.status} ${text.slice(0, 120)}`)
  try {
    return JSON.parse(text)
  } catch {
    return text.trim()
  }
}

export async function tipHeight() {
  return Number(await electrs('/blocks/tip/height'))
}

// The confirmed UTXOs of an asset at an address: [{ outpoint, atoms }].
export async function assetUtxos(address, asset) {
  const utxos = await electrs(`/address/${address}/utxo`)
  return utxos
    .filter((u) => u.asset === asset && u.status && u.status.confirmed)
    .map((u) => ({ outpoint: `${u.txid}:${u.vout}`, atoms: Number(u.value), txid: u.txid, vout: u.vout }))
}

// ── amounts and operator commands ───────────────────────────────────────────

// Elements RPC amounts are always denominated in 1e8 atoms regardless of an
// asset's display precision; format atoms as an 8dp amount string.
export function amount8(atoms) {
  const a = BigInt(atoms)
  const whole = a / 100000000n
  const frac = (a % 100000000n).toString().padStart(8, '0')
  return `${whole}.${frac}`
}

// The exact node command the operator runs to send an asset from the
// seqpal-escrow wallet. sendtoaddress positional parameters, from the node's
// src/wallet/rpc/spend.cpp (Sequentia repo):
//   0 address  1 amount  2 comment  3 comment_to  4 subtractfeefromamount
//   5 replaceable  6 conf_target  7 estimate_mode  8 avoid_reuse
//   9 assetlabel (hex asset id)  10 ignoreblindfail  11 fee_rate
//   12 fee_asset_label  13 verbose
// The asset rides in position 9 (assetlabel); positions 6 and 7 are passed as
// null to take the wallet defaults, and 8 (avoid_reuse) as false.
export function sendCommand(address, atoms, asset) {
  // The box's non-interactive PATH does not carry sequentia-cli, so name it by
  // path (SEQPAL_DRILL_CLI overrides) and pass the chain explicitly. The RPC
  // credentials are never in the repo: set SEQPAL_DRILL_CLI with the box's
  // -rpcuser/-rpcpassword, or rely on the node's cookie file.
  const cli = process.env.SEQPAL_DRILL_CLI || '/root/Sequentia/src/sequentia-cli -chain=test -rpcport=18200'
  // Verified positional order from src/wallet/rpc/spend.cpp sendtoaddress:
  // address, amount, comment, comment_to, subtractfeefromamount, replaceable,
  // conf_target, estimate_mode (a STRING, "unset", never null), avoid_reuse,
  // assetlabel, ignoreblindfail, fee_rate, fee_asset_label. The open fee market
  // has no default fee asset, so fee_asset_label is mandatory here.
  const feeAsset = process.env.SEQPAL_DRILL_FEE_ASSET || 'bitcoin'
  return `${cli} -rpcwallet=seqpal-escrow sendtoaddress ${address} ${amount8(atoms)} "" "" false false null unset false ${asset} false null ${feeAsset}`
}

// Run the operator send over ssh when SEQPAL_DRILL_SSH names the box's ssh
// host (e.g. "seq"); otherwise print the command for the operator and return
// null so the caller waits for the funds to appear on electrs.
export function operatorSend(address, atoms, asset, label) {
  const host = process.env.SEQPAL_DRILL_SSH
  const cmd = sendCommand(address, atoms, asset)
  if (!host) {
    console.log(`ACTION REQUIRED (${label}): run on the box:`)
    console.log(`  ${cmd}`)
    return null
  }
  console.log(`running over ssh ${host}: ${cmd}`)
  const out = execFileSync('ssh', [host, cmd], { encoding: 'utf8' }).trim()
  const txid = out.split('\n').pop().trim()
  console.log(`ok   ${label} sent, txid ${txid}`)
  return txid
}

// ── persisted drill state ───────────────────────────────────────────────────
//
// scripts/e2e/.drill-state.json holds the generated identities (PRIVATE KEYS),
// the issuance and asset ids, and per-step results so a rerun resumes instead
// of repeating chain writes. It is gitignored and written 0600: testnet-only
// keys, but hygiene is hygiene.

export function loadState(path) {
  if (!existsSync(path)) return { steps: {} }
  const st = JSON.parse(readFileSync(path, 'utf8'))
  if (!st.steps) st.steps = {}
  return st
}

export function saveState(path, state) {
  writeFileSync(path, JSON.stringify(state, null, 2) + '\n', { mode: 0o600 })
  chmodSync(path, 0o600)
}
