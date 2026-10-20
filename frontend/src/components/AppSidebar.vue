<script setup lang="ts">
import { LayoutDashboard, ScrollText, Settings, Terminal, Shield, LogOut, Activity, BarChart3, Archive, Bell, Cog, Container, Server } from 'lucide-vue-next'
import { useAuthStore } from '../stores/auth'
import { useRouter } from 'vue-router'
import { useApi } from '../composables/useApi'

const emit = defineEmits<{ navigate: [] }>()
const authStore = useAuthStore()
const router = useRouter()
const { apiFetch } = useApi()

const nav = [
  { to: '/', label: 'Status', icon: LayoutDashboard },
  { to: '/containers', label: 'Containers', icon: Server },
  { to: '/projects', label: 'Projects', icon: Container },
  { to: '/health', label: 'Health', icon: Activity },
  { to: '/metrics', label: 'Metrics', icon: BarChart3 },
  { to: '/logs', label: 'Logs', icon: ScrollText },
  { to: '/config', label: 'Ingress', icon: Settings },
  { to: '/backups', label: 'Backups', icon: Archive },
  { to: '/tcp-access', label: 'TCP', icon: Terminal },
  { to: '/notifications', label: 'Alerts', icon: Bell },
  { to: '/audit', label: 'Audit', icon: Shield },
  { to: '/settings', label: 'Settings', icon: Cog },
]

async function handleLogout() {
  try { await apiFetch('/auth/logout', { method: 'POST' }) } catch {}
  authStore.logout()
  router.push('/login')
}
</script>

<template>
  <aside class="flex h-full w-56 flex-col border-r border-slate-800 bg-slate-950">
    <div class="border-b border-slate-800 p-5">
      <div class="flex items-center gap-2">
        <img src="/icon.png" alt="Tunlify" class="h-7 w-7" />
        <div>
          <div class="text-base font-semibold tracking-[0.18em] text-slate-100">TUNLIFY</div>
        </div>
      </div>
    </div>

    <nav class="flex-1 space-y-1 p-3">
      <router-link
        v-for="item in nav"
        :key="item.to"
        :to="item.to"
        @click="emit('navigate')"
        class="flex items-center gap-3 rounded-lg px-3 py-2.5 text-xs font-medium uppercase tracking-[0.16em] text-slate-300 transition hover:bg-slate-900 hover:text-slate-100"
        active-class="bg-slate-900 text-slate-100"
      >
        <component :is="item.icon" class="h-4 w-4" :stroke-width="1.7" />
        {{ item.label }}
      </router-link>
    </nav>

    <div class="border-t border-slate-800 p-4">
      <div class="flex items-center justify-between">
        <span class="text-[10px] uppercase tracking-[0.18em] text-slate-400">{{ authStore.username }}</span>
        <button @click="handleLogout" class="text-slate-400 transition hover:text-slate-200">
          <LogOut class="h-4 w-4" :stroke-width="1.7" />
        </button>
      </div>
    </div>
  </aside>
</template>
