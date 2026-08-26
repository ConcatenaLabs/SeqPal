import { useEffect, useState } from 'react'
import { Icon } from './icons'
import { useStore } from '../lib/store'
import { computeAID, isAid, isXonly } from '../lib/statements'
import * as oamp from '../lib/openamp'
import * as wallet from '../lib/wallet'
import { RESIDENCE_OPTIONS } from '../data/jurisdictions'

// Attaching a SeqPal ID to a Sequentia wallet.
//
// SeqPal is not a wallet and does not make one for you: a SeqPal ID IS the
// OpenAMP enclave account your Sequentia wallet already derives, which is why a
// security token issued to it is one you can see and move in that wallet. There
// is no "create a key here" path, and there is no backup file to lose — your
// wallet's own seed already covers the key.
//
// Two ways in. A wallet that injects window.sequentia is asked directly. Any
// other Sequentia wallet is linked by its public enclave key, and signs each
// statement out of band (see WalletSignPrompt).
//
// Registration stays role-agnostic: it never asks whether you intend to issue
// or invest. The real eligibility verification (document review + sanctions
// screen) is a separate step on /id/register once the identity exists.

export function Field({ id, label, children, hint }) {
  return (
    <div>
      <label className="label" htmlFor={id}>
        {label}
      </label>
      {children}
      {hint && <p className="mt-1.5 text-xs leading-relaxed text-ink-700/60">{hint}</p>}
    </div>
  )
}

export function ErrorNote({ children }) {
  if (!children) return null
  return <p className="rounded-lg bg-rose-50 px-3 py-2 text-sm text-rose-700">{children}</p>
}

export function Spinner() {
  return (
    <span className="h-4 w-4 animate-spin rounded-full border-2 border-white/40 border-t-white" />
  )
}

/* ─────────────────────── the profile a new account needs ─────────────────── */

function NewAccount({ identity, onDone }) {
  const { registerId } = useStore()
  const [form, setForm] = useState({ name: '', residence: 'AE', accredited: false })
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)

  const res = RESIDENCE_OPTIONS.find((r) => r.code === form.residence)
  const docVerify = ['US', 'CA'].includes(form.residence)

  const submit = async (e) => {
    e.preventDefault()
    setErr(null)
    setBusy(true)
    try {
      await registerId(identity, {
        displayName: form.name.trim(),
        residence: form.residence,
        profile: {
          residence: res.name,
          residence_code: form.residence,
          accredited: !!form.accredited,
          accreditation_basis: form.accredited ? res.accreditationLabel : null,
          accreditation_method: form.accredited ? (docVerify ? 'document' : 'self') : null,
          kyc: 'simulated',
          verified_at: new Date().toISOString(),
        },
      })
      onDone?.()
    } catch (e2) {
      setErr(e2.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} className="space-y-4">
      <div className="rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3 text-xs">
        <div className="flex justify-between gap-4">
          <span className="text-ink-700/70">Account id (AID)</span>
          <span className="font-mono text-ink-900">{identity.aid}</span>
        </div>
        <div className="mt-1.5 flex justify-between gap-4">
          <span className="text-ink-700/70">Enclave key</span>
          <span className="truncate font-mono text-ink-900">{identity.xonly}</span>
        </div>
      </div>
      <p className="text-sm leading-relaxed text-ink-700/80">
        This wallet has no SeqPal ID yet. Tell us who you are and we will register this account
        with the policy server.
      </p>
      <Field id="ind-name" label="Full legal name">
        <input
          id="ind-name"
          className="input"
          placeholder="Jordan Avery"
          value={form.name}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
        />
      </Field>
      <Field
        id="ind-residence"
        label="Country of residence"
        hint="This is your starting point. Your eligibility categories are set in the verification step, not here."
      >
        <select
          id="ind-residence"
          className="select"
          value={form.residence}
          onChange={(e) => setForm({ ...form, residence: e.target.value })}
        >
          {RESIDENCE_OPTIONS.map((r) => (
            <option key={r.code} value={r.code}>
              {r.name}
            </option>
          ))}
        </select>
      </Field>
      {res?.accreditationLabel && (
        <label className="flex items-start gap-2.5 text-sm text-ink-700">
          <input
            type="checkbox"
            className="mt-0.5"
            checked={form.accredited}
            onChange={(e) => setForm({ ...form, accredited: e.target.checked })}
          />
          <span>{res.accreditationLabel}</span>
        </label>
      )}
      <ErrorNote>{err}</ErrorNote>
      <button
        type="submit"
        disabled={busy || form.name.trim().length < 2}
        className="btn-primary w-full disabled:opacity-50"
      >
        {busy ? (
          <>
            <Spinner />
            Signing the challenge and registering
          </>
        ) : (
          <>
            Create my SeqPal ID
            <Icon.arrowRight width={16} height={16} />
          </>
        )}
      </button>
    </form>
  )
}

