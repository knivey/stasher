import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import ItemFormView from './ItemFormView.vue'
import { useAuthStore } from '../stores/auth'
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

  it('prefills the location select from ?location=<id> (LocationsView "Add item")', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) =>
      url.includes('/locations')
        ? new Response(JSON.stringify({ data: [{ id: 3, name: 'Bin', parent_id: null }], total: 1 }), { status: 200 })
        : new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })))
    const router = newRouter()
    await router.push('/items/new?location=3')
    const w = mount(ItemFormView, { global: { plugins: [router] } })
    await flushPromises()
    expect((w.vm as any).form.location_id).toBe(3)
    const select = w.find('select[aria-label="Location"]')
    expect((select.element as HTMLSelectElement).value).toBe('3')
  })

  it('silently ignores ?location=<id> that is not among the loaded locations', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) =>
      url.includes('/locations')
        ? new Response(JSON.stringify({ data: [{ id: 3, name: 'Bin', parent_id: null }], total: 1 }), { status: 200 })
        : new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })))
    const router = newRouter()
    await router.push('/items/new?location=999')
    const w = mount(ItemFormView, { global: { plugins: [router] } })
    await flushPromises()
    expect((w.vm as any).form.location_id).toBe(0)
    expect((w.find('select[aria-label="Location"]').element as HTMLSelectElement).value).toBe('0')
    expect(toasts.some(t => t.type === 'error')).toBe(false) // no toast storm
  })

  it('silently ignores a non-numeric ?location value: no prefill, no toast, mounts clean', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) =>
      url.includes('/locations')
        ? new Response(JSON.stringify({ data: [{ id: 3, name: 'Bin', parent_id: null }], total: 1 }), { status: 200 })
        : new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })))
    const router = newRouter()
    await router.push('/items/new?location=abc')
    const w = mount(ItemFormView, { global: { plugins: [router] } })
    await flushPromises()
    expect((w.vm as any).form.location_id).toBe(0) // no prefill
    expect((w.find('select[aria-label="Location"]').element as HTMLSelectElement).value).toBe('0')
    expect(toasts.some(t => t.type === 'error')).toBe(false) // stale/garbage link: silent
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

  it('disables the photo and document pick inputs while a save is in flight', async () => {
    // Residual race (backlog): a pick STARTED during an in-flight save
    // misses the post-POST encode drain. Blocking the pickers closes it.
    let releasePost!: (r: Response) => void
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      if (url.endsWith('/api/v1/items') && init?.method === 'POST')
        return new Promise<Response>(res => { releasePost = res })
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    const router = newRouter()
    await router.push('/items/new')
    const w = mount(ItemFormView, { global: { plugins: [router] } })
    await flushPromises()
    const vm = w.vm as any
    vm.form.name = 'Picker locked'
    const photoInput = w.find('input[accept="image/*"]')
    const docInput = w.findAll('input[type="file"]').find(i => !i.attributes('accept'))!
    expect(photoInput.attributes('disabled')).toBeUndefined()
    expect(docInput.attributes('disabled')).toBeUndefined()

    const saving = vm.save()
    await flushPromises()
    expect(photoInput.attributes('disabled')).toBeDefined()
    expect(docInput.attributes('disabled')).toBeDefined()

    releasePost(new Response(JSON.stringify({ id: 42 }), { status: 200 }))
    await saving
    await flushPromises()
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
    ;(w.vm as any).form.name = 'Docs' // the whitespace guard would otherwise block save before the uploads
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

  it('silently focuses the name field when the name is whitespace-only (native required accepts " ")', async () => {
    let posts = 0
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      if (url.endsWith('/api/v1/items') && init?.method === 'POST') posts++
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    const router = newRouter()
    await router.push('/items/new')
    const pushSpy = vi.spyOn(router, 'push')
    const w = mount(ItemFormView, { global: { plugins: [router] } })
    await flushPromises()
    const vm = w.vm as any
    vm.form.name = '   '
    vm.form.qty = 3 // would be reset to 1 if a save actually ran
    // focus (not document.activeElement): VTU mounts detached from the
    // document, so happy-dom treats focus() on it as a no-op.
    const focusSpy = vi.spyOn(w.find('input').element, 'focus')
    await vm.save()
    await flushPromises()
    expect(posts).toBe(0) // nothing to save: no POST
    expect(vm.form.qty).toBe(3) // …and no reset either
    expect(pushSpy).not.toHaveBeenCalled()
    expect(toasts.some(t => t.message === 'Saved')).toBe(false)
    expect(toasts.some(t => t.type === 'error')).toBe(false) // silent, like native validation
    expect(focusSpy).toHaveBeenCalled() // name field is focused
  })
})

