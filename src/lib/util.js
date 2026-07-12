// Pure, framework-free helpers (no React/JSX) so they can be unit-tested and
// reused without importing the store/context module.

export function slugify(s) {
  return (s || '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/(^-|-$)/g, '')
    .slice(0, 24)
}
