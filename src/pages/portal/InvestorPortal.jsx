import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { Icon } from '../../components/icons'
import { Badge, DemoNote } from '../../components/ui'
import { useStore } from '../../lib/store'
import { getStructure } from '../../data/structures'
import { JURISDICTIONS } from '../../data/jurisdictions'

const ACCENT_HEX = {
  btc: '#F7931A',
  liquid: '#27c2c9',
  indigo: '#4f46e5',
  emerald: '#059669',
  slate: '#334155',
}

function parseMoney(s) {
  return Number(String(s || '').replace(/[^0-9.]/g, '')) || 0
}

export default function InvestorPortal() {
  const { id } = useParams()
  const { account, isLoggedIn, issuances, updateIssuance } = useStore()
  const iss = issuances.find((i) => i.id === id)
  const [phase, setPhase] = useState('offering') // offering | signing | wiring | done
  const [amount, setAmount] = useState('')

  if (!iss || !iss.portal?.published) {
    return (
      <div className="flex min-h-screen flex-col items-center justify-center bg-ink-900/[0.03] px-6 text-center">
        <p className="text-ink-700">This portal isn’t published.</p>
        <Link to="/dashboard" className="btn-outline mt-6">
          Back to dashboard
        </Link>
      </div>
    )
  }

  const p = iss.portal
  const accent = ACCENT_HEX[p.accent] || ACCENT_HEX.btc
  const s = getStructure(iss.structureId)

  // Eligibility: evaluate the visiting SeqPal ID against the token's policy.
  const inv = account.individual
  const tier = inv ? iss.policy?.[inv.residenceCode] : undefined
  const eligible = tier === 'standard' || (tier === 'restricted' && inv?.accredited)
  const jName = inv && JURISDICTIONS.find((j) => j.code === inv.residenceCode)?.name

  const subscribe = () => setPhase('signing')
  const sign = () => setPhase('wiring')
  const wire = () => {
    // Record the subscription against the issuance: funds into escrow, pending
    // settlement on the issuer closing the round.
    const amt = parseMoney(amount || p.minInvestment)
    updateIssuance(iss.id, (i) => ({
      subscriptions: [
        {
          id: 'sub_' + Math.random().toString(36).slice(2, 9),
          name: inv.name,
          jur: inv.residenceCode,
          amount: amt,
          status: 'in_escrow',
          at: new Date().toISOString(),
        },
        ...(i.subscriptions || []),
      ],
    }))
    setPhase('done')
  }

  return (
    <div className="min-h-screen bg-ink-50/40" style={{ background: '#f7f8fa' }}>
      {/* Issuer-branded header (whitelabel) */}
      <header className="border-b border-ink-900/10 bg-white">
        <div className="mx-auto flex h-16 max-w-5xl items-center justify-between px-5">
          <div className="flex items-center gap-2.5">
            <span
              className="flex h-8 w-8 items-center justify-center rounded-lg text-sm font-bold text-white"
              style={{ background: accent }}
            >
              {(p.brandName || 'A').slice(0, 1).toUpperCase()}
            </span>
            <span className="font-bold text-ink-900">{p.brandName}</span>
          </div>
          <div className="flex items-center gap-3 text-xs text-ink-700/60">
            <span className="font-mono">invest.{p.slug}.com</span>
          </div>
        </div>
      </header>

      <div className="mx-auto max-w-5xl px-5 py-10">
        <Link
          to={`/issuance/${iss.id}`}
          className="mb-6 inline-flex items-center gap-1.5 text-sm text-ink-700/70 hover:text-ink-900"
        >
          <Icon.arrowLeft width={15} height={15} /> Back to issuance (demo)
        </Link>

        <div className="grid gap-8 lg:grid-cols-[1.5fr_1fr]">
          {/* Offering */}
          <div>
            <Badge color="slate">{s?.name}</Badge>
            <h1 className="mt-3 text-3xl font-extrabold tracking-tight text-ink-900">
              {p.headline}
            </h1>
            <p className="mt-2 text-ink-700/80">
              {iss.name} · <span className="font-mono text-sm">{iss.ticker}</span> · issued
              on the Liquid Network
            </p>

            <div className="mt-6 grid grid-cols-2 gap-3 sm:grid-cols-3">
              {[
                ['Target raise', iss.raise || '—'],
                ['Min. investment', `$${p.minInvestment}`],
                ['Offering', iss.isPublic ? 'Public' : 'Private placement'],
              ].map(([k, v]) => (
                <div key={k} className="rounded-xl border border-ink-900/10 bg-white p-4">
                  <div className="text-xs text-ink-700/60">{k}</div>
                  <div className="mt-0.5 font-bold text-ink-900">{v}</div>
                </div>
              ))}
            </div>

            {/* Data room — only after eligibility */}
            <div className="mt-8">
              <h2 className="flex items-center gap-2 font-bold text-ink-900">
                <Icon.lock width={16} height={16} className="text-ink-600" /> Data room
              </h2>
              {eligible ? (
                <div className="mt-3 space-y-2">
                  {p.docs.map((d) => (
                    <div
                      key={d}
                      className="flex items-center gap-3 rounded-lg border border-ink-900/10 bg-white px-4 py-3"
                    >
                      <Icon.doc width={18} height={18} className="text-ink-600" />
                      <span className="flex-1 text-sm font-medium text-ink-900">{d}</span>
                      <span className="text-xs text-ink-700/50">PDF</span>
                    </div>
                  ))}
                </div>
              ) : (
                <div className="mt-3 rounded-xl border border-dashed border-ink-900/20 bg-white p-6 text-center text-sm text-ink-700/70">
                  <Icon.lock width={22} height={22} className="mx-auto text-ink-500" />
                  <p className="mt-2">
                    Offering documents unlock once your SeqPal ID clears the eligibility
                    gate.
                  </p>
                </div>
              )}
            </div>
          </div>

          {/* Eligibility / subscription panel */}
          <div className="lg:sticky lg:top-6 lg:self-start">
            <div className="overflow-hidden rounded-2xl border border-ink-900/10 bg-white shadow-card">
              <div
                className="px-5 py-4 text-white"
                style={{ background: accent }}
              >
                <div className="flex items-center gap-2 text-sm font-semibold">
                  <Icon.shield width={16} height={16} /> SeqPal ID eligibility gate
                </div>
              </div>
              <div className="p-5">
                {!isLoggedIn ? (
                  <>
                    <p className="text-sm text-ink-700/80">
                      Every investor must hold a verified SeqPal ID that satisfies this
                      offering’s jurisdiction and accreditation policy before viewing terms
                      or subscribing.
                    </p>
                    <Link
                      to={`/id?next=/portal/${iss.id}`}
                      className="btn-primary mt-4 w-full"
                    >
                      Verify with SeqPal ID
                      <Icon.arrowRight width={16} height={16} />
                    </Link>
                  </>
                ) : !eligible ? (
                  <>
                    <div className="flex items-center gap-2 text-sm font-semibold text-rose-600">
                      <Icon.close width={16} height={16} /> Not eligible
                    </div>
                    <p className="mt-2 text-sm text-ink-700/80">
                      Your SeqPal ID ({jName}
                      {inv?.accredited ? ', qualified' : ', retail'}) doesn’t satisfy this
                      offering’s policy for your jurisdiction.
                    </p>
                    {tier === 'restricted' && !inv?.accredited && (
                      <p className="mt-2 text-xs text-ink-700/60">
                        This offering admits {jName} investors on a qualified / accredited
                        basis only.
                      </p>
                    )}
                    <Link to="/id" className="btn-outline mt-4 w-full">
                      Manage SeqPal ID
                    </Link>
                  </>
                ) : phase === 'done' ? (
                  <div className="text-center">
                    <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-emerald-500 text-white">
                      <Icon.check width={26} height={26} />
                    </div>
                    <h3 className="mt-3 font-bold text-ink-900">Subscription received</h3>
                    <p className="mt-1 text-sm text-ink-700/80">
                      ${amount || p.minInvestment} is held in escrow. On closing, your{' '}
                      {iss.ticker} tokens are delivered to your whitelisted wallet
                      {inv?.gaid ? (
                        <>
                          {' '}
                          <span className="font-mono text-xs text-ink-700">
                            (GAID {inv.gaid.slice(0, 6)}…{inv.gaid.slice(-4)})
                          </span>
                        </>
                      ) : null}
                      .
                    </p>
                  </div>
                ) : (
                  <>
                    <div className="flex items-center gap-2 rounded-lg bg-emerald-50 px-3 py-2 text-sm font-medium text-emerald-700">
                      <Icon.check width={16} height={16} /> Eligible · {jName}
                    </div>

                    {phase === 'offering' && (
                      <div className="mt-4">
                        <label className="label">Investment amount</label>
                        <div className="relative">
                          <span className="pointer-events-none absolute left-3.5 top-1/2 -translate-y-1/2 text-sm text-ink-700/60">
                            $
                          </span>
                          <input
                            className="input pl-7"
                            placeholder={p.minInvestment}
                            value={amount}
                            onChange={(e) => setAmount(e.target.value)}
                          />
                        </div>
                        {(() => {
                          const belowMin =
                            parseMoney(amount) > 0 &&
                            parseMoney(amount) < parseMoney(p.minInvestment)
                          return (
                            <>
                              <p
                                className={`mt-1.5 text-xs ${
                                  belowMin ? 'font-medium text-rose-600' : 'text-ink-700/60'
                                }`}
                              >
                                {belowMin
                                  ? `Below the $${p.minInvestment} minimum for this offering`
                                  : `Minimum $${p.minInvestment}`}
                              </p>
                              <button
                                onClick={subscribe}
                                disabled={belowMin}
                                className="btn-primary mt-4 w-full"
                              >
                                Subscribe
                                <Icon.arrowRight width={16} height={16} />
                              </button>
                            </>
                          )
                        })()}
                      </div>
                    )}

                    {phase === 'signing' && (
                      <div className="mt-4">
                        <div className="rounded-lg border border-ink-900/10 p-4 text-sm text-ink-700/80">
                          <div className="flex items-center gap-2 font-semibold text-ink-900">
                            <Icon.doc width={16} height={16} /> Subscription Agreement
                          </div>
                          <p className="mt-2 text-xs">
                            Sign the subscription agreement to commit ${amount || p.minInvestment}.
                            Executed via integrated e-signature (mocked).
                          </p>
                        </div>
                        <button onClick={sign} className="btn-primary mt-4 w-full">
                          <Icon.check width={16} height={16} /> E-sign agreement
                        </button>
                      </div>
                    )}

                    {phase === 'wiring' && (
                      <div className="mt-4">
                        <div className="rounded-lg border border-ink-900/10 p-4 text-sm text-ink-700/80">
                          <div className="flex items-center gap-2 font-semibold text-ink-900">
                            <Icon.lock width={16} height={16} /> Fund escrow
                          </div>
                          <p className="mt-2 text-xs">
                            Wire ${amount || p.minInvestment} to the tri-party escrow
                            account. Released to the issuer on closing; tokens delivered to
                            your whitelisted wallet on settlement.
                          </p>
                        </div>
                        <button onClick={wire} className="btn-primary mt-4 w-full">
                          <Icon.wallet width={16} height={16} /> Confirm wire (mock)
                        </button>
                      </div>
                    )}
                  </>
                )}
              </div>
            </div>

            <p className="mt-3 text-center text-[11px] leading-relaxed text-ink-700/50">
              Operated by {iss.principal?.name}. Powered by SeqPal technology. Not an offer
              to sell or a solicitation to buy any security.
            </p>
          </div>
        </div>

        <DemoNote className="mx-auto mt-10 max-w-xl">
          This is the investor-facing portal as it appears on the issuer’s own domain. In
          the demo your logged-in SeqPal ID acts as the visiting investor, and signing,
          escrow funding, and token delivery are simulated.
        </DemoNote>
      </div>
    </div>
  )
}
