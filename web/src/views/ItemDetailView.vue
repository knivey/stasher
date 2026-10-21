<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api/client'
import type { Breadcrumb, Doc, Item, Location, Photo } from '../api/types'
import { useItemsStore } from '../stores/items'
import { toast } from '../utils/toast'
import PhotoGallery from '../components/PhotoGallery.vue'

const route = useRoute()
const router = useRouter()
const itemsStore = useItemsStore()

const item = ref<Item | null>(null)
const breadcrumbs = ref<Breadcrumb[]>([])

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
  if (!item.value || !window.confirm(`Delete "${item.value.name}"?`)) return
  try {
    await api.del(`/api/v1/items/${route.params.id}`)
    toast('Item deleted', 'success')
    router.push('/items')
  } catch (e) {
    toast((e as Error).message, 'error')
  }
}

onMounted(fetchItem)
</script>
<template>
  <div v-if="item" class="space-y-6">
    <div class="flex items-center justify-between gap-2">
      <h1 class="text-2xl font-bold">{{ item.name }}</h1>
      <div class="flex gap-2">
        <router-link :to="`/items/${item.id}/edit`" class="rounded-lg bg-slate-900 text-white px-3 py-1.5 text-sm">Edit</router-link>
        <button @click="deleteItem" class="rounded-lg bg-red-600 text-white px-3 py-1.5 text-sm">Delete</button>
      </div>
    </div>

    <div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
      <span class="font-medium">{{ item.qty }}{{ item.unit ? ` ${item.unit}` : '' }}</span>
      <span v-if="breadcrumbs.length" class="text-slate-500">{{ breadcrumbs.map(b => b.name).join(' › ') }}</span>
      <span :class="item.visibility === 'shared' ? 'bg-emerald-100 text-emerald-700' : 'bg-slate-100 text-slate-600'"
        class="rounded-full px-2 py-0.5 text-xs font-medium">{{ item.visibility === 'shared' ? 'Shared' : 'Private' }}</span>
    </div>

    <p v-if="item.description" class="bg-white rounded-xl shadow p-4 text-sm whitespace-pre-wrap">{{ item.description }}</p>

    <section v-if="item.tags?.length">
      <h2 class="font-semibold mb-2">Tags</h2>
      <div class="flex flex-wrap gap-2">
        <button v-for="t in item.tags" :key="t.id" @click="filterByTag(t.name)"
          class="bg-slate-100 rounded-full px-3 py-1 text-sm hover:bg-slate-200">#{{ t.name }}</button>
      </div>
    </section>

    <section v-if="item.photos?.length">
      <h2 class="font-semibold mb-2">Photos</h2>
      <PhotoGallery :photos="item.photos" :can-edit="true" @delete="deletePhoto" />
    </section>

    <section v-if="item.documents?.length">
      <h2 class="font-semibold mb-2">Documents</h2>
      <ul class="bg-white rounded-xl shadow divide-y text-sm">
        <li v-for="d in item.documents" :key="d.id" class="flex items-center justify-between gap-2 px-4 py-2">
          <a :href="`/files/${d.storage_key}`" target="_blank" class="hover:underline">📄 {{ d.filename }} ({{ Math.ceil(d.size_bytes / 1024) }} KB)</a>
          <button @click="deleteDoc(d)" class="text-red-600">Delete</button>
        </li>
      </ul>
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
  </div>
  <p v-else class="text-slate-400 text-sm">Loading…</p>
</template>
