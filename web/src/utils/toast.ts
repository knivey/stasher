import { reactive } from 'vue'
export interface Toast { id: number; message: string; type: 'info' | 'error' | 'success' }
export const toasts = reactive<Toast[]>([])
let next = 1
export function toast(message: string, type: Toast['type'] = 'info') {
  const t = { id: next++, message, type }
  toasts.push(t)
  setTimeout(() => {
    const i = toasts.indexOf(t)
    if (i >= 0) toasts.splice(i, 1)
  }, 4000)
}
