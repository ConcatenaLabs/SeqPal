package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// migrations are applied in order; the schema_version row holds the count of
// migrations already applied, so a migration is never re-run.
var migrations = []string{
	`
CREATE TABLE accounts (
    aid          TEXT PRIMARY KEY,
    kind         TEXT NOT NULL DEFAULT 'individual',
    xonly        TEXT UNIQUE NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    id_number    TEXT UNIQUE,
    profile      TEXT NOT NULL DEFAULT '{}',
    created_at   INTEGER NOT NULL
);
CREATE TABLE entities (
    id           TEXT PRIMARY KEY,
    owner_aid    TEXT NOT NULL REFERENCES accounts(aid),
    name         TEXT NOT NULL DEFAULT '',
    jurisdiction TEXT NOT NULL DEFAULT '',
    profile      TEXT NOT NULL DEFAULT '{}',
    created_at   INTEGER NOT NULL
);
CREATE INDEX idx_entities_owner ON entities(owner_aid);
CREATE TABLE sessions (
    token      TEXT PRIMARY KEY,
    aid        TEXT NOT NULL REFERENCES accounts(aid),
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE TABLE challenges (
    challenge  TEXT PRIMARY KEY,
    xonly      TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used       INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE issuances (
    id              TEXT PRIMARY KEY,
    owner_aid       TEXT NOT NULL REFERENCES accounts(aid),
    entity_id       TEXT,
    name            TEXT NOT NULL DEFAULT '',
    ticker          TEXT NOT NULL DEFAULT '',
    structure_id    TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'draft',
    terms           TEXT NOT NULL DEFAULT '{}',
    supply          INTEGER NOT NULL DEFAULT 0,
    precision       INTEGER NOT NULL DEFAULT 0,
    confidential    INTEGER NOT NULL DEFAULT 0,
    clawback        INTEGER NOT NULL DEFAULT 1,
    asset_id        TEXT NOT NULL DEFAULT '',
    txid            TEXT NOT NULL DEFAULT '',
    contract_hash   TEXT NOT NULL DEFAULT '',
    holder_aid      TEXT NOT NULL DEFAULT '',
    enclave_address TEXT NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE INDEX idx_issuances_owner ON issuances(owner_aid);
CREATE TABLE deploys (
    idem_key      TEXT PRIMARY KEY,
    issuance_id   TEXT NOT NULL,
    asset_id      TEXT NOT NULL DEFAULT '',
    txid          TEXT NOT NULL DEFAULT '',
    contract_hash TEXT NOT NULL DEFAULT '',
    aid           TEXT NOT NULL DEFAULT '',
    address       TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL
);
CREATE TABLE audit_log (
    seq       INTEGER PRIMARY KEY AUTOINCREMENT,
    ts        INTEGER NOT NULL,
    actor_aid TEXT NOT NULL DEFAULT '',
    action    TEXT NOT NULL,
    detail    TEXT NOT NULL DEFAULT '{}',
    prev_hash TEXT NOT NULL DEFAULT '',
    hash      TEXT NOT NULL
);
`,
	// M2: compliance engine. All additive; an M1 database migrates forward in
	// place. Claims records are the source of truth from which each user's
	// openampd category list is a pure projection; screening and the review
	// queue drive the sanctions path; enclave_keys hold the per-offering escrow
	// and per-entity treasury keys seqpald custodies; platform_keys hold the
	// claims-signing key.
	`
CREATE TABLE claims (
    aid                TEXT PRIMARY KEY REFERENCES accounts(aid),
    residence          TEXT    NOT NULL DEFAULT '',
    base_eligibility   TEXT    NOT NULL DEFAULT 'ret',
    accredited         INTEGER NOT NULL DEFAULT 0,
    accred_artifact    TEXT    NOT NULL DEFAULT '',
    accred_valid_until INTEGER NOT NULL DEFAULT 0,
    us_person          INTEGER NOT NULL DEFAULT 0,
    gb_hnw             INTEGER NOT NULL DEFAULT 0,
    gb_soph            INTEGER NOT NULL DEFAULT 0,
    tax_residencies    TEXT    NOT NULL DEFAULT '[]',
    valid_until        INTEGER NOT NULL DEFAULT 0,
    status             TEXT    NOT NULL DEFAULT 'unverified',
    vocab_version      INTEGER NOT NULL DEFAULT 0,
    claims_sig         TEXT    NOT NULL DEFAULT '',
    verified_at        INTEGER NOT NULL DEFAULT 0,
    updated_at         INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE screening (
    aid              TEXT    NOT NULL,
    list             TEXT    NOT NULL,
    last_screened_at INTEGER NOT NULL DEFAULT 0,
    matched          INTEGER NOT NULL DEFAULT 0,
    matched_entry    TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (aid, list)
);
CREATE TABLE review_queue (
    id            TEXT PRIMARY KEY,
    aid           TEXT    NOT NULL,
    list          TEXT    NOT NULL DEFAULT '',
    matched_entry TEXT    NOT NULL DEFAULT '',
    state         TEXT    NOT NULL DEFAULT 'pending',
    disposition   TEXT    NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    decided_at    INTEGER NOT NULL DEFAULT 0,
    decided_by    TEXT    NOT NULL DEFAULT '',
    note          TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX idx_review_state ON review_queue(state);
CREATE INDEX idx_review_aid ON review_queue(aid);
CREATE TABLE enclave_keys (
    aid        TEXT PRIMARY KEY,
    kind       TEXT    NOT NULL,
    ref_id     TEXT    NOT NULL DEFAULT '',
    xonly      TEXT    NOT NULL,
    priv       TEXT    NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX idx_enclave_ref ON enclave_keys(kind, ref_id);
CREATE TABLE platform_keys (
    name       TEXT PRIMARY KEY,
    xonly      TEXT    NOT NULL,
    priv       TEXT    NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE TABLE ubo_links (
    entity_id    TEXT PRIMARY KEY,
    ubo_aid      TEXT    NOT NULL,
    treasury_aid TEXT    NOT NULL DEFAULT '',
    statement    TEXT    NOT NULL DEFAULT '',
    sig          TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL
);
CREATE TABLE notices (
    id         TEXT PRIMARY KEY,
    aid        TEXT    NOT NULL,
    kind       TEXT    NOT NULL,
    body       TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);
CREATE INDEX idx_notices_aid ON notices(aid);
`,
	// M3: the chain watcher's persistent state. One row per live issuance, keyed
	// by issuance id, holding the last-observed chain status so a restart
	// reconciles from chain rather than resetting. seqpald's watcher is
	// authoritative for rendering (openampd re-marks internally but exposes no
	// read surface). All additive; an M2 database migrates forward in place.
	`
CREATE TABLE watch (
    issuance_id   TEXT PRIMARY KEY REFERENCES issuances(id),
    owner_aid     TEXT    NOT NULL DEFAULT '',
    asset_id      TEXT    NOT NULL DEFAULT '',
    txid          TEXT    NOT NULL DEFAULT '',
    status        TEXT    NOT NULL DEFAULT 'broadcast',
    confirmations INTEGER NOT NULL DEFAULT 0,
    anchor_depth  INTEGER NOT NULL DEFAULT 0,
    anchor_height INTEGER NOT NULL DEFAULT 0,
    anchor_hash   TEXT    NOT NULL DEFAULT '',
    anchor_status TEXT    NOT NULL DEFAULT '',
    block_hash    TEXT    NOT NULL DEFAULT '',
    block_height  INTEGER NOT NULL DEFAULT 0,
    contract      TEXT    NOT NULL DEFAULT '',
    registry_url  TEXT    NOT NULL DEFAULT '',
    price_seeded  INTEGER NOT NULL DEFAULT 0,
    updated_at    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_watch_owner ON watch(owner_aid);
`,
	// M4: the legal artifact pipeline. Content-addressed documents, the
	// terms->manifest binding that makes terms_hash commit to the document set,
	// per-offering offer-window state (preimage gating), the RFSA simulated
	// registry, the rules-amendment chain, and document e-signatures. All
	// additive; an M3 database migrates forward in place.
	`
CREATE TABLE documents (
    doc_hash      TEXT PRIMARY KEY,
    issuance_id   TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL DEFAULT '',
    title         TEXT NOT NULL DEFAULT '',
    content_type  TEXT NOT NULL DEFAULT 'text/html; charset=utf-8',
    always_public INTEGER NOT NULL DEFAULT 0,
    body          BLOB NOT NULL,
    pdf           BLOB,
    created_at    INTEGER NOT NULL
);
CREATE INDEX idx_documents_issuance ON documents(issuance_id);
CREATE TABLE terms_docs (
    terms_hash      TEXT PRIMARY KEY,
    issuance_id     TEXT NOT NULL DEFAULT '',
    canonical_terms TEXT NOT NULL,
    manifest_hash   TEXT NOT NULL DEFAULT '',
    manifest        TEXT NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL
);
CREATE INDEX idx_terms_docs_issuance ON terms_docs(issuance_id);
CREATE TABLE offerings (
    issuance_id  TEXT PRIMARY KEY,
    offer_open   INTEGER NOT NULL DEFAULT 1,
    close_height INTEGER NOT NULL DEFAULT 0,
    closed_at    INTEGER NOT NULL DEFAULT 0,
    updated_at   INTEGER NOT NULL
);
CREATE TABLE filings (
    filing_number     TEXT PRIMARY KEY,
    issuer            TEXT NOT NULL DEFAULT '',
    issuer_aid        TEXT NOT NULL DEFAULT '',
    issuance_id       TEXT NOT NULL DEFAULT '',
    structure         TEXT NOT NULL DEFAULT '',
    doc_manifest_hash TEXT NOT NULL DEFAULT '',
    terms_hash        TEXT NOT NULL DEFAULT '',
    filing_hash       TEXT NOT NULL DEFAULT '',
    anchor_txid       TEXT NOT NULL DEFAULT '',
    created_at        INTEGER NOT NULL
);
CREATE INDEX idx_filings_terms ON filings(terms_hash);
CREATE INDEX idx_filings_issuance ON filings(issuance_id);
CREATE TABLE amendments (
    amend_hash       TEXT PRIMARY KEY,
    issuance_id      TEXT NOT NULL DEFAULT '',
    asset_id         TEXT NOT NULL DEFAULT '',
    ord              INTEGER NOT NULL DEFAULT 0,
    prior_rules_hash TEXT NOT NULL DEFAULT '',
    new_rules_hash   TEXT NOT NULL DEFAULT '',
    basis            TEXT NOT NULL DEFAULT '',
    effective_height INTEGER NOT NULL DEFAULT 0,
    anchor_txid      TEXT NOT NULL DEFAULT '',
    created_at       INTEGER NOT NULL
);
CREATE INDEX idx_amendments_issuance ON amendments(issuance_id);
CREATE TABLE doc_signatures (
    doc_hash    TEXT NOT NULL,
    signer_aid  TEXT NOT NULL,
    xonly       TEXT NOT NULL DEFAULT '',
    sig         TEXT NOT NULL,
    anchor_txid TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    PRIMARY KEY (doc_hash, signer_aid)
);
CREATE INDEX idx_doc_sig_doc ON doc_signatures(doc_hash);
`,
	// M5: the money engine. Subscriptions and their per-rail escrow deposits, the
	// exactly-one-per-subscription settlement record (the idempotency anchor), the
	// append-only escrow ledger (funds_simulated flag on fiat-derived rows), the
	// SIMULATED fiat processor's payments, platform-fee invoices (setup blocks
	// deploy; escrow accrues and is deducted at release), issuer payout mandates,
	// and the EU per-member-state offeree counters + captured offer-side gate
	// records. All additive; an M4 database migrates forward in place.
	`
CREATE TABLE subscriptions (
    id              TEXT PRIMARY KEY,
    issuance_id     TEXT    NOT NULL,
    investor_aid    TEXT    NOT NULL,
    rail            TEXT    NOT NULL,
    token_atoms     INTEGER NOT NULL DEFAULT 0,
    pay_amount      INTEGER NOT NULL DEFAULT 0,
    pay_ccy         TEXT    NOT NULL DEFAULT '',
    deposit_address TEXT    NOT NULL DEFAULT '',
    refund_address  TEXT    NOT NULL DEFAULT '',
    state           TEXT    NOT NULL DEFAULT 'created',
    funds_simulated INTEGER NOT NULL DEFAULT 0,
    deposit_txid    TEXT    NOT NULL DEFAULT '',
    confirmations   INTEGER NOT NULL DEFAULT 0,
    deposited_atoms INTEGER NOT NULL DEFAULT 0,
    usd_rate        REAL    NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE INDEX idx_subs_issuance ON subscriptions(issuance_id);
CREATE INDEX idx_subs_investor ON subscriptions(investor_aid);
CREATE INDEX idx_subs_deposit_addr ON subscriptions(deposit_address);
CREATE INDEX idx_subs_state ON subscriptions(state);
CREATE TABLE settlements (
    subscription_id TEXT PRIMARY KEY,
    issuance_id     TEXT    NOT NULL DEFAULT '',
    state           TEXT    NOT NULL DEFAULT 'pending',
    delivery_txid   TEXT    NOT NULL DEFAULT '',
    release_txid    TEXT    NOT NULL DEFAULT '',
    refund_txid     TEXT    NOT NULL DEFAULT '',
    fee_atoms       INTEGER NOT NULL DEFAULT 0,
    error           TEXT    NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE TABLE escrow_ledger (
    id              TEXT PRIMARY KEY,
    subscription_id TEXT    NOT NULL DEFAULT '',
    issuance_id     TEXT    NOT NULL DEFAULT '',
    kind            TEXT    NOT NULL,
    rail            TEXT    NOT NULL DEFAULT '',
    amount          INTEGER NOT NULL DEFAULT 0,
    ccy             TEXT    NOT NULL DEFAULT '',
    txid            TEXT    NOT NULL DEFAULT '',
    funds_simulated INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL
);
CREATE INDEX idx_ledger_sub ON escrow_ledger(subscription_id);
CREATE INDEX idx_ledger_issuance ON escrow_ledger(issuance_id);
CREATE TABLE fiat_payments (
    id              TEXT PRIMARY KEY,
    subscription_id TEXT    NOT NULL DEFAULT '',
    invoice_id      TEXT    NOT NULL DEFAULT '',
    rail            TEXT    NOT NULL,
    amount_minor    INTEGER NOT NULL DEFAULT 0,
    ccy             TEXT    NOT NULL DEFAULT 'USD',
    state           TEXT    NOT NULL DEFAULT 'pending',
    receipt         TEXT    NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    settle_at       INTEGER NOT NULL DEFAULT 0,
    updated_at      INTEGER NOT NULL
);
CREATE INDEX idx_fiat_sub ON fiat_payments(subscription_id);
CREATE INDEX idx_fiat_state ON fiat_payments(state);
CREATE TABLE fee_invoices (
    id              TEXT PRIMARY KEY,
    issuance_id     TEXT    NOT NULL,
    kind            TEXT    NOT NULL,
    rail            TEXT    NOT NULL DEFAULT '',
    amount_usd      REAL    NOT NULL DEFAULT 0,
    amount          INTEGER NOT NULL DEFAULT 0,
    ccy             TEXT    NOT NULL DEFAULT '',
    state           TEXT    NOT NULL DEFAULT 'unpaid',
    txid            TEXT    NOT NULL DEFAULT '',
    address         TEXT    NOT NULL DEFAULT '',
    funds_simulated INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    paid_at         INTEGER NOT NULL DEFAULT 0,
    updated_at      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_fee_invoices_issuance ON fee_invoices(issuance_id);
CREATE TABLE payout_mandates (
    issuance_id  TEXT    NOT NULL,
    chain        TEXT    NOT NULL,
    asset        TEXT    NOT NULL DEFAULT '',
    address      TEXT    NOT NULL,
    signature    TEXT    NOT NULL DEFAULT '',
    signer_xonly TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    PRIMARY KEY (issuance_id, chain)
);
CREATE TABLE offerees (
    issuance_id  TEXT    NOT NULL,
    member_state TEXT    NOT NULL,
    aid          TEXT    NOT NULL,
    created_at   INTEGER NOT NULL,
    PRIMARY KEY (issuance_id, member_state, aid)
);
CREATE INDEX idx_offerees_ms ON offerees(issuance_id, member_state);
CREATE TABLE gate_records (
    aid         TEXT    NOT NULL,
    issuance_id TEXT    NOT NULL DEFAULT '',
    kind        TEXT    NOT NULL,
    detail      TEXT    NOT NULL DEFAULT '{}',
    valid_until INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    PRIMARY KEY (aid, issuance_id, kind)
);
`,
	// M6: reorged-BTC-deposit holds. A settled native-BTC subscription's deposit is
	// watched on testnet4 AFTER delivery; a reorg that unwinds the deposit triggers a
	// GLOBAL account freeze (recorded here so a re-confirmation lifts only the freeze
	// this hold caused, never a sanctions freeze).
	`
CREATE TABLE reorg_holds (
    subscription_id TEXT PRIMARY KEY,
    issuance_id     TEXT    NOT NULL DEFAULT '',
    investor_aid    TEXT    NOT NULL,
    deposit_txid    TEXT    NOT NULL DEFAULT '',
    state           TEXT    NOT NULL DEFAULT 'active',
    reason          TEXT    NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE INDEX idx_reorg_holds_state ON reorg_holds(state);
`,
	// M7: transfer-agent servicing. The distribution engine (a run funds a
	// servicing deposit, snapshots the register at a block height, computes
	// pro-rata + withholding, and pays each holder's registered ordinary mandate
	// address) and the INVESTOR-side payout mandates. distribution_payments reuses
	// the M5/M6 settlement idempotency shape: exactly one row per (run, holder),
	// its txid recorded pre-broadcast so an ambiguous timeout is reconciled by a
	// chain scan before any retry, never a double-pay. investor_mandates is
	// distinct from the issuer payout_mandates: keyed by the INVESTOR's AID and
	// chain, it is the ordinary address a distribution pays. All additive; an M6
	// database migrates forward in place.
	`
CREATE TABLE investor_mandates (
    investor_aid TEXT    NOT NULL,
    chain        TEXT    NOT NULL,
    asset        TEXT    NOT NULL DEFAULT '',
    address      TEXT    NOT NULL,
    signature    TEXT    NOT NULL DEFAULT '',
    signer_xonly TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (investor_aid, chain)
);
CREATE TABLE distributions (
    id               TEXT PRIMARY KEY,
    issuance_id      TEXT    NOT NULL,
    asset_id         TEXT    NOT NULL DEFAULT '',
    pool_atoms       INTEGER NOT NULL DEFAULT 0,
    memo             TEXT    NOT NULL DEFAULT '',
    state            TEXT    NOT NULL DEFAULT 'awaiting_funding',
    deposit_address  TEXT    NOT NULL DEFAULT '',
    funded_atoms     INTEGER NOT NULL DEFAULT 0,
    funded_txid      TEXT    NOT NULL DEFAULT '',
    snapshot_height  INTEGER NOT NULL DEFAULT 0,
    total_held       INTEGER NOT NULL DEFAULT 0,
    dust_atoms       INTEGER NOT NULL DEFAULT 0,
    gross_total      INTEGER NOT NULL DEFAULT 0,
    withheld_total   INTEGER NOT NULL DEFAULT 0,
    net_total        INTEGER NOT NULL DEFAULT 0,
    dividend_equiv   INTEGER NOT NULL DEFAULT 0,
    statement_hash   TEXT    NOT NULL DEFAULT '',
    withholding_hash TEXT    NOT NULL DEFAULT '',
    crs_hash         TEXT    NOT NULL DEFAULT '',
    anchor_txid      TEXT    NOT NULL DEFAULT '',
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_distributions_issuance ON distributions(issuance_id);
CREATE INDEX idx_distributions_state ON distributions(state);
CREATE TABLE distribution_payments (
    run_id          TEXT    NOT NULL,
    holder_aid      TEXT    NOT NULL,
    balance_atoms   INTEGER NOT NULL DEFAULT 0,
    gross_atoms     INTEGER NOT NULL DEFAULT 0,
    withheld_atoms  INTEGER NOT NULL DEFAULT 0,
    net_atoms       INTEGER NOT NULL DEFAULT 0,
    treaty_bps      INTEGER NOT NULL DEFAULT 0,
    tax_status      TEXT    NOT NULL DEFAULT '',
    mandate_address TEXT    NOT NULL DEFAULT '',
    state           TEXT    NOT NULL DEFAULT 'pending',
    txid            TEXT    NOT NULL DEFAULT '',
    reason          TEXT    NOT NULL DEFAULT '',
    statement_hash  TEXT    NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (run_id, holder_aid)
);
CREATE INDEX idx_dist_payments_run ON distribution_payments(run_id);
`,
	// M7 (Backend-B): the servicing consoles. rules_mutations is the durable intent
	// record for the rules-amendment chokepoint (§3): a mutation persists here BEFORE
	// the openampd rules POST and the anchored amendment, so a half-applied mutation
	// (rules posted but not anchored, or vice versa) is detectable and retried, never
	// left inconsistent. clawbacks and redeliveries are the fund-movement idempotency
	// anchors for the freeze/clawback console (§4) and the stranded-key runbook (§5):
	// intent persisted before broadcast, reconciled by a chain/log scan before any
	// retry, never a double-sweep. ownership_snapshots holds the scheduled register
	// snapshots and annual-report artifacts (§6), each anchored. All additive; an M7
	// (Backend-A) database migrates forward in place.
	`
CREATE TABLE rules_mutations (
    id               TEXT PRIMARY KEY,
    issuance_id      TEXT    NOT NULL,
    asset_id         TEXT    NOT NULL DEFAULT '',
    prior_rules_hash TEXT    NOT NULL DEFAULT '',
    new_rules_hash   TEXT    NOT NULL DEFAULT '',
    basis            TEXT    NOT NULL DEFAULT '',
    effective_height INTEGER NOT NULL DEFAULT 0,
    rules_json       TEXT    NOT NULL DEFAULT '',
    state            TEXT    NOT NULL DEFAULT 'pending',
    amend_hash       TEXT    NOT NULL DEFAULT '',
    anchor_txid      TEXT    NOT NULL DEFAULT '',
    error            TEXT    NOT NULL DEFAULT '',
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_rules_mutations_issuance ON rules_mutations(issuance_id);
CREATE INDEX idx_rules_mutations_state ON rules_mutations(state);
CREATE TABLE clawbacks (
    id          TEXT PRIMARY KEY,
    issuance_id TEXT    NOT NULL,
    asset_id    TEXT    NOT NULL DEFAULT '',
    holder_aid  TEXT    NOT NULL,
    reason      TEXT    NOT NULL DEFAULT '',
    state       TEXT    NOT NULL DEFAULT 'sweeping',
    atoms       INTEGER NOT NULL DEFAULT 0,
    txid        TEXT    NOT NULL DEFAULT '',
    by_aid      TEXT    NOT NULL DEFAULT '',
    context     TEXT    NOT NULL DEFAULT 'console',
    error       TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_clawbacks_holder ON clawbacks(asset_id, holder_aid);
CREATE INDEX idx_clawbacks_state ON clawbacks(state);
CREATE TABLE redeliveries (
    id             TEXT PRIMARY KEY,
    issuance_id    TEXT    NOT NULL,
    asset_id       TEXT    NOT NULL DEFAULT '',
    old_aid        TEXT    NOT NULL,
    new_aid        TEXT    NOT NULL,
    reason         TEXT    NOT NULL DEFAULT '',
    state          TEXT    NOT NULL DEFAULT 'created',
    swept_atoms    INTEGER NOT NULL DEFAULT 0,
    clawback_id    TEXT    NOT NULL DEFAULT '',
    clawback_txid  TEXT    NOT NULL DEFAULT '',
    source_aid     TEXT    NOT NULL DEFAULT '',
    source_kind    TEXT    NOT NULL DEFAULT '',
    new_before     INTEGER NOT NULL DEFAULT 0,
    redeliver_txid TEXT    NOT NULL DEFAULT '',
    by_aid         TEXT    NOT NULL DEFAULT '',
    error          TEXT    NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL DEFAULT 0,
    UNIQUE(issuance_id, old_aid, new_aid)
);
CREATE INDEX idx_redeliveries_issuance ON redeliveries(issuance_id);
CREATE TABLE ownership_snapshots (
    id            TEXT PRIMARY KEY,
    issuance_id   TEXT    NOT NULL,
    asset_id      TEXT    NOT NULL DEFAULT '',
    height        INTEGER NOT NULL DEFAULT 0,
    holders_count INTEGER NOT NULL DEFAULT 0,
    total_atoms   INTEGER NOT NULL DEFAULT 0,
    report_hash   TEXT    NOT NULL DEFAULT '',
    kind          TEXT    NOT NULL DEFAULT 'snapshot',
    anchor_txid   TEXT    NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL
);
CREATE INDEX idx_ownership_snapshots_issuance ON ownership_snapshots(issuance_id);
`,
	// M8: secondary market + Depository-Receipt programme + venue listings. p2p_transfers
	// is the travel-rule record for a policy-co-signed holder-to-holder transfer: it stores
	// the originator and beneficiary AIDs (both joined to registered platform identities) and
	// its txid pre-broadcast, so an ambiguous complete is reconciled by a chain/log scan before
	// any retry, never a double-transfer. A refusal is a first-class terminal state carrying the
	// real policy-server reason (also in openampd /v1/log). market_abuse_acks records the
	// once-per-investor insider-dealing acknowledgment (a platform-layer control, disclosed as
	// such, not policy-enforced at co-sign). listings holds the issuer-granted listing
	// authorization a venue reads (a venue can only CHECK, never GRANT). dr_programs + dr_ops
	// drive the DR programme: mint = OA-6 reissuance, redeem = OA-5 burn, each op keyed by an
	// idempotency request_id so a crash-then-retry never double-mints or double-burns; supply
	// stays chain-derived (never a stored counter). kv holds the wallet-transfer log poller's
	// cursor. All additive; an M7 database migrates forward in place.
	`
CREATE TABLE p2p_transfers (
    id               TEXT PRIMARY KEY,
    oa_id            TEXT    NOT NULL DEFAULT '',
    issuance_id      TEXT    NOT NULL DEFAULT '',
    asset_id         TEXT    NOT NULL DEFAULT '',
    originator_aid   TEXT    NOT NULL,
    beneficiary_aid  TEXT    NOT NULL DEFAULT '',
    atoms            INTEGER NOT NULL DEFAULT 0,
    state            TEXT    NOT NULL DEFAULT 'awaiting_signature',
    source           TEXT    NOT NULL DEFAULT 'browser',
    txid             TEXT    NOT NULL DEFAULT '',
    reason           TEXT    NOT NULL DEFAULT '',
    orig_name        TEXT    NOT NULL DEFAULT '',
    orig_residence   TEXT    NOT NULL DEFAULT '',
    benef_name       TEXT    NOT NULL DEFAULT '',
    benef_residence  TEXT    NOT NULL DEFAULT '',
    benef_registered INTEGER NOT NULL DEFAULT 0,
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_p2p_originator ON p2p_transfers(originator_aid);
CREATE INDEX idx_p2p_asset ON p2p_transfers(asset_id);
CREATE UNIQUE INDEX idx_p2p_txid ON p2p_transfers(txid) WHERE txid != '';
CREATE TABLE market_abuse_acks (
    aid          TEXT PRIMARY KEY,
    version      INTEGER NOT NULL DEFAULT 1,
    ack_hash     TEXT    NOT NULL DEFAULT '',
    signature    TEXT    NOT NULL DEFAULT '',
    signer_xonly TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL
);
CREATE TABLE listings (
    asset_id     TEXT PRIMARY KEY,
    issuance_id  TEXT    NOT NULL DEFAULT '',
    issuer_aid   TEXT    NOT NULL DEFAULT '',
    ticker       TEXT    NOT NULL DEFAULT '',
    name         TEXT    NOT NULL DEFAULT '',
    authorized   INTEGER NOT NULL DEFAULT 0,
    venues       TEXT    NOT NULL DEFAULT '[]',
    signature    TEXT    NOT NULL DEFAULT '',
    signer_xonly TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE dr_programs (
    issuance_id         TEXT PRIMARY KEY,
    asset_id            TEXT    NOT NULL DEFAULT '',
    enabled             INTEGER NOT NULL DEFAULT 0,
    us_exclusion_height INTEGER NOT NULL DEFAULT 0,
    mutation_id         TEXT    NOT NULL DEFAULT '',
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE dr_ops (
    id               TEXT PRIMARY KEY,
    issuance_id      TEXT    NOT NULL,
    asset_id         TEXT    NOT NULL DEFAULT '',
    kind             TEXT    NOT NULL,
    request_id       TEXT    NOT NULL,
    target_aid       TEXT    NOT NULL DEFAULT '',
    holder_aid       TEXT    NOT NULL DEFAULT '',
    atoms            INTEGER NOT NULL DEFAULT 0,
    oa_id            TEXT    NOT NULL DEFAULT '',
    txid             TEXT    NOT NULL DEFAULT '',
    state            TEXT    NOT NULL DEFAULT 'pending',
    attestation_hash TEXT    NOT NULL DEFAULT '',
    anchor_txid      TEXT    NOT NULL DEFAULT '',
    error            TEXT    NOT NULL DEFAULT '',
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX idx_dr_ops_request ON dr_ops(request_id);
CREATE INDEX idx_dr_ops_issuance ON dr_ops(issuance_id);
CREATE TABLE kv (
    key        TEXT PRIMARY KEY,
    value      TEXT    NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL DEFAULT 0
);
`,
	// M9: the external issuer key + two-phase clawback. issuer_external records, per
	// issuance, whether the enclave issuer half is the entity's own browser key (so
	// clawback needs the issuer's browser signature, run build->sign->complete)
	// rather than a server-held key (the legacy single-call clawback). issuer_pubkey
	// stores that external x-only for the signer cross-check. On the clawbacks row,
	// oa_id references the openampd two-phase build awaiting the issuer's signatures
	// and to_sign caches the leaf sighashes the issuer must sign, so a resumed build
	// re-surfaces them without asking openampd to assemble a second sweep. All
	// additive with safe defaults: an M8 database migrates forward in place, every
	// existing issuance stays issuer_external=0 (the legacy path), and BONDX and the
	// M1-M8 assets are untouched.
	`
ALTER TABLE issuances ADD COLUMN issuer_external INTEGER NOT NULL DEFAULT 0;
ALTER TABLE issuances ADD COLUMN issuer_pubkey TEXT NOT NULL DEFAULT '';
ALTER TABLE clawbacks ADD COLUMN oa_id TEXT NOT NULL DEFAULT '';
ALTER TABLE clawbacks ADD COLUMN to_sign TEXT NOT NULL DEFAULT '';
`,
	// M10: enforcement elections + bearer (supervised) issuance + corporate
	// actions + deposit-time escrow-fee accrual. All additive; an M9 database
	// migrates forward in place.
	//
	// issuances.enforcement records the issuer's election: 'serviced' (the
	// existing co-signed OpenAMP path), 'network' (OpenDAMP; refused 501 until
	// SEQPALD_DAMP is enabled, the election is still recorded), or 'bearer'
	// (a consensus-supervised asset issued through the node's raw path, no
	// openampd involvement). recovery_pubkey + supervision_pause are the bearer
	// asset's supervision descriptor halves beside the operational key (which is
	// the issuer's own session key, stored in issuer_pubkey).
	//
	// bearer_attestations: the annually-refreshable no-US-nexus / risk
	// attestation a bearer deploy requires, BIP340-signed by the session key
	// under tag "seqpal-bearer-attestation-v1".
	//
	// supervision_ops: pending freeze/unfreeze operations. The row is the
	// idempotency anchor: it records the locked funding prevout at build and the
	// assembled raw tx + txid BEFORE broadcast, so an ambiguous submit is
	// resumed, never re-built (a rebuilt record would need a fresh issuer
	// signature and could double-spend the funding input).
	//
	// corporate_actions + action_snapshots + action_claims +
	// action_claim_outpoints: the W-3 corporate-action engine. The snapshot is
	// the chain UTXO set carrying the asset at the first watcher pass at or
	// after the record height; a claim proves key control over snapshot
	// outpoints (tag "seqpal-holding-proof-v1"); action_claim_outpoints'
	// primary key IS the one-claim-per-(action, outpoint) rule.
	//
	// escrow_ledger needs no new column for W-6 fee accrual: the accrual is an
	// ordinary kind='escrow_fee' row written at deposit confirmation instead of
	// at release, and its presence is what closing/refund consume.
	`
ALTER TABLE issuances ADD COLUMN enforcement TEXT NOT NULL DEFAULT 'serviced';
ALTER TABLE issuances ADD COLUMN recovery_pubkey TEXT NOT NULL DEFAULT '';
ALTER TABLE issuances ADD COLUMN supervision_pause INTEGER NOT NULL DEFAULT 0;
CREATE TABLE bearer_attestations (
    issuance_id TEXT PRIMARY KEY REFERENCES issuances(id),
    aid          TEXT    NOT NULL,
    no_us_nexus  INTEGER NOT NULL DEFAULT 0,
    risk_accepted INTEGER NOT NULL DEFAULT 0,
    statement    TEXT    NOT NULL DEFAULT '',
    sig          TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    valid_until  INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE supervision_ops (
    id            TEXT PRIMARY KEY,
    issuance_id   TEXT    NOT NULL,
    asset_id      TEXT    NOT NULL DEFAULT '',
    kind          TEXT    NOT NULL,
    target        TEXT    NOT NULL DEFAULT '',
    target_hash   TEXT    NOT NULL DEFAULT '',
    reason        TEXT    NOT NULL DEFAULT '',
    order_hash    TEXT    NOT NULL DEFAULT '',
    ref_id        TEXT    NOT NULL DEFAULT '',
    fund_txid     TEXT    NOT NULL DEFAULT '',
    fund_vout     INTEGER NOT NULL DEFAULT 0,
    fund_atoms    INTEGER NOT NULL DEFAULT 0,
    sighash       TEXT    NOT NULL DEFAULT '',
    record_script TEXT    NOT NULL DEFAULT '',
    record_vout   INTEGER NOT NULL DEFAULT -1,
    rawtx         TEXT    NOT NULL DEFAULT '',
    txid          TEXT    NOT NULL DEFAULT '',
    state         TEXT    NOT NULL DEFAULT 'pending',
    channel       TEXT    NOT NULL DEFAULT '',
    error         TEXT    NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_supervision_ops_issuance ON supervision_ops(issuance_id);
CREATE TABLE corporate_actions (
    id              TEXT PRIMARY KEY,
    issuance_id     TEXT    NOT NULL,
    asset_id        TEXT    NOT NULL DEFAULT '',
    kind            TEXT    NOT NULL,
    state           TEXT    NOT NULL DEFAULT 'awaiting_snapshot',
    record_height   INTEGER NOT NULL DEFAULT 0,
    div_asset       TEXT    NOT NULL DEFAULT '',
    div_total_atoms INTEGER NOT NULL DEFAULT 0,
    deposit_address TEXT    NOT NULL DEFAULT '',
    funded          INTEGER NOT NULL DEFAULT 0,
    funded_atoms    INTEGER NOT NULL DEFAULT 0,
    funded_txid     TEXT    NOT NULL DEFAULT '',
    question        TEXT    NOT NULL DEFAULT '',
    choices         TEXT    NOT NULL DEFAULT '[]',
    closes_height   INTEGER NOT NULL DEFAULT 0,
    snapshot_height INTEGER NOT NULL DEFAULT 0,
    snapshot_total  INTEGER NOT NULL DEFAULT 0,
    snapshot_count  INTEGER NOT NULL DEFAULT 0,
    snapshot_hash   TEXT    NOT NULL DEFAULT '',
    snapshot_note   TEXT    NOT NULL DEFAULT '',
    anchor_txid     TEXT    NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_actions_issuance ON corporate_actions(issuance_id);
CREATE INDEX idx_actions_state ON corporate_actions(state);
CREATE TABLE action_snapshots (
    action_id TEXT    NOT NULL,
    outpoint  TEXT    NOT NULL,
    script    TEXT    NOT NULL DEFAULT '',
    atoms     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (action_id, outpoint)
);
CREATE TABLE action_claims (
    id             TEXT PRIMARY KEY,
    action_id      TEXT    NOT NULL,
    aid            TEXT    NOT NULL DEFAULT '',
    pubkey         TEXT    NOT NULL DEFAULT '',
    outpoints      TEXT    NOT NULL DEFAULT '[]',
    atoms          INTEGER NOT NULL DEFAULT 0,
    payout_address TEXT    NOT NULL DEFAULT '',
    choice         TEXT    NOT NULL DEFAULT '',
    gross_atoms    INTEGER NOT NULL DEFAULT 0,
    withheld_atoms INTEGER NOT NULL DEFAULT 0,
    net_atoms      INTEGER NOT NULL DEFAULT 0,
    treaty_bps     INTEGER NOT NULL DEFAULT 0,
    tax_status     TEXT    NOT NULL DEFAULT '',
    sig            TEXT    NOT NULL DEFAULT '',
    state          TEXT    NOT NULL DEFAULT 'registered',
    txid           TEXT    NOT NULL DEFAULT '',
    reason         TEXT    NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_action_claims_action ON action_claims(action_id);
CREATE TABLE action_claim_outpoints (
    action_id TEXT    NOT NULL,
    outpoint  TEXT    NOT NULL,
    claim_id  TEXT    NOT NULL,
    atoms     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (action_id, outpoint)
);
`,
	// M11: per-transfer confidentiality. Confidentiality stopped being an asset
	// property elected at issuance and became a per-transfer choice: any holder
	// may move (or receive) any restricted asset confidentially in a given
	// transfer, transparent by default, with the registrar keeping full sight
	// through the policy server's blinding keys. p2p_transfers.confidential notes
	// the election on the travel-rule record. issuances.confidential stays in the
	// schema (additive, never destructive) but is no longer written or read for
	// behavior; every deploy is a transparent mint. All additive; an M10 database
	// migrates forward in place.
	`
ALTER TABLE p2p_transfers ADD COLUMN confidential INTEGER NOT NULL DEFAULT 0;
`,
	// M12: network-enforced issuance. A network-enforced asset is policed on chain
	// rather than by the policy server, so the facts worth keeping are different
	// ones: the verifier asset and its fixed amount, the policy commitment the
	// deployed program is instantiated with, the whitelist root that commitment
	// covers, and the two addresses holders and auditors actually look at. None of
	// them is a secret and none of them is a key.
	//
	// damp_prepares is the two-phase handoff. A network deploy cannot complete in
	// one call: the on-chain programs are compiled outside this platform, and the
	// values needed to compile them are only fixed once the policy server has
	// chosen the funding outpoint. So the first deploy attempt prepares, records
	// what the issuer's registrar must run, and refuses; the second attempt carries
	// the two values back and completes. One row per issuance keeps that resumable
	// and stops a retry preparing (and minting a verifier asset) twice.
	// All additive; an M11 database migrates forward in place.
	`
ALTER TABLE issuances ADD COLUMN verifier_asset TEXT NOT NULL DEFAULT '';
ALTER TABLE issuances ADD COLUMN verifier_amount INTEGER NOT NULL DEFAULT 0;
ALTER TABLE issuances ADD COLUMN policy_commitment TEXT NOT NULL DEFAULT '';
ALTER TABLE issuances ADD COLUMN whitelist_root TEXT NOT NULL DEFAULT '';
ALTER TABLE issuances ADD COLUMN holder_covenant_address TEXT NOT NULL DEFAULT '';
ALTER TABLE issuances ADD COLUMN verifier_covenant_address TEXT NOT NULL DEFAULT '';
CREATE TABLE damp_prepares (
    issuance_id          TEXT PRIMARY KEY,
    prepare_id           TEXT    NOT NULL,
    asset_id             TEXT    NOT NULL,
    verifier_asset       TEXT    NOT NULL,
    verifier_amount      INTEGER NOT NULL DEFAULT 0,
    policy_commitment    TEXT    NOT NULL,
    whitelist_root       TEXT    NOT NULL,
    whitelist            TEXT    NOT NULL DEFAULT '[]',
    derive_document      TEXT    NOT NULL DEFAULT '{}',
    snapshot_sig_message TEXT    NOT NULL DEFAULT '',
    created_at           INTEGER NOT NULL
);
`,
	// M13: the holder controls a network-enforced asset actually has.
	//
	// A serviced asset is frozen by withholding a signature, and a bearer asset by
	// a consensus record. A network-enforced asset has neither: its rules live in a
	// published policy the chain reads, so the only way to stop a holder or a
	// specific coin is to publish the next policy AND move the on-chain rules
	// output onto it. That is two round trips through the policy server plus a step
	// only the issuer's registrar can run, so it needs a durable row.
	//
	// damp_policy_ops is that row, and it is the idempotency anchor exactly as
	// supervision_ops is for a bearer asset: created at build with the policy
	// server's operation id, the sequence number and commitment it will publish,
	// the change in human terms, the reason and the order fingerprint; then
	// carrying the registrar's program identity and the broadcast txid. A replayed
	// build against the same order and the same targets resumes this row rather
	// than opening a second sequence number.
	//
	// targets is the canonical JSON of what the request named (holder keys and
	// coins). Together with kind and order_hash it identifies the operation, so two
	// different orders against the same holder stay two operations.
	// All additive; an M12 database migrates forward in place.
	`
CREATE TABLE damp_policy_ops (
    id                 TEXT PRIMARY KEY,
    issuance_id        TEXT    NOT NULL,
    asset_id           TEXT    NOT NULL DEFAULT '',
    kind               TEXT    NOT NULL,
    policy_id          TEXT    NOT NULL DEFAULT '',
    seq                INTEGER NOT NULL DEFAULT 0,
    prev_pi            TEXT    NOT NULL DEFAULT '',
    pi_next            TEXT    NOT NULL DEFAULT '',
    targets            TEXT    NOT NULL DEFAULT '{}',
    holders            TEXT    NOT NULL DEFAULT '[]',
    holders_added      TEXT    NOT NULL DEFAULT '[]',
    holders_removed    TEXT    NOT NULL DEFAULT '[]',
    coins_frozen       TEXT    NOT NULL DEFAULT '[]',
    coins_unfrozen     TEXT    NOT NULL DEFAULT '[]',
    reason             TEXT    NOT NULL DEFAULT '',
    order_hash         TEXT    NOT NULL DEFAULT '',
    to_sign            TEXT    NOT NULL DEFAULT '',
    registrar_document TEXT    NOT NULL DEFAULT '{}',
    verifier_cmr       TEXT    NOT NULL DEFAULT '',
    txid               TEXT    NOT NULL DEFAULT '',
    state              TEXT    NOT NULL DEFAULT 'pending',
    error              TEXT    NOT NULL DEFAULT '',
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_damp_policy_ops_issuance ON damp_policy_ops(issuance_id);
`,
	// A SeqPal ID is the enclave key of the holder's own Sequentia wallet, and a
	// wallet refuses to sign a digest it cannot check: it recomputes every sighash
	// from the transaction itself. So a two-phase clawback has to hand back the
	// built sweep transaction alongside its sighashes, and a resumed build has to
	// re-surface the same one. Additive with a safe default; an existing database
	// migrates forward in place, and a clawback built before this column simply
	// has no transaction to re-surface and is rebuilt.
	`
ALTER TABLE clawbacks ADD COLUMN tx TEXT NOT NULL DEFAULT '';
`,
	// A network-enforced freeze is authorized by a signature over the TAGGED
	// hash of the policy snapshot, so the issuer's wallet needs the snapshot
	// hash rather than the already-tagged message: it applies the tag itself and
	// never signs a digest it was simply handed. Additive with a safe default; a
	// policy op prepared before this column has no snapshot hash to re-surface
	// and is prepared again.
	`
ALTER TABLE damp_policy_ops ADD COLUMN snapshot_hash TEXT NOT NULL DEFAULT '';
`,
	// A SeqPal ID no longer has to be an OpenAMP enclave account. A wallet that
	// can prove control of a descriptor gets one too, and reaches everything
	// that is not the enclave: supervised stocks, network-enforced assets, the
	// distributions attached to them. `identity` says which kind an account is
	// and `descriptor` carries the wallet's, both additive with defaults that
	// keep every existing row an enclave account exactly as before.
	`
ALTER TABLE accounts ADD COLUMN identity TEXT NOT NULL DEFAULT 'aid';
ALTER TABLE accounts ADD COLUMN descriptor TEXT NOT NULL DEFAULT '';
`,
	// Nothing put a holder on a network-enforced asset's whitelist except the
	// issuer typing their key, so a verified SeqPal ID was a credential with
	// nowhere to present itself. A holder can now ask, per asset, having proved
	// control of the key they want admitted; the issuer decides, and a published
	// policy change is what turns an approval into a fact.
	`
CREATE TABLE whitelist_requests (
    id            TEXT PRIMARY KEY,
    issuance_id   TEXT NOT NULL,
    asset_id      TEXT NOT NULL DEFAULT '',
    aid           TEXT NOT NULL,
    holding_key   TEXT NOT NULL,
    proof         TEXT NOT NULL DEFAULT '',
    note          TEXT NOT NULL DEFAULT '',
    state         TEXT NOT NULL DEFAULT 'pending',
    decided_by    TEXT NOT NULL DEFAULT '',
    decided_at    INTEGER NOT NULL DEFAULT 0,
    decision_note TEXT NOT NULL DEFAULT '',
    policy_op_id  TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_whitelist_requests_issuance ON whitelist_requests(issuance_id, state);
CREATE INDEX idx_whitelist_requests_aid ON whitelist_requests(aid);
CREATE UNIQUE INDEX idx_whitelist_requests_live ON whitelist_requests(issuance_id, holding_key)
    WHERE state IN ('pending','approved');
`,
	// One SeqPal ID, more than one wallet. A holder who opened their ID with a
	// web wallet and also runs the browser extension is one person with one set
	// of obligations; two SeqPal IDs would say otherwise. Every wallet here has
	// been proven in the way its kind allows, and the account id never changes
	// when one is added, so every record pointing at it keeps pointing at it.
	//
	// Existing accounts are backfilled from what they signed up with, so each
	// one starts with exactly the wallet it already had.
	`
CREATE TABLE account_wallets (
    id          TEXT PRIMARY KEY,
    aid         TEXT NOT NULL,
    kind        TEXT NOT NULL,
    descriptor  TEXT NOT NULL DEFAULT '',
    xonly       TEXT NOT NULL DEFAULT '',
    enclave_aid TEXT NOT NULL DEFAULT '',
    label       TEXT NOT NULL DEFAULT '',
    proof       TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);
CREATE INDEX idx_account_wallets_aid ON account_wallets(aid);
CREATE UNIQUE INDEX idx_account_wallets_desc ON account_wallets(descriptor) WHERE descriptor != '';
CREATE UNIQUE INDEX idx_account_wallets_xonly ON account_wallets(xonly) WHERE xonly != '';

INSERT INTO account_wallets (id, aid, kind, descriptor, xonly, enclave_aid, label, proof, created_at)
SELECT lower(hex(randomblob(16))), aid, 'enclave', '', xonly, aid, 'Original wallet', 'migrated', created_at
  FROM accounts WHERE identity = 'aid' AND xonly != '' AND xonly NOT LIKE 'xpub:%';

INSERT INTO account_wallets (id, aid, kind, descriptor, xonly, enclave_aid, label, proof, created_at)
SELECT lower(hex(randomblob(16))), aid, 'descriptor', descriptor, '', '', 'Original wallet', 'migrated', created_at
  FROM accounts WHERE identity = 'xpub' AND descriptor != '';
`,
	// A descriptor names a key AND a script type, and only the key identifies the
	// wallet. Keyed on the descriptor as written, one wallet answered to the form
	// it was linked under and not to the other: signing in with the other form
	// found nothing, and registering then made a SECOND identity for one wallet,
	// which is the thing linking exists to prevent. The lookup key is the
	// normalised (pkh) form; the descriptor itself is kept as the holder gave it,
	// because that is what their wallet shows them.
	`
ALTER TABLE account_wallets ADD COLUMN descriptor_key TEXT NOT NULL DEFAULT '';
UPDATE account_wallets
   SET descriptor_key = replace(substr(descriptor, 1, instr(descriptor, '(') - 1), 'wpkh', 'pkh')
                        || substr(descriptor, instr(descriptor, '('))
 WHERE kind = 'descriptor' AND descriptor != '';
-- A checksum belongs to the text it was computed over, so the one carried in
-- from the other form is wrong for this one. toPKH drops it; this must too, or
-- a backfilled row is keyed on something no lookup will ever compute.
UPDATE account_wallets
   SET descriptor_key = substr(descriptor_key, 1, instr(descriptor_key, '#') - 1)
 WHERE descriptor_key LIKE '%#%';
DROP INDEX IF EXISTS idx_account_wallets_desc;
CREATE UNIQUE INDEX idx_account_wallets_desckey
    ON account_wallets(descriptor_key) WHERE descriptor_key != '';
`,
	// The first version of the backfill above carried the checksum across from
	// the form it converted, and a checksum belongs to the text it was computed
	// over. A row written that way is keyed on something no lookup will ever
	// compute, so the wallet is invisible: signing in with it reports it as
	// unregistered, which is how one wallet becomes two identities. Rows already
	// written that way are corrected here.
	`
UPDATE account_wallets
   SET descriptor_key = substr(descriptor_key, 1, instr(descriptor_key, '#') - 1)
 WHERE descriptor_key LIKE '%#%';
`,
	// One enclave per SeqPal ID, enforced where it cannot be raced. The handler
	// checks before inserting, and between those two statements a second request
	// can pass the same check: two enclave accounts on one ID, and no answer to
	// which one its restricted assets settle in. A partial unique index decides
	// it in the database, where there is no window.
	`
CREATE UNIQUE INDEX idx_account_wallets_one_enclave
    ON account_wallets(aid) WHERE kind = 'enclave';
`,
	// A document e-signature is anchored so that anyone can check it later, and
	// the record carried only the signer's x-only key. A SeqPal ID that is only
	// a wallet has none: it signs an ordinary message, which is checked against
	// an ADDRESS. Recorded with an empty key, such a signature is a string
	// nobody can verify -- which is the one thing an anchored signature has to
	// be. The address it verified against is recorded beside it.
	`
ALTER TABLE doc_signatures ADD COLUMN address TEXT NOT NULL DEFAULT '';
`,
}

