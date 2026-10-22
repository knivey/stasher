import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import TagInput from './TagInput.vue'
import { toasts } from '../utils/toast'

// Sorted assertion data: screws (10 uses) must rank above screwdrivers (4).
const TAG_RESPONSE = () => new Response(JSON.stringify({
  data: [
    { id: 1, name: 'washers', item_count: 2 },
    { id: 2, name: 'screws', item_count: 10 },
    { id: 3, name: 'screwdrivers', item_count: 4 },
    { id: 4, name: 'nails', item_count: 7 },
  ],
  total: 4,
}), { status: 200, headers: { 'Content-Type': 'application/json' } })

async function mountFocused(propsTags: string[] = []) {
  const fetchMock = vi.fn(async () => TAG_RESPONSE())
  vi.stubGlobal('fetch', fetchMock)
  const w = mount(TagInput, { props: { tags: propsTags } })
  const input = w.find('input')
  await input.trigger('focus') // lazy fetch fires here
  await flushPromises()
  return { w, input }
}

beforeEach(() => { vi.unstubAllGlobals(); toasts.splice(0, toasts.length) })

describe('TagInput', () => {
  it('placeholder advertises the Enter-to-commit affordance', () => {
    const w = mount(TagInput, { props: { tags: [] } })
    expect(w.find('input').attributes('placeholder')).toBe('Add tag… (Enter)')
  })

  it('shows the commit hint while text is uncommitted, hides it after Enter commits', async () => {
    const w = mount(TagInput, { props: { tags: [] } })
    expect(w.text()).not.toContain('Press Enter')
    const input = w.find('input')
    await input.setValue('blue')
    const hint = w.find('p')
    expect(hint.exists()).toBe(true)
    expect(hint.text()).toContain('Press Enter (or comma) to add “blue”')
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('update:tags')).toEqual([[['blue']]])
    // input cleared by the commit → hint disappears again
    expect(w.find('p').exists()).toBe(false)
  })

  it('hint stays hidden when an existing chip is removed', async () => {
    const w = mount(TagInput, { props: { tags: ['hardware'] } })
    expect(w.find('p').exists()).toBe(false)
    await w.find('button[aria-label="Remove tag hardware"]').trigger('click')
    expect(w.emitted('update:tags')).toEqual([[[]]])
    expect(w.find('p').exists()).toBe(false)
  })

  it('fetches tags lazily: not on mount, once on first focus, never again', async () => {
    const fetchMock = vi.fn(async (_url: string) => TAG_RESPONSE())
    vi.stubGlobal('fetch', fetchMock)
    const w = mount(TagInput, { props: { tags: [] } })
    expect(fetchMock).not.toHaveBeenCalled() // nothing before focus
    const input = w.find('input')
    await input.trigger('focus')
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/tags?page_size=200')
    await input.trigger('blur')
    await input.trigger('focus') // second focus: cache holds
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('suggests matching tags while typing, excluding already-selected ones', async () => {
    const { w } = await mountFocused(['screws'])
    await w.find('input').setValue('scr')
    const listbox = w.find('[role="listbox"]')
    expect(listbox.exists()).toBe(true)
    expect(listbox.findAll('[role="option"]').map(o => o.text())).toEqual(['screwdrivers'])
  })

  it('orders suggestions by item_count desc, then name', async () => {
    const { w } = await mountFocused()
    await w.find('input').setValue('scr')
    expect(w.findAll('[role="option"]').map(o => o.text())).toEqual(['screws', 'screwdrivers'])
  })

  it('ArrowDown activates a suggestion and Enter picks it instead of committing typed text', async () => {
    const { w, input } = await mountFocused()
    await input.setValue('scr')
    expect(w.find('p').text()).toContain('Press Enter') // nothing active → hint stays
    await input.trigger('keydown', { key: 'ArrowDown' })
    const activeId = input.attributes('aria-activedescendant')
    expect(activeId).toBe(w.findAll('[role="option"]')[0].attributes('id'))
    expect(w.find('p').exists()).toBe(false) // Enter means "pick" now → hint hidden
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('update:tags')).toEqual([[['screws']]]) // the suggestion, not "scr"
    expect((input.element as HTMLInputElement).value).toBe('')
    expect(w.find('[role="listbox"]').exists()).toBe(false)
  })

  it('ArrowDown/ArrowUp wrap around the suggestion list', async () => {
    const { w, input } = await mountFocused()
    await input.setValue('scr')
    const ids = w.findAll('[role="option"]').map(o => o.attributes('id'))
    await input.trigger('keydown', { key: 'ArrowDown' })
    await input.trigger('keydown', { key: 'ArrowDown' })
    expect(input.attributes('aria-activedescendant')).toBe(ids[1])
    await input.trigger('keydown', { key: 'ArrowDown' }) // past the end → wraps to first
    expect(input.attributes('aria-activedescendant')).toBe(ids[0])
    await input.trigger('keydown', { key: 'ArrowUp' }) // back past the start → wraps to last
    expect(input.attributes('aria-activedescendant')).toBe(ids[1])
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('update:tags')).toEqual([[['screwdrivers']]])
  })

  it('clicking a suggestion picks it, with no double-add from blur', async () => {
    const { w, input } = await mountFocused()
    await input.setValue('scr')
    const opt = w.findAll('[role="option"]')[1] // screwdrivers
    await opt.trigger('mousedown') // .prevent — input must not blur before click
    await opt.trigger('click')
    expect(w.emitted('update:tags')).toEqual([[['screwdrivers']]]) // exactly once
    expect((input.element as HTMLInputElement).value).toBe('')
    expect(w.find('[role="listbox"]').exists()).toBe(false)
  })

  it('Escape closes the dropdown without committing and stops propagation', async () => {
    const bubbled = vi.fn()
    vi.stubGlobal('fetch', vi.fn(async () => TAG_RESPONSE()))
    // Wrapper via render function (no runtime template compiler in vitest).
    const Wrapper = defineComponent({
      setup: () => () => h('div', { onKeydown: bubbled }, [h(TagInput, { tags: [] })]),
    })
    const w = mount(Wrapper)
    const input = w.find('input')
    await input.trigger('focus')
    await flushPromises()
    await input.setValue('scr')
    expect(w.find('[role="listbox"]').exists()).toBe(true)
    await input.trigger('keydown', { key: 'Escape' })
    expect(w.find('[role="listbox"]').exists()).toBe(false)
    expect((input.element as HTMLInputElement).value).toBe('scr') // nothing committed
    expect(w.emitted('update:tags')).toBeUndefined()
    expect(bubbled).not.toHaveBeenCalled()
  })

  it('Enter with no active suggestion still commits the typed text', async () => {
    const { w, input } = await mountFocused()
    await input.setValue('scr') // matches exist, none active
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('update:tags')).toEqual([[['scr']]])
  })

  it('fetch error: toasts once, suggestions unavailable, typed tags still commit', async () => {
    vi.stubGlobal('fetch', vi.fn(async () =>
      new Response(JSON.stringify({ error: { code: 'internal_error', message: 'tag list failed' } }), { status: 500 })))
    const w = mount(TagInput, { props: { tags: [] } })
    const input = w.find('input')
    await input.trigger('focus')
    await flushPromises()
    expect(toasts.filter(t => t.type === 'error' && t.message === 'tag list failed')).toHaveLength(1)
    await input.setValue('steel')
    expect(w.find('[role="listbox"]').exists()).toBe(false) // no suggestions to offer
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('update:tags')).toEqual([[['steel']]])
    await input.trigger('blur')
    await input.trigger('focus') // failed fetch is not retried → no second toast
    await flushPromises()
    expect(toasts.filter(t => t.message === 'tag list failed')).toHaveLength(1)
  })

  it('gives each instance document-unique aria ids', () => {
    vi.stubGlobal('fetch', vi.fn(async () => TAG_RESPONSE()))
    const w = mount(() => h('div', [h(TagInput, { tags: [] }), h(TagInput, { tags: [] })]))
    const [a, b] = w.findAll('input')
    expect(a.attributes('aria-controls')).toBeTruthy()
    expect(a.attributes('aria-controls')).not.toBe(b!.attributes('aria-controls'))
  })
})
