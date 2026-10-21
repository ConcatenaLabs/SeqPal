import { useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import CopyId from './CopyId'
import * as api from '../lib/api'
import { fmtRail } from '../lib/money'

// One payment to many wallets at once: a distribution's holders, or a refund's
// subscribers. The issuer's own wallet makes it from the payment list this page
// gives, and the issuer brings back its reference; seqpald records it only when
// the payment names exactly the list, then follows it: paid to every wallet,
// collected by each wallet when it next opens, and whatever is not collected by
// the deadline comes back to the issuer.
//
// Default view: how many are paid, the total, the most the fee can be, the
// deadline, who is left out and why. The keys and per-wallet states sit in
// "Details". This page never names the service that carries the payment: the
// people paid read "paid to your wallet".

const STATE = {
  planned: { label: 'Ready to pay', color: 'seq' },
  paying: { label: 'On its way', color: 'amber' },
  paid: { label: 'Paid to wallets', color: 'emerald' },
  complete: { label: 'Complete', color: 'emerald' },
}

const ROW = {
  planned: 'In the payment',
  left_out: 'Not paid',
  pending: 'On its way',
  paid: 'Waiting to be collected',
  collected: 'Collected',
  returned: 'Came back to you',
}

function when(t) {
  if (!t) return null
  return new Date(Number(t) * 1000).toLocaleString('en-GB', {
    day: 'numeric', month: 'long', year: 'numeric', hour: '2-digit', minute: '2-digit', timeZone: 'UTC',
  }) + ' UTC'
}

export default function WalletPayout({ iss, view, onChange }) {
  const p = view.payout
  const rows = view.rows || []
  const ccy = view.asset_label || 'USDX'
  const amt = (a) => fmtRail(a, ccy)
  const who = p.kind === 'refund' ? 'subscriber' : 'holder'
  const [ref, setRef] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const [details, setDetails] = useState(false)
  const leftOut = rows.filter((r) => r.state === 'left_out')
  const count = (s) => rows.filter((r) => r.state === s).length
  const m = STATE[p.state] || { label: p.state, color: 'slate' }

  const record = async () => {
    setErr(null)
    setBusy(true)
    try {
      await api.recordPayoutReference(iss.id, p.id, ref.trim())
      setRef('')
      onChange && onChange()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="mt-4 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] p-4" data-testid="wallet-payout">
      <div className="flex flex-wrap items-center gap-2">
        <Badge color={m.color}>{m.label}</Badge>
        <span className="text-sm font-semibold text-ink-900">
          {p.kind === 'refund' ? 'Refund' : 'Pay'} {p.holders} {who}{p.holders === 1 ? '' : 's'}: {amt(p.total_atoms)}
        </span>
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/80">{view.summary}</p>

      <div className="mt-3 grid gap-3 sm:grid-cols-3">
        <div className="rounded-lg bg-white px-3 py-2">
          <div className="text-xs text-ink-700/60">Total</div>
          <div className="font-mono text-sm font-semibold text-ink-900">{amt(p.total_atoms)}</div>
        </div>
        <div className="rounded-lg bg-white px-3 py-2">
          <div className="text-xs text-ink-700/60">{p.state === 'planned' ? 'Fee, at most' : 'Collected'}</div>
          <div className="font-mono text-sm font-semibold text-ink-900">
            {p.state === 'planned' ? amt(p.fee_bound) : amt(p.collected_atoms)}
          </div>
        </div>
        <div className="rounded-lg bg-white px-3 py-2">
          <div className="text-xs text-ink-700/60">Collect by</div>
          <div className="text-sm font-semibold text-ink-900">
            {p.deadline ? when(p.deadline) : 'set when it is paid, a few weeks out'}
          </div>
        </div>
      </div>

      {leftOut.length > 0 && (
        <div className="mt-3 rounded-lg border border-amber-200 bg-amber-50 px-3 py-2">
          <div className="text-xs font-semibold text-amber-800">
            Not in this payment ({leftOut.length}): their share stays with you
          </div>
          <ul className="mt-1 space-y-0.5 text-xs text-amber-800/90">
            {leftOut.map((r) => (
              <li key={r.holder_aid}>
                <span className="font-mono">{r.holder_aid.slice(0, 12)}</span> {r.reason}
              </li>
            ))}
          </ul>
        </div>
      )}

      {p.state === 'planned' && (
        <div className="mt-4">
          <div className="text-[11px] font-semibold uppercase tracking-wide text-ink-700/60">1. Pay from your wallet</div>
          <p className="mt-1 text-xs leading-relaxed text-ink-700/75">
            Download the payment list and pay it from your own wallet, in {ccy}. Your wallet shows the exact fee
            before it pays, in {ccy}, and gives you a payment reference.
          </p>
          <a href={api.payoutListUrl(iss.id, p.id)} className="btn-outline mt-2 inline-flex text-xs" download>
            <Icon.doc width={14} height={14} /> Download the payment list
          </a>
          <div className="mt-3 text-[11px] font-semibold uppercase tracking-wide text-ink-700/60">2. Bring back its reference</div>
          <input
            className="input mt-1 font-mono text-xs"
            spellCheck={false}
            placeholder="payment reference (64 characters)"
            value={ref}
            onChange={(e) => setRef(e.target.value)}
            aria-label="payment reference"
          />
          <button onClick={record} disabled={busy || ref.trim().length !== 64} className="btn-primary mt-2 disabled:opacity-50">
            <Icon.check width={16} height={16} /> {busy ? 'Checking' : 'Record the payment'}
          </button>
          <p className="mt-1 text-[11px] text-ink-700/60">
            It is recorded only if the payment pays exactly this list: every {who}, each amount, nobody else.
          </p>
        </div>
      )}

      {(p.state === 'paid' || p.state === 'complete') && (
        <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-xs text-ink-700/80">
          <span>Collected: {count('collected')}</span>
          <span>Waiting to be collected: {count('paid')}</span>
          <span>Came back to you: {count('returned')}{p.returned_atoms ? ` (${amt(p.returned_atoms)})` : ''}</span>
        </div>
      )}

      {err && <p className="mt-2 text-xs font-medium text-rose-600">{err}</p>}

      <button onClick={() => setDetails(!details)} className="mt-3 text-xs font-medium text-seq-600 hover:underline">
        {details ? 'Hide details' : 'Details'}
      </button>
      {details && (
        <div className="mt-2 space-y-2 text-xs">
          {p.reference && (
            <div>
              Payment reference <CopyId value={p.reference} label="payment reference" />
            </div>
          )}
          {p.round_txid && (
            <div>
              Paid in transaction <CopyId value={p.round_txid} kind="tx" label="payment txid" />
            </div>
          )}
          {p.return_txids &&
            p.return_txids.split(',').map((t) => (
              <div key={t}>
                Returned in transaction <CopyId value={t} kind="tx" label="return txid" />
              </div>
            ))}
          <div className="overflow-x-auto rounded-lg border border-ink-900/10 bg-white">
            <table className="w-full min-w-[560px]">
              <thead>
                <tr className="border-b border-ink-900/10 text-left text-[11px] uppercase tracking-wide text-ink-700/60">
                  <th className="px-2 py-1.5">{who === 'holder' ? 'Holder' : 'Subscriber'}</th>
                  <th className="px-2 py-1.5">Wallet key</th>
                  <th className="px-2 py-1.5 text-right">Amount</th>
                  <th className="px-2 py-1.5">State</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-ink-900/5">
                {rows.map((r) => (
                  <tr key={r.holder_aid}>
                    <td className="px-2 py-1.5 font-mono">{r.holder_aid.slice(0, 12)}</td>
                    <td className="px-2 py-1.5 font-mono">{r.key ? `${r.key.slice(0, 16)}…` : ''}</td>
                    <td className="px-2 py-1.5 text-right font-mono">{amt(r.atoms)}</td>
                    <td className="px-2 py-1.5">{ROW[r.state] || r.state}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  )
}
