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
              lists, blocked lists, per-holder height bounds, and transfer limits are
              published as on-chain covenants that the Sequentia network&rsquo;s own
              validation enforces on every spend. No SeqPal service touches a transfer:
              SeqPal verifies investors, publishes the rule lists, and services the
              register, and trading between approved holders keeps working even if every
              SeqPal service is offline. The trade-offs follow from the mechanism: rules
              are simpler than OpenAMP&rsquo;s, a rule change or a newly verified investor
              takes effect when the updated list is published on chain rather than
              instantly, and holdings are public.
            </p>
            <p className="mt-2">
              Mechanically, an asset A is issued with a companion verifier asset V. Each
              holding sits in a user covenant C_U(X) keyed by the holder&rsquo;s own x-only
              key, and every regulated transfer must also spend the single verifier output
              C_V(pi), which carries a fixed amount q of V and runs the policy program
              P(pi). P is compiled against the policy commitment pi, so a different
              whitelist root, blacklist root or transfer limit is a different program, a
              different taproot address, and a stale proof cannot be pruned into a valid
              spend. The whitelist is a depth-16 sorted tree (dmt-v1) whose leaf is
              SHA256(0x00 || key || BE32(send_after) || BE32(recv_after)), so a lockup or a
              receive window travels inside the same proof that shows a key is approved.
              The blacklist is an interval tree over outpoint keys
              SHA256(txid_internal || BE32(vout)), which makes non-membership one ordinary
              membership proof. Removing a key stops that holder spending; listing an
              outpoint stops that one coin; both are reversible by a further update.
            </p>
            <p className="mt-2">
              A policy update is therefore two things at once: publish snapshot seq n+1,
              signed by the issuer update key I over the tagged snapshot hash, and respend
              the verifier output through the issuer path G(I) so the on-chain C_V commits
              to pi_{'{'}n+1{'}'}. Until that respend confirms, holders transfer under the
              previous policy, because the old C_V is what their transfers spend. openampd
              is authoritative about policy and recomputes both roots and pi from the
              snapshot chain it publishes; the SimplicityHL compiler in the opendamp crate
              is authoritative about program identity, so a completion supplies a CMR
              openampd cannot compile and openampd refuses it unless it is a new one whose
              derived scriptPubKey matches, and unless the supplied transaction actually
              consumes the recorded verifier outpoint and recreates C_V with exactly q of V.
            </p>
            <p className="mt-2">
              Two shipped bounds are worth stating because they are committed into the
              program identity and cannot be raised for an existing asset: the covenant
              asserts at most 4 inputs and 6 outputs, which leaves room for exactly two
              regulated inputs, so a holder may spend at most TWO coins of the asset in one
              transfer. Raising it is a budget purchase (about 2,015 weight units and 65
              pad words per extra input slot), not a design change. The covenant also
              requires an explicit asset id on every input and output it scans, so a
              network-enforced asset is transparent by construction and a blinded receipt
              could not be spent. What is NOT enforced is named in
              opendamp/STATUS.md section 2 rather than implied here.
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
          commitment, and through the amendment chain to the rules the policy server holds
          right now, all recomputed in your own browser.
        </p>
        <Link to="/docs/verify" className="btn-outline mt-1 inline-flex">
          <Icon.shield width={15} height={15} /> Open the verification explainer
        </Link>
      </Section>

      <Section icon={Icon.lock} title="Key custody">
        <p>
          SeqPal holds no key of YOURS and makes none. A SeqPal ID is the OpenAMP enclave account
          your own Sequentia wallet derives at m/5/0: the wallet registers its x-only public
          key with the policy server, which derives the account id and the enclave address
          from it, and nothing but that public key ever reaches SeqPal. Your wallet&rsquo;s
          own recovery therefore covers the key, and a security token issued to a SeqPal ID
          is one you can see and move in the wallet you already use. In the OpenAMP model the
          holder&rsquo;s key is one half of the 2-of-2 enclave, which makes it negative
          control: it can refuse a transfer but cannot, alone, move a clawback-enabled
          position. An asset deployed with an external issuer key carries the issuing
          entity&rsquo;s own wallet key, so reclaiming tokens there is two-phase: the issuer
          signs first, the policy server co-signs, and the platform holds no key that can
          move a holder&rsquo;s position. Assets deployed before that option existed carry a
          platform-held issuer key instead, and a clawback on one of those completes in a
          single call by SeqPal and the policy server together. Which of the two an asset is
          is not a detail the platform hides: the console that performs a clawback names it
          before anything is swept. For freely-tradable assets the issuer additionally names a recovery
          key, exported to an encrypted offline backup before deploy, whose public half is
          registered on the asset as the replacement path for a stolen issuer key.
        </p>
        <p>
          What the platform does hold, said plainly, is keys of its own: the enclave key of
          each company treasury it provisions at KYB, the two escrow wallets it operates on
          Sequentia and on Bitcoin testnet4, and the key it signs claims records with. A
          company treasury is an enclave account like any other, so moving what a company
          holds takes SeqPal and the policy server together; a holder&rsquo;s own position is
          not among them. This is the interim arrangement, and it is the reason an issuer
          names an external issuer key and a recovery key: those are the paths that do not
          run through this platform at all.
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
