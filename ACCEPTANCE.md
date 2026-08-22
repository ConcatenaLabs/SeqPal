# SeqPal internal acceptance checklist (engineering QA)

This is the M10 execution record for OVERHAUL.md Section 8. It is an ENGINEERING
artifact, never handed to testers or users: real issuers and investors get only
the product's own UI, and any point where a first-time tester stalls is a
usability defect to fix, not a gap to paper over with this document.

Every capability below is proven by one or more of:

- a Go test (deterministic; `go test ./...` in `seqpald/` and
  `openamp/openampd/`, run locally before every PR: there is no CI),
- a driver step in the live acceptance driver
  (`scripts/e2e/run.mjs`, run as `--only mN`), which signs EXACTLY as the browser
  by importing `src/lib/keys.js`, or
- a LIVE OPERATOR action on the box (a fund movement needing box-held keys), which
  the driver makes reproducible but cannot perform itself.

The Section 7 simulated elements (KYC/KYB review content, sanctions vendor
relationship, e-signature provider, incorporation/regulator, fiat rails, DR
custodian, tax forms) are labeled where they appear; every fund movement below is
real USDX / tBTC on the Sequentia testnet, chain-derived, nothing final at 0-conf.

## Status legend

- `PASS` proven by a committed Go test (green locally; there is no CI).
- `DRIVER` shaped + signed by the acceptance driver and validated in `--dry-run`;
  completes live once the operator funds the referenced wallet.
- `LIVE-OP` needs a privileged box fund movement (funding hook / manual step);
  the driver drives it, the operator supplies the keys.
- `PRE-CAPTURED` cannot be triggered on demand on the live chain (a Bitcoin reorg);
  the same rendering code the live platform runs is exercised on regtest.

## How to run the driver

```
# offline validation (signs + shapes every request, hits only read-only endpoints)
node scripts/e2e/run.mjs --dry-run

# one live proof, operator funds the printed deposit address (or passes --fund-cmd)
BASE_URL=https://sequentiatestnet.com \
ISSUER_ENVELOPE=/secure/issuer-id.json  ISSUER_PASSPHRASE=… \
INVESTOR_ENVELOPE=/secure/investor-id.json INVESTOR_PASSPHRASE=… \
ISSUANCE_ID=<deployed-issuance> \
  node scripts/e2e/run.mjs --only m6 --fund-cmd './box-fund.sh'
```

The driver reads its base URL, identity backups + passphrases, and any funding
command from the environment. It never holds or prints a box credential.

---

## Section 8 walkthrough coverage

### 1. Issuer onboarding (browser A)

| Check | Proof | Status |
| --- | --- | --- |
| Real BIP340 key, challenge-response register/login | `seqpald` auth tests + driver handshake (`id.mjs handshake`, `signChallenge`) | PASS / DRIVER |
| SDN screen stamp; simulated document review with a visible pending state | `m2_test.go` (KYC states, sanctions screen) | PASS |
| Corporate entity KYB queue, own enclave key + treasury AID, UBO-signed link | `m2_owned_test.go` (entity verify, UBO link) | PASS |
| Sanctioned TEST persona lands in review, confirmed + refused live, freeze visible at `GET /v1/users/{aid}` | `m2_test.go` refusal path + `LIVE-OP` box review | PASS / LIVE-OP |

### 2. Structuring

| Check | Proof | Status |
| --- | --- | --- |
| Structure choice + issuer's own deal terms (illustrative 1,000,000 @ USD 1.00) | `m3_test.go`, `compiler`/`characterization` tests | PASS |
| Jurisdiction matrix (Prospera retail, DE lift, US 506(c), catch-all excluded) | `m4_test.go` (category compile), `gates` tests | PASS |
| Lockup "until Sequentia block H"; clawback toggle | `m4_test.go`; clawback proven in items 9/10 | PASS |

### 3. Legal

