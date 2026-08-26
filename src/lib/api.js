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
// { ok, network, confidential, damp, openamp_ok, issuer_token_ok }. ok is an
// authenticated upstream probe, so a false here means deployment really would
// fail, and the UI can say so before checkout instead of at the mint.
// `confidential` and `damp` are per-deployment capabilities: whether a
// confidential TRANSFER (confidentiality is a per-transfer choice, never an
// asset property), respectively a network-enforced (damp) deploy, would succeed
// here rather than be refused with a 501.
export const health = () => req('/health')

export const challenge = (xonly) =>
  req('/auth/challenge', { method: 'POST', body: { xonly } })

export const register = (body) => req('/auth/register', { method: 'POST', body })

export const login = (body) => req('/auth/login', { method: 'POST', body })

// ── signing in with a wallet that has no OpenAMP account ────────────────────
// The descriptor names the account; the address SeqPal derives from it is what
// the holder signs the challenge with, in whatever wallet they already use.
// Returns { account_id, descriptor, address, challenge, registered }.
export const walletChallenge = (descriptor) =>
  req('/auth/wallet/challenge', { method: 'POST', body: { descriptor } })

export const walletRegister = (body) => req('/auth/wallet/register', { method: 'POST', body })
export const walletLogin = (body) => req('/auth/wallet/login', { method: 'POST', body })

// Attach an OpenAMP account to a wallet-backed SeqPal ID, which is what lets it
// hold restricted assets. Body { xonly, challenge, sig } -- the same tagged
// challenge an OpenAMP account signs in with.
export const attachEnclave = (body) => req('/auth/attach-enclave', { method: 'POST', body })

// ── the wallets one SeqPal ID is held in ────────────────────────────────────
// One identity, more than one wallet. Descriptor wallets are unlimited; an
// OpenAMP account is limited to one, because restricted assets settle in it and
// a second would leave no answer to which.
export const accountWallets = () => req('/account/wallets')
export const linkWallet = (body) => req('/account/wallets', { method: 'POST', body })
export const unlinkWallet = (id) =>
  req(`/account/wallets/${encodeURIComponent(id)}`, { method: 'DELETE' })

// ── admission to a network-enforced asset's whitelist ───────────────────────
// An OpenDAMP whitelist is a list of holding keys the issuer publishes. Nothing
// puts a holder on one automatically, so this is how a verified SeqPal ID asks.
// Body { holding_key, sig?, note? }: without a signature the server answers with
// `sign_this` when the key is not one this ID's wallets derive.
export const requestWhitelist = (id, body) =>
  req(`/issuances/${encodeURIComponent(id)}/whitelist-requests`, { method: 'POST', body })

export const whitelistRequests = (id) =>
  req(`/issuances/${encodeURIComponent(id)}/whitelist-requests`)

// Owner: approve or refuse one request. Approving is a decision; the key reaches
// the published list when a policy change carries it.
export const decideWhitelist = (id, rid, body) =>
  req(`/issuances/${encodeURIComponent(id)}/whitelist-requests/${encodeURIComponent(rid)}/decide`,
    { method: 'POST', body })

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
// total_atoms }, plus seqpal_ids:{policy-server aid -> SeqPal account id} for
// the holders this platform registered. The register is keyed the policy
// server's way and a passport shows the SeqPal id; those are the same string
// only for an ID founded on an OpenAMP account. 403 unless the session owns the
// issuance; the atoms figure is confirmed on-chain balance, the only truthful
// source for "held".
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
// Posting an empty signature asks what to sign and returns { sign_this, tag,
// sign_this_message }; posting a signature records it.
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
// enclave. REASON REQUIRED and it becomes part of the public transparency log. The
// reason is logged BEFORE anything is swept, on either path.
//   Legacy asset (server-held issuer key): swept in this one call →
//     { clawback, txid, atoms, reason, log_url, note }
//     (or { clawback, note } with state=empty when the holder has no balance).
//   External-issuer asset (M9, the entity's own browser key is the issuer half):
//     this call only BUILDS the sweep and broadcasts nothing →
//     { clawback, clawback_id, to_sign:[{input,sighash,pubkey}], atoms, pubkey,
//       two_phase:true, complete_url, reason, log_url, note }.
//     The issuer signs the sighashes with their SeqPal ID key (keys.signClawbackSighash)
//     and posts them to consoleClawbackComplete; only then is the sweep broadcast.
export const consoleClawback = (id, body) =>
  req(`/issuances/${encodeURIComponent(id)}/clawback`, { method: 'POST', body })

