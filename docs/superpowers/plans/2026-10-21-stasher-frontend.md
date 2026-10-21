# Stasher Frontend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Vue 3 + Tailwind SPA for Stasher — all views in spec §8 — served by the Go binary from `internal/webfs/dist`.

**Architecture:** Vite SPA at `web/`, dev via proxy to the Go API on `:8080`, prod built directly into `internal/webfs/dist` (committed) and embedded via the existing `go:embed`. Cookie-session auth (the SPA never touches the token — `httpOnly` cookie rides requests same-origin). A typed fetch client maps every API response/error; Pinia stores own auth + shared list state; views call resource helpers directly. Mobile-first responsive (cards < md, tables ≥ md), PWA-installable, camera capture on uploads with client-side downscale.

**Tech Stack:** Vue 3.5 + `<script setup lang="ts">`, Vite 7, Tailwind CSS v4 (`@tailwindcss/vite` 4.3.x), Pinia 3, vue-router 4, TypeScript 5 (strict), Vitest 3 (happy-dom), Headless Chromium for integration smoke.

**Spec:** `docs/superpowers/specs/2026-10-21-stasher-design.md` (§7 API surface, §8 frontend) + **`docs/api-notes.md` is binding contract detail** — read both.

## Global Constraints

- API base: same-origin, prefix `/api/v1`; files at `/files/{key}`. No auth headers ever — the `httpOnly` session cookie is sent automatically. Dev proxy: `/api` and `/files` → `http://127.0.0.1:8080`.
- Error shape is `{"error":{"code","message"}}` → surface `message` via toast on every mutation; NEVER swallow an error silently.
- PATCH semantics (api-notes): absent field = keep. Items: `location_id: 0` clears, omit = keep. Locations: parent cannot be cleared; `notes` omitted = kept. Send ONLY changed fields on PATCH.
- Pagination: `page` (1-based) + `page_size` (default 50, max 200); `{"data":[],"total":N}`; empty page reports `total: 0` — read totals from page 1.
- `sort` ∈ {updated_at, created_at, name, qty}, `order` ∈ {asc, desc} on GET /items.
- NO `v-html` anywhere — item/user content renders via text interpolation only.
- TypeScript strict; `vue-tsc --noEmit` must pass; Vitest must pass; keep output pristine.
- Build output: `web/` Vite root, `build.outDir = '../internal/webfs/dist'`, `emptyOutDir: true`; **the built `dist/` is committed** (go:embed needs it on fresh clone; placeholder index.html is replaced on first build).
- Photo upload: `<input type="file" accept="image/*" capture="environment">`, downscale to ≤2000px JPEG (quality 0.85) before upload; skip downscale when already small. Documents upload as-is.
- npm scripts live in `web/package.json`; Makefile gains `web*` targets. Conventional commits.
- Photos render via `<img src="/files/{storage_key}">` (cookie-gated, works same-origin). Inline types (image/pdf/text) preview; others download (backend policy).

## Review Focus

1. **Stale session mid-use** — a 401 from any call (session revoked on another device) must route to `/login` with a toast, not leave spinners forever. Test in Task 2 (client 401 handler) + Task 14 (chromium smoke).
2. **location_id clear-vs-keep** — the item form must send `location_id: 0` ONLY when the user explicitly clears, and omit the field when untouched. Pinned by unit test in Task 6.
3. **Silent failure** — every mutating action surfaces ApiError via toast; a search of the codebase for empty `catch` finds none. Pinned per-view in Tasks 4–13 assertions and a grep check in Task 14.
4. **Mobile usability** — bottom nav visible < md, forms/tables usable at 375px. Chromium smoke at narrow viewport in Task 14.
5. **Content injection** — no `v-html` in any component; grep-pinned in Task 14.

---

## File Structure

```
web/
├── package.json, vite.config.ts, tsconfig.json, vitest.config.ts (may merge into vite.config)
├── index.html                        # root div, manifest link, theme-color, apple meta
├── public/manifest.webmanifest, public/icons/icon-*.png
└── src/
    ├── main.ts                       # pinia + router + toasts + auth bootstrap
    ├── App.vue                       # <router-view> + Toasts
    ├── api/
    │   ├── client.ts                 # typed fetch, ApiError, 401 hook, upload helper
    │   └── types.ts                  # User, Item, ItemDetail, Photo, Document, Location,
    │                                 # Tag, Project, ProjectItem, SessionRow, Invite, SharedItem
    ├── stores/
    │   ├── auth.ts                   # user, ready, login/register/logout, devices, password
    │   └── items.ts                  # list state, filters, fetchItems
    ├── router/index.ts               # routes + auth guard (waits for bootstrap)
    ├── components/
    │   ├── AppShell.vue              # sidebar (md+) + bottom nav (<md) + user menu
    │   ├── Toasts.vue, Pagination.vue, ConfirmDialog.vue
    │   ├── TagInput.vue              # chip-style tag editor
    │   ├── LocationSelect.vue        # flattened indented options from locations store fetch
    │   ├── PhotoGallery.vue          # grid + lightbox + delete
    │   └── FileUpload.vue            # capture attr + downscale + progress state
    ├── utils/
    │   ├── image.ts                  # downscaleImage
    │   └── toast.ts                  # tiny reactive toast store + toast() helper
    └── views/
        ├── LoginView.vue, RegisterView.vue
        ├── DashboardView.vue
        ├── ItemsView.vue, ItemDetailView.vue, ItemFormView.vue
        ├── LocationsView.vue, TagsView.vue
        ├── ProjectsView.vue, ProjectDetailView.vue
        ├── SharedView.vue, SettingsView.vue, AdminView.vue
internal/webfs/dist/                  # committed build output (replaces placeholder)
```

---

### Task 1: Vite scaffold, Tailwind v4, API client, types

**Files:**
- Create: `web/package.json`, `web/vite.config.ts`, `web/tsconfig.json`, `web/index.html`, `web/src/main.ts`, `web/src/App.vue`, `web/src/api/client.ts`, `web/src/api/types.ts`, `web/src/assets/main.css`, `web/src/api/client.test.ts`
- Modify: `.gitignore` (add `web/node_modules`, keep dist tracked)

**Interfaces:**
- Produces: `api.get/post/patch/del<T>` + `api.upload<T>`; `class ApiError {status, code, message}`; `setUnauthorizedHandler(fn)`; all shared types; Vite dev server proxying `/api` + `/files`.

- [ ] **Step 1: Scaffold**

```bash
cd /home/knivey/stasher/web
npm create vite@latest . -- --template vue-ts   # in empty dir; overwrite nothing
npm install
npm install pinia vue-router@4
npm install -D tailwindcss @tailwindcss/vite vitest happy-dom @vue/test-utils
```

