import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Icon } from '../components/icons'
import { Badge } from '../components/ui'
import CopyId from '../components/CopyId'
import { rfsaLookup } from '../lib/api'
import { LEGACY_ASSETS } from '../data/honesty'

// Legal & Licensing (plan 4.5). It states conclusions, not aspirations: the
// custody conclusion, the per-jurisdiction platform-role conclusions as
// production analysis with simulated numbers, the blast-radius disclosure, the
// non-custodial USDX model, the legacy-asset deprecation, and the FATCA/CRS note.
// It carries the public RFSA filing lookup. Public page.

const REGISTRATIONS = [
  { fn: 'Transfer agent', num: 'RFSA-TA-2026-0041' },
  { fn: 'Registrar', num: 'RFSA-RG-2026-0042' },
  { fn: 'Financial Products Registry operator', num: 'RFSA-OP-2026-0009' },
]

const JURISDICTIONS = [
  {
    place: 'United States',
    verdict: 'Broker-dealer registration, Exchange Act s.15(a)',
    body: 'A production SeqPal serving US investors effects securities transactions for them and collects per-subscription, transaction-based fees, which is the classic broker hallmark. The issuer-personnel safe harbor of Rule 3a4-1 does not cover a third-party platform. Serving US investors would require Exchange Act s.15(a) broker-dealer registration, or a restructuring to non-transaction-based fees with the issuer placing its own offering. "Agent of the Prospera issuer" answers none of this and is never leaned on to.',
  },
  {
    place: 'European Union',
    verdict: 'MiFID II investment services, third-country firm',
    body: 'Serving EU retail engages MiFID II investment services, the reception and transmission of orders and placement, by a third-country firm. An active marketing website defeats a reverse-solicitation defence. For some structures AIFMD also engages, which is why an AIF-classified structure disables the EU retail lift and restricts access to professional categories.',
  },
  {
    place: 'United Kingdom',
    verdict: 'RAO Article 25 arranging',
    body: 'Serving UK investors engages Regulated Activities Order Article 25, arranging deals in investments. UK access also requires the statutory investor statements the document engine generates, in SI 2024/301 wording with the income and net-asset thresholds.',
  },
]

function RfsaLookup() {
  const [number, setNumber] = useState('')
  const [state, setState] = useState({ idle: true })

  const look = async (e) => {
    e.preventDefault()
    const n = number.trim()
    if (!n) return
    setState({ loading: true })
    try {
      const r = await rfsaLookup(n)
      setState({ filing: r.filing, label: r.label })
    } catch (err) {
      setState({ error: err.message })
    }
  }

  const f = state.filing
  return (
    <div className="card p-6">
      <div className="flex items-center gap-2">
        <Icon.receipt width={18} height={18} className="text-btc-600" />
        <h2 className="font-bold text-ink-900">RFSA Financial Products Registry lookup</h2>
        <Badge color="amber" className="ml-auto">
          Simulated regulator
        </Badge>
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
        Every public offering on the platform files with the registry before it can deploy. Look up a
        filing by its number. The regulator relationship is simulated; the number, this lookup, the
        deploy gate, and the anchored filing hash are real.
      </p>
      <form onSubmit={look} className="mt-4 flex flex-wrap gap-2">
        <input
          className="input flex-1"
          placeholder="RFSA-FP-2026-XXXXXX"
          value={number}
          onChange={(e) => setNumber(e.target.value)}
        />
        <button className="btn-primary" type="submit">
          Look up
        </button>
      </form>
      {state.loading && <p className="mt-3 text-sm text-ink-700/70">Looking up…</p>}
      {state.error && (
        <p className="mt-3 rounded-lg border border-rose-200 bg-rose-50 px-4 py-2.5 text-sm text-rose-700">
          {state.error}
        </p>
      )}
      {f && (
        <div className="mt-4 space-y-2 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3 text-sm">
          <div className="flex items-center justify-between">
            <span className="text-ink-700/70">Filing</span>
            <span className="font-mono font-semibold text-ink-900">{f.filing_number}</span>
          </div>
          <div className="flex items-center justify-between">
            <span className="text-ink-700/70">Issuer</span>
            <span className="font-medium text-ink-900">{f.issuer}</span>
          </div>
          <div className="flex items-center justify-between">
            <span className="text-ink-700/70">Structure</span>
            <span className="font-medium text-ink-900">{f.structure}</span>
          </div>
          <div className="flex items-center justify-between gap-3">
            <span className="shrink-0 text-ink-700/70">terms_hash</span>
            <CopyId value={f.terms_hash} label="terms hash" />
          </div>
          <div className="flex items-center justify-between gap-3">
            <span className="shrink-0 text-ink-700/70">Filing hash</span>
            <CopyId value={f.filing_hash} label="filing hash" />
          </div>
          {f.anchor_txid && (
            <div className="flex items-center justify-between gap-3">
              <span className="shrink-0 text-ink-700/70">Anchor txid</span>
              <CopyId value={f.anchor_txid} kind="tx" label="anchor txid" />
            </div>
          )}
          <p className="pt-1 text-xs text-ink-700/60">{state.label}</p>
        </div>
      )}
    </div>
  )
}

