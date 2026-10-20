#!/usr/bin/env bash
# The most there will ever be of a token, proven through the issuance page on a
# private elementsregtest chain: a real Sequentia node, a real openampd (the
# policy server, which holds every reissuance token in a supply cap) and the
# real seqpald serving the built page, driven in headless Chromium by
# scripts/e2e/supply-cap-page.mjs.
#
#   SEQUENTIAD=/path/to/sequentiad SEQUENTIA_CLI=/path/to/sequentia-cli \
#   OPENAMPD=/path/to/openampd TOKEN_COVENANTS=/path/to/token-covenants \
#   CHROME=/path/to/chrome GO=/path/to/go scripts/regtest-supply-cap.sh [evidence-dir]
#
# openampd comes from the openamp repository (go build -o openampd/openampd
# ./openampd/cmd/openampd) and token-covenants from sequentia-contracts. The
# chain is always elementsregtest in a fresh data directory (WORKDIR, a new
# temporary directory unless set), never the public testnet. Every process it
# starts is stopped by its own pid on the way out, and the directory it created
# is removed unless KEEP=1.
set -euo pipefail

here="$(cd "$(dirname "$0")/.." && pwd)"
SEQUENTIAD="${SEQUENTIAD:-sequentiad}"
SEQUENTIA_CLI="${SEQUENTIA_CLI:-sequentia-cli}"
GO="${GO:-go}"
: "${OPENAMPD:?OPENAMPD names the openampd binary}"
: "${TOKEN_COVENANTS:?TOKEN_COVENANTS names the token-covenants binary}"
: "${CHROME:?CHROME names a Chromium}"
RPCPORT="${RPCPORT:-28561}"
AMPPORT="${AMPPORT:-28570}"
PALPORT="${PALPORT:-28571}"
EVIDENCE="${1:-}"
created=0
if [ -z "${WORKDIR:-}" ]; then
	WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/seqpal-supply-cap.XXXXXX")"
	created=1
fi
mkdir -p "$WORKDIR/node"
NODE="$WORKDIR/node"
pids=()

cat >"$NODE/elements.conf" <<EOF
chain=elementsregtest
[elementsregtest]
rpcuser=seqpal
rpcpassword=regtest-only
rpcport=$RPCPORT
port=$((RPCPORT + 1))
listen=0
connect=0
server=1
txindex=1
par=1
fallbackfee=0.0001
evbparams=simplicity:-1:::
con_default_blinded_addresses=0
blindedaddresses=0
con_any_asset_fees=1
validatepegin=0
initialfreecoins=2100000000000000
anyonecanspendaremine=1
con_parent_chain_signblockscript=51
maxtxfee=100.0
EOF

cli() { "$SEQUENTIA_CLI" -datadir="$NODE" -chain=elementsregtest "$@"; }

cleanup() {
	for pid in "${pids[@]}"; do
		kill "$pid" 2>/dev/null || true
	done
	for pid in "${pids[@]}"; do
		for _ in $(seq 1 30); do
			kill -0 "$pid" 2>/dev/null || break
			sleep 0.2
		done
	done
	if [ -f "$NODE/sequentiad.pid" ]; then
		pid="$(cat "$NODE/sequentiad.pid")"
		cli stop >/dev/null 2>&1 || true
		for _ in $(seq 1 60); do
			kill -0 "$pid" 2>/dev/null || break
			sleep 1
		done
	fi
	if [ "$created" = "1" ] && [ "${KEEP:-0}" != "1" ]; then
		rm -rf -- "$WORKDIR"
	fi
}
trap cleanup EXIT

echo "=== node: $SEQUENTIAD on elementsregtest, rpc $RPCPORT, data $NODE ==="
"$SEQUENTIAD" -datadir="$NODE" -chain=elementsregtest -daemon -pid="$NODE/sequentiad.pid" >/dev/null
for _ in $(seq 1 60); do
	cli getblockcount >/dev/null 2>&1 && break
	sleep 1
done

echo "=== wallets and coins ==="
for w in miner openamp; do
	cli createwallet "$w" >/dev/null
done
cli -rpcwallet=miner rescanblockchain >/dev/null
cli -rpcwallet=miner -generate 101 >/dev/null
USDX="$(cli -rpcwallet=miner -named issueasset assetamount=1000000 tokenamount=0 blind=false fee_asset=bitcoin |
	sed -n 's/.*"asset": "\([0-9a-f]*\)".*/\1/p')"
