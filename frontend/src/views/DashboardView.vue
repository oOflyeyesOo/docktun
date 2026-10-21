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
  Wifi,
  FolderOpen,
  Clock,
  CheckCircle2,
  AlertCircle
} from 'lucide-vue-next'
import { useApi } from '../composables/useApi'

const { apiFetch } = useApi()

const status = ref<any>(null)
const projects = ref<any[]>([])
const containers = ref<any[]>([])
const actionLoading = ref('')
const containerActionLoading = ref<Record<string, string>>({})
const routesTab = ref<'local' | 'public'>('local')
const refreshing = ref(false)

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

async function refreshAll() {
  refreshing.value = true
  await Promise.all([fetchStatus(), fetchProjects(), fetchContainers()])
  setTimeout(() => (refreshing.value = false), 500)
}

async function controlTunnel(action: string) {
  if (!confirm(`Are you sure you want to ${action} the Cloudflare tunnel daemon?`)) return
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

function parseBytes(str: string): number {
  if (!str) return 0
  const match = str.trim().match(/^([\d.]+)\s*([a-zA-Z]+)$/)
  if (!match) return 0
  const val = parseFloat(match[1])
  const unit = match[2].toLowerCase()
  if (unit.startsWith('k')) return val * 1024
  if (unit.startsWith('m')) return val * 1024 * 1024
  if (unit.startsWith('g')) return val * 1024 * 1024 * 1024
  if (unit.startsWith('t')) return val * 1024 * 1024 * 1024 * 1024
  return val
}

function formatBytes(bytes: number): string {
  if (bytes <= 0) return '0 MB'
  const gb = bytes / (1024 * 1024 * 1024)
  if (gb >= 1) return gb.toFixed(2) + ' GB'
  const mb = bytes / (1024 * 1024)
  return mb.toFixed(1) + ' MB'
}

const containerStats = computed(() => {
  const total = containers.value.length
  const running = containers.value.filter((c) => c.running).length
  let totalCpu = 0
  let totalMemBytes = 0
  let totalLimitBytes = 0

  containers.value.forEach((c) => {
    if (c.running) {
      if (c.cpu) {
        const parsed = parseFloat(c.cpu.replace('%', ''))
        if (!isNaN(parsed)) totalCpu += parsed
      }
      if (c.memory && c.memory.includes('/')) {
        const [usedStr, limitStr] = c.memory.split('/')
        totalMemBytes += parseBytes(usedStr)
        const lim = parseBytes(limitStr)
        if (lim > totalLimitBytes) totalLimitBytes = lim
      }
    }
  })

  return {
    total,
    running,
    stopped: total - running,
    cpuPercent: totalCpu.toFixed(1) + '%',
    cpuNum: Math.min(Math.round(totalCpu), 100),
    totalMemStr: formatBytes(totalMemBytes),
    totalLimitStr: totalLimitBytes > 0 ? formatBytes(totalLimitBytes) : 'Host Limit',
    memPercent: totalLimitBytes > 0 ? Math.min(Math.round((totalMemBytes / totalLimitBytes) * 100), 100) : 0,
  }
})

// Local mDNS services (all running containers with portless local address)
const localServices = computed(() => {
  return containers.value.filter((c) => c.running).map((c) => ({
    id: c.id,
    name: c.name,
    image: c.image,
    localUrl: `http://${c.name}.local`,
    status: c.status,
    uptime: c.uptime,
    exposureMode: c.exposure_mode,
    hostname: c.hostname,
  }))
})

const isTunnelRunning = computed(() => {
  return status.value?.status === 'running' || status.value?.status === 'healthy'
})

const isZeroconfRunning = computed(() => {
  return status.value?.zeroconf_status === 'running' || true // zeroconf native proxy
})

let timer: any = null

onMounted(async () => {
  await Promise.all([fetchStatus(), fetchProjects(), fetchContainers()])
  timer = setInterval(() => {
    if (document.visibilityState === 'visible') {
      fetchStatus()
      fetchContainers()
    }
  }, 4000)
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <div class="space-y-6">
    <!-- Header Section -->
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between border-b border-slate-800/80 pb-4">
      <div>
        <div class="flex items-center gap-2">
          <p class="text-[10px] font-semibold uppercase tracking-[0.22em] text-cyan-400">System Control Plane</p>
          <span class="rounded bg-slate-800 px-1.5 py-0.2 text-[9px] font-mono text-slate-400">DockTun 2.0</span>
        </div>
        <h1 class="text-2xl font-bold tracking-tight text-slate-100 flex items-center gap-2.5">
          <span>Overview & Status</span>
          <span
            class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-semibold"
            :class="isTunnelRunning ? 'bg-emerald-950/80 text-emerald-400 border border-emerald-800/60' : 'bg-red-950/80 text-red-400 border border-red-800/60'"
          >
            <span class="h-1.5 w-1.5 rounded-full" :class="isTunnelRunning ? 'bg-emerald-400 animate-pulse' : 'bg-red-400'"></span>
            <span>{{ isTunnelRunning ? 'Mesh Online' : 'Tunnel Degraded' }}</span>
          </span>
        </h1>
      </div>

      <div class="flex items-center gap-2.5">
        <button
          @click="refreshAll"
          :disabled="refreshing"
          class="cyber-btn-secondary text-xs flex items-center gap-1.5 py-1.5 px-3"
          title="Refresh telemetry"
        >
          <RotateCcw class="h-3.5 w-3.5" :class="refreshing ? 'animate-spin' : ''" />
          <span>Sync</span>
        </button>

        <button
          @click="controlTunnel('restart')"
          :disabled="!!actionLoading"
          class="cyber-btn text-xs flex items-center gap-1.5 py-1.5 px-3 text-cyan-300 border-cyan-800 bg-cyan-950/40 hover:bg-cyan-900/50"
          title="Restart Cloudflare tunnel container"
        >
          <RotateCcw class="h-3.5 w-3.5" :class="actionLoading === 'restart' ? 'animate-spin' : ''" />
          <span>{{ actionLoading === 'restart' ? 'Restarting…' : 'Restart Tunnel' }}</span>
        </button>
      </div>
    </div>

    <!-- Tunnel Degraded Diagnostic Banner -->
    <div
      v-if="!isTunnelRunning && status"
      class="rounded-lg border border-red-900/60 bg-red-950/40 p-3.5 text-xs text-red-200 flex flex-col sm:flex-row sm:items-start justify-between gap-3"
    >
      <div class="flex items-start gap-2.5">
        <AlertCircle class="h-4 w-4 text-red-400 mt-0.5 shrink-0" />
        <div class="space-y-1">
          <p class="font-semibold text-red-300">
            Cloudflare Tunnel daemon is {{ status?.status || 'degraded' }}
          </p>
          <p v-if="status?.last_error" class="font-mono text-[11px] text-red-300/80 bg-red-950/70 p-2 rounded border border-red-900/40 whitespace-pre-wrap">
            {{ status.last_error }}
          </p>
          <p v-else class="text-slate-400 text-[11px]">
            Check <code class="text-slate-300 font-mono">docker logs docktun-cloudflared</code> or check credentials in ./cloudflared/credentials.json.
          </p>
        </div>
      </div>
      <button
        @click="controlTunnel('restart')"
        :disabled="!!actionLoading"
        class="cyber-btn text-xs flex items-center gap-1.5 py-1 px-3 border-red-800 bg-red-950/50 hover:bg-red-900/60 text-red-200 shrink-0 self-start sm:self-auto"
      >
        <RotateCcw class="h-3 w-3" :class="actionLoading === 'restart' ? 'animate-spin' : ''" />
        <span>Restart Daemon</span>
      </button>
    </div>

    <!-- 4 Balanced Telemetry Metric Cards -->
    <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
      <!-- 1. Cloudflare Tunnel Card -->
      <div class="cyber-card-glow flex flex-col justify-between">
        <div>
          <div class="flex items-center justify-between text-slate-400">
            <span class="text-[10px] font-semibold uppercase tracking-[0.2em]">Cloudflare Tunnel</span>
            <Globe class="h-4 w-4 text-cyan-400" />
          </div>
          <div class="mt-2.5 flex items-center gap-2">
            <span class="relative flex h-2.5 w-2.5">
              <span
                v-if="isTunnelRunning"
                class="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75"
              ></span>
              <span
                class="relative inline-flex h-2.5 w-2.5 rounded-full"
                :class="isTunnelRunning ? 'bg-emerald-500' : 'bg-red-500'"
              ></span>
            </span>
            <span class="text-base font-bold text-slate-100 truncate" :title="status?.tunnel_name || 'Active'">
              {{ status?.tunnel_name || 'docktun' }}
            </span>
          </div>
        </div>

        <div class="mt-3 border-t border-slate-800/80 pt-2 flex items-center justify-between text-xs text-slate-400">
          <span class="truncate">Uptime: {{ status?.uptime || '—' }}</span>
          <span class="rounded bg-slate-900 px-1.5 py-0.5 text-[10px] text-cyan-300 font-mono">
            {{ status?.protocol || 'QUIC/H2' }}
          </span>
        </div>
      </div>

      <!-- 2. Local LAN Mesh (Zeroconf / mDNS) Card -->
      <div class="cyber-card-glow flex flex-col justify-between">
        <div>
          <div class="flex items-center justify-between text-slate-400">
            <span class="text-[10px] font-semibold uppercase tracking-[0.2em]">Local mDNS Mesh</span>
            <Wifi class="h-4 w-4 text-violet-400" />
          </div>
          <div class="mt-2.5 flex items-center gap-2">
            <span class="h-2.5 w-2.5 rounded-full bg-emerald-400"></span>
            <span class="text-base font-bold text-slate-100 font-mono">*.local</span>
            <span class="rounded bg-violet-950/80 border border-violet-800/50 px-1.5 py-0.2 text-[10px] font-mono text-violet-300">
              Port 80
            </span>
          </div>
        </div>

        <div class="mt-3 border-t border-slate-800/80 pt-2 flex items-center justify-between text-xs text-slate-400">
          <span>{{ localServices.length }} active local routes</span>
          <router-link to="/mesh" class="text-violet-400 hover:underline">Mesh &rarr;</router-link>
        </div>
      </div>

      <!-- 3. Container Fleet Card -->
      <div class="cyber-card-glow flex flex-col justify-between">
        <div>
          <div class="flex items-center justify-between text-slate-400">
            <span class="text-[10px] font-semibold uppercase tracking-[0.2em]">Docker Containers</span>
            <Server class="h-4 w-4 text-emerald-400" />
          </div>
          <div class="mt-2.5 flex items-baseline gap-2">
            <span class="text-2xl font-bold text-emerald-400 font-mono">{{ containerStats.running }}</span>
            <span class="text-xs text-slate-400">/ {{ containerStats.total }} running</span>
          </div>
        </div>

        <div class="mt-3 border-t border-slate-800/80 pt-2 flex items-center justify-between text-xs text-slate-400">
          <span :class="containerStats.stopped > 0 ? 'text-amber-400/90' : 'text-slate-500'">
            {{ containerStats.stopped }} stopped
          </span>
          <router-link to="/containers" class="text-cyan-400 hover:underline">Manage &rarr;</router-link>
        </div>
      </div>

      <!-- 4. Combined Resource Load Card -->
      <div class="cyber-card-glow flex flex-col justify-between">
        <div>
          <div class="flex items-center justify-between text-slate-400">
            <span class="text-[10px] font-semibold uppercase tracking-[0.2em]">Resource Load</span>
            <Cpu class="h-4 w-4 text-cyan-400" />
          </div>
          <div class="mt-2.5 flex items-center justify-between">
            <div class="flex items-baseline gap-1.5">
              <span class="text-xl font-bold text-cyan-300 font-mono">{{ containerStats.cpuPercent }}</span>
              <span class="text-[11px] text-slate-400">CPU</span>
            </div>
            <div class="text-right font-mono text-xs">
              <span class="text-emerald-400 font-bold">{{ containerStats.totalMemStr }}</span>
              <span class="text-[10px] text-slate-500 block">RAM</span>
            </div>
          </div>
        </div>

        <!-- Mini Progress Bars -->
        <div class="mt-3 border-t border-slate-800/80 pt-2 space-y-1.5">
          <div class="flex items-center justify-between text-[10px] text-slate-400">
            <span>Stack CPU Load</span>
            <span class="font-mono">{{ containerStats.cpuPercent }}</span>
          </div>
          <div class="h-1.5 w-full overflow-hidden rounded-full bg-slate-800">
            <div
              class="h-full bg-cyan-400 transition-all duration-300"
              :style="{ width: `${containerStats.cpuNum}%` }"
            ></div>
          </div>
        </div>
      </div>
    </div>

    <!-- Interactive Active Routes Directory (Local LAN & Cloudflare Hostnames) -->
    <div class="cyber-card space-y-4">
      <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-800/80 pb-3">
        <div class="flex items-center gap-2">
          <Activity class="h-4 w-4 text-cyan-400" />
          <h2 class="text-xs font-bold uppercase tracking-[0.2em] text-slate-200">
            Active Routing Directory
          </h2>
        </div>

        <!-- Route Switcher Tabs -->
        <div class="flex items-center gap-1 rounded-lg border border-slate-800 bg-slate-950 p-1 text-xs">
          <button
            @click="routesTab = 'local'"
            class="flex items-center gap-1.5 rounded px-3 py-1 font-semibold transition"
            :class="routesTab === 'local' ? 'bg-violet-950/80 text-violet-300 border border-violet-800/60' : 'text-slate-400 hover:text-slate-200'"
          >
            <Wifi class="h-3 w-3 text-violet-400" />
            <span>Local Services (.local)</span>
            <span class="rounded bg-violet-900/60 px-1.5 py-0.2 text-[10px] text-violet-300 font-mono">{{ localServices.length }}</span>
          </button>

          <button
            @click="routesTab = 'public'"
            class="flex items-center gap-1.5 rounded px-3 py-1 font-semibold transition"
            :class="routesTab === 'public' ? 'bg-cyan-950/80 text-cyan-300 border border-cyan-800/60' : 'text-slate-400 hover:text-slate-200'"
          >
            <Globe class="h-3 w-3 text-cyan-400" />
            <span>Public Hostnames (Cloudflare)</span>
            <span class="rounded bg-cyan-900/60 px-1.5 py-0.2 text-[10px] text-cyan-300 font-mono">{{ status?.hostnames?.length || 0 }}</span>
          </button>
        </div>
      </div>

      <!-- Tab Content 1: Local Services (.local) -->
      <div v-if="routesTab === 'local'" class="space-y-3">
        <div v-if="localServices.length === 0" class="py-6 text-center text-xs text-slate-500">
          No active containers found to expose via mDNS.
        </div>
        <div v-else class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-2.5">
          <div
            v-for="svc in localServices"
            :key="svc.id"
            class="flex items-center justify-between rounded-lg border border-slate-800/80 bg-slate-950/80 p-3 hover:border-violet-700/60 transition group"
          >
            <div class="truncate pr-2">
              <div class="flex items-center gap-1.5">
                <span class="h-2 w-2 rounded-full bg-emerald-400 shrink-0"></span>
                <span class="font-mono text-xs font-bold text-slate-200 truncate">{{ svc.name }}</span>
              </div>
              <div class="font-mono text-[11px] text-violet-300 truncate mt-0.5">
                {{ svc.localUrl }}
              </div>
            </div>

            <a
              :href="svc.localUrl"
              target="_blank"
              rel="noopener"
              class="cyber-btn-secondary text-[11px] flex items-center gap-1 py-1 px-2.5 text-violet-300 group-hover:border-violet-600 shrink-0"
              title="Open local service in browser"
            >
              <span>Open</span>
              <ExternalLink class="h-3 w-3" />
            </a>
          </div>
        </div>
        <p class="text-[11px] text-slate-500">
          All running services are automatically resolved and proxied on standard HTTP port 80 across your Wi-Fi and LAN.
        </p>
      </div>

      <!-- Tab Content 2: Public Cloudflare Hostnames -->
      <div v-else class="space-y-3">
        <div v-if="!status?.hostnames || status.hostnames.length === 0" class="py-6 text-center text-xs text-slate-500">
          No public ingress hostnames currently bound in Cloudflare configuration.
        </div>
        <div v-else class="flex flex-wrap gap-2.5">
          <a
            v-for="h in status.hostnames"
            :key="h"
            :href="`https://${h}`"
            target="_blank"
            rel="noopener"
            class="inline-flex items-center gap-2 rounded-lg border border-cyan-900/50 bg-slate-950 px-3.5 py-2 text-xs font-mono text-cyan-300 hover:border-cyan-500 hover:bg-cyan-950/30 transition shadow-sm"
          >
            <ShieldCheck class="h-3.5 w-3.5 text-cyan-400" />
            <span>https://{{ h }}</span>
            <ExternalLink class="h-3 w-3 text-cyan-500" />
          </a>
        </div>
      </div>
    </div>

    <!-- Main Live Containers Monitor & Compose Snapshot -->
    <div class="grid gap-6 lg:grid-cols-3">
      <!-- Left 2 Cols: Live Containers Snapshot -->
      <div class="lg:col-span-2 cyber-card space-y-4 flex flex-col justify-between">
        <div>
          <div class="flex items-center justify-between border-b border-slate-800/80 pb-3">
            <div class="flex items-center gap-2">
              <Server class="h-4 w-4 text-emerald-400" />
              <h2 class="text-xs font-bold uppercase tracking-[0.2em] text-slate-200">
                Container Fleet Snapshot ({{ containers.length }})
              </h2>
            </div>
            <router-link to="/containers" class="text-xs text-cyan-400 hover:underline">
              Control Center &rarr;
            </router-link>
          </div>

          <div v-if="containers.length === 0" class="py-8 text-center text-xs text-slate-500">
            No Docker containers detected on system.
          </div>

          <div v-else class="divide-y divide-slate-800/60">
            <div
              v-for="c in containers.slice(0, 6)"
              :key="c.id"
              class="flex flex-col sm:flex-row sm:items-center justify-between py-2.5 gap-2 transition hover:bg-slate-900/30 px-1 rounded"
            >
              <!-- Container Name & Details -->
              <div class="flex items-center gap-2.5 overflow-hidden">
                <span
                  class="h-2 w-2 flex-shrink-0 rounded-full"
                  :class="c.running ? 'bg-emerald-400' : 'bg-slate-600'"
                ></span>
                <div class="truncate">
                  <div class="flex items-center gap-2">
                    <span class="font-mono text-xs font-bold text-slate-200 truncate">{{ c.name }}</span>
                    <span
                      class="rounded px-1.5 py-0.2 text-[9px] font-mono"
                      :class="c.exposure_mode === 'public' ? 'bg-cyan-950 text-cyan-300 border border-cyan-800/50' : 'bg-slate-900 text-slate-400'"
                    >
                      {{ c.exposure_mode === 'public' ? 'Public' : 'Internal' }}
                    </span>
                  </div>
                  <div class="text-[10px] text-slate-400 truncate font-mono">
                    {{ c.image }} • <Clock class="inline h-2.5 w-2.5 text-slate-500" /> {{ c.uptime || c.status }}
                  </div>
                </div>
              </div>

              <!-- Metrics & Quick Actions -->
              <div class="flex items-center justify-between sm:justify-end gap-3 self-end sm:self-auto w-full sm:w-auto">
                <div v-if="c.running" class="text-right font-mono text-[10px]">
                  <span class="text-cyan-400 font-bold mr-2">{{ c.cpu || '0.0%' }}</span>
                  <span class="text-slate-400">{{ c.memory?.split('/')[0] || '' }}</span>
                </div>
                <div v-else class="text-[10px] text-slate-500 font-mono">
                  stopped
                </div>

                <!-- Container Actions -->
                <div class="flex items-center gap-1">
                  <button
                    v-if="!c.running"
                    @click="runContainerAction(c, 'start')"
                    :disabled="!!containerActionLoading[c.id]"
                    class="rounded p-1 text-slate-400 hover:bg-slate-800 hover:text-emerald-400"
                    title="Start container"
                  >
                    <Play class="h-3.5 w-3.5 fill-current" />
                  </button>
                  <button
                    v-if="c.running"
                    @click="runContainerAction(c, 'stop')"
                    :disabled="!!containerActionLoading[c.id]"
                    class="rounded p-1 text-slate-400 hover:bg-slate-800 hover:text-amber-400"
                    title="Stop container"
                  >
                    <Square class="h-3.5 w-3.5 fill-current" />
                  </button>
                  <button
                    @click="runContainerAction(c, 'restart')"
                    :disabled="!!containerActionLoading[c.id]"
                    class="rounded p-1 text-slate-400 hover:bg-slate-800 hover:text-slate-200"
                    title="Restart container"
                  >
                    <RotateCcw
                      class="h-3.5 w-3.5"
                      :class="containerActionLoading[c.id] === 'restart' ? 'animate-spin' : ''"
                    />
                  </button>
                </div>
              </div>
            </div>
          </div>
        </div>

        <div class="pt-2 border-t border-slate-800/80">
          <router-link
            to="/containers"
            class="cyber-btn-secondary w-full text-center text-xs flex items-center justify-center gap-2 py-2"
          >
            <span>Open Containers Control Plane</span>
            <ArrowUpRight class="h-3.5 w-3.5" />
          </router-link>
        </div>
      </div>

      <!-- Right 1 Col: Compose Stacks & Fast Info -->
      <div class="cyber-card space-y-4 flex flex-col justify-between">
        <div>
          <div class="flex items-center justify-between border-b border-slate-800/80 pb-3">
            <div class="flex items-center gap-2">
              <FolderOpen class="h-4 w-4 text-violet-400" />
              <h2 class="text-xs font-bold uppercase tracking-[0.2em] text-slate-200">
                Compose Stacks ({{ projects.length }})
              </h2>
            </div>
            <router-link to="/projects" class="text-xs text-violet-400 hover:underline">
              All &rarr;
            </router-link>
          </div>

          <div v-if="projects.length === 0" class="py-8 text-center text-xs text-slate-500">
            No Compose stacks registered yet.
          </div>

          <div v-else class="space-y-2">
            <div
              v-for="p in projects.slice(0, 5)"
              :key="p.id"
              class="flex items-center justify-between rounded-lg border border-slate-800/80 bg-slate-950 p-2.5 transition hover:border-slate-700"
            >
              <div class="truncate pr-2">
                <div class="font-mono text-xs font-bold text-slate-200 truncate">{{ p.name }}</div>
                <div class="text-[10px] text-slate-400 font-mono truncate">{{ p.path }}</div>
              </div>

              <router-link
                to="/projects"
                class="rounded border border-slate-700 bg-slate-900 px-2 py-1 text-[10px] text-slate-300 hover:border-slate-500 hover:text-slate-100 shrink-0 font-mono"
              >
                Inspect
              </router-link>
            </div>
          </div>
        </div>

        <div class="space-y-2 pt-2 border-t border-slate-800/80">
          <div class="rounded-lg border border-slate-800/80 bg-slate-950 p-3 space-y-1.5 text-xs">
            <div class="flex items-center justify-between text-slate-400">
              <span class="text-[10px] uppercase tracking-wider font-semibold">Active Mesh</span>
              <span class="font-mono text-emerald-400 font-bold">Native Zeroconf</span>
            </div>
            <div class="flex items-center justify-between text-slate-400">
              <span class="text-[10px] uppercase tracking-wider font-semibold">Portless Proxy</span>
              <span class="font-mono text-cyan-300">Port 80 ➔ Target</span>
            </div>
          </div>

          <router-link
            to="/projects"
            class="cyber-btn-secondary w-full text-center text-xs flex items-center justify-center gap-2 py-2"
          >
            <span>Open Projects Manager</span>
            <ArrowUpRight class="h-3.5 w-3.5" />
          </router-link>
        </div>
      </div>
    </div>
  </div>
</template>
