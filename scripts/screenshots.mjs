#!/usr/bin/env node
// Screenshot harness for Stasher's README: boots the REAL Go binary on a
// fresh temp data dir, seeds a believable maker inventory over the API
// (two users, nested locations + generated bins, items with photos /
// a datasheet PDF / notes / usage, a shared project with BOM, comments,
// bookmark and materials), then drives headless Chromium (puppeteer-core —
// same package + chromium discovery as web/scripts/smoke-authed.mjs, just
// resolved from web/node_modules since this runs from the repo root) to
// capture six documented shots into docs/screenshots/.
//
// Requirements:
//   - The stasher binary at the repo root:
//       go build -o stasher ./cmd/stasher        (or point STASHER_BIN at it)
//   - web/node_modules with puppeteer-core installed (make web-setup).
//   - A system Chromium: discovered via CHROME_PATH, else the first of
//     chromium / chromium-browser / google-chrome found on PATH.
//
// Run from the repo root:  node scripts/screenshots.mjs
// Every shot asserts its key DOM content BEFORE capturing, fails loudly
// otherwise. Exits 0 on success, 1 with a message on failure.

import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'
import net from 'node:net'
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { delimiter, dirname, join, resolve } from 'node:path'
import { tmpdir } from 'node:os'
import { fileURLToPath, pathToFileURL } from 'node:url'

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const OUT_DIR = join(rootDir, 'docs', 'screenshots')

const SAM = { email: 'sam@example.com', password: 'workshop-demo-1', name: 'Sam' }
const ALICE = { email: 'alice@example.com', password: 'maker-demo-22', name: 'Alice' }

const sleep = (ms) => new Promise(r => setTimeout(r, ms))

// ---------------------------------------------------------------------------
// Environment discovery (mirrors web/scripts/smoke-authed.mjs)
// ---------------------------------------------------------------------------

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

// puppeteer-core lives in web/node_modules (it is the SPA smoke script's
// devDependency); resolve it from there so this runs from the repo root
// without its own install.
async function loadPuppeteer() {
  const webRequire = createRequire(join(rootDir, 'web', 'package.json'))
  const entry = webRequire.resolve('puppeteer-core')
  const mod = await import(pathToFileURL(entry))
  const puppeteer = mod.default?.launch ? mod.default : mod
  if (!puppeteer?.launch) throw new Error(`puppeteer-core resolved to ${entry} but exposes no launch()`)
  return puppeteer
}

function freePort() {
  return new Promise((res, rej) => {
    const srv = net.createServer()
    srv.on('error', rej)
    srv.listen(0, '127.0.0.1', () => {
      const { port } = srv.address()
      srv.close(() => res(port))
    })
  })
}

// PNG dimensions straight from the IHDR chunk (bytes 16..24, big-endian).
// Accepts the Uint8Array puppeteer returns (not a Node Buffer).
function pngSize(buf) {
  const dv = new DataView(buf.buffer, buf.byteOffset, buf.byteLength)
  return { width: dv.getUint32(16), height: dv.getUint32(20) }
}

// ---------------------------------------------------------------------------
// Server lifecycle (adapted from web/scripts/smoke-authed.mjs)
// ---------------------------------------------------------------------------

const dataDir = mkdtempSync(join(tmpdir(), 'stasher-shots-'))
let BASE = ''
let server = null
let browser = null

