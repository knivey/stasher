import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import AdminView from './AdminView.vue'
import { useAuthStore } from '../stores/auth'
import { toasts } from '../utils/toast'
import type { AdminUser, Invite, User } from '../api/types'

const ME: User = {
  id: 1, email: 'admin@x.dev', display_name: 'Admin', default_item_visibility: 'private', is_admin: true,
}

function adminUser(id: number, email: string, name: string, is_admin: boolean, disabled: boolean): AdminUser {
  return {
    id, email, display_name: name, default_item_visibility: 'private', is_admin, disabled,
    created_at: '2026-09-01T00:00:00Z',
  }
}

const USED_INVITE: Invite = {
  id: 7, code: 'OLD2345678', created_at: '2026-09-02T00:00:00Z',
  used_by_email: 'pleb@x.dev', used_at: '2026-09-03T10:00:00Z',
}

interface Call { method: string; url: string; body?: string }

// Live mock state: PATCH/DELETE/POST mutate the lists so a refetch "sees"
// the server-side change, exactly like the real backend.
function mockFetch(opts: { usersStatus?: number; patchDisabledStatus?: number; patchDisabledBody?: unknown } = {}) {
  const calls: Call[] = []
  let users: AdminUser[] = [
    adminUser(1, 'admin@x.dev', 'Admin', true, false),
    adminUser(2, 'pleb@x.dev', 'Pleb', false, false),
  ]
  let invites: Invite[] = [{ ...USED_INVITE }]
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    calls.push({ method, url, body: init?.body as string | undefined })
    if (url === '/api/v1/admin/users' && method === 'GET') {
      if (opts.usersStatus && opts.usersStatus !== 200)
        return new Response(
          JSON.stringify({ error: { code: 'forbidden', message: 'admin access required' } }),
          { status: opts.usersStatus })
      return new Response(JSON.stringify({ data: users, total: users.length }), { status: 200 })
    }
    if (url.startsWith('/api/v1/admin/users/') && method === 'PATCH') {
      const body = JSON.parse(init!.body as string)
      if (body.disabled === true && opts.patchDisabledStatus)
        return new Response(JSON.stringify(opts.patchDisabledBody), { status: opts.patchDisabledStatus })
      const u = users.find(x => x.id === Number(url.split('/').pop()))!
      Object.assign(u, body)
      // The real PATCH response echoes id/email/display_name/is_admin/disabled.
      return new Response(JSON.stringify({
        id: u.id, email: u.email, display_name: u.display_name,
        is_admin: u.is_admin, disabled: u.disabled,
      }), { status: 200 })
    }
    if (url === '/api/v1/admin/invites' && method === 'POST') {
      invites = [...invites, { id: 1, code: 'ABCD2345', created_at: '', used_by_email: '', used_at: null }]
      return new Response(JSON.stringify({ id: 1, code: 'ABCD2345', created_at: '' }), { status: 201 })
    }
    if (url === '/api/v1/admin/invites' && method === 'GET')
      return new Response(JSON.stringify({ data: invites, total: invites.length }), { status: 200 })
    if (url.startsWith('/api/v1/admin/invites/') && method === 'DELETE') {
      invites = invites.filter(i => i.id !== Number(url.split('/').pop()))
      return new Response('', { status: 204 })
    }
    return new Response('', { status: 404 })
  }))
  return calls
}

async function mountView(calls: Call[]): Promise<{ w: ReturnType<typeof mount>; router: Router }> {
  setActivePinia(createPinia())
  const auth = useAuthStore()
  auth.user = { ...ME }
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/admin', component: AdminView },
      { path: '/', component: { template: '<div>home</div>' } },
    ],
  })
  await router.push('/admin')
  const w = mount(AdminView, { global: { plugins: [router] } })
  await flushPromises()
  expect(calls.some(c => c.method === 'GET' && c.url === '/api/v1/admin/users')).toBe(true) // users fetched
  return { w, router }
}

beforeEach(() => { vi.unstubAllGlobals(); toasts.splice(0) })

