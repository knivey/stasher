<template>
  <div>
    <div class="grid grid-cols-3 sm:grid-cols-4 gap-2">
      <div v-for="p in photos" :key="p.id" class="relative group">
        <img :src="`/files/${p.storage_key}`" :alt="p.caption || 'photo'" class="w-full h-24 object-cover rounded-lg cursor-zoom-in"
          @click="selected = p" />
        <button v-if="canEdit" @click.stop="emit('delete', p)"
          class="absolute top-1 right-1 bg-red-600 text-white rounded px-1 text-xs opacity-0 group-hover:opacity-100">✕</button>
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
const emit = defineEmits<{ delete: [p: Photo] }>()
const selected = ref<Photo | null>(null)
</script>
