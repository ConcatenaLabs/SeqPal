import { useEffect, useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import CopyId from './CopyId'
import * as api from '../lib/api'
import { verifyLog } from '../lib/loghash'
import { txUrl } from '../lib/chain'

// A short human label per log action; unknown actions fall back to the raw verb.
const ACTION_LABEL = {
  issue: 'Asset issued',
  transfer: 'Transfer',
  refusal: 'Transfer refused',
  categories: 'Categories updated',
  rules: 'Rules amended',
  anchor: 'Log head anchored on chain',
  clawback: 'Clawback',
}

// The transparency-log tab for one deployed issuance. It fetches this asset's
// slice of the OpenAMP transparency log (plus every log-head anchor entry),
// recomputes each entry's hash in the browser (never trusting seqpald's relay),
// and links every anchor txid to the explorer. The full contiguous chain, for an
// end-to-end walk, is linked at the public /openamp/v1/log.
export default function TransparencyLog({ iss }) {
  const [state, setState] = useState({ loading: true })

  useEffect(() => {
    let cancelled = false
    setState({ loading: true })
    api
      .issuanceLog(iss.id)
      .then(async (res) => {
        const verified = await verifyLog(res.entries || [])
        if (!cancelled) setState({ loading: false, entries: verified, logUrl: res.log_url })
      })
      .catch((e) => {
        if (!cancelled) setState({ loading: false, error: e.message })
      })
    return () => {
      cancelled = true
    }
  }, [iss.id])

  const { loading, entries, error, logUrl } = state
  const allOk = entries && entries.length > 0 && entries.every((e) => e.ok)

  return (
    <div className="card overflow-hidden">
      <div className="flex items-center gap-2 border-b border-ink-900/10 px-5 py-3.5">
        <Icon.doc width={18} height={18} className="text-ink-700" />
        <h2 className="font-bold text-ink-900">Transparency log</h2>
        {entries && entries.length > 0 && (
          <Badge color={allOk ? 'emerald' : 'rose'} className="ml-auto">
            {allOk ? (
              <>
                <Icon.check width={13} height={13} /> Hash chain verified in your browser
              </>
            ) : (
              'Hash mismatch on an entry'
            )}
          </Badge>
        )}
      </div>

      <div className="px-5 py-4">
        <p className="text-sm leading-relaxed text-ink-700/70">
          Every policy decision on this asset is a hash-chained entry:{' '}
          <span className="font-mono text-xs">sha256(seq | prev | time | action | data)</span>.
          Your browser recomputes each hash below, so you are not trusting SeqPal to relay the log
          faithfully. The full contiguous chain for every asset is public at{' '}
          {logUrl ? (
            <a
              href={logUrl}
              target="_blank"
              rel="noopener noreferrer"
              className="font-medium text-seq-600 hover:underline"
            >
              {logUrl}
            </a>
          ) : (
            <span className="font-mono text-xs">/openamp/v1/log</span>
          )}
          .
        </p>

        {loading ? (
          <div className="mt-4 flex justify-center py-8">
            <span className="h-6 w-6 animate-spin rounded-full border-4 border-btc/20 border-t-btc" />
          </div>
        ) : error ? (
          <div className="mt-4 rounded-lg border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700">
            {error}
          </div>
        ) : entries.length === 0 ? (
          <div className="mt-4 rounded-lg bg-ink-900/[0.03] px-4 py-3 text-sm text-ink-700/70">
            No log entries name this asset yet. Entries appear here as transfers, category updates,
            and log-head anchors are recorded.
          </div>
        ) : (
          <div className="mt-4 divide-y divide-ink-900/10 rounded-lg border border-ink-900/10">
            {entries.map((e) => {
              let anchorTxid = null
              if (e.action === 'anchor' && e.data && typeof e.data === 'object') {
                anchorTxid = e.data.txid || null
              }
              return (
                <div key={e.seq} className="flex flex-col gap-1 px-4 py-3 text-sm sm:flex-row sm:items-center sm:justify-between">
                  <div className="flex items-center gap-2">
                    <span className="font-mono text-xs text-ink-700/50">#{e.seq}</span>
                    <span className="font-medium text-ink-900">
                      {ACTION_LABEL[e.action] || e.action}
                    </span>
                    <Badge color={e.ok ? 'emerald' : 'rose'}>
                      {e.ok ? (
                        <>
                          <Icon.check width={12} height={12} /> hash ok
                        </>
                      ) : (
                        'hash mismatch'
                      )}
                    </Badge>
                  </div>
                  <div className="flex items-center gap-3">
                    {anchorTxid && (
                      <span className="inline-flex items-center gap-1 text-xs text-ink-700/70">
                        <Icon.link width={12} height={12} /> anchor
                        <CopyId value={anchorTxid} kind="tx" label="anchor txid" />
                      </span>
                    )}
                    <span className="font-mono text-[11px] text-ink-700/40" title={e.time}>
                      {e.time?.replace('T', ' ').replace('Z', '')}
                    </span>
                  </div>
                </div>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}
