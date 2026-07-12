import { useState } from 'react'
import { Icon } from './icons'
import { txUrl, assetUrl } from '../lib/chain'

// A chain identifier that is always fully copyable and, where it resolves on an
// explorer, always a link. It may be visually truncated, but the full value is
// on the copy button and behind the link: no identifier is ever a dead,
// truncated string (M3 requirement 4).
//
// kind: 'tx' | 'asset' add an explorer deep link. 'hash' | 'aid' | undefined are
// shown mono with a copy button but no explorer link (a contract hash or an AID
// is not an explorer object).
export default function CopyId({ value, kind, label, className = '' }) {
  const [copied, setCopied] = useState(false)
  if (!value) {
    return <span className="text-ink-700/50">not minted</span>
  }

  const href = kind === 'tx' ? txUrl(value) : kind === 'asset' ? assetUrl(value) : null
  const short = value.length > 22 ? `${value.slice(0, 10)}…${value.slice(-8)}` : value

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      setTimeout(() => setCopied(false), 1400)
    } catch {
      // Clipboard denied (older browser or permission): the link and the title
      // attribute still expose the full value, so nothing is hidden.
    }
  }

  const text = href ? (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      className="inline-flex items-center gap-1 font-mono text-xs text-seq-600 hover:underline"
      title={`${label || 'Open'} on the Sequentia explorer: ${value}`}
    >
      {short}
      <Icon.external width={12} height={12} className="opacity-70" />
    </a>
  ) : (
    <span className="break-all font-mono text-xs text-ink-800" title={value}>
      {short}
    </span>
  )

  return (
    <span className={`inline-flex items-center gap-1.5 ${className}`}>
      {text}
      <button
        type="button"
        onClick={copy}
        aria-label={`Copy ${label || 'identifier'}`}
        className="inline-flex h-5 w-5 items-center justify-center rounded text-ink-600 hover:bg-ink-900/[0.06] hover:text-ink-900"
      >
        {copied ? <Icon.check width={13} height={13} /> : <Icon.copy width={13} height={13} />}
      </button>
    </span>
  )
}
