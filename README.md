# SeqPal — Website & Onboarding Demo

A front-end / UX demo of the **SeqPal** website and issuer onboarding flow.

SeqPal is an end-to-end tokenization-as-a-service platform domiciled in Próspera
ZEDE that lets issuers structure, issue, and service compliant security tokens
on Bitcoin's Liquid Network. This repo is a **demo of what the product's
front-end and onboarding UX will look like** — it is intentionally not connected
to any backend or third-party service.

## What this demo covers

- **Marketing site** — landing page, products, the four issuance structures, and
  the published fee schedule.
- **SeqPal ID** — the consolidated KYC/KYB and accreditation passport.
- **Issuer Dashboard** — lists your issuances and their status.
- **The six-step issuer onboarding flow** (the centrepiece):
  1. Identity & KYB check
  2. Architecture routing — choose an issuance structure
  3. Data room — dynamic deal-term inputs per structure
  4. Document automation suite — generate & e-sign the document package
  5. Tokenomics & compliance baking — name the asset and configure the
     jurisdiction/accreditation policy baked into the token
  6. Checkout & deployment — fixed-fee checkout and on-chain deployment

## What is mocked / skipped

Anything that belongs in the backend or requires a third-party subscription is
**simulated so the user can skip it**, while keeping the UI faithful to the
intended product:

- KYC/KYB identity verification (no real vendor call, no document upload)
- Payments — setup fees, ID fees, and the Platform Services Fee are never charged
- E-signature of the document package
- On-chain deployment via Blockstream AMP, Próspera e-registry, and RFSA filing
  (animated progress only; nothing is broadcast to any network)

State is kept in the browser's `localStorage`, so created issuances persist
across reloads. Use **Reset demo** on the dashboard to clear it.

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
