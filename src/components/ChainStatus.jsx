import { Badge } from './ui'
import { Icon } from './icons'
import { chipFor, isRegressed } from '../lib/chain'

// A chain-verified status chip fed by the SSE watch stream. It is deliberately
// distinct from the amber "Simulated" badges the M2 lifecycle uses: a chain
// chip carries a coloured status dot and a chain glyph, so a viewer can always
// tell a real confirmation from a simulated stage. Nothing here reads "final":
// a 0-confirmation state says exactly that, per Bitcoin-anchoring supremacy.
export function ChainChip({ watch, className = '' }) {
  const c = chipFor(watch)
  return (
    <Badge color={c.color} className={className}>
      <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
      {c.label}
    </Badge>
  )
}

// A short caption spelling out the confirmation and anchor depth in words, for
// use under the chip where there is room. Empty until the first event arrives.
export function ChainDetail({ watch, className = '' }) {
  if (!watch || !watch.status) return null
  const conf = Number(watch.confirmations) || 0
  const depth = Number(watch.anchor_depth) || 0
  const parts = []
  if (watch.status === 'regressed') {
    parts.push('The Sequentia block that confirmed this left the active chain after a Bitcoin reorg.')
  } else {
    parts.push(`${conf} Sequentia confirmation${conf === 1 ? '' : 's'}.`)
    if (watch.anchor_height) {
      parts.push(`Anchored to Bitcoin block ${Number(watch.anchor_height).toLocaleString('en-US')} (depth ${depth}).`)
    } else if (depth > 0) {
      parts.push(`Bitcoin anchor depth ${depth}.`)
    } else {
      parts.push('Not yet anchored to a Bitcoin block, so not final.')
    }
  }
  return <p className={`text-[11px] leading-relaxed text-ink-700/60 ${className}`}>{parts.join(' ')}</p>
}

// The reorg-regression banner. Bitcoin anchoring is supreme: when a watched
// txid's confirming block is orphaned by a Bitcoin reorg, Sequentia unwinds in
// real time and the chip regresses. This states plainly why.
export function ReorgBanner({ watch, className = '' }) {
  if (!isRegressed(watch)) return null
  return (
    <div
      className={`flex items-start gap-3 rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-800 ${className}`}
    >
      <Icon.shield width={18} height={18} className="mt-0.5 shrink-0" />
      <div>
        <div className="font-semibold">Anchoring is supreme: Sequentia follows Bitcoin in real time</div>
        <p className="mt-1 leading-relaxed">
          A Bitcoin reorg orphaned the block that confirmed this issuance, so Sequentia withdrew
          it. This is the chain working as designed, not an error: a state is only as final as its
          Bitcoin anchor is deep. The status will re-advance once the mint reconfirms under the new
          Bitcoin history.
        </p>
      </div>
    </div>
  )
}
