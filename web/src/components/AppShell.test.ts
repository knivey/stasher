import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import AppShell from './AppShell.vue'
import { useAuthStore } from '../stores/auth'
import { toasts } from '../utils/toast'
import type { User } from '../api/types'

const USER: User = {
  id: 1, email: 'me@x.dev', display_name: 'Me', default_item_visibility: 'private', is_admin: false,
}

function makeRouter(): Router {
  return createRouter({
    history: createMemoryHistory(),
    routes: ['/', '/items', '/locations', '/projects', '/shared', '/trash', '/settings', '/admin', '/login']
      .map(path => ({ path, component: { template: `<div>${path}</div>` } })),
  })
}

async function mountShell(isAdmin = false): Promise<{ w: ReturnType<typeof mount>; router: Router }> {
  const auth = useAuthStore()
  auth.user = { ...USER, is_admin: isAdmin }
  const router = makeRouter()
  await router.push('/')
  const w = mount(AppShell, { global: { plugins: [router] } })
  await flushPromises()
  return { w, router }
}

beforeEach(() => { setActivePinia(createPinia()); vi.unstubAllGlobals(); vi.restoreAllMocks(); toasts.splice(0) })

describe('AppShell navigation', () => {
  it('bottom nav shows exactly the five primary destinations', async () => {
    const { w } = await mountShell()
    const links = w.find('nav.bottom-0').findAll('a')
    expect(links.map(l => l.attributes('href'))).toEqual(['/', '/items', '/locations', '/projects', '/shared'])
  })

  it('desktop sidebar lists Trash between Shared and Settings', async () => {
    const { w } = await mountShell()
    const links = w.find('aside').findAll('a')
    expect(links.map(l => l.attributes('href')))
      .toEqual(['/', '/items', '/locations', '/projects', '/shared', '/trash', '/settings'])
  })

  it('mobile header links Shared, Trash and Settings; Admin only for admins', async () => {
    const plain = await mountShell(false)
    const header = plain.w.find('header')
    expect(header.find('a[href="/shared"]').exists()).toBe(true)
    expect(header.find('a[href="/trash"]').exists()).toBe(true)
    expect(header.find('a[href="/settings"]').exists()).toBe(true)
    expect(header.find('a[href="/admin"]').exists()).toBe(false)

    const admin = await mountShell(true)
    const adminHeader = admin.w.find('header')
    expect(adminHeader.find('a[href="/shared"]').exists()).toBe(true)
    expect(adminHeader.find('a[href="/trash"]').exists()).toBe(true)
    expect(adminHeader.find('a[href="/settings"]').exists()).toBe(true)
    expect(adminHeader.find('a[href="/admin"]').exists()).toBe(true)
  })
})

describe('AppShell logout', () => {
  it('toasts and still navigates to /login when the logout request fails', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('network down') }))
    const { w, router } = await mountShell()
    const signOut = w.findAll('button').find(b => b.text() === 'Sign out')!
    await signOut.trigger('click')
    await flushPromises()
    expect(toasts.some(t => t.type === 'error' && t.message === 'network down')).toBe(true)
    expect(router.currentRoute.value.path).toBe('/login')
  })

  it('navigates to /login after a successful logout, without an error toast', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 204 })))
    const { w, router } = await mountShell()
    const signOut = w.findAll('button').find(b => b.text() === 'Sign out')!
    await signOut.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/login')
    expect(toasts.some(t => t.type === 'error')).toBe(false)
  })
})
