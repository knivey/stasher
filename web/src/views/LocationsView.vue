<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '../api/client'
import type { ListResponse, Location } from '../api/types'
import { toast } from '../utils/toast'
import { locationTreeRows, type LocationRow } from '../utils/locations'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import LocationSelect from '../components/LocationSelect.vue'

const locations = ref<Location[]>([])
// The tree indentation IS the hierarchy display here — no breadcrumb fetch.
const rows = computed<LocationRow[]>(() => locationTreeRows(locations.value))

async function fetchLocations() {
  try {
    // Locations ignore pagination — the full list comes back (param unused).
    const res = await api.get<ListResponse<Location>>('/api/v1/locations?page_size=200')
    locations.value = res.data
  } catch (e) {
    toast((e as Error).message, 'error')
  }
}
onMounted(fetchLocations)

// ---- Add ----
const addName = ref('')
const addParent = ref(0) // 0 = root ("No location" in the select)
const addError = ref('')
const adding = ref(false)

async function addLocation() {
  if (adding.value || !addName.value.trim()) return
  adding.value = true
  try {
    const body: Record<string, unknown> = { name: addName.value.trim() }
    if (addParent.value) body.parent_id = addParent.value
    await api.post('/api/v1/locations', body)
    toast('Location added', 'success')
    addName.value = ''
    addError.value = ''
    await fetchLocations()
  } catch (e) {
    // 409 location_exists lands here; show the backend message inline too.
    addError.value = (e as Error).message
    toast((e as Error).message, 'error')
  } finally {
    adding.value = false
  }
}

// ---- Inline edit (rename + move + notes) ----
const editingId = ref<number | null>(null)
const editName = ref('')
const editParent = ref(0)
const editNotes = ref('')
const editError = ref('')
const saving = ref(false)
// Snapshot for the diff — PATCH sends each field only when it changed
// (api-notes: omitted keeps; parent_id can never be cleared).
let editOrig = { name: '', parentId: 0, notes: '' }

function startEdit(row: LocationRow) {
  editingId.value = row.loc.id
  editName.value = row.loc.name
  editParent.value = row.loc.parent_id ?? 0
  editNotes.value = row.loc.notes ?? ''
  editOrig = { name: row.loc.name, parentId: row.loc.parent_id ?? 0, notes: row.loc.notes ?? '' }
  editError.value = ''
}

function cancelEdit() {
  editingId.value = null
  editError.value = ''
}

// The location itself plus all of its descendants — moving under any of
// those would create a cycle (the API would reject with 400 anyway).
function excludedParentIds(loc: Location): number[] {
  const childrenOf = new Map<number, number[]>()
  for (const l of locations.value) {
    if (l.parent_id === null) continue
    childrenOf.set(l.parent_id, [...(childrenOf.get(l.parent_id) ?? []), l.id])
  }
  const excluded = [loc.id]
  const seen = new Set(excluded)
  const queue = [loc.id]
  while (queue.length) {
    for (const child of childrenOf.get(queue.shift()!) ?? []) {
      if (seen.has(child)) continue
      seen.add(child)
      excluded.push(child)
      queue.push(child)
    }
  }
  return excluded
}

async function saveEdit(loc: Location) {
  if (saving.value) return
  const body: Record<string, unknown> = {}
  if (editName.value.trim() !== editOrig.name) body.name = editName.value.trim()
  if (editParent.value !== editOrig.parentId) body.parent_id = editParent.value
  // Notes are sent ONLY when the user edited them (dirty = differs from snapshot).
  if (editNotes.value !== editOrig.notes) body.notes = editNotes.value
  if (!Object.keys(body).length) {
    cancelEdit()
    return
  }
  saving.value = true
  try {
    await api.patch(`/api/v1/locations/${loc.id}`, body)
    toast('Saved', 'success')
    cancelEdit()
    await fetchLocations()
  } catch (e) {
    // 409 location_exists / 400 cycle messages show inline (near the fields)
    // and as a toast.
    editError.value = (e as Error).message
    toast((e as Error).message, 'error')
  } finally {
    saving.value = false
  }
}

// ---- Delete ----
const deleteTarget = ref<Location | null>(null)

