<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
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
  Box,
  HardDrive,
  ChevronDown,
  ChevronRight,
  Sliders,
  Code2
} from 'lucide-vue-next'
import { cloudflareEnabled as isCloudflareEnabled } from '../lib/tunnel-provider.mjs'
import { useApi } from '../composables/useApi'

const { apiFetch } = useApi()
const router = useRouter()

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
    const res = await apiFetch<{ entries: any[] }>('/api/audit?limit=8')
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

// Stacks Filter & Grouping State
interface DashboardStackGroup {
  key: string
  name: string
  isStack: boolean
  projectId?: number
  projectPath?: string
  containers: any[]
  runningCount: number
  totalCount: number
  totalCpu: string
  hasUpdates: boolean
}

const stackFilter = ref<string>('all')
const collapsedStacks = ref<Record<string, boolean>>({})

function toggleStackCollapse(key: string) {
  collapsedStacks.value[key] = !collapsedStacks.value[key]
}

function expandAllStacks() {
  collapsedStacks.value = {}
}

function collapseAllStacks() {
  const next: Record<string, boolean> = {}
  dashboardStackGroups.value.forEach((g) => {
    next[g.key] = true
  })
  collapsedStacks.value = next
}

const areAllCollapsed = computed(() => {
  if (dashboardStackGroups.value.length === 0) return false
  return dashboardStackGroups.value.every((g) => collapsedStacks.value[g.key])
})

const stackActionLoading = ref<Record<number, string>>({})

async function runStackAction(projectId: number, action: 'up' | 'restart' | 'stop') {
  if (!projectId) return
  stackActionLoading.value[projectId] = action
  try {
    await apiFetch(`/api/projects/${projectId}/action/${action}`, { method: 'POST' })
    await fetchContainers()
  } catch (e: any) {
    alert(e.message || `Failed to ${action} stack`)
  } finally {
    delete stackActionLoading.value[projectId]
  }
}

const availableStacks = computed(() => {
  const set = new Set<string>()
  for (const c of containers.value) {
    let pName = c.project_name ? c.project_name.trim() : ''
    const cLower = c.name.toLowerCase()
    if (!pName && (cLower.startsWith('docktun') || cLower === 'cloudflared' || cLower === 'wireguard' || cLower === 'tailscale')) {
      pName = 'docktun'
    }
    if (pName && pName !== 'public-cloudflare-tunnel-cloudflared' && !pName.includes('cloudflare-tunnel')) {
      set.add(pName)
    }
  }
  for (const p of projects.value) {
    if (p.name && p.name !== 'public-cloudflare-tunnel-cloudflared' && !p.name.includes('cloudflare-tunnel') && !p.name.startsWith('docktun-')) {
      set.add(p.name)
    }
  }
  return Array.from(set).sort((a, b) => {
    if (a === 'docktun') return -1
    if (b === 'docktun') return 1
    return a.localeCompare(b)
  })
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
    
    // Canonical stack name resolution for filtering
    let cProj = c.project_name || ''
    const cLower = c.name.toLowerCase()
    if (
      cLower.startsWith('docktun') ||
      cLower === 'wireguard' ||
      cLower === 'cloudflared' ||
      cLower === 'tailscale' ||
      cLower === 'wg-easy' ||
      cLower === 'zeroconf' ||
      cProj === 'docktun' ||
      cProj === 'tun'
    ) {
      cProj = 'docktun'
    }

    if (stackFilter.value !== 'all') {
      if (stackFilter.value === '__standalone__' && cProj) return false
      if (stackFilter.value !== '__standalone__' && cProj !== stackFilter.value) return false
    }
    if (!searchQuery.value) return true
    const q = searchQuery.value.toLowerCase()
    return (
      c.name.toLowerCase().includes(q) ||
      (c.hostname && c.hostname.toLowerCase().includes(q)) ||
      cProj.toLowerCase().includes(q)
    )
  })
})

