// M4 copy overhaul gate: the house-style and first-principles rules that must
// hold across every user-visible string in the SPA source. This is the acceptance
// gate for the copy overhaul (OVERHAUL.md M4, section 4.8): no em dashes anywhere
// in user-visible copy; never "permissionless" (Sequentia assets are permissioned
// and policy co-signed between eligible holders, not permissionless); never "the
// SEQ chain" (the network is "the Sequentia network"; SEQ is the token ticker);
// never "confidential by default" (Sequentia is transparent by default,
// confidentiality is opt-in); never "confidential asset" (confidentiality is a
// per-transfer choice, so there are no confidential assets, only confidential
// transfers). Comments and URLs are out of scope by instruction, so both are
// stripped before a line is inspected.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, dirname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const SRC = join(dirname(fileURLToPath(import.meta.url)), '..', 'src')
const EXTS = new Set(['.js', '.jsx', '.mjs', '.ts', '.tsx'])

// The rules. Each matcher runs over comment-stripped source. The em dash is
// U+2014; the phrases are matched case-insensitively and tolerate a space or a
// hyphen between words so "confidential-by-default" is caught too.
const RULES = [
  { name: 'em-dash', test: (s) => s.includes('—'), why: 'no em dashes in user-visible copy (house style)' },
  { name: 'en-dash', test: (s) => s.includes('–'), why: 'no en dashes in user-visible copy (house style)' },
  { name: 'permissionless', test: (s) => /permissionless/i.test(s), why: 'assets are permissioned and policy co-signed, never "permissionless"' },
  { name: 'the-SEQ-chain', test: (s) => /\bthe\s+SEQ\s+(chain|network)\b/i.test(s) || /\bSEQ\s+chain\b/i.test(s), why: 'the network is "the Sequentia network"; SEQ is the token ticker' },
  { name: 'confidential-by-default', test: (s) => /confidential[\s-]by[\s-]default/i.test(s), why: 'Sequentia is transparent by default; confidentiality is opt-in' },
  // A rule may carry `allow`: relative file paths where the phrase is permitted
  // (the Documentation surface may need it to explain the model's history).
  {
    name: 'confidential-asset',
    test: (s) => /confidential[\s-]assets?\b/i.test(s),
    why: 'there are no confidential assets, only confidential transfers (confidentiality is a per-transfer choice)',
    allow: ['src/pages/Docs.jsx', 'src/pages/Verify.jsx'],
  },
]

function walk(dir) {
  const out = []
  for (const entry of readdirSync(dir)) {
    const p = join(dir, entry)
    if (statSync(p).isDirectory()) out.push(...walk(p))
    else if (EXTS.has(p.slice(p.lastIndexOf('.')))) out.push(p)
  }
  return out
}

// stripComments removes JS/JSX comments while preserving line numbers (so a
// violation reports the right line). It replaces comment characters with spaces
// rather than deleting them. A "//" is treated as a line comment only when it is
// outside a string literal and not part of a "://" URL, so a URL inside a string
// is never mistaken for a comment.
function stripComments(src) {
  const lines = src.split('\n')
  let inBlock = false
  return lines
    .map((line) => {
      let out = ''
      let i = 0
      // string state carried WITHIN a line only (JS string literals do not span
      // raw newlines; template literals are rare in this copy and their content is
      // still user-visible, so treating them as string content is correct here).
      let quote = ''
      while (i < line.length) {
        const c = line[i]
        const c2 = line[i + 1]
        if (inBlock) {
          if (c === '*' && c2 === '/') { inBlock = false; out += '  '; i += 2; continue }
          out += ' '
          i += 1
          continue
        }
        if (quote) {
          out += c
          if (c === '\\') { out += c2 ?? ''; i += 2; continue }
          if (c === quote) quote = ''
          i += 1
          continue
        }
        if (c === '"' || c === "'" || c === '`') { quote = c; out += c; i += 1; continue }
        if (c === '/' && c2 === '*') { inBlock = true; out += '  '; i += 2; continue }
        if (c === '/' && c2 === '/' && line[i - 1] !== ':') break // line comment to EOL
        out += c
        i += 1
      }
      return out
    })
    .join('\n')
}

