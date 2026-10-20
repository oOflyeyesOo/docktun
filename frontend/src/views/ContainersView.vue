<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import {
  Server,
  Play,
  Square,
  RotateCcw,
  Trash2,
  ScrollText,
  Shield,
  ShieldCheck,
  Globe,
  Lock,
  ExternalLink,
  Cpu,
  Layers,
  ArrowUpDown,
  Search,
  CheckCircle2,
  AlertCircle,
  X,
  Info,
  Clock,
  HardDrive
} from 'lucide-vue-next'
import { useApi } from '../composables/useApi'

const { apiFetch } = useApi()

interface ExposureSettings {
  mode: string
  hostname?: string
  target?: string
  private_type?: string
  private_host?: string
  private_port?: number
  notes?: string
}

interface ContainerItem {
  id: string
  name: string
  image: string
  status: string
  state: string
  ports: string
  command: string
  running: boolean
  exposure_mode?: string
  hostname?: string
  target?: string
  private_type?: string
  private_host?: string
  private_port?: number
  notes?: string
  cpu?: string
  memory?: string
  network?: string
  block_io?: string
  pids?: number
  restart_count?: number
  uptime?: string
  exposure?: ExposureSettings
}

interface ContainerDetail extends ContainerItem {
  env?: string[]
  mounts?: Array<{ source: string; destination: string; mode: string; rw: boolean }>
  ip_address?: string
  gateway?: string
  mac_address?: string
  networks?: string[]
  exit_code?: number
}

const containers = ref<ContainerItem[]>([])
const loading = ref(false)
const searchQuery = ref('')
const filter = ref<'all' | 'running' | 'stopped' | 'public' | 'private'>('all')

const actionLoading = ref<Record<string, string>>({})
const exposeDrawerId = ref<string | null>(null)
const exposeForm = ref<Record<string, ExposureSettings>>({})
const exposeSaving = ref<Record<string, boolean>>({})

// Logs modal
const logsContainer = ref<ContainerItem | null>(null)
const logsText = ref('')
const logsLoading = ref(false)
const logsTail = ref(150)

// Inspect modal
const inspectContainer = ref<ContainerDetail | null>(null)
const inspectLoading = ref(false)

const filteredContainers = computed(() => {
  return containers.value.filter((c) => {
    // Mode/state filter
    if (filter.value === 'running' && !c.running) return false
    if (filter.value === 'stopped' && c.running) return false
    if (filter.value === 'public' && c.exposure_mode !== 'public') return false
    if (filter.value === 'private' && c.exposure_mode !== 'private') return false

    // Search query
    if (!searchQuery.value) return true
    const q = searchQuery.value.toLowerCase()
    return (
      c.name.toLowerCase().includes(q) ||
      c.image.toLowerCase().includes(q) ||
      (c.hostname && c.hostname.toLowerCase().includes(q)) ||
      (c.ports && c.ports.toLowerCase().includes(q))
    )
  })
})

const summary = computed(() => {
  const total = containers.value.length
  const running = containers.value.filter((c) => c.running).length
  const publicCount = containers.value.filter((c) => c.exposure_mode === 'public').length
  const privateCount = containers.value.filter((c) => c.exposure_mode === 'private').length

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
    publicCount,
    privateCount,
    cpuUsage: totalCpu.toFixed(1) + '%',
  }
})

function parseCpuPercent(cpuStr?: string): number {
  if (!cpuStr) return 0
  const n = parseFloat(cpuStr.replace('%', ''))
  return isNaN(n) ? 0 : Math.min(n, 100)
}

function parseMemoryUsage(memStr?: string): { used: string; total: string; percent: number } {
  if (!memStr) return { used: '—', total: '—', percent: 0 }
  // Format: "124.5MiB / 1.95GiB" or with percent
  const parts = memStr.split('/')
  if (parts.length >= 2) {
    const used = parts[0].trim()
    const total = parts[1].trim()
    // try to calculate approx percent
    return { used, total, percent: 0 }
  }
  return { used: memStr, total: '', percent: 0 }
}

