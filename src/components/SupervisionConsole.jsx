import { useCallback, useEffect, useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import * as api from '../lib/api'
import { useStore } from '../lib/store'
import { txUrl } from '../lib/chain'

// Court-ordered freezes for a freely-tradable (bearer) asset. There are no
// transfer restrictions on these tokens; the ONE intervention the issuer keeps
// is freezing a specific balance when a court or regulator orders it, and the
// network itself enforces the freeze. The flow is two-phase like a clawback:
// the platform builds the freeze, then the issuer signs the 32-byte message in
// this browser with their own key (store.signSupervision), and only a completed
// operation takes effect. The order document is hashed IN the browser; only its
// fingerprint (sha256) is sent, and that fingerprint is recorded publicly
// beside the freeze.

async function sha256File(file) {
  const buf = await file.arrayBuffer()
  const digest = await crypto.subtle.digest('SHA-256', buf)
  return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, '0')).join('')
}

// One supervision operation from this platform's history: it carries the human
// context (reason, order fingerprint) beside the chain facts (state, txid).
function OpRow({ op }) {
  const lifted = op.kind === 'unfreeze'
  return (
    <div className="rounded-lg bg-ink-900/[0.03] px-3 py-2 text-sm">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="break-all font-mono text-xs text-ink-900">
          {op.target || op.target_hash}
        </span>
        <Badge color={lifted ? 'emerald' : op.state === 'submitted' ? 'rose' : 'amber'}>
          {lifted ? 'freeze lifted' : op.state === 'submitted' ? 'frozen' : op.state}
        </Badge>
      </div>
      {op.reason && <p className="mt-1 text-xs text-ink-700/75">{op.reason}</p>}
      <div className="mt-1 flex flex-wrap items-center gap-3 font-mono text-[11px] text-ink-700/55">
        {op.order_hash && (
          <span title="order document fingerprint">order {op.order_hash.slice(0, 12)}…</span>
        )}
        {op.txid && (
          <a
            href={txUrl(op.txid)}
            target="_blank"
            rel="noopener noreferrer"
            className="text-seq-600 hover:underline"
          >
            {op.txid.slice(0, 10)}…{op.txid.slice(-6)}
          </a>
        )}
      </div>
    </div>
  )
}

