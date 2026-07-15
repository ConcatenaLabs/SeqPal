# M10 contract: integration-spec re-verify + regenesis runbook + acceptance driver

Builds on M1-M9 (all deployed live). Plan: `../OVERHAUL.md` M10 row (line 264), §8 (QA), §9
(SeqDEX handover). Goal: the acceptance layer. Four deliverables:
1. Re-verify the SeqDEX/wallet integration spec against the code as it stands post-M6/M8/M9.
2. A testnet regenesis recovery runbook + scripts, tested against a throwaway LOCAL stack.
3. A Section 8 QA checklist and its execution notes.
4. A full LIVE end-to-end acceptance DRIVER (Node) that produces the deferred on-chain proofs
   from M5-M9, reusing the real SPA signers in `src/lib/keys.js`.

No product-behavior changes; this milestone verifies, documents, and drives what M1-M9 built.
Everything additive. NO em dashes; "Sequentia" spelled out; nothing final at 0-conf.

## 1. Integration-spec re-verify (openamp `spec/venue-wallet-integration.md`)

The spec was written early. Re-read it against the CURRENT openampd + seqpald source (OA-7 landed,
OA-1/3/4/5/6/8/LM live, M9 external issuer key + two-phase clawback, the `/api/eligibility` +
`/api/listings` endpoints). Produce a drift report: every endpoint path, request/response shape,
auth model, and claim in the spec, checked against the code; list mismatches. APPLY doc fixes to
the spec so it matches the deployed reality (endpoints, the eligibility + listings preflight in
Part 2.2, travel-rule + market-abuse in Part 2.7, the confidential + burn/reissue notes). Do not
change code to match the spec; the code is the truth. The spec must be correct before the SeqDEX
agent uses it.

## 2. Regenesis recovery runbook + scripts

A scripted procedure (`~/SeqPal/scripts/regenesis/` or `~/SeqPal/seqpald/regenesis/`) to rebuild
the platform from seqpald's books and records after a chain reset:
- re-issue assets from stored terms (NEW asset IDs, disclosed as new),
- re-register users from stored keys/claims, re-stamp categories,
- re-mint per the last persisted holders snapshot,
- re-anchor, and publish a reconciliation report (old vs new asset IDs, holder deltas).
Plus a backup/restore drill of `~/.openampd` (or the openampd datadir) and `seqpald.db`. The
scripts MUST be tested against a THROWAWAY LOCAL stack (a local openampd + seqpald on a temp
datadir/db, never the live box, never node000). Document the runbook (`REGENESIS.md`): the exact
steps, what is recovered vs permanently lost (an old chain's UTXOs), and the disclosure that new
asset IDs are new.

## 3. Section 8 QA checklist

A checklist doc (`~/SeqPal/ACCEPTANCE.md` or similar) enumerating the Section 8 internal QA items
and, per item, how it is proven (which live proof or test). Mark each item's status. This is the
internal engineering QA list (NOT user-facing guidance); the product UI must be self-guiding.

## 4. Live end-to-end acceptance DRIVER (the flagship)

A Node ESM driver (`~/SeqPal/scripts/e2e/…`, runnable with `node`) that imports the REAL SPA
signers from `../src/lib/keys.js` (computeAID, signChallenge, signMandate, signClosing,
signSighash, signClawbackSighash, signStatement, MARKET_ABUSE_TAG, LISTING_TAG) so the driver
signs EXACTLY as the browser does. It exercises the platform against a configurable base URL
(default the live box `https://sequentiatestnet.com`, paths `/seqpal/api/*` and `/openamp/v1/*`)
and produces the deferred proofs, each printing the on-chain txid / evidence:
- M6: an ATOMIC USDX close, ONE tx delivering restricted tokens to the investor enclave AND USDX
  to the issuer mandate (assert delivery_txid == release_txid, both legs in one tx).
- M7: a pro-rata USDX distribution with per-holder txids paying registered mandate addresses,
  `sum(gross)==sum(net)+sum(withheld)`; a clawback sweep txid with its reason in `/openamp/v1/log`;
  the rules-amendment head equals `GET /openamp/v1/assets/{id}` rules after a mutation.
- M8: a policy-co-signed P2P transfer; a lockup/ineligible resale returning a real 403 with the
  reason in the log; a DR redeem burn txid lowering chain-derived supply; a confidential issuance
  to a blech32 enclave address; a category event logging a set-hash not the raw list.
- M9: a NEW external-issuer-key asset whose contract shows the entity `issuer_pubkey`; a two-phase
  clawback that broadcasts ONLY after the browser issuer signature.

DESIGN for real running: the driver is STRUCTURED IN STEPS, resumable, and idempotent where it can
be (reuse issuance/subscription ids). Wallet FUNDING (sending USDX to a deposit address, keeping
escrow/servicing wallets fee-funded with tSEQ) is a privileged box operation: the driver must
expose funding as a clearly marked hook or a documented manual step (print the deposit address +
amount and wait / accept a `--fund-cmd`), NOT hardcode box credentials. It must NEVER print or
embed secrets. It reads the base URL + any issuer token from env/flags, never inlined. A
`--dry-run` mode runs the signing + request-shaping without broadcasting, so the driver can be
validated without live funds.

## 5. Safety rules

No secrets in any script or doc (the driver reads config from env/flags; funding uses a hook, not
embedded box creds). The regenesis scripts run ONLY against a throwaway local stack in testing,
NEVER the live box or node000. No code behavior changes. Additive. The live proofs are run
separately (by the operator) against the box; the driver makes them reproducible.
