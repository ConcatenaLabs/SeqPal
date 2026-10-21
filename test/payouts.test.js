// Payments to many wallets at once: what an issuer and a holder read.
//
// The service that carries these payments is a hidden product: no page of
// SeqPal names it, and none speaks its protocol. An issuer reads "pay N
// holders", a total, the most the fee can be and a deadline; a holder reads
// "paid to your wallet". These tests pin that on every source file the page is
// built from.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const SRC = join(dirname(fileURLToPath(import.meta.url)), '..', 'src')
const files = (dir) =>
  readdirSync(dir).flatMap((f) => {
    const p = join(dir, f)
    return statSync(p).isDirectory() ? files(p) : /\.(jsx?|html|css)$/.test(f) ? [p] : []
  })

test('no page names the payment service or its protocol', () => {
  for (const f of files(SRC)) {
    const src = readFileSync(f, 'utf8')
    for (const word of [/\barca\b/i, /\bclaim tree/i, /\bclaim key/i, /\bvtxo/i, /\boperator round/i]) {
      assert.doesNotMatch(src, word, `${f} names ${word}`)
    }
  }
})

test('the issuer sees how many are paid, the total, the fee bound and the deadline', () => {
  const src = readFileSync(join(SRC, 'components', 'WalletPayout.jsx'), 'utf8')
  for (const s of ['Fee, at most', 'Collect by', 'Not in this payment', 'Download the payment list', 'Record the payment']) {
    assert.ok(src.includes(s), `WalletPayout shows "${s}"`)
  }
})
