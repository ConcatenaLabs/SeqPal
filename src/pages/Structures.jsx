import { Link } from 'react-router-dom'
import { Icon, StructureIcon } from '../components/icons'
import { SectionHeading, Badge } from '../components/ui'
import { STRUCTURES } from '../data/structures'

function Meta({ label, value }) {
  return (
    <div className="rounded-lg bg-ink-900/[0.03] px-3 py-2.5">
      <div className="text-[11px] font-medium uppercase tracking-wide text-ink-700/60">
        {label}
      </div>
      <div className="mt-0.5 text-sm font-semibold text-ink-900">{value}</div>
    </div>
  )
}

export default function Structures() {
  return (
    <>
      <section className="border-b border-ink-900/10 bg-ink-900/[0.02]">
        <div className="container-x py-16">
          <SectionHeading
            eyebrow="Issuance structures"
            title="Four ways to bring an asset on-chain"
            sub="All four share a common skeleton: an issuer-owned Próspera LLC whose Operating Agreement recognizes the blockchain as the official Registry of Members and appoints SeqPal to specific agent roles. What differs is the legal claim the token represents."
          />
        </div>
      </section>

      <section className="container-x py-16">
        <div className="space-y-8">
          {STRUCTURES.map((s) => {
            const Ic = StructureIcon[s.icon]
            const accent =
              s.accent === 'btc'
                ? 'bg-btc-50 text-btc-600'
                : 'bg-liquid/10 text-liquid-600'
            return (
              <div key={s.id} id={s.id} className="card overflow-hidden">
                <div className="grid gap-0 lg:grid-cols-[1.4fr_1fr]">
                  <div className="p-8 lg:p-10">
                    <div className="flex flex-wrap items-center gap-3">
                      <div className={`flex h-12 w-12 items-center justify-center rounded-xl ${accent}`}>
                        <Ic width={24} height={24} />
                      </div>
                      <h2 className="text-2xl font-extrabold tracking-tight text-ink-900">
                        {s.name}
                      </h2>
                      {s.requiresCustody && (
                        <Badge color="amber">requires brokerage custody</Badge>
                      )}
                    </div>
                    <p className="mt-5 text-lg font-medium leading-relaxed text-ink-900">
                      {s.claim}
                    </p>
                    <p className="mt-3 leading-relaxed text-ink-700/90">
                      {s.description}
                    </p>

                    <div className="mt-6 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] p-4">
                      <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
                        Example
                      </div>
                      <p className="mt-1.5 text-sm italic text-ink-800">{s.example}</p>
                    </div>

                    <div className="mt-6">
                      <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
                        Typical use cases
                      </div>
                      <ul className="mt-3 grid gap-2.5 sm:grid-cols-2">
                        {s.useCases.map((u) => (
                          <li key={u} className="flex items-start gap-2.5">
                            <span className="mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-btc-50 text-btc-600">
                              <Icon.check width={12} height={12} />
                            </span>
                            <span className="text-sm text-ink-800">{u}</span>
                          </li>
                        ))}
                      </ul>
                    </div>
                  </div>

                  <div className="border-t border-ink-900/10 bg-ink-900/[0.02] p-8 lg:border-l lg:border-t-0 lg:p-10">
                    <div className="grid grid-cols-2 gap-3">
                      <Meta label="Setup from" value={s.setup.label} />
                      <Meta label="Annual support" value={`$${s.annual.toLocaleString()}`} />
                      <Meta label="Time to deploy" value={s.timeToDeploy} />
                      <Meta label="Complexity" value={s.complexity} />
                    </div>
                    <div className="mt-4 space-y-3">
                      <Meta label="SeqPal’s role" value={s.seqpalRole} />
                      <Meta label="Offering type" value={s.offering} />
                    </div>
                    {s.setup.simple && (
                      <p className="mt-4 text-xs text-ink-700/70">
                        A “Simple Native Equity” tier at ${s.setup.simple.toLocaleString()} is
                        available for raises up to $500K.
                      </p>
                    )}
                    {s.setup.securedAddOn && (
                      <p className="mt-4 text-xs text-ink-700/70">
                        Secured deals add {s.setup.securedAddOn} depending on collateral
                        complexity.
                      </p>
                    )}
                    <Link to="/onboarding" className="btn-primary mt-6 w-full">
                      Start a {s.short} issuance
                      <Icon.arrowRight width={16} height={16} />
                    </Link>
                  </div>
                </div>
              </div>
            )
          })}
        </div>

        <p className="mx-auto mt-10 max-w-3xl text-center text-sm text-ink-700/70">
          Three structures — Native Equity, Equity SPV, and Debt / Yield — can be run as
          either a private placement or a public offering. Depository Receipts are always
          a public offering. Public offerings carry additional disclosure requirements and
          a setup surcharge; see{' '}
          <Link to="/pricing" className="font-semibold text-btc-600 hover:underline">
            pricing
          </Link>
          .
        </p>
      </section>
    </>
  )
}
