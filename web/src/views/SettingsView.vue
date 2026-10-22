<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, ApiError } from '../api/client'
import type { ListResponse, SessionRow, User } from '../api/types'
import { useAuthStore } from '../stores/auth'
import { toast } from '../utils/toast'

const auth = useAuthStore()

// ---- Profile (diff-PATCH: only fields that changed are sent) ----
const displayName = ref(auth.user?.display_name ?? '')
const visibility = ref<'private' | 'shared'>(auth.user?.default_item_visibility ?? 'private')
// Snapshot for the diff — PATCH sends each field only when it changed
// (api-notes: absent field = keep).
let profileOrig = { display_name: displayName.value, default_item_visibility: visibility.value }
const savingProfile = ref(false)

async function saveProfile() {
  if (savingProfile.value) return
  const body: Record<string, unknown> = {}
  if (displayName.value.trim() !== profileOrig.display_name) body.display_name = displayName.value.trim()
  if (visibility.value !== profileOrig.default_item_visibility) body.default_item_visibility = visibility.value
  if (!Object.keys(body).length) {
    toast('No changes to save')
    return
  }
  savingProfile.value = true
  try {
    // 200 returns the updated user — assign into the store so the app shell
    // (and any new-item default visibility) picks it up immediately.
    const u = await api.patch<User>('/api/v1/auth/me', body)
    auth.user = u
    displayName.value = u.display_name
    visibility.value = u.default_item_visibility
    profileOrig = { display_name: u.display_name, default_item_visibility: u.default_item_visibility }
    toast('Profile saved', 'success')
  } catch (e) {
    toast((e as Error).message, 'error')
  } finally {
    savingProfile.value = false
  }
}

// ---- Password ----
const pwCurrent = ref('')
const pwNew = ref('')
const pwRevokeOthers = ref(false)
const pwError = ref('') // inline, at the current-password field
const changingPw = ref(false)

async function changePassword() {
  if (changingPw.value) return
  changingPw.value = true
  pwError.value = ''
  const revoke = pwRevokeOthers.value
  try {
    // 204 on success. The current session always survives (backend never
    // revokes it), so no forced re-login — just clear the form.
    await api.post('/api/v1/auth/password', {
      current_password: pwCurrent.value,
      new_password: pwNew.value,
      revoke_other_sessions: revoke,
    })
    toast('Password changed', 'success')
    pwCurrent.value = ''
    pwNew.value = ''
    pwRevokeOthers.value = false
    if (revoke) await fetchSessions() // other devices died server-side
  } catch (e) {
    if (e instanceof ApiError && e.code === 'invalid_current_password') pwError.value = e.message
    toast((e as Error).message, 'error')
  } finally {
    changingPw.value = false
  }
}

// ---- Devices (sessions) ----
const sessions = ref<SessionRow[]>([])
const revokingId = ref<number | null>(null)
const revokingAll = ref(false)

async function fetchSessions() {
  try {
    const res = await api.get<ListResponse<SessionRow>>('/api/v1/auth/sessions')
    sessions.value = res.data
  } catch (e) {
    toast((e as Error).message, 'error')
  }
}
onMounted(fetchSessions)

// Only non-current rows are revocable — the current session's button is
// never rendered (logout lives in the app shell instead).
const hasOthers = computed(() => sessions.value.some(s => !s.current))

async function revokeSession(s: SessionRow) {
  if (revokingId.value !== null) return
  revokingId.value = s.id
  try {
    await api.del(`/api/v1/auth/sessions/${s.id}`)
    toast('Device revoked', 'success')
    await fetchSessions()
  } catch (e) {
    toast((e as Error).message, 'error')
  } finally {
    revokingId.value = null
  }
}

async function revokeAllOthers() {
  if (revokingAll.value) return
  revokingAll.value = true
  try {
    // except_current keeps THIS session alive — no re-login.
    await api.post('/api/v1/auth/sessions/revoke-all', { except_current: true })
    toast('Other devices revoked', 'success')
    await fetchSessions()
  } catch (e) {
    toast((e as Error).message, 'error')
  } finally {
    revokingAll.value = false
  }
}

