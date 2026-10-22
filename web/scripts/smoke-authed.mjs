#!/usr/bin/env node
// Authed browser smoke for Stasher's SPA: boots the REAL Go binary on a temp
// data dir, registers an admin over the API, then drives a real headless
// Chromium (puppeteer-core — no bundled browser download) through
// login → dashboard shell → /items at a 375x812 mobile viewport.
//
// Requirements:
//   - The stasher binary at the repo root (from the repo root run:
//       go build -o stasher ./cmd/stasher
//     ) or point STASHER_BIN at it.
//   - A system Chromium: discovered via CHROME_PATH, else the first of
//     chromium / chromium-browser / google-chrome found on PATH.
//
// Run from web/:  npm run smoke:authed   (or: node scripts/smoke-authed.mjs)
// Exits 0 on success, 1 with a message on failure.

import { spawn } from 'node:child_process'
import { existsSync, mkdtempSync, rmSync } from 'node:fs'
import { delimiter, dirname, join, resolve } from 'node:path'
import { tmpdir } from 'node:os'
import { fileURLToPath } from 'node:url'
import puppeteer from 'puppeteer-core'

const ADDR = '127.0.0.1:18098'
const BASE = `http://${ADDR}`
const EMAIL = 'smoke-admin@test.dev'
const PASSWORD = 'longpassword1'

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
const dataDir = mkdtempSync(join(tmpdir(), 'stasher-authed-'))
let server = null
let browser = null

const sleep = (ms) => new Promise(r => setTimeout(r, ms))

// which(1)-like discovery without shelling out: walk PATH looking for the
// first executable named chromium / chromium-browser / google-chrome.
function findChromium() {
  if (process.env.CHROME_PATH) {
    if (existsSync(process.env.CHROME_PATH)) return process.env.CHROME_PATH
    throw new Error(`CHROME_PATH is set but does not exist: ${process.env.CHROME_PATH}`)
  }
  const names = ['chromium', 'chromium-browser', 'google-chrome']
  for (const dir of process.env.PATH.split(delimiter)) {
    if (!dir) continue
    for (const name of names) {
      const p = join(dir, name)
      if (existsSync(p)) return p
    }
  }
  return null
}

function startServer(bin) {
  const proc = spawn(bin, [], {
    env: {
      ...process.env,
      STASHER_DATA_DIR: dataDir,
      STASHER_LISTEN_ADDR: ADDR,
      STASHER_REGISTRATION: 'open',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  // Keep the tail for diagnostics if boot or a request goes wrong.
  let log = ''
  proc.stdout.on('data', d => { log += d })
  proc.stderr.on('data', d => { log += d })
  proc.getLog = () => log
  return proc
}

async function waitForHealth(timeoutMs = 15000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    try {
      const r = await fetch(`${BASE}/healthz`)
      if (r.ok) return
    } catch { /* not up yet */ }
    await sleep(100)
  }
  throw new Error(`server did not become healthy on ${BASE}\n--- server log ---\n${server.getLog()}`)
}

async function stopServer() {
  if (!server || server.exitCode !== null) return
  const exited = new Promise(res => server.once('exit', res))
  server.kill('SIGTERM') // graceful drain
  const killTimer = setTimeout(() => server.kill('SIGKILL'), 5000)
  await exited
  clearTimeout(killTimer)
}

async function main() {
  const bin = process.env.STASHER_BIN ?? join(rootDir, 'stasher')
  if (!existsSync(bin)) {
    throw new Error(`stasher binary not found at ${bin} — build it first: go build -o stasher ./cmd/stasher`)
  }
  const chrome = findChromium()
  if (!chrome) throw new Error('no chromium found on PATH (chromium/chromium-browser/google-chrome); set CHROME_PATH')

  server = startServer(bin)
  await waitForHealth()

  // Register the admin via the API (first user bootstraps as admin).
  const reg = await fetch(`${BASE}/api/v1/auth/register`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ email: EMAIL, password: PASSWORD, display_name: 'Smoke Admin' }),
  })
  if (reg.status !== 201) {
    throw new Error(`register failed: ${reg.status} ${await reg.text()}`)
  }

  browser = await puppeteer.launch({
    executablePath: chrome,
    headless: true,
    args: ['--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage'],
  })
  const page = await browser.newPage()
  await page.setViewport({ width: 375, height: 812 })

  // Log in through the real UI.
  await page.goto(`${BASE}/login`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('input[type="email"]')
  await page.type('input[type="email"]', EMAIL)
  await page.type('input[type="password"]', PASSWORD)
  await page.click('form button')
  // SPA: submit POSTs then router.push('/') — no full navigation, so wait
  // for the authed shell instead (the mobile bottom nav's Dashboard link).
  await page.waitForSelector('nav.bottom-0 a[href="/"]', { timeout: 15000 })

  const body = await page.evaluate(() => document.body.textContent)
  if (!body.includes('Dashboard')) throw new Error('"Dashboard" text missing after login')
  if (!(await page.$('nav.bottom-0'))) throw new Error('mobile bottom nav missing')
  await page.screenshot({ path: join(dataDir, 'authed-mobile.png') })

  // Items page renders its search input.
  await page.goto(`${BASE}/items`)
  await page.waitForSelector('input[placeholder="Search items…"]', { timeout: 15000 })
}

main()
  .then(() => console.log('smoke-authed ok'))
  .catch(err => {
    console.error(`smoke-authed: FAIL — ${err.message}`)
    process.exitCode = 1
  })
  .finally(async () => {
    if (browser) await browser.close().catch(() => {})
    await stopServer()
    rmSync(dataDir, { recursive: true, force: true })
  })
