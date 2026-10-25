<script setup lang="ts">
import { LayoutDashboard, ScrollText, Globe, Shield, LogOut, Bell, Cog, Server, Activity, HardDrive } from 'lucide-vue-next'
import { useAuthStore } from '../stores/auth'
import { useRouter } from 'vue-router'
import { useApi } from '../composables/useApi'

const emit = defineEmits<{ navigate: [] }>()
const authStore = useAuthStore()
const router = useRouter()
const { apiFetch } = useApi()

const nav = [
  { to: '/', label: 'Overview', icon: LayoutDashboard },
  { to: '/containers', label: 'Containers', icon: Server },
  { to: '/metrics', label: 'Metrics', icon: Activity },
  { to: '/storage', label: 'Storage', icon: HardDrive },
  { to: '/access', label: 'Remote Access', icon: Globe },
  { to: '/logs', label: 'Logs & Audit', icon: ScrollText },
  { to: '/notifications', label: 'Alerts', icon: Bell },
  { to: '/settings', label: 'Settings', icon: Cog },
]

async function handleLogout() {
  try { await apiFetch('/auth/logout', { method: 'POST' }) } catch {}
  authStore.logout()
  router.push('/login')
}
</script>

<template>
  <aside class="flex h-full w-64 flex-col border-r border-slate-200/80 bg-slate-50 select-none">
    <!-- Brand Header -->
    <div class="border-b border-slate-200/80 p-5">
      <div class="flex items-center gap-3">
        <div class="p-1.5 rounded-xl bg-indigo-500/10 border border-indigo-500/20 text-indigo-600">
          <img src="/icon.png" alt="DockTun" class="h-6 w-6 object-contain" />
        </div>
        <div>
          <div class="text-sm font-bold tracking-tight text-slate-900 flex items-center gap-1.5">
            DockTun
            <span class="text-[10px] font-medium px-1.5 py-0.2 rounded bg-indigo-500/20 text-indigo-700 border border-indigo-500/30">Workspace</span>
          </div>
          <div class="text-[11px] text-slate-600">Tunnels & Containers</div>
        </div>
      </div>
    </div>

    <!-- Navigation Links -->
    <nav aria-label="Main navigation" class="flex-1 space-y-1 p-4">
      <p class="px-3 pb-3 pt-2 text-[10px] font-semibold uppercase tracking-[0.16em] text-slate-500">Management</p>
      <router-link
        v-for="item in nav"
        :key="item.to"
        :to="item.to"
        @click="emit('navigate')"
        class="flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm font-medium text-slate-600 transition-colors duration-150 hover:bg-white hover:text-slate-900 group"
        active-class="!bg-indigo-600/10 !text-indigo-700 font-semibold border border-indigo-500/20"
      >
        <component
          :is="item.icon"
          class="h-4 w-4 shrink-0 transition-colors group-hover:text-slate-800"
          :stroke-width="1.8"
        />
        <span>{{ item.label }}</span>
      </router-link>
    </nav>

    <!-- User Profile & Logout -->
    <div class="border-t border-slate-200/80 p-3.5 bg-slate-50/60">
      <div class="flex items-center justify-between px-1">
        <div class="flex items-center gap-2">
          <div class="h-6 w-6 rounded-full bg-slate-100 border border-slate-300 flex items-center justify-center text-[10px] font-bold text-slate-700 uppercase">
            {{ (authStore.username || 'A')[0] }}
          </div>
          <span class="text-xs font-medium text-slate-700 truncate max-w-[110px]">{{ authStore.username }}</span>
        </div>
        <button
          @click="handleLogout"
          class="p-1.5 rounded-lg text-slate-600 hover:text-rose-600 hover:bg-rose-500/10 transition"
          title="Sign Out"
        >
          <LogOut class="h-4 w-4" :stroke-width="1.8" />
        </button>
      </div>
    </div>
  </aside>
</template>
