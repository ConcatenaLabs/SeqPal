#!/usr/bin/env bash
#
# restore.sh — the restore half of the backup/restore drill.
#
# Unpacks an archive produced by backup.sh, verifies every file against the
# manifest's SHA-256, runs PRAGMA integrity_check on the restored seqpald.db,
# and places the files at the requested target paths. A mismatch aborts before
# anything is written to the targets.
#
# Usage:
#   ./restore.sh ARCHIVE.tar.gz TARGET_SEQPALD_DB TARGET_OPENAMPD_DATADIR
#
set -euo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
source "$DIR/lib.sh"

require_cmd sqlite3 tar sha256sum

ARCHIVE="${1:?usage: restore.sh ARCHIVE TARGET_SEQPALD_DB TARGET_OPENAMPD_DATADIR}"
TARGET_DB="${2:?target seqpald.db path required}"
TARGET_OA="${3:?target openampd datadir required}"
[ -f "$ARCHIVE" ] || die "archive not found: $ARCHIVE"

STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
tar -C "$STAGE" -xzf "$ARCHIVE"
ROOT="$(find "$STAGE" -maxdepth 1 -type d -name 'seqpal-backup-*' | head -n1)"
[ -n "$ROOT" ] || die "archive does not contain a seqpal-backup-* directory"
[ -f "$ROOT/manifest.json" ] || die "manifest.json missing from archive"

# 1. verify every listed file's checksum.
log "verifying manifest checksums"
fail=0
while read -r path sum; do
  [ -n "$path" ] || continue
  actual="$(sha256sum "$ROOT/$path" 2>/dev/null | cut -d' ' -f1)"
  if [ "$actual" != "$sum" ]; then
    warn "checksum MISMATCH: $path (manifest $sum, got ${actual:-<missing>})"
    fail=1
  fi
done < <(jq -r '.files[] | "\(.path) \(.sha256)"' "$ROOT/manifest.json")
[ $fail -eq 0 ] || die "one or more files failed checksum verification; not restoring"
log "all checksums verified"

# 2. integrity-check the restored database before placing it.
sqlite3 -readonly "$ROOT/seqpald.db" 'PRAGMA integrity_check;' | grep -qx ok \
  || die "restored seqpald.db failed integrity_check"
VER="$(sqlite3 -readonly "$ROOT/seqpald.db" 'SELECT COALESCE(MAX(version),-1) FROM schema_version;' 2>/dev/null || echo '?')"
log "seqpald.db integrity ok (schema_version=$VER)"

# 3. place files. Existing targets are moved aside, never clobbered silently.
mkdir -p "$(dirname "$TARGET_DB")" "$TARGET_OA"
if [ -e "$TARGET_DB" ]; then
  mv "$TARGET_DB" "$TARGET_DB.pre-restore.$(date -u +%s)"
  warn "existing target db moved aside"
fi
cp -p "$ROOT/seqpald.db" "$TARGET_DB"
for f in state.json keys.json transparency.log; do
  [ -f "$ROOT/openampd/$f" ] && cp -p "$ROOT/openampd/$f" "$TARGET_OA/$f"
done

log "restore complete -> db=$TARGET_DB openampd=$TARGET_OA"
