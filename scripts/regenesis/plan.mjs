#!/usr/bin/env node
// plan.mjs — turn a books.json export into a regenesis plan.json.
//
// The plan is the deterministic recovery blueprint the operator applies against
// a freshly reset chain. It computes:
//   - users:   re-register each stored AID's enclave pubkeys, and the category
//              set to re-stamp (projected from the stored claims, mirroring
//              seqpald/taxonomy.go projectCategories).
//   - assets:  a re-issue request per issuance, from the STORED terms, disclosed
//              as a NEW asset (the old asset id is carried only as a cross
//              reference; a reset chain cannot preserve it).
//   - holders: a books-derived reconstruction of who held how much, from settled
//              subscriptions adjusted by completed P2P transfers, swept
//              clawbacks, and broadcast DR mint/redeem ops. This is the re-mint
//              target. It is what the books know; on-chain movements the platform
//              never recorded are NOT here (see REGENESIS.md, "recovered vs lost").
//   - listings: the issuer-granted venue authorizations to re-assert.
//
// No secrets. Reads books.json from --in or stdin; writes plan.json to --out or
// stdout. Pure transform: no network, no chain.
//
// Usage:
//   node plan.mjs --in books.json --out plan.json
//   node export-books.sh | node plan.mjs > plan.json

import { readFileSync, writeFileSync } from 'node:fs';

function arg(name, def) {
  const i = process.argv.indexOf(`--${name}`);
  return i >= 0 && process.argv[i + 1] ? process.argv[i + 1] : def;
}

// --- category projection: mirror of seqpald/taxonomy.go projectCategories ---
const ELIG_TIERS = new Set(['ret', 'acc', 'pro', 'hnw', 'soph']);
const VOCAB_VERSION = 1;

function normalizeResidence(r) {
  r = String(r || '').trim().toUpperCase();
  if (r === 'HN-PRO') return r;
  if (r.length === 2 && /^[A-Z]{2}$/.test(r)) return r;
  return '';
}
function addToken(set, iso2, elig) {
  if (!ELIG_TIERS.has(elig)) return;
  set.add(`j:${iso2}:${elig}`);
}
function projectCategories(c, now) {
  if (!c || c.status !== 'verified') return [];
  if (c.valid_until > 0 && now >= c.valid_until) return [];
  const res = normalizeResidence(c.residence);
  if (!res) return [];
  const set = new Set();
  const base = c.base_eligibility === 'pro' ? 'pro' : 'ret';
  addToken(set, res, base);
  const accredValid = c.accredited && c.accred_artifact &&
    (c.accred_valid_until === 0 || now < c.accred_valid_until);
  if (accredValid) addToken(set, res, 'acc');
  if (res === 'GB') {
    if (c.gb_hnw) addToken(set, res, 'hnw');
    if (c.gb_soph) addToken(set, res, 'soph');
  }
  if (c.us_person && res !== 'US') {
    addToken(set, 'US', 'ret');
    if (accredValid) addToken(set, 'US', 'acc');
  }
  return [...set].sort();
}

