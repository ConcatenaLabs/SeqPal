import { Link } from 'react-router-dom'
import { Icon } from '../components/icons'
import { SectionHeading, Badge } from '../components/ui'

const products = [
  {
    icon: Icon.layers,
    name: 'Issuer Dashboard',
    tagline: 'Self-service issuance, end to end.',
    body: 'A web application that walks an issuer through entity formation in Próspera, choice of issuance structure, automated drafting of formation and offering documents, configuration of jurisdictional and accreditation transfer restrictions, and on-chain deployment.',
    points: [
      'Six-step onboarding flow',
      'Automated document drafting from your inputs',
      'Fixed, published pricing, pay and deploy',
      'Manage live issuances post-deployment',
    ],
    cta: { to: '/onboarding', label: 'Open the dashboard' },
  },
  {
    icon: Icon.id,
    name: 'SeqPal ID',
    tagline: 'A universal compliance passport, for anyone.',
    body: 'The identity and compliance passport for the whole SeqPal-on-Sequentia world, not an issuer login. One role-neutral registration flow: it never asks whether you intend to issue or invest. Once verified, it carries the eligibility categories the policy server enforces on every transfer, so the holder is whitelisted for every SeqPal-managed asset their profile is eligible to hold, and needs it to hold or trade one anywhere on Sequentia at all.',
    points: [
      'One flow for everyone, no user-type question',
      'KYC / KYB and accreditation in one profile',
      'Real sanctions screening on a daily schedule; PEP and adverse-media labeled-simulated',
      'Eligibility categories consumed on-chain, honored by venues that list SeqPal assets',
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
      'Escrow in fiat (segregated bank account, tri-party agreement) or BTC / USDX (segregated wallets), released on closing',
    ],
    cta: { to: '/onboarding', label: 'Configure a portal' },
  },
]

function Architecture() {
  const stages = [
    {
      icon: Icon.layers,
      n: '1',
      name: 'Issuer Dashboard',
      body: 'Structure, draft, configure, and deploy in six steps.',
    },
    {
      icon: Icon.exchange,
      n: '2',
      name: 'Automated Transfer Agent',
      body: 'Registry of Members, distributions, corporate actions.',
    },
    {
      icon: Icon.globe,
      n: '3',
      name: 'Placement Portal',
      body: 'Raise into escrow on your domain; deliver tokens on closing.',
    },
  ]
  return (
    <div className="mt-12">
      <div className="text-xs font-semibold uppercase tracking-[0.18em] text-ink-700/50">
        How it fits together
      </div>
      <div className="mt-4 rounded-2xl border border-ink-900/10 bg-white p-5 shadow-card sm:p-7">
        {/* identity layer */}
        <div className="flex items-center gap-3 rounded-xl bg-seq/10 px-4 py-3 text-seq-600">
          <Icon.id width={20} height={20} className="shrink-0" />
          <div className="text-sm">
            <span className="font-bold text-ink-900">SeqPal ID</span>
            <span className="text-ink-700/80">
              {' '}
              · identity and eligibility, verified once and read on every transfer
            </span>
          </div>
        </div>

        {/* lifecycle stages */}
        <div className="my-3 flex flex-col items-stretch gap-3 lg:flex-row lg:items-center">
          {stages.map((s, i) => (
            <div key={s.name} className="flex items-center gap-3 lg:flex-1">
              <div className="flex flex-1 items-start gap-3 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] p-4">
                <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-ink-900 text-white">
                  <s.icon width={18} height={18} />
                </div>
                <div>
                  <div className="text-sm font-bold text-ink-900">{s.name}</div>
                  <div className="mt-0.5 text-xs leading-snug text-ink-700/80">
                    {s.body}
                  </div>
                </div>
              </div>
              {i < stages.length - 1 && (
                <Icon.arrowRight
                  width={18}
                  height={18}
                  className="hidden shrink-0 text-ink-500 lg:block"
                />
              )}
            </div>
          ))}
        </div>

        {/* settlement layer */}
        <div className="flex items-center gap-3 rounded-xl bg-btc-50 px-4 py-3 text-btc-700">
          <Icon.bitcoin width={20} height={20} className="shrink-0" />
          <div className="text-sm">
            <span className="font-bold text-ink-900">Bitcoin · Sequentia</span>
            <span className="text-ink-700/80">
              {': '}
              Transfer-Restricted assets issued via OpenAMP, settled in BTC or
              stablecoins
            </span>
          </div>
        </div>
      </div>
    </div>
  )
}

export default function Products() {
  return (
    <>
      <section className="border-b border-ink-900/10 bg-ink-900/[0.02]">
        <div className="container-x py-16">
          <SectionHeading
            eyebrow="Products"
            title="The complete issuance stack"
            sub="Four tightly integrated products. Every third-party integration, KYC, e-signature, escrow, and the OpenAMP issuance layer, is pre-built once, so the platform is plug-and-play for every issuer."
          />
          <Architecture />
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
