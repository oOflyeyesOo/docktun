<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import {
  RotateCcw,
  Server,
  Globe,
  Cpu,
  Layers,
  ArrowUpRight,
  ExternalLink,
  ShieldCheck,
  Play,
  Square,
  Activity,
  ScrollText,
  FolderOpen
} from 'lucide-vue-next'
import { useApi } from '../composables/useApi'

const { apiFetch } = useApi()

const status = ref<any>(null)
const projects = ref<any[]>([])
const containers = ref<any[]>([])
const actionLoading = ref('')
const containerActionLoading = ref<Record<string, string>>({})

async function fetchStatus() {
  try {
    status.value = await apiFetch('/api/status')
  } catch (e) {
    console.error('Failed to fetch status', e)
  }
}

async function fetchProjects() {
  try {
    projects.value = await apiFetch('/api/projects')
  } catch (e) {
    console.error('Failed to fetch projects', e)
  }
}

async function fetchContainers() {
  try {
    containers.value = await apiFetch('/api/docker/containers')
  } catch (e) {
    console.error('Failed to fetch containers', e)
  }
}

async function controlTunnel(action: string) {
  actionLoading.value = action
  try {
    await apiFetch(`/api/control/${action}`, { method: 'POST' })
    await new Promise((r) => setTimeout(r, 1200))
    await fetchStatus()
  } catch (e: any) {
    alert(e.message)
  } finally {
    actionLoading.value = ''
  }
}

async function runContainerAction(container: any, action: 'start' | 'stop' | 'restart') {
  containerActionLoading.value[container.id] = action
  try {
    await apiFetch(`/api/docker/containers/${container.id}/${action}`, { method: 'POST' })
    await fetchContainers()
  } catch (e: any) {
    alert(e.message || `Failed to ${action} container`)
  } finally {
    delete containerActionLoading.value[container.id]
  }
}

const containerStats = computed(() => {
  const total = containers.value.length
  const running = containers.value.filter((c) => c.running).length
  let totalCpu = 0
  containers.value.forEach((c) => {
    if (c.running && c.cpu) {
      const parsed = parseFloat(c.cpu.replace('%', ''))
      if (!isNaN(parsed)) totalCpu += parsed
    }
  })

  return {
    total,
    running,
    stopped: total - running,
    cpuPercent: totalCpu.toFixed(1) + '%',
  }
})

let timer: any = null

