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
    </aside>
    <!-- mobile top bar + bottom nav -->
    <header class="md:hidden sticky top-0 z-10 bg-slate-900 text-white px-4 py-3 flex justify-between">
      <span class="font-bold">Stasher</span>
      <button @click="logout">Sign out</button>
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

const auth = useAuthStore(), route = useRoute(), router = useRouter()
const nav = computed(() => {
  const base = [
    { to: '/', label: 'Dashboard' }, { to: '/items', label: 'Items' },
    { to: '/locations', label: 'Locations' }, { to: '/projects', label: 'Projects' },
    { to: '/shared', label: 'Shared' }, { to: '/settings', label: 'Settings' },
  ]
  return auth.user?.is_admin ? [...base, { to: '/admin', label: 'Admin' }] : base
})
const mobileNav = computed(() => nav.value.slice(0, 5).map(n => ({ to: n.to, short: n.label.slice(0, 5) })))
async function logout() { await auth.logout(); router.push('/login') }
</script>
