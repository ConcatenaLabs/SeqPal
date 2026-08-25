# SeqPal — tokenization-as-a-service on Sequentia

SeqPal is an end-to-end tokenization-as-a-service platform domiciled in Próspera
ZEDE that lets issuers structure, issue, and service compliant security tokens on
**Sequentia**, the Bitcoin sidechain. Restricted assets are issued and enforced
through **OpenAMP**, the open-source policy-server layer for issuer-governed
assets (KYC whitelists, categories, velocity, vesting, freeze, clawback).

This repo is the SeqPal product PoC: a faithful front-end plus a Go backend
that **actually issues restricted assets on the Sequentia testnet** through a
live OpenAMP policy server. The token deployment, the setup fee and the escrow
are real; the identity verification, e-signature provider, incorporation and
registrar steps are simulated so the flow can be walked end to end.

## What is real vs simulated

**Real (on the Sequentia testnet):**

- A SeqPal ID is the OpenAMP enclave account of a Sequentia wallet the holder
  already has — the secp256k1 key that wallet derives at `m/5/0`. SeqPal makes no
  key and holds none: only the x-only public key is registered with OpenAMP, from
  which the policy server derives the account id (AID) and the 2-of-2 enclave
  address restricted assets live in. A wallet that injects `window.sequentia` is
  asked directly; any other Sequentia wallet is linked by that public key and
  signs each statement out of band.
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
- The setup fee is invoiced in USDX and must be paid before deploy: seqpald's
  chain watcher confirms the receipt, and `POST /api/deploy` is refused until it
  has.
- Escrow and settlement. Subscriptions fund a segregated per-offering escrow in
  USDX on Sequentia or in native tBTC on Bitcoin testnet4 (the BTC rail is on
  only when `SEQPALD_BTC_RPC_URL` is set). At closing the tokens leave the
  escrow enclave by policy-co-signed transfer; a USDX subscription settles as
  one atomic delivery-versus-payment transaction (tokens to the investor, USDX
  to the issuer's mandate address, the platform fee output). Nothing is shown
  as final at 0-conf.

**Simulated (kept faithful to the intended product):**

- KYC/KYB identity verification: the sanctions screen runs against downloaded
  lists, but document review is a simulated queue with no vendor call.
- The card/fiat payment rail (its settlements are marked `funds_simulated`).
- The e-signature provider: documents and subscription agreements are signed
  with the SeqPal ID key in the holder's own wallet (BIP340 over the content
  hash), not through a vendor.
- Próspera incorporation and the RFSA registry (a filing gets a number and a
  public lookup from a simulated registry).
- The brokerage-custody relationship for Depository Receipts.

The browser keeps no key material at all: `localStorage` holds UI preferences
and, for a wallet linked by hand, the public key of the account signed in
(`seqpal.signer.v1`). Accounts, issuances and subscriptions are records in
seqpald's database and on chain. Clearing browser storage costs you a reconnect
and nothing else — the key is in your wallet, backed up by your wallet's own
recovery.

## What the product covers

- **Marketing site** — landing page, products (with a "how it fits together"
  architecture view), the four issuance structures, and the published fee schedule.
- **SeqPal ID** — the consolidated KYC/KYB and accreditation passport, and your
  login. Created by a natural person (individual KYC); corporate entities (KYB)
  are linked on top. When signed in, it lists the live offerings you're eligible
  to access (the auto-whitelist network effect).
- **Issuer Dashboard** — gated behind a SeqPal ID; lists your issuances, their
  lifecycle status, and placement-portal state.
- **The seven-step issuer onboarding flow** (the centrepiece):
  1. Identity & principal — who applies/owns the new LLC (issuing as an individual
     is Native Equity only; SPV / Debt / DR require a corporate KYB principal). The
     new Próspera LLC is the issuer of record.
  2. Structure — choose an issuance structure (KYB-only ones are locked for
     individual applicants).
  3. Holders & enforcement — who may hold the token and the enforcement
     election (below).
  4. Data room — dynamic deal-term inputs per structure; private vs public offering.
  5. Documents — generate the document package and sign it with the issuer key.
  6. Tokenomics & compliance — name the asset and configure the
     jurisdiction/accreditation policy (Appendix C matrix, per-issuance caps, and
     the public-offering overlay) baked into the token.
  7. Checkout & deploy — pay the setup fee in USDX, then deploy.