/* ───────────────────────── linking a wallet by hand ──────────────────────── */

function LinkWallet({ onIdentity, onBack, busy }) {
  const [entry, setEntry] = useState('')
  const [err, setErr] = useState(null)
  const [resolving, setResolving] = useState(false)
  const value = entry.trim().toLowerCase()
  const known = isAid(value) || isXonly(value)

  // Wallets show the account id far more prominently than the key it is derived
  // from, so both are accepted. An account id is resolved to its key through the
  // policy server, and then CHECKED: the id is re-derived from the key returned
  // and must match what was pasted, so a wrong or substituted key is caught here
  // rather than at a signature that mysteriously fails to verify.
  const submit = async () => {
    setErr(null)
    if (isXonly(value)) {
      onIdentity({ kind: 'linked', xonly: value, aid: computeAID([value]) })
      return
    }
    setResolving(true)
    try {
      const user = await oamp.getUser(value)
      const xonly = (user?.pubkeys || [])[0]
      if (!isXonly(xonly)) {
        throw new Error('The policy server has no account key registered for that account id.')
      }
      if (computeAID([xonly]) !== value) {
        throw new Error('That account id does not match the key the policy server returned for it. Nothing was linked.')
      }
      onIdentity({ kind: 'linked', xonly, aid: value })
    } catch (e) {
      setErr(
        e?.status === 404
          ? 'No account with that id is registered with the policy server yet. Receive a restricted asset in that wallet first, or paste the account key instead.'
          : e.message
      )
    } finally {
      setResolving(false)
    }
  }

  // Say what is wrong with what is actually in the box. A disabled button that
  // explains nothing is the reason someone pastes the right thing from the wrong
  // screen and has no way to find out.
  const hint = () => {
    if (!value) return null
    if (known) return null
    if (!/^[0-9a-f]+$/.test(value)) return 'That does not look like hex. Paste the value your wallet shows, with nothing around it.'
    return `That is ${value.length} hex characters. An account id is 40, and an account key is 64.`
  }
  const problem = hint()

  return (
    <div className="space-y-4">
      <p className="text-sm leading-relaxed text-ink-700/80">
        Any Sequentia wallet works. Paste the <strong>account id</strong> your wallet shows for
        your restricted-asset account. SeqPal looks up the account key that belongs to it, then
        asks that wallet to sign a challenge to prove you hold it, and asks it again for every
        statement you sign afterwards.
      </p>
      <Field
        id="link-aid"
        label="Your restricted-asset account id"
        hint="40 hex characters, and public: it is the same id you give a sender to receive a restricted asset. If your wallet shows you the 64-hex account key instead, that works too."
      >
        <input
          id="link-aid"
          className="input font-mono text-xs"
          spellCheck={false}
          placeholder="account id"
          value={entry}
          onChange={(e) => setEntry(e.target.value)}
        />
      </Field>
      {problem && <p className="text-xs leading-relaxed text-amber-700">{problem}</p>}
      <ErrorNote>{err}</ErrorNote>
      <div className="flex gap-3">
        <button
          onClick={submit}
          disabled={busy || resolving || !known}
          className="btn-primary flex-1 disabled:opacity-50"
        >
          {busy || resolving ? (
            <>
              <Spinner />
              {resolving ? 'Looking up that account' : 'Verifying'}
            </>
          ) : (
            'Link this wallet'
          )}
        </button>
        <button onClick={onBack} className="btn-outline">
          Back
        </button>
      </div>
    </div>
  )
}

/* ────────────── a wallet with no OpenAMP account (descriptor) ────────────── */

