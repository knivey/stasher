import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
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

  it('Make main: toasts the ApiError and leaves the gallery unchanged when the POST fails', async () => {
    const calls: { method: string; url: string }[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ method: init?.method ?? 'GET', url })
      if (url.endsWith('/photos/12/main'))
        return new Response(JSON.stringify({ error: { code: 'internal', message: 'main photo failed' } }), { status: 500 })
      if (url.includes('/items/9/usage')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.includes('/items/9')) return new Response(JSON.stringify(twoPhotos), { status: 200 })
      if (url.includes('/locations/3')) return new Response(JSON.stringify({ location: {}, breadcrumbs: [] }), { status: 200 })
      return new Response(null, { status: 204 })
    }))
    const { w } = await mountView()
    await w.find('button[aria-label="Make main"]').trigger('click')
    await flushPromises()
    expect(toasts.some(t => t.message === 'main photo failed' && t.type === 'error')).toBe(true)
    // no refetch after the failure: exactly the initial item GET
    expect(calls.filter(c => c.method === 'GET' && c.url === '/api/v1/items/9')).toHaveLength(1)
    // gallery state unchanged: both photos still render, Make main still offered
    const photos = w.findAll('section').find(s => s.text().includes('Photos'))
    expect(photos!.findAll('img')).toHaveLength(2)
    expect(w.find('button[aria-label="Make main"]').exists()).toBe(true)
  })

  it('Make main: two rapid clicks issue exactly one POST (in-flight guard)', async () => {
    const calls: { method: string; url: string }[] = []
    let releasePost!: (r: Response) => void
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ method: init?.method ?? 'GET', url })
      if (url.endsWith('/photos/12/main'))
        return new Promise<Response>(res => { releasePost = res }) // hold: clicks stay inside the guard window
      if (url.includes('/items/9/usage')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.includes('/items/9')) return new Response(JSON.stringify(twoPhotos), { status: 200 })
      if (url.includes('/locations/3')) return new Response(JSON.stringify({ location: {}, breadcrumbs: [] }), { status: 200 })
      return new Response(null, { status: 204 })
    }))
    const { w } = await mountView()
    const makeMain = w.find('button[aria-label="Make main"]')
    const first = makeMain.trigger('click')
    const second = makeMain.trigger('click') // while the first POST is still pending
    await Promise.all([first, second])
    expect(calls.filter(c => c.method === 'POST' && c.url.endsWith('/photos/12/main'))).toHaveLength(1)
    releasePost(new Response(null, { status: 204 }))
    await flushPromises()
    expect(calls.filter(c => c.method === 'POST' && c.url.endsWith('/photos/12/main'))).toHaveLength(1) // still exactly one
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
    // concise message; the usage hint moved into the styled hint element
    expect(overlay.find('p.whitespace-pre-line').text())
      .toBe('The item will be moved to trash. You can restore it from Trash before it is permanently purged.')
    const hint = overlay.find('p.text-red-600')
    expect(hint.exists()).toBe(true)
    expect(hint.classes()).toContain('font-bold')
    expect(hint.classes()).toContain('dark:text-red-400')
    expect(hint.text()).toBe("If you just used some up, 'Log usage' is probably what you want — deleting is for mistakes.")
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

  it('history project chip prefers the ledger row\'s project_name, falling back when it is null', async () => {
    const entries = [
      { id: 3, delta: -1, resulting_qty: 1, kind: 'used', project_id: 31, project_name: 'Speaker repair', note: '', created_at: '2026-10-03T10:00:00Z' },
      { id: 2, delta: -2, resulting_qty: 2, kind: 'used', project_id: 31, project_name: null, note: '', created_at: '2026-10-02T10:00:00Z' },
    ]
    const { stub } = usageFetch({ entries, total: 2 })
    vi.stubGlobal('fetch', stub)
    const { w } = await mountView()
    // scope to the history section: "Used in projects" links /projects/31 too
    const chips = w.find('[data-usage]').findAll('a[href="/projects/31"]')
    expect(chips).toHaveLength(2)
    expect(chips[0].text()).toContain('Speaker repair') // server-resolved name wins
    expect(chips[0].text()).not.toContain('3D printer build')
    expect(chips[1].text()).toContain('3D printer build') // null (deleted project / old payload) → client fallback
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

  // ---- Notes (timestamped log, newest first; add + delete only) ----

  const itemWithNotes = {
    ...item,
    notes: [
      { id: 72, body: 'second note', created_at: '2026-10-22T00:00:00Z' },
      { id: 71, body: 'first note', created_at: '2026-10-21T00:00:00Z' },
    ],
  }

  // Mock that understands the detail-view surface plus the notes endpoints;
  // every request lands in `calls`.
  function notesFetch(opts: { item?: unknown; postStatus?: number } = {}) {
    const shown = opts.item ?? item
    const calls: { method: string; url: string; body?: string }[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ method: init?.method ?? 'GET', url, body: init?.body as string | undefined })
      if (url.includes('/items/9/notes') && init?.method === 'POST') {
        if (opts.postStatus)
          return new Response(JSON.stringify({ error: { code: 'validation_error', message: 'note must be 1-2000 characters' } }), { status: opts.postStatus })
        return new Response(JSON.stringify({ id: 73, body: JSON.parse(String(init!.body)).body, created_at: '2026-10-23T00:00:00Z' }), { status: 201 })
      }
      if (url.includes('/items/9/notes') && init?.method === 'DELETE') return new Response(null, { status: 204 })
      if (url.includes('/items/9/usage')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.includes('/items/9')) return new Response(JSON.stringify(shown), { status: 200 })
      if (url.includes('/locations/3')) return new Response(JSON.stringify({ location: {}, breadcrumbs: [] }), { status: 200 })
      return new Response(null, { status: 204 })
    }))
    return calls
  }

  it('renders notes newest-first with body and date', async () => {
    notesFetch({ item: itemWithNotes })
    const { w } = await mountView()
    const section = w.find('[data-notes]')
    expect(section.exists()).toBe(true)
    const text = section.text()
    expect(text).toContain('second note')
    expect(text).toContain('first note')
    expect(text.indexOf('second note')).toBeLessThan(text.indexOf('first note'))
    expect(text).toContain('2026-10-22')
    expect(text).toContain('2026-10-21')
  })

  it('adds a note: POSTs the trimmed body to /notes, refetches, clears the textarea, toasts', async () => {
    const calls = notesFetch()
    const { w } = await mountView()
    const ta = w.find('[data-note-body]')
    expect(ta.exists()).toBe(true)
    ;(ta.element as HTMLTextAreaElement).value = '  fresh idea  '
    await ta.trigger('input')
    await w.find('[data-note-form]').trigger('submit')
    await flushPromises()
    const post = calls.find(c => c.method === 'POST')
    expect(post?.url).toBe('/api/v1/items/9/notes')
    expect(JSON.parse(post!.body!)).toEqual({ body: 'fresh idea' })
    // refetched the item after the POST
    expect(calls.filter(c => c.method === 'GET' && c.url === '/api/v1/items/9')).toHaveLength(2)
    expect((w.find('[data-note-body]').element as HTMLTextAreaElement).value).toBe('')
    expect(toasts.some(t => t.message === 'Note added' && t.type === 'success')).toBe(true)
  })

  it('disables the add-note button on empty or whitespace-only body and shows the empty state', async () => {
    notesFetch()
    const { w } = await mountView()
    const btn = w.find('[data-note-add]')
    expect((btn.element as HTMLButtonElement).disabled).toBe(true)
    const ta = w.find('[data-note-body]')
    ;(ta.element as HTMLTextAreaElement).value = '   '
    await ta.trigger('input')
    expect((btn.element as HTMLButtonElement).disabled).toBe(true)
    expect(w.find('[data-notes]').text()).toContain('No notes yet.')
    ;(ta.element as HTMLTextAreaElement).value = 'ok'
    await ta.trigger('input')
    expect((btn.element as HTMLButtonElement).disabled).toBe(false)
  })

  it('deletes a note via DELETE then refetches the item', async () => {
    const calls = notesFetch({ item: itemWithNotes })
    const { w } = await mountView()
    const del = w.find('[data-notes]').findAll('button').find(b => b.text() === 'Delete')
    expect(del).toBeTruthy()
    await del!.trigger('click')
    await flushPromises()
    expect(calls.some(c => c.method === 'DELETE' && c.url === '/api/v1/items/9/notes/72')).toBe(true)
    expect(calls.filter(c => c.method === 'GET' && c.url === '/api/v1/items/9')).toHaveLength(2)
    expect(toasts.some(t => t.message === 'Note deleted' && t.type === 'success')).toBe(true)
  })

  it('deleting the only note falls back to the "No notes yet." empty state', async () => {
    const singleNote = { ...item, notes: [{ id: 71, body: 'first note', created_at: '2026-10-21T00:00:00Z' }] }
    let deleted = false
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      if (url.includes('/items/9/notes/71') && init?.method === 'DELETE') {
        deleted = true
        return new Response(null, { status: 204 })
      }
      if (url.includes('/items/9/usage')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      // after the DELETE the refetch serves the item with notes gone
      if (url.includes('/items/9')) return new Response(JSON.stringify(deleted ? item : singleNote), { status: 200 })
      if (url.includes('/locations/3')) return new Response(JSON.stringify({ location: {}, breadcrumbs: [] }), { status: 200 })
      return new Response(null, { status: 204 })
    }))
    const { w } = await mountView()
    expect(w.find('[data-notes]').text()).toContain('first note')
    const del = w.find('[data-notes]').findAll('button').find(b => b.text() === 'Delete')
    await del!.trigger('click')
    await flushPromises()
    const text = w.find('[data-notes]').text()
    expect(text).toContain('No notes yet.')
    expect(text).not.toContain('first note')
  })

  it('toasts the error and preserves the textarea when the note POST fails', async () => {
    const calls = notesFetch({ postStatus: 400 })
    const { w } = await mountView()
    const ta = w.find('[data-note-body]')
    ;(ta.element as HTMLTextAreaElement).value = 'a note'
    await ta.trigger('input')
    await w.find('[data-note-form]').trigger('submit')
    await flushPromises()
    expect(toasts.some(t => t.message === 'note must be 1-2000 characters' && t.type === 'error')).toBe(true)
    expect(toasts.some(t => t.message === 'Note added')).toBe(false)
    expect((w.find('[data-note-body]').element as HTMLTextAreaElement).value).toBe('a note')
    expect(calls.filter(c => c.method === 'GET' && c.url === '/api/v1/items/9')).toHaveLength(1) // no refetch
  })
})

