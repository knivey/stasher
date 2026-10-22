<script setup lang="ts">
import { onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api/client'
import type { Breadcrumb, Doc, Item, LedgerEntry, ListResponse, Location, Photo, Project } from '../api/types'
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
  used: 'bg-slate-200 text-slate-700',
  sold: 'bg-blue-100 text-blue-700',
  lost: 'bg-red-100 text-red-700',
  found: 'bg-emerald-100 text-emerald-700',
  restock: 'bg-teal-100 text-teal-700',
  disposed: 'bg-gray-200 text-gray-600',
  adjustment: 'bg-slate-100 text-slate-500',
}

function fmtDelta(d: number): string {
  return d > 0 ? `+${d}` : d < 0 ? `−${Math.abs(d)}` : '±0'
}

// Ledger rows carry project_id only — the chip label falls back through the
// item's linked projects, then the log form's list, then a bare id.
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
</script>
<template>
  <div v-if="item" class="space-y-6">
    <div class="flex items-center justify-between gap-2">
      <h1 class="text-2xl font-bold">{{ item.name }}</h1>
      <div class="flex gap-2">
        <router-link :to="`/items/${item.id}/edit`" class="rounded-lg bg-slate-900 text-white px-3 py-1.5 text-sm">Edit</router-link>
        <button @click="openLogForm" class="rounded-lg border px-3 py-1.5 text-sm">Log usage</button>
        <button @click="confirmDelete = true" class="rounded-lg bg-red-600 text-white px-3 py-1.5 text-sm">Delete</button>
      </div>
    </div>

    <div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
      <span class="font-medium">{{ item.qty }}{{ item.unit ? ` ${item.unit}` : '' }}</span>
      <span v-if="breadcrumbs.length" class="text-slate-500">{{ breadcrumbs.map(b => b.name).join(' › ') }}</span>
      <span :class="item.visibility === 'shared' ? 'bg-emerald-100 text-emerald-700' : 'bg-slate-100 text-slate-600'"
        class="rounded-full px-2 py-0.5 text-xs font-medium">{{ item.visibility === 'shared' ? 'Shared' : 'Private' }}</span>
      <span v-if="item.disposed" data-disposed class="rounded-full bg-gray-200 text-gray-500 px-2 py-0.5 text-xs font-medium">disposed</span>
    </div>

    <!-- Inline usage log form (not a route) -->
    <form v-if="showLogForm" data-log-form class="bg-white rounded-xl shadow p-4 space-y-3" @submit.prevent="submitUsage">
      <h2 class="text-sm font-semibold">Log usage</h2>
      <div class="flex gap-4">
        <label class="flex-1 block text-sm font-medium">Kind
          <select v-model="logForm.kind" data-log-kind class="mt-1 w-full border rounded-lg px-2 py-1.5 text-sm font-normal bg-white">
            <option v-for="k in usageKinds" :key="k" :value="k">{{ k }}</option>
          </select>
        </label>
        <label v-if="logForm.kind !== 'disposed'" class="flex-1 block text-sm font-medium">Delta
          <input v-model.number="logForm.delta" data-log-delta type="number" step="any" required
            class="mt-1 w-full border rounded-lg px-2 py-1.5 text-sm font-normal" />
        </label>
      </div>
      <p v-if="logForm.kind === 'disposed'" class="text-xs text-slate-500">Sets quantity to 0 and marks the item disposed.</p>
      <label class="block text-sm font-medium">Project <span class="font-normal text-slate-400">(optional)</span>
        <select v-model="logForm.project_id" data-log-project class="mt-1 w-full border rounded-lg px-2 py-1.5 text-sm font-normal bg-white">
          <option :value="0">None</option>
          <option v-for="p in logProjects" :key="p.id" :value="p.id">{{ p.name }}</option>
        </select>
      </label>
      <label class="block text-sm font-medium">Note <span class="font-normal text-slate-400">(optional)</span>
        <input v-model="logForm.note" data-log-note maxlength="10000" placeholder="What happened…"
          class="mt-1 w-full border rounded-lg px-2 py-1.5 text-sm font-normal" />
      </label>
      <div class="flex justify-end gap-2">
        <button type="button" @click="showLogForm = false" class="rounded-lg border px-3 py-1.5 text-sm">Cancel</button>
        <button type="submit" :disabled="logSaving"
          class="rounded-lg bg-slate-900 text-white px-3 py-1.5 text-sm disabled:opacity-50">
          {{ logSaving ? 'Logging…' : 'Log' }}</button>
      </div>
    </form>

    <p v-if="item.description" class="bg-white rounded-xl shadow p-4 text-sm whitespace-pre-wrap">{{ item.description }}</p>

    <section v-if="item.tags?.length">
      <h2 class="font-semibold mb-2">Tags</h2>
      <div class="flex flex-wrap gap-2">
        <button v-for="t in item.tags" :key="t.id" @click="filterByTag(t.name)"
          class="bg-slate-100 rounded-full px-3 py-1 text-sm hover:bg-slate-200">#{{ t.name }}</button>
      </div>
    </section>

    <section>
      <div class="flex items-center justify-between mb-2">
        <h2 class="font-semibold">Photos</h2>
        <FileUpload kind="photo" :item-id="item.id" @uploaded="fetchItem" />
      </div>
      <PhotoGallery v-if="item.photos?.length" :photos="item.photos" :can-edit="true" @delete="deletePhoto" />
    </section>

    <section>
      <div class="flex items-center justify-between mb-2">
        <h2 class="font-semibold">Documents</h2>
        <FileUpload kind="doc" :item-id="item.id" @uploaded="fetchItem" />
      </div>
      <ul v-if="item.documents?.length" class="bg-white rounded-xl shadow divide-y text-sm">
        <li v-for="d in item.documents" :key="d.id" class="flex items-center justify-between gap-2 px-4 py-2">
          <a :href="`/files/${d.storage_key}`" target="_blank" rel="noopener" class="hover:underline">📄 {{ d.filename }} ({{ Math.ceil(d.size_bytes / 1024) }} KB)</a>
          <button @click="deleteDoc(d)" class="text-red-600">Delete</button>
        </li>
      </ul>
    </section>

    <!-- Usage history (newest first; the ledger is written by usage posts and
         qty-changing PATCHes alike — see api-notes "Usage ledger") -->
    <section data-usage>
      <h2 class="font-semibold mb-2">History</h2>
      <p v-if="usageLoading" class="text-slate-400 text-sm">Loading…</p>
      <template v-else>
        <ul v-if="usage.length" class="bg-white rounded-xl shadow divide-y text-sm">
          <li v-for="e in usage" :key="e.id" class="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2">
            <span class="font-medium tabular-nums">{{ fmtDelta(e.delta) }}</span>
            <span data-kind-badge class="rounded-full px-2 py-0.5 text-xs font-medium" :class="kindBadgeClass[e.kind]">{{ e.kind }}</span>
            <router-link v-if="e.project_id" :to="`/projects/${e.project_id}`"
              class="bg-slate-100 rounded-full px-2 py-0.5 text-xs hover:bg-slate-200">🧵 {{ projectName(e.project_id) }}</router-link>
            <span v-if="e.note" class="text-slate-500">“{{ e.note }}”</span>
            <span class="text-slate-500">→ {{ e.resulting_qty }} left</span>
            <span class="ml-auto text-xs text-slate-400">{{ e.created_at.slice(0, 10) }}</span>
          </li>
        </ul>
        <p v-else class="text-slate-400 text-sm">No usage recorded.</p>
        <Pagination v-if="usage.length" v-model:page="usagePage" :total="usageTotal" :page-size="usagePageSize" class="mt-2" />
      </template>
    </section>

    <section v-if="item.projects?.length">
      <h2 class="font-semibold mb-2">Used in projects</h2>
      <ul class="bg-white rounded-xl shadow divide-y text-sm">
        <li v-for="p in item.projects" :key="p.id" class="flex items-center justify-between px-4 py-2">
          <router-link :to="`/projects/${p.id}`" class="hover:underline">{{ p.name }}</router-link>
          <span v-if="p.qty_used !== null" class="text-slate-500">{{ p.qty_used }} used</span>
        </li>
      </ul>
    </section>

    <ConfirmDialog :open="confirmDelete" :title="`Delete '${item.name}'`"
      message="The item will be moved to trash. You can restore it from Trash before it is permanently purged."
      confirm-label="Delete" @confirm="deleteItem" @cancel="confirmDelete = false" />
  </div>
  <p v-else class="text-slate-400 text-sm">Loading…</p>
</template>
