# shellcheck shell=bash
# Shared helpers for the SeqPal testnet regenesis recovery scripts.
#
# Nothing in here embeds a secret. Every credential (the OpenAMP issuer token,
# any node RPC auth) is read from the environment at call time and is never
# written to a file. Source this from the numbered scripts:
#
#   source "$(dirname "$0")/lib.sh"
#
# Environment contract (all optional unless a script says otherwise):
#   SEQPALD_DB          path to the seqpald SQLite database to recover FROM
#   OPENAMPD_DATADIR    path to the openampd state directory to recover FROM
#   OPENAMP_URL         base URL of the REBUILT openampd (e.g. http://127.0.0.1:8722)
#   SEQPAL_URL          base URL of the REBUILT seqpald  (e.g. http://127.0.0.1:8730)
#   OPENAMPD_ISSUER_TOKEN  bearer token for openampd issuer endpoints (apply only)
#   REGEN_WORKDIR       working directory for exported books, plans, reports
#
# The token is only ever passed to curl as an Authorization header built in
# memory. It is never echoed and never persisted.

set -euo pipefail

# ---- logging -------------------------------------------------------------

log()  { printf '[regenesis] %s\n' "$*" >&2; }
warn() { printf '[regenesis] WARN: %s\n' "$*" >&2; }
die()  { printf '[regenesis] FATAL: %s\n' "$*" >&2; exit 1; }

# ---- tooling -------------------------------------------------------------

require_cmd() {
  local c
  for c in "$@"; do
    command -v "$c" >/dev/null 2>&1 || die "required command not found: $c"
  done
}

# sqlite3 read helper. Runs a read-only query against a database file and prints
# the result. Opens read-only so a live seqpald holding the file is never
# disturbed; for a true point-in-time recovery, back the file up first.
sq() {
  local db="$1"; shift
  sqlite3 -readonly -noheader "file:${db}?mode=ro" "$@"
}

# ---- safety guard --------------------------------------------------------

# refuse_live aborts if a target looks like the production box or node000. The
# regenesis scripts are for a THROWAWAY local stack (or a deliberate, operator
# driven recovery). This guard stops an accidental run against the live box.
refuse_live() {
  local target="${1:-}"
  case "$target" in
    *sequentiatestnet.com*|*node000*|*159.195.15.140*)
      die "refusing to target what looks like the live box or node000: $target"
      ;;
  esac
}

# curl_json METHOD URL [json-body] — issues a request and prints the response
# body. Reads the issuer token from OPENAMPD_ISSUER_TOKEN only when AUTH=1 is
# set in the environment for this call. Never logs the token.
curl_json() {
  local method="$1" url="$2" body="${3:-}"
  local -a args=(-sS -X "$method" -H 'Content-Type: application/json')
  if [ "${AUTH:-0}" = "1" ]; then
    [ -n "${OPENAMPD_ISSUER_TOKEN:-}" ] || die "OPENAMPD_ISSUER_TOKEN required for this call"
    args+=(-H "Authorization: Bearer ${OPENAMPD_ISSUER_TOKEN}")
  fi
  [ -n "$body" ] && args+=(--data "$body")
  curl "${args[@]}" "$url"
}

workdir() {
  local d="${REGEN_WORKDIR:-./regen-work}"
  mkdir -p "$d"
  printf '%s\n' "$d"
}
