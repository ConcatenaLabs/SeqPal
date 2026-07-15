import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Icon } from '../components/icons'
import { Badge } from '../components/ui'
import CopyId from '../components/CopyId'
import { rfsaLookup } from '../lib/api'

// Legal & Licensing, grounded in the SeqPal business plan (sections 1.8, 5.1-5.9).
// SeqPal is licensed only in Próspera, by the RFSA, for the infrastructure it runs
// there, and it is scoped so that nothing it does needs a licence where the investor
// lives. On every issuance the issuer is the principal and SeqPal enforces the
// issuer's signed-off configuration. Public page.

// SeqPal's RFSA licence slate, all Próspera-side under Financial Regulation A and
// FinTech Regulation A. Numbers are demo.
const LICENSES = [
  {
    fn: 'FinTech services (Crowdfunding Platform)',
    num: 'RFSA-FT-2026-0007',
    note: 'The foundational platform licence. SeqPal runs no trading venue and executes no orders.',
  },
  {
    fn: 'Escrow Agent',
    num: 'RFSA-EA-2026-0018',
    note: 'Holding subscription assets in escrow for primary placements.',
  },
  {
    fn: 'Money Transmitting Business',
    num: 'RFSA-MT-2026-0023',
    note: 'Moving funds for issuers and investors: distributions and the cash leg of DR mint and redeem.',
  },
  {
    fn: 'Transfer agent',
    num: 'RFSA-TA-2026-0041',
    note: 'By Optimal Regulation petition, modelled on SEC Rule 17Ad, for the on-chain register.',
  },
  {
    fn: 'Trust Company, Class I',
    num: 'RFSA-TC-2026-0009',
    note: 'Bare-trust custody for Depository Receipt and SPV structures.',
  },
  {
    fn: 'Investment Company',
    num: 'RFSA-IC-2026-0031',
    note: 'Sponsoring and certifying public offerings; activates with the first public offering or DR programme.',
  },
]

