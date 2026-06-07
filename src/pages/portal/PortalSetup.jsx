import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import Logo from '../../components/Logo'
import { Icon } from '../../components/icons'
import { Badge, DemoNote } from '../../components/ui'
import SignInGate from '../../components/SignInGate'
import { useStore, slugify } from '../../lib/store'
import { getStructure } from '../../data/structures'

const ACCENTS = [
  { id: 'btc', label: 'Bitcoin', hex: '#F7931A' },
  { id: 'liquid', label: 'Liquid', hex: '#27c2c9' },
  { id: 'indigo', label: 'Indigo', hex: '#4f46e5' },
  { id: 'emerald', label: 'Emerald', hex: '#059669' },
  { id: 'slate', label: 'Slate', hex: '#334155' },
]

function dataRoomDocs(structureId) {
  const base = ['Offering Memorandum', 'Term Sheet', 'Subscription Agreement']
  if (structureId === 'equity-spv') base.push('Proof-of-Position attestation')
  if (structureId === 'debt-yield')
    base.push('Note Purchase Agreement', 'Borrower disclosure pack')
  if (structureId === 'native-equity') base.push('Cap table summary')
  return base
}

function PreviewHero({ cfg, iss }) {
  const accent = ACCENTS.find((a) => a.id === cfg.accent) || ACCENTS[0]
  return (
    <div className="overflow-hidden rounded-xl border border-ink-900/10 bg-white shadow-card">
      {/* faux browser chrome */}
      <div className="flex items-center gap-2 border-b border-ink-900/10 bg-ink-900/[0.03] px-3 py-2">
        <span className="h-2.5 w-2.5 rounded-full bg-ink-900/15" />
        <span className="h-2.5 w-2.5 rounded-full bg-ink-900/15" />
        <span className="h-2.5 w-2.5 rounded-full bg-ink-900/15" />
        <span className="ml-2 truncate rounded bg-white px-2 py-0.5 font-mono text-[11px] text-ink-700/70">
          invest.{cfg.slug || 'your-name'}.com
        </span>
      </div>
      <div className="p-5">
        <div className="flex items-center gap-2">
          <span
            className="flex h-7 w-7 items-center justify-center rounded-lg text-xs font-bold text-white"
            style={{ background: accent.hex }}
          >
            {(cfg.brandName || 'A').slice(0, 1).toUpperCase()}
          </span>
          <span className="text-sm font-bold text-ink-900">
            {cfg.brandName || 'Your brand'}
          </span>
        </div>
        <h3 className="mt-4 text-lg font-extrabold leading-tight text-ink-900">
          {cfg.headline || `Invest in ${iss.name}`}
        </h3>
        <p className="mt-1.5 text-xs text-ink-700/70">
          {getStructure(iss.structureId)?.name} · {iss.ticker}
        </p>
        <button
          className="mt-4 w-full rounded-lg py-2 text-sm font-semibold text-white"
          style={{ background: accent.hex }}
        >
          Verify with SeqPal ID to invest
        </button>
        <p className="mt-3 text-center text-[10px] text-ink-700/50">
          Operated by {iss.principal?.name} · powered by SeqPal
        </p>
      </div>
    </div>
  )
}

