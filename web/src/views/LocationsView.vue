<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '../api/client'
import type { ListResponse, Location } from '../api/types'
import { toast } from '../utils/toast'
import { locationTreeRows, type LocationRow } from '../utils/locations'
import { bulkPreview, type BulkNaming, type BulkStructure } from '../utils/locationNames'
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

// Sublocations (create flow only): none, a generated grid, or a copy of
// an existing layout. The grid preview/validation mirror lives in
// utils/locationNames.ts.
type SubMode = 'none' | 'generate' | 'copy'
const subMode = ref<SubMode>('none')
const genDims = ref<number[]>([3, 3])
const genStructure = ref<BulkStructure>('flat')
const genNaming = ref<BulkNaming>('compact')
const genPrefix = ref('')
const copySource = ref(0)
const preview = computed(() =>
  subMode.value === 'generate'
    ? bulkPreview(genDims.value, genStructure.value, genNaming.value, genPrefix.value)
    : null)

// The nested tree render is capped so a max-size grid (1000 nodes) can't
// dump a wall of rows into the form; the remainder shows as one muted
// "… and N more" line.
const PREVIEW_TREE_CAP = 12
const previewTree = computed(() => preview.value?.rows.slice(0, PREVIEW_TREE_CAP) ?? [])
const previewHidden = computed(() => Math.max(0, (preview.value?.rows.length ?? 0) - PREVIEW_TREE_CAP))

function addDimension() {
  if (genDims.value.length < 3) genDims.value.push(2)
}
function removeDimension() {
  if (genDims.value.length > 1) genDims.value.pop()
}

// While copying, the new location's parent may not fall inside the
// source's own subtree (the API would reject it with 400).
const copySourceLoc = computed(() => locations.value.find(l => l.id === copySource.value) ?? null)
const addParentExclude = computed(() =>
  subMode.value === 'copy' && copySourceLoc.value ? excludedParentIds(copySourceLoc.value) : [])

async function addLocation() {
  if (adding.value || !addName.value.trim()) return
  if (subMode.value === 'generate' && preview.value?.error) return
  if (subMode.value === 'copy' && !copySource.value) return
  adding.value = true
  const name = addName.value.trim()
  try {
    if (subMode.value === 'generate') {
      const res = await api.post<{ children_created: number }>('/api/v1/locations/bulk', {
        name, dims: genDims.value, structure: genStructure.value,
        naming: genNaming.value, prefix: genPrefix.value.trim(),
        ...(addParent.value ? { parent_id: addParent.value } : {}),
      })
      toast(`Created ${name} + ${res.children_created} sublocations`, 'success')
    } else if (subMode.value === 'copy') {
      const res = await api.post<{ children_created: number }>(`/api/v1/locations/${copySource.value}/copy-layout`, {
        name, ...(addParent.value ? { parent_id: addParent.value } : {}),
      })
      toast(`Created ${name} + ${res.children_created} sublocations`, 'success')
    } else {
      const body: Record<string, unknown> = { name }
      if (addParent.value) body.parent_id = addParent.value
      await api.post('/api/v1/locations', body)
      toast('Location added', 'success')
    }
    addName.value = ''
    addError.value = ''
    genPrefix.value = ''
    await fetchLocations()
  } catch (e) {
    // 409 location_exists lands here; show the backend message inline too.
    addError.value = (e as Error).message
    toast((e as Error).message, 'error')
  } finally {
    adding.value = false
  }
}

// ---- Duplicate layout (copy a location's subtree beside it) ----
const dupTarget = ref<Location | null>(null)
const dupName = ref('')
const dupError = ref('')
const duplicating = ref(false)

function startDuplicate(loc: Location) {
  dupTarget.value = loc
  dupName.value = `${loc.name} copy`
  dupError.value = ''
}