// The issuer's obligations in the investor's home jurisdiction (plan 5.3, 5.5, 5.6).
// Worked examples; the issuer sets policy across the full country matrix.
const JURISDICTIONS = [
  {
    place: 'United States',
    regime: 'Reg S / Rule 506(c)',
    body: 'The issuer offers and places the securities. US persons come in only through Rule 506(c) with verified accreditation; for everyone else the default is Reg S offshore. Either way the tokens are restricted securities with limited resale, and the SeqPal ID gate stops an unverified US wallet from receiving one at all.',
  },
  {
    place: 'European Union',
    regime: 'Prospectus Regulation Art. 1(4)',
    body: 'MiCA does not cover securities, so a tokenized security answers to the Prospectus Regulation and each country’s private-placement regime. The issuer offers under the Article 1(4) exemptions: qualified investors, plus a capped tail of ordinary investors per member state that the platform counts. An AIF-classified structure also engages AIFMD.',
  },
  {
    place: 'United Kingdom',
    regime: 'FSMA financial promotion',
    body: 'The issuer’s financial promotion must carry the statutory high-net-worth and sophisticated-investor statements, which the document engine generates in SI 2024/301 wording with the income and net-asset thresholds. Arranging the deal is the issuer’s activity as principal.',
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
        <h2 className="font-bold text-ink-900">Financial Products Registry lookup</h2>
        <Badge color="amber" className="ml-auto">
          Simulated regulator
        </Badge>
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
        Every public offering files with the RFSA registry before it can deploy. Look up a filing by
        its number. The regulator relationship is simulated; the number, this lookup, the deploy gate,
        and the anchored filing hash are real.
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
        SeqPal is a Próspera platform, licensed by the RFSA for the registrar, transfer-agent, escrow,
        and platform services it provides. On every issuance the issuer is the principal and carries
        the responsibility for the offering's lawfulness in each investor's jurisdiction; SeqPal
        enforces the configuration the issuer signs off on. This is a testnet proof of concept with no
        legal effect; the entity and the registration numbers are demo, and every page marks what is
        simulated.
      </p>

      {/* Entity, jurisdiction, regulator */}
      <div className="card mt-10 p-6">
        <h2 className="font-bold text-ink-900">Entity, jurisdiction, and regulator</h2>
        <div className="mt-3 grid gap-3 sm:grid-cols-3">
          <div className="rounded-xl bg-ink-900/[0.03] px-4 py-3">
            <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">Entity</div>
            <div className="mt-1 font-semibold text-ink-900">SeqPal LLC</div>
            <div className="text-sm text-ink-700/80">The platform operator.</div>
          </div>
          <div className="rounded-xl bg-ink-900/[0.03] px-4 py-3">
            <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
              Jurisdiction
            </div>
            <div className="mt-1 font-semibold text-ink-900">Próspera ZEDE</div>
            <div className="text-sm text-ink-700/80">
              A Zone for Employment and Economic Development on Roatán, Honduras, where SeqPal and the
              issuer’s offering are domiciled.
            </div>
          </div>
          <div className="rounded-xl bg-ink-900/[0.03] px-4 py-3">
            <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">Regulator</div>
            <div className="mt-1 font-semibold text-ink-900">The RFSA</div>
            <div className="text-sm text-ink-700/80">
              The financial-services regulator for activity conducted in or from Próspera, under its
              Financial Regulation A and FinTech Regulation A.
            </div>
          </div>
        </div>
      </div>

      {/* Licence slate */}
      <div className="card mt-6 p-6">
        <h2 className="font-bold text-ink-900">SeqPal’s RFSA licence slate</h2>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/80">
          SeqPal’s functions span several regulated activities, each under its own RFSA licence. All of
          them are Próspera-side, and the issuer pays for the service.
        </p>
        <div className="mt-4 divide-y divide-ink-900/10">
          {LICENSES.map((l) => (
            <div key={l.fn} className="py-3">
              <div className="flex items-center justify-between gap-3">
                <span className="text-sm font-medium text-ink-900">{l.fn}</span>
                <span className="flex shrink-0 items-center gap-2">
                  <span className="font-mono text-sm text-ink-900">{l.num}</span>
                  <Badge color="amber">Simulated number</Badge>
                </span>
              </div>
              <p className="mt-1 text-xs leading-relaxed text-ink-700/65">{l.note}</p>
            </div>
          ))}
        </div>
      </div>

      {/* Who is responsible for what */}
      <div className="card mt-6 p-6">
        <h2 className="font-bold text-ink-900">Who is responsible for what</h2>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/85">
          On each issuance the issuer LLC is the principal. It owns the claim the token represents, it
          is the offering party a regulator looks to, and it decides which investors it accepts. The
          lawfulness of the offering in every jurisdiction where the issuer makes it available, the
          registration or exemption, the promotion, and the resale limits, is the issuer’s. SeqPal
          supplies a tested default set of restrictions and a console to tighten them, enforces exactly
          what the issuer signed off on, and does not carry the issuer’s liability for the offering.
        </p>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/85">
          SeqPal is not a broker: it does not solicit, advise, recommend, negotiate, or act in the sale,
          and the enclave gives it no key that can move a holder’s asset in a transfer. What it provides
          is the platform, the SeqPal ID identity gate, the transfer agent, and, if the issuer hires it,
          escrow. Those services are licensed by the RFSA and structured to require licensing only in
          Próspera: the no-solicitation posture, and an escrow fee that is due whether or not the offering
          closes, keep them clear of broker-dealer, MiFID, and arranging licensing anywhere else.
        </p>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/85">
          Escrow is optional. The issuer runs its own placement portal, on its own domain and in its own
          name, as its legal operator. If it does not hire SeqPal for escrow, subscription payments go
          directly to the issuer. If it does, SeqPal holds the funds in USDX, BTC, or fiat and settles
          delivery against payment, a custody-and-settlement service under its Próspera Escrow Agent and
          Money Transmitter licences, charged over the holding period whether or not the offering closes.
        </p>
      </div>

      {/* Keys and control */}
      <div className="card mt-6 p-6">
        <h2 className="font-bold text-ink-900">Keys and control</h2>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/85">
          Every asset uses the issuing entity’s own key as the enclave issuer half, held in the issuer’s
          browser. A clawback is two-phase and needs the issuer’s signature, so the platform holds no key
          that can move a holder’s position, and SeqPal is not the custodian of investor positions.
        </p>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/85">
          A normal transfer is signed by the holder and by SeqPal’s policy key. That policy co-signature
          is negative control: it can refuse a transfer to enforce the issuer’s rules, but it cannot
          originate one. The issuer’s key is used only for the discrete actions the issuer takes in the
          browser, deploying the asset, authorising a close, and exercising a clawback, never for routine
          transfers, so the issuer runs no server. Each holder’s assets sit in their own enclave, not a
          pooled account.
        </p>
      </div>

      {/* Per-jurisdiction: the issuer's obligations */}
      <div className="mt-8">
        <h2 className="text-xl font-bold text-ink-900">
          What the issuer is responsible for, by investor jurisdiction
        </h2>
        <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
          The RFSA licence covers SeqPal in Próspera; it does not reach the investor’s home country,
          where the investor’s securities law binds the offer, and binds the issuer as principal. US,
          EU and UK are worked examples. The issuer sets policy across the full country matrix, which
          starts at qualified investors only and admits nothing the issuer has not added.
        </p>
        <div className="mt-4 space-y-3">
          {JURISDICTIONS.map((j) => (
            <div key={j.place} className="card p-5">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <h3 className="font-semibold text-ink-900">{j.place}</h3>
                <Badge color="seq">{j.regime}</Badge>
              </div>
              <p className="mt-1.5 text-sm leading-relaxed text-ink-700/80">{j.body}</p>
            </div>
          ))}
        </div>
        <p className="mt-3 text-sm leading-relaxed text-ink-700/70">
          The platform compiles the issuer’s matrix into policy-server rules, so the restrictions hold
          on every transfer for the life of the asset. A country the issuer has not admitted carries no
          eligibility category, and a resident of it is refused. Only the OFAC- and FATF-aligned
          sanctions floor is fixed, and it can never be admitted.
        </p>
      </div>

      {/* Tax, FATCA and CRS */}
      <div className="card mt-6 p-6">
        <h2 className="font-bold text-ink-900">Tax, FATCA and CRS</h2>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/80">
          Próspera levies a 1% gross-income tax on entities operating under its jurisdiction, which
          covers both SeqPal’s revenue and the issuer LLC’s, and it charges no capital-gains tax on
          token transfers between holders. An investor’s tax is their own country’s: SeqPal’s
          transfer-agent service produces year-end statements for the investor’s domestic filing, and
          gives no tax advice. As a registrar holding investor accounts, SeqPal is likely a Financial
          Institution under CRS, a Próspera-side duty, so onboarding collects a CRS and FATCA
          self-certification and the servicing engine issues a labeled-simulated annual report.
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