// --- holders reconstruction from the books ---------------------------------
function reconstructHolders(books) {
  const assetToIssuance = {};
  for (const iss of books.issuances || []) {
    if (iss.old_asset_id) assetToIssuance[iss.old_asset_id] = iss.id;
  }
  // holdings[issuance_id][aid] = atoms
  const h = {};
  const add = (iss, aid, atoms) => {
    if (!iss || !aid) return;
    h[iss] = h[iss] || {};
    h[iss][aid] = (h[iss][aid] || 0) + Number(atoms);
  };
  for (const s of books.settled_subscriptions || [])
    add(s.issuance_id, s.investor_aid, s.token_atoms);
  for (const t of books.p2p_transfers || []) {
    add(t.issuance_id, t.originator_aid, -Number(t.atoms));
    add(t.issuance_id, t.beneficiary_aid, Number(t.atoms));
  }
  for (const c of books.clawbacks || [])
    add(assetToIssuance[c.asset_id], c.holder_aid, -Number(c.atoms));
  for (const d of books.dr_ops || []) {
    if (d.kind === 'mint') add(d.issuance_id, d.target_aid, Number(d.atoms));
    else if (d.kind === 'redeem') add(d.issuance_id, d.holder_aid, -Number(d.atoms));
  }
  // Emit only positive balances; drop dust/zero/negative (fully clawed or
  // transferred out). Negatives would signal books the reconstruction cannot
  // fully explain; they are surfaced in the plan as a warning, not minted.
  const holders = {};
  const warnings = [];
  for (const [iss, byAid] of Object.entries(h)) {
    holders[iss] = {};
    for (const [aid, atoms] of Object.entries(byAid)) {
      if (atoms > 0) holders[iss][aid] = atoms;
      else if (atoms < 0)
        warnings.push(`negative reconstructed balance issuance=${iss} aid=${aid} atoms=${atoms} (dropped; books incomplete for this holder)`);
    }
  }
  return { holders, warnings };
}

// --- main -------------------------------------------------------------------
const inPath = arg('in');
const raw = inPath ? readFileSync(inPath, 'utf8') : readFileSync(0, 'utf8');
const books = JSON.parse(raw);
const now = Math.floor(Date.now() / 1000);

const claimsByAid = {};
for (const c of books.claims || []) claimsByAid[c.aid] = c;

const users = (books.accounts || []).map((a) => ({
  aid: a.aid,
  kind: a.kind,
  pubkeys: [a.xonly],
  categories: projectCategories(claimsByAid[a.aid], now),
}));

const { holders, warnings } = reconstructHolders(books);

const assets = (books.issuances || [])
  .filter((iss) => iss.old_asset_id) // only assets that were actually deployed
  .map((iss) => {
    let terms = {};
    try { terms = JSON.parse(iss.terms || '{}'); } catch { /* keep {} */ }
    const remint = holders[iss.id] || {};
    const remintTotal = Object.values(remint).reduce((a, b) => a + b, 0);
    return {
      issuance_id: iss.id,
      old_asset_id: iss.old_asset_id, // cross-reference only; NOT reused
      disclosed: 'new', // a reset chain mints a NEW asset id; disclosed as new
      name: iss.name,
      ticker: iss.ticker,
      precision: iss.precision,
      supply_atoms: iss.supply,
      confidential: !!iss.confidential,
      clawback: !!iss.clawback,
      issuer_aid: iss.owner_aid,
      issuer_external: !!iss.issuer_external,
      issuer_pubkey: iss.issuer_pubkey || '',
      terms,
      remint: remint, // aid -> atoms, from the books-derived holders snapshot
      remint_total_atoms: remintTotal,
    };
  });

const plan = {
  generated_at: new Date().toISOString(),
  vocab_version: VOCAB_VERSION,
  disclosure:
    'Regenesis mints NEW asset ids on the reset chain. Old asset ids are cross-references only and are permanently retired. On-chain UTXOs from the old chain are not recoverable.',
  users,
  assets,
  listings: (books.listings || []).map((l) => ({
    issuance_id: l.issuance_id,
    old_asset_id: l.old_asset_id,
    ticker: l.ticker,
    name: l.name,
    venues: (() => { try { return JSON.parse(l.venues || '[]'); } catch { return []; } })(),
  })),
  warnings,
  counts: {
    users: users.length,
    assets: assets.length,
    holders_total_positions: Object.values(holders).reduce(
      (n, m) => n + Object.keys(m).length, 0),
    listings: (books.listings || []).length,
  },
};

const outPath = arg('out');
const out = JSON.stringify(plan, null, 2);
if (outPath) { writeFileSync(outPath, out + '\n'); process.stderr.write(`[regenesis] plan written: ${outPath}\n`); }
else process.stdout.write(out + '\n');
