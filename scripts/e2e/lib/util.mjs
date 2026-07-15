// Shared helpers for the acceptance driver: structured step logging, assertions
// that print the on-chain evidence they check, and the privileged funding hook.
import { spawn } from 'node:child_process'
import { createInterface } from 'node:readline'
import { randomBytes } from 'node:crypto'

let stepN = 0

export function banner(title) {
  process.stdout.write(`\n${'='.repeat(72)}\n${title}\n${'='.repeat(72)}\n`)
}

export function step(name) {
  stepN += 1
  process.stdout.write(`\n[${String(stepN).padStart(2, '0')}] ${name}\n`)
}

export function info(msg) {
  process.stdout.write(`     ${msg}\n`)
}

// Print a labelled piece of evidence (a txid, an address, an assertion result).
export function evidence(label, value) {
  process.stdout.write(`     ${label}: ${value}\n`)
}

export function ok(msg) {
  process.stdout.write(`     PASS  ${msg}\n`)
}

export function skip(msg) {
  process.stdout.write(`     SKIP  ${msg}\n`)
}

export class AssertError extends Error {}

export function assert(cond, msg) {
  if (!cond) throw new AssertError(msg)
  ok(msg)
}

// A synthetic 32-byte sighash, hex, for --dry-run signing proofs (keys.signSighash
// requires a real 32-byte digest). Never used against the live chain.
export function fakeSighash() {
  return randomBytes(32).toString('hex')
}

// The privileged funding hook. Sending USDX to a deposit address (or topping up a
// servicing wallet with tSEQ for fees) is a box operation with box-held keys, so
// the driver NEVER holds those credentials. Three honest modes:
//   --fund-cmd "<cmd>"  : the operator supplied a command; run it with the deposit
//                         details in the environment (FUND_ADDRESS/AMOUNT/CCY).
//   interactive (TTY)   : print the deposit address + amount and wait for the
//                         operator to fund it out of band, then press Enter.
//   dry-run / no TTY     : print what WOULD need funding and continue (no wait).
export async function fund({ address, amount, ccy, label, fundCmd, dryRun }) {
  info(`FUNDING REQUIRED (${label})`)
  evidence('deposit address', address)
  evidence('amount', `${amount} ${ccy}`)
  if (dryRun) {
    skip('dry-run: not funding; the live run pauses here for the operator')
    return
  }
  if (fundCmd) {
    info(`running --fund-cmd (box-side; the driver never sees the keys)`)
    await runShell(fundCmd, { FUND_ADDRESS: address, FUND_AMOUNT: String(amount), FUND_CCY: ccy })
    return
  }
  if (process.stdin.isTTY) {
    await prompt(`     fund the address above, then press Enter to continue... `)
    return
  }
  info('no TTY and no --fund-cmd: fund the address out of band, then re-run this step')
  throw new AssertError('funding required but no funding mechanism available')
}

function runShell(cmd, extraEnv) {
  return new Promise((resolve, reject) => {
    const child = spawn(cmd, { shell: true, stdio: 'inherit', env: { ...process.env, ...extraEnv } })
    child.on('exit', (code) => (code === 0 ? resolve() : reject(new Error(`fund-cmd exited ${code}`))))
    child.on('error', reject)
  })
}

function prompt(q) {
  const rl = createInterface({ input: process.stdin, output: process.stdout })
  return new Promise((resolve) => rl.question(q, () => { rl.close(); resolve() }))
}

// Poll an async read until a predicate holds or the budget runs out. Testnet
// confirms in ~30s and NOTHING is final at 0-conf, so live confirmation waits use
// this rather than assuming an instant result.
export async function waitFor(fn, { tries = 40, everyMs = 15000, what = 'condition' } = {}) {
  for (let i = 0; i < tries; i++) {
    const v = await fn()
    if (v) return v
    info(`waiting for ${what} (${i + 1}/${tries})...`)
    await sleep(everyMs)
  }
  throw new AssertError(`timed out waiting for ${what}`)
}

export function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms))
}
