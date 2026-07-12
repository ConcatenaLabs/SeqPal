// Client for seqpald, the SeqPal backend. seqpald is the source of truth for
// accounts, entities, issuances, and every deployment: the browser asserts no
// financial fact of its own.
//
// Same-origin relative paths in every environment (the vite dev proxy forwards
// /seqpal/api to a local seqpald), which is what the strict CSP's
// `connect-src 'self'` requires. The session cookie is HttpOnly and scoped to
// /seqpal, so the path prefix matters: the SPA never reads the cookie, it only
// has to send it.
const BASE = '/seqpal/api'

export class ApiError extends Error {
  constructor(message, status) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

async function req(path, { method = 'GET', body } = {}) {
  let res
  try {
    res = await fetch(BASE + path, {
      method,
      credentials: 'include',
      headers: body === undefined ? undefined : { 'content-type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch {
    throw new ApiError('The SeqPal backend is unreachable from this browser.', 0)
  }
  const text = await res.text()
  let parsed = {}
  try {
    parsed = text ? JSON.parse(text) : {}
  } catch {
    parsed = { error: text }
  }
  // Server messages are user-presentable and are surfaced verbatim.
  if (!res.ok) throw new ApiError(parsed.error || `${res.status} ${res.statusText}`, res.status)
  return parsed
}

// ── public ──────────────────────────────────────────────────────────────
// { ok, network, confidential, openamp_ok, issuer_token_ok }. ok is an
// authenticated upstream probe, so a false here means deployment really would
// fail, and the UI can say so before checkout instead of at the mint.
export const health = () => req('/health')

export const challenge = (xonly) =>
  req('/auth/challenge', { method: 'POST', body: { xonly } })

export const register = (body) => req('/auth/register', { method: 'POST', body })

export const login = (body) => req('/auth/login', { method: 'POST', body })

// ── session required ────────────────────────────────────────────────────
export const logout = () => req('/auth/logout', { method: 'POST', body: {} })

// { account, entities, issuances } for the signed-in principal.
export const me = () => req('/me')

export const createEntity = (body) => req('/entities', { method: 'POST', body })

export const listIssuances = () => req('/issuances')

export const createIssuance = (body) => req('/issuances', { method: 'POST', body })

export const patchIssuance = (id, body) =>
  req(`/issuances/${encodeURIComponent(id)}`, { method: 'PATCH', body })

// The real mint: returns { asset, txid, contract_hash, aid, address, issuance_id }.
// Idempotent on sha256(xonly || terms_hash), so a retry of the same terms
// returns the first result rather than minting a second asset.
export const deploy = (body) => req('/deploy', { method: 'POST', body })
