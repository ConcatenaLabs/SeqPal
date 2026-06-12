import { Link } from 'react-router-dom'
import { Icon, StructureIcon } from '../components/icons'
import { Badge } from '../components/ui'
import SignInGate from '../components/SignInGate'
import { useStore, fakeAssetId, fakeTxid } from '../lib/store'
import { ownedIssuances } from '../lib/account'
import { getStructure } from '../data/structures'
import { JURISDICTIONS } from '../data/jurisdictions'
import { STATUS } from '../lib/lifecycle'

function buildSample(individual) {
  const policy = Object.fromEntries(JURISDICTIONS.map((j) => [j.code, j.tier]))
  const now = new Date().toISOString()
  return {
    id: 'iss_' + Math.random().toString(36).slice(2, 9),
    name: 'Aurora Ventures Fund I',
    ticker: 'AURA',
    entityName: 'Aurora Ventures Fund I',
    unit: 'USD',
    structureId: 'native-equity',
    principal: { type: 'individual', name: individual.name, idNumber: individual.idNumber },
    isPublic: false,
    raise: '$5,000,000',
    fields: { raise: '5,000,000', premoney: '20,000,000', supply: '10,000,000' },
    policy,
    mintTarget: 'placement-portal escrow address',
    assetId: fakeAssetId(),
    txid: fakeTxid(),
    status: 'live',
    createdAt: now,
    liveAt: now,
    portal: {
      configured: true,
      published: true,
      brandName: 'Aurora Ventures',
      headline: 'Invest in Aurora Ventures Fund I',
      accent: 'btc',
      slug: 'aurora-ventures',
      docs: ['Offering Memorandum', 'Term Sheet', 'Subscription Agreement', 'Cap table summary'],
      minInvestment: '25,000',
      escrowRequested: true,
      tosAccepted: true,
    },
    subscriptions: [
      { id: 'sub_a', name: 'Imani Okafor', jur: 'SV', amount: 250000, rail: 'USD', status: 'settled', at: now },
      { id: 'sub_b', name: 'Lukas Meyer', jur: 'CH', amount: 500000, rail: 'BTC → L-USDT', status: 'in_escrow', at: now },
    ],
  }
}

function StatusBadge({ status }) {
  const s = STATUS[status]
  if (!s) return <Badge color="amber">Draft</Badge>
  return (
    <Badge color={s.color}>
      {status === 'live' && <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />}
      {s.label}
    </Badge>
  )
}