function startServer(bin) {
  const proc = spawn(bin, [], {
    env: {
      ...process.env,
      STASHER_DATA_DIR: dataDir,
      STASHER_LISTEN_ADDR: BASE.replace('http://', ''),
      STASHER_REGISTRATION: 'open',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  })
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

async function cleanup() {
  if (browser) { await browser.close().catch(() => {}); browser = null }
  await stopServer()
  rmSync(dataDir, { recursive: true, force: true })
}

for (const sig of ['SIGINT', 'SIGTERM']) {
  process.on(sig, () => {
    console.error(`screenshots: interrupted (${sig}) — cleaning up`)
    cleanup().finally(() => process.exit(1))
  })
}

// ---------------------------------------------------------------------------
// API client (cookie jars per user, from register's set-cookie)
// ---------------------------------------------------------------------------

const sessions = {} // email -> raw session token

async function call(method, path, { user = SAM.email, body, form } = {}) {
  const headers = {}
  if (sessions[user]) headers.cookie = `stasher_session=${sessions[user]}`
  let payload
  if (form !== undefined) payload = form
  else if (body !== undefined) {
    headers['content-type'] = 'application/json'
    payload = JSON.stringify(body)
  }
  const resp = await fetch(`${BASE}${path}`, { method, headers, body: payload })
  if (!resp.ok) {
    throw new Error(`${method} ${path} → ${resp.status} ${await resp.text()}`)
  }
  if (resp.status === 204) return null
  const text = await resp.text()
  return text ? JSON.parse(text) : null // some 201s (project item links) carry no body
}

const post = (path, body, opts) => call('POST', path, { body, ...opts })

async function register(user) {
  const resp = await fetch(`${BASE}/api/v1/auth/register`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ email: user.email, password: user.password, display_name: user.name }),
  })
  if (resp.status !== 201) throw new Error(`register ${user.email} → ${resp.status} ${await resp.text()}`)
  const raw = (resp.headers.getSetCookie?.() ?? [])
    .map(c => c.split(';')[0])
    .find(c => c.startsWith('stasher_session='))
  if (!raw) throw new Error(`register ${user.email}: no session cookie in response`)
  sessions[user.email] = raw.slice('stasher_session='.length)
}

// multipart upload: one "file" FILE part (the field every upload endpoint
// expects — items/photos, items/documents, projects/files, locations photo).
function upload(path, bytes, filename, mime, user = SAM.email) {
  const fd = new FormData()
  fd.append('file', new Blob([bytes], { type: mime }), filename)
  return call('POST', path, { user, form: fd })
}

// ---------------------------------------------------------------------------
// Generated "photos": styled 640x480 gradient cards rendered in-page
// ---------------------------------------------------------------------------

// Pure CSS gradients + SVG line art + text labels. Labels are
// quantity-free BY DESIGN: qty lives in the live UI and the usage ledger
// moves it, so baking a number into the art would go stale between capture
// and viewing (the review round caught exactly that). Line art also stays
// crisp headless; the system emoji font (now installed) renders the UI's
// own 📷 badges properly.
const CARD_CSS = `
  * { margin: 0; box-sizing: border-box; }
  body { margin: 0; background: #0f172a; }
  #card {
    width: 640px; height: 480px; position: relative; overflow: hidden;
    font-family: 'Liberation Sans', 'DejaVu Sans', sans-serif; color: #fff;
  }
  .label { position: absolute; left: 28px; bottom: 24px; text-shadow: 0 1px 6px rgba(0,0,0,.45); }
  .label .t { font-size: 36px; font-weight: 700; letter-spacing: .5px; }
  .label .s { font-size: 16px; opacity: .9; margin-top: 4px; }
  .art { position: absolute; inset: 0; }
`

