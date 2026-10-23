import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import ProjectDetailView from './ProjectDetailView.vue'
import { toasts } from '../utils/toast'

// Owner projection of GET /projects/1 — full ProjectDetail shape.
const ownerDetail = {
  project: { id: 1, name: 'NAS', status: 'active', notes: 'big rebuild', visibility: 'private', created_at: '', updated_at: '' },
  owner_view: true,
  items: [
    { item_id: 5, item_name: '8TB HDD', qty_used: 4, item_qty: 6, item_unit: '', notes: '', added_at: '', shared: false },
    { item_id: 7, item_name: 'RAM stick', qty_used: null, item_qty: 2, item_unit: 'pcs', notes: '', added_at: '', shared: true },
  ],
  notes_log: [],
  comments: [],
  files: [],
  bookmarks: [],
}

// Sharee projection: masked BOM rows (no item_id/item_qty), no notes_log key.
const shareeDetail = {
  project: { id: 1, name: 'NAS', status: 'active', notes: 'big rebuild', visibility: 'shared', created_at: '', updated_at: '' },
  owner_view: false,
  items: [
    { item_name: 'Priv part', qty_used: 2, shared: false },
    { item_name: '8TB HDD', qty_used: null, shared: true },
  ],
  comments: [],
  files: [],
  bookmarks: [],
}

// Items available to the add-picker (GET /items?page_size=200).
const ITEMS = { data: [
  { id: 5, name: '8TB HDD' }, { id: 7, name: 'RAM stick' }, { id: 9, name: 'NVMe cache' },
], total: 3 }

interface Call { method: string; url: string; body?: string }

// Mocks the project detail, the add-picker item list, and every project-item
// mutation verb. detailStatus lets a test simulate 404 not_found.
function mockFetch(opts: { detailStatus?: number; detailBody?: unknown } = {}) {
  const calls: Call[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    calls.push({ method, url, body: init?.body as string | undefined })
    if (url.includes('/projects/1') && method === 'GET')
      return new Response(JSON.stringify(opts.detailBody ?? ownerDetail), { status: opts.detailStatus ?? 200 })
    if (url.includes('/items?'))
      return new Response(JSON.stringify(ITEMS), { status: 200 })
    if (method === 'PATCH' && url.includes('/projects/1/items/'))
      return new Response('', { status: 204 })
    if (method === 'PATCH')
      // 200 returns the updated project — echo the PATCH body back.
      return new Response(JSON.stringify({ ...ownerDetail.project, ...JSON.parse(init?.body as string ?? '{}') }), { status: 200 })
    if (method === 'POST')
      return new Response('', { status: 201 })
    return new Response('', { status: 204 }) // DELETE
  }))
  return calls
}

function newRouter(): Router {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/projects', component: { template: '<div>projects</div>' } },
      { path: '/items/:id', component: { template: '<div>item</div>' } },
      { path: '/projects/:id', component: ProjectDetailView },
    ],
  })
}

async function mountView() {
  const router = newRouter()
  await router.push('/projects/1')
  const w = mount(ProjectDetailView, { global: { plugins: [router] } })
  await flushPromises()
  return { w, router }
}

beforeEach(() => { vi.unstubAllGlobals(); toasts.splice(0) })

