# SeqPal demo — architecture & QA notes

A front-end/UX demo of the SeqPal tokenization-as-a-service product, inferred from
the business plan (currently **v0.72**). It is intentionally not wired to any
backend or third party — see [What's mocked](#whats-mocked). This document is the
handoff reference: how the code is organised, how state and the core flows work,
what is faithfully modelled vs. simulated, and how it's tested.

## Stack

- **Vite + React 18 + React Router 6**, **Tailwind CSS 3**. No backend.
- State lives in the browser via a small React context backed by `localStorage`
  (key `seqpal.demo.v2`).
- Tests use Node's built-in test runner (`node --test`) — zero extra deps.

```
npm install
npm run dev       # http://localhost:5173
npm run build     # production build to dist/
npm test          # pure-logic unit suite
```

> Fonts load from the Google Fonts CDN; if that's blocked the UI falls back to
> system sans (purely cosmetic).

## Directory map

```
src/
  main.jsx, App.jsx            App shell + routes. "Focused" routes (onboarding,
                               portal setup, investor portal) render without the
                               marketing navbar/footer.
  lib/
    store.jsx                  React context: account + issuances, persisted to
                               localStorage. Re-exports the pure helpers in util.js.
    util.js                    Pure, framework-free helpers (id/hash/GAID
                               generators, addBusinessDays, slugify).
    policy.js                  isEligible() — the single eligibility rule shared by
                               the portal gate and the "offerings you can access" list.
    economics.js               parseMoney/fmtAmount, post-money ownership, and the
                               Escrow & Settlement Fee.
    lifecycle.js               Post-checkout status model + milestones (DR has an
                               extra brokerage-custody milestone).
  data/
    structures.js              The four issuance structures + per-structure metadata.
    pricing.js                 Published fee schedule + computeSetupCost().
    jurisdictions.js           Appendix C matrix (55 jurisdictions + catch-all) and
                               the residence options offered at registration.
  components/                  Navbar, Footer, Logo, Modal, Passport, SignInGate,
                               ServicingPanel (post-issuance Transfer Agent), icons, ui.
  pages/
    Home, Products, Structures, Pricing, SeqPalId, Dashboard, IssuanceDetail, NotFound
    onboarding/NewIssuance.jsx + Steps.jsx   The six-step issuer wizard.
    portal/PortalSetup.jsx                   Issuer configures the whitelabel portal.
    portal/InvestorPortal.jsx                Public investor-facing portal.
test/logic.test.js             Unit tests for pricing, lifecycle, policy, economics, util.
```

## State model

One account per browser (`localStorage`):

```
account: {
  individual: { name, residenceCode, jurisdiction, accredited, accreditationMethod,
                idNumber, gaid } | null,   // null = signed out; this is the "login"
  corporates: [ { id, entity, jurisdiction, idNumber } ]   // KYB entities
}
issuances: [ {
  id, name, ticker, entityName, unit ('USD'|'BTC'),
  structureId, principal:{type,name,idNumber,corpId?}, isPublic,
  raise, fields:{…data room…}, policy:{ [jurCode]: tier }, mintTarget,
  assetId, txid, status, createdAt, incorporationEta,
  portal:{ configured, published, brandName, headline, accent, slug, docs[], minInvestment, … },
  subscriptions:[ { id, name, jur, amount, rail, status:'in_escrow'|'settled', at } ]
} ]
```

Helpers: `registerIndividual`, `addCorporate`, `signOut`, `addIssuance`,
`updateIssuance(id, patch|fn)`, `reset`.

## Core flows & the rules that govern them

### Identity (SeqPal ID is the login)
KYC/KYB happens on the **SeqPal ID** page, not in the dashboard. An account is
created by a natural person (individual KYC) — that *is* the login. Corporate
(KYB) entities are linked on top. The dashboard, onboarding, and issuance pages
are gated behind a signed-in SeqPal ID (`SignInGate`, with a `?next=` return).
Accreditation is **document-verified** for US/Canada (Reg D 506(c) reasonable
steps) and self-certified elsewhere.

### Six-step onboarding (`Steps.jsx`)
1. **Identity & principal** — issue as yourself (individual) or on behalf of a
   verified corporate entity. **Native Equity may be issued by an individual**
   (the new LLC *is* the business); **SPV / Debt / DR require a corporate KYB
   principal**. On every issuance the new Próspera LLC is the *issuer of record*.
2. **Architecture routing** — the four structures; KYB-only ones are locked for
   an individual principal.
3. **Data room** — entity name (names the issuer of record on formation docs),
   **unit of account (USD default / BTC)**, offering type (private/public),
   and per-structure dynamic fields. Structure-specific **mandatory attestations**
   gate advancement (SPV shareholder-agreement, Debt borrower-KYB, DR custody).
4. **Document automation** — generated package is previewable (rendered, prefilled
   from data-room inputs — "the magic moment").
5. **Tokenomics & compliance** — name/ticker + the Appendix C jurisdiction matrix.
   Private placement starts from suggested-minimum tiers; **public offering** is
   excluded-by-default and admission requires per-jurisdiction **confirm**; retail
   in a restricted jurisdiction requires **upload-to-lift**. Mandatory floors
   (sanctions, OFAC/FATF + territorial) are non-overridable.
6. **Checkout** — fixed setup fee (+ public surcharge; Simple Native Equity tier
   for ≤$500K). Payment **does not** go instantly live.

### Lifecycle (`lifecycle.js`)
`awaiting_incorporation` → `deploying` → `live`. Próspera incorporation takes
1–3 business days (ETA shown); DR carries an extra **brokerage-custody** milestone.
A demo "fast-forward" advances states. Asset id/txid appear only once live.

### Servicing (`ServicingPanel.jsx`, live issuances)
- **Distributions** — dividend/coupon/yield; settlement asset defaults to the
  issuance's unit (BTC L-BTC vs USDt); Debt coupons framed as Calculation-&-Paying
  Agent on the Note's agreed schedule.
- **Corporate actions** — structure-specific, with the applicable **event fee**
  surfaced (SPV 0.50%/$5K waterfall; Debt 1% call, $10K–$50K default).
- **DR mint/redeem** — mint fee keyed to mandate (0.25% in-kind / 0.30% cash),
  0.50% redeem, 0.75%/yr AUM strip; NAV/underlying on the asset card.
- **Reporting & tax** — year-end holder statements, W-8/W-9, Próspera 1% filing.

### Portal (`PortalSetup` → `InvestorPortal`)
Issuer configures a branded portal on their own domain (CNAME), requests escrow,
and accepts operator terms; the route is guarded (DR has no subscription portal;
not-live issuances can't publish). The investor portal gates on SeqPal ID
eligibility, enforces the minimum and remaining-capacity (no oversubscription),
takes a funding rail (USD wire / BTC / L-USDT; BTC→L-USDT for USD raises), and
records subscriptions to escrow. The issuer closes the round on closing
conditions → subscriptions settle into the Registry of Members.

### Money & fees (`economics.js`, `pricing.js`)
- **Ownership %** — Native Equity = investment / **post-money** (pre-money + raise);
  SPV/Debt = investment / raise. Verified against standard venture math.
- **Escrow & Settlement Fee** (v0.72) — 0.25%/month on escrowed funds, accrued
  daily, **$5K min** (USD-equivalent for BTC raises), **3% cap**; ≈1% of the raise
  over a typical 4-month window. (Replaced the old Platform Services Fee.)
- All per-structure fees match the plan's §6.2 / Appendix A.

## What's mocked

KYC/KYB verification, payments (setup/ID/E&S fees), e-signature, Próspera
incorporation + RFSA filing + Blockstream AMP deployment (the "fast-forward"),
escrow funding, and the DR brokerage-custody relationship. Asset ids/txids and
GAIDs are plausible fakes. Nothing is broadcast to any network.

## Scope discipline (what is deliberately NOT on the site)

The customer site shows only customer-facing product. Kept **off**: funding
ask/valuation, financial projections, founder names/equity/comp, the RFSA
license slate & regulatory capital, named banking/brokerage partners,
competitor cost comparisons, and market-size projections — all of which live in
the (internal) business plan.

## Testing & verification

- `npm test` — pure-logic unit suite (pricing tiers/surcharge/secured/DR; lifecycle
  counts + DR milestone; eligibility across tiers; post-money ownership; E&S fee incl.
  cap/floor and BTC handling; util format/date).
- Manual/automated browser verification has covered: both USD and BTC paths end to
  end, the principal/KYB gating, public-offering overlay, upload-to-lift, the
  incorporation lifecycle, all servicing actions, portal setup + investor
  subscription + close, mobile (zero horizontal overflow at 390px), and a
  zero-React-console-warnings sweep.

## Known notes / open items

- **Plan erratum**: Appendix C cites "Panama Law 67 of 2020" as the qualified-
  investor basis; the correct citation (used on the site) is Decree Law 1/1999 as
  amended by **Law 67 of 2011** (created the SMV). Worth fixing in the plan.
- Single-tenant demo: signing out doesn't clear issuances; "Reset demo" is the
  full clear. Acceptable for a single-user localStorage demo.
- The investor "my holdings" portfolio view (the network-effect end state) is
  represented on the SeqPal ID page as "offerings you can access" but there is no
  dedicated holdings dashboard yet — a natural next feature.
