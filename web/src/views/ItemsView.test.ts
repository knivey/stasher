import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { useItemsStore } from '../stores/items'
import ItemsView from './ItemsView.vue'

beforeEach(() => { setActivePinia(createPinia()); vi.unstubAllGlobals() })

describe('ItemsView', () => {
  it('fetches with default sort and renders item names', async () => {
    const urls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      urls.push(url)
      if (url.includes('/tags')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.includes('/locations')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      return new Response(JSON.stringify({ data: [{ id: 5, name: 'M3 screws', qty: 250, photo_count: 1 }], total: 1 }), { status: 200 })
    }))
    setActivePinia(createPinia())
    const w = mount(ItemsView)
    await flushPromises()
    expect(urls.some(u => u.includes('/items?') && u.includes('sort=updated_at'))).toBe(true)
    expect(urls.find(u => u.includes('/items?'))).toContain('sort=updated_at&order=desc&page=1&page_size=50')
    expect(w.text()).toContain('M3 screws')
    // both responsive branches exist in the DOM when items are present
    expect(w.find('table').exists()).toBe(true)
    expect(w.find('[data-cards]').exists()).toBe(true)
  })

  it('shows a thumbnail image on mobile cards when thumb_key is present', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.includes('/tags')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.includes('/locations')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      return new Response(JSON.stringify({ data: [{ id: 5, name: 'M3 screws', qty: 250, photo_count: 2, thumb_key: '2026/10/abc.png' }], total: 1 }), { status: 200 })
    }))
    setActivePinia(createPinia())
    const w = mount(ItemsView)
    await flushPromises()
    const cards = w.find('[data-cards]')
    const img = cards.find('img')
    expect(img.exists()).toBe(true)
    expect(img.attributes('src')).toBe('/files/2026/10/abc.png')
    // the thumbnail replaces the photo count badge on the card...
    expect(cards.text()).not.toContain('📷')
    // ...and the desktop table stays badge-only (no images)
    expect(w.find('table img').exists()).toBe(false)
  })

  it('keeps the photo count badge on mobile cards when there is no thumb_key', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.includes('/tags')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.includes('/locations')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      return new Response(JSON.stringify({ data: [{ id: 5, name: 'M3 screws', qty: 250, photo_count: 2 }], total: 1 }), { status: 200 })
    }))
    setActivePinia(createPinia())
    const w = mount(ItemsView)
    await flushPromises()
    const cards = w.find('[data-cards]')
    expect(cards.find('img').exists()).toBe(false)
    expect(cards.text()).toContain('📷 2')
  })

  it('shows the empty state when there are no items', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })))
    setActivePinia(createPinia())
    const w = mount(ItemsView)
    await flushPromises()
    expect(w.text()).toContain('No items found.')
  })

  it('seeds the search box from the persisted filter on mount', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.includes('/tags')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.includes('/locations')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    setActivePinia(createPinia())
    useItemsStore().filters.q = 'screw'
    const w = mount(ItemsView)
    await flushPromises()
    expect((w.find('input').element as HTMLInputElement).value).toBe('screw')
  })

  it('sends location_id after changing the location filter', async () => {
    const urls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      urls.push(url)
      if (url.includes('/tags')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.includes('/locations')) return new Response(JSON.stringify({ data: [{ id: 7, name: 'Bin', parent_id: null }], total: 1 }), { status: 200 })
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    setActivePinia(createPinia())
    const w = mount(ItemsView)
    await flushPromises()
    const locationSelect = w.findAll('select')[0]
    ;(locationSelect.element as HTMLSelectElement).value = '7'
    await locationSelect.trigger('change')
    await flushPromises()
    expect(urls.some(u => u.includes('location_id=7'))).toBe(true)
  })
})
