import { useEffect } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import * as api from '../lib/api'
import { usePoll } from '../lib/poll'
import FeePay from './money/FeePay'

// What a check costs the person asking for it. The identity-verification provider
// bills SeqPal per check, cleared or refused, so the fee is collected before the
// check is submitted rather than after it has already been run.
//
// It polls, because an on-chain payment settles when the chain says so: paying in
// USDX or tBTC flips this card on its own once the deposit confirms.
export default function VerificationFeeCard({ kind = 'identity', entityId, onPaid }) {
  const { data, refresh } = usePoll(() => api.verificationFees(), {
    intervalMs: 5000,
    deps: [kind, entityId || ''],
  })

  const invoice =
    kind === 'business'
      ? data?.businesses?.find((b) => b.entity_id === entityId)?.invoice
      : data?.identity
  const paid = invoice?.state === 'paid'
  const free = Number(invoice?.amount_usd ?? 0) <= 0
  const what = kind === 'business' ? 'business verification' : 'identity verification'

  // Whether the caller may submit. An invoice we could not read at all counts as
  // settled: the server is the gate, and a page that hides the button because a
  // poll failed is a page that locks a paying holder out of a check they own.
  const settled = !invoice || free || paid
  useEffect(() => {
    if (settled) onPaid?.()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [settled])

  // A deployment that charges nothing for this check has nothing to show: the
  // invoice exists already paid, and a "$0 fee, paid" row is noise.
  if (!invoice || free) return null

  const settle = () => {
    refresh()
    onPaid?.()
  }

  if (paid)
    return (
      <div className="flex items-center gap-2 rounded-lg bg-emerald-50 px-3 py-2.5 text-sm font-medium text-emerald-700">
        <Icon.check width={16} height={16} />
        {kind === 'business' ? 'Business' : 'Identity'} verification fee paid
        {invoice.funds_simulated && (
          <Badge color="amber" className="ml-auto">
            SIMULATED
          </Badge>
        )}
      </div>
    )

  return (
    <div className="rounded-xl border border-amber-200 bg-amber-50/50 p-4">
      <div className="flex items-center gap-2">
        <Icon.receipt width={18} height={18} className="text-btc-600" />
        <h3 className="font-bold text-ink-900">Verification fee</h3>
        <Badge color="amber" className="ml-auto">
          Due
        </Badge>
      </div>
      <div className="mt-2 flex items-baseline gap-2">
        <span className="text-2xl font-extrabold text-ink-900">
          ${Number(invoice.amount_usd).toLocaleString('en-US')}
        </span>
        <span className="text-xs text-ink-700/60">per check, in USD via the price server</span>
      </div>
      <p className="mt-2 text-xs leading-relaxed text-ink-700/70">
        An independent verification provider runs the {what} and charges for every check, whether it
        clears or not. The fee is paid before the check is submitted. If the provider comes back
        asking for a clearer document, finishing that check costs nothing more.
      </p>
      <div className="mt-3">
        <FeePay
          label={`Pay $${Number(invoice.amount_usd).toLocaleString('en-US')}`}
          onPay={(rail) => api.payVerificationFee({ kind, entity_id: entityId, rail })}
          onPaid={settle}
        />
      </div>
    </div>
  )
}
