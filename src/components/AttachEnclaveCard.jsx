import { useEffect, useState } from 'react'
import { Icon } from './icons'
import { useStore } from '../lib/store'
import { resolveAccountKey } from '../lib/account'
import { isAid, isXonly } from '../lib/statements'
import * as wallet from '../lib/wallet'

// A SeqPal ID that is a wallet can do almost everything here: hold
// freely-tradable stocks, hold network-enforced assets, claim the distributions
// attached to them. The one thing out of reach is an OpenAMP restricted asset,
// because those live in a 2-of-2 enclave and this ID has no key in one.
//
// The account to attach should be the one belonging to the SAME wallet the ID
// was made from. That is the coherent thing: a SeqPal ID is one wallet, and
// stapling an unrelated wallet's account onto it would make the identity mean
// two different things at once. SeqPal cannot prove two keys share a seed --
// nothing public links them -- so it does not pretend to; what it can do is
// stop offering only the browser extension, which is not where most holders'
// account came from, and let them attach the account their own wallet shows.
export default function AttachEnclaveCard() {
  const { hasEnclave, connectExtension, attachEnclave, refresh } = useStore()
  const [entry, setEntry] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const [done, setDone] = useState(false)
  const [ext, setExt] = useState('checking')

  useEffect(() => {
    let cancelled = false
    wallet.waitForProvider().then((p) => {
      if (!cancelled) setExt(p ? 'present' : 'absent')
    })
    return () => {
      cancelled = true
    }
  }, [])

  if (hasEnclave) return null

  const run = async (get) => {
    setErr(null)
    setBusy(true)
    try {
      await attachEnclave(await get())
      setDone(true)
      await refresh()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  const value = entry.trim().toLowerCase()

  return (
    <div className="card mb-6 p-6">
      <div className="flex items-center gap-2">
        <Icon.lock width={18} height={18} className="text-btc-600" />
        <h3 className="font-bold text-ink-900">No OpenAMP account attached</h3>
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/80">
        This SeqPal ID is a wallet. Freely-tradable stocks, network-enforced assets and the
        distributions attached to them all work as they are. Restricted assets do not: those settle
        in a shared 2-of-2 account this ID has no key in.
      </p>

      {done ? (
        <p className="mt-4 rounded-lg bg-emerald-50 px-3 py-2 text-sm text-emerald-800">
          Attached. This ID can hold restricted assets now.
        </p>
      ) : (
        <>
          <p className="mt-4 text-sm leading-relaxed text-ink-700/80">
            Attach the OpenAMP account of the <strong>same wallet</strong> this ID was made from.
            Its account id is on that wallet&rsquo;s receive screen, beside the address you would
            give a sender.
          </p>
          <div className="mt-3">
            <label className="label" htmlFor="attach-aid">
              Account id, or account key
            </label>
            <input
              id="attach-aid"
              className="input font-mono text-xs"
              spellCheck={false}
              placeholder="account id (40 hex) or account key (64 hex)"
              value={entry}
              onChange={(e) => setEntry(e.target.value)}
            />
            <p className="mt-1.5 text-xs leading-relaxed text-ink-700/60">
              That wallet will be asked to sign a challenge, so you will need it open.
            </p>
          </div>
          {err && <p className="mt-3 rounded-lg bg-rose-50 px-3 py-2 text-sm text-rose-700">{err}</p>}
          <div className="mt-4 flex flex-wrap gap-3">
            <button
              onClick={() =>
                run(async () => {
                  const { xonly, aid } = await resolveAccountKey(value)
                  return { kind: 'linked', xonly, aid }
                })
              }
              disabled={busy || !(isAid(value) || isXonly(value))}
              className="btn-primary disabled:opacity-50"
            >
              {busy ? 'Waiting for that wallet' : 'Attach this account'}
            </button>
            {ext === 'present' && (
              <button onClick={() => run(connectExtension)} disabled={busy} className="btn-outline disabled:opacity-50">
                Use my browser wallet instead
              </button>
            )}
          </div>
        </>
      )}
    </div>
  )
}
