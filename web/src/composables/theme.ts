import { ref } from 'vue'

const KEY = 'stasher-theme'
type Mode = 'light' | 'dark'

function storedMode(): Mode | null {
  const v = localStorage.getItem(KEY)
  return v === 'light' || v === 'dark' ? v : null
}

const systemDark = () =>
  typeof matchMedia === 'function' && matchMedia('(prefers-color-scheme: dark)').matches

// singleton state: every consumer (mobile header, desktop sidebar) shares it
const isDark = ref<boolean>(
  storedMode() ? storedMode() === 'dark' : systemDark(),
)

function apply() {
  document.documentElement.classList.toggle('dark', isDark.value)
}
apply()

if (typeof matchMedia === 'function') {
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', e => {
    if (storedMode()) return // explicit choice wins
    isDark.value = e.matches
    apply()
  })
}

export function useTheme() {
  function toggle() {
    isDark.value = !isDark.value
    apply()
    try {
      localStorage.setItem(KEY, isDark.value ? 'dark' : 'light')
    } catch {
      // Blocked storage (private-mode quota etc.) must not leave the class
      // unapplied — apply() already ran, so the theme works for the session
      // and only the persistence is lost. Silent by design.
    }
  }
  return { isDark, toggle }
}
