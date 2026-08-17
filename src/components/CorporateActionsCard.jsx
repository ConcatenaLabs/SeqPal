import { useCallback, useEffect, useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import * as api from '../lib/api'
import { fmtAssetAmount } from '../lib/format'

// Shareholder actions for a freely-tradable (bearer) asset: the owner declares
// a dividend or a vote, the register snapshot is taken from the chain at the
// first pass at or after the record height, and holders claim by proving their
// holdings with a signed statement from their own key (see the claim page at
// /actions/{id}). Everything here is real: the snapshot, the claims, and the
// tally come from the server's chain reads, never from this browser.

const STATUS_COLOR = {
  declared: 'amber',
  snapshotted: 'seq',
  open: 'seq',
  closed: 'slate',
  paid: 'emerald',
  settled: 'emerald',
}

function ActionRow({ a, iss }) {
  const [detail, setDetail] = useState(null)
  const [open, setOpen] = useState(false)
  const [err, setErr] = useState(null)

  const toggle = async () => {
    const next = !open
    setOpen(next)
    if (next && !detail) {
      try {
        setDetail(await api.getAction(a.id))
      } catch (e) {
        setErr(e.message)
      }
    }
  }

  const snap = detail?.snapshot || a.snapshot
  const tally = detail?.tally || a.tally
  const claimUrl = `${window.location.origin}/actions/${a.id}`

  return (
    <div className="rounded-lg border border-ink-900/10">
      <button onClick={toggle} className="flex w-full items-center gap-3 px-4 py-3 text-left">
        <Icon.coins width={16} height={16} className="shrink-0 text-btc-600" />
        <span className="min-w-0 flex-1">
          <span className="block text-sm font-semibold text-ink-900">
            {a.kind === 'vote' ? 'Vote' : 'Dividend'}
            {a.memo ? `: ${a.memo}` : ''}
          </span>
          <span className="block text-xs text-ink-700/60">
            record height {Number(a.record_height || 0).toLocaleString()}
            {a.kind === 'dividend' && a.pool_atoms
              ? ` · pool ${Number(a.pool_atoms).toLocaleString()} atoms`
              : ''}
          </span>
        </span>
        <Badge color={STATUS_COLOR[a.status] || 'slate'}>{a.status || 'declared'}</Badge>
        <Icon.arrowRight
          width={14}
          height={14}
          className={`shrink-0 text-ink-500 transition-transform ${open ? 'rotate-90' : ''}`}
        />
      </button>
      {open && (
        <div className="space-y-2 border-t border-ink-900/10 px-4 py-3 text-sm">
          {err && <p className="text-rose-600">{err}</p>}
          {snap ? (
            <p className="text-xs leading-relaxed text-ink-700/75">
              Snapshot taken at Sequentia block {Number(snap.height || 0).toLocaleString()} (the
              first pass at or after the record height):{' '}
              {Number(snap.holders ?? snap.holder_count ?? 0).toLocaleString()} holding
              {(snap.holders ?? snap.holder_count) === 1 ? '' : 's'},{' '}
              {fmtAssetAmount(Number(snap.total_atoms || 0), iss.precision || 8, iss.ticker)} in
              scope.
            </p>
          ) : (
            <p className="text-xs text-ink-700/60">
              No snapshot yet. It is taken from the chain at the first pass at or after the
              record height and never restated.
            </p>
          )}
          {a.kind === 'vote' && tally && (
            <div className="rounded-lg bg-ink-900/[0.03] px-3 py-2">
              <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
                Tally (by atoms held at the snapshot)
              </div>
              <dl className="mt-1 space-y-0.5">
                {Object.entries(tally).map(([choice, atoms]) => (
                  <div key={choice} className="flex justify-between text-xs">
                    <dt className="text-ink-800">{choice}</dt>
                    <dd className="font-mono text-ink-900">{Number(atoms).toLocaleString()}</dd>
                  </div>
                ))}
              </dl>
            </div>
          )}
          {typeof (detail?.claims_count ?? a.claims_count) === 'number' && (
            <p className="text-xs text-ink-700/60">
              {Number(detail?.claims_count ?? a.claims_count).toLocaleString()} claim(s) recorded.
            </p>
          )}
          <div className="flex flex-wrap items-center gap-2 pt-1">
            <a
              href={claimUrl}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1.5 text-xs font-medium text-seq-600 hover:underline"
            >
              <Icon.external width={12} height={12} /> Open the holder claim page
            </a>
            <button
              onClick={() => navigator.clipboard?.writeText(claimUrl)}
              className="inline-flex items-center gap-1.5 text-xs font-medium text-ink-700 hover:text-ink-900"
            >
              <Icon.copy width={12} height={12} /> Copy claim link for holders
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

export default function CorporateActionsCard({ iss }) {
  const [actions, setActions] = useState(null)
  const [form, setForm] = useState({ kind: 'dividend', memo: '', pool: '', choices: 'For, Against, Abstain', recordHeight: '' })
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)

  const load = useCallback(() => {
    api
      .listActions(iss.id)
      .then((r) => setActions(r.actions || []))
      .catch((e) => {
        setActions([])
        setErr(e.message)
      })
  }, [iss.id])
  useEffect(load, [load])

  const declare = async (e) => {
    e.preventDefault()
    setErr(null)
    setBusy(true)
    try {
      const body = { kind: form.kind, memo: form.memo.trim() }
      if (form.recordHeight) body.record_height = Number(form.recordHeight)
      if (form.kind === 'dividend') {
        const pool = Number(String(form.pool).replace(/[^0-9]/g, ''))
        if (!pool) throw new Error('Enter the dividend pool, in atoms.')
        body.pool_atoms = pool
      } else {
        const choices = form.choices
          .split(',')
          .map((c) => c.trim())
          .filter(Boolean)
        if (choices.length < 2) throw new Error('A vote needs at least two choices.')
        body.choices = choices
      }
      await api.createAction(iss.id, body)
      setForm({ ...form, memo: '', pool: '', recordHeight: '' })
      load()
    } catch (e2) {
      setErr(e2.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="card p-6">
      <div className="flex items-center gap-2">
        <Icon.coins width={18} height={18} className="text-btc-600" />
        <h2 className="font-bold text-ink-900">Shareholder actions</h2>
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
        Declare a dividend or put a question to a vote. Because anyone can hold this token,
        who holds what is read from the chain: the snapshot is taken at the first pass at or
        after the record height, and holders verify their identity and prove their holdings
        with their own key to collect a dividend or cast a vote.
      </p>

      <form onSubmit={declare} className="mt-4 space-y-3 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] p-4">
        <div className="flex gap-2">
          {[
            ['dividend', 'Dividend'],
            ['vote', 'Vote'],
          ].map(([v, label]) => (
            <button
              type="button"
              key={v}
              onClick={() => setForm({ ...form, kind: v })}
              className={`flex-1 rounded-lg border px-3 py-2 text-sm font-semibold transition-colors ${
                form.kind === v
                  ? 'border-btc bg-btc-50 text-btc-700'
                  : 'border-ink-900/15 text-ink-700 hover:bg-ink-900/[0.02]'
              }`}
            >
              {label}
            </button>
          ))}
        </div>
        <div>
          <label className="label" htmlFor="ca-memo">
            {form.kind === 'vote' ? 'Question' : 'Description'}
          </label>
          <input
            id="ca-memo"
            className="input"
            placeholder={form.kind === 'vote' ? 'e.g. Approve the annual accounts' : 'e.g. FY dividend'}
            value={form.memo}
            onChange={(e) => setForm({ ...form, memo: e.target.value })}
          />
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          {form.kind === 'dividend' ? (
            <div>
              <label className="label" htmlFor="ca-pool">
                Pool (atoms)
              </label>
              <input
                id="ca-pool"
                className="input"
                inputMode="numeric"
                placeholder="e.g. 100000000"
                value={form.pool}
                onChange={(e) => setForm({ ...form, pool: e.target.value })}
              />
            </div>
          ) : (
            <div>
              <label className="label" htmlFor="ca-choices">
                Choices (comma separated)
              </label>
              <input
                id="ca-choices"
                className="input"
                value={form.choices}
                onChange={(e) => setForm({ ...form, choices: e.target.value })}
              />
            </div>
          )}
          <div>
            <label className="label" htmlFor="ca-height">
              Record height (blank = next block)
            </label>
            <input
              id="ca-height"
              className="input"
              inputMode="numeric"
              placeholder="Sequentia block height"
              value={form.recordHeight}
              onChange={(e) => setForm({ ...form, recordHeight: e.target.value })}
            />
          </div>
        </div>
        {err && <p className="text-sm font-medium text-rose-600">{err}</p>}
        <button disabled={busy} className="btn-primary w-full disabled:opacity-60">
          {busy ? 'Declaring…' : form.kind === 'vote' ? 'Declare the vote' : 'Declare the dividend'}
        </button>
      </form>

      <div className="mt-4">
        <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
          Declared actions
        </div>
        {actions === null ? (
          <div className="mt-2 flex justify-center py-4">
            <span className="h-5 w-5 animate-spin rounded-full border-2 border-btc/20 border-t-btc" />
          </div>
        ) : actions.length === 0 ? (
          <p className="mt-2 text-sm text-ink-700/60">Nothing declared yet.</p>
        ) : (
          <div className="mt-2 space-y-2">
            {actions.map((a) => (
              <ActionRow key={a.id} a={a} iss={iss} />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