async function fetchContainers() {
  try {
    const data = await apiFetch<ContainerItem[]>('/api/docker/containers')
    containers.value = data || []
    containers.value.forEach((c) => {
      if (!exposeForm.value[c.id]) {
        exposeForm.value[c.id] = {
          mode: c.exposure_mode || c.exposure?.mode || 'internal',
          hostname: c.hostname || c.exposure?.hostname || '',
          target: c.target || c.exposure?.target || `http://${c.name}:80`,
          private_type: c.private_type || c.exposure?.private_type || 'tailscale',
          private_host: c.private_host || c.exposure?.private_host || '',
          private_port: c.private_port || c.exposure?.private_port || 80,
          notes: c.notes || c.exposure?.notes || '',
        }
      }
    })
  } catch (error) {
    console.error('Failed to load containers:', error)
  }
}

async function runAction(container: ContainerItem, action: 'start' | 'stop' | 'restart') {
  actionLoading.value[container.id] = action
  try {
    await apiFetch(`/api/docker/containers/${container.id}/${action}`, { method: 'POST' })
    await fetchContainers()
  } catch (error: any) {
    alert(error.message || `Failed to ${action} container`)
  } finally {
    delete actionLoading.value[container.id]
  }
}

async function removeContainer(container: ContainerItem) {
  if (!confirm(`Are you sure you want to permanently delete container "${container.name}"?`)) return
  actionLoading.value[container.id] = 'remove'
  try {
    await apiFetch(`/api/docker/containers/${container.id}/remove`, {
      method: 'POST',
      body: JSON.stringify({ force: true }),
    })
    await fetchContainers()
  } catch (error: any) {
    alert(error.message || 'Failed to remove container')
  } finally {
    delete actionLoading.value[container.id]
  }
}

function toggleExposeDrawer(container: ContainerItem) {
  if (exposeDrawerId.value === container.id) {
    exposeDrawerId.value = null
  } else {
    exposeDrawerId.value = container.id
    if (!exposeForm.value[container.id]) {
      exposeForm.value[container.id] = {
        mode: container.exposure_mode || 'internal',
        hostname: container.hostname || '',
        target: container.target || `http://${container.name}:80`,
        private_type: container.private_type || 'tailscale',
        private_host: container.private_host || '',
        private_port: container.private_port || 80,
        notes: container.notes || '',
      }
    }
  }
}

async function saveExposure(container: ContainerItem) {
  exposeSaving.value[container.id] = true
  try {
    const payload = exposeForm.value[container.id]
    await apiFetch(`/api/docker/containers/${container.id}/exposure`, {
      method: 'POST',
      body: JSON.stringify(payload),
    })
    await fetchContainers()
    exposeDrawerId.value = null
  } catch (error: any) {
    alert(error.message || 'Failed to save exposure settings')
  } finally {
    exposeSaving.value[container.id] = false
  }
}

async function openLogs(container: ContainerItem) {
  logsContainer.value = container
  logsLoading.value = true
  logsText.value = ''
  try {
    const res = await apiFetch<{ logs: string }>(`/api/docker/containers/${container.id}/logs?tail=${logsTail.value}`)
    logsText.value = res.logs || 'No logs recorded.'
  } catch (error: any) {
    logsText.value = 'Failed to load logs: ' + error.message
  } finally {
    logsLoading.value = false
  }
}

async function openInspect(container: ContainerItem) {
  inspectContainer.value = null
  inspectLoading.value = true
  try {
    inspectContainer.value = await apiFetch<ContainerDetail>(`/api/docker/containers/${container.id}`)
  } catch (error: any) {
    alert(error.message || 'Failed to inspect container')
  } finally {
    inspectLoading.value = false
  }
}

let timer: any = null

