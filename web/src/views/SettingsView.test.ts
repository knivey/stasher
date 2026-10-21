import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import SettingsView from './SettingsView.vue'
import { useAuthStore } from '../stores/auth'
import { toasts } from '../utils/toast'
import type { SessionRow, User } from '../api/types'

const USER: User = {
  id: 1, email: 'me@x.dev', display_name: 'Old', default_item_visibility: 'private', is_admin: false,
}

function sess(id: number, current: boolean): SessionRow {
  return {
    id, user_agent: `UA-${id}`, last_ip: `10.0.0.${id}`,
    created_at: '2026-09-01T00:00:00Z', last_seen_at: `2026-10-0${id}T12:00:00Z`, current,
  }
}

interface Call { method: string; url: string; body?: string }

// Mocks every /api/v1/auth endpoint the view touches. The sessions list is
// live: DELETE/revoke-all mutate it so a refetch "sees" the server-side change.
function mockFetch(opts: { passwordStatus?: number; passwordBody?: unknown } = {}) {
  const calls: Call[] = []
  let sessions: SessionRow[] = [sess(4, true), sess(5, false)]
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    calls.push({ method, url, body: init?.body as string | undefined })
    if (url === '/api/v1/auth/sessions' && method === 'GET')
      return new Response(JSON.stringify({ data: sessions, total: sessions.length }), { status: 200 })
    if (url === '/api/v1/auth/me' && method === 'PATCH')
      return new Response(JSON.stringify({ ...USER, ...JSON.parse(init!.body as string) }), { status: 200 })
    if (url === '/api/v1/auth/password') {
      if (opts.passwordStatus && opts.passwordStatus !== 204)
        return new Response(JSON.stringify(opts.passwordBody), { status: opts.passwordStatus })
      return new Response('', { status: 204 })
    }
    if (url === '/api/v1/auth/sessions/revoke-all') {
      sessions = sessions.filter(s => s.current)
      return new Response('', { status: 204 })
    }
    if (method === 'DELETE') { // /api/v1/auth/sessions/{id}
      sessions = sessions.filter(s => s.id !== Number(url.split('/').pop()))
      return new Response('', { status: 204 })
    }
    return new Response('', { status: 404 })
  }))
  return calls
}

async function mountView(calls: Call[]) {
  setActivePinia(createPinia())
  const auth = useAuthStore()
  auth.user = { ...USER }
  const w = mount(SettingsView)
  await flushPromises()
  expect(calls.some(c => c.method === 'GET' && c.url === '/api/v1/auth/sessions')).toBe(true) // devices fetched
  return { w, auth }
}

beforeEach(() => { vi.unstubAllGlobals(); toasts.splice(0) })

