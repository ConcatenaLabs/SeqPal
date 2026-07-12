// Reads of the box's ecosystem services that live outside the OpenAMP policy
// server: the asset registry (/registry) and the market-data feed (/prices).
// Both are same-origin behind the box's Caddy front, which is what the strict
// CSP (connect-src 'self') allows; a dev build without those routes just gets a
// caught failure and the surfaces degrade to "not resolved yet".
//
// The price feed's rate math is never touched here (a standing invariant: the
// any-asset fee exchange rates are correct). We only READ /prices to preview a
// fee figure and to show a registry-resolved reference price.

// GET /prices -> { TICKER: { price, ... }, ... }. Upper-cased keys.
export async function getPrices() {
  const res = await fetch('/prices', { headers: { accept: 'application/json' } })
  if (!res.ok) throw new Error(`price feed ${res.status}`)
  const feed = await res.json()
  const out = {}
  for (const [k, v] of Object.entries(feed || {})) {
    const price = typeof v === 'number' ? v : Number(v?.price)
    if (Number.isFinite(price)) out[k.toUpperCase()] = { ...(typeof v === 'object' ? v : {}), price }
  }
  return out
}

// The Sequence token's reference price, accepting either the pricing key (SEQ)
// or the testnet display ticker (TSEQ). Looked up only because fee value is
// expressed relative to it, exactly as the price server and exchangerates.cpp
// do; SEQ carries no privileged standing in the fee market.
export function seqPrice(prices) {
  return prices?.SEQ?.price || prices?.TSEQ?.price || 0
}

// The canonical, value-preserving fee conversion (never inverted):
//   rate  = (asset_price / SEQ_price) * 1e8      // asset value in SEQ-sats
//   atoms = ceil(policy_fee_sats / rate)         // preserves the fee's value
// so a more valuable asset pays FEWER atoms for the same fee value, which is
// correct. Returns null when a needed price is missing.
export function feeConvertAtoms(prices, assetPrice, policyFeeSats) {
  const seq = seqPrice(prices)
  if (!(seq > 0) || !(assetPrice > 0)) return null
  const rate = (assetPrice / seq) * 1e8
  if (!(rate > 0)) return null
  return Math.max(1, Math.ceil((Number(policyFeeSats) || 0) / rate))
}

// GET /registry/{id} -> the registry entry, or null when the asset is not (yet)
// published. { asset_id, contract:{ name, ticker, precision, entity:{domain} },
// contract_hash, issuance_txid, verified, verified_chain, verified_domain }.
export async function getRegistryEntry(assetId) {
  const res = await fetch(`/registry/${encodeURIComponent(assetId)}`, {
    headers: { accept: 'application/json' },
  })
  if (res.status === 404) return null
  if (!res.ok) throw new Error(`registry ${res.status}`)
  return res.json()
}
