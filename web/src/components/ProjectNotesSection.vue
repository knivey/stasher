<script setup lang="ts">
import { ref } from 'vue'
import { addProjectNote, deleteProjectNote, ApiError } from '../api/client'
import type { ProjectNote } from '../api/types'
import { toast } from '../utils/toast'

// Owner-only notes log for a project — the same timestamped add + delete
// thread as ItemDetailView's notes. Presentational-async: mutations call the
// client fns, then the parent refetches via `refresh` (no internal fetching,
// no props mutation; the list renders in the order delivered — newest first).
const props = defineProps<{ projectId: number; notes: ProjectNote[] }>()
const emit = defineEmits<{ refresh: [] }>()

const body = ref('')
const saving = ref(false)

async function submitNote() {
  if (saving.value || !body.value.trim()) return
  saving.value = true
  try {
    // Trimmed body: the backend validates 1-2000 chars AFTER its own trim.
    await addProjectNote(props.projectId, body.value.trim())
    toast('Note added', 'success')
    body.value = ''
    emit('refresh')
  } catch (e) {
    toast(e instanceof ApiError ? e.message : 'Failed to add note', 'error') // textarea keeps its text
  } finally {
    saving.value = false
  }
}

async function deleteNote(n: ProjectNote) {
  try {
    await deleteProjectNote(props.projectId, n.id)
    toast('Note deleted', 'success')
    emit('refresh')
  } catch (e) {
    toast((e as Error).message, 'error')
  }
}
</script>
<template>
  <!-- Mirrors ItemDetailView's notes section (api-notes "Project notes log") -->
  <section data-notes>
    <h2 class="font-semibold mb-2 dark:text-slate-100">Notes</h2>
    <form data-note-form class="bg-white dark:bg-slate-800 rounded-xl shadow p-4 space-y-3 mb-3" @submit.prevent="submitNote">
      <textarea v-model="body" data-note-body rows="3" maxlength="2000" placeholder="Add a note…"
        class="w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm"></textarea>
      <div class="flex justify-end">
        <button type="submit" data-note-add :disabled="saving || !body.trim()"
          class="rounded-lg bg-slate-900 dark:bg-slate-100 text-white dark:text-slate-900 px-3 py-1.5 text-sm disabled:opacity-50">
          {{ saving ? 'Adding…' : 'Add note' }}</button>
      </div>
    </form>
    <ul v-if="notes.length" class="bg-white dark:bg-slate-800 rounded-xl shadow divide-y dark:divide-slate-700 text-sm">
      <li v-for="n in notes" :key="n.id" class="px-4 py-2">
        <p class="whitespace-pre-line dark:text-slate-100">{{ n.body }}</p>
        <div class="flex items-center justify-between gap-2 mt-1">
          <span class="text-xs text-slate-400 dark:text-slate-500">{{ n.created_at.slice(0, 10) }}</span>
          <button data-note-delete @click="deleteNote(n)" class="text-red-600 dark:text-red-400">Delete</button>
        </div>
      </li>
    </ul>
    <p v-else class="text-slate-400 dark:text-slate-500 text-sm">No notes yet.</p>
  </section>
</template>
