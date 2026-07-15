// A tiny same-origin HTTP client with a cookie jar, for the SeqPal acceptance
// driver. seqpald authenticates with an HttpOnly `seqpal_session` cookie scoped
// to /seqpal (auth.go openSession), so the driver must persist and resend that
// cookie exactly as a browser would. Node's global fetch does not keep a jar of
// its own, so this keeps a minimal one: it records every Set-Cookie by name and
// sends the current set on each request to the same origin. No third-party
// dependency, so the driver stays runnable from a bare checkout.

export class ApiError extends Error {
  constructor(message, status, data, path) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.data = data || {}
    this.path = path
  }
}

export class Http {
  // base is the origin, e.g. https://sequentiatestnet.com. seqpald lives under
  // /seqpal/api and the policy server's public reads under /openamp/v1, so the
  // caller passes full paths; this class only owns the origin + cookie jar.
  constructor(base, { verbose = false } = {}) {
    this.base = base.replace(/\/$/, '')
    this.jar = new Map() // cookie name -> value
    this.verbose = verbose
  }

  cookieHeader() {
    if (this.jar.size === 0) return undefined
    return [...this.jar.entries()].map(([k, v]) => `${k}=${v}`).join('; ')
  }

  // Record Set-Cookie response headers into the jar. A MaxAge<=0 (logout)
  // deletes the cookie, matching browser semantics closely enough for the driver.
  absorb(res) {
    // Node exposes multiple Set-Cookie values via getSetCookie() (Node 18.14+).
    const raw = typeof res.headers.getSetCookie === 'function' ? res.headers.getSetCookie() : []
    for (const line of raw) {
      const [pair, ...attrs] = line.split(';')
      const eq = pair.indexOf('=')
      if (eq < 0) continue
      const name = pair.slice(0, eq).trim()
      const value = pair.slice(eq + 1).trim()
      const maxAge = attrs.map((a) => a.trim().toLowerCase()).find((a) => a.startsWith('max-age='))
      if (maxAge && Number(maxAge.slice('max-age='.length)) <= 0) {
        this.jar.delete(name)
      } else {
        this.jar.set(name, value)
      }
    }
  }

  async request(path, { method = 'GET', body, timeoutMs = 20000 } = {}) {
    const url = this.base + path
    const headers = {}
    const cookie = this.cookieHeader()
    if (cookie) headers.cookie = cookie
    if (body !== undefined) headers['content-type'] = 'application/json'
    const ctrl = new AbortController()
    const timer = setTimeout(() => ctrl.abort(), timeoutMs)
    let res
    try {
      res = await fetch(url, {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: ctrl.signal,
      })
    } catch (e) {
      clearTimeout(timer)
      throw new ApiError(`request to ${url} failed: ${e.message}`, 0, {}, path)
    }
    clearTimeout(timer)
    this.absorb(res)
    const text = await res.text()
    let parsed = {}
    try {
      parsed = text ? JSON.parse(text) : {}
    } catch {
      parsed = { error: text }
    }
    if (this.verbose) {
      process.stderr.write(`    ${method} ${path} -> ${res.status}\n`)
    }
    if (!res.ok) {
      throw new ApiError(parsed.error || `${res.status} ${res.statusText}`, res.status, parsed, path)
    }
    return parsed
  }

  get(path, opts) {
    return this.request(path, { ...opts, method: 'GET' })
  }

  post(path, body, opts) {
    return this.request(path, { ...opts, method: 'POST', body: body ?? {} })
  }

  patch(path, body, opts) {
    return this.request(path, { ...opts, method: 'PATCH', body: body ?? {} })
  }
}