- **Enforcement election** (step 3) — three models, recorded on the issuance
  and sent with the deploy:
  - *SeqPal enforces your rules* (`serviced`, the default) — an OpenAMP
    restricted asset; the policy server co-signs every transfer off-chain, with
    zero consensus changes.
  - *The network enforces your rules* (`network`) — OpenDAMP: the rules are
    published as on-chain covenants enforced by Sequentia consensus. A
    deployment capability (`SEQPALD_DAMP`); refused with a 501 where it is unset.
  - *Freely tradable* (`bearer`) — a node-level supervised asset: an ordinary
    bearer token with on-chain freeze/unfreeze under the issuer's supervision
    key plus a recovery key in a second wallet, always transparent.
- **Issuance lifecycle** — draft → documents generated and issuer-signed → RFSA
  filing (simulated registry, public lookup) → setup fee paid in USDX → deploy
  (the real mint) → broadcast → confirmed → anchored, shown as a timeline.
  Próspera incorporation and brokerage custody are off-platform checklist items
  SeqPal does not observe. Nothing is labelled final at 0-conf.
- **Automated Transfer Agent** — schedule distributions (dividend/coupon/yield),
  process corporate actions, and mint/redeem Depository Receipts, with an activity
  log; a Registry of Members and a Secondary Market card (assets can be listed on
  Sequentia-native venues such as SeqDEX with asset-level KYC).
- **Whitelabel Placement Portal** — a branded fundraising portal on the issuer's
  own domain (CNAME, escrow, operator terms), plus the public investor-facing
  portal with real SeqPal ID eligibility gating, subscription, e-sign, and escrow.

## Architecture

```
Browser (React SPA, served by seqpald)
  ├── seqpald  /seqpal/api/*   sessions (BIP340 challenge), issuances, documents,
  │     │                      RFSA, fees, escrow and closing, P2P transfers,
  │     │                      servicing, enforcement consoles
  │     ├── OpenAMP issuer API   (bearer token, never in the browser)
  │     ├── Sequentia node RPC + electrs   (chain watcher, supervision RPCs,
  │     │                                   bearer mints, escrow payouts)
  │     └── Bitcoin testnet4 RPC           (native tBTC escrow rail)
  └── OpenAMP public API  /openamp/v1/*   (register key, assets, balances,
        │                                  addresses, transparency log)
        └── Sequentia node (broadcast issuance tx)
```

- `src/lib/api.js` and `src/lib/openamp.js` — clients for seqpald and for
  OpenAMP's public endpoints. Same-origin behind Caddy in production
  (`/seqpal/`, `/openamp/`).
- `seqpald/` — the Go backend and the platform's books and records: a SQLite
  database (accounts, issuances, subscriptions, documents, the hash-chained
  audit log) plus the per-offering escrow enclave key it uses to settle
  closings. Holder keys never leave the holder's wallet. seqpald also holds the OpenAMP
  issuer token, which must never reach a browser, and is the only party that
  calls the issuer API.

## Running locally

```bash
npm install
npm run dev      # http://localhost:5173; proxies /seqpal and /openamp to LOCAL backends
                 # (127.0.0.1:8730 and :8722). Set VITE_SEQPAL_API / VITE_OPENAMP_API to
                 # aim at the live testnet; that mints real assets.
npm test         # pure-logic + signed-statement unit suite
npm run build    # production build to dist/
```

To run the backend against your own node:

```bash
cd seqpald && go build -o seqpald .
OPENAMPD_URL=http://127.0.0.1:8722 OPENAMPD_ISSUER_TOKEN=<token> ./seqpald
```

Every configuration variable is listed in `seqpald/DEPLOY.md`, which also
documents deployment to the testnet box.

## Tech

Vite · React · React Router · Tailwind CSS · `@noble/curves` (secp256k1) · Go (seqpald).

---

*SeqPal is a technology and services provider; it is not a law firm and does not
provide legal, tax, or investment advice. Nothing here is an offer to sell or a
solicitation to buy any security. This is a testnet proof of concept.*
