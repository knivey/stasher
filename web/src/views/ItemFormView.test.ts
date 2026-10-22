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