const CARDS = {
  lm358Tube: {
    bg: 'linear-gradient(135deg,#f59e0b 0%,#f97316 55%,#dc2626 100%)',
    art: `
      <rect x="170" y="70" width="300" height="150" rx="26" fill="rgba(255,255,255,.22)" stroke="rgba(255,255,255,.5)" stroke-width="2"/>
      <rect x="255" y="108" width="130" height="74" rx="6" fill="#111827"/>
      ${[...Array(4)].map((_, i) =>
        `<line x1="${268 + i * 28}" y1="182" x2="${268 + i * 28}" y2="206" stroke="#111827" stroke-width="7"/>` +
        `<line x1="${268 + i * 28}" y1="108" x2="${268 + i * 28}" y2="84" stroke="#111827" stroke-width="7"/>`).join('')}
      <text x="320" y="152" text-anchor="middle" fill="#e5e7eb" font-size="19" font-family="monospace" font-weight="bold">LM358</text>
      <circle cx="285" cy="122" r="4" fill="none" stroke="#9ca3af" stroke-width="1.5"/>`,
    title: 'LM358', sub: 'Dual op-amp · DIP-8',
  },
  lm358Macro: {
    bg: 'linear-gradient(135deg,#0d9488 0%,#0e7490 55%,#3730a3 100%)',
    art: `
      <g transform="translate(240,150)" stroke="#fff" stroke-width="7" fill="rgba(255,255,255,.14)" stroke-linejoin="round">
        <path d="M0 -70 L0 70 L120 0 Z"/>
        <line x1="-60" y1="-38" x2="0" y2="-38"/><line x1="-60" y1="38" x2="0" y2="38"/>
        <line x1="120" y1="0" x2="180" y2="0"/>
      </g>
      <text x="196" y="122" fill="#fff" font-size="26" font-weight="bold">+</text>
      <text x="196" y="212" fill="#fff" font-size="26" font-weight="bold">−</text>`,
    title: 'LM358', sub: 'Close-up · one from the tube',
  },
  esp32: {
    bg: 'linear-gradient(135deg,#0284c7 0%,#2563eb 55%,#4f46e5 100%)',
    art: `
      <rect x="185" y="80" width="270" height="230" rx="12" fill="#065f46" stroke="#059669" stroke-width="3"/>
      <path d="M210 95 h55 v14 h-18 v13 h18 v14 h-18 v13 h18 v14 h-18 v13 h18 v14 h-55 Z" fill="none" stroke="#d1d5db" stroke-width="3"/>
      <rect x="290" y="112" width="120" height="78" rx="6" fill="#cbd5e1"/>
      <rect x="302" y="124" width="96" height="10" fill="#94a3b8"/><rect x="302" y="140" width="96" height="10" fill="#94a3b8"/>
      <rect x="302" y="156" width="96" height="10" fill="#94a3b8"/>
      <rect x="205" y="215" width="90" height="80" rx="4" fill="#111827"/>
      ${[...Array(9)].map((_, i) =>
        `<line x1="${315 + i * 15}" y1="215" x2="${315 + i * 15}" y2="295" stroke="#facc15" stroke-width="3"/>`).join('')}`,
    title: 'ESP32 DevKit', sub: 'WiFi + Bluetooth dev board',
  },
  rpi4: {
    bg: 'linear-gradient(135deg,#059669 0%,#0d9488 55%,#0891b2 100%)',
    art: `
      <rect x="160" y="90" width="320" height="210" rx="10" fill="#166534" stroke="#22c55e" stroke-width="3"/>
      ${[...Array(20)].map((_, i) =>
        `<rect x="${182 + i * 14}" y="104" width="6" height="26" fill="#e5e7eb"/>` +
        `<rect x="${182 + i * 14}" y="140" width="6" height="26" fill="#e5e7eb"/>`).join('')}
      <rect x="215" y="190" width="90" height="80" rx="4" fill="#111827"/>
      <rect x="330" y="196" width="120" height="66" rx="6" fill="#64748b"/>
      <rect x="330" y="196" width="120" height="18" rx="6" fill="#475569"/>
      <circle cx="452" cy="272" r="12" fill="none" stroke="#facc15" stroke-width="4"/>`,
    title: 'Raspberry Pi 4', sub: '4GB · bench server brain',
  },
  multimeter: {
    bg: 'linear-gradient(135deg,#334155 0%,#1e293b 55%,#0f172a 100%)',
    art: `
      <rect x="205" y="60" width="230" height="290" rx="18" fill="#f8fafc"/>
      <circle cx="320" cy="185" r="82" fill="#0f172a" stroke="#475569" stroke-width="4"/>
      ${[...Array(11)].map((_, i) => {
        const a = (Math.PI * 1.25) + (i * Math.PI * 1.1) / 10
        return `<line x1="${320 + Math.cos(a) * 64}" y1="${185 + Math.sin(a) * 64}" x2="${320 + Math.cos(a) * 76}" y2="${185 + Math.sin(a) * 76}" stroke="#94a3b8" stroke-width="3"/>`
      }).join('')}
      <line x1="320" y1="185" x2="252" y2="128" stroke="#ef4444" stroke-width="4" stroke-linecap="round"/>
      <circle cx="320" cy="185" r="7" fill="#ef4444"/>
      <rect x="245" y="285" width="60" height="26" rx="4" fill="#ef4444"/>
      <rect x="335" y="285" width="60" height="26" rx="4" fill="#111827"/>`,
    title: 'Multimeter', sub: 'True-RMS · the bench workhorse',
  },
  workshop: {
    bg: 'linear-gradient(135deg,#b45309 0%,#92400e 50%,#78350f 100%)',
    art: `
      <line x1="80" y1="300" x2="560" y2="300" stroke="rgba(0,0,0,.4)" stroke-width="10"/>
      <line x1="80" y1="370" x2="560" y2="370" stroke="rgba(0,0,0,.4)" stroke-width="10"/>
      ${[...Array(5)].map((_, r) => [...Array(12)].map((_, c) =>
        `<circle cx="${95 + c * 41}" cy="${150 + r * 30}" r="5" fill="rgba(0,0,0,.35)"/>`).join('')).join('')}
      <rect x="140" y="318" width="150" height="44" rx="8" fill="#dc2626"/>
      <rect x="140" y="318" width="150" height="16" rx="8" fill="#b91c1c"/>
      <rect x="360" y="322" width="130" height="40" rx="6" fill="#0f172a"/>
      <rect x="380" y="308" width="20" height="16" rx="3" fill="#334155"/>`,
    title: 'Workshop', sub: 'Garage corner · everything lives here',
  },
}

