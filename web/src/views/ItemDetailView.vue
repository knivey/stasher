<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, ApiError } from '../api/client'
import type { Breadcrumb, DocSearchResult, DocSearchResponse, Doc, FetchJob, Item, LedgerEntry, ListResponse, Location, Note, Photo, Project } from '../api/types'
import { useItemsStore } from '../stores/items'
import { toast } from '../utils/toast'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import Pagination from '../components/Pagination.vue'
import PhotoGallery from '../components/PhotoGallery.vue'
import FileUpload from '../components/FileUpload.vue'

const route = useRoute()
const router = useRouter()
const itemsStore = useItemsStore()

const item = ref<Item | null>(null)
const breadcrumbs = ref<Breadcrumb[]>([])
const confirmDelete = ref(false)

async function fetchItem() {
  try {
    item.value = await api.get<Item>(`/api/v1/items/${route.params.id}`)
    breadcrumbs.value = []
    if (item.value.location_id) await loadBreadcrumbs(item.value.location_id)
  } catch (e) {
    toast((e as Error).message, 'error')
    router.push('/items')
  }
}

async function loadBreadcrumbs(locationId: number) {
  try {
    const res = await api.get<{ location: Location; breadcrumbs: Breadcrumb[] }>(`/api/v1/locations/${locationId}`)
    breadcrumbs.value = res.breadcrumbs ?? []
  } catch (e) {
    toast((e as Error).message, 'error')
  }
}

// Tag chips are programmatic navigation with a store mutation, so they can't
// be plain router-links: seed the shared items filter, then navigate.
function filterByTag(name: string) {
  itemsStore.filters.tag = name
  itemsStore.filters.page = 1
  router.push('/items')
}

// ---- Notes (timestamped log: add + delete only, newest first) ----

const noteBody = ref('')
const noteSaving = ref(false)

async function submitNote() {
  if (noteSaving.value || !noteBody.value.trim()) return
  noteSaving.value = true
  try {
    // trimmed body: the backend validates 1-2000 chars AFTER its own trim
    await api.post(`/api/v1/items/${route.params.id}/notes`, { body: noteBody.value.trim() })
    toast('Note added', 'success')
    noteBody.value = ''
    await fetchItem() // notes[] rides the item detail
  } catch (e) {
    toast(e instanceof ApiError ? e.message : 'Failed to add note', 'error') // textarea keeps its text
  } finally {
    noteSaving.value = false
  }
}

async function deleteNote(n: Note) {
  try {
    await api.del(`/api/v1/items/${route.params.id}/notes/${n.id}`)
    toast('Note deleted', 'success')
    await fetchItem()
  } catch (e) {
    toast((e as Error).message, 'error')
  }
}

// photos[] is (sort_order, id) ordered — photos[0] is the current main.
const heroZoom = ref(false)
const mainPhoto = computed(() => item.value?.photos?.[0] ?? null)
const mainSaving = ref<Set<number>>(new Set())

async function makeMain(p: Photo) {
  if (mainSaving.value.has(p.id)) return
  mainSaving.value.add(p.id)
  try {
    await api.post(`/api/v1/items/${route.params.id}/photos/${p.id}/main`)
    await fetchItem() // refetch: the backend renumbers and reorders photos[]
  } catch (e) {
    toast(e instanceof ApiError ? e.message : 'Failed to set main photo', 'error')
  } finally {
    mainSaving.value.delete(p.id)
  }
}

async function deletePhoto(p: Photo) {
  try {
    await api.del(`/api/v1/items/${route.params.id}/photos/${p.id}`)
    toast('Photo deleted', 'success')
    await fetchItem()
  } catch (e) {
    toast((e as Error).message, 'error')
  }
}

async function deleteDoc(d: Doc) {
  try {
    await api.del(`/api/v1/items/${route.params.id}/documents/${d.id}`)
    toast('Document deleted', 'success')
    await fetchItem()
  } catch (e) {
    toast((e as Error).message, 'error')
  }
}

