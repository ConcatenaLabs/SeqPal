// Screenshots of the issuer's page for a payment to many wallets at once:
// the distribution run's payout, or the refund card, as an issuer signed in
// with `session` sees it. Run by seqpald/regtest_payouts_test.go when CHROME
// names a Chromium.
//
//   node scripts/e2e/payouts-page.mjs CHROME BASE SESSION ISSUANCE distribution|refund OUT.png
import { writeFileSync } from 'node:fs'
import { launch } from './lib/cdp.mjs'

const [chrome, base, session, issuance, mode, out] = process.argv.slice(2)
const page = await launch(chrome)
try {
  await page.setCookie(base + '/seqpal/', 'seqpal_session', session)
  await page.goto(`${base}/seqpal/issuance/${issuance}`)
  if (mode === 'distribution') {
    await page.waitFor(`[...document.querySelectorAll('h2')].some((h) => h.innerText.includes('Distribution console'))`, 'the distribution console')
    await page.waitFor(`[...document.querySelectorAll('button')].some((b) => /run|dividend/i.test(b.innerText) && b.querySelector('.font-mono'))`, 'a run', 30000)
    await page.eval(`(() => { const b = [...document.querySelectorAll('button')].find((b) => b.innerText.includes('regtest dividend')); b.click(); return true })()`)
    await page.waitFor(`!!document.querySelector('[data-testid=wallet-payout]')`, 'the payout panel')
  } else {
    await page.waitFor(`!!document.querySelector('[data-testid=refund-card] [data-testid=wallet-payout]')`, 'the refund card')
  }
  await page.clickButton('Details')
  await new Promise((r) => setTimeout(r, 500))
  await page.eval(`(() => { const el = document.querySelector(${JSON.stringify(mode === 'distribution' ? '[data-testid=wallet-payout]' : '[data-testid=refund-card]')}); el.scrollIntoView({ block: 'center' }); return true })()`)
  const text = await page.eval(`document.querySelector(${JSON.stringify(mode === 'distribution' ? '[data-testid=wallet-payout]' : '[data-testid=refund-card]')}).innerText`)
  console.log('[payouts-page] ' + text.replace(/\n+/g, ' | '))
  // The panel and what sits around it on the page, not the whole long page.
  const sel = mode === 'distribution' ? '[data-testid=wallet-payout]' : '[data-testid=refund-card]'
  const clip = await page.eval(`(() => {
    const card = document.querySelector(${JSON.stringify(sel)}).closest('.card') || document.querySelector(${JSON.stringify(sel)})
    const r = card.getBoundingClientRect()
    return { x: 0, y: Math.max(0, r.top + scrollY - 20), width: document.documentElement.clientWidth, height: r.height + 40, scale: 1 }
  })()`)
  const { data } = await page.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true, clip })
  writeFileSync(out, Buffer.from(data, 'base64'))
} catch (e) {
  // What the page showed instead, for whoever reads the failure.
  await page.screenshot(out.replace(/\.png$/, '.fail.png')).catch(() => {})
  const text = await page
    .eval(`(() => { const h = [...document.querySelectorAll('h2')].find((h) => /Distribution console|Refund subscribers/.test(h.innerText)); return (h ? h.closest('.card') : document.body).innerText })()`)
    .catch(() => '')
  console.log('[payouts-page] failed; the page reads: ' + String(text).replace(/\n+/g, ' | ').slice(0, 3000))
  throw e
} finally {
  await page.close()
}
