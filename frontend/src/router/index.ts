import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '../stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: () => import('../views/LoginView.vue') },
    { path: '/', component: () => import('../views/DashboardView.vue') },
    { path: '/containers', component: () => import('../views/ContainersView.vue') },
    { path: '/access', component: () => import('../views/RemoteAccessView.vue') },
    { path: '/remote-access', redirect: '/access' },
    { path: '/mesh', redirect: (to) => ({ path: '/access', query: { tab: 'tailscale', ...to.query } }) },
    { path: '/projects', redirect: (to) => ({ path: '/containers', query: to.query }) },
    { path: '/logs', component: () => import('../views/LogsView.vue') },
    { path: '/config', redirect: (to) => ({ path: '/access', query: { tab: 'cloudflare', ...to.query } }) },
    { path: '/cloudflare', redirect: (to) => ({ path: '/access', query: { tab: 'cloudflare', ...to.query } }) },
    { path: '/ingress', redirect: (to) => ({ path: '/access', query: { tab: 'cloudflare', ...to.query } }) },
    { path: '/health', component: () => import('../views/HealthView.vue') },
    { path: '/metrics', redirect: (to) => ({ path: '/access', query: { tab: 'cloudflare', ...to.query } }) },
    { path: '/backups', redirect: (to) => ({ path: '/access', query: { tab: 'cloudflare', ...to.query } }) },
    { path: '/tcp-access', redirect: (to) => ({ path: '/access', query: { tab: 'cloudflare', ...to.query } }) },
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
