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
  // 3 levels deep on purpose: House has ONE direct child (Cabinet) but THREE
  // total descendants (Cabinet, Bin, Tray) — so the delete dialog's pinned
  // sublocation count can only be the total the BFS walk computes.
  { id: 4, name: 'Tray', parent_id: 3, notes: '', created_at: '', updated_at: '' },
] as Location[]

interface Call { method: string; url: string; body?: string }

// Mocks every /api/v1/locations verb. POST responses are configurable so a
// test can simulate 409 location_exists / 400 cycle errors. `locations`
// swaps in a mutable list (GET reads it live, so a test can grow the tree
// between the POST and the refetch); `onPost` fires inside the fetch mock,
// before the POST response resolves — the hook to append created rows.
function mockFetch(opts: {
  postStatus?: number; postBody?: unknown; patchStatus?: number
  locations?: Location[]; onPost?: (url: string, body?: string) => void
} = {}) {
  const locs = opts.locations ?? LOCATIONS
  const calls: Call[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    calls.push({ method, url, body: init?.body as string | undefined })
    if (method === 'GET')
      return new Response(JSON.stringify({ data: locs, total: locs.length }), { status: 200 })
    if (method === 'POST') {
      opts.onPost?.(url, init?.body as string | undefined)
      return new Response(JSON.stringify(opts.postBody ?? {}), { status: opts.postStatus ?? 201 })
    }
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

// The tree renders collapsed by default; expand every collapsed chevron
// until none remain (fresh chevrons appear as hidden parents are revealed).
async function expandAll(w: ReturnType<typeof mount>) {
  for (;;) {
    const next = w.findAll('[data-expand]').find(b => b.attributes('aria-expanded') === 'false')
    if (!next) return
    await next.trigger('click')
  }
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
    await expandAll(w)
    const names = w.findAll('[data-loc-name]')
    expect(names.map(n => n.text())).toEqual(['House', 'Cabinet', 'Bin', 'Tray'])
    const pad = (i: number) => parseFloat((names[i].element as HTMLElement).style.paddingLeft)
    expect(pad(0)).toBe(0)
    expect(pad(1)).toBeGreaterThan(pad(0))
    expect(pad(2)).toBeGreaterThan(pad(1))
    expect(pad(3)).toBeGreaterThan(pad(2))
  })

  it('offers one "Add item" link per row pointing at /items/new?location=<id>', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await expandAll(w)
    const links = w.findAll('[data-add-item]')
    expect(links).toHaveLength(4) // one per location row
    expect(links.map(l => l.attributes('href'))).toEqual([
      '/items/new?location=1', '/items/new?location=2', '/items/new?location=3', '/items/new?location=4',
    ])
  })

  it('hides "Duplicate layout…" on childless locations (House, Cabinet and Bin keep it, Tray loses it)', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await expandAll(w)
    const dupes = w.findAll('[data-duplicate]')
    expect(dupes).toHaveLength(3) // House (Cabinet), Cabinet (Bin), Bin (Tray); Tray is childless
    // the duplicate controls belong to the three parents, not to Tray's row
    const rowOf = (name: string) =>
      w.findAll('li').find(li => li.find('[data-loc-name]').text() === name)!
    expect(rowOf('House').find('[data-duplicate]').exists()).toBe(true)
    expect(rowOf('Cabinet').find('[data-duplicate]').exists()).toBe(true)
    expect(rowOf('Bin').find('[data-duplicate]').exists()).toBe(true)
    expect(rowOf('Tray').find('[data-duplicate]').exists()).toBe(false)
  })

  it('clicking a location name seeds the items location filter and navigates to /items', async () => {
    const calls = mockFetch()
    const store = useItemsStore()
    store.filters.page = 4 // the click must reset pagination
    const { w, router } = await mountViewWithRouter(calls)
    await w.find('[data-expand]').trigger('click') // reveal Cabinet
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
    await expandAll(w)
    await w.findAll('[data-edit]')[2].trigger('click') // Bin enters edit mode
    const input = w.find('[data-edit-name]')
    expect(input.exists()).toBe(true)
    expect(w.findAll('[data-loc-name]')).toHaveLength(3) // Bin's name button is replaced
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
    await expandAll(w)
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
    await expandAll(w)
    await w.findAll('[data-edit]')[2].trigger('click') // Bin
    await w.find('[data-edit-notes]').setValue('Small parts')
    await w.find('[data-save]').trigger('click')
    await flushPromises()
    const patch = calls.find(c => c.method === 'PATCH')
    expect(patch?.url).toBe('/api/v1/locations/3')
    expect(JSON.parse(patch!.body!)).toEqual({ notes: 'Small parts' })
  })

  it('leaf delete keeps the single-confirm flow: bare DELETE, toast, refetch', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await expandAll(w)
    await w.findAll('[data-delete]')[3].trigger('click') // Tray: no children
    const dialog = w.find('[role="dialog"]')
    expect(dialog.exists()).toBe(true)
    expect(dialog.text()).toContain('Children move up')
    const buttons = dialog.findAll('button').map(b => b.text())
    expect(buttons).not.toContain('Delete everything below') // no subtree choice for a leaf
    await dialog.findAll('button').at(-1)!.trigger('click') // confirm Delete
    await flushPromises()
    expect(calls.some(c => c.method === 'DELETE' && c.url === '/api/v1/locations/4')).toBe(true)
    expect(toasts.some(t => t.message === 'Location deleted' && t.type === 'success')).toBe(true)
    expect(calls.filter(c => c.method === 'GET').length).toBeGreaterThanOrEqual(2)
  })

  it('a location with children gets a two-choice dialog naming it and its sublocation count', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await w.findAll('[data-delete]')[0].trigger('click') // House: Cabinet + Bin + Tray below it
    const dialog = w.find('[role="dialog"]')
    expect(dialog.exists()).toBe(true)
    const buttons = dialog.findAll('button').map(b => b.text())
    expect(buttons).toContain('Delete only this')
    expect(buttons).toContain('Delete everything below')
    // plain confirm first (left), destructive alt rightmost
    expect(buttons.slice(-2)).toEqual(['Delete only this', 'Delete everything below'])
    expect(dialog.text()).toContain("'House'")
    // 3 = the TOTAL descendant count (Cabinet, Bin, Tray); House has only ONE
    // direct child, so this pin fails if the walk ever stops at depth 1
    expect(dialog.text()).toContain('3 sublocations')
  })

  it("'Delete only this' fires the bare DELETE (children reparent server-side)", async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await w.findAll('[data-delete]')[0].trigger('click') // House
    const dialog = w.find('[role="dialog"]')
    await dialog.findAll('button').find(b => b.text() === 'Delete only this')!.trigger('click')
    await flushPromises()
    expect(calls.some(c => c.method === 'DELETE' && c.url === '/api/v1/locations/1')).toBe(true)
    expect(calls.some(c => c.method === 'DELETE' && c.url.includes('subtree'))).toBe(false)
    expect(toasts.some(t => t.message === 'Location deleted' && t.type === 'success')).toBe(true)
    expect(calls.filter(c => c.method === 'GET').length).toBeGreaterThanOrEqual(2)
    expect(w.find('[role="dialog"]').exists()).toBe(false)
  })

  it("'Delete everything below' fires DELETE ?subtree=true, toasts and refreshes the list", async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await w.findAll('[data-delete]')[0].trigger('click') // House
    const alt = w.find('[role="dialog"] [data-alt-confirm]')
    expect(alt.exists()).toBe(true)
    await alt.trigger('click')
    await flushPromises()
    expect(calls.some(c => c.method === 'DELETE' && c.url === '/api/v1/locations/1?subtree=true')).toBe(true)
    expect(toasts.some(t => t.type === 'success' && t.message.includes('House'))).toBe(true)
    expect(calls.filter(c => c.method === 'GET').length).toBeGreaterThanOrEqual(2)
    expect(w.find('[role="dialog"]').exists()).toBe(false)
  })

  // ---- Collapsible tree (collapsed by default) ----

  it('renders only root locations by default (the tree starts collapsed)', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    expect(w.findAll('[data-loc-name]').map(n => n.text())).toEqual(['House'])
  })

  it('expanding a root reveals its children but NOT grandchildren until their chevron is clicked', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await w.find('[data-expand]').trigger('click') // House's chevron
    expect(w.findAll('[data-loc-name]').map(n => n.text())).toEqual(['House', 'Cabinet'])
    await w.findAll('[data-expand]')[1].trigger('click') // Cabinet's chevron (now visible)
    expect(w.findAll('[data-loc-name]').map(n => n.text())).toEqual(['House', 'Cabinet', 'Bin'])
  })

  it('leaf rows get a same-width spacer instead of a chevron button', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await expandAll(w)
    const rowOf = (name: string) =>
      w.findAll('li').find(li => li.find('[data-loc-name]').text() === name)!
    expect(rowOf('Tray').find('[data-expand]').exists()).toBe(false)
    const spacer = rowOf('Tray').find('[data-expand-spacer]')
    expect(spacer.exists()).toBe(true)
    expect(spacer.attributes('aria-hidden')).toBe('true')
    // the spacer and the chevron share the same fixed 2rem column so names
    // stay depth-aligned; the button keeps its ≥32px tap height
    expect(spacer.classes()).toContain('w-8')
    expect(rowOf('House').find('[data-expand]').classes()).toContain('w-8')
    expect(rowOf('House').find('[data-expand]').classes()).toContain('min-h-8')
  })

  it("flips the chevron's aria-expanded and label with the toggle", async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    const chev = w.find('[data-expand]')
    expect(chev.attributes('aria-expanded')).toBe('false')
    expect(chev.attributes('aria-label')).toBe('Expand House')
    expect(chev.text()).toBe('▶')
    await chev.trigger('click')
    expect(chev.attributes('aria-expanded')).toBe('true')
    expect(chev.attributes('aria-label')).toBe('Collapse House')
    expect(chev.text()).toBe('▼')
    await chev.trigger('click') // and back
    expect(chev.attributes('aria-expanded')).toBe('false')
    expect(w.findAll('[data-loc-name]').map(n => n.text())).toEqual(['House'])
  })

  it('clicking a chevron never triggers the name navigation', async () => {
    const calls = mockFetch()
    const store = useItemsStore()
    const { w, router } = await mountViewWithRouter(calls)
    await w.find('[data-expand]').trigger('click')
    await flushPromises()
    expect(w.findAll('[data-loc-name]').map(n => n.text())).toEqual(['House', 'Cabinet']) // it toggled
    expect(store.filters.location_id).toBe(0)
    expect(router.currentRoute.value.path).toBe('/') // memory history starts at /
  })

  it('adding a child via the form auto-expands its parent so the new row appears', async () => {
    const locs = [...LOCATIONS]
    const calls = mockFetch({
      locations: locs,
      onPost: (url, body) => {
        if (url !== '/api/v1/locations') return
        const b = JSON.parse(body!) as { name: string; parent_id?: number }
        locs.push({ id: 5, name: b.name, parent_id: b.parent_id ?? null, notes: '', created_at: '', updated_at: '' } as Location)
      },
    })
    const w = await mountView(calls)
    expect(w.findAll('[data-loc-name]').map(n => n.text())).toEqual(['House']) // House collapsed
    await w.find('[data-add-name]').setValue('Shelf')
    await w.find('form select[aria-label="Location"]').setValue('1') // parent House
    await w.find('form').trigger('submit')
    await flushPromises()
    // House was expanded by the add flow — Shelf is visible, Bin (and its
    // child Tray, under the still-collapsed Cabinet) stay hidden
    expect(w.findAll('[data-loc-name]').map(n => n.text())).toEqual(['House', 'Cabinet', 'Shelf'])
  })

  it('adding a child under a deep parent expands the WHOLE ancestor chain so it appears', async () => {
    const locs = [...LOCATIONS]
    const calls = mockFetch({
      locations: locs,
      onPost: (url, body) => {
        if (url !== '/api/v1/locations') return
        const b = JSON.parse(body!) as { name: string; parent_id?: number }
        locs.push({ id: 5, name: b.name, parent_id: b.parent_id ?? null, notes: '', created_at: '', updated_at: '' } as Location)
      },
    })
    const w = await mountView(calls)
    // fully collapsed: only House renders, Bin (depth 2) is selectable but hidden
    expect(w.findAll('[data-loc-name]').map(n => n.text())).toEqual(['House'])
    await w.find('[data-add-name]').setValue('Drawer')
    await w.find('form select[aria-label="Location"]').setValue('3') // Bin, two levels deep
    await w.find('form').trigger('submit')
    await flushPromises()
    // expanding only Bin (the direct parent) would leave it invisible behind
    // collapsed House and Cabinet — the whole chain must reveal (siblings
    // render in name order: Drawer before Tray)
    expect(w.findAll('[data-loc-name]').map(n => n.text())).toEqual(['House', 'Cabinet', 'Bin', 'Drawer', 'Tray'])
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

  it('labels typed in verbose mode, then a dimension removed, post labels matching the remaining dims', async () => {
    const calls = mockFetch({ postBody: { children_created: 3 } })
    const w = await mountView(calls)
    await w.find('[data-add-name]').setValue('Toolbox')
    await w.find('[data-sub-mode]').setValue('generate')
    await w.find('input[type="radio"][value="verbose"]').setValue(true)
    const labels = w.findAll('[data-gen-label]')
    await labels[0].setValue('Drawer')
    await labels[1].setValue('Tray')
    await w.find('[data-remove-dim]').trigger('click') // drops dim 2 in lockstep with its label
    expect(w.findAll('[data-gen-label]')).toHaveLength(1)
    await w.find('form').trigger('submit')
    await flushPromises()
    const post = calls.find(c => c.method === 'POST')
    expect(post?.url).toBe('/api/v1/locations/bulk')
    // the surviving label is kept (trimmed), the dropped dimension's label
    // is gone — exactly len(dims) entries, so no 400
    expect(JSON.parse(post!.body!)).toEqual({
      name: 'Toolbox', dims: [3], structure: 'flat', naming: 'verbose', prefix: '', labels: ['Drawer'],
    })
  })

  it('copy mode picker lists depth-indented tree options', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await w.find('[data-sub-mode]').setValue('copy')
    const opts = w.find('[data-copy-source]').findAll('option')
    expect(opts.map(o => o.text())).toEqual([
      '— choose a layout —', 'House', '— Cabinet', '— — Bin', '— — — Tray',
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
    await expandAll(w)
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

  it('duplicate layout expands the row\'s parent so the copy is visible', async () => {
    const locs = [...LOCATIONS]
    const calls = mockFetch({
      locations: locs,
      postBody: { children_created: 1 },
      onPost: (url, body) => {
        if (!url.endsWith('/copy-layout')) return
        const b = JSON.parse(body!) as { name: string; parent_id: number }
        locs.push({ id: 5, name: b.name, parent_id: b.parent_id || null, notes: '', created_at: '', updated_at: '' } as Location)
      },
    })
    const w = await mountView(calls)
    await w.find('[data-expand]').trigger('click') // reveal Cabinet
    await w.findAll('[data-duplicate]')[1].trigger('click') // Cabinet (copy lands under House)
    // collapse House while the dialog is open — without the re-expand the
    // copy would stay hidden after the POST
    await w.find('[data-expand]').trigger('click')
    expect(w.findAll('[data-loc-name]').map(n => n.text())).toEqual(['House'])
    await w.find('[role="dialog"]').find('[data-dup-confirm]').trigger('click')
    await flushPromises()
    expect(w.findAll('[data-loc-name]').map(n => n.text())).toEqual(['House', 'Cabinet', 'Cabinet copy'])
  })

  it('duplicate layout under a deep parent expands the WHOLE ancestor chain so the copy appears', async () => {
    const deep = [
      { id: 1, name: 'Warehouse', parent_id: null, notes: '', created_at: '', updated_at: '' },
      { id: 2, name: 'House', parent_id: 1, notes: '', created_at: '', updated_at: '' },
      { id: 3, name: 'Cabinet', parent_id: 2, notes: '', created_at: '', updated_at: '' },
      { id: 4, name: 'Bin', parent_id: 3, notes: '', created_at: '', updated_at: '' },
    ] as Location[]
    const locs = [...deep]
    const calls = mockFetch({
      locations: locs,
      postBody: { children_created: 1 },
      onPost: (url, body) => {
        if (!url.endsWith('/copy-layout')) return
        const b = JSON.parse(body!) as { name: string; parent_id: number }
        locs.push({ id: 5, name: b.name, parent_id: b.parent_id || null, notes: '', created_at: '', updated_at: '' } as Location)
      },
    })
    const w = await mountView(calls)
    await expandAll(w) // reveal Cabinet (its Duplicate… button needs the chain open)
    await w.findAll('[data-duplicate]')[2].trigger('click') // Cabinet — copy lands under House (depth 1)
    // collapse EVERY node while the dialog is open (bottom-up, so each
    // click's hidden chevrons are already cleared): only re-expanding the
    // full ancestor chain (Warehouse AND House) can make the copy visible
    for (;;) {
      const open = w.findAll('[data-expand]').filter(b => b.attributes('aria-expanded') === 'true')
      if (!open.length) break
      await open[open.length - 1].trigger('click')
    }
    expect(w.findAll('[data-loc-name]').map(n => n.text())).toEqual(['Warehouse'])
    await w.find('[role="dialog"]').find('[data-dup-confirm]').trigger('click')
    await flushPromises()
    expect(w.findAll('[data-loc-name]').map(n => n.text())).toEqual(['Warehouse', 'House', 'Cabinet', 'Cabinet copy'])
  })

  it('duplicate layout dialog closes on Escape without posting', async () => {
    const calls = mockFetch()
    const w = await mountView(calls)
    await expandAll(w)
    await w.findAll('[data-duplicate]')[1].trigger('click')
    expect(w.find('[role="dialog"]').exists()).toBe(true)
    await w.find('[role="dialog"]').trigger('keydown', { key: 'Escape' })
    expect(w.find('[role="dialog"]').exists()).toBe(false)
    expect(calls.some(c => c.method === 'POST')).toBe(false)
  })
})

// ---- Location pictures ----

const PHOTO_LOCS = [
  { id: 1, name: 'House', parent_id: null, notes: '', photo_key: '2026/01/full.jpg', photo_thumb_key: '2026/01/thumb.jpg', created_at: '', updated_at: '' },
  { id: 2, name: 'Cabinet', parent_id: 1, notes: '', photo_key: '2026/02/full2.png', photo_thumb_key: null, created_at: '', updated_at: '' },
  { id: 3, name: 'Bin', parent_id: 2, notes: '', photo_key: null, photo_thumb_key: null, created_at: '', updated_at: '' },
] as Location[]

interface PhotoCall { method: string; url: string; body?: unknown }

// Fetch mock for the picture tests: real locations list, configurable
// photo endpoints. `onPost`/`onDelete` hooks run before the response is
// returned; `deferPost` (when given) receives the resolve function instead
// of resolving immediately, so a test can observe the in-flight state.
function mockPhotoFetch(opts: {
  locs?: Location[]; postStatus?: number; postBody?: unknown
  deferPost?: boolean
} = {}) {
  const locs = opts.locs ?? PHOTO_LOCS
  const calls: PhotoCall[] = []
  let resolvePost: ((r: Response) => void) | null = null
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    calls.push({ method, url, body: init?.body })
    if (method === 'GET')
      return new Response(JSON.stringify({ data: locs, total: locs.length }), { status: 200 })
    if (method === 'POST') {
      const resp = new Response(JSON.stringify(opts.postBody ?? { photo_key: '2026/03/new.jpg', thumb_key: null }), { status: opts.postStatus ?? 200 })
      if (opts.deferPost) return new Promise<Response>(resolve => { resolvePost = resolve })
      return resp
    }
    if (method === 'DELETE') return new Response('', { status: 204 })
    return new Response(JSON.stringify({}), { status: 200 }) // PATCH
  }))
  return {
    calls,
    resolvePost: (r: Response) => resolvePost?.(r),
    grow: (loc: Location) => locs.push(loc),
  }
}

