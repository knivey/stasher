<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api/client'
import type { ListResponse, Location, Tag } from '../api/types'
import { useItemsStore } from '../stores/items'
import { toast } from '../utils/toast'
import { locationDepthOptions } from '../utils/locations'
import Pagination from '../components/Pagination.vue'

const router = useRouter()
const store = useItemsStore()
const filters = store.filters

// Debounced search: input writes a local ref; after 300ms of quiet it lands in
// filters.q, which the deep filters watcher below turns into page reset + fetch.
const search = ref(filters.q)
let timer: ReturnType<typeof setTimeout> | null = null
watch(search, v => {
  if (timer) clearTimeout(timer)
  timer = setTimeout(() => { filters.q = v }, 300)
})
onBeforeUnmount(() => { if (timer) clearTimeout(timer) })

// Locations & tags feed the filter selects. NOTE: these two endpoints
// ignore pagination entirely — the page_size param is accepted but unused
// and the full list always comes back (see docs/api-notes.md).
const locations = ref<Location[]>([])
const tags = ref<Tag[]>([])
const locationOptions = computed(() => locationDepthOptions(locations.value))
const locationName = computed(() => new Map(locations.value.map(l => [l.id, l.name])))

// Deep-watch filters: any change except page resets page to 1 (the reset
// re-triggers this watcher, which performs the fetch — exactly one per change);
// a page change from Pagination fetches without resetting.
let lastPage = filters.page
watch(filters, () => {
  if (filters.page !== lastPage) {
    lastPage = filters.page
    fetch()
  } else if (filters.page !== 1) {
    filters.page = 1
  } else {
    fetch()
  }
})

async function fetch() {
  try { await store.fetch() } catch (e) { toast((e as Error).message, 'error') }
}

onMounted(() => {
  fetch()
  api.get<ListResponse<Location>>('/api/v1/locations?page_size=200')
    .then(r => { locations.value = r.data }).catch(e => toast(e.message, 'error'))
  api.get<ListResponse<Tag>>('/api/v1/tags?page_size=200')
    .then(r => { tags.value = r.data }).catch(e => toast(e.message, 'error'))
})

// Desktop thumb hover preview: one fixed popup anchored to the hovered/focused
// thumb itself — right of the thumb, flipping to its left when it would
// overflow the viewport, top clamped onscreen. pointer-events-none so the
// popup can never steal the hover that keeps it alive; focus/blur mirror
// mouseenter/mouseleave for keyboard users (one showPopup code path for both).
const hoverThumb = ref<{ key: string; name: string; left: number; top: number } | null>(null)

