<template>
  <div class="min-h-screen flex items-center justify-center p-4 bg-slate-100">
    <form class="w-full max-w-sm bg-white rounded-xl shadow p-6 space-y-4" @submit.prevent="submit">
      <h1 class="text-xl font-semibold">Sign in to Stasher</h1>
      <input v-model="email" type="email" required autocomplete="email" placeholder="Email"
        class="w-full rounded-lg border border-slate-300 px-3 py-2" />
      <input v-model="password" type="password" required autocomplete="current-password" placeholder="Password"
        class="w-full rounded-lg border border-slate-300 px-3 py-2" />
      <p v-if="error" class="text-sm text-red-600">{{ error }}</p>
      <button :disabled="busy" class="w-full rounded-lg bg-slate-900 text-white py-2 disabled:opacity-50">
        {{ busy ? 'Signing in…' : 'Sign in' }}
      </button>
      <router-link to="/register" class="block text-center text-sm text-slate-600 hover:underline">
        Need an account? Register
      </router-link>
    </form>
  </div>
</template>
<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'

const email = ref(''), password = ref(''), error = ref(''), busy = ref(false)
const auth = useAuthStore(), router = useRouter(), route = useRoute()

async function submit() {
  busy.value = true; error.value = ''
  try {
    await auth.login(email.value, password.value)
    await router.push((route.query.redirect as string) || '/')
  } catch (e: any) { error.value = e.message }
  finally { busy.value = false }
}
</script>
