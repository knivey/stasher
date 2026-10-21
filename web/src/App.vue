<script setup lang="ts">
import { useRouter } from 'vue-router'
import Toasts from './components/Toasts.vue'
import { setUnauthorizedHandler } from './api/client'
import { useAuthStore } from './stores/auth'
import { toast } from './utils/toast'

const router = useRouter()
const auth = useAuthStore()
setUnauthorizedHandler(() => {
  if (auth.user) toast('Session ended — please sign in again', 'error')
  auth.user = null
  if (router.currentRoute.value.path !== '/login') router.push('/login')
})
</script>

<template>
  <router-view />
  <Toasts />
</template>
