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
  constructor(message, status, data) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    // The full parsed response body, so a caller can read structured fields a
    // refusal carries (e.g. the subscribe gate `requirements`), not just message.
    this.data = data || {}
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
  if (!res.ok) throw new ApiError(parsed.error || `${res.status} ${res.statusText}`, res.status, parsed)
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

// ── M5 money engine: offering, gates, subscriptions, fees, closing ──────────
// The promotion-gated offering. PUBLIC: with no session (or an ungated one) it
// returns { gated:true, teaser, cta }; a session that fails the gate adds
// { requirements:[{kind,message,blocked}] }; a passing session returns the full
// offering { gated:false, name, ticker, asset_id, price, quote, terms, rails[],
// manifest_hash, offeree_warning? }. Granting is the EU offeree-counting event.
export const offering = (id) => req(`/issuances/${encodeURIComponent(id)}/offering`)

// Clear one offer-side gate: { kind: uk_statement | appropriateness | sof, ... }.
// uk_statement { basis:hnw|soph, income_gbp, net_assets }; appropriateness
// { kid_ack:true, answers }; sof { source, amount_usd }. Returns { kind, recorded }.
export const gate = (id, body) =>
  req(`/issuances/${encodeURIComponent(id)}/gate`, { method: 'POST', body })

// Subscribe on the payer's chosen rail: { rail: usdx|btc|card|bank, amount,
// refund_address }. usdx/btc → { subscription, deposit_address, pay_amount,
// pay_ccy, confs_required, registrar_note? }; card/bank → { subscription,
// checkout, funds_simulated:true, label }. A 403 carries { requirements[] }.
export const subscribe = (id, body) =>
  req(`/issuances/${encodeURIComponent(id)}/subscribe`, { method: 'POST', body })

// The caller's own subscriptions across offerings. Escrow state is polled here:
// created → in_escrow (deposit confirmed at N confs) → settled | refunded.
export const mySubscriptions = () => req('/subscriptions')

// Owner view of one offering's subscription book + escrow ledger.
export const issuanceSubscriptions = (id) =>
  req(`/issuances/${encodeURIComponent(id)}/subscriptions`)

// Poll a SIMULATED fiat checkout: { id, state, receipt, amount_display,
// funds_simulated:true, label }.
export const fiatStatus = (id) => req(`/fiat/${encodeURIComponent(id)}`)

// Owner: the platform-fee invoices (auto-creates the setup invoice):
// { invoices[], setup_fee_usd, escrow_fee_bps }. The setup fee blocks deploy.
export const fees = (id) => req(`/issuances/${encodeURIComponent(id)}/fees`)

// Owner: pay a platform fee on the issuer's chosen rail { kind:setup, rail }.
// Fiat → { checkout }; on-chain → { deposit_address, pay_amount, pay_ccy }.
export const payFee = (id, body) =>
  req(`/issuances/${encodeURIComponent(id)}/fees/pay`, { method: 'POST', body })

// Owner: the registered payout mandates for an issuance.
export const mandates = (id) => req(`/issuances/${encodeURIComponent(id)}/mandate`)

// Owner: register a BIP340-signed payout mandate. POST with no signature returns
// { sign_this, tag } to sign; resubmit with { signature, signer_xonly }.
export const mandate = (id, body) =>
  req(`/issuances/${encodeURIComponent(id)}/mandate`, { method: 'POST', body })

// Owner: close the offering. POST with no signature returns { sign_this, tag };
// resubmit signed → { close_height, results[] } (per-subscription delivery+release).
export const close = (id, body) =>
  req(`/issuances/${encodeURIComponent(id)}/close`, { method: 'POST', body })

// Owner: the per-subscription settlement records (delivery/release/refund txids).
export const settlements = (id) => req(`/issuances/${encodeURIComponent(id)}/settlements`)

// ── M7 transfer-agent servicing ─────────────────────────────────────────────
// Investor payout mandate (distinct from the issuer mandate above). A
// distribution pays ONLY a registered ordinary Sequentia address, captured with a
// BIP340-signed mandate. Two-phase like the issuer mandate: POST with no
// signature returns { sign_this, tag, note }; resubmit { signature, signer_xonly }
// → { mandate }. The server validates the address is an ordinary Sequentia
// address and REJECTS an enclave key-path (2-of-2) address (400). For M7 the
// only chain is sequentia (USDX distributions); tBTC payouts are the plan's cut.
export const investorMandate = (body) => req('/mandates/investor', { method: 'POST', body })

// The caller's current investor payout mandate for a chain: { chain, mandate|null }.
export const investorMandateGet = (chain = 'sequentia') =>
  req(`/mandates/investor?chain=${encodeURIComponent(chain)}`)

// Owner: open a distribution run in awaiting_funding with a servicing-wallet USDX
// deposit invoice. Body { pool_atoms? | pool_usd?, memo? }. Returns
// { distribution, deposit_address, pay_amount, pay_ccy:"USDX", confs_required,
// note }. The run pays nothing until the deposit confirms and covers the pool.
export const createDistribution = (id, body) =>
  req(`/issuances/${encodeURIComponent(id)}/distributions`, { method: 'POST', body })

// Owner: every distribution run for an issuance: { issuance_id, distributions[] }.
export const listDistributions = (id) =>
  req(`/issuances/${encodeURIComponent(id)}/distributions`)

// Owner: one run plus its per-holder payment table:
// { distribution, payments[], label }.
export const getDistribution = (id, runID) =>
  req(`/issuances/${encodeURIComponent(id)}/distributions/${encodeURIComponent(runID)}`)

// Owner: take the immutable record-date snapshot (requires state funded). Captures
// the holder set at a Sequentia block height and computes pro-rata + withholding.
// Idempotent: { distribution, payments[], label }.
export const snapshotDistribution = (id, runID) =>
  req(`/issuances/${encodeURIComponent(id)}/distributions/${encodeURIComponent(runID)}/snapshot`, {
    method: 'POST',
    body: {},
  })

// Owner: pay NET USDX to each holder's registered mandate address, one payment per
// holder (M5/M6 idempotency). Resumable: { distribution, payments[], label }.
export const executeDistribution = (id, runID) =>
  req(`/issuances/${encodeURIComponent(id)}/distributions/${encodeURIComponent(runID)}/execute`, {
    method: 'POST',
    body: {},
  })

// Owner: freeze or unfreeze a holder at the policy server. REASON REQUIRED. Body
// { holder_aid, frozen, reason } → { holder_aid, frozen, reason, log_url, note }.
export const consoleFreeze = (id, body) =>
  req(`/issuances/${encodeURIComponent(id)}/freeze`, { method: 'POST', body })

// Owner: clawback (full sweep) a holder's confirmed enclave UTXOs into the issuer
// enclave. REASON REQUIRED and it becomes part of the public transparency log.
// Body { holder_aid, reason } → { clawback, txid, atoms, reason, log_url, note }
// (or { clawback, note } with state=empty when the holder has no balance).
export const consoleClawback = (id, body) =>
  req(`/issuances/${encodeURIComponent(id)}/clawback`, { method: 'POST', body })

// The signed-in holder's portal notices inbox, newest first:
// { notices:[{id, aid, kind, body, created_at}] }.
export const notices = () => req('/id/notices')

// ── platform reviewer (admin-session only) ──────────────────────────────────
export const reviewQueue = () => req('/admin/review-queue')

export const reviewDecide = (id, body) =>
  req(`/admin/review/${encodeURIComponent(id)}`, { method: 'POST', body })
