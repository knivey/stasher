import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import ItemDetailView from './ItemDetailView.vue'
import { useItemsStore } from '../stores/items'
import { toasts } from '../utils/toast'

const item = {
  id: 9, name: 'M3 screws', description: 'stainless', qty: 250, unit: 'pcs',
  location_id: 3, visibility: 'shared',
  created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-02T00:00:00Z',
  tags: [{ id: 1, name: 'hardware' }],
  photos: [{ id: 11, storage_key: '2026/10/abc.jpg', caption: '', sort_order: 0, created_at: '2026-01-01T00:00:00Z' }],
  documents: [{ id: 21, storage_key: '2026/10/doc.pdf', filename: 'datasheet.pdf', mime_type: 'application/pdf', size_bytes: 2048, created_at: '2026-01-01T00:00:00Z' }],
  projects: [{ id: 31, name: '3D printer build', status: 'active', qty_used: 12 }],
}

// Two-photo detail (photos[0] is the main — backend orders by sort_order, id).
const twoPhotos = {
  ...item,
  photos: [
    { id: 11, storage_key: '2026/10/full.jpg', caption: '', sort_order: 0, created_at: '2026-01-01T00:00:00Z' },
    { id: 12, storage_key: '2026/10/second.jpg', caption: '', sort_order: 1, created_at: '2026-01-01T00:00:00Z' },
  ],
}

function okItemFetch() {
  return vi.fn(async (url: string) => {
    if (url.includes('/items/9/usage')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    if (url.includes('/projects')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    if (url.includes('/items/9')) return new Response(JSON.stringify(item), { status: 200 })
    if (url.includes('/locations/3')) return new Response(JSON.stringify({
      location: { id: 3, name: 'Bin' },
      breadcrumbs: [{ id: 1, name: 'House' }, { id: 2, name: 'Cabinet' }, { id: 3, name: 'Bin' }],
    }), { status: 200 })
    return new Response(JSON.stringify({ error: { code: 'not_found', message: 'not found' } }), { status: 404 })
  })
}

function newRouter(): Router {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/items', component: { template: '<div>items</div>' } },
      { path: '/items/:id/edit', component: { template: '<div>edit</div>' } },
      { path: '/items/:id', component: ItemDetailView },
      { path: '/projects/:id', component: { template: '<div>project</div>' } },
    ],
  })
}

async function mountView() {
  const router = newRouter()
  await router.push('/items/9')
  const w = mount(ItemDetailView, { global: { plugins: [router] } })
  await flushPromises()
  return { w, router }
}

beforeEach(() => { setActivePinia(createPinia()); vi.unstubAllGlobals(); toasts.splice(0) })

