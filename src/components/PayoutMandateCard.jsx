import { useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import CopyId from './CopyId'
import * as api from '../lib/api'
import { usePoll } from '../lib/poll'
import { useStore } from '../lib/store'

// The issuer's payout mandate: a BIP340-signed instruction naming the ordinary
// address escrow release pays. Signed with the browser enclave key over the exact
// canonical statement seqpald returns (the M4 document-sign pattern), never a raw
// digest. An enclave key-path address is rejected server-side; escrow release
// only ever pays a mandated address. One mandate per (issuance, chain).
export default function PayoutMandateCard({ iss }) {
  const { signMandateStmt, xonly, hasKey } = useStore()
  const { data, refresh } = usePoll(() => api.mandates(iss.id), { intervalMs: 8000, deps: [iss.id] })
  const [chain, setChain] = useState('sequentia')
  const [asset, setAsset] = useState('')
  const [address, setAddress] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)

  const registered = data?.mandates || []

  const register = async () => {
    setErr(null)
    setBusy(true)
    try {
      // Step 1: ask seqpald for the exact canonical bytes to sign (it validates
      // the address network and rejects an enclave key-path address here too).
      const prep = await api.mandate(iss.id, { chain, asset: asset.trim(), address: address.trim() })
      if (!prep.sign_this) {
        // Already returned a stored mandate (shouldn't happen without a sig).
        refresh()
        return
      }
      const sig = signMandateStmt(prep.sign_this)
      if (!sig) {
        setErr('Unlock your SeqPal ID to sign the mandate.')
        return
      }
      // Step 2: resubmit with the signature.
      await api.mandate(iss.id, {
        chain,
        asset: asset.trim(),
        address: address.trim(),
        signature: sig,
        signer_xonly: xonly,
      })
      setAddress('')
      setAsset('')
      refresh()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="card p-6">
      <div className="flex items-center gap-2">
        <Icon.wallet width={18} height={18} className="text-seq-600" />
        <h2 className="font-bold text-ink-900">Payout mandate</h2>
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
        Register the ordinary address escrow release pays out to, signed with your SeqPal ID key.
        This is required before closing. Escrow release never pays an enclave key-path address.
      </p>

      {registered.length > 0 && (
        <div className="mt-4 divide-y divide-ink-900/5 rounded-lg border border-ink-900/10">
          {registered.map((m) => (
            <div key={m.chain} className="flex items-center justify-between gap-3 px-3 py-2.5 text-sm">
              <div className="flex items-center gap-2">
                <Badge color={m.chain === 'bitcoin' ? 'btc' : 'seq'}>
                  {m.chain === 'bitcoin' ? 'Bitcoin testnet4' : 'Sequentia'}
                </Badge>
                <CopyId value={m.address} label="payout address" />
              </div>
              <Icon.check width={16} height={16} className="text-emerald-500" />
            </div>
          ))}
        </div>
      )}

      <div className="mt-4 space-y-3">
        <div className="grid grid-cols-2 gap-2">
          <div>
            <label className="label" htmlFor="mnd-chain">
              Chain
            </label>
            <select
              id="mnd-chain"
              className="select"
              value={chain}
              onChange={(e) => setChain(e.target.value)}
            >
              <option value="sequentia">Sequentia (USDX release)</option>
              <option value="bitcoin">Bitcoin testnet4 (tBTC release)</option>
            </select>
          </div>
          <div>
            <label className="label" htmlFor="mnd-asset">
              Asset (optional)
            </label>
            <input
              id="mnd-asset"
              className="input font-mono text-xs"
              placeholder={chain === 'sequentia' ? 'USDX asset id' : 'BTC'}
              value={asset}
              onChange={(e) => setAsset(e.target.value)}
            />
          </div>
        </div>
        <div>
          <label className="label" htmlFor="mnd-addr">
            Payout address
          </label>
          <input
            id="mnd-addr"
            className="input font-mono text-xs"
            placeholder={chain === 'bitcoin' ? 'tb1...' : 'tb1... / ex1...'}
            value={address}
            onChange={(e) => setAddress(e.target.value)}
          />
        </div>
        {!hasKey && (
          <p className="text-xs text-amber-700">
            Your SeqPal ID key is locked. Sign in again to unlock it before signing the mandate.
          </p>
        )}
        {err && <p className="text-xs font-medium text-rose-600">{err}</p>}
        <button
          onClick={register}
          disabled={busy || !address.trim() || !hasKey}
          className="btn-primary w-full disabled:opacity-60"
        >
          <Icon.shield width={16} height={16} />
          {busy ? 'Signing…' : 'Sign and register mandate'}
        </button>
      </div>
    </div>
  )
}
