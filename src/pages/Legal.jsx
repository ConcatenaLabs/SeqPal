import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Icon } from '../components/icons'
import { Badge } from '../components/ui'
import CopyId from '../components/CopyId'
import { rfsaLookup } from '../lib/api'
import { LEGACY_ASSETS } from '../data/honesty'

// Legal & Licensing. The posture, stated as a conclusion and grounded in the
// business plan (sections 1.8, 5.1-5.9): SeqPal is licensed only in Próspera, by
// the RFSA, for the infrastructure functions it performs there, and it is scoped
// so that nothing it does requires a licence in an investor's home country. On
// every issuance the issuer is the principal and SeqPal is the enforcement agent
// operating the issuer's signed-off configuration. Próspera is the jurisdiction;
// the RFSA is its regulator; the two are named separately throughout. Public page.

// SeqPal's RFSA licence slate. All of these are Próspera-side, under Financial
// Regulation A and FinTech Regulation A; none is a foreign licence. Numbers are
// demo. Status notes follow the plan: the transfer-agent category is by Optimal
// Regulation petition, and the Investment Company licence activates with the first
// public offering or DR programme.
const LICENSES = [
  {
    fn: 'FinTech services (Crowdfunding Platform)',
    num: 'RFSA-FT-2026-0007',
    note: 'The foundational platform licence, under FinTech Regulation A. SeqPal runs no trading venue and executes no orders, so no brokerage or trading-venue licence arises.',
  },
  {
    fn: 'Escrow Agent',
    num: 'RFSA-EA-2026-0018',
    note: 'Holding subscription assets in escrow for primary placements.',
  },
  {
    fn: 'Money Transmitting Business',
    num: 'RFSA-MT-2026-0023',
    note: 'Moving funds on behalf of issuers and investors: distributions and the cash leg of DR mint and redeem.',
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
    note: 'For sponsoring and certifying public offerings; activates with the first public offering or DR programme.',
  },
]