cli -rpcwallet=miner -generate 1 >/dev/null
POLICY="$(cli dumpassetlabels | sed -n 's/.*"bitcoin": "\([0-9a-f]*\)".*/\1/p')"
cli setfeeexchangerates "{\"$POLICY\":100000000,\"$USDX\":100000000}" >/dev/null
# openampd's wallet holds both accepted assets; each fee goes to whichever coin
# covers the most fees, as every wallet chooses.
for i in 1 2 3; do
	addr="$(cli -rpcwallet=openamp getnewaddress "" bech32)"
	cli -rpcwallet=miner -named sendtoaddress address="$addr" amount=1000 assetlabel="$USDX" fee_asset_label="$USDX" >/dev/null
	cli -rpcwallet=miner -named sendtoaddress address="$addr" amount=100 fee_asset_label=bitcoin >/dev/null
done
cli -rpcwallet=miner -generate 1 >/dev/null
echo "USDX $USDX, policy asset $POLICY, tip $(cli getblockcount)"

echo "=== openampd ==="
COLD="$("$TOKEN_COVENANTS" key --new --key-file "$WORKDIR/cold.key" | sed -n 's/.*"key": *"\([0-9a-f]*\)".*/\1/p')"
"$OPENAMPD" -listen "127.0.0.1:$AMPPORT" -datadir "$WORKDIR/openampd" \
	-rpc "http://127.0.0.1:$RPCPORT" -rpcauth seqpal:regtest-only -rpcwallet openamp \
	-issuertoken regtest-issuer-token -demoissuer -feesats 3000 -follow 500ms \
	-tokentool "$TOKEN_COVENANTS" -capcoldkey "$COLD" >"$WORKDIR/openampd.log" 2>&1 &
pids+=("$!")
for _ in $(seq 1 100); do
	curl -sf "http://127.0.0.1:$AMPPORT/v1/assets" >/dev/null && break
	sleep 0.1
done
grep listening "$WORKDIR/openampd.log"

echo "=== seqpald and the page ==="
(cd "$here/seqpald" && "$GO" build -o "$WORKDIR/seqpald" .)
(cd "$here" && npm run build >/dev/null)
SEQPALD_LISTEN="127.0.0.1:$PALPORT" SEQPALD_DB="$WORKDIR/seqpald.db" \
	OPENAMPD_URL="http://127.0.0.1:$AMPPORT" OPENAMPD_ISSUER_TOKEN=regtest-issuer-token \
	SEQPALD_NETWORK=elementsregtest SEQPALD_NODE_URL="http://127.0.0.1:$RPCPORT" \
	SEQPALD_NODE_USER=seqpal SEQPALD_NODE_PASS=regtest-only SEQPALD_USDX_ASSET="$USDX" \
	SEQPALD_ELECTRS_URL= SEQPALD_REGISTRY_URL= SEQPALD_PRICE_URL= SEQPALD_SETUP_FEE_USD=0 \
	SEQPALD_KYC_FEE_USD=0 SEQPALD_KYB_FEE_USD=0 SEQPALD_BLOCKS_PER_DAY=2 \
	SEQPALD_WEBROOT="$here/dist" "$WORKDIR/seqpald" >"$WORKDIR/seqpald.log" 2>&1 &
pids+=("$!")
for _ in $(seq 1 100); do
	curl -sf "http://127.0.0.1:$PALPORT/api/health" >/dev/null && break
	sleep 0.1
done

echo "=== the proof, in headless Chromium ==="
status=0
BASE="http://127.0.0.1:$PALPORT" CHROME="$CHROME" EVIDENCE="$EVIDENCE" \
	NODE_URL="http://127.0.0.1:$RPCPORT" NODE_USER=seqpal NODE_PASS=regtest-only \
	OPENAMPD_URL="http://127.0.0.1:$AMPPORT" \
	node "$here/scripts/e2e/supply-cap-page.mjs" || status=$?
if [ -n "$EVIDENCE" ]; then
	grep -h "fee:" "$WORKDIR/openampd.log" >"$EVIDENCE/openampd-fees.log" || true
	cp "$WORKDIR/seqpald.log" "$EVIDENCE/seqpald.log"
fi
exit "$status"
