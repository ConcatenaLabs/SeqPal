import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { Icon } from '../../components/icons'
import { SectionHeading, DemoNote } from '../../components/ui'
import { AuthPanel, Field, ErrorNote, Spinner } from '../../components/IdAuth'
import { useStore } from '../../lib/store'
import { idVerify, idPassport } from '../../lib/api'
import VerificationFeeCard from '../../components/VerificationFeeCard'
import { RESIDENCE_OPTIONS } from '../../data/jurisdictions'

// sha256 hex of a file, computed in the browser. The accreditation artifact
// content is simulated, but its hash and validity window are real: it is what
// gates the j:US:acc category.
async function fileSha256(file) {
  const buf = await file.arrayBuffer()
  const digest = await crypto.subtle.digest('SHA-256', buf)
  return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, '0')).join('')
}

const LIST_LABELS = {
  ofac_sdn: 'OFAC SDN',
  ofac_consolidated: 'OFAC consolidated',
  eu_consolidated: 'EU consolidated',
  un_sc: 'UN Security Council',
}


/* ─────────────────────────── Verify step ─────────────────────────── */

function VerifyStep({ account, onVerified }) {
  const [form, setForm] = useState({
    residence: account?.profile?.residence_code || account?.profile?.residence || 'AE',
    base_eligibility: 'ret',
    accredited: false,
    citizenship: account?.profile?.residence_code || 'AE',
    us_person: false,
    gb_hnw: false,
    gb_soph: false,
    tax_residencies: '',
    screening_name: '',
  })
  const [artifact, setArtifact] = useState(null) // File
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  // The provider bills per check, so the check is bought before it is submitted.
  const [feePaid, setFeePaid] = useState(false)
  const [result, setResult] = useState(null) // { status, categories, valid_until, screening, message }
  const pollRef = useRef(null)

  const isGB = form.residence === 'GB'
  const usDerived = form.residence === 'US' || form.citizenship === 'US'
  const needsArtifact = form.accredited && form.residence === 'US'

  useEffect(() => {
    return () => clearInterval(pollRef.current)
  }, [])

  // The provider decides in their own time, so the submission returns nothing
  // decided and the outcome arrives on their callback. This polls the passport
  // for it, which is what the holder is waiting on.
  const startPolling = () => {
    clearInterval(pollRef.current)
    let tries = 0
    pollRef.current = setInterval(async () => {
      tries += 1
      try {
        const p = await idPassport()
        if (p.status === 'verified') {
          clearInterval(pollRef.current)
          setResult({
            status: 'verified',
            categories: p.categories?.map((c) => c.token) || [],
            valid_until: p.valid_until,
            verification: p.verification,
          })
          onVerified?.()
        } else if (p.status === 'refused') {
          clearInterval(pollRef.current)
          setResult((r) => ({
            ...r,
            status: 'refused',
            verification: p.verification,
            message: p.verification?.reason || 'The verification provider refused this identity.',
          }))
        } else if (p.status === 'needs_info') {
          clearInterval(pollRef.current)
          setResult((r) => ({
            ...r,
            status: 'needs_info',
            verification: p.verification,
            message: p.verification?.reason || 'The verification provider needs more from you.',
          }))
        }
      } catch {
        // transient; keep polling
      }
      // Long enough to outlast a decision that had to be chased rather than
      // delivered: the provider is asked again on a cadence, and giving up
      // before that would leave a decided identity looking undecided.
      if (tries >= 45) clearInterval(pollRef.current)
    }, 4000)
  }

  const submit = async (e) => {
    e.preventDefault()
    setErr(null)
    setBusy(true)
    try {
      let accred_artifact = ''
      if (form.accredited && artifact) accred_artifact = await fileSha256(artifact)
      if (needsArtifact && !accred_artifact)
        throw new Error('Accreditation in the United States (Reg D 506(c)) requires an uploaded verification document.')
      const tax = form.tax_residencies
        .split(',')
        .map((s) => s.trim().toUpperCase())
        .filter(Boolean)
      const body = {
        residence: form.residence,
        base_eligibility: form.base_eligibility,
        accredited: form.accredited,
        accred_artifact,
        us_person: form.us_person || usDerived,
        citizenship: form.citizenship,
        gb_hnw: isGB && form.gb_hnw,
        gb_soph: isGB && form.gb_soph,
        tax_residencies: tax,
        screening_name: form.screening_name.trim() || undefined,
      }
      const res = await idVerify(body)
      setResult(res)
      // Verification is the provider's, and providers are asynchronous: the
      // submission returns nothing decided, and the outcome arrives on their
      // callback. This waits for it the way the holder does.
      if (res.status === 'verified') onVerified?.()
      if (res.status === 'submitted') startPolling()
    } catch (e2) {
      setErr(e2.message)
    } finally {
      setBusy(false)
    }
  }

  if (result) {
    const s = result.status
    if (s === 'verified') {
      return (
        <div className="space-y-4">
          <div className="flex items-center gap-3">
            <span className="flex h-11 w-11 items-center justify-center rounded-full bg-emerald-500 text-white">
              <Icon.check width={24} height={24} />
            </span>
            <div>
              <h3 className="font-bold text-ink-900">Verified</h3>
              <p className="text-sm text-ink-700/80">
                Your eligibility categories are recorded on your SeqPal ID. Where a token's
                transfers are serviced here, the policy server now enforces them on every one.
              </p>
            </div>
          </div>
          {result.categories?.length > 0 && (
            <div className="flex flex-wrap gap-1.5">
              {result.categories.map((c) => (
                <span key={c} className="rounded-md bg-btc-50 px-2 py-1 font-mono text-xs font-semibold text-btc-700">
                  {c}
                </span>
              ))}
            </div>
          )}
          <Link to="/id/passport" className="btn-primary w-full">
            View my passport
            <Icon.arrowRight width={16} height={16} />
          </Link>
        </div>
      )
    }
    if (s === 'submitted') {
      return (
        <div className="space-y-4">
          <div className="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3.5 text-sm text-amber-900">
            <div className="flex items-center gap-2 font-semibold">
              <Icon.shield width={16} height={16} /> With the verification provider
            </div>
            <p className="mt-1.5 leading-relaxed">
              Your details are with{' '}
              {result.provider ? (
                <span className="font-medium">{result.provider}</span>
              ) : (
                'our identity-verification provider'
              )}
              , who checks the documents and the watchlists. Nothing is granted until they
              answer, and this page updates when they do.
            </p>
          </div>
          <div className="flex items-center gap-2 text-sm text-ink-700/70">
            <Spinner /> Waiting for their decision
          </div>
        </div>
      )
    }
    if (s === 'needs_info') {
      return (
        <div className="space-y-4">
          <div className="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3.5 text-sm text-amber-900">
            <div className="font-semibold">More information needed</div>
            <p className="mt-1.5 leading-relaxed">
              {result.message || 'The review needs additional information to continue (SIMULATED review).'}
            </p>
          </div>
          <button onClick={() => setResult(null)} className="btn-outline w-full">
            Update my details and resubmit
          </button>
        </div>
      )
    }
    // refused
    return (
      <div className="space-y-4">
        <div className="rounded-xl border border-rose-200 bg-rose-50 px-4 py-3.5 text-sm text-rose-800">
          <div className="font-semibold">Verification refused</div>
          <p className="mt-1.5 leading-relaxed">
            {result.message ||
              'The verification provider refused this identity. No eligibility was granted.'}
          </p>
        </div>
      </div>
    )
  }

  return (
    <form onSubmit={submit} className="space-y-4">
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id="v-residence" label="Country of residence">
          <select
            id="v-residence"
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
        <Field id="v-citizenship" label="Citizenship">
          <select
            id="v-citizenship"
            className="select"
            value={form.citizenship}
            onChange={(e) => setForm({ ...form, citizenship: e.target.value })}
          >
            {RESIDENCE_OPTIONS.map((r) => (
              <option key={r.code} value={r.code}>
                {r.name}
              </option>
            ))}
          </select>
        </Field>
      </div>

      <Field
        id="v-base"
        label="Investor classification"
        hint="Retail is the default. Professional and institutional clients carry the pro category."
      >
        <div className="flex gap-2">
          {[
            ['ret', 'Retail'],
            ['pro', 'Professional'],
          ].map(([v, label]) => (
            <button
              key={v}
              type="button"
              onClick={() => setForm({ ...form, base_eligibility: v })}
              className={`flex-1 rounded-lg border px-4 py-2.5 text-sm font-semibold transition-colors ${
                form.base_eligibility === v
                  ? 'border-btc bg-btc-50 text-btc-700'
                  : 'border-ink-900/15 text-ink-700 hover:bg-ink-900/[0.02]'
              }`}
            >
              {label}
            </button>
          ))}
        </div>
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
            I am a qualified / accredited investor
          </span>
          <span className="mt-0.5 block text-xs text-ink-700/70">
            Accreditation defaults off and adds the acc category. In the United States it
            requires an uploaded verification document (Reg D Rule 506(c)); the document is
            simulated but its hash and 90-day validity are real.
          </span>
        </span>
      </label>

      {form.accredited && (
        <Field
          id="v-artifact"
          label={needsArtifact ? 'Accreditation document (required)' : 'Accreditation document (optional)'}
          hint="Hashed in your browser. Only the hash and validity window are recorded."
        >
          <input
            id="v-artifact"
            type="file"
            className="input py-2"
            onChange={(e) => setArtifact(e.target.files?.[0] || null)}
          />
        </Field>
      )}

      {isGB && (
        <div className="rounded-xl border border-ink-900/15 p-4">
          <div className="text-sm font-semibold text-ink-900">UK statutory investor statement</div>
          <div className="mt-3 space-y-2">
            <label className="flex cursor-pointer items-center gap-3 text-sm">
              <input
                type="checkbox"
                checked={form.gb_hnw}
                onChange={(e) => setForm({ ...form, gb_hnw: e.target.checked })}
                className="h-4 w-4 accent-btc"
              />
              <span>High-net-worth individual (FSMA s.86)</span>
            </label>
            <label className="flex cursor-pointer items-center gap-3 text-sm">
              <input
                type="checkbox"
                checked={form.gb_soph}
                onChange={(e) => setForm({ ...form, gb_soph: e.target.checked })}
                className="h-4 w-4 accent-btc"
              />
              <span>Self-certified sophisticated investor</span>
            </label>
          </div>
        </div>
      )}

      <label className="flex cursor-pointer items-start gap-3 rounded-xl border border-ink-900/15 p-4">
        <input
          type="checkbox"
          checked={form.us_person || usDerived}
          disabled={usDerived}
          onChange={(e) => setForm({ ...form, us_person: e.target.checked })}
          className="mt-0.5 h-4 w-4 accent-btc"
        />
        <span className="text-sm">
          <span className="font-medium text-ink-900">I am a US person (17 CFR 230.902(k))</span>
          <span className="mt-0.5 block text-xs text-ink-700/70">
            {usDerived
              ? 'Determined from your residence or citizenship. A US person carries j:US tokens and is bound by Reg S windows even when resident elsewhere.'
              : 'Check this if you are a US person by any other determination.'}
          </span>
        </span>
      </label>

      <Field
        id="v-tax"
        label="Tax residencies (CRS / FATCA)"
        hint="Comma-separated country codes, e.g. AE, GB. Self-certified."
      >
        <input
          id="v-tax"
          className="input"
          placeholder="AE, GB"
          value={form.tax_residencies}
          onChange={(e) => setForm({ ...form, tax_residencies: e.target.value })}
        />
      </Field>

      <Field
        id="v-name"
        label="Name to screen (optional)"
        hint="Defaults to your registered legal name. Override only to correct it."
      >
        <input
          id="v-name"
          className="input"
          placeholder={account?.display_name || 'Registered legal name'}
          value={form.screening_name}
          onChange={(e) => setForm({ ...form, screening_name: e.target.value })}
        />
      </Field>

      <VerificationFeeCard onPaid={() => setFeePaid(true)} />

      <DemoNote>
        Verification is run by an independent provider. On this demo that provider is SIMULATED,
        behind the same interface a real one plugs into: the check is submitted, and the outcome --
        approved, refused, or more information needed -- arrives asynchronously on their callback.
      </DemoNote>
      <ErrorNote>{err}</ErrorNote>
      <button
        type="submit"
        disabled={busy || !feePaid}
        className="btn-primary w-full disabled:opacity-50"
      >
        {busy ? (
          <>
            <Spinner />
            Submitting to the provider
          </>
        ) : (
          <>
            {feePaid ? 'Verify my identity' : 'Pay the verification fee to continue'}
            <Icon.arrowRight width={16} height={16} />
          </>
        )}
      </button>
    </form>
  )
}