export default function Dashboard() {
  const { account, isLoggedIn, issuances, reset, addIssuance } = useStore()

  if (!isLoggedIn) {
    return (
      <SignInGate
        title="Sign in to your issuer dashboard"
        body="The dashboard requires a verified SeqPal ID. It’s your login and your identity passport across the platform."
      />
    )
  }

  // Only the signed-in principal's issuances — other personas' deals in this
  // browser's demo store are not theirs to see or manage.
  const mine = ownedIssuances(account, issuances)
  const liveCount = mine.filter((i) => i.status === 'live').length
  const loadSample = () => addIssuance(buildSample(account.individual))

  return (
    <section className="container-x py-12">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-3xl font-extrabold tracking-tight text-ink-900">
            Issuer Dashboard
          </h1>
          <p className="mt-1 text-ink-700/80">
            Signed in as{' '}
            <span className="font-semibold text-ink-900">{account.individual.name}</span> ·{' '}
            <span className="font-mono text-sm">{account.individual.idNumber}</span>
          </p>
        </div>
        <div className="flex gap-2">
          {issuances.length > 0 && (
            <button onClick={reset} className="btn-ghost text-ink-700/70">
              Reset demo
            </button>
          )}
          <button onClick={loadSample} className="btn-outline">
            Load sample
          </button>
          <Link to="/onboarding" className="btn-primary">
            <Icon.spark width={16} height={16} />
            New issuance
          </Link>
        </div>
      </div>

      {/* Account summary */}
      <div className="mt-8 grid gap-4 sm:grid-cols-3">
        <Link to="/id" className="card flex items-center gap-4 p-5 hover:shadow-glow">
          <div className="flex h-11 w-11 items-center justify-center rounded-xl bg-emerald-50 text-emerald-600">
            <Icon.id width={22} height={22} />
          </div>
          <div className="min-w-0">
            <div className="text-sm text-ink-700/70">SeqPal ID</div>
            <div className="truncate font-semibold text-ink-900">
              Verified · {account.corporates.length} corporate
            </div>
          </div>
          <Icon.arrowRight width={16} height={16} className="ml-auto text-ink-500" />
        </Link>
        <div className="card flex items-center gap-4 p-5">
          <div className="flex h-11 w-11 items-center justify-center rounded-xl bg-btc-50 text-btc-600">
            <Icon.layers width={22} height={22} />
          </div>
          <div>
            <div className="text-sm text-ink-700/70">Issuances</div>
            <div className="font-semibold text-ink-900">{mine.length}</div>
          </div>
        </div>
        <div className="card flex items-center gap-4 p-5">
          <div className="flex h-11 w-11 items-center justify-center rounded-xl bg-liquid/10 text-liquid-600">
            <Icon.exchange width={22} height={22} />
          </div>
          <div>
            <div className="text-sm text-ink-700/70">Live on Liquid</div>
            <div className="font-semibold text-ink-900">{liveCount}</div>
          </div>
        </div>
      </div>

      {/* Issuance list */}
      <div className="mt-10">
        <h2 className="text-lg font-bold text-ink-900">Your issuances</h2>
        {mine.length === 0 ? (
          <div className="card mt-4 flex flex-col items-center justify-center px-6 py-16 text-center">
            <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-ink-900/[0.04] text-ink-600">
              <Icon.layers width={28} height={28} />
            </div>
            <h3 className="mt-5 text-lg font-bold text-ink-900">No issuances yet</h3>
            <p className="mt-2 max-w-sm text-sm text-ink-700/80">
              Start your first issuance and walk through the six-step onboarding flow —
              from structure choice to live deployment on the Liquid Network.
            </p>
            <div className="mt-6 flex flex-wrap items-center justify-center gap-3">
              <Link to="/onboarding" className="btn-primary">
                Launch an issuance
                <Icon.arrowRight width={16} height={16} />
              </Link>
              <button onClick={loadSample} className="btn-outline">
                Load a sample issuance
              </button>
            </div>
          </div>
        ) : (
          <div className="mt-4 space-y-3">
            {mine.map((iss) => {
              const s = getStructure(iss.structureId)
              const Ic = StructureIcon[s?.icon] || Icon.layers
              return (
                <Link
                  key={iss.id}
                  to={`/issuance/${iss.id}`}
                  className="card flex items-center gap-4 p-5 transition-all hover:-translate-y-0.5 hover:shadow-glow"
                >
                  <div
                    className={`flex h-12 w-12 shrink-0 items-center justify-center rounded-xl ${
                      s?.accent === 'liquid'
                        ? 'bg-liquid/10 text-liquid-600'
                        : 'bg-btc-50 text-btc-600'
                    }`}
                  >
                    <Ic width={24} height={24} />
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="truncate font-bold text-ink-900">{iss.name}</span>
                      <span className="font-mono text-xs text-ink-700/60">
                        {iss.ticker}
                      </span>
                    </div>
                    <div className="mt-0.5 text-sm text-ink-700/70">
                      {s?.name} · {iss.principal?.name}
                    </div>
                  </div>
                  <div className="hidden text-right sm:block">
                    <StatusBadge status={iss.status} />
                    <div className="mt-1 text-xs text-ink-700/50">
                      {iss.status === 'awaiting_incorporation' && iss.incorporationEta
                        ? `est. ${new Date(iss.incorporationEta).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}`
                        : iss.status === 'live' && iss.structureId !== 'depository-receipt'
                          ? iss.portal?.published
                            ? 'Portal live'
                            : 'Portal not set up'
                          : new Date(iss.createdAt).toLocaleDateString()}
                    </div>
                  </div>
                  <Icon.arrowRight width={18} height={18} className="text-ink-600" />
                </Link>
              )
            })}
          </div>
        )}
      </div>
    </section>
  )
}
