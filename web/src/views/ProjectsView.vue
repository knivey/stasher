<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api/client'
import type { ListResponse, Project } from '../api/types'
import { toast } from '../utils/toast'
import { statusBadgeClass } from '../utils/projects'

const router = useRouter()
const projects = ref<Project[]>([])

// GET /projects returns the full list (no pagination) — {data, total}.
async function fetchProjects() {
  try {
    const res = await api.get<ListResponse<Project>>('/api/v1/projects')
    projects.value = res.data
  } catch (e) {
    toast((e as Error).message, 'error')
  }
}
onMounted(fetchProjects)

// ---- New project ----
const newName = ref('')
const newNotes = ref('')
const newError = ref('')
const creating = ref(false)

async function createProject() {
  if (creating.value || !newName.value.trim()) return
  creating.value = true
  try {
    const body: Record<string, unknown> = { name: newName.value.trim() }
    if (newNotes.value.trim()) body.notes = newNotes.value
    // 201 returns the created project — go straight to its detail page
    // (the list refetches on next mount).
    const p = await api.post<Project>('/api/v1/projects', body)
    toast('Project created', 'success')
    router.push(`/projects/${p.id}`)
  } catch (e) {
    newError.value = (e as Error).message
    toast((e as Error).message, 'error')
  } finally {
    creating.value = false
  }
}
</script>
<template>
  <div class="max-w-4xl space-y-6">
    <h1 class="text-2xl font-bold dark:text-slate-100">Projects</h1>

    <form class="bg-white dark:bg-slate-800 rounded-xl shadow p-4 space-y-3" @submit.prevent="createProject">
      <h2 class="text-sm font-semibold text-slate-700 dark:text-slate-300">New project</h2>
      <label class="block text-sm font-medium dark:text-slate-100">Name
        <input v-model="newName" data-new-name required maxlength="200" placeholder="e.g. NAS rebuild"
          class="mt-1 w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal" @input="newError = ''" />
      </label>
      <textarea v-model="newNotes" data-new-notes rows="2" maxlength="10000" placeholder="Notes (optional)"
        class="w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm"></textarea>
      <p v-if="newError" class="text-xs text-red-600 dark:text-red-400">{{ newError }}</p>
      <button type="submit" :disabled="creating"
        class="rounded-lg bg-slate-900 dark:bg-slate-100 text-white dark:text-slate-900 px-3 py-1.5 text-sm disabled:opacity-50">Create</button>
    </form>

    <p v-if="!projects.length" class="text-slate-400 dark:text-slate-500 text-sm">No projects yet.</p>
    <div v-else class="grid gap-4 sm:grid-cols-2">
      <router-link v-for="p in projects" :key="p.id" :to="`/projects/${p.id}`" data-project-card
        class="block bg-white dark:bg-slate-800 rounded-xl shadow p-4 hover:shadow-md">
        <div class="flex items-center justify-between gap-2">
          <span class="font-medium truncate dark:text-slate-100">{{ p.name }}</span>
          <span data-status-badge class="shrink-0 rounded-full px-2 py-0.5 text-xs font-medium"
            :class="statusBadgeClass(p.status)">{{ p.status }}</span>
        </div>
        <p v-if="p.notes" class="mt-1 text-sm text-slate-500 dark:text-slate-400 whitespace-pre-wrap">{{ p.notes }}</p>
      </router-link>
    </div>
  </div>
</template>
