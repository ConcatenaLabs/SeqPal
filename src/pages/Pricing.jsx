import { Link } from 'react-router-dom'
import { Icon } from '../components/icons'
import { SectionHeading } from '../components/ui'
import {
  SETUP_FEES,
  ANNUAL_FEES,
  TRANSACTION_FEES,
  ID_FEES,
} from '../data/pricing'

function FeeTable({ title, rows }) {
  return (
    <div className="card overflow-hidden">
      <div className="border-b border-ink-900/10 bg-ink-900/[0.02] px-6 py-4">
        <h3 className="font-bold text-ink-900">{title}</h3>
      </div>
      <div className="divide-y divide-ink-900/10">
        {rows.map((r) => (
          <div
            key={r.item}
            className="flex items-start justify-between gap-6 px-6 py-4"
          >
            <div>
              <div className="font-medium text-ink-900">{r.item}</div>
              {r.detail && (
                <div className="mt-0.5 text-sm text-ink-700/70">{r.detail}</div>
              )}
            </div>
            <div className="shrink-0 whitespace-nowrap font-mono text-sm font-semibold text-ink-900">
              {r.amount}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

export default function Pricing() {
  return (
    <>
      <section className="border-b border-ink-900/10 bg-ink-900/[0.02]">
        <div className="container-x py-16">
          <SectionHeading
            eyebrow="Pricing"
            title="Fixed. Published. Transparent."
            sub="No quotes, no human sales call, no weeks of bespoke negotiation. Every fee below is the published price — the same for every issuer."
          />
          <div className="mt-8 flex flex-wrap gap-3">
            <Link to="/onboarding" className="btn-primary">
              Launch an issuance
              <Icon.arrowRight width={16} height={16} />
            </Link>
            <Link to="/structures" className="btn-outline">
              Compare structures
            </Link>
          </div>
        </div>
      </section>

      <section className="container-x py-16">
        <div className="grid gap-6 lg:grid-cols-2">
          <FeeTable title="Setup fees" rows={SETUP_FEES} />
          <FeeTable title="Annual support" rows={ANNUAL_FEES} />
          <FeeTable title="Transaction & servicing fees" rows={TRANSACTION_FEES} />
          <div className="flex flex-col gap-6">
            <FeeTable title="SeqPal ID" rows={ID_FEES} />
            <div className="card flex-1 p-6">
              <div className="flex h-11 w-11 items-center justify-center rounded-xl bg-btc-50 text-btc-600">
                <Icon.tag width={22} height={22} />
              </div>
              <h3 className="mt-4 font-bold text-ink-900">
                The Escrow & Settlement Fee, explained
              </h3>
              <p className="mt-2 text-sm leading-relaxed text-ink-700/90">
                A charge for the escrow custody, compliance-conditioned release, and
                on-chain settlement of the subscription funds SeqPal holds for your
                raise — 0.25% per month on the funds in escrow, accrued daily, with a
                $5,000 minimum and a cap of 3% of the funds held. It compensates a
                custody-and-settlement service performed, payable for the holding
                period whether or not the offering closes — not a placement
                commission. Over a typical multi-month raise it works out to roughly
                1% of the amount raised.
              </p>
            </div>
          </div>
        </div>

        <p className="mx-auto mt-10 max-w-3xl text-center text-sm leading-relaxed text-ink-700/70">
          Issuers electing a public offering should also expect costs outside the SeqPal
          fee schedule — an issuer audit or audit review, and per-jurisdiction
          local-counsel opinions or filings where applicable. These are borne by the
          issuer. SeqPal is not a law firm and does not provide legal advice.
        </p>
      </section>
    </>
  )
}
