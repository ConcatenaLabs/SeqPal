#!/usr/bin/env bash
# Distributions and refunds paid to wallets, proven against a REAL payment
# service: an Arca operator, run by arca's operator harness on its own anchored
# elementsregtest chain, the REAL seqpald binary, and the `arca` command line
# as the issuer's wallet and every holder's (seqpald/regtest_payouts_test.go
# drives it; seqpald/PAYOUTS.md says what it proves).
#
# Start the harness first, in an arca checkout (see its bark-cli/tests/
# arca_operator_for_browsers.rs; it needs SEQUENTIAD_EXEC, ARCA_TEST_POSTGRES
# and arca-signer):
#
#   ARCA_OPERATOR_CONTROL=127.0.0.1:18640 cargo test -p arca-cli \
#     --test arca_operator_for_browsers -- --ignored --nocapture
#
# then, with the `arca` binary that checkout built:
#
#   CONTROL=http://127.0.0.1:18640 ARCA=/path/to/target/debug/arca \
#   GO=/path/to/go scripts/regtest-payouts.sh [evidence-file]
#
# With SHOTS (a directory) and CHROME (a Chromium) set, it also builds the
# page, has seqpald serve it, and screenshots the issuer's payout panels there.
#
# It builds seqpald into a fresh directory (WORKDIR, a new temporary directory
# unless set), runs the proof, and removes the directory unless KEEP=1. It does
# not stop the harness: POST /quit to its control address does.
set -euo pipefail

here="$(cd "$(dirname "$0")/.." && pwd)"
GO="${GO:-go}"
: "${CONTROL:?CONTROL names the operator harness control address}"
: "${ARCA:?ARCA names the arca binary}"
EVIDENCE="${1:-}"
created=0
if [ -z "${WORKDIR:-}" ]; then
	WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/seqpal-payouts.XXXXXX")"
	created=1
fi
cleanup() {
	if [ "$created" = "1" ] && [ "${KEEP:-0}" != "1" ]; then
		rm -rf -- "$WORKDIR"
	fi
}
trap cleanup EXIT

echo "=== seqpald binary ==="
(cd "$here/seqpald" && "$GO" build -o "$WORKDIR/seqpald" .)

WEBROOT=""
if [ -n "${SHOTS:-}" ] && [ -n "${CHROME:-}" ]; then
	echo "=== the issuer page ==="
	(cd "$here" && npm run build >/dev/null)
	WEBROOT="$here/dist"
	mkdir -p "$SHOTS"
fi

echo "=== the proof ==="
cd "$here/seqpald"
SEQPAL_PAYOUT_CONTROL="$CONTROL" SEQPAL_PAYOUT_ARCA="$ARCA" \
	SEQPAL_REGTEST_SEQPALD="$WORKDIR/seqpald" SEQPAL_REGTEST_WORKDIR="$WORKDIR" \
	SEQPAL_REGTEST_WEBROOT="$WEBROOT" SEQPAL_REGTEST_SHOTS="${SHOTS:-}" CHROME="${CHROME:-}" \
	SEQPAL_REGTEST_EVIDENCE="${EVIDENCE:+$EVIDENCE}" \
	"$GO" test -count=1 -timeout 60m -run '^TestRegtestPayouts$' -v .
