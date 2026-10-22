<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api, ApiError } from '../api/client'
import type { Item, ListResponse } from '../api/types'
import { toast } from '../utils/toast'
import Pagination from '../components/Pagination.vue'

type SharedItem = Item & { owner_name: string }

const router = useRouter()

// Local list state — shared items are a different dataset than the items store.
const items = ref<SharedItem[]>([])
const total = ref(0)
const loading = ref(false)
const page = ref(1)
const pageSize = 50

// Debounced search: same 300ms pattern as ItemsView — input writes a local
// ref, after quiet it lands in q, and the watcher below resets page + fetches.
const q = ref('')
const search = ref('')
let timer: ReturnType<typeof setTimeout> | null = null
watch(search, v => {
  if (timer) clearTimeout(timer)
  timer = setTimeout(() => { q.value = v }, 300)
})
onBeforeUnmount(() => { if (timer) clearTimeout(timer) })

// Any change except page resets page to 1 (the reset re-triggers this watcher,
// which performs the fetch — exactly one per change); a page change from
// Pagination fetches without resetting.
let lastPage = page.value
watch([q, page], () => {
  if (page.value !== lastPage) {
    lastPage = page.value
    fetch()
  } else if (page.value !== 1) {
    page.value = 1
  } else {
    fetch()
  }
})

async function fetch() {
  loading.value = true
  try {
    const p = new URLSearchParams()
    if (q.value) p.set('q', q.value)
    p.set('page', String(page.value)); p.set('page_size', String(pageSize))
    const res = await api.get<ListResponse<SharedItem>>(`/api/v1/shared?${p}`)
    items.value = res.data
    total.value = res.total
  } catch (e) {
    toast((e as Error).message, 'error')
  } finally {
    loading.value = false
  }
}

onMounted(fetch)

// Copy: busy tracked per item id — several cards can be in flight at once and
// each stays disabled until its own request settles (a single-slot ref would
// let a later click re-enable an earlier card's button).
const copying = ref(new Set<number>())

async function copy(item: SharedItem) {
  if (copying.value.has(item.id)) return
  copying.value.add(item.id)
  try {
    const created = await api.post<Item>(`/api/v1/items/${item.id}/copy`)
    toast('Copied to your inventory', 'success')
    router.push(`/items/${created.id}`)
  } catch (e) {
    toast((e as Error).message, 'error')
    if (e instanceof ApiError && e.status === 404) fetch() // un-shared/deleted meanwhile
  } finally {
    copying.value.delete(item.id)
  }
}
</script>
<template>
  <div class="space-y-4">
    <h1 class="text-2xl font-bold dark:text-slate-100">Shared with you</h1>

    <div class="bg-white dark:bg-slate-800 rounded-xl shadow p-3 flex gap-2 items-center">
      <input v-model="search" placeholder="Search shared items…" class="border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm flex-1 min-w-40" />
    </div>

    <p v-if="loading" class="text-slate-400 dark:text-slate-500 text-sm">Loading…</p>

    <div v-else-if="items.length" data-cards class="space-y-2">
      <div v-for="i in items" :key="i.id"
        class="bg-white dark:bg-slate-800 rounded-xl shadow px-4 py-3 flex items-center justify-between">
        <div>
          <div class="font-medium dark:text-slate-100">{{ i.name }}<span v-if="i.disposed" data-disposed class="ml-1.5 rounded-full bg-gray-200 dark:bg-slate-700 text-gray-500 dark:text-slate-300 px-1.5 py-0.5 text-xs font-normal align-middle">disposed</span></div>
          <div class="text-xs text-slate-500 dark:text-slate-400">from {{ i.owner_name }} · {{ i.qty }}{{ i.unit ? ` ${i.unit}` : '' }}</div>
          <img v-if="i.thumb_key" :src="`/files/${i.thumb_key}`" loading="lazy" alt=""
            class="h-24 w-full object-cover rounded-lg" />
          <div v-else-if="i.photo_count" class="text-xs text-slate-400 dark:text-slate-500">📷 {{ i.photo_count }}</div>
        </div>
        <button @click="copy(i)" :disabled="copying.has(i.id)"
          class="rounded-lg bg-slate-900 dark:bg-slate-100 text-white dark:text-slate-900 px-3 py-1.5 text-sm disabled:opacity-40">
          {{ copying.has(i.id) ? 'Copying…' : 'Copy' }}
        </button>
      </div>
    </div>

    <p v-else class="bg-white dark:bg-slate-800 rounded-xl shadow p-6 text-center text-slate-400 dark:text-slate-500 text-sm">No shared items found.</p>

    <Pagination v-model:page="page" :total="total" :page-size="pageSize" />
  </div>
</template>