describe('ItemFormView save & add another', () => {
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

  async function mountCreate(router: Router) {
    await router.push('/items/new')
    const w = mount(ItemFormView, { global: { plugins: [router] } })
    await flushPromises()
    return w
  }

  function againButton(w: { findAll: (s: string) => { text: () => string; attributes: (a: string) => unknown; trigger: (e: string) => Promise<void> }[] }) {
    return w.findAll('button').find(b => b.text() === 'Save & add another')!
  }

  it('renders "Save & add another" in create mode only', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })))
    const w = await mountCreate(newRouter())
    expect(againButton(w)).toBeTruthy()

    const item = { id: 9, name: 'Widget', description: 'd', qty: 2, unit: 'pcs', location_id: 3,
      visibility: 'private', created_at: '', updated_at: '', tags: [], photos: [], documents: [], projects: [] }
    vi.stubGlobal('fetch', vi.fn(async (url: string) =>
      url.includes('/locations')
        ? new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
        : new Response(JSON.stringify(item), { status: 200 })))
    const we = mount(ItemFormView, { props: { id: '9' } })
    await flushPromises()
    expect(we.findAll('button').some(b => b.text() === 'Save & add another')).toBe(false)
  })

  it('creates + uploads the queue, then resets the form but keeps the location; never pushes', async () => {
    useAuthStore().user = { id: 1, email: 'a@b.c', display_name: 'A', default_item_visibility: 'shared', is_admin: false }
    const uploads: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      if (url.endsWith('/api/v1/items') && init?.method === 'POST')
        return new Response(JSON.stringify({ id: 42 }), { status: 200 })
      if (url.includes('/items/42/photos')) { uploads.push(url); return new Response(null, { status: 204 }) }
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    const router = newRouter()
    const w = await mountCreate(router)
    const pushSpy = vi.spyOn(router, 'push')
    const replaceSpy = vi.spyOn(router, 'replace')
    const vm = w.vm as any
    vm.form.name = 'First'
    vm.form.qty = 3
    vm.form.tags = ['x']
    vm.form.location_id = 7
    vm.form.visibility = 'private' // differs from the user default: proves the reset re-reads the store

    // queue one photo before saving
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
    expect(w.text()).toContain('1 photo, 0 documents will be added after saving')

    await againButton(w).trigger('click')
    await flushPromises()

    expect(uploads).toEqual(['/api/v1/items/42/photos']) // upload ran with the created id
    expect(toasts.some(t => t.message === 'Saved' && t.type === 'success')).toBe(true)
    expect(vm.form.name).toBe('')
    expect(vm.form.qty).toBe(1)
    expect(vm.form.tags).toEqual([])
    expect(vm.form.visibility).toBe('shared') // reset re-read the user default
    expect(vm.form.location_id).toBe(7) // the whole point: the location is kept
    expect(pushSpy).not.toHaveBeenCalled() // stays on the form for the next item
    expect(replaceSpy).toHaveBeenCalledWith('/items/new?location=7')
    expect(router.currentRoute.value.query.location).toBe('7')
    expect(w.text()).not.toContain('will be added after saving') // queue cleared after upload
  })

  it('loops: two sequential clicks POST twice, the second with the retained location', async () => {
    const bodies: any[] = []
    let nextId = 42
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      if (url.endsWith('/api/v1/items') && init?.method === 'POST') {
        bodies.push(JSON.parse(init.body))
        return new Response(JSON.stringify({ id: nextId++ }), { status: 200 })
      }
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    const router = newRouter()
    const w = await mountCreate(router)
    const pushSpy = vi.spyOn(router, 'push')
    const replaceSpy = vi.spyOn(router, 'replace')
    const vm = w.vm as any
    vm.form.name = 'First'
    vm.form.location_id = 7
    await againButton(w).trigger('click')
    await flushPromises()
    vm.form.name = 'Second'
    await againButton(w).trigger('click')
    await flushPromises()
    expect(bodies).toHaveLength(2)
    expect(bodies[0]).toMatchObject({ name: 'First', location_id: 7 })
    expect(bodies[1]).toMatchObject({ name: 'Second', location_id: 7 })
    expect(replaceSpy).toHaveBeenCalledTimes(2)
    expect(replaceSpy).toHaveBeenCalledWith('/items/new?location=7')
    expect(pushSpy).not.toHaveBeenCalled()
  })

  it('keeps the form intact and toasts the error when the POST fails', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      if (url.endsWith('/api/v1/items') && init?.method === 'POST')
        return new Response(JSON.stringify({ error: { code: 'internal', message: 'boom' } }), { status: 500 })
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    const router = newRouter()
    const replaceSpy = vi.spyOn(router, 'replace')
    const w = await mountCreate(router)
    const vm = w.vm as any
    vm.form.name = 'Precious'
    vm.form.tags = ['keep']
    vm.form.location_id = 7
    await againButton(w).trigger('click')
    await flushPromises()
    expect(vm.form.name).toBe('Precious') // typing survives the failed save
    expect(vm.form.tags).toEqual(['keep'])
    expect(vm.form.location_id).toBe(7)
    expect(replaceSpy).not.toHaveBeenCalled()
    expect(toasts.some(t => t.message === 'boom' && t.type === 'error')).toBe(true)
  })

  it('"Save & add another" with an empty name does nothing but focus the field', async () => {
    let posts = 0
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      if (url.endsWith('/api/v1/items') && init?.method === 'POST') posts++
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    const router = newRouter()
    const replaceSpy = vi.spyOn(router, 'replace')
    const w = await mountCreate(router)
    const vm = w.vm as any
    vm.form.qty = 3 // would be reset to 1 if a save actually ran
    // focus (not document.activeElement): VTU mounts detached from the
    // document, so happy-dom treats focus() on it as a no-op.
    const focusSpy = vi.spyOn(w.find('input').element, 'focus')
    await againButton(w).trigger('click') // type="button": native required-validation never runs
    await flushPromises()
    expect(posts).toBe(0) // nothing to save: no POST
    expect(vm.form.qty).toBe(3) // …and no reset either
    expect(replaceSpy).not.toHaveBeenCalled()
    expect(toasts.some(t => t.message === 'Saved')).toBe(false)
    expect(toasts.some(t => t.type === 'error')).toBe(false) // silent, like native validation
    expect(focusSpy).toHaveBeenCalled() // name field is focused
  })

  it('replaces to bare /items/new when no location is set', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      if (url.endsWith('/api/v1/items') && init?.method === 'POST')
        return new Response(JSON.stringify({ id: 42 }), { status: 200 })
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    const router = newRouter()
    const replaceSpy = vi.spyOn(router, 'replace')
    const w = await mountCreate(router)
    ;(w.vm as any).form.name = 'Loose'
    await againButton(w).trigger('click')
    await flushPromises()
    expect(replaceSpy).toHaveBeenCalledWith('/items/new')
    expect(router.currentRoute.value.query.location).toBeUndefined()
  })

  it('disables both buttons while a save is in flight', async () => {
    let releasePost!: (r: Response) => void
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      if (url.endsWith('/api/v1/items') && init?.method === 'POST')
        return new Promise<Response>(res => { releasePost = res })
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    const w = await mountCreate(newRouter())
    ;(w.vm as any).form.name = 'Held'
    const saving = (w.vm as any).save(true)
    await flushPromises()
    expect(againButton(w).attributes('disabled')).toBeDefined()
    expect(w.find('button[type="submit"]').attributes('disabled')).toBeDefined()
    releasePost(new Response(JSON.stringify({ id: 42 }), { status: 200 }))
    await saving
    await flushPromises()
    expect(againButton(w).attributes('disabled')).toBeUndefined()
  })
})

