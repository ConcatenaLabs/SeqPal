# SeqPal testnet regenesis recovery runbook

How to rebuild the SeqPal platform from its own books and records after a Sequentia
testnet chain reset (a re-genesis), and the backup/restore drill that protects those books.

**What a chain reset destroys, and what it does not.** A re-genesis starts a new chain with new
genesis and new asset ids. On-chain state (UTXOs, balances, the old asset ids, anchored log
commitments) is permanently gone. What survives is what seqpald and openampd persisted off-chain:
the seqpald SQLite books (accounts, claims, issuances + the exact stored terms, settled
subscriptions, servicing history, ownership snapshots, listings) and the openampd state directory
(state.json, keys.json, transparency.log). Regenesis rebuilds the platform from those, minting
**new** asset ids and re-registering identities. It cannot recover an old chain's UTXOs, and every
re-issued asset is disclosed as new. This disclosure is printed in the reconciliation report and
must be surfaced to holders.

## Scripts (`scripts/regenesis/`)

| Script | Role | Needs |
| --- | --- | --- |
| `backup.sh` | Snapshot the seqpald DB + the openampd datadir into one timestamped `tar.gz` with a per-file SHA-256 manifest. | read access to both stores |
| `restore.sh` | Unpack an archive, verify every file against the manifest, `PRAGMA integrity_check` the restored DB, then place the files. A mismatch aborts before writing. | an archive from `backup.sh` |
| `export-books.sh` | Read the (restored) seqpald DB **read-only** and emit `books.json`: issuances + stored terms + old asset id, accounts + enclave pubkeys, claims, settled subscriptions, completed P2P transfers, swept clawbacks, DR ops, ownership snapshots, listings. | a restored `seqpald.db` |
| `plan.mjs` | Pure transform `books.json` → `plan.json`: the recovery blueprint. Users to re-register (with categories projected from claims, mirroring `taxonomy.go projectCategories`), assets to re-issue from stored terms (old id carried only as a cross-reference), and a books-derived holders reconstruction (settled subscriptions adjusted by transfers, clawbacks, DR ops). | `books.json` |
| `apply.sh` | Execute `plan.json` against a rebuilt stack. Phase 1 (identity, no funded chain): `POST /v1/users` + `POST /v1/issuer/categories`. Phase 2 (assets, funded chain): `POST /v1/issuer/assets` per issuance (mints a NEW asset id) then emits a per-holder distribution manifest. | the rebuilt openampd + issuer token; phase 2 needs a funded node |
| `anchor.sh` | Re-anchor the rebuilt transparency-log head into an OP_RETURN (`POST /v1/issuer/anchor`). `--dry-run` by default; `--broadcast` performs it. | a funded openampd wallet |
| `reconcile.mjs` | Pure transform: emit `reconciliation.json` + `.md` — old asset id → new asset id, planned vs applied holder deltas, the recovered-vs-lost disclosure, and any planning warnings. | `plan.json` (+ optional `applied.json`, `distribution-manifest.json`) |
| `mock-node.mjs` | TEST HARNESS ONLY. A minimal JSON-RPC stub answering the two methods `server.New` needs (`getblockhash 0`, `dumpassetlabels`) so a bare openampd can boot on a throwaway LOCAL stack. Never used against the box. | none |

## Procedure

1. **Before any reset (routine):** run `backup.sh` on a schedule; keep the archive off the box.
   The archive contains `keys.json` (openampd PRIVATE keys) — encrypt it at rest, never commit it.
2. **After a reset:**
   a. `restore.sh ARCHIVE.tar.gz <seqpald.db> <openampd-datadir>` — verifies bytes + DB integrity.
   b. `export-books.sh > books.json` (against the restored DB).
   c. `node plan.mjs --in books.json --out plan.json` — inspect the plan before applying.
   d. Rebuild the stack on the reset chain, then `apply.sh plan.json` (phase 1 anywhere; phase 2
      against the funded node). Re-mint per the distribution manifest.
   e. `anchor.sh --broadcast` to re-anchor the log head.
   f. `node reconcile.mjs --plan plan.json --out reconciliation.json` — publish the report
      (old→new asset ids, holder deltas, the new-asset-id disclosure) to holders.

## Validation status

The pure-transform core (`plan.mjs`, `reconcile.mjs`) is smoke-tested locally: a sample `books.json`
plans one re-registered user (categories projected from a stored verified claim) and one asset
re-issue from stored terms (old id retained as a cross-reference only), and the reconciliation
report carries the correct new-asset-id / lost-UTXO disclosure. All shell scripts pass `bash -n`
and all Node scripts pass `node --check`. The full backup → restore → `mock-node` boot → minimal
apply drill against a throwaway LOCAL openampd + seqpald (never the box, never node000) is the
operator validation step before relying on this in anger; `mock-node.mjs` exists precisely so a
bare openampd can boot for it.

## Safety

No secrets in any script; every script reads config (URLs, the issuer token, paths) from env or
flags. `apply.sh`/`anchor.sh` read `OPENAMPD_ISSUER_TOKEN` header-only. The scripts run against a
rebuilt stack or, in testing, a throwaway local stack — never node000, whose config is never
touched. Nothing here is final at 0-conf; phase 2 and anchoring wait for confirmations.
