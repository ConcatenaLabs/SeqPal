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

// ── owner-scoped chain reads (M3) ───────────────────────────────────────────
// The register / cap table for one deployed issuance, proxied verbatim from
// openampd GET /v1/issuer/holders: { asset, height, holders:{aid:atoms},
// total_atoms }. 403 unless the session owns the issuance; the atoms figure is
// confirmed on-chain balance, the only truthful source for "held".
export const issuanceHolders = (id) =>
  req(`/issuances/${encodeURIComponent(id)}/holders`)

// This issuance's slice of the transparency log plus every log-head anchor
// entry: { issuance_id, asset, entries:[{seq,prev,time,action,data,hash}],
// log_url }. Each entry's hash is re-verified client-side; anchor entries carry
// data.txid, deep-linked to the explorer.
export const issuanceLog = (id) => req(`/issuances/${encodeURIComponent(id)}/log`)

// The session-scoped Server-Sent Events endpoint. Consumed with EventSource
// (which sends the HttpOnly session cookie same-origin), not fetch: the store
// owns the single connection and fans "watch" events out to the surfaces.
export const EVENTS_URL = `${BASE}/events`

// Compile the Step 5 matrix (or a preview override) into openampd rules,
// server-side. seqpald is the only place the authoritative rules are computed;
// this returns { rules, tip_height, blocks_per_day } for the onboarding preview.
export const compileIssuance = (id, body) =>
  req(`/issuances/${encodeURIComponent(id)}/compile`, { method: 'POST', body })

// ── SeqPal ID (M2) ─────────────────────────────────────────────────────────
// Run KYC (labeled-simulated document review, real states) plus real sanctions
// screening on the signed-in identity, then stamp categories on approval.
// Returns { status, aid, categories?, valid_until?, screening[] }.
export const idVerify = (body) => req('/id/verify', { method: 'POST', body })

// The passport: { aid, enclave_key, status, categories[], valid_until,
// screening[], lists_screened[], frozen, entities[], accepted{assets,venues} }.
export const idPassport = () => req('/id/passport')

// KYB review for a linked corporate entity: provisions the entity treasury
// enclave and records the UBO link. Returns { entity, treasury_aid, ubo_link }.
export const verifyEntity = (id, body) =>
  req(`/id/entities/${encodeURIComponent(id)}/verify`, { method: 'POST', body })

// Advisory eligibility preflight (public): { aid, asset, eligible, reasons[] }.
export const eligibility = (aid, asset) =>
  req(`/eligibility?aid=${encodeURIComponent(aid)}&asset=${encodeURIComponent(asset)}`)

// ── legal document pipeline (M4) ────────────────────────────────────────────
// Generate (or regenerate) the deterministic, content-addressed document set
// for an issuance and bind its manifest into terms_hash. Owner session only.
// Returns { issuance_id, terms_hash, manifest_hash, documents:[{hash,kind,title}],
// characterization, e_signature_tag, note }. Deploy with the exact returned terms.
export const generateDocuments = (id) =>
  req(`/issuances/${encodeURIComponent(id)}/documents`, { method: 'POST', body: {} })

// The public data-room manifest for a terms_hash: { terms_hash, manifest_hash,
// manifest, terms, access{model,offer_open,close_height}, verify{steps}, issuance }.
// Public: the hash commitment is verifiable with no session.
export const getTerms = (hash) => req(`/terms/${encodeURIComponent(hash)}`)

// A browser-resolvable URL for one document preimage. During the offer window a
// preimage is gate-passers-only (a non-200 to anonymous requests, the standing
// probe); at offer close it publishes. `?format=pdf` serves the house PDF if the
// box toolchain is present, else the canonical HTML with an X-PDF-Unavailable note.
export const docUrl = (docHash, { pdf = false } = {}) =>
  `${BASE}/doc/${encodeURIComponent(docHash)}${pdf ? '?format=pdf' : ''}`

// Publish every offer-window preimage for an issuance (owner). { issuance_id,
// offer_open:false, close_height }.
export const offerClose = (id) =>
  req(`/issuances/${encodeURIComponent(id)}/offer-close`, { method: 'POST', body: {} })

// The instrument-characterization memo. `structure` empty returns { structures }
// (all four); with a structure returns { characterization }. Public.
export const characterization = (structure) =>
  req(`/characterization${structure ? `?structure=${encodeURIComponent(structure)}` : ''}`)

// The genesis-terms-hash plus anchored amendment chain, for the Verify explainer.
// { issuance_id, asset_id, genesis_terms_hash, contract_hash, amendments[], note }.
// Owner session only.
export const amendments = (id) => req(`/issuances/${encodeURIComponent(id)}/amendments`)

// Record a real BIP340 e-signature over the tagged document hash. Owner/session
// signs with the enclave key (see keys.signDocument). Returns { doc_hash,
// signer_aid, tag, anchor_txid, note }.
export const signDocumentSig = (docHash, sig) =>
  req(`/documents/${encodeURIComponent(docHash)}/sign`, { method: 'POST', body: { sig } })

// Every recorded e-signature for a document: { doc_hash, tag, signatures[] }. Public.
export const docSignatures = (docHash) =>
  req(`/documents/${encodeURIComponent(docHash)}/signatures`)

// ── RFSA simulated registry (M4) ────────────────────────────────────────────
// File with the RFSA Financial Products Registry before a public offering can
// deploy. Body { issuer?, structure, doc_manifest_hash, terms_hash, issuance_id? }.
// Returns { filing_number, filing, label }. Session gated (the filer is the
// issuer of record). "Simulated regulator, real registry mechanics".
export const rfsaFile = (body) => req('/rfsa/filings', { method: 'POST', body })

// Public lookup of a filing by number: { filing, label }.
export const rfsaLookup = (number) => req(`/rfsa/filings/${encodeURIComponent(number)}`)

// ── platform reviewer (admin-session only) ──────────────────────────────────
export const reviewQueue = () => req('/admin/review-queue')

export const reviewDecide = (id, body) =>
  req(`/admin/review/${encodeURIComponent(id)}`, { method: 'POST', body })
