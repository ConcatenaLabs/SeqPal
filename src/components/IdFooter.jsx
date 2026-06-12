// Footer for the standalone SeqPal ID subsite. Identity/data-protection focused;
// carries no issuer-product marketing.
export default function IdFooter() {
  return (
    <footer className="mt-20 border-t border-ink-900/10 bg-ink-900/[0.02]">
      <div className="container-x py-10">
        <div className="text-sm font-semibold text-ink-900">
          Seq<span className="text-btc">Pal</span> ID
        </div>
        <p className="mt-2 max-w-2xl text-xs leading-relaxed text-ink-700/70">
          A consolidated KYC/KYB and accreditation passport. Verified once, it whitelists
          you for every SeqPal-issued asset your profile is eligible to hold. SeqPal is the
          data controller for your SeqPal ID record; KYC and screening vendors act as
          processors. Personal data is handled under the applicable data-protection regime
          (GDPR / UK GDPR and equivalents); you may exercise access, rectification, and
          erasure rights (subject to AML retention) via the privacy contact.
        </p>
        <p className="mt-4 text-xs text-ink-700/50">
          © {new Date().getFullYear()} SeqPal ID · id.seqpal.io —{' '}
          <span className="text-btc/80">demo preview</span>. Not investment advice; nothing
          here is an offer to sell or a solicitation to buy any security.
        </p>
      </div>
    </footer>
  )
}
