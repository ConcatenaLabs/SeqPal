import { useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import CopyId from './CopyId'
import * as api from '../lib/api'
import { usePoll } from '../lib/poll'
import { useStore } from '../lib/store'

// The investor payout mandate (M7 section 2). A distribution pays ONLY a
// registered ordinary Sequentia address, captured here with a BIP340 signature by
// the holder's own SeqPal ID key over the exact canonical bytes seqpald returns
// (the M5 mandate tag and verify path), never a raw digest. The address must be an
// ordinary, wallet-scannable Sequentia address: an enclave key-path (2-of-2)
// address is refused, because no wallet scans it and paying one would strand the
// funds. That refusal is enforced server-side and surfaced verbatim here; a light
// client-side check catches the obvious cases first. For this milestone the only
// chain is Sequentia, since USDX distributions are the plan's scope and the tBTC
// payout leg is its explicit cut.

// A light, non-authoritative check that the address looks like an ordinary
// transparent Sequentia address. Sequentia's default unblinded address shares
// Bitcoin's testnet bech32 format (tb1). A confidential blinded address uses a
// distinct blech32 HRP (tsqb), which is not an ordinary payout address. The server
// is authoritative, including the enclave-address rejection a browser cannot see.
function clientAddressIssue(addr) {
  const a = (addr || '').trim()
  if (!a) return 'Enter your payout address.'
  if (/^(tsqb|sqb)1/i.test(a)) {
    return 'That is a confidential (blinded) address. Register an ordinary transparent Sequentia address, which a wallet can scan for the payout.'
  }
  if (!/^tb1[a-z0-9]{20,}$/i.test(a)) {
    return 'That does not look like an ordinary Sequentia testnet address (tb1…). Check it before signing.'
  }
  return null
}

export default function InvestorMandateCard() {
  const { signMandateStmt, xonly, hasKey } = useStore()
  const { data, refresh } = usePoll(() => api.investorMandateGet('sequentia'), {
    intervalMs: 15000,
  })
  const [address, setAddress] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const [warn, setWarn] = useState(null)

  const current = data?.mandate

  const register = async () => {
    setErr(null)
    setWarn(null)
    const addr = address.trim()
    const issue = clientAddressIssue(addr)
    if (issue) {
      setErr(issue)
      return
    }
    if (!hasKey) {
      setErr('Unlock your SeqPal ID to sign the mandate.')
      return
    }
    setBusy(true)
    try {
      // Phase 1: ask seqpald for the exact canonical bytes to sign. This also runs
      // the authoritative address validation and the enclave-address rejection, so
      // a bad address is refused here before anything is signed.
      const prep = await api.investorMandate({ chain: 'sequentia', address: addr })
      if (!prep.sign_this) {
        refresh()
        return
      }
      const sig = signMandateStmt(prep.sign_this)
      if (!sig) {
        setErr('Unlock your SeqPal ID to sign the mandate.')
        return
      }
      // Phase 2: resubmit with the signature.
      await api.investorMandate({
        chain: 'sequentia',
        address: addr,
        signature: sig,
        signer_xonly: xonly,
      })
      setAddress('')
      refresh()
    } catch (e) {
      // The server's refusal message (invalid address, enclave address, signature)
      // is user-presentable and shown verbatim.
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="card p-6">
      <div className="flex items-center gap-2">
        <Icon.wallet width={18} height={18} className="text-seq-600" />
        <h2 className="font-bold text-ink-900">Payout address</h2>
        <Badge color="seq" className="ml-auto">
          Sequentia · USDX
        </Badge>
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
        Register the ordinary Sequentia address where distributions pay you, signed with your SeqPal
        ID key. A distribution to a holder with no registered payout address is skipped with a
        notice, never paid to a guessed address.
      </p>

      {current && (
        <div className="mt-4 flex items-center justify-between gap-3 rounded-lg border border-emerald-200 bg-emerald-50 px-3 py-2.5 text-sm">
          <div className="min-w-0">
            <div className="text-xs font-medium text-emerald-700">Registered payout address</div>
            <CopyId value={current.address} label="payout address" />
          </div>
          <Icon.check width={18} height={18} className="shrink-0 text-emerald-500" />
        </div>
      )}

      <div className="mt-4 space-y-3">
        <div>
          <label className="label" htmlFor="inv-mnd-addr">
            {current ? 'Replace payout address' : 'Payout address'}
          </label>
          <input
            id="inv-mnd-addr"
            className="input font-mono text-xs"
            placeholder="tb1…"
            value={address}
            onChange={(e) => {
              setAddress(e.target.value)
              setWarn(clientAddressIssue(e.target.value))
              setErr(null)
            }}
          />
        </div>
        {warn && address.trim() && <p className="text-xs text-amber-700">{warn}</p>}
        {!hasKey && (
          <p className="text-xs text-amber-700">
            Your SeqPal ID key is locked. Sign in again to unlock it before signing the mandate.
          </p>
        )}
        {err && <p className="text-sm font-medium text-rose-600">{err}</p>}
        <button
          onClick={register}
          disabled={busy || !address.trim() || !hasKey}
          className="btn-primary w-full disabled:opacity-60"
        >
          <Icon.shield width={16} height={16} />
          {busy ? 'Signing…' : 'Sign and register payout address'}
        </button>
      </div>

      <p className="mt-3 text-[11px] leading-relaxed text-ink-700/55">
        The address must be an ordinary Sequentia address a wallet can scan. An enclave key-path
        address, where your restricted tokens live, is refused: paying one would strand the funds.
        Distributions are USDX on Sequentia in this milestone; native BTC payouts are a later step.
      </p>
    </div>
  )
}
