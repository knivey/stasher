<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api/client'
import type { Item } from '../api/types'
import { useAuthStore } from '../stores/auth'
import { toast } from '../utils/toast'
import { downscaleImage } from '../utils/image'
import TagInput from '../components/TagInput.vue'
import LocationSelect from '../components/LocationSelect.vue'

// The route param is the source of truth (/items/new = create, /items/:id/edit
// = edit); the id prop exists so tests can mount without a router. Optional
// chaining keeps both callables safe when no router is installed.
const props = defineProps<{ id?: string }>()
const route = useRoute()
const router = useRouter()
const itemId = computed(() => {
  const v = props.id ?? route?.params?.id
  return v && v !== 'new' ? v : undefined
})
const editing = computed(() => itemId.value !== undefined)

const form = reactive({
  name: '',
  description: '',
  qty: 1,
  unit: '',
  visibility: 'private' as 'private' | 'shared',
  location_id: 0, // 0 = no location (API clears with 0, never null)
  tags: [] as string[],
})

// The create form defaults visibility from the user's settings. The auth
// store is only touched in create mode so edit mounts don't require a pinia.
if (!editing.value) form.visibility = useAuthStore().user?.default_item_visibility ?? 'private'

// Snapshot of the fetched item: buildPatch diffs the live form against it so
// untouched fields are omitted from PATCH entirely.
let initial = snapshot()
let initialTags: string[] = []

function snapshot() {
  return {
    name: form.name,
    description: form.description,
    qty: form.qty,
    unit: form.unit,
    visibility: form.visibility,
    location_id: form.location_id,
  }
}

function tagsEqual(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false
  const sa = [...a].sort()
  const sb = [...b].sort()
  return sa.every((x, i) => x === sb[i])
}

function buildPatch(): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  if (form.name !== initial.name) out.name = form.name
  if (form.description !== initial.description) out.description = form.description
  if (form.qty !== initial.qty) out.qty = form.qty
  if (form.unit !== initial.unit) out.unit = form.unit
  if (form.visibility !== initial.visibility) out.visibility = form.visibility
  if (form.location_id !== initial.location_id) out.location_id = form.location_id // 0 = clear (api-notes)
  if (!tagsEqual(form.tags, initialTags)) out.tags = form.tags
  return out
}

onMounted(async () => {
  if (!editing.value) return
  try {
    const it = await api.get<Item>(`/api/v1/items/${itemId.value}`)
    form.name = it.name
    form.description = it.description ?? ''
    form.qty = it.qty
    form.unit = it.unit ?? ''
    form.visibility = it.visibility
    form.location_id = it.location_id ?? 0
    form.tags = (it.tags ?? []).map(t => t.name)
    initial = snapshot()
    initialTags = [...form.tags]
  } catch (e) {
    toast((e as Error).message, 'error')
    router?.push('/items')
  }
})

// Double-submit guard: one POST (+ queued uploads) per Save click. A second
// click during the run would re-create the item and duplicate its uploads.
const saving = ref(false)

async function save() {
  if (saving.value) return
  saving.value = true
  try {
    if (editing.value) {
      await api.patch(`/api/v1/items/${itemId.value}`, buildPatch())
      toast('Saved', 'success')
      router?.push(`/items/${itemId.value}`)
    } else {
      const body: Record<string, unknown> = {
        name: form.name,
        description: form.description,
        qty: form.qty,
        unit: form.unit,
        visibility: form.visibility,
      }
      if (form.location_id) body.location_id = form.location_id
      if (form.tags.length) body.tags = form.tags
      const created = await api.post<Item>('/api/v1/items', body)
      await uploadQueued(created.id)
      toast('Saved', 'success')
      router?.push(`/items/${created.id}`)
    }
  } catch (e) {
    toast((e as Error).message, 'error')
  } finally {
    saving.value = false
  }
}

// Pre-create upload queue: files can't be uploaded until the item exists, so
// picks are held locally and flushed after the POST returns the new id.
// Photos are downscaled at PICK time so the expensive work precedes submit.
const queuedPhotos = ref<Blob[]>([])
const queuedDocs = ref<File[]>([])

