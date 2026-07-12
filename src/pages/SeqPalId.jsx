import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { Icon } from '../components/icons'
import { SectionHeading, DemoNote } from '../components/ui'
import Passport from '../components/Passport'
import { useStore, downloadEnvelope, envelopeFilename } from '../lib/store'
import { passphraseStrength } from '../lib/keys'
import { RESIDENCE_OPTIONS } from '../data/jurisdictions'

// A SeqPal ID is the identity and compliance layer for SeqPal-managed OpenAMP
// assets on Sequentia. One registration flow, the same for everybody: it never
// asks whether you intend to issue or to invest. What it produces is a verified
// identity bound to an enclave key (the AID). What you do with it afterwards is
// up to you.
const unlocks = [
  [
    'Hold and transfer SeqPal-managed assets',
    'A restricted asset only moves between identities the policy server recognises. Without a SeqPal ID you cannot hold one at all.',
  ],
  [
    'Trade them on a Sequentia venue',
    'A venue listing SeqPal-managed assets checks the eligibility SeqPal has stamped on your identity. It cannot grant eligibility of its own.',
  ],
  [
    'Sign in to the issuance platform',
    'If you go on to issue an asset, the same identity is what signs you in and what the issuance is recorded against.',
  ],
  [
    'Add an entity later',
    'A corporate (KYB) entity is added to an existing personal SeqPal ID. It is never a separate kind of signup.',
  ],
]

const stored = [
  ['Verified identity', 'Passport or national ID, plus liveness'],
  ['Residence and tax residency', 'Verified address and jurisdiction'],
  ['Sanctions, PEP and adverse media', 'Screened on a schedule, not once'],
  ['Qualified / accredited status', 'Jurisdiction-aware, self-certified or documented'],
  [
    'Sequentia enclave key',
    'The x-only key OpenAMP registers as your account. Assets you hold live in its 2-of-2 enclave, and one half of that 2-of-2 is the key in your browser.',
  ],
]

