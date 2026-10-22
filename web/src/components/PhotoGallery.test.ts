import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import PhotoGallery from './PhotoGallery.vue'
import type { Photo } from '../api/types'

const photos: Photo[] = [
  { id: 11, storage_key: '2026/10/a.jpg', thumb_key: null, caption: 'first', sort_order: 0, created_at: '2026-01-01T00:00:00Z' },
  { id: 12, storage_key: '2026/10/b.jpg', thumb_key: null, caption: 'second', sort_order: 1, created_at: '2026-01-01T00:00:00Z' },
  { id: 13, storage_key: '2026/10/c.jpg', thumb_key: null, caption: 'third', sort_order: 2, created_at: '2026-01-01T00:00:00Z' },
]

describe('PhotoGallery', () => {
  it('badges the first photo as main and offers make-main on the others when canEdit', () => {
    const w = mount(PhotoGallery, { props: { photos, canEdit: true } })
    const badge = w.find('[aria-label="Main photo"]')
    expect(badge.exists()).toBe(true)
    expect(badge.text()).toBe('★')
    // only the non-first photos carry the control
    const makeMain = w.findAll('button[aria-label="Make main"]')
    expect(makeMain).toHaveLength(2)
  })

  it('clicking make-main emits main with that photo', async () => {
    const w = mount(PhotoGallery, { props: { photos, canEdit: true } })
    await w.findAll('button[aria-label="Make main"]')[0].trigger('click')
    expect(w.emitted('main')).toEqual([[photos[1]]])
  })

  it('canEdit=false renders no make-main controls (badge still marks the main photo)', () => {
    const w = mount(PhotoGallery, { props: { photos, canEdit: false } })
    expect(w.findAll('button[aria-label="Make main"]')).toHaveLength(0)
    expect(w.findAll('button')).toHaveLength(0) // no delete buttons either
    expect(w.find('[aria-label="Main photo"]').exists()).toBe(true)
  })
})