// Owner: complete a two-phase (external-issuer) clawback with the issuer's browser
// signatures over the L_claw sighashes. Body { sigs:{ "0":sig, ... } } keyed by
// input index. Success → { clawback, txid, atoms, reason, log_url, note }. Idempotent:
// a completed clawback returns the same seizure txid with no second broadcast.
export const consoleClawbackComplete = (id, cid, body) =>
  req(`/issuances/${encodeURIComponent(id)}/clawback/${encodeURIComponent(cid)}/complete`, {
    method: 'POST',
    body,
  })

// The signed-in holder's portal notices inbox, newest first:
// { notices:[{id, aid, kind, body, created_at}] }.
export const notices = () => req('/id/notices')

// ── M8 secondary market: P2P transfers + refusals + travel rule ─────────────
// The once-per-investor market-abuse / insider-dealing acknowledgment state:
// { acknowledged, version, sign_this, tag, at? }. It gates the transfer surfaces.
export const marketAbuseAckGet = () => req('/id/market-abuse-ack')

// Record the acknowledgment (session). A signature is optional; when supplied it
// is a tagged acknowledgment by the caller's own key. Returns { acknowledged, at }.
export const marketAbuseAck = (body = {}) =>
  req('/id/market-abuse-ack', { method: 'POST', body })

// Build a policy-co-signed holder-to-holder transfer. Body { asset, to_aid,
// atoms, confidential? }. confidential is the per-transfer choice: blind this
// transfer's amount and asset from outside observers (the registrar still sees
// everything); it needs health.confidential, else the build is refused 501.
// Returns { transfer_id, oa_id, tx, to_sign:[{input,sighash,pubkey}],
// recipient_preflight:{eligible,reasons}, travel_rule:{originator,beneficiary,
// captured_via} }. A 403 with data.requirement === 'market_abuse_ack' means the
// acknowledgment is unrecorded; a 404 means the beneficiary is not a registered
// SeqPal identity (a P2P transfer captures both counterparties).
export const buildTransfer = (body) => req('/transfers', { method: 'POST', body })

// Complete a built transfer with the originator's signatures. Body { sigs:{input:sig} }.
// Success → { transfer_id, txid, state:'settled' }. A REFUSAL is a first-class 403:
// the thrown ApiError.data carries { state:'refused', refused:true, reason, log_url }.
export const completeTransfer = (id, body) =>
  req(`/transfers/${encodeURIComponent(id)}/complete`, { method: 'POST', body })

// The caller's P2P transfers as originator, newest first: { transfers:[{transfer,
// travel_rule}], log_url }. Each transfer carries its state (incl. a refusal reason).
export const listTransfers = () => req('/transfers')

// ── M8 listings authorization (public; serves the SeqDEX handover) ──────────
// With ?asset=<id>: that asset's authorization or a not-authorized stub. Otherwise
// every authorized listing, optionally filtered by ?issuer=<aid>. A venue reads
// this to learn which assets an issuer authorized for listing; it can never GRANT
// eligibility (that is stamped by SeqPal ID, checked at /api/eligibility).
export const listings = ({ asset, issuer } = {}) => {
  const q = new URLSearchParams()
  if (asset) q.set('asset', asset)
  if (issuer) q.set('issuer', issuer)
  const s = q.toString()
  return req(`/listings${s ? `?${s}` : ''}`)
}

