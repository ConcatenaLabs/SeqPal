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
| `src/` | The React + Vite single-page app. The browser keeps only the encrypted SeqPal ID key envelope and UI preferences in `localStorage`; records come from seqpald. |
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
- Clearing browser state loses the key envelope, not the records, and does **not** undo an
  already-minted on-chain asset.
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

**Push the same day you commit.** The testnet server pulls only from GitHub, so a
branch left on one laptop is invisible to every other machine and to the box.
<!-- END SHARED AGENT CONVENTIONS -->
