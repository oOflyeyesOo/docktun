<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  Activity,
  Cpu,
  Layers,
  HardDrive,
  Clock,
  ArrowDown,
  ArrowUp,
  Search,
  RotateCcw,
  SlidersHorizontal,
  ExternalLink,
  FileText,
  Server,
  Play,
  Pause,
  Filter,
  Check,
  AlertCircle,
  Globe,
  Radio,
  Zap,
} from 'lucide-vue-next'
import { useApi } from '../composables/useApi'

interface HostMetrics {
  cpus: number
  load_avg: [number, number, number]
  total_cpu_percent: number
  mem_total: number
  mem_used: number
  mem_free: number
  mem_percent: number
  disk_total: number
  disk_used: number
  disk_free: number
  disk_percent: number
  uptime_seconds: number
  containers_total: number
  containers_running: number
  containers_stopped: number
}

interface ContainerMetric {
  id: string
  name: string
  image: string
  state: string
  status: string
  project?: string
  cpu_percent: string
  cpu_num: number
  mem_usage: string
  mem_limit: string
  mem_bytes: number
  mem_limit_bytes: number
  mem_percent: number
  net_io: string
  block_io: string
  pids: number
}

interface MetricsData {
  host: HostMetrics
  containers: ContainerMetric[]
  total_requests?: number
  active_connections?: number
  ha_connections?: number
  colo_locations?: string[]
  response_codes?: Record<string, number>
}

const route = useRoute()
const router = useRouter()
const { apiFetch } = useApi()

// Data state
const metrics = ref<MetricsData | null>(null)
const loading = ref(true)
const refreshing = ref(false)
const lastUpdated = ref<Date | null>(null)

// History buffers for sparklines (up to 30 points)
const cpuHistory = ref<number[]>([])
const memHistory = ref<number[]>([])

// Controls
const updateInterval = ref<number>(3000) // 3s default
let timer: ReturnType<typeof setInterval> | null = null

// Filters & Sorting
const searchQuery = ref('')
const stackFilter = ref('all')
const stateFilter = ref<'all' | 'running' | 'stopped'>('all')
const sortBy = ref<'cpu_desc' | 'cpu_asc' | 'mem_desc' | 'mem_asc' | 'name' | 'pids'>('cpu_desc')

// Targeted container from query string
const targetedContainer = computed(() => {
  const q = route.query.container
  return typeof q === 'string' ? q.trim() : ''
})

async function fetchMetrics(silent = false) {
  if (!silent && !metrics.value) {
    loading.value = true
  }
  refreshing.value = true

  try {
    const data: MetricsData = await apiFetch('/api/metrics')
    metrics.value = data
    lastUpdated.value = new Date()

    // Push into sparkline history
    const cpuVal = data.host?.total_cpu_percent || 0
    cpuHistory.value.push(cpuVal)
    if (cpuHistory.value.length > 30) cpuHistory.value.shift()

    const memVal = data.host?.mem_percent || 0
    memHistory.value.push(memVal)
    if (memHistory.value.length > 30) memHistory.value.shift()
  } catch (err) {
    console.error('Failed to fetch metrics:', err)
  } finally {
    loading.value = false
    refreshing.value = false
  }
}

function resetTimer() {
  if (timer) {
    clearInterval(timer)
    timer = null
  }
  if (updateInterval.value > 0) {
    timer = setInterval(() => fetchMetrics(true), updateInterval.value)
  }
}

function setIntervalRate(ms: number) {
  updateInterval.value = ms
  resetTimer()
}

// Available Stacks for Filter
const availableStacks = computed(() => {
  if (!metrics.value?.containers) return []
  const set = new Set<string>()
  for (const c of metrics.value.containers) {
    if (c.project) set.add(c.project)
  }
  return Array.from(set).sort()
})