function fmtTime(iso: string) {
  return iso ? iso.slice(0, 16).replace('T', ' ') : ''
}
</script>
<template>
  <div class="max-w-3xl space-y-6">
    <h1 class="text-2xl font-bold dark:text-slate-100">Settings</h1>

    <!-- Profile -->
    <form data-profile-form class="bg-white dark:bg-slate-800 rounded-xl shadow p-4 space-y-3" @submit.prevent="saveProfile">
      <h2 class="text-sm font-semibold text-slate-700 dark:text-slate-300">Profile</h2>
      <label class="block text-sm font-medium dark:text-slate-100">Display name
        <input v-model="displayName" data-profile-name required maxlength="100"
          class="mt-1 w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal" />
      </label>
      <label class="block text-sm font-medium dark:text-slate-100">Default item visibility
        <select v-model="visibility" data-profile-visibility class="mt-1 w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal">
          <option value="private">Private</option>
          <option value="shared">Shared</option>
        </select>
      </label>
      <button type="submit" :disabled="savingProfile"
        class="rounded-lg bg-slate-900 dark:bg-slate-100 text-white dark:text-slate-900 px-3 py-1.5 text-sm disabled:opacity-50">Save profile</button>
    </form>

    <!-- Password -->
    <form data-pw-form class="bg-white dark:bg-slate-800 rounded-xl shadow p-4 space-y-3" @submit.prevent="changePassword">
      <h2 class="text-sm font-semibold text-slate-700 dark:text-slate-300">Change password</h2>
      <label class="block text-sm font-medium dark:text-slate-100">Current password
        <input v-model="pwCurrent" data-pw-current type="password" required autocomplete="current-password"
          class="mt-1 w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal" @input="pwError = ''" />
        <p v-if="pwError" data-pw-error class="mt-1 text-xs text-red-600 dark:text-red-400">{{ pwError }}</p>
      </label>
      <label class="block text-sm font-medium dark:text-slate-100">New password
        <!-- minlength is the ≥10 client check (browser blocks the submit);
             the backend enforces it again with 400 validation_error. -->
        <input v-model="pwNew" data-pw-new type="password" required minlength="10" autocomplete="new-password"
          class="mt-1 w-full border dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 rounded-lg px-2 py-1.5 text-sm font-normal" />
      </label>
      <label class="flex items-center gap-2 text-sm dark:text-slate-100">
        <input v-model="pwRevokeOthers" data-pw-revoke-others type="checkbox" class="rounded" />
        Revoke other sessions (sign out everywhere else)
      </label>
      <button type="submit" :disabled="changingPw"
        class="rounded-lg bg-slate-900 dark:bg-slate-100 text-white dark:text-slate-900 px-3 py-1.5 text-sm disabled:opacity-50">Change password</button>
    </form>

    <!-- Devices -->
    <section class="bg-white dark:bg-slate-800 rounded-xl shadow p-4 space-y-3">
      <div class="flex items-center justify-between gap-2">
        <h2 class="text-sm font-semibold text-slate-700 dark:text-slate-300">Devices</h2>
        <button v-if="hasOthers" data-revoke-all type="button" :disabled="revokingAll"
          class="rounded-lg border border-red-300 text-red-700 dark:text-red-400 dark:hover:bg-red-400/10 px-3 py-1.5 text-sm hover:bg-red-50 disabled:opacity-50"
          @click="revokeAllOthers">Revoke all other devices</button>
      </div>
      <p v-if="!sessions.length" class="text-slate-400 dark:text-slate-500 text-sm">No devices found.</p>
      <div v-else class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="text-left text-xs text-slate-500 dark:text-slate-400 border-b dark:border-slate-700">
              <th class="py-1.5 pr-2">Device</th>
              <th class="py-1.5 pr-2">IP</th>
              <th class="py-1.5 pr-2">Last seen</th>
              <th class="py-1.5"></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="s in sessions" :key="s.id" data-session-row class="border-b dark:border-slate-700 last:border-0 align-top">
              <td class="py-2 pr-2 max-w-48">
                <span class="break-words dark:text-slate-100">{{ s.user_agent || 'Unknown device' }}</span>
                <span v-if="s.current" data-current-badge
                  class="ml-2 whitespace-nowrap rounded-full bg-emerald-100 text-emerald-800 px-2 py-0.5 text-xs font-medium">This device</span>
              </td>
              <td class="py-2 pr-2 text-slate-500 dark:text-slate-400">{{ s.last_ip }}</td>
              <td class="py-2 pr-2 text-slate-500 dark:text-slate-400">{{ fmtTime(s.last_seen_at) }}</td>
              <td class="py-2 text-right">
                <button v-if="!s.current" data-revoke-session type="button"
                  :disabled="revokingId !== null"
                  class="text-red-600 dark:text-red-400 hover:underline disabled:opacity-50" @click="revokeSession(s)">Revoke</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </div>
</template>
