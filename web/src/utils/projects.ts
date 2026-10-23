import type { Project } from '../api/types'

export const PROJECT_STATUSES: Project['status'][] = ['planned', 'active', 'done', 'archived']

// Badge colors per spec: planned slate, active emerald, done blue, archived gray.
export function statusBadgeClass(status: string): string {
  switch (status) {
    case 'active': return 'bg-emerald-100 text-emerald-700'
    case 'done': return 'bg-blue-100 text-blue-700'
    case 'archived': return 'bg-gray-100 text-gray-500'
    default: return 'bg-slate-100 text-slate-700'
  }
}

// Visibility badge: shared = green accent, private = gray.
export function visibilityBadgeClass(visibility: string): string {
  return visibility === 'shared' ? 'bg-green-100 text-green-700' : 'bg-gray-100 text-gray-500'
}
