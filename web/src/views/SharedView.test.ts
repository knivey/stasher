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

  it('keeps per-card busy state: a second copy elsewhere cannot re-enable or duplicate an in-flight copy', async () => {
    const calls: string[] = []
    let releaseA!: (r: Response) => void
    const pendingA = new Promise<Response>(res => { releaseA = res })
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${url}`)
      if (url.includes('/items/9/copy')) return pendingA            // A: held until the test releases it
      if (url.includes('/items/10/copy')) return new Response(JSON.stringify({ id: 13, name: 'wrench', qty: 1, unit: '' }), { status: 201 })
      return new Response(JSON.stringify({
        data: [
          shared,
          { id: 10, name: 'wrench', owner_name: 'Bob', qty: 1, photo_count: 2 },
        ], total: 2,
      }), { status: 200 })
    }))
    const { w, router } = await mountView()
    const cards = w.findAll('[data-cards] > div')
    const btnA = cards[0].find('button')!
    const btnB = cards[1].find('button')!

    await btnA.trigger('click')                                    // A in flight, held
    expect(btnA.attributes('disabled')).toBeDefined()               // A busy
    await btnB.trigger('click')                                    // B copies concurrently
    expect(btnA.attributes('disabled')).toBeDefined()               // B must NOT re-enable A
    await btnA.trigger('click')                                    // duplicate A click while in flight
    await flushPromises()                                          // B settles; A still held
    expect(calls.filter(c => c === 'POST /api/v1/items/9/copy')).toHaveLength(1)

    releaseA(new Response(JSON.stringify({ id: 12, name: 'solder tips', qty: 8, unit: '' }), { status: 201 }))
    await flushPromises()
    expect(calls.filter(c => c === 'POST /api/v1/items/9/copy')).toHaveLength(1)
    expect(toasts.some(t => t.message === 'Copied to your inventory' && t.type === 'success')).toBe(true)
    expect(router.currentRoute.value.path).toBe('/items/12')
  })

  it('shows the empty state when nothing is shared with you', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })))
    const { w } = await mountView()
    expect(w.text()).toContain('No shared items found.')
  })

  it('shows a thumbnail image on cards when thumb_key is present', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      data: [{ id: 9, name: 'solder tips', owner_name: 'Alice', qty: 8, photo_count: 1, thumb_key: '2026/10/x.webp' }],
      total: 1,
    }), { status: 200 })))
    const { w } = await mountView()
    const cards = w.find('[data-cards]')
    const img = cards.find('img')
    expect(img.exists()).toBe(true)
    expect(img.attributes('src')).toBe('/files/2026/10/x.webp')
    expect(cards.text()).not.toContain('📷')
  })

  it('keeps the photo count badge on cards when there is no thumb_key', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      data: [{ id: 9, name: 'solder tips', owner_name: 'Alice', qty: 8, photo_count: 3 }],
      total: 1,
    }), { status: 200 })))
    const { w } = await mountView()
    const cards = w.find('[data-cards]')
    expect(cards.find('img').exists()).toBe(false)
    expect(cards.text()).toContain('📷 3')
  })

  it('shows a muted disposed badge on cards for disposed shared items', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      data: [
        { id: 9, name: 'solder tips', owner_name: 'Alice', qty: 0, disposed: true },
        { id: 10, name: 'wrench', owner_name: 'Bob', qty: 1 },
      ],
      total: 2,
    }), { status: 200 })))
    const { w } = await mountView()
    const cards = w.findAll('[data-cards] > div')
    expect(cards[0].find('[data-disposed]').exists()).toBe(true)
    expect(cards[0].text()).toContain('disposed')
    expect(cards[1].find('[data-disposed]').exists()).toBe(false)
  })
})
