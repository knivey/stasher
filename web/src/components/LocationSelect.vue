<template>
  <select :value="modelValue" aria-label="Location"
    class="w-full border rounded-lg px-2 py-1.5 text-sm bg-white"
    @change="emit('update:modelValue', Number(($event.target as HTMLSelectElement).value))">
    <option v-if="allowNone !== false" :value="0">{{ noneLabel ?? 'No location' }}</option>
    <option v-for="o in options" :key="o.id" :value="o.id">{{ '— '.repeat(o.depth) }}{{ o.name }}</option>
  </select>
</template>
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '../api/client'
import type { Location, ListResponse } from '../api/types'
import { toast } from '../utils/toast'
import { locationDepthOptions, type LocationOption } from '../utils/locations'

// 0 means "no location" — the API uses 0 to clear an item's location. The
// extra props serve the locations view: `locations` reuses caller-owned data
// (no extra fetch, stays fresh after mutations), `allowNone`/`noneLabel`
// adapt the zero option for parent picking, and `exclude` hides ids that
// must not be selectable (a location being moved + its descendants).
// NOTE: withDefaults is required — Vue casts absent Boolean props to false,
// which would hide the none option for callers that don't pass allow-none.
const props = withDefaults(defineProps<{
  modelValue: number
  locations?: Location[]
  allowNone?: boolean
  noneLabel?: string
  exclude?: number[]
}>(), { allowNone: true })
const emit = defineEmits<{ 'update:modelValue': [n: number] }>()

const fetched = ref<Location[]>([])
const options = computed<LocationOption[]>(() => {
  const excluded = new Set(props.exclude ?? [])
  return locationDepthOptions(props.locations ?? fetched.value)
    .filter(o => !excluded.has(o.id))
})

onMounted(async () => {
  if (props.locations) return
  try {
    // Locations ignore pagination: the full list comes back (the param is
    // accepted but unused — see docs/api-notes.md); fetched once per mount.
    const res = await api.get<ListResponse<Location>>('/api/v1/locations?page_size=200')
    fetched.value = res.data
  } catch (e) {
    toast((e as Error).message, 'error')
  }
})
</script>