describe('ItemDetailView', () => {
  it('renders name, qty+unit, breadcrumb, visibility, gallery img, doc link, project row', async () => {
    vi.stubGlobal('fetch', okItemFetch())
    const { w } = await mountView()
    expect(w.text()).toContain('M3 screws')
    expect(w.text()).toContain('250 pcs')
    expect(w.text()).toContain('House › Cabinet › Bin')
    expect(w.text()).toContain('Shared')
    // scope to the Photos section: the hero is the first img in the document,
    // this test pins the gallery thumbnail
    const photos = w.findAll('section').find(s => s.text().includes('Photos'))
    expect(photos).toBeTruthy()
    expect(photos!.find('img').attributes('src')).toBe('/files/2026/10/abc.jpg')
    const docLink = w.find('a[href="/files/2026/10/doc.pdf"]')
    expect(docLink.exists()).toBe(true)
    expect(docLink.attributes('target')).toBe('_blank')
    expect(docLink.text()).toContain('datasheet.pdf')
    expect(docLink.text()).toContain('2 KB')
    expect(w.text()).toContain('3D printer build')
    expect(w.text()).toContain('12 used')
  })

  it('renders without a breadcrumb and skips the location fetch when the item has no location', async () => {
    const urls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      urls.push(url)
      return new Response(JSON.stringify({ ...item, location_id: null }), { status: 200 })
    }))
    const { w } = await mountView()
    expect(urls.some(u => u.includes('/locations/'))).toBe(false)
    expect(w.text()).not.toContain('›')
  })

  it('opens the lightbox on gallery photo click and closes it again', async () => {
    vi.stubGlobal('fetch', okItemFetch())
    const { w } = await mountView()
    // target the gallery thumbnail, not the hero (first img in the document)
    const thumb = w.findAll('section').find(s => s.text().includes('Photos'))!.find('img')
    await thumb.trigger('click')
    const overlay = w.find('.fixed.inset-0')
    expect(overlay.exists()).toBe(true)
    expect(overlay.find('img').attributes('src')).toBe('/files/2026/10/abc.jpg')
    await overlay.findAll('button').find(b => b.text() === '✕')!.trigger('click')
    expect(w.find('.fixed.inset-0').exists()).toBe(false)
  })

  it('navigates back to /items with a toast when the item is not found', async () => {
    vi.stubGlobal('fetch', vi.fn(async () =>
      new Response(JSON.stringify({ error: { code: 'not_found', message: 'nope' } }), { status: 404 })))
    const { router } = await mountView()
    expect(router.currentRoute.value.path).toBe('/items')
    expect(toasts.some(t => t.message === 'nope' && t.type === 'error')).toBe(true)
  })

  it('tag click seeds the items store tag filter and navigates to /items', async () => {
    vi.stubGlobal('fetch', okItemFetch())
    const store = useItemsStore()
    store.filters.page = 3
    const { w, router } = await mountView()
    const tag = w.findAll('button').find(b => b.text().includes('#hardware'))
    expect(tag).toBeTruthy()
    await tag!.trigger('click')
    await flushPromises()
    expect(store.filters.tag).toBe('hardware')
    expect(store.filters.page).toBe(1)
    expect(router.currentRoute.value.path).toBe('/items')
  })

  it('deletes a photo then refetches the item', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${url}`)
      if (url.includes('/items/9') && init?.method !== 'DELETE') return new Response(JSON.stringify(item), { status: 200 })
      if (url.includes('/locations/3')) return new Response(JSON.stringify({
        location: { id: 3, name: 'Bin' }, breadcrumbs: [{ id: 1, name: 'House' }],
      }), { status: 200 })
      return new Response(null, { status: 204 })
    }))
    const { w } = await mountView()
    const del = w.findAll('button').find(b => b.text() === '✕')
    expect(del).toBeTruthy()
    await del!.trigger('click')
    await flushPromises()
    expect(calls).toContain('DELETE /api/v1/items/9/photos/11')
    expect(calls.filter(c => c === 'GET /api/v1/items/9').length).toBe(2)
  })

  it('renders a hero image of the main photo with the full-size key', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.includes('/items/9/usage')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.includes('/items/9')) return new Response(JSON.stringify(twoPhotos), { status: 200 })
      if (url.includes('/locations/3')) return new Response(JSON.stringify({ location: {}, breadcrumbs: [] }), { status: 200 })
      return new Response(null, { status: 204 })
    }))
    const { w } = await mountView()
    const hero = w.find('[data-hero]')
    expect(hero.exists()).toBe(true)
    expect(hero.attributes('src')).toBe('/files/2026/10/full.jpg')
  })

  it('★ Make main POSTs to the main endpoint and refetches', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${url}`)
      if (url.includes('/items/9/usage')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.endsWith('/photos/12/main')) return new Response(null, { status: 204 })
      if (url.includes('/items/9') && init?.method !== 'DELETE') return new Response(JSON.stringify(twoPhotos), { status: 200 })
      if (url.includes('/locations/3')) return new Response(JSON.stringify({ location: {}, breadcrumbs: [] }), { status: 200 })
      return new Response(null, { status: 204 })
    }))
    const { w } = await mountView()
    const makeMain = w.find('button[aria-label="Make main"]')
    expect(makeMain).toBeTruthy() // on the second photo only
    await makeMain.trigger('click')
    await flushPromises()
    expect(calls).toContain('POST /api/v1/items/9/photos/12/main')
    expect(calls.filter(c => c === 'GET /api/v1/items/9')).toHaveLength(2)
  })

  it('hero click opens the lightbox overlay', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.includes('/items/9/usage')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.includes('/items/9')) return new Response(JSON.stringify(twoPhotos), { status: 200 })
      if (url.includes('/locations/3')) return new Response(JSON.stringify({ location: {}, breadcrumbs: [] }), { status: 200 })
      return new Response(null, { status: 204 })
    }))
    const { w } = await mountView()
    await w.find('[data-hero]').trigger('click')
    const overlay = w.find('.fixed.inset-0')
    expect(overlay.exists()).toBe(true)
    expect(overlay.find('img').attributes('src')).toBe('/files/2026/10/full.jpg')
  })

  it('deletes a document then refetches the item', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${url}`)
      if (url.includes('/items/9') && init?.method !== 'DELETE') return new Response(JSON.stringify(item), { status: 200 })
      if (url.includes('/locations/3')) return new Response(JSON.stringify({ location: {}, breadcrumbs: [] }), { status: 200 })
      return new Response(null, { status: 204 })
    }))
    const { w } = await mountView()
    const docs = w.findAll('section').find(s => s.text().includes('Documents'))
    expect(docs).toBeTruthy()
    const del = docs!.findAll('button').find(b => b.text() === 'Delete')
    expect(del).toBeTruthy()
    await del!.trigger('click')
    await flushPromises()
    expect(calls).toContain('DELETE /api/v1/items/9/documents/21')
    expect(calls.filter(c => c === 'GET /api/v1/items/9').length).toBe(2)
  })

  it('deletes the item after ConfirmDialog confirm, toasts, and returns to /items', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${url}`)
      if (url.includes('/items/9') && init?.method !== 'DELETE') return new Response(JSON.stringify(item), { status: 200 })
      if (url.includes('/locations/3')) return new Response(JSON.stringify({ location: {}, breadcrumbs: [] }), { status: 200 })
      return new Response(null, { status: 204 })
    }))
    const { w, router } = await mountView()
    const headerDelete = w.findAll('button').find(b => b.text() === 'Delete')
    expect(headerDelete).toBeTruthy()
    await headerDelete!.trigger('click')
    // nothing deleted yet: the ConfirmDialog is open instead of window.confirm
    expect(calls.filter(c => c.startsWith('DELETE'))).toHaveLength(0)
    const overlay = w.find('.fixed.inset-0')
    expect(overlay.exists()).toBe(true)
    expect(overlay.text()).toContain('M3 screws')
    expect(overlay.text()).toContain('The item will be moved to trash. You can restore it from Trash before it is permanently purged.')
    expect(overlay.text()).toContain("If you just used some up, 'Log usage' is probably what you want — deleting is for mistakes.")
    await overlay.findAll('button').find(b => b.text() === 'Delete')!.trigger('click')
    await flushPromises()
    expect(calls).toContain('DELETE /api/v1/items/9')
    expect(toasts.some(t => t.message === 'Item deleted' && t.type === 'success')).toBe(true)
    expect(router.currentRoute.value.path).toBe('/items')
  })

  it('cancel in the ConfirmDialog closes it without deleting', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${url}`)
      if (url.includes('/items/9')) return new Response(JSON.stringify(item), { status: 200 })
      if (url.includes('/locations/3')) return new Response(JSON.stringify({ location: {}, breadcrumbs: [] }), { status: 200 })
      return new Response(null, { status: 204 })
    }))
    const { w } = await mountView()
    await w.findAll('button').find(b => b.text() === 'Delete')!.trigger('click')
    const overlay = w.find('.fixed.inset-0')
    await overlay.findAll('button').find(b => b.text() === 'Cancel')!.trigger('click')
    await flushPromises()
    expect(calls.filter(c => c.startsWith('DELETE'))).toHaveLength(0)
    expect(w.find('.fixed.inset-0').exists()).toBe(false)
  })

  // ---- Visibility toggle ----

  // Mock that understands the detail-view surface and records PATCH bodies.
  function visibilityFetch(opts: { patchStatus?: number } = {}) {
    const calls: { method: string; url: string; body?: string }[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ method: init?.method ?? 'GET', url, body: init?.body as string | undefined })
      if (url.includes('/items/9') && init?.method === 'PATCH') {
        if (opts.patchStatus && opts.patchStatus !== 200)
          return new Response(JSON.stringify({ error: { code: 'validation_error', message: 'nope' } }), { status: opts.patchStatus })
        return new Response(JSON.stringify({ ...item, visibility: 'private' }), { status: 200 })
      }
      if (url.includes('/items/9/usage')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.includes('/items/9')) return new Response(JSON.stringify(item), { status: 200 })
      if (url.includes('/locations/3')) return new Response(JSON.stringify({ location: {}, breadcrumbs: [] }), { status: 200 })
      return new Response(null, { status: 204 })
    }))
    return calls
  }

  it('visibility badge is a toggle button; click PATCHes the flipped value, refetches, toasts success', async () => {
    const calls = visibilityFetch()
    const { w } = await mountView()
    const badge = w.find('button[data-visibility]')
    expect(badge.exists()).toBe(true)
    expect(badge.attributes('aria-label')).toBe('Toggle visibility, currently shared')
    await badge.trigger('click')
    await flushPromises()
    const patch = calls.find(c => c.method === 'PATCH')
    expect(patch?.url).toBe('/api/v1/items/9')
    expect(JSON.parse(patch!.body!)).toEqual({ visibility: 'private' })
    // refetched the item after the PATCH
    expect(calls.filter(c => c.method === 'GET' && c.url === '/api/v1/items/9')).toHaveLength(2)
    expect(toasts.some(t => t.message === 'Visibility updated' && t.type === 'success')).toBe(true)
  })

  it('flips the other way from a private item (patch body shared)', async () => {
    const calls: { method: string; url: string; body?: string }[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ method: init?.method ?? 'GET', url, body: init?.body as string | undefined })
      if (url.includes('/items/9') && init?.method === 'PATCH')
        return new Response(JSON.stringify({ ...item, visibility: 'shared' }), { status: 200 })
      if (url.includes('/items/9/usage')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.includes('/items/9')) return new Response(JSON.stringify({ ...item, visibility: 'private' }), { status: 200 })
      if (url.includes('/locations/3')) return new Response(JSON.stringify({ location: {}, breadcrumbs: [] }), { status: 200 })
      return new Response(null, { status: 204 })
    }))
    const { w } = await mountView()
    const badge = w.find('button[data-visibility]')
    expect(badge.attributes('aria-label')).toBe('Toggle visibility, currently private')
    await badge.trigger('click')
    await flushPromises()
    const patch = calls.find(c => c.method === 'PATCH')
    expect(JSON.parse(patch!.body!)).toEqual({ visibility: 'shared' })
  })

  it('toasts the ApiError and does not refetch when the visibility PATCH fails', async () => {
    const calls = visibilityFetch({ patchStatus: 400 })
    const { w } = await mountView()
    await w.find('button[data-visibility]').trigger('click')
    await flushPromises()
    expect(toasts.some(t => t.message === 'nope' && t.type === 'error')).toBe(true)
    expect(toasts.some(t => t.message === 'Visibility updated')).toBe(false)
    expect(calls.filter(c => c.method === 'GET' && c.url === '/api/v1/items/9')).toHaveLength(1)
  })

  // ---- Usage ledger ----

  const project = { id: 31, name: '3D printer build', status: 'active', notes: '', created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }

  // Mock that understands the full detail-view surface (item, usage list,
  // projects, breadcrumbs); POST /usage bodies land in `posts`.
  function usageFetch(opts: { postStatus?: number; entries?: unknown[]; total?: number } = {}) {
    const calls: { method: string; url: string; body?: string }[] = []
    const stub = vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ method: init?.method ?? 'GET', url, body: init?.body as string | undefined })
      if (url.includes('/items/9/usage') && init?.method === 'POST') {
        if (opts.postStatus && opts.postStatus !== 201)
          return new Response(JSON.stringify({ error: { code: 'validation_error', message: 'qty would go below zero' } }), { status: opts.postStatus })
        return new Response(JSON.stringify({ id: 99, delta: -3, resulting_qty: 247, kind: 'used', project_id: null, note: 'x', created_at: '2026-10-21T00:00:00Z' }), { status: 201 })
      }
      if (url.includes('/items/9/usage'))
        return new Response(JSON.stringify({ data: opts.entries ?? [], total: opts.total ?? 0 }), { status: 200 })
      if (url.includes('/projects')) return new Response(JSON.stringify({ data: [project], total: 1 }), { status: 200 })
      if (url.includes('/items/9')) return new Response(JSON.stringify(item), { status: 200 })
      if (url.includes('/locations/3')) return new Response(JSON.stringify({ location: {}, breadcrumbs: [] }), { status: 200 })
      return new Response(null, { status: 204 })
    })
    return { stub, calls }
  }

  async function openLogForm(w: VueWrapper) {
    await w.findAll('button').find(b => b.text() === 'Log usage')!.trigger('click')
    await flushPromises() // projects select loads
    return w.find('[data-log-form]')
  }

  it('logs usage: posts exactly {kind, delta, note}, omits project_id when unselected, toasts and refetches', async () => {
    const { stub, calls } = usageFetch()
    vi.stubGlobal('fetch', stub)
    const { w } = await mountView()
    await openLogForm(w)
    // kind select defaults to 'used'; set delta and note
    const delta = w.find('[data-log-delta]')
    ;(delta.element as HTMLInputElement).value = '-3'
    await delta.trigger('input')
    const note = w.find('[data-log-note]')
    ;(note.element as HTMLInputElement).value = 'x'
    await note.trigger('input')
    await w.find('[data-log-form]').trigger('submit')
    await flushPromises()
    const post = calls.find(c => c.method === 'POST')
    expect(post?.url).toBe('/api/v1/items/9/usage')
    expect(JSON.parse(post!.body!)).toEqual({ kind: 'used', delta: -3, note: 'x' })
    expect(toasts.some(t => t.message === 'Usage logged' && t.type === 'success')).toBe(true)
    // form closed, item refetched, history refetched
    expect(w.find('[data-log-form]').exists()).toBe(false)
    expect(calls.filter(c => c.method === 'GET' && c.url === '/api/v1/items/9')).toHaveLength(2)
    expect(calls.filter(c => c.method === 'GET' && c.url === '/api/v1/items/9/usage?page=1&page_size=20')).toHaveLength(2)
  })

  it('includes project_id (and omits note) when a project is selected and the note is empty', async () => {
    const { stub, calls } = usageFetch()
    vi.stubGlobal('fetch', stub)
    const { w } = await mountView()
    await openLogForm(w)
    const delta = w.find('[data-log-delta]')
    ;(delta.element as HTMLInputElement).value = '-3'
    await delta.trigger('input')
    const projectSel = w.find('[data-log-project]')
    ;(projectSel.element as HTMLSelectElement).value = '31'
    await projectSel.trigger('change')
    await w.find('[data-log-form]').trigger('submit')
    await flushPromises()
    const post = calls.find(c => c.method === 'POST')
    expect(JSON.parse(post!.body!)).toEqual({ kind: 'used', delta: -3, project_id: 31 })
  })

  it('hides the delta input for kind=disposed, shows the helper text, and posts {kind:"disposed"} only', async () => {
    const { stub, calls } = usageFetch()
    vi.stubGlobal('fetch', stub)
    const { w } = await mountView()
    await openLogForm(w)
    expect(w.find('[data-log-delta]').exists()).toBe(true) // visible for used
    const kind = w.find('[data-log-kind]')
    ;(kind.element as HTMLSelectElement).value = 'disposed'
    await kind.trigger('change')
    expect(w.find('[data-log-delta]').exists()).toBe(false) // hidden for disposed
    expect(w.text()).toContain('Sets quantity to 0 and marks the item disposed.')
    await w.find('[data-log-form]').trigger('submit')
    await flushPromises()
    const post = calls.find(c => c.method === 'POST')
    expect(JSON.parse(post!.body!)).toEqual({ kind: 'disposed' }) // no delta key
    expect(toasts.some(t => t.message === 'Usage logged' && t.type === 'success')).toBe(true)
  })

  it('renders history entries newest-first: signed delta, kind badge, quoted note, resulting qty, date, project chip', async () => {
    const entries = [
      { id: 2, delta: -3, resulting_qty: 2, kind: 'used', project_id: 31, note: 'for the printer', created_at: '2026-10-02T10:00:00Z' },
      { id: 1, delta: 5, resulting_qty: 5, kind: 'adjustment', project_id: null, note: 'initial', created_at: '2026-10-01T10:00:00Z' },
    ]
    const { stub } = usageFetch({ entries, total: 25 })
    vi.stubGlobal('fetch', stub)
    const { w } = await mountView()
    const section = w.find('[data-usage]')
    expect(section.exists()).toBe(true)
    const text = section.text()
    expect(text).toContain('−3') // U+2212, newest first
    expect(text).toContain('+5')
    expect(text.indexOf('−3')).toBeLessThan(text.indexOf('+5'))
    expect(text).toContain('→ 2 left')
    expect(text).toContain('“for the printer”')
    expect(text).toContain('2026-10-02')
    expect(section.find('[data-kind-badge]').text()).toBe('used')
    const chip = section.find('a[href="/projects/31"]')
    expect(chip.exists()).toBe(true)
    expect(chip.text()).toContain('3D printer build')
  })

  it('advances the history page via Pagination and fetches page 2', async () => {
    const entries = [{ id: 2, delta: -3, resulting_qty: 2, kind: 'used', project_id: null, note: '', created_at: '2026-10-02T10:00:00Z' }]
    const { stub, calls } = usageFetch({ entries, total: 25 })
    vi.stubGlobal('fetch', stub)
    const { w } = await mountView()
    const section = w.find('[data-usage]')
    const next = section.findAll('button').find(b => b.text() === 'Next')
    expect(next).toBeTruthy()
    await next!.trigger('click')
    await flushPromises()
    expect(calls.some(c => c.url === '/api/v1/items/9/usage?page=2&page_size=20')).toBe(true)
  })

  it('shows a muted disposed badge in the item header when the item is disposed', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.includes('/items/9/usage')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.includes('/items/9')) return new Response(JSON.stringify({ ...item, disposed: true }), { status: 200 })
      if (url.includes('/locations/3')) return new Response(JSON.stringify({ location: {}, breadcrumbs: [] }), { status: 200 })
      return new Response(null, { status: 204 })
    }))
    const { w } = await mountView()
    expect(w.find('[data-disposed]').exists()).toBe(true)
    expect(w.text()).toContain('disposed')
  })

  it('toasts and keeps the form open when the backend rejects a below-zero delta (400)', async () => {
    const { stub, calls } = usageFetch({ postStatus: 400 })
    vi.stubGlobal('fetch', stub)
    const { w } = await mountView()
    await openLogForm(w)
    const delta = w.find('[data-log-delta]')
    ;(delta.element as HTMLInputElement).value = '-999'
    await delta.trigger('input')
    await w.find('[data-log-form]').trigger('submit')
    await flushPromises()
    expect(toasts.some(t => t.message === 'qty would go below zero' && t.type === 'error')).toBe(true)
    expect(toasts.some(t => t.message === 'Usage logged')).toBe(false)
    expect(w.find('[data-log-form]').exists()).toBe(true) // form stays open
    expect(calls.filter(c => c.method === 'GET' && c.url === '/api/v1/items/9')).toHaveLength(1) // no refetch
  })
})
