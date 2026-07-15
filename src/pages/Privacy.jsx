import { Link } from 'react-router-dom'
import { Icon } from '../components/icons'
import { Badge } from '../components/ui'

// Privacy (plan 4.8). Scoped to exactly what ships. It states the partial-erasure
// reality, GDPR Art 3(2) applicability, a lawful-basis table, a labeled-simulated
// Art 27 representative, an SCC note, and the public wallet-surface exposure.

const LAWFUL_BASIS = [
  {
    purpose: 'Identity verification and KYC',
    basis: 'Compliance with a legal obligation, and legitimate interest',
    note: 'Verifying who may hold a restricted asset is what the securities restrictions require.',
  },
  {
    purpose: 'Sanctions screening',
    basis: 'Compliance with a legal obligation',
    note: 'Screening against the public OFAC, EU, and UN lists.',
  },
  {
    purpose: 'Eligibility categories on your AID',
    basis: 'Compliance with a legal obligation, and legitimate interest',
    note: 'Categories are what the policy server enforces on a transfer.',
  },
  {
    purpose: 'The public transparency log',
    basis: 'Legitimate interest in an auditable, tamper-evident record',
    note: 'The log records policy decisions as pseudonymous AIDs and hashes, and its head is anchored on chain.',
  },
  {
    purpose: 'Tax self-certification (CRS and FATCA)',
    basis: 'Compliance with a legal obligation',
    note: 'Stored in the claims record; a labeled-simulated reporting artifact is produced.',
  },
]

export default function Privacy() {
  return (
    <section className="container-x max-w-4xl py-14">
      <div className="eyebrow">Privacy</div>
      <h1 className="mt-3 text-3xl font-extrabold tracking-tight text-ink-900 sm:text-4xl">
        Privacy, scoped to what ships
      </h1>
      <p className="mt-4 text-lg leading-relaxed text-ink-700/90">
        What the platform does with your data, on a testnet proof of concept.
      </p>

      {/* Partial erasure */}
      <div className="card mt-10 border-amber-200 p-6">
        <div className="flex items-center gap-2">
          <Icon.shield width={18} height={18} className="text-amber-600" />
          <h2 className="font-bold text-ink-900">Erasure is partial</h2>
        </div>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/85">
          SeqPal can delete your server-side identity records. It cannot delete the chain or the
          anchored transparency log, which retain pseudonymous AIDs and opaque hashes that no one can
          remove. An AID with balance history linked to a server-side identity record is arguably
          personal data, so an erasure request destroys the AID-to-identity mapping and, optionally,
          rotates the AID, clawing back and re-delivering your holdings to a fresh AID. A clean delete
          of the chain records is not possible.
        </p>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/85">
          A category event in the public transparency log records the hash of your category set, not
          the raw list, so the log does not expose each holder's exact category vector. The hash stays
          verifiable: anyone who knows the set can recompute the commitment and match it.
        </p>
      </div>

      {/* GDPR applicability */}
      <div className="card mt-6 p-6">
        <h2 className="font-bold text-ink-900">GDPR Article 3(2) applicability</h2>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/80">
          Actively targeting investors in the European Union triggers GDPR Article 3(2), even though
          the platform entity is in Próspera, and SeqPal treats it as applicable. International
          transfers of EU investor data processed on the Próspera-side platform are governed by
          Standard Contractual Clauses.
        </p>
        <div className="mt-3 flex flex-wrap items-center gap-2 rounded-lg bg-ink-900/[0.03] px-4 py-3">
          <span className="text-sm font-medium text-ink-900">Article 27 EU representative</span>
          <span className="text-sm text-ink-700/80">recorded on the platform</span>
          <Badge color="amber" className="ml-auto">
            Simulated entry
          </Badge>
        </div>
      </div>

      {/* Lawful-basis table */}
      <div className="mt-8">
        <h2 className="text-xl font-bold text-ink-900">Lawful basis, per processing purpose</h2>
        <div className="mt-4 space-y-2">
          {LAWFUL_BASIS.map((r) => (
            <div key={r.purpose} className="card p-5">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <h3 className="font-semibold text-ink-900">{r.purpose}</h3>
                <Badge color="seq">{r.basis}</Badge>
              </div>
              <p className="mt-1.5 text-sm leading-relaxed text-ink-700/80">{r.note}</p>
            </div>
          ))}
        </div>
      </div>

      {/* Public wallet surface */}
      <div className="card mt-8 p-6">
        <h2 className="font-bold text-ink-900">What is public about your AID</h2>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/80">
          The policy server publicly returns each AID's category tokens and frozen status at{' '}
          <span className="font-mono text-xs">GET /v1/users/&#123;aid&#125;</span>, and balances are
          publicly queryable per AID. Once category tokens encode a jurisdiction and an eligibility
          tier, for example <span className="font-mono text-xs">j:US:acc</span>, they are
          identity-adjacent. The SeqDEX eligibility preflight depends on this surface staying readable,
          so a venue can check what you are eligible to hold. The transparency log records only a hash
          of your category set, but the live user endpoint returns the raw tokens, because eligibility
          enforcement needs them, so your category tokens are readable today.
        </p>
      </div>

      {/* Confidentiality reminder */}
      <div className="card mt-6 p-6">
        <h2 className="font-bold text-ink-900">Confidentiality is opt-in, and not privacy from your registrar</h2>
        <p className="mt-2 text-sm leading-relaxed text-ink-700/80">
          Sequentia is transparent by default. Confidentiality is opt-in per asset. An opt-in
          confidential asset blinds amounts and the asset tag on chain, but the issuer and the policy
          server still see holdings, because they have to enforce eligibility. It is not privacy from
          your registrar.
        </p>
      </div>

      <div className="mt-10 flex flex-wrap gap-4 text-sm">
        <Link to="/legal" className="font-medium text-seq-600 hover:underline">
          Legal and Licensing
        </Link>
        <Link to="/faq" className="font-medium text-seq-600 hover:underline">
          FAQ
        </Link>
        <Link to="/status" className="font-medium text-seq-600 hover:underline">
          Status
        </Link>
      </div>
    </section>
  )
}
