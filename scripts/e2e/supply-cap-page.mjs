// The most there will ever be of a token, through the issuance page, against a
// real Sequentia node, a real openampd and the real seqpald serving the built
// page (scripts/regtest-supply-cap.sh starts all three and runs this).
//
// In headless Chromium, as the issuer: the deploy form asks for the most there
// will ever be and says what it means; the deploy passes it to the policy
// server, which puts the token's reissuance token in a supply cap of the room
// above the initial supply. Then, from the Depository-Receipt console on the
// same page: a mint, a mint one atom past the room left (refused, in plain
// words, nothing built), a mint of exactly the room left, and one atom more
// (refused). The page's supply figures are read from the DOM after each step,
// and each mint is checked on chain.
//
//   BASE=http://127.0.0.1:PORT CHROME=/path/to/chrome NODE_URL=... NODE_USER=...
//   NODE_PASS=... OPENAMPD_URL=... EVIDENCE=dir node scripts/e2e/supply-cap-page.mjs
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { Client, signIn } from './lib/drill.mjs'
import { launch } from './lib/cdp.mjs'
import { generateEnclaveKey, xonlyOf } from './lib/wallet-signer.mjs'

const BASE = process.env.BASE
const EVIDENCE = process.env.EVIDENCE || ''
const TICKER = 'CAPDR'
const out = { steps: [] }
const log = (step, data) => {
  out.steps.push({ step, ...data })
  console.log(`== ${step}: ${JSON.stringify(data)}`)
  if (EVIDENCE) writeFileSync(join(EVIDENCE, 'supply-cap-page.json'), JSON.stringify(out, null, 1))
}
const assert = (cond, what) => {
  if (!cond) throw new Error('FAILED: ' + what)
}

async function rpc(method, params = [], wallet = '') {
  const res = await fetch(process.env.NODE_URL + (wallet ? '/wallet/' + wallet : ''), {
    method: 'POST',
    headers: {
      'content-type': 'application/json',
      authorization: 'Basic ' + Buffer.from(process.env.NODE_USER + ':' + process.env.NODE_PASS).toString('base64'),
    },
    body: JSON.stringify({ jsonrpc: '1.0', id: 1, method, params }),
  })
  const j = await res.json()
  if (j.error) throw new Error(method + ': ' + j.error.message)
  return j.result
}
const mine = async () => rpc('generatetoaddress', [1, await rpc('getnewaddress', [], 'miner')])

async function supplyRows(page) {
  return page.eval(`Object.fromEntries([...document.querySelectorAll('[data-testid=supply-figures]')].slice(0,1)
    .flatMap((f) => [...f.querySelectorAll('[data-supply-row]')].map((d) => [d.dataset.supplyRow, d.innerText]))
    .concat([['words', (document.querySelector('[data-testid=supply-words]')||{}).innerText||'']]))`)
}

