<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import {
  RefreshCw,
  ExternalLink,
  Activity,
  CheckCircle2,
  XCircle,
  Clock,
  Search,
  Box,
  Sliders,
  PlayCircle
} from 'lucide-vue-next'
import { useApi } from '../composables/useApi'

interface HealthService {
  hostname: string
  service: string
  status: 'up' | 'down' | 'invalid'
  latency: string
  latency_ms: number
  probing?: boolean
}

const { apiFetch } = useApi()
const results = ref<HealthService[]>([])
const loading = ref(false)
const searchQuery = ref('')
const filterStatus = ref<'all' | 'up' | 'down'>('all')
const autoRefreshInterval = ref<number>(30) // seconds
let refreshTimer: any = null

async function check() {
  loading.value = true
  try {
    const res = await apiFetch<HealthService[]>('/api/health')
    results.value = Array.isArray(res) ? res : []
  } catch {
    results.value = []
  } finally {
    loading.value = false
  }
}

async function probeSingle(item: HealthService) {
  item.probing = true
  try {
    const start = performance.now()
    const res = await apiFetch<{ reachable: boolean; latency_ms: number; status_code?: number }>(
      '/api/docker/probe',
      {
        method: 'POST',
        body: JSON.stringify({ target: item.service })
      }
    )
    const elapsed = Math.round(performance.now() - start)
    if (res.reachable) {
      item.status = 'up'
      item.latency_ms = res.latency_ms || elapsed
      item.latency = `${item.latency_ms}ms`
    } else {
      item.status = 'down'
      item.latency = 'unreachable'
      item.latency_ms = -1
    }
  } catch {
    item.status = 'down'
    item.latency = 'error'
    item.latency_ms = -1
  } finally {
    item.probing = false
  }
}

function setupAutoRefresh() {
  if (refreshTimer) clearInterval(refreshTimer)
  if (autoRefreshInterval.value > 0) {
    refreshTimer = setInterval(() => {
      check()
    }, autoRefreshInterval.value * 1000)
  }
}

function onIntervalChange() {
  setupAutoRefresh()
}

onMounted(() => {
  check()
  setupAutoRefresh()
})

onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer)
})

const onlineCount = computed(() => results.value.filter(r => r.status === 'up').length)
const offlineCount = computed(() => results.value.filter(r => r.status === 'down').length)

const avgLatency = computed(() => {
  const upServices = results.value.filter(r => r.status === 'up' && r.latency_ms > 0)
  if (!upServices.length) return '0ms'
  const sum = upServices.reduce((acc, curr) => acc + curr.latency_ms, 0)
  return `${Math.round(sum / upServices.length)}ms`
})

const filteredResults = computed(() => {
  return results.value.filter(r => {
    if (filterStatus.value !== 'all' && r.status !== filterStatus.value) return false
    if (!searchQuery.value) return true
    const q = searchQuery.value.toLowerCase()
    return r.hostname.toLowerCase().includes(q) || r.service.toLowerCase().includes(q)
  })
})

