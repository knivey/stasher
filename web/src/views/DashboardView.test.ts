import { describe, it, expect, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import DashboardView from './DashboardView.vue'

describe('DashboardView', () => {
  it('renders counts and recent items from three API calls', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      calls.push(url)
      const body = url.includes('/items?') ? { data: Array.from({ length: 10 }, (_, i) => ({ id: i, name: `Item ${i}` })), total: 123 }
        : url.includes('/locations') ? { data: [{ id: 1 }], total: 7 }
        : { data: [{ id: 1, status: 'active' }], total: 3 }
      return new Response(JSON.stringify(body), { status: 200 })
    }))
    const w = mount(DashboardView)
    await flushPromises()
    expect(calls.some(c => c.includes('sort=created_at'))).toBe(true)
    expect(w.text()).toContain('123')
    expect(w.text()).toContain('7')
    expect(w.text()).toContain('Item 0')
  })
})
