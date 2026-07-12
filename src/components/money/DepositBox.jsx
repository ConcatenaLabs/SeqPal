import { useState } from 'react'
import { Icon } from '../icons'
import QrCode from '../QrCode'
import { fmtRail, railMeta } from '../../lib/money'

// A per-payment deposit instruction: the QR, the full copyable address, the
// exact amount to send in the rail's OWN units, and the confirmation depth at
// which the deposit is credited. Nothing is final at 0 confirmations, so the
// confs line is always shown. Reused by the subscription and the platform-fee
// flows.
export default function DepositBox({ rail, address, payAmount, payCcy, confsRequired, registrarNote }) {
  const [copied, setCopied] = useState(false)
  const meta = railMeta(rail)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(address)
      setCopied(true)
      setTimeout(() => setCopied(false), 1400)
    } catch {
      // Clipboard denied: the full address text is still shown below.
    }
  }
  return (
    <div className="rounded-xl border border-ink-900/10 bg-white p-4">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-start">
        <QrCode value={address} size={148} className="shrink-0" />
        <div className="min-w-0 flex-1">
          <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
            Send exactly
          </div>
          <div className="mt-0.5 text-lg font-extrabold text-ink-900">
            {fmtRail(payAmount, payCcy)}
          </div>
          <div className="mt-3 text-xs font-semibold uppercase tracking-wide text-ink-700/60">
            To this {meta.chain} deposit address
          </div>
          <div className="mt-1 flex items-start gap-2">
            <code className="min-w-0 flex-1 break-all rounded-md bg-ink-900/[0.04] px-2.5 py-1.5 font-mono text-xs text-ink-800">
              {address}
            </code>
            <button
              type="button"
              onClick={copy}
              aria-label="Copy deposit address"
              className="mt-0.5 inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-ink-600 hover:bg-ink-900/[0.06] hover:text-ink-900"
            >
              {copied ? <Icon.check width={15} height={15} /> : <Icon.copy width={15} height={15} />}
            </button>
          </div>
          <p className="mt-2 flex items-start gap-1.5 text-xs text-ink-700/70">
            <Icon.clock width={13} height={13} className="mt-0.5 shrink-0" />
            Credited to escrow after {confsRequired} confirmation
            {Number(confsRequired) === 1 ? '' : 's'} on {meta.chain}. Nothing is final at 0
            confirmations: a Sequentia state is only as final as its Bitcoin anchor is deep.
          </p>
        </div>
      </div>
      {registrarNote && (
        <p className="mt-3 flex items-start gap-1.5 rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-700">
          <Icon.exchange width={13} height={13} className="mt-0.5 shrink-0" />
          {registrarNote}
        </p>
      )}
    </div>
  )
}
