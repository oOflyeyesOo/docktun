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
  AlertCircle,
  Copy,
  Check,
  RefreshCw,
  Zap,
  Radio,
  Sparkles,
  Search,
  Shield,
  Filter,
  ArrowRight,
  Box
} from 'lucide-vue-next'
import { useApi } from '../composables/useApi'

const { apiFetch } = useApi()

// Primary State
const status = ref<any>(null)
const projects = ref<any[]>([])
const containers = ref<any[]>([])
const healthList = ref<any[]>([])
const remoteAccessOverview = ref<any>(null)
const recentAudits = ref<any[]>([])

// Loading / Action State
const actionLoading = ref('')
const containerActionLoading = ref<Record<string, string>>({})
const refreshing = ref(false)
const dnsSyncing = ref(false)
const copiedHost = ref('')
const probingHealth = ref(false)

// UI Filter State
const searchQuery = ref('')
const containerFilter = ref<'all' | 'running' | 'stopped' | 'updates'>('all')
const routesTab = ref<'public' | 'local'>('public')

// Fetch Telemetry Data
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

async function fetchHealth() {
  probingHealth.value = true
  try {
    healthList.value = await apiFetch('/api/health')
  } catch (e) {
    // optional
  } finally {
    probingHealth.value = false
  }
}

async function fetchRemoteAccess() {
  try {
    remoteAccessOverview.value = await apiFetch('/api/remote-access/overview')
  } catch {}
}

async function fetchRecentAudits() {
  try {
    const res = await apiFetch<{ entries: any[] }>('/api/audit?limit=5')
    recentAudits.value = res.entries || []
  } catch {}
}

async function refreshAll() {
  refreshing.value = true
  await Promise.all([
    fetchStatus(),
    fetchProjects(),
    fetchContainers(),
    fetchHealth(),
    fetchRemoteAccess(),
    fetchRecentAudits(),
  ])
  setTimeout(() => (refreshing.value = false), 400)
}

// Tunnel Control Actions
async function controlTunnel(action: string) {
  if (!confirm(`Are you sure you want to ${action} the Cloudflare tunnel daemon?`)) return
  actionLoading.value = action
  try {
    await apiFetch(`/api/control/${action}`, { method: 'POST' })
    await new Promise((r) => setTimeout(r, 1200))
    await fetchStatus()
    await fetchRemoteAccess()
  } catch (e: any) {
    alert(e.message)
  } finally {
    actionLoading.value = ''
  }
}

async function triggerDnsSync() {
  dnsSyncing.value = true
  try {
    const res = await apiFetch('/api/cloudflare/sync-all', { method: 'POST' })
    alert(`Cloudflare DNS Synced! ${res.synced?.length || 0} routes verified in Cloudflare DNS.`)
  } catch (e: any) {
    alert(e.message || 'DNS sync failed. Check API Token in Remote Access.')
  } finally {
    dnsSyncing.value = false
  }
}

function copyUrl(text: string) {
  navigator.clipboard.writeText(text)
  copiedHost.value = text
  setTimeout(() => (copiedHost.value = ''), 2000)
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

// Helper Parsers
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

function formatTimeAgo(dateStr: string): string {
  if (!dateStr) return ''
  try {
    const d = new Date(dateStr)
    const diff = Math.floor((Date.now() - d.getTime()) / 1000)
    if (diff < 60) return 'just now'
    if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
    if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
    return `${Math.floor(diff / 86400)}d ago`
  } catch {
    return dateStr
  }
}

function parseContainerPorts(portsStr?: string): { hostPort: string; targetPort: string } {
  if (!portsStr) return { hostPort: '', targetPort: '80' }
  const hostMatch = portsStr.match(/:(\d+)->(\d+)\/tcp/)
  if (hostMatch) {
    return { hostPort: hostMatch[1], targetPort: hostMatch[2] }
  }
  const simpleHostMatch = portsStr.match(/:(\d+)->/)
  if (simpleHostMatch) {
    return { hostPort: simpleHostMatch[1], targetPort: simpleHostMatch[1] }
  }
  const plainMatch = portsStr.match(/(\d+)\/tcp/)
  if (plainMatch) {
    return { hostPort: '', targetPort: plainMatch[1] }
  }
  return { hostPort: '', targetPort: '80' }
}

function resolveContainerUrls(c: any) {
  const { hostPort, targetPort } = parseContainerPorts(c.ports)
  const host = typeof window !== 'undefined' && window.location.hostname ? window.location.hostname : 'localhost'

  const publicUrl = c.hostname ? `https://${c.hostname}` : null
  const directUrl = hostPort ? `http://${host}:${hostPort}` : null
  const effectivePort = targetPort || hostPort || '80'
  const localUrl = `http://${c.name}.local${effectivePort !== '80' ? ':' + effectivePort : ''}`

  // Best open URL: prefer public HTTPS if configured -> then direct host port if published -> fallback to local mDNS
  const bestUrl = publicUrl || directUrl || localUrl

  return {
    publicUrl,
    directUrl,
    localUrl,
    bestUrl,
    hostPort,
    targetPort: effectivePort,
    isWebExposed: !!(publicUrl || directUrl),
  }
}

function extractContainerFromService(service: string): string {
  if (!service) return ''
  try {
    let s = service.trim()
    if (!s.includes('://')) {
      s = 'http://' + s
    }
    const u = new URL(s)
    return u.hostname
  } catch {
    return service.replace(/^https?:\/\//, '').split(':')[0]
  }
}

// Computed Statistics
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
    updatesCount: containers.value.filter((c) => c.update_available).length,
    cpuPercent: totalCpu.toFixed(1) + '%',
    cpuNum: Math.min(Math.round(totalCpu), 100),
    totalMemStr: formatBytes(totalMemBytes),
    totalLimitStr: totalLimitBytes > 0 ? formatBytes(totalLimitBytes) : 'Host Limit',
    memPercent: totalLimitBytes > 0 ? Math.min(Math.round((totalMemBytes / totalLimitBytes) * 100), 100) : 0,
  }
})

