import { Link, useParams } from 'react-router-dom'
import { Icon, StructureIcon } from '../components/icons'
import { Badge, DemoNote } from '../components/ui'
import SignInGate from '../components/SignInGate'
import ServicingPanel from '../components/ServicingPanel'
import { useStore } from '../lib/store'
import { getStructure } from '../data/structures'
import { JURISDICTIONS } from '../data/jurisdictions'
import { STATUS, milestonesFor, completedCount, nextStatus } from '../lib/lifecycle'

function parseMoney(s) {
  return Number(String(s || '').replace(/[^0-9.]/g, '')) || 0
}
const fmtUSD = (n) => '$' + Math.round(n).toLocaleString()

function Truncate({ value }) {
  if (!value) return <span className="text-ink-700/50">—</span>
  return (
    <span className="font-mono text-xs text-ink-700">
      {value.slice(0, 10)}…{value.slice(-8)}
    </span>
  )
}

function Timeline({ iss, onAdvance }) {
  const milestones = milestonesFor(iss.structureId)
  const done = completedCount(iss.status, iss.structureId)
  const eta = iss.incorporationEta ? new Date(iss.incorporationEta) : null

  return (
    <div className="card p-6">
      <div className="flex items-center justify-between">
        <h2 className="font-bold text-ink-900">Issuance lifecycle</h2>
        <Badge color={STATUS[iss.status]?.color}>
          {iss.status === 'live' && (
            <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />
          )}
          {STATUS[iss.status]?.label}
        </Badge>
      </div>

      <ol className="mt-5 space-y-1">
        {milestones.map((m, i) => {
          const state = i < done ? 'done' : i === done ? 'active' : 'todo'
          return (
            <li key={m.key} className="flex gap-3">
              <div className="flex flex-col items-center">
                <span
                  className={`flex h-7 w-7 items-center justify-center rounded-full text-xs font-bold ${
                    state === 'done'
                      ? 'bg-emerald-500 text-white'
                      : state === 'active'
                        ? 'bg-btc text-white'
                        : 'bg-ink-900/10 text-ink-600'
                  }`}
                >
                  {state === 'done' ? (
                    <Icon.check width={14} height={14} />
                  ) : state === 'active' ? (
                    <span className="h-2.5 w-2.5 animate-pulse rounded-full bg-white" />
                  ) : (
                    i + 1
                  )}
                </span>
                {i < milestones.length - 1 && (
                  <span
                    className={`my-0.5 w-0.5 flex-1 ${
                      i < done ? 'bg-emerald-500/40' : 'bg-ink-900/10'
                    }`}
                  />
                )}
              </div>
              <div className={`pb-4 ${state === 'todo' ? 'opacity-55' : ''}`}>
                <div className="text-sm font-semibold text-ink-900">{m.label}</div>
                <div className="text-xs text-ink-700/70">{m.detail}</div>
                {m.key === 'incorporation' && state === 'active' && eta && (
                  <div className="mt-1 text-xs font-medium text-amber-700">
                    Est. completion{' '}
                    {eta.toLocaleDateString(undefined, {
                      weekday: 'short',
                      month: 'short',
                      day: 'numeric',
                    })}{' '}
                    · 1–3 business days
                  </div>
                )}
              </div>
            </li>
          )
        })}
      </ol>

      {iss.status !== 'live' && (
        <div className="mt-2 rounded-xl border border-dashed border-btc/30 bg-btc-50 p-4">
          <div className="flex items-center gap-2 text-sm font-semibold text-btc-700">
            <Icon.spark width={16} height={16} /> Demo fast-forward
          </div>
          <p className="mt-1 text-xs text-btc-700/80">
            In production these steps complete over 1–3 business days. Advance them here to
            preview the live state.
          </p>
          <button
            onClick={() => onAdvance(nextStatus(iss.status))}
            className="btn-primary mt-3 w-full py-2"
          >
            {iss.status === 'awaiting_incorporation'
              ? 'Complete incorporation'
              : 'Deploy on Liquid'}
            <Icon.arrowRight width={15} height={15} />
          </button>
        </div>
      )}
    </div>
  )
}

