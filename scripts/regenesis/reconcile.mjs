#!/usr/bin/env node
// reconcile.mjs — produce the regenesis reconciliation report.
//
// Cross-references the plan against what was applied and emits both a machine
// readable reconciliation.json and a human-readable reconciliation.md:
//   - old asset id  ->  new asset id  (per issuance; "pending" in a dry-run)
//   - holder deltas: planned remint total vs the distribution manifest total
//   - a recovered-vs-lost summary, stated plainly
//   - any warnings carried from planning (e.g. books that did not fully explain
//     a holder's balance)
//
// Pure transform, no network. Reads plan.json (+ optional applied.json and
// distribution-manifest.json). No secrets.
//
// Usage:
//   node reconcile.mjs --plan plan.json [--applied applied.json] \
//     [--dist distribution-manifest.json] [--out-json reconciliation.json] \
//     [--out-md reconciliation.md]

import { readFileSync, writeFileSync } from 'node:fs';

function arg(name, def) {
  const i = process.argv.indexOf(`--${name}`);
  return i >= 0 && process.argv[i + 1] ? process.argv[i + 1] : def;
}
function readJSON(p, def) {
  try { return JSON.parse(readFileSync(p, 'utf8')); } catch { return def; }
}

const plan = readJSON(arg('plan', 'plan.json'), null);
if (!plan) { process.stderr.write('reconcile: plan.json required\n'); process.exit(1); }
const applied = readJSON(arg('applied', 'applied.json'), []);
const dist = readJSON(arg('dist', 'distribution-manifest.json'), []);

const appliedByIss = {};
for (const a of applied) appliedByIss[a.issuance_id] = a;
const distByIss = {};
for (const d of dist) {
  distByIss[d.issuance_id] = (distByIss[d.issuance_id] || 0) + Number(d.atoms);
}

const assetRows = plan.assets.map((a) => {
  const ap = appliedByIss[a.issuance_id];
  const plannedRemint = a.remint_total_atoms || 0;
  const distTotal = distByIss[a.issuance_id] ?? plannedRemint;
  return {
    issuance_id: a.issuance_id,
    ticker: a.ticker,
    old_asset_id: a.old_asset_id,
    new_asset_id: ap?.new_asset_id || 'pending (not broadcast)',
    issue_txid: ap?.issue_txid || 'pending',
    supply_atoms: a.supply_atoms,
    planned_remint_atoms: plannedRemint,
    distribution_atoms: distTotal,
    remint_matches: distTotal === plannedRemint,
    holder_positions: Object.keys(a.remint || {}).length,
  };
});

const report = {
  generated_at: new Date().toISOString(),
  disclosure: plan.disclosure,
  counts: plan.counts,
  asset_mapping: assetRows,
  warnings: plan.warnings || [],
  recovered: [
    'User identities (AIDs) and their enclave pubkeys, re-registered deterministically (same pubkeys yield the same AID).',
    'Eligibility categories, re-projected from the stored claims and re-stamped.',
    'Asset definitions and stored terms/rules, re-issued as NEW assets.',
    'The books-derived holder balances (settled subscriptions adjusted by recorded P2P transfers, swept clawbacks, and DR ops).',
    'Issuer-granted listing authorizations, re-assertable by the issuer.',
  ],
  lost: [
    'All old-chain asset ids: a reset chain cannot reproduce them. New ids are minted and disclosed as new.',
    'All old-chain UTXOs and on-chain history (issue/transfer/clawback txids, anchors, confirmations).',
    'Any holdings that moved by a wallet-native transfer the platform never recorded: only book-recorded positions are reconstructed.',
    'Live session state, challenge nonces, and any in-flight (unsettled) subscriptions or transfers.',
  ],
};

const jsonOut = arg('out-json');
if (jsonOut) writeFileSync(jsonOut, JSON.stringify(report, null, 2) + '\n');
else process.stdout.write(JSON.stringify(report, null, 2) + '\n');

const md = [];
md.push('# SeqPal regenesis reconciliation report');
md.push('');
md.push(`Generated: ${report.generated_at}`);
md.push('');
md.push(`> ${report.disclosure}`);
md.push('');
md.push('## Old asset id -> new asset id');
md.push('');
md.push('| Issuance | Ticker | Old asset id | New asset id | Issue txid | Supply | Re-mint | Positions | Match |');
md.push('|---|---|---|---|---|---|---|---|---|');
for (const r of assetRows) {
  md.push(`| ${r.issuance_id} | ${r.ticker} | ${r.old_asset_id} | ${r.new_asset_id} | ${r.issue_txid} | ${r.supply_atoms} | ${r.distribution_atoms} | ${r.holder_positions} | ${r.remint_matches ? 'yes' : 'NO'} |`);
}
md.push('');
md.push('## Recovered');
md.push('');
for (const x of report.recovered) md.push(`- ${x}`);
md.push('');
md.push('## Permanently lost');
md.push('');
for (const x of report.lost) md.push(`- ${x}`);
if (report.warnings.length) {
  md.push('');
  md.push('## Warnings');
  md.push('');
  for (const w of report.warnings) md.push(`- ${w}`);
}
md.push('');
const mdOut = arg('out-md');
if (mdOut) { writeFileSync(mdOut, md.join('\n')); process.stderr.write(`[regenesis] wrote ${mdOut}\n`); }