export default function PortalSetup() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { isLoggedIn, issuances, updateIssuance } = useStore()

  if (!isLoggedIn) return <SignInGate />
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

  const existing = iss.portal || {}
  const docs = dataRoomDocs(iss.structureId)
  const [cfg, setCfg] = useState({
    brandName: existing.brandName || iss.principal?.name || iss.name,
    headline: existing.headline || `Invest in ${iss.name}`,
    accent: existing.accent || 'btc',
    slug: existing.slug || slugify(iss.principal?.name || iss.name),
    docs: existing.docs || docs,
    minInvestment: existing.minInvestment || '25,000',
    escrowRequested: existing.escrowRequested || false,
    tosAccepted: existing.tosAccepted || false,
  })

  const set = (patch) => setCfg((c) => ({ ...c, ...patch }))
  const toggleDoc = (d) =>
    set({ docs: cfg.docs.includes(d) ? cfg.docs.filter((x) => x !== d) : [...cfg.docs, d] })

  const canPublish = cfg.brandName && cfg.slug && cfg.escrowRequested && cfg.tosAccepted

  const publish = () => {
    updateIssuance(iss.id, {
      portal: { ...cfg, configured: true, published: true },
    })
    navigate(`/portal/${iss.id}`)
  }

  return (
    <div className="min-h-screen bg-ink-900/[0.03]">
      <header className="sticky top-0 z-30 border-b border-ink-900/10 bg-white">
        <div className="container-x flex h-16 items-center justify-between">
          <Link to="/">
            <Logo />
          </Link>
          <div className="hidden text-sm font-medium text-ink-700 sm:block">
            Placement portal setup · {iss.name}
          </div>
          <Link to={`/issuance/${iss.id}`} className="btn-ghost text-ink-700">
            <Icon.close width={16} height={16} /> Exit
          </Link>
        </div>
      </header>

      <div className="container-x grid gap-8 py-10 lg:grid-cols-[1.4fr_1fr]">
        {/* config */}
        <div className="space-y-6">
          <div>
            <h1 className="text-2xl font-extrabold tracking-tight text-ink-900">
              Set up your placement portal
            </h1>
            <p className="mt-2 max-w-2xl leading-relaxed text-ink-700/90">
              The portal is brandable software that runs on SeqPal’s stack but is operated
              in <span className="font-semibold">your</span> name, on your own domain and
              terms of service. SeqPal supplies the software, the SeqPal ID gate, and the
              escrow — you are the legal operator of the portal.
            </p>
          </div>

          {/* Branding */}
          <section className="card p-6">
            <div className="flex items-center gap-2 font-bold text-ink-900">
              <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-ink-900 text-xs font-bold text-white">
                1
              </span>
              Branding
            </div>
            <div className="mt-4 grid gap-4 sm:grid-cols-2">
              <div>
                <label className="label">Brand / portal name</label>
                <input
                  className="input"
                  value={cfg.brandName}
                  onChange={(e) => set({ brandName: e.target.value })}
                />
              </div>
              <div>
                <label className="label">Accent colour</label>
                <div className="flex gap-2 pt-1">
                  {ACCENTS.map((a) => (
                    <button
                      key={a.id}
                      onClick={() => set({ accent: a.id })}
                      className={`h-9 w-9 rounded-full ring-2 ring-offset-2 transition ${
                        cfg.accent === a.id ? 'ring-ink-900' : 'ring-transparent'
                      }`}
                      style={{ background: a.hex }}
                      aria-label={a.label}
                    />
                  ))}
                </div>
              </div>
              <div className="sm:col-span-2">
                <label className="label">Headline</label>
                <input
                  className="input"
                  value={cfg.headline}
                  onChange={(e) => set({ headline: e.target.value })}
                />
              </div>
            </div>
          </section>

          {/* Domain */}
          <section className="card p-6">
            <div className="flex items-center gap-2 font-bold text-ink-900">
              <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-ink-900 text-xs font-bold text-white">
                2
              </span>
              Domain & hosting
            </div>
            <div className="mt-4">
              <label className="label">Your subdomain</label>
              <div className="flex items-center gap-2">
                <span className="font-mono text-sm text-ink-700/60">invest.</span>
                <input
                  className="input max-w-[180px]"
                  value={cfg.slug}
                  onChange={(e) => set({ slug: slugify(e.target.value) })}
                />
                <span className="font-mono text-sm text-ink-700/60">.com</span>
              </div>
              <div className="mt-4 rounded-lg bg-ink-900/[0.03] p-4 text-sm text-ink-700/80">
                Point your domain at the pre-integrated stack with a single CNAME record —
                SeqPal facilitates the hosting agreement through the dashboard, so you
                don’t procure or run any infrastructure.
                <div className="mt-2 rounded-md bg-ink-950 px-3 py-2 font-mono text-xs text-white/80">
                  invest.{cfg.slug || 'your-name'}.com&nbsp;&nbsp;CNAME&nbsp;&nbsp;portals.seqpal.io
                </div>
              </div>
              <DemoNote className="mt-3">DNS / hosting is mocked in the demo.</DemoNote>
            </div>
          </section>

          {/* Offering content */}
          <section className="card p-6">
            <div className="flex items-center gap-2 font-bold text-ink-900">
              <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-ink-900 text-xs font-bold text-white">
                3
              </span>
              Offering & data room
            </div>
            <p className="mt-2 text-sm text-ink-700/70">
              Choose which documents investors see — only after they clear the SeqPal ID
              gate.
            </p>
            <div className="mt-4 space-y-2">
              {docs.map((d) => (
                <label
                  key={d}
                  className="flex cursor-pointer items-center gap-3 rounded-lg border border-ink-900/10 px-3 py-2.5"
                >
                  <input
                    type="checkbox"
                    checked={cfg.docs.includes(d)}
                    onChange={() => toggleDoc(d)}
                    className="h-4 w-4 accent-btc"
                  />
                  <Icon.doc width={16} height={16} className="text-ink-600" />
                  <span className="text-sm font-medium text-ink-900">{d}</span>
                </label>
              ))}
            </div>
            <div className="mt-4 max-w-[220px]">
              <label className="label">Minimum investment</label>
              <div className="relative">
                <span className="pointer-events-none absolute left-3.5 top-1/2 -translate-y-1/2 text-sm text-ink-700/60">
                  $
                </span>
                <input
                  className="input pl-7"
                  value={cfg.minInvestment}
                  onChange={(e) => set({ minInvestment: e.target.value })}
                />
              </div>
            </div>
          </section>

          {/* Terms & escrow */}
          <section className="card p-6">
            <div className="flex items-center gap-2 font-bold text-ink-900">
              <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-ink-900 text-xs font-bold text-white">
                4
              </span>
              Investor terms & escrow
            </div>
            <button
              onClick={() => set({ escrowRequested: !cfg.escrowRequested })}
              className={`mt-4 flex w-full items-center gap-3 rounded-xl border p-4 text-left transition ${
                cfg.escrowRequested
                  ? 'border-emerald-300 bg-emerald-50'
                  : 'border-ink-900/15 hover:bg-ink-900/[0.02]'
              }`}
            >
              <span
                className={`flex h-9 w-9 items-center justify-center rounded-lg ${
                  cfg.escrowRequested
                    ? 'bg-emerald-500 text-white'
                    : 'bg-ink-900/[0.05] text-ink-600'
                }`}
              >
                {cfg.escrowRequested ? (
                  <Icon.check width={18} height={18} />
                ) : (
                  <Icon.lock width={18} height={18} />
                )}
              </span>
              <span className="flex-1">
                <span className="block text-sm font-semibold text-ink-900">
                  {cfg.escrowRequested
                    ? 'Escrow account active'
                    : 'Request a tri-party escrow account'}
                </span>
                <span className="mt-0.5 block text-xs text-ink-700/70">
                  Subscription funds held under a tri-party agreement between you, the
                  investor, and SeqPal’s banking partner; released to you on closing.
                </span>
              </span>
            </button>

            <label className="mt-4 flex cursor-pointer items-start gap-3 rounded-xl border border-ink-900/15 p-4">
              <input
                type="checkbox"
                checked={cfg.tosAccepted}
                onChange={(e) => set({ tosAccepted: e.target.checked })}
                className="mt-0.5 h-4 w-4 accent-btc"
              />
              <span className="text-sm">
                <span className="font-medium text-ink-900">
                  I am the legal operator of this portal
                </span>
                <span className="mt-0.5 block text-xs text-ink-700/70">
                  Investors accept my own terms of service. SeqPal is a technology and
                  services vendor, not the portal operator or a placement agent.
                </span>
              </span>
            </label>
          </section>
        </div>

        {/* preview + publish */}
        <div className="lg:sticky lg:top-24 lg:self-start">
          <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
            Live preview
          </div>
          <div className="mt-3">
            <PreviewHero cfg={cfg} iss={iss} />
          </div>
          <button
            onClick={publish}
            disabled={!canPublish}
            className="btn-primary mt-5 w-full"
          >
            <Icon.globe width={16} height={16} />
            Publish portal
          </button>
          {!canPublish && (
            <p className="mt-2 text-center text-xs text-ink-700/60">
              Request escrow and confirm you’re the legal operator to publish.
            </p>
          )}
        </div>
      </div>
    </div>
  )
}
