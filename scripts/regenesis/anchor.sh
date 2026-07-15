#!/usr/bin/env bash
#
# anchor.sh — re-anchor the rebuilt openampd transparency log on the reset chain.
#
# After phase 2 has re-issued the assets and the log has fresh entries, this
# commits the current log head into an OP_RETURN on Sequentia (POST
# /v1/issuer/anchor). It requires a funded wallet behind openampd, so it is an
# operator step run against the box, never a bare local stack.
#
# --dry-run (default) prints the call; --broadcast performs it and prints the
# anchor txid. The issuer token is read from OPENAMPD_ISSUER_TOKEN, header-only.
#
# Usage:
#   OPENAMP_URL=... OPENAMPD_ISSUER_TOKEN=... ./anchor.sh --broadcast
#
set -euo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
source "$DIR/lib.sh"
require_cmd curl jq

MODE="dry-run"
for a in "$@"; do case "$a" in --broadcast) MODE="broadcast";; --dry-run) MODE="dry-run";; esac; done

OPENAMP_URL="${OPENAMP_URL:?set OPENAMP_URL}"
refuse_live "$OPENAMP_URL"

if [ "$MODE" = "dry-run" ]; then
  log "dry-run: would POST $OPENAMP_URL/v1/issuer/anchor (funded wallet required)"
  exit 0
fi
resp="$(AUTH=1 curl_json POST "$OPENAMP_URL/v1/issuer/anchor" '')"
txid="$(printf '%s' "$resp" | jq -r '.txid // empty')"
[ -n "$txid" ] || die "anchor failed: $resp"
log "re-anchored: txid=$txid"
printf '%s\n' "$txid"
