// Lightweight inline icon set (stroke-based, currentColor).
const base = {
  width: 20,
  height: 20,
  viewBox: '0 0 24 24',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 1.8,
  strokeLinecap: 'round',
  strokeLinejoin: 'round',
}

export const Icon = {
  bolt: (p) => (
    <svg {...base} {...p}><path d="M13 2 4 14h7l-1 8 9-12h-7l1-8Z" /></svg>
  ),
  shield: (p) => (
    <svg {...base} {...p}><path d="M12 3 5 6v6c0 4 3 7 7 9 4-2 7-5 7-9V6l-7-3Z" /><path d="m9 12 2 2 4-4" /></svg>
  ),
  users: (p) => (
    <svg {...base} {...p}><circle cx="9" cy="8" r="3" /><path d="M3 20c0-3 3-5 6-5s6 2 6 5" /><path d="M16 6a3 3 0 0 1 0 6" /><path d="M21 20c0-2.5-1.8-4.2-4-4.8" /></svg>
  ),
  bitcoin: (p) => (
    <svg {...base} {...p}><circle cx="12" cy="12" r="9" /><path d="M9.5 8h4a2 2 0 0 1 0 4h-4Zm0 4h4.3a2 2 0 0 1 0 4H9.5Zm0-4V6.5m0 11.5V16m2-9.5V8m0 8v1.5" /></svg>
  ),
  layers: (p) => (
    <svg {...base} {...p}><path d="m12 3 9 5-9 5-9-5 9-5Z" /><path d="m3 13 9 5 9-5" /></svg>
  ),
  id: (p) => (
    <svg {...base} {...p}><rect x="3" y="5" width="18" height="14" rx="2" /><circle cx="8.5" cy="11" r="2" /><path d="M5.5 16c.6-1.5 1.7-2 3-2s2.4.5 3 2" /><path d="M14 9h4M14 12h4M14 15h2.5" /></svg>
  ),
  exchange: (p) => (
    <svg {...base} {...p}><path d="M4 8h13l-3-3M20 16H7l3 3" /></svg>
  ),
  doc: (p) => (
    <svg {...base} {...p}><path d="M7 3h7l5 5v13a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1Z" /><path d="M14 3v5h5" /><path d="M9 13h6M9 16h6M9 10h2" /></svg>
  ),
  check: (p) => (
    <svg {...base} {...p}><path d="m5 12 5 5 9-11" /></svg>
  ),
  arrowRight: (p) => (
    <svg {...base} {...p}><path d="M5 12h14M13 6l6 6-6 6" /></svg>
  ),
  arrowLeft: (p) => (
    <svg {...base} {...p}><path d="M19 12H5M11 6l-6 6 6 6" /></svg>
  ),
  spark: (p) => (
    <svg {...base} {...p}><path d="M12 3v4M12 17v4M3 12h4M17 12h4M6 6l2.5 2.5M15.5 15.5 18 18M18 6l-2.5 2.5M8.5 15.5 6 18" /></svg>
  ),
  building: (p) => (
    <svg {...base} {...p}><rect x="4" y="3" width="10" height="18" rx="1" /><path d="M14 8h6v13H4" /><path d="M7 7h2M7 11h2M7 15h2M17 12h0M17 16h0" /></svg>
  ),
  briefcase: (p) => (
    <svg {...base} {...p}><rect x="3" y="7" width="18" height="13" rx="2" /><path d="M8 7V5a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2M3 12h18" /></svg>
  ),
  coins: (p) => (
    <svg {...base} {...p}><ellipse cx="9" cy="7" rx="6" ry="3" /><path d="M3 7v5c0 1.7 2.7 3 6 3s6-1.3 6-3V7" /><path d="M9 15v2c0 1.7 2.7 3 6 3s6-1.3 6-3v-5c0-1.4-1.8-2.5-4.3-2.9" /></svg>
  ),
  receipt: (p) => (
    <svg {...base} {...p}><path d="M5 3h14v18l-2.5-1.5L14 21l-2-1.5L10 21l-2.5-1.5L5 21V3Z" /><path d="M9 8h6M9 12h6" /></svg>
  ),
  lock: (p) => (
    <svg {...base} {...p}><rect x="5" y="11" width="14" height="9" rx="2" /><path d="M8 11V8a4 4 0 0 1 8 0v3" /></svg>
  ),
  globe: (p) => (
    <svg {...base} {...p}><circle cx="12" cy="12" r="9" /><path d="M3 12h18M12 3c2.5 2.5 2.5 15 0 18M12 3c-2.5 2.5-2.5 15 0 18" /></svg>
  ),
  clock: (p) => (
    <svg {...base} {...p}><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></svg>
  ),
  tag: (p) => (
    <svg {...base} {...p}><path d="M3 12V4a1 1 0 0 1 1-1h8l9 9-9 9-9-9Z" /><circle cx="7.5" cy="7.5" r="1.3" /></svg>
  ),
  wallet: (p) => (
    <svg {...base} {...p}><rect x="3" y="6" width="18" height="13" rx="2" /><path d="M3 10h18M16 14h2" /></svg>
  ),
  external: (p) => (
    <svg {...base} {...p}><path d="M14 4h6v6M20 4l-9 9M19 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1h5" /></svg>
  ),
  menu: (p) => (
    <svg {...base} {...p}><path d="M4 7h16M4 12h16M4 17h16" /></svg>
  ),
  close: (p) => (
    <svg {...base} {...p}><path d="M6 6l12 12M18 6 6 18" /></svg>
  ),
  upload: (p) => (
    <svg {...base} {...p}><path d="M12 16V4m0 0L8 8m4-4 4 4" /><path d="M4 16v3a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-3" /></svg>
  ),
}

export const StructureIcon = {
  equity: Icon.building,
  spv: Icon.briefcase,
  debt: Icon.coins,
  dr: Icon.receipt,
}