// Store is seqpald's persistent state. Every financial fact the UI shows is
// read from here or from the chain, never asserted by the browser.
type Store struct {
	db      *sql.DB
	auditMu sync.Mutex // serializes the audit chain's read-head/append
}

func openStore(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// The PRAGMAs above are per connection, and the audit chain wants a single
	// writer anyway; one connection keeps both invariants without ceremony.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var version int
	err := s.db.QueryRow(`SELECT version FROM schema_version`).Scan(&version)
	switch {
	case err == sql.ErrNoRows:
		if _, err := s.db.Exec(`INSERT INTO schema_version (version) VALUES (0)`); err != nil {
			return err
		}
		version = 0
	case err != nil:
		return err
	}
	for i := version; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`UPDATE schema_version SET version = ?`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// --- records ---

type Account struct {
	AID         string          `json:"aid"`
	Kind        string          `json:"kind"`
	XOnly       string          `json:"xonly"`
	DisplayName string          `json:"display_name"`
	IDNumber    string          `json:"id_number,omitempty"`
	Profile     json.RawMessage `json:"profile"`
	CreatedAt   int64           `json:"created_at"`

	// Identity is what this account IS, which decides what it can reach:
	//   "aid"  an OpenAMP enclave account. XOnly is its enclave key and AID is
	//          the account id derived from it, so restricted assets can live in
	//          its 2-of-2 enclave.
	//   "xpub" a wallet identified by a descriptor it controls. There is no
	//          enclave, so OpenAMP restricted assets are out of reach until an
	//          enclave key is attached; everything else, supervised stocks and
	//          network-enforced (OpenDAMP) assets included, is not.
	Identity   string `json:"identity"`
	Descriptor string `json:"descriptor,omitempty"`
}

// HasEnclave reports whether this account can hold OpenAMP restricted assets.
// A wallet-backed account gains one by attaching an enclave key, at which point
// its identity becomes "aid" and nothing else about it changes.
func (a *Account) HasEnclave() bool { return a != nil && a.Identity != "xpub" }

type Entity struct {
	ID           string          `json:"id"`
	OwnerAID     string          `json:"owner_aid"`
	Name         string          `json:"name"`
	Jurisdiction string          `json:"jurisdiction"`
	Profile      json.RawMessage `json:"profile"`
	CreatedAt    int64           `json:"created_at"`
}

type Issuance struct {
	ID             string          `json:"id"`
	OwnerAID       string          `json:"owner_aid"`
	EntityID       string          `json:"entity_id,omitempty"`
	Name           string          `json:"name"`
	Ticker         string          `json:"ticker"`
	StructureID    string          `json:"structure_id"`
	Status         string          `json:"status"`
	Terms          json.RawMessage `json:"terms"`
	Supply         uint64          `json:"supply"`
	Precision      int             `json:"precision"`
	Clawback       bool            `json:"clawback"`
	AssetID        string          `json:"asset_id,omitempty"`
	Txid           string          `json:"txid,omitempty"`
	ContractHash   string          `json:"contract_hash,omitempty"`
	HolderAID      string          `json:"holder_aid,omitempty"`
	EnclaveAddress string          `json:"enclave_address,omitempty"`
	// IssuerExternal (M9) marks an issuance whose enclave issuer half is the
	// entity's own browser key (IssuerPubkey), so clawback runs two-phase and the
	// server never holds an issuer key for this asset. False (the default) is the
	// legacy server-held issuer key and the single-call clawback.
	IssuerExternal bool   `json:"issuer_external,omitempty"`
	IssuerPubkey   string `json:"issuer_pubkey,omitempty"`
	// Enforcement (M10) is the issuer's election: "serviced" (the co-signed
	// OpenAMP path, the default), "network" (OpenDAMP, gated on SEQPALD_DAMP and
	// minted at a holding key the issuer names), or "bearer" (a
	// consensus-supervised asset issued through the node's raw path). For a bearer asset the supervision descriptor is (operational key =
	// IssuerPubkey, RecoveryPubkey, SupervisionPause), committed in the asset id.
	Enforcement      string `json:"enforcement,omitempty"`
	RecoveryPubkey   string `json:"recovery_pubkey,omitempty"`
	SupervisionPause bool   `json:"supervision_pause,omitempty"`
	// M12, network enforcement only. The verifier asset and its fixed amount, the
	// policy commitment the deployed on-chain program is instantiated with, the
	// whitelist root that commitment covers, and the two addresses that matter to
	// a holder: where they hold the token, and where the rules themselves live.
	// All empty for every other election, so existing rows read unchanged.
	VerifierAsset           string `json:"verifier_asset,omitempty"`
	VerifierAmount          uint64 `json:"verifier_amount,omitempty"`
	PolicyCommitment        string `json:"policy_commitment,omitempty"`
	WhitelistRoot           string `json:"whitelist_root,omitempty"`
	HolderCovenantAddress   string `json:"holder_covenant_address,omitempty"`
	VerifierCovenantAddress string `json:"verifier_covenant_address,omitempty"`
	CreatedAt               int64  `json:"created_at"`
	UpdatedAt               int64  `json:"updated_at"`
}

// DampPrepare is the two-phase handoff for a network-enforced deploy: what the
// policy server fixed in phase 1, and the document the issuer's registrar runs
// its derivation against before phase 2 can complete. It holds no key material.
type DampPrepare struct {
	IssuanceID         string          `json:"issuance_id"`
	PrepareID          string          `json:"prepare_id"`
	AssetID            string          `json:"asset_id"`
	VerifierAsset      string          `json:"verifier_asset"`
	VerifierAmount     uint64          `json:"verifier_amount"`
	PolicyCommitment   string          `json:"policy_commitment"`
	WhitelistRoot      string          `json:"whitelist_root"`
	Whitelist          json.RawMessage `json:"whitelist"`
	DeriveDocument     json.RawMessage `json:"derive_document"`
	SnapshotSigMessage string          `json:"snapshot_sig_message,omitempty"`
	CreatedAt          int64           `json:"created_at"`
}

// PutDampPrepare records (or replaces) the handoff for an issuance.
func (s *Store) PutDampPrepare(p *DampPrepare) error {
	wl, dd := string(p.Whitelist), string(p.DeriveDocument)
	if wl == "" {
		wl = "[]"
	}
	if dd == "" {
		dd = "{}"
	}
	_, err := s.db.Exec(
		`INSERT INTO damp_prepares (issuance_id, prepare_id, asset_id, verifier_asset, verifier_amount,
            policy_commitment, whitelist_root, whitelist, derive_document, snapshot_sig_message, created_at)
         VALUES (?,?,?,?,?,?,?,?,?,?,?)
         ON CONFLICT(issuance_id) DO UPDATE SET
            prepare_id=excluded.prepare_id, asset_id=excluded.asset_id,
            verifier_asset=excluded.verifier_asset, verifier_amount=excluded.verifier_amount,
            policy_commitment=excluded.policy_commitment, whitelist_root=excluded.whitelist_root,
            whitelist=excluded.whitelist, derive_document=excluded.derive_document,
            snapshot_sig_message=excluded.snapshot_sig_message`,
		p.IssuanceID, p.PrepareID, p.AssetID, p.VerifierAsset, p.VerifierAmount,
		p.PolicyCommitment, p.WhitelistRoot, wl, dd, p.SnapshotSigMessage, p.CreatedAt)
	return err
}

// DampPrepare returns the handoff for an issuance, or (nil, nil) when there is
// none.
func (s *Store) DampPrepare(issuanceID string) (*DampPrepare, error) {
	var p DampPrepare
	var wl, dd string
	err := s.db.QueryRow(
		`SELECT issuance_id, prepare_id, asset_id, verifier_asset, verifier_amount,
                policy_commitment, whitelist_root, whitelist, derive_document, snapshot_sig_message, created_at
           FROM damp_prepares WHERE issuance_id = ?`, issuanceID).
		Scan(&p.IssuanceID, &p.PrepareID, &p.AssetID, &p.VerifierAsset, &p.VerifierAmount,
			&p.PolicyCommitment, &p.WhitelistRoot, &wl, &dd, &p.SnapshotSigMessage, &p.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.Whitelist = json.RawMessage(wl)
	p.DeriveDocument = json.RawMessage(dd)
	return &p, nil
}

// DeleteDampPrepare consumes a handoff once its issuance is live.
func (s *Store) DeleteDampPrepare(issuanceID string) error {
	_, err := s.db.Exec(`DELETE FROM damp_prepares WHERE issuance_id = ?`, issuanceID)
	return err
}

type DeployRecord struct {
	IdemKey      string `json:"-"`
	IssuanceID   string `json:"issuance_id"`
	AssetID      string `json:"asset"`
	Txid         string `json:"txid"`
	ContractHash string `json:"contract_hash"`
	AID          string `json:"aid"`
	Address      string `json:"address"`
	CreatedAt    int64  `json:"created_at"`
}

// --- accounts ---

// identityOrDefault keeps a row written before wallet-backed accounts existed
// reading as what it is: an enclave account.
func identityOrDefault(v string) string {
	if v == "xpub" {
		return "xpub"
	}
	return "aid"
}

// accounts.xonly is UNIQUE NOT NULL, which a wallet-backed account has nothing
// to put in: it has no enclave key. Storing an empty string would let exactly
// one such account exist and fail the second with a constraint error, so the
// column holds a placeholder that is unique per account and cannot be mistaken
// for a key (it is not 64 hex, and validXOnly rejects it). The Go value stays
// empty, so the API says what is true: no enclave key.
const noEnclaveKeyPrefix = "xpub:"

func storedXOnly(a *Account) string {
	if a.Identity == "xpub" && a.XOnly == "" {
		return noEnclaveKeyPrefix + a.AID
	}
	return a.XOnly
}

func loadedXOnly(v string) string {
	if strings.HasPrefix(v, noEnclaveKeyPrefix) {
		return ""
	}
	return v
}

func (s *Store) CreateAccount(a *Account) error {
	var idNum any
	if a.IDNumber != "" {
		idNum = a.IDNumber
	}
	_, err := s.db.Exec(
		`INSERT INTO accounts (aid, kind, xonly, display_name, id_number, profile, created_at, identity, descriptor) VALUES (?,?,?,?,?,?,?,?,?)`,
		a.AID, a.Kind, storedXOnly(a), a.DisplayName, idNum, string(rawOrEmpty(a.Profile)), a.CreatedAt,
		identityOrDefault(a.Identity), a.Descriptor)
	return err
}

func (s *Store) AccountByAID(aid string) (*Account, error) {
	return s.scanAccount(s.db.QueryRow(
		`SELECT aid, kind, xonly, display_name, COALESCE(id_number,''), profile, created_at, identity, COALESCE(descriptor,'') FROM accounts WHERE aid = ?`, aid))
}

// AttachEnclave binds an OpenAMP enclave key to a wallet-backed account. The
// account id does not change: it is the same SeqPal ID, with the enclave it was
// missing, so every record already pointing at it stays pointing at it.
func (s *Store) AttachEnclave(accountID, xonly string) error {
	_, err := s.db.Exec(
		`UPDATE accounts SET xonly = ?, identity = 'aid' WHERE aid = ? AND identity = 'xpub'`,
		xonly, accountID)
	return err
}

func (s *Store) AccountByXOnly(xonly string) (*Account, error) {
	return s.scanAccount(s.db.QueryRow(
		`SELECT aid, kind, xonly, display_name, COALESCE(id_number,''), profile, created_at, identity, COALESCE(descriptor,'') FROM accounts WHERE xonly = ?`, xonly))
}

func (s *Store) scanAccount(row *sql.Row) (*Account, error) {
	var a Account
	var profile string
	err := row.Scan(&a.AID, &a.Kind, &a.XOnly, &a.DisplayName, &a.IDNumber, &profile, &a.CreatedAt,
		&a.Identity, &a.Descriptor)
	a.XOnly = loadedXOnly(a.XOnly)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.Profile = json.RawMessage(profile)
	return &a, nil
}

// --- entities ---

func (s *Store) CreateEntity(e *Entity) error {
	_, err := s.db.Exec(
		`INSERT INTO entities (id, owner_aid, name, jurisdiction, profile, created_at) VALUES (?,?,?,?,?,?)`,
		e.ID, e.OwnerAID, e.Name, e.Jurisdiction, string(rawOrEmpty(e.Profile)), e.CreatedAt)
	return err
}

func (s *Store) EntitiesByOwner(aid string) ([]*Entity, error) {
	rows, err := s.db.Query(
		`SELECT id, owner_aid, name, jurisdiction, profile, created_at FROM entities WHERE owner_aid = ? ORDER BY created_at`, aid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Entity{}
	for rows.Next() {
		var e Entity
		var profile string
		if err := rows.Scan(&e.ID, &e.OwnerAID, &e.Name, &e.Jurisdiction, &profile, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Profile = json.RawMessage(profile)
		out = append(out, &e)
	}
	return out, rows.Err()
}

func (s *Store) EntityByID(id string) (*Entity, error) {
	var e Entity
	var profile string
	err := s.db.QueryRow(
		`SELECT id, owner_aid, name, jurisdiction, profile, created_at FROM entities WHERE id = ?`, id).
		Scan(&e.ID, &e.OwnerAID, &e.Name, &e.Jurisdiction, &profile, &e.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.Profile = json.RawMessage(profile)
	return &e, nil
}

// --- issuances ---

// issuanceCols deliberately omits the issuances.confidential column: it survives
// in the schema (additive migrations only) but confidentiality is a per-transfer
// choice now, so the issuance record neither writes nor reads it.
const issuanceCols = `id, owner_aid, COALESCE(entity_id,''), name, ticker, structure_id, status, terms,
    supply, precision, clawback, asset_id, txid, contract_hash, holder_aid, enclave_address,
    issuer_external, issuer_pubkey, enforcement, recovery_pubkey, supervision_pause,
    verifier_asset, verifier_amount, policy_commitment, whitelist_root,
    holder_covenant_address, verifier_covenant_address, created_at, updated_at`

func (s *Store) CreateIssuance(i *Issuance) error {
	var entity any
	if i.EntityID != "" {
		entity = i.EntityID
	}
	_, err := s.db.Exec(
		`INSERT INTO issuances (id, owner_aid, entity_id, name, ticker, structure_id, status, terms,
            supply, precision, clawback, created_at, updated_at)
         VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		i.ID, i.OwnerAID, entity, i.Name, i.Ticker, i.StructureID, i.Status, string(rawOrEmpty(i.Terms)),
		i.Supply, i.Precision, boolInt(i.Clawback), i.CreatedAt, i.UpdatedAt)
	return err
}

func (s *Store) IssuanceByID(id string) (*Issuance, error) {
	return scanIssuance(s.db.QueryRow(`SELECT `+issuanceCols+` FROM issuances WHERE id = ?`, id))
}

// LiveIssuances returns every deployed (on-chain) issuance. The chain watcher
// reconciles its watch rows from this set, so a restart resumes tracking from
// chain rather than resetting.
func (s *Store) LiveIssuances() ([]*Issuance, error) {
	rows, err := s.db.Query(`SELECT ` + issuanceCols + ` FROM issuances WHERE status = 'live' AND asset_id != '' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Issuance{}
	for rows.Next() {
		i, err := scanIssuanceRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) IssuancesByOwner(aid string) ([]*Issuance, error) {
	rows, err := s.db.Query(`SELECT `+issuanceCols+` FROM issuances WHERE owner_aid = ? ORDER BY created_at`, aid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Issuance{}
	for rows.Next() {
		i, err := scanIssuanceRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanIssuanceInto(sc scanner) (*Issuance, error) {
	var i Issuance
	var terms string
	var clawback, issuerExternal, supervisionPause int
	err := sc.Scan(&i.ID, &i.OwnerAID, &i.EntityID, &i.Name, &i.Ticker, &i.StructureID, &i.Status, &terms,
		&i.Supply, &i.Precision, &clawback, &i.AssetID, &i.Txid, &i.ContractHash,
		&i.HolderAID, &i.EnclaveAddress, &issuerExternal, &i.IssuerPubkey, &i.Enforcement,
		&i.RecoveryPubkey, &supervisionPause,
		&i.VerifierAsset, &i.VerifierAmount, &i.PolicyCommitment, &i.WhitelistRoot,
		&i.HolderCovenantAddress, &i.VerifierCovenantAddress,
		&i.CreatedAt, &i.UpdatedAt)
	if err != nil {
		return nil, err
	}
	i.Terms = json.RawMessage(terms)
	i.Clawback = clawback != 0
	i.IssuerExternal = issuerExternal != 0
	i.SupervisionPause = supervisionPause != 0
	return &i, nil
}

func scanIssuance(row *sql.Row) (*Issuance, error) {
	i, err := scanIssuanceInto(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return i, err
}

func scanIssuanceRows(rows *sql.Rows) (*Issuance, error) { return scanIssuanceInto(rows) }

// UpdateIssuanceFields applies a partial update. Callers pass only columns the
// caller is allowed to set; ownership is checked before this is reached.
func (s *Store) UpdateIssuanceFields(id string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	cols := make([]string, 0, len(fields)+1)
	for k := range fields {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	set := ""
	args := make([]any, 0, len(cols)+2)
	for _, c := range cols {
		if set != "" {
			set += ", "
		}
		set += c + " = ?"
		args = append(args, fields[c])
	}
	set += ", updated_at = ?"
	args = append(args, time.Now().Unix(), id)
	_, err := s.db.Exec(`UPDATE issuances SET `+set+` WHERE id = ?`, args...)
	return err
}

// --- deploys ---

func (s *Store) DeployByIdem(key string) (*DeployRecord, error) {
	var d DeployRecord
	err := s.db.QueryRow(
		`SELECT idem_key, issuance_id, asset_id, txid, contract_hash, aid, address, created_at FROM deploys WHERE idem_key = ?`, key).
		Scan(&d.IdemKey, &d.IssuanceID, &d.AssetID, &d.Txid, &d.ContractHash, &d.AID, &d.Address, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Store) InsertDeploy(d *DeployRecord) error {
	_, err := s.db.Exec(
		`INSERT INTO deploys (idem_key, issuance_id, asset_id, txid, contract_hash, aid, address, created_at)
         VALUES (?,?,?,?,?,?,?,?)`,
		d.IdemKey, d.IssuanceID, d.AssetID, d.Txid, d.ContractHash, d.AID, d.Address, d.CreatedAt)
	return err
}

// --- sessions ---

func (s *Store) CreateSession(aid string, ttl time.Duration) (string, int64, error) {
	token, err := randHex(32)
	if err != nil {
		return "", 0, err
	}
	now := time.Now().Unix()
	exp := now + int64(ttl.Seconds())
	if _, err := s.db.Exec(
		`INSERT INTO sessions (token, aid, created_at, expires_at) VALUES (?,?,?,?)`, token, aid, now, exp); err != nil {
		return "", 0, err
	}
	return token, exp, nil
}

// SessionAccount returns the account behind a live session token, or nil when
// the token is unknown or expired.
func (s *Store) SessionAccount(token string) (*Account, error) {
	var aid string
	err := s.db.QueryRow(`SELECT aid FROM sessions WHERE token = ? AND expires_at > ?`, token, time.Now().Unix()).Scan(&aid)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.AccountByAID(aid)
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}

func (s *Store) PurgeExpired() error {
	now := time.Now().Unix()
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, now); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM challenges WHERE expires_at <= ?`, now-3600)
	return err
}

// --- challenges ---

func (s *Store) CreateChallenge(xonly string, ttl time.Duration) (string, int64, error) {
	challenge, err := randHex(32)
	if err != nil {
		return "", 0, err
	}
	now := time.Now().Unix()
	exp := now + int64(ttl.Seconds())
	if _, err := s.db.Exec(
		`INSERT INTO challenges (challenge, xonly, created_at, expires_at) VALUES (?,?,?,?)`,
		challenge, xonly, now, exp); err != nil {
		return "", 0, err
	}
	return challenge, exp, nil
}

// ConsumeChallenge marks a challenge used, and only succeeds once: the UPDATE
// itself is the single-use guard, so two concurrent presentations cannot both
// pass. It also binds the challenge to the key it was issued for.
// PeekChallenge reports whether a challenge is live and bound to this id,
// WITHOUT spending it. A flow that sends the holder to another application to
// sign has to be able to fail and be retried: burning the challenge on a
// mistyped signature would make one bad paste cost them the whole exchange.
// Spending it is ConsumeChallenge's job, once the signature has verified.
func (s *Store) PeekChallenge(challenge, xonly string) error {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM challenges WHERE challenge = ? AND xonly = ? AND used = 0 AND expires_at > ?`,
		challenge, xonly, time.Now().Unix()).Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("this challenge has expired or was already used; ask for a new one")
	}
	return nil
}

func (s *Store) ConsumeChallenge(challenge, xonly string) error {
	res, err := s.db.Exec(
		`UPDATE challenges SET used = 1 WHERE challenge = ? AND xonly = ? AND used = 0 AND expires_at > ?`,
		challenge, xonly, time.Now().Unix())
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("challenge is unknown, expired, or already used")
	}
	return nil
}

// --- audit log ---

// Audit appends one hash-chained row. Rows are never updated or deleted:
// hash = sha256(prev_hash || ts || actor_aid || action || canonical(detail)).
func (s *Store) Audit(actorAID, action string, detail any) error {
	body, err := canonicalJSON(detail)
	if err != nil {
		return err
	}
	s.auditMu.Lock()
	defer s.auditMu.Unlock()

	var prev string
	err = s.db.QueryRow(`SELECT hash FROM audit_log ORDER BY seq DESC LIMIT 1`).Scan(&prev)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	ts := time.Now().Unix()
	h := sha256.New()
	h.Write([]byte(prev))
	h.Write([]byte(strconv.FormatInt(ts, 10)))
	h.Write([]byte(actorAID))
	h.Write([]byte(action))
	h.Write(body)
	hash := hex.EncodeToString(h.Sum(nil))
	_, err = s.db.Exec(
		`INSERT INTO audit_log (ts, actor_aid, action, detail, prev_hash, hash) VALUES (?,?,?,?,?,?)`,
		ts, actorAID, action, string(body), prev, hash)
	return err
}

// --- helpers ---

// canonicalJSON encodes v with object keys sorted lexicographically and no
// whitespace, so the same object always hashes to the same value in the SPA and
// here. Numbers keep the literal the caller supplied (JSON has no canonical
// number form); the SPA's JSON.stringify output is that literal.
func canonicalJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var norm any
	if err := dec.Decode(&norm); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, norm); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonicalString(buf, k); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := writeCanonical(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case string:
		return writeCanonicalString(buf, t)
	case json.Number:
		// Negative zero has one canonical form, and it is the one the browser
		// writes. JSON.stringify(-0) is "0"; Go's is "-0". Nothing in this
		// platform can produce a -0 through a browser, but the canonical form is
		// what a terms hash commits to on chain, and "the same document" must not
		// depend on which side hashed it.
		if n := t.String(); n == "-0" {
			buf.WriteString("0")
		} else {
			buf.WriteString(n)
		}
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case nil:
		buf.WriteString("null")
	default:
		return fmt.Errorf("canonical json: unsupported value %T", v)
	}
	return nil
}

// writeCanonicalString matches JSON.stringify: no HTML escaping of < > &.
func writeCanonicalString(buf *bytes.Buffer, s string) error {
	var tmp bytes.Buffer
	enc := json.NewEncoder(&tmp)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return err
	}
	buf.Write(bytes.TrimRight(tmp.Bytes(), "\n"))
	return nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func rawOrEmpty(r json.RawMessage) json.RawMessage {
	if len(r) == 0 {
		return json.RawMessage("{}")
	}
	return r
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
