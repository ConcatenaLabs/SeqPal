# M2 contract: compliance enforcement engine + standalone SeqPal ID surface

Builds on M1 (`M1-CONTRACT.md`). Plan: `../OVERHAUL.md` section 5 milestone M2, plus
sections 3.0 (what a SeqPal ID is), 4.1, 4.2, 4.6. This is where eligibility stops being
browser theater and becomes real: the jurisdiction matrix compiles into policy-server
rules, and a transfer to an ineligible AID gets a real 403 from openampd.

> Section 5 no longer describes what runs. SeqPal screens nobody: identity
> verification is an independent provider's, it is asynchronous, and its decision
> arrives on an authenticated callback. There is no review queue, no reviewer and
> no `pending_review`. What this milestone contracted for is left as written,
> because that is what a milestone contract is; for what the platform does now,
> read the verification rules in `../CLAUDE.md` and `idv.go`.

## 0. The one-line goal

Categories on an AID, stamped only by the platform, are what gate every transfer; the
Step 5 matrix an issuer configures compiles into openampd rules at deploy; and SeqPal ID
becomes a first-class standalone surface at `/id/*`.

## 1. openampd change OA-3 (repo openamp, owner: this work)

Extend `internal/store/store.go` `Rules` and `internal/server/transfer.go` `checkTransfer`,
ALL additive and backward-compatible (every new field `omitempty`, so BONDX and every
existing asset behave identically). Add a Go test.

- `Rules.PrimaryAIDs []string` (json `primary_aids,omitempty`): sender scoping. When the
  transfer's SENDER AID is in this list, `LockinUntilHeight` and `CategoryDenies` do NOT
  bind (so escrow-to-investor delivery works during a lockup), but `AllowedCategories`
  still applies to every recipient. Non-primary senders get the full rule set.
- `Rules.CategoryDenies []CategoryDeny` where `CategoryDeny{Prefix string; UntilHeight int64}`
  (json `category_denies`): for a non-primary sender, refuse if any recipient holds a
  category whose string has `Prefix` as a prefix while `height < UntilHeight` (Reg S
  distribution-compliance windows, e.g. prefix `j:US` until height H).
- `Rules.HolderCapsByCategory map[string]int` (json `holder_caps_by_category`): like
  `HolderCap` but per exact category token (EU per-member-state caps). Counts distinct
  nonzero holders that carry that category, including the incoming recipients. Needs the
  chain balance scan (reuse the `holderBalances` path) plus each holder's categories.
- checkTransfer ordering: compute `senderIsPrimary` once; lockin and category_denies are
  guarded by `!senderIsPrimary`; allowed_categories, holder caps (global and per-category)
  and velocity always apply.
- Go test `oa3_test.go`: primary sender delivers during lockup while a non-primary
  investor-to-investor transfer in the same window is refused; a `j:US` deny refuses a
  US recipient before its height and permits after; a per-category cap of 2 refuses the 3rd.

## 2. Category taxonomy (seqpald, platform-owned, versioned)

Compound tokens `j:<ISO2>:<elig>`, elig in `{ret, acc, pro}` (plus `hnw`, `soph` for GB),
e.g. `j:DE:ret`, `j:US:acc`, `j:GB:hnw`, `j:HN-PRO:ret`. The EU is per-member-state. A
sanctions hit is NOT a category (it triggers freeze); staleness is a category removal.

- The taxonomy carries an integer `version` in seqpald. Each user's openampd category list
  is a pure PROJECTION of the user's seqpald claims record at the current version.
- **Serialized per-user write queue**: openampd category writes are replace-whole-list, and
  three writers exist (verification, expiry cron, sanctions/freeze path). Route ALL category
  writes through one per-user mutex: read the claims record, compute the full new list, write
  via `POST /v1/issuer/categories`, verify by re-reading `GET /v1/users/{aid}`, audit-log the
  pre/post lists and version. Never two concurrent writers on one AID.
- Category validity windows: a claims entry carries `valid_until`; an expiry cron removes
  expired categories (real refusal follows) and notifies the holder 14 days before.

## 3. seqpald endpoints added (base `/seqpal/api`, all session-required unless noted)

| Method | Path | Purpose |
|---|---|---|
| POST | `/id/verify` | run KYC (simulated review, real states) + sanctions screen (real) on the signed-in ID; on approval, stamp categories on the AID via the write queue |
| GET | `/id/passport` | the AID, its categories with validity, per-list screening status + lastScreenedAt, linked entities, and where the ID is accepted (eligible assets, honoring venues) |
| POST | `/id/entities/{id}/verify` | KYB review for a corporate entity; on approval, the entity gets its own enclave key + treasury AID, registered, UBO-signed |
| GET | `/eligibility?aid=&asset=` | advisory preflight: the AID's categories vs the asset's rules -> `{eligible, reasons[]}` (public; also serves the SeqDEX handover) |
| POST | `/issuances/{id}/compile` | compile the issuance's Step 5 matrix into openampd `rules` (returns the compiled rules for preview; the deploy applies them) |

