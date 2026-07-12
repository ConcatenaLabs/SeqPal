// M4 copy overhaul gate: the house-style and first-principles rules that must
// hold across every user-visible string in the SPA source. This is the acceptance
// gate for the copy overhaul (OVERHAUL.md M4, section 4.8): no em dashes anywhere
// in user-visible copy; never "permissionless" (Sequentia assets are permissioned
// and policy co-signed between eligible holders, not permissionless); never "the
// SEQ chain" (the network is "the Sequentia network"; SEQ is the token ticker);
// never "confidential by default" (Sequentia is transparent by default,
// confidentiality is opt-in). Comments and URLs are out of scope by instruction,
// so both are stripped before a line is inspected.

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
  { name: 'permissionless', test: (s) => /permissionless/i.test(s), why: 'assets are permissioned and policy co-signed, never "permissionless"' },
  { name: 'the-SEQ-chain', test: (s) => /\bthe\s+SEQ\s+(chain|network)\b/i.test(s) || /\bSEQ\s+chain\b/i.test(s), why: 'the network is "the Sequentia network"; SEQ is the token ticker' },
  { name: 'confidential-by-default', test: (s) => /confidential[\s-]by[\s-]default/i.test(s), why: 'Sequentia is transparent by default; confidentiality is opt-in' },
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

test('SPA copy: no em dashes, no permissionless / the SEQ chain / confidential by default', () => {
  const violations = []
  for (const file of walk(SRC)) {
    const stripped = stripComments(readFileSync(file, 'utf8'))
    stripped.split('\n').forEach((line, idx) => {
      for (const rule of RULES) {
        if (rule.test(line)) {
          violations.push({
            file: relative(join(SRC, '..'), file),
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