// What the ISSUER is responsible for in the investor's home jurisdiction, and why
// SeqPal's own role does not trigger a licence there. These are the issuer's
// obligations as principal (plan section 5.3, 5.5, 5.6): the badge names the
// issuer's regime, and the body states which activity is the issuer's. US, EU and
// UK are worked examples; the issuer configures the full country matrix, which
// admits qualified investors only by default and admits nothing outside it.
const JURISDICTIONS = [
  {
    place: 'United States',
    regime: 'Issuer: Reg S / Rule 506(c)',
    body: 'The issuer, not SeqPal, offers and places the securities. Reg S offshore is the default for non-US investors; US persons are admitted only through the Rule 506(c) verified-accredited path, enforced cryptographically by the SeqPal ID gate, and the tokens are restricted securities with limited resale. That gate is the strongest available implementation of the 1998 SEC Internet Guidance safe harbor against a directed selling effort. SeqPal does not solicit, advise, recommend, or place, and takes no compensation contingent on a sale closing, so it is not a broker-dealer.',
  },
  {
    place: 'European Union',
    regime: 'Issuer: Prospectus Regulation Art. 1(4)',
    body: 'MiCA does not apply to securities, which are MiFID II financial instruments; tokenized securities are governed by the Prospectus Regulation and national private-placement regimes. The issuer relies on the Article 1(4) exemptions, qualified investors plus the per-member-state offeree tail the platform counts, and an AIF-classified structure additionally engages AIFMD. SeqPal provides no MiFID II investment service, no reception or transmission of orders, no placement, and no advice.',
  },
  {
    place: 'United Kingdom',
    regime: 'Issuer: FSMA financial promotion',
    body: 'The issuer makes the financial promotion, which must carry the statutory high-net-worth and sophisticated-investor statements the document engine generates in SI 2024/301 wording with the income and net-asset thresholds. Arranging deals in investments is the issuer’s activity as principal. SeqPal does not arrange, advise, or deal, so Regulated Activities Order Article 25 is the issuer’s concern, not SeqPal’s.',
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
        SeqPal is licensed only where it operates: in Próspera, by the RFSA, for the infrastructure
        services it performs there. It is scoped so that nothing it does requires a licence in an
        investor’s home country. On every issuance the issuer is the principal, and SeqPal is the
        enforcement agent that operates the configuration the issuer signs off on. This is a testnet
        proof of concept with no legal effect; the entity and the registration numbers are demo, and
        every page says which parts are simulated.
      </p>

      {/* Entity, jurisdiction, regulator: named separately */}
      <div className="card mt-10 p-6">
        <h2 className="font-bold text-ink-900">Entity, jurisdiction, and regulator</h2>
        <div className="mt-3 grid gap-3 sm:grid-cols-3">
          <div className="rounded-xl bg-ink-900/[0.03] px-4 py-3">
            <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">Entity</div>
            <div className="mt-1 font-semibold text-ink-900">SeqPal LLC</div>
            <div className="text-sm text-ink-700/80">The demo platform operator.</div>
          </div>
          <div className="rounded-xl bg-ink-900/[0.03] px-4 py-3">
            <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
              Jurisdiction
            </div>
            <div className="mt-1 font-semibold text-ink-900">Próspera ZEDE</div>
            <div className="text-sm text-ink-700/80">
              A Zone for Employment and Economic Development on Roatán, Honduras, where SeqPal and the
              issuer’s offering are domiciled. A Próspera permit confers intergovernmental immunity
              within the zone for permitted activities.
            </div>
          </div>
          <div className="rounded-xl bg-ink-900/[0.03] px-4 py-3">
            <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">Regulator</div>
            <div className="mt-1 font-semibold text-ink-900">The RFSA</div>
            <div className="text-sm text-ink-700/80">
              The regulator for financial-services activity conducted in or from Próspera, under
              Financial Regulation A and FinTech Regulation A. Próspera is the place; the RFSA is the
              regulator; they are not the same thing.
            </div>
          </div>
        </div>
      </div>

      {/* Licence slate */}
      <div className="card mt-6 p-6">
        <h2 className="font-bold text-ink-900">SeqPal’s RFSA licence slate</h2>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/80">
          SeqPal’s functions span several regulated activities, each with its own RFSA licence. All of
          them are Próspera-side; none is a foreign licence. SeqPal is paid for these services by the
          issuer.
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

      {/* Who is responsible for what: the core positioning (plan 1.8, 5.5.3) */}
      <div className="card mt-6 p-6">
        <h2 className="font-bold text-ink-900">Who is responsible for what</h2>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/85">
          On each issuance the issuer LLC is the principal: it owns the claim the token represents, it
          is the offering party the RFSA and any foreign regulator looks to, and it is legally
          responsible for choosing which investors it accepts. SeqPal’s role is operational. It gives
          the issuer a tested template of suggested minimum restrictions, a console to make them
          stricter, and it then enforces the exact configuration the issuer reviewed and signed off on.
          SeqPal does not, and cannot, take on the per-issuance liability of having chosen the legal
          posture of someone else’s offering.
        </p>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/85">
          That is also why SeqPal needs no licence in the investor’s home country. It does not solicit
          investors, does not advise or recommend, does not negotiate terms, does not act as principal
          or agent in the sale itself, and has no personnel doing any of these. Those are the
          activities that would trigger a broker-dealer licence in the United States, a MiFID II
          investment-services authorisation in the European Union, or arranging permission in the
          United Kingdom. SeqPal performs none of them.
        </p>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/85">
          On compensation: SeqPal’s Escrow and Settlement Fee is a charge for the escrow custody and
          on-chain settlement of the subscription funds it holds, accruing over the period they are
          held and payable whether or not the offering closes. Because it is not contingent on a sale
          completing, it compensates a custody-and-settlement service rather than a placement, which is
          the line the SEC drew in the FundersClub and AngelList no-action letters. A single focused US
          Reg D, Reg S, and 506(c) confirmation opinion is commissioned at launch as belt-and-braces,
          not as a precondition.
        </p>
      </div>

      {/* Custody conclusion */}
      <div className="card mt-6 border-amber-200 p-6">
        <div className="flex items-center gap-2">
          <Icon.shield width={18} height={18} className="text-amber-600" />
          <h2 className="font-bold text-ink-900">The custody conclusion</h2>
        </div>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/85">
          For a clawback-enabled asset whose issuer key is held by the platform, SeqPal can move any
          holder’s position without the holder’s key. That is control amounting to custody under
          essentially any test, so we state it as a conclusion rather than a maybe. It is a risk
          factor in every offering memorandum and it qualifies every self-custodial claim for those
          assets.
        </p>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/85">
          This now applies to legacy-path assets only: those that predate the external issuer key. A
          new asset instead uses the issuing entity’s own SeqPal ID key as the enclave issuer half, so
          a clawback is two-phase and cannot be broadcast without the issuer’s signature. The platform
          holds only the policy key on those assets and cannot move a holder’s position on its own.
          Each asset discloses which path it is on, in its freeze and clawback console.
        </p>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/85">
          The 2-of-2 co-signature is a separate thing and is disclosed separately on both paths: it is
          negative control, the power to refuse a transfer, not the power to move funds on its own, and
          the platform continues to operate the policy server that co-signs. The roadmap to remove the
          custody conclusion for the remaining legacy-path key is the FROST threshold quorum, tracked
          on the{' '}
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
          The platform-held issuer key, on legacy-path assets, is a single LocalKeySigner: one key, one
          token, one box, one disk. A compromise of that one credential is a compromise of every legacy
          asset it can clawback. The mitigation today is operational, a documented backup and restore
          procedure, and the structural fix is the FROST quorum. A new asset is outside this blast
          radius entirely: its issuer key never touches the platform, so there is no platform credential
          that can seize it. We disclose the blast radius rather than imply isolation that does not
          exist.
        </p>
      </div>

      {/* Per-jurisdiction: the ISSUER's obligations, worked examples */}
      <div className="mt-8">
        <h2 className="text-xl font-bold text-ink-900">
          What the issuer is responsible for, by investor jurisdiction
        </h2>
        <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
          The RFSA licence covers SeqPal in Próspera; it does not reach the investor’s home country,
          where the investor’s own securities law binds the offer. That law binds the issuer, as
          principal. US, EU and UK below are worked examples; the issuer configures the full country
          matrix, which suggests qualified investors only by default and admits nothing it does not
          list.
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
          The platform compiles the issuer’s matrix into policy-server rules, so the restrictions the
          issuer configures are enforced by the network on every transfer, for the life of the asset,
          not by a promise. A country the issuer has not admitted matches no eligibility category, so a
          resident of it is refused by the policy server. Only the sanctions floor, OFAC- and
          FATF-aligned blocks, is fixed and cannot be admitted.
        </p>
      </div>

      {/* USDX non-custodial model */}
      <div className="card mt-8 p-6">
        <h2 className="font-bold text-ink-900">Non-custodial USDX commitment model</h2>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/80">
          The escrow role above is a real, licensed custody function for the funds it holds. The
          production design for the USDX rail removes even that: the investor funds a pay-to-taproot
          address that only they control, and delivery versus payment settles atomically against it, so
          the funds are never in SeqPal custody before settlement. This is the successor to custodial
          escrow for the USDX rail, and the platform says which model an offering is on.
        </p>
      </div>

      {/* Tax + FATCA/CRS (plan 5.8) */}
      <div className="card mt-6 p-6">
        <h2 className="font-bold text-ink-900">Tax, FATCA and CRS</h2>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/80">
          Próspera applies a 1% gross-income tax to entities operating under its jurisdiction, which
          covers SeqPal’s revenues and the issuer LLC’s. There is no Próspera capital-gains tax on
          token transfers between holders. An investor’s own tax is their home jurisdiction’s: SeqPal’s
          transfer-agent service produces year-end statements for the investor to use in their domestic
          filing, and SeqPal does not give tax advice. As a registrar and transfer agent keeping
          investor accounts, SeqPal is plausibly a Financial Institution under CRS, a Próspera-side
          obligation, so onboarding collects a CRS and FATCA self-certification and the servicing engine
          emits a labeled-simulated annual reporting artifact.
        </p>
      </div>

      {/* Legacy assets */}
      <div className="card mt-6 p-6">
        <h2 className="font-bold text-ink-900">Legacy assets</h2>
        <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
          {LEGACY_ASSETS.map((a) => a.ticker).join(' and ')} are pre-overhaul demo assets, deprecated
          and excluded from the platform. They are unregistered in the asset registry because the entity
          domain must be committed at issue time and cannot be retrofitted.
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