// ── Flow-surface protocol-name gate ─────────────────────────────────────────
// The issuance wizard, product, pricing, and checkout surfaces speak PLAIN
// BUSINESS LANGUAGE: the protocol names live only on the Documentation surface
// (src/pages/Docs.jsx) and the technical verification explainer it hosts
// (src/pages/Verify.jsx, mounted at /docs/verify). This gate fails if a
// protocol name appears in the flow-surface page sources. Comments are
// stripped first, and import/export lines are skipped so a module path like
// '../../lib/openamp' never trips the gate; everything else in those files,
// including code identifiers, must stay free of the names.
const FLOW_SURFACES = [
  'pages/onboarding/NewIssuance.jsx',
  'pages/onboarding/Steps.jsx',
  'pages/Home.jsx',
  'pages/Products.jsx',
  'pages/Structures.jsx',
  'pages/Pricing.jsx',
  'pages/Faq.jsx',
  'components/Navbar.jsx',
  'components/Footer.jsx',
]

const PROTOCOL_RULES = [
  { name: 'OpenAMP', test: (s) => /open\s?amp\b/i.test(s) },
  { name: 'OpenDAMP', test: (s) => /open\s?damp\b/i.test(s) },
  { name: 'supervised-asset', test: (s) => /supervised[\s-](asset|bearer)/i.test(s) },
  { name: 'enclave', test: (s) => /enclave/i.test(s) },
  { name: 'covenant', test: (s) => /covenant/i.test(s) },
  { name: 'policy-server', test: (s) => /policy[\s-]server/i.test(s) },
  { name: 'co-sign', test: (s) => /co-?sign/i.test(s) },
  { name: 'tier-letter', test: (s) => /\btier\s+[A-D]\b/i.test(s) },
]

test('flow surfaces: no protocol names (plain business language everywhere but /docs)', () => {
  const violations = []
  for (const rel of FLOW_SURFACES) {
    const file = join(SRC, rel)
    const stripped = stripComments(readFileSync(file, 'utf8'))
    stripped.split('\n').forEach((line, idx) => {
      if (/^\s*(import|export)\b/.test(line) || /\bfrom\s+['"]/.test(line)) return
      for (const rule of PROTOCOL_RULES) {
        if (rule.test(line)) {
          violations.push({ file: `src/${rel}`, line: idx + 1, rule: rule.name, text: line.trim().slice(0, 120) })
        }
      }
    })
  }
  if (violations.length) {
    const report = violations.map((v) => `  ${v.file}:${v.line} [${v.rule}] ${v.text}`).join('\n')
    assert.fail(
      `${violations.length} protocol-name violation(s) on flow surfaces. These pages speak plain ` +
        `business language; the protocol names belong on the Documentation surface only:\n${report}`
    )
  }
})

test('SPA copy: no em dashes, no permissionless / the SEQ chain / confidential by default / confidential asset', () => {
  const violations = []
  for (const file of walk(SRC)) {
    const rel = relative(join(SRC, '..'), file)
    const stripped = stripComments(readFileSync(file, 'utf8'))
    stripped.split('\n').forEach((line, idx) => {
      for (const rule of RULES) {
        if (rule.allow?.includes(rel)) continue
        if (rule.test(line)) {
          violations.push({
            file: rel,
            line: idx + 1,
            rule: rule.name,
            why: rule.why,
            text: line.trim().slice(0, 120),
          })
        }
      }
    })
  }

  if (violations.length) {
    const report = violations
      .map((v) => `  ${v.file}:${v.line} [${v.rule}] ${v.text}`)
      .join('\n')
    assert.fail(
      `${violations.length} copy-gate violation(s) in user-visible SPA strings ` +
        `(comments and URLs excluded):\n${report}\n` +
        `Rules: ${RULES.map((r) => `${r.name} = ${r.why}`).join('; ')}`
    )
  }
})
