<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, ApiError } from '../api/client'
import type { AdminUser, Invite, ListResponse } from '../api/types'
import { useAuthStore } from '../stores/auth'
import { toast } from '../utils/toast'

const auth = useAuthStore()
const router = useRouter()

// ---- Users ----
const users = ref<AdminUser[]>([])
const busyUserId = ref<number | null>(null)

async function loadUsers() {
  try {
    users.value = (await api.get<ListResponse<AdminUser>>('/api/v1/admin/users')).data
  } catch (e) {
    if (e instanceof ApiError && e.status === 403) {
      // Non-admin landed on /admin — the nav hides the link, the backend
      // still enforces this. Say why, then bounce home.
      toast(e.message, 'error')
      router.push('/')
      return
    }
    toast((e as Error).message, 'error')
  }
}

// PATCHes ONLY the changed field (api-notes pointer semantics: absent = keep).
// The 200 body echoes the user's flags; merge those locally instead of a
// refetch (the response omits created_at/default_item_visibility).
async function patchUser(u: AdminUser, field: 'is_admin' | 'disabled', value: boolean, revert: () => void) {
  if (busyUserId.value !== null) return
  busyUserId.value = u.id
  try {
    const res = await api.patch<{ is_admin: boolean; disabled: boolean }>(
      `/api/v1/admin/users/${u.id}`, { [field]: value })
    u.is_admin = res.is_admin
    u.disabled = res.disabled
  } catch (e) {
    revert() // keep the checkbox honest; the server said no
    toast((e as Error).message, 'error') // e.g. cannot_disable_self
  } finally {
    busyUserId.value = null
  }
}

// v-model has already flipped the flag by the time @change fires — PATCH the
// new value, revert on failure.
function onAdminToggle(u: AdminUser) {
  void patchUser(u, 'is_admin', u.is_admin, () => { u.is_admin = !u.is_admin })
}
function onDisabledToggle(u: AdminUser) {
  void patchUser(u, 'disabled', u.disabled, () => { u.disabled = !u.disabled })
}

// ---- Invites ----
const invites = ref<Invite[]>([])
const newInvite = ref<Invite | null>(null)
const creatingInvite = ref(false)
const deletingInviteId = ref<number | null>(null)

async function loadInvites() {
  try {
    invites.value = (await api.get<ListResponse<Invite>>('/api/v1/admin/invites')).data
  } catch (e) {
    toast((e as Error).message, 'error')
  }
}

async function createInvite() {
  if (creatingInvite.value) return
  creatingInvite.value = true
  try {
    const inv = await api.post<{ id: number; code: string; created_at: string }>('/api/v1/admin/invites')
    // The 201 body has no used_* fields — a fresh invite is unused.
    newInvite.value = { id: inv.id, code: inv.code, created_at: inv.created_at, used_by_email: '', used_at: null }
    toast('Invite created', 'success')
    await loadInvites()
  } catch (e) {
    toast((e as Error).message, 'error')
  } finally {
    creatingInvite.value = false
  }
}

async function copyCode(code: string) {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(code)
    } else {
      // legacy fallback: select a throwaway textarea and copy
      const ta = document.createElement('textarea')
      ta.value = code
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      document.execCommand('copy')
      ta.remove()
    }
    toast('Code copied', 'success')
  } catch {
    toast('Failed to copy code', 'error')
  }
}

async function deleteInvite(inv: Invite) {
  if (deletingInviteId.value !== null) return
  deletingInviteId.value = inv.id
  try {
    await api.del(`/api/v1/admin/invites/${inv.id}`)
    toast('Invite deleted', 'success')
    await loadInvites()
  } catch (e) {
    toast((e as Error).message, 'error')
  } finally {
    deletingInviteId.value = null
  }
}

function fmtTime(iso: string | null) {
  return iso ? iso.slice(0, 16).replace('T', ' ') : ''
}