function Field({ id, label, children, hint }) {
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

function ErrorNote({ children }) {
  if (!children) return null
  return <p className="rounded-lg bg-rose-50 px-3 py-2 text-sm text-rose-700">{children}</p>
}

function Spinner() {
  return (
    <span className="h-4 w-4 animate-spin rounded-full border-2 border-white/40 border-t-white" />
  )
}

/* ─────────────────────────── Create ─────────────────────────── */

function CreateId({ onDone }) {
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
  // US (Reg D 506(c)) and Canada require the issuer to take reasonable steps to
  // verify qualified status: self-certification alone is not sufficient.
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
              Finish and sign in
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
      <Field id="ind-residence" label="Country of residence">
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
      <label className="flex cursor-pointer items-start gap-3 rounded-xl border border-ink-900/15 p-4">
        <input
          type="checkbox"
          checked={form.accredited}
          onChange={(e) => setForm({ ...form, accredited: e.target.checked })}
          className="mt-0.5 h-4 w-4 accent-btc"
        />
        <span className="text-sm">
          <span className="font-medium text-ink-900">
            {docVerify
              ? 'I will verify my qualified / accredited status with documentation'
              : 'I self-certify as a qualified / accredited investor'}
          </span>
          <span className="mt-0.5 block text-xs text-ink-700/70">
            {res.accreditationLabel}
          </span>
          {docVerify && (
            <span className="mt-1 block text-xs text-amber-700">
              {form.residence === 'US' ? 'Reg D Rule 506(c)' : 'Local rules'} require the
              issuer to take reasonable steps to verify this status. Self-certification alone
              is not sufficient.
            </span>
          )}
        </span>
      </label>

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
        Document verification, liveness, sanctions and PEP screening are SIMULATED in this
        build and pass instantly. They are not what signs you in: authentication is proof
        that you hold the enclave key, checked by signature on every sign-in.
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

function UnlockId({ onDone }) {
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

function ImportId({ onDone }) {
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

/* ─────────────────────── Corporate (KYB) entity ─────────────────────── */

function EntityForm({ onAdded, onCancel }) {
  const { createEntity } = useStore()
  const [form, setForm] = useState({ name: '', jurisdiction: 'United Arab Emirates' })
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)

  const submit = async (e) => {
    e.preventDefault()
    setErr(null)
    setBusy(true)
    try {
      const entity = await createEntity({
        name: form.name.trim(),
        jurisdiction: form.jurisdiction,
        profile: { kyb: 'simulated', verified_at: new Date().toISOString() },
      })
      onAdded?.(entity)
    } catch (e2) {
      setErr(e2.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} className="card p-6">
      <div className="mb-4 flex items-center justify-between">
        <span className="text-sm font-semibold text-ink-900">Add a corporate entity (KYB)</span>
        <button
          type="button"
          onClick={onCancel}
          aria-label="Cancel"
          className="text-ink-600 hover:text-ink-900"
        >
          <Icon.close width={18} height={18} />
        </button>
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id="corp-entity" label="Legal entity name">
          <input
            id="corp-entity"
            className="input"
            placeholder="Acme Holdings Ltd"
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
          />
        </Field>
        <Field id="corp-jur" label="Jurisdiction of formation">
          <select
            id="corp-jur"
            className="select"
            value={form.jurisdiction}
            onChange={(e) => setForm({ ...form, jurisdiction: e.target.value })}
          >
            {[
              'United Arab Emirates',
              'Switzerland',
              'Singapore',
              'Cayman Islands',
              'United States',
              'El Salvador',
              'Honduras',
            ].map((j) => (
              <option key={j}>{j}</option>
            ))}
          </select>
        </Field>
      </div>
      <DemoNote className="mt-4">
        KYB verification is SIMULATED. The entity is recorded against your SeqPal ID on the
        server. It does not yet carry an enclave key of its own, so an asset issued for it is
        still held by your personal AID. Per-entity enclave keys are a later milestone.
      </DemoNote>
      <ErrorNote>{err}</ErrorNote>
      <button
        disabled={busy || !form.name.trim()}
        className="btn-primary mt-4 w-full disabled:opacity-50"
      >
        {busy ? (
          <>
            <Spinner />
            Recording the entity
          </>
        ) : (
          <>
            Add entity
            <Icon.arrowRight width={16} height={16} />
          </>
        )}
      </button>
    </form>
  )
}

/* ─────────────────────────── Page ─────────────────────────── */

export default function SeqPalId() {
  const { loading, isSignedIn, account, entities, issuances, hasLocalId, signOut } = useStore()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const next = params.get('next')
  const [tab, setTab] = useState(hasLocalId ? 'unlock' : 'create')
  const [addingEntity, setAddingEntity] = useState(false)

  const afterAuth = () => {
    if (next) navigate(next)
  }

  const isIssuer = isSignedIn && issuances.length > 0

  return (
    <>
      <section className="border-b border-ink-900/10 bg-ink-900/[0.02]">
        <div className="container-x py-16">
          <SectionHeading
            eyebrow="SeqPal ID"
            title={
              isSignedIn ? 'Your SeqPal ID' : 'One verified identity. Every SeqPal-managed asset.'
            }
            sub={
              isSignedIn
                ? 'Your identity and compliance passport for SeqPal-managed assets on Sequentia, and the entities you have linked to it.'
                : 'SeqPal ID is the identity and compliance layer for SeqPal-managed OpenAMP assets on Sequentia. Anyone who wants to hold, trade, or issue one needs it. One flow, the same for everybody.'
            }
          />
          {next && !isSignedIn && (
            <div className="mt-6 max-w-xl rounded-xl border border-btc/20 bg-btc-50 px-4 py-3 text-sm text-btc-700">
              That page needs a SeqPal ID. Create one or sign in below and we will take you
              back to where you left off.
            </div>
          )}
        </div>
      </section>

      <section className="container-x py-16">
        {loading ? (
          <div className="flex justify-center py-12">
            <span className="h-8 w-8 animate-spin rounded-full border-4 border-btc/20 border-t-btc" />
          </div>
        ) : !isSignedIn ? (
          <div className="grid grid-cols-1 gap-12 lg:grid-cols-2 lg:items-start">
            <div>
              <h3 className="text-lg font-bold text-ink-900">What it unlocks</h3>
              <div className="mt-5 space-y-4">
                {unlocks.map(([t, d]) => (
                  <div key={t} className="flex items-start gap-3">
                    <span className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-lg bg-btc-50 text-btc-600">
                      <Icon.check width={14} height={14} />
                    </span>
                    <div>
                      <div className="font-medium text-ink-900">{t}</div>
                      <div className="text-sm leading-relaxed text-ink-700/70">{d}</div>
                    </div>
                  </div>
                ))}
              </div>

              <h3 className="mt-10 text-lg font-bold text-ink-900">What your profile holds</h3>
              <div className="mt-4 space-y-2.5">
                {stored.map(([t, d]) => (
                  <div key={t} className="flex items-start gap-3">
                    <span className="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-ink-900/30" />
                    <div>
                      <div className="text-sm font-medium text-ink-900">{t}</div>
                      <div className="text-sm leading-relaxed text-ink-700/70">{d}</div>
                    </div>
                  </div>
                ))}
              </div>
              <p className="mt-6 text-sm leading-relaxed text-ink-700/70">
                SeqPal is the data controller for your SeqPal ID record; screening vendors act
                only as processors. Personal data is handled under the applicable
                data-protection regime (GDPR, UK GDPR, and equivalents).
              </p>
            </div>

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
                      tab === k
                        ? 'bg-white text-ink-900 shadow-card'
                        : 'text-ink-700 hover:text-ink-900'
                    }`}
                  >
                    {label}
                  </button>
                ))}
              </div>
              <div className="mt-6">
                {tab === 'create' && <CreateId onDone={afterAuth} />}
                {tab === 'unlock' &&
                  (hasLocalId ? (
                    <UnlockId onDone={afterAuth} />
                  ) : (
                    <div className="rounded-xl border border-dashed border-ink-900/20 p-6 text-center text-sm text-ink-700/70">
                      No SeqPal ID is stored in this browser. Import your backup file, or
                      create a new ID.
                    </div>
                  ))}
                {tab === 'import' && <ImportId onDone={afterAuth} />}
              </div>
            </div>
          </div>
        ) : (
          <div className="grid gap-10 lg:grid-cols-[1fr_1.1fr] lg:items-start">
            <div className="space-y-5">
              <Passport account={account} />
              <div className="flex flex-wrap gap-3">
                {next ? (
                  <button onClick={() => navigate(next)} className="btn-primary">
                    Continue
                    <Icon.arrowRight width={16} height={16} />
                  </button>
                ) : (
                  <Link to="/holdings" className="btn-primary">
                    My holdings
                    <Icon.arrowRight width={16} height={16} />
                  </Link>
                )}
                {isIssuer && (
                  <Link to="/dashboard" className="btn-outline">
                    Issuer dashboard
                  </Link>
                )}
                <button onClick={signOut} className="btn-ghost text-ink-700/70">
                  Sign out
                </button>
              </div>
              <div className="rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3 text-xs leading-relaxed text-ink-700/70">
                Signing out ends the server session and drops the decrypted key from memory.
                The encrypted backup stays in this browser, so you can sign back in with your
                passphrase. Your key is one half of the 2-of-2 enclave that holds your assets:
                keep the backup file you downloaded.
              </div>
            </div>

            <div>
              <div className="flex items-center justify-between">
                <h3 className="text-lg font-bold text-ink-900">Linked entities (KYB)</h3>
                {!addingEntity && (
                  <button
                    onClick={() => setAddingEntity(true)}
                    className="btn-outline px-3 py-1.5 text-xs"
                  >
                    <Icon.spark width={14} height={14} /> Add entity
                  </button>
                )}
              </div>
              <p className="mt-1 text-sm leading-relaxed text-ink-700/70">
                Verify an entity you represent to act as that company. An entity is always an
                addition to your personal SeqPal ID, never a separate identity you sign in
                with.
              </p>

              <div className="mt-5 space-y-4">
                {entities.length === 0 && !addingEntity && (
                  <div className="rounded-xl border border-dashed border-ink-900/20 p-6 text-center text-sm text-ink-700/70">
                    No entities yet. Add one to hold or issue SeqPal-managed assets on behalf
                    of a company you represent.
                  </div>
                )}
                {entities.map((e) => (
                  <Passport key={e.id} entity={e} />
                ))}
                {addingEntity && (
                  <EntityForm
                    onAdded={() => setAddingEntity(false)}
                    onCancel={() => setAddingEntity(false)}
                  />
                )}
              </div>

              <div className="mt-8 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] p-5 text-sm leading-relaxed text-ink-700/75">
                <div className="flex items-center gap-2 font-semibold text-ink-900">
                  <Icon.shield width={16} height={16} className="text-seq-600" />
                  Eligibility categories
                </div>
                <p className="mt-1.5">
                  Your identity is registered and authenticated today. The eligibility
                  categories the policy server enforces on every transfer are stamped onto
                  this AID in a later milestone. Until then this ID identifies you, and it
                  does not yet carry categories a venue or a transfer check can act on.
                </p>
              </div>
            </div>
          </div>
        )}
      </section>
    </>
  )
}
