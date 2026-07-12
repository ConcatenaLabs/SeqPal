# M4 contract: legal artifact pipeline, posture pages, the public FAQ, copy overhaul

Builds on M1-M3. Plan: `../OVERHAUL.md` M4 row + sections 4.4, 4.5, 4.7, 4.8, 4.9, and
disposition items 11, 15, 16, 26, 29, 32, 35, 60, 79, 80, 81, 86, 92, 93, 97, 98, 99, 101,
109, 110, 111, 114. Goal: real, content-addressed legal documents bound to the on-chain
terms_hash; honest posture pages; the ungated FAQ; and the full copy overhaul. No openampd
change (terms_hash already flows into the contract). Confirmation-independent.

## 1. seqpald: the document pipeline

- **Deterministic document generation** to content-addressed artifacts (canonical HTML + PDF
  via the house `soffice --headless --convert-to pdf` toolchain if available on the box, else
  serve canonical HTML and note the PDF step): offering memorandum with per-structure risk
  factors, subscription agreement, operating agreement declaring the on-chain register
  dispositive under Prospera law, escrow terms, KID-style summary, UK statutory investor
  statements (SI 2024/301 wording + thresholds), lift artifacts (basis of admission).
- **Binding**: a sha256 manifest of the document package goes INSIDE the terms object, so
  `terms_hash` (already committed on-chain via `contract_hash`) commits to the documents.
  seqpald canonicalizes and hashes terms server-side (M1 already), stores the canonical
  document set, and serves `GET /seqpal/api/terms/{hash}` (the manifest) and
  `GET /seqpal/api/doc/{docHash}` (a preimage).
- **Offer-window gating**: during the offer window the manifest is public but document
  PREIMAGES are served only to identities that passed the promotion gate (session with an
  eligible/attested state); at offer close preimages publish ungated; holders and signers
  always retain access. A standing probe: an anonymous request for an offer-period preimage
  returns a non-200.
- **Statutory citations in artifacts**: UK SI 2024/301; Reg S 17 CFR 230.903 distribution
  compliance; the EU offeree-count over-enforcement note; lockups stated as "until Sequentia
  block H (approximately N months; the height, not the date, is binding)".
- **Per-structure instrument-characterization memo**: for each structure, a security vs
  AIF/CIS analysis with applicable marketing regime per jurisdiction and PRIIPs applicability;
  the M2 matrix compiler CONSUMES the classification (an AIF-classified structure disables the
  EU retail lift, restricts EU access to professional categories, switches the UK gate to CIS
  wording). Expose the classification for the SPA to display.
