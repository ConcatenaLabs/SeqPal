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
# Set to 1 only on a confidentiality-enabled node. Sequentia is transparent by
# default and the public testnet node runs transparent, so leave this unset.
SEQPALD_CONFIDENTIAL=
# OpenDAMP capability flag (M10). Leave unset: a deploy electing
# enforcement=network is refused 501 (the election is still recorded) until an
# OpenDAMP deployment exists and this is set to 1. /api/health reports it as
# "damp".
SEQPALD_DAMP=
```

Bearer (supervised) issuance and the W-3 corporate-action snapshots also depend
on two variables that already exist for other features: `SEQPALD_NODE_URL` /
`SEQPALD_NODE_USER` / `SEQPALD_NODE_PASS` (the node RPC now also carries the raw
issuance flow and the supervision RPCs, and the seqpal-escrow wallet funds and
receives bearer mints) and `SEQPALD_ELECTRS_URL` (the snapshot walker reads the
asset index at `/asset/<id>/txs/chain` with `/tx/<txid>/outspend/<vout>`).

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

Then in the browser: create a SeqPal ID (export the encrypted key when prompted),
run an issuance through onboarding, and deploy. The asset id and txid returned are
real and resolve against `GET /openamp/v1/assets/{id}`.

## 6. Backup and restore (the DB is books and records)

The SQLite DB is the platform's only copy of accounts, issuances, deploys, and the
hash-chained audit log. Back it up with the online backup API, never by copying a
WAL-mode file in place:

```
install -d -o seqpal -g seqpal -m 0700 /var/backups/seqpald
sudo -u seqpal sqlite3 /var/lib/seqpald/seqpald.db \
  ".backup '/var/backups/seqpald/seqpald-$(date -u +%Y%m%dT%H%M%SZ).db'"
```

Restore:

```
systemctl stop seqpald
sudo -u seqpal cp /var/backups/seqpald/<chosen>.db /var/lib/seqpald/seqpald.db
systemctl start seqpald
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
