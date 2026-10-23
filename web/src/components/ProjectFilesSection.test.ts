import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ProjectFilesSection from './ProjectFilesSection.vue'
import type { ProjectFile } from '../api/types'
import { toasts } from '../utils/toast'

beforeEach(() => { vi.unstubAllGlobals(); toasts.splice(0) })

// A 201 upload response echoing the picked filename.
function uploadOk(f: File): Response {
  return new Response(JSON.stringify(
    { id: 3, storage_key: 'k3', thumb_key: null, filename: f.name, mime_type: 'application/pdf', size_bytes: 10, created_at: '' }),
    { status: 201 })
}

describe('ProjectFilesSection', () => {
  it('renders image thumbs and file rows; owner can upload/delete', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${url}`)
      if (init?.method === 'POST') {
        return new Response(JSON.stringify(
          { id: 3, storage_key: 'k3', thumb_key: null, filename: 'manual.pdf', mime_type: 'application/pdf', size_bytes: 10, created_at: '' }),
          { status: 201 })
      }
      return new Response(null, { status: 204 })
    }))
    const files: ProjectFile[] = [
      { id: 1, storage_key: 'k1', thumb_key: 't1', filename: 'diagram.png', mime_type: 'image/png', size_bytes: 4096, created_at: '' },
      { id: 2, storage_key: 'k2', thumb_key: null, filename: 'spec.pdf', mime_type: 'application/pdf', size_bytes: 1048576, created_at: '' },
    ]
    const w = mount(ProjectFilesSection, { props: { projectId: 1, owner: true, files } })
    const img = w.find('img[data-file-thumb]')
    expect(img.exists()).toBe(true)
    expect(img.attributes('src')).toBe('/files/t1') // thumb ?? storage_key
    expect(w.find('a[href="/files/k1"]').exists()).toBe(true) // click-through = ORIGINAL key
    expect(w.text()).toContain('spec.pdf')
    expect(w.text()).toContain('1.0 MB') // humanized — matches ItemDetailView's fmtDocSize convention

    // Owner upload: hidden input + Add file button (existing upload-test pattern).
    const input = w.find('[data-file-upload]')
    expect(input.exists()).toBe(true)
    Object.defineProperty(input.element, 'files', { value: [new File(['x'], 'manual.pdf', { type: 'application/pdf' })] })
    await input.trigger('change')
    await flushPromises()
    expect(calls).toContain('POST /api/v1/projects/1/files')
    expect(w.emitted('refresh')).toBeTruthy()

    // Inline delete, no dialog (matches ItemDetailView's document delete).
    await w.findAll('[data-file-delete]')[0].trigger('click')
    await flushPromises()
    expect(calls).toContain('DELETE /api/v1/projects/1/files/1')
  })

  it('viewer sees no upload/delete controls', () => {
    const w = mount(ProjectFilesSection, {
      props: {
        projectId: 1, owner: false,
        files: [{ id: 2, storage_key: 'k2', thumb_key: null, filename: 'spec.pdf', mime_type: 'application/pdf', size_bytes: 10, created_at: '' }],
      },
    })
    expect(w.find('[data-file-upload]').exists()).toBe(false)
    expect(w.find('[data-file-delete]').exists()).toBe(false)
    expect(w.find('a[href="/files/k2"]').exists()).toBe(true) // download still available
  })

  it('a two-file pick POSTs each file but emits exactly ONE refresh', async () => {
    const posted: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init?: RequestInit) => {
      if (init?.method === 'POST') {
        const f = (init.body as FormData).get('file') as File
        posted.push(f.name)
        return uploadOk(f)
      }
      return new Response(null, { status: 204 })
    }))
    const w = mount(ProjectFilesSection, { props: { projectId: 1, owner: true, files: [] } })
    const input = w.find('[data-file-upload]')
    Object.defineProperty(input.element, 'files', { value: [
      new File(['x'], 'a.pdf', { type: 'application/pdf' }),
      new File(['y'], 'b.pdf', { type: 'application/pdf' }),
    ] })
    await input.trigger('change')
    await flushPromises()
    expect(posted).toEqual(['a.pdf', 'b.pdf'])
    expect(w.emitted('refresh')).toHaveLength(1)
  })

  it('a failing file toasts with its filename and does not abort the rest of the batch', async () => {
    const posts: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init?: RequestInit) => {
      if (init?.method === 'POST') {
        const f = (init.body as FormData).get('file') as File
        posts.push(f.name)
        if (f.name === 'bad.pdf')
          return new Response(JSON.stringify({ error: { code: 'file_too_large', message: 'file too large' } }), { status: 413 })
        return uploadOk(f)
      }
      return new Response(null, { status: 204 })
    }))
    const w = mount(ProjectFilesSection, { props: { projectId: 1, owner: true, files: [] } })
    const input = w.find('[data-file-upload]')
    Object.defineProperty(input.element, 'files', { value: [
      new File(['x'], 'bad.pdf', { type: 'application/pdf' }),
      new File(['y'], 'good.pdf', { type: 'application/pdf' }),
    ] })
    await input.trigger('change')
    await flushPromises()
    expect(posts).toEqual(['bad.pdf', 'good.pdf']) // failure didn't abort the rest
    expect(toasts.some(t => t.type === 'error' && t.message === 'bad.pdf: file too large')).toBe(true)
    expect(w.emitted('refresh')).toHaveLength(1) // good.pdf landed → one batch refresh
  })
})