- **Rules-amendment artifacts**: any post-issuance `POST /v1/issuer/rules` mutation emits a
  content-addressed amendment document (prior rules hash, new rules hash, basis, effective
  height), anchored via the transparency log. (The mutation paths themselves are M7; M4
  provides the amendment-document generator and the "genesis terms_hash plus anchored
  amendments" rendering in the Verify explainer.)
- **RFSA simulated registry**: `POST /rfsa/filings {issuer, structure, doc-manifest hash,
  terms_hash}` returns a filing number; public `GET /rfsa/filings/{number}` lookup; a public
  offering cannot deploy without a filing; the filing hash is anchored on-chain. Labeled
  "simulated regulator, real registry mechanics".
- **Backup/restore + retention**: scripts to back up and restore the seqpald SQLite DB and
  document store; a documented retention schedule.

## 2. SPA: e-signature, data room, posture pages, the FAQ, copy overhaul

- **E-signature**: BIP340 signature by the signer's enclave key over the document hash,
  tagged `openamp-document-v1` (per the integration spec 0.4), stored and anchored via the
  transparency log. In M4 the browser-key signer signs; the generic wallet sign card is M5
  (SWK agent). The e-signature PROVIDER remains a labeled simulation; the signature is real.
- **Data room**: the generated documents are real downloadable artifacts with their hashes
  shown; the investor sees the documents and the terms_hash binding before subscribing (the
  Subscribe gate itself is M5, but the documents + mandatory risk/subscription acknowledgment
  surface land here).
- **"Verify independently" explainer** (extends M3's): now walks terms document -> doc
  manifest -> terms_hash -> on-chain contract_hash -> policy key, and renders the genesis
  terms_hash plus any anchored amendments. States the access model (hash verifiable
  immediately, preimages public at close).
- **Legal & Licensing page**: demo entity "SeqPal LLC" (Prospera ZEDE); simulated-but-concrete
  RFSA registration numbers per function; the custody CONCLUSION (clawback-enabled assets with
  a platform-held issuer key mean SeqPal has control amounting to custody until M9; the 2-of-2
  co-sign is negative control); the per-jurisdiction platform-role conclusions (US broker-
  dealer s.15(a), EU MiFID II, UK RAO Article 25, all as production analysis with simulated
  numbers); the single-token blast radius disclosure; the non-custodial USDX commitment model;
  the SPBOND/TCT legacy-asset deprecation note; FATCA/CRS FI classification note.
- **Privacy page**: scoped to exactly what ships; explicit PARTIAL-erasure statement (the chain
  and anchored log retain pseudonymous AIDs and hashes); GDPR Art 3(2) applicability, a lawful-
  basis table per processing purpose, a labeled-simulated Art 27 EU representative, an SCC
  transfer note; the public wallet-surface exposure disclosure (GET /v1/users/{aid} exposes
  categories + frozen).
- **Status page**: policy-server health, the FROST roadmap, the legacy assets, and the master
  real-vs-simulated list (matching Section 7 of the plan).
- **The public FAQ** (Section 4.9, UNGATED, reachable with no SeqPal ID and no session):
  what SeqPal is and is not (RFSA transfer agent and registrar, not a law firm, not liable for
  your compliance); what Sequentia gives you and does not replace (secondary market with
  Bitcoin liquidity, but bring your own primary market; native BTC settlement; the restricted
  leg cannot sit in an HTLC); what Prospera gives you and does not replace (favourable issuance
  regime, but investor-jurisdiction law still binds the OFFER); identity/eligibility/access;
  custody/keys/loss; money/fees/settlement (real USDX + native BTC, honestly simulated fiat);
  confidentiality (opt-in, not privacy from your registrar); finality/reorgs (nothing final at
  0-conf); and the testnet real-vs-simulated master list. Plain language, no em dashes.
- **Lifecycle honesty**: the client-side fast-forward button is DELETED; lifecycle advances via
  the M3 chain watcher; a labeled admin-only fast-forward may remain for demos. Incorporation
  and RFSA milestones produce watermarked SIMULATED artifacts (certificate, entity number,
  filing receipt), hashes anchored.
- **Full copy overhaul**: no em dashes anywhere in user-visible strings; "the Sequentia
  network" never "the SEQ chain"; "permissioned"/"policy-co-signed transfers between eligible
  holders" not "permissionless"/"move freely"; opt-in confidentiality not confidential-by-
  default; no unfounded present-tense license/bank claims; illustrations labeled; fee units in
  the fee asset's own units. Grep gates must pass.

## 3. Acceptance proof

Recomputing `terms_hash` from the published terms document plus the document manifest equals
the on-chain `contract_hash` commitment; an anonymous request for an offer-period document
preimage returns a non-200; the RFSA lookup page returns the filing for a deployed public
offering; the FAQ is reachable with no session and no SeqPal ID and states the not-a-law-firm,
bring-your-own-primary-market, and investor-jurisdiction-law boundaries explicitly; grep gates
pass (no em dashes, no "the Sequentia" danglers, no "permissionless" in user-visible strings).

## 4. Out of scope for M4

Real payments/escrow/subscription settlement (M5); the wallet sign card (M5, SWK agent); the
distribution engine and the rules-mutation console (M7). M4 makes the LEGAL and COPY layer real
and honest; it does not move money.