`web/vite.config.ts`:

```ts
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  build: { outDir: '../internal/webfs/dist', emptyOutDir: true },
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:8080',
      '/files': 'http://127.0.0.1:8080',
    },
  },
  test: { environment: 'happy-dom' },
})
```

`web/src/assets/main.css`: `@import "tailwindcss";` and import it in `main.ts`.
`web/src/App.vue` (temporary): `<template><div class="p-4"><router-view /></div></template>` (router arrives Task 2; for now render `<div id="app" class="p-4">stasher</div>` — wire router in Task 2).

`web/index.html`: root div `#app`, `<meta name="viewport" content="width=device-width, initial-scale=1">`, `<meta name="theme-color" content="#1e293b">`, script module `/src/main.ts`. Manifest link added Task 14.

- [ ] **Step 2: Write the failing client tests**

`web/src/api/client.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { api, ApiError, setUnauthorizedHandler } from './client'

function mockFetch(status: number, body: unknown) {
  return vi.fn().mockResolvedValue(
    new Response(body === undefined ? null : JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    }),
  )
}

beforeEach(() => setUnauthorizedHandler(null))

describe('api client', () => {
  it('returns parsed JSON on ok', async () => {
    vi.stubGlobal('fetch', mockFetch(200, { data: [{ id: 1 }], total: 1 }))
    const res = await api.get('/api/v1/items')
    expect(res).toEqual({ data: [{ id: 1 }], total: 1 })
  })

  it('returns undefined on 204', async () => {
    vi.stubGlobal('fetch', mockFetch(204, undefined))
    expect(await api.del('/api/v1/tags/1')).toBeUndefined()
  })

  it('throws ApiError with code+message from error shape', async () => {
    vi.stubGlobal('fetch', mockFetch(409, { error: { code: 'tag_exists', message: 'tag already exists' } }))
    const err = await api.post('/api/v1/tags', { name: 'x' }).catch(e => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(409)
    expect(err.code).toBe('tag_exists')
    expect(err.message).toBe('tag already exists')
  })

  it('fires unauthorized handler once on 401', async () => {
    const on401 = vi.fn()
    setUnauthorizedHandler(on401)
    vi.stubGlobal('fetch', mockFetch(401, { error: { code: 'unauthorized', message: 'authentication required' } }))
    await expect(api.get('/api/v1/auth/me')).rejects.toBeInstanceOf(ApiError)
    expect(on401).toHaveBeenCalledTimes(1)
  })

  it('upload posts FormData', async () => {
    const f = vi.fn().mockResolvedValue(new Response('{"id":1}', { status: 201 }))
    vi.stubGlobal('fetch', f)
    const blob = new Blob(['x'], { type: 'image/png' })
    await api.upload('/api/v1/items/1/photos', 'file', blob, 'p.png')
    const [url, init] = f.mock.calls[0]
    expect(url).toBe('/api/v1/items/1/photos')
    expect(init.body).toBeInstanceOf(FormData)
    expect(init.body.get('file')).toBeTruthy()
  })
})
```

- [ ] **Step 3: Run to verify failure**

