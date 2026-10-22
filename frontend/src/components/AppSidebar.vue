<script setup lang="ts">
import { LayoutDashboard, ScrollText, Globe, Shield, LogOut, Bell, Cog, Server, Activity } from 'lucide-vue-next'
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
  { to: '/access', label: 'Remote Access', icon: Globe },
  { to: '/logs', label: 'Logs', icon: ScrollText },
  { to: '/notifications', label: 'Alerts', icon: Bell },
  { to: '/audit', label: 'Audit Trail', icon: Shield },
  { to: '/settings', label: 'Settings', icon: Cog },
]

async function handleLogout() {
  try { await apiFetch('/auth/logout', { method: 'POST' }) } catch {}
  authStore.logout()
  router.push('/login')
}
</script>

<template>
  <aside class="flex h-full w-60 flex-col border-r border-slate-800/80 bg-slate-950 select-none">
    <!-- Brand Header -->
    <div class="border-b border-slate-800/80 p-5">
      <div class="flex items-center gap-3">
        <div class="p-1.5 rounded-xl bg-indigo-500/10 border border-indigo-500/20 text-indigo-400">
          <img src="/icon.png" alt="DockTun" class="h-6 w-6 object-contain" />
        </div>
        <div>
          <div class="text-sm font-bold tracking-tight text-white flex items-center gap-1.5">
            DockTun
            <span class="text-[10px] font-medium px-1.5 py-0.2 rounded bg-indigo-500/20 text-indigo-300 border border-indigo-500/30">v2.1</span>
          </div>
          <div class="text-[11px] text-slate-400">Tunnels & Containers</div>
        </div>
      </div>
    </div>

    <!-- Navigation Links -->
    <nav class="flex-1 space-y-1 p-3">
      <router-link
        v-for="item in nav"
        :key="item.to"
        :to="item.to"
        @click="emit('navigate')"
        class="flex items-center gap-3 rounded-lg px-3 py-2 text-xs font-medium text-slate-400 transition-colors duration-150 hover:bg-slate-900 hover:text-slate-100 group"
        active-class="!bg-indigo-600/10 !text-indigo-300 font-semibold border border-indigo-500/20"
      >
        <component
          :is="item.icon"
          class="h-4 w-4 shrink-0 transition-colors group-hover:text-slate-200"
          :stroke-width="1.8"
        />
        <span>{{ item.label }}</span>
      </router-link>
    </nav>

    <!-- User Profile & Logout -->
    <div class="border-t border-slate-800/80 p-3.5 bg-slate-950/60">
      <div class="flex items-center justify-between px-1">
        <div class="flex items-center gap-2">
          <div class="h-6 w-6 rounded-full bg-slate-800 border border-slate-700 flex items-center justify-center text-[10px] font-bold text-slate-300 uppercase">
            {{ (authStore.username || 'A')[0] }}
          </div>
          <span class="text-xs font-medium text-slate-300 truncate max-w-[110px]">{{ authStore.username }}</span>
        </div>
        <button
          @click="handleLogout"
          class="p-1.5 rounded-lg text-slate-400 hover:text-rose-400 hover:bg-rose-500/10 transition"
          title="Sign Out"
        >
          <LogOut class="h-4 w-4" :stroke-width="1.8" />
        </button>
      </div>
    </div>
  </aside>
</template>
