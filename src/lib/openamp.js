// Client for the OpenAMP policy server's PUBLIC endpoints (assets, balances,
// addresses, transparency log). No secret is needed for any of these.
//
// Privileged OpenAMP calls (issuing a restricted asset) are bearer-token gated;
// the token is a server-side secret, so issuance goes through seqpald instead
// (see ./api.js). Paths are same-origin and relative in every environment: in
// production Caddy fronts openampd at /openamp, and in dev the vite proxy
// forwards /openamp to a LOCAL openampd. A dev build therefore never touches
// the live box unless the proxy is deliberately pointed at it.
const OPENAMP_API = '/openamp/v1'

async function j(res) {
  const text = await res.text()
  let body
  try {
    body = text ? JSON.parse(text) : {}
  } catch {
    body = { error: text }
  }
  if (!res.ok) throw new Error(body.error || `${res.status} ${res.statusText}`)
  return body
}

// ── OpenAMP public reads ────────────────────────────────────────────────
export const getAsset = (id) => fetch(`${OPENAMP_API}/assets/${encodeURIComponent(id)}`).then(j)

// { assets: [...] }, never a bare array.
export const listAssets = () => fetch(`${OPENAMP_API}/assets`).then(j)

// { aid, asset, atoms, utxos }. Confirmed balances only.
export const getBalance = (aid, asset) =>
  fetch(
    `${OPENAMP_API}/users/${encodeURIComponent(aid)}/balance?asset=${encodeURIComponent(asset)}`
  ).then(j)

export const getAddress = (aid, asset) =>
  fetch(
    `${OPENAMP_API}/users/${encodeURIComponent(aid)}/address?asset=${encodeURIComponent(asset)}`
  ).then(j)

// The transparency log (hash-chained record of every policy decision).
export const getLog = () => fetch(`${OPENAMP_API}/log`).then(j)

// sha256 hex of a canonical JSON encoding, used as the contract's terms_hash so
// the on-chain asset commits to SeqPal's compliance configuration. seqpald
// recomputes this server-side over the terms it actually mints against; the
// value we send is only a cross-check, and a mismatch refuses the deploy.
// Canonical = object keys sorted lexicographically, no whitespace.
export async function termsHash(obj) {
  const canon = canonicalJSON(obj)
  const buf = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(canon))
  return [...new Uint8Array(buf)].map((b) => b.toString(16).padStart(2, '0')).join('')
}

export function canonicalJSON(v) {
  if (Array.isArray(v)) return '[' + v.map(canonicalJSON).join(',') + ']'
  if (v && typeof v === 'object') {
    return (
      '{' +
      Object.keys(v)
        .sort()
        .map((k) => JSON.stringify(k) + ':' + canonicalJSON(v[k]))
        .join(',') +
      '}'
    )
  }
  return JSON.stringify(v)
}
