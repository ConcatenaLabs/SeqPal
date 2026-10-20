import { fmtAssetAmount } from '../lib/format'

// A live token's supply as its issuer reads it: what exists now, what was
// created at issuance and since, what can still be created, and the most there
// will ever be. `supply` is seqpald's supply view (GET /issuances/{id}/supply,
// or the `supply` field of the DR programme's reads).
export default function SupplyFigures({ supply, className = '' }) {
  if (!supply) return null
  const p = supply.precision ?? 8
  const t = supply.ticker
  const rows = [
    ['In circulation now', fmtAssetAmount(supply.circulating_atoms || 0, p, t)],
    ['Created at issuance', fmtAssetAmount(supply.initial_atoms || 0, p, t)],
    ['Created since', fmtAssetAmount(supply.minted_atoms || 0, p, t)],
    ['Can still be created', fmtAssetAmount(supply.room_atoms || 0, p, t)],
    ['The most there will ever be', fmtAssetAmount(supply.max_atoms || 0, p, t)],
  ]
  return (
    <div data-testid="supply-figures" className={'rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3 ' + className}>
      <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">Supply</div>
      <dl className="mt-2 divide-y divide-ink-900/10 text-sm">
        {rows.map(([k, v]) => (
          <div key={k} className="flex items-center justify-between gap-4 py-1.5">
            <dt className="text-ink-700/70">{k}</dt>
            <dd data-supply-row={k} className="font-mono font-medium text-ink-900">
              {v}
            </dd>
          </div>
        ))}
      </dl>
      <p data-testid="supply-words" className="mt-2 text-xs leading-relaxed text-ink-700/80">
        {supply.words}
      </p>
    </div>
  )
}
