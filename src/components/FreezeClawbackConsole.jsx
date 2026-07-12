import { useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import CopyId from './CopyId'
import * as api from '../lib/api'
import { fmtAssetAmount } from '../lib/format'
import { txUrl } from '../lib/chain'

// The M7 freeze / clawback console (issuer-facing, owner-scoped). Both actions are
// real at the policy server. A REASON is required on each and becomes part of the
// audit and, for a clawback, the public transparency log. A clawback is a FULL
// SWEEP: openampd seizes every confirmed enclave UTXO the holder holds of this
// asset into the issuer enclave. This is disclosed here and the resulting sweep
// txid and the public log link are surfaced. Every clawback is idempotent and
// reconciled server-side, so a sweep is never broadcast twice.

const LOG_URL = '/openamp/v1/log'

function ResultBox({ result }) {
  if (!result) return null
  if (result.kind === 'freeze') {
    return (
      <div className="mt-3 rounded-lg border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3 text-sm">
        <div className="flex items-center gap-2 font-medium text-ink-900">
          <Icon.check width={16} height={16} className="text-emerald-500" />
          {result.frozen ? 'Holder frozen' : 'Freeze lifted'}
        </div>
        {result.note && <p className="mt-1 text-xs text-ink-700/70">{result.note}</p>}
        <a
          href={LOG_URL}
          target="_blank"
          rel="noopener noreferrer"
          className="mt-2 inline-flex items-center gap-1.5 text-xs font-medium text-seq-600 hover:underline"
        >
          <Icon.external width={13} height={13} /> Public transparency log
        </a>
      </div>
    )
  }
  // clawback
  const empty = result.clawback?.state === 'empty'
  return (
    <div className="mt-3 rounded-lg border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3 text-sm">
      <div className="flex items-center gap-2 font-medium text-ink-900">
        <Icon.check width={16} height={16} className="text-emerald-500" />
        {empty ? 'Nothing to sweep' : 'Clawback swept'}
      </div>
      {!empty && (
        <div className="mt-2 space-y-1.5">
          <div className="flex items-center gap-2">
            <span className="text-xs text-ink-700/60">Swept</span>
            <span className="font-mono text-xs font-semibold text-ink-900">
              {fmtAssetAmount(result.atoms, result.precision || 8, result.ticker)} (
              {Number(result.atoms).toLocaleString('en-US')} atoms)
            </span>
          </div>
          {result.txid && (
            <div className="flex items-center gap-2">
              <span className="text-xs text-ink-700/60">Sweep txid</span>
              <a
                href={txUrl(result.txid)}
                target="_blank"
                rel="noopener noreferrer"
                className="font-mono text-xs text-seq-600 hover:underline"
              >
                {result.txid.slice(0, 10)}…{result.txid.slice(-8)}
              </a>
            </div>
          )}
        </div>
      )}
      {result.note && <p className="mt-1.5 text-xs text-ink-700/70">{result.note}</p>}
      <a
        href={LOG_URL}
        target="_blank"
        rel="noopener noreferrer"
        className="mt-2 inline-flex items-center gap-1.5 text-xs font-medium text-seq-600 hover:underline"
      >
        <Icon.external width={13} height={13} /> Reason recorded in the public transparency log
      </a>
    </div>
  )
}

export default function FreezeClawbackConsole({ iss }) {
  const [holder, setHolder] = useState('')
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(null) // 'freeze' | 'unfreeze' | 'clawback' | null
  const [confirmClawback, setConfirmClawback] = useState(false)
  const [err, setErr] = useState(null)
  const [result, setResult] = useState(null)

  const ticker = iss.ticker

  const run = async (action) => {
    setErr(null)
    setResult(null)
    const h = holder.trim()
    const rsn = reason.trim()
    if (!h) {
      setErr('Enter the holder AID.')
      return
    }
    if (!rsn) {
      setErr('A reason is required. It is recorded in the audit log, and for a clawback in the public transparency log.')
      return
    }
    setBusy(action)
    try {
      if (action === 'clawback') {
        const res = await api.consoleClawback(iss.id, { holder_aid: h, reason: rsn })
        setResult({ kind: 'clawback', ticker, precision: iss.precision, ...res })
        setConfirmClawback(false)
      } else {
        const frozen = action === 'freeze'
        const res = await api.consoleFreeze(iss.id, { holder_aid: h, frozen, reason: rsn })
        setResult({ kind: 'freeze', ...res })
      }
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(null)
    }
  }

  return (
    <div className="card p-6">
      <div className="flex items-center gap-2">
        <Icon.lock width={18} height={18} className="text-ink-700" />
        <h2 className="font-bold text-ink-900">Freeze and clawback console</h2>
        <Badge color="rose" className="ml-auto">
          Enforcement
        </Badge>
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
        Freeze a holder or claw back their holdings at the policy server. Both are real and a reason
        is required. A clawback is a full sweep: every confirmed enclave UTXO the holder holds of
        this asset is seized into the issuer enclave. The reason is recorded in the public,
        hash-chained transparency log alongside the sweep txid.
      </p>

      <div className="mt-4 space-y-3">
        <div>
          <label className="label" htmlFor="fc-holder">
            Holder AID
          </label>
          <input
            id="fc-holder"
            className="input font-mono text-xs"
            placeholder="holder account id"
            value={holder}
            onChange={(e) => setHolder(e.target.value)}
          />
        </div>
        <div>
          <label className="label" htmlFor="fc-reason">
            Reason (required)
          </label>
          <input
            id="fc-reason"
            className="input"
            placeholder="e.g. court order, sanctions match, lost-key recovery"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
        </div>

        {err && <p className="text-sm font-medium text-rose-600">{err}</p>}

        <div className="flex flex-wrap gap-2">
          <button
            onClick={() => run('freeze')}
            disabled={!!busy}
            className="btn-outline disabled:opacity-60"
          >
            {busy === 'freeze' ? 'Freezing…' : 'Freeze holder'}
          </button>
          <button
            onClick={() => run('unfreeze')}
            disabled={!!busy}
            className="btn-outline disabled:opacity-60"
          >
            {busy === 'unfreeze' ? 'Lifting…' : 'Lift freeze'}
          </button>
          {!confirmClawback ? (
            <button
              onClick={() => {
                setErr(null)
                setConfirmClawback(true)
              }}
              disabled={!!busy}
              className="btn-outline border-rose-300 text-rose-700 hover:bg-rose-50 disabled:opacity-60"
            >
              <Icon.lock width={15} height={15} /> Clawback (full sweep)
            </button>
          ) : (
            <button
              onClick={() => run('clawback')}
              disabled={!!busy}
              className="inline-flex items-center gap-2 rounded-xl bg-rose-600 px-4 py-2 text-sm font-semibold text-white hover:bg-rose-700 disabled:opacity-60"
            >
              {busy === 'clawback' ? 'Sweeping…' : 'Confirm full sweep'}
            </button>
          )}
        </div>

        {confirmClawback && !busy && (
          <p className="rounded-lg bg-rose-50 px-3 py-2 text-xs leading-relaxed text-rose-700">
            This seizes every confirmed {ticker} enclave UTXO of the holder into the issuer enclave.
            It is irreversible on chain and the reason becomes public in the transparency log. Click
            confirm to proceed, or edit the fields to cancel.
          </p>
        )}

        <ResultBox result={result} />
      </div>

      <p className="mt-4 text-[11px] leading-relaxed text-ink-700/55">
        A freeze is a global policy-server account attribute: it gates every transfer the account is
        party to, not this asset alone. Nothing is final at 0 confirmations, because a Sequentia
        state is only as final as its Bitcoin anchor is deep.
      </p>
    </div>
  )
}
