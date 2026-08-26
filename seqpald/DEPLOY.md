# Deploying SeqPal to the Sequentia testnet box

`seqpald` serves both the built SPA and the API; Caddy reverse-proxies `/seqpal/*`
to it. It depends on a running OpenAMP policy server (`openampd`) on the same box.

Topology (nothing lives under `/root` any more, which is why the old unit needed
root at all):

| What | Path | Owner | Mode |
|---|---|---|---|
| binary | `/usr/local/bin/seqpald` | `root:root` | `0755` |
| SPA webroot | `/srv/seqpal/dist` | `root:seqpal` | `0755` |
| SQLite DB | `/var/lib/seqpald/seqpald.db` | `seqpal:seqpal` | `0600` |
| secrets | `/etc/seqpald/seqpald.env` | `root:seqpal` | `0640` |
| git clone | `/root/sequentia/SeqPal` | `root:root` | build only |

The service account is a system user with no login and no home:
`useradd --system --no-create-home --shell /usr/sbin/nologin seqpal`.
The daemon needs write access to `/var/lib/seqpald` only; the unit's
`ProtectSystem=strict` makes the rest of the filesystem read-only to it, and
`ProtectHome=true` hides `/root` entirely.

## 1. One-time box setup

```
useradd --system --no-create-home --shell /usr/sbin/nologin seqpal
install -d -o root -g seqpal -m 0755 /srv/seqpal
install -d -o seqpal -g seqpal -m 0700 /var/lib/seqpald
install -d -o root -g seqpal -m 0750 /etc/seqpald
```

Then write `/etc/seqpald/seqpald.env` (`chown root:seqpal`, `chmod 0640`, never
in git). The issuer token is the same one `openampd` was started with; copy it
from `/root/sequentia/openampd.env` on the box.

