#!/usr/bin/env bash
#
# Restore seqpald's books and records from a snapshot written by backup.sh. This
# restores the SQLite database (including the content-addressed document store,
# which lives inside it) and, when present, the sanctions-list cache archive.
#
# Usage:
#   scripts/restore.sh SNAPSHOT_DB [DB_PATH]
# Defaults:
#   DB_PATH = $SEQPALD_DB or ./seqpald.db
#
# STOP seqpald before restoring: a live process holds the WAL and would fight the
# restore. The script refuses to overwrite a database that appears to be open
# (a present -wal file) unless SEQPALD_FORCE_RESTORE=1 is set.
#
# The restore is verified: the snapshot's integrity is checked, and its recorded
# sha256 (if a .sha256 sidecar exists) is confirmed before anything is replaced.
# The existing database is moved aside, never deleted, so a bad restore is
# reversible.

set -euo pipefail

SNAPSHOT="${1:?usage: restore.sh SNAPSHOT_DB [DB_PATH]}"
DB_PATH="${2:-${SEQPALD_DB:-./seqpald.db}}"
SCREEN_DIR="${SEQPALD_SCREEN_DIR:-./sanctions-cache}"

if [[ ! -f "$SNAPSHOT" ]]; then
  echo "restore: snapshot not found: $SNAPSHOT" >&2
  exit 1
fi

# Verify the snapshot before we trust it.
if [[ -f "$SNAPSHOT.sha256" ]] && command -v sha256sum >/dev/null 2>&1; then
  if ! (cd "$(dirname "$SNAPSHOT")" && sha256sum -c "$(basename "$SNAPSHOT").sha256" >/dev/null); then
    echo "restore: snapshot sha256 mismatch; refusing" >&2
    exit 1
  fi
fi
if command -v sqlite3 >/dev/null 2>&1; then
  if ! sqlite3 "$SNAPSHOT" 'PRAGMA integrity_check;' | grep -qx 'ok'; then
    echo "restore: snapshot failed integrity check; refusing" >&2
    exit 1
  fi
fi

# Refuse to clobber a database that looks open.
if [[ -f "$DB_PATH-wal" && "${SEQPALD_FORCE_RESTORE:-0}" != "1" ]]; then
  echo "restore: $DB_PATH looks open (a -wal file is present). Stop seqpald first," >&2
  echo "         or set SEQPALD_FORCE_RESTORE=1 to override." >&2
  exit 1
fi

# Move the existing database aside (reversible), then install the snapshot.
if [[ -f "$DB_PATH" ]]; then
  ASIDE="$DB_PATH.pre-restore.$(date -u +%Y%m%dT%H%M%SZ)"
  mv -f "$DB_PATH" "$ASIDE"
  [[ -f "$DB_PATH-wal" ]] && mv -f "$DB_PATH-wal" "$ASIDE-wal" || true
  [[ -f "$DB_PATH-shm" ]] && mv -f "$DB_PATH-shm" "$ASIDE-shm" || true
  echo "restore: previous database moved to $ASIDE"
fi

cp -f "$SNAPSHOT" "$DB_PATH"
# If the snapshot carried its own WAL (the CLI-less backup path), install it too.
[[ -f "$SNAPSHOT-wal" ]] && cp -f "$SNAPSHOT-wal" "$DB_PATH-wal" || true

# Restore the sanctions cache if an archive from the same run is alongside it.
STAMP="$(basename "$SNAPSHOT" | sed -n 's/^seqpald-\(.*\)\.db$/\1/p')"
ARCHIVE="$(dirname "$SNAPSHOT")/sanctions-cache-$STAMP.tar.gz"
if [[ -n "$STAMP" && -f "$ARCHIVE" ]]; then
  mkdir -p "$(dirname "$SCREEN_DIR")"
  tar -xzf "$ARCHIVE" -C "$(dirname "$SCREEN_DIR")"
  echo "restore: sanctions cache restored from $ARCHIVE"
fi

echo "restore: installed $SNAPSHOT as $DB_PATH. Start seqpald; it will reconcile live issuances from chain on boot."
