import { useEffect, useState } from 'react'
import { Icon } from '../../components/icons'
import { Badge } from '../../components/ui'
import SignInGate from '../../components/SignInGate'
import { useStore } from '../../lib/store'
import { reviewQueue, reviewDecide } from '../../lib/api'

const LIST_LABELS = {
  ofac_sdn: 'OFAC SDN',
  ofac_consolidated: 'OFAC consolidated',
  eu_consolidated: 'EU consolidated',
  un_sc: 'UN Security Council',
}

// The platform reviewer surface. Session-gated to the admin allowlist on
// seqpald (SEQPALD_ADMIN_AIDS); a non-admin session gets a clean 403. This is
// the human half of the sanctions loop: a confirmed hit freezes and refuses, a
// cleared false positive lets verification proceed. The labeled-simulated
// auto-reviewer resolves deterministic TEST personas, but a human may act first.
function ReviewCard({ item, onDecided }) {
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(null) // 'clear' | 'confirm'
  const [err, setErr] = useState(null)

  const decide = async (decision) => {
    setErr(null)
    setBusy(decision)
    try {
      await reviewDecide(item.id, { decision, note: note.trim() })
      onDecided?.()
    } catch (e) {
      setErr(e.message)
      setBusy(null)
    }
  }

  return (
    <div className="card p-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="font-semibold text-ink-900">{LIST_LABELS[item.list] || item.list}</span>
            {item.disposition ? (
              <Badge color="amber">TEST persona: {item.disposition}</Badge>
            ) : (
              <Badge color="rose">Genuine hit</Badge>
            )}
          </div>
          <div className="mt-1 break-all font-mono text-xs text-ink-700/60">AID {item.aid}</div>
          <div className="mt-2 text-sm text-ink-800">
            Matched entry: <span className="font-medium">{item.matched_entry}</span>
          </div>
        </div>
      </div>

      <input
        className="input mt-4"
        placeholder="Reviewer note (optional)"
        value={note}
        onChange={(e) => setNote(e.target.value)}
      />
      {err && <p className="mt-3 rounded-lg bg-rose-50 px-3 py-2 text-sm text-rose-700">{err}</p>}

      <div className="mt-4 flex flex-wrap gap-2">
        <button onClick={() => decide('clear')} disabled={!!busy} className="btn-outline disabled:opacity-50">
          {busy === 'clear' ? 'Clearing…' : 'Clear (false positive)'}
        </button>
        <button
          onClick={() => decide('confirm')}
          disabled={!!busy}
          className="btn bg-rose-600 text-white hover:bg-rose-700 disabled:opacity-50"
        >
          {busy === 'confirm' ? 'Confirming…' : 'Confirm hit (freeze)'}
        </button>
      </div>
    </div>
  )
}

export default function IdReview() {
  const { loading, isSignedIn } = useStore()
  const [queue, setQueue] = useState(null)
  const [forbidden, setForbidden] = useState(false)
  const [err, setErr] = useState(null)

  const load = () => {
    reviewQueue()
      .then((data) => setQueue(data.queue || []))
      .catch((e) => {
        if (e.status === 403) setForbidden(true)
        else setErr(e.message)
      })
  }

  useEffect(() => {
    if (isSignedIn) load()
  }, [isSignedIn])

  if (loading) {
    return (
      <section className="container-x flex justify-center py-24">
        <span className="h-8 w-8 animate-spin rounded-full border-4 border-btc/20 border-t-btc" />
      </section>
    )
  }
  if (!isSignedIn) {
    return (
      <SignInGate
        title="Sign in to the reviewer surface"
        body="The manual-review queue is restricted to platform reviewers. Sign in with an admin SeqPal ID."
        backTo="/id"
        backLabel="SeqPal ID home"
      />
    )
  }

  return (
    <section className="container-x py-12">
      <div className="flex items-center gap-2">
        <Icon.shield width={22} height={22} className="text-seq-600" />
        <h1 className="text-3xl font-extrabold tracking-tight text-ink-900">Sanctions review</h1>
      </div>
      <p className="mt-1 max-w-2xl text-ink-700/80">
        Name matches parked for a human decision. Confirming a hit freezes the account and
        refuses the identity; clearing a false positive lets verification proceed.
      </p>

      <div className="mt-8">
        {forbidden ? (
          <div className="card px-6 py-16 text-center">
            <div className="mx-auto flex h-14 w-14 items-center justify-center rounded-2xl bg-ink-900/[0.04] text-ink-600">
              <Icon.lock width={28} height={28} />
            </div>
            <h3 className="mt-5 text-lg font-bold text-ink-900">Reviewers only</h3>
            <p className="mt-2 text-sm text-ink-700/70">
              This surface is restricted to the platform reviewer allowlist.
            </p>
          </div>
        ) : err ? (
          <div className="rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700">{err}</div>
        ) : queue === null ? (
          <div className="card flex justify-center py-16">
            <span className="h-6 w-6 animate-spin rounded-full border-4 border-btc/20 border-t-btc" />
          </div>
        ) : queue.length === 0 ? (
          <div className="card flex flex-col items-center justify-center px-6 py-16 text-center">
            <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-emerald-50 text-emerald-600">
              <Icon.check width={28} height={28} />
            </div>
            <h3 className="mt-5 text-lg font-bold text-ink-900">Queue is clear</h3>
            <p className="mt-2 text-sm text-ink-700/70">No pending sanctions reviews.</p>
          </div>
        ) : (
          <div className="space-y-4">
            {queue.map((item) => (
              <ReviewCard key={item.id} item={item} onDecided={load} />
            ))}
          </div>
        )}
      </div>
    </section>
  )
}
