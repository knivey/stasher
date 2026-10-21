<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { api, ApiError } from '../api/client'
import type { ListResponse, TrashEntry } from '../api/types'
import { toast } from '../utils/toast'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import Pagination from '../components/Pagination.vue'

// Local list state — trash is its own dataset, not part of the items store.
const entries = ref<TrashEntry[]>([])
const total = ref(0)
const loading = ref(false)
const page = ref(1)
const pageSize = 20

async function fetch() {
  loading.value = true
  try {
    const res = await api.get<ListResponse<TrashEntry>>(`/api/v1/trash?page=${page.value}&page_size=${pageSize}`)
    entries.value = res.data
    total.value = res.total
    // Backend quirk (docs/api-notes.md): a past-the-end page returns
    // {data: [], total: 0} — clamp back one page instead of showing a lying
    // "Trash is empty." while earlier pages still hold entries. The page
    // watcher above refetches; at page 1 this never fires, so a genuinely
    // empty trash lands in the empty state (no loop).
    if (!entries.value.length && page.value > 1) { page.value--; return }
  } catch (e) {
    toast((e as Error).message, 'error')
  } finally {
    loading.value = false
  }
}

watch(page, () => { fetch() })
onMounted(fetch)

// Restore: busy tracked per entry id — several rows can be in flight at once
// and each stays disabled until its own request settles (same reasoning as
// SharedView's copy guard).
const restoring = ref(new Set<number>())

async function restore(entry: TrashEntry) {
  if (restoring.value.has(entry.id)) return
  restoring.value.add(entry.id)
  try {
    await api.post(`/api/v1/trash/${entry.id}/restore`)
    toast('Item restored', 'success')
    await fetch()
  } catch (e) {
    toast((e as Error).message, 'error')
    // 404 = purged meanwhile (GET /trash sweeps expired entries server-side,
    // so the row is stale) — refetch, like SharedView's copy-404 branch.
    if (e instanceof ApiError && e.status === 404) fetch()
  } finally {
    restoring.value.delete(entry.id)
  }
}

// Purge: irreversible — the ConfirmDialog guards the DELETE. Closing the
// dialog first (like ItemDetailView's delete) prevents a double-confirm.
const purgeTarget = ref<TrashEntry | null>(null)

async function purge() {
  const e = purgeTarget.value
  if (!e) return
  purgeTarget.value = null
  try {
    await api.del(`/api/v1/trash/${e.id}`)
    toast('Purged', 'success')
    await fetch()
  } catch (err) {
    toast((err as Error).message, 'error')
  }
}

const confirmEmpty = ref(false)

async function emptyTrash() {
  confirmEmpty.value = false
  try {
    await api.del('/api/v1/trash')
    toast('Trash emptied', 'success')
    await fetch()
  } catch (e) {
    toast((e as Error).message, 'error')
  }
}
</script>
<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between gap-2">
      <h1 class="text-2xl font-bold">Trash</h1>
      <button v-if="entries.length" @click="confirmEmpty = true"
        class="rounded-lg bg-red-600 text-white px-3 py-1.5 text-sm">Empty trash</button>
    </div>

    <p v-if="loading" class="text-slate-400 text-sm">Loading…</p>

    <div v-else-if="entries.length" data-cards class="space-y-2">
      <div v-for="e in entries" :key="e.id"
        class="bg-white rounded-xl shadow px-4 py-3 flex items-center justify-between gap-2">
        <div>
          <div class="font-medium">{{ e.name }}</div>
          <!-- No <img> here on purpose: thumb_key 404s while trashed. -->
          <div class="text-xs text-slate-500">
            qty {{ e.qty }}<span v-if="e.photo_count"> · 📷 {{ e.photo_count }}</span>
            · trashed {{ e.trashed_at.slice(0, 10) }}
            · <span v-if="e.purges_at">purges {{ e.purges_at.slice(0, 10) }}</span><span v-else>kept until purged</span>
          </div>
        </div>
        <div class="flex gap-2 shrink-0">
          <button @click="restore(e)" :disabled="restoring.has(e.id)"
            class="rounded-lg bg-slate-900 text-white px-3 py-1.5 text-sm disabled:opacity-40">
            {{ restoring.has(e.id) ? 'Restoring…' : 'Restore' }}
          </button>
          <button @click="purgeTarget = e" class="rounded-lg border border-red-300 text-red-600 px-3 py-1.5 text-sm">
            Purge
          </button>
        </div>
      </div>
    </div>

    <p v-else class="bg-white rounded-xl shadow p-6 text-center text-slate-400 text-sm">Trash is empty.</p>

    <Pagination v-if="entries.length" v-model:page="page" :total="total" :page-size="pageSize" />

    <ConfirmDialog :open="!!purgeTarget" title="Purge item"
      message="Permanently delete this item and its files? This cannot be undone."
      confirm-label="Purge" @confirm="purge" @cancel="purgeTarget = null" />
    <ConfirmDialog :open="confirmEmpty" title="Empty trash"
      :message="`Permanently delete all ${total} trashed item${total === 1 ? '' : 's'} and their files? This cannot be undone.`"
      confirm-label="Empty trash" @confirm="emptyTrash" @cancel="confirmEmpty = false" />
  </div>
</template>
