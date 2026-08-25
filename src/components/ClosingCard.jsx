import { useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import { txUrl } from '../lib/chain'
import { fmtAssetAmount } from '../lib/format'
import * as api from '../lib/api'
import { usePoll } from '../lib/poll'
import { useStore } from '../lib/store'
import { fmtRail, subStateChip, settlementModel } from '../lib/money'

// A short investor AID for the book.
const shortAid = (a) => (a ? `${a.slice(0, 6)}…${a.slice(-4)}` : '-')

// A settlement txid line: real on-chain delivery/release resolves on the
// explorer; a "reconciled" delivery (a prior attempt already delivered, detected
// by a balance scan) and a SIMULATED fiat reference are labeled, not linked.
function TxLine({ label, txid }) {
  if (!txid) return null
  const onchain = /^[0-9a-fA-F]{64}$/.test(txid)
  return (
    <div className="flex items-center justify-between gap-3 text-xs">
      <span className="text-ink-700/60">{label}</span>
      {onchain ? (
        <a
          href={txUrl(txid)}
          target="_blank"
          rel="noopener noreferrer"
          className="font-mono text-seq-600 hover:underline"
        >
          {txid.slice(0, 12)}…
        </a>
      ) : (
        <span className="font-mono text-ink-700/70">{txid}</span>
      )}
    </div>
  )
}

export default function ClosingCard({ iss }) {
  const { signCloseStmt, xonly, hasKey } = useStore()
  const { data: book, refresh: refreshBook } = usePoll(() => api.issuanceSubscriptions(iss.id), {
    intervalMs: 6000,
    deps: [iss.id],
  })
  const { data: settle, refresh: refreshSettle } = usePoll(() => api.settlements(iss.id), {
    intervalMs: 6000,
    deps: [iss.id],
  })
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const [results, setResults] = useState(null)

  const subs = book?.subscriptions || []
  const ledger = book?.ledger || []
  const settlements = settle?.settlements || []
  const settById = Object.fromEntries(settlements.map((s) => [s.subscription.id, s.settlement]))

  const inEscrow = subs.filter((s) => s.state === 'in_escrow')
  const settled = subs.filter((s) => s.state === 'settled')
  const refunded = subs.filter((s) => s.state === 'refunded')

  const close = async () => {
    setErr(null)
    setBusy(true)
    try {
      const prep = await api.close(iss.id, {})
      if (!prep.sign_this) {
        refreshBook()
        return
      }
      const sig = await signCloseStmt(prep.sign_this)
      if (!sig) {
        setErr('Connect your Sequentia wallet to sign the closing authorization.')
        return
      }
      const res = await api.close(iss.id, { signature: sig, signer_xonly: xonly })
      setResults(res.results || [])
      refreshBook()
      refreshSettle()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="card p-6">
      <div className="flex items-center gap-2">
        <Icon.coins width={18} height={18} className="text-btc-600" />
        <h2 className="font-bold text-ink-900">Subscriptions and closing</h2>
        <Badge color="emerald" className="ml-auto">
          Real money
        </Badge>
      </div>
      <p className="mt-1.5 text-xs leading-relaxed text-ink-700/70">
        Closing settles each subscription with delivery versus payment. A USDX subscription
        settles atomically in one transaction: the token moves from the offering escrow enclave to
        the investor and the escrowed USDX moves to your payout mandate together. A native BTC
        subscription is cross-chain registrar settlement, not atomic: the payment confirmed on
        Bitcoin testnet4 and delivery is co-signed on Sequentia. A failed close auto-refunds to the
        investor's refund address. Nothing is delivered before you sign, and nothing is final at 0
        confirmations.
      </p>

      <div className="mt-4 grid grid-cols-3 gap-3">
        {[
          ['In escrow', inEscrow.length],
          ['Settled', settled.length],
          ['Refunded', refunded.length],
        ].map(([k, v]) => (
          <div key={k} className="rounded-lg bg-ink-900/[0.03] px-3 py-2.5">
            <div className="text-xs text-ink-700/60">{k}</div>
            <div className="mt-0.5 font-bold text-ink-900">{v}</div>
          </div>
        ))}
      </div>

      {subs.length === 0 ? (
        <div className="mt-4 rounded-lg bg-ink-900/[0.03] px-4 py-3 text-sm text-ink-700/70">
          No subscriptions yet. Investors subscribe from the offering portal.
        </div>
      ) : (
        <div className="mt-4 space-y-2">
          {subs.map((s) => {
            const chip = subStateChip(s.state)
            const st = settById[s.id]
            const model = settlementModel(s.rail)
            // An atomic USDX close carries one combined txid: delivery and release
            // are the same transaction, so render it once, not as two lines.
            const atomic =
              model.atomic && st && st.delivery_txid && st.delivery_txid === st.release_txid
            return (
              <div key={s.id} className="rounded-lg border border-ink-900/10 px-3 py-2.5">
                <div className="flex items-center justify-between gap-3 text-sm">
                  <div className="flex items-center gap-2">
                    <span className="font-mono text-xs text-ink-700/80">{shortAid(s.investor_aid)}</span>
                    <Badge color={s.rail === 'btc' ? 'btc' : s.rail === 'usdx' ? 'seq' : 'amber'}>
                      {s.rail}
                      {s.funds_simulated ? ' · SIM' : ''}
                    </Badge>
                  </div>
                  <Badge color={chip.color}>{chip.label}</Badge>
                </div>
                <div className="mt-1.5 flex items-center justify-between text-xs text-ink-700/70">
                  <span>{fmtAssetAmount(s.token_atoms, iss.precision, iss.ticker)}</span>
                  <span>{fmtRail(s.deposited_atoms || s.pay_amount, s.pay_ccy)} escrowed</span>
                </div>
                {model.label && (
                  <div className="mt-1 flex items-center gap-1.5 text-[11px] text-ink-700/55">
                    <Icon.exchange width={11} height={11} className="shrink-0" />
                    {model.label}
                  </div>
                )}
                {st && (st.delivery_txid || st.release_txid || st.refund_txid) && (
                  <div className="mt-2 space-y-1 border-t border-ink-900/5 pt-2">
                    {atomic ? (
                      <TxLine label="Atomic delivery versus payment" txid={st.delivery_txid} />
                    ) : (
                      <>
                        <TxLine label="Delivery" txid={st.delivery_txid} />
                        <TxLine label="Release" txid={st.release_txid} />
                      </>
                    )}
                    <TxLine label="Refund" txid={st.refund_txid} />
                    {st.state === 'failed' && st.error && (
                      <p className="text-xs text-rose-600">{st.error}</p>
                    )}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}

      {results && (
        <div className="mt-4 rounded-lg border border-ink-900/10 bg-ink-900/[0.02] p-3">
          <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
            Close results
          </div>
          <ul className="mt-1.5 space-y-1 text-xs text-ink-700/80">
            {results.map((r) => (
              <li key={r.subscription_id} className="flex items-center justify-between gap-2">
                <span className="font-mono">{shortAid(r.investor_aid)}</span>
                <span>
                  {r.state}
                  {r.message ? ` · ${r.message}` : ''}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {err && (
        <div className="mt-4 rounded-lg border border-rose-200 bg-rose-50 px-3 py-2 text-sm font-medium text-rose-700">
          {err}
        </div>
      )}

      <button
        onClick={close}
        disabled={busy || inEscrow.length === 0 || !hasKey}
        className="btn-primary mt-4 w-full disabled:opacity-60"
      >
        <Icon.check width={16} height={16} />
        {busy ? 'Closing…' : `Sign closing authorization${inEscrow.length ? ` (${inEscrow.length} to settle)` : ''}`}
      </button>
      {!hasKey && (
        <p className="mt-2 text-center text-xs text-amber-700">
          Connect your Sequentia wallet to sign the closing authorization.
        </p>
      )}
      {ledger.length > 0 && (
        <details className="mt-4">
          <summary className="cursor-pointer text-xs font-semibold uppercase tracking-wide text-ink-700/60">
            Escrow ledger ({ledger.length})
          </summary>
          <div className="mt-2 divide-y divide-ink-900/5 rounded-lg border border-ink-900/10">
            {ledger.map((e) => (
              <div key={e.id} className="flex items-center justify-between gap-2 px-3 py-2 text-xs">
                <span className="capitalize text-ink-800">{e.kind.replace('_', ' ')}</span>
                <span className="flex items-center gap-2">
                  <span className="font-mono text-ink-700/80">{fmtRail(e.amount, e.ccy)}</span>
                  {e.funds_simulated && <Badge color="amber">SIM</Badge>}
                </span>
              </div>
            ))}
          </div>
        </details>
      )}
    </div>
  )
}