export default function SupervisionConsole({ iss }) {
  const { signSupervision, hasKey } = useStore()
  const [sup, setSup] = useState(null) // null = loading; the GET /supervision response
  const [address, setAddress] = useState('')
  const [reason, setReason] = useState('')
  const [orderHash, setOrderHash] = useState('')
  const [orderName, setOrderName] = useState('')
  const [busy, setBusy] = useState(null) // 'freeze' | 'unfreeze' | 'sign' | null
  const [err, setErr] = useState(null)
  const [done, setDone] = useState(null)
  // A built operation awaiting the issuer's signature: { op_id, to_sign, action }.
  const [pending, setPending] = useState(null)

  const load = useCallback(() => {
    api
      .supervision(iss.id)
      .then((r) => setSup(r))
      .catch((e) => {
        setSup({ freezes: [], ops: [] })
        setErr(e.message)
      })
  }, [iss.id])
  useEffect(load, [load])

  const onOrderFile = async (file) => {
    if (!file) return
    setErr(null)
    try {
      setOrderHash(await sha256File(file))
      setOrderName(file.name)
    } catch {
      setErr('Could not read the order document in this browser.')
    }
  }

  const start = async (action) => {
    setErr(null)
    setDone(null)
    setPending(null)
    const a = address.trim()
    const rsn = reason.trim()
    if (!a) return setErr('Enter the address whose balance the order covers.')
    if (!rsn) return setErr('A reason is required. It is recorded publicly beside the freeze.')
    if (action === 'freeze' && !orderHash)
      return setErr('Attach the court or regulator order document. Only its fingerprint is sent.')
    setBusy(action)
    try {
      const res = await api.supervisionStart(iss.id, action, {
        target_address: a,
        reason: rsn,
        order_hash: orderHash,
      })
      setPending({
        op_id: action === 'unfreeze' ? res.unfreeze_id : res.freeze_id,
        to_sign: res.to_sign,
        action,
      })
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(null)
    }
  }

  const complete = async () => {
    setErr(null)
    if (!hasKey) {
      setErr('Connect your Sequentia wallet to sign. Nothing takes effect until you sign.')
      return
    }
    setBusy('sign')
    try {
      let sig
      try {
        sig = signSupervision(pending.to_sign)
      } catch (e) {
        setErr(e.message)
        return
      }
      if (!sig) {
        setErr('Connect your Sequentia wallet to sign.')
        return
      }
      await api.supervisionComplete(iss.id, pending.action, pending.op_id, { sig })
      setDone(pending.action === 'freeze' ? 'Balance frozen.' : 'Freeze lifted.')
      setPending(null)
      setAddress('')
      setReason('')
      setOrderHash('')
      setOrderName('')
      load()
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
        <h2 className="font-bold text-ink-900">Court-ordered freezes</h2>
        <Badge color="rose" className="ml-auto">
          Court orders only
        </Badge>
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
        This token trades freely, so the one intervention you keep is freezing a specific
        balance when a court or regulator orders it. Freezing needs a court or regulator
        order. The order document&rsquo;s fingerprint is recorded publicly beside the
        freeze, and the network itself enforces it. Your signature, made in your own wallet,
        authorizes each freeze and each lift.
      </p>

      <div className="mt-4">
        <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
          Freeze history
        </div>
        {sup === null ? (
          <div className="mt-2 flex justify-center py-4">
            <span className="h-5 w-5 animate-spin rounded-full border-2 border-btc/20 border-t-btc" />
          </div>
        ) : (sup.ops || []).length === 0 ? (
          <p className="mt-2 text-sm text-ink-700/60">No balance is frozen.</p>
        ) : (
          <div className="mt-2 space-y-2">
            {(sup.ops || []).map((op) => (
              <OpRow key={op.id} op={op} />
            ))}
          </div>
        )}
        {sup && (sup.freezes || []).length > 0 && (
          <p className="mt-2 text-[11px] text-ink-700/55">
            The network currently enforces {(sup.freezes || []).length} frozen{' '}
            {(sup.freezes || []).length === 1 ? 'address' : 'addresses'} for this token.
          </p>
        )}
      </div>

      <div className="mt-5 space-y-3 border-t border-ink-900/10 pt-4">
        <div>
          <label className="label" htmlFor="sup-address">
            Address named in the order
          </label>
          <input
            id="sup-address"
            className="input font-mono text-xs"
            placeholder="the address holding the balance to freeze"
            value={address}
            onChange={(e) => setAddress(e.target.value)}
          />
        </div>
        <div>
          <label className="label" htmlFor="sup-reason">
            Reason (recorded publicly)
          </label>
          <input
            id="sup-reason"
            className="input"
            placeholder="e.g. asset-freeze order, case number"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
        </div>
        <div>
          <label className="label" htmlFor="sup-order">
            Court or regulator order document
          </label>
          <input
            id="sup-order"
            type="file"
            className="block w-full text-sm text-ink-700 file:mr-3 file:rounded-lg file:border-0 file:bg-ink-900/[0.05] file:px-3 file:py-2 file:text-sm file:font-semibold file:text-ink-800"
            onChange={(e) => onOrderFile(e.target.files?.[0])}
          />
          {orderHash && (
            <p className="mt-1 font-mono text-[11px] text-ink-700/60">
              {orderName}: fingerprint {orderHash.slice(0, 16)}… (only this fingerprint is
              sent and published)
            </p>
          )}
        </div>

        {err && <p className="text-sm font-medium text-rose-600">{err}</p>}
        {done && <p className="text-sm font-medium text-emerald-600">{done}</p>}

        <div className="flex flex-wrap gap-2">
          <button
            onClick={() => start('freeze')}
            disabled={!!busy}
            className="btn-outline border-rose-300 text-rose-700 hover:bg-rose-50 disabled:opacity-60"
          >
            {busy === 'freeze' ? 'Building…' : 'Freeze this balance (you sign next)'}
          </button>
          <button
            onClick={() => start('unfreeze')}
            disabled={!!busy}
            className="btn-outline disabled:opacity-60"
          >
            {busy === 'unfreeze' ? 'Building…' : 'Lift a freeze (you sign next)'}
          </button>
        </div>

        {pending && (
          <div className="rounded-lg border border-seq-300 bg-seq-50/60 px-4 py-3 text-sm">
            <div className="flex items-center gap-2 font-medium text-ink-900">
              <Icon.lock width={16} height={16} className="text-seq-600" />
              Your signature is required
            </div>
            <p className="mt-1.5 text-xs leading-relaxed text-ink-700/75">
              The {pending.action === 'freeze' ? 'freeze' : 'lift'} is built but nothing has
              taken effect. Sign with your SeqPal ID key to authorize it; the network then
              enforces it.
            </p>
            <div className="mt-3 flex flex-wrap items-center gap-2">
              <button
                onClick={complete}
                disabled={!!busy}
                className="inline-flex items-center gap-2 rounded-xl bg-rose-600 px-4 py-2 text-sm font-semibold text-white hover:bg-rose-700 disabled:opacity-60"
              >
                {busy === 'sign' ? 'Signing…' : 'Sign and apply'}
              </button>
              <button onClick={() => setPending(null)} disabled={!!busy} className="btn-outline disabled:opacity-60">
                Cancel
              </button>
            </div>
            {!hasKey && (
              <p className="mt-2 text-xs font-medium text-amber-700">
                Connect your Sequentia wallet to sign. Cancelling leaves everything unchanged.
              </p>
            )}
          </div>
        )}
      </div>

      <p className="mt-4 text-[11px] leading-relaxed text-ink-700/55">
        Nothing is final at 0 confirmations: a Sequentia state is only as final as its
        Bitcoin anchor is deep.
      </p>
    </div>
  )
}
