import { useCallback, useEffect, useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import * as api from '../lib/api'
import { useStore } from '../lib/store'
import { txUrl } from '../lib/chain'

// Who can hold this token, and court-ordered freezes, for a token whose rules
// the network enforces.
//
// There is no transfer for this platform to approve or refuse: the network reads
// a published list on every transfer, so the two controls the issuer keeps are
// changing that list and stopping one specific coin. Both are published, and
// neither takes effect the moment the issuer presses a button. That is stated on
// this surface rather than left to be discovered.
//
// The custody line is the same as everywhere else on the platform: nothing is
// signed here except by the issuer, in this browser, with their own key. The
// order document is hashed in the browser and only its fingerprint is sent and
// published.

async function sha256File(file) {
  const buf = await file.arrayBuffer()
  const digest = await crypto.subtle.digest('SHA-256', buf)
  return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, '0')).join('')
}

const shortKey = (k) => (k && k.length > 16 ? `${k.slice(0, 10)}…${k.slice(-6)}` : k || '')

// One holder on the published list, with any dates that bind them.
function HolderRow({ holder }) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg bg-ink-900/[0.03] px-3 py-2 text-sm">
      <span className="break-all font-mono text-xs text-ink-900">{shortKey(holder.key)}</span>
      <span className="text-[11px] text-ink-700/60">
        {holder.can_send_from_block
          ? `cannot sell until block ${holder.can_send_from_block}`
          : holder.can_receive_from_block
            ? `cannot be paid until block ${holder.can_receive_from_block}`
            : 'no holding period'}
      </span>
    </div>
  )
}

