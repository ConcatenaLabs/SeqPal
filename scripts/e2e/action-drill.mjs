// Live corporate-action drill against the REAL SeqPal stack: bearer deploy,
// share delivery to a second SeqPal ID, a funded dividend claimed with a
// holding proof, and a weighted vote, end to end on the Sequentia testnet.
//
// Run from the repo root:  node scripts/e2e/action-drill.mjs
//
// Env:
//   SEQPAL_BASE        seqpald API base (default https://sequentiatestnet.com/seqpal/api)
//   SEQPAL_ELECTRS     public electrs base (default https://sequentiatestnet.com/api)
//   SEQPAL_DRILL_SSH   ssh host for the box (e.g. "seq"): when set, the driver
//                      runs the operator sends itself over ssh; when unset it
//                      prints the exact command and waits for the funds to
//                      appear on electrs.
//   SEQPAL_USDX        the dividend asset id (default the testnet USDX)
//   DRILL_SHARES       shares to deliver to the holder (default 1000; the
//                      asset is precision 0, so shares == atoms)
//   DRILL_DIVIDEND_ATOMS  dividend pool in RPC atoms (default 100000000 = 1 USDX)
//
// State: scripts/e2e/.drill-state.json (gitignored, 0600; holds the generated
// private keys and per-step results). Every step is idempotent against it, so
// a rerun resumes where the last run stopped.
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { generateEnclaveKey, generateRecoveryKey } from './lib/wallet-signer.mjs'
import {
  Client,
  must,
  waitFor,
  signIn,
  verifyIdentity,
  deployBearer,
  electrs,
  tipHeight,
  assetUtxos,
  amount8,
  operatorSend,
  loadState,
  saveState,
  signHoldingStatement,
} from './lib/drill.mjs'
import { p2trAddress, p2wpkhAddressFromPriv, decodeSegwit } from './lib/bech32.mjs'

const STATE_PATH = join(dirname(fileURLToPath(import.meta.url)), '.drill-state.json')
const USDX = process.env.SEQPAL_USDX || '2a515539da5e6a60caa7766ecd65bac0c10d15717ddd2088844ba58f4d04b9de'
const SHARES = Number(process.env.DRILL_SHARES || 1000) // precision 0: shares == atoms
const DIV_ATOMS = Number(process.env.DRILL_DIVIDEND_ATOMS || 100000000)

const state = loadState(STATE_PATH)
const save = () => saveState(STATE_PATH, state)

// Two-phase claim: fetch sign_this, sign it with the key controlling the
// outpoints, resubmit. A 409 is "already claimed", which for a resumed drill
// is success, not failure.
async function claimTwoPhase(client, actionId, privHex, body, label) {
  const first = await client.call('POST', `/actions/${actionId}/claim`, body)
  if (first.status === 409) {
    console.log(`ok   ${label} (already claimed; resumed)`)
    return null
  }
  const p1 = must(first, `${label} phase 1`)
  const sig = signHoldingStatement(privHex, p1.sign_this)
  const second = await client.call('POST', `/actions/${actionId}/claim`, { ...body, sig })
  if (second.status === 409) {
    console.log(`ok   ${label} (already claimed; resumed)`)
    return null
  }
  return must(second, `${label} phase 2`)
}

async function getAction(pub, actionId) {
  const r = await pub.call('GET', `/actions/${actionId}`)
  return r.status === 200 && r.data && r.data.action ? r.data.action : null
}

