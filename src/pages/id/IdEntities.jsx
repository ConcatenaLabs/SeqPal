import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Icon } from '../../components/icons'
import { Badge, DemoNote } from '../../components/ui'
import SignInGate from '../../components/SignInGate'
import { Field, ErrorNote, Spinner } from '../../components/IdAuth'
import { useStore } from '../../lib/store'
import { verifyEntity, idPassport } from '../../lib/api'
import { RESIDENCE_OPTIONS } from '../../data/jurisdictions'

const UBO_TAG = 'seqpal-ubo-v1'

/* ─────────────────────────── Add entity ─────────────────────────── */

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
        <button type="button" onClick={onCancel} aria-label="Cancel" className="text-ink-600 hover:text-ink-900">
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
            {RESIDENCE_OPTIONS.map((r) => (
              <option key={r.code}>{r.name}</option>
            ))}
          </select>
        </Field>
      </div>
      <DemoNote className="mt-4">
        KYB verification is SIMULATED. The entity is recorded against your SeqPal ID. Verifying
        it provisions a treasury enclave with its own key, so assets issued for the company are
        held by the company, not by your personal AID.
      </DemoNote>
      <ErrorNote>{err}</ErrorNote>
      <button disabled={busy || !form.name.trim()} className="btn-primary mt-4 w-full disabled:opacity-50">
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

/* ─────────────────────────── Entity card ─────────────────────────── */