// One change this platform made, with the human context beside the chain facts.
function OpRow({ op }) {
  const lifted = op.kind === 'unfreeze'
  return (
    <div className="rounded-lg bg-ink-900/[0.03] px-3 py-2 text-sm">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="font-medium text-ink-900">
          {lifted ? 'Restored' : 'Blocked'}
          {op.seq ? ` · list version ${op.seq}` : ''}
        </span>
        <Badge
          color={op.state === 'published' ? (lifted ? 'emerald' : 'rose') : 'amber'}
        >
          {op.state === 'published' ? (lifted ? 'restored' : 'blocked') : op.state}
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

export default function PolicyConsole({ iss }) {
  const { signSupervision, hasKey } = useStore()
  const [pol, setPol] = useState(null) // null = loading
  const [holder, setHolder] = useState('')
  const [coin, setCoin] = useState('')
  const [reason, setReason] = useState('')
  const [orderHash, setOrderHash] = useState('')
  const [orderName, setOrderName] = useState('')
  const [busy, setBusy] = useState(null)
  const [err, setErr] = useState(null)
  const [done, setDone] = useState(null)
  // A built change awaiting the issuer's signature: { op_id, to_sign, action }.
  const [pending, setPending] = useState(null)
  // The registrar handoff, when the change is signed but not yet published.
  const [handoff, setHandoff] = useState(null)
  const [program, setProgram] = useState('')
  const [rulesTx, setRulesTx] = useState('')

  const load = useCallback(() => {
    api
      .policy(iss.id)
      .then((r) => setPol(r))
      .catch((e) => {
        setPol({ ops: [] })
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

  const targets = () => {
    const holders = holder.trim() ? [holder.trim().toLowerCase()] : []
    const coins = []
    const c = coin.trim()
    if (c) {
      const [txid, vout] = c.split(':')
      coins.push({ txid: (txid || '').trim().toLowerCase(), vout: Number(vout || 0) })
    }
    return { holders, coins }
  }

  const start = async (action) => {
    setErr(null)
    setDone(null)
    setPending(null)
    setHandoff(null)
    const { holders, coins } = targets()
    if (!holders.length && !coins.length)
      return setErr('Name the holder, or the coin, this change covers.')
    if (!reason.trim())
      return setErr('A reason is required. It is published beside the change.')
    if (!orderHash)
      return setErr('Attach the court or regulator order document. Only its fingerprint is sent.')
    setBusy(action)
    try {
      const res = await api.policyStart(iss.id, action, {
        holders,
        coins,
        reason: reason.trim(),
        order_hash: orderHash,
      })
      setPending({ op_id: res.op_id, to_sign: res.to_sign, action })
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(null)
    }
  }

  // Sign, then try to publish. The first attempt is expected to come back asking
  // for the registrar's two values, which is a step in the flow: it hands over
  // the document to compile against.
  const complete = async (extra) => {
    setErr(null)
    if (!hasKey) {
      setErr('Unlock your SeqPal ID to sign. Nothing is published until you sign.')
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
        setErr('Unlock your SeqPal ID to sign.')
        return
      }
      const res = await api.policyComplete(iss.id, pending.op_id, { sig, ...(extra || {}) })
      setDone(
        pending.action === 'freeze'
          ? 'Change published. It binds transfers once the rules transaction confirms.'
          : 'Restored. It takes effect once the rules transaction confirms.',
      )
      setPending(null)
      setHandoff(null)
      setHolder('')
      setCoin('')
      setReason('')
      setOrderHash('')
      setOrderName('')
      setProgram('')
      setRulesTx('')
      load()
      return res
    } catch (e) {
      // The registrar step: the change is signed, and what is missing is the
      // compiled rules program and the finished rules transaction.
      if (e.status === 409 && e.data?.registrar_document) {
        setHandoff(e.data)
      } else {
        setErr(e.message)
      }
    } finally {
      setBusy(null)
    }
  }

  const published = pol?.published

  return (
    <div className="card p-6">
      <div className="flex items-center gap-2">
        <Icon.users width={18} height={18} className="text-ink-700" />
        <h2 className="font-bold text-ink-900">Who can hold this token</h2>
        <Badge color="btc" className="ml-auto">
          Published rules
        </Badge>
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
        The network checks every transfer of this token against a list you publish. Adding
        or removing a holder, and blocking one specific coin, are the two controls you
        keep. Both take effect when the updated list is published and the on-chain rules
        move onto it, not the moment you press a button, so a change is not instant.
      </p>
      <p className="mt-2 rounded-lg bg-ink-900/[0.03] px-3 py-2 text-xs leading-relaxed text-ink-700/75">
        A holder can combine at most{' '}
        {pol?.max_coins_per_transfer === 1 ? 'one of their coins' : 'two of their coins'} of
        this token in a single transfer. A holder with more makes more than one transfer.
        This is fixed when the token is created and cannot be raised later.
      </p>

      <div className="mt-4">
        <div className="flex items-center justify-between">
          <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
            Approved holders
          </div>
          {published && (
            <span className="font-mono text-[11px] text-ink-700/55">
              list version {published.seq}
            </span>
          )}
        </div>
        {pol === null ? (
          <div className="mt-2 flex justify-center py-4">
            <span className="h-5 w-5 animate-spin rounded-full border-2 border-btc/20 border-t-btc" />
          </div>
        ) : !published ? (
          <p className="mt-2 text-sm text-ink-700/60">
            The published list could not be read right now.
          </p>
        ) : (
          <div className="mt-2 space-y-2">
            {(published.holders || []).map((h) => (
              <HolderRow key={h.key} holder={h} />
            ))}
            <p className="text-[11px] text-ink-700/55">
              {published.frozen_coin_count || 0} blocked{' '}
              {published.frozen_coin_count === 1 ? 'coin' : 'coins'} in the published list.
              A blocked coin is published as a fingerprint, so the list never reveals which
              coin it is.
            </p>
          </div>
        )}
      </div>

      {(pol?.ops || []).length > 0 && (
        <div className="mt-5">
          <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
            Court-ordered changes
          </div>
          <div className="mt-2 space-y-2">
            {(pol.ops || []).map((op) => (
              <OpRow key={op.id} op={op} />
            ))}
          </div>
        </div>
      )}

      <div className="mt-5 space-y-3 border-t border-ink-900/10 pt-4">
        <div>
          <label className="label" htmlFor="pol-holder">
            Holder named in the order
          </label>
          <input
            id="pol-holder"
            className="input font-mono text-xs"
            placeholder="the holding key named in the order"
            value={holder}
            onChange={(e) => setHolder(e.target.value)}
          />
        </div>
        <div>
          <label className="label" htmlFor="pol-coin">
            Or one specific coin (optional)
          </label>
          <input
            id="pol-coin"
            className="input font-mono text-xs"
            placeholder="transaction id : output number"
            value={coin}
            onChange={(e) => setCoin(e.target.value)}
          />
        </div>
        <div>
          <label className="label" htmlFor="pol-reason">
            Reason (published)
          </label>
          <input
            id="pol-reason"
            className="input"
            placeholder="e.g. asset-freeze order, case number"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
        </div>
        <div>
          <label className="label" htmlFor="pol-order">
            Court or regulator order document
          </label>
          <input
            id="pol-order"
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
            {busy === 'freeze' ? 'Building…' : 'Block this holder or coin (you sign next)'}
          </button>
          <button
            onClick={() => start('unfreeze')}
            disabled={!!busy}
            className="btn-outline disabled:opacity-60"
          >
            {busy === 'unfreeze' ? 'Building…' : 'Restore (you sign next)'}
          </button>
        </div>

        {pending && !handoff && (
          <div className="rounded-lg border border-seq-300 bg-seq-50/60 px-4 py-3 text-sm">
            <div className="flex items-center gap-2 font-medium text-ink-900">
              <Icon.lock width={16} height={16} className="text-seq-600" />
              Your signature is required
            </div>
            <p className="mt-1.5 text-xs leading-relaxed text-ink-700/75">
              The change is built and nothing has been published. Sign it with your SeqPal
              ID key to authorize the updated list.
            </p>
            <div className="mt-3 flex flex-wrap items-center gap-2">
              <button
                onClick={() => complete()}
                disabled={!!busy}
                className="inline-flex items-center gap-2 rounded-xl bg-rose-600 px-4 py-2 text-sm font-semibold text-white hover:bg-rose-700 disabled:opacity-60"
              >
                {busy === 'sign' ? 'Signing…' : 'Sign and publish'}
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
                Unlock your SeqPal ID to sign. Cancelling leaves everything unchanged.
              </p>
            )}
          </div>
        )}

        {handoff && (
          <div className="rounded-lg border border-amber-300 bg-amber-50/60 px-4 py-3 text-sm">
            <div className="font-medium text-ink-900">One step is done by your registrar</div>
            <p className="mt-1.5 text-xs leading-relaxed text-ink-700/75">
              The change is signed. Your registrar compiles the updated rules from the
              document below and returns two values, which publish it. Nothing has been
              published and no coin has moved.
            </p>
            <textarea
              readOnly
              rows={4}
              className="input mt-2 font-mono text-[11px]"
              value={JSON.stringify(handoff.registrar_document, null, 2)}
            />
            <div className="mt-2 space-y-2">
              <input
                className="input font-mono text-xs"
                placeholder="rules program identity from your registrar"
                value={program}
                onChange={(e) => setProgram(e.target.value)}
              />
              <input
                className="input font-mono text-xs"
                placeholder="finished rules transaction from your registrar"
                value={rulesTx}
                onChange={(e) => setRulesTx(e.target.value)}
              />
            </div>
            <div className="mt-3 flex flex-wrap items-center gap-2">
              <button
                onClick={() =>
                  complete({ verifier_program: program.trim(), rules_tx: rulesTx.trim() })
                }
                disabled={!!busy || !program.trim() || !rulesTx.trim()}
                className="inline-flex items-center gap-2 rounded-xl bg-seq-600 px-4 py-2 text-sm font-semibold text-white hover:bg-seq-700 disabled:opacity-60"
              >
                {busy === 'sign' ? 'Publishing…' : 'Publish the updated list'}
              </button>
              <button
                onClick={() => setHandoff(null)}
                disabled={!!busy}
                className="btn-outline disabled:opacity-60"
              >
                Later
              </button>
            </div>
          </div>
        )}
      </div>

      <p className="mt-4 text-[11px] leading-relaxed text-ink-700/55">
        A change binds transfers once its transaction confirms. Until then holders trade
        under the previous list, and nothing is final at 0 confirmations: a Sequentia state
        is only as final as its Bitcoin anchor is deep.
      </p>
    </div>
  )
}