// Not every wallet has an OpenAMP account. A hardware wallet, a node wallet, a
// plain Bitcoin-style wallet: none of them can hold restricted assets, and
// requiring one shut them out of everything else SeqPal does. So a wallet can
// identify itself by a descriptor it controls instead, and prove it the way
// wallets have proved things for fifteen years: sign a message with an address.
function WalletDescriptorId({ onDone }) {
  const { walletChallenge, walletSignIn, walletRegisterId } = useStore()
  const [desc, setDesc] = useState('')
  const [step, setStep] = useState('descriptor') // descriptor | sign | profile
  const [chal, setChal] = useState(null)
  const [sig, setSig] = useState('')
  const [form, setForm] = useState({ name: '', residence: 'AE' })
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)

  const ask = async (e) => {
    e.preventDefault()
    setErr(null)
    setBusy(true)
    try {
      setChal(await walletChallenge(desc.trim()))
      setStep('sign')
    } catch (e2) {
      setErr(e2.message)
    } finally {
      setBusy(false)
    }
  }

  const submit = async () => {
    setErr(null)
    setBusy(true)
    try {
      if (chal.registered) {
        await walletSignIn({ descriptor: chal.descriptor, challenge: chal.challenge, sig: sig.trim() })
        onDone?.()
        return
      }
      setStep('profile')
    } catch (e2) {
      setErr(e2.message)
    } finally {
      setBusy(false)
    }
  }

  const finish = async (e) => {
    e.preventDefault()
    setErr(null)
    setBusy(true)
    try {
      const res = RESIDENCE_OPTIONS.find((r) => r.code === form.residence)
      await walletRegisterId({
        descriptor: chal.descriptor,
        challenge: chal.challenge,
        sig: sig.trim(),
        displayName: form.name.trim(),
        residence: form.residence,
        profile: {
          residence: res.name,
          residence_code: form.residence,
          kyc: 'simulated',
          verified_at: new Date().toISOString(),
        },
      })
      onDone?.()
    } catch (e2) {
      setErr(e2.message)
      setStep('sign')
    } finally {
      setBusy(false)
    }
  }

  if (step === 'profile') {
    return (
      <form onSubmit={finish} className="space-y-4">
        <p className="text-sm leading-relaxed text-ink-700/80">
          This wallet has no SeqPal ID yet. Tell us who you are and we will register it.
        </p>
        <Field id="wd-name" label="Full legal name">
          <input
            id="wd-name"
            className="input"
            placeholder="Jordan Avery"
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
          />
        </Field>
        <Field id="wd-res" label="Country of residence">
          <select
            id="wd-res"
            className="select"
            value={form.residence}
            onChange={(e) => setForm({ ...form, residence: e.target.value })}
          >
            {RESIDENCE_OPTIONS.map((r) => (
              <option key={r.code} value={r.code}>
                {r.name}
              </option>
            ))}
          </select>
        </Field>
        <ErrorNote>{err}</ErrorNote>
        <button
          type="submit"
          disabled={busy || form.name.trim().length < 2}
          className="btn-primary w-full disabled:opacity-50"
        >
          {busy ? <Spinner /> : null}
          Create my SeqPal ID
        </button>
      </form>
    )
  }

  if (step === 'sign') {
    return (
      <div className="space-y-4">
        <p className="text-sm leading-relaxed text-ink-700/80">
          Sign this challenge with the address below, in your own wallet, and paste the signature
          back. In a Sequentia node that is{' '}
          <span className="font-mono text-xs">signmessage</span>; in most wallets it is a
          &ldquo;sign message&rdquo; button. SeqPal never sees a key.
        </p>
        <dl className="space-y-2 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] p-4 text-xs">
          <div>
            <dt className="text-ink-700/70">Address to sign with</dt>
            <dd className="mt-1 break-all font-mono text-ink-900">{chal.address}</dd>
          </div>
          <div>
            <dt className="text-ink-700/70">Message to sign</dt>
            <dd className="mt-1 break-all font-mono text-ink-900">{chal.challenge}</dd>
          </div>
        </dl>
        <Field id="wd-sig" label="Signature from your wallet">
          <textarea
            id="wd-sig"
            className="input font-mono text-xs"
            rows={3}
            spellCheck={false}
            value={sig}
            onChange={(e) => setSig(e.target.value)}
          />
        </Field>
        <ErrorNote>{err}</ErrorNote>
        <div className="flex gap-3">
          <button
            onClick={submit}
            disabled={busy || sig.trim().length < 20}
            className="btn-primary flex-1 disabled:opacity-50"
          >
            {busy ? <Spinner /> : null}
            {chal.registered ? 'Sign in' : 'Continue'}
          </button>
          <button onClick={() => setStep('descriptor')} className="btn-outline">
            Back
          </button>
        </div>
      </div>
    )
  }

  return (
    <form onSubmit={ask} className="space-y-4">
      <p className="text-sm leading-relaxed text-ink-700/80">
        For a wallet that has no OpenAMP account. Paste the public{' '}
        <span className="font-mono text-xs">pkh(...)</span> descriptor your wallet exports, the
        legacy one. SeqPal derives its first address and asks you to sign a challenge with it.
      </p>
      <Field
        id="wd-desc"
        label="Your wallet descriptor"
        hint="The PUBLIC descriptor, with an extended public key. Never paste one containing a private key."
      >
        <textarea
          id="wd-desc"
          className="input font-mono text-xs"
          rows={3}
          spellCheck={false}
          placeholder="pkh([fingerprint/44h/1h/0h]tpub.../0/*)"
          value={desc}
          onChange={(e) => setDesc(e.target.value)}
        />
      </Field>
      <div className="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-xs leading-relaxed text-amber-900">
        An ID like this cannot hold OpenAMP restricted assets until you attach an OpenAMP account
        to it. Freely-tradable stocks, network-enforced assets and the distributions attached to
        them all work without one.
      </div>
      <ErrorNote>{err}</ErrorNote>
      <button type="submit" disabled={busy || desc.trim().length < 20} className="btn-primary w-full disabled:opacity-50">
        {busy ? <Spinner /> : null}
        Continue
      </button>
    </form>
  )
}

