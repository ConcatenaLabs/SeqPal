import { useState } from 'react'
import { Icon } from '../icons'
import { Badge } from '../ui'
import { railMeta } from '../../lib/money'
import DepositBox from './DepositBox'

// Every SeqPal fee is payable on the same rails, so every fee is paid the same
// way: choose a rail, then either send to the deposit address it returns or watch
// the SIMULATED checkout settle. What differs between fees is which invoice is
// being paid, and that is the caller's business -- onPay is handed the chosen
// rail and returns whatever the server said. If a rail is not configured on this
// deployment the server refuses with a clear message, surfaced inline.
const RAILS = [
  ['usdx', 'USDX'],
  ['btc', 'Bitcoin (testnet4)'],
  ['card', 'Card (SIMULATED)'],
  ['bank', 'Bank (SIMULATED)'],
]

export default function FeePay({ label, onPay, onPaid }) {
  const [rail, setRail] = useState('usdx')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const [pay, setPay] = useState(null) // { deposit_address, ... } or { checkout }

  const submit = async () => {
    setErr(null)
    setBusy(true)
    try {
      const res = await onPay(rail)
      setPay(res?.already_paid ? null : res)
      onPaid?.()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  if (pay && pay.deposit_address)
    return (
      <div>
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
    )

  if (pay && pay.checkout)
    return (
      <div className="rounded-xl border border-amber-200 bg-amber-50/70 p-4">
        <div className="flex items-center gap-2">
          <Icon.wallet width={16} height={16} className="text-amber-700" />
          <span className="text-sm font-semibold text-ink-900">
            {railMeta(pay.checkout.rail).label} checkout
          </span>
          <Badge color="amber" className="ml-auto">
            SIMULATED
          </Badge>
        </div>
        <div className="mt-2 text-xl font-extrabold text-ink-900">{pay.checkout.amount_display}</div>
        <p className="mt-1 text-xs text-amber-700">{pay.checkout.label}</p>
        <p className="mt-2 text-xs text-ink-700/70">
          Receipt <span className="font-mono">{pay.checkout.receipt}</span>. It settles
          automatically, then the invoice is marked paid.
        </p>
      </div>
    )

  return (
    <div className="space-y-3">
      <div>
        <span className="label">Pay with your choice of rail</span>
        <div className="mt-1 grid grid-cols-2 gap-2">
          {RAILS.map(([r, name]) => (
            <button
              key={r}
              type="button"
              onClick={() => setRail(r)}
              className={`rounded-lg border px-3 py-2 text-left text-sm ${
                rail === r ? 'border-seq-500 bg-seq/[0.06]' : 'border-ink-900/15'
              }`}
            >
              {name}
            </button>
          ))}
        </div>
      </div>
      {err && <p className="text-xs font-medium text-rose-600">{err}</p>}
      <button onClick={submit} disabled={busy} className="btn-primary w-full disabled:opacity-60">
        {busy ? 'Starting…' : label}
      </button>
    </div>
  )
}
