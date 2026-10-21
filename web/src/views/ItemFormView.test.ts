import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import ItemFormView from './ItemFormView.vue'
import { toasts } from '../utils/toast'

beforeEach(() => { setActivePinia(createPinia()); vi.unstubAllGlobals(); vi.restoreAllMocks(); toasts.splice(0) })

describe('ItemFormView PATCH body', () => {
  it('omits untouched fields; sends location_id 0 only when cleared', async () => {
    const item = { id: 9, name: 'Widget', description: 'd', qty: 2, unit: 'pcs', location_id: 3,
      visibility: 'private', created_at: '', updated_at: '', tags: [{ id: 1, name: 't1' }],
      photos: [], documents: [], projects: [] }
    const bodies: any[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      if (url.match(/\/items\/9$/) && init?.method === 'PATCH') bodies.push(JSON.parse(init.body))
      if (url.includes('/locations')) return new Response(JSON.stringify({ data: [{ id: 3, name: 'Bin', parent_id: 2 }], total: 1 }), { status: 200 })
      return new Response(JSON.stringify(item), { status: 200 })
    }))
    const w = mount(ItemFormView, { props: { id: '9' } })
    await flushPromises()
    // user clears the location and changes nothing else
    ;(w.vm as any).form.location_id = 0
    await (w.vm as any).save()
    expect(bodies.at(-1)).toEqual({ location_id: 0 })
    // user edits qty instead, leaves location at original
    ;(w.vm as any).form.location_id = 3
    ;(w.vm as any).form.qty = 5
    await (w.vm as any).save()
    expect(bodies.at(-1)).toEqual({ qty: 5 })
  })
})