/* ─────────────────────────── Page ─────────────────────────── */

export default function IdRegister() {
  const { loading, isSignedIn, account } = useStore()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const next = params.get('next')
  const [status, setStatus] = useState(null) // passport status once signed in

  useEffect(() => {
    if (!isSignedIn) return
    let cancelled = false
    idPassport()
      .then((p) => !cancelled && setStatus(p.status))
      .catch(() => !cancelled && setStatus('unverified'))
    return () => {
      cancelled = true
    }
  }, [isSignedIn])

  const afterVerified = () => {
    setStatus('verified')
  }

  return (
    <>
      <section className="border-b border-ink-900/10 bg-ink-900/[0.02]">
        <div className="container-x py-14">
          <SectionHeading
            eyebrow="SeqPal ID"
            title={isSignedIn ? 'Verify your identity' : 'Create or sign in with your SeqPal ID'}
            sub={
              isSignedIn
                ? 'One flow for everyone. Verification runs a labeled-simulated document review and a real sanctions screen, then stamps the eligibility categories the policy server enforces.'
                : 'One registration flow, identical for everybody. It never asks whether you intend to issue or invest.'
            }
          />
        </div>
      </section>

      <section className="container-x py-14">
        {loading ? (
          <div className="flex justify-center py-12">
            <span className="h-8 w-8 animate-spin rounded-full border-4 border-btc/20 border-t-btc" />
          </div>
        ) : !isSignedIn ? (
          <div className="mx-auto max-w-lg">
            {/* If the user arrived from a gated page, signing in returns them
                there; otherwise they stay and continue to verification. */}
            <AuthPanel onDone={() => next && navigate(next)} />
            <p className="mt-4 text-center text-sm text-ink-700/70">
              Already have a SeqPal ID? Sign in with the same Sequentia wallet and you are back
              in the same account, on any device.
            </p>
          </div>
        ) : (
          <div className="mx-auto max-w-lg">
            {next && (
              <button
                onClick={() => navigate(next)}
                className="mb-5 flex w-full items-center justify-center gap-2 rounded-xl border border-btc/20 bg-btc-50 px-4 py-3 text-sm font-semibold text-btc-700 hover:bg-btc-100"
              >
                Continue to where you left off
                <Icon.arrowRight width={16} height={16} />
              </button>
            )}
            {status === 'verified' ? (
              <div className="card p-7 text-center">
                <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-emerald-500 text-white">
                  <Icon.check width={26} height={26} />
                </div>
                <h3 className="mt-4 text-lg font-bold text-ink-900">Your identity is verified</h3>
                <p className="mt-2 text-sm text-ink-700/80">
                  Your eligibility categories are recorded on your SeqPal ID. You can view them on your
                  passport.
                </p>
                <div className="mt-5 flex flex-col gap-2">
                  {next ? (
                    <button onClick={() => navigate(next)} className="btn-primary w-full">
                      Continue
                      <Icon.arrowRight width={16} height={16} />
                    </button>
                  ) : (
                    <Link to="/id/passport" className="btn-primary w-full">
                      View my passport
                      <Icon.arrowRight width={16} height={16} />
                    </Link>
                  )}
                  <button onClick={() => setStatus('unverified')} className="btn-ghost w-full text-ink-700">
                    Re-run verification
                  </button>
                </div>
              </div>
            ) : (
              <div className="card p-7">
                <div className="mb-5 flex items-center gap-3">
                  <span className="flex h-10 w-10 items-center justify-center rounded-xl bg-btc-50 text-btc-600">
                    <Icon.id width={20} height={20} />
                  </span>
                  <div className="min-w-0">
                    <div className="text-sm font-semibold text-ink-900">
                      {account.display_name}
                    </div>
                    <div className="truncate font-mono text-xs text-ink-700/60">{account.aid}</div>
                  </div>
                </div>
                <VerifyStep account={account} onVerified={afterVerified} />
              </div>
            )}
          </div>
        )}
      </section>
    </>
  )
}
