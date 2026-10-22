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
    // ...and the desktop table renders the same thumbnail
    expect(w.find('table img').exists()).toBe(true)
  })

  it('shows a thumbnail image in the desktop table when thumb_key is present', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.includes('/tags')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.includes('/locations')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      return new Response(JSON.stringify({ data: [{ id: 5, name: 'M3 screws', qty: 250, photo_count: 2, thumb_key: '2026/10/abc.jpg' }], total: 1 }), { status: 200 })
    }))
    setActivePinia(createPinia())
    const w = mount(ItemsView)
    await flushPromises()
    const img = w.find('table').find('img[src="/files/2026/10/abc.jpg"]')
    expect(img.exists()).toBe(true)
    expect(img.attributes('loading')).toBe('lazy')
  })

  it('hovering a desktop thumb opens a popup with the image + alt, leaving removes it', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.includes('/tags')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.includes('/locations')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      return new Response(JSON.stringify({ data: [{ id: 5, name: 'M3 screws', qty: 250, photo_count: 2, thumb_key: '2026/10/abc.jpg' }], total: 1 }), { status: 200 })
    }))
    setActivePinia(createPinia())
    const w = mount(ItemsView)
    await flushPromises()
    expect(w.find('[data-thumb-popup]').exists()).toBe(false)
    const thumb = w.find('table img')
    expect(thumb.attributes('tabindex')).toBe('0') // keyboard-reachable
    expect(thumb.attributes('alt')).toBe('M3 screws')
    await thumb.trigger('mouseenter')
    const popup = w.find('[data-thumb-popup]')
    expect(popup.exists()).toBe(true)
    expect(popup.classes()).toContain('pointer-events-none') // never traps the hover
    expect(popup.find('img').attributes('src')).toBe('/files/2026/10/abc.jpg')
    expect(popup.find('img').attributes('alt')).toBe('M3 screws')
    await thumb.trigger('mouseleave')
    expect(w.find('[data-thumb-popup]').exists()).toBe(false)
  })

  it('keyboard focus on a desktop thumb shows the same popup (anchored + clamped); blur removes it', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.includes('/tags')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.includes('/locations')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      return new Response(JSON.stringify({ data: [{ id: 5, name: 'M3 screws', qty: 250, photo_count: 2, thumb_key: '2026/10/abc.jpg' }], total: 1 }), { status: 200 })
    }))
    setActivePinia(createPinia())
    const w = mount(ItemsView)
    await flushPromises()
    const thumb = w.find('table img')
    await thumb.trigger('focus')
    const popup = w.find('[data-thumb-popup]')
    expect(popup.exists()).toBe(true)
    // same rect logic as hover; happy-dom default rect (0,0,0,0) → top clamps to 8
    expect(popup.attributes('style')).toContain('left: 12px')
    expect(popup.attributes('style')).toContain('top: 8px')
    await thumb.trigger('blur')
    expect(w.find('[data-thumb-popup]').exists()).toBe(false)
  })

  it('anchors the hover popup next to the hovered thumb, flipping left near the right edge', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.includes('/tags')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.includes('/locations')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      return new Response(JSON.stringify({ data: [{ id: 5, name: 'M3 screws', qty: 250, photo_count: 2, thumb_key: '2026/10/abc.jpg' }], total: 1 }), { status: 200 })
    }))
    setActivePinia(createPinia())
    const w = mount(ItemsView)
    await flushPromises()
    const thumb = w.find('table img')

    // normal case: popup sits to the right of the thumb (left + width + 12)
    thumb.element.getBoundingClientRect = () => ({ left: 100, top: 50, width: 40, height: 40 } as DOMRect)
    await thumb.trigger('mouseenter')
    let popup = w.find('[data-thumb-popup]')
    expect(popup.exists()).toBe(true)
    expect(popup.attributes('style')).toContain('left: 152px') // 100 + 40 + 12
    expect(popup.attributes('style')).toContain('top: 42px') // 50 - 8
    await thumb.trigger('mouseleave')
    expect(w.find('[data-thumb-popup]').exists()).toBe(false)

    // near-right-edge: 900 + 40 + 12 + 320 > 1024 → flips to the left of the thumb
    thumb.element.getBoundingClientRect = () => ({ left: 900, top: 50, width: 40, height: 40 } as DOMRect)
    await thumb.trigger('mouseenter')
    popup = w.find('[data-thumb-popup]')
    expect(popup.exists()).toBe(true)
    expect(popup.attributes('style')).toContain('left: 568px') // 900 - 332
    await thumb.trigger('mouseleave')
  })

  it('shows an em-dash placeholder in the desktop table thumb cell when there is no thumb_key', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.includes('/tags')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.includes('/locations')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      return new Response(JSON.stringify({ data: [{ id: 5, name: 'M3 screws', qty: 250, photo_count: 2 }], total: 1 }), { status: 200 })
    }))
    setActivePinia(createPinia())
    const w = mount(ItemsView)
    await flushPromises()
    const table = w.find('table')
    expect(table.find('img').exists()).toBe(false)
    const thumbCell = table.findAll('tbody tr')[0].findAll('td')[0]
    expect(thumbCell.text()).toBe('—')
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

  it('resets the filters back to their defaults when leaving the page', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })))
    setActivePinia(createPinia())
    const store = useItemsStore()
    store.filters.tag = 'hardware'
    store.filters.visibility = 'shared'
    store.filters.q = 'screw'
    store.filters.page = 3
    const w = mount(ItemsView)
    await flushPromises()
    w.unmount()
    expect(store.filters).toEqual({
      q: '', location_id: 0, tag: '', project_id: 0,
      visibility: '', sort: 'updated_at', order: 'desc', page: 1, page_size: 50,
    })
  })

  it('still applies a filter set from another view before mount (tag-chip flow survives)', async () => {
    const urls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      urls.push(url)
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    setActivePinia(createPinia())
    // ItemDetailView.filterByTag sets the store THEN pushes /items
    useItemsStore().filters.tag = 'x'
    const w = mount(ItemsView)
    await flushPromises()
    expect(urls.some(u => u.includes('/items?') && u.includes('tag=x'))).toBe(true)
    w.unmount()
  })

  it('mounts with an empty search box after the previous visit reset the filters', async () => {
    const urls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      urls.push(url)
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    setActivePinia(createPinia())
    useItemsStore().filters.q = 'stale'
    let w = mount(ItemsView)
    await flushPromises()
    expect((w.find('input').element as HTMLInputElement).value).toBe('stale')
    w.unmount()
    urls.length = 0
    w = mount(ItemsView)
    await flushPromises()
    expect((w.find('input').element as HTMLInputElement).value).toBe('')
    expect(urls.some(u => u.includes('q='))).toBe(false) // the stale q never reaches the API
  })

  it('shows a muted disposed badge on disposed items in both the table and the mobile cards', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.includes('/tags')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.includes('/locations')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      return new Response(JSON.stringify({
        data: [
          { id: 5, name: 'dead PSU', qty: 0, disposed: true },
          { id: 6, name: 'live PSU', qty: 1 },
        ],
        total: 2,
      }), { status: 200 })
    }))
    setActivePinia(createPinia())
    const w = mount(ItemsView)
    await flushPromises()
    // table: badge sits in the Name cell
    expect(w.find('table').find('[data-disposed]').exists()).toBe(true)
    expect(w.find('table').text()).toContain('disposed')
    // mobile cards: small gray badge
    expect(w.find('[data-cards]').find('[data-disposed]').exists()).toBe(true)
    // exactly one badge per branch (only the disposed row carries it)
    expect(w.findAll('[data-disposed]')).toHaveLength(2)
  })
})