export default function Legal() {
  return (
    <section className="container-x max-w-4xl py-14">
      <div className="eyebrow">Legal and licensing</div>
      <h1 className="mt-3 text-3xl font-extrabold tracking-tight text-ink-900 sm:text-4xl">
        Where SeqPal stands, stated plainly
      </h1>
      <p className="mt-4 text-lg leading-relaxed text-ink-700/90">
        This page states conclusions rather than promising an analysis. The role analysis below is
        real production analysis; the entity and the registration numbers are demo. This is a testnet
        proof of concept with no legal effect.
      </p>

      {/* Entity + registrations */}
      <div className="card mt-10 p-6">
        <h2 className="font-bold text-ink-900">Demo entity and registrations</h2>
        <div className="mt-3 grid gap-3 sm:grid-cols-2">
          <div className="rounded-xl bg-ink-900/[0.03] px-4 py-3">
            <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">Entity</div>
            <div className="mt-1 font-semibold text-ink-900">SeqPal LLC</div>
            <div className="text-sm text-ink-700/80">Prospera ZEDE, Roatan, Honduras</div>
          </div>
          <div className="rounded-xl bg-ink-900/[0.03] px-4 py-3">
            <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">Role</div>
            <div className="mt-1 font-semibold text-ink-900">Transfer agent and registrar</div>
            <div className="text-sm text-ink-700/80">SeqPal operates the policy server that co-signs transfers.</div>
          </div>
        </div>
        <div className="mt-4 divide-y divide-ink-900/10">
          {REGISTRATIONS.map((r) => (
            <div key={r.fn} className="flex items-center justify-between gap-3 py-2.5">
              <span className="text-sm text-ink-700/80">{r.fn}</span>
              <span className="flex items-center gap-2">
                <span className="font-mono text-sm font-medium text-ink-900">{r.num}</span>
                <Badge color="amber">Simulated number</Badge>
              </span>
            </div>
          ))}
        </div>
      </div>

      {/* Custody conclusion */}
      <div className="card mt-6 border-amber-200 p-6">
        <div className="flex items-center gap-2">
          <Icon.shield width={18} height={18} className="text-amber-600" />
          <h2 className="font-bold text-ink-900">The custody conclusion</h2>
        </div>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/85">
          For a clawback-enabled asset whose issuer key is held by the platform, SeqPal can move any
          holder's position without the holder's key. That is control amounting to custody under
          essentially any test, so we state it as a conclusion rather than a maybe. It is a risk
          factor in every offering memorandum and it qualifies every "self-custodial" claim for those
          assets.
        </p>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/85">
          This now applies to legacy-path assets only: those that predate the external issuer key. A
          new asset instead uses the issuing entity's own SeqPal ID key as the enclave issuer half, so
          a clawback is two-phase and cannot be broadcast without the issuer's signature. The platform
          holds only the policy key on those assets and cannot move a holder's position on its own.
          Each asset discloses which path it is on, in its freeze and clawback console.
        </p>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/85">
          The 2-of-2 co-signature is a separate thing and is disclosed separately on both paths: it is
          negative control, the power to refuse a transfer, not the power to move funds on its own,
          and the platform continues to operate the policy server that co-signs. The roadmap to
          remove the custody conclusion for the remaining legacy-path key is the FROST threshold
          quorum, tracked on the{' '}
          <Link to="/status" className="font-medium text-seq-600 hover:underline">
            Status
          </Link>{' '}
          page.
        </p>
      </div>

      {/* Blast radius */}
      <div className="card mt-6 p-6">
        <h2 className="font-bold text-ink-900">Single-token blast radius</h2>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/80">
          The platform-held issuer key, on legacy-path assets, is a single LocalKeySigner: one key,
          one token, one box, one disk. A compromise of that one credential is a compromise of every
          legacy asset it can clawback. The mitigation today is operational, a documented backup and
          restore procedure, and the structural fix is the FROST quorum. A new asset is outside this
          blast radius entirely: its issuer key never touches the platform, so there is no platform
          credential that can seize it. We disclose the blast radius rather than imply isolation that
          does not exist.
        </p>
      </div>

      {/* Per-jurisdiction conclusions */}
      <div className="mt-8">
        <h2 className="text-xl font-bold text-ink-900">
          What a production SeqPal would be in the investor's jurisdiction
        </h2>
        <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
          Production analysis with simulated registration numbers. Prospera is a legitimate issuance
          jurisdiction, but it does not extend to your investors: their home law binds the offer.
        </p>
        <div className="mt-4 space-y-3">
          {JURISDICTIONS.map((j) => (
            <div key={j.place} className="card p-5">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <h3 className="font-semibold text-ink-900">{j.place}</h3>
                <Badge color="seq">{j.verdict}</Badge>
              </div>
              <p className="mt-1.5 text-sm leading-relaxed text-ink-700/80">{j.body}</p>
            </div>
          ))}
        </div>
        <p className="mt-3 text-sm leading-relaxed text-ink-700/70">
          This is exactly why the platform compiles your jurisdiction matrix into policy-server
          rules: the restrictions you configure are enforced by the network on every transfer, not by
          a promise. Unanalyzed jurisdictions are excluded by default, and that default protects you.
        </p>
      </div>

      {/* USDX non-custodial model */}
      <div className="card mt-8 p-6">
        <h2 className="font-bold text-ink-900">Non-custodial USDX commitment model</h2>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/80">
          The production design for the USDX rail does not put subscription funds in SeqPal custody
          before settlement. The investor funds a pay-to-taproot address that only they control, and
          delivery versus payment settles atomically against it, so the funds are never in the
          platform's custody. This replaces custodial escrow for the USDX rail and is the successor to
          any escrow-superiority framing.
        </p>
      </div>

      {/* FATCA/CRS */}
      <div className="card mt-6 p-6">
        <h2 className="font-bold text-ink-900">FATCA and CRS classification</h2>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/80">
          A production registrar and transfer agent that holds investor accounts is plausibly a
          Financial Institution under CRS, with self-certification and annual reporting duties for
          foreign account holders. A W-8 or W-9 is not a CRS self-certification, so investor
          onboarding collects a separate CRS and FATCA self-certification of all tax residencies and
          TINs, and the servicing engine emits a labeled-simulated annual CRS and FATCA reporting
          artifact from the registrar entity.
        </p>
      </div>

      {/* Legacy assets */}
      <div className="card mt-6 p-6">
        <h2 className="font-bold text-ink-900">Legacy assets</h2>
        <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
          {LEGACY_ASSETS.map((a) => a.ticker).join(' and ')} are pre-overhaul demo assets, deprecated
          and excluded from the platform. They are unregistered in the asset registry because the
          entity domain must be committed at issue time and cannot be retrofitted.
        </p>
      </div>

      <div className="mt-8">
        <RfsaLookup />
      </div>

      <div className="mt-10 flex flex-wrap gap-4 text-sm">
        <Link to="/faq" className="font-medium text-seq-600 hover:underline">
          FAQ
        </Link>
        <Link to="/privacy" className="font-medium text-seq-600 hover:underline">
          Privacy
        </Link>
        <Link to="/status" className="font-medium text-seq-600 hover:underline">
          Status
        </Link>
        <Link to="/verify" className="font-medium text-seq-600 hover:underline">
          Verify independently
        </Link>
      </div>
    </section>
  )
}
