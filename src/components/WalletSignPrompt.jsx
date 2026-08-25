import { useEffect, useState } from 'react'
import { Icon } from './icons'
import { useStore, TAG_LABELS } from '../lib/store'

// The signing prompt for a LINKED wallet — one the holder operates themselves,
// rather than an extension SeqPal can ask directly.
//
// SeqPal shows exactly what has to be signed: the domain-separation tag, and
// either the statement's text or the 32-byte hash. The holder signs it in their
// own wallet and pastes the signature back. Nothing here ever sees a key, and
// nothing about the request is SeqPal-specific: it is a BIP340 signature over
// sha256(sha256(tag) || sha256(tag) || message), which is what seqpald verifies
// whichever wallet produced it.
export default function WalletSignPrompt() {
  const { pendingSig, resolvePendingSig, cancelPendingSig } = useStore()
  const [sig, setSig] = useState('')
  const [err, setErr] = useState(null)

  useEffect(() => {
    setSig('')
    setErr(null)
  }, [pendingSig])

  if (!pendingSig) return null

  const { tag, statement, hash, label, xonly } = pendingSig
  const submit = (e) => {
    e.preventDefault()
    const v = sig.trim().toLowerCase()
    if (!/^[0-9a-f]{128}$/.test(v)) {
      setErr('A BIP340 signature is 64 bytes, or 128 hex characters.')
      return
    }
    resolvePendingSig(v)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-ink-900/40 p-4">
      <div className="w-full max-w-lg rounded-2xl bg-white p-6 shadow-xl">
        <div className="flex items-center gap-2 text-sm font-bold text-ink-900">
          <Icon.lock width={16} height={16} className="text-btc-600" />
          Sign this in your Sequentia wallet
        </div>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/80">
          {TAG_LABELS[tag] || 'Statement'}. Your wallet signs the tagged message below with the
          enclave key of the account you linked, and you paste the signature back. SeqPal never
          sees your key.
        </p>

        <dl className="mt-4 space-y-2 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] p-4 text-xs">
          <div className="flex justify-between gap-4">
            <dt className="text-ink-700/70">Signing account</dt>
            <dd className="truncate font-mono text-ink-900">{xonly}</dd>
          </div>
          <div className="flex justify-between gap-4">
            <dt className="text-ink-700/70">Domain tag</dt>
            <dd className="font-mono text-ink-900">{tag}</dd>
          </div>
          {label && (
            <div className="flex justify-between gap-4">
              <dt className="text-ink-700/70">Document</dt>
              <dd className="text-ink-900">{label}</dd>
            </div>
          )}
        </dl>

        <div className="mt-3">
          <div className="label">{hash ? 'Message (32-byte hash)' : 'Statement'}</div>
          <pre className="mt-1 max-h-40 overflow-auto whitespace-pre-wrap break-all rounded-xl border border-ink-900/10 bg-ink-900/[0.02] p-3 font-mono text-xs text-ink-900">
            {hash || statement}
          </pre>
        </div>

        <form onSubmit={submit} className="mt-4 space-y-3">
          <div>
            <label className="label" htmlFor="linked-sig">
              Signature from your wallet
            </label>
            <textarea
              id="linked-sig"
              className="input font-mono text-xs"
              rows={3}
              spellCheck={false}
              placeholder="128 hex characters"
              value={sig}
              onChange={(e) => setSig(e.target.value)}
            />
          </div>
          {err && <p className="rounded-lg bg-rose-50 px-3 py-2 text-sm text-rose-700">{err}</p>}
          <div className="flex gap-3">
            <button type="submit" className="btn-primary flex-1">
              Submit signature
            </button>
            <button type="button" onClick={cancelPendingSig} className="btn-outline">
              Cancel
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
