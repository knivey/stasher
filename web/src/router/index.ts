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
        { path: 'trash', component: () => import('../views/TrashView.vue') },
        { path: 'settings', component: () => import('../views/SettingsView.vue') },
        { path: 'admin', component: () => import('../views/AdminView.vue') },
      ],
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

router.beforeEach(async (to) => {
  const auth = useAuthStore()
  if (!auth.ready) await auth.bootstrap()
  if (to.path !== '/login' && to.path !== '/register' && !auth.user)
    return { path: '/login', query: { redirect: to.fullPath } }
  return true
})

export default router
