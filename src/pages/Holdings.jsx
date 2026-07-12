import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Icon, StructureIcon } from '../components/icons'
import { Badge } from '../components/ui'
import SignInGate from '../components/SignInGate'
import { useStore } from '../lib/store'
import { view } from '../lib/issuance'
import { getBalance } from '../lib/openamp'
import { getStructure } from '../data/structures'

// A holding is a real on-chain balance: the atoms this AID holds of a
// SeqPal-managed asset, read from the policy server. openampd has no "list every
// asset I hold" endpoint, so we can only read balances for assets we know the id
// of. Those are the issuances this SeqPal ID has deployed (its own issuer
// treasury). Investor-side positions require the escrow and delivery rails,
// which are a later milestone, so they are honestly absent rather than faked.
export default function Holdings() {
  const { loading, isSignedIn, account, issuances } = useStore()
  const [rows, setRows] = useState(null) // null = loading

  useEffect(() => {
    if (!account) return
    let cancelled = false
    const live = (issuances || []).map(view).filter((i) => i.live && i.assetId)
    Promise.all(
      live.map((iss) =>
        getBalance(account.aid, iss.assetId)
          .then((b) => ({ iss, atoms: Number(b.atoms) || 0, error: false }))
          .catch(() => ({ iss, atoms: 0, error: true }))
      )
    ).then((r) => {
      if (!cancelled) setRows(r)
    })
    return () => {
      cancelled = true
    }
  }, [account, issuances])

  if (loading) {
    return (
      <section className="container-x flex justify-center py-24">
        <span className="h-8 w-8 animate-spin rounded-full border-4 border-btc/20 border-t-btc" />
      </section>
    )
  }

  if (!isSignedIn) {
    return (
      <SignInGate
        title="Sign in to see your holdings"
        body="Your holdings are read from the chain against your SeqPal ID, the identity and compliance passport that lets you hold SeqPal-managed assets at all."
        backTo="/id"
        backLabel="SeqPal ID home"
      />
    )
  }

  const held = (rows || []).filter((r) => r.atoms > 0)

  return (
    <section className="container-x py-12">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-3xl font-extrabold tracking-tight text-ink-900">My holdings</h1>
          <p className="mt-1 text-ink-700/80">
            On-chain balances for your enclave account,{' '}
            <span className="font-mono text-sm">
              {account.aid.slice(0, 8)}…{account.aid.slice(-6)}
            </span>
            .
          </p>
        </div>
        <Link to="/id" className="btn-outline">
          <Icon.id width={16} height={16} /> Manage SeqPal ID
        </Link>
      </div>

      <div className="mt-10">
        <h2 className="text-lg font-bold text-ink-900">Positions</h2>
        {rows === null ? (
          <div className="card mt-4 flex justify-center py-16">
            <span className="h-6 w-6 animate-spin rounded-full border-4 border-btc/20 border-t-btc" />
          </div>
        ) : held.length === 0 ? (
          <div className="card mt-4 flex flex-col items-center justify-center px-6 py-16 text-center">
            <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-ink-900/[0.04] text-ink-600">
              <Icon.wallet width={28} height={28} />
            </div>
            <h3 className="mt-5 text-lg font-bold text-ink-900">No holdings yet</h3>
            <p className="mt-2 max-w-md text-sm leading-relaxed text-ink-700/80">
              This account holds no SeqPal-managed asset that the policy server can see. When
              you deploy an asset it mints into this enclave and appears here. Investor
              delivery from a placement portal is a later milestone.
            </p>
            <Link to="/dashboard" className="btn-primary mt-6">
              Go to the issuer dashboard
              <Icon.arrowRight width={16} height={16} />
            </Link>
          </div>
        ) : (
          <div className="mt-4 space-y-3">
            {held.map(({ iss, atoms }) => {
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
                      s?.accent === 'seq' ? 'bg-seq/10 text-seq-600' : 'bg-btc-50 text-btc-600'
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
                      {s?.name || 'Structure not set'} · issuer treasury
                    </div>
                  </div>
                  <div className="text-right">
                    <div className="font-mono text-sm font-semibold text-ink-900">
                      {atoms.toLocaleString()} atoms
                    </div>
                    <div className="mt-1">
                      <Badge color="emerald">
                        <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" /> On chain
                      </Badge>
                    </div>
                  </div>
                </Link>
              )
            })}
            <p className="px-1 pt-1 text-xs leading-relaxed text-ink-700/55">
              Balances are confirmed-only figures from the policy server, shown in atoms.
              SeqPal does not yet track Bitcoin anchor depth, so nothing here is final at 0
              confirmations.
            </p>
          </div>
        )}
      </div>
    </section>
  )
}
