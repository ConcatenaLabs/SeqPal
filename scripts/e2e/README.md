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
ISSUER_ENVELOPE=/secure/issuer-id.json  ISSUER_PASSPHRASE=… \
INVESTOR_ENVELOPE=/secure/investor-id.json INVESTOR_PASSPHRASE=… \
ISSUANCE_ID=<deployed-issuance> \
  node scripts/e2e/run.mjs --only m7 --fund-cmd './box-fund.sh'
```

`--fund-cmd` runs a box-side command with `FUND_ADDRESS`, `FUND_AMOUNT`, and
`FUND_CCY` in its environment; the keys stay on the box. With no `--fund-cmd` a
live run prints the deposit address and pauses for the operator on a TTY.

## Config (environment; never inlined)

| Variable | Meaning |
| --- | --- |
| `BASE_URL` | origin (default `https://sequentiatestnet.com`) |
| `ISSUANCE_ID` | an existing deployed issuance for the M6/M7/M8 proofs |
| `ASSET_ID` | read-probe asset (default USDX) |
| `ISSUER_ENVELOPE` / `ISSUER_PASSPHRASE` | the issuer's exported SeqPal ID backup + passphrase |
| `INVESTOR_ENVELOPE` / `INVESTOR_PASSPHRASE` | the investor's backup + passphrase |
| `FUND_CMD` | box-side funding command (or `--fund-cmd`) |

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
shaped correctly; the atomic-close, reconciliation, refusal-guard, supply,
external-key, and set-hash invariants are asserted on representative records; the
live read-only surfaces (`/seqpal/api/health`, `/openamp/v1/assets`) are reached.

Live: each step completes its fund movement once the operator funds the printed
deposit, and prints the on-chain txid / evidence. See `../../ACCEPTANCE.md` for
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