describe('ItemDetailView product url & fetch', () => {
  const itemWithUrl = { ...item, product_url: 'https://example.com/m3-screws' }

  // Mock that understands the detail-view surface plus the fetch/job endpoints;
  // every request lands in `calls`. `jobs` is a queue of poll responses — once
  // empty, every poll answers {"status":"running"} forever.
  function fetchMock(opts: {
    item?: unknown; jobs?: unknown[]; jobStatus?: number; postStatus?: number
    search?: unknown; searchStatus?: number; searchCode?: string; searchMessage?: string
    holdSearch?: (release: (r: Response) => void) => void
  } = {}) {
    const calls: { method: string; url: string; body?: string }[] = []
    const jobs = [...(opts.jobs ?? [])]
    const shown = opts.item ?? item
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ method: init?.method ?? 'GET', url, body: init?.body as string | undefined })
      if (url.includes('/items/9/search-documents')) {
        if (opts.searchStatus)
          return new Response(JSON.stringify({ error: { code: opts.searchCode ?? 'search_disabled', message: opts.searchMessage ?? 'set brave_api_key in config to enable document search' } }), { status: opts.searchStatus })
        if (opts.holdSearch)
          return new Promise<Response>(res => opts.holdSearch!(res))
        return new Response(JSON.stringify(opts.search ?? { results: [], rejected: 0 }), { status: 200 })
      }
      if (url.includes('/items/9/fetch')) {
        if (opts.postStatus) return new Response(JSON.stringify({ error: { code: 'validation_error', message: 'url must be an http(s) URL' } }), { status: opts.postStatus })
        return new Response(JSON.stringify({ job_id: 'j1' }), { status: 202 })
      }
      if (url.includes('/jobs/')) {
        if (opts.jobStatus === 404)
          return new Response(JSON.stringify({ error: { code: 'not_found', message: 'gone' } }), { status: 404 })
        return new Response(JSON.stringify(jobs.length ? jobs.shift() : { status: 'running' }), { status: 200 })
      }
      if (url.includes('/items/9/usage')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.includes('/items/9')) return new Response(JSON.stringify(shown), { status: 200 })
      if (url.includes('/locations/3')) return new Response(JSON.stringify({ location: {}, breadcrumbs: [] }), { status: 200 })
      return new Response(null, { status: 204 })
    }))
    return calls
  }

  async function openFetchDialog(w: VueWrapper) {
    await w.findAll('button').find(b => b.text() === 'Fetch from URL…')!.trigger('click')
    return w.find('[data-fetch-dialog]')
  }

  function jobCalls(calls: { url: string }[]) {
    return calls.filter(c => c.url.includes('/jobs/j1'))
  }

  afterEach(() => vi.useRealTimers())

  it('renders product_url as an external link with a small Archive button', async () => {
    fetchMock({ item: itemWithUrl })
    const { w } = await mountView()
    const line = w.find('[data-product-url]')
    expect(line.exists()).toBe(true)
    const a = line.find('a')
    expect(a.attributes('href')).toBe('https://example.com/m3-screws')
    expect(a.attributes('target')).toBe('_blank')
    expect(a.attributes('rel')).toBe('noopener')
    const archive = line.findAll('button').find(b => b.text() === 'Archive')
    expect(archive).toBeTruthy()
  })

  it('renders no product url line when the item has none', async () => {
    fetchMock()
    const { w } = await mountView()
    expect(w.find('[data-product-url]').exists()).toBe(false)
  })

  it('Archive button POSTs /fetch with mode archive and the item URL, then follows the job to done', async () => {
    vi.useFakeTimers()
    const calls = fetchMock({ item: itemWithUrl, jobs: [{ status: 'running' }, { status: 'done', document_id: 55 }] })
    const { w } = await mountView()
    await w.find('[data-product-url]').findAll('button').find(b => b.text() === 'Archive')!.trigger('click')
    await flushPromises()
    const post = calls.find(c => c.url === '/api/v1/items/9/fetch')
    expect(post?.method).toBe('POST')
    expect(JSON.parse(post!.body!)).toEqual({ url: 'https://example.com/m3-screws', mode: 'archive' })
    expect(toasts.some(t => t.message === 'Fetching…' && t.type === 'info')).toBe(true)
    expect(jobCalls(calls)).toHaveLength(0) // polling starts only after the 2s wait
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    expect(jobCalls(calls)).toHaveLength(1)
    expect(toasts.some(t => t.message === 'Added document')).toBe(false) // still running
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    expect(toasts.some(t => t.message === 'Added document' && t.type === 'success')).toBe(true)
    expect(calls.filter(c => c.method === 'GET' && c.url === '/api/v1/items/9')).toHaveLength(2) // refetched
  })

  it('fetch dialog opens from the Documents header; .pdf preselects direct, other URLs archive; override wins; Escape closes', async () => {
    fetchMock()
    const { w } = await mountView()
    const docs = w.findAll('section').find(s => s.text().includes('Documents'))
    expect(docs!.findAll('button').some(b => b.text() === 'Fetch from URL…')).toBe(true)
    const dlg = await openFetchDialog(w)
    expect(dlg.exists()).toBe(true)
    const url = dlg.find('[data-fetch-url]')
    const mode = dlg.find('[data-fetch-mode]')
    expect((mode.element as HTMLSelectElement).value).toBe('archive') // default
    await url.setValue('https://example.com/datasheet.pdf')
    expect((mode.element as HTMLSelectElement).value).toBe('direct') // .pdf → direct
    await url.setValue('https://example.com/page?q=datasheet.pdf') // .pdf only in the query: ignored
    expect((mode.element as HTMLSelectElement).value).toBe('archive')
    await mode.setValue('direct') // user override, no further url input
    expect((mode.element as HTMLSelectElement).value).toBe('direct')
    await dlg.trigger('keydown.escape')
    expect(w.find('[data-fetch-dialog]').exists()).toBe(false)
  })

  it('submit is disabled while the URL is empty and while a job is in flight', async () => {
    vi.useFakeTimers()
    const calls = fetchMock({ jobs: [{ status: 'running' }, { status: 'failed', error: 'chromium not configured' }] })
    const { w } = await mountView()
    const dlg = await openFetchDialog(w)
    const submit = dlg.find('[data-fetch-submit]')
    expect((submit.element as HTMLButtonElement).disabled).toBe(true) // empty URL
    await dlg.find('[data-fetch-url]').setValue('https://example.com/page')
    expect((submit.element as HTMLButtonElement).disabled).toBe(false)
    await dlg.find('form').trigger('submit')
    await flushPromises()
    expect(calls.some(c => c.method === 'POST' && c.url === '/api/v1/items/9/fetch')).toBe(true)
    expect((w.find('[data-fetch-submit]').element as HTMLButtonElement).disabled).toBe(true) // job in flight
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises() // poll 1 → running: still busy
    expect((w.find('[data-fetch-submit]').element as HTMLButtonElement).disabled).toBe(true)
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises() // poll 2 → failed
    expect(toasts.some(t => t.message === 'chromium not configured' && t.type === 'error')).toBe(true)
    expect(toasts.some(t => t.message === 'Added document')).toBe(false)
    expect((w.find('[data-fetch-submit]').element as HTMLButtonElement).disabled).toBe(false) // retryable
  })

  it('failed job: error toast with the job error, dialog stays open, no refetch', async () => {
    vi.useFakeTimers()
    const calls = fetchMock({ jobs: [{ status: 'failed', error: 'private address blocked' }] })
    const { w } = await mountView()
    const dlg = await openFetchDialog(w)
    await dlg.find('[data-fetch-url]').setValue('https://example.com/page')
    await dlg.find('form').trigger('submit')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    expect(toasts.some(t => t.message === 'private address blocked' && t.type === 'error')).toBe(true)
    expect(toasts.some(t => t.message === 'Added document')).toBe(false)
    expect(w.find('[data-fetch-dialog]').exists()).toBe(true) // stays open for a retry
    expect(calls.filter(c => c.method === 'GET' && c.url === '/api/v1/items/9')).toHaveLength(1) // no refetch
  })

  it('happy path: POST body {url, mode}, Fetching… toast, polls running→done, Added document toast + refetch + close', async () => {
    vi.useFakeTimers()
    const calls = fetchMock({ jobs: [{ status: 'running' }, { status: 'done', document_id: 55 }] })
    const { w } = await mountView()
    const dlg = await openFetchDialog(w)
    await dlg.find('[data-fetch-url]').setValue('https://example.com/page') // no .pdf → archive
    await dlg.find('form').trigger('submit')
    await flushPromises()
    const post = calls.find(c => c.method === 'POST' && c.url === '/api/v1/items/9/fetch')
    expect(JSON.parse(post!.body!)).toEqual({ url: 'https://example.com/page', mode: 'archive' })
    expect(toasts.some(t => t.message === 'Fetching…' && t.type === 'info')).toBe(true)
    expect(w.find('[data-fetch-dialog]').exists()).toBe(true) // open while polling
    expect(jobCalls(calls)).toHaveLength(0)
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    expect(jobCalls(calls)).toHaveLength(1)
    expect(toasts.some(t => t.message === 'Added document')).toBe(false)
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    expect(toasts.some(t => t.message === 'Added document' && t.type === 'success')).toBe(true)
    expect(calls.filter(c => c.method === 'GET' && c.url === '/api/v1/items/9')).toHaveLength(2)
    expect(w.find('[data-fetch-dialog]').exists()).toBe(false) // closed on done
  })

  it('job GET 404 (interrupted): "fetch interrupted" error toast, no success', async () => {
    vi.useFakeTimers()
    const calls = fetchMock({ jobStatus: 404 })
    const { w } = await mountView()
    const dlg = await openFetchDialog(w)
    await dlg.find('[data-fetch-url]').setValue('https://example.com/page')
    await dlg.find('form').trigger('submit')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    expect(toasts.some(t => t.message === 'fetch interrupted' && t.type === 'error')).toBe(true)
    expect(toasts.some(t => t.message === 'Added document')).toBe(false)
    expect(calls.filter(c => c.method === 'GET' && c.url === '/api/v1/items/9')).toHaveLength(1) // no refetch
  })

  it('fetch POST rejection (ApiError): error toast, no polling started', async () => {
    vi.useFakeTimers()
    const calls = fetchMock({ postStatus: 400 })
    const { w } = await mountView()
    const dlg = await openFetchDialog(w)
    await dlg.find('[data-fetch-url]').setValue('not a url')
    await dlg.find('form').trigger('submit')
    await flushPromises()
    expect(toasts.some(t => t.message === 'url must be an http(s) URL' && t.type === 'error')).toBe(true)
    expect(toasts.some(t => t.message === 'Fetching…')).toBe(false)
    expect(jobCalls(calls)).toHaveLength(0)
  })

  it('gives up after 45 polls (90s deadline) with a timeout toast', async () => {
    vi.useFakeTimers()
    const calls = fetchMock({ jobs: [{ status: 'running' }] }) // running forever
    const { w } = await mountView()
    const dlg = await openFetchDialog(w)
    await dlg.find('[data-fetch-url]').setValue('https://example.com/page')
    await dlg.find('form').trigger('submit')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(90_000)
    await flushPromises()
    expect(jobCalls(calls)).toHaveLength(45)
    expect(toasts.some(t => t.type === 'error' && t.message.includes('timed out'))).toBe(true)
    expect(toasts.some(t => t.message === 'Added document')).toBe(false)
  })

  it('stops polling when the view unmounts', async () => {
    vi.useFakeTimers()
    const calls = fetchMock({ jobs: [{ status: 'running' }] })
    const { w } = await mountView()
    const dlg = await openFetchDialog(w)
    await dlg.find('[data-fetch-url]').setValue('https://example.com/page')
    await dlg.find('form').trigger('submit')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    expect(jobCalls(calls)).toHaveLength(1)
    w.unmount()
    await vi.advanceTimersByTimeAsync(120_000)
    await flushPromises()
    expect(jobCalls(calls)).toHaveLength(1) // cancelled: no further polls
    expect(toasts.some(t => t.message.includes('timed out'))).toBe(false)
  })

  // ---- Find document (Brave search → verified PDFs) ----

  async function openFindDialog(w: VueWrapper) {
    const docs = w.findAll('section').find(s => s.text().includes('Documents'))
    expect(docs!.findAll('button').some(b => b.text() === 'Find document…')).toBe(true)
    await docs!.findAll('button').find(b => b.text() === 'Find document…')!.trigger('click')
    return w.find('[data-find-dialog]')
  }

  it('find dialog opens from the Documents header with the query prefilled from the item name', async () => {
    fetchMock()
    const { w } = await mountView()
    const dlg = await openFindDialog(w)
    expect(dlg.exists()).toBe(true)
    expect((dlg.find('[data-find-query]').element as HTMLInputElement).value).toBe('M3 screws')
    // Escape closes, same dup-dialog pattern as the fetch dialog
    await dlg.trigger('keydown.escape')
    expect(w.find('[data-find-dialog]').exists()).toBe(false)
  })

  it('search button is disabled while the query is empty and while a search is in flight', async () => {
    let release!: (r: Response) => void
    fetchMock({ holdSearch: res => { release = res } })
    const { w } = await mountView()
    const dlg = await openFindDialog(w)
    const search = dlg.find('[data-find-search]')
    expect((search.element as HTMLButtonElement).disabled).toBe(false) // prefilled
    await dlg.find('[data-find-query]').setValue('   ')
    expect((search.element as HTMLButtonElement).disabled).toBe(true) // blank
    await dlg.find('[data-find-query]').setValue('lm358 datasheet')
    expect((search.element as HTMLButtonElement).disabled).toBe(false)
    await dlg.find('form').trigger('submit')
    await flushPromises() // search POST still held open
    expect((w.find('[data-find-search]').element as HTMLButtonElement).disabled).toBe(true) // busy
    release(new Response(JSON.stringify({ results: [], rejected: 0 }), { status: 200 }))
    await flushPromises()
    expect((w.find('[data-find-search]').element as HTMLButtonElement).disabled).toBe(false)
  })

  it('search POSTs the trimmed query and renders verified candidates with title, host and humanized size', async () => {
    const calls = fetchMock({
      search: {
        results: [
          { title: 'LM358 datasheet (Rev. H)', url: 'https://www.ti.com/lit/lm358.pdf', host: 'www.ti.com', size: 1_572_864 },
          { title: 'LM358 clone', url: 'https://pdf1.alldatasheet.com/datasheet.pdf', host: 'pdf1.alldatasheet.com' },
        ],
        rejected: 3,
      },
    })
    const { w } = await mountView()
    const dlg = await openFindDialog(w)
    await dlg.find('[data-find-query]').setValue('  lm358 datasheet  ')
    await dlg.find('form').trigger('submit')
    await flushPromises()
    const post = calls.find(c => c.method === 'POST' && c.url === '/api/v1/items/9/search-documents')
    expect(JSON.parse(post!.body!)).toEqual({ query: 'lm358 datasheet' })
    const results = w.findAll('[data-find-result]')
    expect(results).toHaveLength(2)
    expect(results[0].text()).toContain('LM358 datasheet (Rev. H)')
    expect(results[0].text()).toContain('www.ti.com')
    expect(results[0].text()).toContain('1.5 MB')
    expect(results[1].text()).toContain('pdf1.alldatasheet.com')
    expect(results[1].text()).toContain('—') // unknown size
    // rejected line is muted and present next to results
    expect(w.find('[data-find-dialog]').text()).toContain('3 results skipped')
  })

  it('candidate click closes the dialog and downloads via the fetch flow (direct mode + polling)', async () => {
    vi.useFakeTimers()
    const calls = fetchMock({
      search: {
        results: [{ title: 'LM358 datasheet', url: 'https://www.ti.com/lit/lm358.pdf', host: 'www.ti.com', size: 1000 }],
        rejected: 0,
      },
      jobs: [{ status: 'done', document_id: 55 }],
    })
    const { w } = await mountView()
    const dlg = await openFindDialog(w)
    await dlg.find('form').trigger('submit')
    await flushPromises()
    await w.find('[data-find-result]').trigger('click')
    await flushPromises()
    // find dialog closed, fetch job started with the chosen URL
    expect(w.find('[data-find-dialog]').exists()).toBe(false)
    const post = calls.find(c => c.method === 'POST' && c.url === '/api/v1/items/9/fetch')
    expect(JSON.parse(post!.body!)).toEqual({ url: 'https://www.ti.com/lit/lm358.pdf', mode: 'direct' })
    expect(toasts.some(t => t.message === 'Fetching…' && t.type === 'info')).toBe(true)
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    expect(toasts.some(t => t.message === 'Added document' && t.type === 'success')).toBe(true)
    expect(calls.filter(c => c.method === 'GET' && c.url === '/api/v1/items/9')).toHaveLength(2) // refetched
  })

  it('search_disabled (400) toasts the configure hint instead of the raw message', async () => {
    fetchMock({ searchStatus: 400, searchCode: 'search_disabled' })
    const { w } = await mountView()
    const dlg = await openFindDialog(w)
    await dlg.find('form').trigger('submit')
    await flushPromises()
    expect(toasts.some(t => t.message === 'Configure brave_api_key to enable search' && t.type === 'error')).toBe(true)
  })

  it('empty results with rejected candidates show the counted no-PDFs message', async () => {
    fetchMock({ search: { results: [], rejected: 5 } })
    const { w } = await mountView()
    const dlg = await openFindDialog(w)
    await dlg.find('form').trigger('submit')
    await flushPromises()
    expect(w.find('[data-find-dialog]').text())
      .toContain('No downloadable PDFs found (5 candidates checked).')
    // the skipped-count line is intentionally withheld when nothing verified
    expect(w.find('[data-find-dialog]').text()).not.toContain('skipped')
  })

  it('empty results with zero rejected keep the plain no-PDFs message', async () => {
    fetchMock({ search: { results: [], rejected: 0 } })
    const { w } = await mountView()
    const dlg = await openFindDialog(w)
    await dlg.find('form').trigger('submit')
    await flushPromises()
    expect(w.find('[data-find-dialog]').text())
      .toContain('No downloadable PDFs found — try a more specific part number.')
    expect(w.find('[data-find-dialog]').text()).not.toContain('candidates checked')
  })
})