| Check | Proof | Status |
| --- | --- | --- |
| Deterministic content-addressed documents with statutory wording | `m4_test.go` (document generation, terms_hash binding) | PASS |
| Issuer signs the manifest hash with the entity key | `signDocument` (keys.js) + `m4_test.go` | PASS |
| RFSA sim-registry filing number, publicly look-up-able | `m4_test.go` RFSA filing/lookup | PASS |
| Setup fee invoiced in USDX, paid, watcher-confirmed receipt txid | `TestM5UnpaidSetupFeeBlocksDeploy` + `LIVE-OP` fee payment | PASS / LIVE-OP |

### 4. Deploy

| Check | Proof | Status |
| --- | --- | --- |
| Mint lands in the per-offering escrow enclave on testnet | `oa1_test.go` (contract goldens), `LIVE-OP` deploy | PASS / LIVE-OP |
| Rules land on the policy server (`GET /v1/assets/{id}`) | `oa1_test.go`; driver reads `GET /openamp/v1/assets/{id}` | PASS / DRIVER |
| Ticker resolves in registry, `/prices`, live wallet | `LIVE-OP` (registry + price seed) | LIVE-OP |
| Status walks broadcast -> confirmations -> anchored; Verify recomputes terms_hash | `m3_test.go` (anchor/log), `anchor` tests | PASS |

### 5. Investor A (DE retail, wallet-first)

| Check | Proof | Status |
| --- | --- | --- |
| Wallet links AID to a new SeqPal ID by signing a challenge | `signChallenge` + `m2` wallet-link tests | PASS |
| Promotion gate (DE lift), appropriateness, KID/risk, subscription-hash e-sign | `TestM5UKStatementGates…`, `gates`/`m4` tests | PASS |
| Funds USDX from the faucet into the segregated escrow deposit address | `TestM5USDXSubscriptionLifecycleToSettled` + `LIVE-OP` funding | PASS / LIVE-OP |

### 6. Investor B (US accredited) + card rail

| Check | Proof | Status |
| --- | --- | --- |
| Category granted with a visible expiry | `m2_test.go`, `TestM7CategoryExpiry…` | PASS |
| Pays in BTC on Bitcoin testnet4; refund address captured; trust-comparison copy | `TestM5BTCDepositCreditsAtCapturedRate` + `LIVE-OP` BTC deposit | PASS / LIVE-OP |
| SIMULATED card rail runs end to end (pending/settled/receipt), funds_simulated flagged | `TestM5FiatCheckoutSettlesSimulated` | PASS |

### 7. Closing (the M6 deferred proof)

| Check | Proof | Status |
| --- | --- | --- |
| Issuer signs the closing authorization once (`signClosing`) | driver `--only m6` step 04; `m6_test.go` | DRIVER / PASS |
| Investor A settles as ONE atomic txid: tokens to her enclave AND USDX to the issuer payout address AND the platform fee output | `TestM6_UsdxClose_AtomicOrFallback…`; driver asserts `delivery_txid == release_txid && atomic` on `GET /settlements` | PASS / DRIVER (LIVE-OP funding) |
| Investor B delivered by policy-co-signed transfer after testnet4 confirmations | `TestM5CloseDeliversAndReleases`, `TestM6_BTCReorgOut…` | PASS |
| Closing idempotent, reconciled before retry, no double-pay | `TestM6_ReplayedClose…`, `TestM6_ReconcilesDeliveryBeforeRetry`, `TestM6_AtomicDisabledMidSettling…` | PASS |
| Position simply appears in the unmodified web wallet, no privileged asset | `LIVE-OP` wallet check | LIVE-OP |

### 8. Register and audit

| Check | Proof | Status |
| --- | --- | --- |
| Registry of Members renders from `/v1/issuer/holders` in atoms | `m3_test.go` (holders read) | PASS |
| Auditor verifies a balance against the public endpoint + transparency-log hash chain client-side, through to the OP_RETURN anchor | `m3_test.go` (log hash chain, anchor tx) | PASS |
| A Bitcoin-driven reorg regresses a "settled" chip, anchoring-supremacy banner | regtest capture; same regression code the live platform runs | PRE-CAPTURED |

