import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ConfirmDialog from './ConfirmDialog.vue'

describe('ConfirmDialog', () => {
  it('renders the hint as its own red+bold paragraph below the message', () => {
    const w = mount(ConfirmDialog, {
      props: { open: true, title: "Delete 'X'", message: 'Gone to trash.', hint: 'Log usage instead.', confirmLabel: 'Delete' },
    })
    const hint = w.find('p.text-red-600')
    expect(hint.exists()).toBe(true)
    expect(hint.classes()).toContain('font-bold')
    expect(hint.classes()).toContain('dark:text-red-400')
    expect(hint.text()).toBe('Log usage instead.')
    // plain interpolation, sits directly BELOW the message paragraph
    expect(hint.text()).not.toContain('<')
    expect(hint.element.previousElementSibling).toBe(w.find('p.whitespace-pre-line').element)
  })

  it('renders no hint element when the hint prop is absent', () => {
    const w = mount(ConfirmDialog, {
      props: { open: true, title: "Delete 'X'", message: 'Gone to trash.' },
    })
    expect(w.find('p.text-red-600').exists()).toBe(false)
  })

  it('renders nothing (hint included) when closed', () => {
    const w = mount(ConfirmDialog, {
      props: { open: false, title: 't', message: 'm', hint: 'h' },
    })
    expect(w.find('p.text-red-600').exists()).toBe(false)
  })
})
