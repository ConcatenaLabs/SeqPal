import { useCallback, useEffect, useState } from 'react'
import { Icon } from './icons'
import { useStore } from '../lib/store'
import * as api from '../lib/api'
import * as wallet from '../lib/wallet'
import { isAid, isXonly, looksLikeDescriptor } from '../lib/statements'
import { resolveAccountKey } from '../lib/account'

// The wallets one SeqPal ID is held in.
//
// A holder who opened their ID with a web wallet and also runs the browser
// extension is one person with one set of obligations. Making them keep two
// SeqPal IDs would say otherwise: two verifications, two passports, two sets of
// eligibility, for one human being. So an ID holds as many wallets as its
// holder can prove, and signing in with any of them lands in the same account.
//
// An OpenAMP account is the exception, at one. Restricted assets settle in it,
// and a second would leave no answer to which one they settle in.
export default function LinkedWallets() {
  const { refresh } = useStore()
  const [state, setState] = useState({ wallets: [], has_enclave: false })
  const [entry, setEntry] = useState('')
  const [label, setLabel] = useState('')
  const [step, setStep] = useState('idle') // idle | sign | busy
  const [chal, setChal] = useState(null)
  const [sig, setSig] = useState('')
  const [err, setErr] = useState(null)

  const load = useCallback(async () => {
    try {
      setState(await api.accountWallets())
    } catch (e) {
      setErr(e.message)
    }
  }, [])
  useEffect(() => {
    load()
  }, [load])

  const value = entry.trim()
  const kind = looksLikeDescriptor(value)
    ? 'descriptor'
    : isAid(value.toLowerCase()) || isXonly(value.toLowerCase())
      ? 'enclave'
      : null

  const start = async () => {
    setErr(null)
    setStep('busy')
    try {
      if (kind === 'descriptor') {
        const res = await api.linkWallet({ descriptor: value, label })
        setChal(res)
        setStep('sign')
        return
      }
      // An OpenAMP account: resolve an account id to its key, then prove it the
      // way an enclave always proves itself, with a tagged challenge.
      const { xonly } = await resolveAccountKey(value.toLowerCase())
      const res = await api.linkWallet({ xonly, label })
      if (res?.challenge) {
        const signature = await signEnclaveChallenge(res.challenge)
        if (!signature) {
          // No extension to ask: show the challenge and take the signature here.
          setChal({ ...res, xonly, enclave: true })
          setStep('sign')
          return
        }
        await api.linkWallet({ xonly, challenge: res.challenge, sig: signature, label })
      }
      await done()
    } catch (e) {
      setErr(e.message)
      setStep('idle')
    }
  }

  // The extension signs the enclave challenge if it is here. If it is not, the
  // holder signs it in their own wallet and pastes it back, on the same panel as
  // every other signature: a browser prompt cannot be styled, cannot be copied
  // out of comfortably, and is blocked outright in some contexts.
  const signEnclaveChallenge = async (challenge) => {
    const p = await wallet.waitForProvider(600)
    if (!p) return null
    try {
      return await wallet.signTagged(wallet.CHALLENGE_TAG, { statement: challenge })
    } catch {
      return null
    }
  }

  const finishDescriptor = async () => {
    setErr(null)
    setStep('busy')
    try {
      await api.linkWallet(
        chal.enclave
          ? { xonly: chal.xonly, challenge: chal.challenge, sig: sig.trim(), label }
          : { descriptor: chal.descriptor, challenge: chal.challenge, sig: sig.trim(), label }
      )
      await done()
    } catch (e) {
      setErr(e.message)
      setStep('sign')
    }
  }

  const done = async () => {
    setEntry('')
    setLabel('')
    setSig('')
    setChal(null)
    setStep('idle')
    await load()
    await refresh()
  }

  const unlink = async (id) => {
    setErr(null)
    try {
      await api.unlinkWallet(id)
      await load()
      await refresh()
    } catch (e) {
      setErr(e.message)
    }
  }

  return (
    <div className="card p-6">
      <div className="flex items-center gap-2">
        <Icon.lock width={18} height={18} className="text-btc-600" />
        <h3 className="font-bold text-ink-900">Wallets on this SeqPal ID</h3>
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/80">
        One identity, as many wallets as you can prove. Sign in with any of them and you are in
        this same account. Only an OpenAMP account is limited to one, because restricted assets
        settle in it.
      </p>

      <ul className="mt-4 space-y-2">
        {state.wallets.map((wl) => (
          <li
            key={wl.id}
            className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3"
          >
            <div className="min-w-0">
              <div className="text-sm font-semibold text-ink-900">
                {wl.label || (wl.kind === 'enclave' ? 'OpenAMP account' : 'Wallet')}
                <span className="ml-2 text-xs font-normal text-ink-700/60">
                  {wl.kind === 'enclave' ? 'holds restricted assets' : 'addresses only'}
                </span>
              </div>
              <div className="truncate font-mono text-xs text-ink-700/70">
                {wl.kind === 'enclave' ? wl.enclave_aid || wl.xonly : wl.descriptor}
              </div>
            </div>
            {wl.kind !== 'enclave' && state.wallets.length > 1 && (
              <button onClick={() => unlink(wl.id)} className="btn-ghost text-xs text-ink-700/70">
                Unlink
              </button>
            )}
          </li>
        ))}
      </ul>

      {step === 'sign' && chal ? (
        <div className="mt-5 space-y-3">
          <p className="text-sm leading-relaxed text-ink-700/80">
            {chal.enclave
              ? `Sign this challenge in that wallet, tagged ${wallet.CHALLENGE_TAG}, and paste the signature back.`
              : `Sign this in that wallet, with address ${chal.index}, and paste the signature back.`}
          </p>
          <dl className="space-y-2 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] p-4 text-xs">
            {chal.address && (
              <div>
                <dt className="text-ink-700/70">Address to sign with</dt>
                <dd className="mt-1 break-all font-mono text-ink-900">{chal.address}</dd>
              </div>
            )}
            <div>
              <dt className="font-semibold text-ink-900">Message, and nothing else in that box</dt>
              <dd className="mt-1 break-all font-mono text-ink-900">{chal.challenge}</dd>
            </div>
          </dl>
          <textarea
            className="input font-mono text-xs"
            rows={3}
            spellCheck={false}
            placeholder="signature"
            value={sig}
            onChange={(e) => setSig(e.target.value)}
          />
          {err && <p className="rounded-lg bg-rose-50 px-3 py-2 text-sm text-rose-700">{err}</p>}
          <div className="flex gap-3">
            <button
              onClick={finishDescriptor}
              disabled={sig.trim().length < 20}
              className="btn-primary flex-1 disabled:opacity-50"
            >
              Link this wallet
            </button>
            <button onClick={() => setStep('idle')} className="btn-outline">
              Cancel
            </button>
          </div>
        </div>
      ) : (
        <div className="mt-5 space-y-3">
          <div>
            <label className="label" htmlFor="link-more">
              Link another wallet
            </label>
            <textarea
              id="link-more"
              className="input font-mono text-xs"
              rows={2}
              spellCheck={false}
              placeholder="wallet descriptor, or an OpenAMP account id or key"
              value={entry}
              onChange={(e) => setEntry(e.target.value)}
            />
            <p className="mt-1.5 text-xs leading-relaxed text-ink-700/60">
              A public descriptor adds a wallet whose addresses this ID recognises. An OpenAMP
              account id or key adds the account restricted assets settle in
              {state.has_enclave ? ', which this ID already has' : ''}.
            </p>
          </div>
          <input
            className="input"
            placeholder="A name for it (optional)"
            value={label}
            onChange={(e) => setLabel(e.target.value)}
          />
          {err && <p className="rounded-lg bg-rose-50 px-3 py-2 text-sm text-rose-700">{err}</p>}
          <button
            onClick={start}
            disabled={!kind || step === 'busy'}
            className="btn-outline disabled:opacity-50"
          >
            {step === 'busy' ? 'Working' : 'Prove and link'}
          </button>
        </div>
      )}
    </div>
  )
}