function cardHtml(spec) {
  return `<!doctype html><html><head><meta charset="utf-8"><style>${CARD_CSS}</style></head>
<body><div id="card" style="background:${spec.bg}">
  <svg class="art" viewBox="0 0 640 480" width="640" height="480">${spec.art}</svg>
  <div class="label"><div class="t">${spec.title}</div><div class="s">${spec.sub}</div></div>
</div></body></html>`
}

async function renderPhoto(page, spec) {
  await page.setContent(cardHtml(spec), { waitUntil: 'load' })
  const el = await page.$('#card')
  return el.screenshot({ type: 'png' })
}

// ---------------------------------------------------------------------------
// A minimal but valid PDF (correct xref offsets, real text objects) padded
// with a generated "electrical characteristics" table so the stored size
// lands in datasheet territory (~180 KB) — a 1 KB "datasheet" read as
// implausible in review. The server's sniffer only needs the %PDF- magic
// to store it as a document.
// ---------------------------------------------------------------------------

function makePdf(title, targetBytes = 180 * 1024) {
  const esc = s => s.replace(/\\/g, '\\\\').replace(/\(/g, '\\(').replace(/\)/g, '\\)')
  const intro = [
    `${title}`,
    '',
    'Stasher demo seed document - a stand-in for the real',
    'datasheet PDF. Generated by scripts/screenshots.mjs.',
  ]
  // Filler rows rotate through plausible datasheet parameters so the file
  // opens as a long characteristics table rather than one repeated line.
  // ASCII-only, so string length == byte length for the xref offsets.
  const params = [
    ['Input offset voltage', 'mV', 2, 7],
    ['Input bias current', 'nA', 15, 100],
    ['Large-signal voltage gain', 'V/mV', 25, 200],
    ['Slew rate', 'V/us', 0.3, 0.9],
    ['Gain-bandwidth product', 'MHz', 0.7, 1.5],
    ['Supply current (both amps)', 'mA', 0.5, 1.2],
    ['Output voltage swing', 'V', 12, 26],
    ['Common-mode rejection', 'dB', 65, 95],
    ['Power-supply rejection', 'dB', 65, 100],
    ['Input voltage range', 'V', 12, 26],
  ]
  let stream = 'BT /F1 18 Tf 56 760 Td (' + esc(title) + ') Tj /F1 11 Tf 0 -30 Td '
    + intro.slice(1).map(l => `(${esc(l)}) Tj 0 -16 Td `).join('')
    + 'ET\nBT /F1 8 Tf 56 660 Td 14 TL\n'
  let i = 0
  while (stream.length < targetBytes) {
    const [name, unit, lo, hi] = params[i % params.length]
    const v = (lo + ((i * 37) % 100) / 100 * (hi - lo)).toFixed(2)
    stream += `(${i + 1}. ${esc(name)}  ${v} ${unit}  min/typ/max  VCC=+/-15V  TA=25C   ) Tj T*\n`
    i++
  }
  stream += 'ET'
  const objs = {
    1: '<< /Type /Catalog /Pages 2 0 R >>',
    2: '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    3: '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>',
    4: `<< /Length ${stream.length} >>\nstream\n${stream}\nendstream`,
    5: '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
    6: `<< /Title (${esc(title)}) /Producer (stasher screenshots) >>`,
  }
  let pdf = '%PDF-1.4\n'
  const offsets = {}
  for (const id of [1, 2, 3, 4, 5, 6]) {
    offsets[id] = Buffer.byteLength(pdf)
    pdf += `${id} 0 obj\n${objs[id]}\nendobj\n`
  }
  const xrefAt = Buffer.byteLength(pdf)
  pdf += `xref\n0 7\n0000000000 65535 f \n`
  for (const id of [1, 2, 3, 4, 5, 6]) pdf += `${String(offsets[id]).padStart(10, '0')} 00000 n \n`
  pdf += `trailer\n<< /Size 7 /Root 1 0 R /Info 6 0 R >>\nstartxref\n${xrefAt}\n%%EOF\n`
  return Buffer.from(pdf, 'latin1')
}

