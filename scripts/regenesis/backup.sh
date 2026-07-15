#!/usr/bin/env bash
#
# backup.sh — the backup half of the SeqPal backup/restore drill.
#
# Snapshots the two stores that hold everything the platform can rebuild itself
# from after a chain reset:
#   1. the seqpald SQLite database (books and records: accounts, claims,
#      issuances + stored terms, subscriptions, servicing history), and
#   2. the openampd state directory (state.json, keys.json, transparency.log).
#
# It produces one timestamped tar.gz plus a manifest.json with a SHA-256 for
# every file, so restore.sh can prove the bytes came back intact.
#
# SECURITY: the openampd datadir contains keys.json, which holds PRIVATE keys
# (the policy key and any server-generated issuer keys). This archive is
# therefore SENSITIVE. Store it encrypted and off the public path. NEVER commit
# it. This script does not encrypt for you; it warns and leaves that to the
# operator's backup pipeline.
#
# Usage:
#   SEQPALD_DB=/path/seqpald.db OPENAMPD_DATADIR=/path/.openampd \
#     ./backup.sh [OUTPUT_DIR]
#
set -euo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
source "$DIR/lib.sh"

require_cmd sqlite3 tar gzip sha256sum

SEQPALD_DB="${SEQPALD_DB:?set SEQPALD_DB to the seqpald.db to back up}"
OPENAMPD_DATADIR="${OPENAMPD_DATADIR:?set OPENAMPD_DATADIR to the openampd state dir}"
OUT_DIR="${1:-${REGEN_WORKDIR:-./regen-backups}}"

[ -f "$SEQPALD_DB" ]      || die "seqpald db not found: $SEQPALD_DB"
[ -d "$OPENAMPD_DATADIR" ] || die "openampd datadir not found: $OPENAMPD_DATADIR"

mkdir -p "$OUT_DIR"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
SNAP="$STAGE/seqpal-backup-$STAMP"
mkdir -p "$SNAP/openampd"

# 1. seqpald.db: use the sqlite backup API for a consistent point-in-time copy
#    even if a seqpald process holds the file (WAL-safe).
log "snapshotting seqpald database (consistent sqlite backup)"
sqlite3 "$SEQPALD_DB" ".backup '$SNAP/seqpald.db'"
sqlite3 -readonly "$SNAP/seqpald.db" 'PRAGMA integrity_check;' | grep -qx ok \
  || die "snapshot failed integrity_check"

# 2. openampd datadir: copy the flat JSON + log store.
log "copying openampd state directory"
for f in state.json keys.json transparency.log; do
  if [ -f "$OPENAMPD_DATADIR/$f" ]; then
    cp -p "$OPENAMPD_DATADIR/$f" "$SNAP/openampd/$f"
  else
    warn "openampd file missing (skipped): $f"
  fi
done

# 3. manifest with per-file checksums.
log "writing manifest"
(
  cd "$SNAP"
  find . -type f ! -name manifest.json -print0 | sort -z | xargs -0 sha256sum > .sums
  printf '{\n'
  printf '  "created_at": "%s",\n' "$STAMP"
  printf '  "source": { "seqpald_db": "%s", "openampd_datadir": "%s" },\n' \
    "$SEQPALD_DB" "$OPENAMPD_DATADIR"
  printf '  "files": [\n'
  first=1
  while read -r sum path; do
    [ $first -eq 1 ] || printf ',\n'
    first=0
    printf '    { "path": "%s", "sha256": "%s" }' "${path#./}" "$sum"
  done < .sums
  printf '\n  ]\n}\n'
  rm -f .sums
) > "$SNAP/manifest.json"

ARCHIVE="$OUT_DIR/seqpal-backup-$STAMP.tar.gz"
tar -C "$STAGE" -czf "$ARCHIVE" "seqpal-backup-$STAMP"
CHK="$(sha256sum "$ARCHIVE" | cut -d' ' -f1)"

log "backup complete: $ARCHIVE"
log "archive sha256: $CHK"
warn "this archive contains openampd keys.json (PRIVATE KEYS). Encrypt it at rest and never commit it."
printf '%s\n' "$ARCHIVE"