function showPopup(key: string, name: string, el: EventTarget | null) {
  const r = (el as HTMLElement | null)?.getBoundingClientRect()
  if (!r) return
  const rightOfThumb = r.left + r.width + 12
  const left = rightOfThumb + 320 > window.innerWidth ? r.left - 332 : rightOfThumb
  hoverThumb.value = { key, name, left, top: Math.max(r.top - 8, 8) }
}
</script>
<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold dark:text-slate-100">Items</h1>
      <router-link to="/items/new" class="rounded-lg bg-slate-900 dark:bg-slate-100 text-white dark:text-slate-900 px-3 py-1.5 text-sm">+ New item</router-link>
    </div>

    <div class="bg-white dark:bg-slate-800 rounded-xl shadow p-3 flex flex-wrap gap-2 items-center">
      <input v-model="search" placeholder="Search items…" class="border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm flex-1 min-w-40" />
      <select v-model="filters.location_id" class="border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm" aria-label="Filter by location">
        <option :value="0">All locations</option>
        <option v-for="l in locationOptions" :key="l.id" :value="l.id">{{ '— '.repeat(l.depth) }}{{ l.name }}</option>
      </select>
      <select v-model="filters.tag" class="border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm" aria-label="Filter by tag">
        <option value="">All tags</option>
        <option v-for="t in tags" :key="t.id" :value="t.name">{{ t.name }}<template v-if="t.item_count !== undefined"> ({{ t.item_count }})</template></option>
      </select>
      <select v-model="filters.visibility" class="border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm" aria-label="Filter by visibility">
        <option value="">All visibility</option>
        <option value="private">Private</option>
        <option value="shared">Shared</option>
      </select>
      <select v-model="filters.sort" class="border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm" aria-label="Sort by">
        <option value="updated_at">Updated</option>
        <option value="created_at">Created</option>
        <option value="name">Name</option>
        <option value="qty">Qty</option>
      </select>
      <button @click="filters.order = filters.order === 'desc' ? 'asc' : 'desc'"
        class="border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2.5 py-1.5 text-sm" :title="filters.order === 'desc' ? 'Descending' : 'Ascending'">
        {{ filters.order === 'desc' ? '↓' : '↑' }}
      </button>
    </div>

    <p v-if="store.loading" class="text-slate-400 dark:text-slate-500 text-sm">Loading…</p>

    <template v-else-if="store.items.length">
      <!-- desktop table (≥md) -->
      <table class="hidden md:table w-full bg-white dark:bg-slate-800 rounded-xl shadow text-sm">
        <thead>
          <tr class="text-left text-slate-500 dark:text-slate-400 border-b dark:border-slate-700">
            <th class="w-12 px-3 py-2"></th>
            <th class="px-3 py-2 font-medium">Name</th>
            <th class="px-3 py-2 font-medium">Qty</th>
            <th class="px-3 py-2 font-medium">Location</th>
            <th class="px-3 py-2 font-medium">Tags</th>
            <th class="px-3 py-2 font-medium">Updated</th>
            <th class="px-3 py-2 font-medium">📷</th>
          </tr>
        </thead>
        <tbody class="divide-y dark:divide-slate-700">
          <tr v-for="i in store.items" :key="i.id" @click="router.push(`/items/${i.id}`)" class="cursor-pointer hover:bg-slate-50 dark:hover:bg-slate-700/50">
            <td class="px-3 py-2">
              <img v-if="i.thumb_key" :src="`/files/${i.thumb_key}`" loading="lazy" :alt="i.name" tabindex="0"
                class="w-10 h-10 object-cover rounded-lg"
                @mouseenter="showPopup(i.thumb_key, i.name, $event.target)" @mouseleave="hoverThumb = null"
                @focus="showPopup(i.thumb_key, i.name, $event.target)" @blur="hoverThumb = null" />
              <span v-else class="text-slate-400 dark:text-slate-500">—</span>
            </td>
            <td class="px-3 py-2"><router-link :to="`/items/${i.id}`" class="hover:underline">{{ i.name }}</router-link><span v-if="i.disposed" data-disposed class="ml-1.5 rounded-full bg-gray-200 dark:bg-slate-700 text-gray-500 dark:text-slate-300 px-1.5 py-0.5 text-xs align-middle">disposed</span></td>
            <td class="px-3 py-2">{{ i.qty }}{{ i.unit ? ` ${i.unit}` : '' }}</td>
            <td class="px-3 py-2">{{ i.location_id && locationName.get(i.location_id) || '—' }}</td>
            <td class="px-3 py-2 text-slate-500 dark:text-slate-400">{{ i.tags?.length ? i.tags.map(t => t.name).join(', ') : '—' }}</td>
            <td class="px-3 py-2 text-slate-500 dark:text-slate-400">{{ i.updated_at?.slice(0, 10) }}</td>
            <td class="px-3 py-2">{{ i.photo_count ? `📷 ${i.photo_count}` : '—' }}</td>
          </tr>
        </tbody>
      </table>

      <!-- Hover preview of the desktop table thumb: fixed so it can't be
           clipped by the table, positioned next to the thumb (see showPopup),
           desktop-only (the trigger is the md table). -->
      <div v-if="hoverThumb" data-thumb-popup
        class="pointer-events-none fixed z-50 hidden md:block w-80 max-w-[80vw] rounded-xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 shadow-lg p-1.5"
        :style="{ left: `${hoverThumb.left}px`, top: `${hoverThumb.top}px` }">
        <img :src="`/files/${hoverThumb.key}`" :alt="hoverThumb.name" class="w-full rounded-lg" />
      </div>

      <!-- mobile cards (<md) -->
      <div data-cards class="md:hidden space-y-2">
        <router-link v-for="i in store.items" :key="i.id" :to="`/items/${i.id}`"
          class="block bg-white dark:bg-slate-800 rounded-xl shadow px-4 py-3 flex items-center justify-between">
          <div>
            <div class="font-medium dark:text-slate-100">{{ i.name }}<span v-if="i.disposed" data-disposed class="ml-1.5 rounded-full bg-gray-200 dark:bg-slate-700 text-gray-500 dark:text-slate-300 px-1.5 py-0.5 text-xs font-normal align-middle">disposed</span></div>
            <img v-if="i.thumb_key" :src="`/files/${i.thumb_key}`" loading="lazy" alt=""
              class="h-24 w-full object-cover rounded-lg" />
            <div v-else-if="i.photo_count" class="text-xs text-slate-400 dark:text-slate-500">📷 {{ i.photo_count }}</div>
          </div>
          <div class="text-sm text-slate-500 dark:text-slate-400">{{ i.qty }}{{ i.unit ? ` ${i.unit}` : '' }}</div>
        </router-link>
      </div>
    </template>

    <p v-else class="bg-white dark:bg-slate-800 rounded-xl shadow p-6 text-center text-slate-400 dark:text-slate-500 text-sm">No items found.</p>

    <Pagination v-model:page="filters.page" :total="store.total" :page-size="filters.page_size" />
  </div>
</template>
