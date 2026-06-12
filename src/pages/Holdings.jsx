import { Link } from 'react-router-dom'
import { Icon, StructureIcon } from '../components/icons'
import { Badge } from '../components/ui'
import SignInGate from '../components/SignInGate'
import { useStore } from '../lib/store'
import { getStructure } from '../data/structures'
import { fmtAmount } from '../lib/economics'

// A position = a subscription the signed-in investor made, in the context of its
// issuance. The demo's logged-in SeqPal ID acts as the visiting investor, so we
// match subscriptions by the individual's name (how the portal records them).
function positionsFor(individual, issuances) {
  const out = []
  for (const iss of issuances) {
    for (const sub of iss.subscriptions || []) {
      if (sub.name === individual.name) out.push({ iss, sub })
    }
  }
  return out.sort((a, b) => (a.sub.at < b.sub.at ? 1 : -1))
}

export default function Holdings() {
  const { account, isLoggedIn, issuances } = useStore()
  if (!isLoggedIn) {
    return (
      <SignInGate
        title="Sign in to see your holdings"
        body="Your holdings are tied to your SeqPal ID — it’s your login and your identity passport across every SeqPal-issued asset."
        backTo="/id"
        backLabel="SeqPal ID home"
      />
    )
  }

  const positions = positionsFor(account.individual, issuances)
  // Group amounts by unit so USD and BTC positions don't get summed together.
  const totals = positions.reduce((m, { iss, sub }) => {
    const u = iss.unit === 'BTC' ? 'BTC' : 'USD'
    m[u] = (m[u] || 0) + sub.amount
    return m
  }, {})
  const settled = positions.filter((p) => p.sub.status === 'settled').length
  const issuers = new Set(positions.map((p) => p.iss.entityName || p.iss.name)).size

  return (
    <section className="container-x py-12">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-3xl font-extrabold tracking-tight text-ink-900">
            My holdings
          </h1>
          <p className="mt-1 text-ink-700/80">
            Every SeqPal-issued asset you hold — one verified SeqPal ID,{' '}
            <span className="font-mono text-sm">{account.individual.idNumber}</span>.
          </p>
        </div>
        <Link to="/id" className="btn-outline">
          <Icon.id width={16} height={16} /> Manage SeqPal ID
        </Link>
      </div>

      {/* summary */}
      <div className="mt-8 grid gap-4 sm:grid-cols-3">
        <div className="card p-5">
          <div className="text-sm text-ink-700/70">Committed</div>
          <div className="mt-0.5 text-2xl font-extrabold tracking-tight text-ink-900">
            {Object.keys(totals).length === 0
              ? '—'
              : Object.entries(totals)
                  .map(([u, n]) => fmtAmount(n, u))
                  .join(' · ')}
          </div>
        </div>
        <div className="card p-5">
          <div className="text-sm text-ink-700/70">Positions</div>
          <div className="mt-0.5 text-2xl font-extrabold tracking-tight text-ink-900">
            {positions.length}
            <span className="ml-2 text-sm font-medium text-ink-700/60">
              {settled} settled
            </span>
          </div>
        </div>
        <div className="card p-5">
          <div className="text-sm text-ink-700/70">Issuers</div>
          <div className="mt-0.5 text-2xl font-extrabold tracking-tight text-ink-900">
            {issuers}
          </div>
        </div>
      </div>

      {/* positions */}
      <div className="mt-10">
        <h2 className="text-lg font-bold text-ink-900">Positions</h2>
        {positions.length === 0 ? (
          <div className="card mt-4 flex flex-col items-center justify-center px-6 py-16 text-center">
            <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-ink-900/[0.04] text-ink-600">
              <Icon.wallet width={28} height={28} />
            </div>
            <h3 className="mt-5 text-lg font-bold text-ink-900">No holdings yet</h3>
            <p className="mt-2 max-w-sm text-sm text-ink-700/80">
              Once your SeqPal ID is verified, you’re auto-whitelisted for every
              SeqPal-issued asset your profile is eligible to hold — subscribe through an
              issuer’s placement portal and your position appears here.
            </p>
            <Link to="/id" className="btn-primary mt-6">
              See offerings you can access
              <Icon.arrowRight width={16} height={16} />
            </Link>
          </div>
        ) : (
          <div className="mt-4 space-y-3">
            {positions.map(({ iss, sub }) => {
              const s = getStructure(iss.structureId)
              const Ic = StructureIcon[s?.icon] || Icon.layers
              const held = sub.status === 'settled'
              return (
                <div
                  key={sub.id}
                  className="card flex items-center gap-4 p-5"
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
                      <span className="font-mono text-xs text-ink-700/60">{iss.ticker}</span>
                    </div>
                    <div className="mt-0.5 text-sm text-ink-700/70">
                      {s?.name} · {iss.entityName || iss.name} LLC
                      {sub.rail ? ` · funded ${sub.rail}` : ''}
                    </div>
                  </div>
                  <div className="text-right">
                    <div className="font-mono text-sm font-semibold text-ink-900">
                      {fmtAmount(sub.amount, iss.unit)}
                    </div>
                    <div className="mt-1">
                      {held ? (
                        <Badge color="emerald">
                          <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" /> Held
                        </Badge>
                      ) : (
                        <Badge color="amber">In escrow</Badge>
                      )}
                    </div>
                  </div>
                  {iss.portal?.published && (
                    <Link
                      to={`/portal/${iss.id}`}
                      className="hidden text-ink-600 hover:text-ink-900 sm:block"
                      aria-label="Open offering"
                    >
                      <Icon.arrowRight width={18} height={18} />
                    </Link>
                  )}
                </div>
              )
            })}
            <p className="px-1 pt-1 text-xs text-ink-700/55">
              Settled positions have been delivered to your whitelisted wallet
              {account.individual.gaid
                ? ` (GAID ${account.individual.gaid.slice(0, 6)}…${account.individual.gaid.slice(-4)})`
                : ''}
              . In-escrow positions settle when the issuer’s closing conditions are met.
            </p>
          </div>
        )}
      </div>
    </section>
  )
}