async function deleteLocation() {
  const target = deleteTarget.value
  if (!target) return
  deleteTarget.value = null
  try {
    await api.del(`/api/v1/locations/${target.id}`)
    toast('Location deleted', 'success')
    if (editingId.value === target.id) cancelEdit()
    await fetchLocations()
  } catch (e) {
    toast((e as Error).message, 'error')
  }
}
</script>
<template>
  <div class="max-w-3xl space-y-6">
    <h1 class="text-2xl font-bold dark:text-slate-100">Locations</h1>

    <form class="bg-white dark:bg-slate-800 rounded-xl shadow p-4 flex flex-wrap items-end gap-3" @submit.prevent="addLocation">
      <label class="flex-1 min-w-48 block text-sm font-medium dark:text-slate-100">Add location
        <input v-model="addName" data-add-name required maxlength="200" placeholder="Name"
          class="mt-1 w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal" @input="addError = ''" />
        <p v-if="addError" class="mt-1 text-xs text-red-600 dark:text-red-400">{{ addError }}</p>
      </label>
      <div class="w-56 text-sm font-medium dark:text-slate-100">Parent
        <LocationSelect v-model="addParent" :locations="locations" class="mt-1" />
      </div>
      <button type="submit" :disabled="adding"
        class="rounded-lg bg-slate-900 dark:bg-slate-100 text-white dark:text-slate-900 px-3 py-1.5 text-sm font-normal disabled:opacity-50">Add</button>
    </form>

    <p v-if="!rows.length" class="text-slate-400 dark:text-slate-500 text-sm">No locations yet.</p>
    <ul v-else class="bg-white dark:bg-slate-800 rounded-xl shadow divide-y dark:divide-slate-700 text-sm">
      <li v-for="r in rows" :key="r.loc.id" class="px-4 py-2">
        <div v-if="editingId !== r.loc.id" class="flex items-center justify-between gap-2">
          <div class="min-w-0">
            <span data-loc-name class="font-medium dark:text-slate-100" :style="{ paddingLeft: r.depth * 1.5 + 'rem' }">{{ r.loc.name }}</span>
            <p v-if="r.loc.notes" class="text-slate-500 dark:text-slate-400 whitespace-pre-wrap" :style="{ paddingLeft: r.depth * 1.5 + 'rem' }">{{ r.loc.notes }}</p>
          </div>
          <span class="flex gap-2 shrink-0">
            <button data-edit class="text-slate-600 dark:text-slate-300 hover:underline" @click="startEdit(r)">Edit</button>
            <button data-delete class="text-red-600 dark:text-red-400 hover:underline" @click="deleteTarget = r.loc">Delete</button>
          </span>
        </div>
        <div v-else class="space-y-2 py-1">
          <input v-model="editName" data-edit-name maxlength="200" aria-label="Name"
            class="w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-medium" />
          <div class="max-w-xs">
            <span class="block text-xs text-slate-500 dark:text-slate-400 mb-1">Parent (cannot be cleared)</span>
            <!-- Roots may stay root (value 0 = keep); children must keep or
                 change parent — the API has no "move to root". -->
            <LocationSelect v-model="editParent" :locations="locations" :exclude="excludedParentIds(r.loc)"
              :allow-none="r.loc.parent_id === null" none-label="— root (no parent) —" />
          </div>
          <textarea v-model="editNotes" data-edit-notes rows="2" maxlength="10000" placeholder="Notes"
            class="w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm"></textarea>
          <p v-if="editError" class="text-xs text-red-600 dark:text-red-400">{{ editError }}</p>
          <div class="flex gap-2">
            <button data-save :disabled="saving"
              class="rounded-lg bg-slate-900 dark:bg-slate-100 text-white dark:text-slate-900 px-3 py-1.5 text-sm disabled:opacity-50" @click="saveEdit(r.loc)">Save</button>
            <button class="rounded-lg border dark:border-slate-700 dark:text-slate-100 px-3 py-1.5 text-sm" @click="cancelEdit">Cancel</button>
          </div>
        </div>
      </li>
    </ul>

    <ConfirmDialog :open="deleteTarget !== null" title="Delete location"
      :message="deleteTarget
        ? `Delete '${deleteTarget.name}'? Children move up to this location's parent; items here become unlocated.`
        : ''"
      confirm-label="Delete" @confirm="deleteLocation" @cancel="deleteTarget = null" />
  </div>
</template>
