import { Link } from 'react-router-dom'
import { Icon } from '../components/icons'

// The public FAQ. Ungated, because the people who most need it are deciding whether
// to trust the platform at all. It is a compliance artifact, not marketing: it draws
// the boundaries of what SeqPal is and is not, what it is licensed to do and where
// (Próspera, by the RFSA), and what stays the issuer's responsibility as principal.
// Plain language, no privileged-asset framing, no promise the platform cannot keep.

// Each item's answer is an array of paragraphs (strings) or a small element.
const SECTIONS = [
  {
    title: 'What SeqPal is, and is not',
    items: [
      {
        q: 'What is SeqPal?',
        a: [
          'SeqPal is the infrastructure and the transfer agent for tokenized securities. It gives issuers the tooling to structure, issue, and service a compliant offering, and it enforces, on every transfer for the life of the asset, the eligibility restrictions the issuer configures. In this proof of concept the licences and the registration numbers are labeled simulations, and every page says which parts are simulated.',
          'Mechanically, SeqPal does identity and eligibility, policy-enforced transfer restrictions, the register, escrow and settlement, servicing, the documents, and the transparency log. It never chooses your structure for you, warrants that your terms are lawful, acts as your counsel, or absorbs your liability.',
        ],
      },
      {
        q: 'Is SeqPal a law firm? Is it responsible if I break the law?',
        a: [
          'No. SeqPal is not a law firm, it does not give legal advice, and it does not opine on your offering. On each issuance you, the issuer, are the principal: you own the claim the token represents, you are the party a regulator looks to, and you are responsible for the lawfulness of your offering in every jurisdiction where you make it available. SeqPal is the enforcement agent that operates the configuration you sign off on. It does not absorb your liability.',
        ],
      },
      {
        q: 'Is SeqPal a broker or a placement agent?',
        a: [
          'No. SeqPal does not solicit investors, does not advise or recommend, does not negotiate terms, and does not act as principal or agent in the sale itself. Those are the activities that make a firm a broker-dealer in the US, a MiFID II investment firm in the EU, or an arranger in the UK, and SeqPal performs none of them. You place your own offering as principal.',
          'SeqPal charges you, the issuer, for the platform and, if you hire it, the escrow-and-settlement service. The escrow fee accrues on the funds held and is payable whether or not the offering closes, so it pays for a custody-and-settlement service rather than a completed sale, which is what keeps it off the placement-commission side of the line the SEC drew in the FundersClub and AngelList no-action letters.',
        ],
      },
    ],
  },
  {
    title: 'What Sequentia gives you, and what it does not replace',
    items: [
      {
        q: 'What does issuing on the Sequentia network actually give me?',
        a: [
          'A real secondary market with access to Bitcoin liquidity, native BTC settlement on the parent chain rather than a wrapped token, Bitcoin-anchored finality, fees payable in any accepted asset, an open and auditable register, and restrictions that persist for the life of the asset because every transfer is checked against your rules.',
          'A restricted asset is one row among equals next to BTC and every other asset on the network. It is permissioned, not privileged: it can only move between eligible, registered holders, and each transfer is checked against the issuer’s rules. Issuers who want the opposite can choose the freely-tradable option instead: anyone in the world can hold and trade that token, buyers in the initial sale are still identity-checked, and specific balances can be frozen only on a court or regulator order.',
        ],
      },
      {
        q: 'Does Sequentia bring me investors?',
        a: [
          'No. Sequentia does not bring you a primary market. You still have to find and qualify your own investors, and nobody on the network will do that for you. A listing is not a market: the network does not create demand, does not underwrite, and does not make an illiquid asset liquid.',
          'Secondary trading of a restricted asset is deliberately narrow: only eligible, registered holders may acquire it, and a venue can check eligibility but never grant it. A refusal at settlement is an expected outcome.',
        ],
      },
      {
        q: 'Can an investor pay in Bitcoin?',
        a: [
          'Native BTC works, and it settles on the parent chain, not as a wrapped token. One limit is real: the restricted leg cannot sit in a hash-locked swap output, so a BTC purchase routes through the cross-chain mechanism in the integration spec instead of a plain atomic swap, and its trust trade-offs are shown at the point of use.',
        ],
      },
    ],
  },
  {
    title: 'What Próspera and the RFSA give you, and what they do not replace',
    items: [
      {
        q: 'Why Próspera, and what is the RFSA?',
        a: [
          'Próspera is the jurisdiction: a Zone for Employment and Economic Development on Roatán, Honduras, where SeqPal and the issuer’s offering are domiciled. The RFSA is its financial-services regulator.',
          'SeqPal is licensed by the RFSA for the infrastructure it runs, and Próspera’s regime is genuinely favourable to tokenized issuance. That is a real advantage on the issuance side.',
        ],
      },
      {
        q: 'Does being domiciled in Próspera cover my investors?',
        a: [
          'No. SeqPal’s Próspera licensing is necessary but not sufficient. Every token sold to an investor is also subject to that investor’s home securities law, and that law binds you, the issuer, as the offering party.',
          'Concretely: a public web page can be a financial promotion in many places; US persons are admitted only through Rule 506(c) verified accreditation, with Reg S offshore as the default, and their tokens are restricted securities whose resale is limited; EU access relies on the Prospectus Regulation qualified-investor exemptions and, for some structures, engages AIFMD; the UK requires statutory investor statements. A country you do not admit is excluded by default, and that default protects you.',
          'This is why the platform compiles your jurisdiction matrix into enforceable rules: the restrictions you configure are checked on every transfer for the life of the asset. SeqPal supplies a suggested minimum; the final policy is yours, and you can make it stricter, or with documented authority broader, everywhere except the sanctions floor.',
        ],
      },
    ],
  },
  {
    title: 'Identity, eligibility, and access',
    items: [
      {
        q: 'What is a SeqPal ID?',
        a: [
          'A SeqPal ID is the identity layer, and there is one registration flow, identical for everyone; it never asks whether you are an issuer or an investor. It produces a verified identity bound to the account key of the Sequentia wallet you already have.',
          'An investor needs one to hold or trade these restricted assets anywhere on Sequentia, including on a venue that lists them, and an issuer uses the same credential to issue. "Issuer" and "investor" are things a verified identity does later, never a type chosen at signup.',
        ],
      },
      {
        q: 'What is an eligibility category, and why can it expire?',
        a: [
          'A category is a compound token such as j:DE:ret or j:US:acc that records your jurisdiction and eligibility tier. Only the platform can stamp one, and those categories are what gate every transfer of a restricted asset. A category carries a validity window, so it can expire, for example when an accreditation verification goes stale, and a real refusal follows until it is renewed.',
          'One kind of asset opts out of all of this: a freely-tradable token has no eligibility gate on trading at all, so anyone can hold and trade it. Identity still matters at the edges there: buyers in the issuer’s initial sale are identity-checked, and holders verify their identity to collect dividends or vote.',
        ],
      },
      {
        q: 'What can the platform see and do about my holdings?',
        a: [
          'For a standard restricted asset, SeqPal keeps the register and its transfer service checks every transfer against the issuer’s rules, so it sees your holdings and can refuse a transfer that breaks a rule. It cannot move your position on its own: the key that can reclaim tokens is the issuing entity’s own, held in the issuer’s browser, so a seizure needs the issuer’s signature. Making a transfer confidential changes none of this: it hides that transfer from outside observers only, while the issuer and SeqPal’s transfer service still see everything, because they enforce eligibility. A freely-tradable asset is different: who holds what is public, and nobody screens its trades.',
        ],
      },
    ],
  },
  {
    title: 'Custody, keys, and loss',
    items: [
      {
        q: 'What key do I hold, and what does it control?',
        a: [
          'You hold your own key, in your own Sequentia wallet: a SeqPal ID is the account that wallet already holds your restricted tokens in, so there is no separate SeqPal key to back up and your wallet’s recovery covers it. For a restricted asset it is one half of a shared 2-of-2 address, which makes it negative control: it lets you refuse a transfer, and it does not, on its own, move an asset whose issuer kept the recovery power. The key that can reclaim tokens is the issuing entity’s own wallet key, so the issuer directs a seizure and SeqPal adds the second signature. The platform holds no key that can move your position.',
        ],
      },
      {
        q: 'What happens if I lose my key?',
        a: [
          'For a clawback-enabled asset there is a runbook: claw back and re-deliver your position to a fresh AID you control. The seizure step needs the issuing entity’s signature, since the issuer key is external to the platform, and the re-delivery runs under a disclosed treasury delegation. Because the platform holds no key that can move your position, self-custodial is accurate for your holding, with the one qualification that a clawback-enabled asset can be seized by the issuer under this runbook.',
        ],
      },
    ],
  },
  {
    title: 'Money, fees, and settlement',
    items: [
      {
        q: 'Which payment rails are real?',
        a: [
          'USDX and native testnet BTC are real settlement assets. Card and bank transfer are simulated: the full checkout flow, state machine, receipts, and refund path run, but the funds are marked simulated, and every money surface derived from them carries a simulated badge at the checkout itself.',
        ],
      },
      {
        q: 'What does delivery versus payment mean here?',
        a: [
          'It means the token and the payment settle together rather than one party trusting the other to follow through. Where a rail supports it, this is atomic; where it does not, it is registrar-settled, and the platform says which. If the issuer uses SeqPal’s escrow, SeqPal holds the funds and releases the token against payment at settlement; if not, the investor pays the issuer directly.',
        ],
      },
      {
        q: 'Who pays network fees, and in what asset? What does SeqPal charge?',
        a: [
          'Network fees are paid in an accepted asset, and the amount is shown in that fee asset’s own units. There is no privileged fee coin: block proposers choose what they accept, and the Sequence token is one accepted asset among equals. SeqPal’s own platform fees are published on the Pricing page rather than quoted case by case.',
        ],
      },
    ],
  },
  {
    title: 'Confidentiality and transparency',
    items: [
      {
        q: 'Is my holding private?',
        a: [
          'Sequentia is transparent by default, and confidentiality is a choice you make per transfer, not a property of a token. When the deployment supports it, any transfer of any restricted token can be made confidential, which hides that transfer’s amount and asset from outside observers. The issuer and SeqPal’s transfer service still see every holding and every transfer, because they enforce eligibility, so it is not privacy from your registrar. Freely-tradable tokens are the exception: their transfers are always public by design, because the network itself must be able to read them.',
          'The public transparency log records policy decisions as pseudonymous AIDs and hashes, and your AID’s category tokens and frozen status are publicly readable. The Privacy page details exactly what is exposed.',
        ],
      },
    ],
  },
  {
    title: 'Finality and reorgs',
    items: [
      {
        q: 'When is a transfer final?',
        a: [
          'Nothing is final at zero confirmations. Sequentia follows Bitcoin reorgs in real time by design, which is what makes cross-chain settlement with Bitcoin safe. A state is only as final as its Bitcoin anchor is deep, and if Bitcoin reorgs, the interface regresses the state instead of showing it as final.',
        ],
      },
    ],
  },
  {
    title: 'This is a testnet proof of concept',
    items: [
      {
        q: 'What is real, and what is simulated?',
        a: [
          'No real funds, no real securities, no legal effect. Genuinely real: the keys, the assets, the transfers, the enforcement and the refusals, the register, and the log. Simulated and labeled: identity verification, incorporation, the regulator relationship, the fiat rails, and the legal instruments.',
        ],
        after: {
          to: '/status',
          label: 'See the full real-vs-simulated master list on the Status page',
        },
      },
    ],
  },
]

