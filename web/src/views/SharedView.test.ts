import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import SharedView from './SharedView.vue'
import { toasts } from '../utils/toast'

const shared = { id: 9, name: 'solder tips', owner_name: 'Alice', qty: 8, photo_count: 0 }

async function mountView(): Promise<{ w: ReturnType<typeof mount>; router: Router }> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/shared', component: SharedView },
      { path: '/items/:id', component: { template: '<div>item</div>' } },
    ],
  })
  await router.push('/shared')
  const w = mount(SharedView, { global: { plugins: [router] } })
  await flushPromises()
  return { w, router }
}

beforeEach(() => { vi.unstubAllGlobals(); toasts.splice(0) })

describe('SharedView', () => {
  it('renders shared items with owner name and copies to own inventory', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${url}`)
      if (url.includes('/items/9/copy'))
        return new Response(JSON.stringify({ id: 12, name: 'solder tips', qty: 8, unit: '' }), { status: 201 })
      return new Response(JSON.stringify({ data: [shared], total: 1 }), { status: 200 })
    }))
    const { w, router } = await mountView()
    expect(calls).toContain('GET /api/v1/shared?page=1&page_size=50')
    expect(w.text()).toContain('solder tips')
    expect(w.text()).toContain('Alice')
    expect(w.text()).toContain('8')
    const copy = w.findAll('button').find(b => b.text() === 'Copy')
    expect(copy).toBeTruthy()
    await copy!.trigger('click')
    await flushPromises()
    expect(calls).toContain('POST /api/v1/items/9/copy')
    expect(toasts.some(t => t.message === 'Copied to your inventory' && t.type === 'success')).toBe(true)
    expect(router.currentRoute.value.path).toBe('/items/12')
  })

  it('toasts and refetches the list when the item was un-shared (404)', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${url}`)
      if (url.includes('/items/9/copy'))
        return new Response(JSON.stringify({ error: { code: 'not_found', message: 'item not found' } }), { status: 404 })
      return new Response(JSON.stringify({ data: [shared], total: 1 }), { status: 200 })
    }))
    const { w, router } = await mountView()
    await w.findAll('button').find(b => b.text() === 'Copy')!.trigger('click')
    await flushPromises()
    expect(toasts.some(t => t.message === 'item not found' && t.type === 'error')).toBe(true)
    expect(calls.filter(c => c === 'GET /api/v1/shared?page=1&page_size=50')).toHaveLength(2)
    expect(router.currentRoute.value.path).toBe('/shared')
  })

  it('shows the empty state when nothing is shared with you', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })))
    const { w } = await mountView()
    expect(w.text()).toContain('No shared items found.')
  })
})