Run: `cd web && npx vitest run` — Expected: FAIL (client.ts doesn't exist).

- [ ] **Step 4: Implement client + types**

`web/src/api/client.ts`:

```ts
export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message)
  }
}

let onUnauthorized: (() => void) | null = null
export function setUnauthorizedHandler(fn: (() => void) | null) { onUnauthorized = fn }

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const resp = await fetch(path, {
    method,
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : {},
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  return handle<T>(resp)
}

async function handle<T>(resp: Response): Promise<T> {
  if (resp.status === 204) return undefined as T
  const data = await resp.json().catch(() => null)
  if (!resp.ok) {
    const e = data?.error
    if (resp.status === 401 && onUnauthorized) onUnauthorized()
    throw new ApiError(resp.status, e?.code ?? 'unknown', e?.message ?? resp.statusText)
  }
  return data as T
}

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body),
  patch: <T>(path: string, body?: unknown) => request<T>('PATCH', path, body),
  del: (path: string) => request<void>('DELETE', path),
  upload: async <T>(path: string, field: string, file: Blob, filename: string): Promise<T> => {
    const fd = new FormData()
    fd.append(field, file, filename)
    return handle<T>(await fetch(path, { method: 'POST', body: fd }))
  },
}
```

`web/src/api/types.ts` (mirrors backend JSON exactly):

```ts
export interface User { id: number; email: string; display_name: string; default_item_visibility: 'private' | 'shared'; is_admin: boolean }
export interface Tag { id: number; name: string; item_count?: number }
export interface Photo { id: number; storage_key: string; caption: string; sort_order: number; created_at: string }
export interface Doc { id: number; storage_key: string; filename: string; mime_type: string; size_bytes: number; created_at: string }
export interface ItemProject { id: number; name: string; status: string; qty_used: number | null }
export interface Item {
  id: number; name: string; description: string; qty: number; unit: string
  location_id: number | null; visibility: 'private' | 'shared'
  photo_count?: number; created_at: string; updated_at: string
  tags?: Tag[]; photos?: Photo[]; documents?: Doc[]; projects?: ItemProject[]; owner_name?: string
}
export interface Location { id: number; name: string; parent_id: number | null; notes: string; created_at: string; updated_at: string }
export interface Breadcrumb { id: number; name: string }
export interface Project { id: number; name: string; status: 'planned' | 'active' | 'done' | 'archived'; notes: string; created_at: string; updated_at: string }
export interface ProjectItem { item_id: number; item_name: string; item_qty: number; item_unit: string; qty_used: number | null; notes: string; added_at: string }
export interface SessionRow { id: number; user_agent: string; last_ip: string; created_at: string; last_seen_at: string; current: boolean }
export interface Invite { id: number; code: string; created_at: string; used_by_email: string; used_at: string | null }
export interface AdminUser extends User { disabled: boolean; created_at: string }
export interface ListResponse<T> { data: T[]; total: number }
```

Add `.gitignore` lines: `web/node_modules/`, `web/dist/` (NOT `internal/webfs/dist` — that stays tracked).

- [ ] **Step 5: Verify + typecheck + commit**

```bash
cd web && npx vitest run && npx vue-tsc --noEmit
cd .. && git add -A && git commit -m "feat(web): vite scaffold with typed api client"
```

(Note: `vue-tsc` needs `"build.vue-tsc"` availability — `npm i -D vue-tsc` if the template lacks it.)

---

### Task 2: Auth store, router + guards, Login/Register, App shell, toasts

**Files:**
- Create: `src/stores/auth.ts`, `src/router/index.ts`, `src/utils/toast.ts`, `src/components/Toasts.vue`, `src/components/AppShell.vue`, `src/views/LoginView.vue`, `src/views/RegisterView.vue`, `src/stores/auth.test.ts`
- Modify: `src/main.ts`, `src/App.vue`

**Interfaces:**
- Consumes: `api`, `User` type, `setUnauthorizedHandler`.
- Produces: `useAuthStore()` (`user: Ref<User|null>`, `ready: Ref<boolean>`, `bootstrap()`, `login()`, `register()`, `logout()`); router with `/login`, `/register` public and `/` (shell) guarded; `toast(message, type?)` helper + `<Toasts/>`; `AppShell` with nav items Dashboard/Items/Locations/Projects/Shared/Settings (+Admin when `user.is_admin`).

- [ ] **Step 1: Write failing auth store tests**

`src/stores/auth.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore } from './auth'

const me = { id: 1, email: 'a@b.c', display_name: 'A', default_item_visibility: 'private', is_admin: false }

beforeEach(() => { setActivePinia(createPinia()); vi.unstubAllGlobals() })

describe('auth store', () => {
  it('bootstrap hydrates user on 200', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify(me), { status: 200 })))
    const s = useAuthStore()
    await s.bootstrap()
    expect(s.user?.email).toBe('a@b.c')
    expect(s.ready).toBe(true)
  })

  it('bootstrap clears user on 401', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'x' } }), { status: 401 })))
    const s = useAuthStore()
    await s.bootstrap()
    expect(s.user).toBeNull()
    expect(s.ready).toBe(true)
  })

  it('login stores user and clears error', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify(me), { status: 200 })))
    const s = useAuthStore()
    await s.login('a@b.c', 'longpassword1')
    expect(s.user?.email).toBe('a@b.c')
  })
})
```

- [ ] **Step 2: Run** — `npx vitest run` FAIL (store missing).

- [ ] **Step 3: Implement store, toasts, router, views, shell**

`src/utils/toast.ts`:

```ts
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
```

`src/components/Toasts.vue`:

```vue
<template>
  <div class="fixed bottom-20 md:bottom-4 right-4 z-50 flex flex-col gap-2" aria-live="polite">
    <div v-for="t in toasts" :key="t.id"
      class="rounded-lg px-4 py-2 text-sm shadow-lg max-w-xs"
      :class="t.type === 'error' ? 'bg-red-600 text-white' : t.type === 'success' ? 'bg-emerald-600 text-white' : 'bg-slate-800 text-white'">
      {{ t.message }}
    </div>
  </div>
</template>
<script setup lang="ts">
import { toasts } from '../utils/toast'
</script>
```

`src/stores/auth.ts`:

```ts
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
```

`src/router/index.ts`:

```ts
import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '../stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: () => import('../views/LoginView.vue') },
    { path: '/register', component: () => import('../views/RegisterView.vue') },
    {
      path: '/',
      component: () => import('../components/AppShell.vue'),
      children: [
        { path: '', component: () => import('../views/DashboardView.vue') },
        { path: 'items', component: () => import('../views/ItemsView.vue') },
        { path: 'items/new', component: () => import('../views/ItemFormView.vue') },
        { path: 'items/:id', component: () => import('../views/ItemDetailView.vue') },
        { path: 'items/:id/edit', component: () => import('../views/ItemFormView.vue') },
        { path: 'locations', component: () => import('../views/LocationsView.vue') },
        { path: 'tags', component: () => import('../views/TagsView.vue') },
        { path: 'projects', component: () => import('../views/ProjectsView.vue') },
        { path: 'projects/:id', component: () => import('../views/ProjectDetailView.vue') },
        { path: 'shared', component: () => import('../views/SharedView.vue') },
        { path: 'settings', component: () => import('../views/SettingsView.vue') },
        { path: 'admin', component: () => import('../views/AdminView.vue') },
      ],
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

router.beforeEach((to) => {
  const auth = useAuthStore()
  if (!auth.ready) return auth.bootstrap().then(() => true)
  if (to.path !== '/login' && to.path !== '/register' && !auth.user)
    return { path: '/login', query: { redirect: to.fullPath } }
  return true
})

export default router
```

`src/main.ts`:

```ts
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import './assets/main.css'

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')
```

`src/App.vue`: `<template><router-view /><Toasts /></template>` + import Toasts.
Wire the 401 hook inside `App.vue` `setup` (needs router + store after install):

```ts
import { watch } from 'vue'
import { useRouter } from 'vue-router'
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
```

`src/views/LoginView.vue` (Register similar + display_name + optional invite_code field):

```vue
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
```

`RegisterView.vue` — same shape with `display_name` + `invite_code` (optional, labeled "Invite code (if required)") fields posting `auth.register(...)`; on ApiError `code === 'invalid_invite'` highlight that field's message.

`src/components/AppShell.vue`:

```vue
<template>
  <div class="min-h-screen bg-slate-100">
    <!-- desktop sidebar -->
    <aside class="hidden md:flex fixed inset-y-0 left-0 w-56 flex-col bg-slate-900 text-slate-100 p-4 gap-1">
      <h1 class="text-lg font-bold px-2 py-3">Stasher</h1>
      <router-link v-for="n in nav" :key="n.to" :to="n.to"
        class="rounded-lg px-3 py-2 hover:bg-slate-700"
        :class="{ 'bg-slate-700': route.path.startsWith(n.to) && n.to !== '/' }">
        {{ n.label }}
      </router-link>
      <div class="mt-auto text-sm">
        <div class="px-3 py-2 truncate">{{ auth.user?.display_name }}</div>
        <button class="w-full text-left rounded-lg px-3 py-2 hover:bg-slate-700" @click="logout">Sign out</button>
      </div>
    </aside>
    <!-- mobile top bar + bottom nav -->
    <header class="md:hidden sticky top-0 z-10 bg-slate-900 text-white px-4 py-3 flex justify-between">
      <span class="font-bold">Stasher</span>
      <button @click="logout">Sign out</button>
    </header>
    <main class="md:pl-56 pb-20 md:pb-0">
      <div class="p-4 max-w-5xl mx-auto"><router-view /></div>
    </main>
    <nav class="md:hidden fixed bottom-0 inset-x-0 z-10 bg-slate-900 text-slate-100 flex">
      <router-link v-for="n in mobileNav" :key="n.to" :to="n.to"
        class="flex-1 text-center text-xs py-2">{{ n.short }}</router-link>
    </nav>
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'

const auth = useAuthStore(), route = useRoute(), router = useRouter()
const nav = computed(() => {
  const base = [
    { to: '/', label: 'Dashboard' }, { to: '/items', label: 'Items' },
    { to: '/locations', label: 'Locations' }, { to: '/projects', label: 'Projects' },
    { to: '/shared', label: 'Shared' }, { to: '/settings', label: 'Settings' },
  ]
  return auth.user?.is_admin ? [...base, { to: '/admin', label: 'Admin' }] : base
})
const mobileNav = computed(() => nav.value.slice(0, 5).map(n => ({ to: n.to, short: n.label.slice(0, 5) })))
async function logout() { await auth.logout(); router.push('/login') }
</script>
```

Create placeholder view files for every route (each `web/src/views/X.vue` containing `<template><div>{{ 'X' }}</div></template>`) so the router builds — later tasks replace them one by one.

- [ ] **Step 4: Verify**

```bash
cd web && npx vitest run && npx vue-tsc --noEmit && npm run build
```

Expected: tests pass, build succeeds into `../internal/webfs/dist` (placeholder replaced — commit it). Also run backend `go build ./...` once to prove embed still compiles.

- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat(web): auth flow, router, app shell"`

---

### Task 3: Dashboard view

**Files:**
- Modify: `src/views/DashboardView.vue`
- Test: `src/views/DashboardView.test.ts`

**Interfaces:**
- Consumes: `api`, `ListResponse<Item>`, `ListResponse<Location>`, `ListResponse<Project>`.
- Produces: `/` showing item/location/project counts, 10 recent items (sort=created_at&order=desc), active projects; every card links into the app.

- [ ] **Step 1: Failing test**

```ts
import { describe, it, expect, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import DashboardView from './DashboardView.vue'

describe('DashboardView', () => {
  it('renders counts and recent items from three API calls', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      calls.push(url)
      const body = url.includes('/items?') ? { data: Array.from({ length: 10 }, (_, i) => ({ id: i, name: `Item ${i}` })), total: 123 }
        : url.includes('/locations') ? { data: [{ id: 1 }], total: 7 }
        : { data: [{ id: 1, status: 'active' }], total: 3 }
      return new Response(JSON.stringify(body), { status: 200 })
    }))
    const w = mount(DashboardView)
    await flushPromises()
    expect(calls.some(c => c.includes('sort=created_at'))).toBe(true)
    expect(w.text()).toContain('123')
    expect(w.text()).toContain('7')
    expect(w.text()).toContain('Item 0')
  })
})
```

- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement** — three `api.get` calls (`/api/v1/items?page_size=10&sort=created_at&order=desc`, `/api/v1/locations`, `/api/v1/projects?page_size=200`), count cards, recent list, active projects; errors toast per call.

```vue
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../api/client'
import type { Item, ListResponse, Location, Project } from '../api/types'
import { toast } from '../utils/toast'

const items = ref<ListResponse<Item>>({ data: [], total: 0 })
const locations = ref<ListResponse<Location>>({ data: [], total: 0 })
const projects = ref<ListResponse<Project>>({ data: [], total: 0 })
const activeProjects = ref<Project[]>([])

onMounted(async () => {
  const jobs: [Promise<unknown>, (v: any) => void, string][] = [
    [api.get('/api/v1/items?page_size=10&sort=created_at&order=desc'), v => items.value = v, 'items'],
    [api.get('/api/v1/locations'), v => locations.value = v, 'locations'],
    [api.get('/api/v1/projects?page_size=200'), v => projects.value = v, 'projects'],
  ]
  await Promise.all(jobs.map(([p, set, label]) => p.then(set).catch(e => toast(`${label}: ${e.message}`, 'error'))))
  activeProjects.value = projects.value.data.filter(p => p.status === 'active')
})
</script>
<template>
  <div class="space-y-6">
    <h1 class="text-2xl font-bold">Dashboard</h1>
    <div class="grid grid-cols-3 gap-3">
      <router-link to="/items" class="bg-white rounded-xl shadow p-4 text-center">
        <div class="text-3xl font-bold">{{ items.total }}</div><div class="text-sm text-slate-500">Items</div></router-link>
      <router-link to="/locations" class="bg-white rounded-xl shadow p-4 text-center">
        <div class="text-3xl font-bold">{{ locations.total }}</div><div class="text-sm text-slate-500">Locations</div></router-link>
      <router-link to="/projects" class="bg-white rounded-xl shadow p-4 text-center">
        <div class="text-3xl font-bold">{{ projects.total }}</div><div class="text-sm text-slate-500">Projects</div></router-link>
    </div>
    <section>
      <h2 class="font-semibold mb-2">Recently added</h2>
      <ul class="bg-white rounded-xl shadow divide-y">
        <li v-for="i in items.data" :key="i.id">
          <router-link :to="`/items/${i.id}`" class="flex justify-between px-4 py-2 hover:bg-slate-50">
            <span>{{ i.name }}</span><span class="text-slate-400 text-sm">{{ i.created_at.slice(0, 10) }}</span>
          </router-link>
        </li>
      </ul>
    </section>
    <section>
      <h2 class="font-semibold mb-2">Active projects</h2>
      <div class="flex flex-wrap gap-2">
        <router-link v-for="p in activeProjects" :key="p.id" :to="`/projects/${p.id}`"
          class="bg-white rounded-lg shadow px-3 py-1.5">{{ p.name }}</router-link>
        <span v-if="!activeProjects.length" class="text-slate-400 text-sm">None</span>
      </div>
    </section>
  </div>
</template>
```
- [ ] **Step 4: Verify** — `npx vitest run && npx vue-tsc --noEmit`.
- [ ] **Step 5: Commit** — `feat(web): dashboard`.

---

### Task 4: Items list view + store + Pagination

**Files:**
- Create: `src/stores/items.ts`, `src/components/Pagination.vue`, `src/views/ItemsView.test.ts`
- Modify: `src/views/ItemsView.vue`

**Interfaces:**
- Consumes: `api`, `Item`, `ListResponse`, `Location`, `Tag`.
- Produces: `useItemsStore()` (`items, total, loading, filters {q, location_id, tag, project_id, visibility, sort, order, page, page_size}`, `fetch()`); `Pagination.vue` (`v-model:page`, props `total, pageSize`); `/items` view with debounced search, filter selects, sort select, responsive table (≥md) / cards (<md).

- [ ] **Step 1: Failing tests**

`src/views/ItemsView.test.ts`:

```ts
import { describe, it, expect, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ItemsView from './ItemsView.vue'

describe('ItemsView', () => {
  it('fetches with default sort and renders item names', async () => {
    const urls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      urls.push(url)
      if (url.includes('/tags')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      if (url.includes('/locations')) return new Response(JSON.stringify({ data: [], total: 0 }), { status: 200 })
      return new Response(JSON.stringify({ data: [{ id: 5, name: 'M3 screws', qty: 250, photo_count: 1 }], total: 1 }), { status: 200 })
    }))
    setActivePinia(createPinia())
    const w = mount(ItemsView)
    await flushPromises()
    expect(urls.some(u => u.includes('/items?') && u.includes('sort=updated_at'))).toBe(true)
    expect(w.text()).toContain('M3 screws')
  })
})
```

- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement**

`src/stores/items.ts`:

```ts
import { defineStore } from 'pinia'
import { reactive, ref } from 'vue'
import { api } from '../api/client'
import type { Item, ListResponse } from '../api/types'

export const useItemsStore = defineStore('items', () => {
  const items = ref<Item[]>([])
  const total = ref(0)
  const loading = ref(false)
  const filters = reactive({
    q: '', location_id: 0, tag: '', project_id: 0,
    visibility: '' as '' | 'private' | 'shared',
    sort: 'updated_at' as 'updated_at' | 'created_at' | 'name' | 'qty',
    order: 'desc' as 'asc' | 'desc',
    page: 1, page_size: 50,
  })

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
  return { items, total, loading, filters, fetch }
})
```

`src/components/Pagination.vue`:

```vue
<template>
  <div class="flex items-center gap-2 text-sm">
    <button :disabled="page <= 1" @click="$emit('update:page', page - 1)"
      class="rounded border px-2 py-1 disabled:opacity-40">Prev</button>
    <span>Page {{ page }} of {{ pages }} · {{ total }} items</span>
    <button :disabled="page >= pages" @click="$emit('update:page', page + 1)"
      class="rounded border px-2 py-1 disabled:opacity-40">Next</button>
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
const props = defineProps<{ page: number; pageSize: number; total: number }>()
defineEmits<{ 'update:page': [n: number] }>()
const pages = computed(() => Math.max(1, Math.ceil(props.total / props.pageSize)))
</script>
```

`ItemsView.vue`: search input (300ms debounce writing `filters.q` + `filters.page = 1` + fetch), selects for location (indented names: `— `.repeat(depth) + name, computed from flat list via parent walk), tag, visibility, sort (`updated_at|created_at|name|qty`) + order toggle button; desktop table columns Name / Qty / Location / Tags / Updated / photo count; mobile cards showing name + qty + thumbnail when `photo_count > 0`; row click → `router-link :to="`/items/${item.id}`"`; New item button → `/items/new`; `watch(filters, fetch)` with page reset on any filter change except page; errors `toast(e.message, 'error')`.

- [ ] **Step 4: Verify** — vitest + vue-tsc + build.
- [ ] **Step 5: Commit** — `feat(web): items list with search, filters, pagination`.

---

### Task 5: Item detail view + PhotoGallery

**Files:**
- Create: `src/components/PhotoGallery.vue`
- Modify: `src/views/ItemDetailView.vue`
- Test: `src/views/ItemDetailView.test.ts`

**Interfaces:**
- Consumes: `api`, `Item`, breadcrumb endpoint `GET /api/v1/locations/{id}` → `{location, breadcrumbs}`.
- Produces: `/items/:id` — name, qty+unit, description, breadcrumb path, tags (links to `/?tag=`), projects with qty_used, PhotoGallery (grid, click → fullscreen overlay, delete button per photo), documents list (inline preview link `/files/{key}` opens new tab + delete), Edit + Delete (ConfirmDialog arrives Task 6 — use `confirm()` here and swap in Task 6) buttons.

- [ ] **Step 1: Failing test** — mount with mocked item detail (`/api/v1/items/9` returns tags/photos/documents/projects; `/api/v1/locations/3` returns breadcrumbs `[House, Cabinet, Bin]`); assert name, breadcrumb text `House › Cabinet › Bin`, gallery `<img>` with `/files/{key}` src, doc link, project row.

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement** — `api.get<Item>(`/api/v1/items/${route.params.id}`)`; when `item.location_id` fetch breadcrumbs; `PhotoGallery`:

```vue
<template>
  <div>
    <div class="grid grid-cols-3 sm:grid-cols-4 gap-2">
      <div v-for="p in photos" :key="p.id" class="relative group">
        <img :src="`/files/${p.storage_key}`" :alt="p.caption || 'photo'" class="w-full h-24 object-cover rounded-lg cursor-zoom-in"
          @click="selected = p" />
        <button v-if="canEdit" @click.stop="emit('delete', p)"
          class="absolute top-1 right-1 bg-red-600 text-white rounded px-1 text-xs opacity-0 group-hover:opacity-100">✕</button>
      </div>
    </div>
    <div v-if="selected" class="fixed inset-0 z-50 bg-black/80 flex items-center justify-center p-4"
      @click="selected = null">
      <img :src="`/files/${selected.storage_key}`" class="max-h-full max-w-full object-contain" @click.stop />
      <button class="absolute top-4 right-4 text-white text-2xl" @click="selected = null">✕</button>
    </div>
  </div>
</template>
<script setup lang="ts">
import { ref } from 'vue'
import type { Photo } from '../api/types'
defineProps<{ photos: Photo[]; canEdit: boolean }>()
const emit = defineEmits<{ delete: [p: Photo] }>()
const selected = ref<Photo | null>(null)
</script>
```

Documents: `<a :href="`/files/${d.storage_key}`" target="_blank">📄 {{ d.filename }} ({{ Math.ceil(d.size_bytes / 1024) }} KB)</a>` + delete button; `visibility` badge (Private/Shared). Delete item → `api.del` → toast → `router.push('/items')`.

- [ ] **Step 4: Verify** — vitest + vue-tsc + build.
- [ ] **Step 5: Commit** — `feat(web): item detail with gallery`.

---

### Task 6: Item form + TagInput + LocationSelect + ConfirmDialog

**Files:**
- Create: `src/components/ConfirmDialog.vue`, `src/components/TagInput.vue`, `src/components/LocationSelect.vue`, `src/views/ItemFormView.test.ts`
- Modify: `src/views/ItemFormView.vue`, `src/views/ItemDetailView.vue` (swap `confirm()` for ConfirmDialog)

**Interfaces:**
- Consumes: `api`, `Item`, tags/locations endpoints.
- Produces: `/items/new` and `/items/:id/edit`; `TagInput` (chips, `v-model:tags: string[]`, creates-on-submit — plain names, backend auto-creates); `LocationSelect` (props `modelValue: number` (0 = none), emits update — includes a "No location" option value 0); `ConfirmDialog` (`props: open, title, message; emits confirm/cancel`).
- **Review Focus #2 binding:** the form tracks `initialLocationId`; on save it sends `location_id` ONLY if changed — `0` when cleared to none, the new id when set, OMITTED when untouched. Same rule for every other field (build the PATCH body from diffs).

- [ ] **Step 1: Failing test (diff-semantics, Review Focus #2)**

```ts
import { describe, it, expect, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ItemFormView from './ItemFormView.vue'

describe('ItemFormView PATCH body', () => {
  it('omits untouched fields; sends location_id 0 only when cleared', async () => {
    const item = { id: 9, name: 'Widget', description: 'd', qty: 2, unit: 'pcs', location_id: 3,
      visibility: 'private', created_at: '', updated_at: '', tags: [{ id: 1, name: 't1' }],
      photos: [], documents: [], projects: [] }
    const bodies: any[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: any) => {
      if (url.match(/\/items\/9$/) && init?.method === 'PATCH') bodies.push(JSON.parse(init.body))
      if (url.includes('/locations')) return new Response(JSON.stringify({ data: [{ id: 3, name: 'Bin', parent_id: 2 }], total: 1 }), { status: 200 })
      return new Response(JSON.stringify(item), { status: 200 })
    }))
    const w = mount(ItemFormView, { props: { id: '9' } })
    await flushPromises()
    // user clears the location and changes nothing else
    ;(w.vm as any).form.location_id = 0
    await (w.vm as any).save()
    expect(bodies.at(-1)).toEqual({ location_id: 0 })
    // user edits qty instead, leaves location at original
    ;(w.vm as any).form.location_id = 3
    ;(w.vm as any).form.qty = 5
    await (w.vm as any).save()
    expect(bodies.at(-1)).toEqual({ qty: 5 })
  })
})
```

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement** — `defineProps<{ id?: string }>()` with fallback `const itemId = computed(() => props.id ?? (route.params.id as string))` so both the route and the test's prop work. `form` reactive mirrored from fetched item (or blank defaults for create: `qty: 1, visibility: auth.user.default_item_visibility`); expose `form`, `save`, and `tagsEqual` via `defineExpose` for the test. `save()`:

```ts
function buildPatch(): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  if (form.name !== initial.name) out.name = form.name
  if (form.description !== initial.description) out.description = form.description
  if (form.qty !== initial.qty) out.qty = form.qty
  if (form.unit !== initial.unit) out.unit = form.unit
  if (form.visibility !== initial.visibility) out.visibility = form.visibility
  if (form.location_id !== initial.location_id) out.location_id = form.location_id // 0 = clear (api-notes)
  if (!tagsEqual(form.tags, initialTags)) out.tags = form.tags
  return out
}
```

Create posts the full body (name, description, qty, unit, visibility, location_id only if non-zero, tags if non-empty); success → `router.push(`/items/${id}`)` + toast('Saved','success'); errors → `toast(e.message,'error')`. `TagInput`: text input, Enter/comma adds chip (trimmed, dedup, max 50), × removes; emits string[] names. `LocationSelect`: fetch `/api/v1/locations` once, compute depth per node (walk parents), render `<option :value="loc.id">{{ '— '.repeat(depth) }}{{ loc.name }}</option>` plus `<option :value="0">No location</option>`.

- [ ] **Step 4: Verify** — vitest + vue-tsc + build.
- [ ] **Step 5: Commit** — `feat(web): item form with diff-patch semantics`.

---

### Task 7: Uploads — FileUpload, downscale, gallery wiring

**Files:**
- Create: `src/utils/image.ts`, `src/utils/image.test.ts`, `src/components/FileUpload.vue`
- Modify: `src/views/ItemDetailView.vue`, `src/views/ItemFormView.vue` (photo/doc upload on create: upload after item exists)

**Interfaces:**
- Consumes: `api.upload`, gallery from Task 5.
- Produces: `downscaleImage(file, maxDim=2000, quality=0.85): Promise<Blob>` (passthrough when `createImageBitmap` missing, scale ≥ 1 AND size < 2MB); `FileUpload` (props `kind: 'photo' | 'doc'`, `itemId`, emits `uploaded`); detail-view Photo section + Documents section gain upload buttons; form view uploads queued files after create.

- [ ] **Step 1: Failing tests (image util)**

```ts
import { describe, it, expect, vi } from 'vitest'
import { downscaleImage } from './image'

function fakeBitmap(w: number, h: number) { return { width: w, height: h, close() {} } }

describe('downscaleImage', () => {
  it('passes through small files untouched', async () => {
    const file = new File(['x'], 'a.png', { type: 'image/png' })
    const spy = vi.fn()
    vi.stubGlobal('createImageBitmap', spy)
    expect(await downscaleImage(file)).toBe(file)
    expect(spy).not.toHaveBeenCalled()
  })

  it('scales large images to fit maxDim', async () => {
    const file = new File(['x'], 'big.png', { type: 'image/png' })
    Object.defineProperty(file, 'size', { value: 9_000_000 })
    const draw = vi.fn()
    const ctx = { drawImage: draw }
    const canvas = { width: 0, height: 0, getContext: () => ctx,
      toBlob: (cb: (b: Blob) => void) => cb(new Blob(['j'], { type: 'image/jpeg' })) }
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue(fakeBitmap(4000, 2000)))
    const creator = vi.spyOn(document, 'createElement').mockReturnValue(canvas as unknown as HTMLCanvasElement)
    const out = await downscaleImage(file, 2000, 0.85)
    expect(creator).toHaveBeenCalledWith('canvas')
    expect(canvas.width).toBe(2000)   // 4000 scaled to fit
    expect(canvas.height).toBe(1000)
    expect(out.type).toBe('image/jpeg')
  })
})
```

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement**

`src/utils/image.ts`:

```ts
export async function downscaleImage(file: File, maxDim = 2000, quality = 0.85): Promise<Blob> {
  if (file.size < 2_000_000) return file
  if (typeof createImageBitmap !== 'function') return file
  const bitmap = await createImageBitmap(file)
  try {
    const scale = Math.min(1, maxDim / Math.max(bitmap.width, bitmap.height))
    if (scale >= 1) return file
    const canvas = document.createElement('canvas')
    canvas.width = Math.round(bitmap.width * scale)
    canvas.height = Math.round(bitmap.height * scale)
    canvas.getContext('2d')!.drawImage(bitmap, 0, 0, canvas.width, canvas.height)
    return await new Promise<Blob>((res, rej) =>
      canvas.toBlob(b => (b ? res(b) : rej(new Error('encode failed'))), 'image/jpeg', quality))
  } finally { bitmap.close?.() }
}
```

`FileUpload.vue`: hidden `<input :type="'file'" :accept="kind==='photo' ? 'image/*' : undefined" :capture="kind==='photo' ? 'environment' : undefined" multiple>` + styled button ("Add photo" 📷 / "Add document"); on change per file: photos → `downscaleImage` then `api.upload(`/api/v1/items/${itemId}/photos`, 'file', blob, 'photo.jpg')`; docs → `api.upload(.../documents, 'file', file, file.name)`; emit `uploaded`; busy/disabled state; error → toast. Re-fetch item on `uploaded` in detail view.

- [ ] **Step 4: Verify** — vitest + vue-tsc + build.
- [ ] **Step 5: Commit** — `feat(web): photo and document uploads with client downscale`.

---

### Task 8: Locations view (tree, breadcrumbs, CRUD)

**Files:**
- Modify: `src/views/LocationsView.vue`
- Test: `src/views/LocationsView.test.ts`

**Interfaces:**
- Consumes: `api`, `Location`, `Breadcrumb`, `{location, breadcrumbs}` GET shape.
- Produces: `/locations` — indented tree (computed depth via parent map), inline Add (name + parent select = any existing location), rename (inline edit), move (parent select incl. "— none"), delete (ConfirmDialog; message explains children move up + items detach), notes textarea. Cycle errors (`code === 'cycle'`) and duplicate names (`location_exists`) surface their messages.

- [ ] **Step 1: Failing test** — mock `/locations` returning `[House(id1), Cabinet(id2,parent1), Bin(id3,parent2)]`; assert render order with indentation (`Bin` deeper than `House` — check padding class or prefix) and that Add posts `{name:'Shed'}` to `/locations`; assert 409 `location_exists` from POST surfaces as toast text.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement** — tree computed: `const byId = Map`, depth walk with visited-set (defensive); recursive render via flattened array `{loc, depth}[]` sorted by path name. PATCH body: `{name}` if changed, `{parent_id}` if changed, `{notes}` only when user edited (per api-notes: omitted = kept). Delete → `api.del` → refetch + toast('Location deleted','success'). Every mutation error → toast.
- [ ] **Step 4: Verify** — vitest + vue-tsc + build.
- [ ] **Step 5: Commit** — `feat(web): locations tree management`.

---

### Task 9: Tags view

**Files:**
- Modify: `src/views/TagsView.vue`
- Test: `src/views/TagsView.test.ts`

**Interfaces:**
- Consumes: `api`, `Tag`.
- Produces: `/tags` — table (name, item count), inline rename (input + save on Enter), delete with ConfirmDialog; `tag_exists` 409 shows message; clicking a tag navigates `/?tag=<name>` (via router push to /items with store filter).

- [ ] **Step 1: Failing test** — mock list `[{id:1,name:'electronics',item_count:4}]`; assert row + count; mock PATCH 409 `tag_exists`; trigger rename-save; assert toast contains backend message and row keeps old name.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement** — rename sends PATCH `{name}` only when changed; on error revert input to original + toast. Delete → ConfirmDialog → `api.del` → refetch. Note in UI: deleting a tag detaches it from all items.
- [ ] **Step 4: Verify** — vitest + vue-tsc + build.
- [ ] **Step 5: Commit** — `feat(web): tags management`.

---

### Task 10: Projects list + detail

**Files:**
- Modify: `src/views/ProjectsView.vue`, `src/views/ProjectDetailView.vue`
- Test: `src/views/ProjectDetailView.test.ts`

**Interfaces:**
- Consumes: `api`, `Project`, `ProjectItem`, `{project, items}` GET shape.
- Produces: `/projects` — cards (name, status badge, notes), New project form (name, notes); `/projects/:id` — status select (PATCH), notes edit, linked-items table (name → item link, qty_used editable number input + save, remove), Add item picker (select from `/items?page_size=200` + qty_used input) posting `/projects/:id/items`.

- [ ] **Step 1: Failing test** — mock project detail `{project:{id:1,name:'NAS',status:'active'},items:[{item_id:5,item_name:'8TB HDD',qty_used:4,item_qty:6,item_unit:''}]}`; assert row renders and qty_used edit PATCHes `/projects/1/items/5` with `{qty_used: 6}`; assert status change PATCHes `/projects/1` with `{status:'done'}`.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement** — as interfaces; qty_used empty input sends `{qty_used: 0}`; remove → DELETE; add → POST `{item_id, qty_used?}`; all errors toast; 404 (`not_found`) navigates back to `/projects` with toast.
- [ ] **Step 4: Verify** — vitest + vue-tsc + build.
- [ ] **Step 5: Commit** — `feat(web): projects with linked items`.

---

### Task 11: Shared browse + copy

**Files:**
- Modify: `src/views/SharedView.vue`
- Test: `src/views/SharedView.test.ts`

**Interfaces:**
- Consumes: `api`, `Item & {owner_name}`, `GET /api/v1/shared?q=&page=&page_size=`, `POST /api/v1/items/{id}/copy`.
- Produces: `/shared` — cards (name, owner_name, qty+unit, photo thumb when `photo_count > 0`), debounced search, Pagination, Copy button per card → POST copy → toast('Copied to your inventory','success') → optionally navigate to the new item (use returned Item.id).

- [ ] **Step 1: Failing test** — mock `/shared` one item `{id:9,name:'solder tips',owner_name:'Alice',qty:8,photo_count:0}`; assert owner name renders; click Copy (mock POST 201 `{id:12,...}`) → assert POST URL `/api/v1/items/9/copy` and success toast.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement** — same list pattern as Items (local state, not the items store); Copy busy-state per card; error → toast (404 `not_found` message: "item not found" — it may have been un-shared; refetch list on that code).
- [ ] **Step 4: Verify** — vitest + vue-tsc + build.
- [ ] **Step 5: Commit** — `feat(web): shared items browse and copy`.

---

### Task 12: Settings — profile, password, devices

**Files:**
- Modify: `src/views/SettingsView.vue`
- Test: `src/views/SettingsView.test.ts`

**Interfaces:**
- Consumes: `api`, `User`, `SessionRow`; endpoints `PATCH /auth/me`, `POST /auth/password`, `GET /auth/sessions`, `DELETE /auth/sessions/{id}`, `POST /auth/sessions/revoke-all`.
- Produces: `/settings` — profile card (display_name, default_item_visibility select) saving via diff-PATCH; password card (current, new, "revoke other sessions" checkbox) → 204 → toast + force re-login only when revoke-all-others was NOT checked (session survives); devices table (user_agent, last_ip, last_seen, "This device" badge when `current`, revoke button) + "Revoke all other devices" button (except_current true).

- [ ] **Step 1: Failing test** — mock `/auth/me` GET (User) then PATCH; change display_name + visibility → save → assert PATCH body `{display_name:'New', default_item_visibility:'shared'}` (diff semantics — omitted when untouched); password flow: POST `/auth/password` `{current_password:'a', new_password:'b', revoke_other_sessions:true}` → 204 → success toast; sessions: mock two rows one `current:true`, revoke non-current → DELETE `/auth/sessions/5` → row disappears.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement** — per interfaces; profile save updates `auth.user` locally on success; device list refetch after any revoke.
- [ ] **Step 4: Verify** — vitest + vue-tsc + build.
- [ ] **Step 5: Commit** — `feat(web): settings with device management`.

---

### Task 13: Admin — users + invites

**Files:**
- Modify: `src/views/AdminView.vue`
- Test: `src/views/AdminView.test.ts`

**Interfaces:**
- Consumes: `api`, `AdminUser`, `Invite`; endpoints `GET /admin/users`, `PATCH /admin/users/{id}`, `GET/POST /admin/invites`, `DELETE /admin/invites/{id}`.
- Produces: `/admin` (route already guarded by UI visibility; backend enforces 403) — users table (email, display_name, admin toggle, disabled toggle, created) with self-disable blocked client-side (`cannot_disable_self` error toasted if backend rejects); invites card (create → shows code with copy-to-clipboard button `navigator.clipboard.writeText`, list with used_by_email/used_at, delete unused).

- [ ] **Step 1: Failing test** — mock users `[{id:1,email:'a@x',is_admin:true,disabled:false},{id:2,...false,false}]`; toggling user 2 admin PATCHes `{is_admin:true}`; invites POST returns `{id:1,code:'ABCD2345',created_at:''}` → code rendered + clipboard stub called on copy click; delete invite 1 → DELETE `/admin/invites/1`.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement** — per interfaces; disable toggle sends `{disabled:true|false}` and on success drops that user's row to `disabled` state (strikethrough); every error toasted (esp. `cannot_disable_self`, `forbidden`).
- [ ] **Step 4: Verify** — vitest + vue-tsc + build.
- [ ] **Step 5: Commit** — `feat(web): admin users and invites`.

---

### Task 14: PWA, build integration, chromium smoke, docs

**Files:**
- Create: `web/public/manifest.webmanifest`, `web/public/icons/icon-192.png`, `web/public/icons/icon-512.png`, `scripts/smoke-web.sh`
- Modify: `web/index.html` (manifest + apple meta), `Makefile`, `README.md`, `AGENTS.md`, `web/package.json` (scripts)

**Interfaces:**
- Consumes: everything.
- Produces: installable PWA; `make web`, `make web-dev`, `make web-test`; committed `internal/webfs/dist`; headless-chromium smoke proving the real binary serves the real SPA; docs updated.

- [ ] **Step 1: PWA assets** — manifest:

```json
{
  "name": "Stasher", "short_name": "Stasher",
  "start_url": "/", "display": "standalone",
  "background_color": "#0f172a", "theme_color": "#0f172a",
  "icons": [
    { "src": "/icons/icon-192.png", "sizes": "192x192", "type": "image/png" },
    { "src": "/icons/icon-512.png", "sizes": "512x512", "type": "image/png" }
  ]
}
```

Generate simple icons (solid slate rounded square via ImageMagick if present, else a tiny Go/node script writing a PNG — any recognizable icon): `convert -size 512x512 xc:'#0f172a' -fill '#e2e8f0' -pointsize 240 -gravity center -annotate 0 'S' icon-512.png` and resize to 192. `index.html`: `<link rel="manifest" href="/manifest.webmanifest">`, `<link rel="apple-touch-icon" href="/icons/icon-192.png">`, `<meta name="apple-mobile-web-app-capable" content="yes">`.

- [ ] **Step 2: npm scripts + Makefile**

`web/package.json` scripts:

```json
{
  "dev": "vite",
  "build": "vue-tsc --noEmit && vite build",
  "test": "vitest run",
  "typecheck": "vue-tsc --noEmit"
}
```

Makefile additions:

```make
.PHONY: web web-dev web-test

web:
	cd web && npm ci && npm run build

web-dev:
	cd web && npm run dev

web-test:
	cd web && npm ci && npm run test
```

- [ ] **Step 3: Full build + embed**

```bash
make web && make test && go build -o stasher ./cmd/stasher
git status --short internal/webfs/dist   # built assets present, tracked
```

- [ ] **Step 4: Chromium smoke (real binary, real SPA)**

`scripts/smoke-web.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
DATA=$(mktemp -d /tmp/opencode/stasher-smoke.XXXX)
STASHER_DATA_DIR="$DATA" STASHER_LISTEN_ADDR=127.0.0.1:18097 ./stasher &
PID=$!
trap 'kill $PID 2>/dev/null || true; rm -rf "$DATA"' EXIT
sleep 1
CHROME=$(command -v chromium || command -v chromium-browser || command -v google-chrome)
[ -n "$CHROME" ] || { echo "no chromium found"; exit 1; }

# desktop: login form renders
"$CHROME" --headless=new --disable-gpu --no-sandbox --dump-dom \
  --virtual-time-budget=5000 http://127.0.0.1:18097/login 2>/dev/null \
  | grep -q 'Sign in to Stasher' || { echo "FAIL: login form missing"; exit 1; }

# mobile viewport: shell nav renders (Review Focus #4)
"$CHROME" --headless=new --disable-gpu --no-sandbox --window-size=375,812 \
  --screenshot="$DATA/mobile.png" --virtual-time-budget=5000 \
  http://127.0.0.1:18097/login 2>/dev/null
[ -s "$DATA/mobile.png" ] || { echo "FAIL: no mobile screenshot"; exit 1; }

# manifest served
curl -sf http://127.0.0.1:18097/manifest.webmanifest >/dev/null || { echo "FAIL: manifest"; exit 1; }
echo "smoke ok"
```

`chmod +x scripts/smoke-web.sh && ./scripts/smoke-web.sh` — expected `smoke ok`.

- [ ] **Step 5: Hygiene checks (Review Focus #3 + #5)**

```bash
cd web
! grep -rn "v-html" src/                       # no v-html anywhere
! grep -rn "catch\s*{}\|catch (e) {}" src/     # no silent catches
```

Both greps must return zero matches (the `!` semantics in bash: these commands succeed when nothing is found).

- [ ] **Step 6: Docs** — README: replace "web UI not built yet" placeholder note with dev instructions (`make web-dev` + `make run` in two terminals; `make web` before deploy builds), PWA install note. AGENTS.md: fill the frontend sections — `web/` layout, npm scripts, the dist-commit rule, `make web*` targets, chromium smoke script, vitest conventions (happy-dom, fetch-mocked store/view tests).

- [ ] **Step 7: Full verification**

```bash
cd web && npm run test && npm run build && cd ..
make test && go build -o stasher ./cmd/stasher && ./scripts/smoke-web.sh
```

All green → commit:

```bash
git add -A && git commit -m "feat(web): pwa, build integration, chromium smoke"
```

---

## Post-plan

Backend + frontend complete. Remaining v1.1 work lives in `docs/backlog.md`. Future candidates: Playwright E2E on top of the chromium smoke, offline shell caching, QR/barcode phase.



