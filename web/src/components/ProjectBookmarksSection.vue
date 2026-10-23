<script setup lang="ts">
import { onUnmounted, ref } from 'vue'
import { addProjectBookmark, archiveProjectBookmark, deleteProjectBookmark, api, ApiError } from '../api/client'
import type { FetchJob, ProjectBookmark } from '../api/types'
import { toast } from '../utils/toast'
import ConfirmDialog from './ConfirmDialog.vue'
import { isVideoHost } from '../utils/videoHosts'

// URL bookmarks with opt-in page→PDF archiving (api-notes "Project
// bookmarks" / "Bookmark archiving"). Presentational-async like
// ProjectNotesSection: mutations call the client fns, then the parent
// refetches via `refresh`. Bookmark titles are auto-computed server-side
// (best-effort). Sharees see links + the archived PDF; every mutation is
// owner-only server-side.
const props = defineProps<{ projectId: number; bookmarks: ProjectBookmark[]; owner: boolean }>()
const emit = defineEmits<{ refresh: [] }>()

// ---- Add (the url follows the shared external-URL contract; the server
// validates and auto-titles) ----
const url = ref('')
const adding = ref(false)

async function addBookmark() {
  const u = url.value.trim()
  if (adding.value || !u) return
  adding.value = true
  try {
    await addProjectBookmark(props.projectId, u)
    toast('Bookmark added', 'success')
    url.value = ''
    emit('refresh')
  } catch (e) {
    toast(e instanceof ApiError ? e.message : 'Failed to add bookmark', 'error') // input keeps its text
  } finally {
    adding.value = false
  }
}

// Row meta hostname (safe: unparseable urls fall back to the raw string).
function hostOf(u: string): string {
  try {
    return new URL(u).hostname
  } catch {
    return u
  }
}

// ---- Archive: POST → poll /jobs/{id} → refresh on done ----
// Cadence mirrors ItemDetailView's runFetchJob: 2s between polls, 45 tries
// = the server's 90s overall job deadline. The FIRST poll fires
// immediately — the 202 already queued the job, a fresh job just reports
// 'running', and it keeps the loop testable with flushPromises alone.
const ARCHIVE_POLL_MS = 2000
const ARCHIVE_POLL_TRIES = 45

const archiving = ref(new Set<number>()) // per-bookmark in-flight state
let timer: ReturnType<typeof setTimeout> | null = null
let cancelled = false

onUnmounted(() => {
  cancelled = true
  if (timer !== null) clearTimeout(timer)
})

// Resolves true only on done — the caller refreshes then. Failure modes
// (failed job, restart 404, timeout) toast their own reason and resolve
// false: failed archives change nothing server-side (api-notes).
async function pollJob(jobId: string): Promise<boolean> {
  for (let i = 0; i < ARCHIVE_POLL_TRIES; i++) {
    if (i > 0) await new Promise<void>(r => { timer = setTimeout(r, ARCHIVE_POLL_MS) })
    if (cancelled) return false // unmounted: timer cleared, abandon the loop
    try {
      const job = await api.get<FetchJob>(`/api/v1/jobs/${jobId}`)
      if (job.status === 'done') {
        toast('Bookmark archived', 'success')
        return true
      }
      if (job.status === 'failed') {
        toast(job.error ?? 'archive failed', 'error')
        return false
      }
    } catch (e) {
      // Jobs are in-memory only: a 404 mid-poll means the server restarted
      // and the archive was NOT created (api-notes). Network blips rethrow
      // to the outer handler.
      if (e instanceof ApiError && e.status === 404) {
        toast('archive interrupted', 'error')
        return false
      }
      throw e
    }
  }
  toast('archive timed out — check the bookmark in a moment', 'error')
  return false
}

// ---- Video-host confirm: archiving captures the PAGE as a PDF, not the
// video — ask before spending a chromium render on it. Decline = no POST. ----
const videoOpen = ref(false)
let settleConfirm: ((ok: boolean) => void) | null = null

function confirmVideo(): Promise<boolean> {
  videoOpen.value = true
  return new Promise(resolve => { settleConfirm = resolve })
}

function settle(ok: boolean) {
  videoOpen.value = false
  settleConfirm?.(ok)
  settleConfirm = null
}