// ---------------------------------------------------------------------------
// Seeding
// ---------------------------------------------------------------------------

async function seed(photoPage) {
  const log = msg => console.log(`  · ${msg}`)

  await register(SAM) // first user → admin
  await register(ALICE)
  log(`registered ${SAM.name} (admin) + ${ALICE.name}`)

  // Locations: Workshop › Shelving Unit › Parts Bins (bulk-generated bins),
  // plus Toolbox and Desk Drawer at the root.
  const workshop = await post('/api/v1/locations', { name: 'Workshop', notes: 'Garage corner bench' })
  const shelving = await post('/api/v1/locations', { name: 'Shelving Unit', parent_id: workshop.id })
  const bins = await post('/api/v1/locations/bulk', {
    name: 'Parts Bins', parent_id: shelving.id, dims: [1, 3],
    structure: 'flat', naming: 'compact', prefix: 'Bin',
  })
  const toolbox = await post('/api/v1/locations', { name: 'Toolbox' })
  const drawer = await post('/api/v1/locations', { name: 'Desk Drawer' })
  log(`locations: Workshop › Shelving Unit › Parts Bins (+${bins.children_created} generated bins), Toolbox, Desk Drawer`)

  // Workshop picture (generated gradient card, same as item photos).
  const locShot = await renderPhoto(photoPage, CARDS.workshop)
  await upload(`/api/v1/locations/${workshop.id}/photo`, locShot, 'workshop.png', 'image/png')

  // Item photo bytes first (element screenshots), then the items.
  const shots = {
    lm358Tube: await renderPhoto(photoPage, CARDS.lm358Tube),
    lm358Macro: await renderPhoto(photoPage, CARDS.lm358Macro),
    esp32: await renderPhoto(photoPage, CARDS.esp32),
    rpi4: await renderPhoto(photoPage, CARDS.rpi4),
    multimeter: await renderPhoto(photoPage, CARDS.multimeter),
  }

  const bin = async n => (await call('GET', '/api/v1/locations')).data.find(l => l.name === `Bin ${n}`).id
  const binA1 = await bin('A1'), binA2 = await bin('A2'), binA3 = await bin('A3')

  // Created oldest-first: the list defaults to updated_at DESC, so the most
  // recently touched (photographed / used / shared) items sit on top.
  const mk = (name, extra = {}) => post('/api/v1/items', { name, ...extra })
  await mk('Header pins', { description: '40-pin 2.54mm break-away headers', qty: 40, unit: 'pcs', location_id: binA3, tags: ['components', 'electronics'] })
  await mk('Wire strippers', { qty: 1, location_id: toolbox.id, tags: ['tools'] })
  await mk('Calipers', { description: 'Digital, 150mm', qty: 1, location_id: drawer.id, tags: ['tools'] })
  await mk('Heat-shrink kit', { description: 'Assorted diameters, 2:1', qty: 1, location_id: shelving.id, tags: ['consumables'] })
  await mk('M3 screws', { description: 'Socket head, 8mm, black oxide', qty: 200, unit: 'pcs', location_id: shelving.id, tags: ['consumables'] })
  await mk('Multimeter', { description: 'True-RMS handheld', qty: 1, location_id: toolbox.id, tags: ['tools'] })
  await mk('2N7000 MOSFET', { description: 'N-channel, TO-92', qty: 60, unit: 'pcs', location_id: binA2, tags: ['components', 'electronics'] })
  await mk('Solder spool 0.8mm', { description: '63/37 leaded, flows nicely', qty: 1, unit: 'spool', location_id: workshop.id, tags: ['consumables'] })
  await mk('Bench power supply', { description: '0-30V, 3A, kit build', qty: 1, location_id: toolbox.id, tags: ['tools'] })
  const rpi = await mk('Raspberry Pi 4', { description: '4GB, runs the print server', qty: 1, location_id: drawer.id, tags: ['components', 'electronics'] })
  const esp32 = await mk('ESP32 DevKit', { description: 'WROOM-32, 38-pin', qty: 2, location_id: drawer.id, tags: ['components', 'electronics'] })
  const lm358 = await mk('LM358 op-amp', { description: 'Dual op-amp, DIP-8, general purpose', qty: 24, unit: 'pcs', location_id: binA1, tags: ['components', 'electronics'] })
  log('12 items created (components / consumables / tools)')

  await upload(`/api/v1/items/${lm358.id}/photos`, shots.lm358Tube, 'lm358-tube.png', 'image/png')
  await upload(`/api/v1/items/${lm358.id}/photos`, shots.lm358Macro, 'lm358-macro.png', 'image/png')
  await upload(`/api/v1/items/${esp32.id}/photos`, shots.esp32, 'esp32.png', 'image/png')
  await upload(`/api/v1/items/${rpi.id}/photos`, shots.rpi4, 'rpi4.png', 'image/png')
  const mm = (await call('GET', '/api/v1/items?q=multimeter')).data[0]
  await upload(`/api/v1/items/${mm.id}/photos`, shots.multimeter, 'multimeter.png', 'image/png')
  log('6 generated photos uploaded (LM358 ×2, ESP32, RPi4, multimeter) + Workshop location picture')

  // LM358 extras: timestamped note + a small hand-written PDF document.
  await post(`/api/v1/items/${lm358.id}/notes`, { body: 'Bought 2 packs, merged the tubes' })
  const pdf = makePdf('LM358 datasheet')
  await upload(`/api/v1/items/${lm358.id}/documents`, pdf, 'lm358-datasheet.pdf', 'application/pdf')
  log(`item note + LM358-datasheet.pdf (${pdf.length} bytes) on the LM358`)

  // Project: shared, active, with a BOM, owner notes, a two-voice comment
  // thread, a bookmark and the datasheet again as a material.
  const project = await post('/api/v1/projects', {
    name: 'Bench PSU Build', status: 'active', visibility: 'shared',
    notes: '0-30V / 3A bench supply from a kit — LM358 regulation, ESP32 front panel.',
  })
  await post(`/api/v1/projects/${project.id}/items`, { item_id: lm358.id, qty_used: 2, notes: 'Regulation stage' })
  await post(`/api/v1/projects/${project.id}/items`, { item_id: esp32.id, qty_used: 1, notes: 'Front panel display' })
  await post(`/api/v1/projects/${project.id}/items`, { item_id: mm.id, qty_used: 1, notes: 'Calibration' })
  const screws = (await call('GET', '/api/v1/items?q=screws')).data[0]
  await post(`/api/v1/projects/${project.id}/items`, { item_id: screws.id, qty_used: 12 })
  await post(`/api/v1/projects/${project.id}/notes`, { body: 'Case arrived' })
  await post(`/api/v1/projects/${project.id}/notes`, { body: 'Wired the AC inlet and fuse holder' })
  await post(`/api/v1/projects/${project.id}/comments`, { body: 'Front panel holes drilled — waiting on the labels.' })
  await post(`/api/v1/projects/${project.id}/comments`, { user: ALICE.email, body: 'Nice! What current limit did you settle on?' })
  await post(`/api/v1/projects/${project.id}/bookmarks`, {
    url: 'https://www.ti.com/lit/ds/symlink/lm358.htm', title: 'LM358 datasheet (TI)',
  })
  await upload(`/api/v1/projects/${project.id}/files`, pdf, 'lm358-datasheet.pdf', 'application/pdf')
  log('project "Bench PSU Build": 4 BOM lines, 2 notes, 2 comments, bookmark, material PDF (shared)')

  // Usage ledger: build consumed parts; solder got a restock.
  await post(`/api/v1/items/${lm358.id}/usage`, { kind: 'used', delta: -2, project_id: project.id, note: 'Regulation stage' })
  await post(`/api/v1/items/${esp32.id}/usage`, { kind: 'used', delta: -1, project_id: project.id, note: 'Display module' })
  const solder = (await call('GET', '/api/v1/items?q=solder spool')).data[0]
  await post(`/api/v1/items/${solder.id}/usage`, { kind: 'restock', delta: 1, note: 'New spool' })

  // Per-item sharing: the Raspberry Pi is visible to every user.
  await call('PATCH', `/api/v1/items/${rpi.id}`, { body: { visibility: 'shared' } })
  log('usage entries (used 2×LM358, 1×ESP32 on the project; solder restock) + shared the Raspberry Pi')

  return { lm358Id: lm358.id, projectId: project.id }
}

