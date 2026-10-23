<script setup lang="ts">
import { ref } from 'vue'
import { deleteProjectFile, uploadProjectFile } from '../api/client'
import type { ProjectFile } from '../api/types'
import { toast } from '../utils/toast'

// Reference materials for a project — any file type (api-notes "Project
// reference materials"). Presentational-async like ProjectNotesSection:
// mutations call the client fns, then the parent refetches via `refresh`
// (no internal fetching, no props mutation). Sharees see rows + download
// only; every mutation is owner-only server-side.
const props = defineProps<{ projectId: number; files: ProjectFile[]; owner: boolean }>()
const emit = defineEmits<{ refresh: [] }>()

// Mirrors ItemDetailView's fmtDocSize convention (MB with one decimal,
// KB rounded up; defensive negative → '—').
function humanSize(size: number): string {
  if (size < 0) return '—'
  if (size >= 1024 * 1024) return `${(size / (1024 * 1024)).toFixed(1)} MB`
  return `${Math.ceil(size / 1024)} KB`
}

function isImage(f: ProjectFile): boolean {
  return f.mime_type.startsWith('image/')
}

// ---- Upload (hidden input + button, same pattern as FileUpload) ----
const uploading = ref(false)
const input = ref<HTMLInputElement | null>(null)

function pick() {
  input.value?.click()
}

async function onChange(e: Event) {
  const el = e.target as HTMLInputElement
  const files = Array.from(el.files ?? [])
  el.value = '' // allow re-picking the same file
  if (!files.length) return
  uploading.value = true
  try {
    for (const file of files) {
      try {
        await uploadProjectFile(props.projectId, file)
        emit('refresh')
      } catch (err) {
        toast((err as Error).message, 'error')
      }
    }
  } finally {
    uploading.value = false
  }
}

// ---- Delete (inline, no dialog — files are cheap to re-upload, matching
// ItemDetailView's document delete) ----
async function removeFile(f: ProjectFile) {
  try {
    await deleteProjectFile(props.projectId, f.id)
    toast('File deleted', 'success')
    emit('refresh')
  } catch (e) {
    toast((e as Error).message, 'error')
  }
}
</script>
<template>
  <!-- Mirrors ItemDetailView's documents section -->
  <section data-materials>
    <div class="flex items-center justify-between mb-2">
      <h2 class="font-semibold dark:text-slate-100">Materials</h2>
      <span v-if="owner" class="inline-flex">
        <input ref="input" data-file-upload type="file" class="hidden" multiple @change="onChange" />
        <button type="button" :disabled="uploading"
          class="rounded-lg border dark:border-slate-700 dark:text-slate-100 px-3 py-1.5 text-sm disabled:opacity-50"
          @click="pick">Add file{{ uploading ? '…' : '' }}</button>
      </span>
    </div>
    <ul v-if="files.length" class="bg-white dark:bg-slate-800 rounded-xl shadow divide-y dark:divide-slate-700 text-sm">
      <li v-for="f in files" :key="f.id" class="flex items-center gap-3 px-4 py-2">
        <!-- Images: derived thumb (fallback to the original when thumb
             generation failed); every row click-through links the ORIGINAL
             storage_key, never the thumb. -->
        <a v-if="isImage(f)" :href="`/files/${f.storage_key}`" target="_blank" rel="noopener" class="shrink-0">
          <img data-file-thumb :src="`/files/${f.thumb_key ?? f.storage_key}`" :alt="f.filename"
            class="h-12 w-12 object-cover rounded-lg bg-slate-100 dark:bg-slate-700" />
        </a>
        <span v-else class="shrink-0" aria-hidden="true">📄</span>
        <a :href="`/files/${f.storage_key}`" target="_blank" rel="noopener"
          class="hover:underline dark:text-slate-100 break-all">{{ f.filename }}</a>
        <span class="text-xs text-slate-400 dark:text-slate-500 shrink-0">{{ humanSize(f.size_bytes) }}</span>
        <button v-if="owner" data-file-delete
          class="ml-auto shrink-0 text-red-600 dark:text-red-400" @click="removeFile(f)">Delete</button>
      </li>
    </ul>
    <p v-else class="text-slate-400 dark:text-slate-500 text-sm">No files yet.</p>
  </section>
</template>
