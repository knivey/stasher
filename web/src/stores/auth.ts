import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '../api/client'
import type { User } from '../api/types'

export const useAuthStore = defineStore('auth', () => {
  const user = ref<User | null>(null)
  const ready = ref(false)

  async function bootstrap() {
    try { user.value = await api.get<User>('/api/v1/auth/me') }
    catch { user.value = null }
    finally { ready.value = true }
  }
  async function login(email: string, password: string) {
    user.value = await api.post<User>('/api/v1/auth/login', { email, password })
  }
  async function register(payload: { email: string; password: string; display_name: string; invite_code?: string }) {
    user.value = await api.post<User>('/api/v1/auth/register', payload)
  }
  async function logout() {
    try { await api.post('/api/v1/auth/logout') } finally { user.value = null }
  }
  return { user, ready, bootstrap, login, register, logout }
})
