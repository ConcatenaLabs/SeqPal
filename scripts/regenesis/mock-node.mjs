#!/usr/bin/env node
// mock-node.mjs — a minimal JSON-RPC stub, TEST HARNESS ONLY.
//
// openampd refuses to start without a reachable node RPC: server.New calls
// getblockhash 0 (for the genesis order) and, unless -feeasset is set,
// dumpassetlabels. This stub answers exactly those two methods so a bare
// openampd can boot on a throwaway LOCAL stack for the backup/restore + minimal
// regenesis drill. It performs NO issuance and is never used against the box.
//
// It listens on 127.0.0.1 at the port given by the first arg (default 18000).

import http from 'node:http';

const port = Number(process.argv[2] || 18000);
// A deterministic fake genesis hash (32 bytes hex). Not a real chain.
const GENESIS = '00'.repeat(31) + '01';
// USDX display hex used across SeqPal testnet; harmless as a label here.
const POLICY = '2a515539da5e6a60caa7766ecd65bac0c10d15717ddd2088844ba58f4d04b9de';

const server = http.createServer((req, res) => {
  let body = '';
  req.on('data', (c) => { body += c; });
  req.on('end', () => {
    let id = null, method = '';
    try { const j = JSON.parse(body || '{}'); id = j.id ?? null; method = j.method || ''; } catch { /* ignore */ }
    let result = null, error = null;
    switch (method) {
      case 'getblockhash': result = GENESIS; break;
      case 'dumpassetlabels': result = { bitcoin: POLICY }; break;
      case 'getblockchaininfo': result = { chain: 'regtest', blocks: 0, bestblockhash: GENESIS }; break;
      default: error = { code: -32601, message: `mock-node: unsupported method ${method}` };
    }
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ result, error, id }));
  });
});
server.listen(port, '127.0.0.1', () => {
  process.stderr.write(`[mock-node] listening on 127.0.0.1:${port}\n`);
});