describe('ItemFormView product_url', () => {
  const itemWithUrl = { id: 9, name: 'Widget', description: 'd', qty: 2, unit: 'pcs', location_id: 3,
    visibility: 'private', product_url: 'https://example.com/pi5', created_at: '', updated_at: '',
    tags: [], photos: [], documents: [], projects: [] }

  // Edit-mode mock: serves the item (+ the locations list), records PATCH bodies.
  function editFetch(itemJson: unknown, patches: any[]) {
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      if (url.match(/\/items\/9$/) && init?.method === 'PATCH') patches.push(JSON.parse(init.body))
      if (url.includes('/locations')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      return new Response(JSON.stringify(itemJson), { status: 200 })
    }))
  }

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

  it('renders the optional Product page URL input, prefilled from the item', async () => {
    editFetch(itemWithUrl, [])
    const w = mount(ItemFormView, { props: { id: '9' } })
    await flushPromises()
    expect(w.text()).toContain('Product page URL')
    const input = w.find('input[type="url"]')
    expect(input.exists()).toBe(true)
    expect((input.element as HTMLInputElement).value).toBe('https://example.com/pi5')
  })

  it('PATCH omits product_url when untouched, sends it when changed, sends "" when cleared', async () => {
    const patches: any[] = []
    editFetch(itemWithUrl, patches)
    const w = mount(ItemFormView, { props: { id: '9' } })
    await flushPromises()
    const vm = w.vm as any
    await vm.save()
    expect(patches.at(-1)).toEqual({}) // absent = keep (api-notes)
    vm.form.product_url = 'https://example.com/pi5-kit'
    await vm.save()
    expect(patches.at(-1)).toEqual({ product_url: 'https://example.com/pi5-kit' })
    vm.form.product_url = '' // present-empty = clear
    await vm.save()
    expect(patches.at(-1)).toEqual({ product_url: '' })
  })

  it('PATCH omits product_url for an item without one left untouched, sends it when set', async () => {
    const patches: any[] = []
    editFetch({ ...itemWithUrl, product_url: null }, patches)
    const w = mount(ItemFormView, { props: { id: '9' } })
    await flushPromises()
    const vm = w.vm as any
    expect(vm.form.product_url).toBe('')
    await vm.save()
    expect(patches.at(-1)).toEqual({}) // unchanged "": no key (does NOT clear)
    vm.form.product_url = 'https://example.com/new'
    await vm.save()
    expect(patches.at(-1)).toEqual({ product_url: 'https://example.com/new' })
  })

  it('create POST includes product_url only when non-empty', async () => {
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
    vm.form.name = 'With url'
    vm.form.product_url = 'https://example.com/x'
    await vm.save()
    expect(bodies.at(-1).product_url).toBe('https://example.com/x')

    // fresh mount, URL left empty: the key must be absent entirely
    const router2 = newRouter()
    await router2.push('/items/new')
    const w2 = mount(ItemFormView, { global: { plugins: [router2] } })
    await flushPromises()
    ;(w2.vm as any).form.name = 'No url'
    await (w2.vm as any).save()
    expect(bodies.at(-1)).toEqual({ name: 'No url', description: '', qty: 1, unit: '', visibility: 'private' })
  })

  describe('Archive button', () => {
    function archiveButton(w: { findAll: (s: string) => { text: () => string; trigger: (e: string) => Promise<void> }[] }) {
      return w.findAll('button').find(b => b.text() === 'Archive')
    }

    it('renders in edit mode with a non-empty URL, hides when the field is cleared', async () => {
      editFetch(itemWithUrl, [])
      const w = mount(ItemFormView, { props: { id: '9' } })
      await flushPromises()
      expect(archiveButton(w)).toBeTruthy()
      ;(w.vm as any).form.product_url = '   ' // whitespace-only counts as empty
      await flushPromises()
      expect(archiveButton(w)).toBeUndefined()
    })

    it('is absent in create mode even with a URL typed (no item id to fetch for)', async () => {
      vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })))
      const router = newRouter()
      await router.push('/items/new')
      const w = mount(ItemFormView, { global: { plugins: [router] } })
      await flushPromises()
      ;(w.vm as any).form.product_url = 'https://example.com/x'
      await flushPromises()
      expect(archiveButton(w)).toBeUndefined()
    })

    it('click POSTs /fetch with the exact body and toasts fire-and-forget success (no polling)', async () => {
      const fetchBodies: any[] = []
      vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
        if (url === '/api/v1/items/9/fetch' && init?.method === 'POST') {
          fetchBodies.push(JSON.parse(init.body))
          return new Response(JSON.stringify({ job_id: 'j1' }), { status: 202 })
        }
        if (url.includes('/locations')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
        if (url.includes('/jobs/')) throw new Error('the edit form must not poll the job') // canary
        return new Response(JSON.stringify(itemWithUrl), { status: 200 })
      }))
      const w = mount(ItemFormView, { props: { id: '9' } })
      await flushPromises()
      await archiveButton(w)!.trigger('click')
      await flushPromises()
      expect(fetchBodies).toEqual([{ url: 'https://example.com/pi5', mode: 'archive' }])
      expect(toasts.some(t => t.message === 'Archive started — it will appear in Documents shortly' && t.type === 'success')).toBe(true)
    })

    it('toasts the ApiError message when the fetch POST fails', async () => {
      vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
        if (url === '/api/v1/items/9/fetch' && init?.method === 'POST')
          return new Response(JSON.stringify({ error: { code: 'validation_error', message: 'url must be http(s)' } }), { status: 400 })
        if (url.includes('/locations')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
        return new Response(JSON.stringify(itemWithUrl), { status: 200 })
      }))
      const w = mount(ItemFormView, { props: { id: '9' } })
      await flushPromises()
      await archiveButton(w)!.trigger('click')
      await flushPromises()
      expect(toasts.some(t => t.message === 'url must be http(s)' && t.type === 'error')).toBe(true)
    })
  })
})

