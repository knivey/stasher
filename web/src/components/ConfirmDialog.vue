<template>
  <div v-if="open" class="fixed inset-0 z-50 bg-black/50 flex items-center justify-center p-4" @click="emit('cancel')">
    <div class="bg-white rounded-xl shadow-lg w-full max-w-sm p-5" role="dialog" aria-modal="true" @click.stop>
      <h2 class="text-lg font-bold mb-2">{{ title }}</h2>
      <p class="text-sm text-slate-600 mb-4">{{ message }}</p>
      <div class="flex justify-end gap-2">
        <button class="rounded-lg border px-3 py-1.5 text-sm" @click="emit('cancel')">Cancel</button>
        <button class="rounded-lg bg-red-600 text-white px-3 py-1.5 text-sm" @click="emit('confirm')">{{ confirmLabel ?? 'Confirm' }}</button>
      </div>
    </div>
  </div>
</template>
<script setup lang="ts">
defineProps<{ open: boolean; title: string; message: string; confirmLabel?: string }>()
const emit = defineEmits<{ confirm: []; cancel: [] }>()
</script>
