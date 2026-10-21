<template>
  <div class="border rounded-lg px-2 py-1.5 flex flex-wrap gap-1 items-center bg-white min-h-9">
    <span v-for="t in tags" :key="t" class="bg-slate-100 rounded-full px-2 py-0.5 text-sm flex items-center gap-1">
      {{ t }}
      <button type="button" class="text-slate-400 hover:text-red-600" :aria-label="`Remove tag ${t}`" @click="remove(t)">✕</button>
    </span>
    <input v-model="input" placeholder="Add tag…" aria-label="Add tag"
      class="flex-1 min-w-24 outline-none text-sm"
      @keydown="onKeydown" @blur="add" />
  </div>
</template>
<script setup lang="ts">
import { ref } from 'vue'

// Chips of plain tag names — the backend auto-creates unknown tags on item
// save, so no tag lookup happens here. Enter or comma commits the chip.
const props = defineProps<{ tags: string[] }>()
const emit = defineEmits<{ 'update:tags': [names: string[]] }>()

const input = ref('')

function add() {
  const name = input.value.trim()
  input.value = ''
  if (!name) return
  if (props.tags.length >= 50) return // backend cap: max 50 tags
  if (props.tags.includes(name)) return // dedupe (case-sensitive)
  emit('update:tags', [...props.tags, name])
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' || e.key === ',') {
    e.preventDefault()
    add()
  }
}

function remove(name: string) {
  emit('update:tags', props.tags.filter(t => t !== name))
}
</script>