describe('ProjectDetailView', () => {
  it('renders the project name, status and linked item rows with qty_used inputs', async () => {
    const calls = mockFetch()
    const { w } = await mountView()
    expect(calls.length).toBeGreaterThan(0)
    expect(w.text()).toContain('NAS')
    expect(w.text()).toContain('big rebuild')
    const names = w.findAll('[data-item-name]')
    expect(names.map(n => n.text())).toEqual(['8TB HDD', 'RAM stick'])
    const inputs = w.findAll('[data-qty-used]')
    expect((inputs[0].element as HTMLInputElement).value).toBe('4')
    expect((inputs[1].element as HTMLInputElement).value).toBe('')
    // item name links to the item page
    expect(w.find('a[href="/items/5"]').exists()).toBe(true)
  })

  it('hides owner controls and links in the sharee view', async () => {
    mockFetch({ detailBody: shareeDetail })
    const { w } = await mountView()
    expect(w.find('[data-status]').exists()).toBe(false)
    expect(w.find('[data-edit]').exists()).toBe(false)
    expect(w.find('[data-delete]').exists()).toBe(false)
    expect(w.find('[data-add-item]').exists()).toBe(false)
    expect(w.find('[data-qty-used]').exists()).toBe(false)
    expect(w.find('[data-item-name]').exists()).toBe(false) // plain text, no router-link
    expect(w.find('[data-visibility-badge]').exists()).toBe(false) // badge-is-toggle is owner-only
    expect(w.text()).toContain('Priv part') // masked row: name still shown
    expect(w.text()).not.toContain('No items linked yet')
    // masked rows: no stock value, '—' placeholders for hidden columns
    expect(w.findAll('tbody tr').length).toBe(2)
    expect(w.findAll('tbody tr')[0].text()).toContain('—')
  })

  it('toggles visibility from the badge (owner)', async () => {
    const calls = mockFetch()
    const { w } = await mountView()
    const badge = w.find('[data-visibility-badge]')
    expect(badge.exists()).toBe(true)
    expect(badge.text()).toBe('private')
    await badge.trigger('click')
    await flushPromises()
    const patch = calls.find(c => c.method === 'PATCH' && c.url === '/api/v1/projects/1')
    expect(patch).toBeTruthy()
    expect(JSON.parse(patch!.body!)).toEqual({ visibility: 'shared' })
    // badge text follows the server's answer
    expect(w.find('[data-visibility-badge]').text()).toBe('shared')
    expect(toasts.some(t => t.type === 'success')).toBe(true)
  })

  it('editing qty_used PATCHes /projects/1/items/5 with {qty_used: 6} and refetches', async () => {
    const calls = mockFetch()
    const { w } = await mountView()
    await w.findAll('[data-qty-used]')[0].setValue('6')
    await w.findAll('[data-qty-save]')[0].trigger('click')
    await flushPromises()
    const patch = calls.find(c => c.method === 'PATCH' && c.url === '/api/v1/projects/1/items/5')
    expect(patch).toBeTruthy()
    expect(JSON.parse(patch!.body!)).toEqual({ qty_used: 6 })
    expect(calls.filter(c => c.method === 'GET' && c.url.includes('/projects/1')).length).toBeGreaterThanOrEqual(2)
  })

  it('an emptied qty_used input sends {qty_used: 0}', async () => {
    const calls = mockFetch()
    const { w } = await mountView()
    await w.findAll('[data-qty-used]')[0].setValue('')
    await w.findAll('[data-qty-save]')[0].trigger('click')
    await flushPromises()
    const patch = calls.find(c => c.method === 'PATCH' && c.url.includes('/items/5'))
    expect(JSON.parse(patch!.body!)).toEqual({ qty_used: 0 })
  })

  it('changing status PATCHes /projects/1 with {status: "done"}', async () => {
    const calls = mockFetch()
    const { w } = await mountView()
    await w.find('[data-status]').setValue('done')
    await flushPromises()
    const patch = calls.find(c => c.method === 'PATCH' && c.url === '/api/v1/projects/1' && !c.url.includes('/items/'))
    expect(patch).toBeTruthy()
    expect(JSON.parse(patch!.body!)).toEqual({ status: 'done' })
  })

  it('re-selecting the current status does not PATCH', async () => {
    const calls = mockFetch()
    const { w } = await mountView()
    await w.find('[data-status]').setValue('active')
    await flushPromises()
    expect(calls.some(c => c.method === 'PATCH' && c.url === '/api/v1/projects/1')).toBe(false)
  })

  it('add picker POSTs {item_id, qty_used}, refetches and clears the form', async () => {
    const calls = mockFetch()
    const { w } = await mountView()
    await w.find('[data-add-item]').setValue('9')
    await w.find('[data-add-qty]').setValue('2')
    await w.find('form').trigger('submit')
    await flushPromises()
    const post = calls.find(c => c.method === 'POST')
    expect(post?.url).toBe('/api/v1/projects/1/items')
    expect(JSON.parse(post!.body!)).toEqual({ item_id: 9, qty_used: 2 })
    expect(calls.filter(c => c.method === 'GET' && c.url.includes('/projects/1')).length).toBeGreaterThanOrEqual(2)
    expect((w.find('[data-add-item]').element as HTMLSelectElement).value).toBe('')
    expect((w.find('[data-add-qty]').element as HTMLInputElement).value).toBe('')
  })

  it('add picker omits qty_used when the input is empty', async () => {
    const calls = mockFetch()
    const { w } = await mountView()
    await w.find('[data-add-item]').setValue('9')
    await w.find('form').trigger('submit')
    await flushPromises()
    const post = calls.find(c => c.method === 'POST')
    expect(JSON.parse(post!.body!)).toEqual({ item_id: 9 })
  })

  it('remove calls DELETE on the project item and refetches', async () => {
    const calls = mockFetch()
    const { w } = await mountView()
    await w.findAll('[data-remove]')[0].trigger('click')
    await flushPromises()
    expect(calls.some(c => c.method === 'DELETE' && c.url === '/api/v1/projects/1/items/5')).toBe(true)
    expect(calls.filter(c => c.method === 'GET' && c.url.includes('/projects/1')).length).toBeGreaterThanOrEqual(2)
  })

  it('notes edit sends a notes-only PATCH (dirty flag)', async () => {
    const calls = mockFetch()
    const { w } = await mountView()
    await w.find('[data-edit]').trigger('click')
    await w.find('[data-edit-notes]').setValue('small rebuild')
    await w.find('[data-save]').trigger('click')
    await flushPromises()
    const patch = calls.find(c => c.method === 'PATCH' && c.url === '/api/v1/projects/1' && !c.url.includes('/items/'))
    expect(patch).toBeTruthy()
    expect(JSON.parse(patch!.body!)).toEqual({ notes: 'small rebuild' })
  })

  it('navigates back to /projects with a toast on 404 not_found', async () => {
    mockFetch({ detailStatus: 404, detailBody: { error: { code: 'not_found', message: 'project not found' } } })
    const { router } = await mountView()
    expect(router.currentRoute.value.path).toBe('/projects')
    expect(toasts.some(t => t.message === 'project not found' && t.type === 'error')).toBe(true)
  })

  it('delete via ConfirmDialog: DELETEs /projects/1, toasts, navigates to /projects', async () => {
    const calls = mockFetch()
    const { w, router } = await mountView()
    await w.find('[data-delete]').trigger('click')
    // nothing deleted yet — the dialog is open with the linked-items warning
    expect(calls.some(c => c.method === 'DELETE')).toBe(false)
    const overlay = w.find('.fixed.inset-0')
    expect(overlay.text())
      .toContain('Permanently delete this project? Linked items are NOT deleted — only their links to this project are removed.')
    await overlay.findAll('button').find(b => b.text() === 'Delete')!.trigger('click')
    await flushPromises()
    expect(calls.some(c => c.method === 'DELETE' && c.url === '/api/v1/projects/1')).toBe(true)
    expect(toasts.some(t => t.message === 'Project deleted' && t.type === 'success')).toBe(true)
    expect(router.currentRoute.value.path).toBe('/projects')
  })

  it('canceling the delete dialog sends no DELETE', async () => {
    const calls = mockFetch()
    const { w } = await mountView()
    await w.find('[data-delete]').trigger('click')
    await w.find('.fixed.inset-0').findAll('button').find(b => b.text() === 'Cancel')!.trigger('click')
    await flushPromises()
    expect(calls.some(c => c.method === 'DELETE')).toBe(false)
    expect(w.find('.fixed.inset-0').exists()).toBe(false)
  })
})
