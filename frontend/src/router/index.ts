import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '../stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: () => import('../views/LoginView.vue') },
    { path: '/', component: () => import('../views/DashboardView.vue') },
    { path: '/containers', component: () => import('../views/ContainersView.vue') },
    { path: '/mesh', component: () => import('../views/MeshView.vue') },
    { path: '/projects', redirect: (to) => ({ path: '/containers', query: to.query }) },
    { path: '/logs', component: () => import('../views/LogsView.vue') },
    { path: '/config', component: () => import('../views/ConfigView.vue') },
    { path: '/cloudflare', redirect: '/config' },
    { path: '/ingress', redirect: '/config' },
    { path: '/health', component: () => import('../views/HealthView.vue') },
    { path: '/metrics', redirect: '/config' },
    { path: '/backups', redirect: '/config' },
    { path: '/tcp-access', redirect: '/config' },
    { path: '/audit', component: () => import('../views/AuditView.vue') },
    { path: '/notifications', component: () => import('../views/NotificationsView.vue') },
    { path: '/settings', component: () => import('../views/SettingsView.vue') },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

router.beforeEach((to) => {
  const auth = useAuthStore()
  if (to.path !== '/login' && !auth.isAuthenticated) return '/login'
})

export default router
