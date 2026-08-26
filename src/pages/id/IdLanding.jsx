import { Link, useSearchParams } from 'react-router-dom'
import { Icon } from '../../components/icons'
import { SectionHeading } from '../../components/ui'
import { useStore } from '../../lib/store'

// The /id landing. Role-neutral: it describes what a SeqPal ID is and what it
// unlocks, for anyone, with exactly one call to action. It never asks whether
// you are an issuer or an investor. "Issuer" and "investor" are things a
// verified identity does later, never a type chosen at signup.
const unlocks = [
  [
    'Hold and transfer SeqPal-managed assets',
    'A restricted asset only moves between identities the policy server recognises. Without a SeqPal ID you cannot hold one at all.',
  ],
  [
    'Trade them on a Sequentia venue',
    'A venue that lists SeqPal-managed assets checks the eligibility SeqPal has stamped on your identity. It cannot grant eligibility of its own.',
  ],
  [
    'Issue an asset, if you ever want to',
    'The same identity signs you in to the issuance platform and is recorded against the issuance. Issuing is something the identity does later.',
  ],
  [
    'Add a company later',
    'A corporate (KYB) entity is added to your existing personal SeqPal ID.',
  ],
]

const holds = [
  ['Verified identity', 'Passport or national ID, plus liveness'],
  ['Residence and tax residency', 'Verified address and self-certified tax residencies'],
  ['Sanctions, PEP and adverse media', 'Checked by the verification provider as part of their check'],
  ['Eligibility categories', 'Jurisdiction and investor-class tokens the policy server enforces on every transfer'],
  [
    'Sequentia enclave key',
    'The x-only key registered as your Sequentia account. Assets you hold live in its 2-of-2 enclave, and one half of that 2-of-2 is the key in your own wallet.',
  ],
]

export default function IdLanding() {
  const { loading, isSignedIn, account, issuances } = useStore()
  const [params] = useSearchParams()
  const next = params.get('next')
  const registerTo = next ? `/id/register?next=${encodeURIComponent(next)}` : '/id/register'
  const isIssuer = isSignedIn && issuances.length > 0

  return (
    <>
      <section className="border-b border-ink-900/10 bg-ink-900/[0.02]">
        <div className="container-x py-16">
          <SectionHeading
            eyebrow="SeqPal ID"
            title={isSignedIn ? 'Your SeqPal ID' : 'One verified identity. Every SeqPal-managed asset.'}
            sub={
              isSignedIn
                ? 'Your identity and compliance passport for SeqPal-managed assets on Sequentia.'
                : 'SeqPal ID is the identity and compliance layer for SeqPal-managed assets on Sequentia. Anyone who wants to hold, trade, or issue one needs it. One flow, the same for everybody.'
            }
          />
          {next && !isSignedIn && (
            <div className="mt-6 max-w-xl rounded-xl border border-btc/20 bg-btc-50 px-4 py-3 text-sm text-btc-700">
              That page needs a SeqPal ID. Create one or sign in and we will take you back to
              where you left off.
            </div>
          )}
          <div className="mt-8 flex flex-wrap gap-3">
            {isSignedIn ? (
              <>
                <Link to="/id/passport" className="btn-primary px-5 py-3 text-base">
                  Open my passport
                  <Icon.arrowRight width={18} height={18} />
                </Link>
                <Link to="/holdings" className="btn-outline px-5 py-3 text-base">
                  My holdings
                </Link>
              </>
            ) : (
              <Link to={registerTo} className="btn-primary px-5 py-3 text-base">
                Create or sign in with your SeqPal ID
                <Icon.arrowRight width={18} height={18} />
              </Link>
            )}
          </div>
        </div>
      </section>

      <section className="container-x py-16">
        {loading ? (
          <div className="flex justify-center py-12">
            <span className="h-8 w-8 animate-spin rounded-full border-4 border-btc/20 border-t-btc" />
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-12 lg:grid-cols-2 lg:items-start">
            <div>
              <h3 className="text-lg font-bold text-ink-900">What it unlocks</h3>
              <div className="mt-5 space-y-4">
                {unlocks.map(([t, d]) => (
                  <div key={t} className="flex items-start gap-3">
                    <span className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-lg bg-btc-50 text-btc-600">
                      <Icon.check width={14} height={14} />
                    </span>
                    <div>
                      <div className="font-medium text-ink-900">{t}</div>
                      <div className="text-sm leading-relaxed text-ink-700/70">{d}</div>
                    </div>
                  </div>
                ))}
              </div>

              <h3 className="mt-10 text-lg font-bold text-ink-900">What your passport holds</h3>
              <div className="mt-4 space-y-2.5">
                {holds.map(([t, d]) => (
                  <div key={t} className="flex items-start gap-3">
                    <span className="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-ink-900/30" />
                    <div>
                      <div className="text-sm font-medium text-ink-900">{t}</div>
                      <div className="text-sm leading-relaxed text-ink-700/70">{d}</div>
                    </div>
                  </div>
                ))}
              </div>
              <p className="mt-6 text-sm leading-relaxed text-ink-700/70">
                SeqPal is the data controller for your SeqPal ID record; the
                identity-verification provider acts only as a processor. Personal data is handled under the applicable
                data-protection regime (GDPR, UK GDPR, and equivalents).
              </p>
            </div>

            <div>
              {isSignedIn ? (
                <div className="card p-7">
                  <div className="flex items-center gap-3">
                    <span className="flex h-11 w-11 items-center justify-center rounded-xl bg-btc-50 text-btc-600">
                      <Icon.id width={22} height={22} />
                    </span>
                    <div className="min-w-0">
                      <div className="font-bold text-ink-900">
                        {account.display_name || 'Your SeqPal ID'}
                      </div>
                      <div className="truncate font-mono text-xs text-ink-700/60">
                        {account.aid}
                      </div>
                    </div>
                  </div>
                  <p className="mt-4 text-sm leading-relaxed text-ink-700/75">
                    Your identity is signed in. The passport shows your eligibility categories,
                    where your verification stands, linked companies, and where the ID is
                    accepted.
                  </p>
                  <div className="mt-5 grid gap-2">
                    <Link to="/id/passport" className="btn-primary w-full">
                      Open my passport
                      <Icon.arrowRight width={16} height={16} />
                    </Link>
                    <Link to="/id/entities" className="btn-outline w-full">
                      Manage companies (KYB)
                    </Link>
                    <Link to="/id/register" className="btn-ghost w-full text-ink-700">
                      Run verification
                    </Link>
                    {isIssuer && (
                      <Link to="/dashboard" className="btn-ghost w-full text-ink-700">
                        Issuer dashboard
                      </Link>
                    )}
                  </div>
                </div>
              ) : (
                <div className="card p-7">
                  <div className="flex items-center gap-2 text-sm font-semibold text-ink-900">
                    <Icon.shield width={18} height={18} className="text-seq-600" />
                    One credential, every door
                  </div>
                  <p className="mt-2 text-sm leading-relaxed text-ink-700/75">
                    Create a SeqPal ID once. It is the compliance passport a counterparty or a
                    regulator would ask to see, and the credential every SeqPal-managed asset is
                    gated on.
                  </p>
                  <Link to={registerTo} className="btn-primary mt-5 w-full">
                    Get started
                    <Icon.arrowRight width={16} height={16} />
                  </Link>
                  <p className="mt-3 text-center text-xs text-ink-700/60">
                    Registration takes the Sequentia wallet you already have. No new key, no
                    backup file.
                  </p>
                </div>
              )}
            </div>
          </div>
        )}
      </section>
    </>
  )
}
