// Chain-surface helpers: explorer deep links and the truthful status chip.
//
// Bitcoin anchoring is supreme on Sequentia: a block is provisional until it and
// its Bitcoin anchor are deep, and it is withdrawn the moment its anchor is
// orphaned. So no chip ever says "final", a 0-confirmation state is labeled as
// exactly that, and a regression is a first-class, rendered state.

// The live Sequentia testnet explorer (esplora). tx/{txid} and asset/{id}
// resolve there; the registry entry resolves same-origin at /registry/{id}.
export const EXPLORER = 'https://sequentiatestnet.com'
export const txUrl = (txid) => `${EXPLORER}/tx/${encodeURIComponent(txid)}`
export const assetUrl = (id) => `${EXPLORER}/asset/${encodeURIComponent(id)}`
// Same-origin registry path (Caddy fronts the box registry at /registry/); this
// is the browser-resolvable link, not the watcher's internal registry_url.
export const registryPath = (id) => `/registry/${encodeURIComponent(id)}`

const plural = (n, w) => `${n} ${w}${Number(n) === 1 ? '' : 's'}`

// Map a watch event (from the SSE stream) onto a chip descriptor. The five
// contract fields drive it: status, confirmations, anchor_depth, plus the
// additive anchor_height for the "anchored at Bitcoin block H" label.
//
//   broadcast  -> amber, "Broadcast, 0 confirmations" (never final)
//   confirmed  -> seq,   "N confirmations" (+ anchor depth if any)
//   anchored   -> emerald,"Anchored at Bitcoin block H"
//   regressed  -> rose,  "Reorged, chain state withdrawn"
//
// A missing watch (SSE not yet delivered) is a distinct pending descriptor, not
// a fabricated "confirmed".
export function chipFor(watch) {
  if (!watch || !watch.status) {
    return { label: 'Awaiting chain status', color: 'slate', dot: 'bg-ink-600/40', chain: true }
  }
  const conf = Number(watch.confirmations) || 0
  const depth = Number(watch.anchor_depth) || 0
  switch (watch.status) {
    case 'broadcast':
      return { label: 'Broadcast, 0 confirmations', color: 'amber', dot: 'bg-amber-500', chain: true }
    case 'confirmed':
      return {
        label:
          depth > 0
            ? `${plural(conf, 'confirmation')}, anchor depth ${depth}`
            : plural(conf, 'confirmation'),
        color: 'seq',
        dot: 'bg-seq-500',
        chain: true,
      }
    case 'anchored':
      return {
        label: watch.anchor_height
          ? `Anchored at Bitcoin block ${Number(watch.anchor_height).toLocaleString('en-US')}`
          : `Anchored, depth ${depth}`,
        color: 'emerald',
        dot: 'bg-emerald-500',
        chain: true,
      }
    case 'regressed':
      return { label: 'Reorged, chain state withdrawn', color: 'rose', dot: 'bg-rose-500', chain: true }
    default:
      return { label: watch.status, color: 'slate', dot: 'bg-ink-600/40', chain: true }
  }
}

// True when a watch is in the reorg-regression state, which drives the
// "anchoring is supreme" banner.
export const isRegressed = (watch) => !!watch && watch.status === 'regressed'
