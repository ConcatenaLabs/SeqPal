import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Icon, StructureIcon } from '../components/icons'
import { Badge } from '../components/ui'
import Modal from '../components/Modal'
import SignInGate from '../components/SignInGate'
import AttachEnclaveCard from '../components/AttachEnclaveCard'
import { useStore } from '../lib/store'
import { view } from '../lib/issuance'
import { getBalance } from '../lib/openamp'
import { getStructure } from '../data/structures'
import { STATUS } from '../lib/lifecycle'


function StatusBadge({ status }) {
  const s = STATUS[status] || STATUS.draft
  return (
    <Badge color={s.color}>
      {status === 'live' && <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />}
      {s.label}
    </Badge>
  )
}

// Signing out no longer destroys anything: the enclave key belongs to the
// holder's wallet, not to this browser, so there is no half of a 2-of-2 here to
// lose and no backup file to check for first. All this clears is the session and
// the public key of the wallet it was signed in with.
function SignOutModal({ open, onClose }) {
  const { signOut, signerKind } = useStore()
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)

  const go = async () => {
    setErr(null)
    setBusy(true)
    try {
      await signOut()
      onClose()
    } catch (e) {
      setErr(e.message)
      setBusy(false)
    }
  }

  return (
    <Modal open={open} onClose={onClose} title="Sign out of SeqPal">
      <div className="space-y-4 text-sm">
        <p className="leading-relaxed text-ink-700/80">
          Your SeqPal ID is the OpenAMP account in your Sequentia wallet, and it stays exactly
          where it is. Signing out ends this session and forgets which wallet it belonged to;
          every asset you hold remains yours, and signing back in with the same wallet returns
          you to the same account.
        </p>
        {signerKind === 'linked' && (
          <p className="leading-relaxed text-ink-700/70">
            You linked this wallet by hand, so you will be asked for its account key again next
            time.
          </p>
        )}
        {err && <p className="rounded-lg bg-rose-50 px-3 py-2 text-rose-700">{err}</p>}
        <div className="flex gap-3">
          <button onClick={onClose} className="btn-outline flex-1">
            Stay signed in
          </button>
          <button onClick={go} disabled={busy} className="btn-primary flex-1 disabled:opacity-40">
            {busy ? 'Signing out' : 'Sign out'}
          </button>
        </div>
      </div>
    </Modal>
  )
}

