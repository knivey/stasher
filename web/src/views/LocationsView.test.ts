import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { createPinia, setActivePinia } from 'pinia'
import LocationsView from './LocationsView.vue'
import type { Location } from '../api/types'
import { toasts } from '../utils/toast'
import { useItemsStore } from '../stores/items'

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

async function mountViewWithRouter(calls: Call[]): Promise<{ w: ReturnType<typeof mount>; router: Router }> {
  // router so per-row router-links resolve to real anchors
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div />' } }],
  })
  const w = mount(LocationsView, { global: { plugins: [router] } })
  await flushPromises()
  expect(calls.length).toBeGreaterThan(0) // initial list fetch happened
  return { w, router }
}

async function mountView(calls: Call[]) {
  const { w } = await mountViewWithRouter(calls)
  return w
}

beforeEach(() => {
  setActivePinia(createPinia())
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

  it('hides "Duplicate layout…" on childless locations (House and Cabinet keep it, Bin loses it)', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    const dupes = w.findAll('[data-duplicate]')
    expect(dupes).toHaveLength(2) // House (has Cabinet), Cabinet (has Bin); Bin is childless
    // the duplicate controls belong to the two parents, not to Bin's row
    const rowOf = (name: string) =>
      w.findAll('li').find(li => li.find('[data-loc-name]').text() === name)!
    expect(rowOf('House').find('[data-duplicate]').exists()).toBe(true)
    expect(rowOf('Cabinet').find('[data-duplicate]').exists()).toBe(true)
    expect(rowOf('Bin').find('[data-duplicate]').exists()).toBe(false)
  })

  it('clicking a location name seeds the items location filter and navigates to /items', async () => {
    const calls = mockFetch()
    const store = useItemsStore()
    store.filters.page = 4 // the click must reset pagination
    const { w, router } = await mountViewWithRouter(calls)
    await w.findAll('[data-loc-name]')[1].trigger('click') // Cabinet, id 2
    await flushPromises()
    expect(store.filters.location_id).toBe(2)
    expect(store.filters.page).toBe(1)
    expect(router.currentRoute.value.path).toBe('/items')
  })

  it('the edit-mode name input never navigates or filters (the click target is gone)', async () => {
    const calls = mockFetch()
    const store = useItemsStore()
    const { w, router } = await mountViewWithRouter(calls)
    await w.findAll('[data-edit]')[2].trigger('click') // Bin enters edit mode
    const input = w.find('[data-edit-name]')
    expect(input.exists()).toBe(true)
    expect(w.findAll('[data-loc-name]')).toHaveLength(2) // Bin's name button is replaced
    await input.trigger('click')
    await flushPromises()
    expect(store.filters.location_id).toBe(0)
    expect(router.currentRoute.value.path).toBe('/') // memory history starts at /
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

  // ---- Sublocations section (bulk generation + layout copy) ----

  it('generate mode previews the count and first names of a 12x6 compact flat grid', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await w.find('[data-sub-mode]').setValue('generate')
    const dims = w.findAll('[data-dim]')
    expect(dims).toHaveLength(2)
    await dims[0].setValue('12')
    await dims[1].setValue('6')
    const preview = w.find('[data-preview]')
    expect(preview.text()).toContain('72 sublocations')
    expect(preview.text()).toContain('A1, A2, A3, A4, A5, …')
  })

  it('nested 2x2 compact preview renders a header plus an indented tree in creation order', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await w.find('[data-sub-mode]').setValue('generate')
    await w.find('input[type="radio"][value="nested"]').setValue(true)
    const dims = w.findAll('[data-dim]')
    await dims[0].setValue('2')
    await dims[1].setValue('2')
    const preview = w.find('[data-preview]')
    expect(preview.text()).toContain('Will create 6 sublocations (nested tree):')
    const rows = w.findAll('[data-preview-tree-row]')
    // parents first, then their children — one 16px indent level per dimension
    expect(rows.map(r => r.text())).toEqual(['A', 'A1', 'A2', 'B', 'B1', 'B2'])
    const margins = rows.map(r => (r.element as HTMLElement).style.marginLeft)
    expect(margins).toEqual(['0px', '16px', '16px', '0px', '16px', '16px'])
    expect(w.find('[data-preview-more]').exists()).toBe(false)
  })

  it('nested preview caps the tree at 12 rows with an "and N more" line', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await w.find('[data-sub-mode]').setValue('generate')
    await w.find('input[type="radio"][value="nested"]').setValue(true)
    const dims = w.findAll('[data-dim]')
    await dims[0].setValue('5')
    await dims[1].setValue('3') // 5 intermediates + 15 leaves = 20 rows
    const rows = w.findAll('[data-preview-tree-row]')
    expect(rows).toHaveLength(12)
    expect(w.find('[data-preview-more]').text()).toContain('and 8 more')
  })

  it('nested size-1 dims show the full 12-row tree with no ellipsis', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await w.find('[data-sub-mode]').setValue('generate')
    await w.find('input[type="radio"][value="nested"]').setValue(true)
    await w.find('[data-add-dim]').trigger('click') // 3 dims
    const dims = w.findAll('[data-dim]')
    await dims[0].setValue('4')
    await dims[1].setValue('1')
    await dims[2].setValue('1')
    const preview = w.find('[data-preview]')
    // 12 nodes total (4 per level) — all visible, nothing hidden
    expect(preview.text()).toContain('12 sublocations')
    expect(w.findAll('[data-preview-tree-row]').map(r => r.text())).toEqual([
      'A', 'A1', 'A1a', 'B', 'B1', 'B1a', 'C', 'C1', 'C1a', 'D', 'D1', 'D1a',
    ])
    expect(w.find('[data-preview-more]').exists()).toBe(false)
    expect(preview.text()).not.toContain('…')
  })

  it('shows a hint line spelling out the flat vs nested semantics', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await w.find('[data-sub-mode]').setValue('generate')
    const hint = w.find('[data-structure-hint]')
    expect(hint.exists()).toBe(true)
    expect(hint.classes()).toContain('text-xs')
    expect(hint.text()).toContain('flat: one level of children')
    expect(hint.text()).toContain('nested: a level per dimension')
  })

  it('mode switching shows and hides the right inputs', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    const mode = w.find('[data-sub-mode]')
    expect(w.find('[data-sub-generate]').exists()).toBe(false)
    expect(w.find('[data-sub-copy]').exists()).toBe(false)
    await mode.setValue('generate')
    expect(w.find('[data-sub-generate]').exists()).toBe(true)
    expect(w.find('[data-sub-copy]').exists()).toBe(false)
    await mode.setValue('copy')
    expect(w.find('[data-sub-generate]').exists()).toBe(false)
    expect(w.find('[data-sub-copy]').exists()).toBe(true)
    await mode.setValue('none')
    expect(w.find('[data-sub-generate]').exists()).toBe(false)
    expect(w.find('[data-sub-copy]').exists()).toBe(false)
  })

  it('add/remove dimension buttons respect the 1-3 bounds', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await w.find('[data-sub-mode]').setValue('generate')
    expect(w.findAll('[data-dim]')).toHaveLength(2)
    await w.find('[data-remove-dim]').trigger('click')
    expect(w.findAll('[data-dim]')).toHaveLength(1)
    expect(w.find('[data-remove-dim]').attributes('disabled')).toBeDefined()
    await w.find('[data-add-dim]').trigger('click')
    await w.find('[data-add-dim]').trigger('click')
    expect(w.findAll('[data-dim]')).toHaveLength(3)
    expect(w.find('[data-add-dim]').attributes('disabled')).toBeDefined()
  })

  it('generate mode posts to /locations/bulk with the configured grid and toasts the count', async () => {
    const calls = mockFetch({ postBody: { children_created: 72 } })
    const w = await mountView(calls)
    await w.find('[data-add-name]').setValue('Shelves')
    await w.find('[data-sub-mode]').setValue('generate')
    const dims = w.findAll('[data-dim]')
    await dims[0].setValue('12')
    await dims[1].setValue('6')
    await w.find('[data-gen-prefix]').setValue('Bin')
    await w.find('form').trigger('submit')
    await flushPromises()
    const post = calls.find(c => c.method === 'POST')
    expect(post?.url).toBe('/api/v1/locations/bulk')
    expect(JSON.parse(post!.body!)).toEqual({
      name: 'Shelves', dims: [12, 6], structure: 'flat', naming: 'compact', prefix: 'Bin',
    })
    expect(toasts.some(t => t.message === 'Created Shelves + 72 sublocations' && t.type === 'success')).toBe(true)
    expect(calls.filter(c => c.method === 'GET').length).toBeGreaterThanOrEqual(2)
  })

  // ---- custom verbose labels ----

  it('label inputs render only for verbose naming, one per dimension, with default-word placeholders', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await w.find('[data-sub-mode]').setValue('generate')
    expect(w.findAll('[data-gen-label]')).toHaveLength(0) // compact default
    await w.find('input[type="radio"][value="verbose"]').setValue(true)
    let inputs = w.findAll('[data-gen-label]')
    expect(inputs).toHaveLength(2) // dims default [3, 3]
    expect(inputs.map(i => i.attributes('placeholder'))).toEqual(['Row', 'Col'])
    // 1D: the only axis defaults to "Slot"; 3D adds "Bin"
    await w.find('[data-remove-dim]').trigger('click')
    inputs = w.findAll('[data-gen-label]')
    expect(inputs).toHaveLength(1)
    expect(inputs[0].attributes('placeholder')).toBe('Slot')
    await w.find('[data-add-dim]').trigger('click')
    await w.find('[data-add-dim]').trigger('click')
    inputs = w.findAll('[data-gen-label]')
    expect(inputs.map(i => i.attributes('placeholder'))).toEqual(['Row', 'Col', 'Bin'])
  })

  it('typing labels retargets the preview names (flat and nested)', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await w.find('[data-sub-mode]').setValue('generate')
    await w.find('input[type="radio"][value="verbose"]').setValue(true)
    const dims = w.findAll('[data-dim]')
    await dims[0].setValue('2')
    await dims[1].setValue('2')
    let labels = w.findAll('[data-gen-label]')
    await labels[0].setValue('Drawer')
    await labels[1].setValue('Tray')
    expect(w.find('[data-preview]').text())
      .toContain('Drawer 1 Tray 1, Drawer 1 Tray 2, Drawer 2 Tray 1, Drawer 2 Tray 2')
    // nested intermediates use their own axis word
    await w.find('input[type="radio"][value="nested"]').setValue(true)
    expect(w.findAll('[data-preview-tree-row]').map(r => r.text()))
      .toEqual(['Drawer 1', 'Drawer 1 Tray 1', 'Drawer 1 Tray 2', 'Drawer 2', 'Drawer 2 Tray 1', 'Drawer 2 Tray 2'])
  })

  it('verbose posts trimmed labels; switching to compact hides the inputs and posts without labels', async () => {
    const calls = mockFetch({ postBody: { children_created: 4 } })
    const w = await mountView(calls)
    await w.find('[data-add-name]').setValue('Toolbox')
    await w.find('[data-sub-mode]').setValue('generate')
    await w.find('input[type="radio"][value="verbose"]').setValue(true)
    const dims = w.findAll('[data-dim]')
    await dims[0].setValue('2')
    await dims[1].setValue('2')
    const labels = w.findAll('[data-gen-label]')
    await labels[0].setValue('  Drawer  ') // trimmed before the POST
    await labels[1].setValue('Tray')
    await w.find('form').trigger('submit')
    await flushPromises()
    let post = calls.find(c => c.method === 'POST')
    expect(JSON.parse(post!.body!).labels).toEqual(['Drawer', 'Tray'])

    // back to compact: inputs disappear and the body carries no labels key
    calls.length = 0
    await w.find('[data-add-name]').setValue('Toolbox 2') // first POST cleared the field
    await w.find('input[type="radio"][value="compact"]').setValue(true)
    expect(w.findAll('[data-gen-label]')).toHaveLength(0)
    await w.find('form').trigger('submit')
    await flushPromises()
    post = calls.find(c => c.method === 'POST')
    expect(JSON.parse(post!.body!)).not.toHaveProperty('labels')
  })

  it('copy mode picker lists depth-indented tree options', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await w.find('[data-sub-mode]').setValue('copy')
    const opts = w.find('[data-copy-source]').findAll('option')
    expect(opts.map(o => o.text())).toEqual([
      '— choose a layout —', 'House', '— Cabinet', '— — Bin',
    ])
  })

  it('copy mode posts to /locations/{id}/copy-layout with the new name', async () => {
    const calls = mockFetch({ postBody: { children_created: 2 } })
    const w = await mountView(calls)
    await w.find('[data-add-name]').setValue('New cabinet')
    await w.find('[data-sub-mode]').setValue('copy')
    await w.find('[data-copy-source]').setValue('2') // Cabinet
    await w.find('form').trigger('submit')
    await flushPromises()
    const post = calls.find(c => c.method === 'POST')
    expect(post?.url).toBe('/api/v1/locations/2/copy-layout')
    expect(JSON.parse(post!.body!)).toEqual({ name: 'New cabinet' })
    expect(toasts.some(t => t.message === 'Created New cabinet + 2 sublocations' && t.type === 'success')).toBe(true)
    expect(calls.filter(c => c.method === 'GET').length).toBeGreaterThanOrEqual(2)
  })

  it('an invalid grid config shows the validation message inline and never POSTs', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await w.find('[data-add-name]').setValue('Too big')
    await w.find('[data-sub-mode]').setValue('generate')
    const dims = w.findAll('[data-dim]')
    await dims[0].setValue('100')
    await dims[1].setValue('100')
    const err = w.find('[data-preview-error]')
    expect(err.exists()).toBe(true)
    expect(err.text()).toContain('at most 1000')
    expect(w.find('[data-preview]').exists()).toBe(false)
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(calls.some(c => c.method === 'POST')).toBe(false)
  })

  it('duplicate layout dialog prefills "{name} copy", posts copy-layout with the own parent, toasts', async () => {
    const calls = mockFetch({ postBody: { children_created: 1 } })
    const w = await mountView(calls)
    await w.findAll('[data-duplicate]')[1].trigger('click') // Cabinet (parent House)
    const dialog = w.find('[role="dialog"]')
    expect(dialog.exists()).toBe(true)
    expect((dialog.find('[data-dup-name]').element as HTMLInputElement).value).toBe('Cabinet copy')
    await dialog.find('[data-dup-confirm]').trigger('click')
    await flushPromises()
    const post = calls.find(c => c.method === 'POST')
    expect(post?.url).toBe('/api/v1/locations/2/copy-layout')
    expect(JSON.parse(post!.body!)).toEqual({ name: 'Cabinet copy', parent_id: 1 })
    expect(toasts.some(t => t.message === 'Created Cabinet copy + 1 sublocations' && t.type === 'success')).toBe(true)
    expect(calls.filter(c => c.method === 'GET').length).toBeGreaterThanOrEqual(2)
  })

  it('duplicate layout dialog closes on Escape without posting', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await w.findAll('[data-duplicate]')[1].trigger('click')
    expect(w.find('[role="dialog"]').exists()).toBe(true)
    await w.find('[role="dialog"]').trigger('keydown', { key: 'Escape' })
    expect(w.find('[role="dialog"]').exists()).toBe(false)
    expect(calls.some(c => c.method === 'POST')).toBe(false)
  })
})
