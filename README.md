# SeqPal — Website & Onboarding Demo

A front-end / UX demo of the **SeqPal** website and issuer onboarding flow.

SeqPal is an end-to-end tokenization-as-a-service platform domiciled in Próspera
ZEDE that lets issuers structure, issue, and service compliant security tokens
on Bitcoin's Liquid Network. This repo is a **demo of what the product's
front-end and onboarding UX will look like** — it is intentionally not connected
to any backend or third-party service.

## What this demo covers

- **Marketing site** — landing page, products (with a "how it fits together"
  architecture view), the four issuance structures, and the published fee schedule.
- **SeqPal ID** — the consolidated KYC/KYB and accreditation passport, and your
  **login**. An account is created by a natural person (individual KYC); corporate
  entities (KYB) are linked on top. When signed in, it also lists the live
  offerings you're eligible to access (the auto-whitelist network effect).
- **Issuer Dashboard** — gated behind a SeqPal ID; lists your issuances, their
  lifecycle status, and placement-portal state.
- **The six-step issuer onboarding flow** (the centrepiece):
  1. Identity & principal — who applies/owns the new LLC (issuing as an
     individual is **Native Equity only**; SPV / Debt / DR require a corporate
     KYB principal). The new Próspera LLC is the issuer of record.
  2. Architecture routing — choose an issuance structure (KYB-only ones are
     locked for individual applicants)
  3. Data room — dynamic deal-term inputs per structure; private vs public offering
  4. Document automation suite — generate & e-sign the document package
  5. Tokenomics & compliance baking — name the asset and configure the
     jurisdiction/accreditation policy (Appendix C matrix, per-issuance caps,
     and the public-offering overlay) baked into the token
  6. Checkout & deployment — fixed-fee checkout (reflecting your inputs, e.g. the
     Simple Native Equity tier); the LLC is then submitted for incorporation
- **Issuance lifecycle** — checkout is not instant-live: payment → Próspera
  incorporation (1–3 business days, with an ETA) → RFSA filing → AMP deploy →
  live, shown as a timeline with a demo fast-forward.
- **Automated Transfer Agent** — schedule distributions (dividend/coupon/yield),
  process corporate actions, and mint/redeem Depository Receipts, with an
  activity log; a live Registry of Members and a Secondary Market (SideSwap) card.
- **Whitelabel Placement Portal** — set up a branded fundraising portal on your
  own domain (CNAME, escrow, issuer-as-operator terms), plus the public
  investor-facing portal with real SeqPal ID eligibility gating, subscription,
  e-sign, and escrow. Subscriptions flow back into the issuer's Fundraising view
  and, on closing, into the Registry of Members.

## What is mocked / skipped

Anything that belongs in the backend or requires a third-party subscription is
**simulated so the user can skip it**, while keeping the UI faithful to the
intended product:

- KYC/KYB identity verification (no real vendor call, no document upload)
- Payments — setup fees, ID fees, and the Escrow & Settlement Fee are never charged
- E-signature of documents and subscription agreements
- Próspera incorporation, RFSA filing, and on-chain deployment via Blockstream
  AMP (a demo "fast-forward" advances the lifecycle; nothing is broadcast)
- Escrow funding and the brokerage-custody relationship for Depository Receipts

State is kept in the browser's `localStorage`, so your account and issuances
persist across reloads. Use **Reset demo** on the dashboard to clear it.

## Running locally

```bash
npm install
npm run dev      # http://localhost:5173
```

```bash
npm run build    # production build to dist/
npm run preview  # preview the production build
```

## Tech

Vite · React · React Router · Tailwind CSS.

---

*This is a demo preview. SeqPal is a technology and services provider; it is not
a law firm and does not provide legal, tax, or investment advice. Nothing here is
an offer to sell or a solicitation to buy any security.*
