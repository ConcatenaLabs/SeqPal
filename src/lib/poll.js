import { useEffect, useRef, useState, useCallback } from 'react'

// usePoll runs an async fetcher immediately and then on an interval while
// `active` is true, exposing { data, error, loading, refresh }. It is how the
// money surfaces watch server-owned escrow state: seqpald is authoritative and
// the browser only mirrors it (the deposit watcher advances state server-side;
// there is no client-side escrow fact). The fetcher is re-run on `refresh()`
// after any mutating action so the UI reflects the new server state at once.
export function usePoll(fetcher, { intervalMs = 4000, active = true, deps = [] } = {}) {
  const [data, setData] = useState(null)
  const [error, setError] = useState(null)
  const [loading, setLoading] = useState(true)
  const savedFetcher = useRef(fetcher)
  savedFetcher.current = fetcher

  const run = useCallback(async () => {
    try {
      const d = await savedFetcher.current()
      setData(d)
      setError(null)
    } catch (e) {
      setError(e)
    } finally {
      setLoading(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    if (!active) return undefined
    let cancelled = false
    const tick = () => {
      if (!cancelled) run()
    }
    tick()
    const t = setInterval(tick, intervalMs)
    return () => {
      cancelled = true
      clearInterval(t)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active, intervalMs, ...deps])

  return { data, error, loading, refresh: run }
}