// Filtered and sorted containers
const filteredContainers = computed(() => {
  if (!metrics.value?.containers) return []
  let list = [...metrics.value.containers]

  // Query search
  if (searchQuery.value) {
    const q = searchQuery.value.toLowerCase()
    list = list.filter(
      (c) =>
        c.name.toLowerCase().includes(q) ||
        c.image.toLowerCase().includes(q) ||
        (c.project && c.project.toLowerCase().includes(q))
    )
  }

  // Stack filter
  if (stackFilter.value !== 'all') {
    if (stackFilter.value === '__standalone__') {
      list = list.filter((c) => !c.project)
    } else {
      list = list.filter((c) => c.project === stackFilter.value)
    }
  }

  // State filter
  if (stateFilter.value === 'running') {
    list = list.filter((c) => c.state === 'running' || c.status.toLowerCase().startsWith('up'))
  } else if (stateFilter.value === 'stopped') {
    list = list.filter((c) => c.state !== 'running' && !c.status.toLowerCase().startsWith('up'))
  }

  // Sorting
  list.sort((a, b) => {
    switch (sortBy.value) {
      case 'cpu_desc':
        return (b.cpu_num || 0) - (a.cpu_num || 0)
      case 'cpu_asc':
        return (a.cpu_num || 0) - (b.cpu_num || 0)
      case 'mem_desc':
        return (b.mem_bytes || 0) - (a.mem_bytes || 0)
      case 'mem_asc':
        return (a.mem_bytes || 0) - (b.mem_bytes || 0)
      case 'pids':
        return (b.pids || 0) - (a.pids || 0)
      case 'name':
      default:
        return a.name.localeCompare(b.name)
    }
  })

  return list
})

// Scroll targeted container into view
function scrollToTarget() {
  if (!targetedContainer.value) return
  nextTick(() => {
    const el = document.getElementById(`metric-container-${targetedContainer.value}`)
    if (el) {
      el.scrollIntoView({ behavior: 'smooth', block: 'center' })
    }
  })
}

watch(targetedContainer, () => {
  scrollToTarget()
})

onMounted(async () => {
  await fetchMetrics()
  resetTimer()
  if (targetedContainer.value) {
    setTimeout(scrollToTarget, 400)
  }
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
})

function clearTarget() {
  const query = { ...route.query }
  delete query.container
  router.replace({ query })
}

// Sparkline path generator
function generateSparkline(data: number[], width = 240, height = 44): { path: string; area: string } {
  if (!data || data.length < 2) return { path: '', area: '' }
  const max = Math.max(...data, 10)
  const min = 0
  const range = max - min || 1
  const step = width / (data.length - 1)

  const points = data.map((val, idx) => {
    const x = idx * step
    const y = height - ((val - min) / range) * (height - 6) - 3
    return `${x.toFixed(1)},${y.toFixed(1)}`
  })

  const path = `M ${points.join(' L ')}`
  const area = `${path} L ${width},${height} L 0,${height} Z`
  return { path, area }
}

const cpuSparkline = computed(() => generateSparkline(cpuHistory.value))
const memSparkline = computed(() => generateSparkline(memHistory.value))

