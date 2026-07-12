# M7 contract: transfer-agent servicing

Builds on M1-M6. Plan: `../OVERHAUL.md` M7 row (line 261), §2.G, §4.1 (rules-amendment
chain), §2.H (tax/withholding). Goal: seqpald becomes a working transfer agent. All money
is real USDX on the Sequentia testnet; every financial fact is chain-derived or artifact-backed,
never browser-asserted. Cut (deferred): tBTC payouts (USDX distributions first, per the plan's
explicit cut). Everything additive; openampd unchanged except where noted (freeze/clawback/rules
endpoints already exist).

## 1. Distribution engine (seqpald; new distribution.go + store)

A distribution run for an issuance, USDX only:
1. ISSUER FUNDING FIRST. The run is created in state `awaiting_funding` with a per-run invoice:
   an amount (gross = sum of per-holder gross) and a servicing-wallet deposit address on Sequentia
   (USDX). A watcher (extend moneywatch.go pattern; separate servicing-deposit watcher or reuse)
   confirms the deposit at `escrowConfs` before the run advances to `funded`. The run BLOCKS
   (pays nothing) until funded. Under-funding is audit-logged and keeps the run blocked.
2. RECORD-DATE SNAPSHOT. At snapshot time, capture the holder set + per-holder atoms from the
   CHAIN (openampd `GET /v1/issuer/holders` = confirmed enclave balances), persisted WITH the
   Sequentia block height. The snapshot is immutable once taken (idempotent per run).
3. PRO-RATA in atoms. Per holder: `gross_i = floor(pool * balance_i / total_supply_held)`.
   Dust remainder from flooring is disclosed (audit + statement line), never silently dropped.
4. WITHHOLDING math per holder from the ID's tax status (W-9 vs W-8BEN treaty rate captured at
   verification; a DR-style asset adds the dividend-equivalent line). `net_i = gross_i - withheld_i`.
   The run reconciles: `sum(gross) == sum(net) + sum(withheld)` (an acceptance invariant).
5. PAYMENT. Pay NET on-chain in USDX from the servicing wallet to each holder's REGISTERED
   investor payout mandate address (section 2), one payment per holder, recording the per-holder
   txid. Idempotency + reconciliation identical to M5/M6 closing: a per-holder settlement-scoped
   marker comment (`seqpal-dist-<runID>-<holderAID>`), intent persisted before broadcast,
   reconciled by `escrowFindSend` before any retry, never double-pays a holder.
6. ARTIFACTS. Per-holder statement + a 1042-S-style artifact (gross/withheld/net/treaty), an
   issuer withholding summary, and a labeled-simulated annual CRS/FATCA reporting artifact from
   the registrar entity, all content-addressed (docstore) and anchored (existing anchor path).
   Labeled "simulated regulator/tax authority remittance; real math + hashed artifacts".

## 2. Investor payout mandates, per-chain (seqpald; extend money_store.go + a portal endpoint)

Distributions pay ONLY a registered ordinary wallet address captured via a BIP340-signed payout
mandate in the portal: `mandate = {asset_or_chain, address, signature, signer_xonly}`. Validate:
the signature is a tagged mandate signature (reuse the M5 mandate tag/verify) by the investor's
own registered key; the address is a valid ordinary address for the correct network (a Sequentia
address for USDX). REJECT an enclave key-path / 2-of-2 address explicitly (no wallet scans an
enclave address; paying one would strand the funds): resolve the investor's enclave scriptPubKey
and refuse a mandate address matching it. Distinct from the existing issuer `PayoutMandate`
(MandateFor/UpsertMandate keyed by chain); this is the INVESTOR side, keyed by (issuance? or
global per investor) + chain. A distribution to a holder with no registered mandate is skipped
with a portal notice, never paid to a guessed address.

## 3. Rules-amendment chain wiring (seqpald; wire amendment.go into every mutation)

