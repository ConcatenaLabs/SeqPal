import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Icon } from '../../components/icons'
import { Badge } from '../../components/ui'
import SignInGate from '../../components/SignInGate'
import { useStore } from '../../lib/store'
import { idPassport } from '../../lib/api'
import LinkedWallets from '../../components/LinkedWallets'

const LIST_LABELS = {
  ofac_sdn: 'OFAC SDN',
  ofac_consolidated: 'OFAC consolidated',
  eu_consolidated: 'EU consolidated',
  un_sc: 'UN Security Council',
}

const STATUS = {
  verified: { label: 'Verified', color: 'emerald' },
  pending_review: { label: 'In review', color: 'amber' },
  needs_info: { label: 'Needs info', color: 'amber' },
  refused: { label: 'Refused', color: 'rose' },
  unverified: { label: 'Not verified', color: 'slate' },
}

function fmtDate(unix) {
  if (!unix) return 'n/a'
  return new Date(unix * 1000).toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  })
}

function Section({ title, sub, children }) {
  return (
    <div>
      <h2 className="text-lg font-bold text-ink-900">{title}</h2>
      {sub && <p className="mt-1 text-sm text-ink-700/70">{sub}</p>}
      <div className="mt-4">{children}</div>
    </div>
  )
}

export default function IdPassport() {
  const { loading, isSignedIn } = useStore()
  const [p, setP] = useState(null)
  const [err, setErr] = useState(null)

  useEffect(() => {
    if (!isSignedIn) return
    let cancelled = false
    idPassport()
      .then((data) => !cancelled && setP(data))
      .catch((e) => !cancelled && setErr(e.message))
    return () => {
      cancelled = true
    }
  }, [isSignedIn])

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
        title="Sign in to see your passport"
        body="Your SeqPal ID passport shows the eligibility categories the policy server enforces on your holdings. It is the credential a counterparty or a regulator would ask to see."
        backTo="/id"
        backLabel="SeqPal ID home"
      />
    )
  }
  if (err) {
    return (
      <section className="container-x py-16">
        <div className="mx-auto max-w-lg rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700">
          {err}
        </div>
      </section>
    )
  }
  if (!p) {
    return (
      <section className="container-x flex justify-center py-24">
        <span className="h-8 w-8 animate-spin rounded-full border-4 border-btc/20 border-t-btc" />
      </section>
    )
  }

  const st = STATUS[p.status] || STATUS.unverified
  const notVerified = p.status !== 'verified'

  return (
    <section className="container-x py-12">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-3xl font-extrabold tracking-tight text-ink-900">Your passport</h1>
          <p className="mt-1 text-ink-700/80">
            The credential every SeqPal-managed asset is gated on.
          </p>
        </div>
        <div className="flex items-center gap-2">
          {p.frozen && (
            <Badge color="rose">
              <Icon.lock width={12} height={12} /> Frozen
            </Badge>
          )}
          <Badge color={st.color}>{st.label}</Badge>
        </div>
      </div>

      {/* The card */}
      <div className="mt-8 overflow-hidden rounded-2xl border border-ink-800 bg-ink-950 text-white shadow-xl">
        <div className="flex items-center justify-between border-b border-white/10 px-6 py-4">
          <div className="flex items-center gap-2 text-sm font-semibold">
            <Icon.id width={18} height={18} className="text-btc" /> SeqPal ID
            <span className="text-white/40">·</span>
            <span className="text-white/60">Individual</span>
          </div>
          <Badge color={st.color}>{st.label}</Badge>
        </div>
        <div className="grid gap-6 px-6 py-6 sm:grid-cols-2">
          <div>
            {/* A wallet-backed ID has no enclave account. Calling its id an AID
                describes something that does not exist, and offering an empty
                "enclave key" beside it says the same thing twice. */}
            <div className="text-xs text-white/40">
              {p.has_enclave ? 'Sequentia enclave account (AID)' : 'SeqPal account id'}
            </div>
            <div className="mt-1 break-all font-mono text-sm text-seq-400">{p.aid}</div>
          </div>
          <div>
            <div className="text-xs text-white/40">
              {p.has_enclave ? 'Enclave key (x-only)' : 'OpenAMP account'}
            </div>
            {p.has_enclave ? (
              <div className="mt-1 break-all font-mono text-xs text-white/70">{p.enclave_key}</div>
            ) : (
              <div className="mt-1 text-xs leading-relaxed text-white/70">
                None attached. Restricted assets need one; freely-tradable stocks and
                network-enforced assets do not.
              </div>
            )}
          </div>
          <div>
            <div className="text-xs text-white/40">Eligibility valid until</div>
            <div className="mt-1 font-semibold">{fmtDate(p.valid_until)}</div>
          </div>
          <div>
            <div className="text-xs text-white/40">Categories carried</div>
            <div className="mt-1 font-semibold">{p.categories?.length || 0}</div>
          </div>
        </div>
      </div>

      {notVerified && (
        <div className="mt-6 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3.5 text-sm text-amber-900">
          <span>
            This identity carries no eligibility categories yet. Run verification to be granted
            them.
          </span>
          <Link to="/id/register" className="btn-primary shrink-0">
            Verify now
            <Icon.arrowRight width={16} height={16} />
          </Link>
        </div>
      )}

      <div className="mt-10 grid gap-10 lg:grid-cols-2">
        {/* Categories */}
        <Section
          title="Eligibility categories"
          sub="Compound tokens the policy server checks on every transfer. An expired identity carries none."
        >
          {p.categories?.length ? (
            <div className="space-y-2">
              {p.categories.map((c) => (
                <div
                  key={c.token}
                  className="flex items-center justify-between rounded-xl border border-ink-900/10 bg-white px-4 py-3"
                >
                  <span className="font-mono text-sm font-semibold text-ink-900">{c.token}</span>
                  <span className="flex items-center gap-2 text-xs">
                    {c.expiring_soon && (
                      <Badge color="amber">
                        <Icon.clock width={11} height={11} /> Expiring soon
                      </Badge>
                    )}
                    <span className="text-ink-700/60">valid to {fmtDate(c.valid_until)}</span>
                  </span>
                </div>
              ))}
            </div>
          ) : (
            <div className="rounded-xl border border-dashed border-ink-900/20 px-4 py-6 text-center text-sm text-ink-700/60">
              No categories yet.
            </div>
          )}
        </Section>

        {/* Screening */}
        <Section
          title="Sanctions screening"
          sub="Screened at verification and on a daily schedule against the real public lists."
        >
          <div className="divide-y divide-ink-900/10 rounded-xl border border-ink-900/10 bg-white">
            {(p.screening?.length ? p.screening : (p.lists_screened || []).map((l) => ({ list: l }))).map(
              (s) => (
                <div key={s.list} className="flex items-center justify-between gap-3 px-4 py-3">
                  <div>
                    <div className="text-sm font-medium text-ink-900">
                      {LIST_LABELS[s.list] || s.list}
                    </div>
                    <div className="text-xs text-ink-700/55">
                      {s.last_screened_at ? `Last screened ${fmtDate(s.last_screened_at)}` : 'Not yet screened'}
                    </div>
                  </div>
                  {s.matched ? (
                    <span className="inline-flex items-center gap-1.5 text-sm font-medium text-rose-700">
                      <span className="h-1.5 w-1.5 rounded-full bg-rose-500" />
                      Match{s.matched_entry ? `: ${s.matched_entry}` : ''}
                    </span>
                  ) : (
                    <span className="inline-flex items-center gap-1.5 text-sm text-emerald-700">
                      <Icon.check width={14} height={14} /> Clear
                    </span>
                  )}
                </div>
              )
            )}
          </div>
        </Section>

        {/* Linked entities */}
        <Section
          title="Linked companies (KYB)"
          sub="Corporate entities added to this personal ID, each with its own treasury once verified."
        >
          {p.entities?.length ? (
            <div className="space-y-2">
              {p.entities.map((e) => (
                <div key={e.id} className="rounded-xl border border-ink-900/10 bg-white px-4 py-3">
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-semibold text-ink-900">{e.name}</span>
                    {e.verified ? (
                      <Badge color="emerald">
                        <Icon.check width={11} height={11} /> Verified
                      </Badge>
                    ) : (
                      <Badge color="slate">Unverified</Badge>
                    )}
                  </div>
                  <div className="mt-1 text-xs text-ink-700/60">{e.jurisdiction}</div>
                  {e.treasury_aid && (
                    <div className="mt-1.5 break-all font-mono text-[11px] text-ink-700/55">
                      treasury {e.treasury_aid}
                      {e.ubo_signed ? ' · UBO signed' : ' · UBO unsigned'}
                    </div>
                  )}
                </div>
              ))}
            </div>
          ) : (
            <div className="rounded-xl border border-dashed border-ink-900/20 px-4 py-6 text-center text-sm text-ink-700/60">
              No companies linked.{' '}
              <Link to="/id/entities" className="font-medium text-btc-600">
                Add one
              </Link>
              .
            </div>
          )}
        </Section>

        {/* Where accepted */}
        <Section
          title="Where the ID is accepted"
          sub="Which SeqPal-managed assets you are eligible for right now, and which venues honor the credential."
        >
          <div className="space-y-2">
            {(p.accepted?.assets || []).length === 0 && (
              <div className="rounded-xl border border-dashed border-ink-900/20 px-4 py-6 text-center text-sm text-ink-700/60">
                No SeqPal-managed assets are live yet.
              </div>
            )}
            {(p.accepted?.assets || []).map((a) => (
              <div key={a.asset} className="rounded-xl border border-ink-900/10 bg-white px-4 py-3">
                <div className="flex items-center justify-between gap-2">
                  <span className="font-semibold text-ink-900">
                    {a.name || a.ticker || a.asset}
                    {a.ticker && <span className="ml-2 font-mono text-xs text-ink-700/60">{a.ticker}</span>}
                  </span>
                  {a.eligible ? (
                    <Badge color="emerald">Eligible</Badge>
                  ) : (
                    <Badge color="slate">Not eligible</Badge>
                  )}
                </div>
                {!a.eligible && a.reasons?.length > 0 && (
                  <div className="mt-1 text-xs leading-relaxed text-ink-700/60">
                    {a.reasons.join('; ')}
                  </div>
                )}
              </div>
            ))}
            {(p.accepted?.venues || []).length > 0 && (
              <div className="mt-3 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3">
                <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/50">
                  Honoring venues
                </div>
                <ul className="mt-2 space-y-1.5">
                  {p.accepted.venues.map((v) => (
                    <li key={v.name} className="flex items-start gap-2 text-sm">
                      <Icon.check width={14} height={14} className="mt-0.5 shrink-0 text-seq-600" />
                      <span>
                        <span className="font-medium text-ink-900">{v.name}</span>
                        <span className="text-ink-700/60"> · {v.role}</span>
                      </span>
                    </li>
                  ))}
                </ul>
                <p className="mt-2 text-xs leading-relaxed text-ink-700/55">
                  A venue can only check what SeqPal has stamped. It cannot grant eligibility of
                  its own, and nothing here is final at 0 confirmations.
                </p>
              </div>
            )}
          </div>
        </Section>
      </div>

      <div className="mt-8">
        <LinkedWallets />
      </div>

      <div className="mt-10 flex flex-wrap gap-3">
        <Link to="/holdings" className="btn-outline">
          <Icon.wallet width={16} height={16} /> My holdings
        </Link>
        <Link to="/id/entities" className="btn-outline">
          <Icon.building width={16} height={16} /> Manage companies
        </Link>
      </div>
    </section>
  )
}
