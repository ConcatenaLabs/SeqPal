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

// Platform Services Fee: 3% of capital raised, with a US$10,000 floor.
export function platformServicesFee(raised) {
  return Math.max(10000, 0.03 * (raised || 0))
}