describe('ItemFormView create', () => {
  function newRouter(): Router {
    return createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/items', component: { template: '<div>items</div>' } },
        { path: '/items/new', component: ItemFormView },
        { path: '/items/:id', component: { template: '<div>detail</div>' } },
      ],
    })
  }

  it('posts the full body, omitting location_id when 0 and tags when empty, then navigates', async () => {
    const bodies: any[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      if (url.endsWith('/api/v1/items') && init?.method === 'POST') {
        bodies.push(JSON.parse(init.body))
        return new Response(JSON.stringify({ id: 42 }), { status: 200 })
      }
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    const router = newRouter()
    await router.push('/items/new')
    const w = mount(ItemFormView, { global: { plugins: [router] } })
    await flushPromises()
    const vm = w.vm as any
    vm.form.name = 'New thing'
    vm.form.qty = 3
    vm.form.unit = 'pcs'
    vm.form.tags = ['x']
    await vm.save()
    await flushPromises()
    expect(bodies.at(-1)).toEqual({ name: 'New thing', description: '', qty: 3, unit: 'pcs', visibility: 'private', tags: ['x'] })
    expect(router.currentRoute.value.path).toBe('/items/42')
  })

  it('includes location_id in the create body only when non-zero', async () => {
    const bodies: any[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      if (url.endsWith('/api/v1/items') && init?.method === 'POST') {
        bodies.push(JSON.parse(init.body))
        return new Response(JSON.stringify({ id: 43 }), { status: 200 })
      }
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    const router = newRouter()
    await router.push('/items/new')
    const w = mount(ItemFormView, { global: { plugins: [router] } })
    await flushPromises()
    const vm = w.vm as any
    vm.form.name = 'Binned'
    vm.form.location_id = 7
    await vm.save()
    expect(bodies.at(-1)).toEqual({ name: 'Binned', description: '', qty: 1, unit: '', visibility: 'private', location_id: 7 })
  })

  it('uploads queued photos (downscaled at pick time) and docs after create, then navigates', async () => {
    const calls: string[] = []
    let uploadedBlob: Blob | undefined
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      if (url.endsWith('/api/v1/items') && init?.method === 'POST')
        return new Response(JSON.stringify({ id: 42 }), { status: 200 })
      if (url.includes('/items/42/photos')) {
        uploadedBlob = (init.body as FormData).get('file') as Blob
        calls.push('POST photo')
        return new Response(null, { status: 204 })
      }
      if (url.includes('/items/42/documents')) { calls.push('POST doc'); return new Response(null, { status: 204 }) }
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    const router = newRouter()
    await router.push('/items/new')
    const w = mount(ItemFormView, { global: { plugins: [router] } })
    await flushPromises()
    const vm = w.vm as any
    vm.form.name = 'Snapshotted'

    // canvas stubs AFTER mount; only 'canvas' is faked so Vue's own DOM updates keep working
    const ctx = { drawImage: vi.fn() }
    const canvas = { width: 0, height: 0, getContext: () => ctx,
      toBlob: (cb: (b: Blob) => void) => cb(new Blob(['j'], { type: 'image/jpeg' })) }
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue({ width: 4000, height: 2000, close() {} }))
    const realCreate = document.createElement.bind(document)
    vi.spyOn(document, 'createElement').mockImplementation((tag: string) =>
      tag === 'canvas' ? canvas as unknown as HTMLCanvasElement : realCreate(tag as keyof HTMLElementTagNameMap))

    const big = new File(['x'], 'big.jpg', { type: 'image/jpeg' })
    Object.defineProperty(big, 'size', { value: 9_000_000 })
    const photoInput = w.find('input[accept="image/*"]')
    Object.defineProperty(photoInput.element, 'files', { value: [big] })
    await photoInput.trigger('change')
    await flushPromises()
    const docInput = w.findAll('input[type="file"]').find(i => !i.attributes('accept'))!
    Object.defineProperty(docInput.element, 'files', { value: [new File(['%PDF'], 'sheet.pdf', { type: 'application/pdf' })] })
    await docInput.trigger('change')
    await flushPromises()
    expect(w.text()).toContain('1 photo, 1 document will be added after saving')

    await vm.save()
    await flushPromises()
    expect(calls).toEqual(['POST photo', 'POST doc'])
    expect(uploadedBlob?.type).toBe('image/jpeg') // downscaled at pick time, uploaded as-is
    expect(router.currentRoute.value.path).toBe('/items/42')
  })

  it('waits for an in-flight photo downscale before snapshotting the queue (save race)', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      if (url.endsWith('/api/v1/items') && init?.method === 'POST') {
        calls.push('POST item')
        return new Response(JSON.stringify({ id: 42 }), { status: 200 })
      }
      if (url.includes('/items/42/photos')) { calls.push('POST photo'); return new Response(null, { status: 204 }) }
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    const router = newRouter()
    await router.push('/items/new')
    const w = mount(ItemFormView, { global: { plugins: [router] } })
    await flushPromises()
    const vm = w.vm as any
    vm.form.name = 'Race'

    // Hold the canvas encode: toBlob's callback fires only on release, so
    // the pick's downscale promise stays pending past the Save click.
    let releaseEncode!: (b: Blob) => void
    const ctx = { drawImage: vi.fn() }
    const canvas = { width: 0, height: 0, getContext: () => ctx,
      toBlob: (cb: (b: Blob) => void) => { releaseEncode = cb } }
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue({ width: 4000, height: 2000, close() {} }))
    const realCreate = document.createElement.bind(document)
    vi.spyOn(document, 'createElement').mockImplementation((tag: string) =>
      tag === 'canvas' ? canvas as unknown as HTMLCanvasElement : realCreate(tag as keyof HTMLElementTagNameMap))

    const big = new File(['x'], 'big.jpg', { type: 'image/jpeg' })
    Object.defineProperty(big, 'size', { value: 9_000_000 })
    const photoInput = w.find('input[accept="image/*"]')
    Object.defineProperty(photoInput.element, 'files', { value: [big] })
    await photoInput.trigger('change')
    await flushPromises()

    // Save while the encode is still in flight…
    const saving = vm.save()
    await flushPromises()
    expect(calls).toEqual([]) // …nothing POSTed yet: save drains the encode first

    releaseEncode(new Blob(['j'], { type: 'image/jpeg' }))
    await saving
    await flushPromises()
    expect(calls).toEqual(['POST item', 'POST photo']) // the picked photo survived the race
    expect(router.currentRoute.value.path).toBe('/items/42')
  })

  it('toasts but still navigates when a queued upload fails after create', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      if (url.endsWith('/api/v1/items') && init?.method === 'POST')
        return new Response(JSON.stringify({ id: 42 }), { status: 200 })
      if (url.includes('/items/42/documents'))
        return new Response(JSON.stringify({ error: { code: 'internal', message: 'upload blew up' } }), { status: 500 })
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    const router = newRouter()
    await router.push('/items/new')
    const w = mount(ItemFormView, { global: { plugins: [router] } })
    await flushPromises()
    const docInput = w.findAll('input[type="file"]').find(i => !i.attributes('accept'))!
    Object.defineProperty(docInput.element, 'files', { value: [new File(['x'], 'a.txt', { type: 'text/plain' })] })
    await docInput.trigger('change')
    await flushPromises()
    await (w.vm as any).save()
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/items/42') // item exists: navigate anyway
    expect(toasts.some(t => t.message === 'upload blew up' && t.type === 'error')).toBe(true)
  })

  it('ignores a second save while one is in flight: exactly one item POST', async () => {
    let releasePost!: (r: Response) => void
    let posts = 0
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      if (url.endsWith('/api/v1/items') && init?.method === 'POST') {
        posts++
        return new Promise<Response>(res => { releasePost = res })
      }
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    const router = newRouter()
    await router.push('/items/new')
    const w = mount(ItemFormView, { global: { plugins: [router] } })
    await flushPromises()
    const vm = w.vm as any
    vm.form.name = 'Once only'
    const first = vm.save()
    const second = vm.save() // second click while the POST is still pending
    await flushPromises()
    expect(posts).toBe(1) // early-returned: no second POST was issued
    const submit = w.find('button[type="submit"]')
    expect(submit.attributes('disabled')).toBeDefined()
    expect(submit.text()).toContain('Saving…')
    releasePost(new Response(JSON.stringify({ id: 42 }), { status: 200 }))
    await Promise.all([first, second])
    await flushPromises()
    expect(posts).toBe(1) // still exactly one item POST
    expect(router.currentRoute.value.path).toBe('/items/42')
  })
})

