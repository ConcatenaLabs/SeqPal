// Client for the OpenAMP policy server and the SeqPal deployment backend.
//
// Two origins, both same-origin behind Caddy in production:
//   - OPENAMP_API   the OpenAMP policy server's PUBLIC endpoints (register a
//                   key, read assets/balances/addresses). No secret needed.
//   - SEQPAL_API    SeqPal's own thin backend (seqpald). The only privileged
//                   OpenAMP call, issuing a restricted asset, is bearer-token
//                   gated; that token is a server-side secret, so issuance is
//                   proxied through seqpald rather than exposed to the browser.
//
// In local dev (vite on :5173) these default to the live testnet box so the
// flow works end to end without running the whole stack locally. Override with
// VITE_OPENAMP_API / VITE_SEQPAL_API if you run your own node + seqpald.
const DEV = import.meta.env.DEV
export const OPENAMP_API =
  import.meta.env.VITE_OPENAMP_API ||
  (DEV ? 'https://sequentiatestnet.com/openamp/v1' : '/openamp/v1')
export const SEQPAL_API =
  import.meta.env.VITE_SEQPAL_API ||
  (DEV ? 'https://sequentiatestnet.com/seqpal/api' : '/seqpal/api')

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
export const getAsset = (id) => fetch(`${OPENAMP_API}/assets/${id}`).then(j)

export const getBalance = (aid, asset) =>
  fetch(`${OPENAMP_API}/users/${aid}/balance?asset=${asset}`).then(j)

export const getAddress = (aid, asset) =>
  fetch(`${OPENAMP_API}/users/${aid}/address?asset=${asset}`).then(j)

// The transparency log (hash-chained record of every policy decision).
export const getLog = () => fetch(`${OPENAMP_API}/log`).then(j)

// ── SeqPal backend: deploy a restricted asset ───────────────────────────
// Registers the issuer's enclave key and mints the initial supply into its
// enclave, all under OpenAMP policy control. Returns { asset, txid,
// contract_hash, aid, address }.
export function deployIssuance(payload) {
  return fetch(`${SEQPAL_API}/deploy`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(payload),
  }).then(j)
}

// Is the SeqPal backend reachable (does live deployment work here)?
export async function backendHealthy() {
  try {
    const r = await fetch(`${SEQPAL_API}/health`)
    return r.ok
  } catch {
    return false
  }
}

// sha256 hex of a stable JSON encoding, used as the contract's terms_hash so
// the on-chain asset commits to SeqPal's compliance configuration.
export async function termsHash(obj) {
  const canon = stableStringify(obj)
  const buf = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(canon))
  return [...new Uint8Array(buf)].map((b) => b.toString(16).padStart(2, '0')).join('')
}

function stableStringify(v) {
  if (Array.isArray(v)) return '[' + v.map(stableStringify).join(',') + ']'
  if (v && typeof v === 'object') {
    return (
      '{' +
      Object.keys(v)
        .sort()
        .map((k) => JSON.stringify(k) + ':' + stableStringify(v[k]))
        .join(',') +
      '}'
    )
  }
  return JSON.stringify(v)
}
