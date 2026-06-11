// Pure financial helpers shared across the issuer and investor surfaces.

export function parseMoney(s) {
  return Number(String(s ?? '').replace(/[^0-9.]/g, '')) || 0
}

export const fmtUSD = (n) => '$' + Math.round(n || 0).toLocaleString()

// Cap-table ownership basis:
//   Native Equity → investment / post-money valuation (pre-money + raise)
//   Equity SPV / Debt → investment / raise (share of the funded position / note)
export function ownershipDenominator(structureId, fields, raiseNum) {
  const premoney = parseMoney(fields?.premoney)
  return structureId === 'native-equity' && premoney > 0 ? premoney + raiseNum : raiseNum
}

export function ownershipPct(structureId, fields, raiseNum, amount) {
  const denom = ownershipDenominator(structureId, fields, raiseNum)
  return denom ? (amount / denom) * 100 : null
}

// Format an amount in the issuance's elected unit of account.
// USD is the default; a Próspera entity may elect BTC (plan v0.72, §1.3/§3.3).
export const unitSymbol = (unit) => (unit === 'BTC' ? '₿' : '$')
export function fmtAmount(n, unit) {
  return unitSymbol(unit) + Math.round(n || 0).toLocaleString()
}

// Escrow & Settlement Fee (plan v0.72, §4.2.3 / §5.5.3 / Appendix D):
// 0.25% per month on subscription funds held in escrow, accrued daily, charged
// for escrow custody, compliance-conditioned release, and on-chain settlement.
// Payable in respect of the holding period whether or not the offering closes.
// US$5,000 minimum per issuance; capped at 3% of the funds held. Over a typical
// ~4-month subscription window this is roughly 1% of the amount raised.
export const ESCROW_FEE_TYPICAL_MONTHS = 4
export function escrowSettlementFee(fundsHeld, months = ESCROW_FEE_TYPICAL_MONTHS, unit = 'USD') {
  const accrued = (fundsHeld || 0) * 0.0025 * months
  const capped = Math.min(accrued, 0.03 * (fundsHeld || 0))
  // The US$5,000 minimum is a dollar figure; on a BTC-denominated raise it
  // applies as a USD-equivalent and cannot be netted against a ₿ amount here.
  return unit === 'BTC' ? capped : Math.max(5000, capped)
}
