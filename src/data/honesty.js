// The master real-vs-simulated list (plan Section 7). One source of truth, shared
// by the Status page (the master list) and referenced by the FAQ. Every simulated
// element carries what is genuinely real about it and how the UI labels it.
//
// House rule: nothing simulated ever renders in the same visual style as
// chain-verified state, and nothing is labeled final at 0 confirmations.

export const REAL = [
  'The SeqPal ID: a Sequentia wallet the holder already has. Usually its OpenAMP enclave account, registered with the policy server by its public key alone; or a wallet with no OpenAMP account at all, identified by its public descriptor and proved with an ordinary signed message',
  'Restricted assets minted on the Sequentia testnet',
  'Policy co-signed transfers and their real refusals',
  'The register and cap table, read from the chain in atoms',
  'The hash-chained transparency log and its on-chain anchors',
  'Sanctions screening against the public OFAC, EU, and UN lists',
  'terms_hash committed on chain, and the content-addressed document set',
  'BIP340 document e-signatures over the tagged document hash',
  'The RFSA filing number, its public lookup, the deploy gate, and the anchored filing hash',
  'USDX and native testnet BTC as settlement assets',
  'Atomic delivery versus payment for USDX: the token and the USDX payment settle in one transaction',
  'Pro-rata USDX distributions: gross, withholding, and net are computed in atoms from the on-chain register and the net is paid on chain to each holder’s registered mandate address, one payment per holder',
  'The distribution reconciliation invariant: sum of gross equals sum of net plus sum of withheld, with the pro-rata flooring dust disclosed, never dropped',
  'Clawback full-sweep seizures, with the reason recorded in the public transparency log alongside the sweep txid',
  'The rules-amendment chain: the live on-chain rules always equal the anchored chain head, which is the genesis terms_hash plus N anchored amendments',
  'Category expiry: an expired accreditation stops being an eligibility anywhere it is read, and for an ID with an OpenAMP account it is stripped at the policy server, where it becomes a real transfer refusal',
  'Holder-to-holder secondary transfers, policy co-signed, with the browser signing the enclave sighash and both travel-rule counterparties captured',
  'Secondary-transfer refusals surfaced explicitly: an ineligible recipient, a lockup-window resale, or a Reg S window returns a real policy-server refusal with the reason in the public log',
  'The Depository-Receipt programme: mint is a real reissuance and redeem a real burn, with circulating supply chain-derived, never a stored counter',
  'The Depository-Receipt US-person exclusion, enforced as a real policy-server j:US category deny applied through the amendment chain, not a display string',
  'Transparency-log minimization: a category event logs the hash of the category set, which recomputes from the known set, instead of the raw list',
  'Per-transfer confidential transfers: any holder can move any restricted token confidentially in a given transfer, blinding its amount and asset from outside observers while the issuer and registrar keep full sight',
  'Issuer venue-listing authorization, readable at the public listings endpoint, which a venue reads to list an asset but can never use to grant eligibility',
  'The issuer key: wherever an asset has an enclave, its issuer half is the issuing entity’s own wallet key, so a clawback is two-phase and cannot be broadcast without the issuer’s signature, which the platform does not hold. A network-enforced asset has no enclave and no clawback at all: the chain enforces its rules and nobody can seize a holding',
  'The enforcement election: who can hold the token and who enforces the rules is chosen at issuance, committed in the terms, and each model is enforced for real',
  'Freely-tradable (bearer) issuance, where a court-ordered freeze is enforced by the network’s consensus rules and the order document’s fingerprint is recorded publicly beside the freeze',
  'Corporate-action snapshots and claims: the register snapshot is taken from the chain at the first pass at or after the record height, and a claim is a real signed holding proof over named outpoints',
  'The recovery key for a freely-tradable asset: a second Sequentia wallet the issuer names before deploy, whose public half is registered on the asset and whose private half SeqPal never sees',
  'The bearer attestation: a real signature by the issuer’s own key over the two US-exposure statements, recorded before a freely-tradable deploy is accepted',
]

export const SIMULATED = [
  {
    element: 'KYC and KYB document review, selfie, PEP, adverse media',
    real: 'A server-side review queue with pending, approved, rejected, and needs-info states, deterministic refusal personas, and a signed claims record. The sanctions part is real.',
    label: 'Screening: simulated review, real refusals',
  },
  {
    element: 'Sanctions vendor',
    real: 'The OFAC SDN and consolidated, EU consolidated, and UN Security Council list data and the daily cron are real. Only the commercial vendor relationship is absent.',
    label: 'Real data, no commercial vendor',
  },
  {
    element: 'E-signature provider',
    real: 'A real BIP340 signature by your enclave key over the real document hash, stored and anchored.',
    label: 'Cryptographically real signature; provider-grade e-signature simulated',
  },
  {
    element: 'Próspera incorporation and e-registry',
    real: 'A watermarked certificate artifact and entity number, hash anchored, with server-driven timers.',
    label: 'Watermark: no e-registry sandbox exists',
  },
  {
    element: 'RFSA regulator approval',
    real: 'Filing numbers, public lookup, the deploy gate, and anchored filing hashes.',
    label: 'Simulated regulator, real registry mechanics',
  },
  {
    element: 'Fiat rails (card, bank transfer)',
    real: 'The full checkout flow, state machine, receipts, and refund path, with ledger entries marked funds_simulated. The tokens delivered against them are real.',
    label: 'Simulated at the checkout itself and on every money surface derived from it',
  },
  {
    element: 'Depository-receipt custodian',
    real: 'The reissue (mint) and burn (redeem) are real supply changes on chain, and circulating supply is chain-derived. Each operation produces a content-addressed reserve attestation whose hash is anchored. Only the custodian and its underlying holding are simulated.',
    label: 'Simulated custody, real supply changes and anchored attestations',
  },
  {
    element: 'Market-abuse / insider-dealing acknowledgment',
    real: 'A real once-per-investor record, optionally signed by your SeqPal ID key, that gates the platform transfer surfaces.',
    label: 'Platform-layer control, not enforced at the policy co-signature',
  },
  {
    element: 'Tax forms and remittance',
    real: 'The withholding math is real and nets are paid on chain in USDX. The per-holder 1042-S-style statement, the issuer withholding summary, and the annual CRS/FATCA artifact are content-addressed and anchored, so their hashes are real. The forms themselves and any remittance to a tax authority are simulated.',
    label: 'Forms simulated, math and artifact hashes real; remittance out of scope',
  },
  {
    element: 'Annual holder report and ownership-register filing',
    real: 'The register snapshot, its content address, and the on-chain anchor are real, and a notice is delivered to each current holder. The report is a labeled-simulated regulatory filing: nothing is transmitted to any authority.',
    label: 'Real anchored artifact, simulated regulatory filing',
  },
  {
    element: 'Licenses and registrations',
    real: 'The licence slate on the Legal and Licensing page reflects the services SeqPal operates; only the entity and the registration numbers are demo.',
    label: 'Simulated registration numbers',
  },
  {
    element: 'Legal-world corporate actions (voting proxy, default workflow)',
    real: 'Notices and records. Distinct from the on-chain shareholder actions, whose snapshots, holding-proof claims, and tallies are real; only the off-chain legal process around a corporate action is simulated.',
    label: 'Off-chain legal process, simulated',
  },
  {
    element: 'Offering price',
    real: 'Displayed only as the offering reference price.',
    label: 'Reference price, not a market quote',
  },
]

