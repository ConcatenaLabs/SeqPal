import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Icon, StructureIcon } from '../components/icons'
import CopyId from '../components/CopyId'
import { ChainChip } from '../components/ChainStatus'
import SignInGate from '../components/SignInGate'
import InvestorMandateCard from '../components/InvestorMandateCard'
import NoticesInbox from '../components/NoticesInbox'
import MarketAbuseGate from '../components/MarketAbuseGate'
import TransferConsole from '../components/TransferConsole'
import { useStore } from '../lib/store'
import { view } from '../lib/issuance'
import { getBalance } from '../lib/openamp'
import { getStructure } from '../data/structures'
import { fmtAssetAmount } from '../lib/format'

// A holding is a real on-chain balance: the atoms this AID holds of a
// SeqPal-managed asset, read from the policy server. openampd has no "list every
// asset I hold" endpoint, so we can only read balances for assets we know the id
// of. Those are the issuances this SeqPal ID has deployed (its own issuer
// treasury). A position delivered to a holder through another issuer's portal
// has no id we can enumerate here, so it is absent rather than faked.
export default function Holdings() {
  const { loading, isSignedIn, account, issuances, watchFor, hasEnclave } = useStore()
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
        body="Your holdings are read from the chain against your SeqPal ID, the identity and compliance passport for holding SeqPal-managed assets."
        backTo="/id"
        backLabel="SeqPal ID home"
      />
    )
  }

  const held = (rows || []).filter((r) => r.atoms > 0)
  const holdings = held.map(({ iss, atoms }) => ({
    assetId: iss.assetId,
    ticker: iss.ticker,
    name: iss.name,
    atoms,
    precision: iss.precision || 8,
  }))

  return (
    <section className="container-x py-12">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-3xl font-extrabold tracking-tight text-ink-900">My holdings</h1>
          <p className="mt-1 text-ink-700/80">
            {hasEnclave ? (
              <>
                On-chain balances for your enclave account,{' '}
                <span className="font-mono text-sm">
                  {account.aid.slice(0, 8)}…{account.aid.slice(-6)}
                </span>
                .
              </>
            ) : (
              <>
                This page shows balances held in an OpenAMP account. This SeqPal ID has none
                attached, so there is nothing here to show. A freely-tradable stock is an
                ordinary coin and appears in your own wallet. A network-enforced holding does
                not: it sits at its own covenant address, which ordinary wallet software neither
                displays nor spends, and the token's own page is where you see it.
              </>
            )}
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
              {hasEnclave ? (
                <>
                  This account holds no SeqPal-managed asset that the policy server can see. When
                  you deploy an asset it mints into this enclave and appears here. A position you
                  received through another issuer&rsquo;s portal is not enumerated on this page.
                </>
              ) : (
                <>
                  Nothing settles here for a SeqPal ID with no OpenAMP account attached. What you
                  hold in your own wallet is shown by that wallet, not by this page.
                </>
              )}
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
                // The card is a div, not a Link: the asset id carries its own
                // explorer anchor, and an anchor must not be nested inside one.
                // The name links to the issuance instead.
                <div
                  key={iss.id}
                  className="card flex items-center gap-4 p-5 transition-all hover:shadow-glow"
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
                      <Link
                        to={`/issuance/${iss.id}`}
                        className="truncate font-bold text-ink-900 hover:text-btc-600"
                      >
                        {iss.name}
                      </Link>
                      <span className="font-mono text-xs text-ink-700/60">{iss.ticker}</span>
                    </div>
                    <div className="mt-0.5 text-sm text-ink-700/70">
                      {s?.name || 'Structure not set'} · issuer treasury
                    </div>
                    {iss.assetId && (
                      <div className="mt-1">
                        <CopyId value={iss.assetId} kind="asset" label="asset id" />
                      </div>
                    )}
                  </div>
                  <div className="text-right">
                    <div className="font-mono text-sm font-semibold text-ink-900">
                      {fmtAssetAmount(atoms, iss.precision || 8, iss.ticker)}
                    </div>
                    <div className="font-mono text-[11px] text-ink-700/50">
                      {atoms.toLocaleString('en-US')} atoms
                    </div>
                    <div className="mt-1 flex justify-end">
                      <ChainChip watch={watchFor(iss.id)} />
                    </div>
                  </div>
                </div>
              )
            })}
            <p className="px-1 pt-1 text-xs leading-relaxed text-ink-700/55">
              Balances are confirmed-only figures from the policy server. The status chip shows
              Bitcoin anchor depth from the live chain watcher: a state is only as final as its
              Bitcoin anchor is deep, so nothing here is final at 0 confirmations.
            </p>
          </div>
        )}
      </div>

      <div className="mt-10">
        <h2 className="text-lg font-bold text-ink-900">Transfer to another holder</h2>
        <p className="mt-1 text-sm text-ink-700/70">
          Send a restricted asset to another SeqPal identity. Every transfer is co-signed by the
          policy server, which can refuse an ineligible recipient, a resale inside the lockup window,
          or a Reg S window. The refusal, when it happens, is shown here in full.
        </p>
        <div className="mt-4">
          <MarketAbuseGate>
            <TransferConsole holdings={holdings} />
          </MarketAbuseGate>
        </div>
      </div>

      <div className="mt-10">
        <h2 className="text-lg font-bold text-ink-900">Distributions and servicing</h2>
        <p className="mt-1 text-sm text-ink-700/70">
          Register the ordinary Sequentia address where distributions pay you, and read your
          servicing notices. Statements and reports link to their anchored document.
        </p>
        <div className="mt-4 grid gap-6 lg:grid-cols-2">
          <InvestorMandateCard />
          <NoticesInbox />
        </div>
      </div>
    </section>
  )
}
