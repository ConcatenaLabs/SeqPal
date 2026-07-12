import { Icon } from './icons'
import { Badge } from './ui'
import CopyId from './CopyId'
import * as api from '../lib/api'
import { usePoll } from '../lib/poll'

// The rules-amendment chain on the asset page (M7 section 3). The on-chain
// commitment is the genesis terms_hash plus an anchored chain of amendments. Every
// policy-rules mutation flows through the seqpald chokepoint, which posts the rules
// to the policy server and records an anchored amendment, so the live on-chain
// rules always equal the head of this chain. That invariant is the M7 acceptance
// proof and it is surfaced here from the server's own head-consistency check. Each
// amendment records the prior rules hash, the new rules hash, the basis, and the
// effective Sequentia block height, all read from the server, never asserted here.

function HashPair({ prior, next }) {
  return (
    <div className="flex flex-wrap items-center gap-1.5 font-mono text-[11px] text-ink-700/70">
      <span title={prior || 'genesis'}>{prior ? `${prior.slice(0, 8)}…` : 'genesis'}</span>
      <Icon.arrowRight width={12} height={12} className="text-ink-500" />
      <span title={next || ''}>{next ? `${next.slice(0, 8)}…` : '-'}</span>
    </div>
  )
}

export default function AmendmentChainCard({ iss }) {
  const { data } = usePoll(() => api.amendments(iss.id), { intervalMs: 15000, deps: [iss.id] })

  const genesis = data?.genesis_terms_hash
  const amends = data?.amendments || []
  const head = data?.head

  return (
    <div className="card p-6">
      <div className="flex items-center gap-2">
        <Icon.layers width={18} height={18} className="text-seq-600" />
        <h2 className="font-bold text-ink-900">Rules-amendment chain</h2>
        {head &&
          (head.head_consistent ? (
            <Badge color="emerald" className="ml-auto">
              Rules equal chain head
            </Badge>
          ) : (
            <Badge color="rose" className="ml-auto">
              Head inconsistent
            </Badge>
          ))}
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
        The on-chain commitment is the genesis terms_hash plus {amends.length} anchored amendment
        {amends.length === 1 ? '' : 's'}. Every policy-rules mutation is posted to the policy server
        and recorded as an anchored amendment through the same chokepoint, so the live rules always
        equal the head of this chain.
      </p>

      <div className="mt-4 space-y-2">
        <div className="rounded-lg border border-ink-900/10 bg-ink-900/[0.02] px-3 py-2.5">
          <div className="flex items-center justify-between gap-2">
            <span className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
              Genesis terms_hash
            </span>
            {genesis ? (
              <CopyId value={genesis} label="genesis terms hash" />
            ) : (
              <span className="text-xs text-ink-700/50">not deployed</span>
            )}
          </div>
        </div>

        {amends.map((a) => (
          <div key={a.amend_hash} className="rounded-lg border border-ink-900/10 px-3 py-2.5">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="text-sm font-semibold text-ink-900">
                Amendment {a.ord} · effective block{' '}
                {Number(a.effective_height).toLocaleString('en-US')}
              </span>
              {a.anchor_txid && <CopyId value={a.anchor_txid} kind="tx" label="anchor txid" />}
            </div>
            <div className="mt-1.5">
              <HashPair prior={a.prior_rules_hash} next={a.new_rules_hash} />
            </div>
            {a.basis && <p className="mt-1 text-xs text-ink-700/70">{a.basis}</p>}
          </div>
        ))}

        {amends.length === 0 && (
          <p className="rounded-lg bg-ink-900/[0.03] px-3 py-2 text-xs text-ink-700/70">
            No amendments filed. The current rules are the genesis commitment.
          </p>
        )}
      </div>

      {head && (
        <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-ink-700/70">
          <span>
            Head rules hash:{' '}
            <span className="font-mono">
              {head.head_rules_hash ? `${head.head_rules_hash.slice(0, 10)}…` : 'genesis'}
            </span>
          </span>
          <span>
            On-chain rules hash:{' '}
            <span className="font-mono">
              {head.onchain_rules_hash ? `${head.onchain_rules_hash.slice(0, 10)}…` : '-'}
            </span>
          </span>
        </div>
      )}

      <p className="mt-3 text-[11px] leading-relaxed text-ink-700/55">
        Each amendment is a content-addressed artifact anchored through the transparency log, so the
        current rules are the genesis terms plus every amendment, and each link is checkable. You
        can reproduce the same head check yourself against the policy server.
      </p>
    </div>
  )
}
