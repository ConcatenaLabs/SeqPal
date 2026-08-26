import { useEffect, useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { Icon } from '../components/icons'
import { Badge } from '../components/ui'
import SignInGate from '../components/SignInGate'
import * as api from '../lib/api'
import OfflineSignature from '../components/OfflineSignature'
import { getBalance } from '../lib/openamp'
import { useStore } from '../lib/store'

// The holder-side claim page for a shareholder action (dividend or vote) on a
// freely-tradable asset. The holder proves their holding by naming the exact
// on-chain outputs (outpoints) they hold and signing a statement with their own
// key; the server checks the outputs against the snapshot taken at the record
// height. Nothing here moves funds: a dividend pays to the payout address the
// holder names, and a vote records the ballot choice, weighted by the atoms in
// the proven outputs.

function parseOutpoints(text) {
  return text
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean)
}

export default function ActionClaim() {
  const { id } = useParams()
  const { loading, isSignedIn, account, hasKey, xonly, signHoldingStmt } = useStore()
  const [action, setAction] = useState(null)
  const [loadErr, setLoadErr] = useState(null)
  const [mine, setMine] = useState(null) // my utxos of this asset, when readable
  const [selected, setSelected] = useState({}) // outpoint -> true
  const [pasted, setPasted] = useState('')
  const [payout, setPayout] = useState('')
  const [choice, setChoice] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const [result, setResult] = useState(null)
  // The key that controls the outpoints being proven. An OpenAMP account holds
  // them under its own key; a SeqPal ID that is only a wallet holds a
  // freely-tradable token under a key of that wallet, and names it here.
  const [claimKey, setClaimKey] = useState('')
  const [prep, setPrep] = useState(null)
  const [sig, setSig] = useState('')

  useEffect(() => {
    let cancelled = false
    api
      .getAction(id)
      .then((r) => {
        if (!cancelled) setAction(r.action || r)
      })
      .catch((e) => {
        if (!cancelled) setLoadErr(e.message)
      })
    return () => {
      cancelled = true
    }
  }, [id])

  // Offer the holder's own confirmed outputs of this asset as a picklist when
  // the balance is publicly readable; pasting outpoints always works too.
  useEffect(() => {
    if (!account || !action?.asset) return
    let cancelled = false
    getBalance(account.aid, action.asset)
      .then((b) => {
        if (!cancelled) setMine(b.utxos || [])
      })
      .catch(() => {
        if (!cancelled) setMine(null)
      })
    return () => {
      cancelled = true
    }
  }, [account, action])

  const outpoints = useMemo(() => {
    const fromPicks = Object.keys(selected).filter((k) => selected[k])
    const fromPaste = parseOutpoints(pasted)
    return [...new Set([...fromPicks, ...fromPaste])]
  }, [selected, pasted])

  const isVote = action?.kind === 'vote'

  const submit = async (e) => {
    e.preventDefault()
    setErr(null)
    setResult(null)
    if (outpoints.length === 0) {
      setErr('Select or paste at least one outpoint (txid:vout) you hold.')
      return
    }
    if (isVote && !choice) {
      setErr('Pick a ballot choice.')
      return
    }
    if (!isVote && !payout.trim()) {
      setErr('Enter the Sequentia address the dividend should pay to.')
      return
    }
    const pubkey = (hasKey ? xonly : claimKey.trim().toLowerCase()) || ''
    if (!pubkey) {
      setErr('Enter the x-only public key that controls those outpoints.')
      return
    }
    setBusy(true)
    try {
      const sorted = [...outpoints].sort()
      const fields = {
        action_id: id,
        asset: action.asset,
        record_height: action.record_height,
        outpoints: sorted,
        purpose: isVote ? 'vote' : 'dividend',
        aid: account.aid,
        ...(isVote ? { choice } : { payout_address: payout.trim() }),
      }
      if (!hasKey) {
        // Nothing here can sign: ask seqpald for the exact characters instead.
        const got = await api.claimAction(id, {
          pubkey,
          outpoints: sorted,
          ...(isVote ? { choice } : { payout_address: payout.trim() }),
        })
        if (got.sign_this_message) {
          setPrep(got)
          return
        }
        setResult(got)
        return
      }
      const signature = await signHoldingStmt(fields)
      if (!signature) throw new Error('Your wallet did not return a signature, so nothing was claimed.')
      const res = await api.claimAction(id, {
        pubkey,
        outpoints: sorted,
        ...(isVote ? { choice } : { payout_address: payout.trim() }),
        sig: signature,
      })
      setResult(res)
    } catch (e2) {
      setErr(e2.message)
    } finally {
      setBusy(false)
    }
  }

  const submitSigned = async () => {
    setErr(null)
    setBusy(true)
    try {
      const res = await api.claimAction(id, {
        pubkey: claimKey.trim().toLowerCase(),
        outpoints: [...outpoints].sort(),
        ...(isVote ? { choice } : { payout_address: payout.trim() }),
        sig: sig.trim(),
      })
      setSig('')
      setPrep(null)
      setResult(res)
    } catch (e2) {
      setErr(e2.message)
    } finally {
      setBusy(false)
    }
  }

  if (loading) {
    return (
      <section className="container-x flex justify-center py-24">
        <span className="h-8 w-8 animate-spin rounded-full border-4 border-btc/20 border-t-btc" />
      </section>
    )
  }
  if (!isSignedIn) {
    return (
      <SignInGate
        title="Sign in to claim"
        body="Collecting a dividend or casting a vote needs your SeqPal ID: your identity is verified and your key signs the holding proof."
        backTo="/id"
        backLabel="SeqPal ID home"
      />
    )
  }

  return (
    <section className="container-x max-w-3xl py-12">
      <Link to="/holdings" className="inline-flex items-center gap-1.5 text-sm font-medium text-ink-700 hover:text-ink-900">
        <Icon.arrowLeft width={15} height={15} /> My holdings
      </Link>

      {loadErr && (
        <div className="mt-6 rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700">
          This action could not be loaded ({loadErr}).
        </div>
      )}

      {action && (
        <>
          <div className="mt-6 flex flex-wrap items-center gap-3">
            <h1 className="text-2xl font-extrabold tracking-tight text-ink-900">
              {isVote ? 'Cast your vote' : 'Claim your dividend'}
            </h1>
            <Badge color="seq">{action.status || 'open'}</Badge>
          </div>
          <p className="mt-2 leading-relaxed text-ink-700/85">
            {action.memo || (isVote ? 'A shareholder vote.' : 'A declared dividend.')}
            {' '}The snapshot of who held what was taken from the chain at the first pass at
            or after Sequentia block {Number(action.record_height || 0).toLocaleString()}.
            You claim by proving which of those outputs are yours: name them and sign with
            your own key. Your identity is already verified through your SeqPal ID.
          </p>

          <form onSubmit={submit} className="card mt-6 space-y-4 p-6">
            <div>
              <div className="label">Your holdings (outpoints)</div>
              {mine && mine.length > 0 && (
                <div className="mb-2 space-y-1.5">
                  {mine.map((u) => {
                    const op = `${u.txid}:${u.vout}`
                    return (
                      <label key={op} className="flex cursor-pointer items-center gap-2 rounded-lg bg-ink-900/[0.03] px-3 py-2">
                        <input
                          type="checkbox"
                          className="h-4 w-4 accent-btc"
                          checked={!!selected[op]}
                          onChange={(e) => setSelected({ ...selected, [op]: e.target.checked })}
                        />
                        <span className="break-all font-mono text-xs text-ink-800">{op}</span>
                        <span className="ml-auto shrink-0 font-mono text-xs text-ink-700/60">
                          {Number(u.atoms || 0).toLocaleString()} atoms
                        </span>
                      </label>
                    )
                  })}
                </div>
              )}
              <textarea
                className="input min-h-[70px] w-full resize-y font-mono text-xs"
                placeholder={'Or paste outpoints, one per line, as txid:vout'}
                value={pasted}
                onChange={(e) => setPasted(e.target.value)}
              />
              <p className="mt-1 text-xs text-ink-700/60">
                {outpoints.length} outpoint{outpoints.length === 1 ? '' : 's'} selected.
              </p>
            </div>

            {isVote ? (
              <div>
                <div className="label">Your ballot choice</div>
                <div className="flex flex-wrap gap-2">
                  {(action.choices || []).map((c) => (
                    <button
                      type="button"
                      key={c}
                      onClick={() => setChoice(c)}
                      className={`rounded-lg border px-3 py-2 text-sm font-semibold transition-colors ${
                        choice === c
                          ? 'border-btc bg-btc-50 text-btc-700'
                          : 'border-ink-900/15 text-ink-700 hover:bg-ink-900/[0.02]'
                      }`}
                    >
                      {c}
                    </button>
                  ))}
                </div>
              </div>
            ) : (
              <div>
                <label className="label" htmlFor="claim-payout">
                  Payout address
                </label>
                <input
                  id="claim-payout"
                  className="input font-mono text-xs"
                  placeholder="an ordinary Sequentia address the dividend pays to"
                  value={payout}
                  onChange={(e) => setPayout(e.target.value)}
                />
              </div>
            )}

            {err && <p className="text-sm font-medium text-rose-600">{err}</p>}
            {result && (
              <div className="rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800">
                <span className="font-semibold">
                  {isVote ? 'Vote recorded.' : 'Claim recorded.'}
                </span>{' '}
                {result.note ||
                  (isVote
                    ? 'Your ballot is weighted by the atoms in the outputs you proved.'
                    : 'The payout settles on chain to the address you named; nothing is final at 0 confirmations.')}
              </div>
            )}

            {!hasKey && (
              <div>
                <label className="label" htmlFor="claim-key">
                  The key that controls those outputs (32-byte x-only, hex)
                </label>
                <input
                  id="claim-key"
                  className="input font-mono text-xs"
                  spellCheck={false}
                  placeholder="x-only public key, 64 hex"
                  value={claimKey}
                  onChange={(e) => setClaimKey(e.target.value)}
                />
                <p className="mt-1.5 text-xs leading-relaxed text-ink-700/60">
                  The proof is checked against this key and the outputs it controls, so it has to
                  be the one that actually holds them.
                </p>
              </div>
            )}

            <button disabled={busy} className="btn-primary w-full disabled:opacity-60">
              {busy
                ? 'Signing and submitting…'
                : isVote
                  ? 'Sign the holding proof and vote'
                  : 'Sign the holding proof and claim'}
            </button>
            <p className="text-center text-xs leading-relaxed text-ink-700/60">
              Your wallet signs a statement binding these outpoints to your{' '}
              {isVote ? 'ballot choice' : 'payout address'}. The key never leaves your
              wallet, and SeqPal never sees it.
            </p>
            <OfflineSignature
              prep={prep}
              sig={sig}
              onSig={setSig}
              onSubmit={submitSigned}
              busy={busy}
              label={isVote ? 'Submit the ballot' : 'Submit the claim'}
            />
          </form>
        </>
      )}
    </section>
  )
}