// ---------------------------------------------------------------------------
// Capture
// ---------------------------------------------------------------------------

async function openPage(path, { user = SAM.email, dark = false } = {}) {
  const page = await browser.newPage()
  await page.setViewport({ width: 1280, height: 800, deviceScaleFactor: 1 })
  await page.setCookie({ name: 'stasher_session', value: sessions[user], url: BASE })
  if (dark) {
    // localStorage must hold the theme BEFORE the SPA's no-FOUC script in
    // index.html runs — evaluateOnNewDocument sets it on the new document
    // ahead of any page script, so the dark class lands with zero flash.
    await page.evaluateOnNewDocument(() => localStorage.setItem('stasher-theme', 'dark'))
  }
  await page.goto(`${BASE}${path}`, { waitUntil: 'networkidle0', timeout: 30000 })
  await sleep(300) // settle: transitions, lazy fetches, toasts
  return page
}

async function assertText(page, needles) {
  const body = await page.evaluate(() => document.body.textContent)
  for (const n of needles) {
    if (!body.includes(n)) throw new Error(`content assertion failed: "${n}" not on page`)
  }
}

// Lazy <img> elements only load once scrolled near the viewport — walk the
// page so fullPage captures contain every thumbnail, then wait for decode.
async function loadAllImages(page) {
  await page.evaluate(async () => {
    await new Promise(resolve => {
      let y = 0
      const step = () => {
        window.scrollTo(0, y)
        y += window.innerHeight
        if (y < document.body.scrollHeight) setTimeout(step, 60)
        else { window.scrollTo(0, 0); setTimeout(resolve, 120) }
      }
      step()
    })
  })
  await page.waitForFunction(
    // Only RENDERED images must be complete: the SPA keeps a CSS-hidden
    // mobile card list (md:hidden) in the DOM whose <img>s never load on
    // a desktop viewport — skip anything without layout boxes.
    () => [...document.images].every(i =>
      i.getClientRects().length === 0 || (i.complete && i.naturalWidth > 0)),
    { timeout: 10000 },
  )
}