function PortalCard({ iss }) {
  const isRaise = iss.structureId !== 'depository-receipt'
  const live = iss.status === 'live'
  const published = iss.portal?.published

  return (
    <div className="card p-6">
      <div className="flex items-center gap-2">
        <Icon.globe width={18} height={18} className="text-liquid-600" />
        <h2 className="font-bold text-ink-900">Placement Portal</h2>
        {published && <Badge color="emerald" className="ml-auto">Published</Badge>}
      </div>
      <p className="mt-1.5 text-sm text-ink-700/70">
        {isRaise
          ? 'Your own branded fundraising portal, operated on your domain. Investors clear the SeqPal ID gate, sign the subscription agreement, and wire into escrow.'
          : 'Depository Receipts are minted and redeemed directly rather than raised through a subscription portal.'}
      </p>

      {!isRaise ? (
        <div className="mt-4 rounded-lg bg-ink-900/[0.03] px-4 py-3 text-sm text-ink-700/80">
          Mint and redemption are handled from the asset’s transfer-agent controls.
        </div>
      ) : !live ? (
        <div className="mt-4 rounded-lg bg-ink-900/[0.03] px-4 py-3 text-sm text-ink-700/70">
          Available once your issuance is live.
        </div>
      ) : published ? (
        <div className="mt-4 space-y-3">
          <div className="flex items-center justify-between rounded-lg bg-ink-900/[0.03] px-4 py-2.5 text-sm">
            <span className="text-ink-700">Live at</span>
            <span className="font-mono text-xs text-liquid-600">
              invest.{iss.portal.slug}.com
            </span>
          </div>
          <div className="flex gap-2">
            <Link to={`/portal/${iss.id}`} className="btn-primary flex-1">
              <Icon.external width={15} height={15} /> View investor portal
            </Link>
            <Link to={`/issuance/${iss.id}/portal`} className="btn-outline">
              Edit
            </Link>
          </div>
        </div>
      ) : (
        <Link to={`/issuance/${iss.id}/portal`} className="btn-primary mt-4 w-full">
          <Icon.spark width={16} height={16} /> Set up your placement portal
        </Link>
      )}
    </div>
  )
}