function Item({ item }) {
  return (
    <div className="border-t border-ink-900/10 py-5 first:border-t-0 first:pt-0">
      <h3 className="font-semibold text-ink-900">{item.q}</h3>
      <div className="mt-2 space-y-2 text-sm leading-relaxed text-ink-700/85">
        {item.a.map((p, i) => (
          <p key={i}>{p}</p>
        ))}
      </div>
      {item.after && (
        <Link
          to={item.after.to}
          className="mt-3 inline-flex items-center gap-1.5 text-sm font-medium text-seq-600 hover:underline"
        >
          {item.after.label}
          <Icon.arrowRight width={14} height={14} />
        </Link>
      )}
    </div>
  )
}

export default function Faq() {
  return (
    <section className="container-x max-w-4xl py-14">
      <div className="eyebrow">Questions and boundaries</div>
      <h1 className="mt-3 text-3xl font-extrabold tracking-tight text-ink-900 sm:text-4xl">
        Frequently asked questions
      </h1>
      <p className="mt-4 text-lg leading-relaxed text-ink-700/90">
        The boundaries, drawn plainly: what SeqPal is and is not, what it is licensed to do and where,
        and what stays the issuer’s responsibility. Where something is simulated in this proof of
        concept, it says so.
      </p>

      <div className="mt-10 space-y-8">
        {SECTIONS.map((sec) => (
          <div key={sec.title} className="card p-6">
            <h2 className="text-xl font-bold text-ink-900">{sec.title}</h2>
            <div className="mt-4">
              {sec.items.map((it) => (
                <Item key={it.q} item={it} />
              ))}
            </div>
          </div>
        ))}
      </div>

      <div className="mt-10 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-5 py-4">
        <div className="flex items-center gap-2 font-semibold text-ink-900">
          <Icon.shield width={16} height={16} /> Verify it yourself
        </div>
        <p className="mt-1.5 text-sm leading-relaxed text-ink-700/80">
          You do not have to trust any of this. The verification explainer on the Documentation
          pages walks you from an offering’s terms document to its on-chain commitments, all
          recomputed in your own browser.
        </p>
        <div className="mt-3 flex flex-wrap gap-4 text-sm">
          <Link to="/docs/verify" className="font-medium text-seq-600 hover:underline">
            Verify independently
          </Link>
          <Link to="/docs" className="font-medium text-seq-600 hover:underline">
            Documentation
          </Link>
          <Link to="/legal" className="font-medium text-seq-600 hover:underline">
            Legal and Licensing
          </Link>
          <Link to="/privacy" className="font-medium text-seq-600 hover:underline">
            Privacy
          </Link>
          <Link to="/status" className="font-medium text-seq-600 hover:underline">
            Status
          </Link>
        </div>
      </div>
    </section>
  )
}