async function capture(name, page, { fullPage = false } = {}) {
  // A focused control freezes its focus ring into the shot (the review
  // round caught the search input's ring in shared.png) — blur first.
  await page.evaluate(() => document.activeElement?.blur())
  await sleep(50)
  if (fullPage) await loadAllImages(page)
  const buf = await page.screenshot({ fullPage, type: 'png' })
  if (buf.length < 20000) throw new Error(`${name}.png suspiciously small (${buf.length} bytes) — likely a blank/broken shot`)
  const { width, height } = pngSize(buf)
  writeFileSync(join(OUT_DIR, `${name}.png`), buf)
  console.log(`  ✓ docs/screenshots/${name}.png — ${width}×${height} — ${(buf.length / 1024).toFixed(1)} KB`)
  await page.close()
}

async function run() {
  const bin = process.env.STASHER_BIN ?? join(rootDir, 'stasher')
  if (!existsSync(bin)) {
    throw new Error(`stasher binary not found at ${bin} — build it first: make build`)
  }
  const chrome = findChromium()
  if (!chrome) throw new Error('no chromium found on PATH (chromium/chromium-browser/google-chrome); set CHROME_PATH')

  mkdirSync(OUT_DIR, { recursive: true })
  const port = await freePort()
  BASE = `http://127.0.0.1:${port}`

  const puppeteer = await loadPuppeteer()
  console.log(`screenshots: booting ${bin} on ${BASE} (fresh temp data dir)`)
  server = startServer(bin)
  await waitForHealth()

  browser = await puppeteer.launch({
    executablePath: chrome,
    headless: true,
    args: ['--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage'],
  })
  const photoPage = await browser.newPage()
  await photoPage.setViewport({ width: 640, height: 480, deviceScaleFactor: 1 })

  console.log('screenshots: seeding demo data via the API')
  const { lm358Id, projectId } = await seed(photoPage)
  await photoPage.close()

  console.log('screenshots: capturing')

  // items.png — the list: search bar, thumbnails, tags (as Sam).
  {
    const page = await openPage('/items')
    await page.waitForSelector('input[placeholder="Search items…"]', { timeout: 15000 })
    // List rows don't carry tags (detail-only field) — the tag names are
    // visible in the "All tags" filter dropdown instead.
    await assertText(page, ['LM358 op-amp', 'Raspberry Pi 4', 'components', 'electronics'])
    await loadAllImages(page)
    await capture('items', page)
  }

  // item-detail.png — the LM358: hero photo, tags, note, document, history.
  {
    const page = await openPage(`/items/${lm358Id}`)
    await page.waitForSelector('h1', { timeout: 15000 })
    await assertText(page, [
      'LM358 op-amp', 'Photos', 'Documents', 'History', 'Notes',
      'Bought 2 packs, merged the tubes', 'lm358-datasheet.pdf', 'Bench PSU Build',
    ])
    await capture('item-detail', page, { fullPage: true })
  }

  // locations.png — the tree, Workshop picture thumbnail, expanded bins.
  {
    const page = await openPage('/locations')
    await page.waitForSelector('[data-loc-name]', { timeout: 15000 })
    for (const name of ['Workshop', 'Shelving Unit', 'Parts Bins']) {
      await page.click(`button[aria-label="Expand ${name}"]`)
      await sleep(150)
    }
    await assertText(page, ['Workshop', 'Bin A1', 'Bin A2', 'Bin A3', 'Toolbox', 'Desk Drawer'])
    await capture('locations', page)
  }

  // project.png — owner view: BOM, notes log, comments, bookmark, materials.
  {
    const page = await openPage(`/projects/${projectId}`)
    await page.waitForSelector('h1', { timeout: 15000 })
    await assertText(page, [
      'Bench PSU Build', 'LM358 op-amp', 'Case arrived', 'Wired the AC inlet',
      'Nice! What current limit did you settle on?', 'LM358 datasheet (TI)', 'lm358-datasheet.pdf',
    ])
    await capture('project', page, { fullPage: true })
  }

  // shared.png — Alice's view of Sam's shared project + shared item.
  {
    const page = await openPage('/shared', { user: ALICE.email })
    await page.waitForSelector('input[placeholder="Search shared items…"]', { timeout: 15000 })
    await assertText(page, ['Shared with you', 'Raspberry Pi 4', `from ${SAM.name}`, 'Bench PSU Build'])
    await loadAllImages(page)
    await capture('shared', page)
  }

  // dark.png — /items again, dark theme (set before app load, no FOUC).
  {
    const page = await openPage('/items', { dark: true })
    await page.waitForSelector('input[placeholder="Search items…"]', { timeout: 15000 })
    const isDark = await page.evaluate(() => document.documentElement.classList.contains('dark'))
    if (!isDark) throw new Error('dark mode assertion failed: html.dark missing')
    await assertText(page, ['LM358 op-amp', '2N7000 MOSFET'])
    await loadAllImages(page)
    await capture('dark', page)
  }

  console.log('screenshots: ok — 6 shots in docs/screenshots/')
}

run()
  .catch(err => {
    console.error(`screenshots: FAIL — ${err.message}`)
    if (server) console.error(`--- server log tail ---\n${server.getLog().split('\n').slice(-25).join('\n')}`)
    process.exitCode = 1
  })
  .finally(cleanup)
