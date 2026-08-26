import { useEffect, useState } from 'react'
import { Icon } from './icons'
import { useStore } from '../lib/store'
import { isAid, isXonly, looksLikeDescriptor } from '../lib/statements'
import { resolveAccountKey } from '../lib/account'
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
// or invest. The eligibility verification, which the provider performs, is a
// separate step on /id/register once the identity exists.

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

// Proving a DESCRIPTOR, which is the branch for a wallet that has no OpenAMP
// account at all: a hardware wallet, a node wallet, an ordinary Bitcoin-style
// one. It proves itself the way wallets have proved things for fifteen years,
// by signing a message with an address, so there is nothing to install and
// nothing new to learn.
function DescriptorProof({ descriptor, onDone, onBack }) {
  const { walletChallenge, walletSignIn, walletRegisterId } = useStore()
  const [chal, setChal] = useState(null)
  const [sig, setSig] = useState('')
  const [form, setForm] = useState({ name: '', residence: 'AE' })
  const [step, setStep] = useState('loading') // loading | sign | profile
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)

  useEffect(() => {
    let cancelled = false
    walletChallenge(descriptor)
      .then((c) => {
        if (!cancelled) {
          setChal(c)
          setStep('sign')
        }
      })
      .catch((e) => {
        if (!cancelled) {
          setErr(e.message)
          setStep('sign')
        }
      })
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [descriptor])

  const renew = async () => {
    setErr(null)
    setSig('')
    setStep('loading')
    try {
      setChal(await walletChallenge(descriptor))
    } catch (e) {
      setErr(e.message)
    } finally {
      setStep('sign')
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
    } catch (e) {
      setErr(e.message)
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

  if (step === 'loading') {
    return (
      <div className="flex items-center gap-3 py-8 text-sm text-ink-700/70">
        <span className="h-4 w-4 animate-spin rounded-full border-2 border-btc/30 border-t-btc" />
        Reading that wallet
      </div>
    )
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

  return (
    <div className="space-y-4">
      {chal && (
        <>
          <p className="text-sm leading-relaxed text-ink-700/80">
            In that wallet, find its &ldquo;sign a message&rdquo; screen, pick the address below,
            and sign the message below with it. In a Sequentia node the same thing is{' '}
            <span className="font-mono text-xs">signmessage</span>. SeqPal never sees a key.
          </p>
          <dl className="space-y-3 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] p-4 text-xs">
            <div>
              <dt className="text-ink-700/70">
                Address to sign with{typeof chal.index === 'number' ? ` (address ${chal.index})` : ''}
              </dt>
              <dd className="mt-1 break-all font-mono text-ink-900">{chal.address}</dd>
            </div>
            <div>
              <dt className="font-semibold text-ink-900">
                Message to sign, and nothing else in that box
              </dt>
              <dd className="mt-1 break-all font-mono text-ink-900">{chal.challenge}</dd>
            </div>
          </dl>
          <Field id="wd-sig" label="Signature from that wallet">
            <textarea
              id="wd-sig"
              className="input font-mono text-xs"
              rows={3}
              spellCheck={false}
              value={sig}
              onChange={(e) => setSig(e.target.value)}
            />
          </Field>
        </>
      )}
      <ErrorNote>{err}</ErrorNote>
      {/* A challenge that ran out mid-signing is the likeliest failure here, and
          going back to re-paste the descriptor to get another one is a poor way
          to spend it. */}
      {err && (
        <button onClick={renew} className="btn-outline w-full text-sm">
          Get a fresh challenge and try again
        </button>
      )}
      <div className="flex gap-3">
        <button
          onClick={submit}
          disabled={!chal || busy || sig.trim().length < 20}
          className="btn-primary flex-1 disabled:opacity-50"
        >
          {busy ? <Spinner /> : null}
          {chal?.registered ? 'Sign in' : 'Continue'}
        </button>
        <button onClick={onBack} className="btn-outline">
          Back
        </button>
      </div>
    </div>
  )
}

// Linking a wallet SeqPal cannot ask directly. One field, because a holder
// should not have to know which of these things they are holding: an account id,
// an account key and a wallet descriptor are all just "what my wallet shows me",
// and which one it is decides how the wallet proves itself, not whether it can.
function LinkWallet({ onIdentity, onBack, onDone, busy }) {
  const [entry, setEntry] = useState('')
  const [err, setErr] = useState(null)
  const [resolving, setResolving] = useState(false)
  const [proving, setProving] = useState(null) // a descriptor being proved
  const raw = entry.trim()
  const value = raw.toLowerCase()
  const isDesc = looksLikeDescriptor(raw)
  const known = isAid(value) || isXonly(value) || isDesc

  // An account id is resolved to its key through the policy server, and then
  // CHECKED: the id is re-derived from the key returned and must match what was
  // pasted, so a wrong or substituted key is caught here rather than at a
  // signature that mysteriously fails to verify.
  const submit = async () => {
    setErr(null)
    if (isDesc) {
      setProving(raw)
      return
    }
    setResolving(true)
    try {
      const { xonly, aid } = await resolveAccountKey(value)
      onIdentity({ kind: 'linked', xonly, aid })
    } catch (e) {
      setErr(e.message)
    } finally {
      setResolving(false)
    }
  }

  // Say what is wrong with what is actually in the box. A disabled button that
  // explains nothing is the reason someone pastes the right thing from the wrong
  // screen and has no way to find out.
  const hint = () => {
    if (!value || known) return null
    if (!/^[0-9a-f]+$/.test(value)) {
      return 'That is neither hex nor a wallet descriptor. Paste the value your wallet shows, with nothing around it.'
    }
    return `That is ${value.length} hex characters. An account id is 40, and an account key is 64.`
  }
  const problem = hint()

  if (proving) {
    return <DescriptorProof descriptor={proving} onDone={onDone} onBack={() => setProving(null)} />
  }

  return (
    <div className="space-y-4">
      <p className="text-sm leading-relaxed text-ink-700/80">
        Any Sequentia wallet works. Paste whatever it shows you for your account: its{' '}
        <strong>account id</strong>, its account key, or its public{' '}
        <strong>wallet descriptor</strong>. SeqPal works out which and asks that wallet to prove
        it is yours.
      </p>
      <Field
        id="link-account"
        label="Your account, as your wallet shows it"
        hint="An account id (40 hex), an account key (64 hex), or the public descriptor your wallet exports for its own addresses (the wpkh one; its legacy pkh form works too). All are public. Never paste a descriptor containing a private key."
      >
        <textarea
          id="link-account"
          className="input font-mono text-xs"
          rows={2}
          spellCheck={false}
          placeholder="account id, account key, or wallet descriptor"
          value={entry}
          onChange={(e) => setEntry(e.target.value)}
        />
      </Field>
      {isDesc && (
        <p className="rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3 text-xs leading-relaxed text-ink-700/80">
          A wallet linked by descriptor can hold freely-tradable stocks, network-enforced assets
          and the distributions attached to them. Restricted assets need an OpenAMP account,
          which you can attach to this ID at any time.
        </p>
      )}
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
              onDone={onDone}
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
