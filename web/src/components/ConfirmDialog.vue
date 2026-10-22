<template>
  <div v-if="open" class="fixed inset-0 z-50 bg-black/50 flex items-center justify-center p-4" @click="emit('cancel')">
    <div class="bg-white dark:bg-slate-800 rounded-xl shadow-lg w-full max-w-sm p-5" role="dialog" aria-modal="true" @click.stop>
      <h2 class="text-lg font-bold mb-2 dark:text-slate-100">{{ title }}</h2>
      <p class="text-sm text-slate-600 dark:text-slate-300 mb-4 whitespace-pre-line">{{ message }}</p>
      <p v-if="hint" class="text-sm text-red-600 dark:text-red-400 font-bold mb-4 -mt-2">{{ hint }}</p>
      <div class="flex justify-end gap-2">
        <button class="rounded-lg border dark:border-slate-700 dark:text-slate-100 px-3 py-1.5 text-sm" @click="emit('cancel')">Cancel</button>
        <button class="rounded-lg bg-red-600 text-white px-3 py-1.5 text-sm" @click="emit('confirm')">{{ confirmLabel ?? 'Confirm' }}</button>
      </div>
    </div>
  </div>
</template>
<script setup lang="ts">
defineProps<{ open: boolean; title: string; message: string; hint?: string; confirmLabel?: string }>()
const emit = defineEmits<{ confirm: []; cancel: [] }>()
</script>
