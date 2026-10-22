<template>
  <div class="min-h-screen bg-slate-100">
    <!-- desktop sidebar -->
    <aside class="hidden md:flex fixed inset-y-0 left-0 w-56 flex-col bg-slate-900 text-slate-100 p-4 gap-1">
      <h1 class="text-lg font-bold px-2 py-3">Stasher</h1>
      <router-link v-for="n in nav" :key="n.to" :to="n.to"
        class="rounded-lg px-3 py-2 hover:bg-slate-700"
        :class="{ 'bg-slate-700': route.path.startsWith(n.to) && n.to !== '/' }">
        {{ n.label }}
      </router-link>
      <div class="mt-auto text-sm">
        <div class="px-3 py-2 truncate">{{ auth.user?.display_name }}</div>
        <button class="w-full text-left rounded-lg px-3 py-2 hover:bg-slate-700" @click="logout">Sign out</button>
      </div>
      <button @click="theme.toggle()" :aria-label="theme.isDark.value ? 'Switch to light theme' : 'Switch to dark theme'"
        class="mt-auto inline-flex min-w-11 min-h-11 items-center justify-center rounded-lg hover:bg-slate-700">{{ theme.isDark.value ? '☀️' : '🌙' }}</button>
    </aside>
    <!-- mobile top bar + bottom nav. Settings/Shared/Trash live as compact
         icon-links in the top bar (Admin too, for admins) so every
         destination is ≤1 tap on mobile; the bottom nav keeps the five
         primary destinations. -->
    <header class="md:hidden sticky top-0 z-10 bg-slate-900 text-white px-4 py-3 flex items-center justify-between">
      <span class="font-bold">Stasher</span>
      <nav class="flex items-center gap-3 text-sm" aria-label="Secondary">
        <!-- 44px tap targets (WCAG/mobile comfort): the emoji glyphs are
             tiny, so pad the links out to finger size without changing
             the desktop sidebar above. -->
        <router-link to="/shared" aria-label="Shared" title="Shared"
          class="inline-flex min-w-11 min-h-11 items-center justify-center">👥</router-link>
        <router-link to="/trash" aria-label="Trash" title="Trash"
          class="inline-flex min-w-11 min-h-11 items-center justify-center">🗑️</router-link>
        <router-link to="/settings" aria-label="Settings" title="Settings"
          class="inline-flex min-w-11 min-h-11 items-center justify-center">⚙️</router-link>
        <router-link v-if="auth.user?.is_admin" to="/admin" aria-label="Admin" title="Admin"
          class="inline-flex min-w-11 min-h-11 items-center justify-center">🛡️</router-link>
        <button @click="theme.toggle()" :aria-label="theme.isDark.value ? 'Switch to light theme' : 'Switch to dark theme'"
          class="inline-flex min-w-11 min-h-11 items-center justify-center">{{ theme.isDark.value ? '☀️' : '🌙' }}</button>
        <button @click="logout">Sign out</button>
      </nav>
    </header>
    <main class="md:pl-56 pb-20 md:pb-0">
      <div class="p-4 max-w-5xl mx-auto"><router-view /></div>
    </main>
    <nav class="md:hidden fixed bottom-0 inset-x-0 z-10 bg-slate-900 text-slate-100 flex">
      <router-link v-for="n in mobileNav" :key="n.to" :to="n.to"
        class="flex-1 text-center text-xs py-2">{{ n.short }}</router-link>
    </nav>
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { toast } from '../utils/toast'
import { useTheme } from '../composables/theme'

const auth = useAuthStore(), route = useRoute(), router = useRouter()
const theme = useTheme()
const nav = computed(() => {
  const base = [
    { to: '/', label: 'Dashboard' }, { to: '/items', label: 'Items' },
    { to: '/locations', label: 'Locations' }, { to: '/projects', label: 'Projects' },
    { to: '/shared', label: 'Shared' }, { to: '/trash', label: 'Trash' }, { to: '/settings', label: 'Settings' },
  ]
  return auth.user?.is_admin ? [...base, { to: '/admin', label: 'Admin' }] : base
})
const mobileNav = computed(() => nav.value.slice(0, 5).map(n => ({ to: n.to, short: n.label.slice(0, 5) })))
// The logout POST can fail on a dead network — the store still clears the
// user (finally), so toast the error and land on /login either way instead
// of leaving an unhandled rejection stranded on the shell.
async function logout() {
  await auth.logout().catch((e: Error) => toast(e.message, 'error'))
  router.push('/login')
}
</script>
