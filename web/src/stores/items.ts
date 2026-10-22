import { defineStore } from 'pinia'
import { reactive, ref } from 'vue'
import { api } from '../api/client'
import type { Item, ListResponse } from '../api/types'

export const useItemsStore = defineStore('items', () => {
  const items = ref<Item[]>([])
  const total = ref(0)
  const loading = ref(false)
  function defaultFilters() {
    return {
      q: '', location_id: 0, tag: '', project_id: 0,
      visibility: '' as '' | 'private' | 'shared',
      sort: 'updated_at' as 'updated_at' | 'created_at' | 'name' | 'qty',
      order: 'desc' as 'asc' | 'desc',
      page: 1, page_size: 50,
    }
  }
  const filters = reactive(defaultFilters())
  // ItemsView calls this on unmount so returning to the page starts clean;
  // filters set from another view before navigating are applied AFTER the
  // previous page's reset, so the tag-chip flow still works.
  function resetFilters() { Object.assign(filters, defaultFilters()) }

  async function fetch() {
    loading.value = true
    try {
      const p = new URLSearchParams()
      if (filters.q) p.set('q', filters.q)
      if (filters.location_id) p.set('location_id', String(filters.location_id))
      if (filters.tag) p.set('tag', filters.tag)
      if (filters.project_id) p.set('project_id', String(filters.project_id))
      if (filters.visibility) p.set('visibility', filters.visibility)
      p.set('sort', filters.sort); p.set('order', filters.order)
      p.set('page', String(filters.page)); p.set('page_size', String(filters.page_size))
      const res = await api.get<ListResponse<Item>>(`/api/v1/items?${p}`)
      items.value = res.data; total.value = res.total
    } finally { loading.value = false }
  }
  return { items, total, loading, filters, fetch, resetFilters }
})
