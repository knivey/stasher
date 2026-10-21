<script setup lang="ts">
import { ref } from 'vue'
import { api } from '../api/client'
import { downscaleImage } from '../utils/image'
import { toast } from '../utils/toast'

// Immediate uploader for an existing item (detail view). The create form
// can't use this — there is no item id until POST /items returns — so it
// keeps its own pre-create queue in ItemFormView.
const props = defineProps<{ kind: 'photo' | 'doc'; itemId: number | string }>()
const emit = defineEmits<{ uploaded: [] }>()

const busy = ref(false)
const input = ref<HTMLInputElement | null>(null)

function pick() {
  input.value?.click()
}

async function onChange(e: Event) {
  const el = e.target as HTMLInputElement
  const files = Array.from(el.files ?? [])
  el.value = '' // allow re-picking the same file
  if (!files.length) return
  busy.value = true
  try {
    for (const file of files) {
      try {
        if (props.kind === 'photo') {
          const blob = await downscaleImage(file)
          await api.upload(`/api/v1/items/${props.itemId}/photos`, 'file', blob, 'photo.jpg')
        } else {
          await api.upload(`/api/v1/items/${props.itemId}/documents`, 'file', file, file.name)
        }
        emit('uploaded')
      } catch (err) {
        toast((err as Error).message, 'error')
      }
    }
  } finally {
    busy.value = false
  }
}
</script>
<template>
  <span class="inline-flex">
    <input ref="input" type="file" class="hidden" multiple
      :accept="kind === 'photo' ? 'image/*' : undefined"
      :capture="kind === 'photo' ? 'environment' : undefined"
      @change="onChange" />
    <button type="button" :disabled="busy" @click="pick"
      class="rounded-lg border px-3 py-1.5 text-sm disabled:opacity-50">
      {{ kind === 'photo' ? 'Add photo 📷' : 'Add document' }}{{ busy ? '…' : '' }}
    </button>
  </span>
</template>
