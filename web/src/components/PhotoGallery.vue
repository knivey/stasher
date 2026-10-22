<template>
  <div>
    <div class="grid grid-cols-3 sm:grid-cols-4 gap-2">
      <div v-for="(p, i) in photos" :key="p.id" class="relative group">
        <img :src="`/files/${p.storage_key}`" :alt="p.caption || 'photo'" class="w-full h-24 object-cover rounded-lg cursor-zoom-in"
          @click="selected = p" />
        <span v-if="i === 0" aria-label="Main photo" title="Main photo"
          class="absolute bottom-1 left-1 text-xs bg-slate-900/80 text-white rounded px-1 py-0.5">★</span>
        <button v-if="canEdit && i > 0" @click.stop="emit('main', p)" title="Make main" aria-label="Make main"
          class="absolute bottom-1 right-1 bg-slate-900/80 text-white rounded px-1.5 py-0.5 text-xs min-w-9 min-h-9 inline-flex items-center justify-center">★</button>
        <button v-if="canEdit" @click.stop="emit('delete', p)"
          class="absolute top-1 right-1 bg-red-600 text-white rounded px-1 text-xs opacity-100 md:opacity-0 md:group-hover:opacity-100">✕</button>
      </div>
    </div>
    <div v-if="selected" class="fixed inset-0 z-50 bg-black/80 flex items-center justify-center p-4"
      @click="selected = null">
      <img :src="`/files/${selected.storage_key}`" class="max-h-full max-w-full object-contain" @click.stop />
      <button class="absolute top-4 right-4 text-white text-2xl" @click="selected = null">✕</button>
    </div>
  </div>
</template>
<script setup lang="ts">
import { ref } from 'vue'
import type { Photo } from '../api/types'
defineProps<{ photos: Photo[]; canEdit: boolean }>()
const emit = defineEmits<{ delete: [p: Photo]; main: [p: Photo] }>()
const selected = ref<Photo | null>(null)
</script>