async function main() {
  console.log('base   ', process.env.SEQPAL_BASE || 'https://sequentiatestnet.com/seqpal/api')
  console.log('electrs', process.env.SEQPAL_ELECTRS || 'https://sequentiatestnet.com/api')
  console.log('state  ', STATE_PATH)

  // ── identities (persisted; the private keys live only in the state file) ──
  if (!state.issuer) {
    state.issuer = generateEnclaveKey()
    state.recovery = generateRecoveryKey()
    save()
  }
  if (!state.holder) {
    state.holder = generateEnclaveKey()
    save()
  }
  console.log('issuer xonly   ', state.issuer.xonly)
  console.log('holder xonly   ', state.holder.xonly)

  const issuer = new Client()
  const holder = new Client()
  const pub = new Client() // anonymous, for the public reads

  // ── step 1: issuer ID + bearer deploy ─────────────────────────────────────
  state.aid_issuer = await signIn(issuer, state.issuer, 'Action Drill Issuer')
  save()
  await verifyIdentity(issuer, { residence: 'HN-PRO', name: 'Action Drill Issuer' })

  if (!state.asset) {
    const ticker = 'AD' + Math.random().toString(36).slice(2, 6).toUpperCase().replace(/[^A-Z0-9]/g, 'X')
    const { issId, deploy } = await deployBearer(issuer, state.issuer, state.recovery, state.aid_issuer, {
      name: 'Action Drill Shares',
      ticker,
      supply: 1000000,
      precision: 0,
      pause: false,
    })
    state.issuance_id = issId
    state.asset = deploy.asset
    state.deploy_txid = deploy.txid
    state.treasury_address = deploy.treasury_address
    save()
  }
  console.log('ok   issuance', state.issuance_id)
  console.log('     asset   ', state.asset)
  console.log('     mint tx ', state.deploy_txid)

  // The mint must confirm before the escrow wallet can deliver from it.
  await waitFor('mint confirmation', async () => {
    const tx = await electrs(`/tx/${state.deploy_txid}`).catch(() => null)
    return tx && tx.status && tx.status.confirmed
  })
  console.log('ok   mint confirmed')

  // ── step 2: the holder, a second verified SeqPal ID ───────────────────────
  state.aid_holder = await signIn(holder, state.holder, 'Action Drill Holder')
  save()
  await verifyIdentity(holder, { residence: 'DE', name: 'Action Drill Holder' })

  // ── step 3: deliver shares to the holder's P2TR key-path address ──────────
  const holderAddress = p2trAddress(state.holder.xonly, 'tb')
  state.holder_address = holderAddress
  save()
  // Sanity: the address decodes back to witness v1 over exactly the x-only
  // key (the 5120||xonly script the claim derivation expects), and the box's
  // electrs parses it (a bad address would 400 on the address endpoint). The
  // encoder itself is pinned in test/enforcement.test.js against vectors from
  // the node's own key_io_valid.json.
  const dec = decodeSegwit(holderAddress)
  if (dec.version !== 1 || dec.program !== state.holder.xonly) {
    console.error('FAIL holder address does not decode to the x-only key')
    process.exit(1)
  }
  await electrs(`/address/${holderAddress}`)
  console.log('ok   holder address', holderAddress)

  let shareUtxos = await assetUtxos(holderAddress, state.asset)
  if (shareUtxos.length === 0) {
    const txid = operatorSend(holderAddress, SHARES, state.asset, 'share delivery')
    if (txid) {
      state.steps.delivery = { txid }
      save()
    }
    shareUtxos = await waitFor('share delivery confirmation', async () => {
      const us = await assetUtxos(holderAddress, state.asset)
      return us.length > 0 ? us : null
    })
  }
  const heldAtoms = shareUtxos.reduce((s, u) => s + u.atoms, 0)
  console.log('ok   holder holds', heldAtoms, 'shares across', shareUtxos.length, 'outpoint(s)')
  for (const u of shareUtxos) console.log('     outpoint', u.outpoint)

  // ── step 4: the dividend ──────────────────────────────────────────────────
  if (!state.steps.dividend) {
    const tip = await tipHeight()
    const create = must(
      await issuer.call('POST', `/issuances/${state.issuance_id}/actions`, {
        kind: 'dividend',
        record_height: tip + 2,
        dividend: { asset: USDX, total_atoms: DIV_ATOMS },
      }),
      'declare dividend',
    )
    state.steps.dividend = {
      action_id: create.action.id,
      record_height: tip + 2,
      deposit_address: create.deposit_address,
    }
    save()
  }
  const div = state.steps.dividend
  console.log('ok   dividend action', div.action_id, 'record height', div.record_height)

  let action = await waitFor('dividend snapshot', async () => {
    const a = await getAction(pub, div.action_id)
    return a && a.state === 'snapshotted' ? a : null
  }, { timeoutMs: 1200000 })
  console.log('ok   snapshot at height', action.snapshot.height, 'holders', action.snapshot.holder_count, 'total', action.snapshot.total_atoms)

  if (!action.dividend.funded) {
    const txid = operatorSend(div.deposit_address, DIV_ATOMS, action.dividend.asset, 'dividend funding')
    if (txid) {
      div.funding_txid = txid
      save()
    }
    console.log('     funding', amount8(DIV_ATOMS), 'of', action.dividend.asset, '->', div.deposit_address)
    action = await waitFor('dividend funded', async () => {
      const a = await getAction(pub, div.action_id)
      return a && a.dividend && a.dividend.funded ? a : null
    })
  }
  console.log('ok   dividend funded, atoms', action.dividend.funded_atoms)

  // ── step 5: the holder claims with a holding proof ────────────────────────
  const payoutAddress = p2wpkhAddressFromPriv(state.holder.priv, 'tb')
  state.payout_address = payoutAddress
  save()
  console.log('     payout address', payoutAddress)

  if (!div.claimed) {
    const claim = await claimTwoPhase(
      holder,
      div.action_id,
      state.holder.priv,
      {
        pubkey: state.holder.xonly,
        outpoints: shareUtxos.map((u) => u.outpoint),
        payout_address: payoutAddress,
      },
      'dividend claim',
    )
    div.claimed = true
    if (claim && claim.claim) {
      div.claim_id = claim.claim.id
      div.net_atoms = claim.claim.net_atoms
      div.withheld_atoms = claim.claim.withheld_atoms
      div.payment_txid = claim.claim.txid || ''
      console.log('     gross', claim.claim.gross_atoms, 'withheld', claim.claim.withheld_atoms, 'net', claim.claim.net_atoms)
    }
    save()
  }

  // The payment txid appears once the fund-safety machinery pays; verify the
  // net amount actually arrived at the payout address on electrs.
  const arrived = await waitFor('dividend payout arrival', async () => {
    const us = await assetUtxos(payoutAddress, action.dividend.asset)
    return us.length > 0 ? us : null
  }, { timeoutMs: 900000 })
  const arrivedAtoms = arrived.reduce((s, u) => s + u.atoms, 0)
  if (div.net_atoms && arrivedAtoms !== div.net_atoms) {
    console.error(`FAIL payout amount: ${arrivedAtoms} atoms arrived, claim says net ${div.net_atoms}`)
    process.exit(1)
  }
  div.payment_txid = div.payment_txid || arrived[0].txid
  save()
  console.log('ok   dividend paid:', arrivedAtoms, 'atoms at', payoutAddress)
  console.log('     payment txid', div.payment_txid)

  // ── step 6: the vote ──────────────────────────────────────────────────────
  if (!state.steps.vote) {
    const tip = await tipHeight()
    const create = must(
      await issuer.call('POST', `/issuances/${state.issuance_id}/actions`, {
        kind: 'vote',
        record_height: tip + 2,
        vote: {
          question: 'Approve the corporate-action drill?',
          choices: ['for', 'against'],
          closes_height: tip + 240,
        },
      }),
      'declare vote',
    )
    state.steps.vote = { action_id: create.action.id, record_height: tip + 2 }
    save()
  }
  const vote = state.steps.vote
  console.log('ok   vote action', vote.action_id, 'record height', vote.record_height)

  await waitFor('vote snapshot', async () => {
    const a = await getAction(pub, vote.action_id)
    return a && a.state === 'snapshotted' ? a : null
  }, { timeoutMs: 1200000 })
  console.log('ok   vote snapshotted')

  if (!vote.claimed) {
    // The snapshot is fresh, so re-read the holder's outpoints.
    const voteUtxos = await assetUtxos(holderAddress, state.asset)
    await claimTwoPhase(
      holder,
      vote.action_id,
      state.holder.priv,
      {
        pubkey: state.holder.xonly,
        outpoints: voteUtxos.map((u) => u.outpoint),
        choice: 'for',
      },
      'vote ballot',
    )
    vote.claimed = true
    save()
  }

  const tallyView = await getAction(pub, vote.action_id)
  const tally = tallyView && tallyView.vote && tallyView.vote.tally
  const proofs = (tallyView && tallyView.vote && tallyView.vote.proofs) || []
  if (!tally || Number(tally.for) < heldAtoms) {
    console.error('FAIL vote tally does not carry the holder weight:', JSON.stringify(tally))
    process.exit(1)
  }
  const proofOutpoints = new Set(proofs.map((p) => p.outpoint))
  for (const u of shareUtxos) {
    if (!proofOutpoints.has(u.outpoint)) {
      console.error('FAIL the public proof list is missing outpoint', u.outpoint)
      process.exit(1)
    }
  }
  console.log('ok   vote tally', JSON.stringify(tally), 'with', proofs.length, 'public proof row(s)')

  console.log('\nDRILL COMPLETE')
  console.log('  asset            ', state.asset)
  console.log('  mint txid        ', state.deploy_txid)
  console.log('  holder address   ', holderAddress)
  console.log('  dividend action  ', div.action_id, 'payment txid', div.payment_txid)
  console.log('  vote action      ', vote.action_id)
}

main().catch((e) => {
  console.error('FAIL', e.message)
  process.exit(1)
})
