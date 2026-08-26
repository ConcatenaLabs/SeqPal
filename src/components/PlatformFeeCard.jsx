import { Icon } from './icons'
import { Badge } from './ui'
import * as api from '../lib/api'
import { usePoll } from '../lib/poll'
import FeePay from './money/FeePay'

// The SeqPal platform setup fee: invoiced and COLLECTED before deploy, payable by
// the issuer's choice of rail (on-chain USDX/tBTC or the SIMULATED fiat rail). An
// unpaid setup fee blocks the mint. The fee is shown in USD via the price server.
// The escrow fee is not collected here: it accrues on real escrow balances and is
// deducted at release.
export default function PlatformFeeCard({ iss }) {
  const { data, refresh } = usePoll(() => api.fees(iss.id), { intervalMs: 5000, deps: [iss.id] })

  const setup = (data?.invoices || []).find((i) => i.kind === 'setup')
  const paid = setup?.state === 'paid'
  const feeUsd = data?.setup_fee_usd ?? 0
  // The escrow fee is a schedule, not a rate: the server states it so this does
  // not have to restate it, and a deployment charging a flat rate instead says
  // so rather than being described as the published one.
  const escrow = data?.escrow_fee_published
  const escrowOverrideBps = data?.escrow_fee_override_bps

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
        fee is charged separately on real escrow balances and deducted at release, not here:{' '}
        {typeof escrowOverrideBps === 'number'
          ? `${escrowOverrideBps} bps of the funds held, which is what this deployment charges instead of the published schedule.`
          : escrow
            ? `${escrow.rate_per_month_bps / 100}% a month accrued daily, with a $${escrow.minimum_usd.toLocaleString()} minimum and a ${escrow.cap_bps / 100}% cap.`
            : 'on the published schedule.'}
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
      ) : (
        <div className="mt-4">
          <FeePay
            label="Pay the setup fee"
            onPay={(rail) => api.payFee(iss.id, { kind: 'setup', rail })}
            onPaid={refresh}
          />
        </div>
      )}
    </div>
  )
}