async function main() {
  if (EVIDENCE) mkdirSync(EVIDENCE, { recursive: true })
  const shot = (name) => (EVIDENCE ? page.screenshot(join(EVIDENCE, name + '.png')) : null)

  // The issuer: a SeqPal ID with an OpenAMP account (its enclave key).
  const { priv } = generateEnclaveKey()
  const key = { priv, xonly: xonlyOf(priv) }
  const api = new Client(BASE + '/seqpal/api')
  const aid = await signIn(api, key, 'Cap Issuer')
  const created = await api.call('POST', '/issuances', {
    name: 'Capped Receipt',
    ticker: TICKER,
    structure_id: 'depository-receipt',
    terms: { jurisdictions: { HN: { access: 'standard' } }, price: 1.0 },
  })
  assert(created.status === 200 || created.status === 201, 'create issuance: ' + JSON.stringify(created))
  const issId = created.data.id || created.data.issuance?.id
  log('issuer', { aid, xonly: key.xonly, issuance: issId })

  var page = await launch(process.env.CHROME)
  try {
    await page.setCookie(BASE + '/seqpal/', 'seqpal_session', api.cookie.split('=').slice(1).join('='))
    await page.goto(`${BASE}/seqpal/issuance/${issId}`)
    await page.waitFor(`!!document.querySelector('#dep-max-supply')`, 'the deploy form')

    // The question, its default and its consequence, as the issuer sees them.
    await page.type('#dep-supply', '1000')
    await page.type('#dep-precision', '2')
    const label = await page.eval(`document.querySelector('label[for=dep-max-supply]').innerText`)
    const dflt = await page.eval(`document.querySelector('#dep-max-supply').value`)
    const dfltWords = await page.text('[data-testid=max-supply-consequence]')
    assert(dflt === '1000' && dfltWords.startsWith('Final: no more'), 'the default is the initial supply, final')
    await page.type('#dep-max-supply', '1250')
    const words = await page.text('[data-testid=max-supply-consequence]')
    const explain = await page.eval(`document.querySelector('#dep-max-supply').nextElementSibling.innerText`)
    log('deploy form', { label, default: dflt, default_words: dfltWords, set_to: '1250', words, explanation: explain })
    await shot('1-deploy-form')

    await page.clickButton('Deploy on Sequentia')
    await page.waitFor(`document.body.innerText.includes('Depository-Receipt programme')`, 'the live issuance', 120000)
    const list = (await api.call('GET', '/issuances')).data
    const issued = (list.issuances || list).find((x) => x.id === issId)
    assert(issued.status === 'live' && issued.max_supply === 1250, 'deployed with max_supply 1250: ' + JSON.stringify(issued).slice(0, 300))
    const asset = issued.asset_id
    const oa = await (await fetch(`${process.env.OPENAMPD_URL}/v1/supply?asset=${asset}`)).json()
    assert(oa.supply_cap.cap_atoms === 25000 && oa.supply_cap.minted_atoms === 0, 'the cap is the room above the initial supply')
    const itx = await rpc('getrawtransaction', [issued.txid, true])
    log('deployed', {
      asset, issuance_txid: issued.txid, max_supply: issued.max_supply,
      openampd_supply_cap: { cap_atoms: oa.supply_cap.cap_atoms, minted_atoms: oa.supply_cap.minted_atoms, script_pubkey: oa.supply_cap.script_pubkey },
      token_output: itx.vout.find((o) => o.scriptPubKey.hex === oa.supply_cap.script_pubkey),
    })
    await mine()

    await page.clickButton('Enable programme')
    await page.waitFor(`!!document.querySelector('#dr-mint-atoms')`, 'the mint form', 60000)
    await page.goto(`${BASE}/seqpal/issuance/${issId}`)
    await page.waitFor(`!!document.querySelector('[data-testid=supply-figures]') && !!document.querySelector('#dr-mint-atoms')`, 'supply figures')
    log('supply before any mint', await supplyRows(page))
    await shot('2-live-before-mint')

    const mint = async (atoms) => {
      // A fresh page each time, so no earlier notice or error is read as this one's.
      await page.goto(`${BASE}/seqpal/issuance/${issId}`)
      await page.waitFor(`!!document.querySelector('#dr-mint-atoms')`, 'the mint form')
      await page.type('#dr-mint-atoms', String(atoms))
      await page.clickButton('Mint')
      return page.waitFor(
        `(() => { const e = document.querySelector('[data-testid=dr-error]'); if (e) return 'error: ' + e.innerText;
          return document.body.innerText.includes('Reissuance broadcast') ? 'broadcast' : '' })()`,
        'the mint result',
        60000,
      )
    }
    const refresh = async () => {
      await page.goto(`${BASE}/seqpal/issuance/${issId}`)
      await page.waitFor(`!!document.querySelector('[data-testid=supply-figures]')`, 'supply figures')
      return supplyRows(page)
    }
    const ops = async () => (await api.call('GET', `/issuances/${issId}/dr`)).data.ops

    let r = await mint(15000)
    assert(r === 'broadcast', 'a mint under the cap: ' + r)
    await mine()
    let tx = (await ops()).find((o) => o.atoms === 15000)
    let rtx = await rpc('getrawtransaction', [tx.txid, true])
    log('mint 150 (15,000 atoms)', { txid: tx.txid, confirmations: rtx.confirmations, vsize: rtx.vsize,
      issuance_amount: rtx.vin.find((v) => v.issuance)?.issuance.assetamount, page: await refresh() })
    await shot('3-after-first-mint')

    const before = (await (await fetch(`${process.env.OPENAMPD_URL}/v1/supply?asset=${asset}`)).json()).supply_cap
    const mempool = await rpc('getrawmempool')
    r = await mint(10001)
    assert(r.startsWith('error: This would create 100.01 CAPDR, but only 100 more can ever be created'), 'one atom past the room: ' + r)
    const after = (await (await fetch(`${process.env.OPENAMPD_URL}/v1/supply?asset=${asset}`)).json()).supply_cap
    assert(after.minted_atoms === before.minted_atoms && (await rpc('getrawmempool')).length === mempool.length, 'nothing built')
    log('one atom past the room left (10,001 atoms)', { refused: r, openampd_minted_atoms: after.minted_atoms, mempool: mempool.length })
    await shot('4-one-atom-over-refused')

    r = await mint(10000)
    assert(r === 'broadcast', 'a mint of exactly the room left: ' + r)
    await mine()
    tx = (await ops()).find((o) => o.atoms === 10000 && o.state === 'broadcast')
    rtx = await rpc('getrawtransaction', [tx.txid, true])
    const atCap = await refresh()
    log('mint 100 (10,000 atoms): the room left', { txid: tx.txid, confirmations: rtx.confirmations, vsize: rtx.vsize, page: atCap })
    assert(atCap['Can still be created'] === `0 ${TICKER}` && atCap['The most there will ever be'] === `1,250 ${TICKER}`, 'at the cap')

    r = await mint(1)
    assert(r.startsWith(`error: No more ${TICKER} can be created`), 'one atom at the cap: ' + r)
    const final = (await (await fetch(`${process.env.OPENAMPD_URL}/v1/supply?asset=${asset}`)).json()).supply_cap
    log('one atom at the cap', { refused: r, openampd: { cap_atoms: final.cap_atoms, minted_atoms: final.minted_atoms, room_atoms: final.room_atoms } })
    await shot('5-at-the-cap')
    console.log('OK: issued with a cap through the page, minted to it, one atom over refused')
  } finally {
    await page.close()
  }
}

main().catch((e) => {
  console.error(e.stack || e.message)
  process.exit(1)
})
