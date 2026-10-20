// Just enough of the Chrome DevTools Protocol to drive a page headless: start a
// Chromium, open a page, set a cookie, evaluate, wait, click, type into a React
// input, screenshot. Node's own WebSocket and fetch, so a bare checkout runs it.
import { spawn } from 'node:child_process'
import { mkdtempSync, readFileSync, rmSync, existsSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

export async function launch(chrome) {
  const dir = mkdtempSync(join(tmpdir(), 'seqpal-cdp-'))
  const proc = spawn(
    chrome,
    ['--headless=new', '--no-sandbox', '--disable-gpu', '--remote-debugging-port=0', `--user-data-dir=${dir}`,
      '--window-size=1280,2000', 'about:blank'],
    // Its own process group, so closing it stops every helper process too.
    { stdio: 'ignore', detached: true },
  )
  const portFile = join(dir, 'DevToolsActivePort')
  for (let i = 0; i < 100 && !existsSync(portFile); i++) await sleep(100)
  const port = readFileSync(portFile, 'utf8').split('\n')[0]
  let target
  for (let i = 0; i < 50 && !target; i++) {
    const list = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()
    target = list.find((t) => t.type === 'page')
    if (!target) await sleep(100)
  }
  const page = new Page(target.webSocketDebuggerUrl)
  await page.open()
  page.close = async () => {
    try {
      page.ws.close()
    } catch {
      /* closing */
    }
    const exited = new Promise((r) => proc.once('exit', r))
    try {
      process.kill(-proc.pid, 'SIGTERM')
    } catch {
      proc.kill('SIGTERM')
    }
    await exited
    // Helper processes may still be writing the profile as they go; it is a
    // scratch directory, so a last straggler is retried and then left be.
    for (let i = 0; i < 20; i++) {
      try {
        rmSync(dir, { recursive: true, force: true })
        break
      } catch {
        await sleep(250)
      }
    }
  }
  page.pid = proc.pid
  return page
}

class Page {
  constructor(url) {
    this.url = url
    this.seq = 0
    this.waiting = new Map()
    this.listeners = []
  }

  open() {
    this.ws = new WebSocket(this.url)
    this.ws.onmessage = (ev) => {
      const msg = JSON.parse(ev.data)
      if (msg.id && this.waiting.has(msg.id)) {
        const { resolve, reject } = this.waiting.get(msg.id)
        this.waiting.delete(msg.id)
        if (msg.error) reject(new Error(msg.error.message))
        else resolve(msg.result)
      } else if (msg.method) {
        for (const l of this.listeners) l(msg)
      }
    }
    return new Promise((resolve, reject) => {
      this.ws.onopen = async () => {
        await this.send('Page.enable')
        await this.send('Runtime.enable')
        await this.send('Network.enable')
        resolve()
      }
      this.ws.onerror = reject
    })
  }

  send(method, params = {}) {
    const id = ++this.seq
    this.ws.send(JSON.stringify({ id, method, params }))
    return new Promise((resolve, reject) => this.waiting.set(id, { resolve, reject }))
  }

  setCookie(url, name, value) {
    return this.send('Network.setCookie', { url, name, value, path: '/seqpal' })
  }

  async goto(url) {
    const loaded = new Promise((r) => {
      const l = (m) => {
        if (m.method === 'Page.loadEventFired') {
          this.listeners = this.listeners.filter((x) => x !== l)
          r()
        }
      }
      this.listeners.push(l)
    })
    await this.send('Page.navigate', { url })
    await loaded
  }

  async eval(expression) {
    const r = await this.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true })
    if (r.exceptionDetails) throw new Error('page: ' + (r.exceptionDetails.exception?.description || r.exceptionDetails.text))
    return r.result.value
  }

  // Poll a page expression until it is truthy; returns its value.
  async waitFor(expression, label, timeoutMs = 60000) {
    const end = Date.now() + timeoutMs
    for (;;) {
      const v = await this.eval(expression).catch(() => null)
      if (v) return v
      if (Date.now() > end) throw new Error('timed out waiting for ' + label)
      await sleep(200)
    }
  }

  text(selector) {
    return this.eval(`(document.querySelector(${JSON.stringify(selector)})||{}).innerText||''`)
  }

  // Type into a React-controlled input or select: the native value setter, then
  // the event React listens for.
  type(selector, value) {
    return this.eval(`(() => {
      const el = document.querySelector(${JSON.stringify(selector)})
      if (!el) throw new Error('no element ${selector}')
      const proto = el.tagName === 'SELECT' ? HTMLSelectElement.prototype : HTMLInputElement.prototype
      Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, ${JSON.stringify(String(value))})
      el.dispatchEvent(new Event(el.tagName === 'SELECT' ? 'change' : 'input', { bubbles: true }))
      return el.value
    })()`)
  }

  // Click the button whose text is `label`, else the first that contains it.
  clickButton(label) {
    return this.eval(`(() => {
      const all = [...document.querySelectorAll('button')]
      const b = all.find((x) => x.innerText.trim() === ${JSON.stringify(label)}) ||
        all.find((x) => x.innerText.includes(${JSON.stringify(label)}))
      if (!b) throw new Error('no button ' + ${JSON.stringify(label)})
      b.click()
      return b.innerText
    })()`)
  }

  async screenshot(path) {
    const { data } = await this.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true })
    writeFileSync(path, Buffer.from(data, 'base64'))
  }
}