### 9. Servicing (the M7 deferred proof)

| Check | Proof | Status |
| --- | --- | --- |
| USDX dividend: record-date snapshot, pro-rata, withholding math | `TestM7Distribution_BlocksUntilFundedThenPaysNetToMandates`; driver `--only m7` steps 07-08 | PASS / DRIVER |
| `sum(gross) == sum(net) + sum(withheld)` | driver step 08 asserts the invariant; `m7_test.go` | DRIVER / PASS |
| NET paid on-chain to registered mandate addresses, per-holder txids, downloadable statements | `m7_test.go` payout + artifacts; driver prints per-holder txids | PASS / DRIVER (LIVE-OP funding) |
| Investor mandate must be an ordinary address (enclave rejected) | `TestM7InvestorMandate_EnclaveRejected_OrdinaryAcceptedBadSigRefused` | PASS |
| Distribution reconciled, never double-pays | `TestM7Distribution_ReconcilesLostPayoutByMarker` | PASS |
| Clawback sweep txid + reason in `/openamp/v1/log` | `TestM7Clawback_ReasonRequired_RecordsTxidAndLog_Idempotent`; driver `--only m7` step 09 | PASS / DRIVER |
| Rules mutation: `GET /v1/assets/{id}` rules == anchored amendment head | `TestM7Amendment_LiveMutationKeepsChainHeadConsistent`, `…HalfAppliedMutationIsReconciled`; driver step 10 | PASS / DRIVER |
| Stranded-key runbook: re-auth, new wallet-linked AID, clawback + re-deliver, every step logged | `TestM7Redeliver_EndToEndAndIdempotent`, `…UnknownOldIdentityRefused` | PASS |

### 10. Enforcement finale (the M8 + M9 deferred proofs)

| Check | Proof | Status |
| --- | --- | --- |
| Resale to a Brazilian retail persona: real 403, reason in the public log | `TestM8_P2PRefusalPathReturns403WithReason`; driver `--only m8` step 13 | PASS / DRIVER |
| Resale inside the lockup window: refused | `TestM8_P2PRefusalPathReturns403WithReason`; driver step 13 | PASS / DRIVER |
| P2P transfer to an eligible DE investor settles, initiated from the wallet send UI | `TestM8_P2PBrowserTransferCosignsWithTravelRuleAndIsIdempotent`, `TestM8_WalletInitiatedTransferCapturedByPoller`; driver step 12 | PASS / DRIVER |
| Market-abuse ack gates the transfer surfaces (signed variant) | `TestM8_MarketAbuseAckGatesSurfaceAndSignedVariant`; driver step 11 (`MARKET_ABUSE_TAG`) | PASS / DRIVER |
| DE per-category holder cap refuses the third DE-retail recipient; offeree counter at the gate | `TestM5OffereeCap150Blocked`, `TestM5OffereeCountingMechanism` (149/150 on regtest) | PASS |
| DR redeem burn txid lowers chain-derived supply | `TestM8_DRRedeemBurnLowersChainDerivedSupplyIdempotent`; driver `--only m8` step 14 | PASS / DRIVER |
| Confidential P2P transfer elected per transfer (no confidential assets): the settled tx carries blinded outputs to a blech32 (`tsqb`) address, no node000 flag flipped; `GET /api/transfers` shows `confidential:true` | `oa8_lm_test.go` (OA-8 per-call blinding); driver step 15 | PASS / DRIVER (LIVE-OP funding) |
| Category log entries carry set-hashes, not raw lists | `oa8_lm_test.go` (OA-LM); driver step 16 | PASS / DRIVER |
| Issuer freezes a holder (simulated court order), then a real clawback with reason, issuer-signed in the browser (M9), txid + log | `TestM9Console_ExternalClawbackIsTwoPhase`, `TestM9Redeliver_ExternalPausesForIssuerSignatureThenCompletes`; driver `--only m9` step 18 (`signClawbackSighash`) | PASS / DRIVER |
| New asset's contract shows the entity `issuer_pubkey` (external), server holds no issuer key | `TestM9Deploy_ExternalKeyIsOwnBrowserKey`; driver `--only m9` step 17 | PASS / DRIVER |
| Legacy single-key clawback still works and is disclosed as legacy | `TestM9Console_LegacyClawbackStillSingleCall` | PASS |
| Status page: policy-server health, FROST roadmap, one-token disclosure, deprecated legacy assets, anchor depth | `LIVE-OP` status page review | LIVE-OP |

