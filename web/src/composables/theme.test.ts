import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

// The composable initializes from localStorage/matchMedia at module import
// time, so each test sets the mock FIRST and then dynamically imports a
// fresh module instance.

function mockMatchMedia(dark: boolean) {
  const listeners: Array<(e: { matches: boolean }) => void> = []
  const mq = {
    matches: dark,
    addEventListener: (_: string, cb: (e: { matches: boolean }) => void) => listeners.push(cb),
    removeEventListener: () => { },
  }
  ;(window as any).matchMedia = vi.fn().mockReturnValue(mq)
  return (dark2: boolean) => listeners.forEach(cb => cb({ matches: dark2 }))
}

async function freshTheme() {
  vi.resetModules()
  return await import('./theme')
}

describe('useTheme', () => {
  beforeEach(() => { localStorage.clear(); document.documentElement.className = '' })
  afterEach(() => vi.restoreAllMocks())

  it('defaults to system preference', async () => {
    mockMatchMedia(true)
    const { useTheme } = await freshTheme()
    const { isDark } = useTheme()
    expect(isDark.value).toBe(true)
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('toggle persists an explicit choice', async () => {
    mockMatchMedia(true)
    const { useTheme } = await freshTheme()
    const { isDark, toggle } = useTheme()
    toggle()
    expect(isDark.value).toBe(false)
    expect(localStorage.getItem('stasher-theme')).toBe('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('toggle still applies the class when localStorage is blocked', async () => {
    mockMatchMedia(true)
    vi.spyOn(localStorage, 'setItem').mockImplementation(() => { throw new Error('QuotaExceededError') })
    const { useTheme } = await freshTheme()
    const { isDark, toggle } = useTheme()
    expect(() => toggle()).not.toThrow() // blocked storage must not break the toggle
    expect(isDark.value).toBe(false)
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('follows live system changes while no explicit choice stored', async () => {
    const flip = mockMatchMedia(false)
    const { useTheme } = await freshTheme()
    const { isDark } = useTheme()
    expect(isDark.value).toBe(false)
    flip(true)
    expect(isDark.value).toBe(true)
  })
})
