# M9 contract: external issuer key + two-phase clawback

Builds on M1-M8. Plan: `../OVERHAUL.md` M9 row (line 263), §5 (custody conclusion). Goal: make
"the issuer directs, the registrar co-signs" literally true for NEW assets. The entity's browser
key becomes the enclave issuer half; clawback requires the issuer's browser signature, not just
the platform's. NEW ISSUANCES ONLY (decision 1: no key migration for existing assets). Everything
additive: the legacy single-key path stays for existing assets and is disclosed.

## 1. openampd: external issuer key at issuance (issue.go) — additive

`handleIssue` today `rand.Read`s the issuer private key server-side (issue.go ~line 66) and puts
the derived `issuer_pubkey` in the contract. M9: the request MAY carry an external
`issuer_pubkey` (x-only hex = the entity's browser key). When present:
- Do NOT generate the issuer key. Use the external x-only pubkey as the enclave issuer half, so
  the 2-of-2 enclave and the L_claw leaf are (policy_key, EXTERNAL issuer_key). The server never
  holds the issuer private key for this asset.
- The contract JSON's `issuer_pubkey` is the entity's key; record on the Asset that the issuer key
  is external (e.g. `Asset.IssuerExternal=true`) so clawback knows to run two-phase.
When ABSENT: byte-identical to today (server-generated key, legacy path). Prove with the OA-1
contract goldens. Validate the external pubkey (32-byte x-only) and refuse a malformed one.

## 2. openampd: two-phase clawback (issue.go handleClawback) — additive

Split clawback into build + complete over the existing L_claw leaf:
- `POST /v1/issuer/clawback` (build): resolve the holder's enclave UTXOs, assemble the sweep into
  the issuer/treasury enclave via the L_claw leaf, and RETURN the leaf sighashes to sign
  (`{id, tx, to_sign:[{input,sighash,pubkey}], ...}`), plus the reason logged BEFORE signing (as
  today). Persist a pending clawback (survives restart, like the OA-4 pending transfer).
- `POST /v1/issuer/clawback/{id}/complete`: accept the ISSUER's schnorr signatures over those
  sighashes, add the policy signature, and broadcast. Idempotent + reconciled (consume-once
  pending; a replay returns the same txid; reconcile a lost write by the log/chain like M7's
  clawback). Reason already logged at build.
- LEGACY path (server holds the issuer key, `IssuerExternal` false/absent): the server signs the
  issuer part itself, so build+complete can run in ONE server-side call and the existing
  `POST /v1/issuer/clawback` behaves EXACTLY as today (single call, no external signature). This
  keeps M7's console + stranded-key runbook working unchanged for legacy assets.

The issuer signs a REAL taproot script-path spend sighash here (not a tagged challenge); that is
the entity key's legitimate purpose. The signing-oracle guard (keys.js tagged challenges) is
unaffected: those are separate from a genuine clawback spend the issuer explicitly authorizes.

## 3. seqpald: deploy passes the issuer key; two-phase clawback orchestration

- Deploy (deploy.go): pass the entity's browser issuer pubkey as `issuer_pubkey` to
  `POST /v1/issuer/assets` for NEW assets. Decide the source: the entity's registered enclave/
  treasury key (the entity KYB key from M2). Persist per-issuance whether the issuer key is
  external.
- Two-phase clawback in the M7 freeze/clawback console (console.go) and the stranded-key runbook
  (redeliver.go): for an EXTERNAL-key asset, the flow is build (openampd) -> the issuer browser
  signs -> complete (openampd); seqpald orchestrates and surfaces the sighashes to the issuer.
  For a LEGACY asset, the existing single-call clawback path is unchanged. seqpald selects the
  path from the persisted `issuer_external` flag. Idempotency + reconciliation preserved (never
  double-sweep). The stranded-key runbook's sweep of a new asset now requires the issuer signature
  (the plan's "the ISSUER entity signs the re-delivery, or a disclosed pre-M9 treasury
  delegation"): implement the issuer-signed path; keep the disclosed delegation only for the
  treasury/legacy case.

## 4. SPA: issuer browser-signing + deploy wiring + custody copy

- Deploy flow: send the entity's issuer x-only pubkey with the deploy request (new assets).
- Clawback console + stranded-key: when openampd returns clawback sighashes, the issuer signs them
  in the browser with the entity key (schnorr over the raw sighash via keys.js; a NEW signing
  function distinct from the tagged-challenge signers, clearly commented as a real spend sighash),
  then submits to complete. Show the two-phase step honestly.
- Update the custody / Legal & Licensing copy (§5): for NEW assets the issuer key is external, so
  SeqPal cannot unilaterally move a holder's position (the clawback needs the issuer's key); the
  platform-held-key custody conclusion now applies to LEGACY assets only, disclosed per asset. Do
  not overclaim: the 2-of-2 co-sign (negative control) still exists; the platform still operates
  the policy server.

## 5. Acceptance proof (live box; M10 walkthrough may host the full live run)

- A new asset's contract JSON shows the entity's `issuer_pubkey` (the browser key), and
  `GET /v1/assets/{id}` reflects it.
- A clawback on that asset broadcasts ONLY after the issuer's browser signature completes it (the
  build returns sighashes; without the issuer signature nothing broadcasts).
- The old single-key clawback path still works for a legacy asset and is disclosed as such.

## 6. Safety rules

Additive + backward-compatible: an issuance WITHOUT `issuer_pubkey` and a clawback on a legacy
asset behave byte-identically to M8; BONDX and all M1-M8 assets unaffected (test FIRST). The
external issuer PRIVATE key never touches the server. Every clawback is idempotent + reconciled
(never double-sweep). Reason required + logged before broadcast. Do not touch node000 config.
Nothing final at 0-conf. NO em dashes; "Sequentia" spelled out.
