<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { Cpu, HardDrive, MemoryStick, Clock, RefreshCw, Plus, ArrowRight, AlertCircle } from 'lucide-vue-next'
import { useApi } from '../composables/useApi'
import { summarizeInventory, summarizeUsage, formatBytes, formatUptime } from '../lib/overview-stats.mjs'
const { apiFetch } = useApi()
const containers = ref<any[] | null>(null)
const metrics = ref<any>(null)
const status = ref<any>(null)
const events = ref<any[]>([])
const loading = ref(true)
const error = ref('')
const metricsError = ref('')
const statusError = ref('')
const auditError = ref('')
const lastUpdated = ref<Date | null>(null)
const attentionPanel = ref<HTMLElement | null>(null)
const stats = computed(() => summarizeInventory(containers.value || []))
const engine = computed(() => !statusError.value && !error.value ? status.value?.docker_system : null)
const usage = computed(() => summarizeUsage(metrics.value, engine.value?.cpus))
const percent = (value: number | null) => value === null ? '—' : `${value.toFixed(1)}%`
const inventoryCards = computed(() => [
  { label: 'Total containers', value: stats.value.total, filter: 'all' },
  { label: 'Running', value: stats.value.running, filter: 'running' },
  { label: 'Not running', value: stats.value.stopped, filter: 'stopped' },
  { label: 'Updates available', value: stats.value.updates, filter: 'updates' },
  { label: 'Compose stacks', value: stats.value.stacks, filter: 'all' },
  { label: 'Exposure configured', value: stats.value.exposed, filter: 'exposed' },
])
const resourceCards = computed(() => {
  const host = metrics.value?.host
  return [
    { label: 'Container CPU', icon: Cpu, to: '/metrics', value: usage.value.cpuPercent !== null ? percent(usage.value.cpuPercent) : usage.value.cpuCores !== null ? `${usage.value.cpuCores.toFixed(2)} cores` : '—', progress: usage.value.cpuPercent,
      detail: usage.value.sampled ? `${usage.value.sampled}/${usage.value.running} running containers sampled${engine.value?.cpus ? ` · ${engine.value.cpus} CPUs` : ''}` : 'Waiting for container usage samples' },
    { label: 'Host memory', icon: MemoryStick, to: '/metrics', value: percent(usage.value.memoryPercent), progress: usage.value.memoryPercent,
      detail: usage.value.memoryPercent !== null ? `${formatBytes(host.mem_used)} / ${formatBytes(host.mem_total)}` : 'Memory data unavailable' },
    { label: 'Data filesystem', icon: HardDrive, to: '/storage', value: percent(usage.value.diskPercent), progress: usage.value.diskPercent,
      detail: usage.value.diskPercent !== null ? `${formatBytes(host.disk_free)} free / ${formatBytes(host.disk_total)}` : 'Filesystem data unavailable' },
    { label: 'Host uptime', icon: Clock, to: '/metrics', value: formatUptime(host?.uptime_seconds), progress: null,
      detail: host?.load_avg ? `Load average ${host.load_avg.map((v: number) => v.toFixed(2)).join(' / ')}` : 'Uptime data unavailable' },
  ]
})
const wireguardCompanion = computed(() => containers.value?.find(c => c.name === 'docktun-wireguard'))
const cloudflareState = computed(() => statusError.value ? 'Unavailable' : !status.value ? 'Loading' : status.value.running ? 'Running' : status.value.status === 'not installed' ? 'Not installed' : status.value.status || 'Unknown')
const wireguardState = computed(() => error.value ? 'Unavailable' : !containers.value ? 'Loading' : !wireguardCompanion.value ? 'Existing VPN' : wireguardCompanion.value.running ? 'Companion running' : 'Companion stopped')
let pending = false
let timer: ReturnType<typeof setInterval>
async function refresh() {
  if (pending) return
  pending = true
  loading.value = true
  try {
    const [inventory, resource, service, audit] = await Promise.allSettled([
      apiFetch<any[]>('/api/docker/containers'), apiFetch<any>('/api/metrics'),
      apiFetch<any>('/api/status'), apiFetch<{ entries: any[] }>('/api/audit?limit=6'),
    ])
    if (inventory.status === 'fulfilled') { containers.value = inventory.value; error.value = '' }
    else error.value = inventory.reason?.message || 'Unable to load Docker containers.'
    if (resource.status === 'fulfilled') { metrics.value = resource.value; metricsError.value = '' }
    else metricsError.value = resource.reason?.message || 'Unable to load resource usage.'
    if (service.status === 'fulfilled') { status.value = service.value; statusError.value = '' }
    else statusError.value = service.reason?.message || 'Unable to read Docker and connector status.'
    if (audit.status === 'fulfilled') { events.value = audit.value.entries || []; auditError.value = '' }
    else auditError.value = 'Recent activity could not be refreshed.'
    if (inventory.status === 'fulfilled' && resource.status === 'fulfilled') lastUpdated.value = new Date()
  } finally { pending = false; loading.value = false }
}
function eventTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleString()
}
onMounted(() => {
  refresh()
  timer = setInterval(() => { if (document.visibilityState === 'visible') refresh() }, 15000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div class="space-y-6">
    <header class="flex flex-wrap items-center justify-between gap-4">
      <div><div class="flex flex-wrap items-center gap-3"><h1 class="text-2xl font-bold text-slate-900">Docker overview</h1><button v-if="stats.attention.length" class="rounded-full border border-amber-200 bg-amber-50 px-3 py-1 text-xs font-medium text-amber-800" @click="attentionPanel?.scrollIntoView({ behavior: 'smooth', block: 'start' })">{{ stats.attention.length }} need attention</button></div><p class="mt-1 text-sm text-slate-600">Container health, resource usage, and optional exposure at a glance.</p><p class="mt-1 text-xs text-slate-500">Refreshes every 15 seconds<span v-if="lastUpdated"> · Inventory and usage updated {{ lastUpdated.toLocaleTimeString() }}</span></p></div>
      <div class="flex gap-2"><button class="cyber-btn-secondary" :disabled="loading" @click="refresh" aria-label="Refresh overview"><RefreshCw class="h-4 w-4" :class="{ 'animate-spin': loading }" /></button><router-link to="/containers?launch=true" class="cyber-btn inline-flex items-center gap-2"><Plus class="h-4 w-4" />Create container</router-link></div>
    </header>
    <p v-if="error" role="alert" class="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700">{{ error }} {{ containers ? 'Previous inventory is shown.' : 'Inventory is unavailable.' }}</p>
    <div class="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-6">
      <router-link v-for="item in inventoryCards" :key="item.label" :to="{ path: '/containers', query: { filter: item.filter } }" class="cyber-card block !p-4 hover:border-indigo-300"><p class="text-xs text-slate-600">{{ item.label }}</p><p class="mt-2 text-2xl font-semibold text-slate-900">{{ containers ? item.value : '—' }}</p></router-link>
    </div>
    <section class="space-y-3" aria-label="System resource usage">
      <div class="flex flex-wrap items-center justify-between gap-2"><h2 class="font-semibold">Resources</h2><router-link to="/metrics" class="text-sm text-indigo-700">Detailed metrics →</router-link></div>
      <p v-if="metricsError" role="status" class="text-sm text-amber-800">{{ metricsError }} {{ metrics ? 'Previous samples are shown.' : 'No resource samples are available.' }}</p>
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <router-link v-for="card in resourceCards" :key="card.label" :to="card.to" class="cyber-card block space-y-3 hover:border-indigo-300">
          <div class="flex items-center justify-between text-sm text-slate-600"><span>{{ card.label }}</span><component :is="card.icon" class="h-4 w-4 text-indigo-600" /></div>
          <p class="text-2xl font-semibold text-slate-900">{{ card.value }}</p>
          <div v-if="card.progress !== null" role="progressbar" :aria-label="card.label" :aria-valuenow="Math.min(100, Math.max(0, card.progress))" aria-valuemin="0" aria-valuemax="100" class="h-1.5 overflow-hidden rounded-full bg-slate-100"><div class="h-full rounded-full" :class="card.progress >= 90 ? 'bg-amber-500' : 'bg-indigo-500'" :style="{ width: `${Math.min(100, Math.max(0, card.progress))}%` }"></div></div>
          <p class="text-xs text-slate-500">{{ card.detail }}</p>
        </router-link>
      </div>
      <p class="text-xs text-slate-500">CPU covers containers, excluding other host processes. Memory and uptime describe the Linux host or Docker VM; disk capacity covers the filesystem serving DockTun’s data.</p>
      <div class="flex flex-wrap gap-x-4 gap-y-1 rounded-lg border border-slate-200 bg-white px-4 py-3 text-xs text-slate-600">
        <span class="font-medium" :class="engine?.server_version ? 'text-emerald-700' : 'text-slate-500'">Docker {{ engine?.server_version || (loading ? 'loading…' : 'unavailable') }}</span><span v-if="engine?.operating_system">{{ engine.operating_system }}</span><span v-if="engine?.architecture">{{ engine.architecture }}</span><span v-if="engine?.cpus">{{ engine.cpus }} CPUs</span><span v-if="engine?.server_version">{{ engine.images_count ?? 0 }} images</span>
      </div>
      <p v-if="statusError" class="text-xs text-amber-800">{{ statusError }}</p>
    </section>
    <div class="grid items-start gap-6 xl:grid-cols-2">
      <section class="cyber-card space-y-4">
        <div class="flex items-center justify-between gap-2"><h2 class="font-semibold">Busiest containers</h2><router-link to="/metrics" class="text-sm text-indigo-700">All usage →</router-link></div>
        <p v-if="metricsError" class="text-xs text-amber-800">Usage could not be refreshed.</p>
        <p v-if="!usage.busiest.length" class="text-sm text-slate-500">{{ containers && !stats.running ? 'No running containers.' : 'Waiting for CPU samples from running containers.' }}</p>
        <div v-else class="overflow-x-auto"><table class="w-full text-left text-sm"><thead><tr class="border-b border-slate-200 text-xs text-slate-500"><th class="pb-2 pr-3 font-medium">Container</th><th class="pb-2 pr-3 font-medium">CPU</th><th class="pb-2 pr-3 font-medium">Memory</th><th class="pb-2 font-medium">Network I/O</th></tr></thead><tbody><tr v-for="container in usage.busiest" :key="container.id || container.name" class="border-b border-slate-100 last:border-0"><td class="py-3 pr-3"><router-link :to="{ path: '/containers', query: { name: container.name } }" class="font-medium text-indigo-700 hover:underline">{{ container.name }}</router-link></td><td class="whitespace-nowrap py-3 pr-3 tabular-nums">{{ container.cpu.toFixed(1) }}%</td><td class="whitespace-nowrap py-3 pr-3 tabular-nums">{{ container.mem_usage || '—' }}</td><td class="whitespace-nowrap py-3 text-xs tabular-nums">{{ container.net_io || '—' }}</td></tr></tbody></table></div>
        <p class="text-xs text-slate-500">Sorted by CPU; 100% in this table means one core. Network I/O is cumulative, not a transfer rate.</p>
      </section>
      <section class="cyber-card space-y-4">
        <div class="flex items-center justify-between"><h2 class="font-semibold">Exposure overview</h2><router-link to="/access" class="text-sm text-indigo-700">Providers →</router-link></div>
        <p class="text-xs text-slate-500">All methods are optional. App counts reflect saved settings.</p>
        <router-link to="/access?tab=cloudflare" class="block rounded-lg border border-slate-200 p-3 hover:border-indigo-300"><div class="flex flex-wrap items-center justify-between gap-2"><span class="text-sm font-medium">Cloudflare</span><span class="rounded-full bg-slate-100 px-2 py-0.5 text-xs" :class="status?.running && !statusError ? 'text-emerald-700' : 'text-slate-600'">{{ cloudflareState }}</span></div><p class="mt-1 text-xs text-slate-500">{{ containers ? stats.cloudflare : '—' }} apps configured<span v-if="status && !statusError"> · {{ status.ingress_count }} local ingress hostnames</span></p></router-link>
        <router-link to="/access?tab=tailscale" class="block rounded-lg border border-slate-200 p-3 hover:border-indigo-300"><div class="flex items-center justify-between gap-2"><span class="text-sm font-medium">Tailscale</span><span class="text-xs text-slate-600">Per-app devices</span></div><p class="mt-1 text-xs text-slate-500">{{ containers ? stats.tailscale : '—' }} apps configured · {{ containers ? stats.tailscaleProxiesRunning : '—' }} app proxies running</p><p class="mt-1 text-xs text-slate-500">Device sign-in and connectivity are checked in each app’s Access tab.</p></router-link>
        <router-link to="/access?tab=wireguard" class="block rounded-lg border border-slate-200 p-3 hover:border-indigo-300"><div class="flex flex-wrap items-center justify-between gap-2"><span class="text-sm font-medium">WireGuard</span><span class="text-xs text-slate-600">{{ wireguardState }}</span></div><p class="mt-1 text-xs text-slate-500">{{ containers ? stats.wireguard : '—' }} saved VPN addresses · VPN routing is configured separately.</p></router-link>
        <router-link to="/containers?filter=exposed" class="inline-flex items-center gap-2 text-sm text-indigo-700">Manage app exposure <ArrowRight class="h-4 w-4" /></router-link>
      </section>
    </div>
    <section ref="attentionPanel" class="cyber-card space-y-4">
      <div class="flex flex-wrap items-center justify-between gap-2"><h2 class="font-semibold">Needs attention <span v-if="containers" class="ml-1 text-sm text-slate-500">{{ stats.attention.length }}</span></h2><router-link to="/containers" class="text-sm text-indigo-700">All containers →</router-link></div>
      <p v-if="error" class="text-sm text-slate-600">Refresh to check current health.</p>
      <p v-else-if="!stats.attention.length" class="text-sm text-slate-600">{{ !containers ? 'Loading containers…' : stats.total ? 'No restarting, dead, or unhealthy containers reported.' : 'No containers found. Create a container or import a Compose stack to get started.' }}</p>
      <p v-if="stats.paused" class="text-xs text-slate-500">{{ stats.paused }} paused containers; paused and restarting states are included in the not-running count.</p>
      <router-link v-for="container in stats.attention.slice(0, 6)" :key="container.id" :to="{ path: '/containers', query: { name: container.name } }" class="flex items-center justify-between gap-4 rounded-lg border border-amber-200 bg-amber-50 p-3"><span class="flex items-center gap-2 text-sm font-medium"><AlertCircle class="h-4 w-4 text-amber-700" />{{ container.name }}</span><span class="text-xs text-slate-600">{{ container.status || container.state }}</span></router-link>
      <p v-if="stats.attention.length > 6" class="text-xs text-slate-500">{{ stats.attention.length - 6 }} more containers need attention. Open the container list to review them.</p>
    </section>
    <section class="cyber-card space-y-4">
      <div class="flex items-center justify-between"><h2 class="font-semibold">Recent activity</h2><router-link to="/logs?tab=audit" class="text-sm text-indigo-700">View audit log →</router-link></div>
      <p v-if="auditError" class="text-sm text-amber-800">{{ auditError }} {{ events.length ? 'Previous activity is shown.' : '' }}</p>
      <p v-else-if="!events.length" class="text-sm text-slate-500">No recent activity to show.</p>
      <div v-for="(event, index) in events" :key="event.id || index" class="flex flex-wrap justify-between gap-2 border-t border-slate-100 pt-3 text-sm"><span class="text-slate-700">{{ event.details || event.action }}</span><time class="text-xs text-slate-500">{{ eventTime(event.created_at || event.timestamp) }}</time></div>
    </section>
  </div>
</template>
