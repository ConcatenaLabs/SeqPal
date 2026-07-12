# M1 contract: seqpald API, data model, and security posture

This is the interface every M1 work item builds against (backend, SPA, tests, ops).
It is normative for M1 and is the base later milestones extend. Plan: `../OVERHAUL.md`
section 5, milestone M1. Wallet and venue integration rules that bind the SPA's key
handling: `openamp` repo, `spec/venue-wallet-integration.md`.

## 0. What M1 changes, in one line

No financial fact is asserted by the browser anymore: seqpald grows a persistent store,
real challenge-response authentication, per-account ownership, an append-only audit log,
and a hardened deployment; the browser keeps only an encrypted key, a session, and UI state.

## 1. Principals and authentication

**What a SeqPal ID is.** SeqPal ID is the KYC and AML identity layer for the whole
OpenAMP-on-Sequentia world. It is not an issuer login. There is ONE registration flow,
identical for everybody, and it never asks whether you are an issuer or an investor: no
user-type selector, no fork in the flow, role-neutral copy throughout. What registration
produces is a verified identity bound to an enclave key (the AID); the platform later stamps
eligibility categories onto that AID (M2), and those categories are what gate everything
downstream. The same single credential then opens different doors depending on what the
person does next: an issuer signs into the issuance platform with it, and an investor needs
it to be permitted to hold or trade SeqPal-managed restricted assets at all, including on a
Sequentia DEX that lists SeqPal assets (a venue cannot grant eligibility; it can only check
what SeqPal stamped). "Issuer" and "investor" are things a verified identity does later,
never a type chosen at signup. A corporate (KYB) entity is an addition to an existing
personal SeqPal ID, never an alternative signup path.

Technically, a SeqPal ID is a Sequentia identity: one secp256k1 enclave keypair generated in
the browser, whose x-only public key is registered with OpenAMP and whose AID
(`sha256("openamp-aid-v1" || sorted x-only hex)[:20]`) is the account id.

Authentication is proof of possession of that key, never a password:

1. `POST /api/auth/challenge {xonly}` returns a random 32-byte `challenge` (hex) with a
   120-second TTL, single use.
2. The browser signs it **tagged**, never raw:
   `sig = BIP340_sign(tagged_hash("openamp-challenge-v1", utf8(challenge)))`
   where `tagged_hash(tag, m) = sha256(sha256(tag) || sha256(tag) || m)`.
   Raw-signing an externally supplied digest with the enclave key is forbidden platform-wide:
   it turns the key into a signing oracle over transfer sighashes (integration spec 0.4).
3. `POST /api/auth/register` (first time) or `POST /api/auth/login` (returning) presents
   `{xonly, challenge, sig}`. seqpald verifies the tagged signature against `xonly`,
   recomputes the AID server-side, and opens a session.

Sessions: opaque 32-byte token in an `HttpOnly; SameSite=Strict; Secure; Path=/seqpal`
cookie named `seqpal_session`, 14-day expiry, server-side row, revoked on logout.
Every non-public endpoint requires it. Ownership is always derived from the session's AID,
never from a body field.

## 2. Endpoints (base `/seqpal/api`)

Public:

| Method | Path | Body | Returns |
|---|---|---|---|
| GET | `/health` | | `{ok, network, confidential, openamp_ok, issuer_token_ok}` |
| POST | `/auth/challenge` | `{xonly}` | `{challenge, expires_at}` |
| POST | `/auth/register` | `{xonly, challenge, sig, kind:"individual", display_name, residence, ...}` | `{account}` + session cookie |
| POST | `/auth/login` | `{xonly, challenge, sig}` | `{account}` + session cookie |

Session-required:

| Method | Path | Body | Returns | Notes |
|---|---|---|---|---|
| POST | `/auth/logout` | | `{ok:true}` | revokes the session row |
| GET | `/me` | | `{account, entities, issuances}` | the signed-in principal's whole world |
| POST | `/entities` | `{name, jurisdiction, structure_note, ...}` | `{entity}` | corporate (KYB) record; per-entity enclave keys land in M2 |
| GET | `/issuances` | | `{issuances}` | scoped to `owner_aid` = session AID |
| POST | `/issuances` | `{name, ticker, structure_id, entity_id?, terms}` | `{issuance}` | status starts `draft` |
| PATCH | `/issuances/{id}` | partial | `{issuance}` | 403 unless owner |
| POST | `/deploy` | `{issuance_id, supply, precision, clawback, confidential, fee_convert_atoms, terms}` | `{asset, txid, contract_hash, aid, address}` | the real mint |

`GET /health` performs an authenticated upstream probe: it calls OpenAMP with the issuer
token and reports `issuer_token_ok` truthfully, so the UI can say "deployment unavailable"
before a user reaches checkout instead of failing at the mint.

### Deploy rules (item 20, 28, 41, 115)

- Session required; the issuance must belong to the session's AID; 403 otherwise.
- Idempotency key = `sha256(issuer_pubkey || terms_hash)`. A replay returns the SAME
  `{asset, txid, ...}` from the `deploys` table and never mints twice.
- Rate limit: 5 deploys per account per hour, 20 per hour globally (in-memory token bucket
  is sufficient for M1; the audit log records refusals).
- Validation, 400 on failure: `supply >= 1`; `1 <= precision <= 8`; ticker matches
  `^[A-Z0-9]{2,8}$`, is not in the reserved list (`SEQ`, `TSEQ`, `BTC`, `TBTC`, `USDX`,
  `EURX`, `GOLD`, `SILVR`, `OILX`), and is not already used by a live asset
  (checked against `GET /openamp/v1/assets`; the residual race inside openampd is
  disclosed, not closed, per decision 8).
- `terms_hash` is computed SERVER-side over the canonical terms object (the browser's value
  is only a cross-check; a mismatch is a 400). Canonicalization: JSON with object keys
  sorted lexicographically, no whitespace, UTF-8, then sha256, hex.
- Confidential requests still 501 when the node is not CT-capable (unchanged).

## 3. Data model (SQLite, `modernc.org/sqlite`, pure Go, no CGO)

DB path from `SEQPALD_DB` (default `./seqpald.db`; the box uses `/var/lib/seqpald/seqpald.db`).
`PRAGMA journal_mode=WAL; foreign_keys=ON`. Schema versioned in a `schema_version` table.

```
accounts(aid TEXT PK, kind TEXT, xonly TEXT UNIQUE NOT NULL, display_name TEXT,
         id_number TEXT UNIQUE, profile JSON, created_at INTEGER)
entities(id TEXT PK, owner_aid TEXT REFERENCES accounts(aid), name TEXT,
         jurisdiction TEXT, profile JSON, created_at INTEGER)
sessions(token TEXT PK, aid TEXT REFERENCES accounts(aid), created_at INTEGER,
         expires_at INTEGER)
challenges(challenge TEXT PK, xonly TEXT, created_at INTEGER, expires_at INTEGER,
           used INTEGER DEFAULT 0)
issuances(id TEXT PK, owner_aid TEXT REFERENCES accounts(aid), entity_id TEXT,
          name TEXT, ticker TEXT, structure_id TEXT, status TEXT, terms JSON,
          supply INTEGER, precision INTEGER, confidential INTEGER, clawback INTEGER,
          asset_id TEXT, txid TEXT, contract_hash TEXT, holder_aid TEXT,
          enclave_address TEXT, created_at INTEGER, updated_at INTEGER)
deploys(idem_key TEXT PK, issuance_id TEXT, asset_id TEXT, txid TEXT,
        contract_hash TEXT, aid TEXT, address TEXT, created_at INTEGER)
audit_log(seq INTEGER PK AUTOINCREMENT, ts INTEGER, actor_aid TEXT, action TEXT,
          detail JSON, prev_hash TEXT, hash TEXT)
```

