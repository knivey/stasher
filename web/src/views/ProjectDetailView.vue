<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, ApiError } from '../api/client'
import type { Item, ListResponse, Project, ProjectDetail, ProjectItem, ProjectNote } from '../api/types'
import { toast } from '../utils/toast'
import { PROJECT_STATUSES, statusBadgeClass, visibilityBadgeClass } from '../utils/projects'
import ConfirmDialog from '../components/ConfirmDialog.vue'

const route = useRoute()
const router = useRouter()

const detail = ref<ProjectDetail | null>(null)
const project = ref<Project | null>(null)
// Owner rows only (viewer rows are masked and have no ids). Kept separate so
// qty edits / the add-picker stay typed; rendering iterates detail.items.
const items = ref<ProjectItem[]>([])
// Owner-only notes log — held here for the notes-log section to mount.
const notes = ref<ProjectNote[]>([])

async function fetchDetail() {
  try {
    const res = await api.get<ProjectDetail>(`/api/v1/projects/${route.params.id}`)
    detail.value = res
    project.value = res.project
    items.value = res.items.filter((i): i is ProjectItem => 'item_id' in i)
    notes.value = res.notes_log ?? []
    origStatus.value = res.project.status
    editOrig = { name: res.project.name, notes: res.project.notes ?? '' }
    seedQtyEdits()
  } catch (e) {
    toast((e as Error).message, 'error')
    // Foreign/deleted projects 404 — bounce back to the list.
    if (e instanceof ApiError && e.code === 'not_found') router.push('/projects')
  }
}

// Items for the add-picker. Unlike locations/tags (which ignore pagination
// and always return the full list), /items DOES paginate — page_size=200 is
// the real backend max here. Loaded once alongside the detail.
const allItems = ref<Item[]>([])

async function loadPickerItems() {
  try {
    const res = await api.get<ListResponse<Item>>('/api/v1/items?page_size=200')
    allItems.value = res.data
  } catch (e) {
    toast((e as Error).message, 'error')
  }
}

onMounted(() => {
  void fetchDetail()
  void loadPickerItems()
})

// ---- Status select ----
// The select v-models straight onto project.status; PATCH fires only when it
// actually differs from the last known server value (diff-PATCH discipline).
const origStatus = ref<Project['status']>('planned')
const statusSaving = ref(false)

async function onStatusChange() {
  if (!project.value || statusSaving.value) return
  const next = project.value.status
  if (next === origStatus.value) return
  statusSaving.value = true
  try {
    // 200 returns the updated project — adopt it wholesale.
    project.value = await api.patch<Project>(`/api/v1/projects/${project.value.id}`, { status: next })
    origStatus.value = project.value.status
    toast('Saved', 'success')
  } catch (e) {
    if (project.value) project.value.status = origStatus.value
    toast((e as Error).message, 'error')
  } finally {
    statusSaving.value = false
  }
}

// ---- Visibility badge-is-toggle (owner only, like ItemDetailView) ----
// PATCH the flipped value and adopt the returned project — badge text
// follows the server's answer.
const visibilitySaving = ref(false)

async function toggleVisibility() {
  if (!project.value || !detail.value?.owner_view || visibilitySaving.value) return
  visibilitySaving.value = true
  const next = project.value.visibility === 'shared' ? 'private' : 'shared'
  try {
    project.value = await api.patch<Project>(`/api/v1/projects/${project.value.id}`, { visibility: next })
    toast(next === 'shared' ? 'Project shared' : 'Project private', 'success')
  } catch (e) {
    toast((e as Error).message, 'error') // badge keeps its current state
  } finally {
    visibilitySaving.value = false
  }
}

// ---- Name + notes inline edit (dirty-flag diff, like LocationsView) ----
const editing = ref(false)
const editName = ref('')
const editNotes = ref('')
const editError = ref('')
const saving = ref(false)
let editOrig = { name: '', notes: '' }

