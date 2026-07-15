// Export a self-contained, deployable placement-portal bundle.
//
// This is the real artifact the self-host model produces: a single static
// index.html the issuer deploys to their own web host, under their own domain,
// in their own name. It renders the issuer's branded offering shell from the
// config baked in at export time (so it renders with zero JS, even opened as a
// local file), progressively refreshes the teaser from SeqPal's public offering
// endpoint when it is reachable, and sends investors to the live SeqPal ID gate
// to verify and subscribe.
//
// The in-app /portal/:id preview stays as a convenience; this is what the issuer
// would actually host. Nothing here is a SeqPal-hosted page: the file is the
// issuer's, it only calls SeqPal's public APIs.
import { getStructure } from '../data/structures'
import { unitSymbol } from './economics'

const ACCENT_HEX = {
  btc: '#F7931A',
  seq: '#27c2c9',
  indigo: '#4f46e5',
  emerald: '#059669',
  slate: '#334155',
}

// HTML-escape a value going into text or an attribute in the generated file.
function esc(s) {
  return String(s ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

// The values the bundle needs, resolved from the issuance and the portal config.
export function bundleModel(iss, cfg) {
  const origin = window.location.origin
  // The SeqPal app mount (e.g. "/seqpal/") so links point at the live gate.
  const env = (import.meta && import.meta.env) || {}
  const base = (env.BASE_URL || '/').replace(/\/+$/, '')
  const structure = getStructure(iss.structureId)
  const operator = `${iss.entityName || iss.name || 'New Próspera'} LLC`
  const slug = cfg.slug || 'your-name'
  return {
    origin,
    base,
    accentHex: ACCENT_HEX[cfg.accent] || ACCENT_HEX.btc,
    brandName: cfg.brandName || operator,
    headline: cfg.headline || `Invest in ${iss.name}`,
    operator,
    domain: `invest.${slug}.com`,
    slug,
    ticker: iss.ticker || '',
    structureName: structure?.name || 'Offering',
    minInvestment: cfg.minInvestment || '',
    unitSym: unitSymbol(iss.unit),
    docs: Array.isArray(cfg.docs) ? cfg.docs : [],
    escrow: !!cfg.escrowRequested,
    // Live endpoints the deployed portal talks to (public offering teaser + the
    // SeqPal ID gate). Same-origin in the demo; in production SeqPal allowlists
    // the issuer's domain (CORS) so these work from the issuer's own host.
    offeringUrl: `${origin}${base}/api/issuances/${encodeURIComponent(iss.id)}/offering`,
    gateUrl: `${origin}${base}/id?next=/portal/${encodeURIComponent(iss.id)}`,
    issuanceId: iss.id,
  }
}

export function buildPortalHtml(iss, cfg) {
  const m = bundleModel(iss, cfg)
  const teaser = [
    ['Issuer', m.operator],
    ['Structure', m.structureName],
    ['Ticker', m.ticker || '—'],
    ['Available in', 'By verified jurisdiction'],
  ]
  const config = {
    issuanceId: m.issuanceId,
    offeringUrl: m.offeringUrl,
    gateUrl: m.gateUrl,
  }
  // A plain-string inline script (no backticks / no ${}) so it survives inside
  // this outer template literal. It reads the baked config and, best-effort,
  // refreshes the teaser from the public offering endpoint when reachable.
  const enhance =
    "(function(){try{var c=JSON.parse(document.getElementById('seqpal-config').textContent);" +
    "fetch(c.offeringUrl,{credentials:'omit'}).then(function(r){return r.ok?r.json():null;}).then(function(o){" +
    "if(!o||!o.teaser)return;var t=o.teaser;var set=function(id,v){var e=document.getElementById(id);if(e&&v)e.textContent=v;};" +
    "set('t-issuer',t.issuer);set('t-structure',t.structure);" +
    "if(Array.isArray(t.available_in)&&t.available_in.length)set('t-available',t.available_in.join(', '));" +
    "}).catch(function(){});}catch(e){}})();"

  const deployNote =
    '<!--\n' +
    '  Your SeqPal placement portal (exported bundle).\n\n' +
    '  Host this file on YOUR OWN web host, under YOUR OWN domain (' + m.domain + '),\n' +
    '  in your name. You are the legal operator of this portal.\n\n' +
    '  It renders your branded offering shell on its own, and sends investors to the\n' +
    '  SeqPal ID gate to verify and subscribe. In production, SeqPal allowlists your\n' +
    '  domain (CORS) so the offering teaser, eligibility gate, and subscription flow\n' +
    '  run on this page against SeqPal\'s public APIs. Optional escrow, if you hire it,\n' +
    '  is an RFSA-licensed SeqPal service; without it, subscriptions go directly to you.\n\n' +
    '  SeqPal supplies this software and the SeqPal ID gate. SeqPal does not host or\n' +
    '  operate this portal and is not a party to your investor terms of service.\n' +
    '-->\n'

  return (
    deployNote +
    '<!doctype html>\n<html lang="en">\n<head>\n<meta charset="utf-8">\n' +
    '<meta name="viewport" content="width=device-width, initial-scale=1">\n' +
    `<title>${esc(m.brandName)} — invest in ${esc(iss.name)}</title>\n` +
    `<script id="seqpal-config" type="application/json">${JSON.stringify(config)}</script>\n` +
    '<style>\n' +
    `:root{--accent:${m.accentHex};}\n` +
    '*{box-sizing:border-box;margin:0;padding:0}' +
    'body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;color:#0f172a;background:#f7f8fa;line-height:1.5}' +
    '.wrap{max-width:960px;margin:0 auto;padding:0 20px}' +
    'header{background:#fff;border-bottom:1px solid rgba(15,23,42,.08)}' +
    '.hrow{display:flex;align-items:center;justify-content:space-between;height:64px}' +
    '.brand{display:flex;align-items:center;gap:10px;font-weight:700}' +
    '.mark{width:32px;height:32px;border-radius:8px;background:var(--accent);color:#fff;display:flex;align-items:center;justify-content:center;font-weight:700}' +
    '.muted{color:rgba(15,23,42,.55);font-size:12px}' +
    'main{padding:40px 0}' +
    '.tag{display:inline-block;font-size:12px;font-weight:600;color:rgba(15,23,42,.6);background:rgba(15,23,42,.05);border-radius:999px;padding:3px 10px}' +
    'h1{font-size:30px;font-weight:800;letter-spacing:-.02em;margin-top:12px}' +
    '.sub{color:rgba(15,23,42,.7);margin-top:6px}' +
    '.grid{display:grid;grid-template-columns:1fr;gap:24px;margin-top:28px}' +
    '@media(min-width:820px){.grid{grid-template-columns:1.5fr 1fr}}' +
    '.cards{display:grid;grid-template-columns:1fr 1fr;gap:12px}' +
    '@media(min-width:560px){.cards{grid-template-columns:1fr 1fr 1fr}}' +
    '.card{background:#fff;border:1px solid rgba(15,23,42,.1);border-radius:12px;padding:16px}' +
    '.k{font-size:12px;color:rgba(15,23,42,.6)}' +
    '.v{font-weight:700;margin-top:2px}' +
    '.locked{margin-top:28px;border:1px dashed rgba(15,23,42,.2);background:#fff;border-radius:16px;padding:24px}' +
    '.locked h2{font-size:16px}' +
    '.locked p{font-size:14px;color:rgba(15,23,42,.7);margin-top:8px}' +
    '.gate{border:1px solid rgba(15,23,42,.1);border-radius:16px;overflow:hidden;background:#fff}' +
    '.gate .top{background:#0f172a;color:#fff;padding:14px 20px;font-weight:600;font-size:14px}' +
    '.gate .body{padding:20px}' +
    '.gate p{font-size:14px;color:rgba(15,23,42,.8)}' +
    '.btn{display:block;text-align:center;text-decoration:none;background:var(--accent);color:#fff;font-weight:600;border-radius:10px;padding:11px 16px;margin-top:16px}' +
    '.fine{font-size:11px;color:rgba(15,23,42,.5);margin-top:12px;text-align:center}' +
    'footer{border-top:1px solid rgba(15,23,42,.1);margin-top:48px;padding:24px 0;color:rgba(15,23,42,.55);font-size:12px;text-align:center}' +
    '</style>\n</head>\n<body>\n' +
    '<header><div class="wrap hrow">' +
    `<div class="brand"><span class="mark">${esc(m.brandName.slice(0, 1).toUpperCase())}</span>${esc(m.brandName)}</div>` +
    '<span class="muted">Powered by SeqPal on Sequentia</span>' +
    '</div></header>\n' +
    '<main class="wrap"><div class="grid">\n' +
    '<div>' +
    `<span class="tag">${esc(m.structureName)}</span>` +
    `<h1>${esc(m.headline)}</h1>` +
    `<p class="sub">${esc(iss.name)}${m.ticker ? ' · ' + esc(m.ticker) : ''} · issued on Sequentia, a Bitcoin sidechain</p>` +
    '<div class="cards">' +
    teaser
      .map((row, i) => {
        const id =
          i === 0 ? 't-issuer' : i === 1 ? 't-structure' : i === 2 ? 't-ticker-label' : 't-available'
        return `<div class="card"><div class="k">${esc(row[0])}</div><div class="v" id="${id}">${esc(row[1])}</div></div>`
      })
      .join('') +
    '</div>' +
    '<div class="locked"><h2>Verification required to view details</h2>' +
    '<p>Price, target raise, offering documents and subscription mechanics are shown only to a verified investor whose jurisdiction and eligibility clear this offering&#39;s policy. This is not a general solicitation.</p></div>' +
    '</div>\n' +
    '<div><div class="gate"><div class="top">SeqPal ID gate</div><div class="body">' +
    '<p>Verify with a SeqPal ID to check your eligibility for this offering and subscribe. The rail you pay with is always your choice, never set by your jurisdiction.</p>' +
    `<a class="btn" href="${esc(m.gateUrl)}">Verify with SeqPal ID to invest</a>` +
    '</div></div>' +
    '<p class="fine">Nothing here is an offer to sell or a solicitation to buy any security.</p></div>\n' +
    '</div></main>\n' +
    `<footer>Operated by ${esc(m.operator)} · powered by SeqPal · ${esc(m.domain)}</footer>\n` +
    `<script>${enhance}</script>\n` +
    '</body>\n</html>\n'
  )
}

// Trigger a download of the exported bundle from the browser.
export function downloadPortalBundle(iss, cfg) {
  const html = buildPortalHtml(iss, cfg)
  const blob = new Blob([html], { type: 'text/html;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  const slug = (cfg.slug || 'portal').replace(/[^a-z0-9-]/gi, '') || 'portal'
  a.download = `invest-${slug}-portal.html`
  document.body.appendChild(a)
  a.click()
  a.remove()
  // Revoke on the next tick so the download has started.
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

// Open the exported bundle in a new tab so the issuer can see the deployed
// file itself (distinct from the in-app /portal/:id preview).
export function openPortalBundle(iss, cfg) {
  const html = buildPortalHtml(iss, cfg)
  const blob = new Blob([html], { type: 'text/html;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  window.open(url, '_blank', 'noopener,noreferrer')
  setTimeout(() => URL.revokeObjectURL(url), 60000)
}