describe('ItemFormView AI autofill (add form)', () => {
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

  async function mountCreate() {
    const router = newRouter()
    await router.push('/items/new')
    const w = mount(ItemFormView, { global: { plugins: [router] } })
    await flushPromises()
    return w
  }

  // Queue n photos through the real pick flow (canvas stubbed AFTER mount,
  // like the upload tests above); returns the downscaled blobs in pick
  // order, each with DISTINCT content ("j0", "j1", …) so tests can pin
  // which blobs a FormData POST carries and in what order.
  async function queuePhotos(w: ReturnType<typeof mount>, n: number): Promise<Blob[]> {
    const blobs: Blob[] = []
    const ctx = { drawImage: vi.fn() }
    const canvas = { width: 0, height: 0, getContext: () => ctx,
      toBlob: (cb: (b: Blob) => void) => { const b = new Blob([`j${blobs.length}`], { type: 'image/jpeg' }); blobs.push(b); cb(b) } }
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue({ width: 4000, height: 2000, close() {} }))
    const realCreate = document.createElement.bind(document)
    vi.spyOn(document, 'createElement').mockImplementation((tag: string) =>
      tag === 'canvas' ? canvas as unknown as HTMLCanvasElement : realCreate(tag as keyof HTMLElementTagNameMap))
    const files = Array.from({ length: n }, (_, i) => {
      const f = new File(['x'], `big${i}.jpg`, { type: 'image/jpeg' })
      Object.defineProperty(f, 'size', { value: 9_000_000 }) // past the 2MB downscale gate
      return f
    })
    const input = w.find('input[accept="image/*"]')
    Object.defineProperty(input.element, 'files', { value: files })
    await input.trigger('change')
    await flushPromises()
    return blobs
  }

  function autofillButton(w: ReturnType<typeof mount>) {
    return w.findAll('button').find(b => b.text().includes('Autofill from photos'))
  }

  it('renders no autofill button until photos are queued', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })))
    const w = await mountCreate()
    expect(autofillButton(w)).toBeUndefined()
    expect(w.text()).not.toContain('Autofill from photos')
  })

  it('shows a count hint; click POSTs every queued blob as a "files" part', async () => {
    const calls: any[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      if (url === '/api/v1/autofill') { calls.push([url, init]); return new Response(JSON.stringify({}), { status: 200 }) }
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    const w = await mountCreate()
    const blobs = await queuePhotos(w, 2)
    expect(blobs).toHaveLength(2)
    expect(w.text()).toContain('uses 2 photos')
    await autofillButton(w)!.trigger('click')
    await flushPromises()
    expect(calls).toHaveLength(1)
    const [url, init] = calls[0]
    expect(url).toBe('/api/v1/autofill')
    expect(init.method).toBe('POST')
    const parts = (init.body as FormData).getAll('files')
    expect(parts).toHaveLength(2)
    // FormData.append(name, blob, filename) wraps blobs as Files — pin
    // identity by content: BOTH queued blobs, in pick order.
    expect(parts[0]).toBeInstanceOf(Blob)
    expect((parts[0] as Blob).type).toBe('image/jpeg')
    expect(await (parts[0] as Blob).text()).toBe('j0')
    expect(await (parts[1] as Blob).text()).toBe('j1')
    // every key absent: nothing applied, no ✨ markers, no error toast
    expect(w.findAll('[data-ai-hint]')).toHaveLength(0)
    expect(toasts.some(t => t.type === 'error')).toBe(false)
  })

  it('fills returned fields with ✨ markers, appends tags deduped; 6 photos post only the first 4', async () => {
    const bodies: FormData[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      if (url === '/api/v1/autofill') {
        bodies.push(init.body)
        return new Response(JSON.stringify({ name: 'Resistor pack', description: 'blue ones', qty: 12, unit: 'pcs', tags: ['electronics', 'existing'] }), { status: 200 })
      }
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    const w = await mountCreate()
    const blobs = await queuePhotos(w, 6)
    expect(blobs).toHaveLength(6)
    ;(w.vm as any).form.tags = ['existing'] // the user's own chip: must survive
    expect(w.text()).toContain('first 4')
    await autofillButton(w)!.trigger('click')
    await flushPromises()
    expect(bodies[0].getAll('files')).toHaveLength(4)
    expect(await (bodies[0].getAll('files')[3] as Blob).text()).toBe('j3') // …and they are the FIRST four
    const vm = w.vm as any
    expect(vm.form.name).toBe('Resistor pack')
    expect(vm.form.description).toBe('blue ones')
    expect(vm.form.qty).toBe(12)
    expect(vm.form.unit).toBe('pcs')
    expect(vm.form.tags).toEqual(['existing', 'electronics']) // appended after the user's chip, deduped
    expect(w.findAll('[data-ai-hint]')).toHaveLength(5) // name description qty unit tags
    expect(toasts.some(t => t.message === 'Filled: name, description, qty, unit, tags' && t.type === 'success')).toBe(true)
    // a manual edit clears just that field's marker
    await w.find('input').setValue('My own name') // first input in the form = Name
    expect(w.findAll('[data-ai-hint]')).toHaveLength(4)
  })

  it('ai_not_configured toasts the server config hint and leaves every field untouched', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url === '/api/v1/autofill')
        return new Response(JSON.stringify({ error: { code: 'ai_not_configured', message: 'set ai_api_key and ai_model in config to enable autofill' } }), { status: 400 })
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    const w = await mountCreate()
    await queuePhotos(w, 1)
    await autofillButton(w)!.trigger('click')
    await flushPromises()
    expect(toasts.some(t => t.message === 'set ai_api_key and ai_model in config to enable autofill' && t.type === 'error')).toBe(true)
    expect(toasts.some(t => t.message.startsWith('Filled'))).toBe(false)
    const vm = w.vm as any
    expect(vm.form.name).toBe('')
    expect(vm.form.qty).toBe(1)
    expect(vm.form.unit).toBe('')
    expect(vm.form.tags).toEqual([])
    expect(w.findAll('[data-ai-hint]')).toHaveLength(0)
  })

  it('while analyzing: button disabled + "Analyzing…", form untouched, never POSTs /items', async () => {
    let release!: (r: Response) => void
    const itemPosts: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      if (url === '/api/v1/autofill') return new Promise<Response>(res => { release = res })
      if (url.endsWith('/api/v1/items') && init?.method === 'POST') { itemPosts.push(url); return new Response(JSON.stringify({ id: 42 }), { status: 200 }) }
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    const w = await mountCreate()
    await queuePhotos(w, 1)
    const btn = autofillButton(w)!
    await btn.trigger('click')
    await flushPromises()
    expect(btn.attributes('disabled')).toBeDefined()
    expect(btn.text()).toContain('Analyzing…')
    expect(btn.find('.animate-spin').exists()).toBe(true) // spinner + text (spec §5)
    expect((w.vm as any).form.name).toBe('') // no form state changes while analyzing
    expect(itemPosts).toEqual([]) // autofill alone never saves
    release(new Response(JSON.stringify({ name: 'Gadget', qty: 3 }), { status: 200 }))
    await flushPromises()
    expect(btn.attributes('disabled')).toBeUndefined()
    expect(btn.text()).toContain('Autofill from photos')
    expect(btn.find('.animate-spin').exists()).toBe(false) // spinner leaves with the state
    expect((w.vm as any).form.name).toBe('Gadget')
    expect(itemPosts).toEqual([]) // applying fields never saves either
  })

  // Carried from Task 4's review: the TagInput ✨-marker clear wiring
  // (@update:tags → aiTouched.delete('tags')) was verified live but unpinned.
  it('clears the tags ✨ marker on a manual chip edit — both add and remove', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url === '/api/v1/autofill') return new Response(JSON.stringify({ tags: ['ai-tag'] }), { status: 200 })
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    const w = await mountCreate()
    await queuePhotos(w, 1)
    await autofillButton(w)!.trigger('click')
    await flushPromises()
    const vm = w.vm as any
    expect(vm.form.tags).toEqual(['ai-tag'])
    expect(w.findAll('[data-ai-hint]')).toHaveLength(1) // Tags only — nothing else came back

    // manual ADD (Enter commit inside TagInput) clears the marker
    const input = w.find('input[aria-label="Add tag"]')
    await input.setValue('mine')
    await input.trigger('keydown', { key: 'Enter' })
    expect(vm.form.tags).toEqual(['ai-tag', 'mine'])
    expect(w.findAll('[data-ai-hint]')).toHaveLength(0)

    // a second autofill re-marks Tags (dedup keeps both chips)…
    await autofillButton(w)!.trigger('click')
    await flushPromises()
    expect(vm.form.tags).toEqual(['ai-tag', 'mine'])
    expect(w.findAll('[data-ai-hint]')).toHaveLength(1)

    // …and a manual REMOVE (chip ✕) clears it again
    await w.find('button[aria-label="Remove tag ai-tag"]').trigger('click')
    expect(vm.form.tags).toEqual(['mine'])
    expect(w.findAll('[data-ai-hint]')).toHaveLength(0)
  })
})

