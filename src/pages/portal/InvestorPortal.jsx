import { useCallback, useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { Icon } from '../../components/icons'
import { Badge } from '../../components/ui'
import { useStore } from '../../lib/store'
import * as api from '../../lib/api'
import GateSteps from '../../components/money/GateSteps'
import SubscribeFlow from '../../components/money/SubscribeFlow'

// The investor-facing offering, served by seqpald's promotion-gated endpoint.
// An anonymous or ungated visitor sees only the fixed teaser (issuer, sector,
// structure, availability, a verification CTA): no price, raise, returns, or
// mechanics, so the ungated page stays below the EU public-offer and UK
// financial-promotion lines. A verified, eligible investor who clears the gate
// sees the full offering and can subscribe. Gate passage is the EU
// offeree-counting event, recorded server-side.

function Teaser({ teaser }) {
  const t = teaser || {}
  const rows = [
    ['Issuer', t.issuer],
    ['Sector', t.sector || 'Not stated'],
    ['Structure', t.structure],
  ]
  return (
    <div className="mt-6 grid grid-cols-2 gap-3 sm:grid-cols-3">
      {rows.map(([k, v]) => (
        <div key={k} className="rounded-xl border border-ink-900/10 bg-white p-4">
          <div className="text-xs text-ink-700/60">{k}</div>
          <div className="mt-0.5 font-bold text-ink-900">{v || '-'}</div>
        </div>
      ))}
      <div className="rounded-xl border border-ink-900/10 bg-white p-4">
        <div className="text-xs text-ink-700/60">Available in</div>
        <div className="mt-1 flex flex-wrap gap-1">
          {(t.available_in || []).length ? (
            t.available_in.map((c) => (
              <Badge key={c} color="slate">
                {c}
              </Badge>
            ))
          ) : (
            <span className="text-sm text-ink-700/70">By verified jurisdiction</span>
          )}
        </div>
      </div>
    </div>
  )
}

export default function InvestorPortal() {
  const { id } = useParams()
  const { status, isSignedIn, simFor } = useStore()
  const [offering, setOffering] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)

  const load = useCallback(async () => {
    try {
      const o = await api.offering(id)
      setOffering(o)
      setError(null)
    } catch (e) {
      setError(e.message)
    } finally {
      setLoading(false)
    }
  }, [id])

  // Re-fetch when the session changes (verifying flips the gate) and after a gate
  // is cleared. status transitions loading -> anon | in.
  useEffect(() => {
    if (status === 'loading') return
    load()
  }, [status, load])

  // Branding: if the issuer previewing from their own session set up a portal, use
  // it; otherwise a neutral SeqPal header with the issuer name from the teaser.
  const portalCfg = simFor(id).portal || {}
  const teaser = offering?.teaser
  const brand = portalCfg.brandName || teaser?.issuer || 'Offering'

  return (
    <div className="min-h-screen" style={{ background: '#f7f8fa' }}>
      <header className="border-b border-ink-900/10 bg-white">
        <div className="mx-auto flex h-16 max-w-5xl items-center justify-between px-5">
          <div className="flex items-center gap-2.5">
            <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-ink-900 text-sm font-bold text-white">
              {brand.slice(0, 1).toUpperCase()}
            </span>
            <span className="font-bold text-ink-900">{brand}</span>
          </div>
          <span className="text-xs text-ink-700/60">Powered by SeqPal on Sequentia</span>
        </div>
      </header>

      <div className="mx-auto max-w-5xl px-5 py-10">
        <Link
          to="/id"
          className="mb-6 inline-flex items-center gap-1.5 text-sm text-ink-700/70 hover:text-ink-900"
        >
          <Icon.arrowLeft width={15} height={15} /> SeqPal ID
        </Link>

        {loading ? (
          <div className="flex justify-center py-24">
            <span className="h-8 w-8 animate-spin rounded-full border-4 border-btc/20 border-t-btc" />
          </div>
        ) : error ? (
          <div className="rounded-2xl border border-rose-200 bg-white p-8 text-center">
            <p className="text-ink-700">{error}</p>
          </div>
        ) : (
          <div className="grid gap-8 lg:grid-cols-[1.5fr_1fr]">
            {/* Offering column */}
            <div>
              <Badge color="slate">{teaser?.structure || 'Offering'}</Badge>
              <h1 className="mt-3 text-3xl font-extrabold tracking-tight text-ink-900">
                {offering.gated ? teaser?.issuer : offering.name}
              </h1>
              <p className="mt-2 text-ink-700/80">
                {offering.gated ? (
                  'A tokenized offering issued on Sequentia, a Bitcoin sidechain.'
                ) : (
                  <>
                    {offering.name} · <span className="font-mono text-sm">{offering.ticker}</span> ·
                    issued on Sequentia, a Bitcoin sidechain
                  </>
                )}
              </p>

              {offering.gated ? (
                <>
                  <Teaser teaser={teaser} />
                  <div className="mt-8 rounded-2xl border border-dashed border-ink-900/20 bg-white p-6">
                    <div className="flex items-center gap-2">
                      <Icon.lock width={18} height={18} className="text-ink-500" />
                      <h2 className="font-bold text-ink-900">Verification required to view details</h2>
                    </div>
                    <p className="mt-2 text-sm leading-relaxed text-ink-700/75">
                      Price, target raise, returns, offering documents and subscription mechanics
                      are shown only to a verified investor whose jurisdiction and eligibility
                      clear this offering's policy. This is not a general solicitation.
                    </p>
                  </div>
                </>
              ) : (
                <>
                  <div className="mt-6 grid grid-cols-2 gap-3 sm:grid-cols-3">
                    <div className="rounded-xl border border-ink-900/10 bg-white p-4">
                      <div className="text-xs text-ink-700/60">Price</div>
                      <div className="mt-0.5 font-bold text-ink-900">
                        {Number(offering.price).toLocaleString('en-US')} {offering.quote || 'USD'}
                      </div>
                    </div>
                    <div className="rounded-xl border border-ink-900/10 bg-white p-4">
                      <div className="text-xs text-ink-700/60">Ticker</div>
                      <div className="mt-0.5 font-mono font-bold text-ink-900">{offering.ticker}</div>
                    </div>
                    <div className="rounded-xl border border-ink-900/10 bg-white p-4">
                      <div className="text-xs text-ink-700/60">Network</div>
                      <div className="mt-0.5 font-bold text-ink-900">Sequentia</div>
                    </div>
                  </div>
                  {offering.offeree_warning && (
                    <p className="mt-4 flex items-start gap-2 rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-700">
                      <Icon.spark width={14} height={14} className="mt-0.5 shrink-0" />
                      {offering.offeree_warning}
                    </p>
                  )}
                  <div className="mt-8">
                    <h2 className="flex items-center gap-2 font-bold text-ink-900">
                      <Icon.doc width={16} height={16} className="text-ink-600" /> Offering
                      identifiers
                    </h2>
                    <dl className="mt-3 divide-y divide-ink-900/10 rounded-xl border border-ink-900/10 bg-white text-sm">
                      {[
                        ['Asset id', offering.asset_id],
                        ['Document manifest', offering.manifest_hash],
                      ].map(([k, v]) => (
                        <div key={k} className="flex items-center justify-between gap-4 px-4 py-3">
                          <dt className="text-ink-700/70">{k}</dt>
                          <dd className="min-w-0 truncate text-right font-mono text-xs text-ink-800">
                            {v || '-'}
                          </dd>
                        </div>
                      ))}
                    </dl>
                    <Link
                      to={`/verify?asset=${encodeURIComponent(offering.asset_id)}`}
                      className="mt-3 inline-flex items-center gap-1.5 text-sm font-medium text-seq-600 hover:underline"
                    >
                      <Icon.shield width={15} height={15} /> Verify independently
                    </Link>
                  </div>
                </>
              )}
            </div>

            {/* Action column */}
            <div className="lg:sticky lg:top-6 lg:self-start">
              <div className="overflow-hidden rounded-2xl border border-ink-900/10 bg-white shadow-card">
                <div className="bg-ink-900 px-5 py-4 text-white">
                  <div className="flex items-center gap-2 text-sm font-semibold">
                    <Icon.shield width={16} height={16} /> SeqPal ID gate
                  </div>
                </div>
                <div className="p-5">
                  {!isSignedIn ? (
                    <>
                      <p className="text-sm text-ink-700/80">
                        Verify with a SeqPal ID to check your eligibility for this offering. The
                        rail you pay with is always your choice, never set by your jurisdiction.
                      </p>
                      <Link to={`/id?next=/portal/${id}`} className="btn-primary mt-4 w-full">
                        Verify with SeqPal ID
                        <Icon.arrowRight width={16} height={16} />
                      </Link>
                    </>
                  ) : offering.gated ? (
                    Array.isArray(offering.requirements) && offering.requirements.length > 0 ? (
                      <>
                        <p className="mb-3 text-sm text-ink-700/80">
                          Complete these steps to view the offering and subscribe.
                        </p>
                        <GateSteps id={id} requirements={offering.requirements} onCleared={load} />
                      </>
                    ) : (
                      <p className="text-sm text-ink-700/80">
                        {offering.cta || 'Verification is required to view this offering.'}
                      </p>
                    )
                  ) : (
                    <SubscribeFlow offering={offering} issuanceId={id} />
                  )}
                </div>
              </div>
              <p className="mt-3 text-center text-[11px] leading-relaxed text-ink-700/50">
                Nothing is an offer to sell or a solicitation to buy any security. Fiat rails are
                SIMULATED on the testnet and labeled on every derived surface.
              </p>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
