<template>
  <div class="fixed bottom-20 md:bottom-4 right-4 z-50 flex flex-col gap-2" aria-live="polite">
    <div v-for="t in toasts" :key="t.id"
      class="rounded-lg px-4 py-2 text-sm shadow-lg max-w-xs"
      :class="t.type === 'error' ? 'bg-red-600 text-white' : t.type === 'success' ? 'bg-emerald-600 text-white' : 'bg-slate-800 text-white'">
      {{ t.message }}
    </div>
  </div>
</template>
<script setup lang="ts">
import { toasts } from '../utils/toast'
</script>
