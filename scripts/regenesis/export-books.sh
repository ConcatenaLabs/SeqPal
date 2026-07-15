#!/usr/bin/env bash
#
# export-books.sh — read the recovered seqpald database and emit a single
# books.json bundle containing everything the platform rebuilds itself from:
#   - issuances  (stored terms, the OLD asset id, precision, flags, issuer)
#   - accounts   (AIDs and their enclave pubkeys, for re-registration)
#   - claims     (KYC/eligibility, for category re-stamping)
#   - settled subscriptions, completed P2P transfers, swept clawbacks, DR ops
#     (the inputs to the books-derived holders reconstruction)
#   - ownership_snapshots (the platform's last aggregate holder snapshots)
#   - listings   (issuer-granted venue authorizations)
#
# This reads the database READ-ONLY. For a true point-in-time export, run it
# against a restored backup, not a live file.
#
# Usage:
#   SEQPALD_DB=/path/seqpald.db ./export-books.sh > books.json
#
set -euo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
source "$DIR/lib.sh"

require_cmd sqlite3 jq

SEQPALD_DB="${SEQPALD_DB:?set SEQPALD_DB to the seqpald.db to export}"
[ -f "$SEQPALD_DB" ] || die "seqpald db not found: $SEQPALD_DB"

# jn NAME SQL — run a query and print `"NAME": <json-array>` for jq assembly.
jn() {
  local name="$1" sql="$2" out
  out="$(sqlite3 -readonly -json "file:${SEQPALD_DB}?mode=ro" "$sql" 2>/dev/null || true)"
  [ -n "$out" ] || out='[]'
  printf '%s' "$out" | jq --arg n "$name" '{($n): .}'
}

log "exporting books from $SEQPALD_DB" >&2

{
  jn issuances \
    "SELECT id, owner_aid, entity_id, name, ticker, structure_id, status, terms,
            supply, precision, confidential, clawback, asset_id AS old_asset_id,
            contract_hash AS old_contract_hash, holder_aid, issuer_external,
            issuer_pubkey
       FROM issuances;"
  jn accounts \
    "SELECT aid, kind, xonly, display_name FROM accounts;"
  jn claims \
    "SELECT aid, residence, base_eligibility, accredited, accred_artifact,
            accred_valid_until, us_person, gb_hnw, gb_soph, valid_until, status,
            vocab_version FROM claims;"
  jn settled_subscriptions \
    "SELECT s.id, s.issuance_id, s.investor_aid, s.token_atoms
       FROM subscriptions s
       JOIN settlements t ON t.subscription_id = s.id
      WHERE t.delivery_txid <> '' AND t.state = 'settled';"
  jn p2p_transfers \
    "SELECT id, issuance_id, asset_id, originator_aid, beneficiary_aid, atoms
       FROM p2p_transfers WHERE state = 'settled';"
  jn clawbacks \
    "SELECT asset_id, holder_aid, atoms FROM clawbacks WHERE state = 'swept';"
  jn dr_ops \
    "SELECT issuance_id, asset_id, kind, target_aid, holder_aid, atoms
       FROM dr_ops WHERE state = 'broadcast';"
  jn ownership_snapshots \
    "SELECT issuance_id, asset_id, height, holders_count, total_atoms, report_hash,
            created_at
       FROM ownership_snapshots
      WHERE id IN (SELECT id FROM ownership_snapshots o2
                    WHERE o2.issuance_id = ownership_snapshots.issuance_id
                 ORDER BY created_at DESC LIMIT 1);"
  jn listings \
    "SELECT asset_id AS old_asset_id, issuance_id, issuer_aid, ticker, name,
            authorized, venues FROM listings WHERE authorized = 1;"
} | jq -s 'add + {exported_at: (now | todate)}'