function formatBytes(bytes: number): string {
  if (!bytes || bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
}

function formatUptime(seconds: number): string {
  if (!seconds || seconds <= 0) return '—'
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (days > 0) return `${days}d ${hours}h`
  if (hours > 0) return `${hours}h ${minutes}m`
  return `${minutes}m`
}

function formatLoad(load?: [number, number, number]): string {
  if (!load) return '—'
  return `${load[0]?.toFixed(2) || '0.00'}, ${load[1]?.toFixed(2) || '0.00'}, ${load[2]?.toFixed(2) || '0.00'}`
}

function getCpuColorClass(pct: number): string {
  if (pct >= 85) return 'text-rose-600 bg-rose-50 border-rose-200'
  if (pct >= 50) return 'text-amber-600 bg-amber-50 border-amber-200'
  return 'text-indigo-600 bg-indigo-50 border-indigo-200'
}

function getCpuBarClass(pct: number): string {
  if (pct >= 85) return 'bg-rose-500'
  if (pct >= 50) return 'bg-amber-500'
  return 'bg-indigo-500'
}
</script>

<template>
  <div class="space-y-6">
    <!-- Header with Live Interval Controls & Deep Link Banner -->
    <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
      <div>
        <div class="flex items-center gap-2.5">
          <div class="p-2 rounded-xl bg-indigo-500/10 border border-indigo-500/20 text-indigo-600">
            <Activity class="h-5 w-5" />
          </div>
          <div>
            <h1 class="text-xl font-bold tracking-tight text-slate-900 flex items-center gap-2">
              System & Container Metrics
              <span class="inline-flex items-center gap-1 text-[11px] font-mono px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-700 border border-emerald-500/20">
                <Radio class="h-2.5 w-2.5 animate-pulse text-emerald-500" />
                Live Telemetry
              </span>
            </h1>
            <p class="text-xs text-slate-600">Real-time host compute load, memory footprints, and per-container resource metrics</p>
          </div>
        </div>
      </div>

      <!-- Controls: Refresh Rates & Manual Trigger -->
      <div class="flex items-center gap-2 flex-wrap">
        <div class="flex items-center rounded-lg border border-slate-200 bg-slate-50 p-0.5 text-xs font-mono">
          <button
            @click="setIntervalRate(2000)"
            :class="updateInterval === 2000 ? 'bg-white shadow-xs text-indigo-600 font-semibold' : 'text-slate-600 hover:text-slate-900'"
            class="px-2.5 py-1 rounded-md transition"
            title="Update every 2 seconds"
          >
            2s (Live)
          </button>
          <button
            @click="setIntervalRate(5000)"
            :class="updateInterval === 5000 ? 'bg-white shadow-xs text-indigo-600 font-semibold' : 'text-slate-600 hover:text-slate-900'"
            class="px-2.5 py-1 rounded-md transition"
            title="Update every 5 seconds"
          >
            5s
          </button>
          <button
            @click="setIntervalRate(10000)"
            :class="updateInterval === 10000 ? 'bg-white shadow-xs text-indigo-600 font-semibold' : 'text-slate-600 hover:text-slate-900'"
            class="px-2.5 py-1 rounded-md transition"
            title="Update every 10 seconds"
          >
            10s
          </button>
          <button
            @click="setIntervalRate(0)"
            :class="updateInterval === 0 ? 'bg-white shadow-xs text-rose-600 font-semibold' : 'text-slate-600 hover:text-slate-900'"
            class="px-2 py-1 rounded-md transition flex items-center gap-1"
            title="Pause auto-refresh"
          >
            <Pause v-if="updateInterval === 0" class="h-3 w-3" />
            <span>Pause</span>
          </button>
        </div>

        <button
          @click="fetchMetrics(false)"
          :disabled="refreshing"
          class="cyber-btn-secondary text-xs flex items-center gap-1.5 py-1.5 px-3 text-slate-700 hover:text-slate-900"
          title="Refresh metrics immediately"
        >
          <RotateCcw class="h-3.5 w-3.5" :class="refreshing ? 'animate-spin text-indigo-600' : ''" />
          <span>Refresh</span>
        </button>
      </div>
    </div>

    <!-- Targeted Container Deep-Link Alert Banner -->
    <div
      v-if="targetedContainer"
      class="flex items-center justify-between p-3.5 rounded-xl border border-indigo-200 bg-indigo-50/70 text-indigo-900 text-xs shadow-xs"
    >
      <div class="flex items-center gap-2.5">
        <div class="p-1 rounded-lg bg-indigo-600 text-white">
          <Zap class="h-3.5 w-3.5" />
        </div>
        <div>
          <span class="font-semibold">Deep-linked Inspection Target:</span>
          <span class="font-mono ml-1.5 px-1.5 py-0.5 rounded bg-white border border-indigo-300 font-bold text-indigo-700">
            {{ targetedContainer }}
          </span>
          <span class="text-indigo-700/80 ml-2">Viewing focused metrics for this container</span>
        </div>
      </div>
      <button
        @click="clearTarget"
        class="text-indigo-600 hover:text-indigo-900 font-medium underline px-2 py-1 rounded hover:bg-indigo-100 transition"
      >
        Show All Containers
      </button>
    </div>

    <!-- Top Telemetry KPI Cards -->
    <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
      <!-- 1. CPU Workload & Cores -->
      <div class="cyber-card flex flex-col justify-between">
        <div>
          <div class="flex items-center justify-between text-slate-600">
            <span class="text-[11px] font-semibold uppercase tracking-wider">Host CPU Workload</span>
            <div class="p-1.5 rounded-lg bg-indigo-500/10 text-indigo-600">
              <Cpu class="h-4 w-4" />
            </div>
          </div>

          <div class="mt-3 flex items-baseline justify-between">
            <div class="flex items-baseline gap-1.5">
              <span class="text-2xl font-bold font-mono text-slate-900">
                {{ metrics?.host?.total_cpu_percent ? metrics.host.total_cpu_percent.toFixed(1) + '%' : '0.0%' }}
              </span>
              <span class="text-xs text-slate-500 font-mono">active</span>
            </div>
            <span class="text-xs font-mono font-semibold px-2 py-0.5 rounded bg-slate-100 text-slate-700 border border-slate-200">
              {{ metrics?.host?.cpus || 1 }} vCPUs
            </span>
          </div>

          <div class="mt-2 flex items-center justify-between text-[11px] font-mono text-slate-600">
            <span>Load Avg (1m, 5m, 15m):</span>
            <span class="font-semibold text-slate-800">{{ formatLoad(metrics?.host?.load_avg) }}</span>
          </div>
        </div>

        <div class="mt-4 space-y-2 border-t border-slate-200/80 pt-3">
          <!-- Sparkline -->
          <div v-if="cpuSparkline.path" class="h-10 w-full overflow-hidden flex items-end">
            <svg class="w-full h-10 overflow-visible" viewBox="0 0 240 44" preserveAspectRatio="none">
              <defs>
                <linearGradient id="cpuGrad" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stop-color="#6366f1" stop-opacity="0.3" />
                  <stop offset="100%" stop-color="#6366f1" stop-opacity="0.0" />
                </linearGradient>
              </defs>
              <path :d="cpuSparkline.area" fill="url(#cpuGrad)" />
              <path :d="cpuSparkline.path" fill="none" stroke="#6366f1" stroke-width="2" stroke-linecap="round" />
            </svg>
          </div>
          <!-- Progress Bar -->
          <div class="h-1.5 w-full overflow-hidden rounded-full bg-slate-100">
            <div
              class="h-full bg-indigo-500 transition-all duration-300 rounded-full"
              :style="{ width: `${Math.min(metrics?.host?.total_cpu_percent || 0, 100)}%` }"
            ></div>
          </div>
        </div>
      </div>

      <!-- 2. Host RAM Usage -->
      <div class="cyber-card flex flex-col justify-between">
        <div>
          <div class="flex items-center justify-between text-slate-600">
            <span class="text-[11px] font-semibold uppercase tracking-wider">Memory Allocation</span>
            <div class="p-1.5 rounded-lg bg-purple-500/10 text-purple-600">
              <Layers class="h-4 w-4" />
            </div>
          </div>

          <div class="mt-3 flex items-baseline justify-between">
            <div class="flex items-baseline gap-1.5">
              <span class="text-2xl font-bold font-mono text-slate-900">
                {{ formatBytes(metrics?.host?.mem_used || 0) }}
              </span>
              <span class="text-xs text-slate-500 font-mono">/ {{ formatBytes(metrics?.host?.mem_total || 0) }}</span>
            </div>
            <span class="text-xs font-mono font-semibold px-2 py-0.5 rounded bg-purple-50 text-purple-700 border border-purple-200">
              {{ metrics?.host?.mem_percent ? metrics.host.mem_percent.toFixed(1) + '%' : '—' }}
            </span>
          </div>

          <div class="mt-2 flex items-center justify-between text-[11px] font-mono text-slate-600">
            <span>Free / Available:</span>
            <span class="font-semibold text-emerald-600">{{ formatBytes(metrics?.host?.mem_free || 0) }}</span>
          </div>
        </div>

        <div class="mt-4 space-y-2 border-t border-slate-200/80 pt-3">
          <!-- Sparkline -->
          <div v-if="memSparkline.path" class="h-10 w-full overflow-hidden flex items-end">
            <svg class="w-full h-10 overflow-visible" viewBox="0 0 240 44" preserveAspectRatio="none">
              <defs>
                <linearGradient id="memGrad" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stop-color="#a855f7" stop-opacity="0.3" />
                  <stop offset="100%" stop-color="#a855f7" stop-opacity="0.0" />
                </linearGradient>
              </defs>
              <path :d="memSparkline.area" fill="url(#memGrad)" />
              <path :d="memSparkline.path" fill="none" stroke="#a855f7" stroke-width="2" stroke-linecap="round" />
            </svg>
          </div>
          <!-- Progress Bar -->
          <div class="h-1.5 w-full overflow-hidden rounded-full bg-slate-100">
            <div
              class="h-full bg-purple-500 transition-all duration-300 rounded-full"
              :style="{ width: `${Math.min(metrics?.host?.mem_percent || 0, 100)}%` }"
            ></div>
          </div>
        </div>
      </div>

      <!-- 3. Host Storage Space -->
      <div class="cyber-card flex flex-col justify-between">
        <div>
          <div class="flex items-center justify-between text-slate-600">
            <span class="text-[11px] font-semibold uppercase tracking-wider">Storage Volume</span>
            <div class="p-1.5 rounded-lg bg-emerald-500/10 text-emerald-600">
              <HardDrive class="h-4 w-4" />
            </div>
          </div>

          <div class="mt-3 flex items-baseline justify-between">
            <div class="flex items-baseline gap-1.5">
              <span class="text-2xl font-bold font-mono text-slate-900">
                {{ formatBytes(metrics?.host?.disk_used || 0) }}
              </span>
              <span class="text-xs text-slate-500 font-mono">/ {{ formatBytes(metrics?.host?.disk_total || 0) }}</span>
            </div>
            <span class="text-xs font-mono font-semibold px-2 py-0.5 rounded bg-emerald-50 text-emerald-700 border border-emerald-200">
              {{ metrics?.host?.disk_percent ? metrics.host.disk_percent.toFixed(1) + '%' : '—' }}
            </span>
          </div>

          <div class="mt-2 flex items-center justify-between text-[11px] font-mono text-slate-600">
            <span>Free Storage:</span>
            <span class="font-semibold text-slate-800">{{ formatBytes(metrics?.host?.disk_free || 0) }}</span>
          </div>
        </div>

        <div class="mt-4 space-y-2 border-t border-slate-200/80 pt-3">
          <div class="flex items-center justify-between text-[11px] font-mono text-slate-500">
            <span>Volume Mount: /app/data</span>
            <router-link to="/storage" class="text-indigo-600 hover:underline">Storage &rarr;</router-link>
          </div>
          <div class="h-1.5 w-full overflow-hidden rounded-full bg-slate-100">
            <div
              class="h-full bg-emerald-500 transition-all duration-300 rounded-full"
              :style="{ width: `${Math.min(metrics?.host?.disk_percent || 0, 100)}%` }"
            ></div>
          </div>
        </div>
      </div>

      <!-- 4. Fleet & Uptime -->
      <div class="cyber-card flex flex-col justify-between">
        <div>
          <div class="flex items-center justify-between text-slate-600">
            <span class="text-[11px] font-semibold uppercase tracking-wider">Fleet & System Uptime</span>
            <div class="p-1.5 rounded-lg bg-cyan-500/10 text-cyan-600">
              <Clock class="h-4 w-4" />
            </div>
          </div>

          <div class="mt-3 flex items-baseline justify-between">
            <div class="flex items-baseline gap-1.5">
              <span class="text-2xl font-bold font-mono text-slate-900">
                {{ metrics?.host?.containers_running || 0 }}
              </span>
              <span class="text-xs text-slate-500 font-mono">/ {{ metrics?.host?.containers_total || 0 }} running</span>
            </div>
            <span class="text-xs font-mono font-semibold px-2 py-0.5 rounded bg-cyan-50 text-cyan-700 border border-cyan-200">
              {{ metrics?.host?.containers_stopped || 0 }} stopped
            </span>
          </div>

          <div class="mt-2 flex items-center justify-between text-[11px] font-mono text-slate-600">
            <span>System Uptime:</span>
            <span class="font-semibold text-slate-800">{{ formatUptime(metrics?.host?.uptime_seconds || 0) }}</span>
          </div>
        </div>

        <div class="mt-4 border-t border-slate-200/80 pt-3 flex items-center justify-between text-[11px] font-mono text-slate-600">
          <span>Last sync: {{ lastUpdated ? lastUpdated.toLocaleTimeString() : '—' }}</span>
          <router-link to="/containers" class="text-indigo-600 hover:underline">Manage Fleet &rarr;</router-link>
        </div>
      </div>
    </div>

    <!-- Container Resource Breakdown Section -->
    <div class="cyber-card space-y-4">
      <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-200/80 pb-3">
        <div class="flex items-center gap-2 flex-wrap">
          <Server class="h-4 w-4 text-indigo-600" />
          <h2 class="text-sm font-semibold text-slate-900">
            Per-Container Live Resource Breakdown
          </h2>
          <span class="text-[10px] px-2 py-0.5 rounded-full bg-slate-100 text-slate-700 font-mono font-medium">
            {{ filteredContainers.length }} / {{ metrics?.containers?.length || 0 }} containers
          </span>
        </div>

        <!-- Filter & Search Controls -->
        <div class="flex items-center gap-2 flex-wrap">
          <!-- Search box -->
          <div class="relative w-40 sm:w-48">
            <Search class="h-3.5 w-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-slate-400" />
            <input
              v-model="searchQuery"
              type="text"
              placeholder="Search container…"
              class="w-full pl-8 pr-2.5 py-1 text-xs bg-slate-50 border border-slate-200 rounded-lg text-slate-800 placeholder:text-slate-400 focus:outline-none focus:border-indigo-500 font-mono"
            />
          </div>

          <!-- Stack Filter -->
          <select
            v-if="availableStacks.length > 0"
            v-model="stackFilter"
            class="text-xs bg-slate-50 border border-slate-200 rounded-lg px-2 py-1 text-slate-700 focus:outline-none focus:border-indigo-500 font-mono"
          >
            <option value="all">All Stacks</option>
            <option v-for="stk in availableStacks" :key="stk" :value="stk">
              {{ stk === 'docktun' ? 'DockTun Core' : stk }}
            </option>
            <option value="__standalone__">Standalone</option>
          </select>

          <!-- State Filter -->
          <select
            v-model="stateFilter"
            class="text-xs bg-slate-50 border border-slate-200 rounded-lg px-2 py-1 text-slate-700 focus:outline-none focus:border-indigo-500 font-mono"
          >
            <option value="all">All States</option>
            <option value="running">Running</option>
            <option value="stopped">Stopped</option>
          </select>

          <!-- Sort dropdown -->
          <select
            v-model="sortBy"
            class="text-xs bg-slate-50 border border-slate-200 rounded-lg px-2 py-1 text-slate-700 focus:outline-none focus:border-indigo-500 font-mono"
          >
            <option value="cpu_desc">CPU: High &rarr; Low</option>
            <option value="cpu_asc">CPU: Low &rarr; High</option>
            <option value="mem_desc">RAM: High &rarr; Low</option>
            <option value="mem_asc">RAM: Low &rarr; High</option>
            <option value="pids">PIDs: High &rarr; Low</option>
            <option value="name">Name: A &rarr; Z</option>
          </select>
        </div>
      </div>

      <!-- Loading skeleton -->
      <div v-if="loading && !metrics" class="py-12 text-center text-slate-400 text-sm font-mono">
        <RotateCcw class="h-6 w-6 animate-spin mx-auto text-indigo-500 mb-2" />
        Collecting live metrics from Docker engine…
      </div>

      <!-- Empty state -->
      <div v-else-if="filteredContainers.length === 0" class="py-10 text-center text-slate-500 text-xs font-mono">
        No containers matched the selected filters.
      </div>

      <!-- Containers Table (Desktop & Tablet) -->
      <div v-else class="overflow-x-auto">
        <table class="w-full text-left text-xs border-collapse">
          <thead>
            <tr class="border-b border-slate-200/80 text-slate-500 font-semibold uppercase tracking-wider text-[10px]">
              <th class="py-2.5 px-3">Container</th>
              <th class="py-2.5 px-3">State</th>
              <th class="py-2.5 px-3 min-w-[140px]">CPU Usage</th>
              <th class="py-2.5 px-3 min-w-[160px]">RAM Usage</th>
              <th class="py-2.5 px-3">Network I/O</th>
              <th class="py-2.5 px-3">Block I/O</th>
              <th class="py-2.5 px-3">PIDs</th>
              <th class="py-2.5 px-3 text-right">Actions</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100">
            <tr
              v-for="c in filteredContainers"
              :key="c.id"
              :id="`metric-container-${c.name}`"
              :class="[
                targetedContainer === c.name
                  ? 'bg-indigo-50/70 ring-2 ring-indigo-500/80 rounded-lg'
                  : 'hover:bg-slate-50/80 transition'
              ]"
            >
              <!-- Container Name & Stack -->
              <td class="py-3 px-3">
                <div class="flex items-center gap-2">
                  <div
                    class="h-2 w-2 rounded-full shrink-0"
                    :class="c.state === 'running' || c.status.toLowerCase().startsWith('up') ? 'bg-emerald-500' : 'bg-slate-400'"
                  ></div>
                  <div class="min-w-0 max-w-[200px]">
                    <div class="font-semibold text-slate-900 truncate font-mono" :title="c.name">
                      {{ c.name }}
                    </div>
                    <div class="flex items-center gap-1.5 text-[10px] text-slate-500 truncate mt-0.5">
                      <span
                        v-if="c.project"
                        class="px-1.5 py-0.2 rounded font-mono text-[9px] bg-purple-50 text-purple-700 border border-purple-200 shrink-0"
                      >
                        {{ c.project }}
                      </span>
                      <span class="truncate" :title="c.image">{{ c.image }}</span>
                    </div>
                  </div>
                </div>
              </td>

              <!-- State & Status -->
              <td class="py-3 px-3 font-mono text-[11px]">
                <span
                  class="inline-block px-1.5 py-0.5 rounded text-[10px] font-medium uppercase"
                  :class="c.state === 'running' || c.status.toLowerCase().startsWith('up') ? 'bg-emerald-50 text-emerald-700 border border-emerald-200' : 'bg-slate-100 text-slate-600'"
                >
                  {{ c.state || 'stopped' }}
                </span>
                <div class="text-[10px] text-slate-500 mt-0.5 truncate max-w-[110px]" :title="c.status">
                  {{ c.status }}
                </div>
              </td>

              <!-- CPU Usage Gauge -->
              <td class="py-3 px-3 font-mono">
                <div class="flex items-center justify-between text-xs mb-1">
                  <span class="font-bold" :class="c.cpu_num > 50 ? 'text-amber-600' : 'text-indigo-600'">
                    {{ c.cpu_percent || '0.0%' }}
                  </span>
                </div>
                <div class="h-1.5 w-full overflow-hidden rounded-full bg-slate-100">
                  <div
                    class="h-full transition-all duration-300 rounded-full"
                    :class="getCpuBarClass(c.cpu_num || 0)"
                    :style="{ width: `${Math.min(c.cpu_num || 0, 100)}%` }"
                  ></div>
                </div>
              </td>

              <!-- RAM Usage Gauge -->
              <td class="py-3 px-3 font-mono">
                <div class="flex items-center justify-between text-xs mb-1">
                  <span class="font-bold text-slate-800">
                    {{ c.mem_usage || '—' }}
                  </span>
                  <span v-if="c.mem_percent > 0" class="text-[10px] text-slate-500">
                    {{ c.mem_percent.toFixed(1) }}%
                  </span>
                </div>
                <div class="h-1.5 w-full overflow-hidden rounded-full bg-slate-100 mb-0.5">
                  <div
                    class="h-full bg-purple-500 transition-all duration-300 rounded-full"
                    :style="{ width: `${Math.min(c.mem_percent || 0, 100)}%` }"
                  ></div>
                </div>
                <div class="text-[10px] text-slate-400 truncate">
                  Limit: {{ c.mem_limit || 'Unlimited' }}
                </div>
              </td>

              <!-- Network I/O -->
              <td class="py-3 px-3 font-mono text-[11px] text-slate-700">
                <div v-if="c.net_io" class="truncate max-w-[130px]" :title="`Net I/O: ${c.net_io}`">
                  {{ c.net_io }}
                </div>
                <div v-else class="text-slate-400">—</div>
              </td>

              <!-- Block I/O -->
              <td class="py-3 px-3 font-mono text-[11px] text-slate-700">
                <div v-if="c.block_io" class="truncate max-w-[120px]" :title="`Block I/O: ${c.block_io}`">
                  {{ c.block_io }}
                </div>
                <div v-else class="text-slate-400">—</div>
              </td>

              <!-- PIDs -->
              <td class="py-3 px-3 font-mono text-[11px] text-slate-800 font-semibold">
                {{ c.pids || '—' }}
              </td>

              <!-- Quick Deep-Links -->
              <td class="py-3 px-3 text-right">
                <div class="flex items-center justify-end gap-1.5">
                  <router-link
                    :to="`/containers?name=${encodeURIComponent(c.name)}`"
                    class="p-1.5 rounded-lg text-indigo-600 hover:bg-indigo-50 border border-transparent hover:border-indigo-200 transition"
                    :title="`Inspect container ${c.name} in Containers view`"
                  >
                    <ExternalLink class="h-3.5 w-3.5" />
                  </router-link>
                  <router-link
                    :to="`/logs?container=${encodeURIComponent(c.name)}`"
                    class="p-1.5 rounded-lg text-slate-600 hover:bg-slate-100 border border-transparent hover:border-slate-200 transition"
                    :title="`View logs for container ${c.name}`"
                  >
                    <FileText class="h-3.5 w-3.5" />
                  </router-link>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- Cloudflare Edge Tunnel Metrics Section (Collapsible / Summary) -->
    <div v-if="metrics?.total_requests !== undefined" class="cyber-card p-4 space-y-3">
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-2">
          <Globe class="h-4 w-4 text-orange-500" />
          <h3 class="text-xs font-semibold text-slate-900">Cloudflare Tunnel Edge Telemetry</h3>
        </div>
        <router-link to="/access?tab=cloudflare&sub=ingress" class="text-xs text-indigo-600 hover:underline">
          Manage Ingress &rarr;
        </router-link>
      </div>

      <div class="grid grid-cols-2 sm:grid-cols-4 gap-3 text-xs font-mono">
        <div class="p-2.5 rounded-lg bg-slate-50 border border-slate-200">
          <span class="text-[10px] text-slate-500 uppercase tracking-wider block">Total Requests</span>
          <span class="text-base font-bold text-slate-900">{{ metrics.total_requests.toLocaleString() }}</span>
        </div>
        <div class="p-2.5 rounded-lg bg-slate-50 border border-slate-200">
          <span class="text-[10px] text-slate-500 uppercase tracking-wider block">Active Streams</span>
          <span class="text-base font-bold text-indigo-600">{{ metrics.active_connections || 0 }}</span>
        </div>
        <div class="p-2.5 rounded-lg bg-slate-50 border border-slate-200">
          <span class="text-[10px] text-slate-500 uppercase tracking-wider block">HA Conns</span>
          <span class="text-base font-bold text-emerald-600">{{ metrics.ha_connections || 0 }}</span>
        </div>
        <div class="p-2.5 rounded-lg bg-slate-50 border border-slate-200">
          <span class="text-[10px] text-slate-500 uppercase tracking-wider block">Edge Colos</span>
          <span class="text-base font-bold text-slate-900">{{ metrics.colo_locations?.length || 0 }} locations</span>
        </div>
      </div>
    </div>
  </div>
</template>
