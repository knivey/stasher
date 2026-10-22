import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import TagInput from './TagInput.vue'

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
})
