# Deploying SeqPal to the Sequentia testnet box

SeqPal has two deployable parts:

1. the static SPA (Vite build) served by Caddy at `https://<host>/seqpal/`;
2. `seqpald`, a tiny Go backend that holds the OpenAMP issuer token and proxies
   asset issuance, reachable at `/seqpal/api/*`.

It depends on a running OpenAMP policy server (`openampd`) on the same box.

## 1. Pull and build (on the box)

```
cd /root/sequentia/SeqPal
git pull
# front-end
npm ci
npm run build            # -> dist/ (base path /seqpal/)
# backend
cd seqpald && go build -o seqpald .
```

## 2. Backend secret (box-only, never in git)

`seqpald` needs the same issuer token OpenAMP was started with. Reuse it from
`/root/sequentia/openampd.env`. Write `/root/sequentia/seqpald.env` (chmod 600):

```
OPENAMPD_URL=http://127.0.0.1:8722
OPENAMPD_ISSUER_TOKEN=<same token as openampd.env>
SEQPALD_LISTEN=127.0.0.1:8730
SEQPALD_NETWORK=sequentia-testnet
# Set to 1 only on a confidentiality-enabled node (blindedaddresses=1). The
# public testnet node runs transparent, so leave this unset there.
SEQPALD_CONFIDENTIAL=
```

## 3. Install and start the service

```
cp seqpald/seqpald.service /etc/systemd/system/seqpald.service
systemctl daemon-reload
systemctl enable --now seqpald
systemctl status seqpald
```

## 4. Caddy

Serve the built SPA and proxy the API. The `/api` handle must come first:

```
handle_path /seqpal/api/* {
    reverse_proxy 127.0.0.1:8730
}
handle_path /seqpal/* {
    root * /root/sequentia/SeqPal/dist
    try_files {path} /index.html
    file_server
}
```

Then `systemctl reload caddy`.

## 5. Verify

```
curl -s https://<host>/seqpal/api/health          # {"ok":true,"confidential":false,...}
curl -s -I https://<host>/seqpal/                  # 200, serves the SPA
```

Then in the browser: create a SeqPal ID, run an issuance through onboarding, and
on the issuance page click **Deploy on Sequentia**. The asset id / txid returned
are real and resolve against `GET /openamp/v1/assets/{id}`.

## Redeploy

`git pull && npm ci && npm run build && (cd seqpald && go build -o seqpald .) && systemctl restart seqpald`.
The SPA is static, so a rebuilt `dist/` is picked up immediately; only backend
changes need the `seqpald` restart.
