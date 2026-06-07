import { Link } from 'react-router-dom'
import { Icon } from '../components/icons'
import { SectionHeading, Badge } from '../components/ui'
import { STRUCTURES } from '../data/structures'
import { StructureIcon } from '../components/icons'

const pillars = [
  {
    icon: Icon.shield,
    title: 'Smart Compliance',
    body: 'Investor eligibility is enforced at the token-contract level — jurisdiction, accreditation, and sanctions screening baked in. Verify once with SeqPal ID, hold every asset you qualify for.',
  },
  {
    icon: Icon.users,
    title: 'Consistently User-Centric',
    body: 'A self-service dashboard takes you from KYB to live deployment with no required human sales call. Fixed, published pricing — no quotes, no weeks of bespoke negotiation.',
  },
  {
    icon: Icon.bitcoin,
    title: 'Strategically Bitcoin-First',
    body: 'Every asset is issued on Bitcoin’s Liquid Network via Blockstream AMP. Confidential transactions, sub-cent fees, and distributions in BTC or stablecoins.',
  },
]

const products = [
  {
    icon: Icon.layers,
    name: 'Issuer Dashboard',
    body: 'A six-step, self-service flow: entity formation, structure choice, automated document drafting, smart-contract configuration, and on-chain deployment.',
  },
  {
    icon: Icon.id,
    name: 'SeqPal ID',
    body: 'A consolidated KYC/KYB and accreditation passport — and your login. Established once, it whitelists the holder for every SeqPal-issued asset their profile is eligible to hold.',
  },
  {
    icon: Icon.exchange,
    name: 'Automated Transfer Agent',
    body: 'The blockchain is the official Registry of Members. SeqPal manages corporate actions, distributions, reissuances, and tax reporting.',
  },
  {
    icon: Icon.globe,
    name: 'Placement Portal',
    body: 'Brandable fundraising portal software on your own domain — offering memorandum, data room, subscription workflow, and escrow, gated by SeqPal ID.',
  },
]

const steps = [
  ['Identity & principal', 'Sign in with your SeqPal ID and choose who is issuing.'],
  ['Choose a structure', 'Pick one of four issuance structures.'],
  ['Data room', 'Enter your deal terms in dynamic forms.'],
  ['Document automation', 'Templated paperwork generated for review & e-signature.'],
  ['Tokenomics & compliance', 'Bake jurisdiction and accreditation rules into the token.'],
  ['Checkout & deploy', 'Pay a fixed fee; your Próspera LLC is incorporated and the asset deploys.'],
]