// Owner: grant or revoke a listing authorization for the issuance's asset. POST
// with no signature records it; an optional signature by the issuer's own key
// (tag seqpal-listing-v1) is recorded when present. Body { authorized, venues[],
// signature?, signer_xonly? } → { listing }.
export const grantListing = (id, body) =>
  req(`/issuances/${encodeURIComponent(id)}/listing`, { method: 'POST', body })

// ── M8 Depository-Receipt programme (owner) ─────────────────────────────────
// The programme state, its ops, and the chain-derived supply: { program, ops[],
// circulating_atoms, height, supply_source:'chain-derived', custodian }.
export const drProgram = (id) => req(`/issuances/${encodeURIComponent(id)}/dr`)

// Enable the programme and enforce the US-person exclusion as a REAL policy-server
// j:US category deny (not a display string): { program, note }.
export const drEnable = (id) =>
  req(`/issuances/${encodeURIComponent(id)}/dr/enable`, { method: 'POST', body: {} })

// Mint = OA-6 reissuance into the custodian enclave. Body { atoms, target_aid?,
// request_id? } (request_id is the idempotency key). Success → { op, reissue_txid,
// state, circulating_atoms, height }. A 202 { state:'reblinding', request_id } means
// the reissuance token is re-blinding; retry with the SAME request_id shortly.
export const drMint = (id, body) =>
  req(`/issuances/${encodeURIComponent(id)}/dr/mint`, { method: 'POST', body })

// Redeem = OA-5 burn from a custodied enclave, reducing chain-derived supply. Body
// { atoms, holder_aid?, request_id? } → { op, burn_txid, state, circulating_atoms,
// height }. Idempotent by request_id; a lost write reconciles from the public log.
export const drRedeem = (id, body) =>
  req(`/issuances/${encodeURIComponent(id)}/dr/redeem`, { method: 'POST', body })

// The chain-derived circulating supply alone: { asset, circulating_atoms, height,
// supply_source:'chain-derived' }. Never a stored counter.
export const drSupply = (id) => req(`/issuances/${encodeURIComponent(id)}/dr/supply`)

// ── bearer (freely-tradable) issuance ───────────────────────────────────────
// Record the signed bearer attestation before a freely-tradable deploy. Body
// { issuance_id, no_us_nexus, risk_accepted, aid, pubkey, sig } where sig is
// the session key's tagged signature (keys.signBearerAttestation, tag
// seqpal-bearer-attestation-v1) over sha256 of the canonical JSON of
// { issuance_id, no_us_nexus, risk_accepted, aid }. Owner session only. The
// deploy refuses a bearer issuance whose attestation is not on record.
export const bearerAttestation = (id, body) =>
  req(`/issuances/${encodeURIComponent(id)}/bearer-attestation`, { method: 'POST', body })

// ── supervision: court-ordered freezes on a bearer asset ────────────────────
// The supervision status for a bearer issuance: { supervised, enforcement,
// asset, operational_key, recovery_key, pause, freezes:[{targethash, records,
// txids, target?}], ops:[{id, kind, target, reason, order_hash, state, txid,
// channel}] }. freezes is the consensus register (what the chain enforces);
// ops is this platform's operation history carrying the human context (the
// reason and the order fingerprint). Owner session only.
export const supervision = (id) => req(`/issuances/${encodeURIComponent(id)}/supervision`)

// Start a freeze or unfreeze; action is 'freeze' | 'unfreeze'. Body
// { target_address, reason, order_hash } (order_hash = sha256 hex of the
// court/regulator order document, computed in the browser; it is recorded
// publicly beside the freeze; an unfreeze may instead name { freeze_id }).
// Two-phase like a clawback: this BUILDS the operation and returns
// { freeze_id | unfreeze_id, to_sign, ... } where to_sign is the raw 64-hex
// 32-byte node-produced message the CURRENT operational key must sign
// (keys.signSupervisionMessage); nothing takes effect until complete. On a
// bearer deploy the operational key is the issuer's own session key.
export const supervisionStart = (id, action, body) =>
  req(`/issuances/${encodeURIComponent(id)}/supervision/${encodeURIComponent(action)}`, {
    method: 'POST',
    body,
  })

