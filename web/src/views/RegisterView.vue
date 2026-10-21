<template>
  <div class="min-h-screen flex items-center justify-center p-4 bg-slate-100">
    <form class="w-full max-w-sm bg-white rounded-xl shadow p-6 space-y-4" @submit.prevent="submit">
      <h1 class="text-xl font-semibold">Create your Stasher account</h1>
      <input v-model="email" type="email" required autocomplete="email" placeholder="Email"
        class="w-full rounded-lg border border-slate-300 px-3 py-2" />
      <input v-model="displayName" type="text" required autocomplete="name" placeholder="Display name"
        class="w-full rounded-lg border border-slate-300 px-3 py-2" />
      <input v-model="password" type="password" required autocomplete="new-password" placeholder="Password"
        class="w-full rounded-lg border border-slate-300 px-3 py-2" />
      <input v-model="invite" type="text" placeholder="Invite code (if required)"
        class="w-full rounded-lg border px-3 py-2"
        :class="inviteError ? 'border-red-500' : 'border-slate-300'" />
      <p v-if="inviteError" class="text-sm text-red-600">{{ inviteError }}</p>
      <p v-if="error" class="text-sm text-red-600">{{ error }}</p>
      <button :disabled="busy" class="w-full rounded-lg bg-slate-900 text-white py-2 disabled:opacity-50">
        {{ busy ? 'Creating account…' : 'Register' }}
      </button>
      <router-link to="/login" class="block text-center text-sm text-slate-600 hover:underline">
        Already have an account? Sign in
      </router-link>
    </form>
  </div>
</template>
<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError } from '../api/client'
import { useAuthStore } from '../stores/auth'

const email = ref(''), displayName = ref(''), password = ref(''), invite = ref('')
const error = ref(''), inviteError = ref(''), busy = ref(false)
const auth = useAuthStore(), router = useRouter()

async function submit() {
  busy.value = true; error.value = ''; inviteError.value = ''
  try {
    await auth.register({
      email: email.value,
      password: password.value,
      display_name: displayName.value,
      ...(invite.value ? { invite_code: invite.value } : {}),
    })
    await router.push('/')
  } catch (e: any) {
    if (e instanceof ApiError && e.code === 'invalid_invite') inviteError.value = e.message
    else error.value = e.message
  } finally { busy.value = false }
}
</script>
