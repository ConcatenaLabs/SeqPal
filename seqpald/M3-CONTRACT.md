# M3 contract: truthful chain surfaces

Builds on M1+M2. Plan: `../OVERHAUL.md` section 5 milestone M3, plus 2.C/2.D/2.F/2.H,
and the disposition rows for items 5, 6, 19, 27, 28, 49, 50, 56, 57, 73, 74, 75, 76, 84,
119, 121. Goal: no financial fact on any surface is fabricated or asserted; every balance,
holder, status, and identifier comes from the chain or the policy server, with confirmations
and Bitcoin anchor depth shown, and nothing is ever labeled final at 0-conf.

## 1. openampd change OA-1 (repo openamp; additive, backward-compatible)

`internal/server/issue.go` builds the issuance contract JSON. Add an `entity` block so the
asset can be published to the Sequentia asset registry (which requires `entity.domain`).
New request fields on `POST /v1/issuer/assets` (all optional): `entity_domain`,
`entity_name`, `operator_name`, `operator_registration`. When `entity_domain` is present,
add to the contract:
```
"entity": { "domain": <entity_domain>, "issuer": <entity_name?> },
"operator": { "name": <operator_name?>, "registration": <operator_registration?> }
```
Keep canonical JSON (sorted keys, no whitespace); the contract_hash still commits to the
whole document. Assets minted without these fields are unchanged (BONDX unaffected). A Go
test asserts the contract with entity.domain hashes deterministically and round-trips.

## 2. seqpald: the chain watcher + SSE

A Sequentia chain watcher (via the node RPC already configured, `SEQPALD_NODE_URL`):
- Tracks every issued asset's `issue_txid` and each live issuance's escrow/holder state:
  broadcast -> N confirmations -> anchored (the Bitcoin anchor depth of the confirming
  Sequentia block; read the block's Bitcoin anchor and compute depth against Bitcoin tip).
- Detects state REGRESSION on a reorg: seqpald's own watcher is authoritative for rendering
  (openampd re-marks internally but exposes no read surface). On a reorg that unwinds a
  confirmed txid, the affected issuance's chip regresses and an event is emitted.
- Fans out to the SPA via Server-Sent Events at `GET /seqpal/api/events` (session-scoped to
  the principal's issuances): `{issuance_id, status, confirmations, anchor_depth, txid}`.
- Persists the watch state so a restart reconciles from chain rather than resetting.

Because a Bitcoin reorg cannot be triggered on demand on the live box, the regression path
also gets a REGTEST functional test (node repo pattern or a seqpald test with a mock node
that reorgs) as the acceptance artifact; the live surfaces prove the rest.

## 3. seqpald: registry publication + price feed + reads

- **Registry publication** at deploy: POST the issued asset's contract (with `entity.domain =
  sequentiatestnet.com`, the operator identity) to the box registry (`/registry/`). Serve the
  `.well-known/sequentia-asset-proof-<assetid>` proof from seqpald so the entry can verify, OR
  publish discovery-only (verified:false) if the box registry runs with REQUIRE_DOMAIN_PROOF.
  Record the registry URL on the issuance.
- **Price feed seeding** at deploy: seed the offering price into the box price server's
  `/prices` as a DATA-ONLY entry tagged `kind:"offering-reference"` (NEVER touch the
  price-server rate calculation, per the standing fee-rate rule). Consuming UIs can label the
  source. `fee_convert_atoms` is derived from the price server, not hardcoded.
- **Reads scoped to the owner**: `GET /issuances/{id}/holders` proxies openampd
  `GET /v1/issuer/holders?asset=` (the register/cap table, in atoms); `GET /issuances/{id}/log`
  proxies the relevant `/v1/log` slice; both 403 unless the session owns the issuance.
- **Log-head anchor cron**: trigger openampd `POST /v1/issuer/anchor` daily so the transparency
  log head is anchored on-chain; surface the anchor txids. (seqpald's OWN state anchor is M7.)

## 4. SPA: truthful surfaces (the bulk of M3)

Delete every fabricated-data path (`fakeAssetId`, `fakeTxid`, `fakeHex` if any remain) and:
- **Holdings / cap table / Registry of Members** render from `GET /issuances/{id}/holders`
  (openampd holders) and per-AID balances, in atoms, never from localStorage. "Held" shows a
  real on-chain balance > 0.
- **Confirmations + Bitcoin anchor depth on every state** via the SSE stream: a state chip is
  `broadcast` / `n confirmations` / `anchored at Bitcoin block H`. Simulated stages (from M2
  lifecycle) are visually distinct from chain-verified ones. NOTHING is labeled final at
  0-conf (standing first principle).
- **Reorg regression rendering**: when the SSE stream regresses a chip, show it regress with an
  "anchoring is supreme: Sequentia follows Bitcoin in real time" banner.
- **Explorer deep links** on every txid/asset id (`https://sequentiatestnet.com/tx/{txid}`,
  `/asset/{id}`), with copy buttons; no truncated non-clickable identifiers.
- **"Verify independently" explainer** (public): walks an auditor from the terms document to
  `terms_hash` to the on-chain `contract_hash` to the policy-key commitment. (The terms
  document itself is generated in M4; M3 provides the chain-verification chain and links.)
- **Transparency-log tab** per asset: fetch `/issuances/{id}/log`, verify the hash chain
  client-side (recompute `hash = sha256(prev || ...)`), link each anchor txid to the explorer.
- **Network-fees panel**: the operator fee asset, a per-transfer estimate, and the fee-
  conversion mechanic, always in the fee asset's OWN units (never "sat/vB"); price-fed
  `fee_convert_atoms` with a preview. Reference-currency display via the price server.
- **Unit-aware formatting**: 8-decimal BTC, atoms internal; fractional BTC renders correctly
  (0.4 BTC is not 0). Asset amounts respect precision.
- **"Load sample"** performs a REAL shared, rate-limited deploy (reuse a single sample asset to
  avoid mint spam) OR is removed; it may never fabricate asset ids/txids.
- **Registry + price surfaced**: the new asset resolves at `/registry/{id}`, in `/prices`, and
  the ticker is shown as registry-resolved; deep-link the registry entry.

## 5. Acceptance proof (live box)

An auditor with no SeqPal session verifies the register (holders endpoint) and a log-anchor
OP_RETURN on the explorer; the new asset's ticker resolves at `/registry/` and in `/prices`
and renders (as an ordinary row) where consumed; a regtest run shows a settlement/confirmation
state regressing after a reorg and the SSE stream emitting the regression; every txid and asset
id on every surface is an explorer-resolvable link; the transparency-log tab verifies the hash
chain client-side against the live `/v1/log`.

## 6. Out of scope for M3

Legal documents bound to terms_hash and the FAQ (M4); real payments/escrow/settlement (M5);
distributions (M7). M3 makes the surfaces TRUE; it does not add new money movement.
