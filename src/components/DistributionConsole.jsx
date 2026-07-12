import { useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import CopyId from './CopyId'
import DepositBox from './money/DepositBox'
import * as api from '../lib/api'
import { docUrl } from '../lib/api'
import { usePoll } from '../lib/poll'
import { fmtRail } from '../lib/money'
import { txUrl } from '../lib/chain'

// The M7 distribution console (issuer-facing, owner-scoped). A run is real USDX:
// the issuer funds a servicing deposit first, takes an immutable record-date
// snapshot of the on-chain register, reviews the pro-rata and withholding table
// (gross, withheld, net per holder, the reconciliation total, and the disclosed
// dust), executes the net USDX payouts one per holder, then sees the per-holder
// txids and the anchored statements. Nothing is asserted in the browser: every
// figure is a seqpald value read from the chain and the withholding math.
//
// State machine: awaiting_funding -> funded -> snapshotted -> paying -> complete.
// The run pays nothing until the deposit confirms and covers the pool, and
// nothing is final at 0 confirmations because a Sequentia state is only as final
// as its Bitcoin anchor is deep.

const RUN_STATE = {
  awaiting_funding: { label: 'Awaiting funding', color: 'amber' },
  funded: { label: 'Funded', color: 'seq' },
  snapshotted: { label: 'Snapshot taken', color: 'seq' },
  paying: { label: 'Paying', color: 'amber' },
  complete: { label: 'Complete', color: 'emerald' },
}

const PAY_STATE = {
  pending: { label: 'Pending', color: 'slate' },
  paying: { label: 'Paying', color: 'amber' },
  paid: { label: 'Paid', color: 'emerald' },
  skipped: { label: 'Skipped', color: 'amber' },
  no_payment: { label: 'No payment', color: 'slate' },
  failed: { label: 'Failed', color: 'rose' },
}

function StateBadge({ map, state }) {
  const m = map[state] || { label: state, color: 'slate' }
  return <Badge color={m.color}>{m.label}</Badge>
}

const usdx = (atoms) => fmtRail(atoms, 'USDX')

// The per-holder pro-rata + withholding table, rendered from the immutable
// snapshot. Every column is a server value; the reconciliation total and the
// disclosed dust are shown so sum(gross) == sum(net) + sum(withheld) is visible.
function PaymentsTable({ d, payments }) {
  const rows = payments || []
  const grossT = rows.reduce((n, p) => n + (Number(p.gross_atoms) || 0), 0)
  const withheldT = rows.reduce((n, p) => n + (Number(p.withheld_atoms) || 0), 0)
  const netT = rows.reduce((n, p) => n + (Number(p.net_atoms) || 0), 0)
  const reconciles = grossT === netT + withheldT

  return (
    <div>
      <div className="overflow-x-auto rounded-lg border border-ink-900/10">
        <table className="w-full min-w-[640px] text-sm">
          <thead>
            <tr className="border-b border-ink-900/10 bg-ink-900/[0.02] text-left text-xs uppercase tracking-wide text-ink-700/60">
              <th className="px-3 py-2 font-semibold">Holder</th>
              <th className="px-3 py-2 font-semibold">Tax status</th>
              <th className="px-3 py-2 text-right font-semibold">Rate</th>
              <th className="px-3 py-2 text-right font-semibold">Gross</th>
              <th className="px-3 py-2 text-right font-semibold">Withheld</th>
              <th className="px-3 py-2 text-right font-semibold">Net</th>
              <th className="px-3 py-2 font-semibold">Payment</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-ink-900/5">
            {rows.map((p) => (
              <tr key={p.holder_aid}>
                <td className="px-3 py-2">
                  <CopyId value={p.holder_aid} label="holder AID" />
                </td>
                <td className="px-3 py-2 text-ink-700/80">{taxLabel(p.tax_status)}</td>
                <td className="px-3 py-2 text-right font-mono text-xs text-ink-700/80">
                  {(Number(p.treaty_bps) / 100).toFixed(2)}%
                </td>
                <td className="px-3 py-2 text-right font-mono text-xs text-ink-900">
                  {usdx(p.gross_atoms)}
                </td>
                <td className="px-3 py-2 text-right font-mono text-xs text-ink-700/80">
                  {usdx(p.withheld_atoms)}
                </td>
                <td className="px-3 py-2 text-right font-mono text-xs font-semibold text-ink-900">
                  {usdx(p.net_atoms)}
                </td>
                <td className="px-3 py-2">
                  <div className="flex flex-col items-start gap-1">
                    <StateBadge map={PAY_STATE} state={p.state} />
                    {p.txid && <CopyId value={p.txid} kind="tx" label="payout txid" />}
                    {p.statement_hash && (
                      <a
                        href={docUrl(p.statement_hash)}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="inline-flex items-center gap-1 text-xs font-medium text-seq-600 hover:underline"
                      >
                        <Icon.doc width={12} height={12} /> Statement
                      </a>
                    )}
                    {p.reason && !p.txid && (
                      <span className="text-[11px] text-ink-700/60">{p.reason}</span>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
          <tfoot>
            <tr className="border-t border-ink-900/10 bg-ink-900/[0.02] font-mono text-xs">
              <td className="px-3 py-2 font-sans font-semibold text-ink-900" colSpan={3}>
                Reconciliation
              </td>
              <td className="px-3 py-2 text-right font-semibold text-ink-900">{usdx(grossT)}</td>
              <td className="px-3 py-2 text-right text-ink-700/80">{usdx(withheldT)}</td>
              <td className="px-3 py-2 text-right font-semibold text-ink-900">{usdx(netT)}</td>
              <td className="px-3 py-2" />
            </tr>
          </tfoot>
        </table>
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-ink-700/70">
        <span className={reconciles ? 'text-emerald-600' : 'text-rose-600'}>
          {reconciles ? (
            <>
              <Icon.check width={12} height={12} className="mr-1 inline" /> Gross equals net plus
              withheld
            </>
          ) : (
            'Reconciliation mismatch'
          )}
        </span>
        <span>
          Dust remainder: <span className="font-mono">{usdx(d.dust_atoms)}</span>, disclosed and
          kept in the servicing wallet, never dropped
        </span>
      </div>
    </div>
  )
}

function taxLabel(s) {
  if (s === 'w9') return 'US person (W-9)'
  if (s === 'w8ben') return 'non-US (W-8BEN)'
  return 'undetermined'
}

// A single run, polled so funding and payment state advance live from the server.
function RunPanel({ iss, runID, confsHint, onClose }) {
  const { data, refresh } = usePoll(() => api.getDistribution(iss.id, runID), {
    intervalMs: 5000,
    deps: [iss.id, runID],
  })
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)

  const d = data?.distribution
  const payments = data?.payments || []

  const act = async (fn) => {
    setErr(null)
    setBusy(true)
    try {
      await fn()
      await refresh()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  if (!d) {
    return (
      <div className="rounded-xl border border-ink-900/10 bg-white p-6">
        <div className="flex justify-center py-6">
          <span className="h-6 w-6 animate-spin rounded-full border-4 border-btc/20 border-t-btc" />
        </div>
      </div>
    )
  }

  return (
    <div className="rounded-xl border border-ink-900/10 bg-white p-5">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <StateBadge map={RUN_STATE} state={d.state} />
          <span className="font-mono text-xs text-ink-700/60">run {d.id.slice(0, 8)}</span>
          {d.memo && <span className="text-sm text-ink-700/70">· {d.memo}</span>}
        </div>
        <button onClick={onClose} className="text-sm text-ink-700/70 hover:text-ink-900">
          Back to runs
        </button>
      </div>

      <div className="mt-3 grid gap-3 sm:grid-cols-3">
        <div className="rounded-lg bg-ink-900/[0.03] px-3 py-2">
          <div className="text-xs text-ink-700/60">Gross pool</div>
          <div className="font-mono text-sm font-semibold text-ink-900">{usdx(d.pool_atoms)}</div>
        </div>
        {d.snapshot_height > 0 && (
          <div className="rounded-lg bg-ink-900/[0.03] px-3 py-2">
            <div className="text-xs text-ink-700/60">Record-date height</div>
            <div className="font-mono text-sm font-semibold text-ink-900">
              Sequentia block {Number(d.snapshot_height).toLocaleString('en-US')}
            </div>
          </div>
        )}
        {d.total_held > 0 && (
          <div className="rounded-lg bg-ink-900/[0.03] px-3 py-2">
            <div className="text-xs text-ink-700/60">Total held of record</div>
            <div className="font-mono text-sm font-semibold text-ink-900">
              {Number(d.total_held).toLocaleString('en-US')} atoms
            </div>
          </div>
        )}
      </div>

      {/* Stage 1: funding invoice */}
      {d.state === 'awaiting_funding' && (
        <div className="mt-4">
          <p className="mb-2 text-sm text-ink-700/80">
            Fund this run first. Send USDX covering the gross pool to the servicing deposit
            address. The run pays nothing and cannot be snapshotted until the deposit confirms and
            covers the pool.
          </p>
          <DepositBox
            rail="usdx"
            address={d.deposit_address}
            payAmount={d.pool_atoms}
            payCcy="USDX"
            confsRequired={confsHint || 1}
          />
        </div>
      )}

      {/* Stage 2: funded, ready to snapshot */}
      {d.state === 'funded' && (
        <div className="mt-4 rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-3">
          <div className="flex items-center gap-2 text-sm font-medium text-emerald-700">
            <Icon.check width={16} height={16} /> Funded with {usdx(d.funded_atoms)}
            {d.funded_txid && (
              <a
                href={txUrl(d.funded_txid)}
                target="_blank"
                rel="noopener noreferrer"
                className="ml-1 font-mono text-xs hover:underline"
              >
                funding txid
              </a>
            )}
          </div>
          <p className="mt-1 text-xs text-emerald-700/80">
            Take the record-date snapshot to capture the holder set from the chain at the current
            Sequentia block height. The snapshot is immutable once taken.
          </p>
          <button
            onClick={() => act(() => api.snapshotDistribution(iss.id, runID))}
            disabled={busy}
            className="btn-primary mt-3 disabled:opacity-60"
          >
            <Icon.users width={16} height={16} />
            {busy ? 'Capturing…' : 'Take record-date snapshot'}
          </button>
        </div>
      )}

      {/* Stage 3+: the pro-rata + withholding table */}
      {(d.state === 'snapshotted' || d.state === 'paying' || d.state === 'complete') && (
        <div className="mt-4">
          <PaymentsTable d={d} payments={payments} />
          {(d.state === 'snapshotted' || d.state === 'paying') && (
            <button
              onClick={() => act(() => api.executeDistribution(iss.id, runID))}
              disabled={busy}
              className="btn-primary mt-3 disabled:opacity-60"
            >
              <Icon.coins width={16} height={16} />
              {busy
                ? 'Paying…'
                : d.state === 'paying'
                  ? 'Resume payouts'
                  : 'Execute net USDX payouts'}
            </button>
          )}
          {d.state === 'complete' && (
            <div className="mt-3 space-y-2">
              <div className="flex items-center gap-2 text-sm font-medium text-emerald-700">
                <Icon.check width={16} height={16} /> Run complete. Net paid on chain in USDX.
              </div>
              <div className="flex flex-wrap gap-3 text-sm">
                {d.withholding_hash && (
                  <a
                    href={docUrl(d.withholding_hash)}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-1.5 font-medium text-seq-600 hover:underline"
                  >
                    <Icon.doc width={15} height={15} /> Withholding summary
                  </a>
                )}
                {d.crs_hash && (
                  <a
                    href={docUrl(d.crs_hash)}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-1.5 font-medium text-seq-600 hover:underline"
                  >
                    <Icon.doc width={15} height={15} /> Annual CRS/FATCA (simulated)
                  </a>
                )}
                {d.anchor_txid && <CopyId value={d.anchor_txid} kind="tx" label="anchor txid" />}
              </div>
            </div>
          )}
        </div>
      )}

      {err && <p className="mt-3 text-sm font-medium text-rose-600">{err}</p>}

      <p className="mt-4 text-[11px] leading-relaxed text-ink-700/55">
        Real USDX pro-rata distribution: the withholding math and the net payments are real on the
        Sequentia testnet. The 1042-S-style statements, the withholding summary, and the annual
        CRS/FATCA artifact are content-addressed and anchored, so their hashes are real. Any
        remittance of the withheld amount to a tax authority is a simulated, labeled step, out of
        scope here.
      </p>
    </div>
  )
}

function CreateForm({ iss, onCreated, onCancel }) {
  const [usd, setUsd] = useState('')
  const [memo, setMemo] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)

  const create = async () => {
    setErr(null)
    setBusy(true)
    try {
      const amount = Number(String(usd).replace(/[^0-9.]/g, '')) || 0
      if (amount <= 0) {
        setErr('Enter a positive USDX pool amount.')
        return
      }
      const res = await api.createDistribution(iss.id, { pool_usd: amount, memo: memo.trim() })
      onCreated(res)
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="rounded-xl border border-ink-900/10 bg-white p-5">
      <h3 className="font-semibold text-ink-900">New distribution run</h3>
      <p className="mt-1 text-sm text-ink-700/70">
        The pool is paid pro-rata across the holder set as of the record-date snapshot, net of
        per-holder withholding. USDX is 1:1 with USD on this testnet.
      </p>
      <div className="mt-4 space-y-3">
        <div>
          <label className="label" htmlFor="dist-usd">
            Gross pool (USDX)
          </label>
          <div className="relative">
            <span className="pointer-events-none absolute left-3.5 top-1/2 -translate-y-1/2 text-sm text-ink-700/60">
              $
            </span>
            <input
              id="dist-usd"
              className="input pl-7"
              inputMode="decimal"
              placeholder="100,000"
              value={usd}
              onChange={(e) => setUsd(e.target.value)}
            />
          </div>
        </div>
        <div>
          <label className="label" htmlFor="dist-memo">
            Memo (optional)
          </label>
          <input
            id="dist-memo"
            className="input"
            placeholder="Q3 dividend"
            value={memo}
            onChange={(e) => setMemo(e.target.value)}
          />
        </div>
        {err && <p className="text-sm font-medium text-rose-600">{err}</p>}
        <div className="flex gap-2">
          <button onClick={create} disabled={busy} className="btn-primary disabled:opacity-60">
            {busy ? 'Creating…' : 'Create run and show funding invoice'}
          </button>
          <button onClick={onCancel} className="btn-outline">
            Cancel
          </button>
        </div>
      </div>
    </div>
  )
}

export default function DistributionConsole({ iss }) {
  const { data, refresh } = usePoll(() => api.listDistributions(iss.id), {
    intervalMs: 12000,
    deps: [iss.id],
  })
  const [mode, setMode] = useState('list') // list | create | run
  const [runID, setRunID] = useState(null)
  const [confsHint, setConfsHint] = useState(1)

  const runs = data?.distributions || []

  const openRun = (id) => {
    setRunID(id)
    setMode('run')
  }

  return (
    <div className="card p-6">
      <div className="flex items-center gap-2">
        <Icon.coins width={18} height={18} className="text-seq-600" />
        <h2 className="font-bold text-ink-900">Distribution console</h2>
        <Badge color="emerald" className="ml-auto">
          Real USDX
        </Badge>
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
        Run a real USDX distribution to holders of record. Fund the servicing deposit, snapshot the
        on-chain register at a Sequentia block height, review the pro-rata and withholding table,
        then pay each holder their net on chain to the payout address they registered. A holder
        with no registered mandate is skipped with a portal notice, never paid to a guessed
        address.
      </p>

      {mode === 'create' && (
        <div className="mt-4">
          <CreateForm
            iss={iss}
            onCancel={() => setMode('list')}
            onCreated={(res) => {
              setConfsHint(res.confs_required || 1)
              refresh()
              openRun(res.distribution.id)
            }}
          />
        </div>
      )}

      {mode === 'run' && runID && (
        <div className="mt-4">
          <RunPanel
            iss={iss}
            runID={runID}
            confsHint={confsHint}
            onClose={() => {
              setMode('list')
              refresh()
            }}
          />
        </div>
      )}

      {mode === 'list' && (
        <div className="mt-4">
          <button onClick={() => setMode('create')} className="btn-primary">
            <Icon.spark width={16} height={16} /> New distribution
          </button>
          {runs.length > 0 && (
            <div className="mt-4 divide-y divide-ink-900/5 rounded-lg border border-ink-900/10">
              {runs.map((d) => (
                <button
                  key={d.id}
                  onClick={() => openRun(d.id)}
                  className="flex w-full items-center justify-between gap-3 px-3 py-2.5 text-left text-sm hover:bg-ink-900/[0.02]"
                >
                  <div className="flex items-center gap-2">
                    <StateBadge map={RUN_STATE} state={d.state} />
                    <span className="font-mono text-xs text-ink-700/60">{d.id.slice(0, 8)}</span>
                    {d.memo && <span className="text-ink-700/80">{d.memo}</span>}
                  </div>
                  <span className="font-mono text-xs font-semibold text-ink-900">
                    {usdx(d.pool_atoms)}
                  </span>
                </button>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