export default function Dashboard() {
  const { loading, isSignedIn, account, entities, issuances, createIssuance } = useStore()
  const [signingOut, setSigningOut] = useState(false)
  const [sampleErr, setSampleErr] = useState(null)
  const [sampling, setSampling] = useState(false)

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
        title="Sign in to your issuer dashboard"
        body="This page needs a SeqPal ID. The ID is the identity and compliance passport for SeqPal-managed assets on Sequentia, and issuing is one of the things it lets you do."
      />
    )
  }

  // seqpald scopes issuances to the session's AID, so everything here is this
  // principal's and nothing else can be.
  const mine = issuances.map(view)
  const live = mine.filter((i) => i.live)

  // A sample is a DRAFT and nothing more. It fabricates no asset id and no txid:
  // those come from a real mint or they do not exist.
  const loadSample = async () => {
    setSampleErr(null)
    setSampling(true)
    try {
      await createIssuance({
        name: 'Aurora Ventures Fund I',
        ticker: 'AURA' + Math.floor(Math.random() * 90 + 10),
        structure_id: 'native-equity',
        terms: {
          structure_id: 'native-equity',
          is_public: false,
          unit: 'USD',
          entity_name: 'Aurora Ventures Fund I',
          raise: '$5,000,000',
          fields: { raise: '5,000,000', premoney: '20,000,000', supply: '10,000,000' },
          policy: {},
          principal: { kind: 'individual', name: account.display_name },
          mint_target: 'issuer enclave',
        },
      })
    } catch (e) {
      setSampleErr(e.message)
    } finally {
      setSampling(false)
    }
  }

  return (
    <section className="container-x py-12">
      <AttachEnclaveCard />
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-3xl font-extrabold tracking-tight text-ink-900">
            Issuer Dashboard
          </h1>
          <p className="mt-1 text-ink-700/80">
            Signed in as{' '}
            <span className="font-semibold text-ink-900">{account.display_name}</span> ·{' '}
            <span className="font-mono text-sm">
              {account.aid.slice(0, 8)}…{account.aid.slice(-6)}
            </span>
          </p>
        </div>
        <div className="flex gap-2">
          <button onClick={() => setSigningOut(true)} className="btn-ghost text-ink-700/70">
            Sign out
          </button>
          <button onClick={loadSample} disabled={sampling} className="btn-outline">
            {sampling ? 'Creating' : 'Load sample draft'}
          </button>
          <Link to="/onboarding" className="btn-primary">
            <Icon.spark width={16} height={16} />
            New issuance
          </Link>
        </div>
      </div>
      {sampleErr && (
        <p className="mt-4 rounded-lg bg-rose-50 px-3 py-2 text-sm text-rose-700">{sampleErr}</p>
      )}

      {/* Account summary */}
      <div className="mt-8 grid gap-4 sm:grid-cols-3">
        <Link to="/id" className="card flex items-center gap-4 p-5 hover:shadow-glow">
          <div className="flex h-11 w-11 items-center justify-center rounded-xl bg-emerald-50 text-emerald-600">
            <Icon.id width={22} height={22} />
          </div>
          <div className="min-w-0">
            <div className="text-sm text-ink-700/70">SeqPal ID</div>
            <div className="truncate font-semibold text-ink-900">
              Verified · {entities.length} {entities.length === 1 ? 'entity' : 'entities'}
            </div>
          </div>
          <Icon.arrowRight width={16} height={16} className="ml-auto text-ink-500" />
        </Link>
        <div className="card flex items-center gap-4 p-5">
          <div className="flex h-11 w-11 items-center justify-center rounded-xl bg-btc-50 text-btc-600">
            <Icon.layers width={22} height={22} />
          </div>
          <div>
            <div className="text-sm text-ink-700/70">Issuances</div>
            <div className="font-semibold text-ink-900">{mine.length}</div>
          </div>
        </div>
        <div className="card flex items-center gap-4 p-5">
          <div className="flex h-11 w-11 items-center justify-center rounded-xl bg-seq/10 text-seq-600">
            <Icon.exchange width={22} height={22} />
          </div>
          <div>
            <div className="text-sm text-ink-700/70">Deployed on Sequentia</div>
            <div className="font-semibold text-ink-900">{live.length}</div>
          </div>
        </div>
      </div>

      {/* Issuance list */}
      <div className="mt-10">
        <h2 className="text-lg font-bold text-ink-900">Your issuances</h2>
        {mine.length === 0 ? (
          <div className="card mt-4 flex flex-col items-center justify-center px-6 py-16 text-center">
            <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-ink-900/[0.04] text-ink-600">
              <Icon.layers width={28} height={28} />
            </div>
            <h3 className="mt-5 text-lg font-bold text-ink-900">No issuances yet</h3>
            <p className="mt-2 max-w-sm text-sm leading-relaxed text-ink-700/80">
              Start your first issuance and walk the seven-step flow, from structure choice to a
              real mint on the Sequentia testnet.
            </p>
            <div className="mt-6 flex flex-wrap items-center justify-center gap-3">
              <Link to="/onboarding" className="btn-primary">
                Launch an issuance
                <Icon.arrowRight width={16} height={16} />
              </Link>
              <button onClick={loadSample} disabled={sampling} className="btn-outline">
                Load a sample draft
              </button>
            </div>
          </div>
        ) : (
          <div className="mt-4 space-y-3">
            {mine.map((iss) => {
              const s = getStructure(iss.structureId)
              const Ic = StructureIcon[s?.icon] || Icon.layers
              return (
                <Link
                  key={iss.id}
                  to={`/issuance/${iss.id}`}
                  className="card flex items-center gap-4 p-5 transition-all hover:-translate-y-0.5 hover:shadow-glow"
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
                      <span className="truncate font-bold text-ink-900">{iss.name}</span>
                      <span className="font-mono text-xs text-ink-700/60">{iss.ticker}</span>
                    </div>
                    <div className="mt-0.5 text-sm text-ink-700/70">
                      {s?.name || 'Structure not set'}
                      {iss.entityName ? ` · ${iss.entityName}` : ''}
                    </div>
                  </div>
                  <div className="hidden text-right sm:block">
                    <StatusBadge status={iss.status} />
                    <div className="mt-1 text-xs text-ink-700/50">
                      {new Date(iss.created_at * 1000).toLocaleDateString()}
                    </div>
                  </div>
                  <Icon.arrowRight width={18} height={18} className="text-ink-600" />
                </Link>
              )
            })}
          </div>
        )}
      </div>

      <SignOutModal open={signingOut} onClose={() => setSigningOut(false)} />
    </section>
  )
}
