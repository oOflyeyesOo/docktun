<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import {
  RotateCcw,
  Server,
  Globe,
  Cpu,
  Layers,
  ExternalLink,
  ShieldCheck,
  Activity,
  Wifi,
  FolderOpen,
  Clock,
  AlertCircle,
  Copy,
  Check,
  RefreshCw,
  Zap,
  Radio,
  Sparkles,
  Shield,
  ArrowRight,
  Box,
  HardDrive,
  Plus
} from 'lucide-vue-next'
import { cloudflareEnabled as isCloudflareEnabled } from '../lib/tunnel-provider.mjs'
import { useApi } from '../composables/useApi'

const { apiFetch } = useApi()
const router = useRouter()

// Primary State
const status = ref<any>(null)
const containers = ref<any[]>([])
const healthList = ref<any[]>([])
const recentAudits = ref<any[]>([])

// Action / Loading State
const actionLoading = ref('')
const refreshing = ref(false)
const dnsSyncing = ref(false)
const copiedHost = ref('')
const probingHealth = ref(false)
const routesTab = ref<'public' | 'local'>('public')

// Fetch Telemetry Data
async function fetchStatus() {
  try {
    status.value = await apiFetch('/api/status')
  } catch (e) {
    console.error('Failed to fetch status', e)
  }
}

async function fetchContainers() {
  try {
    containers.value = await apiFetch('/api/docker/containers')
  } catch (e) {
    console.error('Failed to fetch containers', e)
  }
}

async function fetchHealth(force = false) {
  probingHealth.value = true
  try {
    healthList.value = await apiFetch(force ? '/api/health?force=true' : '/api/health')
  } catch {}
  finally {
    probingHealth.value = false
  }
}

async function fetchRecentAudits() {
  try {
    const res = await apiFetch<{ entries: any[] }>('/api/audit?limit=6')
    recentAudits.value = res.entries || []
  } catch {}
}

async function refreshAll() {
  refreshing.value = true
  await Promise.all([
    fetchStatus(),
    fetchContainers(),
    fetchHealth(true),
    fetchRecentAudits(),
  ])
  setTimeout(() => (refreshing.value = false), 300)
}

// Tunnel Control Actions
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

