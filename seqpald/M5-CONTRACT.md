# M5 contract: real money legs, offer-side gates, closing v1, wallet-first custody

Builds on M1-M4. Plan: `../OVERHAUL.md` M5 row + sections 4.2 (promotion gates, offeree
counting, primary-settlement scoping), 4.5, and disposition items 9, 10, 23, 33, 42, 46,
47, 62, 67, 70, 71, 85, 94, 95, 99, 103, 107, 108. This is the milestone where money moves
for real on testnet. Closing v1 is TWO transactions (delivery + release), not atomic DvP
(that is M6). No openampd change is required for M5 (delivery uses the existing hosted
transfer / cosign flow, with seqpald holding the per-offering escrow enclave key).

## 0. Box facts (verified)

- USDX (Sequentia payment asset): `2a515539da5e6a60caa7766ecd65bac0c10d15717ddd2088844ba58f4d04b9de`.
- Policy fee asset (tSEQ): `c8eccacf0953e1931cd31e434d8319101cc36e6c38b0e2104d8687552fae3e40`.
- Sequentia node RPC: node000 via `SEQPALD_NODE_URL` (already configured).
- Bitcoin testnet4 RPC: the mainchain node at `mainchainrpcport=48332` (creds in node000's
  elements.conf as `mainchainrpcuser`/`mainchainrpcpassword`); seqpald gets new config
  `SEQPALD_BTC_RPC_URL/USER/PASS`.
- Create DEDICATED seqpal escrow wallets; NEVER touch the existing wallets (treasury2,
  seqdex-mm-btc, seqln-*, etc.). Fund the demo escrow/faucet flows from treasury2 (holds USDX)
  and a fresh testnet4 wallet.

## 1. Escrow (seqpald operates the payment escrow)

Two segregated escrow surfaces per offering, distinct from the M2 per-offering restricted-asset
enclave (which receives the mint and is the delivery SENDER):

- **USDX escrow (Sequentia)**: a dedicated seqpald-controlled node wallet (`seqpal-escrow`)
  on node000. Per subscription, derive a fresh USDX deposit address; watch it; a deposit
  becomes `in_escrow` only at N confirmations (N configurable, default 1 given ~30s blocks).
- **BTC escrow (testnet4, native, cross-chain)**: a dedicated testnet4 wallet
  (`seqpal-btc-escrow`) on the mainchain node. Per subscription, derive a fresh testnet4
  address; watch it; credit at N testnet4 confirmations; capture the price-server USD/BTC rate
  at confirmation. The BTC rail is REGISTRAR-style (payment confirmed on testnet4, then
  delivery co-signed on Sequentia), NOT atomic; surface the trust difference.
- **Refund addresses**: captured at subscription time (a Sequentia address for USDX, a testnet4
  address for BTC), validated for the correct network; refunds pay them.

## 2. Offer-side gates (all ship here; no real money moves without them)

- **Promotion gate**: the offering page is served by seqpald filtered on
  `promotable_jurisdictions` (part of terms, committed in terms_hash). Anonymous visitors get
  a fixed teaser template (issuer name, sector, structure, jurisdiction-availability, a
  "verification required" CTA; NO price/raise/returns/mechanics) plus a gate.
- **UK statutory statements**: a UK resident records the SI 2024/301 high-net-worth or
  self-certified-sophisticated statement (GBP 100k income / GBP 250k net assets) before the
  offering renders; valid 12 months; recorded on the ID.
- **EU offeree counting**: seqpald counts distinct non-qualified persons per member state to
  whom the offer is made available (gate passage is the counting event); warns at 149; blocks
  the 150th offeree at the gate.
- **Appropriateness + KID**: for retail-lift jurisdictions, an appropriateness questionnaire
  and the generated KID-style summary (from M4) with a standardized risk banner.
- **Source-of-funds**: an SoF questionnaire above a USD 10k-equivalent subscription.
- **US 506(c)**: `j:US:acc` requires the uploaded verification artifact (M2), accredited
  defaults false, 90-day staleness.
- All gates ship IN M5: no milestone demos real money without them.

## 3. Subscriptions

`POST /issuances/{id}/subscribe {rail, amount, refund_address}` (session; the investor must be
a verified, eligible SeqPal ID): validates eligibility (`/eligibility`), runs the offer-side
gates for the investor's jurisdiction, records a subscription, and returns a per-subscription
deposit address for the chosen rail:
- `usdx`: a Sequentia USDX deposit address in `seqpal-escrow`.
- `btc`: a testnet4 address in `seqpal-btc-escrow` + the captured rate at confirmation.
- `card` / `bank`: the SIMULATED fiat rail (section 4).
Subscription states: `created -> in_escrow (deposit confirmed) -> settled (delivered at close)`
or `-> refunded`. The watcher advances `in_escrow` at N confs. Nothing is `settled` until close.

