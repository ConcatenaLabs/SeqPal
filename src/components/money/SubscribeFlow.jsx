import { useState } from 'react'
import { Icon } from '../icons'
import { Badge } from '../ui'
import { txUrl } from '../../lib/chain'
import * as api from '../../lib/api'
import { usePoll } from '../../lib/poll'
import { fmtRail, railMeta, subStateChip, refundHint } from '../../lib/money'
import DepositBox from './DepositBox'
import GateSteps from './GateSteps'

// The escrow-state panel for a live subscription. It polls the caller's own
// subscriptions (seqpald is authoritative; the deposit watcher advances state
// server-side) and, for a SIMULATED fiat payment, polls the checkout. Nothing is
// shown as final at 0 confirmations: the confirmation count and the "not final"
// framing are always present until settle.
function EscrowState({ subId, rail, checkoutId }) {
  const { data } = usePoll(() => api.mySubscriptions(), { intervalMs: 4000, deps: [subId] })
  const { data: fiat } = usePoll(() => api.fiatStatus(checkoutId), {
    intervalMs: 4000,
    active: !!checkoutId,
    deps: [checkoutId],
  })
  const sub = (data?.subscriptions || []).find((s) => s.id === subId)
  const state = sub?.state || 'created'
  const chip = subStateChip(state)
  const meta = railMeta(rail)
  const confs = Number(sub?.confirmations || 0)
  return (
    <div className="mt-4 rounded-xl border border-ink-900/10 bg-white p-4">
      <div className="flex items-center justify-between">
        <span className="text-sm font-semibold text-ink-900">Escrow status</span>
        <Badge color={chip.color}>{chip.label}</Badge>
      </div>
      <ol className="mt-3 space-y-2 text-sm">
        {[
          ['created', 'Subscription recorded'],
          ['in_escrow', meta.simulated ? 'Payment settled (SIMULATED)' : `Deposit confirmed`],
          ['settled', 'Delivered at close'],
        ].map(([key, label], i) => {
          const order = ['created', 'in_escrow', 'settled']
          const reached = order.indexOf(state) >= i || state === 'settled'
          const active = order[order.indexOf(state)] === key
          return (
            <li key={key} className="flex items-center gap-2.5">
              <span
                className={`flex h-5 w-5 items-center justify-center rounded-full text-[10px] ${
                  reached ? 'bg-emerald-500 text-white' : 'bg-ink-900/[0.08] text-ink-600'
                }`}
              >
                {reached ? <Icon.check width={12} height={12} /> : i + 1}
              </span>
              <span className={active ? 'font-semibold text-ink-900' : 'text-ink-700/80'}>
                {label}
                {key === 'in_escrow' && state === 'created' && !meta.simulated && (
                  <span className="ml-1 text-xs text-ink-700/60">
                    ({confs} confirmation{confs === 1 ? '' : 's'} so far, not final at 0)
                  </span>
                )}
              </span>
            </li>
          )
        })}
      </ol>
      {state === 'refunded' && (
        <p className="mt-2 text-xs text-ink-700/70">
          This subscription was refunded to your refund address.
        </p>
      )}
      {sub?.deposit_txid && !meta.simulated && (
        <p className="mt-2 text-xs text-ink-700/70">
          Deposit tx:{' '}
          <a
            href={txUrl(sub.deposit_txid)}
            target="_blank"
            rel="noopener noreferrer"
            className="font-mono text-seq-600 hover:underline"
          >
            {sub.deposit_txid.slice(0, 12)}…
          </a>
        </p>
      )}
      {meta.simulated && (
        <p className="mt-2 flex items-center gap-1.5 text-xs text-amber-700">
          <Icon.spark width={12} height={12} />
          {fiat?.label || 'SIMULATED payment: no real card or bank transfer is processed.'}
        </p>
      )}
      <p className="mt-3 text-[11px] leading-relaxed text-ink-700/55">
        Your tokens are delivered to your SeqPal ID enclave only when the issuer closes the
        offering. Until then the funds sit in a segregated escrow; nothing is delivered and
        nothing is final at 0 confirmations.
      </p>
    </div>
  )
}

