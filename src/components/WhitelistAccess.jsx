import { useCallback, useEffect, useState } from 'react'
import { Icon } from './icons'
import { useStore } from '../lib/store'
import * as api from '../lib/api'

// Asking to be admitted to a network-enforced asset's whitelist, and deciding
// on the requests.
//
// An OpenDAMP whitelist is a list of holding keys the chain enforces on both
// sides of every regulated transfer. Nothing puts a holder on one automatically,
// which left a verified SeqPal ID with nowhere to present itself: the credential
// existed and the only way to use it was to find the issuer out of band.
//
// Approving is a decision. The key reaches the published list when a policy
// change carries it, which is the same issuer-signed path every other change to
// that list takes, so this never claims more than has happened.
export default function WhitelistAccess({ iss }) {
  const { account } = useStore()
  const [rows, setRows] = useState([])
  const [isOwner, setIsOwner] = useState(false)
  const [key, setKey] = useState('')
  const [note, setNote] = useState('')
  const [signThis, setSignThis] = useState(null)
  const [sig, setSig] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)

  const network = (iss?.enforcement || iss?.terms?.enforcement) === 'network'

  const load = useCallback(async () => {
    if (!iss?.id || !network) return
    try {
      const res = await api.whitelistRequests(iss.id)
      setRows(res.requests || [])
      setIsOwner(!!res.is_owner)
    } catch (e) {
      setErr(e.message)
    }
  }, [iss?.id, network])
  useEffect(() => {
    load()
  }, [load])

  if (!iss?.id || !network) return null

  const ask = async () => {
    setErr(null)
    setBusy(true)
    try {
      const body = { holding_key: key.trim().toLowerCase(), note: note.trim() }
      if (sig.trim()) body.sig = sig.trim()
      const res = await api.requestWhitelist(iss.id, body)
      if (res.sign_this) {
        // The key is not one this ID's wallets derive, so it has to be proven.
        setSignThis(res.sign_this)
        return
      }
      setKey('')
      setNote('')
      setSig('')
      setSignThis(null)
      await load()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  const decide = async (rid, approve) => {
    setErr(null)
    setBusy(true)
    try {
      await api.decideWhitelist(iss.id, rid, { approve })
      await load()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  const mine = rows.filter((r) => r.request.aid === account?.aid)

  return (
    <div className="card p-6">
      <div className="flex items-center gap-2">
        <Icon.shield width={18} height={18} className="text-seq-600" />
        <h3 className="font-bold text-ink-900">Who may hold this token</h3>
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/80">
        The network itself checks every holder of this token against a published list of keys. A
        verified SeqPal ID can ask to be on it; the issuer decides, and the list changes when the
        issuer publishes it.
      </p>

      {isOwner ? (
        <div className="mt-5">
          <div className="text-sm font-semibold text-ink-900">Requests</div>
          {rows.length === 0 ? (
            <p className="mt-2 text-sm text-ink-700/70">Nobody has asked yet.</p>
          ) : (
            <ul className="mt-2 space-y-2">
              {rows.map(({ request, holder, categories }) => (
                <li
                  key={request.id}
                  className="rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3"
                >
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div className="min-w-0">
                      <div className="text-sm font-semibold text-ink-900">
                        {holder?.display_name || request.aid.slice(0, 12)}
                        <span className="ml-2 text-xs font-normal text-ink-700/60">
                          {request.state}
                          {request.proof ? ` · proven by ${request.proof}` : ''}
                        </span>
                      </div>
                      <div className="truncate font-mono text-xs text-ink-700/70">
                        {request.holding_key}
                      </div>
                      {categories?.length > 0 && (
                        <div className="mt-1 text-xs text-ink-700/60">
                          Eligibility: {categories.join(', ')}
                        </div>
                      )}
                    </div>
                    {request.state === 'pending' && (
                      <div className="flex gap-2">
                        <button
                          onClick={() => decide(request.id, true)}
                          disabled={busy}
                          className="btn-primary px-3 py-1.5 text-xs disabled:opacity-50"
                        >
                          Approve
                        </button>
                        <button
                          onClick={() => decide(request.id, false)}
                          disabled={busy}
                          className="btn-outline px-3 py-1.5 text-xs disabled:opacity-50"
                        >
                          Refuse
                        </button>
                      </div>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          )}
          <p className="mt-3 text-xs leading-relaxed text-ink-700/60">
            Approving records your decision. The keys you approved reach the published list when
            you publish a policy change carrying them, and not before.
          </p>
        </div>
      ) : (
        <div className="mt-5 space-y-3">
          {mine.length > 0 && (
            <ul className="space-y-2">
              {mine.map(({ request }) => (
                <li
                  key={request.id}
                  className="rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-2.5 text-sm"
                >
                  <span className="font-mono text-xs text-ink-700/70">
                    {request.holding_key.slice(0, 16)}…
                  </span>
                  <span className="ml-2 font-semibold text-ink-900">{request.state}</span>
                  {request.state === 'approved' && (
                    <span className="ml-2 text-xs text-ink-700/60">
                      approved, not yet on the published list
                    </span>
                  )}
                </li>
              ))}
            </ul>
          )}
          <div>
            <label className="label" htmlFor="wl-key">
              The key that will hold the tokens
            </label>
            <input
              id="wl-key"
              className="input font-mono text-xs"
              spellCheck={false}
              placeholder="x-only public key, 64 hex"
              value={key}
              onChange={(e) => setKey(e.target.value)}
            />
            <p className="mt-1.5 text-xs leading-relaxed text-ink-700/60">
              This is what the network checks, so it has to be a key you hold. If it belongs to a
              wallet linked to this SeqPal ID, nothing further is needed. Once tokens reach it
              they sit at their own covenant address, which ordinary wallet software neither
              displays nor spends, and signing with this key through OpenDAMP tooling is the
              only way to move them again: name a key you will still be able to use.
            </p>
          </div>
          {signThis && (
            <div className="space-y-2">
              <div className="rounded-xl border border-ink-900/10 bg-ink-900/[0.02] p-4 text-xs">
                <div className="font-semibold text-ink-900">
                  Sign these exact characters with that key
                </div>
                <div className="mt-1 break-all font-mono text-ink-900">{signThis}</div>
              </div>
              <textarea
                className="input font-mono text-xs"
                rows={3}
                spellCheck={false}
                placeholder="signature"
                value={sig}
                onChange={(e) => setSig(e.target.value)}
              />
            </div>
          )}
          <input
            className="input"
            placeholder="Anything the issuer should know (optional)"
            value={note}
            onChange={(e) => setNote(e.target.value)}
          />
          {err && <p className="rounded-lg bg-rose-50 px-3 py-2 text-sm text-rose-700">{err}</p>}
          <button
            onClick={ask}
            disabled={busy || key.trim().length !== 64}
            className="btn-primary disabled:opacity-50"
          >
            {busy ? 'Sending' : signThis ? 'Send with the signature' : 'Ask to be admitted'}
          </button>
        </div>
      )}
    </div>
  )
}
