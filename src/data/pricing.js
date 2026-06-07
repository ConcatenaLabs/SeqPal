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
    item: 'Platform Services Fee',
    detail:
      'Technology, escrow, document automation, transfer-agent setup & SeqPal ID gate. Invoiced regardless of amount raised.',
    amount: 'Capped at 3% of capital raised · $10K floor',
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

// Computed checkout breakdown for the onboarding demo.
export function computeSetupCost(structureId, isPublic) {
  const base = {
    'native-equity': 12500,
    'equity-spv': 17500,
    'debt-yield': 20000,
    'depository-receipt': 22500,
  }[structureId]
  const surcharge = isPublic && structureId !== 'depository-receipt' ? 12500 : 0
  return { base, surcharge, total: base + surcharge }
}
