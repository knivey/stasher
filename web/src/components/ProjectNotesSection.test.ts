import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ProjectNotesSection from './ProjectNotesSection.vue'

// Newest first, as the server delivers the notes log.
const notes = [
  { id: 2, body: 'second', created_at: '2026-10-23T02:00:00Z' },
  { id: 1, body: 'first', created_at: '2026-10-23T01:00:00Z' },
]

beforeEach(() => { vi.unstubAllGlobals() })

describe('ProjectNotesSection', () => {
  it('adds a note and emits refresh', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method} ${url}`)
      return new Response(JSON.stringify({ id: 3, body: 'third', created_at: '' }), { status: 201 })
    }))
    const w = mount(ProjectNotesSection, { props: { projectId: 1, notes } })
    await w.find('[data-note-body]').setValue('third')
    await w.find('[data-note-add]').trigger('submit')
    await flushPromises()
    expect(calls).toContain('POST /api/v1/projects/1/notes')
    expect(w.emitted('refresh')).toBeTruthy()
  })

  it('empty body is a no-op; delete posts to the right id', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method} ${url}`)
      return new Response(null, { status: 204 })
    }))
    const w = mount(ProjectNotesSection, { props: { projectId: 1, notes } })
    await w.find('[data-note-add]').trigger('submit')
    expect(calls).toHaveLength(0)
    await w.findAll('[data-note-delete]')[0].trigger('click')
    await flushPromises()
    expect(calls).toContain('DELETE /api/v1/projects/1/notes/2')
    expect(w.emitted('refresh')).toBeTruthy()
  })
})
