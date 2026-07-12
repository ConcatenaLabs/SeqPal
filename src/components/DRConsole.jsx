import { useRef, useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import * as api from '../lib/api'
import { usePoll } from '../lib/poll'
import { fmtAssetAmount } from '../lib/format'
import { txUrl } from '../lib/chain'

// A fresh idempotency key for a DR supply operation. A retry of the same op (for
// example after a re-blinding 202) reuses it, so the operation never double-mints
// or double-burns server-side.
const newRequestID = (kind) =>
  `dr-${kind}-${(crypto.randomUUID?.() || String(Date.now()) + Math.random().toString(16).slice(2))}`

function OpRow({ op }) {
  const isMint = op.kind === 'mint'
  return (
    <div className="rounded-lg border border-ink-900/10 px-3 py-2.5 text-sm">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <Badge color={isMint ? 'emerald' : 'rose'}>{isMint ? 'Mint (reissuance)' : 'Redeem (burn)'}</Badge>
          <span className="font-mono text-ink-700/80">{Number(op.atoms).toLocaleString('en-US')} atoms</span>
          {op.state !== 'broadcast' && <Badge color="amber">{op.state}</Badge>}
        </div>
        {op.txid && (
          <a
            href={txUrl(op.txid)}
            target="_blank"
            rel="noopener noreferrer"
            className="font-mono text-xs text-seq-600 hover:underline"
          >
            {op.txid.slice(0, 10)}…{op.txid.slice(-6)}
          </a>
        )}
      </div>
      {(op.attestation_hash || op.anchor_txid) && (
        <div className="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
          {op.attestation_hash && (
            <a
              href={api.docUrl(op.attestation_hash)}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 text-seq-600 hover:underline"
            >
              <Icon.doc width={12} height={12} /> Custodian attestation (simulated)
            </a>
          )}
          {op.anchor_txid && (
            <a
              href={txUrl(op.anchor_txid)}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 text-ink-700/70 hover:underline"
            >
              <Icon.link width={12} height={12} /> anchored
            </a>
          )}
        </div>
      )}
      {op.error && <p className="mt-1 text-xs text-rose-600">{op.error}</p>}
    </div>
  )
}

// The Depository-Receipt programme console (contract section 6), owner-facing. It
// enables the programme (which enforces the US-person exclusion as a REAL
// policy-server category rule, not a display string), mints depository receipts
// with a real OA-6 reissuance, and redeems them with a real OA-5 burn. Circulating
// supply is chain-derived on every read, never a stored counter. Each completed
// operation carries a labeled-simulated custodian attestation whose hash is
// anchored on chain.
export default function DRConsole({ iss }) {
  const { data, refresh } = usePoll(() => api.drProgram(iss.id), { intervalMs: 15000 })
  const prog = data?.program
  const enabled = !!prog?.enabled
  const ops = data?.ops || []
  const precision = iss.precision || 8

  const [busy, setBusy] = useState(null) // 'enable' | 'mint' | 'redeem' | null
  const [err, setErr] = useState(null)
  const [notice, setNotice] = useState(null)
  const [mintAtoms, setMintAtoms] = useState('')
  const [mintTarget, setMintTarget] = useState('')
  const [redeemAtoms, setRedeemAtoms] = useState('')
  const [redeemHolder, setRedeemHolder] = useState('')
  const mintReq = useRef(null)
  const redeemReq = useRef(null)

  const enable = async () => {
    setErr(null)
    setNotice(null)
    setBusy('enable')
    try {
      await api.drEnable(iss.id)
      refresh()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(null)
    }
  }

  const mint = async () => {
    setErr(null)
    setNotice(null)
    const atoms = Number(String(mintAtoms).replace(/[^0-9]/g, ''))
    if (!atoms) {
      setErr('Enter a positive atom amount to mint.')
      return
    }
    if (!mintReq.current) mintReq.current = newRequestID('mint')
    setBusy('mint')
    try {
      const body = { atoms, request_id: mintReq.current }
      if (mintTarget.trim()) body.target_aid = mintTarget.trim()
      const res = await api.drMint(iss.id, body)
      if (res.state === 'reblinding') {
        // The reissuance token is being re-blinded (the phantom-tx hazard). Keep
        // the same request_id and let the issuer retry once it confirms.
        setNotice(
          'The reissuance token is being re-blinded on chain. Click Mint again in a few seconds to finish; the same idempotency key is reused, so it will not double-mint.'
        )
      } else {
        mintReq.current = null
        setMintAtoms('')
        setMintTarget('')
        setNotice('Reissuance broadcast. Supply is chain-derived and updates as it confirms.')
      }
      refresh()
    } catch (e) {
      // RETAIN the idempotency key on error. An error may mean the reissuance was
      // already broadcast but the response was lost; a retry must reuse the SAME
      // request_id so the policy server returns the same txid instead of minting a
      // second time. The key is cleared only on a definitive success (above).
      setErr(e.message)
    } finally {
      setBusy(null)
    }
  }

  const redeem = async () => {
    setErr(null)
    setNotice(null)
    const atoms = Number(String(redeemAtoms).replace(/[^0-9]/g, ''))
    if (!atoms) {
      setErr('Enter a positive atom amount to redeem.')
      return
    }
    if (!redeemReq.current) redeemReq.current = newRequestID('redeem')
    setBusy('redeem')
    try {
      const body = { atoms, request_id: redeemReq.current }
      if (redeemHolder.trim()) body.holder_aid = redeemHolder.trim()
      await api.drRedeem(iss.id, body)
      redeemReq.current = null
      setRedeemAtoms('')
      setRedeemHolder('')
      setNotice('Burn broadcast. Circulating supply falls as it confirms.')
      refresh()
    } catch (e) {
      // Retain the idempotency key on error (same reasoning as mint): a retry reuses
      // the same request_id so a lost-response burn is never repeated.
      setErr(e.message)
    } finally {
      setBusy(null)
    }
  }

  return (
    <div className="card p-6">
      <div className="flex items-center gap-2">
        <Icon.coins width={18} height={18} className="text-seq-600" />
        <h2 className="font-bold text-ink-900">Depository-Receipt programme</h2>
        <Badge color={enabled ? 'emerald' : 'slate'} className="ml-auto">
          {enabled ? 'Enabled' : 'Not enabled'}
        </Badge>
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
        Mint depository receipts with a real reissuance and redeem them with a real burn. Circulating
        supply is chain-derived on every read, never a stored counter. Enabling the programme enforces
        the US-person exclusion (17 CFR 230.902(k)) as a real policy-server category rule, so a
        secondary transfer to a US-person holder is refused on chain, not merely labeled.
      </p>

      {/* Chain-derived supply. */}
      <div className="mt-4 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3">
        <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
          Circulating supply (chain-derived)
        </div>
        <div className="mt-1 font-mono text-2xl font-bold text-ink-900">
          {fmtAssetAmount(data?.circulating_atoms || 0, precision, iss.ticker)}
        </div>
        <div className="mt-0.5 font-mono text-xs text-ink-700/55">
          {Number(data?.circulating_atoms || 0).toLocaleString('en-US')} atoms
          {data?.height ? ` · as of Sequentia block ${Number(data.height).toLocaleString('en-US')}` : ''}
        </div>
      </div>

      {!enabled ? (
        <button onClick={enable} disabled={busy === 'enable'} className="btn-primary mt-4 w-full disabled:opacity-60">
          {busy === 'enable' ? 'Enabling…' : 'Enable programme and enforce the US-person exclusion'}
        </button>
      ) : (
        <>
          {prog?.us_exclusion_height ? (
            <div className="mt-4 flex items-start gap-2 rounded-lg border border-emerald-200 bg-emerald-50 px-3 py-2.5 text-sm">
              <Icon.shield width={16} height={16} className="mt-0.5 shrink-0 text-emerald-600" />
              <span className="text-emerald-800">
                US-person exclusion is a live policy rule: a permanent{' '}
                <span className="font-mono text-xs">j:US</span> category deny, applied through the
                amendment chain. A US-person recipient is refused on the secondary market.
              </span>
            </div>
          ) : null}

          <div className="mt-4 grid gap-5 sm:grid-cols-2">
            {/* Mint */}
            <div className="rounded-xl border border-ink-900/10 p-4">
              <div className="flex items-center gap-2">
                <Icon.arrowRight width={15} height={15} className="text-emerald-600" />
                <h3 className="font-semibold text-ink-900">Mint (reissuance)</h3>
              </div>
              <p className="mt-1 text-xs text-ink-700/65">
                Reissue more units into the custodian enclave (default: the offering escrow).
              </p>
              <label className="label mt-3" htmlFor="dr-mint-atoms">
                Atoms
              </label>
              <input
                id="dr-mint-atoms"
                className="input"
                inputMode="numeric"
                placeholder="e.g. 100000000"
                value={mintAtoms}
                onChange={(e) => setMintAtoms(e.target.value)}
              />
              <label className="label mt-2" htmlFor="dr-mint-target">
                Target enclave AID (optional)
              </label>
              <input
                id="dr-mint-target"
                className="input font-mono text-xs"
                placeholder="defaults to the offering escrow"
                value={mintTarget}
                onChange={(e) => setMintTarget(e.target.value)}
              />
              <button onClick={mint} disabled={busy === 'mint'} className="btn-outline mt-3 w-full disabled:opacity-60">
                {busy === 'mint' ? 'Reissuing…' : 'Mint'}
              </button>
            </div>

            {/* Redeem */}
            <div className="rounded-xl border border-ink-900/10 p-4">
              <div className="flex items-center gap-2">
                <Icon.lock width={15} height={15} className="text-rose-600" />
                <h3 className="font-semibold text-ink-900">Redeem (burn)</h3>
              </div>
              <p className="mt-1 text-xs text-ink-700/65">
                Burn units held in a custodied enclave, reducing supply for real.
              </p>
              <label className="label mt-3" htmlFor="dr-redeem-atoms">
                Atoms
              </label>
              <input
                id="dr-redeem-atoms"
                className="input"
                inputMode="numeric"
                placeholder="e.g. 100000000"
                value={redeemAtoms}
                onChange={(e) => setRedeemAtoms(e.target.value)}
              />
              <label className="label mt-2" htmlFor="dr-redeem-holder">
                Custodied holder AID (optional)
              </label>
              <input
                id="dr-redeem-holder"
                className="input font-mono text-xs"
                placeholder="defaults to the offering escrow"
                value={redeemHolder}
                onChange={(e) => setRedeemHolder(e.target.value)}
              />
              <button onClick={redeem} disabled={busy === 'redeem'} className="btn-outline mt-3 w-full disabled:opacity-60">
                {busy === 'redeem' ? 'Burning…' : 'Redeem'}
              </button>
            </div>
          </div>
        </>
      )}

      {notice && <p className="mt-3 rounded-lg bg-seq/5 px-3 py-2 text-xs text-seq-700">{notice}</p>}
      {err && <p className="mt-3 text-sm font-medium text-rose-600">{err}</p>}

      {ops.length > 0 && (
        <div className="mt-5">
          <h3 className="text-sm font-semibold text-ink-900">Supply operations</h3>
          <div className="mt-2 space-y-2">
            {ops.map((op) => (
              <OpRow key={op.id} op={op} />
            ))}
          </div>
        </div>
      )}

      <p className="mt-5 text-[11px] leading-relaxed text-ink-700/55">
        The custodian is SIMULATED and labeled as such: each operation produces a real, content-
        addressed attestation whose hash is anchored on the Sequentia network. The supply figure and
        the reissue and burn are real. A redeem burns from a custodied enclave; a holder-signed burn
        goes through the wallet. Nothing is final at 0 confirmations.
      </p>
    </div>
  )
}
