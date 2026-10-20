<script setup lang="ts">
import { ref, onUnmounted } from 'vue'
import { RotateCcw } from 'lucide-vue-next'
import { useApi } from '../composables/useApi'

const { apiFetch } = useApi()
const status = ref<any>(null)
const projects = ref<any[]>([])
const actionLoading = ref('')

async function fetchStatus() {
  try { status.value = await apiFetch('/api/status') } catch {}
}

async function fetchProjects() {
  try { projects.value = await apiFetch('/api/projects') } catch {}
}

async function control(action: string) {
  actionLoading.value = action
  try {
    await apiFetch(`/api/control/${action}`, { method: 'POST' })
    await new Promise(r => setTimeout(r, 1200))
    await fetchStatus()
  } catch (e: any) { alert(e.message) }
  finally { actionLoading.value = '' }
}

fetchStatus()
fetchProjects()
const interval = setInterval(fetchStatus, 5000)
onUnmounted(() => clearInterval(interval))
</script>

<template>
  <div class="space-y-6">
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <p class="text-[10px] uppercase tracking-[0.22em] text-slate-400">Overview</p>
        <h1 class="text-2xl font-semibold text-slate-100">System status</h1>
      </div>
      <button @click="control('restart')" :disabled="!!actionLoading" class="cyber-btn">
        <RotateCcw class="mr-2 h-4 w-4" :stroke-width="1.8" /> {{ actionLoading ? 'Restarting…' : 'Restart' }}
      </button>
    </div>

    <div v-if="status" class="grid gap-3 md:grid-cols-2 xl:grid-cols-5">
      <div class="cyber-card-glow">
        <p class="text-[10px] uppercase tracking-[0.18em] text-slate-400">Uptime</p>
        <p class="mt-3 text-xl font-semibold text-slate-100">{{ status.uptime || '—' }}</p>
      </div>
      <div class="cyber-card-glow">
        <p class="text-[10px] uppercase tracking-[0.18em] text-slate-400">Tunnel</p>
        <p class="mt-3 text-base font-medium text-slate-200">{{ status.tunnel_name || 'n/a' }}</p>
      </div>
      <div class="cyber-card-glow">
        <p class="text-[10px] uppercase tracking-[0.18em] text-slate-400">Ingress</p>
        <p class="mt-3 text-2xl font-semibold text-slate-100">{{ status.ingress_count || 0 }}</p>
      </div>
      <div class="cyber-card-glow">
        <p class="text-[10px] uppercase tracking-[0.18em] text-slate-400">Memory</p>
        <p class="mt-3 text-base font-medium text-slate-200">{{ status.memory_usage || '—' }}</p>
      </div>
      <div class="cyber-card-glow">
        <p class="text-[10px] uppercase tracking-[0.18em] text-slate-400">Version</p>
        <p class="mt-3 text-base font-medium text-slate-200">{{ status.version?.split(' ')[2] || '—' }}</p>
      </div>
    </div>

    <div v-if="status?.hostnames?.length" class="cyber-card">
      <h2 class="mb-3 text-[10px] uppercase tracking-[0.2em] text-slate-400">Active hostnames</h2>
      <div class="flex flex-wrap gap-2">
        <span v-for="h in status.hostnames" :key="h" class="rounded-full border border-slate-700 bg-slate-950 px-2.5 py-1 text-xs text-slate-300">{{ h }}</span>
      </div>
    </div>

    <div v-if="actionLoading" class="text-xs uppercase tracking-[0.18em] text-slate-400">Executing {{ actionLoading }}…</div>

    <div v-if="projects.length" class="cyber-card">
      <h2 class="mb-3 text-[10px] uppercase tracking-[0.2em] text-slate-400">Projects ({{ projects.length }})</h2>
      <div class="flex flex-wrap gap-2">
        <router-link v-for="p in projects" :key="p.id" to="/projects" class="rounded border border-slate-700 bg-slate-950 px-3 py-2 text-xs text-slate-300 transition hover:border-slate-500 hover:text-slate-100">
          {{ p.name }}
        </router-link>
      </div>
    </div>
  </div>
</template>
