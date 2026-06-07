import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { Icon, StructureIcon } from '../components/icons'
import { SectionHeading, Badge, DemoNote } from '../components/ui'
import Passport from '../components/Passport'
import { useStore, fakeIdNumber, fakeGaid } from '../lib/store'
import { RESIDENCE_OPTIONS } from '../data/jurisdictions'
import { getStructure } from '../data/structures'

const stored = [
  ['Verified identity', 'Passport scan + selfie liveness'],
  ['Residence & tax residency', 'Verified address and jurisdiction'],
  ['Sanctions, PEP & adverse-media', 'Re-screened on a monthly cadence'],
  ['Accreditation status', 'Jurisdiction-aware, self-certified or documented'],
  ['Linked wallets', 'Liquid GAIDs (EVM / Solana on the roadmap)'],
  ['Cryptographic claim envelope', 'Signed claims that token contracts verify'],
]

function UploadBox({ title, hint }) {
  return (
    <div className="rounded-xl border border-dashed border-ink-900/20 p-5 text-center">
      <Icon.upload width={22} height={22} className="mx-auto text-ink-600" />
      <p className="mt-2 text-sm font-medium text-ink-800">{title}</p>
      <p className="mt-1 text-xs text-ink-700/50">{hint}</p>
    </div>
  )
}

