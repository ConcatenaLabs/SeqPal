// The enforcement election: three models. Restricted assets stay on the chain,
// so no wizard card offers to hold a token anywhere else, and a token stored
// with the retired "tree" election reads as the serviced one it is.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { readEnforcement, view } from '../src/lib/issuance.js'

const SRC = join(dirname(fileURLToPath(import.meta.url)), '..', 'src')

test('the wizard offers serviced, network and bearer, and nothing else', () => {
  const src = readFileSync(join(SRC, 'pages/onboarding/Steps.jsx'), 'utf8')
  const start = src.indexOf('const ENFORCEMENT_MODELS = [')
  const models = src.slice(start, src.indexOf('\n]\n', start))
  const ids = [...models.matchAll(/^ {4}id: '([a-z]+)',$/gm)].map((m) => m[1])
  assert.deepEqual(ids, ['serviced', 'network', 'bearer'])
})

test('a stored tree election reads as serviced', () => {
  assert.equal(readEnforcement('tree'), 'serviced')
  assert.equal(readEnforcement(' Tree '), 'serviced')
  assert.equal(readEnforcement(''), 'serviced')
  assert.equal(readEnforcement('network'), 'network')
  assert.equal(view({ enforcement: 'tree', terms: { enforcement: 'tree' } }).enforcement, 'serviced')
  assert.equal(view({ enforcement: 'serviced', terms: {} }).enforcement, 'serviced')
})
