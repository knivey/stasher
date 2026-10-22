import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import LocationsView from './LocationsView.vue'
import type { Location } from '../api/types'
import { toasts } from '../utils/toast'

const LOCATIONS = [
  { id: 1, name: 'House', parent_id: null, notes: '', created_at: '', updated_at: '' },
  { id: 2, name: 'Cabinet', parent_id: 1, notes: '', created_at: '', updated_at: '' },
  { id: 3, name: 'Bin', parent_id: 2, notes: '', created_at: '', updated_at: '' },
] as Location[]

interface Call { method: string; url: string; body?: string }

// Mocks every /api/v1/locations verb. POST responses are configurable so a
// test can simulate 409 location_exists / 400 cycle errors.
function mockFetch(opts: { postStatus?: number; postBody?: unknown; patchStatus?: number } = {}) {
  const calls: Call[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    calls.push({ method, url, body: init?.body as string | undefined })
    if (method === 'GET')
      return new Response(JSON.stringify({ data: LOCATIONS, total: 3 }), { status: 200 })
    if (method === 'POST')
      return new Response(JSON.stringify(opts.postBody ?? {}), { status: opts.postStatus ?? 201 })
    if (method === 'PATCH')
      return new Response(JSON.stringify({}), { status: opts.patchStatus ?? 200 })
    return new Response('', { status: 204 }) // DELETE
  }))
  return calls
}

async function mountView(calls: Call[]) {
  // router so per-row router-links resolve to real anchors
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div />' } }],
  })
  const w = mount(LocationsView, { global: { plugins: [router] } })
  await flushPromises()
  expect(calls.length).toBeGreaterThan(0) // initial list fetch happened
  return w
}

beforeEach(() => {
  vi.unstubAllGlobals()
  toasts.splice(0, toasts.length)
})

describe('LocationsView', () => {
  it('renders the tree in path order with increasing indentation by depth', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    const names = w.findAll('[data-loc-name]')
    expect(names.map(n => n.text())).toEqual(['House', 'Cabinet', 'Bin'])
    const pad = (i: number) => parseFloat((names[i].element as HTMLElement).style.paddingLeft)
    expect(pad(0)).toBe(0)
    expect(pad(1)).toBeGreaterThan(pad(0))
    expect(pad(2)).toBeGreaterThan(pad(1))
  })

  it('offers one "Add item" link per row pointing at /items/new?location=<id>', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    const links = w.findAll('[data-add-item]')
    expect(links).toHaveLength(3) // one per location row
    expect(links.map(l => l.attributes('href'))).toEqual([
      '/items/new?location=1', '/items/new?location=2', '/items/new?location=3',
    ])
  })

  it('Add posts {name} without parent_id for a root location', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await w.find('[data-add-name]').setValue('Shed')
    await w.find('form').trigger('submit')
    await flushPromises()
    const post = calls.find(c => c.method === 'POST')
    expect(post?.url).toBe('/api/v1/locations')
    expect(JSON.parse(post!.body!)).toEqual({ name: 'Shed' })
    // refetched the list after the mutation
    expect(calls.filter(c => c.method === 'GET').length).toBeGreaterThanOrEqual(2)
  })

  it('surfaces a 409 location_exists from POST as toast text', async () => {
    const calls = mockFetch({
      postStatus: 409,
      postBody: { error: { code: 'location_exists', message: 'You already have a location named Shed there.' } },
    })
    const w = await mountView(calls)
    await w.find('[data-add-name]').setValue('Shed')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(toasts.some(t => t.type === 'error' && t.message.includes('You already have a location named Shed there.'))).toBe(true)
  })

  it('rename sends a name-only PATCH then refetches', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await w.findAll('[data-edit]')[1].trigger('click') // Cabinet
    await w.find('[data-edit-name]').setValue('Drawer')
    await w.find('[data-save]').trigger('click')
    await flushPromises()
    const patch = calls.find(c => c.method === 'PATCH')
    expect(patch?.url).toBe('/api/v1/locations/2')
    expect(JSON.parse(patch!.body!)).toEqual({ name: 'Drawer' })
    expect(calls.filter(c => c.method === 'GET').length).toBeGreaterThanOrEqual(2)
  })

  it('notes are sent only when edited (dirty flag), with no other fields', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await w.findAll('[data-edit]')[2].trigger('click') // Bin
    await w.find('[data-edit-notes]').setValue('Small parts')
    await w.find('[data-save]').trigger('click')
    await flushPromises()
    const patch = calls.find(c => c.method === 'PATCH')
    expect(patch?.url).toBe('/api/v1/locations/3')
    expect(JSON.parse(patch!.body!)).toEqual({ notes: 'Small parts' })
  })

  it('delete confirms, calls DELETE, toasts success and refetches', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await w.findAll('[data-delete]')[0].trigger('click') // House
    const dialog = w.find('[role="dialog"]')
    expect(dialog.exists()).toBe(true)
    expect(dialog.text()).toContain('Children move up')
    await dialog.findAll('button').at(-1)!.trigger('click') // confirm Delete
    await flushPromises()
    expect(calls.some(c => c.method === 'DELETE' && c.url === '/api/v1/locations/1')).toBe(true)
    expect(toasts.some(t => t.message === 'Location deleted' && t.type === 'success')).toBe(true)
    expect(calls.filter(c => c.method === 'GET').length).toBeGreaterThanOrEqual(2)
  })
})