// Compute Grouped Stacks for the Dashboard
const dashboardStackGroups = computed<DashboardStackGroup[]>(() => {
  const list = filteredContainers.value
  const map = new Map<string, any[]>()

  list.forEach((c) => {
    let key = c.project_name ? c.project_name.trim() : ''
    const lower = c.name.toLowerCase()
    if (
      !key ||
      lower.startsWith('docktun') ||
      lower === 'wireguard' ||
      lower === 'cloudflared' ||
      lower === 'tailscale' ||
      lower === 'wg-easy' ||
      lower === 'zeroconf'
    ) {
      if (
        lower.startsWith('docktun') ||
        lower === 'wireguard' ||
        lower === 'cloudflared' ||
        lower === 'tailscale' ||
        lower === 'wg-easy' ||
        lower === 'zeroconf' ||
        key === 'docktun' ||
        key === 'tun'
      ) {
        key = 'docktun'
      }
    }
    if (!key) {
      key = '__standalone__'
    }
    if (!map.has(key)) {
      map.set(key, [])
    }
    map.get(key)!.push(c)
  })

  const groups: DashboardStackGroup[] = []
  map.forEach((items, key) => {
    let cpuSum = 0
    items.forEach((c) => {
      if (c.running && c.cpu) {
        const p = parseFloat(c.cpu.replace('%', ''))
        if (!isNaN(p)) cpuSum += p
      }
    })

    const isStack = key !== '__standalone__'
    const firstWithProj = items.find((c) => c.project_id)
    const projectId = firstWithProj?.project_id
    const projectPath = firstWithProj?.project_path

    let groupName = isStack ? key : 'Standalone Containers'
    if (key === 'docktun') {
      groupName = 'DockTun Core'
    }

    groups.push({
      key,
      name: groupName,
      isStack,
      projectId,
      projectPath,
      containers: items,
      runningCount: items.filter((c) => c.running).length,
      totalCount: items.length,
      totalCpu: cpuSum.toFixed(1) + '%',
      hasUpdates: items.some((c) => c.update_available),
    })
  })

  groups.sort((a, b) => {
    if (a.key === 'docktun') return -1
    if (b.key === 'docktun') return 1
    if (a.isStack && !b.isStack) return -1
    if (!a.isStack && b.isStack) return 1
    return a.name.localeCompare(b.name)
  })

  return groups
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

const cloudflareEnabled = computed(() => isCloudflareEnabled(status.value))

const isTunnelRunning = computed(() => {
  return status.value?.status === 'running' || status.value?.status === 'healthy'
})

let fastTimer: any = null
let slowTimer: any = null

onMounted(async () => {
  await refreshAll()
  // Fast telemetry polling (status & containers with cached stats) every 5s
  fastTimer = setInterval(() => {
    if (document.visibilityState === 'visible') {
      fetchStatus()
      fetchContainers()
    }
  }, 5000)

  // Heavy TCP health dials and remote access every 15s
  slowTimer = setInterval(() => {
    if (document.visibilityState === 'visible') {
      fetchHealth()
      fetchRemoteAccess()
    }
  }, 15000)
})

onUnmounted(() => {
  if (fastTimer) clearInterval(fastTimer)
  if (slowTimer) clearInterval(slowTimer)
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
        </h1>
      </div>

      <div class="flex items-center gap-2 flex-wrap">
        <button
          v-if="cloudflareEnabled"
          @click="triggerDnsSync"
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
      v-if="cloudflareEnabled && !isTunnelRunning && status"
      class="rounded-xl border border-rose-500/30 bg-rose-500/10 p-4 text-xs text-rose-200 flex flex-col sm:flex-row sm:items-start justify-between gap-3 shadow-lg"
    >
      <div class="flex items-start gap-3">
        <AlertCircle class="h-5 w-5 text-rose-600 mt-0.5 shrink-0" />
        <div class="space-y-2">
          <p class="font-bold text-rose-700 text-sm">
            Cloudflare Tunnel is {{ status?.status || 'degraded' }}
          </p>
          <p v-if="status?.last_error" class="font-mono text-[11px] text-rose-200/90 bg-slate-50/80 p-2.5 rounded-lg border border-rose-900/50 whitespace-pre-wrap">
            {{ status.last_error }}
          </p>
          <div class="flex items-center gap-3 pt-1 text-[11px]">
            <router-link to="/access?tab=cloudflare" class="text-indigo-600 hover:underline font-semibold flex items-center gap-1">
              <span>Inspect Tunnel Ingress Config &rarr;</span>
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
      <div v-if="cloudflareEnabled" class="cyber-card flex flex-col justify-between">
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
          <span class="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] text-indigo-700 font-mono font-medium">
            {{ status?.ha_connections ? `${status.ha_connections} HA Links` : (status?.protocol || 'HTTP/2') }}
          </span>
        </div>
      </div>

      <!-- 2. Traffic & Origin Health Card -->
      <div class="cyber-card flex flex-col justify-between">
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
          <span class="text-slate-600">Active edge streams:</span>
          <span class="font-mono text-emerald-600 font-bold">
            {{ status?.active_connections || 0 }} live
          </span>
        </div>
      </div>

      <!-- 3. Container Fleet & Stacks Card -->
      <div class="cyber-card flex flex-col justify-between">
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
            <span class="text-[11px] font-mono text-purple-700 bg-purple-500/10 border border-purple-500/20 px-2 py-0.2 rounded">
              {{ dashboardStackGroups.length }} Stacks
            </span>
            <span :class="containerStats.stopped > 0 ? 'text-amber-600 font-medium' : 'text-slate-500'" class="text-[11px] font-mono">
              {{ containerStats.stopped }} stopped
            </span>
          </div>
        </div>

        <div class="mt-3 border-t border-slate-200/80 pt-2 flex items-center justify-between text-xs text-slate-600">
          <div>
            <router-link
              v-if="containerStats.updatesCount > 0"
              to="/containers"
              class="inline-flex items-center gap-1 rounded bg-amber-500/20 border border-amber-500/40 px-1.5 py-0.2 text-[10px] font-bold text-amber-700 animate-pulse hover:bg-amber-500/30"
              title="Click to view containers with updates available"
            >
              <Sparkles class="h-2.5 w-2.5" />
              {{ containerStats.updatesCount }} update{{ containerStats.updatesCount > 1 ? 's' : '' }}
            </router-link>
            <span v-else class="text-[11px] text-slate-500 font-mono">
              All up to date
            </span>
          </div>
          <router-link to="/containers" class="text-indigo-600 hover:underline">Manage &rarr;</router-link>
        </div>
      </div>

      <!-- 4. Combined Resource Load Card (Clickable to /metrics) -->
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
            <span>Engine: v{{ status?.docker_system?.server_version || '—' }} ({{ status?.docker_system?.architecture || 'x86_64' }})</span>
            <span class="text-purple-700 font-semibold">{{ status?.docker_system?.cpus ? `${status.docker_system.cpus} vCPU` : '' }}</span>
          </div>
        </div>

        <!-- Progress Bar & Link -->
        <div class="mt-3 border-t border-slate-200/80 pt-2 space-y-1">
          <div class="flex items-center justify-between text-[10px] font-mono text-slate-500">
            <span>Workload</span>
            <span class="text-indigo-600 group-hover:underline flex items-center gap-0.5">Metrics &rarr;</span>
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

    <!-- Remote Access Multi-Pillar Connectivity Summary Banner -->
    <div class="cyber-card p-4 bg-white/40 border-slate-200/90">
      <div class="flex flex-col lg:flex-row lg:items-center justify-between gap-4">
        <div class="flex items-center gap-3">
          <div class="p-2.5 rounded-xl bg-indigo-500/10 text-indigo-600 border border-indigo-500/20 shrink-0">
            <Globe class="h-5 w-5" />
          </div>
          <div>
            <div class="flex items-center gap-2">
              <h3 class="text-xs font-semibold text-slate-900">Remote Access & Mesh Connectivity</h3>
              <span class="text-[10px] font-mono text-indigo-700 bg-indigo-500/10 border border-indigo-500/20 px-1.5 py-0.2 rounded">
                Ingress Hub
              </span>
            </div>
            <p class="text-[11px] text-slate-600 mt-0.5">Unified secure ingress across Cloudflare edge tunnels, Tailscale zero-trust, and WireGuard VPN</p>
          </div>
        </div>

        <div class="flex items-center gap-2.5 flex-wrap">
          <!-- Cloudflare Badge Link -->
          <router-link
            to="/access?tab=cloudflare"
            class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-slate-50 border border-slate-200 text-xs font-mono hover:border-orange-500/50 hover:bg-white transition group"
            title="Configure Cloudflare Tunnel Ingress Routes"
          >
            <span class="h-2 w-2 rounded-full" :class="isTunnelRunning ? 'bg-emerald-400' : 'bg-rose-400'"></span>
            <span class="text-slate-600 group-hover:text-slate-800">Cloudflare:</span>
            <span class="text-orange-700 font-semibold">{{ status?.hostnames?.length || 0 }} routes</span>
          </router-link>

          <!-- Tailscale Badge Link -->
          <router-link
            to="/access?tab=tailscale"
            class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-slate-50 border border-slate-200 text-xs font-mono hover:border-purple-500/50 hover:bg-white transition group"
            title="Configure Tailscale Zero-Trust Mesh"
          >
            <span
              class="h-2 w-2 rounded-full"
              :class="remoteAccessOverview?.tailscale?.backend_state === 'Running' ? 'bg-emerald-400' : 'bg-slate-500'"
            ></span>
            <span class="text-slate-600 group-hover:text-slate-800">Tailscale:</span>
            <span class="text-purple-700 font-semibold">
              {{ remoteAccessOverview?.tailscale?.backend_state === 'Running' ? 'Connected' : 'Stopped' }}
            </span>
          </router-link>

          <!-- WireGuard Badge Link -->
          <router-link
            to="/access?tab=wireguard"
            class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-slate-50 border border-slate-200 text-xs font-mono hover:border-emerald-500/50 hover:bg-white transition group"
            title="Configure WireGuard Peer VPN"
          >
            <span
              class="h-2 w-2 rounded-full"
              :class="remoteAccessOverview?.wireguard?.running ? 'bg-emerald-400' : 'bg-slate-500'"
            ></span>
            <span class="text-slate-600 group-hover:text-slate-800">WireGuard:</span>
            <span class="text-emerald-700 font-semibold">
              {{ remoteAccessOverview?.wireguard?.running ? `${remoteAccessOverview.wireguard.peers_count} peers` : 'Inactive' }}
            </span>
          </router-link>

          <!-- Reworked Control Button -->
          <router-link
            to="/access"
            class="cyber-btn-secondary text-xs flex items-center gap-1.5 py-1.5 px-3 text-indigo-700 border-indigo-500/30 hover:bg-indigo-600/10 hover:border-indigo-400 transition"
            title="Open Ingress & Mesh Control Center"
          >
            <ShieldCheck class="h-3.5 w-3.5 text-indigo-600" />
            <span class="font-medium">Configure Ingress & Mesh</span>
            <ArrowRight class="h-3.5 w-3.5 ml-0.5" />
          </router-link>
        </div>
      </div>
    </div>

    <!-- Combined Ingress Routing & Origin Health Directory -->
    <div class="cyber-card space-y-4">
      <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-200/80 pb-3">
        <div class="flex items-center gap-2">
          <Layers class="h-4 w-4 text-indigo-600" />
          <h2 class="text-xs font-semibold text-slate-900">
            Ingress Routing & Origin Health
          </h2>
        </div>

        <div class="flex items-center gap-2 flex-wrap">
          <!-- Route Switcher Tabs -->
          <div class="flex items-center gap-1 rounded-lg border border-slate-200 bg-slate-50 p-1 text-xs">
            <button
              @click="routesTab = 'public'"
              class="flex items-center gap-1.5 rounded-md px-3 py-1 font-medium transition"
              :class="routesTab === 'public' ? 'bg-slate-100 text-slate-900 shadow-sm' : 'text-slate-600 hover:text-slate-800'"
            >
              <Globe class="h-3.5 w-3.5 text-orange-600" />
              <span>Public Hostnames (Cloudflare)</span>
              <span class="rounded bg-white px-1.5 py-0.2 text-[10px] text-orange-700 font-mono">{{ publicRoutes.length }}</span>
            </button>

            <button
              @click="routesTab = 'local'"
              class="flex items-center gap-1.5 rounded-md px-3 py-1 font-medium transition"
              :class="routesTab === 'local' ? 'bg-slate-100 text-slate-900 shadow-sm' : 'text-slate-600 hover:text-slate-800'"
            >
              <Wifi class="h-3.5 w-3.5 text-indigo-600" />
              <span>Local Network (.local)</span>
              <span class="rounded bg-white px-1.5 py-0.2 text-[10px] text-indigo-700 font-mono">{{ localServices.length }}</span>
            </button>
          </div>

          <!-- Actions -->
          <button
            @click="fetchHealth"
            :disabled="probingHealth"
            class="cyber-btn-secondary text-xs flex items-center gap-1 py-1 px-2.5 text-slate-700"
            title="Probe live latency of ingress origins"
          >
            <RotateCcw class="h-3 w-3" :class="probingHealth ? 'animate-spin' : ''" />
            <span>{{ probingHealth ? 'Probing…' : 'Probe Latency' }}</span>
          </button>
          <router-link
            to="/access?tab=cloudflare"
            class="cyber-btn-secondary text-xs flex items-center gap-1 py-1 px-2.5 text-indigo-700 hover:text-slate-900"
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
            class="flex flex-col justify-between rounded-xl border border-slate-200/90 bg-slate-50 p-3.5 hover:border-slate-300 transition group shadow-sm"
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

      <!-- Tab 2: Local & Network Services (.local / Host Port) -->
      <div v-else class="space-y-3">
        <div v-if="localServices.length === 0" class="py-6 text-center text-xs text-slate-500 font-mono">
          No active running containers found.
        </div>
        <div v-else class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          <div
            v-for="svc in localServices"
            :key="svc.id"
            class="flex items-center justify-between rounded-xl border border-slate-200 bg-slate-50 p-3 hover:border-slate-300 transition group"
          >
            <div class="truncate pr-2">
              <div class="flex items-center gap-1.5">
                <span class="h-2 w-2 rounded-full bg-emerald-400 shrink-0"></span>
                <router-link
                  :to="'/containers?id=' + encodeURIComponent(svc.id)"
                  class="font-mono text-xs font-semibold text-slate-800 hover:text-slate-900 truncate hover:underline"
                  :title="`Inspect container '${svc.name}' in Control Center`"
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

    <!-- Grouped Container Fleet & Managed Compose Stacks -->
    <div class="cyber-card space-y-4">
      <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-200/80 pb-3">
        <div class="flex items-center gap-2 flex-wrap">
          <Server class="h-4 w-4 text-emerald-600" />
          <h2 class="text-xs font-semibold text-slate-900">
            Container Fleet & Managed Stacks
          </h2>
          <span class="text-[10px] px-2 py-0.5 rounded-full bg-slate-100 text-slate-700 font-mono">
            {{ filteredContainers.length }} / {{ containers.length }} containers
          </span>
          <span v-if="dashboardStackGroups.length > 0" class="text-[10px] px-2 py-0.5 rounded-full bg-purple-500/10 text-purple-700 border border-purple-500/30 font-mono">
            {{ dashboardStackGroups.length }} stacks
          </span>
        </div>

        <!-- Search & Filter Controls -->
        <div class="flex items-center gap-2 flex-wrap">
          <div class="relative w-40 sm:w-48">
            <Search class="h-3.5 w-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-slate-500" />
            <input
              v-model="searchQuery"
              type="text"
              placeholder="Search services…"
              class="w-full pl-8 pr-2.5 py-1 text-xs bg-slate-50 border border-slate-200 rounded-lg text-slate-800 placeholder:text-slate-500 focus:outline-none focus:border-indigo-500 font-mono"
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
            v-model="containerFilter"
            class="text-xs bg-slate-50 border border-slate-200 rounded-lg px-2 py-1 text-slate-700 focus:outline-none focus:border-indigo-500 font-mono"
          >
            <option value="all">All States</option>
            <option value="running">Running</option>
            <option value="stopped">Stopped</option>
            <option value="updates">Updates</option>
          </select>

          <!-- Expand / Collapse All Toggle -->
          <button
            @click="areAllCollapsed ? expandAllStacks() : collapseAllStacks()"
            class="cyber-btn-secondary text-xs flex items-center gap-1 py-1 px-2.5 text-slate-700"
            :title="areAllCollapsed ? 'Expand all stacks' : 'Collapse all stacks'"
          >
            <component :is="areAllCollapsed ? ChevronRight : ChevronDown" class="h-3.5 w-3.5" />
            <span>{{ areAllCollapsed ? 'Expand' : 'Collapse' }}</span>
          </button>
        </div>
      </div>

      <!-- Empty State -->
      <div v-if="dashboardStackGroups.length === 0" class="py-8 text-center text-xs text-slate-500 font-mono">
        No containers or stacks match the current filter or search criteria.
      </div>

      <!-- Grouped Stacks Accordion Cards -->
      <div v-else class="space-y-3">
        <div
          v-for="group in dashboardStackGroups"
          :key="group.key"
          class="rounded-xl border border-slate-200/90 bg-slate-50/70 overflow-hidden transition-all duration-200 hover:border-slate-300/80 shadow-sm"
        >
          <!-- Stack Header Bar -->
          <div
            @click="toggleStackCollapse(group.key)"
            class="flex flex-col sm:flex-row sm:items-center justify-between px-3.5 py-2.5 gap-2.5 bg-white/60 cursor-pointer select-none transition hover:bg-white/90"
          >
            <!-- Left: Toggle Chevron + Stack Icon + Title + Metrics -->
            <div class="flex items-center gap-2.5 flex-wrap">
              <component
                :is="collapsedStacks[group.key] ? ChevronRight : ChevronDown"
                class="h-4 w-4 text-slate-600 hover:text-indigo-600 transition-colors"
              />
              <component
                :is="group.key === 'docktun' ? ShieldCheck : (group.isStack ? Layers : Box)"
                class="h-4 w-4"
                :class="group.key === 'docktun' ? 'text-cyan-600' : (group.isStack ? 'text-indigo-600' : 'text-slate-600')"
              />
              <span class="font-mono text-xs font-bold text-slate-900">
                {{ group.name }}
              </span>

              <!-- Badge: Core or Compose -->
              <span
                v-if="group.key === 'docktun'"
                class="rounded bg-cyan-500/10 border border-cyan-500/30 px-1.5 py-0.2 text-[10px] font-mono text-cyan-700 font-semibold"
              >
                Core Stack
              </span>
              <span
                v-else-if="group.isStack"
                class="rounded bg-indigo-500/10 border border-indigo-500/30 px-1.5 py-0.2 text-[10px] font-mono text-indigo-700"
              >
                Compose Stack
              </span>

              <!-- Running count pill -->
              <span
                class="rounded-full px-2 py-0.2 text-[10px] font-mono font-semibold"
                :class="group.runningCount === group.totalCount && group.totalCount > 0
                  ? 'bg-emerald-500/10 text-emerald-600 border border-emerald-500/30'
                  : group.runningCount > 0
                    ? 'bg-amber-500/10 text-amber-600 border border-amber-500/30'
                    : 'bg-slate-100 text-slate-600 border border-slate-300'"
              >
                {{ group.runningCount }}/{{ group.totalCount }} running
              </span>

              <!-- Stack CPU (Clickable to /metrics) -->
              <router-link
                v-if="group.runningCount > 0"
                to="/metrics"
                class="text-[10px] font-mono text-indigo-700 bg-white px-1.5 py-0.2 rounded border border-slate-200 hover:border-indigo-400 hover:bg-indigo-50 transition"
                :title="`View live telemetry for ${group.name} stack in Metrics`"
              >
                CPU: {{ group.totalCpu }} &rarr;
              </router-link>

              <!-- Updates pill -->
              <span
                v-if="group.hasUpdates"
                class="rounded-full bg-amber-500/20 border border-amber-500/40 px-1.5 py-0.2 text-[10px] font-bold text-amber-700 animate-pulse"
              >
                Update Available
              </span>
            </div>

            <!-- Right: Stack Compose Actions -->
            <div class="flex items-center gap-1.5 self-end sm:self-auto" @click.stop>
              <!-- Compose link if project exists -->
              <router-link
                v-if="group.projectId"
                :to="'/containers?project=' + group.projectId + '&tab=compose'"
                class="cyber-btn-secondary text-[11px] flex items-center gap-1 py-1 px-2 text-indigo-700 hover:text-slate-900"
                title="Edit Compose YAML & environment"
              >
                <Code2 class="h-3 w-3" />
                <span>Compose</span>
              </router-link>

              <!-- Restart Stack action -->
              <button
                v-if="group.projectId"
                @click="runStackAction(group.projectId, 'restart')"
                :disabled="!!stackActionLoading[group.projectId]"
                class="cyber-btn-secondary text-[11px] flex items-center gap-1 py-1 px-2 text-slate-700 hover:text-slate-900"
                :title="`Restart all services in stack ${group.name}`"
              >
                <RotateCcw class="h-3 w-3" :class="stackActionLoading[group.projectId] === 'restart' ? 'animate-spin' : ''" />
                <span>Restart</span>
              </button>

              <button
                @click="toggleStackCollapse(group.key)"
                class="text-slate-600 hover:text-slate-900 p-1 text-xs"
                :title="collapsedStacks[group.key] ? 'Expand' : 'Collapse'"
              >
                <span class="text-[10px] font-mono uppercase">{{ collapsedStacks[group.key] ? 'Expand' : 'Hide' }}</span>
              </button>
            </div>
          </div>

          <!-- Stack Member Containers (Collapsible) -->
          <div v-show="!collapsedStacks[group.key]" class="divide-y divide-slate-200/50 p-1 bg-slate-50/40">
            <div
              v-for="c in group.containers"
              :key="c.id"
              class="flex flex-col sm:flex-row sm:items-center justify-between py-2.5 px-3 gap-2 transition hover:bg-white/50 rounded-lg"
            >
              <!-- Left: Pulsing status, Name, Exposure badge, Uptime (NO image name to keep it clean!) -->
              <div class="flex items-center gap-2.5 min-w-0 flex-1">
                <span
                  class="h-2.5 w-2.5 flex-shrink-0 rounded-full"
                  :class="c.running ? 'bg-emerald-400 shadow-sm shadow-emerald-500/50' : 'bg-slate-300'"
                ></span>

                <div class="min-w-0 flex-1">
                  <div class="flex items-center gap-2 flex-wrap">
                    <router-link
                      :to="'/containers?id=' + encodeURIComponent(c.id)"
                      class="font-mono text-xs font-bold text-slate-900 hover:text-indigo-700 truncate hover:underline"
                      :title="`Open ${c.name} in Control Center`"
                    >
                      {{ c.name }}
                    </router-link>

                    <!-- Web app link if running -->
                    <a
                      v-if="c.running && resolveContainerUrls(c).bestUrl"
                      :href="resolveContainerUrls(c).bestUrl"
                      target="_blank"
                      rel="noopener"
                      class="p-0.5 rounded text-indigo-600 hover:text-indigo-700 hover:bg-indigo-500/10 transition"
                      :title="`Open Web Application: ${resolveContainerUrls(c).bestUrl}`"
                    >
                      <ExternalLink class="h-3 w-3" />
                    </a>

                    <!-- Exposure Badge -->
                    <span
                      v-if="c.exposure_mode === 'public'"
                      class="inline-flex items-center gap-1 rounded-full px-2 py-0.2 text-[9px] font-mono font-semibold bg-orange-500/10 text-orange-700 border border-orange-500/30"
                    >
                      <Globe class="h-2.5 w-2.5 text-orange-600" />
                      <span>Public: {{ c.hostname }}</span>
                    </span>
                    <span
                      v-else-if="c.exposure_mode === 'private'"
                      class="inline-flex items-center gap-1 rounded-full px-2 py-0.2 text-[9px] font-mono font-semibold bg-purple-500/10 text-purple-700 border border-purple-500/30"
                    >
                      <Shield class="h-2.5 w-2.5 text-purple-600" />
                      <span>Private Mesh</span>
                    </span>
                    <span
                      v-else-if="c.running"
                      class="inline-flex items-center gap-1 rounded-full px-2 py-0.2 text-[9px] font-mono text-slate-600 bg-white border border-slate-200"
                    >
                      <Wifi class="h-2.5 w-2.5 text-indigo-600" />
                      <span>{{ c.name }}.local</span>
                    </span>
                    <span
                      v-else
                      class="rounded-full px-2 py-0.2 text-[9px] font-mono text-slate-500 bg-white"
                    >
                      Internal
                    </span>

                    <!-- Update Available Badge -->
                    <span
                      v-if="c.update_available"
                      class="rounded-full px-1.5 py-0.2 text-[9px] font-bold bg-amber-500/20 text-amber-700 border border-amber-500/40 animate-pulse"
                    >
                      Update
                    </span>
                  </div>

                  <!-- Uptime without image (Image is kept in Control Panel) -->
                  <div class="text-[11px] text-slate-600 font-mono mt-0.5 flex items-center gap-1.5">
                    <Clock class="h-2.5 w-2.5 text-slate-500" />
                    <span>{{ c.uptime || c.status }}</span>
                    <span v-if="c.ports" class="text-slate-500 truncate max-w-[200px]" :title="c.ports">
                      &bull; {{ c.ports }}
                    </span>
                  </div>
                </div>
              </div>

              <!-- Right: Live CPU/RAM & Actions -->
              <div class="flex items-center justify-between sm:justify-end gap-3 self-end sm:self-auto shrink-0">
                <!-- Live CPU/RAM Clickable Badge -->
                <router-link
                  v-if="c.running"
                  :to="`/metrics?container=${encodeURIComponent(c.name)}`"
                  class="text-right font-mono text-[11px] p-1 -m-1 rounded-md hover:bg-indigo-50/70 border border-transparent hover:border-indigo-200 transition group/metric"
                  :title="`Open real-time metrics for ${c.name}`"
                >
                  <span class="text-indigo-600 font-bold mr-2 group-hover/metric:underline">{{ c.cpu || '0.0%' }}</span>
                  <span class="text-slate-600">{{ c.memory?.split('/')[0] || '' }}</span>
                </router-link>
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
                    class="rounded-lg p-1.5 text-slate-600 hover:bg-slate-100 hover:text-indigo-700 transition"
                    :title="`Open Web UI: ${resolveContainerUrls(c).bestUrl}`"
                  >
                    <ExternalLink class="h-3.5 w-3.5" />
                  </a>

                  <router-link
                    :to="'/containers?id=' + encodeURIComponent(c.id)"
                    class="rounded-lg p-1.5 text-slate-600 hover:bg-slate-100 hover:text-indigo-700 transition"
                    :title="`Inspect container '${c.name}' in Control Center`"
                  >
                    <Sliders class="h-3.5 w-3.5" />
                  </router-link>

                  <button
                    v-if="!c.running"
                    @click="runContainerAction(c, 'start')"
                    :disabled="!!containerActionLoading[c.id]"
                    class="rounded-lg p-1.5 text-slate-600 hover:bg-slate-100 hover:text-emerald-600 transition"
                    title="Start container"
                  >
                    <Play class="h-3.5 w-3.5 fill-current" />
                  </button>
                  <button
                    v-if="c.running"
                    @click="runContainerAction(c, 'stop')"
                    :disabled="!!containerActionLoading[c.id]"
                    class="rounded-lg p-1.5 text-slate-600 hover:bg-slate-100 hover:text-rose-600 transition"
                    title="Stop container"
                  >
                    <Square class="h-3.5 w-3.5 fill-current" />
                  </button>
                  <button
                    @click="runContainerAction(c, 'restart')"
                    :disabled="!!containerActionLoading[c.id]"
                    class="rounded-lg p-1.5 text-slate-600 hover:bg-slate-100 hover:text-slate-900 transition"
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
      </div>

      <!-- Footer Bar -->
      <div class="pt-3 border-t border-slate-200/80 flex flex-col sm:flex-row sm:items-center justify-between gap-2 text-xs">
        <span class="text-slate-600 font-mono text-[11px]">
          Showing {{ dashboardStackGroups.length }} stacks ({{ filteredContainers.length }} containers)
        </span>
        <div class="flex items-center gap-3">
          <router-link
            to="/containers?tab=stacks"
            class="text-purple-600 hover:text-purple-700 font-medium flex items-center gap-1"
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

    <!-- Recent System Events (Simplified List View) -->
    <div class="cyber-card space-y-3">
      <div class="flex items-center justify-between border-b border-slate-200/80 pb-2.5">
        <div class="flex items-center gap-2">
          <Shield class="h-4 w-4 text-indigo-600" />
          <h2 class="text-xs font-semibold text-slate-900">Recent System Events</h2>
          <span v-if="recentAudits.length > 0" class="text-[10px] text-slate-500 font-mono">({{ recentAudits.length }})</span>
        </div>
        <router-link to="/audit" class="text-xs text-indigo-600 hover:text-indigo-700 hover:underline flex items-center gap-1 font-medium">
          <span>View Full Audit Log</span>
          <ArrowRight class="h-3 w-3" />
        </router-link>
      </div>

      <div v-if="recentAudits.length === 0" class="py-6 text-center text-xs text-slate-500 font-mono">
        No recent system events logged yet.
      </div>

      <!-- Scannable Vertical List -->
      <div v-else class="divide-y divide-slate-200/60">
        <div
          v-for="item in recentAudits"
          :key="item.id"
          class="py-2.5 px-2 flex flex-col sm:flex-row sm:items-center justify-between gap-2 text-xs hover:bg-white/40 rounded-lg transition group"
        >
          <div class="flex items-start sm:items-center gap-2.5 min-w-0 flex-1">
            <!-- Event Type Badge -->
            <span
              class="inline-flex items-center gap-1 text-[10px] font-semibold px-2 py-0.5 rounded-md border shrink-0"
              :class="getAuditBadge(item.action).color"
            >
              <component :is="getAuditBadge(item.action).icon" class="h-3 w-3" />
              <span>{{ getAuditBadge(item.action).label }}</span>
            </span>

            <!-- Action & Detail Description -->
            <div class="min-w-0 flex-1">
              <span class="font-semibold text-slate-800 capitalize">
                {{ (item.action || '').replace(/_/g, ' ') }}
              </span>
              <span v-if="item.detail" class="text-slate-600 ml-1.5 truncate inline-block align-bottom max-w-[280px] sm:max-w-[420px] lg:max-w-[620px]" :title="item.detail">
                — {{ item.detail }}
              </span>
            </div>
          </div>

          <!-- Meta (IP & Time) -->
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