function EntityCard({ entity, link, onChanged }) {
  const { signWithKey } = useStore()
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  // Local view of the link, seeded from the passport and updated as we act.
  const [state, setState] = useState(link || null)

  const verified = !!state?.treasury_aid
  const uboSigned = !!state?.ubo_signed

  // First verification: provisions the treasury enclave and records the link.
  const verify = async () => {
    setErr(null)
    setBusy(true)
    try {
      const res = await verifyEntity(entity.id, {})
      setState({
        treasury_aid: res.treasury_aid,
        ubo_signed: !!res.ubo_link?.sig,
        statement: res.ubo_link?.statement,
      })
      onChanged?.()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  // Sign the UBO declaration with the personal enclave key, then re-submit so
  // seqpald records the signature. The statement names the treasury AID, which
  // only exists after the first verify, so signing is a second step.
  const signUbo = async () => {
    setErr(null)
    setBusy(true)
    try {
      // The statement names the treasury AID; if it is not in local state (e.g.
      // after a reload) re-run the idempotent verify to obtain it.
      let statement = state?.statement
      if (!statement) {
        const seed = await verifyEntity(entity.id, {})
        statement = seed.ubo_link?.statement
      }
      if (!statement) throw new Error('Could not obtain the UBO declaration for this entity.')
      const sig = signWithKey(UBO_TAG, statement)
      if (!sig)
        throw new Error('Your enclave key is locked. Sign in again to unlock it, then sign the declaration.')
      const res = await verifyEntity(entity.id, { ubo_sig: sig })
      setState({
        treasury_aid: res.treasury_aid,
        ubo_signed: !!res.ubo_link?.sig,
        statement: res.ubo_link?.statement,
      })
      onChanged?.()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="card p-6">
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-start gap-3">
          <span className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl bg-seq/10 text-seq-600">
            <Icon.building width={22} height={22} />
          </span>
          <div className="min-w-0">
            <div className="font-bold text-ink-900">{entity.name}</div>
            <div className="text-sm text-ink-700/70">{entity.jurisdiction}</div>
          </div>
        </div>
        {verified ? (
          <Badge color="emerald">
            <Icon.check width={12} height={12} /> Verified
          </Badge>
        ) : (
          <Badge color="slate">Unverified</Badge>
        )}
      </div>

      {verified && (
        <div className="mt-4 space-y-1.5 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3 text-xs">
          <div className="flex justify-between gap-4">
            <span className="text-ink-700/70">Treasury enclave (AID)</span>
            <span className="break-all text-right font-mono text-ink-900">{state.treasury_aid}</span>
          </div>
          <div className="flex justify-between gap-4">
            <span className="text-ink-700/70">UBO declaration</span>
            <span className={uboSigned ? 'font-medium text-emerald-700' : 'text-ink-700/70'}>
              {uboSigned ? 'Signed by your key' : 'Not yet signed'}
            </span>
          </div>
        </div>
      )}

      <ErrorNote>{err}</ErrorNote>

      <div className="mt-4 flex flex-wrap gap-2">
        {!verified ? (
          <button onClick={verify} disabled={busy} className="btn-primary disabled:opacity-50">
            {busy ? (
              <>
                <span className="h-4 w-4 animate-spin rounded-full border-2 border-white/40 border-t-white" />
                Provisioning treasury
              </>
            ) : (
              <>
                Verify company (KYB)
                <Icon.arrowRight width={16} height={16} />
              </>
            )}
          </button>
        ) : !uboSigned ? (
          <button onClick={signUbo} disabled={busy} className="btn-outline disabled:opacity-50">
            {busy ? (
              <>
                <span className="h-4 w-4 animate-spin rounded-full border-2 border-ink-900/20 border-t-ink-700" />
                Signing UBO declaration
              </>
            ) : (
              <>
                <Icon.lock width={16} height={16} /> Sign UBO declaration
              </>
            )}
          </button>
        ) : (
          <span className="inline-flex items-center gap-1.5 text-sm text-emerald-700">
            <Icon.check width={16} height={16} /> Ownership link complete
          </span>
        )}
      </div>
    </div>
  )
}

/* ─────────────────────────── Page ─────────────────────────── */

export default function IdEntities() {
  const { loading, isSignedIn, entities } = useStore()
  const [links, setLinks] = useState({}) // entity_id -> { treasury_aid, ubo_signed }
  const [adding, setAdding] = useState(false)

  const loadLinks = () => {
    idPassport()
      .then((p) => {
        const map = {}
        for (const e of p.entities || []) map[e.id] = e
        setLinks(map)
      })
      .catch(() => {})
  }

  useEffect(() => {
    if (isSignedIn) loadLinks()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isSignedIn])

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
        title="Sign in to manage your companies"
        body="A corporate (KYB) entity is an addition to your personal SeqPal ID. Sign in with your ID to add or verify one."
        backTo="/id"
        backLabel="SeqPal ID home"
      />
    )
  }

  return (
    <section className="container-x py-12">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-3xl font-extrabold tracking-tight text-ink-900">Companies</h1>
          <p className="mt-1 max-w-2xl text-ink-700/80">
            Corporate (KYB) entities you represent, added to your personal SeqPal ID. An entity
            is always an addition to your ID, never a separate identity you sign in with.
          </p>
        </div>
        <div className="flex gap-2">
          <Link to="/id/passport" className="btn-outline">
            <Icon.id width={16} height={16} /> Passport
          </Link>
          {!adding && (
            <button onClick={() => setAdding(true)} className="btn-primary">
              <Icon.spark width={16} height={16} /> Add company
            </button>
          )}
        </div>
      </div>

      <div className="mt-8 space-y-4">
        {adding && (
          <EntityForm
            onAdded={() => {
              setAdding(false)
              loadLinks()
            }}
            onCancel={() => setAdding(false)}
          />
        )}
        {entities.length === 0 && !adding && (
          <div className="card flex flex-col items-center justify-center px-6 py-16 text-center">
            <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-ink-900/[0.04] text-ink-600">
              <Icon.building width={28} height={28} />
            </div>
            <h3 className="mt-5 text-lg font-bold text-ink-900">No companies yet</h3>
            <p className="mt-2 max-w-md text-sm leading-relaxed text-ink-700/80">
              Add a company to hold or issue SeqPal-managed assets on behalf of an entity you
              represent. Verifying it provisions a treasury enclave and links it to you as the
              beneficial owner.
            </p>
            <button onClick={() => setAdding(true)} className="btn-primary mt-6">
              <Icon.spark width={16} height={16} /> Add a company
            </button>
          </div>
        )}
        {entities.map((e) => (
          <EntityCard key={e.id} entity={e} link={links[e.id]} onChanged={loadLinks} />
        ))}
      </div>
    </section>
  )
}
