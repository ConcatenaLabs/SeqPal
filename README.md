# SeqPal — tokenization-as-a-service on Sequentia

SeqPal is an end-to-end tokenization-as-a-service platform domiciled in Próspera
ZEDE that lets issuers structure, issue, and service compliant security tokens on
**Sequentia**, the Bitcoin sidechain. Restricted assets are issued and enforced
through **OpenAMP**, the open-source policy-server layer for issuer-governed
assets (KYC whitelists, categories, velocity, vesting, freeze, clawback).

This repo is the SeqPal product PoC: a faithful front-end plus a thin backend
that **actually issues restricted assets on the Sequentia testnet** through a
live OpenAMP policy server. The compliance and legal scaffolding around it
(identity verification, payments, incorporation, e-signature) is simulated so the
flow can be walked end to end, but the token deployment itself is real.

## What is real vs simulated

**Real (on the Sequentia testnet):**

- Each SeqPal ID generates a real secp256k1 enclave keypair (`src/lib/keys.js`).
  The private key stays in the browser; only the x-only public key is registered
  with OpenAMP, from which the policy server derives the account id (AID) and the
  2-of-2 enclave address restricted assets live in.
- Deploying an issuance mints a real OpenAMP restricted asset: the SeqPal backend
  (`seqpald`) registers the issuer's enclave key and calls OpenAMP's issuer API,
  which builds and broadcasts the issuance transaction on Sequentia. The returned
  asset id, txid, contract hash and enclave AID are shown on the issuance page.
- Clawback is on by default, and the on-chain contract commits to a `terms_hash`
  binding SeqPal's compliance configuration (structure, jurisdiction matrix, deal
  terms) to the asset.
- Confidentiality is a per-transfer choice, not an asset property. Every deploy
  is a transparent mint (Sequentia is transparent by default), and any holder may
  move any restricted asset confidentially in a given transfer, blinding that
  transfer's amount and asset tag on-chain while the issuer and registrar still
  see everything through the policy server's blinding keys. The capability is
  gated per deployment (`SEQPALD_CONFIDENTIAL`); without it a confidential
  transfer is refused with a 501, never silently downgraded. Supervised
  (freely-tradable) assets are always transparent, by consensus.

**Simulated (kept faithful to the intended product):**

- KYC/KYB identity verification (no real vendor call, no document upload).
- Payments — setup fees, ID fees, and the Escrow & Settlement Fee are never charged.
- E-signature of documents and subscription agreements.
- Próspera incorporation and RFSA filing (a demo "fast-forward" advances the
  incorporation and filing steps; only the final deploy step touches the chain).
- Escrow funding and the brokerage-custody relationship for Depository Receipts.

Front-end state (your account, issuances, subscriptions) is kept in the browser's
`localStorage`. Use **Reset demo** on the dashboard to clear it. Clearing it does
not undo an already-minted on-chain asset.

## What the product covers

- **Marketing site** — landing page, products (with a "how it fits together"
  architecture view), the four issuance structures, and the published fee schedule.
- **SeqPal ID** — the consolidated KYC/KYB and accreditation passport, and your
  login. Created by a natural person (individual KYC); corporate entities (KYB)
  are linked on top. When signed in, it lists the live offerings you're eligible
  to access (the auto-whitelist network effect).
- **Issuer Dashboard** — gated behind a SeqPal ID; lists your issuances, their
  lifecycle status, and placement-portal state.
- **The six-step issuer onboarding flow** (the centrepiece):
  1. Identity & principal — who applies/owns the new LLC (issuing as an individual
     is Native Equity only; SPV / Debt / DR require a corporate KYB principal). The
     new Próspera LLC is the issuer of record.
  2. Architecture routing — choose an issuance structure (KYB-only ones are locked
     for individual applicants).
  3. Data room — dynamic deal-term inputs per structure; private vs public offering.
  4. Document automation suite — generate and e-sign the document package.
  5. Tokenomics & compliance — name the asset and configure the
     jurisdiction/accreditation policy (Appendix C matrix, per-issuance caps, and
     the public-offering overlay) baked into the token.
  6. Checkout — fixed-fee checkout; the LLC is submitted for incorporation.
- **Issuance lifecycle** — checkout is not instant-live: payment → Próspera
  incorporation → RFSA filing → OpenAMP deployment → live, shown as a timeline.
  The deploy step performs the real OpenAMP mint.
- **Automated Transfer Agent** — schedule distributions (dividend/coupon/yield),
  process corporate actions, and mint/redeem Depository Receipts, with an activity
  log; a Registry of Members and a Secondary Market card (assets can be listed on
  Sequentia-native venues such as SeqDEX with asset-level KYC).
- **Whitelabel Placement Portal** — a branded fundraising portal on the issuer's
  own domain (CNAME, escrow, operator terms), plus the public investor-facing
  portal with real SeqPal ID eligibility gating, subscription, e-sign, and escrow.

## Architecture

```
Browser (React SPA)
  ├── OpenAMP public API   (register key, read assets/balances/addresses, log)
  └── seqpald  /api/deploy  ── holds the issuer bearer token (never in the browser)
                              └── OpenAMP issuer API (mint restricted asset)
                                    └── Sequentia node (broadcast issuance tx)
```

- `src/lib/openamp.js` — client for OpenAMP's public endpoints and the seqpald
  deploy endpoint. Same-origin behind Caddy in production (`/openamp/`, `/seqpal/`).
- `seqpald/` — a tiny stateless Go backend. OpenAMP gates issuance behind a bearer
  token that must never reach the browser, so seqpald is the one component that
  holds it and proxies the privileged mint. It holds no keys and no database.

## Running locally

```bash
npm install
npm run dev      # http://localhost:5173 (points at the live testnet OpenAMP + seqpald)
npm test         # pure-logic + enclave-key unit suite
npm run build    # production build to dist/
```

To run the backend against your own node:

```bash
cd seqpald && go build -o seqpald .
OPENAMPD_URL=http://127.0.0.1:8722 OPENAMPD_ISSUER_TOKEN=<token> ./seqpald
```

Deployment to the testnet box is documented in `seqpald/DEPLOY.md`.

## Tech

Vite · React · React Router · Tailwind CSS · `@noble/curves` (secp256k1) · Go (seqpald).

---

*SeqPal is a technology and services provider; it is not a law firm and does not
provide legal, tax, or investment advice. Nothing here is an offer to sell or a
solicitation to buy any security. This is a testnet proof of concept.*
