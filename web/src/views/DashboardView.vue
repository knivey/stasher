<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../api/client'
import type { Item, ListResponse, Location, Project } from '../api/types'
import { toast } from '../utils/toast'

const items = ref<ListResponse<Item>>({ data: [], total: 0 })
const locations = ref<ListResponse<Location>>({ data: [], total: 0 })
const projects = ref<ListResponse<Project>>({ data: [], total: 0 })
const activeProjects = ref<Project[]>([])

onMounted(async () => {
  const jobs: [Promise<unknown>, (v: any) => void, string][] = [
    [api.get('/api/v1/items?page_size=10&sort=created_at&order=desc'), v => items.value = v, 'items'],
    [api.get('/api/v1/locations'), v => locations.value = v, 'locations'],
    [api.get('/api/v1/projects?page_size=200'), v => projects.value = v, 'projects'],
  ]
  await Promise.all(jobs.map(([p, set, label]) => p.then(set).catch(e => toast(`${label}: ${e.message}`, 'error'))))
  activeProjects.value = projects.value.data.filter(p => p.status === 'active')
})
</script>
<template>
  <div class="space-y-6">
    <h1 class="text-2xl font-bold dark:text-slate-100">Dashboard</h1>
    <div class="grid grid-cols-3 gap-3">
      <router-link to="/items" class="bg-white dark:bg-slate-800 rounded-xl shadow p-4 text-center">
        <div class="text-3xl font-bold dark:text-slate-100">{{ items.total }}</div><div class="text-sm text-slate-500 dark:text-slate-400">Items</div></router-link>
      <router-link to="/locations" class="bg-white dark:bg-slate-800 rounded-xl shadow p-4 text-center">
        <div class="text-3xl font-bold dark:text-slate-100">{{ locations.total }}</div><div class="text-sm text-slate-500 dark:text-slate-400">Locations</div></router-link>
      <router-link to="/projects" class="bg-white dark:bg-slate-800 rounded-xl shadow p-4 text-center">
        <div class="text-3xl font-bold dark:text-slate-100">{{ projects.total }}</div><div class="text-sm text-slate-500 dark:text-slate-400">Projects</div></router-link>
    </div>
    <section>
      <h2 class="font-semibold mb-2 dark:text-slate-100">Recently added</h2>
      <ul class="bg-white dark:bg-slate-800 rounded-xl shadow divide-y dark:divide-slate-700">
        <li v-for="i in items.data" :key="i.id">
          <router-link :to="`/items/${i.id}`" class="flex justify-between px-4 py-2 hover:bg-slate-50 dark:hover:bg-slate-700/50">
            <span class="dark:text-slate-100">{{ i.name }}</span><span class="text-slate-400 dark:text-slate-500 text-sm">{{ i.created_at?.slice(0, 10) }}</span>
          </router-link>
        </li>
      </ul>
    </section>
    <section>
      <h2 class="font-semibold mb-2 dark:text-slate-100">Active projects</h2>
      <div class="flex flex-wrap gap-2">
        <router-link v-for="p in activeProjects" :key="p.id" :to="`/projects/${p.id}`"
          class="bg-white dark:bg-slate-800 rounded-lg shadow px-3 py-1.5 dark:text-slate-100">{{ p.name }}</router-link>
        <span v-if="!activeProjects.length" class="text-slate-400 dark:text-slate-500 text-sm">None</span>
      </div>
    </section>
  </div>
</template>
