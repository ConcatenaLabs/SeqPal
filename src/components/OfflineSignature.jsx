import { Icon } from './icons'

// Signing something when the browser cannot do it for you.
//
// A SeqPal ID backed by a browser extension signs in place: the page asks, the
// extension signs under SeqPal's tag, nothing is pasted. A SeqPal ID that is
// only a wallet has no such connection -- it proved itself by signing a message
// somewhere else and pasting the result -- so every surface that asks for a
// signature has to be able to ask for it the same way, or the surface is a dead
// end for that holder.
//
// What is signed here is the whole `sign_this_message` string, tag included.
// seqpald builds it and checks it against the addresses of the wallets this ID
// has linked, so the tag is part of what the signature commits to, exactly as it
// is on the tagged path.
export default function OfflineSignature({ prep, sig, onSig, onSubmit, busy, label = 'Submit the signature' }) {
  const message = prep?.sign_this_message
  if (!message) return null
  return (
    <div className="mt-4 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] p-4">
      <div className="flex items-center gap-2 text-sm font-semibold text-ink-900">
        <Icon.id width={16} height={16} className="text-seq-600" />
        Sign this with your wallet
      </div>
      <p className="mt-1 text-xs leading-relaxed text-ink-700/70">
        Open your wallet&rsquo;s sign-message tab, pick an address of a wallet you have linked to
        this SeqPal ID, and sign these exact characters. Paste the signature back here.
      </p>
      <pre className="mt-3 max-h-40 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-white/70 p-3 font-mono text-[11px] leading-relaxed text-ink-900">
        {message}
      </pre>
      <textarea
        className="input mt-3 font-mono text-xs"
        rows={3}
        spellCheck={false}
        placeholder="signature"
        value={sig}
        onChange={(e) => onSig(e.target.value)}
      />
      <button
        onClick={onSubmit}
        disabled={busy || !sig.trim()}
        className="btn-primary mt-3 disabled:opacity-50"
      >
        {busy ? 'Checking' : label}
      </button>
    </div>
  )
}
