import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '../stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: () => import('../views/LoginView.vue') },
    { path: '/', component: () => import('../views/DashboardView.vue') },
    { path: '/containers', component: () => import('../views/ContainersView.vue') },
    { path: '/storage', component: () => import('../views/StorageView.vue') },
    { path: '/access', component: () => import('../views/RemoteAccessView.vue') },
    { path: '/remote-access', redirect: '/access' },
    { path: '/mesh', redirect: (to) => ({ path: '/access', query: { tab: 'tailscale', ...to.query } }) },
    { path: '/projects', redirect: (to) => ({ path: '/containers', query: to.query }) },
    { path: '/logs', component: () => import('../views/LogsView.vue') },
    { path: '/config', redirect: (to) => ({ path: '/access', query: { tab: 'cloudflare', ...to.query } }) },
    { path: '/cloudflare', redirect: (to) => ({ path: '/access', query: { tab: 'cloudflare', ...to.query } }) },
    { path: '/health', redirect: (to) => ({ path: '/access', query: { tab: 'cloudflare', ...to.query } }) },
    { path: '/metrics', component: () => import('../views/MetricsView.vue') },
    { path: '/tcp-access', redirect: (to) => ({ path: '/access', query: { tab: 'cloudflare', ...to.query } }) },
    { path: '/audit', redirect: (to) => ({ path: '/logs', query: { tab: 'audit', ...to.query } }) },
    { path: '/notifications', component: () => import('../views/NotificationsView.vue') },
    { path: '/setup', component: () => import('../views/SetupView.vue') },
    { path: '/settings', component: () => import('../views/SettingsView.vue') },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

let setupChecked = false

router.beforeEach(async (to) => {
  const auth = useAuthStore()

  // First-run setup check
  if (!setupChecked && to.path !== '/setup') {
    try {
      const res = await fetch('/api/setup/status')
      if (res.ok) {
        const data = await res.json()
        setupChecked = true
        if (!data.completed && to.query.force !== 'false') {
          return '/setup'
        }
      }
    } catch {
      setupChecked = true
    }
  }

  if (to.path === '/setup') {
    return true
  }

  if (to.path !== '/login' && !auth.isAuthenticated) return '/login'
})

router.onError((error, to) => {
  const msg = error?.message || ''
  const isChunkError =
    /failed to fetch dynamically imported module|unexpected token '<'|importing a module script failed|error loading dynamically imported module/i.test(
      msg
    )
  if (isChunkError) {
    const reloadKey = `docktun_chunk_${to?.fullPath || 'route'}`
    const lastReload = sessionStorage.getItem(reloadKey)
    const now = Date.now()
    if (!lastReload || now - parseInt(lastReload, 10) > 8000) {
      sessionStorage.setItem(reloadKey, now.toString())
      if (to?.fullPath) {
        window.location.href = to.fullPath
      } else {
        window.location.reload()
      }
    }
  }
})

export default router
