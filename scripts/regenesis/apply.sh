#!/usr/bin/env bash
#
# apply.sh — execute a regenesis plan.json against a REBUILT stack.
#
# Two phases, because they have very different requirements:
#
#   Phase 1 (identity, no chain): re-register every user's enclave pubkeys at
#     openampd (POST /v1/users) and re-stamp their categories (POST
#     /v1/issuer/categories). These are pure policy-server store writes: they
#     need the issuer token but NOT a funded node, so they run anywhere.
#
#   Phase 2 (assets, funded chain): for each issuance, re-issue the asset from
#     the stored terms (POST /v1/issuer/assets) which mints a NEW asset id, then
#     emit a per-holder distribution manifest (treasury -> each holder enclave)
#     for the recovered balances. Phase 2 requires a funded Sequentia node behind
#     openampd; the operator runs it against the box, never a bare local stack.
#
# Default mode is --dry-run: it prints every request body (no secrets) and
# broadcasts nothing. --broadcast performs the calls. --phase1-only runs just the
# identity phase (safe against a bare openampd with no node).
#
# Secrets: the issuer token is read from OPENAMPD_ISSUER_TOKEN and passed only as
# an Authorization header. It is never printed or written to a file.
#
# Usage:
#   OPENAMP_URL=http://127.0.0.1:8722 ./apply.sh --in plan.json [--dry-run|--broadcast] [--phase1-only]
#
set -euo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
source "$DIR/lib.sh"

require_cmd jq curl

MODE="dry-run"
PHASE1_ONLY=0
PLAN=""
for a in "$@"; do
  case "$a" in
    --broadcast)   MODE="broadcast" ;;
    --dry-run)     MODE="dry-run" ;;
    --phase1-only) PHASE1_ONLY=1 ;;
    --in) : ;; # handled below
    *) if [ -z "$PLAN" ] && [ -f "$a" ]; then PLAN="$a"; fi ;;
  esac
done
i=$(( $# )); prev=""
for a in "$@"; do [ "$prev" = "--in" ] && PLAN="$a"; prev="$a"; done
PLAN="${PLAN:-plan.json}"
[ -f "$PLAN" ] || die "plan not found: $PLAN (pass --in plan.json)"

OPENAMP_URL="${OPENAMP_URL:?set OPENAMP_URL to the REBUILT openampd base URL}"
refuse_live "$OPENAMP_URL"
WORK="$(workdir)"
APPLIED="$WORK/applied.json"
DISTMAN="$WORK/distribution-manifest.json"

log "apply mode=$MODE phase1_only=$PHASE1_ONLY plan=$PLAN openamp=$OPENAMP_URL"

do_post() { # path json-body auth(0/1)
  local path="$1" body="$2" auth="${3:-0}"
  if [ "$MODE" = "dry-run" ]; then
    printf 'POST %s%s\n%s\n\n' "$OPENAMP_URL" "$path" "$body"
    return 0
  fi
  AUTH="$auth" curl_json POST "$OPENAMP_URL$path" "$body"
}

# ---- Phase 1: identity ----------------------------------------------------
log "phase 1: re-register users and re-stamp categories"
jq -c '.users[]' "$PLAN" | while read -r u; do
  aid="$(printf '%s' "$u" | jq -r '.aid')"
  pubkeys="$(printf '%s' "$u" | jq -c '{pubkeys}')"
  cats="$(printf '%s' "$u" | jq -c '{aid, categories}')"
  log "user $aid (categories: $(printf '%s' "$u" | jq -c '.categories'))"
  do_post /v1/users "$pubkeys" 0 >/dev/null || warn "register failed: $aid"
  do_post /v1/issuer/categories "$cats" 1 >/dev/null || warn "categories failed: $aid"
done

if [ "$PHASE1_ONLY" = "1" ]; then
  log "phase1-only: done (assets not touched)"
  exit 0
fi

# ---- Phase 2: assets ------------------------------------------------------
log "phase 2: re-issue assets (NEW asset ids) and build the distribution manifest"
echo '[]' > "$APPLIED"
echo '[]' > "$DISTMAN"

jq -c '.assets[]' "$PLAN" | while read -r asset; do
  iss="$(printf '%s' "$asset" | jq -r '.issuance_id')"
  old="$(printf '%s' "$asset" | jq -r '.old_asset_id')"
  # Issue the full recovered supply to the issuer treasury enclave. The stored
  # terms/rules ride along; clawback + confidential flags are preserved; an
  # external-issuer asset carries its issuer_pubkey so the L_claw leaf is rebuilt
  # with the entity's own key.
  issue_body="$(printf '%s' "$asset" | jq -c '{
    name, ticker, precision,
    atoms: .supply_atoms,
    holder_aid: .issuer_aid,
    issuer_aid: .issuer_aid,
    clawback, confidential,
    rules: .terms
  } + (if .issuer_external and (.issuer_pubkey|length>0) then {issuer_pubkey: .issuer_pubkey} else {} end)')"

  log "re-issue issuance=$iss (old asset $old) -> NEW asset id"
  if [ "$MODE" = "dry-run" ]; then
    do_post /v1/issuer/assets "$issue_body" 1
    newid="<new-asset-id-pending-broadcast>"; txid="<pending>"
  else
    resp="$(do_post /v1/issuer/assets "$issue_body" 1)"
    newid="$(printf '%s' "$resp" | jq -r '.asset // empty')"
    txid="$(printf '%s' "$resp" | jq -r '.txid // empty')"
    [ -n "$newid" ] || { warn "issue returned no asset id for $iss: $resp"; continue; }
  fi

  # record old -> new
  tmp="$(mktemp)"
  jq --arg iss "$iss" --arg old "$old" --arg new "$newid" --arg txid "$txid" \
    '. + [{issuance_id:$iss, old_asset_id:$old, new_asset_id:$new, issue_txid:$txid}]' \
    "$APPLIED" > "$tmp" && mv "$tmp" "$APPLIED"

  # per-holder distribution manifest: treasury -> holder enclave for the
  # recovered atoms. Delivery reuses the platform's policy-co-signed transfer
  # machinery (M5/M7); it is an operator step, listed here explicitly.
  printf '%s' "$asset" | jq -c --arg iss "$iss" --arg new "$newid" \
    '.remint | to_entries | map({issuance_id:$iss, new_asset_id:$new, holder_aid:.key, atoms:.value})' \
    > "$WORK/.dist.$iss.json"
done

# merge per-issuance distribution fragments
if ls "$WORK"/.dist.*.json >/dev/null 2>&1; then
  jq -s 'add' "$WORK"/.dist.*.json > "$DISTMAN"
  rm -f "$WORK"/.dist.*.json
fi

log "applied map: $APPLIED"
log "distribution manifest (operator delivers via seqpald servicing): $DISTMAN"
[ "$MODE" = "dry-run" ] && warn "dry-run: nothing was broadcast; new asset ids are placeholders"
exit 0