export default function Home() {
  return (
    <>
      {/* Hero */}
      <section className="relative overflow-hidden bg-ink-950 text-white">
        <div className="absolute inset-0 grid-bg opacity-60" />
        <div className="absolute -left-40 -top-40 h-96 w-96 rounded-full bg-btc/20 blur-3xl" />
        <div className="absolute -right-32 top-20 h-96 w-96 rounded-full bg-liquid/20 blur-3xl" />
        <div className="container-x relative grid gap-12 py-20 lg:grid-cols-2 lg:items-center lg:py-28">
          <div>
            <span className="inline-flex items-center gap-2 rounded-full border border-white/15 bg-white/5 px-3 py-1.5 text-xs font-medium text-white/80">
              <span className="h-1.5 w-1.5 rounded-full bg-btc" />
              Domiciled in Próspera ZEDE · built on the Liquid Network
            </span>
            <h1 className="mt-6 text-4xl font-extrabold leading-[1.05] tracking-tight sm:text-5xl lg:text-6xl">
              Tokenize real-world assets in{' '}
              <span className="text-btc">days</span>, not months.
            </h1>
            <p className="mt-6 max-w-xl text-lg leading-relaxed text-white/70">
              SeqPal is an end-to-end tokenization-as-a-service platform. Structure,
              issue, and service compliant security tokens on Bitcoin — at fixed,
              transparent prices, with no required human sales call.
            </p>
            <div className="mt-8 flex flex-wrap gap-3">
              <Link to="/onboarding" className="btn-primary px-5 py-3 text-base">
                Launch an issuance
                <Icon.arrowRight width={18} height={18} />
              </Link>
              <Link
                to="/structures"
                className="btn px-5 py-3 text-base text-white ring-1 ring-inset ring-white/20 hover:bg-white/10"
              >
                Explore structures
              </Link>
            </div>
            <div className="mt-10 flex flex-wrap items-center gap-x-8 gap-y-4 text-sm text-white/60">
              <span className="inline-flex items-center gap-2">
                <Icon.check width={16} height={16} className="text-btc" /> Live in 3–7
                business days
              </span>
              <span className="inline-flex items-center gap-2">
                <Icon.check width={16} height={16} className="text-btc" /> Fixed,
                published pricing
              </span>
              <span className="inline-flex items-center gap-2">
                <Icon.check width={16} height={16} className="text-btc" /> Open
                secondary liquidity
              </span>
            </div>
          </div>

          {/* Mock dashboard card */}
          <div className="relative">
            <div className="rounded-2xl border border-white/10 bg-white/5 p-2 shadow-2xl backdrop-blur">
              <div className="rounded-xl bg-white p-5 text-ink-900">
                <div className="flex items-center justify-between border-b border-ink-900/10 pb-3">
                  <div className="flex items-center gap-2 text-sm font-semibold">
                    <Icon.layers width={18} height={18} className="text-btc" />
                    New issuance
                  </div>
                  <Badge color="emerald">
                    <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" /> Step 4 of 6
                  </Badge>
                </div>
                <div className="mt-4 space-y-3">
                  <div className="flex items-center justify-between rounded-lg bg-ink-900/[0.03] px-3 py-2.5 text-sm">
                    <span className="text-ink-700">Structure</span>
                    <span className="font-semibold">Equity SPV</span>
                  </div>
                  <div className="flex items-center justify-between rounded-lg bg-ink-900/[0.03] px-3 py-2.5 text-sm">
                    <span className="text-ink-700">Target raise</span>
                    <span className="font-semibold">$5,000,000</span>
                  </div>
                  <div className="flex items-center justify-between rounded-lg bg-ink-900/[0.03] px-3 py-2.5 text-sm">
                    <span className="text-ink-700">Network</span>
                    <span className="font-mono text-xs font-semibold text-liquid-600">
                      Liquid · AMP
                    </span>
                  </div>
                  <div className="rounded-lg border border-btc/20 bg-btc-50 px-3 py-2.5">
                    <div className="flex items-center justify-between text-sm">
                      <span className="font-medium text-btc-700">
                        Documents generated
                      </span>
                      <Icon.check width={16} height={16} className="text-btc-600" />
                    </div>
                    <div className="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-btc/15">
                      <div className="h-full w-2/3 rounded-full bg-btc" />
                    </div>
                  </div>
                </div>
                <button className="btn-primary mt-4 w-full">
                  Continue to compliance
                  <Icon.arrowRight width={16} height={16} />
                </button>
              </div>
            </div>
            <div className="absolute -bottom-5 -left-5 hidden rounded-xl border border-white/10 bg-ink-900 px-4 py-3 shadow-xl sm:block">
              <div className="text-xs text-white/50">SeqPal ID</div>
              <div className="mt-0.5 flex items-center gap-2 text-sm font-semibold text-white">
                <Icon.shield width={16} height={16} className="text-liquid" />
                Eligibility verified
              </div>
            </div>
          </div>
        </div>
      </section>

      {/* Pillars */}
      <section className="container-x py-20">
        <SectionHeading
          center
          eyebrow="Why SeqPal"
          title="Three commandments"
          sub="The principles behind every issuance on the platform."
        />
        <div className="mt-12 grid gap-6 md:grid-cols-3">
          {pillars.map((p) => (
            <div key={p.title} className="card p-7">
              <div className="flex h-12 w-12 items-center justify-center rounded-xl bg-btc-50 text-btc-600">
                <p.icon width={24} height={24} />
              </div>
              <h3 className="mt-5 text-lg font-bold text-ink-900">{p.title}</h3>
              <p className="mt-2 text-sm leading-relaxed text-ink-700/90">{p.body}</p>
            </div>
          ))}
        </div>
      </section>

      {/* Products */}
      <section className="border-y border-ink-900/10 bg-ink-900/[0.02]">
        <div className="container-x py-20">
          <SectionHeading
            eyebrow="The platform"
            title="One platform, four products"
            sub="Everything an issuer needs to take an asset from formation to live secondary trading."
          />
          <div className="mt-12 grid gap-6 sm:grid-cols-2">
            {products.map((p) => (
              <div key={p.name} className="card flex gap-5 p-7">
                <div className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl bg-ink-900 text-white">
                  <p.icon width={22} height={22} />
                </div>
                <div>
                  <h3 className="text-lg font-bold text-ink-900">{p.name}</h3>
                  <p className="mt-1.5 text-sm leading-relaxed text-ink-700/90">
                    {p.body}
                  </p>
                </div>
              </div>
            ))}
          </div>
          <div className="mt-8">
            <Link to="/products" className="btn-outline">
              See how the platform fits together
              <Icon.arrowRight width={16} height={16} />
            </Link>
          </div>
        </div>
      </section>

      {/* Structures */}
      <section className="container-x py-20">
        <SectionHeading
          eyebrow="Issuance structures"
          title="Four structures, one workflow"
          sub="Each backed by a Próspera LLC and templated paperwork. Pick the one that matches your asset."
        />
        <div className="mt-12 grid gap-5 sm:grid-cols-2">
          {STRUCTURES.map((s) => {
            const Ic = StructureIcon[s.icon]
            return (
              <Link
                key={s.id}
                to="/structures"
                className="card group p-7 transition-all hover:-translate-y-0.5 hover:shadow-glow"
              >
                <div className="flex items-start justify-between">
                  <div
                    className={`flex h-11 w-11 items-center justify-center rounded-xl ${
                      s.accent === 'btc'
                        ? 'bg-btc-50 text-btc-600'
                        : 'bg-liquid/10 text-liquid-600'
                    }`}
                  >
                    <Ic width={22} height={22} />
                  </div>
                  <span className="text-sm font-semibold text-ink-700">
                    from {s.setup.label}
                  </span>
                </div>
                <h3 className="mt-5 text-lg font-bold text-ink-900">{s.name}</h3>
                <p className="mt-2 text-sm leading-relaxed text-ink-700/90">{s.claim}</p>
                <div className="mt-4 flex items-center gap-2 text-sm font-semibold text-btc-600 opacity-0 transition-opacity group-hover:opacity-100">
                  Learn more <Icon.arrowRight width={15} height={15} />
                </div>
              </Link>
            )
          })}
        </div>
      </section>

      {/* How it works */}
      <section className="border-y border-ink-900/10 bg-ink-950 text-white">
        <div className="container-x py-20">
          <SectionHeading
            className="[&_h2]:text-white [&_p]:text-white/70"
            eyebrow="The flow"
            title="From KYB to live deployment in six steps"
            sub="The Issuer Dashboard walks you through the entire issuance — no spreadsheets, no bespoke negotiation."
          />
          <ol className="mt-12 grid gap-5 sm:grid-cols-2 lg:grid-cols-3">
            {steps.map(([t, d], i) => (
              <li
                key={t}
                className="rounded-2xl border border-white/10 bg-white/5 p-6"
              >
                <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-btc font-bold text-white">
                  {i + 1}
                </div>
                <h3 className="mt-4 font-semibold text-white">{t}</h3>
                <p className="mt-1.5 text-sm leading-relaxed text-white/60">{d}</p>
              </li>
            ))}
          </ol>
          <div className="mt-10">
            <Link to="/onboarding" className="btn-primary px-5 py-3 text-base">
              Try the onboarding flow
              <Icon.arrowRight width={18} height={18} />
            </Link>
          </div>
        </div>
      </section>

      {/* Liquidity */}
      <section className="container-x py-20">
        <div className="grid gap-12 lg:grid-cols-2 lg:items-center">
          <div>
            <SectionHeading
              eyebrow="Open liquidity"
              title="Compliance at the contract level — liquidity in the open market"
              sub="SeqPal embeds investor eligibility checks directly into each token, so SeqPal-issued assets can trade freely on any vetted public DEX rather than being locked inside a proprietary trading system."
            />
            <ul className="mt-8 space-y-4">
              {[
                'Transfer-Restricted assets issued via Blockstream AMP',
                'Whitelist enforced by SeqPal ID at the protocol level',
                'Discoverable and tradable on Liquid-native venues such as SideSwap, with asset-level KYC',
                'Distributions paid pro-rata in BTC or stablecoins',
              ].map((t) => (
                <li key={t} className="flex items-start gap-3">
                  <span className="mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-btc-50 text-btc-600">
                    <Icon.check width={13} height={13} />
                  </span>
                  <span className="text-ink-800">{t}</span>
                </li>
              ))}
            </ul>
          </div>
          <div className="card overflow-hidden">
            <div className="border-b border-ink-900/10 bg-ink-900/[0.02] px-6 py-4">
              <div className="flex items-center gap-2 text-sm font-semibold text-ink-800">
                <Icon.wallet width={18} height={18} className="text-liquid-600" />
                Holder portfolio
              </div>
            </div>
            <div className="divide-y divide-ink-900/10">
              {[
                ['ACME SPV-A', 'Equity SPV', 'Eligible', 'emerald'],
                ['Trade Finance Note', 'Debt / Yield', 'Eligible', 'emerald'],
                ['Liberty Holdings', 'Native Equity', 'Eligible', 'emerald'],
                ['Tokenized SPY', 'Depository Receipt', 'Accredited only', 'amber'],
              ].map(([n, s, st, c]) => (
                <div key={n} className="flex items-center justify-between px-6 py-4">
                  <div>
                    <div className="font-semibold text-ink-900">{n}</div>
                    <div className="text-xs text-ink-700/70">{s}</div>
                  </div>
                  <Badge color={c}>{st}</Badge>
                </div>
              ))}
            </div>
            <div className="bg-ink-900/[0.02] px-6 py-4 text-center text-xs text-ink-700/70">
              One SeqPal ID · every asset you qualify for, auto-whitelisted.
            </div>
          </div>
        </div>
      </section>

      {/* CTA */}
      <section className="container-x pb-4">
        <div className="relative overflow-hidden rounded-3xl bg-ink-950 px-8 py-16 text-center text-white sm:px-16">
          <div className="absolute -left-20 -top-20 h-72 w-72 rounded-full bg-btc/20 blur-3xl" />
          <div className="absolute -bottom-20 -right-20 h-72 w-72 rounded-full bg-liquid/20 blur-3xl" />
          <div className="relative">
            <h2 className="text-3xl font-extrabold tracking-tight sm:text-4xl">
              Ready to issue your asset?
            </h2>
            <p className="mx-auto mt-4 max-w-xl text-lg text-white/70">
              Walk through the full issuer onboarding flow and see your token go from
              formation to deployment.
            </p>
            <div className="mt-8 flex flex-wrap justify-center gap-3">
              <Link to="/onboarding" className="btn-primary px-6 py-3 text-base">
                Launch an issuance
                <Icon.arrowRight width={18} height={18} />
              </Link>
              <Link
                to="/pricing"
                className="btn px-6 py-3 text-base text-white ring-1 ring-inset ring-white/20 hover:bg-white/10"
              >
                View pricing
              </Link>
            </div>
          </div>
        </div>
      </section>
    </>
  )
}