onMounted(async () => {
  await Promise.all([fetchStatus(), fetchProjects(), fetchContainers()])
  timer = setInterval(() => {
    if (document.visibilityState === 'visible') {
      fetchStatus()
      fetchContainers()
    }
  }, 5000)
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <div class="space-y-6">
    <!-- Header -->
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <p class="text-[10px] font-semibold uppercase tracking-[0.22em] text-slate-400">Mission Control</p>
        <h1 class="text-2xl font-bold tracking-tight text-slate-100">System Dashboard</h1>
      </div>
      <div class="flex items-center gap-2">
        <button
          @click="controlTunnel('restart')"
          :disabled="!!actionLoading"
          class="cyber-btn flex items-center gap-1.5 text-xs text-cyan-300 border-cyan-700 bg-cyan-950/40 hover:bg-cyan-900/50"
        >
          <RotateCcw class="h-3.5 w-3.5" :class="actionLoading === 'restart' ? 'animate-spin' : ''" />
          <span>{{ actionLoading === 'restart' ? 'Restarting Tunnel…' : 'Restart Cloudflared' }}</span>
        </button>
      </div>
    </div>

    <!-- Top Telemetry Row -->
    <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
      <!-- Tunnel Status -->
      <div class="cyber-card-glow">
        <div class="flex items-center justify-between text-slate-400">
          <span class="text-[10px] font-semibold uppercase tracking-[0.2em]">Cloudflare Tunnel</span>
          <Globe class="h-4 w-4 text-cyan-400" />
        </div>
        <div class="mt-2 flex items-center gap-2">
          <span class="relative flex h-2.5 w-2.5">
            <span
              v-if="status?.status === 'running' || status?.status === 'healthy'"
              class="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75"
            ></span>
            <span
              class="relative inline-flex h-2.5 w-2.5 rounded-full"
              :class="status?.status === 'running' || status?.status === 'healthy' ? 'bg-emerald-500' : 'bg-red-500'"
            ></span>
          </span>
          <span class="text-lg font-bold text-slate-100">{{ status?.tunnel_name || 'Active' }}</span>
        </div>
        <div class="mt-2 flex items-center justify-between text-xs text-slate-400">
          <span>Uptime: {{ status?.uptime || '—' }}</span>
          <span class="rounded bg-slate-800 px-1.5 py-0.5 text-[10px] text-cyan-300 font-mono">
            {{ status?.protocol || 'HTTP/2' }}
          </span>
        </div>
      </div>

      <!-- Live Containers -->
      <div class="cyber-card-glow">
        <div class="flex items-center justify-between text-slate-400">
          <span class="text-[10px] font-semibold uppercase tracking-[0.2em]">Docker Containers</span>
          <Server class="h-4 w-4 text-emerald-400" />
        </div>
        <div class="mt-2 flex items-baseline gap-2">
          <span class="text-2xl font-bold text-emerald-400">{{ containerStats.running }}</span>
          <span class="text-xs text-slate-400">/ {{ containerStats.total }} running</span>
        </div>
        <div class="mt-2 flex items-center justify-between text-xs text-slate-400">
          <span>{{ containerStats.stopped }} stopped</span>
          <router-link to="/containers" class="text-cyan-400 hover:underline">Manage &rarr;</router-link>
        </div>
      </div>

      <!-- Total CPU -->
      <div class="cyber-card-glow">
        <div class="flex items-center justify-between text-slate-400">
          <span class="text-[10px] font-semibold uppercase tracking-[0.2em]">Stack CPU</span>
          <Cpu class="h-4 w-4 text-cyan-400" />
        </div>
        <div class="mt-2 text-2xl font-bold text-cyan-300">{{ containerStats.cpuPercent }}</div>
        <p class="mt-2 text-xs text-slate-400">Combined container load</p>
      </div>

      <!-- Ingress Count -->
      <div class="cyber-card-glow">
        <div class="flex items-center justify-between text-slate-400">
          <span class="text-[10px] font-semibold uppercase tracking-[0.2em]">Public Routes</span>
          <ShieldCheck class="h-4 w-4 text-violet-400" />
        </div>
        <div class="mt-2 flex items-baseline gap-2">
          <span class="text-2xl font-bold text-slate-100">{{ status?.ingress_count || 0 }}</span>
          <span class="text-xs text-slate-400">active routes</span>
        </div>
        <div class="mt-2 flex items-center justify-between text-xs text-slate-400">
          <span>{{ status?.hostnames?.length || 0 }} hostnames</span>
          <router-link to="/config" class="text-violet-400 hover:underline">Edit Ingress &rarr;</router-link>
        </div>
      </div>
    </div>

    <!-- Active Public Hostnames Pill Section -->
    <div v-if="status?.hostnames?.length" class="cyber-card">
      <div class="mb-3 flex items-center justify-between">
        <div class="flex items-center gap-2">
          <Globe class="h-4 w-4 text-cyan-400" />
          <h2 class="text-xs font-bold uppercase tracking-[0.2em] text-slate-300">
            Active Cloudflare Public Hostnames ({{ status.hostnames.length }})
          </h2>
        </div>
        <router-link to="/config" class="text-xs text-cyan-400 hover:underline">View Ingress Config &rarr;</router-link>
      </div>
      <div class="flex flex-wrap gap-2">
        <a
          v-for="h in status.hostnames"
          :key="h"
          :href="`https://${h}`"
          target="_blank"
          rel="noopener"
          class="inline-flex items-center gap-1.5 rounded-full border border-slate-700 bg-slate-950 px-3 py-1 text-xs text-slate-200 transition hover:border-cyan-500 hover:text-cyan-300 font-mono"
        >
          <span>{{ h }}</span>
          <ExternalLink class="h-3 w-3 text-slate-500" />
        </a>
      </div>
    </div>

    <!-- Main Grid: Live Containers & Projects Snapshot -->
    <div class="grid gap-6 lg:grid-cols-2">
      <!-- Container Quick Monitor -->
      <div class="cyber-card flex flex-col justify-between space-y-4">
        <div>
          <div class="mb-3 flex items-center justify-between">
            <div class="flex items-center gap-2">
              <Server class="h-4 w-4 text-emerald-400" />
              <h2 class="text-xs font-bold uppercase tracking-[0.2em] text-slate-300">
                Containers Snapshot ({{ containers.length }})
              </h2>
            </div>
            <router-link to="/containers" class="text-xs text-cyan-400 hover:underline">View All &rarr;</router-link>
          </div>

          <div v-if="containers.length === 0" class="py-6 text-center text-xs text-slate-500">
            No Docker containers detected.
          </div>

          <div v-else class="space-y-2">
            <div
              v-for="c in containers.slice(0, 6)"
              :key="c.id"
              class="flex items-center justify-between rounded-lg border border-slate-800/80 bg-slate-950/70 p-2.5 transition hover:border-slate-700"
            >
              <div class="flex items-center gap-2.5 overflow-hidden">
                <span
                  class="h-2 w-2 flex-shrink-0 rounded-full"
                  :class="c.running ? 'bg-emerald-400' : 'bg-slate-600'"
                ></span>
                <div class="truncate">
                  <div class="font-mono text-xs font-bold text-slate-200 truncate">{{ c.name }}</div>
                  <div class="text-[10px] text-slate-400 truncate">{{ c.image }}</div>
                </div>
              </div>

              <div class="flex items-center gap-3">
                <div v-if="c.running" class="text-right font-mono text-[10px]">
                  <div class="text-cyan-400">{{ c.cpu || '0.0%' }}</div>
                  <div class="text-slate-400">{{ c.memory?.split('/')[0] || '' }}</div>
                </div>

                <div class="flex items-center gap-1">
                  <button
                    v-if="!c.running"
                    @click="runContainerAction(c, 'start')"
                    :disabled="!!containerActionLoading[c.id]"
                    class="rounded p-1 text-slate-400 hover:bg-slate-800 hover:text-emerald-400"
                    title="Start"
                  >
                    <Play class="h-3.5 w-3.5 fill-current" />
                  </button>
                  <button
                    v-if="c.running"
                    @click="runContainerAction(c, 'stop')"
                    :disabled="!!containerActionLoading[c.id]"
                    class="rounded p-1 text-slate-400 hover:bg-slate-800 hover:text-amber-400"
                    title="Stop"
                  >
                    <Square class="h-3.5 w-3.5 fill-current" />
                  </button>
                  <button
                    @click="runContainerAction(c, 'restart')"
                    :disabled="!!containerActionLoading[c.id]"
                    class="rounded p-1 text-slate-400 hover:bg-slate-800 hover:text-slate-200"
                    title="Restart"
                  >
                    <RotateCcw class="h-3.5 w-3.5" :class="containerActionLoading[c.id] === 'restart' ? 'animate-spin' : ''" />
                  </button>
                </div>
              </div>
            </div>
          </div>
        </div>

        <div class="pt-2">
          <router-link
            to="/containers"
            class="cyber-btn-secondary w-full text-center text-xs flex items-center justify-center gap-2"
          >
            <span>Open Containers Control Plane</span>
            <ArrowUpRight class="h-3.5 w-3.5" />
          </router-link>
        </div>
      </div>

      <!-- Docker Compose Projects Snapshot -->
      <div class="cyber-card flex flex-col justify-between space-y-4">
        <div>
          <div class="mb-3 flex items-center justify-between">
            <div class="flex items-center gap-2">
              <FolderOpen class="h-4 w-4 text-violet-400" />
              <h2 class="text-xs font-bold uppercase tracking-[0.2em] text-slate-300">
                Compose Projects ({{ projects.length }})
              </h2>
            </div>
            <router-link to="/projects" class="text-xs text-violet-400 hover:underline">View All &rarr;</router-link>
          </div>

          <div v-if="projects.length === 0" class="py-6 text-center text-xs text-slate-500">
            No compose projects registered yet.
          </div>

          <div v-else class="space-y-2">
            <div
              v-for="p in projects.slice(0, 6)"
              :key="p.id"
              class="flex items-center justify-between rounded-lg border border-slate-800/80 bg-slate-950/70 p-2.5 transition hover:border-slate-700"
            >
              <div class="truncate">
                <div class="font-mono text-xs font-bold text-slate-200 truncate">{{ p.name }}</div>
                <div class="text-[10px] text-slate-400 font-mono truncate">{{ p.path }}</div>
              </div>

              <router-link
                to="/projects"
                class="rounded border border-slate-700 bg-slate-900 px-2 py-1 text-[10px] text-slate-300 hover:border-slate-500 hover:text-slate-100"
              >
                Inspect
              </router-link>
            </div>
          </div>
        </div>

        <div class="pt-2">
          <router-link
            to="/projects"
            class="cyber-btn-secondary w-full text-center text-xs flex items-center justify-center gap-2"
          >
            <span>Open Projects Manager</span>
            <ArrowUpRight class="h-3.5 w-3.5" />
          </router-link>
        </div>
      </div>
    </div>
  </div>
</template>