describe('ItemFormView widgets', () => {
  const item = { id: 9, name: 'Widget', description: 'd', qty: 2, unit: 'pcs', location_id: 3,
    visibility: 'private', created_at: '', updated_at: '', tags: [], photos: [], documents: [], projects: [] }

  it('TagInput adds chips on Enter, dedupes case-sensitively and caps at 50', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) =>
      url.includes('/locations')
        ? new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
        : new Response(JSON.stringify(item), { status: 200 })))
    const w = mount(ItemFormView, { props: { id: '9' } })
    await flushPromises()
    const input = w.find('input[placeholder="Add tag…"]')
    const add = async (v: string) => { await input.setValue(v); await input.trigger('keydown', { key: 'Enter' }) }
    await add('  alpha ')
    await add('beta')
    await add('alpha') // exact dup ignored
    await add('Alpha') // case-sensitive: different tag
    expect((w.vm as any).form.tags).toEqual(['alpha', 'beta', 'Alpha'])
    ;(w.vm as any).form.tags = Array.from({ length: 50 }, (_, i) => `t${i}`)
    await add('overflow')
    expect((w.vm as any).form.tags).toHaveLength(50)
  })

  it('LocationSelect offers "No location" with value 0 and indented locations', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) =>
      url.includes('/locations')
        ? new Response(JSON.stringify({ data: [
          { id: 3, name: 'Bin', parent_id: 2 }, { id: 1, name: 'House', parent_id: null },
          { id: 2, name: 'Cabinet', parent_id: 1 },
        ], total: 3 }), { status: 200 })
        : new Response(JSON.stringify(item), { status: 200 })))
    const w = mount(ItemFormView, { props: { id: '9' } })
    await flushPromises()
    const select = w.find('select[aria-label="Location"]')
    const none = select.findAll('option').find(o => o.text() === 'No location')
    expect(none).toBeTruthy()
    expect((none!.element as HTMLOptionElement).value).toBe('0')
    expect(select.text()).toContain('— Cabinet')
    expect(select.text()).toContain('— — Bin')
  })

  it('toasts and returns to /items when the edit fetch 404s', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) =>
      url.includes('/locations')
        ? new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
        : new Response(JSON.stringify({ error: { code: 'not_found', message: 'nope' } }), { status: 404 })))
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/items', component: { template: '<div>items</div>' } },
        { path: '/items/:id/edit', component: ItemFormView },
      ],
    })
    await router.push('/items/9/edit')
    mount(ItemFormView, { global: { plugins: [router] } })
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/items')
  })
})
