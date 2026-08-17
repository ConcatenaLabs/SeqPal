import { Link } from 'react-router-dom'
import { Icon } from '../components/icons'
import { Badge } from '../components/ui'

// The Documentation surface. This is the ONE place in the SPA where the
// underlying protocol names appear: everywhere else (the wizard, product,
// pricing, and checkout surfaces) speaks plain business language and links
// here for the technically curious. The copy-gate test enforces that split.

function Section({ icon: Ic, title, children }) {
  return (
    <div className="card mt-6 p-7">
      <div className="flex items-center gap-2">
        <Ic width={18} height={18} className="text-seq-600" />
        <h2 className="text-xl font-bold text-ink-900">{title}</h2>
      </div>
      <div className="mt-3 space-y-3 text-sm leading-relaxed text-ink-700/85">{children}</div>
    </div>
  )
}

export default function Docs() {
  return (
    <section className="container-x max-w-4xl py-14">
      <div className="eyebrow">Documentation</div>
      <h1 className="mt-3 text-3xl font-extrabold tracking-tight text-ink-900 sm:text-4xl">
        How SeqPal works, in detail
      </h1>
      <p className="mt-4 text-lg leading-relaxed text-ink-700/90">
        The rest of the platform speaks plain language. This page names the machinery
        underneath it, for auditors, integrators, and the technically curious, and links to
        the public records you can check without trusting SeqPal.
      </p>

      <Section icon={Icon.shield} title="How enforcement works: the three models">
        <p>
          When you issue a token you choose who can hold it and who enforces the rules.
          Each choice maps to a distinct protocol mechanism:
        </p>
        <div className="space-y-3">
          <div className="rounded-xl border border-ink-900/10 bg-ink-900/[0.02] p-4">
            <div className="flex items-center gap-2">
              <span className="font-bold text-ink-900">SeqPal enforces your rules</span>
              <Badge color="btc">OpenAMP</Badge>
            </div>
            <p className="mt-1.5">
              The standard model is <span className="font-semibold">OpenAMP</span>: each
              holding lives in a 2-of-2 taproot address called an enclave, one key held by
              the holder&rsquo;s browser and one by the OpenAMP policy server. Every
              transfer needs the policy server&rsquo;s co-signature, which it grants only
              after checking the issuer&rsquo;s compiled rules (jurisdiction and
              accreditation categories, lockups, holder caps, holding-period windows).
              Refusals are real and recorded in the public transparency log. Because the
              co-signature sits on every spend, the rule set can be rich and rule changes
              take effect immediately; the trade-off is that transfers pause if the policy
              server is down. Confidentiality is per transfer, not per asset: any holder
              can elect, transfer by transfer, to blind the amount and asset tag on chain
              (a confidential transaction to a blinded address the policy server derives),
              while the issuer and the policy server keep full sight through the policy
              server&rsquo;s blinding keys. Transparent stays the default, and a supervised
              (freely-tradable) asset is always transparent, because consensus must read
              its outputs to enforce a freeze.
            </p>
          </div>
          <div className="rounded-xl border border-ink-900/10 bg-ink-900/[0.02] p-4">
            <div className="flex items-center gap-2">
              <span className="font-bold text-ink-900">The network enforces your rules</span>
              <Badge color="seq">OpenDAMP</Badge>
            </div>
            <p className="mt-1.5">
              The network-enforced model is <span className="font-semibold">OpenDAMP</span>
              (decentralized asset management policy): the issuer&rsquo;s rules, approved
              lists, blocked lists, and transfer limits are published as on-chain
              covenants that the Sequentia network&rsquo;s own validation enforces on every
              spend. No SeqPal service touches a transfer: SeqPal verifies investors,
              publishes the rule lists, and services the register, and trading between
              approved holders keeps working even if every SeqPal service is offline. The
              trade-offs follow from the mechanism: rules are simpler than OpenAMP&rsquo;s,
              a rule change or a newly verified investor takes effect when the updated
              list is published on chain rather than instantly, and holdings are public.
            </p>
          </div>
          <div className="rounded-xl border border-ink-900/10 bg-ink-900/[0.02] p-4">
            <div className="flex items-center gap-2">
              <span className="font-bold text-ink-900">Freely tradable</span>
              <Badge color="rose">Supervised asset</Badge>
            </div>
            <p className="mt-1.5">
              The freely-tradable model issues a{' '}
              <span className="font-semibold">supervised asset</span>: an ordinary bearer
              token with no transfer restrictions, plus one consensus-level power, the
              freeze. The issuer&rsquo;s supervision key can freeze a specific balance,
              and the Sequentia network&rsquo;s consensus rules refuse to spend a frozen
              output, so the freeze binds every node, not just SeqPal&rsquo;s services.
              SeqPal exposes this power only against a court or regulator order, and the
              order document&rsquo;s sha256 fingerprint is recorded publicly beside the
              freeze. A second, offline recovery key is registered at issuance so a stolen
              everyday key can be replaced. Identity checks survive at the edges: primary
              sale buyers are verified, and dividend claims and votes require a signed
              holding proof from a verified identity.
            </p>
          </div>
        </div>
      </Section>

      <Section icon={Icon.check} title="Verify independently">
        <p>
          You do not have to trust SeqPal for any financial fact about an asset it manages.
          The verification explainer walks from an offering&rsquo;s terms document, to its
          canonical hash, to the on-chain contract commitment, to the policy-key
          commitment, and through the anchored amendment chain, all recomputed in your own
          browser.
        </p>
        <Link to="/docs/verify" className="btn-outline mt-1 inline-flex">
          <Icon.shield width={15} height={15} /> Open the verification explainer
        </Link>
      </Section>

      <Section icon={Icon.lock} title="Key custody">
        <p>
          Every key that can move value is generated in a browser and never leaves it. A
          SeqPal ID is a secp256k1 keypair created client-side; only the x-only public key
          reaches the server, and the private key is persisted solely as an AES-GCM
          envelope encrypted under the user&rsquo;s passphrase. In the OpenAMP model the
          holder&rsquo;s key is one half of the 2-of-2 enclave, which makes it negative
          control: it can refuse a transfer but cannot, alone, move a clawback-enabled
          position. The issuer key on every asset is the issuing entity&rsquo;s own browser
          key, so reclaiming tokens is two-phase, the issuer signs first and the policy
          server co-signs, and the platform holds no key that can move a holder&rsquo;s
          position. For freely-tradable assets the issuer additionally generates a recovery
          key, exported to an encrypted offline backup before deploy, whose public half is
          registered on the asset as the replacement path for a stolen issuer key.
        </p>
        <p>
          Application-layer signatures (login challenges, document e-signatures, mandates,
          attestations, holding proofs) are always made over domain-tagged hashes, never
          over raw externally supplied digests, so no flow can be turned into a signing
          oracle over a spendable transaction. The few deliberate raw-digest signers
          (transfer sighashes, clawback sighashes, supervision freeze messages) sign only
          material the signer themselves initiated, after checking the key it binds is
          their own.
        </p>
      </Section>

      <Section icon={Icon.building} title="Business continuity and the signer seam">
        <p>
          This deployment runs on a single host: the OpenAMP policy server (openampd, whose
          datadir holds the policy key and the enclave index), the platform backend
          (seqpald, the books and records), and the bearer issuer token that gates minting
          all live on that one server, with backups and a restore runbook. The
          plain-language disclosure of what pauses and what keeps trading during an outage
          is on the Legal and Status pages; mechanically, OpenAMP transfers need the
          co-signer and therefore pause, while OpenDAMP and supervised bearer assets are
          enforced by the chain itself and keep settling.
        </p>
        <p>
          The policy key is reached only through a signer interface, a deliberate seam in
          the software: nothing else in the stack knows whether a signature came from the
          single in-process signer or from a threshold-signing backend where a quorum of
          parties signs jointly for the same public key. Enclave addresses and asset ids
          commit to the policy public key, not to the machinery behind it, so swapping the
          backend behind the seam changes no address and no asset id, and no issued asset
          is ever stranded by a signing upgrade.
        </p>
      </Section>

      <Section icon={Icon.external} title="Public records">
        <p>
          Everything the enforcement machinery does leaves a public trail you can read
          without an account:
        </p>
        <div className="flex flex-wrap gap-2">
          <a href="/openamp/v1/log" target="_blank" rel="noopener noreferrer" className="btn-outline">
            <Icon.external width={15} height={15} /> The public transparency log
          </a>
          <a href="/registry/" target="_blank" rel="noopener noreferrer" className="btn-outline">
            <Icon.external width={15} height={15} /> The Sequentia asset registry
          </a>
        </div>
        <p className="text-xs text-ink-700/60">
          The transparency log is hash-chained and anchored on chain, so a removed or
          altered entry is detectable. The registry re-derives each asset id from its
          issuance output and contract hash before calling it verified.
        </p>
      </Section>

      <div className="mt-10 flex flex-wrap gap-4 text-sm">
        <Link to="/status" className="font-medium text-seq-600 hover:underline">
          Status and honesty
        </Link>
        <Link to="/faq" className="font-medium text-seq-600 hover:underline">
          FAQ
        </Link>
        <Link to="/legal" className="font-medium text-seq-600 hover:underline">
          Legal and Licensing
        </Link>
        <Link to="/privacy" className="font-medium text-seq-600 hover:underline">
          Privacy
        </Link>
      </div>
    </section>
  )
}