`audit_log` is append-only and hash-chained: `hash = sha256(prev_hash || ts || actor_aid ||
action || canonical(detail))`. Every privileged call (register, login, logout, entity create,
issuance create/update, deploy attempt, deploy success, deploy refusal, rate-limit refusal)
appends one row. Nothing ever updates or deletes a row. (M7 anchors the head on-chain.)

## 4. Browser-side rules

- The enclave private key is stored **encrypted**: AES-GCM (WebCrypto) under a key derived
  by PBKDF2-SHA256 (200k iterations, 16-byte salt) from a user passphrase. localStorage holds
  `{v, salt, iv, ct, xonly, aid}` and never the plaintext key.
- **Export is mandatory at creation**: the user cannot finish registration without downloading
  or copying the encrypted backup file (`seqpal-id-<aid>.json`, the same envelope). Import on a
  new device restores the ID; the same passphrase unlocks it.
- The decrypted key lives in memory for the session only, used to sign login challenges.
- **Reset guard** (item 3): "Reset demo" refuses whenever any AID in this browser holds a
  nonzero OpenAMP balance or owns a live issuance, unless the user has exported first and
  types the confirmation; it explains that clearing browser data destroys the only key to a
  2-of-2 enclave, and that already-minted assets persist on-chain.
- Session state (`seqpal_session` cookie) is set by the server; the SPA never reads it.
- localStorage may hold ONLY: the encrypted key envelope, UI preferences, and a cache marker.
  No issuances, no subscriptions, no balances, no personas roster.

## 5. Security posture (items 40, 112, 116, 117, 118)

- **CORS**: same-origin only in production. A dev allowlist (`SEQPALD_DEV_ORIGINS`) exists for
  local work; the wildcard is gone.
- **CSP and headers**, set by seqpald on every response:
  `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'`
  plus `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`,
  `X-Frame-Options: DENY`, `Permissions-Policy: geolocation=(), camera=(), microphone=()`.
  (`connect-src 'self'` works because OpenAMP is same-origin behind Caddy in production. Dev
  uses the vite proxy, so it is same-origin there too.)
- **Fonts are self-hosted** (the Google Fonts CDN link is removed; it leaked visitor IPs and
  would break the CSP).
- **seqpald runs as a non-root user** (`seqpal`), with `NoNewPrivileges`, `ProtectSystem=strict`,
  `ProtectHome`, `PrivateTmp`, `RestrictAddressFamilies=AF_INET AF_UNIX`, and write access only
  to its state dir. The SPA is served from `/srv/seqpal/dist`, NOT from `/root` (that is why the
  old unit needed root at all).
- **Dev never points at production by default**: the vite dev server proxies `/seqpal/api` and
  `/openamp` to localhost. Aiming dev at the live box requires setting `VITE_SEQPAL_API` /
  `VITE_OPENAMP_API` explicitly.

## 6. Test obligations (item 120)

- **AID golden vector**: a fixed x-only pubkey and its AID, generated from the Go
  `store.AID` implementation in the openamp repo, asserted by both the JS `computeAID` and the
  Go server. This is the one crypto seam between our stack and the policy server.
- **Tagged-challenge vector**: fixed challenge string, key, and expected signature; the JS signer
  and the Go verifier must agree.
- **terms_hash canonicalization**: fixed terms object, expected hex; key order and whitespace
  must not change the hash (the SPA and seqpald must produce the same value).
- **Go httptest suite**: challenge/register/login/session lifecycle; ownership 403s; deploy
  validation matrix; deploy idempotency (same key returns the same asset, one mint);
  rate-limit refusal; audit-chain integrity after a sequence of calls.
- **Live probes** (`scripts/live-probe.sh`), run before and after every box deploy:
  `/seqpal/api/health`, `/prices`, `/openamp/v1/assets`.

## 7. Out of scope for M1 (do not build here)

Categories and eligibility rules (M2), escrow and payments (M5), settlement (M6), the wallet
sign card (M5, and it is the SWK/wallet agent's deliverable per the integration spec),
distributions (M7). M1 only makes the platform real, authenticated, persistent, and honest.