describe('AdminView', () => {
  it('renders users with email, name, created and toggles; the self row has no disable toggle', async () => {
    const { w } = await mountView(mockFetch())
    const rows = w.findAll('[data-user-row]')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('admin@x.dev')
    expect(rows[0].text()).toContain('2026-09-01')
    expect(rows[1].text()).toContain('pleb@x.dev')
    expect(rows[1].text()).toContain('Pleb')
    // every row has an admin toggle…
    expect(w.findAll('[data-admin-toggle]')).toHaveLength(2)
    // …but only the OTHER user gets a disable toggle — self-disable is
    // impossible from the UI (backend would reject with cannot_disable_self).
    expect(w.findAll('[data-disable-toggle]')).toHaveLength(1)
    expect(rows[0].find('[data-disable-toggle]').exists()).toBe(false)
    expect(rows[1].find('[data-disable-toggle]').exists()).toBe(true)
  })

  it('toggling admin on user 2 PATCHes exactly {is_admin:true}', async () => {
    const calls = mockFetch()
    const { w } = await mountView(calls)
    const row = w.findAll('[data-user-row]')[1]
    await row.find('[data-admin-toggle]').setValue(true)
    await flushPromises()
    const patches = calls.filter(c => c.method === 'PATCH')
    expect(patches).toHaveLength(1)
    expect(patches[0].url).toBe('/api/v1/admin/users/2')
    expect(JSON.parse(patches[0].body!)).toEqual({ is_admin: true }) // single field only
    expect((row.find('[data-admin-toggle]').element as HTMLInputElement).checked).toBe(true)
  })

  it('disable toggle PATCHes exactly {disabled:true} and strikes the row through', async () => {
    const calls = mockFetch()
    const { w } = await mountView(calls)
    const row = w.findAll('[data-user-row]')[1]
    await row.find('[data-disable-toggle]').setValue(true)
    await flushPromises()
    const patch = calls.find(c => c.method === 'PATCH')!
    expect(patch.url).toBe('/api/v1/admin/users/2')
    expect(JSON.parse(patch.body!)).toEqual({ disabled: true }) // single field only
    expect(row.attributes('data-disabled')).toBe('true')
    expect(row.classes()).toContain('line-through')
  })

  it('cannot_disable_self error reverts the toggle and toasts the message', async () => {
    const calls = mockFetch({
      patchDisabledStatus: 400,
      patchDisabledBody: { error: { code: 'cannot_disable_self', message: 'you cannot disable your own account' } },
    })
    const { w } = await mountView(calls)
    const row = w.findAll('[data-user-row]')[1]
    const toggle = row.find('[data-disable-toggle]')
    await toggle.setValue(true)
    await flushPromises()
    expect(toasts.some(t => t.type === 'error' && t.message.includes('you cannot disable your own account'))).toBe(true)
    // local state never accepted the change: checkbox back off, no strikethrough
    expect((toggle.element as HTMLInputElement).checked).toBe(false)
    expect(row.classes()).not.toContain('line-through')
  })

  it('creating an invite renders the code, and copy uses navigator.clipboard.writeText + toasts', async () => {
    const clip = vi.fn(async () => {})
    vi.stubGlobal('navigator', { clipboard: { writeText: clip } })
    const calls = mockFetch()
    const { w } = await mountView(calls)
    await w.find('[data-create-invite]').trigger('click')
    await flushPromises()
    expect(calls.some(c => c.method === 'POST' && c.url === '/api/v1/admin/invites')).toBe(true)
    expect(w.find('[data-new-code]').text()).toBe('ABCD2345')
    await w.find('[data-copy-code]').trigger('click')
    await flushPromises()
    expect(clip).toHaveBeenCalledWith('ABCD2345')
    expect(toasts.some(t => t.type === 'success' && t.message === 'Code copied')).toBe(true)
  })

  it('used invites show who used them and have no delete; unused ones DELETE by id', async () => {
    const calls = mockFetch()
    const { w } = await mountView(calls)
    const rows = w.findAll('[data-invite-row]')
    expect(rows).toHaveLength(1) // the pre-existing used invite
    expect(rows[0].text()).toContain('OLD2345678')
    expect(rows[0].text()).toContain('pleb@x.dev')
    expect(rows[0].text()).toContain('2026-09-03')
    expect(rows[0].find('[data-delete-invite]').exists()).toBe(false) // used → undeletable
    // create one → it appears in the refreshed list and it IS deletable
    await w.find('[data-create-invite]').trigger('click')
    await flushPromises()
    const fresh = w.findAll('[data-invite-row]').find(r => r.text().includes('ABCD2345'))!
    expect(fresh.find('[data-delete-invite]').exists()).toBe(true)
    await fresh.find('[data-delete-invite]').trigger('click')
    await flushPromises()
    expect(calls.some(c => c.method === 'DELETE' && c.url === '/api/v1/admin/invites/1')).toBe(true)
    expect(w.findAll('[data-invite-row]').find(r => r.text().includes('ABCD2345'))).toBeUndefined()
    expect(toasts.some(t => t.type === 'success' && /deleted/i.test(t.message))).toBe(true)
  })

  it('403 forbidden toasts and redirects to /', async () => {
    const calls = mockFetch({ usersStatus: 403 })
    const { router } = await mountView(calls)
    expect(toasts.some(t => t.type === 'error' && t.message === 'admin access required')).toBe(true)
    expect(router.currentRoute.value.path).toBe('/')
    // bounced before touching the invites endpoints
    expect(calls.some(c => c.url === '/api/v1/admin/invites')).toBe(false)
  })
})