onMounted(async () => {
  loading.value = true
  await fetchContainers()
  loading.value = false

  // Live polling every 4 seconds
  timer = setInterval(() => {
    if (document.visibilityState === 'visible') {
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
    <!-- Header -->
    <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <div class="flex items-center gap-2 text-[10px] font-semibold uppercase tracking-[0.22em] text-slate-400">
          <Layers class="h-3.5 w-3.5 text-cyan-400" />
          <span>Container Orchestration</span>
        </div>
        <h1 class="text-2xl font-bold tracking-tight text-slate-100">Containers</h1>
      </div>
      <div class="flex items-center gap-2">
        <button @click="fetchContainers" class="cyber-btn-secondary flex items-center gap-2 text-xs">
          <RotateCcw class="h-3.5 w-3.5" /> Refresh
        </button>
      </div>
    </div>

    <!-- Live Telemetry Summary Cards -->
    <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
      <div class="cyber-card-glow">
        <div class="flex items-center justify-between text-slate-400">
          <span class="text-[10px] font-semibold uppercase tracking-[0.2em]">Total Stack</span>
          <Server class="h-4 w-4 text-slate-500" />
        </div>
        <div class="mt-2 flex items-baseline gap-2">
          <span class="text-2xl font-bold text-slate-100">{{ summary.total }}</span>
          <span class="text-xs text-slate-400">containers</span>
        </div>
        <div class="mt-2 flex items-center gap-2 text-xs">
          <span class="flex items-center gap-1 text-emerald-400">
            <span class="inline-block h-2 w-2 rounded-full bg-emerald-400"></span> {{ summary.running }} running
          </span>
          <span class="text-slate-600">•</span>
          <span class="text-slate-400">{{ summary.stopped }} stopped</span>
        </div>
      </div>

      <div class="cyber-card-glow">
        <div class="flex items-center justify-between text-slate-400">
          <span class="text-[10px] font-semibold uppercase tracking-[0.2em]">Aggregate CPU</span>
          <Cpu class="h-4 w-4 text-cyan-400" />
        </div>
        <div class="mt-2 text-2xl font-bold text-cyan-300">{{ summary.cpuUsage }}</div>
        <p class="mt-2 text-xs text-slate-400">Combined live CPU across all containers</p>
      </div>

      <div class="cyber-card-glow">
        <div class="flex items-center justify-between text-slate-400">
          <span class="text-[10px] font-semibold uppercase tracking-[0.2em]">Public Ingress</span>
          <Globe class="h-4 w-4 text-cyan-400" />
        </div>
        <div class="mt-2 text-2xl font-bold text-slate-100">{{ summary.publicCount }}</div>
        <p class="mt-2 text-xs text-slate-400">Exposed via Cloudflare Tunnel</p>
      </div>

      <div class="cyber-card-glow">
        <div class="flex items-center justify-between text-slate-400">
          <span class="text-[10px] font-semibold uppercase tracking-[0.2em]">Private Mesh</span>
          <Shield class="h-4 w-4 text-violet-400" />
        </div>
        <div class="mt-2 text-2xl font-bold text-slate-100">{{ summary.privateCount }}</div>
        <p class="mt-2 text-xs text-slate-400">Tailscale / WireGuard routes</p>
      </div>
    </div>

    <!-- Filters & Search Toolbar -->
    <div class="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
      <div class="flex flex-wrap items-center gap-1.5 rounded-lg border border-slate-800 bg-slate-900/60 p-1">
        <button
          @click="filter = 'all'"
          :class="filter === 'all' ? 'bg-slate-800 text-slate-100' : 'text-slate-400 hover:text-slate-200'"
          class="rounded px-3 py-1.5 text-[11px] font-semibold uppercase tracking-[0.16em] transition"
        >
          All ({{ summary.total }})
        </button>
        <button
          @click="filter = 'running'"
          :class="filter === 'running' ? 'bg-emerald-500/20 text-emerald-300 border border-emerald-500/30' : 'text-slate-400 hover:text-slate-200'"
          class="rounded px-3 py-1.5 text-[11px] font-semibold uppercase tracking-[0.16em] transition"
        >
          Running ({{ summary.running }})
        </button>
        <button
          @click="filter = 'stopped'"
          :class="filter === 'stopped' ? 'bg-slate-800 text-slate-100' : 'text-slate-400 hover:text-slate-200'"
          class="rounded px-3 py-1.5 text-[11px] font-semibold uppercase tracking-[0.16em] transition"
        >
          Stopped ({{ summary.stopped }})
        </button>
        <button
          @click="filter = 'public'"
          :class="filter === 'public' ? 'bg-cyan-500/20 text-cyan-300 border border-cyan-500/30' : 'text-slate-400 hover:text-slate-200'"
          class="rounded px-3 py-1.5 text-[11px] font-semibold uppercase tracking-[0.16em] transition"
        >
          Public ({{ summary.publicCount }})
        </button>
        <button
          @click="filter = 'private'"
          :class="filter === 'private' ? 'bg-violet-500/20 text-violet-300 border border-violet-500/30' : 'text-slate-400 hover:text-slate-200'"
          class="rounded px-3 py-1.5 text-[11px] font-semibold uppercase tracking-[0.16em] transition"
        >
          Private ({{ summary.privateCount }})
        </button>
      </div>

      <div class="relative w-full md:w-72">
        <Search class="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500" />
        <input
          v-model="searchQuery"
          type="text"
          placeholder="Filter container, image, port..."
          class="cyber-input pl-9 text-xs"
        />
      </div>
    </div>

    <!-- Empty state -->
    <div v-if="filteredContainers.length === 0 && !loading" class="cyber-card py-12 text-center text-slate-400">
      <Server class="mx-auto h-8 w-8 text-slate-600 mb-2" />
      <p class="text-sm font-medium text-slate-300">No containers match the current filter.</p>
      <p class="mt-1 text-xs text-slate-500">Try adjusting your search terms or status filters.</p>
    </div>

    <!-- Containers List -->
    <div class="space-y-4">
      <div
        v-for="container in filteredContainers"
        :key="container.id"
        class="cyber-card transition-all duration-200 hover:border-slate-700"
      >
        <div class="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
          <!-- Left: Identity & Status -->
          <div class="space-y-2">
            <div class="flex flex-wrap items-center gap-2.5">
              <!-- Pulsing status dot -->
              <span
                class="relative flex h-2.5 w-2.5"
                :title="container.running ? 'Running' : 'Stopped'"
              >
                <span
                  v-if="container.running"
                  class="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75"
                ></span>
                <span
                  class="relative inline-flex h-2.5 w-2.5 rounded-full"
                  :class="container.running ? 'bg-emerald-500' : 'bg-slate-600'"
                ></span>
              </span>

              <span class="text-base font-bold text-slate-100 font-mono">{{ container.name }}</span>

              <!-- Status Badge -->
              <span
                class="rounded border px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wider"
                :class="container.running ? 'bg-emerald-500/10 text-emerald-400 border-emerald-500/30' : 'bg-slate-800 text-slate-400 border-slate-700'"
              >
                {{ container.running ? 'Running' : 'Stopped' }}
              </span>

              <!-- Exposure Badge -->
              <span
                v-if="container.exposure_mode === 'public'"
                class="inline-flex items-center gap-1.5 rounded-full border border-cyan-500/40 bg-cyan-500/10 px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-cyan-300"
              >
                <Globe class="h-3 w-3" />
                <span>Public Ingress</span>
              </span>
              <span
                v-else-if="container.exposure_mode === 'private'"
                class="inline-flex items-center gap-1.5 rounded-full border border-violet-500/40 bg-violet-500/10 px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-violet-300"
              >
                <Shield class="h-3 w-3" />
                <span>Private Mesh ({{ container.private_type || 'tailscale' }})</span>
              </span>
              <span
                v-else
                class="inline-flex items-center gap-1.5 rounded-full border border-slate-800 bg-slate-900/60 px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-slate-400"
              >
                <Lock class="h-3 w-3 text-slate-500" />
                <span>Internal</span>
              </span>
            </div>

            <!-- Image & Command -->
            <div class="flex flex-wrap items-center gap-3 text-xs text-slate-400">
              <span class="font-mono text-slate-300">{{ container.image }}</span>
              <span>•</span>
              <span class="flex items-center gap-1 text-slate-400">
                <Clock class="h-3 w-3 text-slate-500" /> {{ container.uptime || container.status }}
              </span>
              <span v-if="container.restart_count" class="text-amber-400/90">
                • Restarts: {{ container.restart_count }}
              </span>
            </div>

            <!-- Public Hostname Link if Exposed -->
            <div v-if="container.exposure_mode === 'public' && container.hostname" class="flex items-center gap-2 pt-1 text-xs">
              <span class="text-slate-400">Public URL:</span>
              <a
                :href="`https://${container.hostname}`"
                target="_blank"
                rel="noopener"
                class="inline-flex items-center gap-1 font-mono text-cyan-400 hover:text-cyan-300 hover:underline"
              >
                <span>https://{{ container.hostname }}</span>
                <ExternalLink class="h-3 w-3" />
              </a>
            </div>
          </div>

          <!-- Right: Control Actions -->
          <div class="flex flex-wrap items-center gap-2">
            <button
              v-if="!container.running"
              @click="runAction(container, 'start')"
              :disabled="!!actionLoading[container.id]"
              class="cyber-btn flex items-center gap-1.5 text-xs text-emerald-300 hover:border-emerald-600 hover:bg-emerald-950/40"
            >
              <Play class="h-3.5 w-3.5 fill-current" />
              <span>{{ actionLoading[container.id] === 'start' ? 'Starting…' : 'Start' }}</span>
            </button>

            <button
              v-if="container.running"
              @click="runAction(container, 'stop')"
              :disabled="!!actionLoading[container.id]"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-amber-300 hover:border-amber-600 hover:bg-amber-950/40"
            >
              <Square class="h-3.5 w-3.5 fill-current" />
              <span>{{ actionLoading[container.id] === 'stop' ? 'Stopping…' : 'Stop' }}</span>
            </button>

            <button
              @click="runAction(container, 'restart')"
              :disabled="!!actionLoading[container.id]"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs"
            >
              <RotateCcw class="h-3.5 w-3.5" :class="actionLoading[container.id] === 'restart' ? 'animate-spin' : ''" />
              <span>Restart</span>
            </button>

            <button
              @click="toggleExposeDrawer(container)"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs"
              :class="exposeDrawerId === container.id ? 'border-cyan-500 text-cyan-300' : ''"
            >
              <Globe class="h-3.5 w-3.5 text-cyan-400" />
              <span>Expose</span>
            </button>

            <button
              @click="openLogs(container)"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs"
            >
              <ScrollText class="h-3.5 w-3.5 text-slate-400" />
              <span>Logs</span>
            </button>

            <button
              @click="openInspect(container)"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs"
              title="Inspect details"
            >
              <Info class="h-3.5 w-3.5 text-slate-400" />
              <span>Inspect</span>
            </button>

            <button
              @click="removeContainer(container)"
              :disabled="!!actionLoading[container.id]"
              class="cyber-btn-danger flex items-center gap-1.5 text-xs"
              title="Delete container"
            >
              <Trash2 class="h-3.5 w-3.5" />
            </button>
          </div>
        </div>

        <!-- Live Telemetry Row -->
        <div class="mt-4 grid grid-cols-2 gap-3 sm:grid-cols-4 rounded-lg border border-slate-800/80 bg-slate-950/60 p-3">
          <!-- CPU -->
          <div class="space-y-1">
            <div class="flex items-center justify-between text-[10px] font-semibold uppercase tracking-wider text-slate-400">
              <span class="flex items-center gap-1"><Cpu class="h-3 w-3 text-cyan-400" /> CPU</span>
              <span class="font-mono text-slate-200">{{ container.cpu || (container.running ? '0.0%' : '—') }}</span>
            </div>
            <div class="h-1.5 w-full overflow-hidden rounded-full bg-slate-800">
              <div
                class="h-full bg-cyan-400 transition-all duration-300"
                :style="{ width: `${parseCpuPercent(container.cpu)}%` }"
              ></div>
            </div>
          </div>

          <!-- Memory -->
          <div class="space-y-1">
            <div class="flex items-center justify-between text-[10px] font-semibold uppercase tracking-wider text-slate-400">
              <span class="flex items-center gap-1"><Layers class="h-3 w-3 text-emerald-400" /> RAM</span>
              <span class="font-mono text-slate-200 truncate max-w-[110px]" :title="container.memory">
                {{ parseMemoryUsage(container.memory).used }}
              </span>
            </div>
            <div class="text-[10px] text-slate-500 truncate">
              {{ parseMemoryUsage(container.memory).total ? `Limit: ${parseMemoryUsage(container.memory).total}` : (container.running ? 'Active' : '—') }}
            </div>
          </div>

          <!-- Network I/O -->
          <div class="space-y-1">
            <div class="flex items-center justify-between text-[10px] font-semibold uppercase tracking-wider text-slate-400">
              <span class="flex items-center gap-1"><ArrowUpDown class="h-3 w-3 text-violet-400" /> Net I/O</span>
            </div>
            <div class="font-mono text-xs text-slate-200 truncate" :title="container.network">
              {{ container.network || (container.running ? '0B / 0B' : '—') }}
            </div>
          </div>

          <!-- Ports / PIDs -->
          <div class="space-y-1">
            <div class="flex items-center justify-between text-[10px] font-semibold uppercase tracking-wider text-slate-400">
              <span class="flex items-center gap-1"><HardDrive class="h-3 w-3 text-amber-400" /> Ports</span>
              <span v-if="container.pids" class="text-[10px] text-slate-400 font-mono">{{ container.pids }} pids</span>
            </div>
            <div class="font-mono text-xs text-slate-200 truncate" :title="container.ports">
              {{ container.ports || '—' }}
            </div>
          </div>
        </div>

        <!-- Inline Expose / Exposure Config Drawer -->
        <div v-if="exposeDrawerId === container.id" class="mt-4 rounded-xl border border-cyan-500/30 bg-slate-950 p-4">
          <div class="flex items-center justify-between border-b border-slate-800 pb-3">
            <div class="flex items-center gap-2">
              <Globe class="h-4 w-4 text-cyan-400" />
              <h3 class="text-xs font-bold uppercase tracking-[0.18em] text-slate-200">
                Exposure & Ingress Configuration: {{ container.name }}
              </h3>
            </div>
            <button @click="exposeDrawerId = null" class="text-slate-400 hover:text-slate-200">
              <X class="h-4 w-4" />
            </button>
          </div>

          <div class="mt-4 grid gap-4 md:grid-cols-3">
            <div>
              <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-400">
                Access Mode
              </label>
              <select v-model="exposeForm[container.id].mode" class="cyber-input">
                <option value="internal">Internal (No Ingress)</option>
                <option value="public">Public (Cloudflare Tunnel)</option>
                <option value="private">Private (Tailscale / WireGuard)</option>
              </select>
            </div>

            <!-- Public Mode Inputs -->
            <template v-if="exposeForm[container.id].mode === 'public'">
              <div>
                <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-400">
                  Public Hostname (FQDN)
                </label>
                <input
                  v-model="exposeForm[container.id].hostname"
                  type="text"
                  placeholder="app.yourdomain.com"
                  class="cyber-input"
                />
              </div>
              <div>
                <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-400">
                  Service Target URL
                </label>
                <input
                  v-model="exposeForm[container.id].target"
                  type="text"
                  :placeholder="`http://${container.name}:80`"
                  class="cyber-input"
                />
              </div>
            </template>

            <!-- Private Mode Inputs -->
            <template v-if="exposeForm[container.id].mode === 'private'">
              <div>
                <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-400">
                  Private Mesh Type
                </label>
                <select v-model="exposeForm[container.id].private_type" class="cyber-input">
                  <option value="tailscale">Tailscale</option>
                  <option value="wireguard">WireGuard</option>
                  <option value="mesh">Custom Mesh</option>
                </select>
              </div>
              <div>
                <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-400">
                  Private Host / IP
                </label>
                <input
                  v-model="exposeForm[container.id].private_host"
                  type="text"
                  placeholder="100.x.y.z or 10.0.0.x"
                  class="cyber-input"
                />
              </div>
              <div>
                <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-400">
                  Private Port
                </label>
                <input
                  v-model.number="exposeForm[container.id].private_port"
                  type="number"
                  placeholder="8080"
                  class="cyber-input"
                />
              </div>
            </template>
          </div>

          <div class="mt-4 flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 pt-3 border-t border-slate-900">
            <p class="text-xs text-slate-400">
              <span v-if="exposeForm[container.id].mode === 'public'" class="text-cyan-300">
                Saving public mode automatically adds a Cloudflare Tunnel ingress rule and reloads cloudflared.
              </span>
              <span v-else-if="exposeForm[container.id].mode === 'private'" class="text-violet-300">
                Routes traffic privately through your encrypted mesh network.
              </span>
              <span v-else class="text-slate-500">
                Removes public ingress rules for this container. Accessible only inside Docker network.
              </span>
            </p>
            <div class="flex items-center gap-2">
              <button @click="exposeDrawerId = null" class="cyber-btn-secondary text-xs">
                Cancel
              </button>
              <button
                @click="saveExposure(container)"
                :disabled="exposeSaving[container.id]"
                class="cyber-btn flex items-center gap-1.5 text-xs text-cyan-300 border-cyan-600 bg-cyan-950/40 hover:bg-cyan-900/50"
              >
                <ShieldCheck class="h-3.5 w-3.5" />
                <span>{{ exposeSaving[container.id] ? 'Saving & Applying…' : 'Save & Apply Ingress' }}</span>
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Container Logs Modal -->
    <div
      v-if="logsContainer"
      class="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/80 p-4 backdrop-blur-sm"
      @click.self="logsContainer = null"
    >
      <div class="flex max-h-[85vh] w-full max-w-4xl flex-col rounded-xl border border-slate-800 bg-slate-900 shadow-2xl">
        <div class="flex items-center justify-between border-b border-slate-800 px-5 py-3">
          <div class="flex items-center gap-2">
            <ScrollText class="h-4 w-4 text-cyan-400" />
            <h3 class="text-sm font-bold text-slate-100 font-mono">Logs: {{ logsContainer.name }}</h3>
          </div>
          <div class="flex items-center gap-3">
            <select
              v-model.number="logsTail"
              @change="openLogs(logsContainer!)"
              class="rounded border border-slate-700 bg-slate-950 px-2 py-1 text-xs text-slate-300"
            >
              <option :value="100">Last 100 lines</option>
              <option :value="300">Last 300 lines</option>
              <option :value="500">Last 500 lines</option>
            </select>
            <button @click="openLogs(logsContainer!)" class="text-slate-400 hover:text-slate-200">
              <RotateCcw class="h-4 w-4" :class="logsLoading ? 'animate-spin' : ''" />
            </button>
            <button @click="logsContainer = null" class="text-slate-400 hover:text-slate-200">
              <X class="h-4 w-4" />
            </button>
          </div>
        </div>
        <div class="flex-1 overflow-auto bg-slate-950 p-4 font-mono text-xs leading-5 text-slate-300">
          <pre v-if="logsLoading" class="text-slate-500">Fetching container logs…</pre>
          <pre v-else class="whitespace-pre-wrap">{{ logsText }}</pre>
        </div>
      </div>
    </div>

    <!-- Container Inspect Modal -->
    <div
      v-if="inspectContainer"
      class="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/80 p-4 backdrop-blur-sm"
      @click.self="inspectContainer = null"
    >
      <div class="flex max-h-[85vh] w-full max-w-3xl flex-col rounded-xl border border-slate-800 bg-slate-900 shadow-2xl">
        <div class="flex items-center justify-between border-b border-slate-800 px-5 py-3">
          <div class="flex items-center gap-2">
            <Info class="h-4 w-4 text-cyan-400" />
            <h3 class="text-sm font-bold text-slate-100 font-mono">Inspect: {{ inspectContainer.name }}</h3>
          </div>
          <button @click="inspectContainer = null" class="text-slate-400 hover:text-slate-200">
            <X class="h-4 w-4" />
          </button>
        </div>
        <div class="flex-1 overflow-auto p-5 space-y-4 text-xs">
          <!-- Network Details -->
          <div class="rounded-lg border border-slate-800 bg-slate-950 p-3">
            <h4 class="text-[10px] font-bold uppercase tracking-wider text-slate-400 mb-2">Network</h4>
            <div class="grid grid-cols-2 gap-2 font-mono">
              <div>IP Address: <span class="text-slate-200">{{ inspectContainer.ip_address || 'Host / None' }}</span></div>
              <div>Gateway: <span class="text-slate-200">{{ inspectContainer.gateway || '—' }}</span></div>
              <div>MAC: <span class="text-slate-200">{{ inspectContainer.mac_address || '—' }}</span></div>
              <div>Networks: <span class="text-slate-200">{{ inspectContainer.networks?.join(', ') || '—' }}</span></div>
            </div>
          </div>

          <!-- Mounts -->
          <div v-if="inspectContainer.mounts?.length" class="rounded-lg border border-slate-800 bg-slate-950 p-3">
            <h4 class="text-[10px] font-bold uppercase tracking-wider text-slate-400 mb-2">Volumes & Mounts</h4>
            <div class="space-y-1.5 font-mono">
              <div v-for="(m, i) in inspectContainer.mounts" :key="i" class="text-slate-300">
                <span class="text-cyan-400">{{ m.source }}</span>
                <span class="text-slate-500"> -> </span>
                <span class="text-emerald-400">{{ m.destination }}</span>
                <span class="text-slate-500 text-[10px]"> ({{ m.mode || (m.rw ? 'rw' : 'ro') }})</span>
              </div>
            </div>
          </div>

          <!-- Environment -->
          <div v-if="inspectContainer.env?.length" class="rounded-lg border border-slate-800 bg-slate-950 p-3">
            <h4 class="text-[10px] font-bold uppercase tracking-wider text-slate-400 mb-2">Environment Variables</h4>
            <div class="space-y-1 font-mono text-[11px] max-h-48 overflow-auto">
              <div v-for="(e, i) in inspectContainer.env" :key="i" class="text-slate-300">
                {{ e }}
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
