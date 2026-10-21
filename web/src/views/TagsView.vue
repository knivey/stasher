<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api/client'
import type { ListResponse, Tag } from '../api/types'
import { useItemsStore } from '../stores/items'
import { toast } from '../utils/toast'
import ConfirmDialog from '../components/ConfirmDialog.vue'

const router = useRouter()
const itemsStore = useItemsStore()

const tags = ref<Tag[]>([])

async function fetchTags() {
  try {
    const res = await api.get<ListResponse<Tag>>('/api/v1/tags')
    tags.value = res.data
  } catch (e) {
    toast((e as Error).message, 'error')
  }
}
onMounted(fetchTags)

// Tag names are programmatic navigation with a store mutation (same pattern
// as ItemDetailView.filterByTag): seed the shared items filter, then navigate.
function filterByTag(name: string) {
  itemsStore.filters.tag = name
  itemsStore.filters.page = 1
  router.push('/items')
}

// ---- Inline rename ----
const editingId = ref<number | null>(null)
const editName = ref('')
const saving = ref(false)

function startEdit(t: Tag) {
  editingId.value = t.id
  editName.value = t.name
}

function cancelEdit() {
  editingId.value = null
}

async function saveRename(t: Tag) {
  if (saving.value) return
  const name = editName.value.trim()
  // PATCH only when the name actually changed (api-notes: omitted = kept).
  if (name === t.name) {
    cancelEdit()
    return
  }
  saving.value = true
  try {
    await api.patch(`/api/v1/tags/${t.id}`, { name })
    toast('Saved', 'success')
    cancelEdit()
    await fetchTags()
  } catch (e) {
    // 409 tag_exists: revert the input so the row keeps the original name.
    editName.value = t.name
    toast((e as Error).message, 'error')
  } finally {
    saving.value = false
  }
}

// ---- Delete ----
const deleteTarget = ref<Tag | null>(null)

async function deleteTag() {
  const target = deleteTarget.value
  if (!target) return
  deleteTarget.value = null
  try {
    await api.del(`/api/v1/tags/${target.id}`)
    toast('Tag deleted', 'success')
    if (editingId.value === target.id) cancelEdit()
    await fetchTags()
  } catch (e) {
    toast((e as Error).message, 'error')
  }
}
</script>
<template>
  <div class="max-w-2xl space-y-6">
    <h1 class="text-2xl font-bold">Tags</h1>

    <p v-if="!tags.length" class="text-slate-400 text-sm">No tags yet.</p>
    <table v-else class="w-full bg-white rounded-xl shadow text-sm">
      <thead>
        <tr class="text-left text-slate-500">
          <th class="px-4 py-2 font-medium">Name</th>
          <th class="px-4 py-2 font-medium">Items</th>
          <th class="px-4 py-2"></th>
        </tr>
      </thead>
      <tbody class="divide-y">
        <tr v-for="t in tags" :key="t.id">
          <td class="px-4 py-2">
            <button v-if="editingId !== t.id" data-tag-name class="font-medium hover:underline"
              :title="`Show items tagged '${t.name}'`" @click="filterByTag(t.name)">#{{ t.name }}</button>
            <input v-else v-model="editName" data-edit-name maxlength="100" aria-label="Tag name"
              class="border rounded-lg px-2 py-1.5 text-sm font-medium w-full"
              @keyup.enter="saveRename(t)" @keyup.esc="cancelEdit" />
          </td>
          <td class="px-4 py-2 text-slate-600" data-tag-count>{{ t.item_count ?? 0 }}</td>
          <td class="px-4 py-2 text-right whitespace-nowrap">
            <span v-if="editingId !== t.id" class="flex gap-2 justify-end">
              <button data-edit class="text-slate-600 hover:underline" @click="startEdit(t)">Rename</button>
              <button data-delete class="text-red-600 hover:underline" @click="deleteTarget = t">Delete</button>
            </span>
            <span v-else class="flex gap-2 justify-end">
              <button data-save :disabled="saving" class="text-slate-600 hover:underline disabled:opacity-50"
                @click="saveRename(t)">Save</button>
              <button class="text-slate-600 hover:underline" @click="cancelEdit">Cancel</button>
            </span>
          </td>
        </tr>
      </tbody>
    </table>

    <ConfirmDialog :open="deleteTarget !== null" title="Delete tag"
      :message="deleteTarget
        ? `Delete '${deleteTarget.name}'? Deleting a tag detaches it from all items.`
        : ''"
      confirm-label="Delete" @confirm="deleteTag" @cancel="deleteTarget = null" />
  </div>
</template>
