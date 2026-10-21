import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
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

function okItemFetch() {
  return vi.fn(async (url: string) => {
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
    expect(w.find('img').attributes('src')).toBe('/files/2026/10/abc.jpg')
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

  it('opens the lightbox on photo click and closes it again', async () => {
    vi.stubGlobal('fetch', okItemFetch())
    const { w } = await mountView()
    await w.find('img').trigger('click')
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
})
