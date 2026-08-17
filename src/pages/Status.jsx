import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Icon } from '../components/icons'
import { Badge } from '../components/ui'
import { health } from '../lib/api'
import { REAL, SIMULATED } from '../data/honesty'

// Status page: policy-server health, the key-custody posture, and the master
// real-vs-simulated list (plan Section 7). Public.

function Dot({ ok }) {
  return (
    <span
      className={`inline-block h-2.5 w-2.5 rounded-full ${
        ok === null ? 'bg-ink-600/30' : ok ? 'bg-emerald-500' : 'bg-rose-500'
      }`}
    />
  )
}

function HealthRow({ label, ok, detail }) {
  return (
    <div className="flex items-center justify-between gap-4 py-3">
      <div className="min-w-0">
        <div className="flex items-center gap-2 font-medium text-ink-900">
          <Dot ok={ok} /> {label}
        </div>
        {detail && <p className="mt-0.5 pl-[18px] text-xs text-ink-700/70">{detail}</p>}
      </div>
      <span className="shrink-0 text-sm font-medium text-ink-700/80">
        {ok === null ? 'unknown' : ok ? 'operational' : 'unavailable'}
      </span>
    </div>
  )
}

export default function Status() {
  const [h, setH] = useState(null)
  const [err, setErr] = useState(null)

  useEffect(() => {
    let cancelled = false
    health()
      .then((r) => {
        if (!cancelled) setH(r)
      })
      .catch((e) => {
        if (!cancelled) setErr(e.message)
      })
    return () => {
      cancelled = true
    }
  }, [])

  return (
    <section className="container-x max-w-4xl py-14">
      <div className="eyebrow">Platform status</div>
      <h1 className="mt-3 text-3xl font-extrabold tracking-tight text-ink-900 sm:text-4xl">
        Status and honesty
      </h1>
      <p className="mt-4 text-lg leading-relaxed text-ink-700/90">
        This is a testnet proof of concept. No real funds, no real securities, no legal effect. This
        page tells you what is live, what is real, and what stays a labeled simulation, with the same
        master list every other surface labels against.
      </p>

      {/* Policy-server + platform health */}
      <div className="card mt-10 p-6">
        <h2 className="font-bold text-ink-900">Service health</h2>
        {err && (
          <p className="mt-3 rounded-lg border border-rose-200 bg-rose-50 px-4 py-2.5 text-sm text-rose-700">
            The platform health probe did not answer ({err}). The services may be down or unreachable
            from this browser.
          </p>
        )}
        <div className="mt-2 divide-y divide-ink-900/10">
          <HealthRow
            label="The platform backend"
            ok={h ? !!h.ok : null}
            detail="An authenticated upstream probe. When this is unavailable, a deploy would fail, and the platform says so before checkout."
          />
          <HealthRow
            label="Transfer service reachable"
            ok={h ? !!h.openamp_ok : null}
            detail="The service that checks and approves every restricted transfer against the issuer's rules."
          />
          <HealthRow
            label="Issuer token configured"
            ok={h ? !!h.issuer_token_ok : null}
            detail="Required to mint a restricted asset from the platform."
          />
          <HealthRow
            label="Confidential transfers"
            ok={h ? !!h.confidential : null}
            detail="A per-deployment capability. Sequentia is transparent by default; when this deployment has confidentiality enabled, any holder can make any individual transfer confidential, hiding its amount and asset from outside observers while the registrar still sees everything. When it does not, a confidential transfer is refused rather than silently downgraded."
          />
          <HealthRow
            label="Network-enforced rules"
            ok={h ? !!h.damp : null}
            detail="A per-deployment capability: whether an issuer here can choose the model where the network itself enforces the transfer rules. When this deployment does not have it, that choice cannot be selected and a deploy asking for it is refused."
          />
        </div>
        {h?.network && (
          <p className="mt-3 text-xs text-ink-700/60">Network: {h.network}.</p>
        )}
      </div>

      {/* Key custody */}
      <div className="card mt-6 p-6">
        <div className="flex items-center gap-2">
          <Icon.lock width={18} height={18} className="text-seq-600" />
          <h2 className="font-bold text-ink-900">Key custody</h2>
        </div>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/80">
          Every asset uses the issuer’s own key: the issuing entity’s own SeqPal ID key, held in its
          browser, so deploying an asset, authorising a close, and reclaiming tokens each need the
          issuer’s signature, taken deliberately in the browser. The issuer runs no server.
        </p>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/80">
          SeqPal holds only its approval key. It checks every restricted transfer to enforce the
          issuer’s eligibility rules, and it is negative control: it can refuse a transfer, never
          originate one or move a holder’s position. The full account is on the{' '}
          <Link to="/legal" className="font-medium text-seq-600 hover:underline">
            Legal and Licensing
          </Link>{' '}
          page, and the machinery is named on the{' '}
          <Link to="/docs" className="font-medium text-seq-600 hover:underline">
            Documentation
          </Link>{' '}
          page.
        </p>
      </div>

      {/* Business continuity, honestly disclosed */}
      <div className="card mt-6 p-6">
        <div className="flex items-center gap-2">
          <Icon.building width={18} height={18} className="text-seq-600" />
          <h2 className="font-bold text-ink-900">Business continuity</h2>
        </div>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/80">
          SeqPal’s transfer service, its books and records, and the credential that lets the
          platform mint all run on one server, with backups and a restore runbook. When that server
          is down, the rows above go red and transfers of standard restricted assets pause until it
          is restored; tokens whose rules the network enforces, and freely-tradable tokens, keep
          trading regardless. Nothing on chain is lost in a server loss: assets, balances, and the
          register live on the Sequentia network, and the platform’s records restore from backup.
        </p>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/80">
          The approval key sits behind a clean seam in the software: replacing the single signer
          with a group of signers that approve jointly changes no address and no asset id, so a
          stronger signing setup never strands an issued asset. The technical detail is on the{' '}
          <Link to="/docs" className="font-medium text-seq-600 hover:underline">
            Documentation
          </Link>{' '}
          page.
        </p>
      </div>

      {/* What is real */}
      <div className="card mt-6 p-6">
        <div className="flex items-center gap-2">
          <Icon.check width={18} height={18} className="text-emerald-600" />
          <h2 className="font-bold text-ink-900">What is genuinely real</h2>
        </div>
        <ul className="mt-3 grid gap-2 sm:grid-cols-2">
          {REAL.map((r) => (
            <li key={r} className="flex items-start gap-2 text-sm text-ink-700/90">
              <Icon.check width={15} height={15} className="mt-0.5 shrink-0 text-emerald-600" />
              {r}
            </li>
          ))}
        </ul>
      </div>

      {/* Master simulated list */}
      <div className="mt-6">
        <h2 className="text-xl font-bold text-ink-900">What stays simulated, and how it is labeled</h2>
        <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
          Each row is simulated in one specific way. The table gives what is real about it and the
          label you will see on that surface.
        </p>
        <div className="mt-4 space-y-3">
          {SIMULATED.map((s) => (
            <div key={s.element} className="card p-5">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <h3 className="font-semibold text-ink-900">{s.element}</h3>
                <Badge color="amber">{s.label}</Badge>
              </div>
              <p className="mt-1.5 text-sm leading-relaxed text-ink-700/80">{s.real}</p>
            </div>
          ))}
        </div>
      </div>

      <div className="mt-10 flex flex-wrap gap-4 text-sm">
        <Link to="/faq" className="font-medium text-seq-600 hover:underline">
          Read the FAQ
        </Link>
        <Link to="/legal" className="font-medium text-seq-600 hover:underline">
          Legal and Licensing
        </Link>
        <Link to="/privacy" className="font-medium text-seq-600 hover:underline">
          Privacy
        </Link>
        <Link to="/docs" className="font-medium text-seq-600 hover:underline">
          Documentation
        </Link>
        <Link to="/docs/verify" className="font-medium text-seq-600 hover:underline">
          Verify independently
        </Link>
      </div>
    </section>
  )
}