describe('ItemFormView AI autofill (edit form)', () => {
  const photo = (id: number, key: string, thumb: string | null) =>
    ({ id, storage_key: key, thumb_key: thumb, caption: '', sort_order: id, created_at: '' })
  const itemWithPhotos = (photos: unknown[]) => ({ id: 9, name: 'Widget', description: 'd', qty: 2, unit: 'pcs',
    location_id: 3, visibility: 'private', created_at: '', updated_at: '', tags: [],
    photos, documents: [], projects: [] })

  function mountEdit(photos: unknown[]) {
    vi.stubGlobal('fetch', vi.fn(async (url: string) =>
      url.includes('/locations')
        ? new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
        : new Response(JSON.stringify(itemWithPhotos(photos)), { status: 200 })))
    return mount(ItemFormView, { props: { id: '9' } })
  }

  const pickerToggle = (w: ReturnType<typeof mount>) =>
    w.findAll('button').find(b => b.text().includes('Autofill from photos'))

  it('opens the picker on click: thumbs are thumb_key ?? storage_key, clicks toggle, badge counts, Analyze disabled at 0', async () => {
    const w = mountEdit([
      photo(71, '2026/10/a.jpg', '2026/10/a_t.webp'),
      photo(72, '2026/10/b.jpg', null), // no thumb: falls back to the original
      photo(73, '2026/10/c.jpg', '2026/10/c_t.webp'),
    ])
    await flushPromises()
    expect(w.find('[data-ai-picker]').exists()).toBe(false) // closed until opened
    await pickerToggle(w)!.trigger('click')
    const picker = w.find('[data-ai-picker]')
    expect(picker.exists()).toBe(true)
    const thumbs = picker.findAll('[data-ai-thumb]')
    expect(thumbs).toHaveLength(3)
    expect(picker.findAll('img').map(i => i.attributes('src'))).toEqual([
      '/files/2026/10/a_t.webp', '/files/2026/10/b.jpg', '/files/2026/10/c_t.webp',
    ])
    // nothing selected: Analyze disabled, badge at 0
    expect(w.find('[data-ai-analyze]').attributes('disabled')).toBeDefined()
    expect(w.find('[data-ai-count]').text()).toBe('0/4 selected')
    // clicking a thumb selects it (aria-pressed) and the badge follows
    await thumbs[0].trigger('click')
    expect(thumbs[0].attributes('aria-pressed')).toBe('true')
    expect(w.find('[data-ai-count]').text()).toBe('1/4 selected')
    expect(w.find('[data-ai-analyze]').attributes('disabled')).toBeUndefined()
    await thumbs[1].trigger('click')
    expect(w.find('[data-ai-count]').text()).toBe('2/4 selected')
    // clicking again DEselects
    await thumbs[0].trigger('click')
    expect(thumbs[0].attributes('aria-pressed')).toBe('false')
    expect(w.find('[data-ai-count]').text()).toBe('1/4 selected')
  })

  it('caps selection at 4: further clicks are a no-op (endpoint limit)', async () => {
    const w = mountEdit([1, 2, 3, 4, 5].map(i => photo(70 + i, `2026/10/p${i}.jpg`, null)))
    await flushPromises()
    await pickerToggle(w)!.trigger('click')
    const thumbs = w.findAll('[data-ai-thumb]')
    for (const t of thumbs) await t.trigger('click')
    expect(w.find('[data-ai-count]').text()).toBe('4/4 selected')
    expect(thumbs[4].attributes('aria-pressed')).toBe('false') // 5th never joined
  })

  it('Analyze fetches each selected ORIGINAL from /files and posts the blobs; spinner + Analyzing… while in flight', async () => {
    const gets: string[] = []
    const autofillCalls: any[] = []
    let release!: (r: Response) => void
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      if (url === '/files/2026/10/k1.jpg') { gets.push(url); return new Response(new Blob(['orig1'], { type: 'image/jpeg' }), { status: 200 }) }
      if (url === '/files/2026/10/k2.jpg') { gets.push(url); return new Response(new Blob(['orig2'], { type: 'image/jpeg' }), { status: 200 }) }
      if (url === '/api/v1/autofill') { autofillCalls.push(init); return new Promise<Response>(res => { release = res }) }
      if (url.includes('/locations')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      return new Response(JSON.stringify(itemWithPhotos([photo(71, '2026/10/k1.jpg', null), photo(72, '2026/10/k2.jpg', '2026/10/k2_t.webp')])), { status: 200 })
    }))
    const w = mount(ItemFormView, { props: { id: '9' } })
    await flushPromises()
    await pickerToggle(w)!.trigger('click')
    const thumbs = w.findAll('[data-ai-thumb]')
    await thumbs[0].trigger('click') // k1
    await thumbs[1].trigger('click') // k2
    await w.find('[data-ai-analyze]').trigger('click')
    await flushPromises()
    // both ORIGINALS fetched same-origin (thumb_key ignored for analysis)
    expect(gets.sort()).toEqual(['/files/2026/10/k1.jpg', '/files/2026/10/k2.jpg'])
    // spinner + Analyzing… on the panel's button while held (spec §5)
    const btn = w.find('[data-ai-analyze]')
    expect(btn.attributes('disabled')).toBeDefined()
    expect(btn.text()).toContain('Analyzing…')
    expect(btn.find('.animate-spin').exists()).toBe(true)
    release(new Response(JSON.stringify({ name: 'Resistor pack', tags: ['electronics'] }), { status: 200 }))
    await flushPromises()
    expect(autofillCalls).toHaveLength(1)
    const parts = autofillCalls[0].body.getAll('files') as Blob[]
    expect(parts).toHaveLength(2)
    expect(await parts[0].text()).toBe('orig1') // photo order
    expect(await parts[1].text()).toBe('orig2')
    const vm = w.vm as any
    expect(vm.form.name).toBe('Resistor pack')
    expect(vm.form.tags).toEqual(['electronics'])
    expect(toasts.some(t => t.message === 'Filled: name, tags' && t.type === 'success')).toBe(true)
  })

  it('a failed original fetch toasts which photo failed and posts NOTHING to autofill', async () => {
    let autofillPosts = 0
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url === '/files/2026/10/ok.jpg') return new Response(new Blob(['orig1'], { type: 'image/jpeg' }), { status: 200 })
      if (url === '/files/2026/10/gone.jpg') return new Response(JSON.stringify({ error: { code: 'not_found', message: 'nope' } }), { status: 404 })
      if (url === '/api/v1/autofill') { autofillPosts++; return new Response(JSON.stringify({}), { status: 200 }) }
      if (url.includes('/locations')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      return new Response(JSON.stringify(itemWithPhotos([photo(71, '2026/10/ok.jpg', null), photo(72, '2026/10/gone.jpg', null)])), { status: 200 })
    }))
    const w = mount(ItemFormView, { props: { id: '9' } })
    await flushPromises()
    await pickerToggle(w)!.trigger('click')
    const thumbs = w.findAll('[data-ai-thumb]')
    await thumbs[0].trigger('click')
    await thumbs[1].trigger('click') // this one's original 404s
    await w.find('[data-ai-analyze]').trigger('click')
    await flushPromises()
    expect(autofillPosts).toBe(0) // failure aborts before the autofill POST
    const t = toasts.find(x => x.type === 'error')
    expect(t?.message).toContain('#72') // names the failing photo…
    expect(t?.message).toContain('404') // …and why
    expect((w.vm as any).form.name).toBe('Widget') // form untouched
  })

  it('add mode: no picker anywhere — the button still analyzes the queued photos directly', async () => {
    const posts: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url === '/api/v1/autofill') { posts.push(url); return new Response(JSON.stringify({}), { status: 200 }) }
      return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
    }))
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/items', component: { template: '<div>items</div>' } },
        { path: '/items/new', component: ItemFormView },
        { path: '/items/:id', component: { template: '<div>detail</div>' } },
      ],
    })
    await router.push('/items/new')
    const w = mount(ItemFormView, { global: { plugins: [router] } })
    await flushPromises()
    // queue one photo through the real pick flow (canvas stubbed after mount)
    const ctx = { drawImage: vi.fn() }
    const canvas = { width: 0, height: 0, getContext: () => ctx,
      toBlob: (cb: (b: Blob) => void) => cb(new Blob(['j0'], { type: 'image/jpeg' })) }
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue({ width: 4000, height: 2000, close() {} }))
    const realCreate = document.createElement.bind(document)
    vi.spyOn(document, 'createElement').mockImplementation((tag: string) =>
      tag === 'canvas' ? canvas as unknown as HTMLCanvasElement : realCreate(tag as keyof HTMLElementTagNameMap))
    const f = new File(['x'], 'big.jpg', { type: 'image/jpeg' })
    Object.defineProperty(f, 'size', { value: 9_000_000 })
    const input = w.find('input[accept="image/*"]')
    Object.defineProperty(input.element, 'files', { value: [f] })
    await input.trigger('change')
    await flushPromises()
    // the button acts directly on the queue…
    await w.findAll('button').find(b => b.text().includes('Autofill from photos'))!.trigger('click')
    await flushPromises()
    expect(posts).toEqual(['/api/v1/autofill'])
    // …and never opens a picker panel
    expect(w.find('[data-ai-picker]').exists()).toBe(false)
    expect(w.find('[data-ai-analyze]').exists()).toBe(false)
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
    const input = w.find('input[aria-label="Add tag"]') // stable across placeholder copy changes
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