async function deleteItem() {
  if (!item.value) return
  confirmDelete.value = false
  try {
    await api.del(`/api/v1/items/${route.params.id}`)
    toast('Item deleted', 'success')
    router.push('/items')
  } catch (e) {
    toast((e as Error).message, 'error')
  }
}

// The badge IS the toggle (todo: visibility without a trip through the edit
// form; this view is owner-only). PATCH the flipped value, refetch, toast.
const visibilitySaving = ref(false)

async function toggleVisibility() {
  if (!item.value || visibilitySaving.value) return
  visibilitySaving.value = true
  const next = item.value.visibility === 'shared' ? 'private' : 'shared'
  try {
    await api.patch(`/api/v1/items/${route.params.id}`, { visibility: next })
    await fetchItem() // badge text follows the server's answer
    toast('Visibility updated', 'success')
  } catch (e) {
    toast((e as Error).message, 'error') // badge keeps its current state
  } finally {
    visibilitySaving.value = false
  }
}

// ---- Usage ledger ----

// Mirror of the backend's kind CHECK whitelist (usage_handlers.go).
const usageKinds = ['used', 'sold', 'lost', 'found', 'restock', 'disposed', 'adjustment'] as const
type UsageKind = LedgerEntry['kind']

const usage = ref<LedgerEntry[]>([])
const usageTotal = ref(0)
const usagePage = ref(1)
const usagePageSize = 20
const usageLoading = ref(false)

async function fetchUsage() {
  usageLoading.value = true
  try {
    const res = await api.get<ListResponse<LedgerEntry>>(
      `/api/v1/items/${route.params.id}/usage?page=${usagePage.value}&page_size=${usagePageSize}`)
    usage.value = res.data ?? []
    usageTotal.value = res.total ?? 0
  } catch (e) {
    toast((e as Error).message, 'error')
  } finally {
    usageLoading.value = false
  }
}

// Page changes come from the Pagination control; a page set back to 1 by
// submitUsage() goes through here too, so it must not fetch again itself.
watch(usagePage, () => { fetchUsage() })

// Badge colors per kind (see api-notes: kinds are a fixed whitelist).
// found vs restock: both are "gains" — emerald vs teal keeps them distinct
// (backlog: they previously shared a color).
const kindBadgeClass: Record<UsageKind, string> = {
  used: 'bg-slate-200 text-slate-700 dark:bg-slate-700 dark:text-slate-300',
  sold: 'bg-blue-100 text-blue-700',
  lost: 'bg-red-100 text-red-700',
  found: 'bg-emerald-100 text-emerald-700',
  restock: 'bg-teal-100 text-teal-700',
  disposed: 'bg-gray-200 text-gray-600 dark:bg-slate-700 dark:text-slate-300',
  adjustment: 'bg-slate-100 text-slate-500 dark:bg-slate-700 dark:text-slate-300',
}

function fmtDelta(d: number): string {
  return d > 0 ? `+${d}` : d < 0 ? `−${Math.abs(d)}` : '±0'
}

// Ledger rows carry project_name (nullable: null when the project was
// deleted, absent in old payloads) — the chip prefers it and falls back
// through the item's linked projects, then the log form's list, then a
// bare id.
function projectName(id: number): string {
  return item.value?.projects?.find(p => p.id === id)?.name
    ?? logProjects.value.find(p => p.id === id)?.name
    ?? `project #${id}`
}

// ---- Log usage form (inline card, not a route) ----

const showLogForm = ref(false)
const logSaving = ref(false)
const logProjects = ref<Project[]>([])
const logForm = reactive({ kind: 'used' as UsageKind, delta: -1, project_id: 0, note: '' })