## 4. Fiat rails (first-class SIMULATED, for issuers and investors)

Per the plan decision: card and bank transfer are first-class simulated rails for BOTH
subscriptions and SeqPal platform fees. A labeled simulated processor in seqpald with a full
checkout (pending -> settled with realistic timing), receipts, a refund path, and ledger
entries flagged `funds_simulated: true`. Fiat-funded subscriptions run the SAME
subscription/escrow/closing state machine and settle by policy-co-signed delivery (no atomic
DvP, since the payment leg has no chain). The SIMULATED label appears at the checkout itself
and on every money surface derived from a fiat payment. Rail choice is ALWAYS the payer's,
never coupled to jurisdiction.

## 5. Platform fees

SeqPal's own fees (the Pricing page) are invoiced and COLLECTED before deploy, payable by the
ISSUER's choice of rail: on-chain USDX/tBTC OR the simulated fiat rail. An unpaid setup fee
blocks issuance creation/deploy. Escrow fee accrues on real balances and is deducted at
release. Prices shown in USD via the price server.

## 6. Issuer payout mandates

Before closing, the issuer registers a BIP340-signed payout mandate `{asset/chain, address,
signature}` (from M4's e-sign path or the entity key), validated for the correct network;
enclave key-path fallback addresses are rejected. Escrow release and (M7) distributions pay
only mandated ordinary addresses.

## 7. Closing v1 (two transactions per subscription)

`POST /issuances/{id}/close` (session, owner): the issuer signs a single closing authorization;
then for each `in_escrow` subscription seqpald runs:
1. **Delivery**: a policy-co-signed transfer of the purchased token amount from the per-offering
   escrow ENCLAVE (holder = escrow AID, a primary AID so lockups do not block it) to the
   investor's enclave. seqpald holds the escrow enclave key and signs the holder side; openampd
   co-signs (existing `POST /v1/transfers` + `/complete` or `/cosign`). US-tranche purchasers'
   AIDs are stamped with a per-AID `rules.Vesting` entry (until close height + ~12 months, the
   Rule 144 approximation) at close.
2. **Release**: the escrow payment (USDX from `seqpal-escrow`, or tBTC from `seqpal-btc-escrow`)
   is paid to the issuer's registered payout mandate address, minus the accrued escrow fee.
- **Idempotency + reconciliation**: exactly one settlement record per subscription; the txid is
  recorded pre-broadcast; an ambiguous timeout is reconciled by a chain scan before any retry
  (never double-deliver, never double-release).
- **Refunds**: a failed close (delivery refused, or reconciliation shows no delivery) auto-
  refunds the escrowed payment to the captured refund address with a txid on the correct chain.
- Confirmations use the M3 watcher (electrs-backed).

## 8. Wallet-first custody (SeqPal side)

The generic "sign an OpenAMP request" wallet card is the SWK/wallet agent's deliverable (see
the integration spec Part 1), NOT built here. For M5 SeqPal: the investor's default custody is
their SeqPal ID enclave key (browser, encrypted, M1); wallet-link accepts a challenge signed by
that key. With the escrow-funded closing shape, delivery needs NO investor signature at close,
so a wallet-linked investor is fully served for subscribe/settle/hold; deliveries land in the
investor's enclave (viewable in the live web wallet once the SWK agent ships OpenAMP display).
The SPA's future Transfer button hands off to the wallet (P2P is M8).

## 9. Acceptance proof (live box)

A faucet-funded (or treasury-funded) USDX subscription flips to `in_escrow` only after the
deposit txid confirms (explorer-resolvable); a testnet4 BTC deposit credits at the captured
/prices rate with a refund address on record; a card-funded (SIMULATED) subscription settles
and delivers real tokens with the SIMULATED label visible; an unpaid setup fee blocks issuance
creation; close delivers tokens into the investor's enclave (a real on-chain transfer, verified
via `GET /v1/users/{investorAid}/balance`); a failed close auto-refunds with a txid on the
correct chain; a UK persona cannot render the offering before completing the statutory
statement (server-side gate); the 150th DE-retail offeree is blocked at the gate.

## 10. Cut (if it must ship early)

USDX rail first (the atomic-on-Sequentia payment); the BTC testnet4 rail and the simulated fiat
rails follow inside the milestone window. The offer-side gates are NOT cuttable.

## 11. Out of scope for M5

Atomic DvP in one transaction (M6, needs OA-4 + OA-7); the distribution engine and rules-
mutation console (M7); P2P secondary transfers (M8). M5 moves money into escrow and delivers at
close; it does not do single-transaction atomic settlement.