async function onPickPhotos(e: Event) {
  const el = e.target as HTMLInputElement
  const files = Array.from(el.files ?? [])
  el.value = '' // allow re-picking the same file
  for (const file of files) {
    try {
      queuedPhotos.value.push(await downscaleImage(file))
    } catch (err) {
      toast((err as Error).message, 'error')
    }
  }
}

function onPickDocs(e: Event) {
  const el = e.target as HTMLInputElement
  queuedDocs.value.push(...Array.from(el.files ?? []))
  el.value = ''
}

// Upload failures toast but never block navigation — the item already exists.
async function uploadQueued(id: number) {
  for (const blob of queuedPhotos.value) {
    try {
      await api.upload(`/api/v1/items/${id}/photos`, 'file', blob, 'photo.jpg')
    } catch (e) {
      toast((e as Error).message, 'error')
    }
  }
  for (const doc of queuedDocs.value) {
    try {
      await api.upload(`/api/v1/items/${id}/documents`, 'file', doc, doc.name)
    } catch (e) {
      toast((e as Error).message, 'error')
    }
  }
}

function cancel() {
  router?.push(editing.value ? `/items/${itemId.value}` : '/items')
}

defineExpose({ form, save, tagsEqual })
</script>
<template>
  <div class="max-w-xl space-y-4">
    <h1 class="text-2xl font-bold">{{ editing ? 'Edit item' : 'New item' }}</h1>
    <form class="bg-white rounded-xl shadow p-4 space-y-4" @submit.prevent="save">
      <label class="block text-sm font-medium">Name
        <input v-model="form.name" required class="mt-1 w-full border rounded-lg px-2 py-1.5 text-sm font-normal" />
      </label>
      <label class="block text-sm font-medium">Description
        <textarea v-model="form.description" rows="3" class="mt-1 w-full border rounded-lg px-2 py-1.5 text-sm font-normal"></textarea>
      </label>
      <div class="flex gap-4">
        <label class="flex-1 block text-sm font-medium">Qty
          <input v-model.number="form.qty" type="number" min="0" step="any" class="mt-1 w-full border rounded-lg px-2 py-1.5 text-sm font-normal" />
        </label>
        <label class="flex-1 block text-sm font-medium">Unit
          <input v-model="form.unit" placeholder="pcs, kg…" class="mt-1 w-full border rounded-lg px-2 py-1.5 text-sm font-normal" />
        </label>
      </div>
      <label class="block text-sm font-medium">Visibility
        <select v-model="form.visibility" class="mt-1 w-full border rounded-lg px-2 py-1.5 text-sm font-normal bg-white">
          <option value="private">Private</option>
          <option value="shared">Shared</option>
        </select>
      </label>
      <div class="block text-sm font-medium">Location
        <LocationSelect v-model="form.location_id" class="mt-1" />
      </div>
      <div class="block text-sm font-medium">Tags
        <TagInput v-model:tags="form.tags" class="mt-1" />
      </div>
      <div v-if="!editing" class="block text-sm font-medium">Photos & documents
        <div class="mt-1 flex gap-2">
          <label class="rounded-lg border px-3 py-1.5 text-sm font-normal cursor-pointer">Add photo 📷
            <input type="file" accept="image/*" capture="environment" multiple class="hidden" @change="onPickPhotos" />
          </label>
          <label class="rounded-lg border px-3 py-1.5 text-sm font-normal cursor-pointer">Add document
            <input type="file" multiple class="hidden" @change="onPickDocs" />
          </label>
        </div>
        <p v-if="queuedPhotos.length || queuedDocs.length" class="mt-1 text-xs text-slate-500 font-normal">
          {{ queuedPhotos.length }} photo{{ queuedPhotos.length === 1 ? '' : 's' }},
          {{ queuedDocs.length }} document{{ queuedDocs.length === 1 ? '' : 's' }} will be added after saving
        </p>
      </div>
      <div class="flex justify-end gap-2">
        <button type="button" class="rounded-lg border px-3 py-1.5 text-sm" @click="cancel">Cancel</button>
        <button type="submit" :disabled="saving"
          class="rounded-lg bg-slate-900 text-white px-3 py-1.5 text-sm disabled:opacity-50">
          {{ saving ? 'Saving…' : 'Save' }}</button>
      </div>
    </form>
  </div>
</template>