function openLogForm() {
  showLogForm.value = true
  logForm.kind = 'used'
  logForm.delta = -1
  logForm.project_id = 0
  logForm.note = ''
  // Lazy one-shot fetch: /projects ignores pagination (api-notes) — the
  // page_size param is an accepted no-op, kept for consistency with the
  // other list calls.
  if (!logProjects.value.length) {
    api.get<ListResponse<Project>>('/api/v1/projects?page_size=200')
      .then(r => { logProjects.value = r.data ?? [] })
      .catch(e => toast(e.message, 'error'))
  }
}

async function submitUsage() {
  if (logSaving.value) return
  logSaving.value = true
  try {
    const body: Record<string, unknown> = { kind: logForm.kind }
    if (logForm.kind !== 'disposed') body.delta = Number(logForm.delta) // disposed forces −qty server-side
    if (logForm.project_id) body.project_id = Number(logForm.project_id)
    if (logForm.note.trim()) body.note = logForm.note.trim()
    await api.post(`/api/v1/items/${route.params.id}/usage`, body)
    toast('Usage logged', 'success')
    showLogForm.value = false
    if (usagePage.value !== 1) usagePage.value = 1 // watcher refetches page 1
    else await fetchUsage()
    await fetchItem() // header qty + disposed flag follow the ledger write
  } catch (e) {
    toast((e as Error).message, 'error') // form stays open so the entry can be fixed
  } finally {
    logSaving.value = false
  }
}

onMounted(async () => {
  await fetchItem()
  if (item.value) await fetchUsage()
})

// ---- External fetch (product page URL + fetch-from-URL dialog) ----

// Cadence and cap follow api-notes: poll every 2s for at most 45 tries =
// the server's 90s overall job deadline (queue wait + fetch, single-flight).
const FETCH_POLL_MS = 2000
const FETCH_POLL_TRIES = 45

const productUrl = computed(() => item.value?.product_url ?? '')

const showFetch = ref(false)
const fetchUrl = ref('')
const fetchMode = ref<'direct' | 'archive'>('archive')
const fetchBusy = ref(false)
let fetchTimer: ReturnType<typeof setTimeout> | null = null
let fetchCancelled = false

onUnmounted(() => {
  fetchCancelled = true
  if (fetchTimer !== null) clearTimeout(fetchTimer)
})

// '.pdf' before any query/hash → direct download; anything else → archive.
function guessMode(url: string): 'direct' | 'archive' {
  const bare = url.split('#')[0].split('?')[0]
  return bare.toLowerCase().includes('.pdf') ? 'direct' : 'archive'
}

// Re-guess on URL input; the select stays user-overridable until the next
// keystroke (standard preselect behavior).
watch(fetchUrl, u => { fetchMode.value = guessMode(u) })

function openFetch() {
  fetchUrl.value = ''
  fetchMode.value = 'archive'
  showFetch.value = true
}

function submitFetch() {
  const url = fetchUrl.value.trim()
  if (!url || fetchBusy.value) return
  void runFetchJob(url, fetchMode.value)
}

function archiveProductUrl() {
  if (item.value?.product_url) void runFetchJob(item.value.product_url, 'archive')
}

async function runFetchJob(url: string, mode: 'direct' | 'archive') {
  if (fetchBusy.value) return
  fetchBusy.value = true
  try {
    const { job_id } = await api.post<{ job_id: string }>(`/api/v1/items/${route.params.id}/fetch`, { url, mode })
    toast('Fetching…', 'info')
    for (let i = 0; i < FETCH_POLL_TRIES; i++) {
      await new Promise<void>(r => { fetchTimer = setTimeout(r, FETCH_POLL_MS) })
      if (fetchCancelled) return // unmounted: timer cleared, just abandon the loop
      try {
        const job = await api.get<FetchJob>(`/api/v1/jobs/${job_id}`)
        if (job.status === 'done') {
          toast('Added document', 'success')
          showFetch.value = false
          await fetchItem() // documents[] rides the item detail
          return
        }
        if (job.status === 'failed') {
          toast(job.error ?? 'fetch failed', 'error')
          return
        }
      } catch (e) {
        // Jobs are in-memory only: a 404 mid-poll means the server restarted
        // and the document was NOT created (api-notes). Other errors
        // (network blips) fall through to the outer handler.
        if (e instanceof ApiError && e.status === 404) {
          toast('fetch interrupted', 'error')
          return
        }
        throw e
      }
    }
    toast('fetch timed out — check Documents in a moment', 'error')
  } catch (e) {
    toast((e as Error).message, 'error')
  } finally {
    fetchBusy.value = false
  }
}
// ---- Document finder (Brave search → server-verified PDFs) ----

