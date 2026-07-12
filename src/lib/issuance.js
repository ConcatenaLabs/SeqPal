// The shape seqpald stores, and the shape the UI reads.
//
// seqpald owns the issuance record: id, owner_aid, name, ticker, structure_id,
// status, supply, precision, confidential, clawback, and the chain fields
// (asset_id, txid, contract_hash, holder_aid, enclave_address) that only the
// deploy path may write. Everything else about the deal (offering type, unit of
// account, entity name, data-room fields, jurisdiction policy) lives in the
// issuance's `terms` object, which is exactly what the on-chain terms_hash
// commits to. These two functions are the only place the two shapes meet.

export function toTerms(d) {
  return {
    structure_id: d.structureId || '',
    is_public: !!d.isPublic,
    unit: d.unit || 'USD',
    entity_name: d.entityName || '',
    raise: d.raise || '',
    fields: d.fields || {},
    policy: d.policy || {},
    principal: d.principal || null,
    mint_target: d.mintTarget || '',
  }
}

export function view(iss) {
  if (!iss) return null
  const t = iss.terms && typeof iss.terms === 'object' ? iss.terms : {}
  return {
    ...iss,
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
