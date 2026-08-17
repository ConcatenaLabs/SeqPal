import { Link } from 'react-router-dom'
import Logo from './Logo'

const cols = [
  {
    title: 'Product',
    links: [
      { to: '/products', label: 'Issuer Dashboard' },
      { to: '/id', label: 'SeqPal ID' },
      { to: '/products', label: 'Automated Transfer Agent' },
      { to: '/products', label: 'Placement Portal' },
    ],
  },
  {
    title: 'Issuance',
    links: [
      { to: '/structures', label: 'Native Equity' },
      { to: '/structures', label: 'Equity SPV' },
      { to: '/structures', label: 'Debt / Yield' },
      { to: '/structures', label: 'Depository Receipts' },
    ],
  },
  {
    title: 'Resources',
    links: [
      { to: '/pricing', label: 'Pricing' },
      { to: '/docs', label: 'Documentation' },
      { to: '/docs/verify', label: 'Verify independently' },
      { to: '/onboarding', label: 'Start an issuance' },
      { to: '/dashboard', label: 'Dashboard' },
    ],
  },
  {
    title: 'Trust and legal',
    links: [
      { to: '/faq', label: 'FAQ' },
      { to: '/legal', label: 'Legal & Licensing' },
      { to: '/privacy', label: 'Privacy' },
      { to: '/status', label: 'Status' },
    ],
  },
]

export default function Footer() {
  return (
    <footer className="mt-24 border-t border-ink-900/10 bg-ink-950 text-white">
      <div className="container-x py-14">
        <div className="grid gap-10 md:grid-cols-2 lg:grid-cols-6">
          <div className="lg:col-span-2">
            <Logo light />
            <p className="mt-4 max-w-xs text-sm leading-relaxed text-white/60">
              End-to-end tokenization-as-a-service on Sequentia, a Bitcoin sidechain.
              Structure, issue, and service transfer-restricted security tokens in days.
            </p>
            <div className="mt-5 flex flex-wrap gap-2 text-xs text-white/50">
              <span className="rounded-full border border-white/15 px-2.5 py-1">
                Próspera ZEDE
              </span>
              <span className="rounded-full border border-white/15 px-2.5 py-1">
                Sequentia
              </span>
              <span className="rounded-full border border-white/15 px-2.5 py-1">
                Bitcoin anchored
              </span>
            </div>
          </div>

          {cols.map((c) => (
            <div key={c.title}>
              <h4 className="text-sm font-semibold text-white">{c.title}</h4>
              <ul className="mt-4 space-y-2.5">
                {c.links.map((l, i) => (
                  <li key={i}>
                    <Link
                      to={l.to}
                      className="text-sm text-white/60 transition-colors hover:text-btc"
                    >
                      {l.label}
                    </Link>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>

        <div className="mt-12 border-t border-white/10 pt-7 text-xs leading-relaxed text-white/40">
          <p className="max-w-3xl">
            SeqPal is a technology and services provider. SeqPal is not a law firm
            and does not provide legal, tax, or investment advice. On each issuance
            the issuer is the principal and is responsible for its offering;
            SeqPal acts only as enforcement agent operating to the issuer&apos;s
            signed-off configuration. Nothing on this site is an offer to sell or a
            solicitation to buy any security.
          </p>
          <p className="mt-4">
            © {new Date().getFullYear()} SeqPal · seqpal.io ·{' '}
            <span className="text-btc/80">demo preview</span>
          </p>
        </div>
      </div>
    </footer>
  )
}
