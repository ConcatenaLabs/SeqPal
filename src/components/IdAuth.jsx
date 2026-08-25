import { useEffect, useState } from 'react'
import { Icon } from './icons'
import { useStore } from '../lib/store'
import { isXonly } from '../lib/statements'
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
  const [xonly, setXonly] = useState('')
  const value = xonly.trim().toLowerCase()
  return (
    <div className="space-y-4">
      <p className="text-sm leading-relaxed text-ink-700/80">
        Any Sequentia wallet works. Find your account key, the x-only public key your wallet
        derives at <span className="font-mono text-xs">m/5/0</span>, shown wherever it lists
        your restricted-asset account, and paste it here. SeqPal will ask that wallet to sign a
        challenge to prove you hold it, and will ask it again for every statement you sign
        afterwards.
      </p>
      <Field
        id="link-xonly"
        label="Your OpenAMP account key (x-only, 64 hex)"
        hint="A public key. It is safe to paste, and it is all SeqPal keeps."
      >
        <input
          id="link-xonly"
          className="input font-mono text-xs"
          spellCheck={false}
          placeholder="0000…"
          value={xonly}
          onChange={(e) => setXonly(e.target.value)}
        />
      </Field>
      <div className="flex gap-3">
        <button
          onClick={() => onIdentity({ kind: 'linked', xonly: value, aid: null })}
          disabled={busy || !isXonly(value)}
          className="btn-primary flex-1 disabled:opacity-50"
        >
          {busy ? (
            <>
              <Spinner />
              Verifying
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
          </div>
          <div className="mt-4">
            <ErrorNote>{err}</ErrorNote>
          </div>
        </>
      )}
    </div>
  )
}
