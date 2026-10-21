<template>
  <div class="flex items-center gap-2 text-sm">
    <button :disabled="page <= 1" @click="$emit('update:page', page - 1)"
      class="rounded border px-2 py-1 disabled:opacity-40">Prev</button>
    <span>Page {{ page }} of {{ pages }} · {{ total }} items</span>
    <button :disabled="page >= pages" @click="$emit('update:page', page + 1)"
      class="rounded border px-2 py-1 disabled:opacity-40">Next</button>
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
const props = defineProps<{ page: number; pageSize: number; total: number }>()
defineEmits<{ 'update:page': [n: number] }>()
const pages = computed(() => Math.max(1, Math.ceil(props.total / props.pageSize)))
</script>