export default function SubscribeFlow({ offering, issuanceId }) {
  const rails = offering.rails || []
  const [rail, setRail] = useState(rails[0]?.rail || 'usdx')
  const [amount, setAmount] = useState('')
  const [refund, setRefund] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const [reqs, setReqs] = useState(null) // subscribe-side gate requirements
  const [result, setResult] = useState(null)

  const meta = railMeta(rail)
  const hint = refundHint(rail)
  const price = Number(offering.price) || 0
  const amt = Number(String(amount).replace(/[^0-9.]/g, '')) || 0
  const estCost = price > 0 && amt > 0 ? amt * price : 0

  const submit = async () => {
    setErr(null)
    setReqs(null)
    setBusy(true)
    try {
      const res = await api.subscribe(issuanceId, {
        rail,
        amount: amt,
        refund_address: refund.trim(),
      })
      setResult(res)
    } catch (e) {
      // A 403 may carry structured gate requirements (appropriateness/KID, SoF,
      // US accreditation) in the response body; render them as clearable steps.
      const bodyReqs = e.data?.requirements
      if (e.status === 403 && Array.isArray(bodyReqs) && bodyReqs.length > 0) {
        setReqs(bodyReqs)
      } else {
        setErr(e.message)
      }
    } finally {
      setBusy(false)
    }
  }

  if (result) {
    const dep = result.deposit_address
    const checkout = result.checkout
    const subId = result.subscription?.id
    return (
      <div>
        {dep ? (
          <DepositBox
            rail={rail}
            address={dep}
            payAmount={result.pay_amount}
            payCcy={result.pay_ccy}
            confsRequired={result.confs_required}
            registrarNote={result.registrar_note}
          />
        ) : checkout ? (
          <div className="rounded-xl border border-amber-200 bg-amber-50/70 p-4">
            <div className="flex items-center gap-2">
              <Icon.wallet width={16} height={16} className="text-amber-700" />
              <span className="text-sm font-semibold text-ink-900">
                {meta.label} checkout
              </span>
              <Badge color="amber" className="ml-auto">
                SIMULATED
              </Badge>
            </div>
            <div className="mt-2 text-2xl font-extrabold text-ink-900">
              {checkout.amount_display || fmtRail(result.subscription?.pay_amount, 'USD')}
            </div>
            <p className="mt-1 text-xs text-amber-700">{result.label || checkout.label}</p>
            <p className="mt-2 text-xs text-ink-700/70">
              Receipt <span className="font-mono">{checkout.receipt}</span>. The simulated
              processor settles this automatically, then it enters the same escrow as an
              on-chain deposit.
            </p>
          </div>
        ) : null}
        {subId && <EscrowState subId={subId} rail={rail} checkoutId={checkout?.id} />}
        <button
          onClick={() => {
            setResult(null)
            setAmount('')
            setRefund('')
          }}
          className="btn-outline mt-4 w-full"
        >
          Make another subscription
        </button>
      </div>
    )
  }

  if (reqs) {
    return (
      <div>
        <p className="mb-3 text-sm text-ink-700/80">
          A few steps are required before this subscription can be funded.
        </p>
        <GateSteps id={issuanceId} requirements={reqs} onCleared={() => setReqs(null)} />
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div>
        <span className="label">Choose how to pay</span>
        <p className="mb-2 text-[11px] text-ink-700/60">
          The rail is always your choice. It is never set by your jurisdiction.
        </p>
        <div className="grid grid-cols-2 gap-2">
          {rails.map((r) => {
            const rm = railMeta(r.rail)
            return (
              <button
                key={r.rail}
                type="button"
                onClick={() => setRail(r.rail)}
                className={`rounded-lg border px-3 py-2.5 text-left text-sm transition ${
                  rail === r.rail ? 'border-seq-500 bg-seq/[0.06]' : 'border-ink-900/15 hover:bg-ink-900/[0.02]'
                }`}
              >
                <span className="flex items-center gap-1.5 font-semibold text-ink-900">
                  {r.label || rm.label}
                  {r.simulated && (
                    <Badge color="amber" className="ml-auto">
                      SIM
                    </Badge>
                  )}
                </span>
              </button>
            )
          })}
        </div>
      </div>

      <p className="rounded-lg bg-ink-900/[0.03] px-3 py-2 text-xs leading-relaxed text-ink-700/75">
        {meta.trust}
      </p>

      <div>
        <label className="label" htmlFor="sub-amount">
          Amount to purchase ({offering.ticker})
        </label>
        <input
          id="sub-amount"
          className="input"
          inputMode="decimal"
          placeholder="0"
          value={amount}
          onChange={(e) => setAmount(e.target.value)}
        />
        {estCost > 0 && (
          <p className="mt-1 text-xs text-ink-700/60">
            About {estCost.toLocaleString('en-US', { maximumFractionDigits: 2 })}{' '}
            {offering.quote || 'USD'} at {price.toLocaleString('en-US')} {offering.quote || 'USD'} /{' '}
            {offering.ticker}. The exact amount to send is shown after you subscribe.
          </p>
        )}
      </div>

      <div>
        <label className="label" htmlFor="sub-refund">
          {hint.label}
        </label>
        <input
          id="sub-refund"
          className="input font-mono text-xs"
          placeholder={hint.ph}
          value={refund}
          onChange={(e) => setRefund(e.target.value)}
        />
        {hint.required && (
          <p className="mt-1 text-[11px] text-ink-700/60">
            Validated on the {rail === 'btc' ? 'Bitcoin testnet4' : 'Sequentia'} network. A failed
            close refunds your deposit here.
          </p>
        )}
      </div>

      {err && (
        <div className="rounded-lg border border-rose-200 bg-rose-50 px-3 py-2 text-sm font-medium text-rose-700">
          {err}
        </div>
      )}

      <button
        onClick={submit}
        disabled={busy || amt <= 0 || (hint.required && !refund.trim())}
        className="btn-primary w-full disabled:opacity-60"
      >
        {busy ? 'Subscribing…' : 'Subscribe'}
        {!busy && <Icon.arrowRight width={16} height={16} />}
      </button>
    </div>
  )
}