async function mountPhotoView(calls: PhotoCall[]) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div />' } }],
  })
  const w = mount(LocationsView, { global: { plugins: [router] } })
  await flushPromises()
  expect(calls.length).toBeGreaterThan(0)
  return w
}

const rowOf = (w: ReturnType<typeof mount>, name: string) =>
  w.findAll('li').find(li => li.find('[data-loc-name]').text() === name)!

describe('LocationsView pictures', () => {
  it('renders a row thumbnail using thumb ?? photo, absent rows get none', async () => {
    const m = mockPhotoFetch()
    const w = await mountPhotoView(m.calls)
    await expandAll(w)
    const house = rowOf(w, 'House')
    const cabinet = rowOf(w, 'Cabinet')
    const bin = rowOf(w, 'Bin')
    // House: thumb preferred for the img, FULL photo for the link target
    const houseThumb = house.find('[data-loc-thumb]')
    expect(houseThumb.exists()).toBe(true)
    expect(houseThumb.find('img').attributes('src')).toBe('/files/2026/01/thumb.jpg')
    expect(houseThumb.attributes('href')).toBe('/files/2026/01/full.jpg')
    // Cabinet: no derived thumb — the full image is both preview and target
    const cabinetThumb = cabinet.find('[data-loc-thumb]')
    expect(cabinetThumb.exists()).toBe(true)
    expect(cabinetThumb.find('img').attributes('src')).toBe('/files/2026/02/full2.png')
    expect(cabinetThumb.attributes('href')).toBe('/files/2026/02/full2.png')
    // Bin: no picture, no thumbnail
    expect(bin.find('[data-loc-thumb]').exists()).toBe(false)
    // alt text is the location name
    expect(houseThumb.find('img').attributes('alt')).toBe('House')
  })

  it('thumbnail anchor opens the FULL image in a new tab (target/rel pinned)', async () => {
    const m = mockPhotoFetch()
    const w = await mountPhotoView(m.calls)
    const anchor = rowOf(w, 'House').find('[data-loc-thumb]')
    expect(anchor.attributes('target')).toBe('_blank')
    expect(anchor.attributes('rel')).toBe('noopener')
  })

  it('the add form has no picture control (pictures come after creation)', async () => {
    const m = mockPhotoFetch()
    const w = await mountPhotoView(m.calls)
    expect(w.find('form [data-photo-input]').exists()).toBe(false)
    expect(w.find('form [data-photo-upload]').exists()).toBe(false)
  })

  it('edit mode: choosing a file uploads it immediately, then the list refreshes', async () => {
    const m = mockPhotoFetch()
    const w = await mountPhotoView(m.calls)
    await expandAll(w)
    await rowOf(w, 'House').find('[data-edit]').trigger('click')
    const controls = w.find('[data-photo-controls]')
    expect(controls.exists()).toBe(true)
    const input = controls.find('[data-photo-input]')
    expect(input.attributes('accept')).toBe('image/*')
    const file = new File(['x'], 'bin.jpg', { type: 'image/jpeg' })
    Object.defineProperty(input.element, 'files', { value: [file] })
    await input.trigger('change')
    await flushPromises()
    const post = m.calls.find(c => c.method === 'POST')
    expect(post?.url).toBe('/api/v1/locations/1/photo')
    expect(post?.body).toBeInstanceOf(FormData)
    expect((post!.body as FormData).get('file')).toBeTruthy()
    // list refetched after the upload
    expect(m.calls.filter(c => c.method === 'GET').length).toBeGreaterThanOrEqual(2)
    // still in edit mode, no error line
    expect(w.find('[data-photo-controls]').exists()).toBe(true)
    expect(w.find('[data-edit-name]').exists()).toBe(true)
  })

  it('upload disables the controls while in flight and re-enables after', async () => {
    const m = mockPhotoFetch({ deferPost: true })
    const w = await mountPhotoView(m.calls)
    await expandAll(w)
    await rowOf(w, 'House').find('[data-edit]').trigger('click')
    const input = w.find('[data-photo-input]')
    Object.defineProperty(input.element, 'files', { value: [new File(['x'], 'bin.jpg', { type: 'image/jpeg' })] })
    await input.trigger('change')
    // in flight: both controls disabled
    expect(w.find('[data-photo-upload]').attributes('disabled')).toBeDefined()
    expect(w.find('[data-photo-remove]').attributes('disabled')).toBeDefined()
    m.resolvePost(new Response(JSON.stringify({ photo_key: 'k', thumb_key: null }), { status: 200 }))
    await flushPromises()
    expect(w.find('[data-photo-upload]').attributes('disabled')).toBeUndefined()
    expect(w.find('[data-photo-remove]').attributes('disabled')).toBeUndefined()
  })

  it('upload errors surface in the editError line (no toast assertion needed)', async () => {
    const m = mockPhotoFetch({
      postStatus: 415,
      postBody: { error: { code: 'unsupported_type', message: 'photos must be jpeg, png, webp, or gif' } },
    })
    const w = await mountPhotoView(m.calls)
    await expandAll(w)
    await rowOf(w, 'House').find('[data-edit]').trigger('click')
    const input = w.find('[data-photo-input]')
    Object.defineProperty(input.element, 'files', { value: [new File(['x'], 'bin.txt', { type: 'text/plain' })] })
    await input.trigger('change')
    await flushPromises()
    const err = w.find('[data-edit-error]')
    expect(err.exists()).toBe(true)
    expect(err.text()).toContain('photos must be jpeg, png, webp, or gif')
    expect(w.find('[data-photo-upload]').attributes('disabled')).toBeUndefined()
  })

  it('remove posts DELETE, refetches, confirm-free; hidden when no picture', async () => {
    const m = mockPhotoFetch()
    const w = await mountPhotoView(m.calls)
    await expandAll(w)
    // Bin has no picture: no Remove button in its edit row
    await rowOf(w, 'Bin').find('[data-edit]').trigger('click')
    expect(w.find('[data-photo-remove]').exists()).toBe(false)
    const cancel = w.findAll('button').find(b => b.text() === 'Cancel')!
    await cancel.trigger('click')
    // House has a picture: Remove is confirm-free (no dialog) and calls DELETE
    await rowOf(w, 'House').find('[data-edit]').trigger('click')
    expect(w.find('[data-photo-remove]').exists()).toBe(true)
    expect(w.find('[role="dialog"]').exists()).toBe(false)
    await w.find('[data-photo-remove]').trigger('click')
    await flushPromises()
    expect(m.calls.some(c => c.method === 'DELETE' && c.url === '/api/v1/locations/1/photo')).toBe(true)
    expect(m.calls.filter(c => c.method === 'GET').length).toBeGreaterThanOrEqual(2)
  })

  it('edit preview shows the current picture thumbnail', async () => {
    const m = mockPhotoFetch()
    const w = await mountPhotoView(m.calls)
    await expandAll(w)
    await rowOf(w, 'House').find('[data-edit]').trigger('click')
    const preview = w.find('[data-photo-controls] img')
    expect(preview.exists()).toBe(true)
    expect(preview.attributes('src')).toBe('/files/2026/01/thumb.jpg')
  })
})
