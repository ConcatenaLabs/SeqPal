import { Link } from 'react-router-dom'
import { Icon } from '../components/icons'
import { SectionHeading, Badge } from '../components/ui'

const products = [
  {
    icon: Icon.layers,
    name: 'Issuer Dashboard',
    tagline: 'Self-service issuance, end to end.',
    body: 'A web application that walks an issuer through entity formation in Próspera, choice of issuance structure, automated drafting of formation and offering documents, smart-contract configuration of jurisdictional and accreditation restrictions, and on-chain deployment.',
    points: [
      'Six-step onboarding flow',
      'Automated document drafting from your inputs',
      'Fixed, published pricing — pay and deploy',
      'Manage live issuances post-deployment',
    ],
    cta: { to: '/onboarding', label: 'Open the dashboard' },
  },
  {
    icon: Icon.id,
    name: 'SeqPal ID',
    tagline: 'A universal compliance passport.',
    body: 'A consolidated KYC/KYB and accreditation passport that, once established, automatically whitelists the holder for every SeqPal-issued asset for which their profile is compliant. The passport is read directly by the issued tokens’ smart contracts.',
    points: [
      'KYC / KYB and accreditation in one profile',
      'Sanctions and PEP screening on a monthly cadence',
      'Jurisdiction-aware accreditation logic',
      'Cryptographic eligibility claims consumed on-chain',
    ],
    cta: { to: '/id', label: 'Create a SeqPal ID' },
  },
  {
    icon: Icon.exchange,
    name: 'Automated Transfer Agent',
    tagline: 'The blockchain is your Registry of Members.',
    body: 'SeqPal acts as transfer agent and secretary for issued tokens. The blockchain itself is the official Registry of Members, and SeqPal’s services manage corporate actions, distributions in stablecoins or BTC, reissuances, and tax reporting.',
    points: [
      'Real-time canonical cap-table view',
      'Scheduled distributions and coupon runs',
      'Corporate actions: redemptions, lockup releases, voting proxies',
      'Year-end holder statements and Próspera tax filings',
    ],
    cta: { to: '/structures', label: 'See per-structure servicing' },
  },
  {
    icon: Icon.globe,
    name: 'Placement Portal',
    tagline: 'Your own branded fundraising portal.',
    body: 'A hosted technology and services stack that issuers use to run their own private placement portals on their own domain (e.g. invest.your-name.com). SeqPal supplies the software, the SeqPal ID identity gate, and the escrow services; the issuer is the legal operator.',
    points: [
      'Offering memorandum & version-controlled data room',
      'SeqPal ID identity and eligibility gate',
      'Subscription-agreement workflow and e-signature',
      'Escrow held under a tri-party agreement, released on closing',
    ],
    cta: { to: '/onboarding', label: 'Configure a portal' },
  },
]

export default function Products() {
  return (
    <>
      <section className="border-b border-ink-900/10 bg-ink-900/[0.02]">
        <div className="container-x py-16">
          <SectionHeading
            eyebrow="Products"
            title="The complete issuance stack"
            sub="Four tightly integrated products. Every third-party integration — KYC, e-signature, escrow, and the AMP issuance layer — is pre-built once, so the platform is plug-and-play for every issuer."
          />
        </div>
      </section>

      <section className="container-x py-16">
        <div className="space-y-6">
          {products.map((p, i) => (
            <div
              key={p.name}
              className="card grid gap-8 p-8 lg:grid-cols-[1.1fr_1fr] lg:p-10"
            >
              <div>
                <div className="flex items-center gap-3">
                  <div className="flex h-12 w-12 items-center justify-center rounded-xl bg-ink-900 text-white">
                    <p.icon width={24} height={24} />
                  </div>
                  <Badge color="slate">0{i + 1}</Badge>
                </div>
                <h2 className="mt-5 text-2xl font-extrabold tracking-tight text-ink-900">
                  {p.name}
                </h2>
                <p className="mt-1 font-semibold text-btc-600">{p.tagline}</p>
                <p className="mt-4 leading-relaxed text-ink-700/90">{p.body}</p>
                <Link to={p.cta.to} className="btn-outline mt-6">
                  {p.cta.label}
                  <Icon.arrowRight width={16} height={16} />
                </Link>
              </div>
              <div className="rounded-2xl bg-ink-900/[0.03] p-6">
                <div className="text-xs font-semibold uppercase tracking-wider text-ink-700/60">
                  What it does
                </div>
                <ul className="mt-4 space-y-3">
                  {p.points.map((pt) => (
                    <li key={pt} className="flex items-start gap-3">
                      <span className="mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-btc-50 text-btc-600">
                        <Icon.check width={13} height={13} />
                      </span>
                      <span className="text-sm text-ink-800">{pt}</span>
                    </li>
                  ))}
                </ul>
              </div>
            </div>
          ))}
        </div>
      </section>
    </>
  )
}
