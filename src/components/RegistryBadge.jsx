import { useEffect, useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import { getRegistryEntry } from '../lib/market'
import { registryPath } from '../lib/chain'

// Resolves the asset in the Sequentia asset registry (/registry/{id}) and shows
// the ticker AS THE REGISTRY RESOLVES IT, deep-linking the entry. The ticker on
// this line is not what SeqPal typed into a form: it is what an independent
// registry returns after checking the on-chain contract commitment, which is why
// "registry-resolved" is a stronger claim than the label on the issuance form.
export default function RegistryBadge({ assetId }) {
  const [state, setState] = useState({ loading: true })

  useEffect(() => {
    if (!assetId) return undefined
    let cancelled = false
    setState({ loading: true })
    getRegistryEntry(assetId)
      .then((entry) => {
        if (!cancelled) setState({ loading: false, entry })
      })
      .catch(() => {
        if (!cancelled) setState({ loading: false, entry: null, unreachable: true })
      })
    return () => {
      cancelled = true
    }
  }, [assetId])

  const { loading, entry } = state
  if (loading) return <span className="text-ink-700/50">resolving…</span>
  if (!entry) {
    return (
      <span className="text-ink-700/60">
        not yet published (registry publication runs once the mint is confirmed)
      </span>
    )
  }

  const verified = entry.verified
  return (
    <span className="inline-flex flex-wrap items-center gap-2">
      <a
        href={registryPath(assetId)}
        target="_blank"
        rel="noopener noreferrer"
        className="inline-flex items-center gap-1 font-medium text-seq-600 hover:underline"
      >
        {entry.contract?.ticker || entry.contract?.name || 'registry entry'}
        <Icon.external width={12} height={12} className="opacity-70" />
      </a>
      <Badge color={verified ? 'emerald' : 'amber'}>
        {verified ? (
          <>
            <Icon.check width={12} height={12} /> registry-verified
          </>
        ) : (
          'registry-listed (discovery)'
        )}
      </Badge>
    </span>
  )
}