`amendment.go generateAmendment` exists (M4) but nothing calls it on a real mutation yet. Wire
EVERY policy-rules mutation, whatever its origin (corporate action, category migration, closing
vesting stamp if it mutates rules), to: (a) call openampd `POST /v1/issuer/rules`, then (b) emit
`generateAmendment(prior_rules_hash, new_rules_hash, basis, effective_height)` and anchor it. The
invariant, an M7 acceptance proof: `GET /v1/assets/{id}` rules ALWAYS equals the head of the
anchored amendment chain (genesis terms_hash + anchored amendments). Add a reconciliation/guard so
a mutation that anchors but whose rules POST failed (or vice versa) is detectable and retried, not
left inconsistent.

## 4. Freeze / clawback console (seqpald; new servicing endpoints + SPA)

Issuer-facing (scoped to the entity's own issuances, challenge-auth) console over the existing
openampd endpoints: `POST /v1/issuer/freeze` and `POST /v1/issuer/clawback`. REASON REQUIRED on
both; the resulting txid (clawback) and a public `/v1/log` entry are surfaced. Full-sweep
semantics (clawback seizes ALL of a holder's enclave UTXOs for the asset into the issuer/treasury
enclave) disclosed in the UI. A clawback emits an audit + ledger row. Acceptance: a clawback
sweep txid with its reason visible in the public log.

## 5. Stranded-key re-delivery runbook (seqpald; demoable end to end)

A returning investor who lost their enclave key: (1) re-authenticate against the server-held
identity record (not the lost key); (2) register a NEW AID (or wallet link); (3) clawback-with-
reason sweeps ALL the old AID's units of the asset into the entity treasury enclave; (4) the
ISSUER entity signs the re-delivery to the new AID (or seqpald exercises a disclosed pre-M9
treasury delegation); (5) a sweep-then-redeliver-remainder accounting step returns any
over-collected UNRELATED holdings the sweep caught. Every step logged. Must be executable end to
end (a scripted/admin path is fine); idempotent and reconciled like every other fund movement.

## 6. Category expiry + servicing notices (seqpald; cron + inbox)

Category expiry + re-verification cron: categories carry a validity window (M2); an expired
accreditation must cause a REAL transfer refusal (openampd already enforces category gating, so
expiry = removing/denying the category at the policy server on expiry). Pre-expiry portal notices.
A holder notices inbox + a labeled-simulated annual report to holders (anchored). A scheduled
ownership snapshot + anchor from go-live.

## 7. SPA (src/): servicing surfaces

Issuer: a Distribution console (create run, fund invoice + watch, snapshot, review pro-rata +
withholding, execute, per-holder txids + statements), a Freeze/Clawback console (reason required,
txid + log link, full-sweep disclosure), the rules-amendment chain rendered on the asset page
("genesis terms_hash + N anchored amendments"). Investor: payout-mandate capture (sign with the
SeqPal ID key, network-validated, enclave-address rejected), a notices inbox, downloadable
statements/1042-S artifacts. Honest labels: real USDX math + payments vs simulated
regulator/tax-authority remittance. No em dashes; "Sequentia" spelled out; nothing final at 0-conf.

## 8. Acceptance proof (live box, M10 walkthrough may host the full live run)

- A real pro-rata USDX distribution with per-holder txids paying registered mandate addresses,
  `sum(gross) == sum(net) + sum(withheld)`.
- A clawback sweep txid with its reason in the public log.
- An expired accreditation causes a real transfer refusal at the policy server.
- The stranded-key runbook executed end to end.
- After a rules mutation, `GET /v1/assets/{id}` rules equals the head of the anchored amendment
  chain.

## 9. Safety rules

Every fund movement (distribution payment, clawback, re-delivery) is idempotent + reconciled
before retry (the M5/M6 invariant): intent persisted before broadcast, per-item marker comment,
reconcile by chain scan, never double-pay/double-sweep. Nothing final at 0-conf. openampd changes
stay additive; do not touch node000 config. Investor mandate addresses MUST be ordinary (never an
enclave address). Withholding remittance to authorities is out of scope and labeled.