// Stacks Filter State
const stackFilter = ref<string>('all')

const availableStacks = computed(() => {
  const set = new Set<string>()
  for (const c of containers.value) {
    if (c.project_name) set.add(c.project_name)
  }
  for (const p of projects.value) {
    if (p.name) set.add(p.name)
  }
  return Array.from(set).sort()
})

// Origin Health lookup mapped by Hostname
const healthByHostname = computed(() => {
  const map: Record<string, { service: string; status: string; latency: string }> = {}
  for (const h of healthList.value) {
    if (h.hostname) {
      map[h.hostname] = h
    }
  }
  return map
})

// Unified Public Routes Combining Cloudflare Configured Hostnames & Health Probe Latencies
const publicRoutes = computed(() => {
  const hostSet = new Set<string>()
  if (status.value?.hostnames && Array.isArray(status.value.hostnames)) {
    status.value.hostnames.forEach((h: string) => {
      if (h) hostSet.add(h)
    })
  }
  healthList.value.forEach((h: any) => {
    if (h.hostname) hostSet.add(h.hostname)
  })

  return Array.from(hostSet).map((hostname) => {
    const health = healthByHostname.value[hostname]
    return {
      hostname,
      service: health?.service || '',
      status: health?.status || (isTunnelRunning.value ? 'up' : 'unknown'),
      latency: health?.latency || '—',
      container: health?.service ? extractContainerFromService(health.service) : null,
    }
  })
})

// Filtered Containers with Stack, State, and Search Query
const filteredContainers = computed(() => {
  return containers.value.filter((c) => {
    if (containerFilter.value === 'running' && !c.running) return false
    if (containerFilter.value === 'stopped' && c.running) return false
    if (containerFilter.value === 'updates' && !c.update_available) return false
    if (stackFilter.value !== 'all') {
      if (stackFilter.value === '__standalone__' && c.project_name) return false
      if (stackFilter.value !== '__standalone__' && c.project_name !== stackFilter.value) return false
    }
    if (!searchQuery.value) return true
    const q = searchQuery.value.toLowerCase()
    return (
      c.name.toLowerCase().includes(q) ||
      (c.image && c.image.toLowerCase().includes(q)) ||
      (c.hostname && c.hostname.toLowerCase().includes(q)) ||
      (c.project_name && c.project_name.toLowerCase().includes(q))
    )
  })
})

// Local & Network accessible container services
const localServices = computed(() => {
  return containers.value.filter((c) => c.running).map((c) => {
    const urls = resolveContainerUrls(c)
    return {
      id: c.id,
      name: c.name,
      image: c.image,
      status: c.status,
      uptime: c.uptime,
      exposureMode: c.exposure_mode,
      hostname: c.hostname,
      ...urls,
    }
  })
})

const isTunnelRunning = computed(() => {
  return status.value?.status === 'running' || status.value?.status === 'healthy'
})

let timer: any = null

