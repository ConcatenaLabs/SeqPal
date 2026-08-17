// The shape seqpald stores, and the shape the UI reads.
//
// seqpald owns the issuance record: id, owner_aid, name, ticker, structure_id,
// status, supply, precision, confidential, clawback, and the chain fields
// (asset_id, txid, contract_hash, holder_aid, enclave_address) that only the
// deploy path may write. Everything else about the deal (offering type, unit of
// account, entity name, data-room fields, jurisdiction policy) lives in the
// issuance's `terms` object, which is exactly what the on-chain terms_hash
// commits to. These two functions are the only place the two shapes meet.

// The tier a jurisdiction row carries maps directly onto the compiler's access
// levels. A blocked jurisdiction (a mandatory floor) admits nothing, exactly
// like excluded, so it contributes no category.
const TIER_ACCESS = { standard: 'standard', restricted: 'restricted', excluded: 'excluded', blocked: 'excluded' }

// Build the compiler's terms.jurisdictions map from the Step 5 matrix. Only
// admitting jurisdictions (standard or restricted) are emitted; everything else
// is left out, because the catch-all is EXCLUDED by default in seqpald. An
// explicit per-jurisdiction category narrowing (eligCategories) is passed
// through verbatim and clamped by access server-side.
function buildJurisdictions(policy, eligCategories) {
  const out = {}
  for (const [code, tier] of Object.entries(policy || {})) {
    const access = TIER_ACCESS[tier]
    if (access !== 'standard' && access !== 'restricted') continue
    const row = { access }
    const sel = eligCategories?.[code]
    if (Array.isArray(sel) && sel.length > 0) row.elig_categories = sel
    out[code] = row
  }
  return out
}

export function toTerms(d) {
  // The enforcement election (serviced | network | bearer): who can hold the
  // token and who enforces the rules, chosen early in the wizard and committed
  // in the terms like every other deal term.
  const enforcement = d.enforcement || 'serviced'
  const terms = {
    enforcement,
    structure_id: d.structureId || '',
    is_public: !!d.isPublic,
    unit: d.unit || 'USD',
    entity_name: d.entityName || '',
    raise: d.raise || '',
    fields: d.fields || {},
    // policy is kept for display (view() reads it); jurisdictions is what the
    // seqpald compiler consumes into allowed_categories.
    policy: d.policy || {},
    jurisdictions: buildJurisdictions(d.policy, d.eligCategories),
    principal: d.principal || null,
    mint_target: d.mintTarget || '',
    // structure name drives the compiler's velocity defaults (e.g. debt-yield).
    structure: d.structureId || '',
  }

  // A freely-tradable (bearer) issuance carries no transfer restrictions at
  // all: no lockup, no holding-period window, no per-country caps, no category
  // matrix. Emitting none keeps the committed terms honest about that.
  if (enforcement === 'bearer') {
    terms.policy = {}
    terms.jurisdictions = {}
    return terms
  }

  // Lockup: an absolute Sequentia block height, or a number of days converted
  // against the chain tip server-side. Only one is sent.
  const lk = d.lockup
  if (lk?.mode === 'height' && Number(lk.height) > 0) terms.lockup_height = Number(lk.height)
  else if (lk?.mode === 'days' && Number(lk.days) > 0) terms.lockup_days = Number(lk.days)

  // Reg S offshore distribution-compliance window.
  const rs = d.regS
  if (rs?.enabled) {
    const reg = { prefix: (rs.prefix || 'j:US').trim() }
    if (rs.mode === 'height' && Number(rs.height) > 0) reg.until_height = Number(rs.height)
    else if (Number(rs.days) > 0) reg.days = Number(rs.days)
    if (reg.until_height || reg.days) terms.reg_s = reg
  }

  // EU per-member-state offeree caps.
  const caps = {}
  for (const [code, v] of Object.entries(d.euCaps || {})) {
    const n = Number(v)
    if (n > 0) caps[code] = n
  }
  if (Object.keys(caps).length > 0) terms.eu_caps = caps

  // Optional global holder cap.
  if (Number(d.holderCap) > 0) terms.holder_cap = Number(d.holderCap)

  return terms
}

export function view(iss) {
  if (!iss) return null
  const t = iss.terms && typeof iss.terms === 'object' ? iss.terms : {}
  return {
    ...iss,
    enforcement: t.enforcement || iss.enforcement || 'serviced',
    structureId: iss.structure_id || t.structure_id || '',
    isPublic: !!t.is_public,
    unit: t.unit || 'USD',
    entityName: t.entity_name || '',
    raise: t.raise || '',
    fields: t.fields || {},
    policy: t.policy || {},
    principal: t.principal || null,
    mintTarget: t.mint_target || '',
    assetId: iss.asset_id || '',
    txid: iss.txid || '',
    contractHash: iss.contract_hash || '',
    holderAid: iss.holder_aid || '',
    enclaveAddress: iss.enclave_address || '',
    live: iss.status === 'live',
  }
}
