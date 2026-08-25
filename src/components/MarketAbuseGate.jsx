import { useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import * as api from '../lib/api'
import { usePoll } from '../lib/poll'
import { useStore } from '../lib/store'
import { MARKET_ABUSE_TAG } from '../lib/statements'

// The once-per-investor market-abuse / insider-dealing acknowledgment (contract
// section 5). It is recorded before the platform's transfer surfaces are enabled,
// and it is disclosed HONESTLY as a platform-layer control: it gates the SeqPal
// UI, it is NOT enforced at the policy co-signature, so it never blocks a chain
// transfer on its own. When acknowledged, this renders its children (the transfer
// surface). A signature by the investor's own SeqPal ID key is optional and, when
// present, is recorded over the exact canonical bytes seqpald returns.
export default function MarketAbuseGate({ children }) {
  const { signWithKey, xonly, hasKey } = useStore()
  const { data, refresh, loading } = usePoll(() => api.marketAbuseAckGet(), { intervalMs: 30000 })
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)

  const acknowledged = !!data?.acknowledged

  const acknowledge = async (withSignature) => {
    setErr(null)
    setBusy(true)
    try {
      const body = {}
      if (withSignature) {
        if (!hasKey) {
          setErr('Connect your Sequentia wallet to sign the acknowledgment.')
          return
        }
        const sig = await signWithKey(MARKET_ABUSE_TAG, data?.sign_this || '')
        if (!sig) {
          setErr('Connect your Sequentia wallet to sign the acknowledgment.')
          return
        }
        body.signature = sig
        body.signer_xonly = xonly
      }
      await api.marketAbuseAck(body)
      refresh()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  if (acknowledged) return children

  return (
    <div className="card border-amber-200 p-6">
      <div className="flex items-center gap-2">
        <Icon.shield width={18} height={18} className="text-amber-600" />
        <h2 className="font-bold text-ink-900">Market-abuse acknowledgment</h2>
        <Badge color="amber" className="ml-auto">
          Platform-layer control
        </Badge>
      </div>
      <p className="mt-2 text-sm leading-relaxed text-ink-700/85">
        Before the transfer surfaces are enabled, acknowledge once that you understand the
        market-abuse and insider-dealing rules: you will not deal in a restricted asset on the basis
        of inside information, nor engage in manipulative trading. This is recorded against your
        SeqPal ID.
      </p>
      <p className="mt-2 text-[13px] leading-relaxed text-ink-700/70">
        This is a platform-layer control that gates this interface. It is not enforced at the
        policy co-signature, so it does not by itself block a chain transfer. It is recorded once.
      </p>

      {err && <p className="mt-3 text-sm font-medium text-rose-600">{err}</p>}

      <div className="mt-4 flex flex-wrap gap-2">
        <button
          onClick={() => acknowledge(false)}
          disabled={busy || loading}
          className="btn-primary disabled:opacity-60"
        >
          {busy ? 'Recording…' : 'Acknowledge and continue'}
        </button>
        <button
          onClick={() => acknowledge(true)}
          disabled={busy || loading || !hasKey}
          className="btn-outline disabled:opacity-60"
          title={hasKey ? 'Also record a signature by your SeqPal ID key' : 'Connect your Sequentia wallet to sign'}
        >
          <Icon.shield width={15} height={15} /> Acknowledge and sign
        </button>
      </div>
      {!hasKey && (
        <p className="mt-2 text-xs text-amber-700">
          A signed acknowledgment needs your unlocked SeqPal ID key. You can acknowledge without a
          signature now, or sign in again with your wallet.
        </p>
      )}
    </div>
  )
}
