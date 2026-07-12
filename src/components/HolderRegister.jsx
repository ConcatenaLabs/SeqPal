import { useEffect, useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import CopyId from './CopyId'
import * as api from '../lib/api'
import { fmtAssetAmount } from '../lib/format'

// The Registry of Members / cap table for a deployed issuance, rendered ENTIRELY
// from the on-chain holder register (openampd GET /v1/issuer/holders, proxied by
// seqpald). Balances are confirmed enclave balances in atoms; a member appears
// here only on a real on-chain balance > 0. Nothing is read from localStorage
// and no holder is fabricated. The blockchain is the official Registry of
// Members, so this view is a read of it, never a second ledger.
export default function HolderRegister({ iss }) {
  const [state, setState] = useState({ loading: true })

  useEffect(() => {
    let cancelled = false
    setState({ loading: true })
    api
      .issuanceHolders(iss.id)
      .then((res) => {
        if (cancelled) return
        const holders = Object.entries(res.holders || {})
          .map(([aid, atoms]) => ({ aid, atoms: Number(atoms) || 0 }))
          .filter((h) => h.atoms > 0)
          .sort((a, b) => b.atoms - a.atoms)
        setState({ loading: false, holders, total: Number(res.total_atoms) || 0, height: res.height })
      })
      .catch((e) => {
        if (!cancelled) setState({ loading: false, error: e.message })
      })
    return () => {
      cancelled = true
    }
  }, [iss.id])

  const { loading, holders, total, height, error } = state

  return (
    <div className="card overflow-hidden">
      <div className="flex items-center gap-2 border-b border-ink-900/10 px-5 py-3.5">
        <Icon.users width={18} height={18} className="text-ink-700" />
        <h2 className="font-bold text-ink-900">Registry of Members</h2>
        {height != null && (
          <span className="ml-auto text-xs text-ink-700/50">
            scanned at Sequentia height {Number(height).toLocaleString('en-US')}
          </span>
        )}
      </div>

      <div className="px-5 py-4">
        <p className="text-sm leading-relaxed text-ink-700/70">
          The on-chain register, read from the policy server. Each member is a real enclave account
          (AID) holding a confirmed balance in atoms. This is the official Registry of Members: the
          blockchain itself, not a separate ledger.
        </p>

        {loading ? (
          <div className="mt-4 flex justify-center py-8">
            <span className="h-6 w-6 animate-spin rounded-full border-4 border-btc/20 border-t-btc" />
          </div>
        ) : error ? (
          <div className="mt-4 rounded-lg border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700">
            {error}
          </div>
        ) : holders.length === 0 ? (
          <div className="mt-4 rounded-lg bg-ink-900/[0.03] px-4 py-3 text-sm text-ink-700/70">
            No confirmed holders yet. When the mint confirms into the issuer treasury (or a delivery
            reaches an investor) the member appears here from the chain.
          </div>
        ) : (
          <>
            <div className="mt-4 divide-y divide-ink-900/10 rounded-lg border border-ink-900/10">
              {holders.map((h) => {
                const pct = total > 0 ? (h.atoms / total) * 100 : 0
                return (
                  <div key={h.aid} className="flex items-center justify-between gap-4 px-4 py-3 text-sm">
                    <div className="min-w-0">
                      <CopyId value={h.aid} label="holder AID" />
                      <div className="mt-0.5 text-xs text-ink-700/50">{pct.toFixed(2)}% of supply</div>
                    </div>
                    <div className="text-right">
                      <div className="font-mono font-semibold text-ink-900">
                        {fmtAssetAmount(h.atoms, iss.precision || 8, iss.ticker)}
                      </div>
                      <div className="font-mono text-[11px] text-ink-700/50">
                        {h.atoms.toLocaleString('en-US')} atoms
                      </div>
                    </div>
                  </div>
                )
              })}
            </div>
            <div className="mt-3 flex items-center justify-between text-sm">
              <span className="text-ink-700/70">
                {holders.length} member{holders.length === 1 ? '' : 's'}
              </span>
              <span className="font-mono font-semibold text-ink-900">
                {fmtAssetAmount(total, iss.precision || 8, iss.ticker)} total
              </span>
            </div>
            <p className="mt-2 text-[11px] leading-relaxed text-ink-700/55">
              Confirmed balances only. A pending transfer is not counted until it confirms, so this
              register can trail an in-flight transfer by a block or two. Any holder can be audited
              trustlessly against the public balance endpoint on the policy server.
            </p>
          </>
        )}
      </div>
    </div>
  )
}