onMounted(async () => {
  await loadUsers()
  // only after a successful users load — a 403 already bounced us home
  if (users.value.length) await loadInvites()
})
</script>
<template>
  <div class="max-w-5xl space-y-6">
    <h1 class="text-2xl font-bold dark:text-slate-100">Admin</h1>

    <!-- Users -->
    <section class="bg-white dark:bg-slate-800 rounded-xl shadow p-4 space-y-3">
      <h2 class="text-sm font-semibold text-slate-700 dark:text-slate-300">Users</h2>
      <p v-if="!users.length" class="text-slate-400 dark:text-slate-500 text-sm">No users found.</p>
      <div v-else class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="text-left text-xs text-slate-500 dark:text-slate-400 border-b dark:border-slate-700">
              <th class="py-1.5 pr-2">Email</th>
              <th class="py-1.5 pr-2">Name</th>
              <th class="py-1.5 pr-2">Admin</th>
              <th class="py-1.5 pr-2">Disabled</th>
              <th class="py-1.5">Created</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="u in users" :key="u.id" data-user-row class="border-b dark:border-slate-700 last:border-0 align-top"
              :data-disabled="u.disabled ? 'true' : undefined"
              :class="{ 'line-through text-slate-400 dark:text-slate-500': u.disabled }">
              <td class="py-2 pr-2">
                <span class="break-words dark:text-slate-100">{{ u.email }}</span>
                <span v-if="u.id === auth.user?.id" data-you-badge
                  class="ml-2 whitespace-nowrap rounded-full bg-emerald-100 text-emerald-800 px-2 py-0.5 text-xs font-medium">you</span>
              </td>
              <td class="py-2 pr-2 dark:text-slate-100">{{ u.display_name }}</td>
              <td class="py-2 pr-2">
                <input v-model="u.is_admin" data-admin-toggle type="checkbox" class="rounded"
                  :disabled="busyUserId !== null" @change="onAdminToggle(u)" />
              </td>
              <td class="py-2 pr-2">
                <!-- Never render a control that could disable yourself; the
                     backend would reject it with cannot_disable_self. -->
                <input v-if="u.id !== auth.user?.id" v-model="u.disabled" data-disable-toggle type="checkbox"
                  class="rounded" :disabled="busyUserId !== null" @change="onDisabledToggle(u)" />
                <span v-else class="text-slate-300 dark:text-slate-600" title="You cannot disable your own account">—</span>
              </td>
              <td class="py-2 text-slate-500 dark:text-slate-400">{{ fmtTime(u.created_at) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <!-- Invites -->
    <section class="bg-white dark:bg-slate-800 rounded-xl shadow p-4 space-y-3">
      <div class="flex items-center justify-between gap-2">
        <h2 class="text-sm font-semibold text-slate-700 dark:text-slate-300">Invites</h2>
        <button data-create-invite type="button" :disabled="creatingInvite"
          class="rounded-lg bg-slate-900 dark:bg-slate-100 text-white dark:text-slate-900 px-3 py-1.5 text-sm hover:bg-slate-700 dark:hover:bg-slate-300 disabled:opacity-50"
          @click="createInvite">New invite</button>
      </div>

      <div v-if="newInvite" data-new-invite
        class="rounded-lg border border-emerald-300 bg-emerald-50 px-3 py-2 flex items-center justify-between gap-3">
        <span data-new-code class="font-mono text-sm break-all">{{ newInvite.code }}</span>
        <button data-copy-code type="button" class="rounded-lg border border-emerald-300 text-emerald-800 px-3 py-1.5 text-sm hover:bg-emerald-100"
          @click="copyCode(newInvite.code)">Copy</button>
      </div>

      <p v-if="!invites.length" class="text-slate-400 dark:text-slate-500 text-sm">No invites yet.</p>
      <div v-else class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="text-left text-xs text-slate-500 dark:text-slate-400 border-b dark:border-slate-700">
              <th class="py-1.5 pr-2">Code</th>
              <th class="py-1.5 pr-2">Created</th>
              <th class="py-1.5 pr-2">Used by</th>
              <th class="py-1.5 pr-2">Used at</th>
              <th class="py-1.5"></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="i in invites" :key="i.id" data-invite-row class="border-b dark:border-slate-700 last:border-0 align-top">
              <td class="py-2 pr-2 font-mono dark:text-slate-100">{{ i.code }}</td>
              <td class="py-2 pr-2 text-slate-500 dark:text-slate-400">{{ fmtTime(i.created_at) }}</td>
              <td class="py-2 pr-2 dark:text-slate-100">{{ i.used_at ? i.used_by_email : '—' }}</td>
              <td class="py-2 pr-2 text-slate-500 dark:text-slate-400">{{ fmtTime(i.used_at) }}</td>
              <td class="py-2 text-right">
                <!-- only unused invites can be deleted -->
                <button v-if="!i.used_at" data-delete-invite type="button" :disabled="deletingInviteId !== null"
                  class="text-red-600 dark:text-red-400 hover:underline disabled:opacity-50" @click="deleteInvite(i)">Delete</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </div>
</template>
