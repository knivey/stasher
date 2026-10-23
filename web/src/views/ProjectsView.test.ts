import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import ProjectsView from './ProjectsView.vue'
import { toasts } from '../utils/toast'

const PROJECTS = { data: [
  { id: 1, name: 'NAS', status: 'active', notes: 'big rebuild', visibility: 'private', created_at: '', updated_at: '' },
  { id: 2, name: 'Desk', status: 'planned', notes: '', visibility: 'shared', created_at: '', updated_at: '' },
], total: 2 }

interface Call { method: string; url: string; body?: string }

function mockFetch(opts: { postStatus?: number; postBody?: unknown } = {}) {
  const calls: Call[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    calls.push({ method, url, body: init?.body as string | undefined })
    if (method === 'GET')
      return new Response(JSON.stringify(PROJECTS), { status: 200 })
    return new Response(JSON.stringify(opts.postBody ?? { id: 42, name: 'Shed', status: 'planned', notes: '' }),
      { status: opts.postStatus ?? 201 })
  }))
  return calls
}

async function mountView(calls?: Call[]) {
  const router: Router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/projects', component: ProjectsView },
      { path: '/projects/:id', component: { template: '<div>detail</div>' } },
    ],
  })
  await router.push('/projects')
  const w = mount(ProjectsView, { global: { plugins: [router] } })
  await flushPromises()
  if (calls) expect(calls.length).toBeGreaterThan(0) // initial list fetch happened
  return { w, router }
}

beforeEach(() => { vi.unstubAllGlobals(); toasts.splice(0) })

describe('ProjectsView', () => {
  it('renders a card per project with name, status badge text and notes', async () => {
    const calls = mockFetch()
    const { w } = await mountView(calls)
    const cards = w.findAll('[data-project-card]')
    expect(cards.length).toBe(2)
    expect(cards[0].text()).toContain('NAS')
    expect(cards[0].text()).toContain('big rebuild')
    expect(cards[0].find('[data-status-badge]').text()).toBe('active')
    expect(cards[1].find('[data-status-badge]').text()).toBe('planned')
    expect(cards[0].find('[data-visibility-badge]').text()).toBe('private')
    // cards link to the project detail page (the card itself is the anchor)
    expect(cards[0].attributes('href')).toBe('/projects/1')
  })

  it('shows a shared badge for shared projects', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      data: [{ id: 1, name: 'Bench PSU', status: 'active', notes: '', visibility: 'shared', created_at: '', updated_at: '' }],
      total: 1,
    }), { status: 200 })))
    const { w } = await mountView()
    const badge = w.find('[data-visibility-badge]')
    expect(badge.text()).toBe('shared')
  })

  it('shows an empty state when there are no projects', async () => {
    vi.stubGlobal('fetch', vi.fn(async () =>
      new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })))
    const router: Router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/projects', component: ProjectsView }],
    })
    await router.push('/projects')
    const w = mount(ProjectsView, { global: { plugins: [router] } })
    await flushPromises()
    expect(w.text()).toContain('No projects yet')
  })

  it('creating a project POSTs {name, notes} then opens the new detail page', async () => {
    const calls = mockFetch()
    const { w, router } = await mountView(calls)
    await w.find('[data-new-name]').setValue('Shed')
    await w.find('[data-new-notes]').setValue('needs shelving')
    await w.find('form').trigger('submit')
    await flushPromises()
    const post = calls.find(c => c.method === 'POST')
    expect(post?.url).toBe('/api/v1/projects')
    expect(JSON.parse(post!.body!)).toEqual({ name: 'Shed', notes: 'needs shelving' })
    expect(router.currentRoute.value.path).toBe('/projects/42')
  })

  it('surfaces a create error as a toast and stays put', async () => {
    const calls = mockFetch({
      postStatus: 400,
      postBody: { error: { code: 'validation_error', message: 'name must be 1-200 chars' } },
    })
    const { w, router } = await mountView(calls)
    await w.find('[data-new-name]').setValue('Shed')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(toasts.some(t => t.type === 'error' && t.message.includes('name must be 1-200 chars'))).toBe(true)
    expect(router.currentRoute.value.path).toBe('/projects')
  })
})
