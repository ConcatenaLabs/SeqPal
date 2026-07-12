import { useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import * as api from '../lib/api'
import { usePoll } from '../lib/poll'
import { railMeta } from '../lib/money'
import DepositBox from './money/DepositBox'

// The SeqPal platform setup fee: invoiced and COLLECTED before deploy, payable by
// the issuer's choice of rail (on-chain USDX/tBTC or the SIMULATED fiat rail). An
// unpaid setup fee blocks the mint. The fee is shown in USD via the price server.
// The escrow fee is not collected here: it accrues on real escrow balances and is
// deducted at release.
export default function PlatformFeeCard({ iss }) {
  const { data, refresh } = usePoll(() => api.fees(iss.id), { intervalMs: 5000, deps: [iss.id] })
  const [rail, setRail] = useState('usdx')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const [pay, setPay] = useState(null) // { deposit_address,... } or { checkout }

  const setup = (data?.invoices || []).find((i) => i.kind === 'setup')
  const paid = setup?.state === 'paid'
  const feeUsd = data?.setup_fee_usd ?? 0
  const escrowBps = data?.escrow_fee_bps ?? 0

  const submit = async () => {
    setErr(null)
    setBusy(true)
    try {
      const res = await api.payFee(iss.id, { kind: 'setup', rail })
      if (res.already_paid) {
        setPay(null)
        refresh()
      } else {
        setPay(res)
        refresh()
      }
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  // Rail choice is the issuer's. If the BTC rail is not configured on this
  // deployment the server refuses with a clear message, surfaced inline.
  const rails = [
    ['usdx', 'USDX'],
    ['btc', 'Bitcoin (testnet4)'],
    ['card', 'Card (SIMULATED)'],
    ['bank', 'Bank (SIMULATED)'],
  ]

  return (
    <div className="card p-6">
      <div className="flex items-center gap-2">
        <Icon.receipt width={18} height={18} className="text-btc-600" />
        <h2 className="font-bold text-ink-900">SeqPal setup fee</h2>
        <Badge color={paid ? 'emerald' : 'amber'} className="ml-auto">
          {paid ? 'Paid' : 'Due'}
        </Badge>
      </div>
      <div className="mt-3 flex items-baseline gap-2">
        <span className="text-2xl font-extrabold text-ink-900">
          ${Number(feeUsd).toLocaleString('en-US')}
        </span>
        <span className="text-xs text-ink-700/60">one-time, in USD via the price server</span>
      </div>
      <p className="mt-2 text-xs leading-relaxed text-ink-700/70">
        The setup fee is collected before deploy: an unpaid fee blocks the mint. A separate escrow
        fee of {escrowBps} bps accrues on real escrow balances and is deducted at release, not here.
      </p>

      {paid ? (
        <div className="mt-4 flex items-center gap-2 rounded-lg bg-emerald-50 px-3 py-2.5 text-sm font-medium text-emerald-700">
          <Icon.check width={16} height={16} /> Setup fee paid
          {setup?.funds_simulated && (
            <Badge color="amber" className="ml-auto">
              SIMULATED
            </Badge>
          )}
        </div>
      ) : pay && pay.deposit_address ? (
        <div className="mt-4">
          <DepositBox
            rail={pay.rail}
            address={pay.deposit_address}
            payAmount={pay.pay_amount}
            payCcy={pay.pay_ccy}
            confsRequired={pay.confs_required}
          />
          <button onClick={() => setPay(null)} className="btn-ghost mt-2 w-full text-ink-700">
            Choose a different rail
          </button>
        </div>
      ) : pay && pay.checkout ? (
        <div className="mt-4 rounded-xl border border-amber-200 bg-amber-50/70 p-4">
          <div className="flex items-center gap-2">
            <Icon.wallet width={16} height={16} className="text-amber-700" />
            <span className="text-sm font-semibold text-ink-900">
              {railMeta(pay.checkout.rail).label} checkout
            </span>
            <Badge color="amber" className="ml-auto">
              SIMULATED
            </Badge>
          </div>
          <div className="mt-2 text-xl font-extrabold text-ink-900">
            {pay.checkout.amount_display}
          </div>
          <p className="mt-1 text-xs text-amber-700">{pay.checkout.label}</p>
          <p className="mt-2 text-xs text-ink-700/70">
            Receipt <span className="font-mono">{pay.checkout.receipt}</span>. It settles
            automatically, then the invoice is marked paid.
          </p>
        </div>
      ) : (
        <div className="mt-4 space-y-3">
          <div>
            <span className="label">Pay with your choice of rail</span>
            <div className="mt-1 grid grid-cols-2 gap-2">
              {rails.map(([r, label]) => (
                <button
                  key={r}
                  type="button"
                  onClick={() => setRail(r)}
                  className={`rounded-lg border px-3 py-2 text-left text-sm ${
                    rail === r ? 'border-seq-500 bg-seq/[0.06]' : 'border-ink-900/15'
                  }`}
                >
                  {label}
                </button>
              ))}
            </div>
          </div>
          {err && <p className="text-xs font-medium text-rose-600">{err}</p>}
          <button onClick={submit} disabled={busy} className="btn-primary w-full disabled:opacity-60">
            {busy ? 'Starting…' : `Pay the setup fee`}
          </button>
        </div>
      )}
    </div>
  )
}
