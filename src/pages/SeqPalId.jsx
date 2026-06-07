import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Icon } from '../components/icons'
import { SectionHeading, Badge, DemoNote } from '../components/ui'
import { useStore } from '../lib/store'

const stored = [
  ['Verified identity', 'Passport scan + selfie liveness'],
  ['Residence & tax residency', 'Verified address and jurisdiction'],
  ['Sanctions, PEP & adverse-media', 'Re-screened on a monthly cadence'],
  ['Accreditation status', 'Jurisdiction-aware, self-certified or documented'],
  ['Linked wallets', 'Liquid GAIDs (EVM / Solana on the roadmap)'],
  ['Cryptographic claim envelope', 'Signed claims that token contracts verify'],
]

function PassportCard({ id }) {
  return (
    <div className="overflow-hidden rounded-2xl border border-ink-800 bg-ink-950 text-white shadow-xl">
      <div className="flex items-center justify-between border-b border-white/10 px-6 py-4">
        <div className="flex items-center gap-2 text-sm font-semibold">
          <Icon.id width={18} height={18} className="text-btc" /> SeqPal ID
        </div>
        <Badge color="emerald">
          <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" /> Verified
        </Badge>
      </div>
      <div className="space-y-4 px-6 py-6">
        <div>
          <div className="text-xs text-white/40">Entity</div>
          <div className="text-lg font-bold">{id.entity}</div>
        </div>
        <div className="grid grid-cols-2 gap-4">
          <div>
            <div className="text-xs text-white/40">Jurisdiction</div>
            <div className="font-semibold">{id.jurisdiction}</div>
          </div>
          <div>
            <div className="text-xs text-white/40">Type</div>
            <div className="font-semibold">Corporate (KYB)</div>
          </div>
        </div>
        <div>
          <div className="text-xs text-white/40">SeqPal ID</div>
          <div className="break-all font-mono text-sm text-liquid-400">{id.idNumber}</div>
        </div>
        <div className="flex items-center gap-2 rounded-lg bg-white/5 px-3 py-2.5 text-sm text-white/70">
          <Icon.shield width={16} height={16} className="text-liquid-400" />
          Sanctions clear · screening active
        </div>
      </div>
    </div>
  )
}

export default function SeqPalId() {
  const { id, setId } = useStore()
  const [form, setForm] = useState({ entity: '', jurisdiction: 'United Arab Emirates' })
  const [verifying, setVerifying] = useState(false)

  const submit = (e) => {
    e.preventDefault()
    setVerifying(true)
    // Mocked KYB verification — no real KYC vendor call in the demo.
    setTimeout(() => {
      setId({
        entity: form.entity || 'Acme Holdings Ltd',
        jurisdiction: form.jurisdiction,
        idNumber:
          'SQID-' +
          Math.random().toString(36).slice(2, 8).toUpperCase() +
          '-' +
          Math.random().toString(36).slice(2, 6).toUpperCase(),
        verifiedAt: new Date().toISOString(),
      })
      setVerifying(false)
    }, 1400)
  }

  return (
    <>
      <section className="border-b border-ink-900/10 bg-ink-900/[0.02]">
        <div className="container-x py-16">
          <SectionHeading
            eyebrow="SeqPal ID"
            title="One passport. Every asset you qualify for."
            sub="Complete verification once. From then on you are automatically whitelisted for every SeqPal-issued asset your profile is eligible to hold — read directly by each token’s smart contract."
          />
        </div>
      </section>

      <section className="container-x py-16">
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
              SeqPal is the data controller for your SeqPal ID record. KYC and screening
              vendors act only as processors. Personal data is handled under the
              applicable data-protection regime (GDPR / UK GDPR and equivalents).
            </p>
          </div>

          <div>
            {id ? (
              <div className="space-y-5">
                <PassportCard id={id} />
                <DemoNote>
                  Identity verification is mocked in this demo — no real KYC vendor or
                  document upload is performed.
                </DemoNote>
                <div className="flex flex-wrap gap-3">
                  <Link to="/onboarding" className="btn-primary">
                    Start an issuance
                    <Icon.arrowRight width={16} height={16} />
                  </Link>
                  <Link to="/dashboard" className="btn-outline">
                    Go to dashboard
                  </Link>
                </div>
              </div>
            ) : (
              <div className="card p-7">
                <div className="flex items-center gap-2 text-sm font-semibold text-ink-800">
                  <Icon.id width={18} height={18} className="text-btc-600" />
                  Create a corporate SeqPal ID
                </div>
                <form onSubmit={submit} className="mt-5 space-y-4">
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
                      onChange={(e) =>
                        setForm({ ...form, jurisdiction: e.target.value })
                      }
                    >
                      {[
                        'United Arab Emirates',
                        'Switzerland',
                        'Singapore',
                        'El Salvador',
                        'United States',
                        'Cayman Islands',
                        'Honduras',
                      ].map((j) => (
                        <option key={j}>{j}</option>
                      ))}
                    </select>
                  </div>
                  <div className="rounded-xl border border-dashed border-ink-900/20 p-5 text-center">
                    <Icon.upload width={22} height={22} className="mx-auto text-ink-600" />
                    <p className="mt-2 text-sm text-ink-700/80">
                      Passports & proof-of-address for UBOs and directors, W-8/W-9 forms
                    </p>
                    <p className="mt-1 text-xs text-ink-700/50">
                      Document upload is skipped in this demo
                    </p>
                  </div>
                  <button
                    type="submit"
                    disabled={verifying}
                    className="btn-primary w-full"
                  >
                    {verifying ? (
                      <>
                        <span className="h-4 w-4 animate-spin rounded-full border-2 border-white/40 border-t-white" />
                        Running verification…
                      </>
                    ) : (
                      <>
                        Simulate KYB verification
                        <Icon.arrowRight width={16} height={16} />
                      </>
                    )}
                  </button>
                  <p className="text-center text-xs text-ink-700/60">
                    Corporate ID fee $150 · individual ID fee $20 per UBO — not charged in
                    the demo
                  </p>
                </form>
              </div>
            )}
          </div>
        </div>
      </section>
    </>
  )
}