function startEdit() {
  if (!project.value) return
  editing.value = true
  editName.value = project.value.name
  editNotes.value = project.value.notes ?? ''
  editOrig = { name: project.value.name, notes: project.value.notes ?? '' }
  editError.value = ''
}

function cancelEdit() {
  editing.value = false
  editError.value = ''
}

async function saveEdit() {
  if (!project.value || saving.value) return
  if (!editName.value.trim()) {
    editError.value = 'Name is required'
    return
  }
  // Each field goes out only when it changed (api-notes: omitted keeps).
  const body: Record<string, unknown> = {}
  if (editName.value.trim() !== editOrig.name) body.name = editName.value.trim()
  if (editNotes.value !== editOrig.notes) body.notes = editNotes.value
  if (!Object.keys(body).length) {
    cancelEdit()
    return
  }
  saving.value = true
  try {
    project.value = await api.patch<Project>(`/api/v1/projects/${project.value.id}`, body)
    editOrig = { name: project.value.name, notes: project.value.notes ?? '' }
    toast('Saved', 'success')
    cancelEdit()
  } catch (e) {
    editError.value = (e as Error).message
    toast((e as Error).message, 'error')
  } finally {
    saving.value = false
  }
}

// ---- qty_used editing ----
// One string input per row; PATCH always sends a number — empty input is 0
// (qty_used cannot be cleared back to null via PATCH).
const qtyEdits = reactive<Record<number, string | number>>({})
const qtySaving = ref(0)

function seedQtyEdits() {
  for (const k of Object.keys(qtyEdits)) delete qtyEdits[Number(k)]
  for (const it of items.value) qtyEdits[it.item_id] = it.qty_used == null ? '' : String(it.qty_used)
}

// v-model on type=number inputs yields number | '' — normalize to a number,
// or null when empty/invalid.
function toQty(v: string | number): number | null {
  if (typeof v === 'number') return Number.isNaN(v) ? null : v
  const t = v.trim()
  if (t === '') return null
  const n = Number(t)
  return Number.isNaN(n) ? null : n
}

async function saveQty(pi: ProjectItem) {
  if (!project.value) return
  qtySaving.value++
  try {
    // Empty input means 0 — qty_used cannot be cleared back to null via PATCH.
    await api.patch(`/api/v1/projects/${project.value.id}/items/${pi.item_id}`,
      { qty_used: toQty(qtyEdits[pi.item_id] ?? '') ?? 0 })
    toast('Saved', 'success')
    await fetchDetail()
  } catch (e) {
    toast((e as Error).message, 'error')
  } finally {
    qtySaving.value--
  }
}

// ---- Remove item ----
// Low stakes (unlink, not delete) — inline button, no confirm dialog.
const removing = ref(0)

async function removeItem(pi: ProjectItem) {
  if (!project.value) return
  removing.value++
  try {
    await api.del(`/api/v1/projects/${project.value.id}/items/${pi.item_id}`)
    toast('Removed from project', 'success')
    await fetchDetail()
  } catch (e) {
    toast((e as Error).message, 'error')
  } finally {
    removing.value--
  }
}

// ---- Delete project ----
// Deleting removes the project and its item links only — the items
// themselves survive. The ConfirmDialog spells that out before the DELETE.
const confirmDelete = ref(false)

async function deleteProject() {
  if (!project.value) return
  confirmDelete.value = false
  try {
    await api.del(`/api/v1/projects/${project.value.id}`)
    toast('Project deleted', 'success')
    router.push('/projects')
  } catch (e) {
    toast((e as Error).message, 'error')
  }
}

// ---- Add item picker ----
// Already-linked items are hidden; qty_used is optional on add (empty omits it,
// leaving the backend's NULL until the user edits the row).
const addItem = ref('')
const addQty = ref<string | number>('')
const adding = ref(false)
const linkedIds = computed(() => new Set(items.value.map(i => i.item_id)))
const pickerOptions = computed(() => allItems.value.filter(i => !linkedIds.value.has(i.id)))