async function archive(b: ProjectBookmark) {
  if (archiving.value.has(b.id)) return
  if (isVideoHost(b.url) && !(await confirmVideo())) return
  archiving.value.add(b.id)
  try {
    const { job_id } = await archiveProjectBookmark(props.projectId, b.id)
    // Done jobs carry no document_id (item-fetch only) — refetch the
    // project to see the archive.
    if (await pollJob(job_id)) emit('refresh')
  } catch (e) {
    toast((e as Error).message, 'error')
  } finally {
    archiving.value.delete(b.id) // the row returns to its Archive state
  }
}
// ---- Delete (inline, no dialog — matching the file rows' delete
// affordance; the archived file row AND its bytes are retired server-side) ----
const deleting = ref(new Set<number>())

async function removeBookmark(b: ProjectBookmark) {
  if (deleting.value.has(b.id)) return
  deleting.value.add(b.id)
  try {
    await deleteProjectBookmark(props.projectId, b.id)
    toast('Bookmark deleted', 'success')
    emit('refresh')
  } catch (e) {
    toast((e as Error).message, 'error')
  } finally {
    deleting.value.delete(b.id)
  }
}
</script>
<template>
  <!-- Mirrors ItemDetailView's external-fetch affordances -->
  <section data-bookmarks>
    <h2 class="font-semibold mb-2 dark:text-slate-100">Bookmarks</h2>

    <form v-if="owner" data-bm-form
      class="bg-white dark:bg-slate-800 rounded-xl shadow p-4 space-y-3 mb-3" @submit.prevent="addBookmark">
      <label class="block text-sm font-medium dark:text-slate-100">URL
        <input v-model="url" data-bm-url type="url" maxlength="2000" placeholder="https://…"
          class="mt-1 w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal" />
      </label>
      <div class="flex justify-end">
        <button type="submit" data-bm-add :disabled="adding || !url.trim()"
          class="rounded-lg bg-slate-900 dark:bg-slate-100 text-white dark:text-slate-900 px-3 py-1.5 text-sm disabled:opacity-50">
          {{ adding ? 'Adding…' : 'Add bookmark' }}</button>
      </div>
    </form>

    <ul v-if="bookmarks.length" class="bg-white dark:bg-slate-800 rounded-xl shadow divide-y dark:divide-slate-700 text-sm">
      <li v-for="b in bookmarks" :key="b.id" class="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2">
        <a :href="b.url" target="_blank" rel="noopener" class="hover:underline dark:text-slate-100 break-all">{{ b.title }}</a>
        <span class="text-xs text-slate-400 dark:text-slate-500">{{ hostOf(b.url) }} · {{ b.created_at.slice(0, 10) }}</span>
        <span class="ml-auto flex items-center gap-3 shrink-0">
          <a v-if="b.archived_file" :href="`/files/${b.archived_file.storage_key}`" target="_blank" rel="noopener"
            class="text-blue-600 dark:text-blue-400 hover:underline">View PDF</a>
          <button v-if="owner" data-archive :disabled="archiving.has(b.id)"
            class="text-slate-600 dark:text-slate-300 hover:underline disabled:opacity-50" @click="archive(b)">
            {{ archiving.has(b.id) ? 'Archiving…' : b.archived_file ? 'Re-archive' : 'Archive' }}</button>
          <button v-if="owner" data-bm-delete :disabled="deleting.has(b.id)"
            class="shrink-0 text-red-600 dark:text-red-400 disabled:opacity-50"
            @click="removeBookmark(b)">Delete</button>
        </span>
      </li>
    </ul>
    <p v-else class="text-slate-400 dark:text-slate-500 text-sm">No bookmarks yet.</p>

    <!-- Wrapper anchor exists exactly while the dialog is open (ConfirmDialog
         renders no test anchors of its own). -->
    <div v-if="videoOpen" data-video-confirm>
      <ConfirmDialog :open="videoOpen" title="Video link" confirm-label="Archive anyway"
        message="Archiving captures the page as a PDF, not the video — archive anyway?"
        @confirm="settle(true)" @cancel="settle(false)" />
    </div>
  </section>
</template>
