import { Link, useParams } from 'react-router-dom'
import { Icon, StructureIcon } from '../components/icons'
import { Badge, DemoNote } from '../components/ui'
import { useStore } from '../lib/store'
import { getStructure } from '../data/structures'
import { JURISDICTIONS } from '../data/jurisdictions'

function Truncate({ value }) {
  return (
    <span className="font-mono text-xs text-ink-700">
      {value.slice(0, 10)}…{value.slice(-8)}
    </span>
  )
}

export default function IssuanceDetail() {
  const { id } = useParams()
  const { issuances } = useStore()
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

  const openJ = JURISDICTIONS.filter((j) => iss.policy?.[j.code] === 'open')
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
            <div className="mt-1 text-sm text-ink-700/80">{s?.name}</div>
          </div>
        </div>
        <Badge color="emerald">
          <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" /> Live on Liquid
        </Badge>
      </div>

      <DemoNote className="mt-6">
        This issuance was deployed in the demo. The asset id, transactions, and cap table
        below are illustrative — nothing was broadcast to a live network.
      </DemoNote>

      <div className="mt-8 grid gap-6 lg:grid-cols-3">
        {/* Asset card */}
        <div className="card p-6 lg:col-span-2">
          <h2 className="font-bold text-ink-900">Asset</h2>
          <dl className="mt-4 divide-y divide-ink-900/10 text-sm">
            {[
              ['Network', 'Bitcoin · Liquid Network'],
              ['Issuance layer', 'Blockstream AMP · Transfer-Restricted'],
              ['Asset id', <Truncate key="a" value={iss.assetId} />],
              ['Issuance txid', <Truncate key="t" value={iss.txid} />],
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

        {/* Transfer agent actions */}
        <div className="card p-6">
          <h2 className="font-bold text-ink-900">Transfer Agent</h2>
          <p className="mt-1 text-sm text-ink-700/70">
            The blockchain is the official Registry of Members.
          </p>
          <div className="mt-4 space-y-2">
            {[
              [Icon.coins, 'Schedule distribution'],
              [Icon.exchange, 'Process corporate action'],
              [Icon.doc, 'Export holder statements'],
            ].map(([I, label]) => (
              <button
                key={label}
                className="flex w-full items-center gap-3 rounded-lg border border-ink-900/10 px-3 py-2.5 text-left text-sm font-medium text-ink-800 hover:bg-ink-900/[0.02]"
              >
                <I width={18} height={18} className="text-ink-600" />
                {label}
                <Icon.arrowRight width={15} height={15} className="ml-auto text-ink-500" />
              </button>
            ))}
          </div>
        </div>
      </div>

      {/* Cap table + policy */}
      <div className="mt-6 grid gap-6 lg:grid-cols-2">
        <div className="card overflow-hidden">
          <div className="flex items-center justify-between border-b border-ink-900/10 px-6 py-4">
            <h2 className="font-bold text-ink-900">Registry of Members</h2>
            <span className="text-xs text-ink-700/60">snapshot</span>
          </div>
          <div className="divide-y divide-ink-900/10">
            {[
              ['Treasury (issuer wallet)', 'HN', '72.0%'],
              ['Investor — GAID ····8f2a', 'AE', '12.5%'],
              ['Investor — GAID ····1b03', 'SV', '9.5%'],
              ['Investor — GAID ····d77c', 'CH', '6.0%'],
            ].map(([who, jur, pct]) => (
              <div key={who} className="flex items-center justify-between px-6 py-3.5">
                <div className="flex items-center gap-3">
                  <span className="font-medium text-ink-900">{who}</span>
                  <Badge color="slate">{jur}</Badge>
                </div>
                <span className="font-mono text-sm text-ink-800">{pct}</span>
              </div>
            ))}
          </div>
        </div>

        <div className="card overflow-hidden">
          <div className="border-b border-ink-900/10 px-6 py-4">
            <h2 className="font-bold text-ink-900">Compliance policy</h2>
          </div>
          <div className="space-y-4 px-6 py-5 text-sm">
            <div>
              <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
                Open jurisdictions
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
    </section>
  )
}