```
OPENAMPD_URL=http://127.0.0.1:8722
OPENAMPD_ISSUER_TOKEN=<paste from openampd.env; never commit this>
SEQPALD_LISTEN=127.0.0.1:8730
SEQPALD_NETWORK=sequentia-testnet
SEQPALD_WEBROOT=/srv/seqpal/dist
SEQPALD_DB=/var/lib/seqpald/seqpald.db
# Set to 1 to let holders elect per-transfer confidentiality (confidential:true
# on POST /api/transfers). Needs no node flag: blinding is opt-in per
# transaction on every Sequentia node. Unset, a confidential transfer is
# refused 501; deploys are transparent mints either way.
SEQPALD_CONFIDENTIAL=
# OpenDAMP capability flag (M10/M12). Unset, a deploy electing
# enforcement=network is refused 501 and the election is still recorded.
# /api/health reports it as "damp". Set it to 1 ONLY when the policy server is
# itself started with -dampregistry <cmr pinning file>; without that, every
# network deploy fails at the policy server instead, one round trip later.
#
# A network deploy is two-phase by nature and cannot be made one-phase: the
# on-chain programs are compiled by the issuer's registrar with `opendamp
# derive`, against values that only exist once the policy server has prepared
# the issuance. The first POST /api/deploy prepares and answers 409 with the
# document to run; the second carries the registrar's three values back and
# mints. Preparation mints a small internal verifier asset on the issuer's
# behalf; an abandoned preparation leaves that asset unused, which is harmless.
SEQPALD_DAMP=
```

Bearer (supervised) issuance and the W-3 corporate-action snapshots also depend
on two variables that already exist for other features: `SEQPALD_NODE_URL` /
`SEQPALD_NODE_USER` / `SEQPALD_NODE_PASS` (the node RPC now also carries the raw
issuance flow and the supervision RPCs, and the seqpal-escrow wallet funds and
receives bearer mints) and `SEQPALD_ELECTRS_URL` (the snapshot walker reads the
asset index at `/asset/<id>/txs/chain` with `/tx/<txid>/outspend/<vout>`).

### Full configuration

Everything seqpald reads, from `seqpald -h` and `main.go`. Each flag has an
environment variable; the environment file is how the box sets them. The
native-BTC escrow rail is off unless `SEQPALD_BTC_RPC_URL` is set.

| Variable (flag) | Default | Meaning |
|---|---|---|
| `SEQPALD_LISTEN` (`-listen`) | `127.0.0.1:8730` | HTTP listen address |
| `SEQPALD_DB` (`-db`) | `./seqpald.db` | SQLite database path (books and records) |
| `OPENAMPD_URL` (`-openamp`) | `http://127.0.0.1:8722` | OpenAMP policy server base URL |
| `OPENAMPD_ISSUER_TOKEN` (`-issuertoken`) | empty | OpenAMP issuer bearer token; the one secret |
| `SEQPALD_NETWORK` (`-network`) | `sequentia-testnet` | network label reported to the UI |
| `SEQPALD_CONFIDENTIAL` (`-confidential`) | unset | `1`/`true`: accept per-transfer confidential transfers |
| `SEQPALD_WEBROOT` (`-webroot`) | empty | built SPA directory served at `/` (empty = API only) |
| `SEQPALD_DEV_ORIGINS` (`-devorigins`) | empty | comma-separated extra CORS origins for local development |
| `SEQPALD_ADMIN_AIDS` (`-adminaids`) | empty | comma-separated AIDs allowed to use the admin surface (re-delivery) |
| `SEQPALD_IDV_PROVIDER` | `simulated` | which identity-verification provider adjudicates. Only `simulated` ships; a real one is an adapter satisfying the same interface |
| `SEQPALD_IDV_CALLBACK_SECRET` | minted per process | what authenticates the provider's callback, which is the thing that decides who is verified. Required for any provider but the simulator, which is handed the per-process one and is the only caller that could know it |
| `SEQPALD_IDV_DECISION_SECS` | `3` | how long the simulated provider takes to answer. A real one takes as long as it takes |
| `SEQPALD_BLOCKS_PER_DAY` (`-blocksperday`) | `1440` | assumed Sequentia blocks per day for lockup height conversion (60-second spacing: 1440). Below 1 is refused: a lockup measured in zero blocks expires the moment it is stamped |
| `SEQPALD_TIP_HEIGHT` (`-tipheight`) | `0` | fallback tip height when no node RPC is configured |
| `SEQPALD_NODE_URL` (`-nodeurl`) | empty | Sequentia node JSON-RPC URL: chain watcher, tip height, supervision RPCs, bearer mints |
| `SEQPALD_NODE_USER` (`-nodeuser`) | empty | node RPC username |
| `SEQPALD_NODE_PASS` (`-nodepass`) | empty | node RPC password |
| `SEQPALD_ELECTRS_URL` (`-electrsurl`) | `http://127.0.0.1:3003` | box explorer (electrs) base URL; confirmation source when the node lacks `-txindex` |
| `SEQPALD_REGISTRY_URL` (`-registryurl`) | `http://127.0.0.1:3005` | asset registry base URL for publication (empty = disabled) |
| `SEQPALD_PRICE_URL` (`-priceurl`) | `http://127.0.0.1:8088` | box price feed base URL (`GET /prices`, `POST /seed`; empty = disabled) |
| `SEQPALD_ENTITY_DOMAIN` (`-entitydomain`) | `sequentiatestnet.com` | entity domain committed in the contract for the registry proof |
| `SEQPALD_ENTITY_NAME` (`-entityname`) | empty | optional issuer display name added to the contract |
| `SEQPALD_OPERATOR_NAME` (`-operatorname`) | empty | optional operator identity added to the contract |
| `SEQPALD_OPERATOR_REGISTRATION` (`-operatorreg`) | empty | optional operator registration added to the contract |
| `SEQPALD_POLICY_FEE_SATS` (`-policyfeesats`) | `1000` | network fee reference in rfa (1e-8 reference fee units) used to derive `fee_convert_atoms` |
| `SEQPALD_BTC_RPC_URL` (`-btcurl`) | empty | Bitcoin testnet4 node RPC URL for the native-tBTC escrow (empty = BTC rail disabled) |
| `SEQPALD_BTC_RPC_USER` (`-btcuser`) | empty | testnet4 RPC username (the node's `mainchainrpcuser`) |
| `SEQPALD_BTC_RPC_PASS` (`-btcpass`) | empty | testnet4 RPC password (the node's `mainchainrpcpassword`) |
| `SEQPALD_USDX_ASSET` (`-usdxasset`) | the public USDX id `2a5155…b9de` | USDX asset id (fee and escrow payment asset) |
| `SEQPALD_ESCROW_CONFS` (`-escrowconfs`) | `1` | confirmations before a deposit becomes `in_escrow`. Below 1 is refused: nothing here is final at 0-conf, and a deposit that has not confirmed has not arrived |
| `SEQPALD_ATOMIC_CLOSE` (`-atomicclose`) | `1` | settle USDX subscriptions as one atomic delivery-versus-payment transaction; falls back to the two-transaction close when the policy server has no payment leg |
| `SEQPALD_DAMP` | unset | `1`/`true`: network-enforced (OpenDAMP) deploys allowed; see above |
| `SEQPALD_SETUP_FEE_USD` | `500` | platform setup fee invoiced in USDX |
| `SEQPALD_ESCROW_FEE_BPS` | `50` | escrow and settlement fee, basis points of the released payment |
| `SEQPALD_KYC_FEE_USD` | `20` | identity verification fee, collected before the check is submitted to the provider. The default is the published price in `src/data/pricing.js`; zero charges nothing |
| `SEQPALD_KYB_FEE_USD` | `150` | business verification fee, charged per business for the same reason. Also the published price; zero charges nothing |
| `SEQPALD_IDV_GRACE_SECS` | `30` | how long a check may be outstanding before the provider is asked about it directly |
| `SEQPALD_IDV_RECONCILE_SECS` | `15` | how often outstanding checks are chased. A decision delivered over a network can be missed; a check nobody chases leaves its holder stuck at `submitted` |
| `SEQPALD_FEE_ASSET` | empty | asset the escrow wallet's and bearer mint's network fees are paid in; empty = the node's `bitcoin` label (tSEQ) |
| `SEQPALD_RULES_RECONCILE_SECS` | `30` | rules-mutation reconcile cadence. A value below 1 second falls back to the default: a ticker cannot run a non-positive cadence |
| `SEQPALD_SNAPSHOT_SECS` | `86400` | register snapshot cadence. A value below 1 second falls back to the default: a ticker cannot run a non-positive cadence |
| `SEQPALD_REPORT_SECS` | `31536000` | annual report cadence. A value below 1 second falls back to the default: a ticker cannot run a non-positive cadence |
| `SEQPALD_WALLET_POLL_SECS` | `15` | escrow wallet poll cadence. A value below 1 second falls back to the default: a ticker cannot run a non-positive cadence |

## 1b. Checks before a deploy

    cd seqpald && go vet ./... && go test ./...
    go run honnef.co/go/tools/cmd/staticcheck@latest ./...

`staticcheck` is clean, so anything it reports is something this change
introduced. It is what found a signing tag left behind by a removed feature,
which is the kind of dead code that reads as security-relevant to the next
person.

## 2. Pre-deploy probe

Always, from the laptop or the box:

```
scripts/live-probe.sh          # /seqpal/api/health, /prices, /openamp/v1/assets
```

A failure here is a pre-existing outage: fix it before deploying, so a post-deploy
failure is unambiguously yours.

## 3. Build and install (on the box)

Source comes from git only; nothing is scp'd.

```
cd /root/sequentia/SeqPal && git pull

npm ci && npm run build                       # -> dist/ (base /seqpal/)
rsync -a --delete dist/ /srv/seqpal/dist/
chown -R root:seqpal /srv/seqpal/dist && chmod -R a+rX /srv/seqpal/dist

# seqpald is NOT vendored (the pure-Go modernc.org/sqlite dependency is ~139MB, too
# heavy for git history), so the box builds from the module cache. The first build
# fetches modules; ensure GOFLAGS does not force -mod=vendor for this build.
cd seqpald && GOFLAGS= go build -o /tmp/seqpald . && install -o root -g root -m 0755 /tmp/seqpald /usr/local/bin/seqpald

install -m 0644 seqpald.service /etc/systemd/system/seqpald.service
systemctl daemon-reload
systemctl enable --now seqpald
systemctl status seqpald
```

seqpald applies its schema migrations on start; a fresh box creates the DB on the
first run.

## 4. Caddy (unchanged)

One block: strip the `/seqpal` prefix and proxy everything to seqpald, which
serves the SPA and `/api/*`.

```
handle /seqpal/* {
    uri strip_prefix /seqpal
    reverse_proxy 127.0.0.1:8730
}
```

`systemctl reload caddy`. seqpald sets the CSP and the security headers itself, so
Caddy adds none.

## 5. Post-deploy probe and check

```
scripts/live-probe.sh
curl -s -I https://sequentiatestnet.com/seqpal/            # 200 + CSP header
curl -s -X POST https://sequentiatestnet.com/seqpal/api/deploy   # 401, never a mint
```

`/seqpal/api/health` reports `issuer_token_ok`: it probes OpenAMP with the real
token, so a false value means deployment is broken even though the daemon is up.

Check the sanctions lists loaded, because nothing verifies until they have and
an instance that quietly fell back to the bundled fixture finds no match against
anybody:

```
journalctl -u seqpald --since -5min | grep screening
# one "loaded from cache" or "refreshed" line per list, each with a name count
# in the thousands. A "parsed to no names" line means the parser no longer
# matches that source's format.
```

Then in the browser: sign in with a Sequentia wallet (SeqPal holds no key and
makes none), run an issuance through onboarding, and deploy. The asset id and
txid returned are real and resolve against `GET /openamp/v1/assets/{id}`.

Identity verification is a provider's, and the demo ships a simulated one. Check
that its decisions are arriving:

```
journalctl -u seqpald --since -5min | grep -iE "idv|verify"
# a submitted check, then the decision landing on the callback
```

A verification that stays "submitted" means the callback never arrived. The
simulator calls back over loopback to this process's own listener, so the usual
cause is `SEQPALD_LISTEN` naming an address the process cannot reach itself on.

## 6. Backup and restore (the DB is books and records)

The SQLite DB is the platform's only copy of accounts, issuances, deploys, and the
hash-chained audit log. `deploy/seqpald-backup` copies it through SQLite's online
backup API -- never by copying a WAL-mode file in place, which gets a torn read
whenever seqpald writes during the copy -- then reads the copy back and prints
its schema version and row counts, because a backup nobody has opened is a hope
rather than a backup. Each copy is one self-contained file: the destination is
switched off WAL, so there is no `-wal` beside it holding writes a restore would
drop.

Install it, and the timer that runs it every six hours:

```
install -m 0755 seqpald/deploy/seqpald-backup /usr/local/bin/seqpald-backup
install -m 0644 seqpald/deploy/seqpald-backup.service /etc/systemd/system/
install -m 0644 seqpald/deploy/seqpald-backup.timer /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now seqpald-backup.timer
systemctl start seqpald-backup.service      # take one now
systemctl list-timers seqpald-backup.timer  # confirm the next run
```

It keeps the last 14 copies (`--keep`), in `/var/backups/seqpald` (`--dir`).
Run it by hand before any schema change too:

```
/usr/local/bin/seqpald-backup
```

Restore:

```
systemctl stop seqpald
cp /var/backups/seqpald/<chosen>.db /var/lib/seqpald/seqpald.db
systemctl start seqpald
curl -s localhost:8730/api/health
```

An asset already minted survives any DB loss (it is on-chain), but the record of
who owns it does not. Back up before every deploy that touches the schema.

Note the standing dependency: `~/.openampd` holds the issuer and enclave keys that
control every deployed asset. Its backup story is openampd's, not SeqPal's, and it
is at least as important as this one.

## Redeploy

```
scripts/live-probe.sh && cd /root/sequentia/SeqPal && git pull \
  && npm ci && npm run build && rsync -a --delete dist/ /srv/seqpal/dist/ \
  && (cd seqpald && go build -o /tmp/seqpald . && install -m 0755 /tmp/seqpald /usr/local/bin/seqpald) \
  && systemctl restart seqpald && scripts/live-probe.sh
```
