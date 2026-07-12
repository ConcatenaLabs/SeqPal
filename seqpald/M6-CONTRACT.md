# M6 contract: atomic delivery-versus-payment

Builds on M1-M5. Plan: `../OVERHAUL.md` M6 row + section 4.2 (primary-settlement scoping).
Goal: closing settles each USDX subscription as ONE multi-asset transaction (tokens to the
investor enclave AND escrow USDX to the issuer in the same tx), instead of M5's two separate
transactions. Closing v1 (M5) REMAINS the shipped fallback if OA-4 slips (the v1/v2 hedge).
The BTC rail stays registrar-style (cross-chain, not atomic). Ordered internally so the
security fix and the regtest proof gate the rest.

## 1. OA-7 FIRST (openampd; the security precondition)

`internal/server/transfer.go handleCosign` promises (comment at ~line 671-673) that no
UNCLAIMED input may be an enclave output of this asset, but only validates the CLAIMED inputs.
Add the missing check: reject a cosign request whose transaction contains an enclave output of
this asset among the inputs NOT listed in `req.Inputs` (a co-sign for someone else's coins
hidden in the same tx). Additive; existing valid cosigns are unaffected. A Go test proves a
crafted tx with an unclaimed enclave input is rejected, and a normal cosign still passes.

## 2. Mixed-asset regtest proof (gates OA-4 and closing v2)

A functional test (node repo feature_openamp_* pattern, or a seqpald/openampd integration test
against a stub, whichever is exercisable) proving `checkTransfer` TOLERATES a foreign payment
leg (an explicit USDX output + input) alongside the restricted leg in ONE transaction: build a
two-asset tx (restricted asset escrow->investor + explicit USDX escrow->issuer + self-paid fee),
and confirm the policy server co-signs it and it broadcasts. This is the assumption closing v2
rests on; if it does NOT hold, closing v2 is infeasible and M6 ships closing v1 only (report it).

## 3. OA-4 (openampd; the hosted-builder payment leg)

Extend `POST /v1/transfers` (handleTransferBuild) so the builder can add a PAYMENT leg: an
ordinary-asset (USDX) input+output in the same transaction as the restricted transfer, plus
multi-party pending signatures. Request gains optional `payment` `{asset, atoms, from_address,
to_address}` (the escrow's USDX in, the issuer's USDX out) so one tx carries both legs. The
pending-transfer state persists (survives a restart) with a 72h TTL (M5's pending was in-memory
15min; persist it for the multi-party case). `POST /v1/transfers/{id}/complete` accepts the
sigs for BOTH the enclave (restricted) inputs and the ordinary (payment) inputs. checkTransfer
already tolerates the payment leg (proven in section 2). The whole transaction must stay
transparent (every output explicit, per the existing rule). Additive and backward-compatible:
a transfer WITHOUT `payment` behaves exactly as today (M5 delivery still works).

## 4. Closing v2 (seqpald)

`POST /issuances/{id}/close` gains an atomic path for USDX subscriptions: build ONE tx per
subscription via the OA-4 payment leg: tokens escrow-enclave -> investor enclave AND escrow USDX
-> the issuer's registered ordinary payout address (never an enclave) AND the platform fee
output AND changes AND the explicit fee. With the escrow-funded shape, seqpald signs everything
(the escrow enclave key + the escrow wallet's USDX inputs); neither issuer nor investor liveness
is required. The settlement record's idempotency + reconciliation (M5) still holds: one atomic
tx per subscription, txid recorded pre-broadcast, reconciled by chain scan before any retry.
- FALLBACK: if OA-4 is not deployed or the mixed-asset proof fails, closing v2 falls back to
  M5's closing v1 (two txs) transparently; the milestone still ships.
- The atomic path is USDX-only (transparent). BTC subscriptions keep the registrar-model close
  from M5. Fiat subscriptions keep the M5 SIMULATED-release close.

## 5. BTC reorged-deposit handling (M5 gap closed)

The testnet4 watcher keeps tracking credited BTC deposits AFTER delivery. If a deposit is
reorged out post-delivery, seqpald applies a GLOBAL account freeze (`POST /v1/issuer/freeze`,
disclosing the cross-asset blast radius) pending investigation, then unfreezes on re-confirmation
or executes clawback-with-reason. Offerings accepting the BTC rail must have clawback enabled,
or the residual reorg risk is stated in the offering documents and the rail's trust-comparison
UI. Document the window.

## 6. Acceptance proof (live box)

A single explorer txid shows restricted tokens to the investor enclave AND escrow USDX to the
issuer's payout address in ONE transaction (`GET /v1/users/{investorAid}/balance` shows the
tokens; the issuer's payout address shows the USDX); the UI labels "delivery-versus-payment:
atomic" for USDX vs "BTC: cross-chain registrar settlement" for BTC; the OA-7 crafted-input
cosign is rejected; the mixed-asset regtest proof passes. If closing v2 is not feasible, the
box runs closing v1 and the report says so.

## 7. Out of scope for M6

The distribution engine + rules-mutation console (M7); P2P secondary transfers (M8); the SeqDEX
adaptor-swap (handover doc). M6 makes PRIMARY settlement atomic for USDX; it does not add
secondary trading.

## 8. Safety rules (live policy server)

Every openampd change (OA-7, OA-4) is ADDITIVE and backward-compatible: BONDX and all existing
transfers (including M5 closing-v1 delivery) must behave identically. Test that first. Never
change node000 config. Deploy openampd carefully (BONDX resolves after the swap).