async function duplicateLayout() {
  const target = dupTarget.value
  const name = dupName.value.trim()
  if (!target || duplicating.value || !name) return
  duplicating.value = true
  try {
    const res = await api.post<{ children_created: number }>(`/api/v1/locations/${target.id}/copy-layout`, {
      name, parent_id: target.parent_id ?? 0,
    })
    dupTarget.value = null
    toast(`Created ${name} + ${res.children_created} sublocations`, 'success')
    await fetchLocations()
  } catch (e) {
    dupError.value = (e as Error).message
    toast((e as Error).message, 'error')
  } finally {
    duplicating.value = false
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
        <LocationSelect v-model="addParent" :locations="locations" :exclude="addParentExclude" class="mt-1" />
      </div>
      <div class="w-full border-t dark:border-slate-700 pt-3">
        <span class="block text-sm font-medium dark:text-slate-100">Sublocations</span>
        <select v-model="subMode" data-sub-mode
          class="mt-1 w-full max-w-xs border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal bg-white">
          <option value="none">None</option>
          <option value="generate">Generate grid</option>
          <option value="copy">Copy existing layout</option>
        </select>

        <div v-if="subMode === 'generate'" data-sub-generate class="mt-2 space-y-2 text-sm">
          <div class="flex flex-wrap items-end gap-2">
            <label v-for="(_, i) in genDims" :key="i" class="text-xs text-slate-500 dark:text-slate-400">Dim {{ i + 1 }}
              <input v-model.number="genDims[i]" :data-dim="i" type="number" min="1" max="100"
                class="mt-0.5 block w-16 border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1" />
            </label>
            <button type="button" data-add-dim :disabled="genDims.length >= 3" @click="addDimension"
              class="rounded-lg border dark:border-slate-700 dark:text-slate-100 px-2 py-1 text-xs disabled:opacity-50">+ dimension</button>
            <button type="button" data-remove-dim :disabled="genDims.length <= 1" @click="removeDimension"
              class="rounded-lg border dark:border-slate-700 dark:text-slate-100 px-2 py-1 text-xs disabled:opacity-50">− dimension</button>
          </div>
          <div class="flex flex-wrap gap-4 text-xs text-slate-600 dark:text-slate-300">
            <span class="flex gap-2">
              <label><input type="radio" value="flat" v-model="genStructure" /> flat</label>
              <label><input type="radio" value="nested" v-model="genStructure" /> nested</label>
            </span>
            <span class="flex gap-2">
              <label><input type="radio" value="compact" v-model="genNaming" /> compact (A1)</label>
              <label><input type="radio" value="verbose" v-model="genNaming" /> verbose (Row 1 Col 1)</label>
            </span>
          </div>
          <p data-structure-hint class="text-xs text-slate-500 dark:text-slate-400">
            flat: one level of children (A1, A2…) · nested: a level per dimension (A › A1 › A1a)
          </p>
          <input v-model="genPrefix" data-gen-prefix maxlength="50" placeholder="Prefix (optional, leaves only)"
            class="w-full max-w-xs border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm" />
          <p v-if="preview?.error" data-preview-error class="text-xs text-red-600 dark:text-red-400">{{ preview.error }}</p>
          <div v-else-if="preview" data-preview class="text-xs text-slate-500 dark:text-slate-400">
            <p v-if="genStructure === 'flat'">
              Will create {{ preview.count }} sublocations: {{ preview.first.join(', ') }}{{ preview.more ? ', …' : '' }}
            </p>
            <template v-else>
              <p>Will create {{ preview.count }} sublocations (nested tree):</p>
              <div v-for="(row, i) in previewTree" :key="i" data-preview-tree-row
                :style="{ marginLeft: (row.depth * 16) + 'px' }">{{ row.name }}</div>
              <p v-if="previewHidden > 0" data-preview-more class="text-slate-400 dark:text-slate-500">
                … and {{ previewHidden }} more
              </p>
            </template>
          </div>
        </div>

        <div v-else-if="subMode === 'copy'" data-sub-copy class="mt-2 space-y-1">
          <div class="max-w-xs">
            <LocationSelect v-model="copySource" data-copy-source :locations="locations" none-label="— choose a layout —" />
          </div>
          <p class="text-xs text-slate-500 dark:text-slate-400">Copies names and shape only — items are not copied.</p>
        </div>
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
            <router-link :to="`/items/new?location=${r.loc.id}`" data-add-item
              class="text-slate-600 dark:text-slate-300 hover:underline">Add item</router-link>
            <button data-edit class="text-slate-600 dark:text-slate-300 hover:underline" @click="startEdit(r)">Edit</button>
            <button data-duplicate class="text-slate-600 dark:text-slate-300 hover:underline" @click="startDuplicate(r.loc)">Duplicate layout…</button>
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

    <div v-if="dupTarget" class="fixed inset-0 z-50 bg-black/50 flex items-center justify-center p-4"
      @click="dupTarget = null" @keydown.escape="dupTarget = null">
      <div class="bg-white dark:bg-slate-800 rounded-xl shadow-lg w-full max-w-sm p-5" role="dialog" aria-modal="true" @click.stop>
        <h2 class="text-lg font-bold mb-2 dark:text-slate-100">Duplicate layout</h2>
        <p class="text-sm text-slate-600 dark:text-slate-300 mb-3">
          Copies '{{ dupTarget.name }}' and its sublocations (names and shape only — items stay put) beside it.
        </p>
        <input v-model="dupName" data-dup-name maxlength="200" aria-label="New location name"
          class="w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm" />
        <p v-if="dupError" class="mt-1 text-xs text-red-600 dark:text-red-400">{{ dupError }}</p>
        <div class="flex justify-end gap-2 mt-3">
          <button class="rounded-lg border dark:border-slate-700 dark:text-slate-100 px-3 py-1.5 text-sm" @click="dupTarget = null">Cancel</button>
          <button data-dup-confirm :disabled="duplicating"
            class="rounded-lg bg-slate-900 dark:bg-slate-100 text-white dark:text-slate-900 px-3 py-1.5 text-sm disabled:opacity-50" @click="duplicateLayout">Duplicate</button>
        </div>
      </div>
    </div>
  </div>
</template>