// Complete a supervision operation with the issuer's signature. The build
// returns `record`, the fields the message commits to, and the issuer's wallet
// rebuilds and signs it from those rather than being handed a digest.
// Body { sig }. Returns { freeze_id | unfreeze_id, txid,
// state, channel? }; a replay returns the same txid with idempotent:true.
export const supervisionComplete = (id, action, opId, body) =>
  req(
    `/issuances/${encodeURIComponent(id)}/supervision/${encodeURIComponent(action)}/${encodeURIComponent(opId)}/complete`,
    { method: 'POST', body },
  )

// ── holder list and frozen coins on a network-enforced token ────────────────
// The token's rules live in a list the network itself reads on every transfer,
// so the two controls an issuer keeps are changing who may hold it and stopping
// one specific coin. Both take effect when the updated list is published and the
// on-chain rules output has moved onto it, never the moment the issuer presses a
// button.
//
// The read: { network_enforced, max_coins_per_transfer, published: { seq,
// commitment, holders:[{key, can_send_from_block?, can_receive_from_block?}],
// frozen_coin_count, frozen_coin_prints }, ops:[...] }. ops is this platform's
// change history, carrying the reason and the order fingerprint beside the
// chain facts. Owner session only.
export const policy = (id) => req(`/issuances/${encodeURIComponent(id)}/policy`)

// Start a change; action is 'freeze' | 'unfreeze'. Body { holders:[key],
// coins:[{txid,vout}], reason, order_hash } where order_hash is the sha256 of
// the order document, computed in the browser: only the fingerprint is sent and
// only the fingerprint is published. Returns { op_id, to_sign, ... }; nothing is
// published until complete. A replay of the same order against the same targets
// resumes the same change rather than opening a second one.
export const policyStart = (id, action, body) =>
  req(`/issuances/${encodeURIComponent(id)}/policy/${encodeURIComponent(action)}`, {
    method: 'POST',
    body,
  })

// Complete a change with the issuer's signature over the policy snapshot (the
// wallet signs its hash under the policy server's own tag), plus
// the two values only the issuer's registrar can produce (the recompiled rules
// program and the finished rules transaction). Body { sig, verifier_program,
// verifier_address?, rules_tx }. Without the registrar values the call answers
// 409 carrying the document to compile against and the commands to run, which is
// a step in the flow rather than a failure.
export const policyComplete = (id, opId, body) =>
  req(`/issuances/${encodeURIComponent(id)}/policy/${encodeURIComponent(opId)}/complete`, {
    method: 'POST',
    body,
  })

// ── shareholder actions (corporate actions) ─────────────────────────────────
// Owner: declare a dividend or a vote. Body { kind:'dividend'|'vote', memo,
// record_height?, pool_atoms? (dividend), choices? (vote) } → { action }. The
// snapshot of who holds what is taken from the on-chain register at the first
// pass at or after the record height, never restated.
export const createAction = (id, body) =>
  req(`/issuances/${encodeURIComponent(id)}/actions`, { method: 'POST', body })

// Owner: every declared action for an issuance: { actions:[...] }.
export const listActions = (id) => req(`/issuances/${encodeURIComponent(id)}/actions`)

// One action with its status, snapshot summary, tally, and claims. Readable by
// any holder who has the action id (the claim link).
export const getAction = (actionId) => req(`/actions/${encodeURIComponent(actionId)}`)

// Claim an action as a holder. Body { pubkey, outpoints, payout_address|choice,
// sig } where sig is the tagged holding proof (keys.signHoldingProof, tag
// seqpal-holding-proof-v1) binding the outpoints and the payout address or
// ballot choice to the claimant's key.
export const claimAction = (actionId, body) =>
  req(`/actions/${encodeURIComponent(actionId)}/claim`, { method: 'POST', body })

// ── platform reviewer (admin-session only) ──────────────────────────────────
export const reviewQueue = () => req('/admin/review-queue')

export const reviewDecide = (id, body) =>
  req(`/admin/review/${encodeURIComponent(id)}`, { method: 'POST', body })
