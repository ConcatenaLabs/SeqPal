import { useState } from 'react'
import { Icon } from './icons'
import WalletPayout from './WalletPayout'
import * as api from '../lib/api'
import { usePoll } from '../lib/poll'

// Refunds after the release: money the escrow already released to the issuer,
// paid back to subscribers in one payment from the issuer's own wallet, straight
// to each subscriber's wallet. Each subscriber gets what they deposited, in
// USDX. A subscription still held by the escrow is refunded by the escrow itself
// (the closing card's cancel, or the refund anyone can send after the deadline),
// not here. A refund moves no tokens: taking those back is a clawback.
export default function RefundCard({ iss }) {
  const { data, refresh } = usePoll(() => api.listPayouts(iss.id), { intervalMs: 8000, deps: [iss.id] })
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const refunds = (data?.payouts || []).filter((v) => v.payout.kind === 'refund')
  if (!data?.available && refunds.length === 0) return null

  const start = async () => {
    setErr(null)
    setBusy(true)
    try {
      await api.createRefund(iss.id, {})
      await refresh()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="card p-6" data-testid="refund-card">
      <div className="flex items-center gap-2">
        <Icon.wallet width={18} height={18} className="text-seq-600" />
        <h2 className="font-bold text-ink-900">Refund subscribers</h2>
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
        Pay back subscribers whose money was released to you, in one payment from your own wallet, straight to
        each subscriber&rsquo;s wallet. Each gets what they deposited. Money still held in escrow is refunded by the
        escrow itself, not here. A refund does not take the tokens back: that is a clawback, a separate decision.
      </p>
      {data?.available && (
        <button onClick={start} disabled={busy} className="btn-primary mt-3 disabled:opacity-60">
          <Icon.coins width={16} height={16} /> {busy ? 'Preparing…' : 'Refund every released subscription'}
        </button>
      )}
      {err && <p className="mt-2 text-sm font-medium text-rose-600">{err}</p>}
      {refunds.map((v) => (
        <WalletPayout key={v.payout.id} iss={iss} view={v} onChange={refresh} />
      ))}
    </div>
  )
}