---

## Deferred on-chain proofs the driver owns (M5-M9)

These are the proofs Section 8 defers to the live run. Each is validated offline
in `--dry-run` (signing + request shaping proven against the live read-only
endpoints) and completed live once the operator funds the referenced wallet.

| Proof | Driver | Live prerequisite (operator) | Status |
| --- | --- | --- | --- |
| M6 atomic USDX close: one tx, `delivery_txid == release_txid` | `--only m6` | a funded USDX subscription on `ISSUANCE_ID`, escrow wallet tSEQ for fees | DRIVER + LIVE-OP |
| M7 pro-rata distribution, `sum(gross)==sum(net)+sum(withheld)`, per-holder txids | `--only m7` | servicing-wallet USDX covering the gross pool (funding hook prints the deposit) | DRIVER + LIVE-OP |
| M7 clawback sweep txid + reason in `/openamp/v1/log` | `--only m7` | a holder with a confirmed balance to sweep | DRIVER + LIVE-OP |
| M7 rules == amendment head after a mutation | `--only m7` | an owned deployed `ISSUANCE_ID` (read-only assertion) | DRIVER |
| M8 policy-co-signed P2P transfer settles | `--only m8` | investor holding the asset; both AIDs registered | DRIVER + LIVE-OP |
| M8 lockup / ineligible resale returns a real 403 + reason | `--only m8` | a locked-up or ineligible beneficiary | DRIVER |
| M8 DR redeem burn lowers chain-derived supply | `--only m8` | a custodied balance to burn | DRIVER + LIVE-OP |
| M8 confidential P2P transfer elected per transfer, blinded outputs to a blech32 address | `--only m8` | a delivered holding to move and `SEQPALD_CONFIDENTIAL=1` on the deployment | DRIVER + LIVE-OP |
| M8 category event logs a set-hash | `--only m8` | a category mutation in the log window | DRIVER |
| M9 new external-issuer-key asset; contract shows the entity key | `--only m9` | full onboarding + setup fee funded for the new deploy | DRIVER + LIVE-OP |
| M9 two-phase clawback broadcasts ONLY after the issuer browser signature | `--only m9` | an external-issuer `ISSUANCE_ID` with a holder to sweep | DRIVER + LIVE-OP |

## Live steps that need operator funding

The driver cannot fund live wallets: those keys live only on the box. Each step
below prints its deposit address + amount and pauses (or runs `--fund-cmd`):

1. A USDX subscription into the offering escrow (M6 close).
2. The servicing wallet's USDX pool for a distribution (M7), plus a tSEQ balance
   for network fees.
3. A holder balance to clawback / burn / transfer (M7 clawback, M8 DR redeem,
   M8 P2P transfer).
4. The setup fee (USDX) that gates a new deploy (M9 external-key).

All other checks are proven by the committed Go tests and the offline `--dry-run`.

## Dry-run result (offline validation)

`node scripts/e2e/run.mjs --dry-run` against `https://sequentiatestnet.com`:
read-only surfaces reachable (`health` reports `ok:true`, `openamp_ok:true`,
`issuer_token_ok:true`; 10 assets visible); every proof produced a real BIP340
signature from `src/lib/keys.js` and shaped its exact request body; the atomic,
reconciliation, refusal-guard, supply, external-key, and set-hash invariants all
asserted PASS; no request was broadcast and no secret was read or printed.
