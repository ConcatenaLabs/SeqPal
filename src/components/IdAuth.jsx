import { useState } from 'react'
import { Icon } from './icons'
import { DemoNote } from './ui'
import { useStore, downloadEnvelope, envelopeFilename } from '../lib/store'
import { passphraseStrength } from '../lib/keys'
import { RESIDENCE_OPTIONS } from '../data/jurisdictions'

// The three account forms for a SeqPal ID: create a new enclave key, unlock the
// one stored in this browser, or import a backup file. Registration is
// role-agnostic: it never asks whether you intend to issue or invest. The real
// eligibility verification (document review + sanctions screen) is a separate
// step on /id/register once the identity exists.

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

/* ─────────────────────────── Create ─────────────────────────── */

export function CreateId({ onDone }) {
  const { prepareId, registerWithKey } = useStore()
  const [form, setForm] = useState({
    name: '',
    residence: 'AE',
    accredited: false,
    passphrase: '',
    confirm: '',
  })
  const [phase, setPhase] = useState('form') // form | sealing | export | registering
  const [pending, setPending] = useState(null) // { priv, envelope }
  const [exported, setExported] = useState(false)
  const [err, setErr] = useState(null)

  const res = RESIDENCE_OPTIONS.find((r) => r.code === form.residence)
  const docVerify = ['US', 'CA'].includes(form.residence)
  const strength = passphraseStrength(form.passphrase)
  const mismatch = form.confirm.length > 0 && form.confirm !== form.passphrase
  const canSeal =
    form.name.trim().length > 1 &&
    strength.level !== 'weak' &&
    form.confirm === form.passphrase

  const seal = async (e) => {
    e.preventDefault()
    setErr(null)
    setPhase('sealing')
    try {
      setPending(await prepareId(form.passphrase))
      setPhase('export')
    } catch (e2) {
      setErr(e2.message)
      setPhase('form')
    }
  }

  const finish = async () => {
    setErr(null)
    setPhase('registering')
    try {
      await registerWithKey({
        priv: pending.priv,
        envelope: pending.envelope,
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
      setPhase('export')
    }
  }

  if (phase === 'export' || phase === 'registering') {
    const env = pending.envelope
    return (
      <div className="space-y-4">
        <div className="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3.5 text-sm text-amber-900">
          <div className="flex items-center gap-2 font-semibold">
            <Icon.lock width={16} height={16} /> Save your backup before you finish
          </div>
          <p className="mt-1.5 leading-relaxed">
            Your enclave key is generated and encrypted with your passphrase. It is one half
            of the 2-of-2 that every asset you hold sits behind. If this browser is cleared
            and you have no backup, that half is gone for good: assets already minted stay on
            chain without you, and there is no reset and no support recovery.
          </p>
        </div>
        <div className="rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3 text-xs">
          <div className="flex justify-between gap-4">
            <span className="text-ink-700/70">Account id (AID)</span>
            <span className="font-mono text-ink-900">{env.aid}</span>
          </div>
          <div className="mt-1.5 flex justify-between gap-4">
            <span className="text-ink-700/70">Backup file</span>
            <span className="font-mono text-ink-900">{envelopeFilename(env.aid)}</span>
          </div>
        </div>
        <button
          onClick={() => {
            downloadEnvelope(env)
            setExported(true)
          }}
          className={exported ? 'btn-outline w-full' : 'btn-primary w-full'}
        >
          <Icon.upload width={16} height={16} className="rotate-180" />
          {exported ? 'Download again' : 'Download my encrypted backup'}
        </button>
        <ErrorNote>{err}</ErrorNote>
        <button
          onClick={finish}
          disabled={!exported || phase === 'registering'}
          className="btn-primary w-full disabled:opacity-50"
        >
          {phase === 'registering' ? (
            <>
              <Spinner />
              Signing the challenge and registering
            </>
          ) : (
            <>
              Finish and continue to verification
              <Icon.arrowRight width={16} height={16} />
            </>
          )}
        </button>
        {!exported && (
          <p className="text-center text-xs text-ink-700/60">
            Download the backup to continue. No export, no account.
          </p>
        )}
      </div>
    )
  }

  return (
    <form onSubmit={seal} className="space-y-4">
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

      <div className="rounded-xl border border-ink-900/15 p-4">
        <div className="flex items-center gap-2 text-sm font-semibold text-ink-900">
          <Icon.lock width={16} height={16} className="text-btc-600" />
          Passphrase for your enclave key
        </div>
        <p className="mt-1 text-xs leading-relaxed text-ink-700/70">
          Your key is generated here and encrypted with this passphrase before anything is
          stored or exported. SeqPal never sees the key or the passphrase, and neither can be
          recovered.
        </p>
        <div className="mt-3 space-y-3">
          <input
            id="ind-pass"
            type="password"
            className="input"
            placeholder="Passphrase"
            autoComplete="new-password"
            value={form.passphrase}
            onChange={(e) => setForm({ ...form, passphrase: e.target.value })}
          />
          {form.passphrase && (
            <p
              className={`text-xs ${
                strength.level === 'strong'
                  ? 'text-emerald-600'
                  : strength.level === 'fair'
                    ? 'text-amber-700'
                    : 'text-rose-600'
              }`}
            >
              {strength.label}
            </p>
          )}
          <input
            id="ind-pass2"
            type="password"
            className="input"
            placeholder="Repeat passphrase"
            autoComplete="new-password"
            value={form.confirm}
            onChange={(e) => setForm({ ...form, confirm: e.target.value })}
          />
          {mismatch && <p className="text-xs text-rose-600">The passphrases do not match.</p>}
        </div>
      </div>

      <DemoNote>
        Document verification, liveness, and PEP screening are SIMULATED in this build, with
        real states. Sanctions screening runs against the real public lists. Authentication is
        proof that you hold the enclave key, checked by signature on every sign-in.
      </DemoNote>
      <ErrorNote>{err}</ErrorNote>
      <button
        type="submit"
        disabled={!canSeal || phase === 'sealing'}
        className="btn-primary w-full disabled:opacity-50"
      >
        {phase === 'sealing' ? (
          <>
            <Spinner />
            Generating and encrypting your key
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

/* ─────────────────────────── Sign in ─────────────────────────── */

export function UnlockId({ onDone }) {
  const { unlock, envelope } = useStore()
  const [pass, setPass] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)

  const submit = async (e) => {
    e.preventDefault()
    setErr(null)
    setBusy(true)
    try {
      await unlock(pass)
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
          <span className="text-ink-700/70">SeqPal ID in this browser</span>
          <span className="font-mono text-ink-900">
            {envelope.aid.slice(0, 8)}…{envelope.aid.slice(-6)}
          </span>
        </div>
      </div>
      <Field
        id="unlock-pass"
        label="Passphrase"
        hint="Unlocking decrypts your key locally and signs a one-time challenge from the server. The passphrase never leaves this browser."
      >
        <input
          id="unlock-pass"
          type="password"
          className="input"
          autoComplete="current-password"
          value={pass}
          onChange={(e) => setPass(e.target.value)}
        />
      </Field>
      <ErrorNote>{err}</ErrorNote>
      <button disabled={busy || !pass} className="btn-primary w-full disabled:opacity-50">
        {busy ? (
          <>
            <Spinner />
            Signing the challenge
          </>
        ) : (
          <>
            Unlock and sign in
            <Icon.arrowRight width={16} height={16} />
          </>
        )}
      </button>
    </form>
  )
}

/* ─────────────────────────── Import ─────────────────────────── */

export function ImportId({ onDone }) {
  const { importId } = useStore()
  const [file, setFile] = useState(null)
  const [pass, setPass] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)

  const submit = async (e) => {
    e.preventDefault()
    setErr(null)
    setBusy(true)
    try {
      const env = JSON.parse(await file.text())
      await importId(env, pass)
      onDone?.()
    } catch (e2) {
      setErr(e2 instanceof SyntaxError ? 'That file is not a SeqPal ID backup.' : e2.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} className="space-y-4">
      <Field
        id="imp-file"
        label="Backup file"
        hint="The seqpal-id file you downloaded when you created the ID. It is encrypted, so it is useless to anyone without your passphrase."
      >
        <input
          id="imp-file"
          type="file"
          accept="application/json,.json"
          className="input py-2"
          onChange={(e) => setFile(e.target.files?.[0] || null)}
        />
      </Field>
      <Field id="imp-pass" label="Passphrase">
        <input
          id="imp-pass"
          type="password"
          className="input"
          autoComplete="current-password"
          value={pass}
          onChange={(e) => setPass(e.target.value)}
        />
      </Field>
      <ErrorNote>{err}</ErrorNote>
      <button
        disabled={busy || !file || !pass}
        className="btn-primary w-full disabled:opacity-50"
      >
        {busy ? (
          <>
            <Spinner />
            Unlocking and signing in
          </>
        ) : (
          <>
            Import and sign in
            <Icon.arrowRight width={16} height={16} />
          </>
        )}
      </button>
    </form>
  )
}

// The three-tab auth panel, shared by the landing and the register page.
export function AuthPanel({ onDone, initialTab }) {
  const { hasLocalId } = useStore()
  const [tab, setTab] = useState(initialTab || (hasLocalId ? 'unlock' : 'create'))
  return (
    <div className="card p-7">
      <div className="flex gap-1 rounded-xl bg-ink-900/[0.04] p-1">
        {[
          ['create', 'Create an ID'],
          ['unlock', 'Sign in'],
          ['import', 'Import backup'],
        ].map(([k, label]) => (
          <button
            key={k}
            onClick={() => setTab(k)}
            className={`flex-1 rounded-lg px-3 py-2 text-sm font-semibold transition-colors ${
              tab === k ? 'bg-white text-ink-900 shadow-card' : 'text-ink-700 hover:text-ink-900'
            }`}
          >
            {label}
          </button>
        ))}
      </div>
      <div className="mt-6">
        {tab === 'create' && <CreateId onDone={onDone} />}
        {tab === 'unlock' &&
          (hasLocalId ? (
            <UnlockId onDone={onDone} />
          ) : (
            <div className="rounded-xl border border-dashed border-ink-900/20 p-6 text-center text-sm text-ink-700/70">
              No SeqPal ID is stored in this browser. Import your backup file, or create a new
              ID.
            </div>
          ))}
        {tab === 'import' && <ImportId onDone={onDone} />}
      </div>
    </div>
  )
}
