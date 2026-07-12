#!/bin/sh
# Live probes for the SeqPal stack on the Sequentia testnet box.
# Run BEFORE and AFTER every box deploy: before, to know the baseline; after, to
# know the deploy did not take a working surface down.
#
#   scripts/live-probe.sh                       # probes https://sequentiatestnet.com
#   scripts/live-probe.sh http://127.0.0.1:8730 # probes a local seqpald (paths differ, see below)
#
# Read-only: it never mints, never authenticates, never sends a token.
# Exits nonzero if any probe fails.
set -eu

HOST="${1:-https://sequentiatestnet.com}"
HOST="${HOST%/}"

fail=0

probe() {
	path="$1"
	expect="$2" # a string that must appear in the body
	url="$HOST$path"
	body=$(curl -fsS --max-time 15 "$url" 2>/dev/null) || {
		printf 'FAIL  %-28s %s (request failed)\n' "$path" "$url"
		fail=1
		return
	}
	case "$body" in
	*"$expect"*)
		printf 'ok    %-28s %s\n' "$path" "$(printf '%.100s' "$body")"
		;;
	*)
		printf 'FAIL  %-28s expected %s in body, got: %s\n' "$path" "$expect" "$(printf '%.160s' "$body")"
		fail=1
		;;
	esac
}

printf 'SeqPal live probes against %s\n\n' "$HOST"

# seqpald: reports network, upstream reachability, and whether the issuer token
# actually authenticates against OpenAMP (a truthful health check, not a bare ok).
probe /seqpal/api/health '"ok"'
# price server: feeds every reference-currency display in the stack.
probe /prices '"price"'
# OpenAMP policy server: the issuer seam that makes the mint real.
probe /openamp/v1/assets '"assets"'

printf '\n'
if [ "$fail" -ne 0 ]; then
	printf 'RESULT: FAIL (at least one probe failed)\n'
	exit 1
fi
printf 'RESULT: all probes ok\n'
