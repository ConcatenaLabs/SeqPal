import { useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import * as api from '../lib/api'
import { useStore } from '../lib/store'
import { fmtAssetAmount } from '../lib/format'
import { txUrl } from '../lib/chain'

// The M7 freeze / clawback console (issuer-facing, owner-scoped). Both actions are
// real at the policy server. A REASON is required on each and becomes part of the
// audit and, for a clawback, the public transparency log. A clawback is a FULL
// SWEEP: openampd seizes every confirmed enclave UTXO the holder holds of this
// asset into the issuer enclave. This is disclosed here and the resulting sweep
// txid and the public log link are surfaced. Every clawback is idempotent and
// reconciled server-side, so a sweep is never broadcast twice.
//
// M9: for an external-issuer asset (the entity's own SeqPal ID key is the enclave
// issuer half), a clawback is TWO-PHASE. The build logs the reason and returns the
// L_claw sighashes but broadcasts nothing; the issuer then signs those sighashes in
// this browser with their own key and posts them to complete, and only then does
// the policy server co-sign and broadcast. The platform cannot move a holder's
// position alone: the issuer authorizes, the registrar co-signs.

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
  const { signClawbackSigs, hasKey } = useStore()
  const [holder, setHolder] = useState('')
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(null) // 'freeze' | 'unfreeze' | 'clawback' | 'sign' | null
  const [confirmClawback, setConfirmClawback] = useState(false)
  const [err, setErr] = useState(null)
  const [result, setResult] = useState(null)
  // A two-phase (external-issuer) clawback build awaiting the issuer's signature:
  // { clawback_id, to_sign, pubkey, atoms }. Nothing is swept until it is signed.
  const [pending, setPending] = useState(null)

  const ticker = iss.ticker
  const external = !!iss.issuer_external

  const run = async (action) => {
    setErr(null)
    setResult(null)
    setPending(null)
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
        setConfirmClawback(false)
        // External-issuer asset: the server built the sweep and logged the reason
        // but broadcast nothing. Hold the sighashes for the issuer to sign.
        if (res.two_phase && res.to_sign?.length) {
          setPending({
            clawback_id: res.clawback_id,
            to_sign: res.to_sign,
            pubkey: res.pubkey,
            atoms: res.atoms,
          })
        } else {
          // Legacy asset swept in one call, or an empty/already-swept result.
          setResult({ kind: 'clawback', ticker, precision: iss.precision, ...res })
        }
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

  // Phase two: the issuer signs the L_claw sighashes with their own SeqPal ID key
  // and posts them to complete. Only then does the policy server co-sign and
  // broadcast. The store refuses to sign any sighash for a key the issuer does not
  // hold, so a signature is never produced over a foreign key.
  const completeClawback = async () => {
    setErr(null)
    if (!hasKey) {
      setErr('Connect your Sequentia wallet to sign the clawback. The issuer key authorizes the seizure; nothing is swept until you sign.')
      return
    }
    setBusy('sign')
    try {
      let sigs
      try {
        // The wallet is handed the sweep TRANSACTION and the holder whose
        // enclave output it spends: the clawback leaf comes from that holder's
        // address, and the wallet recomputes each sighash from it rather than
        // signing a digest seqpald handed over.
        sigs = await signClawbackSigs(pending, {
          asset: pending.asset || iss.asset_id || iss.assetId,
          fromAid: pending.holder_aid,
        })
      } catch (e) {
        setErr(e.message)
        return
      }
      if (!sigs) {
        setErr('Connect your Sequentia wallet to sign the clawback.')
        return
      }
      const res = await api.consoleClawbackComplete(iss.id, pending.clawback_id, { sigs })
      setResult({ kind: 'clawback', ticker, precision: iss.precision, ...res })
      setPending(null)
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

      <div
        className={`mt-3 rounded-lg border px-4 py-3 text-xs leading-relaxed ${
          external
            ? 'border-emerald-200 bg-emerald-50 text-emerald-800'
            : 'border-amber-200 bg-amber-50 text-amber-800'
        }`}
      >
        {external ? (
          <>
            <span className="font-semibold">External issuer key.</span> This asset&rsquo;s issuer key
            is your own SeqPal ID key, held in your Sequentia wallet, not by the platform. A clawback is
            two-phase: the platform builds the sweep and logs the reason, then you authorize the
            seizure with your key and the registrar co-signs. SeqPal cannot move a holder&rsquo;s
            position on its own.
          </>
        ) : (
          <>
            <span className="font-semibold">Platform-held issuer key.</span> This asset is not
            configured with an external issuer key, so the platform holds the issuer key and completes
            a clawback in one call rather than the two-phase issuer-signed flow.
          </>
        )}
      </div>

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
              {busy === 'clawback'
                ? external
                  ? 'Building…'
                  : 'Sweeping…'
                : external
                  ? 'Build sweep (you sign next)'
                  : 'Confirm full sweep'}
            </button>
          )}
        </div>

        {confirmClawback && !busy && (
          <p className="rounded-lg bg-rose-50 px-3 py-2 text-xs leading-relaxed text-rose-700">
            This seizes every confirmed {ticker} enclave UTXO of the holder into the issuer enclave,
            and the reason becomes public in the transparency log.{' '}
            {external
              ? 'Building broadcasts nothing: you sign the sweep with your issuer key in the next step, then the registrar co-signs. The on-chain seizure is irreversible once you complete it.'
              : 'It is irreversible on chain. Click confirm to proceed, or edit the fields to cancel.'}
          </p>
        )}

        {pending && (
          <div className="rounded-lg border border-seq-300 bg-seq-50/60 px-4 py-3 text-sm">
            <div className="flex items-center gap-2 font-medium text-ink-900">
              <Icon.lock width={16} height={16} className="text-seq-600" />
              Issuer signature required
            </div>
            <p className="mt-1.5 text-xs leading-relaxed text-ink-700/75">
              The sweep is built and the reason is already in the public transparency log, but nothing
              is swept yet. Sign the {pending.to_sign.length === 1 ? 'sighash' : `${pending.to_sign.length} sighashes`}{' '}
              with your SeqPal ID key to authorize the seizure of{' '}
              <span className="font-mono font-semibold text-ink-900">
                {fmtAssetAmount(pending.atoms, iss.precision || 8, ticker)}
              </span>
              . The platform then adds the policy co-signature and broadcasts. The issuer authorizes;
              the registrar co-signs.
            </p>
            <div className="mt-3 flex flex-wrap items-center gap-2">
              <button
                onClick={completeClawback}
                disabled={!!busy}
                className="inline-flex items-center gap-2 rounded-xl bg-rose-600 px-4 py-2 text-sm font-semibold text-white hover:bg-rose-700 disabled:opacity-60"
              >
                {busy === 'sign' ? 'Signing and completing…' : 'Sign and complete sweep'}
              </button>
              <button
                onClick={() => setPending(null)}
                disabled={!!busy}
                className="btn-outline disabled:opacity-60"
              >
                Cancel
              </button>
            </div>
            {!hasKey && (
              <p className="mt-2 text-xs font-medium text-amber-700">
                Connect your Sequentia wallet to sign. Cancelling leaves nothing swept; the reason stays in
                the log.
              </p>
            )}
          </div>
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
