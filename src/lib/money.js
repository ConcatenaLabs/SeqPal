// Display helpers for the M5 money surfaces. Amounts from seqpald are integers
// in the rail's base unit: USDX atoms and native BTC sats are 8-decimal; a
// SIMULATED fiat figure is USD minor units (cents). Nothing is invented here:
// every figure is a server value rendered in the fee/payment asset's OWN units.
import { scaleDown, trimZeros } from './format'

// Format a rail base-unit integer for its currency. USDX and BTC are 8-decimal;
// USD (fiat) is minor units. The label is the asset's own ticker, never "sat".
export function fmtRail(amount, ccy) {
  const c = String(ccy || '').toUpperCase()
  const n = Number(amount) || 0
  if (c === 'USD') return `$${(n / 100).toFixed(2)}`
  if (c === 'BTC') return `${trimZeros(scaleDown(amount, 8))} BTC`
  // USDX and any other 8-decimal Sequentia asset.
  return `${trimZeros(scaleDown(amount, 8))} ${c || 'USDX'}`
}

// Per-rail presentation: label, whether it is SIMULATED, and the honest trust
// description. Rail choice is ALWAYS the payer's, never inferred from
// jurisdiction; this only describes the settlement model of the chosen rail.
export const RAIL_META = {
  usdx: {
    label: 'USDX',
    chain: 'Sequentia',
    simulated: false,
    trust:
      'USDX settles on Sequentia. You pay to an escrow address of your own subscription that no SeqPal wallet holds a key to: before the close it can only come back to your refund address, and from the close the issuer\u2019s key can also release it, in a transaction that pays the issuer at least the offering\u2019s minimum. SeqPal asks the issuer for that signature only after your tokens are delivered. Your payment is read from the chain and credited once it confirms; nothing is final at 0 confirmations, because a Sequentia block is only as final as its Bitcoin anchor is deep.',
  },
  btc: {
    label: 'Bitcoin (testnet4)',
    chain: 'Bitcoin testnet4',
    simulated: false,
    trust:
      'Native BTC is genuinely cross-chain: your payment confirms on Bitcoin testnet4, then delivery is co-signed on Sequentia. This is registrar-style settlement, not atomic, and the escrow is custodial: your BTC is held in a SeqPal wallet until the close, which pays the issuer or refunds you. The USD/BTC rate is captured when the deposit confirms.',
  },
  card: {
    label: 'Card',
    chain: 'Simulated processor',
    simulated: true,
    trust:
      'SIMULATED payment rail: no real card is charged. The checkout runs the same escrow and closing state machine a real payment would, and every derived figure is labeled SIMULATED.',
  },
  bank: {
    label: 'Bank transfer',
    chain: 'Simulated processor',
    simulated: true,
    trust:
      'SIMULATED payment rail: no real bank transfer is processed. The checkout runs the same escrow and closing state machine a real payment would, and every derived figure is labeled SIMULATED.',
  },
}

export const railMeta = (rail) => RAIL_META[rail] || { label: rail, chain: '', simulated: false, trust: '' }

// The settlement model of a rail, labeled honestly for the close surface. USDX
// is held by the escrow covenant: the tokens are delivered first, then one
// release the issuer signs pays the raise. Native BTC is cross-chain registrar
// settlement out of a custodial SeqPal wallet, not atomic; the simulated fiat
// rails deliver on chain against a simulated payment. atomic is true only for a
// settlement whose delivery and release were one transaction, which the close
// surface then shows as a single txid.
export function settlementModel(rail) {
  switch (rail) {
    case 'usdx':
      return { atomic: false, label: 'tokens delivered first, then released to the issuer in one transaction it signs' }
    case 'btc':
      return { atomic: false, label: 'cross-chain registrar settlement from a SeqPal-held wallet, not atomic' }
    case 'card':
    case 'bank':
      return { atomic: false, label: 'simulated payment, on-chain delivery' }
    default:
      return { atomic: false, label: '' }
  }
}

// The escrow lifecycle chip descriptor for a subscription state. Nothing is
// "final": in_escrow means the deposit confirmed to N, settled means delivered
// at close. Colours reuse the Badge palette.
export function subStateChip(state) {
  switch (state) {
    case 'created':
      return { color: 'amber', label: 'Awaiting deposit' }
    case 'in_escrow':
      return { color: 'seq', label: 'In escrow' }
    case 'settled':
      return { color: 'emerald', label: 'Settled: delivered' }
    case 'refunded':
      return { color: 'slate', label: 'Refunded' }
    case 'expired':
      return { color: 'slate', label: 'Expired' }
    default:
      return { color: 'slate', label: state || 'unknown' }
  }
}

// The refund-address network label + placeholder for a rail.
export function refundHint(rail) {
  if (rail === 'btc') return { label: 'Bitcoin testnet4 refund address', ph: 'tb1...', required: true }
  if (rail === 'usdx') return { label: 'Sequentia refund address', ph: 'tb1... / ex1...', required: true }
  return { label: 'Refund reference (optional)', ph: 'optional for the simulated rail', required: false }
}
