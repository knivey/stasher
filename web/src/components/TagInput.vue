<template>
  <div>
    <div class="border dark:border-slate-700 rounded-lg px-2 py-1.5 flex flex-wrap gap-1 items-center bg-white dark:bg-slate-800 min-h-9">
      <span v-for="t in tags" :key="t" class="bg-slate-100 dark:bg-slate-700 dark:text-slate-100 rounded-full px-2 py-0.5 text-sm flex items-center gap-1">
        {{ t }}
        <button type="button" class="text-slate-400 dark:text-slate-500 hover:text-red-600 dark:hover:text-red-400" :aria-label="`Remove tag ${t}`" @click="remove(t)">✕</button>
      </span>
      <div class="relative flex-1 min-w-24">
        <input v-model="input" placeholder="Add tag… (Enter)" aria-label="Add tag"
          role="combobox" :aria-expanded="open" :aria-controls="listboxId"
          :aria-activedescendant="active >= 0 ? optionId(active) : undefined" aria-autocomplete="list"
          class="w-full outline-none text-sm dark:text-slate-100"
          @keydown="onKeydown" @blur="add" @focus="ensureSuggestions" />
        <ul v-if="open && suggestions.length" :id="listboxId" role="listbox"
          class="absolute left-0 right-0 top-full mt-1 z-20 bg-white dark:bg-slate-800 border dark:border-slate-700 rounded-lg shadow-lg max-h-60 overflow-y-auto">
          <li v-for="(s, i) in suggestions" :key="s.name" :id="optionId(i)" role="option" :aria-selected="i === active"
            class="px-2 py-1 text-sm cursor-pointer dark:text-slate-100"
            :class="i === active ? 'bg-slate-100 dark:bg-slate-700' : 'hover:bg-slate-100 dark:hover:bg-slate-700'"
            @mousedown.prevent @click="pick(i)">{{ s.name }}</li>
        </ul>
      </div>
    </div>
    <!-- Discoverability: only while something is typed; outside the bordered
         box so the input's height doesn't change when it appears. Suppressed
         while a suggestion is active — Enter means "pick" then, not "commit". -->
    <p v-if="input.trim() && !(open && active >= 0)" class="mt-1 text-xs text-slate-500 dark:text-slate-400">Press Enter (or comma) to add “{{ input.trim() }}”</p>
  </div>
</template>
<script lang="ts">
// Module scope (NOT <script setup>, which re-runs per instance): instance
// counter so the listbox/option DOM ids stay document-unique when two
// TagInputs are mounted at once.
let tagInputInstance = 0
</script>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '../api/client'
import type { Tag, ListResponse } from '../api/types'
import { toast } from '../utils/toast'

// Chips of plain tag names — the backend auto-creates unknown tags on item
// save. Enter or comma commits the chip; the listbox suggests existing tags
// (fetched lazily on first focus, cached for the component's life — a failed
// fetch is not retried, typing a new tag still works).
const props = defineProps<{ tags: string[] }>()
const emit = defineEmits<{ 'update:tags': [names: string[]] }>()

const uid = ++tagInputInstance
const listboxId = `tag-suggest-list-${uid}`
const optionId = (i: number) => `tag-suggest-${uid}-${i}`

const MAX_SUGGESTIONS = 8

const input = ref('')
const allTags = ref<Tag[]>([])
const open = ref(false)
const active = ref(-1) // index into suggestions; -1 = none active

const suggestions = computed<Tag[]>(() => {
  const q = input.value.trim().toLowerCase()
  if (!q) return []
  const selected = new Set(props.tags)
  return allTags.value
    .filter(t => !selected.has(t.name) && t.name.toLowerCase().includes(q))
    .sort((a, b) => (b.item_count ?? 0) - (a.item_count ?? 0) || a.name.localeCompare(b.name))
    .slice(0, MAX_SUGGESTIONS)
})

watch(input, v => {
  active.value = -1
  open.value = v.trim().length > 0
})

let suggestionsTried = false
async function ensureSuggestions() {
  if (suggestionsTried) return
  suggestionsTried = true
  try {
    // page_size=200 is the API max (docs/api-notes.md); plenty for a home
    // inventory's tag set. One fetch per component, on first focus only.
    const res = await api.get<ListResponse<Tag>>('/api/v1/tags?page_size=200')
    allTags.value = res.data
  } catch (e) {
    toast((e as Error).message, 'error')
  }
}

function commit(name: string) {
  if (!name) return
  if (props.tags.length >= 50) return // backend cap: max 50 tags
  if (props.tags.includes(name)) return // dedupe (case-sensitive)
  emit('update:tags', [...props.tags, name])
}

function add() {
  const name = input.value.trim()
  input.value = ''
  commit(name)
}

function pick(i: number) {
  const t = suggestions.value[i]
  if (!t) return
  input.value = '' // clears the dropdown too
  commit(t.name)
}

function onKeydown(e: KeyboardEvent) {
  if (open.value && suggestions.value.length) {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      active.value = active.value + 1 >= suggestions.value.length ? 0 : active.value + 1
      return
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault()
      active.value = active.value - 1 < 0 ? suggestions.value.length - 1 : active.value - 1
      return
    }
    if (e.key === 'Enter' && active.value >= 0) {
      e.preventDefault()
      pick(active.value)
      return
    }
    if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation() // don't close a surrounding dialog/modal
      open.value = false
      active.value = -1
      return
    }
  }
  if (e.key === 'Enter' || e.key === ',') {
    e.preventDefault()
    add()
  }
}

function remove(name: string) {
  emit('update:tags', props.tags.filter(t => t !== name))
}
</script>