// The search response is synchronous (15s server budget) — no job polling
// here; picking a result hands the URL to runFetchJob above.
const showFindDoc = ref(false)
const findQuery = ref('')
const findBusy = ref(false)
const findResults = ref<DocSearchResult[] | null>(null)
const findRejected = ref(0)

function openFindDoc() {
  // prefill: the item name is usually the part number
  findQuery.value = item.value?.name ?? ''
  findResults.value = null
  findRejected.value = 0
  showFindDoc.value = true
}

async function submitFindDoc() {
  const query = findQuery.value.trim()
  if (!query || findBusy.value) return
  findBusy.value = true
  try {
    const res = await api.post<DocSearchResponse>(`/api/v1/items/${route.params.id}/search-documents`, { query })
    findResults.value = res.results ?? []
    findRejected.value = res.rejected ?? 0
  } catch (e) {
    if (e instanceof ApiError && e.code === 'search_disabled') {
      toast('Configure brave_api_key to enable search', 'error')
    } else {
      toast((e as Error).message, 'error')
    }
  } finally {
    findBusy.value = false
  }
}

function chooseDoc(c: DocSearchResult) {
  showFindDoc.value = false
  void runFetchJob(c.url, 'direct')
}

// unknown size (absent key, or a defensive negative) renders as "—"
function fmtDocSize(size?: number): string {
  if (size == null || size < 0) return '—'
  if (size >= 1024 * 1024) return `${(size / (1024 * 1024)).toFixed(1)} MB`
  return `${Math.ceil(size / 1024)} KB`
}
</script>
<template>
  <div v-if="item" class="space-y-6">
    <div class="flex items-center justify-between gap-2">
      <h1 class="text-2xl font-bold dark:text-slate-100">{{ item.name }}</h1>
      <div class="flex gap-2">
        <router-link :to="`/items/${item.id}/edit`" class="rounded-lg bg-slate-900 dark:bg-slate-100 text-white dark:text-slate-900 px-3 py-1.5 text-sm">Edit</router-link>
        <button @click="openLogForm" class="rounded-lg border dark:border-slate-700 dark:text-slate-100 px-3 py-1.5 text-sm">Log usage</button>
        <button @click="confirmDelete = true" class="rounded-lg bg-red-600 text-white px-3 py-1.5 text-sm">Delete</button>
      </div>
    </div>

    <img v-if="mainPhoto" :src="`/files/${mainPhoto.storage_key}`" :alt="item.name" data-hero
      class="w-full max-h-80 object-contain rounded-xl bg-slate-100 dark:bg-slate-700 cursor-zoom-in"
      @click="heroZoom = true" />
    <div v-if="heroZoom" class="fixed inset-0 z-50 bg-black/80 flex items-center justify-center p-4" @click="heroZoom = false">
      <img v-if="mainPhoto" :src="`/files/${mainPhoto.storage_key}`" class="max-h-full max-w-full object-contain" @click.stop />
      <button class="absolute top-4 right-4 text-white text-2xl" @click="heroZoom = false">✕</button>
    </div>

    <div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
      <span class="font-medium dark:text-slate-100">{{ item.qty }}{{ item.unit ? ` ${item.unit}` : '' }}</span>
      <span v-if="breadcrumbs.length" class="text-slate-500 dark:text-slate-400">{{ breadcrumbs.map(b => b.name).join(' › ') }}</span>
      <button data-visibility :disabled="visibilitySaving"
        :aria-label="`Toggle visibility, currently ${item.visibility}`"
        :class="item.visibility === 'shared' ? 'bg-emerald-100 text-emerald-700' : 'bg-slate-100 text-slate-600 dark:bg-slate-700 dark:text-slate-300'"
        class="cursor-pointer rounded-full px-2 py-0.5 text-xs font-medium hover:ring-2 hover:ring-slate-300 dark:hover:ring-slate-500 disabled:opacity-50"
        @click="toggleVisibility">{{ item.visibility === 'shared' ? 'Shared' : 'Private' }}</button>
      <span v-if="item.disposed" data-disposed class="rounded-full bg-gray-200 dark:bg-slate-700 text-gray-500 dark:text-slate-300 px-2 py-0.5 text-xs font-medium">disposed</span>
    </div>

    <!-- Product page URL: external link + one-click archive (api-notes "External fetch") -->
    <div v-if="productUrl" data-product-url class="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
      <a :href="productUrl" target="_blank" rel="noopener"
        class="text-blue-600 dark:text-blue-400 hover:underline break-all max-w-full">{{ productUrl }}</a>
      <button type="button" :disabled="fetchBusy" @click="archiveProductUrl"
        class="rounded-lg border dark:border-slate-700 dark:text-slate-100 px-2 py-0.5 text-xs disabled:opacity-50">Archive</button>
    </div>

    <!-- Inline usage log form (not a route) -->
    <form v-if="showLogForm" data-log-form class="bg-white dark:bg-slate-800 rounded-xl shadow p-4 space-y-3" @submit.prevent="submitUsage">
      <h2 class="text-sm font-semibold dark:text-slate-100">Log usage</h2>
      <div class="flex gap-4">
        <label class="flex-1 block text-sm font-medium dark:text-slate-100">Kind
          <select v-model="logForm.kind" data-log-kind class="mt-1 w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal bg-white">
            <option v-for="k in usageKinds" :key="k" :value="k">{{ k }}</option>
          </select>
        </label>
        <label v-if="logForm.kind !== 'disposed'" class="flex-1 block text-sm font-medium dark:text-slate-100">Delta
          <input v-model.number="logForm.delta" data-log-delta type="number" step="any" required
            class="mt-1 w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal" />
        </label>
      </div>
      <p v-if="logForm.kind === 'disposed'" class="text-xs text-slate-500 dark:text-slate-400">Sets quantity to 0 and marks the item disposed.</p>
      <label class="block text-sm font-medium dark:text-slate-100">Project <span class="font-normal text-slate-400 dark:text-slate-500">(optional)</span>
        <select v-model="logForm.project_id" data-log-project class="mt-1 w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal bg-white">
          <option :value="0">None</option>
          <option v-for="p in logProjects" :key="p.id" :value="p.id">{{ p.name }}</option>
        </select>
      </label>
      <label class="block text-sm font-medium dark:text-slate-100">Note <span class="font-normal text-slate-400 dark:text-slate-500">(optional)</span>
        <input v-model="logForm.note" data-log-note maxlength="10000" placeholder="What happened…"
          class="mt-1 w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal" />
      </label>
      <div class="flex justify-end gap-2">
        <button type="button" @click="showLogForm = false" class="rounded-lg border dark:border-slate-700 dark:text-slate-100 px-3 py-1.5 text-sm">Cancel</button>
        <button type="submit" :disabled="logSaving"
          class="rounded-lg bg-slate-900 dark:bg-slate-100 text-white dark:text-slate-900 px-3 py-1.5 text-sm disabled:opacity-50">
          {{ logSaving ? 'Logging…' : 'Log' }}</button>
      </div>
    </form>

    <p v-if="item.description" class="bg-white dark:bg-slate-800 rounded-xl shadow p-4 text-sm dark:text-slate-100 whitespace-pre-wrap">{{ item.description }}</p>

    <section v-if="item.tags?.length">
      <h2 class="font-semibold mb-2 dark:text-slate-100">Tags</h2>
      <div class="flex flex-wrap gap-2">
        <button v-for="t in item.tags" :key="t.id" @click="filterByTag(t.name)"
          class="bg-slate-100 dark:bg-slate-700 dark:text-slate-100 rounded-full px-3 py-1 text-sm hover:bg-slate-200 dark:hover:bg-slate-600">#{{ t.name }}</button>
      </div>
    </section>

    <!-- Notes: timestamped log, newest first (api-notes "Item notes") -->
    <section data-notes>
      <h2 class="font-semibold mb-2 dark:text-slate-100">Notes</h2>
      <form data-note-form class="bg-white dark:bg-slate-800 rounded-xl shadow p-4 space-y-3 mb-3" @submit.prevent="submitNote">
        <textarea v-model="noteBody" data-note-body rows="3" maxlength="2000" placeholder="Add a note…"
          class="w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm"></textarea>
        <div class="flex justify-end">
          <button type="submit" data-note-add :disabled="noteSaving || !noteBody.trim()"
            class="rounded-lg bg-slate-900 dark:bg-slate-100 text-white dark:text-slate-900 px-3 py-1.5 text-sm disabled:opacity-50">
            {{ noteSaving ? 'Adding…' : 'Add note' }}</button>
        </div>
      </form>
      <ul v-if="item.notes?.length" class="bg-white dark:bg-slate-800 rounded-xl shadow divide-y dark:divide-slate-700 text-sm">
        <li v-for="n in item.notes" :key="n.id" class="px-4 py-2">
          <p class="whitespace-pre-line dark:text-slate-100">{{ n.body }}</p>
          <div class="flex items-center justify-between gap-2 mt-1">
            <span class="text-xs text-slate-400 dark:text-slate-500">{{ n.created_at.slice(0, 10) }}</span>
            <button @click="deleteNote(n)" class="text-red-600 dark:text-red-400">Delete</button>
          </div>
        </li>
      </ul>
      <p v-else class="text-slate-400 dark:text-slate-500 text-sm">No notes yet.</p>
    </section>

    <section>
      <div class="flex items-center justify-between mb-2">
        <h2 class="font-semibold dark:text-slate-100">Photos</h2>
        <FileUpload kind="photo" :item-id="item.id" @uploaded="fetchItem" />
      </div>
      <PhotoGallery v-if="item.photos?.length" :photos="item.photos" :can-edit="true" @delete="deletePhoto" @main="makeMain" />
    </section>

    <section>
      <div class="flex items-center justify-between mb-2">
        <h2 class="font-semibold dark:text-slate-100">Documents</h2>
        <div class="flex gap-2">
          <button type="button" @click="openFindDoc"
            class="rounded-lg border dark:border-slate-700 dark:text-slate-100 px-3 py-1.5 text-sm">Find document…</button>
          <button type="button" @click="openFetch"
            class="rounded-lg border dark:border-slate-700 dark:text-slate-100 px-3 py-1.5 text-sm">Fetch from URL…</button>
          <FileUpload kind="doc" :item-id="item.id" @uploaded="fetchItem" />
        </div>
      </div>
      <ul v-if="item.documents?.length" class="bg-white dark:bg-slate-800 rounded-xl shadow divide-y dark:divide-slate-700 text-sm">
        <li v-for="d in item.documents" :key="d.id" class="flex items-center justify-between gap-2 px-4 py-2">
          <a :href="`/files/${d.storage_key}`" target="_blank" rel="noopener" class="hover:underline dark:text-slate-100">📄 {{ d.filename }} ({{ Math.ceil(d.size_bytes / 1024) }} KB)</a>
          <button @click="deleteDoc(d)" class="text-red-600 dark:text-red-400">Delete</button>
        </li>
      </ul>
    </section>

    <!-- Usage history (newest first; the ledger is written by usage posts and
         qty-changing PATCHes alike — see api-notes "Usage ledger") -->
    <section data-usage>
      <h2 class="font-semibold mb-2 dark:text-slate-100">History</h2>
      <p v-if="usageLoading" class="text-slate-400 dark:text-slate-500 text-sm">Loading…</p>
      <template v-else>
        <ul v-if="usage.length" class="bg-white dark:bg-slate-800 rounded-xl shadow divide-y dark:divide-slate-700 text-sm">
          <li v-for="e in usage" :key="e.id" class="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2">
            <span class="font-medium tabular-nums dark:text-slate-100">{{ fmtDelta(e.delta) }}</span>
            <span data-kind-badge class="rounded-full px-2 py-0.5 text-xs font-medium" :class="kindBadgeClass[e.kind]">{{ e.kind }}</span>
            <router-link v-if="e.project_id" :to="`/projects/${e.project_id}`"
              class="bg-slate-100 dark:bg-slate-700 dark:text-slate-100 rounded-full px-2 py-0.5 text-xs hover:bg-slate-200 dark:hover:bg-slate-600">🧵 {{ e.project_name ?? projectName(e.project_id) }}</router-link>
            <span v-if="e.note" class="text-slate-500 dark:text-slate-400">“{{ e.note }}”</span>
            <span class="text-slate-500 dark:text-slate-400">→ {{ e.resulting_qty }} left</span>
            <span class="ml-auto text-xs text-slate-400 dark:text-slate-500">{{ e.created_at.slice(0, 10) }}</span>
          </li>
        </ul>
        <p v-else class="text-slate-400 dark:text-slate-500 text-sm">No usage recorded.</p>
        <Pagination v-if="usage.length" v-model:page="usagePage" :total="usageTotal" :page-size="usagePageSize" class="mt-2" />
      </template>
    </section>

    <section v-if="item.projects?.length">
      <h2 class="font-semibold mb-2 dark:text-slate-100">Used in projects</h2>
      <ul class="bg-white dark:bg-slate-800 rounded-xl shadow divide-y dark:divide-slate-700 text-sm">
        <li v-for="p in item.projects" :key="p.id" class="flex items-center justify-between px-4 py-2">
          <router-link :to="`/projects/${p.id}`" class="hover:underline dark:text-slate-100">{{ p.name }}</router-link>
          <span v-if="p.qty_used !== null" class="text-slate-500 dark:text-slate-400">{{ p.qty_used }} used</span>
        </li>
      </ul>
    </section>

    <!-- Fetch-from-URL dialog (overlay click / Escape close, dup-dialog
         pattern). Closing mid-poll is allowed — the poll keeps running in
         the background and its outcome still toasts. -->
    <div v-if="showFetch" data-fetch-dialog class="fixed inset-0 z-50 bg-black/50 flex items-center justify-center p-4"
      @click="showFetch = false" @keydown.escape="showFetch = false">
      <form class="bg-white dark:bg-slate-800 rounded-xl shadow-lg w-full max-w-sm p-5" role="dialog" aria-modal="true"
        @click.stop @submit.prevent="submitFetch">
        <h2 class="text-lg font-bold mb-2 dark:text-slate-100">Fetch from URL</h2>
        <label class="block text-sm font-medium dark:text-slate-100">URL
          <input v-model="fetchUrl" data-fetch-url type="url" maxlength="2000" placeholder="https://…"
            class="mt-1 w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal" />
        </label>
        <label class="block text-sm font-medium dark:text-slate-100 mt-2">Mode
          <select v-model="fetchMode" data-fetch-mode
            class="mt-1 w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal bg-white">
            <option value="direct">Direct download</option>
            <option value="archive">Archive (full-page PDF)</option>
          </select>
        </label>
        <div class="flex justify-end gap-2 mt-3">
          <button type="button" class="rounded-lg border dark:border-slate-700 dark:text-slate-100 px-3 py-1.5 text-sm"
            @click="showFetch = false">Cancel</button>
          <button type="submit" data-fetch-submit :disabled="!fetchUrl.trim() || fetchBusy"
            class="rounded-lg bg-slate-900 dark:bg-slate-100 text-white dark:text-slate-900 px-3 py-1.5 text-sm disabled:opacity-50">
            {{ fetchBusy ? 'Fetching…' : 'Fetch' }}</button>
        </div>
      </form>
    </div>

    <!-- Find-document dialog (Brave search; overlay click / Escape close,
         same dup-dialog pattern). Results are server-verified PDFs only;
         picking one closes this dialog and runs the normal fetch flow. -->
    <div v-if="showFindDoc" data-find-dialog class="fixed inset-0 z-50 bg-black/50 flex items-center justify-center p-4"
      @click="showFindDoc = false" @keydown.escape="showFindDoc = false">
      <div class="bg-white dark:bg-slate-800 rounded-xl shadow-lg w-full max-w-sm p-5" role="dialog" aria-modal="true" @click.stop>
        <h2 class="text-lg font-bold mb-2 dark:text-slate-100">Find document</h2>
        <form @submit.prevent="submitFindDoc">
          <label class="block text-sm font-medium dark:text-slate-100">Query
            <input v-model="findQuery" data-find-query maxlength="200" placeholder="part number + datasheet"
              class="mt-1 w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal" />
          </label>
          <div class="flex justify-end gap-2 mt-3">
            <button type="button" class="rounded-lg border dark:border-slate-700 dark:text-slate-100 px-3 py-1.5 text-sm"
              @click="showFindDoc = false">Cancel</button>
            <button type="submit" data-find-search :disabled="findBusy || !findQuery.trim()"
              class="rounded-lg bg-slate-900 dark:bg-slate-100 text-white dark:text-slate-900 px-3 py-1.5 text-sm disabled:opacity-50">
              {{ findBusy ? 'Searching…' : 'Search' }}</button>
          </div>
        </form>
        <div v-if="findResults !== null" class="mt-3 border-t dark:border-slate-700 pt-3">
          <ul v-if="findResults.length" class="space-y-1">
            <li v-for="c in findResults" :key="c.url">
              <button type="button" data-find-result @click="chooseDoc(c)"
                class="w-full text-left rounded-lg px-2 py-1.5 hover:bg-slate-100 dark:hover:bg-slate-700 text-sm">
                <span class="font-medium dark:text-slate-100 break-all">{{ c.title }}</span>
                <span class="block text-xs text-slate-400 dark:text-slate-500">{{ c.host }} · {{ fmtDocSize(c.size) }}</span>
              </button>
            </li>
          </ul>
          <p v-else class="text-sm text-slate-500 dark:text-slate-400">
            {{ findRejected > 0
              ? `No downloadable PDFs found (${findRejected} candidates checked).`
              : 'No downloadable PDFs found — try a more specific part number.' }}
          </p>
          <p v-if="findRejected > 0 && findResults.length" class="mt-2 text-xs text-slate-400 dark:text-slate-500">
            {{ findRejected }} result{{ findRejected === 1 ? '' : 's' }} skipped (not a downloadable PDF).
          </p>
        </div>
      </div>
    </div>

    <ConfirmDialog :open="confirmDelete" :title="`Delete '${item.name}'`"
      message="The item will be moved to trash. You can restore it from Trash before it is permanently purged."
      hint="If you just used some up, 'Log usage' is probably what you want — deleting is for mistakes."
      confirm-label="Delete" @confirm="deleteItem" @cancel="confirmDelete = false" />
  </div>
  <p v-else class="text-slate-400 dark:text-slate-500 text-sm">Loading…</p>
</template>
