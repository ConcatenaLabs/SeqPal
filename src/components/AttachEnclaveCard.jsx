import { useState } from 'react'
import { Icon } from './icons'
import { useStore } from '../lib/store'

// A SeqPal ID that is a wallet with no OpenAMP account can do almost everything
// here: hold freely-tradable stocks, hold network-enforced assets, claim the
// distributions attached to them. The one thing out of reach is an OpenAMP
// restricted asset, because those live in a 2-of-2 enclave and this ID has no
// key in one. Attaching an OpenAMP account lifts that, and changes nothing
// else: it is the same SeqPal ID, so every record already pointing at it still
// does.
export default function AttachEnclaveCard() {
  const { hasEnclave, connectExtension, attachEnclave, refresh } = useStore()
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const [done, setDone] = useState(null)

  if (hasEnclave) return null

  const attach = async () => {
    setErr(null)
    setBusy(true)
    try {
      const identity = await connectExtension()
      const res = await attachEnclave(identity)
      setDone(res?.enclave_aid || 'attached')
      await refresh()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="card mb-6 p-6">
      <div className="flex items-center gap-2">
        <Icon.lock width={18} height={18} className="text-btc-600" />
        <h3 className="font-bold text-ink-900">No OpenAMP account attached</h3>
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/80">
        This SeqPal ID is a wallet. Freely-tradable stocks, network-enforced assets and the
        distributions attached to them all work as they are. Restricted assets do not: those
        settle in a shared 2-of-2 account this ID has no key in.
      </p>
      {done ? (
        <p className="mt-4 rounded-lg bg-emerald-50 px-3 py-2 text-sm text-emerald-800">
          Attached. This ID can hold restricted assets now.
        </p>
      ) : (
        <>
          {err && <p className="mt-3 rounded-lg bg-rose-50 px-3 py-2 text-sm text-rose-700">{err}</p>}
          <button onClick={attach} disabled={busy} className="btn-outline mt-4 disabled:opacity-50">
            {busy ? 'Waiting for your wallet' : 'Attach an OpenAMP account from my browser wallet'}
          </button>
        </>
      )}
    </div>
  )
}
