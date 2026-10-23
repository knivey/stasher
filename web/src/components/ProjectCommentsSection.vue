<script setup lang="ts">
import { ref } from 'vue'
import { addProjectComment, deleteProjectComment, ApiError } from '../api/client'
import type { ProjectComment } from '../api/types'
import { toast } from '../utils/toast'

// Public comment thread on a project. Presentational-async: mutations call
// the client fns, then the parent refetches via `refresh` (no internal
// fetching, no props mutation). Comments arrive oldest-first — rendered in
// the delivered order, no sorting. `mine` is computed per viewer by the
// server, so delete buttons show on the viewer's own entries — plus on ALL
// entries for the project owner (`owner`), whom the API lets moderate any
// comment.
const props = defineProps<{ projectId: number; comments: ProjectComment[]; canPost: boolean; owner: boolean }>()
const emit = defineEmits<{ refresh: [] }>()

const body = ref('')
const saving = ref(false)

async function submitComment() {
  if (saving.value || !body.value.trim()) return
  saving.value = true
  try {
    // Trimmed body: the backend validates 1-2000 chars AFTER its own trim.
    await addProjectComment(props.projectId, body.value.trim())
    toast('Comment added', 'success')
    body.value = ''
    emit('refresh')
  } catch (e) {
    toast(e instanceof ApiError ? e.message : 'Failed to add comment', 'error') // textarea keeps its text
  } finally {
    saving.value = false
  }
}

async function deleteComment(c: ProjectComment) {
  try {
    await deleteProjectComment(props.projectId, c.id)
    toast('Comment deleted', 'success')
    emit('refresh')
  } catch (e) {
    toast((e as Error).message, 'error')
  }
}
</script>
<template>
  <!-- Same visual language as the notes log (api-notes "Project comments") -->
  <section data-comments>
    <h2 class="font-semibold mb-2 dark:text-slate-100">Comments</h2>
    <form v-if="canPost" data-comment-form
      class="bg-white dark:bg-slate-800 rounded-xl shadow p-4 space-y-3 mb-3" @submit.prevent="submitComment">
      <textarea v-model="body" data-comment-body rows="3" maxlength="2000" placeholder="Add a comment…"
        class="w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm"></textarea>
      <div class="flex justify-end">
        <button type="submit" data-comment-add :disabled="saving || !body.trim()"
          class="rounded-lg bg-slate-900 dark:bg-slate-100 text-white dark:text-slate-900 px-3 py-1.5 text-sm disabled:opacity-50">
          {{ saving ? 'Adding…' : 'Add comment' }}</button>
      </div>
    </form>
    <ul v-if="comments.length" class="bg-white dark:bg-slate-800 rounded-xl shadow divide-y dark:divide-slate-700 text-sm">
      <li v-for="c in comments" :key="c.id" class="px-4 py-2">
        <p class="whitespace-pre-line dark:text-slate-100">{{ c.body }}</p>
        <div class="flex items-center justify-between gap-2 mt-1">
          <span class="text-xs text-slate-400 dark:text-slate-500">{{ c.author }} · {{ c.created_at.slice(0, 10) }}</span>
          <button v-if="c.mine || owner" data-comment-delete @click="deleteComment(c)"
            class="text-red-600 dark:text-red-400">Delete</button>
        </div>
      </li>
    </ul>
    <p v-else class="text-slate-400 dark:text-slate-500 text-sm">No comments yet.</p>
  </section>
</template>
