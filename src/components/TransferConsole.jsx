import { useEffect, useMemo, useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import CopyId from './CopyId'
import * as api from '../lib/api'
import { usePoll } from '../lib/poll'
import { useStore } from '../lib/store'
import { fmtAssetAmount, scaleUp } from '../lib/format'
import { txUrl } from '../lib/chain'

const LOG_URL = '/openamp/v1/log'
const WALLET_URL = '/wallet'

// A person's captured travel-rule identity, one side of the counterparty pair.
function Party({ label, party }) {
  if (!party) return null
  return (
    <div className="rounded-lg border border-ink-900/10 bg-ink-900/[0.02] px-3 py-2.5">
      <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">{label}</div>
      <div className="mt-1 text-sm font-medium text-ink-900">{party.name || 'Name on file'}</div>
      {party.residence && <div className="text-xs text-ink-700/70">Residence: {party.residence}</div>}
      <div className="mt-1">
        <CopyId value={party.aid} label={`${label} AID`} />
      </div>
    </div>
  )
}

// The first-class refusal panel. A policy refusal is a HEADLINE result, not an
// error toast: the real reason the policy server returned is shown plainly, with
// a link to the public transparency-log entry that records it.
function RefusalPanel({ reason }) {
  return (
    <div className="mt-4 rounded-xl border border-rose-300 bg-rose-50 p-4">
      <div className="flex items-center gap-2">
        <Icon.lock width={17} height={17} className="text-rose-600" />
        <h3 className="font-bold text-rose-800">The policy server refused this transfer</h3>
        <Badge color="rose" className="ml-auto">
          Enforced on chain
        </Badge>
      </div>
      <p className="mt-2 text-sm leading-relaxed text-rose-800">{reason}</p>
      <p className="mt-2 text-[13px] leading-relaxed text-rose-700/90">
        This is the compliance restriction working. The asset could not move because the recipient,
        the lockup window, or the Reg S distribution-compliance window did not permit it. Nothing was
        transferred. The reason is recorded in the public transparency log.
      </p>
      <a
        href={LOG_URL}
        target="_blank"
        rel="noopener noreferrer"
        className="mt-3 inline-flex items-center gap-1.5 text-xs font-semibold text-rose-700 hover:underline"
      >
        <Icon.external width={13} height={13} /> Read the refusal in the transparency log
      </a>
    </div>
  )
}

function StateBadge({ state }) {
  const map = {
    settled: ['emerald', 'Settled'],
    refused: ['rose', 'Refused'],
    failed: ['amber', 'Failed'],
    completing: ['seq', 'Completing'],
    awaiting_signature: ['slate', 'Awaiting signature'],
  }
  const [color, label] = map[state] || ['slate', state]
  return <Badge color={color}>{label}</Badge>
}

// The secondary-market transfer surface (contract section 5), holder-facing. A
// holder sends a restricted asset to another SeqPal identity: seqpald builds the
// policy-co-signed transfer at openampd, the SPA signs the enclave sighashes with
// the holder's own key, and seqpald completes it. Both counterparties are captured
// (travel rule). A policy refusal is a first-class result. A holder who keeps the
// asset in a linked wallet transfers there instead; the /v1/log poller captures
// that and joins it to identities server-side.
export default function TransferConsole({ holdings }) {
  const { signTransferSigs, hasKey, account } = useStore()
  const { data: history, refresh: refreshHistory } = usePoll(() => api.listTransfers(), {
    intervalMs: 15000,
  })

  const sendable = useMemo(() => (holdings || []).filter((h) => h.assetId && h.atoms > 0), [holdings])
  const [asset, setAsset] = useState('')
  const [toAid, setToAid] = useState('')
  const [amount, setAmount] = useState('')
  const [confidential, setConfidential] = useState(false) // per-transfer choice, off by default
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const [built, setBuilt] = useState(null) // { transfer_id, to_sign, travel_rule, recipient_preflight }
  const [result, setResult] = useState(null) // { kind:'settled'|'refused', ... }

  // Whether this deployment accepts confidential transfers, probed once from
  // /api/health. The checkbox only renders when it does, so a transfer here
  // never discovers a 501 at build time.
  const [confAvailable, setConfAvailable] = useState(false)
  useEffect(() => {
    let cancelled = false
    api
      .health()
      .then((h) => {
        if (!cancelled) setConfAvailable(!!h.confidential)
      })
      .catch(() => {
        if (!cancelled) setConfAvailable(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  const selected = sendable.find((h) => h.assetId === asset) || null
  const atoms = selected ? scaleUp(amount, selected.precision || 8) : null

  const reset = () => {
    setBuilt(null)
    setResult(null)
    setErr(null)
  }

  const build = async () => {
    setErr(null)
    setResult(null)
    if (!selected) {
      setErr('Choose an asset to transfer.')
      return
    }
    const to = toAid.trim()
    if (!to) {
      setErr('Enter the beneficiary SeqPal ID (AID).')
      return
    }
    if (to === account?.aid) {
      setErr('The beneficiary must be a different identity.')
      return
    }
    if (!atoms || atoms === '0') {
      setErr('Enter a valid amount within the asset precision.')
      return
    }
    if (BigInt(atoms) > BigInt(Math.trunc(selected.atoms))) {
      setErr('That is more than your confirmed balance of this asset.')
      return
    }
    setBusy(true)
    try {
      const res = await api.buildTransfer({
        asset,
        to_aid: to,
        atoms: Number(atoms),
        confidential: confAvailable && confidential,
      })
      setBuilt(res)
    } catch (e) {
      // The server's message is user-presentable and shown verbatim (unregistered
      // beneficiary, insufficient balance, and the market-abuse gate all land here).
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  const complete = async () => {
    setErr(null)
    if (!hasKey) {
      setErr('Unlock your SeqPal ID to sign the transfer.')
      return
    }
    setBusy(true)
    try {
      let sigs
      try {
        sigs = signTransferSigs(built.to_sign)
      } catch (e) {
        setErr(e.message)
        return
      }
      if (!sigs) {
        setErr('Unlock your SeqPal ID to sign the transfer.')
        return
      }
      const res = await api.completeTransfer(built.transfer_id, { sigs })
      setResult({ kind: 'settled', txid: res.txid })
      setBuilt(null)
      setAmount('')
      setToAid('')
      refreshHistory()
    } catch (e) {
      // A 403 refusal is the headline result, not an error: render it as such.
      if (e.status === 403 && e.data?.refused) {
        setResult({ kind: 'refused', reason: e.data.reason || e.message })
        setBuilt(null)
        refreshHistory()
      } else {
        setErr(e.message)
      }
    } finally {
      setBusy(false)
    }
  }

  const rows = history?.transfers || []

  return (
    <div className="card p-6">
      <div className="flex items-center gap-2">
        <Icon.exchange width={18} height={18} className="text-seq-600" />
        <h2 className="font-bold text-ink-900">Transfer to another holder</h2>
        <Badge color="seq" className="ml-auto">
          Policy co-signed
        </Badge>
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
        Send a restricted asset to another SeqPal identity. The policy server co-signs every transfer
        and re-checks eligibility, so it can refuse an ineligible recipient, a resale inside the
        lockup window, or a Reg S distribution-compliance window. Both counterparties are captured for
        the travel rule.
      </p>

      {sendable.length === 0 ? (
        <div className="mt-4 rounded-lg bg-ink-900/[0.03] px-4 py-3 text-sm text-ink-700/75">
          You hold no SeqPal-managed asset to transfer yet. A position appears here once an asset is
          delivered to your enclave account.
        </div>
      ) : (
        <div className="mt-4 space-y-3">
          <div>
            <label className="label" htmlFor="tr-asset">
              Asset
            </label>
            <select
              id="tr-asset"
              className="select"
              value={asset}
              onChange={(e) => {
                setAsset(e.target.value)
                reset()
              }}
            >
              <option value="">Choose an asset…</option>
              {sendable.map((h) => (
                <option key={h.assetId} value={h.assetId}>
                  {h.name} ({h.ticker}) · {fmtAssetAmount(h.atoms, h.precision || 8)} available
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className="label" htmlFor="tr-to">
              Beneficiary SeqPal ID (AID)
            </label>
            <input
              id="tr-to"
              className="input font-mono text-xs"
              placeholder="the recipient's account id"
              value={toAid}
              onChange={(e) => {
                setToAid(e.target.value)
                reset()
              }}
            />
            <p className="mt-1 text-xs text-ink-700/60">
              The beneficiary must be a registered SeqPal identity: a P2P transfer captures both
              counterparties.
            </p>
          </div>
          <div>
            <label className="label" htmlFor="tr-amount">
              Amount {selected ? `(${selected.ticker})` : ''}
            </label>
            <input
              id="tr-amount"
              className="input"
              inputMode="decimal"
              placeholder="0.00"
              value={amount}
              onChange={(e) => {
                setAmount(e.target.value)
                reset()
              }}
            />
            {selected && atoms && atoms !== '0' && (
              <p className="mt-1 font-mono text-xs text-ink-700/60">
                {Number(atoms).toLocaleString('en-US')} atoms
              </p>
            )}
          </div>

          {confAvailable && (
            <label className="flex cursor-pointer items-start gap-3 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3">
              <input
                type="checkbox"
                className="mt-1 h-4 w-4 accent-seq-600"
                checked={confidential}
                onChange={(e) => {
                  setConfidential(e.target.checked)
                  reset()
                }}
              />
              <span className="text-sm">
                <span className="font-medium text-ink-900">Transfer confidentially</span>
                <span className="block text-xs leading-relaxed text-ink-700/70">
                  Hide the amount and asset of this transfer from outside observers. Your registrar
                  still sees it, as the rules require. The other party receives it like any other
                  transfer.
                </span>
              </span>
            </label>
          )}

          {err && <p className="text-sm font-medium text-rose-600">{err}</p>}

          {!built && !result && (
            <button
              onClick={build}
              disabled={busy || !asset || !toAid.trim() || !amount.trim()}
              className="btn-primary w-full disabled:opacity-60"
            >
              {busy ? 'Building…' : 'Build transfer'}
            </button>
          )}
        </div>
      )}

      {/* Built: travel-rule review + advisory preflight, then sign and complete. */}
      {built && (
        <div className="mt-5 space-y-3 rounded-xl border border-ink-900/10 bg-ink-900/[0.015] p-4">
          <div className="flex items-center gap-2">
            <Icon.shield width={16} height={16} className="text-seq-600" />
            <h3 className="font-semibold text-ink-900">Confirm and sign</h3>
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <Party label="Originator" party={built.travel_rule?.originator} />
            <Party label="Beneficiary" party={built.travel_rule?.beneficiary} />
          </div>
          {built.recipient_preflight && built.recipient_preflight.eligible === false && (
            <div className="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2.5 text-sm">
              <div className="font-medium text-amber-800">
                Advisory: the policy server may refuse this recipient
              </div>
              {Array.isArray(built.recipient_preflight.reasons) &&
                built.recipient_preflight.reasons.length > 0 && (
                  <ul className="mt-1 list-inside list-disc text-xs text-amber-700">
                    {built.recipient_preflight.reasons.map((r, i) => (
                      <li key={i}>{r}</li>
                    ))}
                  </ul>
                )}
              <p className="mt-1 text-xs text-amber-700/80">
                This is a preview. The policy server decides authoritatively when you complete: if it
                refuses, the reason is shown here and recorded in the transparency log. Nothing moves
                unless it co-signs.
              </p>
            </div>
          )}
          {!hasKey && (
            <p className="text-xs text-amber-700">
              Your SeqPal ID key is locked. Sign in again to unlock it before signing.
            </p>
          )}
          <div className="flex flex-wrap gap-2">
            <button onClick={complete} disabled={busy || !hasKey} className="btn-primary disabled:opacity-60">
              <Icon.shield width={15} height={15} />
              {busy ? 'Signing…' : 'Sign and complete'}
            </button>
            <button onClick={reset} disabled={busy} className="btn-outline disabled:opacity-60">
              Cancel
            </button>
          </div>
        </div>
      )}

      {/* Settled result. */}
      {result?.kind === 'settled' && (
        <div className="mt-4 rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm">
          <div className="flex items-center gap-2 font-medium text-emerald-800">
            <Icon.check width={16} height={16} className="text-emerald-500" /> Transfer broadcast
          </div>
          {result.txid && (
            <a
              href={txUrl(result.txid)}
              target="_blank"
              rel="noopener noreferrer"
              className="mt-2 inline-block font-mono text-xs text-seq-600 hover:underline"
            >
              {result.txid.slice(0, 12)}…{result.txid.slice(-8)}
            </a>
          )}
          <p className="mt-1.5 text-xs leading-relaxed text-emerald-700/90">
            The transfer is on chain. Nothing is final at 0 confirmations: a Sequentia state is only as
            final as its Bitcoin anchor is deep.
          </p>
        </div>
      )}

      {/* First-class refusal. */}
      {result?.kind === 'refused' && <RefusalPanel reason={result.reason} />}

      {/* Wallet-linked handoff. */}
      <div className="mt-5 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3">
        <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
          Holding this asset in a linked wallet?
        </div>
        <p className="mt-1.5 text-sm leading-relaxed text-ink-700/75">
          Transfer it from the wallet's own send screen. The same policy server co-signs it, and the
          transparency-log watcher captures the transfer and joins it to both SeqPal identities, so it
          appears in your history below.
        </p>
        <a
          href={WALLET_URL}
          target="_blank"
          rel="noopener noreferrer"
          className="mt-2 inline-flex items-center gap-1.5 text-sm font-medium text-seq-600 hover:underline"
        >
          <Icon.external width={14} height={14} /> Open the Sequentia wallet
        </a>
      </div>

      {/* History. */}
      {rows.length > 0 && (
        <div className="mt-6">
          <h3 className="text-sm font-semibold text-ink-900">Your transfers</h3>
          <div className="mt-2 space-y-2">
            {rows.map(({ transfer: t, travel_rule: tr }) => (
              <div key={t.id} className="rounded-lg border border-ink-900/10 px-3 py-2.5 text-sm">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="flex items-center gap-2">
                    <StateBadge state={t.state} />
                    <span className="text-ink-700/80">
                      {Number(t.atoms).toLocaleString('en-US')} atoms
                    </span>
                    <Badge color="slate">{t.source === 'wallet' ? 'Wallet' : 'In-app'}</Badge>
                    {t.confidential && <Badge color="seq">Confidential</Badge>}
                  </div>
                  {t.txid && (
                    <a
                      href={txUrl(t.txid)}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="font-mono text-xs text-seq-600 hover:underline"
                    >
                      {t.txid.slice(0, 10)}…{t.txid.slice(-6)}
                    </a>
                  )}
                </div>
                <div className="mt-1 text-xs text-ink-700/60">
                  To {tr?.beneficiary?.name || (t.beneficiary_aid ? `${t.beneficiary_aid.slice(0, 8)}…` : 'unresolved recipient')}
                  {tr?.beneficiary?.residence ? ` · ${tr.beneficiary.residence}` : ''}
                </div>
                {t.state === 'refused' && t.reason && (
                  <div className="mt-1.5 rounded-md bg-rose-50 px-2.5 py-1.5 text-xs text-rose-700">
                    Refused: {t.reason}{' '}
                    <a href={LOG_URL} target="_blank" rel="noopener noreferrer" className="font-medium underline">
                      log
                    </a>
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      <p className="mt-5 text-[11px] leading-relaxed text-ink-700/55">
        A refusal is a real policy decision on the live server, not a client-side check: the asset can
        only move between holders the policy server recognises. Nothing is final at 0 confirmations.
      </p>
    </div>
  )
}