Deploy (M1's `POST /deploy`) is extended: it compiles the matrix into `rules` and passes
them to `POST /v1/issuer/assets`; it creates a PER-OFFERING escrow/distribution enclave
(a registered openampd user with its own key, held in seqpald) and mints into THAT enclave
(holder = the escrow AID), not the issuer's personal AID; it registers the investor/issuer
AIDs as needed; and it lists the escrow AID and the entity treasury AID in `rules.primary_aids`.

## 4. The Step 5 matrix -> rules compiler (the heart of M2)

Input: the issuance's `terms.jurisdictions` (per-jurisdiction `{access: standard|restricted|excluded, elig_categories[]}`),
`terms.lockup_days` (or a block height), `terms.reg_s` (offshore distribution-compliance
period), `terms.eu_caps` (per-member-state holder caps), `terms.structure` (velocity defaults).

Output (openampd `rules`):
- `allowed_categories`: union of admitted `j:XX:elig` tokens; standard admits ret|acc|pro,
  restricted admits acc|pro only, excluded admits nothing, the catch-all is EXCLUDED by default.
- `lockin_until_height`: from lockup, expressed as a Sequentia block height (display as
  "until Sequentia block H").
- `category_denies`: Reg S windows -> `[{prefix:"j:US", until_height}]`.
- `holder_caps_by_category`: EU per-member-state caps.
- `velocity_*`: structure defaults.
- `primary_aids`: the per-offering escrow AID + entity treasury AID.

The compiler is pure and unit-tested (fixed matrix -> fixed rules). It runs server-side in
seqpald; the SPA previews via `/issuances/{id}/compile` but never computes the authoritative
rules itself.

## 5. Sanctions and KYC (real screening, honest simulation)

- Real: screen the registered name against public OFAC SDN + OFAC consolidated, the EU
  consolidated list, and the UN Security Council list, at ID creation and daily by cron.
  Record per-list `lastScreenedAt` and the matched entry. A hit moves the ID to
  `pending_review` (does NOT auto-refuse).
- Reviewer surface: a minimal session-gated admin page on seqpald PLUS a labeled SIMULATED
  auto-reviewer that confirms/clears the deterministic TEST personas after a short delay
  (so demos run unattended; a human can act first). A confirmed hit refuses/freezes; a
  cleared false positive proceeds.
- Simulated (labeled): document/selfie review (pending/approved/rejected/needs-info states,
  refusal personas), PEP/adverse-media. The artifact is real either way: a seqpald-signed
  claims record `{aid, residence, eligibility, verifiedAt, valid_until}` drives categories.
- Accreditation defaults FALSE; `j:US:acc` requires an uploaded verification artifact
  (content simulated, hash + validity window real, 90-day staleness).

## 6. The standalone SeqPal ID surface (SPA, `/id/*`)

Role-agnostic, one flow for everyone (no user-type fork). Uses the existing IdNav/IdFooter.
- `/id` landing: what a SeqPal ID is and unlocks, role-neutral (hold and trade SeqPal-managed
  restricted assets anywhere on Sequentia including on a venue that lists them; and issue if
  you want). One call to action.
- `/id/register`: the M1 real registration (key, passphrase, mandatory encrypted-backup
  export) plus the M2 verify step (labeled document review + real sanctions screen).
- `/id/passport`: AID, enclave key, categories + validity + expiry warnings, per-list
  screening status, linked entities, and where the ID is accepted.
- `/id/entities`: add/manage corporate (KYB) entities as additions to the personal ID.
- SeqPal ID is presented as a product on the marketing site (Products page), not a login.

## 7. Acceptance proof (live box)

A transfer to an ineligible AID returns openampd 403 with the reason in `/v1/log`; an
eligible transfer confirms; a sanctions TEST persona lands in the review queue and, confirmed,
is refused with a public freeze; `GET /v1/assets/{id}` shows compiled rules including a
per-category cap; escrow-to-investor delivery succeeds during the lockup window while an
investor-to-investor transfer in the same window is refused; a person who never touched the
issuance platform creates a SeqPal ID at `/id`, sees real categories on `/id/passport`, and
those categories are what the policy server enforces.

## 8. Cut (if the milestone must ship early)

Ship with global `holder_cap` + `allowed_categories` + `lockin_until_height` only; OA-3's
`category_denies`, `holder_caps_by_category`, and `primary_aids` follow inside the milestone
window. The `/id` surface and real screening are not cuttable (they are the milestone's point).

## 9. Out of scope for M2

Payments/escrow funding and settlement (M5), documents bound to terms_hash (M4), the FAQ
(M4), distributions (M7), the wallet sign card (M5, and it is the SWK agent's deliverable).
