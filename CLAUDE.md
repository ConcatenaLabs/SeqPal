# SeqPal

A tokenization-as-a-service platform proof of concept: a React front-end plus a thin Go backend
that **actually issues restricted assets on the Sequentia testnet** through a live
[OpenAMP](https://github.com/GracedEternalKingCabbageMan/openamp) policy server.

`README.md`'s "What is real vs simulated" section is the most important thing in the repo. The
token deployment is real; the compliance and legal scaffolding around it (KYC/KYB, payments,
e-signature, incorporation, escrow) is simulated so the flow can be walked end to end. Never blur
that line in code or in copy.

Node and consensus conventions live in the
[`Sequentia`](https://github.com/GracedEternalKingCabbageMan/Sequentia) repo.

## Two halves

| Path | What |
|---|---|
| `src/` | The React + Vite single-page app. Front-end state lives in the browser's `localStorage`. |
| `seqpald/` | The Go backend, its own module. It serves the built SPA (with history-API fallback) *and* the API, so one reverse-proxy route covers both. |
| `scripts/` | `live-probe.sh`, the `e2e/` driver, and the `regenesis/` runbook scripts. |
| `seqpald/M*-CONTRACT.md` | Per-milestone contracts, each with a matching `m*_test.go`. |

```sh
npm install
npm run dev          # vite
npm run build        # vite build -> dist/
npm run preview
npm test             # node --test

cd seqpald && go build ./... && go test ./...
```

There is no CI. Those commands are the whole gate.

## The custody line

- The browser generates a real secp256k1 enclave keypair per SeqPal ID (`src/lib/keys.js`). **The
  private key never leaves the browser.** Only the x-only public key reaches the backend.
- `seqpald` is deliberately **not** a custodian. It registers the holder's public key and mints the
  initial supply into that holder's own enclave.
- `seqpald` exists for one security reason: OpenAMP's issuer endpoint is bearer-token gated, and
  that token is a server-side secret that must never reach a browser. `seqpald` holds it and is the
  only party that calls the issuer endpoint.
- Everything the browser can read without the token — assets, balances, enclave addresses, the
  transparency log — it fetches from OpenAMP's public endpoints directly, same-origin.

Do not add a route that proxies a browser request straight through to an issuer endpoint, and do
not move any key material server-side.

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
- Clearing browser state resets the demo but does **not** undo an already-minted on-chain asset.
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
- PRs go against `claude/session-85GzG`, which is this repository's default branch. It is an odd
  name for a default branch, but it is the one GitHub serves and the only branch that exists.
- **Deployment is pull-only.** The server pulls this repo from GitHub and builds there; see
  `seqpald/DEPLOY.md`. Never edit source on the server and never copy binaries onto it.
