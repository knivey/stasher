import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import TagsView from './TagsView.vue'
import { useItemsStore } from '../stores/items'
import { toasts } from '../utils/toast'

const TAGS = [
  { id: 1, name: 'electronics', item_count: 4 },
  { id: 2, name: 'hardware', item_count: 0 },
]

interface Call { method: string; url: string; body?: string }

// Mocks every /api/v1/tags verb; PATCH responses are configurable so a test
// can simulate 409 tag_exists.
function mockFetch(opts: { patchStatus?: number; patchBody?: unknown } = {}) {
  const calls: Call[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    calls.push({ method, url, body: init?.body as string | undefined })
    if (method === 'GET')
      return new Response(JSON.stringify({ data: TAGS, total: 2 }), { status: 200 })
    if (method === 'PATCH')
      return new Response(JSON.stringify(opts.patchBody ?? {}), { status: opts.patchStatus ?? 200 })
    return new Response('', { status: 204 }) // DELETE
  }))
  return calls
}

async function mountView(calls: Call[]) {
  const router: Router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/items', component: { template: '<div>items</div>' } },
      { path: '/tags', component: TagsView },
    ],
  })
  await router.push('/tags')
  const w = mount(TagsView, { global: { plugins: [router] } })
  await flushPromises()
  expect(calls.length).toBeGreaterThan(0) // initial list fetch happened
  return { w, router }
}

beforeEach(() => { setActivePinia(createPinia()); vi.unstubAllGlobals(); toasts.splice(0) })

describe('TagsView', () => {
  it('renders rows with name and item count', async () => {
    const calls = mockFetch()
    const { w } = await mountView(calls)
    const names = w.findAll('[data-tag-name]')
    expect(names.map(n => n.text())).toEqual(['#electronics', '#hardware'])
    const counts = w.findAll('[data-tag-count]').map(c => c.text())
    expect(counts).toEqual(['4', '0'])
  })

  it('shows an empty state when there are no tags', async () => {
    const calls: Call[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ method: init?.method ?? 'GET', url })
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    const { w } = await mountView(calls)
    expect(w.text()).toContain('No tags yet.')
  })

  it('Enter in the rename input sends a name-only PATCH then refetches', async () => {
    const calls = mockFetch()
    const { w } = await mountView(calls)
    await w.findAll('[data-edit]')[0].trigger('click') // electronics
    await w.find('[data-edit-name]').setValue('circuits')
    await w.find('[data-edit-name]').trigger('keyup.enter')
    await flushPromises()
    const patch = calls.find(c => c.method === 'PATCH')
    expect(patch?.url).toBe('/api/v1/tags/1')
    expect(JSON.parse(patch!.body!)).toEqual({ name: 'circuits' })
    expect(calls.filter(c => c.method === 'GET').length).toBeGreaterThanOrEqual(2)
  })

  it('saving an unchanged name sends no PATCH', async () => {
    const calls = mockFetch()
    const { w } = await mountView(calls)
    await w.findAll('[data-edit]')[0].trigger('click')
    await w.find('[data-edit-name]').trigger('keyup.enter')
    await flushPromises()
    expect(calls.some(c => c.method === 'PATCH')).toBe(false)
  })

  it('409 tag_exists toasts the backend message and reverts the row to the old name', async () => {
    const calls = mockFetch({
      patchStatus: 409,
      patchBody: { error: { code: 'tag_exists', message: 'another tag already has this name' } },
    })
    const { w } = await mountView(calls)
    await w.findAll('[data-edit]')[0].trigger('click') // electronics
    const input = w.find('[data-edit-name]')
    await input.setValue('hardware') // collides with the other tag
    await input.trigger('keyup.enter')
    await flushPromises()
    expect(toasts.some(t => t.type === 'error' && t.message.includes('another tag already has this name'))).toBe(true)
    // the edit input reverted to the original name, still editable
    expect((w.find('[data-edit-name]').element as HTMLInputElement).value).toBe('electronics')
    expect(w.find('[data-edit-name]').exists()).toBe(true)
  })

  it('delete confirms via ConfirmDialog, calls DELETE, toasts success and refetches', async () => {
    const calls = mockFetch()
    const { w } = await mountView(calls)
    await w.findAll('[data-delete]')[0].trigger('click') // electronics
    const dialog = w.find('[role="dialog"]')
    expect(dialog.exists()).toBe(true)
    expect(dialog.text()).toContain('detaches it from all items')
    await dialog.findAll('button').at(-1)!.trigger('click') // confirm Delete
    await flushPromises()
    expect(calls.some(c => c.method === 'DELETE' && c.url === '/api/v1/tags/1')).toBe(true)
    expect(toasts.some(t => t.message === 'Tag deleted' && t.type === 'success')).toBe(true)
    expect(calls.filter(c => c.method === 'GET').length).toBeGreaterThanOrEqual(2)
  })

  it('clicking a tag name seeds the items store filter and navigates to /items', async () => {
    const calls = mockFetch()
    const store = useItemsStore()
    store.filters.page = 3
    const { w, router } = await mountView(calls)
    await w.findAll('[data-tag-name]')[0].trigger('click')
    await flushPromises()
    expect(store.filters.tag).toBe('electronics')
    expect(store.filters.page).toBe(1)
    expect(router.currentRoute.value.path).toBe('/items')
  })
})
