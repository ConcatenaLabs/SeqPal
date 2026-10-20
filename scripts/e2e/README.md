# SeqPal live end-to-end acceptance driver

`run.mjs` produces the deferred on-chain proofs from M5-M9 against a running
SeqPal stack, signing EXACTLY as the browser by importing the real SPA signers
from `lib/wallet-signer.mjs`, which plays the holder's wallet over the statement
constructions in `src/lib/statements.js`. It is the flagship of M10 deliverable 4; the QA mapping it
feeds lives in `../../ACCEPTANCE.md`.

It holds no secrets. The base URL, the identity keys it signs with, and any
funding command all come from the environment or flags. Wallet funding is a hook,
never an embedded box credential. A `--dry-run` mode signs and shapes every
request without broadcasting, so the driver validates offline.

## Files

- `run.mjs` the driver: config, the health probe, and the M6/M7/M8/M9 proof steps.
- `wallet-live.mjs` the same question asked of the OTHER kind of holder: a
  SeqPal ID that is only a wallet, with no OpenAMP account and no browser
  extension, which signs ordinary messages and pastes them. It walks
  registration, the passport, verification, the refusals, a payout mandate, the
  market-abuse acknowledgment and all three deploy elections, and needs no
  funding: nothing it does moves a coin.
- `supply-cap-page.mjs` the most there will ever be of a token, through the
  issuance page in headless Chromium against a real node, openampd and seqpald:
  the deploy form's question and default, a mint, one atom past the room left
  refused in plain words, a mint to the cap, one atom more refused, with the
  page's supply figures read after each step. `../regtest-supply-cap.sh` starts
  the stack on a private regtest chain and runs it.
- `lib/cdp.mjs` the few Chrome DevTools Protocol calls that drive a page, over
  Node's own WebSocket.
- `lib/hd.mjs` just enough BIP32 to BE a wallet -- an account xpub, its ranged
  receive descriptor, and the private key behind any receive index. A wallet is
  identified by a ranged descriptor, so a driver playing one has to derive.
  `signWalletMessage` in `lib/wallet-signer.mjs` then signs the way every wallet
  has always been able to, in the format `verifymessage` checks.
- `lib/http.mjs` a same-origin client with a cookie jar (the `seqpal_session`
  cookie seqpald sets, scoped to `/seqpal`). No third-party dependency.
- `lib/id.mjs` a `SeqPalID` wrapping `wallet-signer.mjs`, with the
  store.jsx `signTransferSigs` / `signClawbackSigs` oracle guards verbatim.
- `lib/util.mjs` step logging, assertions that print their evidence, the funding
  hook, and the confirmation waiter (nothing final at 0-conf).

## Run

```
# offline: sign + shape every request, hit only read-only endpoints, no broadcast
node scripts/e2e/run.mjs --dry-run

# one live proof; the operator funds the printed deposit address
BASE_URL=https://sequentiatestnet.com \
ISSUER_KEY=<32-byte hex>  INVESTOR_KEY=<32-byte hex> \
ISSUANCE_ID=<deployed-issuance> \
  node scripts/e2e/run.mjs --only m7 --fund-cmd './box-fund.sh'
```

`--fund-cmd` runs a box-side command with `FUND_ADDRESS`, `FUND_AMOUNT`, and
`FUND_CCY` in its environment; the keys stay on the box. With no `--fund-cmd` a
live run prints the deposit address and pauses for the operator on a TTY.

Two steps are the ISSUER's wallet, which the driver never holds: signing the
release (or a cancel) of an offering's escrow covenant, and funding and
signing a distribution's one payout transaction. `--sign-cmd` runs a command
with `SPEND_TX` (the unsigned transaction) and `SPEND_TO_SIGN` (the inputs to
sign, as `signopenamptransfer` takes them) in its environment and reads the
signatures JSON from its stdout; `--payout-cmd` runs a command with `PAYOUT_TX`
(the unfunded payout) and `PAYOUT_FEE_ASSET` and reads the signed transaction
hex from its stdout. With neither, a live run prints what to sign and asks the
operator to paste the result on a TTY.

## Config (environment; never inlined)

| Variable | Meaning |
| --- | --- |
| `BASE_URL` | origin (default `https://sequentiatestnet.com`) |
| `ISSUANCE_ID` | an existing deployed issuance for the M6/M7/M8 proofs |
| `ASSET_ID` | read-probe asset (default USDX) |
| `ISSUER_KEY` | the issuer's private key, 32 bytes of hex |
| `INVESTOR_KEY` | the investor's private key, 32 bytes of hex |
| `FUND_CMD` | box-side funding command (or `--fund-cmd`) |
| `SIGN_CMD` | the issuer's wallet signing an escrow release or cancel (or `--sign-cmd`) |
| `PAYOUT_CMD` | the issuer's wallet funding and signing a distribution payout (or `--payout-cmd`) |

## Live sequencing note (two principals, two sessions)

seqpald authenticates one principal per session cookie. Owner-scoped proofs
(close, distribution, clawback, listing) run as the issuer; investor-scoped calls
(investor mandate, market-abuse ack, the originating side of a P2P transfer) run
as the investor. In a full live run drive the owner steps with
`ISSUER_ENVELOPE` set, then the investor steps with `INVESTOR_ENVELOPE` set, so
each principal signs its own actions with its own key. `--dry-run` needs no
session: it signs with ephemeral keys and hits only public read endpoints.

## What is proven where

Offline (`--dry-run`): the signing is real (real BIP340 signatures over the exact
tagged statements and raw sighashes the server verifies) and every request body is
shaped correctly; the escrow-release settlement, reconciliation, refusal-guard, supply,
external-key, and set-hash invariants are asserted on representative records; the
live read-only surfaces (`/seqpal/api/health`, `/openamp/v1/assets`) are reached.

Live: each step completes its fund movement once the operator funds the printed
deposit, or the issuer's wallet signs what it is handed, and prints the on-chain
txid / evidence. See `../../ACCEPTANCE.md` for
the full Section 8 mapping and which steps need operator funding.

## The bearer drivers

- `bearer-live.mjs` deploys a freely-tradable (bearer, supervised) issuance
  through the real API path and runs the court-order freeze/unfreeze drill.
  Fresh keys per run; env `SEQPAL_BASE` (default
  `https://sequentiatestnet.com/seqpal/api`).
- `action-drill.mjs` is the live corporate-action drill: bearer deploy, a
  second verified SeqPal ID as the holder, share delivery to the holder's P2TR
  key-path address, a funded dividend claimed with a holding proof and paid to
  an ordinary P2WPKH address, and a weighted vote with the public per-outpoint
  proof list. Steps are idempotent against `scripts/e2e/.drill-state.json`
  (gitignored, mode 0600; it holds the generated private keys), so a rerun
  resumes where the last run stopped.
- `lib/drill.mjs` the shared client + flows both bearer drivers use;
  `lib/bech32.mjs` the bech32/bech32m encoder, pinned in
  `test/enforcement.test.js` against address vectors from the node's own
  `src/test/data/key_io_valid.json`.

Drill env: `SEQPAL_BASE`, `SEQPAL_ELECTRS` (default
`https://sequentiatestnet.com/api`), `SEQPAL_DRILL_SSH` (an ssh host such as
`seq`; when set the driver runs the operator sends itself, when unset it prints
the exact `sequentia-cli -rpcwallet=seqpal-escrow sendtoaddress ...` command and
waits for the funds on electrs), `SEQPAL_USDX`, `DRILL_SHARES` (default 1000),
`DRILL_DIVIDEND_ATOMS` (default 100000000, i.e. 1 USDX).
