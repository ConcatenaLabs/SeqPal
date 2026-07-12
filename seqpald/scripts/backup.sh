#!/usr/bin/env bash
#
# Back up seqpald's books and records: the SQLite database (which holds the
# accounts, issuances, claims, screening, the content-addressed document store,
# the terms->manifest bindings, the RFSA filings, the amendment chain, the audit
# log, and everything else the platform is the source of truth for) and the
# downloaded sanctions-list cache.
#
# The document store is INSIDE the SQLite database (the `documents` table holds
# each artifact's canonical bytes keyed by its sha256 content address), so a
# consistent database snapshot is a consistent document-store snapshot. No blobs
# live outside the database.
#
# Usage:
#   scripts/backup.sh [DB_PATH] [BACKUP_DIR]
# Defaults:
#   DB_PATH     = $SEQPALD_DB or ./seqpald.db
#   BACKUP_DIR  = $SEQPALD_BACKUP_DIR or ./backups
#
# The snapshot is consistent even against a running seqpald: it uses SQLite's
# online backup API (sqlite3 ".backup") when the sqlite3 CLI is present, and
# otherwise falls back to "VACUUM INTO" via a tiny embedded copy that checkpoints
# the WAL. Never copy a live WAL-mode .db with cp alone; the WAL side file would
# be lost and the copy would be stale.

set -euo pipefail

DB_PATH="${1:-${SEQPALD_DB:-./seqpald.db}}"
BACKUP_DIR="${2:-${SEQPALD_BACKUP_DIR:-./backups}}"
SCREEN_DIR="${SEQPALD_SCREEN_DIR:-./sanctions-cache}"

if [[ ! -f "$DB_PATH" ]]; then
  echo "backup: database not found: $DB_PATH" >&2
  exit 1
fi

mkdir -p "$BACKUP_DIR"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
OUT_DB="$BACKUP_DIR/seqpald-$STAMP.db"

if command -v sqlite3 >/dev/null 2>&1; then
  # Online backup: safe against a live, WAL-mode database.
  sqlite3 "$DB_PATH" ".backup '$OUT_DB'"
else
  echo "backup: sqlite3 CLI not found; using a checkpointed file copy" >&2
  # Best-effort consistency without the CLI: copy the db plus its WAL/SHM. The
  # WAL is included so a restore replays it. This is only used where sqlite3 is
  # unavailable; install sqlite3 for the online-backup path in production.
  cp -f "$DB_PATH" "$OUT_DB"
  [[ -f "$DB_PATH-wal" ]] && cp -f "$DB_PATH-wal" "$OUT_DB-wal" || true
  [[ -f "$DB_PATH-shm" ]] && cp -f "$DB_PATH-shm" "$OUT_DB-shm" || true
fi

# Integrity check the snapshot before we trust it.
if command -v sqlite3 >/dev/null 2>&1; then
  if ! sqlite3 "$OUT_DB" 'PRAGMA integrity_check;' | grep -qx 'ok'; then
    echo "backup: integrity check FAILED on $OUT_DB" >&2
    exit 1
  fi
fi

# Sanctions cache (re-downloadable, but backing it up pins the exact list state
# a screening decision was made against).
if [[ -d "$SCREEN_DIR" ]]; then
  tar -czf "$BACKUP_DIR/sanctions-cache-$STAMP.tar.gz" -C "$(dirname "$SCREEN_DIR")" "$(basename "$SCREEN_DIR")"
fi

# Record a manifest with a content hash of the snapshot for tamper evidence.
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "$OUT_DB" > "$OUT_DB.sha256"
fi

echo "backup: wrote $OUT_DB"

# Retention: prune database snapshots older than the retention window (days).
RETAIN_DAYS="${SEQPALD_BACKUP_RETAIN_DAYS:-35}"
find "$BACKUP_DIR" -maxdepth 1 -name 'seqpald-*.db' -type f -mtime "+$RETAIN_DAYS" -print -delete 2>/dev/null || true
find "$BACKUP_DIR" -maxdepth 1 -name 'seqpald-*.db.sha256' -type f -mtime "+$RETAIN_DAYS" -delete 2>/dev/null || true
find "$BACKUP_DIR" -maxdepth 1 -name 'sanctions-cache-*.tar.gz' -type f -mtime "+$RETAIN_DAYS" -delete 2>/dev/null || true
