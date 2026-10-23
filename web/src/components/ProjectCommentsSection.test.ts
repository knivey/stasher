import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ProjectCommentsSection from './ProjectCommentsSection.vue'

beforeEach(() => { vi.unstubAllGlobals() })

describe('ProjectCommentsSection', () => {
  it('shows delete only on own comments and only posts when canPost', async () => {
    const w = mount(ProjectCommentsSection, {
      props: { projectId: 1, comments: [
        { id: 1, author: 'Alice', mine: false, body: 'nice', created_at: '' },
        { id: 2, author: 'You', mine: true, body: 'thanks', created_at: '' },
      ], canPost: false },
    })
    expect(w.findAll('[data-comment-delete]')).toHaveLength(1) // only mine
    expect(w.find('[data-comment-body]').exists()).toBe(false) // no post box
  })

  it('posts a comment and emits refresh when canPost', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method} ${url}`)
      return new Response(JSON.stringify({ id: 3, author: 'You', mine: true, body: 'hi', created_at: '' }), { status: 201 })
    }))
    const w = mount(ProjectCommentsSection, {
      props: { projectId: 1, comments: [], canPost: true },
    })
    await w.find('[data-comment-body]').setValue('hi')
    await w.find('[data-comment-add]').trigger('submit')
    await flushPromises()
    expect(calls).toContain('POST /api/v1/projects/1/comments')
    expect(w.emitted('refresh')).toBeTruthy()
  })
})
