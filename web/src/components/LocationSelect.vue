<template>
  <select :value="modelValue" aria-label="Location"
    class="w-full border rounded-lg px-2 py-1.5 text-sm bg-white"
    @change="emit('update:modelValue', Number(($event.target as HTMLSelectElement).value))">
    <option :value="0">No location</option>
    <option v-for="o in options" :key="o.id" :value="o.id">{{ '— '.repeat(o.depth) }}{{ o.name }}</option>
  </select>
</template>
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../api/client'
import type { Location, ListResponse } from '../api/types'
import { toast } from '../utils/toast'
import { locationDepthOptions, type LocationOption } from '../utils/locations'

// 0 means "no location" — the API uses 0 to clear an item's location.
defineProps<{ modelValue: number }>()
const emit = defineEmits<{ 'update:modelValue': [n: number] }>()

const options = ref<LocationOption[]>([])

onMounted(async () => {
  try {
    // page_size=200 is the backend max; fetched once per mount.
    const res = await api.get<ListResponse<Location>>('/api/v1/locations?page_size=200')
    options.value = locationDepthOptions(res.data)
  } catch (e) {
    toast((e as Error).message, 'error')
  }
})
</script>
