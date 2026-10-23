import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ProjectBookmarksSection from './ProjectBookmarksSection.vue'
import type { ProjectBookmark } from '../api/types'
import { toasts } from '../utils/toast'

beforeEach(() => { vi.unstubAllGlobals(); toasts.splice(0) })

const bm = (over: Partial<ProjectBookmark> = {}): ProjectBookmark => ({
  id: 1, url: 'https://example.com/build', title: 'Build log', created_at: '', archived_file: null, ...over,
})

function cancelButton(w: ReturnType<typeof mount>) {
  // ConfirmDialog renders no data-test anchors — its cancel is the plain
  // "Cancel" button (see ConfirmDialog.test.ts / its template).
  const b = w.findAll('button').find(x => x.text() === 'Cancel')
  expect(b, 'dialog cancel button').toBeDefined()
  return b!
}

describe('ProjectBookmarksSection', () => {
  it('adds a bookmark and emits refresh', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${url}`)
      return new Response(JSON.stringify(
        { id: 2, url: 'https://example.com/x', title: 'x', created_at: '', archived_file: null }),
        { status: 201 })
    }))
    const w = mount(ProjectBookmarksSection, { props: { projectId: 1, owner: true, bookmarks: [] } })
    await w.find('[data-bm-url]').setValue('https://example.com/x')
    await w.find('[data-bm-add]').trigger('submit')
    await flushPromises()
    expect(calls).toContain('POST /api/v1/projects/1/bookmarks')
    expect(w.emitted('refresh')).toBeTruthy()
  })

  it('archives after job polling and emits refresh', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${url}`)
      if (url.includes('/archive')) return new Response(JSON.stringify({ job_id: 'j1' }), { status: 202 })
      if (url.includes('/jobs/j1')) return new Response(JSON.stringify({ status: 'done' }), { status: 200 })
      return new Response(null, { status: 204 })
    }))
    const w = mount(ProjectBookmarksSection, { props: { projectId: 1, owner: true, bookmarks: [bm()] } })
    await w.find('[data-archive]').trigger('click')
    await flushPromises()
    expect(calls).toContain('POST /api/v1/projects/1/bookmarks/1/archive')
    expect(calls).toContain('GET /api/v1/jobs/j1')
    expect(w.emitted('refresh')).toBeTruthy()
  })

  it('requires confirmation for video hosts — decline sends nothing', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${url}`)
      return new Response(null, { status: 204 })
    }))
    const w = mount(ProjectBookmarksSection, {
      props: { projectId: 1, owner: true, bookmarks: [bm({ url: 'https://www.youtube.com/watch?v=x' })] },
    })
    await w.find('[data-archive]').trigger('click')
    await flushPromises()
    expect(w.find('[data-video-confirm]').exists()).toBe(true)
    await cancelButton(w).trigger('click')
    await flushPromises()
    expect(w.find('[data-video-confirm]').exists()).toBe(false)
    expect(calls.filter(c => c.includes('/archive'))).toHaveLength(0)
    expect(calls).toHaveLength(0) // decline = no POST, no job poll
  })

  it('archives a video-host bookmark after confirming', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${url}`)
      if (url.includes('/archive')) return new Response(JSON.stringify({ job_id: 'j2' }), { status: 202 })
      if (url.includes('/jobs/j2')) return new Response(JSON.stringify({ status: 'done' }), { status: 200 })
      return new Response(null, { status: 204 })
    }))
    const w = mount(ProjectBookmarksSection, {
      props: { projectId: 1, owner: true, bookmarks: [bm({ url: 'https://youtu.be/x' })] },
    })
    await w.find('[data-archive]').trigger('click')
    await flushPromises()
    expect(w.find('[data-video-confirm]').exists()).toBe(true)
    const yes = w.findAll('button').find(x => x.text() === 'Archive anyway')
    expect(yes, 'confirm button labelled by confirm-label').toBeDefined()
    await yes!.trigger('click')
    await flushPromises()
    expect(calls).toContain('POST /api/v1/projects/1/bookmarks/1/archive')
    expect(calls).toContain('GET /api/v1/jobs/j2')
    expect(w.emitted('refresh')).toBeTruthy()
  })

  it('toasts the job error and does not refresh when the archive job fails', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${url}`)
      if (url.includes('/archive')) return new Response(JSON.stringify({ job_id: 'j3' }), { status: 202 })
      if (url.includes('/jobs/j3')) return new Response(JSON.stringify({ status: 'failed', error: 'chromium missing' }), { status: 200 })
      return new Response(null, { status: 204 })
    }))
    const w = mount(ProjectBookmarksSection, { props: { projectId: 1, owner: true, bookmarks: [bm()] } })
    await w.find('[data-archive]').trigger('click')
    await flushPromises()
    expect(calls).toContain('GET /api/v1/jobs/j3')
    expect(toasts.some(t => t.message === 'chromium missing' && t.type === 'error')).toBe(true) // job's error surfaced
    expect(w.emitted('refresh')).toBeUndefined() // nothing changed server-side
    expect(w.find('[data-archive]').attributes('disabled')).toBeUndefined() // row back to Archive state
  })

  it('links archived PDFs and hides owner controls from viewers', () => {
    const archived = bm({
      archived_file: { id: 9, storage_key: 'pdf1', thumb_key: null, filename: 'Build log.pdf', mime_type: 'application/pdf', size_bytes: 5, created_at: '' },
    })
    const viewer = mount(ProjectBookmarksSection, { props: { projectId: 1, owner: false, bookmarks: [archived] } })
    expect(viewer.find('a[href="/files/pdf1"]').exists()).toBe(true) // View PDF
    expect(viewer.find('[data-archive]').exists()).toBe(false)
    expect(viewer.find('[data-bm-add]').exists()).toBe(false)
    // Row still shows the bookmark link + hostname to sharees.
    expect(viewer.find('a[href="https://example.com/build"]').exists()).toBe(true)
    expect(viewer.text()).toContain('example.com')
  })
})