function getContainerName(serviceUrl: string): string {
  try {
    let clean = serviceUrl.replace(/^https?:\/\//, '')
    clean = clean.split(':')[0]
    return clean.replace(/\.local$/, '')
  } catch {
    return ''
  }
}
</script>

<template>
  <div class="space-y-6">
    <!-- Header -->
    <div class="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4">
      <div>
        <h1 class="cyber-heading text-xl lg:text-2xl flex items-center gap-2">
          <Activity class="w-6 h-6 text-neon" />
          Origin Health & Status
        </h1>
        <p class="text-xs text-muted mt-1 font-mono">
          Real-time probe monitoring of Cloudflare Tunnel ingress origins and backend containers.
        </p>
      </div>

      <div class="flex items-center gap-3">
        <!-- Auto Refresh Selector -->
        <div class="flex items-center gap-1.5 text-xs text-muted font-mono bg-dark-bg/60 border border-dark-border rounded px-2.5 py-1.5">
          <Clock class="w-3.5 h-3.5 text-muted" />
          <span>Auto:</span>
          <select
            v-model="autoRefreshInterval"
            @change="onIntervalChange"
            class="bg-transparent text-gray-200 outline-none cursor-pointer"
          >
            <option :value="10">10s</option>
            <option :value="30">30s</option>
            <option :value="60">60s</option>
            <option :value="0">Off</option>
          </select>
        </div>

        <button @click="check" :disabled="loading" class="cyber-btn-secondary flex items-center gap-2">
          <RefreshCw class="w-4 h-4" :stroke-width="1.5" :class="loading && 'animate-spin'" />
          Probe All
        </button>
      </div>
    </div>

    <!-- Summary Metrics Grid -->
    <div class="grid grid-cols-2 sm:grid-cols-4 gap-4">
      <div class="cyber-card p-4">
        <div class="flex items-center justify-between text-muted mb-1">
          <span class="text-xs font-mono uppercase tracking-wider">Total Targets</span>
          <Activity class="w-4 h-4 text-neon" />
        </div>
        <p class="text-2xl font-bold font-mono text-gray-100">{{ results.length }}</p>
      </div>

      <div class="cyber-card p-4">
        <div class="flex items-center justify-between text-muted mb-1">
          <span class="text-xs font-mono uppercase tracking-wider">Online / Active</span>
          <CheckCircle2 class="w-4 h-4 text-neon" />
        </div>
        <p class="text-2xl font-bold font-mono text-neon">{{ onlineCount }}</p>
      </div>

      <div class="cyber-card p-4">
        <div class="flex items-center justify-between text-muted mb-1">
          <span class="text-xs font-mono uppercase tracking-wider">Degraded / Down</span>
          <XCircle class="w-4 h-4 text-danger" />
        </div>
        <p class="text-2xl font-bold font-mono" :class="offlineCount > 0 ? 'text-danger' : 'text-gray-400'">
          {{ offlineCount }}
        </p>
      </div>

      <div class="cyber-card p-4">
        <div class="flex items-center justify-between text-muted mb-1">
          <span class="text-xs font-mono uppercase tracking-wider">Avg Latency</span>
          <Clock class="w-4 h-4 text-cyan" />
        </div>
        <p class="text-2xl font-bold font-mono text-cyan">{{ avgLatency }}</p>
      </div>
    </div>

    <!-- Filters & Search Toolbar -->
    <div class="flex flex-col sm:flex-row items-center justify-between gap-3">
      <!-- Search Input -->
      <div class="relative w-full sm:w-80">
        <Search class="w-4 h-4 absolute left-3 top-1/2 -translate-y-1/2 text-muted" />
        <input
          v-model="searchQuery"
          type="text"
          placeholder="Filter by hostname or service..."
          class="cyber-input w-full pl-9 py-1.5 text-xs font-mono"
        />
      </div>

      <!-- Status Filter Tabs -->
      <div class="flex items-center gap-1.5 p-1 bg-dark-bg/60 border border-dark-border rounded w-full sm:w-auto justify-center">
        <button
          @click="filterStatus = 'all'"
          class="px-3 py-1 text-xs font-mono rounded transition-colors"
          :class="filterStatus === 'all' ? 'bg-neon/15 text-neon font-medium' : 'text-muted hover:text-gray-200'"
        >
          All ({{ results.length }})
        </button>
        <button
          @click="filterStatus = 'up'"
          class="px-3 py-1 text-xs font-mono rounded transition-colors"
          :class="filterStatus === 'up' ? 'bg-neon/15 text-neon font-medium' : 'text-muted hover:text-gray-200'"
        >
          Online ({{ onlineCount }})
        </button>
        <button
          @click="filterStatus = 'down'"
          class="px-3 py-1 text-xs font-mono rounded transition-colors"
          :class="filterStatus === 'down' ? 'bg-danger/15 text-danger font-medium' : 'text-muted hover:text-gray-200'"
        >
          Down ({{ offlineCount }})
        </button>
      </div>
    </div>

    <!-- Services List -->
    <div class="space-y-3">
      <div
        v-for="r in filteredResults"
        :key="r.hostname"
        class="cyber-card p-4 transition-all hover:border-dark-border/80"
        :class="r.status === 'up' ? 'hover:shadow-[0_0_15px_rgba(34,197,94,0.06)]' : 'border-danger/30'"
      >
        <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
          <!-- Hostname & Service Target -->
          <div class="space-y-1">
            <div class="flex items-center gap-2">
              <span
                class="w-2.5 h-2.5 rounded-full inline-block"
                :class="r.status === 'up' ? 'bg-neon shadow-[0_0_6px_rgba(34,197,94,0.6)] animate-pulse' : 'bg-danger shadow-[0_0_6px_rgba(239,68,68,0.6)]'"
              ></span>
              <a
                :href="'https://' + r.hostname"
                target="_blank"
                rel="noopener"
                class="text-sm font-semibold text-gray-100 font-mono hover:text-neon transition-colors flex items-center gap-1.5"
              >
                {{ r.hostname }}
                <ExternalLink class="w-3.5 h-3.5 text-muted inline" />
              </a>
            </div>

            <div class="flex flex-wrap items-center gap-3 text-xs text-muted font-mono">
              <span>Target: <strong class="text-gray-300">{{ r.service }}</strong></span>
              <span v-if="getContainerName(r.service)">• Container: <strong class="text-cyan">{{ getContainerName(r.service) }}</strong></span>
            </div>
          </div>

          <!-- Status, Latency & Interactive Actions -->
          <div class="flex items-center flex-wrap gap-2.5 justify-end">
            <!-- Latency Badge -->
            <span
              class="text-xs font-mono px-2 py-0.5 rounded border"
              :class="
                r.status === 'up' && (r.latency_ms < 100 || r.latency_ms === 0)
                  ? 'border-neon/30 text-neon bg-neon/5'
                  : r.status === 'up' && r.latency_ms < 300
                  ? 'border-yellow-500/30 text-yellow-400 bg-yellow-500/5'
                  : 'border-danger/30 text-danger bg-danger/5'
              "
            >
              {{ r.latency }}
            </span>

            <!-- Status Badge -->
            <span
              class="cyber-badge uppercase text-[11px] px-2.5 py-0.5 font-bold tracking-wider"
              :class="r.status === 'up' ? 'border-neon text-neon bg-neon/10' : 'border-danger text-danger bg-danger/10'"
            >
              {{ r.status }}
            </span>

            <!-- Probe button -->
            <button
              @click="probeSingle(r)"
              :disabled="r.probing"
              class="p-1.5 text-muted hover:text-neon rounded hover:bg-dark-bg/80 transition-colors"
              title="Probe origin now"
            >
              <PlayCircle class="w-4 h-4" :class="r.probing && 'animate-spin text-neon'" />
            </button>

            <!-- Jump to Container View -->
            <RouterLink
              v-if="getContainerName(r.service)"
              :to="{ path: '/containers', query: { search: getContainerName(r.service) } }"
              class="p-1.5 text-muted hover:text-cyan rounded hover:bg-dark-bg/80 transition-colors"
              title="Inspect Container"
            >
              <Box class="w-4 h-4" />
            </RouterLink>

            <!-- Jump to Config Ingress View -->
            <RouterLink
              to="/access?tab=cloudflare"
              class="p-1.5 text-muted hover:text-neon rounded hover:bg-dark-bg/80 transition-colors"
              title="Edit Ingress Route"
            >
              <Sliders class="w-4 h-4" />
            </RouterLink>
          </div>
        </div>
      </div>

      <!-- Empty State -->
      <div v-if="!filteredResults.length && !loading" class="cyber-card text-center text-muted py-12">
        <Activity class="w-8 h-8 mx-auto text-muted mb-2 opacity-50" />
        <p class="font-mono text-sm tracking-wider">// NO ENDPOINTS FOUND</p>
        <p class="text-xs text-muted mt-1">No services matched your search or no public ingress rules are defined.</p>
      </div>
    </div>
  </div>
</template>
