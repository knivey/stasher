import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import TrashView from './TrashView.vue'
import { toasts } from '../utils/toast'

const LIST_URL = 'GET /api/v1/trash?page=1&page_size=20'

// thumb_key is set on purpose: the API returns it, but /files/{key} 404s while
// the item is trashed — the view must render a 📷 count badge, never an <img>.
const entries = [
  { id: 7, name: 'M3 screws', qty: 250, photo_count: 2, thumb_key: '2026/10/abc.webp',
    trashed_at: '2026-10-20T12:00:00Z', purges_at: '2026-11-19T12:00:00Z' },
  { id: 8, name: 'old cable', qty: 1, photo_count: 0, thumb_key: null,
    trashed_at: '2026-10-18T09:00:00Z', purges_at: null },
]

// Fetch mock that lists trash by default; `extra` handles mutations first.
function trashFetch(extra?: (url: string, init?: RequestInit) => Response | Promise<Response> | undefined) {
  const calls: string[] = []
  const stub = vi.fn(async (url: string, init?: RequestInit) => {
    calls.push(`${init?.method ?? 'GET'} ${url}`)
    const r = extra?.(url, init)
    if (r) return r
    return new Response(JSON.stringify({ data: entries, total: entries.length }), { status: 200 })
  })
  return { stub, calls }
}

async function mountView(): Promise<VueWrapper> {
  const w = mount(TrashView)
  await flushPromises()
  return w
}

beforeEach(() => { vi.unstubAllGlobals(); toasts.splice(0) })

describe('TrashView', () => {
  it('renders rows: name, qty, trashed date, purge hint, 📷 badge — and no <img> despite thumb_key', async () => {
    const { stub, calls } = trashFetch()
    vi.stubGlobal('fetch', stub)
    const w = await mountView()
    expect(calls).toContain(LIST_URL)
    const cards = w.findAll('[data-cards] > div')
    expect(cards).toHaveLength(2)
    expect(cards[0].text()).toContain('M3 screws')
    expect(cards[0].text()).toContain('250')
    expect(cards[0].text()).toContain('📷 2')
    expect(cards[0].text()).toContain('trashed 2026-10-20')
    expect(cards[0].text()).toContain('purges 2026-11-19')
    // purges_at null → kept until manually purged; no photo badge when count is 0
    expect(cards[1].text()).toContain('kept until purged')
    expect(cards[1].text()).not.toContain('📷')
    // thumb_key was set in the mock — rendering it would 404
    expect(w.find('img').exists()).toBe(false)
  })

  it('restores an entry: POSTs /trash/{id}/restore, toasts success, refetches the list', async () => {
    const { stub, calls } = trashFetch((url, init) => {
      if (url.includes('/trash/7/restore') && init?.method === 'POST')
        return new Response(JSON.stringify({ id: 99, name: 'M3 screws', qty: 250, unit: 'pcs' }), { status: 201 })
    })
    vi.stubGlobal('fetch', stub)
    const w = await mountView()
    const restore = w.findAll('[data-cards] > div')[0].findAll('button').find(b => b.text() === 'Restore')!
    await restore.trigger('click')
    await flushPromises()
    expect(calls).toContain('POST /api/v1/trash/7/restore')
    expect(toasts.some(t => t.message === 'Item restored' && t.type === 'success')).toBe(true)
    expect(calls.filter(c => c === LIST_URL)).toHaveLength(2)
  })

  it('blocks a duplicate restore while one is in flight (per-id busy)', async () => {
    let release!: (r: Response) => void
    const held = new Promise<Response>(res => { release = res })
    const { stub, calls } = trashFetch((url, init) => {
      if (url.includes('/trash/7/restore') && init?.method === 'POST') return held
    })
    vi.stubGlobal('fetch', stub)
    const w = await mountView()
    const restore = w.findAll('[data-cards] > div')[0].findAll('button').find(b => b.text() === 'Restore')!
    await restore.trigger('click')
    expect(restore.attributes('disabled')).toBeDefined()
    expect(restore.text()).toBe('Restoring…')
    await restore.trigger('click') // duplicate click while in flight
    await flushPromises()
    expect(calls.filter(c => c === 'POST /api/v1/trash/7/restore')).toHaveLength(1)
    release(new Response(JSON.stringify({ id: 99, name: 'M3 screws', qty: 250, unit: 'pcs' }), { status: 201 }))
    await flushPromises()
    expect(calls.filter(c => c === 'POST /api/v1/trash/7/restore')).toHaveLength(1)
    expect(toasts.some(t => t.message === 'Item restored' && t.type === 'success')).toBe(true)
  })

  it('purges via ConfirmDialog: DELETE /trash/{id}, toasts, refetches', async () => {
    const { stub, calls } = trashFetch((_url, init) => {
      if (init?.method === 'DELETE') return new Response(null, { status: 204 })
    })
    vi.stubGlobal('fetch', stub)
    const w = await mountView()
    const purge = w.findAll('[data-cards] > div')[0].findAll('button').find(b => b.text() === 'Purge')!
    await purge.trigger('click')
    // nothing deleted yet — the dialog is open with the irreversible warning
    expect(calls.filter(c => c.startsWith('DELETE'))).toHaveLength(0)
    const overlay = w.find('.fixed.inset-0')
    expect(overlay.text()).toContain('Permanently delete this item and its files? This cannot be undone.')
    await overlay.findAll('button').find(b => b.text() === 'Purge')!.trigger('click')
    await flushPromises()
    expect(calls).toContain('DELETE /api/v1/trash/7')
    expect(toasts.some(t => t.message === 'Purged' && t.type === 'success')).toBe(true)
    expect(calls.filter(c => c === LIST_URL)).toHaveLength(2)
    expect(w.find('.fixed.inset-0').exists()).toBe(false)
  })

  it('empties all trash via ConfirmDialog showing the count, then refetches', async () => {
    const { stub, calls } = trashFetch((_url, init) => {
      if (init?.method === 'DELETE') return new Response(null, { status: 204 })
    })
    vi.stubGlobal('fetch', stub)
    const w = await mountView()
    await w.findAll('button').find(b => b.text() === 'Empty trash')!.trigger('click')
    expect(calls.filter(c => c.startsWith('DELETE'))).toHaveLength(0)
    const overlay = w.find('.fixed.inset-0')
    expect(overlay.text()).toContain('2') // count of entries in the dialog copy
    await overlay.findAll('button').find(b => b.text() === 'Empty trash')!.trigger('click')
    await flushPromises()
    expect(calls).toContain('DELETE /api/v1/trash')
    expect(calls.filter(c => c.startsWith('DELETE'))).toHaveLength(1) // bulk, not per-row
    expect(toasts.some(t => t.message === 'Trash emptied' && t.type === 'success')).toBe(true)
    expect(calls.filter(c => c === LIST_URL)).toHaveLength(2)
  })

  it('shows the empty state when there is nothing trashed', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })))
    const w = await mountView()
    expect(w.text()).toContain('Trash is empty.')
  })

  it('toasts when the list fetch fails', async () => {
    vi.stubGlobal('fetch', vi.fn(async () =>
      new Response(JSON.stringify({ error: { code: 'internal', message: 'boom' } }), { status: 500 })))
    await mountView()
    expect(toasts.some(t => t.message === 'boom' && t.type === 'error')).toBe(true)
  })
})
