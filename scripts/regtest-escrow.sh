#!/usr/bin/env bash
# The subscription escrow covenant and the one-transaction distribution, proven
# on a private elementsregtest chain against a real Sequentia node and the real
# seqpald binary (seqpald/regtest_escrow_test.go drives it over its HTTP API).
#
#   SEQUENTIAD=/path/to/sequentiad SEQUENTIA_CLI=/path/to/sequentia-cli \
#   GO=/path/to/go scripts/regtest-escrow.sh [evidence-file]
#
# It starts its own node in a fresh data directory (WORKDIR, a new temporary
# directory unless set), on RPC port RPCPORT (28461 unless set), never on the
# public testnet: the chain is always elementsregtest, with Simplicity on,
# transparent defaults, any-asset fees and -par=1 so a block refused for a
# script names the script error. It stops that node, by its own pid file, on
# the way out, and removes the directory it created unless KEEP=1 (a WORKDIR
# given to it is left in place).
set -euo pipefail

here="$(cd "$(dirname "$0")/.." && pwd)"
SEQUENTIAD="${SEQUENTIAD:-sequentiad}"
SEQUENTIA_CLI="${SEQUENTIA_CLI:-sequentia-cli}"
GO="${GO:-go}"
RPCPORT="${RPCPORT:-28461}"
EVIDENCE="${1:-}"
created=0
if [ -z "${WORKDIR:-}" ]; then
	WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/seqpal-regtest.XXXXXX")"
	created=1
fi
mkdir -p "$WORKDIR/node"
NODE="$WORKDIR/node"

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

stop_node() {
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
trap stop_node EXIT

echo "=== node: $SEQUENTIAD on elementsregtest, rpc $RPCPORT, data $NODE ==="
"$SEQUENTIAD" -datadir="$NODE" -chain=elementsregtest -daemon -pid="$NODE/sequentiad.pid" >/dev/null
for _ in $(seq 1 60); do
	cli getblockcount >/dev/null 2>&1 && break
	sleep 1
done

echo "=== wallets and coins ==="
for w in miner issuer subs holders platform seqpal-escrow; do
	cli createwallet "$w" >/dev/null
done
cli -rpcwallet=miner rescanblockchain >/dev/null
cli -rpcwallet=miner -generate 101 >/dev/null
USDX="$(cli -rpcwallet=miner -named issueasset assetamount=1000000 tokenamount=0 blind=false fee_asset=bitcoin |
	sed -n 's/.*"asset": "\([0-9a-f]*\)".*/\1/p')"
cli -rpcwallet=miner -generate 1 >/dev/null
POLICY="$(cli dumpassetlabels | sed -n 's/.*"bitcoin": "\([0-9a-f]*\)".*/\1/p')"
# The fee whitelist: the policy asset as the chain starts it, and USDX at 1:1, so
# every escrow spend and the payout can pay their fee in USDX itself.
cli setfeeexchangerates "{\"$POLICY\":100000000,\"$USDX\":100000000}" >/dev/null
for w in subs issuer; do
	addr="$(cli -rpcwallet="$w" getnewaddress "" bech32)"
	cli -rpcwallet=miner -named sendtoaddress address="$addr" amount=10000 assetlabel="$USDX" fee_asset_label="$USDX" >/dev/null
done
cli -rpcwallet=miner -generate 1 >/dev/null
echo "USDX $USDX, tip $(cli getblockcount)"

echo "=== seqpald binary ==="
(cd "$here/seqpald" && "$GO" build -o "$WORKDIR/seqpald" .)

echo "=== the proof ==="
cd "$here/seqpald"
SEQPAL_REGTEST_NODE_URL="http://127.0.0.1:$RPCPORT" \
	SEQPAL_REGTEST_NODE_USER=seqpal SEQPAL_REGTEST_NODE_PASS=regtest-only \
	SEQPAL_REGTEST_USDX="$USDX" SEQPAL_REGTEST_SEQPALD="$WORKDIR/seqpald" \
	SEQPAL_REGTEST_WORKDIR="$WORKDIR" SEQPAL_REGTEST_EVIDENCE="${EVIDENCE:+$EVIDENCE}" \
	"$GO" test -count=1 -run '^TestRegtestEscrowCovenant$' -v .