async function triggerDnsSync(force = false) {
  dnsSyncing.value = true
  try {
    const url = force ? '/api/cloudflare/sync-all?force=true' : '/api/cloudflare/sync-all'
    const res: any = await apiFetch(url, { method: 'POST' })
    const syncedList: any[] = res.synced || []
    const successList = syncedList.filter((s) => s.success)
    const failList = syncedList.filter((s) => !s.success)

    if (failList.length > 0) {
      const errDetails = failList.map((f) => `• ${f.hostname}: ${f.error}`).join('\n')
      if (confirm(`DNS Sync Notice:\n${successList.length} route(s) verified.\n${failList.length} route(s) had conflicts or errors:\n${errDetails}\n\nWould you like to force overwrite old DNS records and re-sync?`)) {
        await triggerDnsSync(true)
        return
      }
    } else {
      alert(`Cloudflare DNS Synced! All ${successList.length} public route(s) verified in Cloudflare DNS.`)
    }
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

// Helpers & Parsers
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

function getAuditBadge(action: string): { icon: any; color: string; label: string } {
  const act = (action || '').toLowerCase()
  if (act.includes('container') || act.includes('docker')) {
    return { icon: Box, color: 'text-indigo-600 bg-indigo-500/10 border-indigo-500/20', label: 'Docker' }
  }
  if (act.includes('tunnel') || act.includes('cloudflare') || act.includes('dns')) {
    return { icon: Globe, color: 'text-orange-600 bg-orange-500/10 border-orange-500/20', label: 'Tunnel' }
  }
  if (act.includes('tailscale') || act.includes('wireguard') || act.includes('mesh')) {
    return { icon: Radio, color: 'text-purple-600 bg-purple-500/10 border-purple-500/20', label: 'Mesh' }
  }
  if (act.includes('config') || act.includes('backup')) {
    return { icon: Layers, color: 'text-cyan-600 bg-cyan-500/10 border-cyan-500/20', label: 'Config' }
  }
  if (act.includes('project') || act.includes('deploy')) {
    return { icon: FolderOpen, color: 'text-emerald-600 bg-emerald-500/10 border-emerald-500/20', label: 'Project' }
  }
  return { icon: Shield, color: 'text-slate-600 bg-slate-100 border-slate-300', label: 'System' }
}

function parseContainerPorts(portsStr?: string): { hostPort: string; targetPort: string } {
  if (!portsStr) return { hostPort: '', targetPort: '80' }
  const hostMatch = portsStr.match(/:(\d+)->(\d+)\/tcp/)
  if (hostMatch) return { hostPort: hostMatch[1], targetPort: hostMatch[2] }
  const simpleHostMatch = portsStr.match(/:(\d+)->/)
  if (simpleHostMatch) return { hostPort: simpleHostMatch[1], targetPort: simpleHostMatch[1] }
  const plainMatch = portsStr.match(/(\d+)\/tcp/)
  if (plainMatch) return { hostPort: '', targetPort: plainMatch[1] }
  return { hostPort: '', targetPort: '80' }
}

function resolveContainerUrls(c: any) {
  const { hostPort, targetPort } = parseContainerPorts(c.ports)
  const host = typeof window !== 'undefined' && window.location.hostname ? window.location.hostname : 'localhost'

  const publicUrl = c.hostname ? `https://${c.hostname}` : null
  const directUrl = hostPort ? `http://${host}:${hostPort}` : null
  const effectivePort = targetPort || hostPort || '80'
  const localUrl = `http://${c.name}.local${effectivePort !== '80' ? ':' + effectivePort : ''}`

  const bestUrl = publicUrl || directUrl || localUrl
  return { publicUrl, directUrl, localUrl, bestUrl, hostPort, targetPort: effectivePort }
}

function extractContainerFromService(service: string): string {
  if (!service) return ''
  try {
    let s = service.trim()
    if (!s.includes('://')) s = 'http://' + s
    const u = new URL(s)
    return u.hostname
  } catch {
    return service.replace(/^https?:\/\//, '').split(':')[0]
  }
}

// Computed Statistics
const cloudflareEnabled = computed(() => isCloudflareEnabled(status.value))
const isTunnelRunning = computed(() => Boolean(status.value?.running))

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

  // Count distinct stacks
  const stackSet = new Set<string>()
  containers.value.forEach((c) => {
    if (c.project_name) stackSet.add(c.project_name.trim())
  })

  return {
    total,
    running,
    stopped: total - running,
    stacksCount: stackSet.size,
    updatesCount: containers.value.filter((c) => c.update_available).length,
    cpuPercent: totalCpu.toFixed(1) + '%',
    cpuNum: Math.min(Math.round(totalCpu), 100),
    totalMemStr: formatBytes(totalMemBytes),
    totalLimitStr: totalLimitBytes > 0 ? formatBytes(totalLimitBytes) : 'Host Limit',
  }
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

const originHealthSummary = computed(() => {
  const total = healthList.value.length
  if (total === 0) return { total: 0, up: 0, down: 0, allUp: true, avgLatency: '—' }
  const up = healthList.value.filter((h) => h.status === 'up').length
  const down = total - up
  let totalMs = 0
  let msCount = 0
  for (const h of healthList.value) {
    if (h.latency_ms && h.latency_ms > 0) {
      totalMs += h.latency_ms
      msCount++
    }
  }
  const avg = msCount > 0 ? `${Math.round(totalMs / msCount)}ms` : '—'
  return { total, up, down, allUp: down === 0, avgLatency: avg }
})

// Public Ingress Routes
const publicRoutes = computed(() => {
  const hosts: string[] = status.value?.hostnames || []
  return hosts.map((h) => {
    const health = healthByHostname.value[h]
    return {
      hostname: h,
      service: health?.service || '',
      status: health?.status || (isTunnelRunning.value ? 'up' : 'unknown'),
      latency: health?.latency || 'Edge Active',
      container: extractContainerFromService(health?.service || ''),
    }
  })
})

// Local Network Services
const localServices = computed(() => {
  return containers.value
    .filter((c) => c.running)
    .map((c) => ({
      id: c.id,
      name: c.name,
      ...resolveContainerUrls(c),
    }))
    .slice(0, 12)
})

let timer: any = null

onMounted(async () => {
  await refreshAll()
  // Clean, fast telemetry poll every 6s when tab is active
  timer = setInterval(() => {
    if (document.visibilityState === 'visible') {
      fetchStatus()
      fetchContainers()
    }
  }, 6000)
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <div class="space-y-6">
    <!-- Header Section -->
    <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between border-b border-slate-200/80 pb-4">
      <div>
        <div class="flex items-center gap-2">
          <p class="text-[11px] font-semibold uppercase tracking-wider text-indigo-600">System Telemetry & Status</p>
          <span class="rounded bg-slate-100 px-1.5 py-0.2 text-[10px] font-mono text-slate-700">DockTun Core</span>
        </div>
        <h1 class="text-2xl font-bold tracking-tight text-slate-900 flex items-center gap-3 mt-1">
          <span>Infrastructure Overview</span>
          <span
            v-if="cloudflareEnabled"
            class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-semibold"
            :class="isTunnelRunning ? 'bg-emerald-500/10 text-emerald-600 border border-emerald-500/30' : 'bg-rose-500/10 text-rose-600 border border-rose-500/30'"
          >
            <span class="h-1.5 w-1.5 rounded-full" :class="isTunnelRunning ? 'bg-emerald-400 animate-pulse' : 'bg-rose-400'"></span>
            <span>{{ isTunnelRunning ? 'Tunnel Active' : 'Tunnel Degraded' }}</span>
          </span>
          <span
            v-if="status?.zeroconf_status === 'running'"
            class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-semibold bg-indigo-500/10 text-indigo-700 border border-indigo-500/30"
          >
            <Wifi class="h-3 w-3 text-indigo-600" />
            <span>.local mDNS Active</span>
          </span>
        </h1>
      </div>

      <div class="flex items-center gap-2 flex-wrap">
        <button
          v-if="cloudflareEnabled"
          @click="triggerDnsSync()"
          :disabled="dnsSyncing"
          class="cyber-btn-secondary text-xs flex items-center gap-1.5 py-2 px-3 text-slate-800"
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
          v-if="cloudflareEnabled"
          @click="controlTunnel('restart')"
          :disabled="!!actionLoading"
          class="cyber-btn text-xs flex items-center gap-1.5 py-2 px-3 bg-indigo-600 hover:bg-indigo-500 text-white"
          title="Restart Cloudflare tunnel container"
        >
          <RotateCcw class="h-3.5 w-3.5" :class="actionLoading === 'restart' ? 'animate-spin' : ''" />
          <span>{{ actionLoading === 'restart' ? 'Restarting…' : 'Restart Tunnel' }}</span>
        </button>
      </div>
    </div>

    <!-- Tunnel Degraded Diagnostic Banner -->
    <div
      v-if="cloudflareEnabled && !isTunnelRunning && status"
      class="rounded-xl border border-rose-500/30 bg-rose-500/10 p-4 text-xs text-rose-700 flex flex-col sm:flex-row sm:items-start justify-between gap-3 shadow-sm"
    >
      <div class="flex items-start gap-3">
        <AlertCircle class="h-5 w-5 text-rose-600 mt-0.5 shrink-0" />
        <div class="space-y-2">
          <p class="font-bold text-rose-700 text-sm">
            Cloudflare Tunnel is {{ status?.status || 'degraded' }}
          </p>
          <p v-if="status?.last_error" class="font-mono text-[11px] text-rose-800 bg-white/80 p-2.5 rounded-lg border border-rose-200 whitespace-pre-wrap">
            {{ status.last_error }}
          </p>
          <div class="flex items-center gap-3 pt-1 text-[11px]">
            <router-link to="/access?tab=cloudflare" class="text-indigo-600 hover:underline font-semibold flex items-center gap-1">
              <span>Inspect Tunnel Config &rarr;</span>
            </router-link>
            <router-link to="/logs" class="text-slate-700 hover:underline font-semibold flex items-center gap-1">
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

    <!-- 4 High-Density Executive Telemetry Metric Cards -->
    <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
      <!-- 1. Cloudflare Tunnel Card -->
      <div v-if="cloudflareEnabled" class="cyber-card flex flex-col justify-between hover:border-slate-300 transition">
        <div>
          <div class="flex items-center justify-between text-slate-600">
            <span class="text-[11px] font-semibold uppercase tracking-wider">Cloudflare Tunnel</span>
            <div class="p-1 rounded-md bg-orange-500/10 text-orange-600">
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
            <span class="text-base font-bold text-slate-900 truncate" :title="status?.tunnel_name || 'Active'">
              {{ status?.tunnel_name || 'docktun' }}
            </span>
          </div>

          <!-- Live Edge Colos -->
          <div class="mt-2 flex flex-wrap items-center gap-1.5">
            <span
              v-for="colo in (status?.colo_locations || [])"
              :key="colo"
              class="inline-flex items-center gap-1 rounded bg-slate-100 border border-slate-300 px-1.5 py-0.5 text-[10px] font-mono text-cyan-700 font-semibold"
              :title="`Edge Datacenter: ${colo}`"
            >
              <Radio class="h-2.5 w-2.5 text-emerald-600 animate-pulse" />
              <span>{{ colo }}</span>
            </span>
            <span v-if="!status?.colo_locations?.length && isTunnelRunning" class="text-[11px] text-slate-500 font-mono">
              Edge Active
            </span>
          </div>
        </div>

        <div class="mt-3 border-t border-slate-200/80 pt-2 flex items-center justify-between text-xs text-slate-600">
          <span class="truncate">Uptime: {{ status?.uptime || '—' }}</span>
          <router-link to="/access?tab=cloudflare" class="text-indigo-600 hover:underline font-medium">
            Configure &rarr;
          </router-link>
        </div>
      </div>

      <!-- 2. Traffic & Origin Health Card -->
      <div class="cyber-card flex flex-col justify-between hover:border-slate-300 transition">
        <div>
          <div class="flex items-center justify-between text-slate-600">
            <span class="text-[11px] font-semibold uppercase tracking-wider">Traffic & Origins</span>
            <div class="p-1 rounded-md bg-emerald-500/10 text-emerald-600">
              <Activity class="h-3.5 w-3.5" />
            </div>
          </div>
          <div class="mt-2.5 flex items-baseline gap-2">
            <span class="text-2xl font-bold font-mono text-slate-900">
              {{ status?.total_requests?.toLocaleString() || '0' }}
            </span>
            <span class="text-xs text-slate-600">total requests</span>
          </div>

          <!-- Origin Health Dial Probe -->
          <div class="mt-2 flex items-center gap-2 text-xs">
            <span
              class="inline-flex items-center gap-1 text-[11px] font-mono font-bold"
              :class="originHealthSummary.allUp ? 'text-emerald-600' : 'text-amber-600'"
            >
              <Activity class="h-3 w-3" />
              <span>{{ originHealthSummary.up }}/{{ originHealthSummary.total }} Healthy</span>
            </span>
            <span class="text-[10px] font-mono text-cyan-700 px-1.5 py-0.2 rounded bg-slate-100 border border-slate-300">
              {{ originHealthSummary.avgLatency }}
            </span>
          </div>
        </div>

        <div class="mt-3 border-t border-slate-200/80 pt-2 flex items-center justify-between text-xs text-slate-600">
          <span class="text-slate-600">Active streams: <strong class="font-mono text-emerald-600">{{ status?.active_connections || 0 }}</strong></span>
          <button @click="fetchHealth(true)" :disabled="probingHealth" class="text-indigo-600 hover:underline font-medium">
            {{ probingHealth ? 'Probing…' : 'Probe' }}
          </button>
        </div>
      </div>

      <!-- 3. Container Fleet & Stacks Card -->
      <div class="cyber-card flex flex-col justify-between hover:border-slate-300 transition">
        <div>
          <div class="flex items-center justify-between text-slate-600">
            <span class="text-[11px] font-semibold uppercase tracking-wider">Container Fleet</span>
            <div class="p-1 rounded-md bg-indigo-500/10 text-indigo-600">
              <Server class="h-3.5 w-3.5" />
            </div>
          </div>
          <div class="mt-2.5 flex items-baseline gap-2">
            <span class="text-2xl font-bold text-slate-900 font-mono">{{ containerStats.running }}</span>
            <span class="text-xs text-slate-600">/ {{ containerStats.total }} running</span>
          </div>

          <div class="mt-2 flex items-center gap-2 text-xs">
            <span class="text-[11px] font-mono text-purple-700 bg-purple-500/10 border border-purple-500/20 px-2 py-0.2 rounded font-semibold">
              {{ containerStats.stacksCount }} Stacks
            </span>
            <span :class="containerStats.stopped > 0 ? 'text-amber-600 font-medium' : 'text-slate-500'" class="text-[11px] font-mono">
              {{ containerStats.stopped }} stopped
            </span>
          </div>
        </div>

        <div class="mt-3 border-t border-slate-200/80 pt-2 flex items-center justify-between text-xs text-slate-600">
          <router-link
            v-if="containerStats.updatesCount > 0"
            to="/containers?filter=updates"
            class="inline-flex items-center gap-1 rounded bg-amber-500/20 border border-amber-500/40 px-1.5 py-0.2 text-[10px] font-bold text-amber-700 animate-pulse hover:bg-amber-500/30"
          >
            <Sparkles class="h-2.5 w-2.5" />
            {{ containerStats.updatesCount }} update{{ containerStats.updatesCount > 1 ? 's' : '' }}
          </router-link>
          <span v-else class="text-[11px] text-slate-500 font-mono">All up to date</span>
          <router-link to="/containers" class="text-indigo-600 hover:underline font-medium">Manage &rarr;</router-link>
        </div>
      </div>

      <!-- 4. Combined Resource Load Card -->
      <router-link
        to="/metrics"
        class="cyber-card flex flex-col justify-between hover:border-indigo-400 hover:shadow-xs transition group cursor-pointer"
        title="View in-depth real-time CPU & resource metrics"
      >
        <div>
          <div class="flex items-center justify-between text-slate-600">
            <span class="text-[11px] font-semibold uppercase tracking-wider group-hover:text-indigo-600 transition">Host Resources</span>
            <div class="p-1 rounded-md bg-purple-500/10 text-purple-600 group-hover:bg-indigo-500/10 group-hover:text-indigo-600 transition">
              <Cpu class="h-3.5 w-3.5" />
            </div>
          </div>
          <div class="mt-2.5 flex items-center justify-between">
            <div class="flex items-baseline gap-1.5">
              <span class="text-xl font-bold text-slate-900 font-mono group-hover:text-indigo-600 transition">{{ containerStats.cpuPercent }}</span>
              <span class="text-[11px] text-slate-600">CPU</span>
            </div>
            <div class="text-right font-mono text-xs">
              <span class="text-emerald-600 font-bold">{{ containerStats.totalMemStr }}</span>
              <span class="text-[10px] text-slate-500 block">/ {{ status?.docker_system?.mem_total_str || containerStats.totalLimitStr }}</span>
            </div>
          </div>

          <div class="mt-2 flex items-center justify-between text-[11px] font-mono text-slate-600">
            <span>Engine: v{{ status?.docker_system?.server_version || '—' }}</span>
            <span class="text-purple-700 font-semibold">{{ status?.docker_system?.cpus ? `${status.docker_system.cpus} vCPU` : '' }}</span>
          </div>
        </div>

        <div class="mt-3 border-t border-slate-200/80 pt-2 space-y-1">
          <div class="flex items-center justify-between text-[10px] font-mono text-slate-500">
            <span>Utilization</span>
            <span class="text-indigo-600 group-hover:underline flex items-center gap-0.5 font-medium">Metrics &rarr;</span>
          </div>
          <div class="h-1.5 w-full overflow-hidden rounded-full bg-slate-100">
            <div
              class="h-full bg-indigo-500 transition-all duration-300 rounded-full"
              :style="{ width: `${containerStats.cpuNum}%` }"
            ></div>
          </div>
        </div>
      </router-link>
    </div>

    <!-- Unified Core Navigation Command Center -->
    <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
      <!-- 1. Containers & Stacks Hub -->
      <div class="cyber-card p-4 flex flex-col justify-between hover:border-indigo-300 transition group shadow-xs">
        <div>
          <div class="flex items-center justify-between">
            <div class="p-2 rounded-xl bg-indigo-500/10 text-indigo-600 border border-indigo-500/20">
              <Server class="h-5 w-5" />
            </div>
            <span class="text-[10px] font-mono font-semibold px-2 py-0.5 rounded bg-indigo-50 text-indigo-700 border border-indigo-200">
              {{ containerStats.running }} Active
            </span>
          </div>
          <h3 class="text-sm font-bold text-slate-900 mt-3 group-hover:text-indigo-600 transition">Containers & Stacks</h3>
          <p class="text-xs text-slate-600 mt-1 leading-relaxed">
            Manage Docker containers, deploy Compose stacks, edit environments, and launch new services.
          </p>
        </div>
        <div class="mt-4 pt-3 border-t border-slate-200/80 flex items-center justify-between text-xs">
          <router-link to="/containers" class="text-indigo-600 font-semibold hover:underline flex items-center gap-1">
            <span>Fleet Manager</span>
            <ArrowRight class="h-3.5 w-3.5" />
          </router-link>
          <router-link to="/containers?tab=stacks" class="text-slate-600 hover:text-slate-900 font-medium">
            Stacks
          </router-link>
        </div>
      </div>

      <!-- 2. Remote Access Hub -->
      <div class="cyber-card p-4 flex flex-col justify-between hover:border-orange-300 transition group shadow-xs">
        <div>
          <div class="flex items-center justify-between">
            <div class="p-2 rounded-xl bg-orange-500/10 text-orange-600 border border-orange-500/20">
              <Globe class="h-5 w-5" />
            </div>
            <span class="text-[10px] font-mono font-semibold px-2 py-0.5 rounded bg-orange-50 text-orange-700 border border-orange-200">
              {{ status?.hostnames?.length || 0 }} Domains
            </span>
          </div>
          <h3 class="text-sm font-bold text-slate-900 mt-3 group-hover:text-orange-600 transition">Remote Access & Ingress</h3>
          <p class="text-xs text-slate-600 mt-1 leading-relaxed">
            Zero-Trust Cloudflare tunnels, Tailscale mesh VPN, and peer-to-peer WireGuard networking.
          </p>
        </div>
        <div class="mt-4 pt-3 border-t border-slate-200/80 flex items-center justify-between text-xs">
          <router-link to="/access" class="text-orange-700 font-semibold hover:underline flex items-center gap-1">
            <span>Access Hub</span>
            <ArrowRight class="h-3.5 w-3.5" />
          </router-link>
          <router-link to="/access?tab=cloudflare" class="text-slate-600 hover:text-slate-900 font-medium">
            Cloudflare
          </router-link>
        </div>
      </div>

      <!-- 3. Metrics & Health Hub -->
      <div class="cyber-card p-4 flex flex-col justify-between hover:border-purple-300 transition group shadow-xs">
        <div>
          <div class="flex items-center justify-between">
            <div class="p-2 rounded-xl bg-purple-500/10 text-purple-600 border border-purple-500/20">
              <Activity class="h-5 w-5" />
            </div>
            <span class="text-[10px] font-mono font-semibold px-2 py-0.5 rounded bg-purple-50 text-purple-700 border border-purple-200">
              Live CPU & RAM
            </span>
          </div>
          <h3 class="text-sm font-bold text-slate-900 mt-3 group-hover:text-purple-600 transition">Telemetry & Metrics</h3>
          <p class="text-xs text-slate-600 mt-1 leading-relaxed">
            Real-time charts, container resource consumption, network stream stats, and system limits.
          </p>
        </div>
        <div class="mt-4 pt-3 border-t border-slate-200/80 flex items-center justify-between text-xs">
          <router-link to="/metrics" class="text-purple-700 font-semibold hover:underline flex items-center gap-1">
            <span>Live Charts</span>
            <ArrowRight class="h-3.5 w-3.5" />
          </router-link>
          <router-link to="/logs" class="text-slate-600 hover:text-slate-900 font-medium">
            Logs & Audit
          </router-link>
        </div>
      </div>

      <!-- 4. Storage & Reclamation Hub -->
      <div class="cyber-card p-4 flex flex-col justify-between hover:border-emerald-300 transition group shadow-xs">
        <div>
          <div class="flex items-center justify-between">
            <div class="p-2 rounded-xl bg-emerald-500/10 text-emerald-600 border border-emerald-500/20">
              <HardDrive class="h-5 w-5" />
            </div>
            <span class="text-[10px] font-mono font-semibold px-2 py-0.5 rounded bg-emerald-50 text-emerald-700 border border-emerald-200">
              Volumes & Files
            </span>
          </div>
          <h3 class="text-sm font-bold text-slate-900 mt-3 group-hover:text-emerald-600 transition">Storage & File Center</h3>
          <p class="text-xs text-slate-600 mt-1 leading-relaxed">
            Browse container volumes, prune unused images, inspect config files, and reclaim host disk space.
          </p>
        </div>
        <div class="mt-4 pt-3 border-t border-slate-200/80 flex items-center justify-between text-xs">
          <router-link to="/storage" class="text-emerald-700 font-semibold hover:underline flex items-center gap-1">
            <span>Storage Reclaim</span>
            <ArrowRight class="h-3.5 w-3.5" />
          </router-link>
          <router-link to="/settings" class="text-slate-600 hover:text-slate-900 font-medium">
            Settings
          </router-link>
        </div>
      </div>
    </div>

    <!-- Streamlined Ingress Routing & Origin Health Directory -->
    <div class="cyber-card space-y-4">
      <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-200/80 pb-3">
        <div class="flex items-center gap-2">
          <Layers class="h-4 w-4 text-indigo-600" />
          <h2 class="text-xs font-semibold text-slate-900">
            Ingress Routing & Origins Health
          </h2>
        </div>

        <div class="flex items-center gap-2 flex-wrap">
          <!-- Route Switcher Tabs -->
          <div class="flex items-center gap-1 rounded-lg border border-slate-200 bg-slate-50 p-1 text-xs">
            <button
              @click="routesTab = 'public'"
              class="flex items-center gap-1.5 rounded-md px-3 py-1 font-medium transition"
              :class="routesTab === 'public' ? 'bg-white text-slate-900 shadow-xs' : 'text-slate-600 hover:text-slate-800'"
            >
              <Globe class="h-3.5 w-3.5 text-orange-600" />
              <span>Public Hostnames (Cloudflare)</span>
              <span class="rounded bg-slate-100 px-1.5 py-0.2 text-[10px] text-orange-700 font-mono">{{ publicRoutes.length }}</span>
            </button>

            <button
              @click="routesTab = 'local'"
              class="flex items-center gap-1.5 rounded-md px-3 py-1 font-medium transition"
              :class="routesTab === 'local' ? 'bg-white text-slate-900 shadow-xs' : 'text-slate-600 hover:text-slate-800'"
            >
              <Wifi class="h-3.5 w-3.5 text-indigo-600" />
              <span>Local Network (.local)</span>
              <span class="rounded bg-slate-100 px-1.5 py-0.2 text-[10px] text-indigo-700 font-mono">{{ localServices.length }}</span>
            </button>
          </div>

          <!-- Actions -->
          <button
            @click="fetchHealth(true)"
            :disabled="probingHealth"
            class="cyber-btn-secondary text-xs flex items-center gap-1 py-1 px-2.5 text-slate-700"
            title="Probe live latency of ingress origins"
          >
            <RotateCcw class="h-3 w-3" :class="probingHealth ? 'animate-spin' : ''" />
            <span>{{ probingHealth ? 'Probing…' : 'Probe Latency' }}</span>
          </button>
          <router-link
            to="/access?tab=cloudflare"
            class="cyber-btn-secondary text-xs flex items-center gap-1 py-1 px-2.5 text-indigo-700 hover:text-slate-900 font-medium"
          >
            <span>Manage Rules</span>
            <ArrowRight class="h-3 w-3" />
          </router-link>
        </div>
      </div>

      <!-- Tab 1: Public Cloudflare Hostnames -->
      <div v-if="routesTab === 'public'" class="space-y-3">
        <div v-if="publicRoutes.length === 0" class="py-6 text-center text-xs text-slate-500 font-mono">
          No public ingress routes configured. Add routes in Remote Access &rarr; Cloudflare.
        </div>
        <div v-else class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
          <div
            v-for="r in publicRoutes"
            :key="r.hostname"
            class="flex flex-col justify-between rounded-xl border border-slate-200/90 bg-slate-50 p-3.5 hover:border-slate-300 transition group shadow-xs"
          >
            <div>
              <div class="flex items-center justify-between gap-2 mb-1.5">
                <div class="flex items-center gap-2 truncate">
                  <ShieldCheck class="h-4 w-4 text-emerald-600 shrink-0" />
                  <a
                    :href="`https://${r.hostname}`"
                    target="_blank"
                    rel="noopener"
                    class="font-mono text-xs font-bold text-slate-900 hover:text-indigo-700 truncate hover:underline"
                    :title="`Open https://${r.hostname}`"
                  >
                    {{ r.hostname }}
                  </a>
                </div>

                <!-- Latency Badge -->
                <span
                  class="rounded-md px-2 py-0.5 text-[10px] font-mono font-semibold shrink-0"
                  :class="r.status === 'up' ? 'bg-emerald-500/10 text-emerald-600 border border-emerald-500/30' : (r.status === 'down' ? 'bg-rose-500/10 text-rose-600 border border-rose-500/30' : 'bg-slate-100 text-slate-600 border border-slate-300')"
                >
                  <span v-if="r.status === 'up'" class="inline-block h-1.5 w-1.5 rounded-full bg-emerald-400 mr-1 animate-pulse"></span>
                  {{ r.status === 'up' ? r.latency : (r.status === 'down' ? 'TIMEOUT' : 'Edge Active') }}
                </span>
              </div>

              <!-- Target Origin Container Link -->
              <div class="flex items-center gap-1.5 text-[11px] text-slate-600 font-mono truncate">
                <span class="text-slate-500">&rarr;</span>
                <span v-if="!r.service" class="text-slate-500">Cloudflare Edge Ingress</span>
                <router-link
                  v-else-if="r.container"
                  :to="'/containers?name=' + encodeURIComponent(r.container)"
                  class="text-indigo-600 hover:text-indigo-700 hover:underline truncate"
                  :title="`Inspect container '${r.container}'`"
                >
                  {{ r.service }}
                </router-link>
                <span v-else class="text-slate-600 truncate">{{ r.service }}</span>
              </div>
            </div>

            <!-- Actions Bar -->
            <div class="flex items-center justify-between pt-2.5 mt-2.5 border-t border-slate-200/60 text-xs">
              <span class="text-[10px] font-mono text-slate-500 uppercase">HTTPS &bull; Cloudflare Edge</span>
              <div class="flex items-center gap-1">
                <button
                  @click="copyUrl(`https://${r.hostname}`)"
                  class="p-1.5 rounded-lg text-slate-600 hover:bg-slate-100 hover:text-slate-800 transition"
                  :title="copiedHost === `https://${r.hostname}` ? 'Copied!' : 'Copy URL'"
                >
                  <Check v-if="copiedHost === `https://${r.hostname}`" class="h-3.5 w-3.5 text-emerald-600" />
                  <Copy v-else class="h-3.5 w-3.5" />
                </button>
                <a
                  :href="`https://${r.hostname}`"
                  target="_blank"
                  rel="noopener"
                  class="p-1.5 rounded-lg text-indigo-600 hover:bg-indigo-500/10 hover:text-indigo-700 transition"
                  title="Open URL in new tab"
                >
                  <ExternalLink class="h-3.5 w-3.5" />
                </a>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Tab 2: Local Network Services -->
      <div v-else class="space-y-3">
        <div v-if="localServices.length === 0" class="py-6 text-center text-xs text-slate-500 font-mono">
          No active running containers found.
        </div>
        <div v-else class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          <div
            v-for="svc in localServices"
            :key="svc.id"
            class="flex items-center justify-between rounded-xl border border-slate-200 bg-slate-50 p-3 hover:border-slate-300 transition group shadow-xs"
          >
            <div class="truncate pr-2">
              <div class="flex items-center gap-1.5">
                <span class="h-2 w-2 rounded-full bg-emerald-400 shrink-0"></span>
                <router-link
                  :to="'/containers?id=' + encodeURIComponent(svc.id)"
                  class="font-mono text-xs font-semibold text-slate-800 hover:text-slate-900 truncate hover:underline"
                  :title="`Inspect container '${svc.name}'`"
                >
                  {{ svc.name }}
                </router-link>
                <span
                  v-if="svc.hostPort"
                  class="px-1.5 py-0.2 rounded bg-indigo-500/10 border border-indigo-500/30 text-[10px] font-mono text-indigo-700 font-semibold shrink-0"
                  :title="`Published host port: ${svc.hostPort}`"
                >
                  :{{ svc.hostPort }}
                </span>
              </div>
              <div class="font-mono text-[11px] text-indigo-700 truncate mt-0.5" :title="svc.bestUrl">
                {{ svc.directUrl || svc.publicUrl || svc.localUrl }}
              </div>
            </div>

            <div class="flex items-center gap-1 shrink-0">
              <button
                @click="copyUrl(svc.bestUrl)"
                class="p-1.5 rounded-lg text-slate-600 hover:bg-slate-100 hover:text-slate-800 transition"
                :title="copiedHost === svc.bestUrl ? 'Copied!' : 'Copy URL'"
              >
                <Check v-if="copiedHost === svc.bestUrl" class="h-3.5 w-3.5 text-emerald-600" />
                <Copy v-else class="h-3.5 w-3.5" />
              </button>
              <a
                :href="svc.bestUrl"
                target="_blank"
                rel="noopener"
                class="cyber-btn-secondary text-[11px] flex items-center gap-1 py-1 px-2.5 text-indigo-700 hover:text-slate-900"
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

    <!-- Recent System Events -->
    <div class="cyber-card space-y-3">
      <div class="flex items-center justify-between border-b border-slate-200/80 pb-2.5">
        <div class="flex items-center gap-2">
          <Shield class="h-4 w-4 text-indigo-600" />
          <h2 class="text-xs font-semibold text-slate-900">Recent System Events</h2>
          <span v-if="recentAudits.length > 0" class="text-[10px] text-slate-500 font-mono">({{ recentAudits.length }})</span>
        </div>
        <router-link to="/logs?tab=audit" class="text-xs text-indigo-600 hover:text-indigo-700 hover:underline flex items-center gap-1 font-medium">
          <span>View Full Audit Log</span>
          <ArrowRight class="h-3 w-3" />
        </router-link>
      </div>

      <div v-if="recentAudits.length === 0" class="py-6 text-center text-xs text-slate-500 font-mono">
        No recent system events logged yet.
      </div>

      <div v-else class="divide-y divide-slate-200/60">
        <div
          v-for="item in recentAudits"
          :key="item.id"
          class="py-2.5 px-2 flex flex-col sm:flex-row sm:items-center justify-between gap-2 text-xs hover:bg-white/40 rounded-lg transition group"
        >
          <div class="flex items-start sm:items-center gap-2.5 min-w-0 flex-1">
            <span
              class="inline-flex items-center gap-1 text-[10px] font-semibold px-2 py-0.5 rounded-md border shrink-0"
              :class="getAuditBadge(item.action).color"
            >
              <component :is="getAuditBadge(item.action).icon" class="h-3 w-3" />
              <span>{{ getAuditBadge(item.action).label }}</span>
            </span>

            <div class="min-w-0 flex-1">
              <span class="font-semibold text-slate-800 capitalize">
                {{ (item.action || '').replace(/_/g, ' ') }}
              </span>
              <span v-if="item.detail" class="text-slate-600 ml-1.5 truncate inline-block align-bottom max-w-[280px] sm:max-w-[420px] lg:max-w-[620px]" :title="item.detail">
                — {{ item.detail }}
              </span>
            </div>
          </div>

          <div class="flex items-center gap-2 shrink-0 self-end sm:self-auto text-[11px] text-slate-500 font-mono">
            <span v-if="item.ip_address" class="px-1.5 py-0.2 rounded bg-slate-50 border border-slate-200/80 text-slate-600">
              {{ item.ip_address }}
            </span>
            <span class="text-slate-600 font-medium">{{ formatTimeAgo(item.created_at) }}</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
