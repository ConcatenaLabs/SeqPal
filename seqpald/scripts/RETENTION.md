# seqpald backup and retention schedule

seqpald is the books and records of the platform. Everything the UI shows as a
financial fact is read from the chain or from seqpald's SQLite database; the
database is the only copy of the platform-side records (accounts, SeqPal IDs,
entities, issuances and lifecycle, claims and screening decisions, the review
queue, the content-addressed document store, the terms to manifest bindings, the
RFSA filings, the rules-amendment chain, document e-signatures, and the
append-only audit log). Losing it without a backup loses the register's off-chain
half and the retention duties a registrar and transfer agent owes.

This schedule covers the seqpald database and the sanctions-list cache. It does
NOT cover the OpenAMP policy server's own key and state material (LocalKeySigner
keys and openampd state), which live only on the box and are backed up by the
node and policy-server continuity procedure, disclosed on the status page and in
the offering documents; the M10 regenesis runbook covers a chain reset.

## What is backed up

- The SQLite database (`SEQPALD_DB`, default `seqpald.db`). The document store is
  a table inside this database, keyed by each artifact's sha256 content address,
  so a consistent database snapshot is a consistent document-store snapshot.
  There are no document blobs outside the database.
- The sanctions-list cache (`SEQPALD_SCREEN_DIR`, default `sanctions-cache`),
  archived alongside each snapshot. The lists are re-downloadable, but pinning
  the exact list state preserves the evidence a screening decision was made
  against.

## Schedule

| What | Cadence | Retention | Where |
|---|---|---|---|
| Full database snapshot (`backup.sh`) | Daily, and before every deploy of a new seqpald build | 35 days of daily snapshots on the box | `SEQPALD_BACKUP_DIR` (default `./backups`) |
| Off-box copy of the latest daily snapshot | Daily | 90 days | Operator-controlled off-box store |
| Weekly snapshot promoted to long-term | Weekly | 1 year | Off-box |
| Pre-restore safety copy (`restore.sh` moves the live DB aside) | On every restore | Until the restore is confirmed good, then operator-pruned | Next to the database |
| Audit-log continuity | Continuous (the audit log is append-only and hash-chained inside the database) | For the life of the platform; never truncated | In-database |

`backup.sh` prunes on-box database snapshots older than
`SEQPALD_BACKUP_RETAIN_DAYS` (default 35). Off-box retention (90 days rolling,
weekly promotions kept a year) is enforced by the operator's off-box store, not
by this script.

Retention floors: keep at least one snapshot per calendar month for a year, and
never prune a snapshot that predates the most recent unreconciled discrepancy.
The audit log is never truncated.

## Verifying a backup

`backup.sh` runs `PRAGMA integrity_check` on each snapshot and writes a
`.sha256` sidecar. `restore.sh` re-checks both before it replaces the live
database, and it moves the existing database aside rather than deleting it, so a
restore is always reversible.

## Restore drill

Practice quarterly against a throwaway copy:

```
# 1. Take a snapshot.
scripts/backup.sh /path/to/seqpald.db /tmp/seqpal-backups

# 2. Stop seqpald (it holds the WAL).
systemctl --user stop seqpald   # or however this instance is run

# 3. Restore the latest snapshot into a scratch path and boot against it.
scripts/restore.sh /tmp/seqpal-backups/seqpald-<STAMP>.db /tmp/seqpald-restored.db
SEQPALD_DB=/tmp/seqpald-restored.db seqpald ...

# 4. Confirm accounts, issuances, documents (GET /api/terms/{hash} and
#    /api/doc/{hash}), filings (GET /api/rfsa/filings/{number}), and the audit
#    log resolve, then discard the scratch database.
```

On a real restore, start seqpald normally after `restore.sh`; the chain watcher
reconciles live issuances from chain on boot, so the on-chain half of the
register is re-derived rather than trusted from the snapshot.
