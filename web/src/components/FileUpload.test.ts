import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import FileUpload from './FileUpload.vue'

function bigPhoto() {
  const f = new File(['x'], 'big.jpg', { type: 'image/jpeg' })
  Object.defineProperty(f, 'size', { value: 9_000_000 })
  return f
}

// Stub only canvas creation; delegate other tags to the real creator so a
// mounted component can still patch its DOM while the spy is active.
function stubCanvas(w: number, h: number) {
  const ctx = { drawImage: vi.fn() }
  const canvas = { width: 0, height: 0, getContext: () => ctx,
    toBlob: (cb: (b: Blob) => void) => cb(new Blob(['j'], { type: 'image/jpeg' })) }
  vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue({ width: w, height: h, close() {} }))
  const realCreate = document.createElement.bind(document)
  vi.spyOn(document, 'createElement').mockImplementation((tag: string) =>
    tag === 'canvas' ? canvas as unknown as HTMLCanvasElement : realCreate(tag as keyof HTMLElementTagNameMap))
}

async function pick(w: ReturnType<typeof mount>, file: File) {
  const input = w.find('input[type="file"]')
  Object.defineProperty(input.element, 'files', { value: [file] })
  await input.trigger('change')
  await flushPromises()
}

beforeEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('FileUpload', () => {
  it('uploads a downscaled jpeg for a >2MB photo and emits uploaded', async () => {
    const uploads: Array<{ url: string; file: File }> = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      const fd = init?.body as FormData
      uploads.push({ url, file: fd.get('file') as File })
      return new Response(null, { status: 204 })
    }))
    const w = mount(FileUpload, { props: { kind: 'photo', itemId: 9 } })
    const input = w.find('input[type="file"]')
    expect(input.attributes('accept')).toBe('image/*')
    expect(input.attributes('capture')).toBe('environment')
    stubCanvas(4000, 2000) // after mount: the createElement spy must not catch Vue's own elements
    await pick(w, bigPhoto())
    expect(uploads).toHaveLength(1)
    expect(uploads[0].url).toBe('/api/v1/items/9/photos')
    expect(uploads[0].file.type).toBe('image/jpeg') // the downscaled canvas blob, not the original
    // happy-dom's FormData drops the explicit filename for plain Blobs (browsers
    // keep 'photo.jpg'), so the filename contract is asserted in the doc test below.
    expect(w.emitted('uploaded')).toHaveLength(1)
    expect(w.find('button').attributes('disabled')).toBeUndefined() // busy state cleared
  })

  it('sends documents untouched with no accept/capture and keeps the filename', async () => {
    const uploads: Array<{ url: string; field: string; file: File }> = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      const fd = init?.body as FormData
      uploads.push({ url, field: [...fd.keys()][0], file: fd.get('file') as File })
      return new Response(null, { status: 204 })
    }))
    const w = mount(FileUpload, { props: { kind: 'doc', itemId: 9 } })
    const input = w.find('input[type="file"]')
    expect(input.attributes('accept')).toBeUndefined()
    expect(input.attributes('capture')).toBeUndefined()
    const doc = new File(['%PDF-1.4'], 'datasheet.pdf', { type: 'application/pdf' })
    await pick(w, doc)
    expect(w.text()).toContain('Add document')
    expect(uploads).toHaveLength(1)
    expect(uploads[0].url).toBe('/api/v1/items/9/documents')
    expect(uploads[0].field).toBe('file')
    expect(uploads[0].file.name).toBe('datasheet.pdf')
    expect(uploads[0].file.type).toBe('application/pdf')
    expect(w.emitted('uploaded')).toHaveLength(1)
  })

  it('toasts on upload failure', async () => {
    vi.stubGlobal('fetch', vi.fn(async () =>
      new Response(JSON.stringify({ error: { code: 'too_large', message: 'too large' } }), { status: 413 })))
    const w = mount(FileUpload, { props: { kind: 'doc', itemId: 9 } })
    await pick(w, new File(['x'], 'a.txt', { type: 'text/plain' }))
    expect(w.emitted('uploaded')).toBeUndefined()
    expect(w.find('button').attributes('disabled')).toBeUndefined()
  })
})