function IndividualForm({ onDone }) {
  const { registerIndividual } = useStore()
  const [form, setForm] = useState({ name: '', residence: 'AE', accredited: true })
  const [verifying, setVerifying] = useState(false)

  const res = RESIDENCE_OPTIONS.find((r) => r.code === form.residence)
  // US (Reg D 506(c)) and Canada require the issuer to take reasonable steps to
  // *verify* accredited status — self-certification alone is not sufficient.
  const docVerify = ['US', 'CA'].includes(form.residence)

  const submit = (e) => {
    e.preventDefault()
    setVerifying(true)
    setTimeout(() => {
      registerIndividual({
        name: form.name || 'Jordan Avery',
        jurisdiction: res.name,
        residenceCode: form.residence,
        accredited: form.accredited,
        accreditationBasis: form.accredited ? res.accreditationLabel : null,
        accreditationMethod: form.accredited ? (docVerify ? 'document' : 'self') : null,
        idNumber: fakeIdNumber('SQID-I'),
        gaid: fakeGaid(),
        verifiedAt: new Date().toISOString(),
      })
      setVerifying(false)
      onDone?.()
    }, 1400)
  }

  return (
    <form onSubmit={submit} className="space-y-4">
      <div>
        <label className="label">Full legal name</label>
        <input
          className="input"
          placeholder="Jordan Avery"
          value={form.name}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
        />
      </div>
      <div>
        <label className="label">Country of residence</label>
        <select
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
      </div>
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
              ? 'I will verify my accredited-investor status with documentation'
              : 'I self-certify as a qualified / accredited investor'}
          </span>
          <span className="mt-0.5 block text-xs text-ink-700/70">{res.accreditationLabel}</span>
          {docVerify && (
            <span className="mt-1 block text-xs text-amber-700">
              {form.residence === 'US' ? 'Reg D Rule 506(c)' : 'Local rules'} require the
              issuer to take reasonable steps to verify accredited status — self-certification
              alone is not sufficient.
            </span>
          )}
        </span>
      </label>
      <UploadBox
        title="Passport / national ID + selfie liveness, proof of address"
        hint="Document verification is mocked in this demo"
      />
      <DemoNote>
        The live platform runs document verification, liveness, sanctions and PEP
        screening through a KYC vendor. Here we simulate an instant pass. The $20
        individual ID fee is not charged.
      </DemoNote>
      <button type="submit" disabled={verifying} className="btn-primary w-full">
        {verifying ? (
          <>
            <span className="h-4 w-4 animate-spin rounded-full border-2 border-white/40 border-t-white" />
            Running verification…
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

function CorporateForm({ onAdded }) {
  const { addCorporate } = useStore()
  const [form, setForm] = useState({ entity: '', jurisdiction: 'United Arab Emirates' })
  const [verifying, setVerifying] = useState(false)

  const submit = (e) => {
    e.preventDefault()
    setVerifying(true)
    setTimeout(() => {
      const corp = {
        id: 'corp_' + Math.random().toString(36).slice(2, 9),
        entity: form.entity || 'Acme Holdings Ltd',
        jurisdiction: form.jurisdiction,
        idNumber: fakeIdNumber('SQID-C'),
        verifiedAt: new Date().toISOString(),
      }
      addCorporate(corp)
      setVerifying(false)
      onAdded?.(corp)
    }, 1400)
  }

  return (
    <form onSubmit={submit} className="space-y-4">
      <div className="grid gap-4 sm:grid-cols-2">
        <div>
          <label className="label">Legal entity name</label>
          <input
            className="input"
            placeholder="Acme Holdings Ltd"
            value={form.entity}
            onChange={(e) => setForm({ ...form, entity: e.target.value })}
          />
        </div>
        <div>
          <label className="label">Jurisdiction of formation</label>
          <select
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
        </div>
      </div>
      <UploadBox
        title="UBO & director passports, proof of address, W-8/W-9"
        hint="The corporate ID is cryptographically linked to its UBOs' individual IDs"
      />
      <DemoNote>
        KYB verification is mocked. The $150 corporate ID fee (plus $20 per
        natural-person UBO) is not charged in the demo.
      </DemoNote>
      <button type="submit" disabled={verifying} className="btn-primary w-full">
        {verifying ? (
          <>
            <span className="h-4 w-4 animate-spin rounded-full border-2 border-white/40 border-t-white" />
            Running KYB verification…
          </>
        ) : (
          <>
            Add corporate SeqPal ID
            <Icon.arrowRight width={16} height={16} />
          </>
        )}
      </button>
    </form>
  )
}

function AccessibleOfferings({ individual, issuances }) {
  const eligible = issuances
    .filter((i) => i.portal?.published)
    .filter((i) => {
      const tier = i.policy?.[individual.residenceCode]
      return tier === 'standard' || (tier === 'restricted' && individual.accredited)
    })

  return (
    <div className="mt-12 border-t border-ink-900/10 pt-10">
      <h3 className="text-lg font-bold text-ink-900">Offerings you can access</h3>
      <p className="mt-1 max-w-2xl text-sm text-ink-700/70">
        Because your SeqPal ID is verified, you’re auto-whitelisted for every SeqPal-issued
        asset your profile is eligible to hold — the marginal cost of the next one is zero.
      </p>
      {eligible.length === 0 ? (
        <div className="mt-5 rounded-xl border border-dashed border-ink-900/20 p-6 text-center text-sm text-ink-700/70">
          No live offerings you can access yet. Published offerings you’re eligible for
          appear here automatically.
        </div>
      ) : (
        <div className="mt-5 grid gap-4 sm:grid-cols-2">
          {eligible.map((i) => {
            const s = getStructure(i.structureId)
            const Ic = StructureIcon[s?.icon] || Icon.layers
            return (
              <Link
                key={i.id}
                to={`/portal/${i.id}`}
                className="card flex items-center gap-4 p-5 transition-all hover:-translate-y-0.5 hover:shadow-glow"
              >
                <div
                  className={`flex h-11 w-11 shrink-0 items-center justify-center rounded-xl ${
                    s?.accent === 'liquid'
                      ? 'bg-liquid/10 text-liquid-600'
                      : 'bg-btc-50 text-btc-600'
                  }`}
                >
                  <Ic width={22} height={22} />
                </div>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="truncate font-bold text-ink-900">{i.name}</span>
                    <span className="font-mono text-xs text-ink-700/60">{i.ticker}</span>
                  </div>
                  <div className="text-sm text-ink-700/70">{s?.name}</div>
                </div>
                <Icon.arrowRight width={18} height={18} className="text-ink-600" />
              </Link>
            )
          })}
        </div>
      )}
      <DemoNote className="mt-5">
        In the demo these are offerings created in this browser. On the live platform this
        is every eligible SeqPal-issued asset across all issuers.
      </DemoNote>
    </div>
  )
}

export default function SeqPalId() {
  const { account, isLoggedIn, signOut, issuances } = useStore()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const next = params.get('next')
  const [addingCorp, setAddingCorp] = useState(false)

  const afterLogin = () => {
    if (next) navigate(next)
  }

  return (
    <>
      <section className="border-b border-ink-900/10 bg-ink-900/[0.02]">
        <div className="container-x py-16">
          <SectionHeading
            eyebrow="SeqPal ID"
            title={isLoggedIn ? 'Your SeqPal ID' : 'One passport. Every asset you qualify for.'}
            sub={
              isLoggedIn
                ? 'Your identity profile and any linked corporate entities. Sign in is your SeqPal ID — it gates the issuer dashboard and every SeqPal-issued asset.'
                : 'Create your SeqPal ID once. From then on you’re automatically whitelisted for every SeqPal-issued asset your profile is eligible to hold — and it’s how you sign in to issue.'
            }
          />
          {next && !isLoggedIn && (
            <DemoNote className="mt-6 max-w-xl">
              You need a SeqPal ID to continue. Create one below and we’ll take you back to
              where you left off.
            </DemoNote>
          )}
        </div>
      </section>

      <section className="container-x py-16">
        {!isLoggedIn ? (
          <div className="grid gap-12 lg:grid-cols-2 lg:items-start">
            <div>
              <h3 className="text-lg font-bold text-ink-900">What your profile stores</h3>
              <div className="mt-5 space-y-3">
                {stored.map(([t, d]) => (
                  <div key={t} className="flex items-start gap-3">
                    <span className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-lg bg-btc-50 text-btc-600">
                      <Icon.check width={14} height={14} />
                    </span>
                    <div>
                      <div className="font-medium text-ink-900">{t}</div>
                      <div className="text-sm text-ink-700/70">{d}</div>
                    </div>
                  </div>
                ))}
              </div>
              <p className="mt-6 text-sm leading-relaxed text-ink-700/70">
                SeqPal is the data controller for your SeqPal ID record; KYC and screening
                vendors act only as processors. Personal data is handled under the
                applicable data-protection regime (GDPR / UK GDPR and equivalents).
              </p>
            </div>
            <div className="card p-7">
              <div className="flex items-center gap-2 text-sm font-semibold text-ink-800">
                <Icon.id width={18} height={18} className="text-btc-600" />
                Create your individual SeqPal ID
              </div>
              <p className="mt-1 text-sm text-ink-700/70">
                This is your login. You can add corporate entities (KYB) afterwards.
              </p>
              <div className="mt-5">
                <IndividualForm onDone={afterLogin} />
              </div>
            </div>
          </div>
        ) : (
          <>
          <div className="grid gap-10 lg:grid-cols-[1fr_1.1fr] lg:items-start">
            {/* Individual passport */}
            <div className="space-y-5">
              <Passport profile={account.individual} type="individual" />
              <div className="flex flex-wrap gap-3">
                <Link to="/onboarding" className="btn-primary">
                  Start an issuance
                  <Icon.arrowRight width={16} height={16} />
                </Link>
                <Link to="/dashboard" className="btn-outline">
                  Dashboard
                </Link>
                <button onClick={signOut} className="btn-ghost text-ink-700/70">
                  Sign out
                </button>
              </div>
            </div>

            {/* Corporate IDs */}
            <div>
              <div className="flex items-center justify-between">
                <h3 className="text-lg font-bold text-ink-900">
                  Linked corporate IDs (KYB)
                </h3>
                {!addingCorp && (
                  <button
                    onClick={() => setAddingCorp(true)}
                    className="btn-outline px-3 py-1.5 text-xs"
                  >
                    <Icon.spark width={14} height={14} /> Add entity
                  </button>
                )}
              </div>
              <p className="mt-1 text-sm text-ink-700/70">
                Required to issue Equity SPV, Debt / Yield, and Depository Receipt tokens —
                where an existing entity is the principal of the offering. Native Equity
                needs only your individual ID.
              </p>

              <div className="mt-5 space-y-4">
                {account.corporates.length === 0 && !addingCorp && (
                  <div className="rounded-xl border border-dashed border-ink-900/20 p-6 text-center text-sm text-ink-700/70">
                    No corporate IDs yet. Add one when you’re ready to issue on behalf of
                    an entity.
                  </div>
                )}
                {account.corporates.map((c) => (
                  <Passport key={c.id} profile={c} type="corporate" />
                ))}
                {addingCorp && (
                  <div className="card p-6">
                    <div className="mb-4 flex items-center justify-between">
                      <span className="text-sm font-semibold text-ink-900">
                        New corporate SeqPal ID
                      </span>
                      <button
                        onClick={() => setAddingCorp(false)}
                        aria-label="Cancel"
                        className="text-ink-600 hover:text-ink-900"
                      >
                        <Icon.close width={18} height={18} />
                      </button>
                    </div>
                    <CorporateForm onAdded={() => setAddingCorp(false)} />
                  </div>
                )}
              </div>
            </div>
          </div>
          <AccessibleOfferings individual={account.individual} issuances={issuances} />
          </>
        )}
      </section>
    </>
  )
}