export default function IssuanceDetail() {
  const { id } = useParams()
  const { isLoggedIn, issuances, updateIssuance } = useStore()

  if (!isLoggedIn) return <SignInGate />

  const iss = issuances.find((i) => i.id === id)
  if (!iss) {
    return (
      <section className="container-x py-24 text-center">
        <p className="text-ink-700">Issuance not found.</p>
        <Link to="/dashboard" className="btn-outline mt-6">
          Back to dashboard
        </Link>
      </section>
    )
  }

  const s = getStructure(iss.structureId)
  const Ic = StructureIcon[s?.icon] || Icon.layers
  const live = iss.status === 'live'

  const advance = (status) => {
    const patch = { status }
    if (status === 'live') patch.liveAt = new Date().toISOString()
    updateIssuance(iss.id, patch)
  }

  // Fundraising — derived from portal subscriptions.
  const isRaise = iss.structureId !== 'depository-receipt'
  const subs = iss.subscriptions || []
  const escrowSubs = subs.filter((s) => s.status === 'in_escrow')
  const settledSubs = subs.filter((s) => s.status === 'settled')
  const raiseNum = parseMoney(iss.raise)
  const escrowTotal = escrowSubs.reduce((a, s) => a + s.amount, 0)
  const settledTotal = settledSubs.reduce((a, s) => a + s.amount, 0)
  // Ownership basis differs by structure: Native Equity holders own a slice of
  // the post-money company (investment / post-money); SPV holders share the
  // single funded position and Debt holders share the note principal (both
  // investment / raise). DRs are not subscription-based.
  const premoney = parseMoney(iss.fields?.premoney)
  const isEquity = iss.structureId === 'native-equity'
  const denom = isEquity && premoney > 0 ? premoney + raiseNum : raiseNum
  const ownerPct = (amt) => (denom ? (amt / denom) * 100 : null)
  const ownBasis = isEquity
    ? 'post-money'
    : iss.structureId === 'equity-spv'
      ? '% of SPV'
      : iss.structureId === 'debt-yield'
        ? '% of principal'
        : 'live'
  const treasuryPct = Math.max(0, 100 - (ownerPct(settledTotal) || 0))

  const closeRound = () =>
    updateIssuance(iss.id, (i) => ({
      subscriptions: (i.subscriptions || []).map((s) =>
        s.status === 'in_escrow' ? { ...s, status: 'settled' } : s
      ),
    }))

  const openJ = JURISDICTIONS.filter((j) => iss.policy?.[j.code] === 'standard')
  const restrictedJ = JURISDICTIONS.filter((j) => iss.policy?.[j.code] === 'restricted')

  return (
    <section className="container-x py-12">
      <Link
        to="/dashboard"
        className="inline-flex items-center gap-1.5 text-sm font-medium text-ink-700 hover:text-ink-900"
      >
        <Icon.arrowLeft width={15} height={15} /> Dashboard
      </Link>

      <div className="mt-5 flex flex-wrap items-start justify-between gap-4">
        <div className="flex items-center gap-4">
          <div
            className={`flex h-14 w-14 items-center justify-center rounded-2xl ${
              s?.accent === 'liquid'
                ? 'bg-liquid/10 text-liquid-600'
                : 'bg-btc-50 text-btc-600'
            }`}
          >
            <Ic width={28} height={28} />
          </div>
          <div>
            <div className="flex items-center gap-3">
              <h1 className="text-2xl font-extrabold tracking-tight text-ink-900">
                {iss.name}
              </h1>
              <span className="font-mono text-sm text-ink-700/60">{iss.ticker}</span>
            </div>
            <div className="mt-1 text-sm text-ink-700/80">
              {s?.name} · owned by {iss.principal?.name}
            </div>
          </div>
        </div>
        <Badge color={STATUS[iss.status]?.color}>
          {live && <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />}
          {STATUS[iss.status]?.label}
        </Badge>
      </div>

      <div className="mt-8 grid gap-6 lg:grid-cols-3">
        {/* left: lifecycle + portal */}
        <div className="space-y-6">
          <Timeline iss={iss} onAdvance={advance} />
          <PortalCard iss={iss} />
        </div>

        {/* right: asset + servicing */}
        <div className="space-y-6 lg:col-span-2">
          <div className="card p-6">
            <h2 className="font-bold text-ink-900">Asset</h2>
            {!live && (
              <DemoNote className="mt-3">
                The AMP asset is deployed once incorporation and RFSA filing complete.
                Asset identifiers appear here when the issuance goes live.
              </DemoNote>
            )}
            <dl className="mt-4 divide-y divide-ink-900/10 text-sm">
              {[
                ['Issuer of record', `${iss.entityName || iss.name} LLC · Próspera`],
                ['Applicant / owner', iss.principal?.name],
                ['Network', 'Bitcoin · Liquid Network'],
                ['Issuance layer', 'Blockstream AMP · Transfer-Restricted'],
                ['Asset id', live ? <Truncate key="a" value={iss.assetId} /> : 'pending'],
                ['Issuance txid', live ? <Truncate key="t" value={iss.txid} /> : 'pending'],
                ['Initial mint to', iss.mintTarget],
                ['Target raise', iss.raise || '—'],
                ['Offering type', iss.isPublic ? 'Public offering' : 'Private placement'],
              ].map(([k, v]) => (
                <div key={k} className="flex items-center justify-between py-3">
                  <dt className="text-ink-700/70">{k}</dt>
                  <dd className="font-medium text-ink-900">{v}</dd>
                </div>
              ))}
            </dl>
          </div>

          {live && (
            <>
              <ServicingPanel iss={iss} />

              <div className="card p-6">
                <div className="flex items-center gap-2">
                  <Icon.exchange width={18} height={18} className="text-liquid-600" />
                  <h2 className="font-bold text-ink-900">Secondary market</h2>
                </div>
                <p className="mt-1.5 text-sm text-ink-700/70">
                  Because eligibility is enforced at the token-contract level, the asset
                  can trade freely between whitelisted holders — no proprietary trading
                  system required.
                </p>
                <div className="mt-4 grid gap-3 sm:grid-cols-2">
                  <div className="rounded-lg border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3">
                    <div className="flex items-center gap-2 text-sm font-semibold text-ink-900">
                      <span className="h-2 w-2 rounded-full bg-emerald-500" /> Discoverable
                      on SideSwap
                    </div>
                    <p className="mt-1 text-xs text-ink-700/70">
                      Liquid-native DEXs index every AMP asset automatically — no paid
                      listing.
                    </p>
                  </div>
                  <div className="rounded-lg border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3">
                    <div className="flex items-center gap-2 text-sm font-semibold text-ink-900">
                      <Icon.shield width={15} height={15} className="text-liquid-600" />{' '}
                      Eligibility on every transfer
                    </div>
                    <p className="mt-1 text-xs text-ink-700/70">
                      AMP queries SeqPal ID to approve or reject each transfer against your
                      policy.
                    </p>
                  </div>
                </div>
              </div>

              {isRaise && (
                <div className="card p-6">
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <Icon.coins width={18} height={18} className="text-btc-600" />
                      <h2 className="font-bold text-ink-900">Fundraising</h2>
                    </div>
                    <span className="text-xs text-ink-700/60">
                      via {iss.portal?.published ? `invest.${iss.portal.slug}.com` : 'placement portal'}
                    </span>
                  </div>

                  {subs.length === 0 ? (
                    <div className="mt-4 rounded-lg bg-ink-900/[0.03] px-4 py-3 text-sm text-ink-700/70">
                      No subscriptions yet.{' '}
                      {iss.portal?.published ? (
                        <Link
                          to={`/portal/${iss.id}`}
                          className="font-semibold text-btc-600 hover:underline"
                        >
                          Open your investor portal
                        </Link>
                      ) : (
                        'Publish your placement portal to start raising.'
                      )}
                    </div>
                  ) : (
                    <>
                      <div className="mt-4 grid grid-cols-3 gap-3">
                        <div className="rounded-lg bg-ink-900/[0.03] px-3 py-2.5">
                          <div className="text-xs text-ink-700/60">In escrow</div>
                          <div className="mt-0.5 font-bold text-ink-900">
                            {fmtUSD(escrowTotal)}
                          </div>
                        </div>
                        <div className="rounded-lg bg-ink-900/[0.03] px-3 py-2.5">
                          <div className="text-xs text-ink-700/60">Settled</div>
                          <div className="mt-0.5 font-bold text-ink-900">
                            {fmtUSD(settledTotal)}
                          </div>
                        </div>
                        <div className="rounded-lg bg-ink-900/[0.03] px-3 py-2.5">
                          <div className="text-xs text-ink-700/60">Subscribers</div>
                          <div className="mt-0.5 font-bold text-ink-900">{subs.length}</div>
                        </div>
                      </div>
                      {raiseNum > 0 && (
                        <div className="mt-3">
                          <div className="h-2 w-full overflow-hidden rounded-full bg-ink-900/10">
                            <div
                              className="h-full rounded-full bg-btc"
                              style={{
                                width: `${Math.min(100, ((escrowTotal + settledTotal) / raiseNum) * 100)}%`,
                              }}
                            />
                          </div>
                          <div className="mt-1 text-xs text-ink-700/60">
                            {fmtUSD(escrowTotal + settledTotal)} of {iss.raise} target
                          </div>
                        </div>
                      )}
                      <div className="mt-3 flex items-center justify-between rounded-lg bg-ink-900/[0.03] px-3 py-2 text-xs">
                        <span className="text-ink-700/70">
                          Platform Services Fee · 3% cap, $10K floor
                        </span>
                        <span className="font-mono font-semibold text-ink-900">
                          ~{fmtUSD(Math.max(10000, 0.03 * (escrowTotal + settledTotal)))}
                        </span>
                      </div>
                      <p className="mt-1 text-[11px] text-ink-700/55">
                        Invoiced for the technology, escrow, document automation, and
                        SeqPal ID bundle — not a placement commission.
                      </p>
                      {escrowSubs.length > 0 && (
                        <button onClick={closeRound} className="btn-primary mt-4 w-full">
                          <Icon.check width={16} height={16} />
                          Close round · release escrow & deliver tokens
                        </button>
                      )}
                    </>
                  )}
                </div>
              )}

              <div className="grid gap-6 sm:grid-cols-2">
                <div className="card overflow-hidden">
                  <div className="flex items-center justify-between border-b border-ink-900/10 px-5 py-3.5">
                    <h2 className="font-bold text-ink-900">Registry of Members</h2>
                    <span className="text-xs text-ink-700/60">{ownBasis}</span>
                  </div>
                  <div className="divide-y divide-ink-900/10">
                    <div className="flex items-center justify-between px-5 py-3">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium text-ink-900">
                          {isRaise ? 'Treasury / escrow' : 'Issuer treasury'}
                        </span>
                        <Badge color="slate">HN</Badge>
                      </div>
                      <span className="font-mono text-sm text-ink-800">
                        {treasuryPct.toFixed(1)}%
                      </span>
                    </div>
                    {settledSubs.map((s) => (
                      <div
                        key={s.id}
                        className="flex items-center justify-between px-5 py-3"
                      >
                        <div className="flex items-center gap-2">
                          <span className="text-sm font-medium text-ink-900">{s.name}</span>
                          <Badge color="slate">{s.jur}</Badge>
                        </div>
                        <span className="font-mono text-sm text-ink-800">
                          {denom ? `${ownerPct(s.amount).toFixed(2)}%` : fmtUSD(s.amount)}
                        </span>
                      </div>
                    ))}
                    {settledSubs.length === 0 && (
                      <div className="px-5 py-3 text-xs text-ink-700/55">
                        Holders appear here once subscriptions settle on closing.
                      </div>
                    )}
                  </div>
                </div>

                <div className="card overflow-hidden">
                  <div className="border-b border-ink-900/10 px-5 py-3.5">
                    <h2 className="font-bold text-ink-900">Compliance policy</h2>
                  </div>
                  <div className="space-y-4 px-5 py-4 text-sm">
                    <div>
                      <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
                        Standard jurisdictions
                      </div>
                      <div className="mt-2 flex flex-wrap gap-1.5">
                        {openJ.length ? (
                          openJ.map((j) => (
                            <Badge key={j.code} color="emerald">
                              {j.code}
                            </Badge>
                          ))
                        ) : (
                          <span className="text-ink-700/60">None</span>
                        )}
                      </div>
                    </div>
                    <div>
                      <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
                        Qualified / accredited only
                      </div>
                      <div className="mt-2 flex flex-wrap gap-1.5">
                        {restrictedJ.length ? (
                          restrictedJ.map((j) => (
                            <Badge key={j.code} color="amber">
                              {j.code}
                            </Badge>
                          ))
                        ) : (
                          <span className="text-ink-700/60">None</span>
                        )}
                      </div>
                    </div>
                    <div className="rounded-lg bg-ink-900/[0.03] px-3 py-2.5 text-xs text-ink-700/80">
                      Mandatory floors enforced on every transfer: SeqPal ID verification,
                      sanctions screening, and OFAC/FATF-aligned blocks.
                    </div>
                  </div>
                </div>
              </div>
            </>
          )}
        </div>
      </div>
    </section>
  )
}
