import { Link } from 'react-router-dom'
import { Icon, StructureIcon } from '../components/icons'
import { Badge } from '../components/ui'
import SignInGate from '../components/SignInGate'
import { useStore } from '../lib/store'
import { getStructure } from '../data/structures'
import { STATUS } from '../lib/lifecycle'

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
  const { account, isLoggedIn, issuances, reset } = useStore()

  if (!isLoggedIn) {
    return (
      <SignInGate
        title="Sign in to your issuer dashboard"
        body="The dashboard requires a verified SeqPal ID. It’s your login and your identity passport across the platform."
      />
    )
  }

  const liveCount = issuances.filter((i) => i.status === 'live').length

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
            <div className="font-semibold text-ink-900">{issuances.length}</div>
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
        {issuances.length === 0 ? (
          <div className="card mt-4 flex flex-col items-center justify-center px-6 py-16 text-center">
            <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-ink-900/[0.04] text-ink-600">
              <Icon.layers width={28} height={28} />
            </div>
            <h3 className="mt-5 text-lg font-bold text-ink-900">No issuances yet</h3>
            <p className="mt-2 max-w-sm text-sm text-ink-700/80">
              Start your first issuance and walk through the six-step onboarding flow —
              from structure choice to live deployment on the Liquid Network.
            </p>
            <Link to="/onboarding" className="btn-primary mt-6">
              Launch an issuance
              <Icon.arrowRight width={16} height={16} />
            </Link>
          </div>
        ) : (
          <div className="mt-4 space-y-3">
            {issuances.map((iss) => {
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
                      {new Date(iss.createdAt).toLocaleDateString()}
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