async function addProjectItem() {
  if (!project.value || adding.value || !addItem.value) return
  adding.value = true
  try {
    const body: Record<string, unknown> = { item_id: Number(addItem.value) }
    const q = toQty(addQty.value)
    if (q !== null) body.qty_used = q
    await api.post(`/api/v1/projects/${project.value.id}/items`, body)
    toast('Item added', 'success')
    addItem.value = ''
    addQty.value = ''
    await fetchDetail()
  } catch (e) {
    toast((e as Error).message, 'error')
  } finally {
    adding.value = false
  }
}
</script>
<template>
  <div v-if="project" class="max-w-4xl space-y-6">
    <router-link to="/projects" class="text-sm text-slate-500 dark:text-slate-400 hover:underline">← Projects</router-link>

    <div class="bg-white dark:bg-slate-800 rounded-xl shadow p-4">
      <div v-if="!editing" class="flex flex-wrap items-center justify-between gap-3">
        <div class="min-w-0">
          <h1 class="text-2xl font-bold dark:text-slate-100 flex items-center gap-3 flex-wrap">
            <span>{{ project.name }}</span>
            <span data-status-badge class="rounded-full px-2 py-0.5 text-xs font-medium"
              :class="statusBadgeClass(project.status)">{{ project.status }}</span>
            <button v-if="detail?.owner_view" data-visibility-badge type="button" :disabled="visibilitySaving"
              class="rounded-full px-2 py-0.5 text-xs font-medium" :class="visibilityBadgeClass(project.visibility)"
              :title="project.visibility === 'shared' ? 'Visible to every user — click to make private' : 'Only you — click to share'"
              @click="toggleVisibility">{{ project.visibility }}</button>
          </h1>
          <p v-if="project.notes" class="mt-1 text-sm text-slate-500 dark:text-slate-400 whitespace-pre-wrap">{{ project.notes }}</p>
        </div>
        <div v-if="detail?.owner_view" class="flex items-center gap-3 shrink-0">
          <label class="text-sm text-slate-600 dark:text-slate-300">Status
            <select v-model="project.status" data-status :disabled="statusSaving"
              class="mt-1 ml-1 border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1 text-sm" @change="onStatusChange">
              <option v-for="s in PROJECT_STATUSES" :key="s" :value="s">{{ s }}</option>
            </select>
          </label>
          <button data-edit class="text-slate-600 dark:text-slate-300 hover:underline" @click="startEdit">Edit</button>
          <button data-delete class="text-red-600 dark:text-red-400 hover:underline" @click="confirmDelete = true">Delete</button>
        </div>
      </div>
      <div v-else class="space-y-2">
        <input v-model="editName" data-edit-name maxlength="200" aria-label="Name"
          class="w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-lg font-bold" />
        <textarea v-model="editNotes" data-edit-notes rows="2" maxlength="10000" placeholder="Description" aria-label="Description"
          class="w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm"></textarea>
        <p v-if="editError" class="text-xs text-red-600 dark:text-red-400">{{ editError }}</p>
        <div class="flex gap-2">
          <button data-save :disabled="saving"
            class="rounded-lg bg-slate-900 dark:bg-slate-100 text-white dark:text-slate-900 px-3 py-1.5 text-sm disabled:opacity-50" @click="saveEdit">Save</button>
          <button class="rounded-lg border dark:border-slate-700 dark:text-slate-100 px-3 py-1.5 text-sm" @click="cancelEdit">Cancel</button>
        </div>
      </div>
    </div>

    <div class="bg-white dark:bg-slate-800 rounded-xl shadow p-4 space-y-3">
      <h2 class="text-sm font-semibold text-slate-700 dark:text-slate-300">Items</h2>

      <p v-if="detail && !detail.items.length" class="text-slate-400 dark:text-slate-500 text-sm">No items linked yet.</p>
      <table v-else class="w-full text-sm">
        <thead>
          <tr class="text-left text-xs text-slate-500 dark:text-slate-400 border-b dark:border-slate-700">
            <th class="py-2 pr-2 font-medium">Item</th>
            <th class="py-2 pr-2 font-medium">In stock</th>
            <th class="py-2 pr-2 font-medium">Qty used</th>
            <th class="py-2 font-medium"></th>
          </tr>
        </thead>
        <tbody class="divide-y dark:divide-slate-700">
          <template v-for="pi in detail?.items" :key="'item_id' in pi ? pi.item_id : pi.item_name">
            <!-- Owner row: link + stock + editable qty_used + remove. -->
            <tr v-if="detail?.owner_view && 'item_id' in pi">
              <td class="py-2 pr-2">
                <router-link :to="`/items/${pi.item_id}`" data-item-name class="hover:underline dark:text-slate-100">{{ pi.item_name }}</router-link>
              </td>
              <td class="py-2 pr-2 text-slate-500 dark:text-slate-400">{{ pi.item_qty }}{{ pi.item_unit ? ' ' + pi.item_unit : '' }}</td>
              <td class="py-2 pr-2">
                <span class="flex items-center gap-1">
                  <input v-model="qtyEdits[pi.item_id]" data-qty-used type="number" step="any" min="0"
                    class="w-20 border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1" :aria-label="`Qty used for ${pi.item_name}`" />
                  <button data-qty-save :disabled="qtySaving > 0"
                    class="text-slate-600 dark:text-slate-300 hover:underline disabled:opacity-50" @click="saveQty(pi)">Save</button>
                </span>
              </td>
              <td class="py-2 text-right">
                <button data-remove :disabled="removing > 0"
                  class="text-red-600 dark:text-red-400 hover:underline disabled:opacity-50" @click="removeItem(pi)">Remove</button>
              </td>
            </tr>
            <!-- Sharee row: masked — plain text only, no ids/stock/inputs. -->
            <tr v-else-if="!detail?.owner_view">
              <td class="py-2 pr-2 dark:text-slate-100">{{ pi.item_name }}</td>
              <td class="py-2 pr-2 text-slate-500 dark:text-slate-400">—</td>
              <td class="py-2 pr-2 dark:text-slate-100">{{ pi.qty_used ?? '—' }}</td>
              <td class="py-2"></td>
            </tr>
          </template>
        </tbody>
      </table>

      <form v-if="detail?.owner_view" class="flex flex-wrap items-end gap-2 border-t dark:border-slate-700 pt-3" @submit.prevent="addProjectItem">
        <label class="text-sm font-medium dark:text-slate-100">Add item
          <select v-model="addItem" data-add-item :disabled="adding"
            class="mt-1 block border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal">
            <option value="" disabled>Choose an item…</option>
            <option v-for="i in pickerOptions" :key="i.id" :value="String(i.id)">{{ i.name }}</option>
          </select>
        </label>
        <label class="text-sm font-medium dark:text-slate-100">Qty used
          <input v-model="addQty" data-add-qty type="number" step="any" min="0"
            class="mt-1 w-24 border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal" />
        </label>
        <button type="submit" data-add :disabled="adding || !addItem"
          class="rounded-lg bg-slate-900 dark:bg-slate-100 text-white dark:text-slate-900 px-3 py-1.5 text-sm disabled:opacity-50">Add</button>
        <p v-if="allItems.length && !pickerOptions.length" class="w-full text-xs text-slate-400 dark:text-slate-500">
          All your items are already linked.
        </p>
      </form>
    </div>

    <ConfirmDialog :open="confirmDelete" :title="`Delete '${project.name}'`"
      message="Permanently delete this project? Linked items are NOT deleted — only their links to this project are removed."
      confirm-label="Delete" @confirm="deleteProject" @cancel="confirmDelete = false" />
  </div>
</template>
