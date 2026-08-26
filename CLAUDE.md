# SeqPal

A tokenization-as-a-service platform proof of concept: a React front-end plus a thin Go backend
that **actually issues restricted assets on the Sequentia testnet** through a live
[OpenAMP](https://github.com/ConcatenaLabs/openamp) policy server.

`README.md`'s "What is real vs simulated" section is the most important thing in the repo. The
token deployment, the USDX setup fee and the USDX / tBTC escrow are real; the scaffolding around
them (KYC/KYB review, the e-signature provider, incorporation and the RFSA registry, the card
rail) is simulated so the flow can be walked end to end. Never blur that line in code or in copy.

Node and consensus conventions live in the
[`Sequentia`](https://github.com/ConcatenaLabs/Sequentia) repo.

## Two halves

| Path | What |
|---|---|
| `src/` | The React + Vite single-page app. It holds no key material: `localStorage` keeps UI preferences and, for a wallet linked by hand, the account's PUBLIC key. Records come from seqpald. |
| `seqpald/` | The Go backend, its own module. It serves the built SPA (with history-API fallback) *and* the API, so one reverse-proxy route covers both. |
| `scripts/` | `live-probe.sh`, the `e2e/` driver, and the `regenesis/` runbook scripts. |
| `seqpald/M*-CONTRACT.md` | Per-milestone contracts, each with a matching `m*_test.go`. |

```sh
npm install
npm run dev          # vite
npm run build        # vite build -> dist/
npm run preview
npm test             # eslint (no-undef) + node --test

cd seqpald && go build ./... && go test ./...
```

There is no CI. Those commands are the whole gate.

`eslint` runs one rule, `no-undef`, and it earns its place: a bundler only resolves what
crosses a module boundary, so `vite build` compiles a call to an identifier that does not
exist without a word and the ReferenceError arrives in front of a user. Two such bugs were
already in the tree when it was added. Keep it narrow -- a style sweep here would bury the
rule that catches something that actually breaks.

## The custody line

- **SeqPal is not a wallet and must never become one.** A SeqPal ID IS the OpenAMP enclave
  account of a Sequentia wallet the holder already has: the secp256k1 key that wallet derives
  at `m/5/0`. SeqPal generates no keys, holds none, unlocks none, and stores no key-at-rest
  format. Only the x-only public key reaches the backend. `src/lib/statements.js` defines the
  bytes that get signed; `src/lib/wallet.js` asks a wallet to sign them. There is a test
  (`test/logic.test.js`) that fails if a signer or key generator ever reappears in `src/`.
- Three ways a wallet attaches: a browser extension injecting `window.sequentia` (asked directly
  through `openampGetIdentity` / `openampSignTagged` / `openampSignSpend`); any other Sequentia
  wallet linked by its public key, signing each statement out of band; or a wallet with NO
  OpenAMP account, identified by a public `pkh(...)` descriptor and proved with an ordinary
  signed message (`seqpald/wallet_identity.go`). Never add a fourth that makes a key here.
- `accounts.identity` is `aid` (an OpenAMP enclave account) or `xpub` (a descriptor-backed
  wallet). An `xpub` account is refused every OpenAMP path by `requireEnclave`, and BOTH a
  serviced and a bearer deploy by `handleDeploy` -- serviced mints into an enclave, and bearer
  is supervised by a key that must sign a BIP340 freeze message, which an ordinary wallet
  cannot produce. Only `network` is issuable without one, and it takes a
  `holder_key` naming where the supply mints -- `initialHolderKey` accepts the
  account's own enclave key, or any key `keyDerivesFromAccount` derives from a
  linked wallet, and refuses anything else before a mint happens; supervised, OpenDAMP, corporate actions and claims are deliberately
  NOT behind that gate. `POST /api/auth/attach-enclave` upgrades one in place, keeping its id.
  The node does the descriptor work (`getdescriptorinfo`, `deriveaddresses`, `verifymessage`
  are pure functions needing no wallet), so there is no bespoke key handling in seqpald.
- `account_wallets` holds the wallets ONE SeqPal ID is kept in; signing in with any linked
  wallet resolves to the account that linked it (`AccountByDescriptor` / `AccountByEnclaveKey`),
  never to a second account. Descriptor wallets are unlimited; enclave wallets are capped at
  one, because restricted assets settle in exactly one account. `s.hasEnclave(acct)` reads that
  table -- never `acct.Identity` directly, which is only what the ID was FOUNDED with.
- `whitelist_requests` is how a verified ID asks to join a network-enforced asset's whitelist.
  The requested key must be proven (`descriptor`: derives from a linked wallet; `signature`: a
  signed message from that key), or a verified holder could launder eligibility onto somebody
  else's key. Approving is a decision; `included` is only ever set when a published policy
  change carried the key (`noteWhitelistInclusions`, unfreeze only -- a freeze REMOVES keys).
- **Identity verification is a PROVIDER's, and it is asynchronous.** SeqPal runs no watchlist:
  documents, PEP, adverse media and sanctions are the provider's work, and this platform
  consumes their decision. `POST /api/id/verify` records claims as `submitted`, grants nothing,
  and hands a check to `s.idv`; the decision arrives at `POST /api/id/verify/callback`,
  authenticated by `SEQPALD_IDV_CALLBACK_SECRET` and idempotent because a provider that does
  not hear 200 retries. `applyAdjudication` is the ONE place an outcome takes effect -- clear
  verifies and stamps, reject refuses and freezes, resubmission asks for more. Never decide a
  verification anywhere else, and never make it synchronous: real providers are not, and the
  point of this shape is that swapping one in is an adapter, not a redesign. A refusal cannot
  be submitted away (`handleIDVerify` refuses over `refused` and `submitted`); `needs_info` is
  the one not-verified state that invites another try. The provider bills per check either
  way, so a check is PAID FOR before it is submitted: `requireVerificationFee` gates both
  submit paths on an account-scoped invoice (`kyc`, or `kyb` per business, in `fee_invoices`
  keyed by `aid` and `subject`), and an unpaid caller is refused with a 402 having written
  nothing. Never move that gate after the submit -- the cost is incurred the moment the check
  is created, and a fee asked for afterwards is one the platform can be refused. One paid fee
  buys ONE check: the invoice records the check it bought (`check_id`) and the gate looks only
  for an unspent one, or a holder re-verifies forever on a single payment and runs up a bill
  this fee exists to recoup. The one free submission is the one the PROVIDER asked for
  (`continuesAnOpenCheck`), which is the same check continuing. Both submit paths take
  `verifyMu` for the account FIRST: everything after it reads where the check and the fee stand
  and then acts on it, so without the lock two requests arriving together both read "nothing
  open, one payment available" and the provider bills for every one of them (six, in the test
  that proves it). `GET /api/id/fees` raises
  nothing -- quoting a price is not billing for it. Submitted is
  not verified anywhere, including in the passport: an entity's treasury key and UBO link exist
  from submission, and `verified` comes from the entity's own check.
- **"The latest check for this account" is never the answer to "where does this person's
  identity stand".** A company's check is filed under its controller's account id, because they
  are who asked for it, so `LatestVerificationCheck` TAKES A KIND and every identity question
  passes `"identity"`. Answering with the account's newest check of any kind showed a holder
  their company's decision as their own on the passport, and let a company's resubmission buy
  their next identity check for nothing.
- **A decision about a BUSINESS is about that business, never about its owner.** A business
  check travels on the controller's account id, because they are who asked for it, so
  `applyAdjudication` branches on `check.Kind` FIRST: applying a company's refusal to the
  controller's claims refuses the person, strips their eligibility and freezes them for a
  decision that was never about them. `applyBusinessAdjudication` records the result on the
  check -- which is where the passport reads a company's verified state -- and on a refusal
  freezes the ENTITY TREASURY at the policy server, which is the account that would otherwise
  hold assets for a business the provider would not pass. A business refusal is as final as a
  person's; `POST /api/id/entities/{id}/verify` also records the UBO signature, so it must
  never re-submit a check that is already open.
- **A fee is credited only when what was owed has ARRIVED, on any rail it was quoted on.** A
  fee is a gate, so `watchFeeDeposits` credits an invoice when the confirmed total at a quoted
  address reaches that quote's amount (`FeeQuote.covers`) and not before -- crediting on any
  confirmed amount let one atom open a gate priced in thousands. Quotes are kept PER RAIL on
  the invoice (`quotes`), every one of them is watched, and quoting a rail again returns the
  address it already has: overwriting a single address column stranded whatever had been sent
  to the one it replaced. An account fee is raised at most once (unique index on
  `aid, kind, subject`), because the page that quotes it polls from several cards at a time.
  Quoting a rail takes `st.LockFee(invoice)` and re-reads the row first: the quote set is a
  read-modify-write, so two rails quoted at once would each write a set missing the other's,
  and a dropped quote is a deposit address nothing watches any more. The same uniqueness holds
  for an OFFERING's fee (`issuance_id, kind`): the setup fee is the deploy gate, and raised
  twice the issuer pays one invoice while the gate reads the other. And for an ENCLAVE KEY
  (`kind, ref_id`), where two at once mean two keys for one company treasury and assets going
  to a treasury the ownership link does not name. That migration deletes NOTHING: a duplicate
  key may hold assets, so if any exist the index fails to create and seqpald refuses to start,
  which is a person's problem rather than a coin flip.
- **A decision that never arrives is CHASED, never waited on forever.** A callback crosses a
  network once, and the process can restart between the submission and the delivery, so
  `runIDVReconcileCron` asks the provider directly about any check outstanding past
  `SEQPALD_IDV_GRACE_SECS` (`idvProvider.PollCheck`). Without it a holder sits at `submitted`
  forever: they cannot submit again, and they have already paid for the check. Polling for
  missed webhooks is what a real integration does, which is why the poll is ON the provider
  interface. `CompleteVerificationCheck` records the FIRST decision only, so a late callback
  cannot overwrite a chased one; everything `applyAdjudication` does before it must stay
  idempotent, because a callback and a poll can both apply it.
  A submission that never REACHED the provider must leave nothing behind: `handleIDVerify`
  writes the claims before it calls out, so on failure it restores exactly what was there
  (deleting them if there was nothing), or the account is stuck for good -- refused as already
  open, with no check to chase, and a prior verification thrown away. For the same reason no
  SPA surface may treat provisioning as submission: an entity's `submitted` comes from its
  check, never from its treasury key.
  A REFUSAL is not finished until the policy server has heard it: `applyAdjudication` returns
  an error if the freeze or the category strip fails, so the check stays open for the
  callback's 500 and the reconciler to bring back. Both calls are no-ops for an ID with no
  account there, so a failure always means unreachable, never wallet-backed. Never downgrade
  either to a log line: a refused holder who was verified before still carries live categories
  at openampd until that freeze lands.
- **A DEPLOY mints, so it takes `st.LockIssuance` before it asks whether it already
  happened.** The idempotency key is read before the mint and written after it, so two deploys
  in flight together are both told "no prior deploy" and the chain keeps both assets: five
  concurrent calls minted five, in the test that proves it. The retry that key exists to make
  safe IS the case where two are in flight. Never move that lock below the `DeployByIdem`
  check, and never assume the primary key catches it -- it does, after the mint.
- **A SeqPal ID has TWO account ids, and openampd knows only one of them.** `accounts.aid` is
  the id the ID was founded with and never changes; the policy server knows the account the
  enclave key derives. They coincide only for an ID founded ON an enclave. Never pass
  `acct.AID` to openampd -- go through `enclaveAIDOf` / `openampAIDFor` (`seqpald/wallets.go`),
  which resolve it and return "" when there is no OpenAMP account, meaning "nothing to ask
  about". Getting this wrong is silent: openampd answers about an account it has never heard
  of, and "no categories, not registered, no address" is exactly what a holder with nothing
  looks like. `writeCategoriesFor(claimsAID, enclaveAID)` takes both for this reason.
- **A compliance action must bind every kind of SeqPal ID.** The policy server only knows
  accounts with an OpenAMP key, so `callOpenAMP` fails for an ID that is only a wallet -- and
  a caller that returns on that error has skipped whatever came after it. A confirmed sanctions
  match did exactly that and left the identity verified. Refuse the CLAIMS record first (it is
  what every eligibility read consults, it only ever restricts, and it is the sole enforcement
  such an ID has), then go to the policy server through `freezeAtPolicyServer` /
  `stampCategories`, which report "there was nothing there" rather than failing.
- **A stored descriptor cannot be derived from without canonicalising it first.**
  `toPKH` drops the checksum (it covered different text), and `deriveaddresses` refuses a
  descriptor with no checksum. Go through `pkhForm`. Three callers did not, and because each
  reads a failed derivation as an ordinary negative, the failure was silent: correct
  signatures "did not verify", and no key ever "derived from" a linked wallet. The test node
  requires a checksum for this reason -- keep it that way, and keep the openamp stub answering
  only accounts that were registered.
- **Every statement has two signable forms, and every surface offers both.** `signable` /
  `signableOf` (`seqpald/statement_proof.go`) put `sign_this` (the canonical bytes, hashed
  under `tag` by a wallet that knows SeqPal's tags), `tag`, and `sign_this_message` (the exact
  characters an ordinary wallet signs: tag, newline, statement or `hex:` digest) in one
  response. `verifyAccountStatement` accepts either. Anything that asks a HOLDER for a
  signature -- mandates, closing, market-abuse, documents, attestations, holding proofs -- must
  go through those two, never `verifyKeyStatement(acct.XOnly, ...)`: an ID that is only a
  wallet has no `acct.XOnly`, and comparing against it turns the surface into a dead end that
  reports "does not verify". `verifyKeyStatement` is for a statement that must come from one
  SPECIFIC key regardless of who is signed in -- the holding-proof key over its own outpoints,
  and the whitelist-request key. In the SPA, `OfflineSignature` is that panel.
- **A wallet is never handed a digest to sign.** Application statements are domain-TAGGED, and
  an enclave spend is sent as the TRANSACTION so the wallet recomputes each sighash itself. The
  enclave key is half of the 2-of-2 every restricted asset sits behind, so a digest signer over
  it is a signing oracle that drains the account.
- `seqpald` never holds a holder's key. It registers the holder's public key; the mint lands in a
  per-offering escrow enclave whose key seqpald custodies and uses only to settle closings, and
  it keeps the books and records (the SQLite DB).
- `seqpald` also holds the one server-side secret: OpenAMP's issuer endpoint is bearer-token gated,
  and that token must never reach a browser. `seqpald` holds it and is the only party that calls
  the issuer endpoint.
- Everything the browser can read without the token — assets, balances, enclave addresses, the
  transparency log — it fetches from OpenAMP's public endpoints directly, same-origin.

Do not add a route that proxies a browser request straight through to an issuer endpoint, and do
not move any holder key material server-side.

## Traps

- **Precision 0 is a real value, not "unset".** Integer-only assets exist, so `precision` handling
  must distinguish an explicit `0` from an absent field. This was fixed once on both sides of the
  SeqPal/OpenAMP boundary; do not reintroduce a zero-check that substitutes a default.
- **External issuer keys are the only deploy path.** The platform-held issuer key was retired
  deliberately.
- **The on-chain contract commits to a `terms_hash`** binding the compliance configuration to the
  asset. Changing what goes into that hash changes every derived asset id.
- **Confidentiality is per transfer, never per asset.** Sequentia is transparent by default; every
  deploy is a transparent mint, and a holder elects confidentiality on an individual transfer
  (`confidential: true` on POST /api/transfers), gated per deployment by `SEQPALD_CONFIDENTIAL`.
  There is no such thing as a confidential asset; do not reintroduce an issuance-time election.
  Supervised bearer assets stay transparent by consensus.
- Clearing browser state costs a reconnect and nothing else: the key is in the holder's wallet,
  backed up by that wallet's own recovery. It never undoes an already-minted on-chain asset.
- Public copy has been rewritten several times to correct licensing and regulatory positioning.
  Treat copy changes as substantive, not cosmetic.

## Secrets and vendoring

Gitignored and must stay that way: the built `seqpald/seqpald` binary, `seqpald/*.db*`,
`seqpald/sanctions-cache/`, and `*.env`. Configuration and the OpenAMP issuer token are supplied
through an environment file on the server, never through the repo. The repository is public.

`seqpald/vendor/` is **deliberately not committed** (it is ~139 MB); the server builds from the
module cache.

## Working in this repo

- **Commit author:**
  `GracedEternalKingCabbageMan <151803062+GracedEternalKingCabbageMan@users.noreply.github.com>`
- **Always open a pull request, then merge it yourself immediately.** The PR exists so the change
  and its reasoning are recorded, not because anyone is waiting to review it. There is no review
  process. If you are ever told to leave one specific PR open, that applies to that PR only and
  never becomes the default.
- PRs go against `main`, the default branch.
- **Deployment is pull-only.** The server pulls this repo from GitHub and builds there; see
  `seqpald/DEPLOY.md`. Never edit source on the server and never copy binaries onto it.

<!-- BEGIN SHARED AGENT CONVENTIONS: identical in every Sequentia repo. Change it in all of them together. -->
## Working with git and GitHub here

These rules are the same in every Sequentia repository. They are repeated in each
one because this file is the only thing an agent is guaranteed to read, whatever
machine it is working from.

**Nothing pushed to GitHub credits Claude, Anthropic, or any AI tool.** No
`Co-Authored-By: Claude` trailer, no `Claude-Session:` trailer or `claude.ai`
link, no "Generated with Claude Code" in a commit message or a pull request body,
no `claude/*` branch names or session ids, and no mention in source, comments,
docs or issue text. Agent tooling offers several of these by default; compose the
message without them rather than stripping them afterwards.

**Author every commit as**
`GracedEternalKingCabbageMan <151803062+GracedEternalKingCabbageMan@users.noreply.github.com>`.
Never a personal address.

**Every change lands through a pull request that you merge yourself, at once.**
There is no reviewer on this project; the pull request exists so the reasoning is
recorded beside the diff. Branch, push, open it, merge it, delete the branch, all
in one sitting. Pushing straight to the default branch is the rule most often
broken here, and it is the one that costs the record. A pull request stays open
only when the repository owner asks for that specific one, and that never carries
over to the next.

**Name branches `area/short-description`**: `fix/`, `doc/`, `feature/`, `test/`,
`build/`, or the component being changed. Never a tool name, a session id, or
`worktree-*`.

**Write the subject as `area: what changed`**, one line, 72 characters at the
outside and 50 where you can manage it. Put the reasoning in the body, and
explain why rather than what.

**These repositories are public and world-readable.** Never commit private keys,
seeds, `wallet.dat`, RPC credentials, `.env` files or API tokens. Read the diff
before every commit. Secrets belong on the server and in offline backups.

**A file belongs to the repository whose code it describes.** Decide which repo
owns it before writing it; if it landed in the wrong one, move it rather than
deleting it.

**Documentation is part of the change, not a follow-up.** A change that makes a
README, a doc page, a runbook or a code comment wrong is not finished until that
text is right again, in the same pull request as the code. Before you open the
pull request, search the repository for whatever you renamed, moved or removed —
the old binary name, the old path, the old flag, the old command — and fix every
hit. If the change falsifies another repository's documentation, that repository
gets its own pull request in the same sitting. A stale instruction costs a new
user more than a missing one: they trust it, run it, it fails, and the failure
reads as broken software rather than as an out-of-date sentence.

**Write documentation to be timeless.** Assume the reader is new, arrived today,
and wants to know what the software is and how to use it right now. They do not
care what changed, what it used to be called, or which version added what. So
write in the present tense about current behaviour, and leave the history out:
no changelogs, no "new in", no "recently", no "coming soon", no status or
progress sections, no roadmaps, no dated notes. Quote a version number only where
the reader cannot act without it, and prefer pointing at the file that carries it
over copying the digits. Timeless does not mean thin — what the product is, who
it is for, and how to install, configure and use it all still belong there, in
full. Documentation written this way survives a release without an edit, which is
what keeps it true; the history already has homes in the git log, the tags and
the release notes.

**Push the same day you commit.** The testnet server pulls only from GitHub, so a
branch left on one laptop is invisible to every other machine and to the box.
<!-- END SHARED AGENT CONVENTIONS -->
