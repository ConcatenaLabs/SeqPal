import { useEffect, useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import { getPrices, seqPrice, feeConvertAtoms } from '../lib/market'
import { fmtAssetAmount, fmtReference } from '../lib/format'

// The network fee reference seqpald uses to derive fee_convert_atoms, in the
// Sequence token's base units (SEQPALD_POLICY_FEE_SATS default). This is a VALUE
// basis, not a Bitcoin sat/vB rate: every asset pays the value-equivalent in its
// OWN units, which is the whole point of the open, no-privileged-coin fee market.
const POLICY_FEE_BASE_UNITS = 1000

// The network-fees panel (M3 requirement 7 / item 121). A restricted asset is
// one row among equals in the fee market: its holders pay transfer fees in the
// transacted asset's OWN units by default, never in "sat/vB" (a sat is a Bitcoin
// unit). The per-transfer figure is derived from the price feed with the
// canonical value-preserving conversion, so a more valuable asset pays fewer
// atoms for the same fee value.
export default function NetworkFees({ iss }) {
  const [state, setState] = useState({ loading: true })

  useEffect(() => {
    let cancelled = false
    setState({ loading: true })
    getPrices()
      .then((prices) => {
        if (cancelled) return
        const ticker = (iss.ticker || '').toUpperCase()
        const assetPrice = prices[ticker]?.price || 0
        const seq = seqPrice(prices)
        const atoms = feeConvertAtoms(prices, assetPrice, POLICY_FEE_BASE_UNITS)
        // Fee value from the SEQ value basis: (base units / 1e8) * SEQ price.
        const feeValueUSD = seq > 0 ? (POLICY_FEE_BASE_UNITS / 1e8) * seq : null
        setState({ loading: false, assetPrice, seq, atoms, feeValueUSD, resolved: assetPrice > 0 })
      })
      .catch((e) => {
        if (!cancelled) setState({ loading: false, error: e.message })
      })
    return () => {
      cancelled = true
    }
  }, [iss.ticker])

  const { loading, atoms, feeValueUSD, resolved, error } = state
  const precision = iss.precision || 8

  return (
    <div className="card overflow-hidden">
      <div className="flex items-center gap-2 border-b border-ink-900/10 px-5 py-3.5">
        <Icon.coins width={18} height={18} className="text-ink-700" />
        <h2 className="font-bold text-ink-900">Network fees</h2>
        <Badge color="seq" className="ml-auto">
          Price-fed
        </Badge>
      </div>

      <div className="space-y-4 px-5 py-4 text-sm">
        <p className="leading-relaxed text-ink-700/80">
          Sequentia has an open fee market with no privileged coin. A transfer of this asset pays
          its fee, by default, in the transacted asset itself, quoted in that asset's own units,
          never in a Bitcoin sat rate. The Sequence token (SEQ) is one accepted fee asset among
          equals; a block proposer chooses what it accepts.
        </p>

        <dl className="divide-y divide-ink-900/10">
          <div className="flex items-center justify-between gap-4 py-2.5">
            <dt className="text-ink-700/70">Fee asset (default)</dt>
            <dd className="text-right font-medium text-ink-900">
              {iss.ticker || 'this asset'} · one row among equals
            </dd>
          </div>
          <div className="flex items-center justify-between gap-4 py-2.5">
            <dt className="text-ink-700/70">Network fee reference</dt>
            <dd className="text-right font-medium text-ink-900">
              {POLICY_FEE_BASE_UNITS.toLocaleString('en-US')} SEQ base units / operation
            </dd>
          </div>
          <div className="flex items-center justify-between gap-4 py-2.5">
            <dt className="text-ink-700/70">Per-transfer estimate</dt>
            <dd className="text-right font-medium text-ink-900">
              {loading ? (
                <span className="text-ink-700/50">reading price feed…</span>
              ) : resolved && atoms ? (
                <>
                  {fmtAssetAmount(atoms, precision, iss.ticker)}
                  <span className="block font-mono text-[11px] font-normal text-ink-700/60">
                    {atoms.toLocaleString('en-US')} atoms
                  </span>
                </>
              ) : (
                <span className="text-ink-700/50">no reference price yet</span>
              )}
            </dd>
          </div>
          {feeValueUSD != null && (
            <div className="flex items-center justify-between gap-4 py-2.5">
              <dt className="text-ink-700/70">Fee value (reference currency)</dt>
              <dd className="text-right font-medium text-ink-900">
                ≈ {fmtReference(feeValueUSD, 'USD')}
              </dd>
            </div>
          )}
        </dl>

        <div className="rounded-lg bg-ink-900/[0.03] px-4 py-3 text-xs leading-relaxed text-ink-700/70">
          The conversion is value-preserving:{' '}
          <span className="font-mono">rate = (asset_price / SEQ_price) × 1e8</span>,{' '}
          <span className="font-mono">atoms = ceil(fee / rate)</span>. A more valuable asset pays
          fewer atoms for the same fee value, which is correct. The price basis is the
          offering-reference entry the deploy seeded into the market-data feed, labeled as such.
        </div>

        {error && (
          <p className="text-xs text-ink-700/50">
            The market-data feed was unreachable from this browser, so the live per-transfer
            estimate is unavailable here. The mechanic above is unchanged.
          </p>
        )}
      </div>
    </div>
  )
}