/* ───────────────────────────────── panel ─────────────────────────────────── */

export function AuthPanel({ onDone }) {
  const { connectExtension, signIn } = useStore()
  const [phase, setPhase] = useState('choose') // choose | link | profile
  const [identity, setIdentity] = useState(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const [ext, setExt] = useState('checking') // checking | present | absent

  useEffect(() => {
    let cancelled = false
    wallet.waitForProvider().then((p) => {
      if (!cancelled) setExt(p ? 'present' : 'absent')
    })
    return () => {
      cancelled = true
    }
  }, [])

  // Attach an identity, then sign in with it. seqpald answering "no account for
  // this key" is not an error: it is a new holder, and the profile form follows.
  const attach = async (get) => {
    setErr(null)
    setBusy(true)
    try {
      const id = await get()
      setIdentity(id)
      const acct = await signIn(id)
      if (acct) onDone?.()
      else setPhase('profile')
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  if (phase === 'descriptor') {
    return (
      <div className="card p-7">
        <h2 className="text-lg font-bold text-ink-900">Use a wallet with no OpenAMP account</h2>
        <div className="mt-5">
          <WalletDescriptorId onDone={onDone} />
        </div>
        <button onClick={() => setPhase('choose')} className="btn-ghost mt-4 w-full text-sm">
          Back
        </button>
      </div>
    )
  }

  if (phase === 'profile' && identity) {
    return (
      <div className="card p-7">
        <NewAccount identity={identity} onDone={onDone} />
      </div>
    )
  }

  return (
    <div className="card p-7">
      {phase === 'link' ? (
        <>
          <h2 className="text-lg font-bold text-ink-900">Link another Sequentia wallet</h2>
          <div className="mt-5">
            <LinkWallet
              busy={busy}
              onBack={() => setPhase('choose')}
              onIdentity={(id) => attach(async () => id)}
            />
          </div>
          <ErrorNote>{err}</ErrorNote>
        </>
      ) : (
        <>
          <h2 className="text-lg font-bold text-ink-900">Use your Sequentia wallet</h2>
          <p className="mt-2 text-sm leading-relaxed text-ink-700/80">
            Your SeqPal ID is the OpenAMP account your wallet already holds restricted assets in.
            SeqPal never sees your key, and there is no separate backup to keep: your wallet&apos;s
            own recovery covers it.
          </p>

          <div className="mt-5 space-y-3">
            <button
              onClick={() => attach(connectExtension)}
              disabled={busy || ext !== 'present'}
              className="btn-primary w-full disabled:opacity-50"
            >
              {busy ? (
                <>
                  <Spinner />
                  Waiting for your wallet
                </>
              ) : (
                <>
                  <Icon.lock width={16} height={16} />
                  Continue with my browser wallet
                </>
              )}
            </button>
            {ext === 'absent' && (
              <p className="text-center text-xs text-ink-700/60">
                No Sequentia wallet extension is installed in this browser.
              </p>
            )}
            <button onClick={() => setPhase('link')} className="btn-outline w-full">
              Link another Sequentia wallet
            </button>
            <button onClick={() => setPhase('descriptor')} className="btn-ghost w-full text-sm">
              My wallet has no OpenAMP account
            </button>
          </div>
          <div className="mt-4">
            <ErrorNote>{err}</ErrorNote>
          </div>
        </>
      )}
    </div>
  )
}
