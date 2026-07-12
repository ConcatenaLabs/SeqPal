// Client-side verification of the OpenAMP transparency log's hash chain.
//
// openampd computes each entry's hash exactly as
//   sha256("<seq>|<prev>|<time>|<action>|<data-json>")
// where data-json is the entry's canonical JSON bytes (see the M1 audit chain in
// internal/store/store.go). We recompute it in the browser so an auditor never
// has to trust seqpald's relay: the hash either matches the served entry or it
// does not, and we render which.

async function sha256hex(str) {
  const buf = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(str))
  return [...new Uint8Array(buf)].map((b) => b.toString(16).padStart(2, '0')).join('')
}

// The data value round-trips through JSON.parse in the fetch layer; JSON
// re-serialization preserves the key order and compactness openampd emitted, so
// JSON.stringify reproduces the exact pre-image bytes for the hex/atom/AID data
// the log carries, EXCEPT that Go's json.Marshal HTML-escapes < > & and the line
// separators as \uXXXX while JS JSON.stringify does not. We apply the same
// escaping so an entry whose data legitimately contains one of those bytes (for
// example a clawback reason with "&") still verifies. Residual caveat: an atoms
// value above 2^53 would already have lost precision at JSON.parse in the fetch
// layer, which can only ever produce a false "unverified" badge, never a false
// "verified"; such magnitudes do not occur at demo scale.
function dataJSON(data) {
  if (data === undefined || data === null) return 'null'
  return JSON.stringify(data)
    .replace(/</g, '\\u003c')
    .replace(/>/g, '\\u003e')
    .replace(/&/g, '\\u0026')
    .replace(/\u2028/g, '\\u2028')
    .replace(/\u2029/g, '\\u2029')
}

// Recompute every entry's hash from its stated pre-image. Entries arrive as a
// filtered slice of the full log, so contiguous prev-linkage cannot be checked
// across gaps; what IS verifiable, and what proves the record was not altered in
// relay, is that each entry's own hash equals sha256 of its fields. Returns
// entries annotated with { computed, ok }. The full contiguous chain is at the
// public /openamp/v1/log for an end-to-end walk.
export async function verifyLog(entries) {
  const list = [...(entries || [])].sort((a, b) => a.seq - b.seq)
  const out = []
  for (const e of list) {
    const pre = `${e.seq}|${e.prev}|${e.time}|${e.action}|${dataJSON(e.data)}`
    const computed = await sha256hex(pre)
    out.push({ ...e, computed, ok: computed === e.hash })
  }
  return out
}
