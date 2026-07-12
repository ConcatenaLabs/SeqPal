# M8 contract: secondary market + DR + confidential + log minimization

Builds on M1-M7. Plan: `../OVERHAUL.md` M8 row (line 262), §2.F (secondary/DR), §4.3 (log
minimization), spec/venue-wallet-integration.md Part 2.2/2.7. Goal: real policy-co-signed
secondary transfers with the refusal path as a first-class demo; a Depository-Receipt programme
with real supply changes; opt-in confidential issuance that works on node000 WITHOUT touching its
`-blindedaddresses` flag; and transparency-log minimization. Everything additive; the openampd
changes are on a LIVE policy server (BONDX + all M1-M7 assets must behave identically) and MUST be
proven backward-compatible first. Cut per the plan: P2P + refusals ship first; DR (OA-5/OA-6) may
trail if needed. `/api/eligibility` already shipped in M2; only `/api/listings` is new here.

## 1. openampd OA-5 (redeem = real burn) — additive

A restricted-asset BURN that reduces circulating supply: an enclave holder's units are sent to a
provably-unspendable output (OP_RETURN, the existing `checkTransfer` burn path already permits a
single `0x6a` scriptPubKey output when `asset.BurnAllowed`). Add the issuer/holder-initiated burn
build+cosign so a DR redeem removes supply for real, recording the burn txid. Circulating supply
becomes chain-derived (holders sum). Additive: assets without `BurnAllowed` are unaffected; the
existing transfer/cosign paths are unchanged. Go test: a burn reduces the holder balance and the
chain-derived supply; a burn of a non-burnable asset is refused.

## 2. openampd OA-6 (DR mint = real reissuance) — additive

Reissue MORE of an existing restricted asset (increase supply for a DR programme). CRITICAL node
hazard (see the reissuance memory): reissuing from an UNBLINDED reissuance token produces a
phantom invalid tx; the reissuance token input MUST be blinded first. Implement reissuance that
re-blinds the reissuance token before spending it, mints into the target enclave, and records the
reissue txid. Additive: only reachable via a new issuer endpoint; existing assets unaffected. Go
test: a reissue increases chain-derived supply and lands in the target enclave; the reissuance
token stays blinded across the operation.

## 3. openampd OA-8 (opt-in confidential, per-call, no node flag) — CODE change, additive

Today confidential issuance requires the wallet to run `-blindedaddresses=1` (issue.go refuses
otherwise); node000 runs `-blindedaddresses=0` and MUST NOT be changed (it would flip default
blinding for every wallet on the shared producer node). OA-8: request a blech32 (blinded) address
PER CALL, which forces blinding for that transaction even with `-blindedaddresses=0`, plus explicit
blinding of the issuance and fee-change outputs, so a confidential issuance works on node000
without the node flag. Then `SEQPALD_CONFIDENTIAL=1` can be enabled. STRICT additivity: a
transparent (default) issuance and every transparent transfer must be BYTE-IDENTICAL to today
(prove with the existing transparent tests + BONDX). The confidential path is opt-in per call only.
Go test: a confidential asset issues to a blech32 enclave address on a `-blindedaddresses=0`-style
wallet; a transparent issuance is unchanged.

## 4. openampd OA-LM (transparency-log minimization) — additive

Category events currently log the raw category set. OA-LM: log the HASH of the category set (a
commitment), not the raw list, so the public transparency log stops leaking each holder's exact
category vector. The set-hash must stay verifiable (a holder/auditor who knows the set can
recompute the commitment). Keep older log entries readable (format-versioned). Additive: the log
chain + anchor mechanism is unchanged; only category-event payloads change to a set-hash. Go test:
a category event logs a set-hash that recomputes from the known set; the raw list is absent.

## 5. seqpald: P2P secondary transfers + the refusal path (the acceptance core)

- P2P transfer between two SeqPal identities, policy-co-signed (the hosted transfer path, exactly
  the M5/M6 delivery mechanism but holder-to-holder): browser-key users build+sign in the SPA;
  wallet-linked users transfer via the live wallet's send UI with an SPA handoff, and the
  `/v1/log` poller captures the wallet-initiated transfer and joins it to identities server-side.
- THE REFUSAL PATH IS A FIRST-CLASS DEMO: an ineligible recipient, a resale inside the lockup
  window, and a Reg S distribution-compliance window each return a REAL openampd 403 with the
  reason surfaced in the UI and visible in `/v1/log`. This is a headline feature, not an error.
- Travel-rule counterparty capture: every P2P transfer record stores originator + beneficiary
  identity (both AIDs resolve to registered platform identities; wallet-initiated transfers are
  captured by the log poller and joined server-side). §2.7.
- Market-abuse / insider-dealing acknowledgment: recorded ONCE per investor before the platform's
  transfer surfaces are enabled; disclosed as a platform-layer control (not policy-enforced at
  co-sign), stated honestly.

## 6. seqpald + SPA: DR programme

A Depository-Receipt programme: mint = real reissuance (OA-6), redeem = real burn (OA-5),
circulating supply chain-derived (never a stored counter), a custodian mock producing anchored
attestations (labeled simulated custodian; real hashed/anchored artifacts), and the US-person
exclusion enforced as a REAL category rule at the policy server (not a display string).

## 7. seqpald: listings authorization + confidential enablement

- `GET /api/listings` (issuer-granted listing authorization) + keep `GET /api/eligibility` (M2):
  both promised to the integration spec Part 2.2 (the SeqDEX handover). A venue can CHECK
  eligibility and read which assets an issuer authorized for listing; it can never GRANT
  eligibility.
- Enable opt-in confidential issuance end to end via OA-8 (`SEQPALD_CONFIDENTIAL=1` path), copy per
  transparent-by-default + hosted-settlement-only. The confidential toggle must stop returning 501.

## 8. SPA + privacy page

P2P transfer UI (browser-key in-SPA; wallet-linked via wallet send + handoff); the refusal path as
a first-class surface (real 403 + reason, logged); travel-rule capture + market-abuse ack; DR
mint/redeem console with chain-derived supply; the confidential issuance toggle (no longer 501);
`/api/listings` surfaced. Update the privacy page for log minimization (category events now log a
set-hash, not the raw list). Honest labels; NO em dashes; "Sequentia" spelled out; transparent by
default, confidentiality opt-in; nothing final at 0-conf.

## 9. Acceptance proof (live box; M10 walkthrough may host the full live run)

- Two investors complete a policy-co-signed P2P transfer (one wallet-initiated).
- A resale inside the lockup window returns 403 on the live policy server with the reason in
  `/v1/log`.
- A test asset with a DE per-category holder cap of 2 refuses the third DE-retail recipient on the
  live box (the literal 149/150 boundary is exercised on regtest; the offeree counter is proven at
  the promotion gate).
- A DR redeem burn txid reduces chain-derived supply.
- A confidential asset issues to a blech32 enclave address.
- New category log entries carry set-hashes, not raw lists.

## 10. Safety rules (live policy server)

Every openampd change (OA-5, OA-6, OA-8, OA-LM) is ADDITIVE and backward-compatible: BONDX and all
M1-M7 assets/transfers behave identically; a transparent issuance/transfer is byte-identical; test
that FIRST. NEVER touch node000's `-blindedaddresses` flag (OA-8 is per-call blinding only). The
reissuance token MUST be blinded before any reissue (the phantom-tx hazard). Every fund/supply
movement is idempotent + reconciled before retry (the M5/M6 invariant). Deploy openampd carefully
(staged binary swap; verify BONDX + a transparent asset resolve after). Nothing final at 0-conf.
