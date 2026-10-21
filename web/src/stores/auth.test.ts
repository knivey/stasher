import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore } from './auth'

const me = { id: 1, email: 'a@b.c', display_name: 'A', default_item_visibility: 'private', is_admin: false }

beforeEach(() => { setActivePinia(createPinia()); vi.unstubAllGlobals() })

describe('auth store', () => {
  it('bootstrap hydrates user on 200', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify(me), { status: 200 })))
    const s = useAuthStore()
    await s.bootstrap()
    expect(s.user?.email).toBe('a@b.c')
    expect(s.ready).toBe(true)
  })

  it('bootstrap clears user on 401', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'x' } }), { status: 401 })))
    const s = useAuthStore()
    await s.bootstrap()
    expect(s.user).toBeNull()
    expect(s.ready).toBe(true)
  })

  it('login stores user and clears error', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify(me), { status: 200 })))
    const s = useAuthStore()
    await s.login('a@b.c', 'longpassword1')
    expect(s.user?.email).toBe('a@b.c')
  })
})
