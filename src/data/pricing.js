// SeqPal's published, fixed price list. Public and transparent.

export const SETUP_FEES = [
  {
    item: 'Simple Native Equity',
    detail: 'Raises under $500K · private placement',
    amount: '$7,500',
  },
  {
    item: 'Native Equity',
    detail: 'Standard private placement',
    amount: '$12,500',
  },
  {
    item: 'Equity SPV',
    detail: 'Private placement',
    amount: '$17,500',
  },
  {
    item: 'Debt / Yield (unsecured)',
    detail: 'Private placement',
    amount: '$20,000',
  },
  {
    item: 'Debt / Yield — secured add-on',
    detail: 'Captures collateral-structuring complexity',
    amount: '+$5K–$15K',
  },
  {
    item: 'Depository Receipt',
    detail: 'Always a public offering',
    amount: '$22,500',
  },
  {
    item: 'Public-offering surcharge',
    detail: 'Added to Native Equity, Equity SPV, Debt / Yield setup',
    amount: '+$12,500',
  },
]

export const ANNUAL_FEES = [
  { item: 'Annual support — Native Equity', amount: '$5,000' },
  { item: 'Annual support — Equity SPV', amount: '$8,000' },
  { item: 'Annual support — Debt / Yield', amount: '$10,000' },
  { item: 'Annual support — Depository Receipt', amount: '$12,000' },
  {
    item: 'Annual public-reporting support',
    detail: 'Added for non-DR public offerings',
    amount: '+$6,000 / yr',
  },
]

export const TRANSACTION_FEES = [
  {
    item: 'Escrow & Settlement Fee',
    detail:
      'Custody and on-chain settlement of subscription funds held in escrow. Accrued daily, payable whether or not the offering closes — typically ≈1% of the raise over a multi-month window.',
    amount: '0.25% / month on escrowed funds · $5K min · 3% cap',
  },
  {
    item: 'Equity SPV waterfall event',
    detail: 'Charged at the time of a liquidity event',
    amount: '0.50% or $5K min',
  },
  {
    item: 'Debt call / early redemption',
    detail: 'Of outstanding principal',
    amount: '1%',
  },
  {
    item: 'Debt default-handling fee',
    detail: 'Out-of-pocket pass-through plus work fee',
    amount: '$10K–$50K',
  },
  { item: 'DR minting — cash-settled', amount: '0.30%' },
  { item: 'DR minting — in-kind (direct deposit)', amount: '0.25%' },
  { item: 'DR redemption', amount: '0.50%' },
  { item: 'DR management fee (AUM-based)', amount: '0.75% / yr' },
]

export const ID_FEES = [
  {
    item: 'SeqPal ID — individual',
    detail: 'One-time identity & accreditation passport',
    amount: '$20',
  },
  {
    item: 'SeqPal ID — corporate',
    detail: 'One-time KYB passport per entity',
    amount: '$150',
  },
]

// BTC/USD reference used only to evaluate USD-denominated thresholds (e.g. the
// Simple Native Equity tier) for BTC-denominated raises. Matches the plan v0.72
// reference price (~US$63,500); fees themselves are quoted in USD.
export const BTC_USD_REF = 63500

// Computed checkout breakdown for the onboarding demo. `opts` carries data-room
// inputs (raise, collateral, unit) that affect the fixed price.
export function computeSetupCost(structureId, isPublic, opts = {}) {
  let base = {
    'native-equity': 12500,
    'equity-spv': 17500,
    'debt-yield': 20000,
    'depository-receipt': 22500,
  }[structureId]
  let baseLabel = 'Setup'
  let simple = false

  const raiseNum = Number(String(opts.raise || '').replace(/[^0-9.]/g, '')) || 0
  // The $500K Simple-tier threshold is a USD figure; convert a BTC-denominated
  // raise to its USD-equivalent before comparing (a 100-BTC raise is ~$6M, not $100).
  const raiseUsd = opts.unit === 'BTC' ? raiseNum * BTC_USD_REF : raiseNum

  // Simple Native Equity tier for raises up to US$500K.
  if (structureId === 'native-equity' && raiseUsd > 0 && raiseUsd <= 500000) {
    base = 7500
    simple = true
    baseLabel = 'Setup — Simple Native Equity'
  }

  // Secured Debt/Yield add-on ($5K–$15K; representative $10K in the demo).
  let secured = 0
  if (
    structureId === 'debt-yield' &&
    opts.collateral &&
    opts.collateral !== 'Unsecured'
  ) {
    secured = 10000
  }

  const surcharge = isPublic && structureId !== 'depository-receipt' ? 12500 : 0
  return { base, baseLabel, simple, secured, surcharge, total: base + secured + surcharge }
}