onMounted(async () => {
  await refreshAll()
  timer = setInterval(() => {
    if (document.visibilityState === 'visible') {
      fetchStatus()
      fetchContainers()
      fetchHealth()
      fetchRemoteAccess()
    }
  }, 5000)
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <div class="space-y-6">
    <!-- Header Section -->
    <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between border-b border-slate-800/80 pb-4">
      <div>
        <div class="flex items-center gap-2">
          <p class="text-[11px] font-semibold uppercase tracking-wider text-indigo-400">System Telemetry & Status</p>
          <span class="rounded bg-slate-800 px-1.5 py-0.2 text-[10px] font-mono text-slate-300">DockTun Core</span>
        </div>
        <h1 class="text-2xl font-bold tracking-tight text-white flex items-center gap-3 mt-1">
          <span>Infrastructure Overview</span>
          <span
            class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-semibold"
            :class="isTunnelRunning ? 'bg-emerald-500/10 text-emerald-400 border border-emerald-500/30' : 'bg-rose-500/10 text-rose-400 border border-rose-500/30'"
          >
            <span class="h-1.5 w-1.5 rounded-full" :class="isTunnelRunning ? 'bg-emerald-400 animate-pulse' : 'bg-rose-400'"></span>
            <span>{{ isTunnelRunning ? 'Tunnel Active' : 'Tunnel Degraded' }}</span>
          </span>
        </h1>
      </div>

      <div class="flex items-center gap-2 flex-wrap">
        <button
          @click="triggerDnsSync"
          :disabled="dnsSyncing"
          class="cyber-btn-secondary text-xs flex items-center gap-1.5 py-2 px-3 text-slate-200"
          title="Sync all ingress rules directly to Cloudflare DNS CNAME records"
        >
          <RefreshCw class="h-3.5 w-3.5" :class="dnsSyncing ? 'animate-spin' : ''" />
          <span>{{ dnsSyncing ? 'Syncing…' : 'Sync DNS' }}</span>
        </button>

        <button
          @click="refreshAll"
          :disabled="refreshing"
          class="cyber-btn-secondary text-xs flex items-center gap-1.5 py-2 px-3"
          title="Refresh all metrics"
        >
          <RotateCcw class="h-3.5 w-3.5" :class="refreshing ? 'animate-spin' : ''" />
          <span>Refresh</span>
        </button>

        <button
          @click="controlTunnel('restart')"
          :disabled="!!actionLoading"
          class="cyber-btn text-xs flex items-center gap-1.5 py-2 px-3 bg-indigo-600 hover:bg-indigo-500"
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
      class="rounded-xl border border-rose-500/30 bg-rose-500/10 p-4 text-xs text-rose-200 flex flex-col sm:flex-row sm:items-start justify-between gap-3 shadow-lg"
    >
      <div class="flex items-start gap-3">
        <AlertCircle class="h-5 w-5 text-rose-400 mt-0.5 shrink-0" />
        <div class="space-y-2">
          <p class="font-bold text-rose-300 text-sm">
            Cloudflare Tunnel is {{ status?.status || 'degraded' }}
          </p>
          <p v-if="status?.last_error" class="font-mono text-[11px] text-rose-200/90 bg-slate-950/80 p-2.5 rounded-lg border border-rose-900/50 whitespace-pre-wrap">
            {{ status.last_error }}
          </p>
          <div class="flex items-center gap-3 pt-1 text-[11px]">
            <router-link to="/access?tab=cloudflare" class="text-indigo-400 hover:underline font-semibold flex items-center gap-1">
              <span>Inspect Tunnel Ingress Config &rarr;</span>
            </router-link>
            <router-link to="/logs" class="text-slate-300 hover:underline font-semibold flex items-center gap-1">
              <span>View Tunnel Logs &rarr;</span>
            </router-link>
          </div>
        </div>
      </div>
      <button
        @click="controlTunnel('restart')"
        :disabled="!!actionLoading"
        class="cyber-btn-danger text-xs flex items-center gap-1.5 py-1.5 px-3 shrink-0 self-start sm:self-auto"
      >
        <RotateCcw class="h-3 w-3" :class="actionLoading === 'restart' ? 'animate-spin' : ''" />
        <span>Restart Tunnel</span>
      </button>
    </div>

    <!-- 4 High-Resolution Telemetry Metric Cards -->
    <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
      <!-- 1. Cloudflare Tunnel Card -->
      <div class="cyber-card flex flex-col justify-between">
        <div>
          <div class="flex items-center justify-between text-slate-400">
            <span class="text-[11px] font-semibold uppercase tracking-wider">Cloudflare Tunnel</span>
            <div class="p-1 rounded-md bg-orange-500/10 text-orange-400">
              <Globe class="h-3.5 w-3.5" />
            </div>
          </div>
          <div class="mt-2.5 flex items-center gap-2">
            <span class="relative flex h-2.5 w-2.5">
              <span
                v-if="isTunnelRunning"
                class="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75"
              ></span>
              <span
                class="relative inline-flex h-2.5 w-2.5 rounded-full"
                :class="isTunnelRunning ? 'bg-emerald-500' : 'bg-rose-500'"
              ></span>
            </span>
            <span class="text-base font-bold text-white truncate" :title="status?.tunnel_name || 'Active'">
              {{ status?.tunnel_name || 'docktun' }}
            </span>
          </div>

          <!-- Live Edge Colos -->
          <div class="mt-2 flex flex-wrap items-center gap-1.5">
            <span
              v-for="colo in (status?.colo_locations || [])"
              :key="colo"
              class="inline-flex items-center gap-1 rounded bg-slate-800 border border-slate-700 px-1.5 py-0.5 text-[10px] font-mono text-cyan-300 font-semibold"
              :title="`Edge Datacenter: ${colo}`"
            >
              <Radio class="h-2.5 w-2.5 text-emerald-400 animate-pulse" />
              <span>{{ colo }}</span>
            </span>
            <span v-if="!status?.colo_locations?.length && isTunnelRunning" class="text-[11px] text-slate-500 font-mono">
              Edge Active
            </span>
          </div>
        </div>

        <div class="mt-3 border-t border-slate-800/80 pt-2 flex items-center justify-between text-xs text-slate-400">
          <span class="truncate">Uptime: {{ status?.uptime || '—' }}</span>
          <span class="rounded bg-slate-800 px-1.5 py-0.5 text-[10px] text-indigo-300 font-mono font-medium">
            {{ status?.ha_connections ? `${status.ha_connections} HA Links` : (status?.protocol || 'HTTP/2') }}
          </span>
        </div>
      </div>

      <!-- 2. Traffic & Stream Activity Card -->
      <div class="cyber-card flex flex-col justify-between">
        <div>
          <div class="flex items-center justify-between text-slate-400">
            <span class="text-[11px] font-semibold uppercase tracking-wider">Tunnel Traffic</span>
            <div class="p-1 rounded-md bg-emerald-500/10 text-emerald-400">
              <Activity class="h-3.5 w-3.5" />
            </div>
          </div>
          <div class="mt-2.5 flex items-baseline gap-2">
            <span class="text-2xl font-bold font-mono text-white">
              {{ status?.total_requests?.toLocaleString() || '0' }}
            </span>
            <span class="text-xs text-slate-400">total requests</span>
          </div>
        </div>

        <div class="mt-3 border-t border-slate-800/80 pt-2 flex items-center justify-between text-xs text-slate-400">
          <span class="text-slate-400">Active edge streams:</span>
          <span class="font-mono text-emerald-400 font-bold">
            {{ status?.active_connections || 0 }} live
          </span>
        </div>
      </div>

      <!-- 3. Container Fleet Card -->
      <div class="cyber-card flex flex-col justify-between">
        <div>
          <div class="flex items-center justify-between text-slate-400">
            <span class="text-[11px] font-semibold uppercase tracking-wider">Docker Fleet</span>
            <div class="p-1 rounded-md bg-indigo-500/10 text-indigo-400">
              <Server class="h-3.5 w-3.5" />
            </div>
          </div>
          <div class="mt-2.5 flex items-baseline gap-2">
            <span class="text-2xl font-bold text-white font-mono">{{ containerStats.running }}</span>
            <span class="text-xs text-slate-400">/ {{ containerStats.total }} running</span>
          </div>
        </div>

        <div class="mt-3 border-t border-slate-800/80 pt-2 flex items-center justify-between text-xs text-slate-400">
          <div class="flex items-center gap-2">
            <span :class="containerStats.stopped > 0 ? 'text-amber-400 font-medium' : 'text-slate-500'">
              {{ containerStats.stopped }} stopped
            </span>
            <router-link
              v-if="containerStats.updatesCount > 0"
              to="/containers"
              class="inline-flex items-center gap-1 rounded bg-amber-500/20 border border-amber-500/40 px-1.5 py-0.2 text-[10px] font-bold text-amber-300 animate-pulse hover:bg-amber-500/30"
              title="Click to view containers with updates available"
            >
              <Sparkles class="h-2.5 w-2.5" />
              {{ containerStats.updatesCount }} update{{ containerStats.updatesCount > 1 ? 's' : '' }}
            </router-link>
          </div>
          <router-link to="/containers" class="text-indigo-400 hover:underline">Manage &rarr;</router-link>
        </div>
      </div>

      <!-- 4. Combined Resource Load Card -->
      <div class="cyber-card flex flex-col justify-between">
        <div>
          <div class="flex items-center justify-between text-slate-400">
            <span class="text-[11px] font-semibold uppercase tracking-wider">Resource Load</span>
            <div class="p-1 rounded-md bg-purple-500/10 text-purple-400">
              <Cpu class="h-3.5 w-3.5" />
            </div>
          </div>
          <div class="mt-2.5 flex items-center justify-between">
            <div class="flex items-baseline gap-1.5">
              <span class="text-xl font-bold text-white font-mono">{{ containerStats.cpuPercent }}</span>
              <span class="text-[11px] text-slate-400">CPU</span>
            </div>
            <div class="text-right font-mono text-xs">
              <span class="text-emerald-400 font-bold">{{ containerStats.totalMemStr }}</span>
              <span class="text-[10px] text-slate-500 block">RAM</span>
            </div>
          </div>
        </div>

        <!-- Progress Bar -->
        <div class="mt-3 border-t border-slate-800/80 pt-2 space-y-1.5">
          <div class="flex items-center justify-between text-[10px] text-slate-400">
            <span>Container Stack CPU</span>
            <span class="font-mono">{{ containerStats.cpuPercent }}</span>
          </div>
          <div class="h-1.5 w-full overflow-hidden rounded-full bg-slate-800">
            <div
              class="h-full bg-indigo-500 transition-all duration-300 rounded-full"
              :style="{ width: `${containerStats.cpuNum}%` }"
            ></div>
          </div>
        </div>
      </div>
    </div>

    <!-- Remote Access Multi-Pillar Connectivity Summary Banner -->
    <div class="cyber-card p-4 bg-slate-900/40 border-slate-800/90">
      <div class="flex flex-col md:flex-row md:items-center justify-between gap-4">
        <div class="flex items-center gap-3">
          <div class="p-2 rounded-xl bg-indigo-500/10 text-indigo-400 border border-indigo-500/20">
            <Globe class="h-5 w-5" />
          </div>
          <div>
            <h3 class="text-xs font-semibold text-white">Remote Access & Mesh Connectivity</h3>
            <p class="text-[11px] text-slate-400">Unified secure ingress across Cloudflare edge tunnels, Tailscale zero-trust, and WireGuard VPN</p>
          </div>
        </div>

        <div class="flex items-center gap-3 flex-wrap">
          <!-- Cloudflare Badge -->
          <div class="flex items-center gap-1.5 px-2.5 py-1 rounded-lg bg-slate-950 border border-slate-800 text-xs font-mono">
            <span class="h-2 w-2 rounded-full" :class="isTunnelRunning ? 'bg-emerald-400' : 'bg-rose-400'"></span>
            <span class="text-slate-400">Cloudflare:</span>
            <span class="text-slate-200 font-semibold">{{ status?.hostnames?.length || 0 }} routes</span>
          </div>

          <!-- Tailscale Badge -->
          <div class="flex items-center gap-1.5 px-2.5 py-1 rounded-lg bg-slate-950 border border-slate-800 text-xs font-mono">
            <span
              class="h-2 w-2 rounded-full"
              :class="remoteAccessOverview?.tailscale?.backend_state === 'Running' ? 'bg-emerald-400' : 'bg-slate-500'"
            ></span>
            <span class="text-slate-400">Tailscale:</span>
            <span class="text-purple-300 font-semibold">
              {{ remoteAccessOverview?.tailscale?.backend_state === 'Running' ? 'Connected' : 'Stopped' }}
            </span>
          </div>

          <!-- WireGuard Badge -->
          <div class="flex items-center gap-1.5 px-2.5 py-1 rounded-lg bg-slate-950 border border-slate-800 text-xs font-mono">
            <span
              class="h-2 w-2 rounded-full"
              :class="remoteAccessOverview?.wireguard?.running ? 'bg-emerald-400' : 'bg-slate-500'"
            ></span>
            <span class="text-slate-400">WireGuard:</span>
            <span class="text-emerald-300 font-semibold">
              {{ remoteAccessOverview?.wireguard?.running ? `${remoteAccessOverview.wireguard.peers_count} peers` : 'Inactive' }}
            </span>
          </div>

          <router-link
            to="/access"
            class="text-xs font-medium text-indigo-400 hover:text-indigo-300 hover:underline flex items-center gap-1"
          >
            <span>Control &rarr;</span>
          </router-link>
        </div>
      </div>
    </div>

    <!-- Combined Ingress Routing & Origin Health Directory -->
    <div class="cyber-card space-y-4">
      <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-800/80 pb-3">
        <div class="flex items-center gap-2">
          <Layers class="h-4 w-4 text-indigo-400" />
          <h2 class="text-xs font-semibold text-white">
            Ingress Routing & Origin Health
          </h2>
        </div>

        <div class="flex items-center gap-2 flex-wrap">
          <!-- Route Switcher Tabs -->
          <div class="flex items-center gap-1 rounded-lg border border-slate-800 bg-slate-950 p-1 text-xs">
            <button
              @click="routesTab = 'public'"
              class="flex items-center gap-1.5 rounded-md px-3 py-1 font-medium transition"
              :class="routesTab === 'public' ? 'bg-slate-800 text-white shadow-sm' : 'text-slate-400 hover:text-slate-200'"
            >
              <Globe class="h-3.5 w-3.5 text-orange-400" />
              <span>Public Hostnames (Cloudflare)</span>
              <span class="rounded bg-slate-900 px-1.5 py-0.2 text-[10px] text-orange-300 font-mono">{{ publicRoutes.length }}</span>
            </button>

            <button
              @click="routesTab = 'local'"
              class="flex items-center gap-1.5 rounded-md px-3 py-1 font-medium transition"
              :class="routesTab === 'local' ? 'bg-slate-800 text-white shadow-sm' : 'text-slate-400 hover:text-slate-200'"
            >
              <Wifi class="h-3.5 w-3.5 text-indigo-400" />
              <span>Local Network (.local)</span>
              <span class="rounded bg-slate-900 px-1.5 py-0.2 text-[10px] text-indigo-300 font-mono">{{ localServices.length }}</span>
            </button>
          </div>

          <!-- Actions -->
          <button
            @click="fetchHealth"
            :disabled="probingHealth"
            class="cyber-btn-secondary text-xs flex items-center gap-1 py-1 px-2.5 text-slate-300"
            title="Probe live latency of ingress origins"
          >
            <RotateCcw class="h-3 w-3" :class="probingHealth ? 'animate-spin' : ''" />
            <span>{{ probingHealth ? 'Probing…' : 'Probe Latency' }}</span>
          </button>
          <router-link
            to="/access?tab=cloudflare"
            class="cyber-btn-secondary text-xs flex items-center gap-1 py-1 px-2.5 text-indigo-300 hover:text-white"
          >
            <span>Manage Rules</span>
            <ArrowRight class="h-3 w-3" />
          </router-link>
        </div>
      </div>

      <!-- Tab 1: Public Cloudflare Hostnames with Origin Latency & Target Service -->
      <div v-if="routesTab === 'public'" class="space-y-3">
        <div v-if="publicRoutes.length === 0" class="py-6 text-center text-xs text-slate-500 font-mono">
          No public ingress routes configured. Add routes in Remote Access &rarr; Cloudflare.
        </div>
        <div v-else class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
          <div
            v-for="r in publicRoutes"
            :key="r.hostname"
            class="flex flex-col justify-between rounded-xl border border-slate-800/90 bg-slate-950 p-3.5 hover:border-slate-700 transition group shadow-sm"
          >
            <div>
              <div class="flex items-center justify-between gap-2 mb-1.5">
                <div class="flex items-center gap-2 truncate">
                  <ShieldCheck class="h-4 w-4 text-emerald-400 shrink-0" />
                  <a
                    :href="`https://${r.hostname}`"
                    target="_blank"
                    rel="noopener"
                    class="font-mono text-xs font-bold text-white hover:text-indigo-300 truncate hover:underline"
                    :title="`Open https://${r.hostname}`"
                  >
                    {{ r.hostname }}
                  </a>
                </div>

                <!-- Latency Badge -->
                <span
                  class="rounded-md px-2 py-0.5 text-[10px] font-mono font-semibold shrink-0"
                  :class="r.status === 'up' ? 'bg-emerald-500/10 text-emerald-400 border border-emerald-500/30' : (r.status === 'down' ? 'bg-rose-500/10 text-rose-400 border border-rose-500/30' : 'bg-slate-800 text-slate-400 border border-slate-700')"
                >
                  <span v-if="r.status === 'up'" class="inline-block h-1.5 w-1.5 rounded-full bg-emerald-400 mr-1 animate-pulse"></span>
                  {{ r.status === 'up' ? r.latency : (r.status === 'down' ? 'TIMEOUT' : 'Edge Active') }}
                </span>
              </div>

              <!-- Target Origin Container Link -->
              <div class="flex items-center gap-1.5 text-[11px] text-slate-400 font-mono truncate">
                <span class="text-slate-500">&rarr;</span>
                <span v-if="!r.service" class="text-slate-500">Cloudflare Edge Ingress</span>
                <router-link
                  v-else-if="r.container"
                  :to="'/containers?name=' + encodeURIComponent(r.container)"
                  class="text-indigo-400 hover:text-indigo-300 hover:underline truncate"
                  :title="`Inspect container '${r.container}'`"
                >
                  {{ r.service }}
                </router-link>
                <span v-else class="text-slate-400 truncate">{{ r.service }}</span>
              </div>
            </div>

            <!-- Actions Bar -->
            <div class="flex items-center justify-between pt-2.5 mt-2.5 border-t border-slate-800/60 text-xs">
              <span class="text-[10px] font-mono text-slate-500 uppercase">HTTPS &bull; Cloudflare Edge</span>
              <div class="flex items-center gap-1">
                <button
                  @click="copyUrl(`https://${r.hostname}`)"
                  class="p-1.5 rounded-lg text-slate-400 hover:bg-slate-800 hover:text-slate-200 transition"
                  :title="copiedHost === `https://${r.hostname}` ? 'Copied!' : 'Copy URL'"
                >
                  <Check v-if="copiedHost === `https://${r.hostname}`" class="h-3.5 w-3.5 text-emerald-400" />
                  <Copy v-else class="h-3.5 w-3.5" />
                </button>
                <a
                  :href="`https://${r.hostname}`"
                  target="_blank"
                  rel="noopener"
                  class="p-1.5 rounded-lg text-indigo-400 hover:bg-indigo-500/10 hover:text-indigo-300 transition"
                  title="Open URL in new tab"
                >
                  <ExternalLink class="h-3.5 w-3.5" />
                </a>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Tab 2: Local & Network Services (.local / Host Port) -->
      <div v-else class="space-y-3">
        <div v-if="localServices.length === 0" class="py-6 text-center text-xs text-slate-500 font-mono">
          No active running containers found.
        </div>
        <div v-else class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          <div
            v-for="svc in localServices"
            :key="svc.id"
            class="flex items-center justify-between rounded-xl border border-slate-800 bg-slate-950 p-3 hover:border-slate-700 transition group"
          >
            <div class="truncate pr-2">
              <div class="flex items-center gap-1.5">
                <span class="h-2 w-2 rounded-full bg-emerald-400 shrink-0"></span>
                <router-link
                  :to="'/containers?id=' + encodeURIComponent(svc.id)"
                  class="font-mono text-xs font-semibold text-slate-200 hover:text-white truncate hover:underline"
                  :title="`Inspect container '${svc.name}' in Control Center`"
                >
                  {{ svc.name }}
                </router-link>
                <span
                  v-if="svc.hostPort"
                  class="px-1.5 py-0.2 rounded bg-indigo-500/10 border border-indigo-500/30 text-[10px] font-mono text-indigo-300 font-semibold shrink-0"
                  :title="`Published host port: ${svc.hostPort}`"
                >
                  :{{ svc.hostPort }}
                </span>
              </div>
              <div class="font-mono text-[11px] text-indigo-300 truncate mt-0.5" :title="svc.bestUrl">
                {{ svc.directUrl || svc.publicUrl || svc.localUrl }}
              </div>
            </div>

            <div class="flex items-center gap-1 shrink-0">
              <button
                @click="copyUrl(svc.bestUrl)"
                class="p-1.5 rounded-lg text-slate-400 hover:bg-slate-800 hover:text-slate-200 transition"
                :title="copiedHost === svc.bestUrl ? 'Copied!' : 'Copy URL'"
              >
                <Check v-if="copiedHost === svc.bestUrl" class="h-3.5 w-3.5 text-emerald-400" />
                <Copy v-else class="h-3.5 w-3.5" />
              </button>
              <a
                :href="svc.bestUrl"
                target="_blank"
                rel="noopener"
                class="cyber-btn-secondary text-[11px] flex items-center gap-1 py-1 px-2.5 text-indigo-300 hover:text-white"
                :title="`Open ${svc.bestUrl} in new tab`"
              >
                <span>Open</span>
                <ExternalLink class="h-3 w-3" />
              </a>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Combined Container Fleet & Managed Compose Stacks (Full-Width Unified Section) -->
    <div class="cyber-card space-y-4">
      <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-800/80 pb-3">
        <div class="flex items-center gap-2 flex-wrap">
          <Server class="h-4 w-4 text-emerald-400" />
          <h2 class="text-xs font-semibold text-white">
            Container Fleet & Managed Stacks
          </h2>
          <span class="text-[10px] px-2 py-0.5 rounded-full bg-slate-800 text-slate-300 font-mono">
            {{ filteredContainers.length }} / {{ containers.length }} containers
          </span>
          <span v-if="projects.length > 0" class="text-[10px] px-2 py-0.5 rounded-full bg-purple-500/10 text-purple-300 border border-purple-500/30 font-mono">
            {{ projects.length }} stacks
          </span>
        </div>

        <!-- Search & Filters -->
        <div class="flex items-center gap-2 flex-wrap">
          <div class="relative w-44 sm:w-52">
            <Search class="h-3.5 w-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-slate-500" />
            <input
              v-model="searchQuery"
              type="text"
              placeholder="Search containers or stacks…"
              class="w-full pl-8 pr-2.5 py-1 text-xs bg-slate-950 border border-slate-800 rounded-lg text-slate-200 placeholder:text-slate-500 focus:outline-none focus:border-indigo-500 font-mono"
            />
          </div>

          <!-- Stack Filter -->
          <select
            v-if="availableStacks.length > 0"
            v-model="stackFilter"
            class="text-xs bg-slate-950 border border-slate-800 rounded-lg px-2 py-1 text-slate-300 focus:outline-none focus:border-indigo-500 font-mono"
          >
            <option value="all">All Stacks</option>
            <option v-for="stk in availableStacks" :key="stk" :value="stk">Stack: {{ stk }}</option>
            <option value="__standalone__">Standalone</option>
          </select>

          <!-- State Filter -->
          <select
            v-model="containerFilter"
            class="text-xs bg-slate-950 border border-slate-800 rounded-lg px-2 py-1 text-slate-300 focus:outline-none focus:border-indigo-500 font-mono"
          >
            <option value="all">All States</option>
            <option value="running">Running</option>
            <option value="stopped">Stopped</option>
            <option value="updates">Updates</option>
          </select>
        </div>
      </div>

      <!-- Compose Stacks Quick Jump Bar (if any projects registered) -->
      <div v-if="projects.length > 0" class="flex items-center gap-2 overflow-x-auto pb-1 text-xs">
        <span class="text-slate-500 font-mono text-[11px] shrink-0">Stacks:</span>
        <button
          v-for="p in projects"
          :key="p.id"
          @click="stackFilter = (stackFilter === p.name ? 'all' : p.name)"
          class="flex items-center gap-1.5 px-2.5 py-1 rounded-lg border text-xs font-mono transition shrink-0"
          :class="stackFilter === p.name ? 'bg-purple-950/60 border-purple-500/50 text-purple-200' : 'bg-slate-950 border-slate-800 text-slate-300 hover:border-slate-700'"
        >
          <FolderOpen class="h-3 w-3 text-purple-400" />
          <span class="font-semibold">{{ p.name }}</span>
          <router-link
            :to="'/containers?project=' + p.id"
            @click.stop
            class="text-slate-500 hover:text-purple-300 ml-1"
            :title="`Inspect stack ${p.name} files`"
          >
            <ExternalLink class="h-3 w-3" />
          </router-link>
        </button>
      </div>

      <!-- Container Fleet List -->
      <div v-if="filteredContainers.length === 0" class="py-8 text-center text-xs text-slate-500 font-mono">
        No containers match the current filter or search criteria.
      </div>

      <div v-else class="divide-y divide-slate-800/60">
        <div
          v-for="c in filteredContainers.slice(0, 10)"
          :key="c.id"
          class="flex flex-col sm:flex-row sm:items-center justify-between py-2.5 gap-2 transition hover:bg-slate-900/40 px-2 rounded-lg"
        >
          <!-- Left: Status dot, Name, Stack, Exposure, Updates, Image, Uptime -->
          <div class="flex items-center gap-3 overflow-hidden">
            <span
              class="h-2.5 w-2.5 flex-shrink-0 rounded-full"
              :class="c.running ? 'bg-emerald-400 shadow-sm shadow-emerald-500/50' : 'bg-slate-600'"
            ></span>
            <div class="truncate">
              <div class="flex items-center gap-2 flex-wrap">
                <router-link
                  :to="'/containers?id=' + encodeURIComponent(c.id)"
                  class="font-mono text-xs font-bold text-white hover:text-indigo-300 truncate hover:underline"
                  :title="`Inspect container '${c.name}' in Control Center`"
                >
                  {{ c.name }}
                </router-link>

                <!-- Web link if running & exposed -->
                <a
                  v-if="c.running && resolveContainerUrls(c).bestUrl"
                  :href="resolveContainerUrls(c).bestUrl"
                  target="_blank"
                  rel="noopener"
                  class="p-0.5 rounded text-indigo-400 hover:text-indigo-300 hover:bg-indigo-500/10 transition"
                  :title="`Open Web App: ${resolveContainerUrls(c).bestUrl}`"
                >
                  <ExternalLink class="h-3 w-3" />
                </a>

                <!-- Compose Stack Badge -->
                <button
                  v-if="c.project_name"
                  @click="stackFilter = c.project_name"
                  class="rounded bg-indigo-950/60 border border-indigo-500/30 px-1.5 py-0.2 text-[10px] text-indigo-300 font-mono hover:bg-indigo-900/60 transition"
                  :title="`Filter by stack '${c.project_name}'`"
                >
                  stack: {{ c.project_name }}
                </button>

                <!-- Exposure Mode Badge -->
                <span
                  class="rounded-full px-2 py-0.2 text-[9px] font-mono font-medium"
                  :class="c.exposure_mode === 'public' ? 'bg-orange-500/10 text-orange-300 border border-orange-500/30' : (c.exposure_mode === 'private' ? 'bg-purple-500/10 text-purple-300 border border-purple-500/30' : 'bg-slate-800 text-slate-400')"
                  :title="c.exposure_mode === 'public' ? `Public: ${c.hostname}` : 'Internal'"
                >
                  {{ c.exposure_mode === 'public' ? 'Public' : (c.exposure_mode === 'private' ? c.private_type : 'Internal') }}
                </span>

                <!-- Update Available Badge -->
                <span
                  v-if="c.update_available"
                  class="rounded-full px-1.5 py-0.2 text-[9px] font-bold bg-amber-500/20 text-amber-300 border border-amber-500/40 animate-pulse"
                >
                  Update
                </span>
              </div>

              <div class="text-[11px] text-slate-400 truncate font-mono mt-0.5">
                {{ c.image }} &bull; <Clock class="inline h-2.5 w-2.5 text-slate-500" /> {{ c.uptime || c.status }}
              </div>
            </div>
          </div>

          <!-- Right: Live Metrics & Power Actions -->
          <div class="flex items-center justify-between sm:justify-end gap-3 self-end sm:self-auto w-full sm:w-auto shrink-0">
            <div v-if="c.running" class="text-right font-mono text-[11px]">
              <span class="text-indigo-400 font-bold mr-2">{{ c.cpu || '0.0%' }}</span>
              <span class="text-slate-400">{{ c.memory?.split('/')[0] || '' }}</span>
            </div>
            <div v-else class="text-[11px] text-slate-500 font-mono">
              stopped
            </div>

            <!-- Action Buttons -->
            <div class="flex items-center gap-1">
              <a
                v-if="c.running && resolveContainerUrls(c).bestUrl"
                :href="resolveContainerUrls(c).bestUrl"
                target="_blank"
                rel="noopener"
                class="rounded-lg p-1.5 text-slate-400 hover:bg-slate-800 hover:text-indigo-300 transition"
                :title="`Open Web UI: ${resolveContainerUrls(c).bestUrl}`"
              >
                <ExternalLink class="h-3.5 w-3.5" />
              </a>

              <router-link
                :to="'/containers?id=' + encodeURIComponent(c.id)"
                class="rounded-lg p-1.5 text-slate-400 hover:bg-slate-800 hover:text-indigo-300 transition"
                :title="`Inspect container '${c.name}' in Control Center`"
              >
                <FolderOpen class="h-3.5 w-3.5" />
              </router-link>

              <button
                v-if="!c.running"
                @click="runContainerAction(c, 'start')"
                :disabled="!!containerActionLoading[c.id]"
                class="rounded-lg p-1.5 text-slate-400 hover:bg-slate-800 hover:text-emerald-400 transition"
                title="Start container"
              >
                <Play class="h-3.5 w-3.5 fill-current" />
              </button>
              <button
                v-if="c.running"
                @click="runContainerAction(c, 'stop')"
                :disabled="!!containerActionLoading[c.id]"
                class="rounded-lg p-1.5 text-slate-400 hover:bg-slate-800 hover:text-rose-400 transition"
                title="Stop container"
              >
                <Square class="h-3.5 w-3.5 fill-current" />
              </button>
              <button
                @click="runContainerAction(c, 'restart')"
                :disabled="!!containerActionLoading[c.id]"
                class="rounded-lg p-1.5 text-slate-400 hover:bg-slate-800 hover:text-white transition"
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

      <!-- Footer Bar -->
      <div class="pt-3 border-t border-slate-800/80 flex flex-col sm:flex-row sm:items-center justify-between gap-2 text-xs">
        <span class="text-slate-400 font-mono text-[11px]">
          Showing {{ Math.min(filteredContainers.length, 10) }} of {{ filteredContainers.length }} filtered containers
        </span>
        <div class="flex items-center gap-3">
          <router-link
            to="/containers?tab=stacks"
            class="text-purple-400 hover:text-purple-300 font-medium flex items-center gap-1"
          >
            <FolderOpen class="h-3.5 w-3.5" />
            <span>Manage Stacks &rarr;</span>
          </router-link>
          <router-link
            to="/containers"
            class="cyber-btn-secondary text-xs flex items-center gap-1.5 py-1.5 px-3"
          >
            <span>Open All Containers</span>
            <ArrowUpRight class="h-3.5 w-3.5" />
          </router-link>
        </div>
      </div>
    </div>

    <!-- Recent System Events & Audit Trail Feed -->
    <div class="cyber-card space-y-3">
      <div class="flex items-center justify-between border-b border-slate-800/80 pb-2.5">
        <div class="flex items-center gap-2">
          <Shield class="h-4 w-4 text-indigo-400" />
          <h2 class="text-xs font-semibold text-white">Recent System Events & Audit Trail</h2>
        </div>
        <router-link to="/audit" class="text-xs text-indigo-400 hover:underline">
          View Full Audit Log &rarr;
        </router-link>
      </div>

      <div v-if="recentAudits.length === 0" class="py-4 text-center text-xs text-slate-500 font-mono">
        No audit records logged yet.
      </div>

      <div v-else class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-2.5">
        <div
          v-for="item in recentAudits.slice(0, 4)"
          :key="item.id"
          class="p-3 rounded-xl border border-slate-800 bg-slate-950/70 text-xs space-y-1 hover:border-slate-700 transition"
        >
          <div class="flex items-center justify-between">
            <span class="font-semibold text-slate-200 capitalize truncate">
              {{ item.action.replace(/_/g, ' ') }}
            </span>
            <span class="text-[10px] text-slate-500 font-mono">{{ formatTimeAgo(item.created_at) }}</span>
          </div>
          <p class="text-[11px] text-slate-400 truncate" :title="item.detail || item.action">
            {{ item.detail || 'Executed action' }}
          </p>
        </div>
      </div>
    </div>
  </div>
</template>
