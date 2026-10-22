<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api/client'
import type { Item, ListResponse, Location } from '../api/types'
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

// Locations feed the select AND the ?location= prefill guard below — fetched
// here once and passed down (this replaces LocationSelect's internal fetch).
const locations = ref<Location[]>([])

// /items/new?location=N (LocationsView "Add item" links): prefill the select
// with N when N is one of the loaded locations. Invalid or unknown ids are
// ignored silently — no toast storm for a stale link.
function applyLocationPrefill() {
  if (editing.value) return // edit mode: the item's own location wins
  const raw = route?.query?.location
  const v = typeof raw === 'string' ? raw : Array.isArray(raw) ? String(raw[0]) : ''
  if (!/^\d+$/.test(v)) return
  const id = Number(v)
  if (locations.value.some(l => l.id === id)) form.location_id = id
}

onMounted(async () => {
  api.get<ListResponse<Location>>('/api/v1/locations?page_size=200')
    .then(r => {
      locations.value = r.data
      applyLocationPrefill()
    })
    .catch(e => toast(e.message, 'error'))
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

// Focus target for "Save & add another": after the reset, typing continues
// straight into Name without a click.
const nameInput = ref<HTMLInputElement | null>(null)

// Double-submit guard: one POST (+ queued uploads) per Save click. A second
// click during the run would re-create the item and duplicate its uploads.
const saving = ref(false)

async function save(again: boolean = false) {
  if (saving.value) return
  // Whitespace-only name = empty form. Native `required` accepts " ", and
  // "Save & add another" is type="button" so it never runs native validation
  // at all — guard both paths here: focus the field and stay silent (no
  // POST, no reset, no error toast), exactly like native validation would.
  if (!form.name.trim()) {
    nameInput.value?.focus()
    return
  }
  saving.value = true
  try {
    // Drain any in-flight pick encodes first so the item isn't created
    // while a photo the user just picked is still encoding…
    await drainPendingEncodes()
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
      // …and drain again before the queue snapshot: a pick that started
      // during the POST would otherwise miss the upload flush.
      await drainPendingEncodes()
      await uploadQueued(created.id)
      toast('Saved', 'success')
      if (!again) {
        router?.push(`/items/${created.id}`)
        return
      }
      // "Save & add another": reset for the next item, keeping the location.
      // The queues clear only now — uploadQueued above has flushed them.
      form.name = ''
      form.description = ''
      form.qty = 1
      form.unit = ''
      form.visibility = useAuthStore().user?.default_item_visibility ?? 'private'
      form.tags = []
      initial = snapshot()
      initialTags = []
      queuedPhotos.value = []
      queuedDocs.value = []
      // replace, not push: no history entry per item, and a reload keeps the
      // location prefill via applyLocationPrefill.
      router?.replace(`/items/new${form.location_id ? `?location=${form.location_id}` : ''}`)
      nameInput.value?.focus()
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

// In-flight pick encodes (canvas downscale work). save() drains these
// before it snapshots queuedPhotos, so a Save click inside the encode
// window can't silently drop a just-picked photo.
const pendingEncodes: Promise<void>[] = []

// Splice-and-await until no encodes remain. A pick landing while we await
// re-enters the loop, so the drain can't be outrun by a concurrent pick.
async function drainPendingEncodes() {
  while (pendingEncodes.length) await Promise.all(pendingEncodes.splice(0))
}

async function onPickPhotos(e: Event) {
  const el = e.target as HTMLInputElement
  const files = Array.from(el.files ?? [])
  el.value = '' // allow re-picking the same file
  for (const file of files) {
    const enc: Promise<void> = downscaleImage(file).then(
      b => { queuedPhotos.value.push(b) },
      err => { toast((err as Error).message, 'error') },
    )
    pendingEncodes.push(enc)
    void enc.finally(() => {
      const i = pendingEncodes.indexOf(enc)
      if (i >= 0) pendingEncodes.splice(i, 1)
    })
    await enc
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
    <h1 class="text-2xl font-bold dark:text-slate-100">{{ editing ? 'Edit item' : 'New item' }}</h1>
    <form class="bg-white dark:bg-slate-800 rounded-xl shadow p-4 space-y-4" @submit.prevent="save(false)">
      <label class="block text-sm font-medium dark:text-slate-100">Name
        <input ref="nameInput" v-model="form.name" required class="mt-1 w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal" />
      </label>
      <label class="block text-sm font-medium dark:text-slate-100">Description
        <textarea v-model="form.description" rows="3" class="mt-1 w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal"></textarea>
      </label>
      <div class="flex gap-4">
        <label class="flex-1 block text-sm font-medium dark:text-slate-100">Qty
          <input v-model.number="form.qty" type="number" min="0" step="any" class="mt-1 w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal" />
        </label>
        <label class="flex-1 block text-sm font-medium dark:text-slate-100">Unit
          <input v-model="form.unit" placeholder="pcs, kg…" class="mt-1 w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal" />
        </label>
      </div>
      <label class="block text-sm font-medium dark:text-slate-100">Visibility
        <select v-model="form.visibility" class="mt-1 w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal bg-white">
          <option value="private">Private</option>
          <option value="shared">Shared</option>
        </select>
      </label>
      <div class="block text-sm font-medium dark:text-slate-100">Location
        <LocationSelect v-model="form.location_id" :locations="locations" class="mt-1" />
      </div>
      <div class="block text-sm font-medium dark:text-slate-100">Tags
        <TagInput v-model:tags="form.tags" class="mt-1" />
      </div>
      <div v-if="!editing" class="block text-sm font-medium dark:text-slate-100">Photos & documents
        <div class="mt-1 flex gap-2">
          <!-- :disabled blocks label-click activation too; the labels dim and
               go inert so a pick can't START mid-save and miss the encode
               drain (backlog: residual upload race). -->
          <label class="rounded-lg border dark:border-slate-700 dark:text-slate-100 px-3 py-1.5 text-sm font-normal cursor-pointer"
            :class="saving && 'opacity-50 pointer-events-none'">Add photo 📷
            <input type="file" accept="image/*" capture="environment" multiple class="hidden"
              :disabled="saving" @change="onPickPhotos" />
          </label>
          <label class="rounded-lg border dark:border-slate-700 dark:text-slate-100 px-3 py-1.5 text-sm font-normal cursor-pointer"
            :class="saving && 'opacity-50 pointer-events-none'">Add document
            <input type="file" multiple class="hidden" :disabled="saving" @change="onPickDocs" />
          </label>
        </div>
        <p v-if="queuedPhotos.length || queuedDocs.length" class="mt-1 text-xs text-slate-500 dark:text-slate-400 font-normal">
          {{ queuedPhotos.length }} photo{{ queuedPhotos.length === 1 ? '' : 's' }},
          {{ queuedDocs.length }} document{{ queuedDocs.length === 1 ? '' : 's' }} will be added after saving
        </p>
      </div>
      <div class="flex justify-end gap-2">
        <button type="button" class="rounded-lg border dark:border-slate-700 dark:text-slate-100 px-3 py-1.5 text-sm" @click="cancel">Cancel</button>
        <!-- Outlined sibling of Save's scheme; type="button" so Enter keeps
             submitting via the form (@submit → save(false)). min-h-10 keeps a
             40px tap target. Create mode only — edit navigates on save. -->
        <button v-if="!editing" type="button" :disabled="saving" @click="save(true)"
          class="rounded-lg min-h-10 border border-slate-900 dark:border-slate-100 text-slate-900 dark:text-slate-100 px-3 text-sm font-medium whitespace-nowrap disabled:opacity-50">
          Save &amp; add another</button>
        <button type="submit" :disabled="saving"
          class="rounded-lg bg-slate-900 dark:bg-slate-100 text-white dark:text-slate-900 px-3 py-1.5 text-sm disabled:opacity-50">
          {{ saving ? 'Saving…' : 'Save' }}</button>
      </div>
    </form>
  </div>
</template>
