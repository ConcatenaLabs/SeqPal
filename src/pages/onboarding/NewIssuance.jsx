import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import Logo from '../../components/Logo'
import { Icon } from '../../components/icons'
import SignInGate from '../../components/SignInGate'
import { useStore } from '../../lib/store'
import {
  Step1Identity,
  Step2Structure,
  Step3Enforcement,
  Step4DataRoom,
  Step5Documents,
  Step6Compliance,
  Step7Checkout,
} from './Steps'

const STEPS = [
  { n: 1, title: 'Identity & principal', sub: 'Who is issuing' },
  { n: 2, title: 'Structure', sub: 'Choose an issuance type' },
  { n: 3, title: 'Holders & enforcement', sub: 'Who holds, who enforces' },
  { n: 4, title: 'Data room', sub: 'Enter your deal terms' },
  { n: 5, title: 'Documents', sub: 'Generate & e-sign' },
  { n: 6, title: 'Tokenomics & compliance', sub: 'Bake in the rules' },
  { n: 7, title: 'Checkout & deploy', sub: 'Pay and go live' },
]

export default function NewIssuance() {
  const navigate = useNavigate()
  const { loading, isSignedIn } = useStore()
  const [step, setStep] = useState(1)
  const [data, setData] = useState({
    principal: null, // { kind:'individual'|'entity', name, entity_id }
    structureId: null,
    // The enforcement election: 'serviced' (default) | 'network' | 'bearer'.
    enforcement: 'serviced',
    isPublic: false,
    entityName: '', // the new Próspera LLC's name (set before formation docs)
    unit: 'USD', // elected unit of account (USD default; BTC available)
    attested: false, // structure-specific mandatory attestation
    fields: {},
    raise: '',
    name: '',
    ticker: '',
    policy: null,
    lifted: {},
    clawback: true, // issuer recovery power, an explicit election (off for bearer)
    recovery: null, // bearer only: { xonly, envelope, exported }
    bearerNoUs: false, // bearer attestation checkboxes, signed at deploy
    bearerRisk: false,
    docsSigned: false,
  })

  const update = (patch) => setData((d) => ({ ...d, ...patch }))
  const next = () => setStep((s) => Math.min(7, s + 1))
  const back = () => setStep((s) => Math.max(1, s - 1))

  // Per-step guard: can the user advance?
  const REQUIRES_ATTESTATION = ['equity-spv', 'debt-yield', 'depository-receipt']
  const canAdvance = () => {
    if (step === 1) return !!data.principal
    if (step === 2) return !!data.structureId
    if (step === 3) return !!data.enforcement
    if (step === 4)
      return !REQUIRES_ATTESTATION.includes(data.structureId) || !!data.attested
    if (step === 5) return data.docsSigned
    if (step === 6)
      return !!data.name && !!data.ticker && (data.enforcement === 'bearer' || !!data.policy)
    return true
  }

  if (loading || !isSignedIn) {
    return (
      <div className="min-h-screen bg-ink-900/[0.03]">
        <header className="border-b border-ink-900/10 bg-white">
          <div className="container-x flex h-16 items-center justify-between">
            <Link to="/">
              <Logo />
            </Link>
            <Link to="/dashboard" className="btn-ghost text-ink-700">
              Exit
            </Link>
          </div>
        </header>
        {loading ? (
          <div className="flex justify-center py-24">
            <span className="h-8 w-8 animate-spin rounded-full border-4 border-btc/20 border-t-btc" />
          </div>
        ) : (
          <SignInGate
            title="Sign in to start an issuance"
            body="Issuing needs a SeqPal ID. The ID is the identity and compliance passport for restricted assets on Sequentia, and it is the identity your issuance is recorded against. Create one and we will bring you straight back."
          />
        )}
      </div>
    )
  }

  const stepProps = { data, update, next }

  return (
    <div className="flex min-h-screen flex-col bg-ink-900/[0.03]">
      {/* Top bar */}
      <header className="sticky top-0 z-30 border-b border-ink-900/10 bg-white">
        <div className="container-x flex h-16 items-center justify-between">
          <Link to="/">
            <Logo />
          </Link>
          <div className="hidden text-sm font-medium text-ink-700 sm:block">
            New issuance · Step {step} of 7
          </div>
          <Link to="/dashboard" className="btn-ghost text-ink-700">
            <Icon.close width={16} height={16} />
            Exit
          </Link>
        </div>
        {/* progress bar */}
        <div className="h-1 w-full bg-ink-900/10">
          <div
            className="h-full bg-btc transition-all duration-300"
            style={{ width: `${(step / 7) * 100}%` }}
          />
        </div>
      </header>

      <div className="container-x flex flex-1 gap-10 py-8 lg:py-12">
        {/* Step rail */}
        <aside className="hidden w-64 shrink-0 lg:block">
          <ol className="sticky top-28 space-y-1">
            {STEPS.map((s) => {
              const state =
                s.n < step ? 'done' : s.n === step ? 'active' : 'todo'
              return (
                <li key={s.n}>
                  <button
                    onClick={() => s.n < step && setStep(s.n)}
                    disabled={s.n >= step}
                    className={`flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left transition-colors ${
                      state === 'active'
                        ? 'bg-white shadow-card'
                        : state === 'done'
                          ? 'hover:bg-white/60'
                          : 'opacity-60'
                    }`}
                  >
                    <span
                      className={`flex h-7 w-7 shrink-0 items-center justify-center rounded-full text-xs font-bold ${
                        state === 'done'
                          ? 'bg-btc text-white'
                          : state === 'active'
                            ? 'bg-ink-900 text-white'
                            : 'bg-ink-900/10 text-ink-700'
                      }`}
                    >
                      {state === 'done' ? <Icon.check width={14} height={14} /> : s.n}
                    </span>
                    <span>
                      <span className="block text-sm font-semibold text-ink-900">
                        {s.title}
                      </span>
                      <span className="block text-xs text-ink-700/70">{s.sub}</span>
                    </span>
                  </button>
                </li>
              )
            })}
          </ol>
        </aside>

        {/* Step content */}
        <div className="min-w-0 flex-1">
          <div className="mx-auto max-w-3xl">
            {step === 1 && <Step1Identity {...stepProps} />}
            {step === 2 && <Step2Structure {...stepProps} />}
            {step === 3 && <Step3Enforcement {...stepProps} />}
            {step === 4 && <Step4DataRoom {...stepProps} />}
            {step === 5 && <Step5Documents {...stepProps} />}
            {step === 6 && <Step6Compliance {...stepProps} />}
            {step === 7 && (
              <Step7Checkout
                {...stepProps}
                onDeployed={(issuanceId) => navigate(`/issuance/${issuanceId}`)}
              />
            )}

            {/* Nav buttons (step 7 has its own deploy CTA) */}
            {step < 7 && (
              <div className="mt-8 flex items-center justify-between">
                <button
                  onClick={back}
                  disabled={step === 1}
                  className="btn-ghost text-ink-700 disabled:opacity-0"
                >
                  <Icon.arrowLeft width={16} height={16} />
                  Back
                </button>
                <button
                  onClick={next}
                  disabled={!canAdvance()}
                  className="btn-primary px-5"
                >
                  Continue
                  <Icon.arrowRight width={16} height={16} />
                </button>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