describe('SettingsView', () => {
  it('profile save PATCHes only changed fields and updates auth.user from the response', async () => {
    const calls = mockFetch()
    const { w, auth } = await mountView(calls)
    await w.find('[data-profile-name]').setValue('New')
    await w.find('[data-profile-visibility]').setValue('shared')
    await w.find('[data-profile-form]').trigger('submit')
    await flushPromises()
    const patch = calls.find(c => c.method === 'PATCH')
    expect(patch?.url).toBe('/api/v1/auth/me')
    expect(JSON.parse(patch!.body!)).toEqual({ display_name: 'New', default_item_visibility: 'shared' })
    // the 200 body (updated user) was assigned into the store
    expect(auth.user?.display_name).toBe('New')
    expect(auth.user?.default_item_visibility).toBe('shared')
    expect(toasts.some(t => t.type === 'success' && /profile/i.test(t.message))).toBe(true)
  })

  it('sends no PATCH when nothing changed; single-field change omits the other field', async () => {
    const calls = mockFetch()
    const { w } = await mountView(calls)
    await w.find('[data-profile-form]').trigger('submit') // untouched → no request
    await flushPromises()
    expect(calls.some(c => c.method === 'PATCH')).toBe(false)

    await w.find('[data-profile-name]').setValue('Newer')
    await w.find('[data-profile-form]').trigger('submit')
    await flushPromises()
    const patch = calls.find(c => c.method === 'PATCH')
    expect(JSON.parse(patch!.body!)).toEqual({ display_name: 'Newer' })
  })

  it('password change POSTs {current,new,revoke_other_sessions} and toasts on 204', async () => {
    const calls = mockFetch()
    const { w } = await mountView(calls)
    await w.find('[data-pw-current]').setValue('a')
    await w.find('[data-pw-new]').setValue('b')
    await w.find('[data-pw-revoke-others]').setValue(true)
    await w.find('[data-pw-form]').trigger('submit')
    await flushPromises()
    const post = calls.find(c => c.method === 'POST' && c.url === '/api/v1/auth/password')
    expect(JSON.parse(post!.body!)).toEqual({
      current_password: 'a', new_password: 'b', revoke_other_sessions: true,
    })
    expect(toasts.some(t => t.type === 'success' && /password changed/i.test(t.message))).toBe(true)
    // revoke_other_sessions killed the other device server-side → list refetched
    expect(calls.filter(c => c.method === 'GET' && c.url === '/api/v1/auth/sessions').length).toBeGreaterThanOrEqual(2)
  })

  it('400 invalid_current_password surfaces inline at the current-password field and as a toast', async () => {
    const calls = mockFetch({
      passwordStatus: 400,
      passwordBody: { error: { code: 'invalid_current_password', message: 'current password is incorrect' } },
    })
    const { w } = await mountView(calls)
    await w.find('[data-pw-current]').setValue('wrong')
    await w.find('[data-pw-new]').setValue('hunter2hunter2')
    await w.find('[data-pw-form]').trigger('submit')
    await flushPromises()
    expect(w.find('[data-pw-error]').text()).toContain('current password is incorrect')
    expect(toasts.some(t => t.type === 'error' && t.message.includes('current password is incorrect'))).toBe(true)
  })

  it('renders one row per session; This device badge and revoke button only on the right rows', async () => {
    const { w } = await mountView(mockFetch())
    const rows = w.findAll('[data-session-row]')
    expect(rows).toHaveLength(2)
    expect(w.findAll('[data-current-badge]')).toHaveLength(1)
    expect(w.find('[data-current-badge]').text()).toBe('This device')
    // badge sits on the current row (UA-4); that row has no revoke button
    const currentRow = rows.find(r => r.find('[data-current-badge]').exists())!
    expect(currentRow.text()).toContain('UA-4')
    expect(currentRow.find('[data-revoke-session]').exists()).toBe(false)
    const revokeBtns = w.findAll('[data-revoke-session]')
    expect(revokeBtns).toHaveLength(1)
    expect(revokeBtns[0].text().toLowerCase()).toContain('revoke')
    expect(rows[1].text()).toContain('10.0.0.5')
    expect(rows[1].text()).toContain('2026-10-05')
  })

  it('revoking a device DELETEs its session then refetches and the row disappears', async () => {
    const calls = mockFetch()
    const { w } = await mountView(calls)
    await w.find('[data-revoke-session]').trigger('click')
    await flushPromises()
    expect(calls.some(c => c.method === 'DELETE' && c.url === '/api/v1/auth/sessions/5')).toBe(true)
    expect(calls.filter(c => c.method === 'GET' && c.url === '/api/v1/auth/sessions').length).toBeGreaterThanOrEqual(2)
    expect(w.text()).not.toContain('UA-5')
    expect(w.findAll('[data-session-row]')).toHaveLength(1)
    expect(toasts.some(t => t.type === 'success' && /revoked/i.test(t.message))).toBe(true)
  })

  it('Revoke all other devices POSTs {except_current:true}, refetches and toasts', async () => {
    const calls = mockFetch()
    const { w } = await mountView(calls)
    await w.find('[data-revoke-all]').trigger('click')
    await flushPromises()
    const post = calls.find(c => c.method === 'POST' && c.url === '/api/v1/auth/sessions/revoke-all')
    expect(JSON.parse(post!.body!)).toEqual({ except_current: true })
    expect(calls.filter(c => c.method === 'GET' && c.url === '/api/v1/auth/sessions').length).toBeGreaterThanOrEqual(2)
    expect(w.findAll('[data-session-row]')).toHaveLength(1) // only the current device remains
    expect(w.text()).toContain('UA-4')
    expect(toasts.some(t => t.type === 'success' && /revoked/i.test(t.message))).toBe(true)
  })
})
