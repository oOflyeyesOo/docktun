<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { Cpu, HardDrive, MemoryStick, Clock, RefreshCw, Plus, ArrowRight, AlertCircle, CheckCircle2, Server, ShieldCheck, Activity } from 'lucide-vue-next'
import { useApi } from '../composables/useApi'
import { useAuthStore } from '../stores/auth'
import { summarizeInventory, summarizeUsage, formatBytes, formatUptime } from '../lib/overview-stats.mjs'
const { apiFetch } = useApi()
const authStore = useAuthStore()
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
const overallHealth = computed(() => {
  if (error.value && statusError.value) return { label: 'Connection unavailable', detail: 'DockTun could not read Docker or service state.', tone: 'rose' }
  if (stats.value.attention.length || status.value?.running === false) return { label: 'Attention required', detail: `${stats.value.attention.length || 0} container issue${stats.value.attention.length === 1 ? '' : 's'} reported.`, tone: 'amber' }
  if (!containers.value || !status.value) return { label: 'Collecting status', detail: 'Waiting for the first health snapshot.', tone: 'indigo' }
  return { label: 'All systems operational', detail: 'Docker and managed containers are responding normally.', tone: 'emerald' }
})
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
    <header class="flex flex-wrap items-end justify-between gap-4">
      <div>
        <p class="text-xs font-semibold uppercase tracking-[0.18em] text-indigo-600">Workspace status</p>
        <h1 class="mt-1 text-3xl font-bold tracking-tight text-slate-950">Good morning, {{ authStore?.username || 'admin' }}</h1>
        <p class="mt-1 text-sm text-slate-600">A live view of Docker, containers, resources, and exposure.</p>
      </div>
      <div class="flex gap-2">
        <button class="cyber-btn-secondary" :disabled="loading" @click="refresh" aria-label="Refresh overview"><RefreshCw class="h-4 w-4" :class="{ 'animate-spin': loading }" /></button>
        <router-link to="/containers?launch=true" class="cyber-btn inline-flex items-center gap-2"><Plus class="h-4 w-4" />Create container</router-link>
      </div>
    </header>

    <section class="overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm">
      <div class="flex flex-wrap items-center justify-between gap-5 border-b border-slate-100 bg-gradient-to-r from-indigo-50 via-white to-sky-50 px-5 py-5 sm:px-7">
        <div class="flex items-center gap-4">
          <div class="flex h-12 w-12 items-center justify-center rounded-2xl" :class="overallHealth.tone === 'emerald' ? 'bg-emerald-100 text-emerald-700' : overallHealth.tone === 'amber' ? 'bg-amber-100 text-amber-700' : overallHealth.tone === 'rose' ? 'bg-rose-100 text-rose-700' : 'bg-indigo-100 text-indigo-700'">
            <CheckCircle2 v-if="overallHealth.tone === 'emerald'" class="h-6 w-6" />
            <AlertCircle v-else class="h-6 w-6" />
          </div>
          <div><div class="flex flex-wrap items-center gap-2"><h2 class="text-lg font-semibold text-slate-950">{{ overallHealth.label }}</h2><span class="rounded-full bg-white/80 px-2 py-0.5 text-xs text-slate-600">{{ lastUpdated ? `Updated ${lastUpdated.toLocaleTimeString()}` : 'Updating…' }}</span></div><p class="mt-1 text-sm text-slate-600">{{ overallHealth.detail }}</p></div>
        </div>
        <button v-if="stats.attention.length" class="text-sm font-medium text-amber-800 underline underline-offset-2" @click="attentionPanel?.scrollIntoView({ behavior: 'smooth', block: 'start' })">Review {{ stats.attention.length }} issue{{ stats.attention.length === 1 ? '' : 's' }}</button>
        <span v-else class="inline-flex items-center gap-2 text-sm font-medium text-emerald-700"><ShieldCheck class="h-4 w-4" />No active alerts</span>
      </div>
      <div class="grid grid-cols-2 divide-x divide-slate-100 sm:grid-cols-4">
        <router-link v-for="item in inventoryCards.slice(0, 4)" :key="item.label" :to="{ path: '/containers', query: { filter: item.filter } }" class="px-5 py-4 transition hover:bg-slate-50 sm:px-7"><p class="text-xs text-slate-500">{{ item.label }}</p><p class="mt-1 text-2xl font-semibold tabular-nums text-slate-950">{{ containers ? item.value : '—' }}</p></router-link>
      </div>
    </section>

    <p v-if="error" role="alert" class="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700">{{ error }} {{ containers ? 'Previous inventory is shown.' : 'Inventory is unavailable.' }}</p>
    <section class="space-y-3" aria-label="System resource usage">
      <div class="flex flex-wrap items-center justify-between gap-2"><div><h2 class="text-lg font-semibold text-slate-950">Resource pulse</h2><p class="text-sm text-slate-500">Live capacity across the Docker host and managed containers.</p></div><router-link to="/metrics" class="inline-flex items-center gap-1 text-sm font-medium text-indigo-700">Open full metrics <ArrowRight class="h-4 w-4" /></router-link></div>
      <p v-if="metricsError" role="status" class="text-sm text-amber-800">{{ metricsError }} {{ metrics ? 'Previous samples are shown.' : 'No resource samples are available.' }}</p>
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <router-link v-for="card in resourceCards" :key="card.label" :to="card.to" class="cyber-card block space-y-3 hover:-translate-y-0.5 hover:border-indigo-300">
          <div class="flex items-center justify-between text-sm text-slate-600"><span>{{ card.label }}</span><component :is="card.icon" class="h-4 w-4 text-indigo-600" /></div>
          <p class="text-2xl font-semibold tabular-nums text-slate-950">{{ card.value }}</p>
          <div v-if="card.progress !== null" role="progressbar" :aria-label="card.label" :aria-valuenow="Math.min(100, Math.max(0, card.progress))" aria-valuemin="0" aria-valuemax="100" class="h-1.5 overflow-hidden rounded-full bg-slate-100"><div class="h-full rounded-full" :class="card.progress >= 90 ? 'bg-amber-500' : 'bg-indigo-500'" :style="{ width: `${Math.min(100, Math.max(0, card.progress))}%` }"></div></div>
          <p class="text-xs text-slate-500">{{ card.detail }}</p>
        </router-link>
      </div>
      <div class="flex flex-wrap gap-x-4 gap-y-1 rounded-lg border border-slate-200 bg-white px-4 py-3 text-xs text-slate-600"><span class="font-medium" :class="engine?.server_version ? 'text-emerald-700' : 'text-slate-500'">Docker {{ engine?.server_version || (loading ? 'loading…' : 'unavailable') }}</span><span v-if="engine?.operating_system">{{ engine.operating_system }}</span><span v-if="engine?.architecture">{{ engine.architecture }}</span><span v-if="engine?.cpus">{{ engine.cpus }} CPUs</span><span v-if="engine?.server_version">{{ engine.images_count ?? 0 }} images</span></div>
    </section>

    <div class="grid items-start gap-6 xl:grid-cols-[1.25fr_0.75fr]">
      <section class="cyber-card space-y-4">
        <div class="flex items-center justify-between gap-2"><div><h2 class="font-semibold">Busiest containers</h2><p class="text-xs text-slate-500">Sorted by current CPU usage.</p></div><router-link to="/metrics" class="text-sm text-indigo-700">All usage →</router-link></div>
        <p v-if="!usage.busiest.length" class="text-sm text-slate-500">{{ containers && !stats.running ? 'No running containers.' : 'Waiting for CPU samples from running containers.' }}</p>
        <div v-else class="overflow-x-auto"><table class="w-full text-left text-sm"><thead><tr class="border-b border-slate-200 text-xs text-slate-500"><th class="pb-2 pr-3 font-medium">Container</th><th class="pb-2 pr-3 font-medium">CPU</th><th class="pb-2 pr-3 font-medium">Memory</th><th class="pb-2 font-medium">Network I/O</th></tr></thead><tbody><tr v-for="container in usage.busiest" :key="container.id || container.name" class="border-b border-slate-100 last:border-0"><td class="py-3 pr-3"><router-link :to="{ path: '/containers', query: { name: container.name } }" class="font-medium text-indigo-700 hover:underline">{{ container.name }}</router-link></td><td class="whitespace-nowrap py-3 pr-3 tabular-nums">{{ container.cpu.toFixed(1) }}%</td><td class="whitespace-nowrap py-3 pr-3 tabular-nums">{{ container.mem_usage || '—' }}</td><td class="whitespace-nowrap py-3 text-xs tabular-nums">{{ container.net_io || '—' }}</td></tr></tbody></table></div>
        <p class="text-xs text-slate-500">100% represents one CPU core. Network I/O is cumulative.</p>
      </section>
      <section class="cyber-card space-y-4">
        <div class="flex items-center justify-between"><div><h2 class="font-semibold">Connectivity</h2><p class="text-xs text-slate-500">Exposure providers and managed routes.</p></div><Globe class="h-5 w-5 text-indigo-600" /></div>
        <router-link to="/access?tab=cloudflare" class="flex items-center justify-between rounded-lg border border-slate-200 p-3 hover:border-indigo-300"><span class="flex items-center gap-2 text-sm font-medium"><Activity class="h-4 w-4 text-indigo-600" />Cloudflare</span><span class="text-xs text-slate-600">{{ cloudflareState }}</span></router-link>
        <router-link to="/access?tab=tailscale" class="flex items-center justify-between rounded-lg border border-slate-200 p-3 hover:border-indigo-300"><span class="flex items-center gap-2 text-sm font-medium"><Globe class="h-4 w-4 text-indigo-600" />Tailscale</span><span class="text-xs text-slate-600">{{ containers ? stats.tailscale : '—' }} apps</span></router-link>
        <router-link to="/access?tab=wireguard" class="flex items-center justify-between rounded-lg border border-slate-200 p-3 hover:border-indigo-300"><span class="flex items-center gap-2 text-sm font-medium"><ShieldCheck class="h-4 w-4 text-indigo-600" />WireGuard</span><span class="text-xs text-slate-600">{{ wireguardState }}</span></router-link>
        <router-link to="/access" class="inline-flex items-center gap-2 text-sm text-indigo-700">Manage providers <ArrowRight class="h-4 w-4" /></router-link>
      </section>
    </div>
    <section ref="attentionPanel" class="cyber-card space-y-4">
      <div class="flex flex-wrap items-center justify-between gap-2"><div><h2 class="font-semibold">Needs attention <span v-if="containers" class="ml-1 text-sm text-slate-500">{{ stats.attention.length }}</span></h2><p class="text-xs text-slate-500">Restarting, unhealthy, dead, or paused containers.</p></div><router-link to="/containers" class="text-sm text-indigo-700">All containers →</router-link></div>
      <p v-if="error" class="text-sm text-slate-600">Refresh to check current health.</p>
      <p v-else-if="!stats.attention.length" class="flex items-center gap-2 text-sm text-emerald-700"><CheckCircle2 class="h-4 w-4" />{{ !containers ? 'Loading containers…' : stats.total ? 'No container issues reported.' : 'No containers found. Create one to get started.' }}</p>
      <router-link v-for="container in stats.attention.slice(0, 6)" :key="container.id" :to="{ path: '/containers', query: { name: container.name } }" class="flex items-center justify-between gap-4 rounded-lg border border-amber-200 bg-amber-50 p-3"><span class="flex items-center gap-2 text-sm font-medium"><AlertCircle class="h-4 w-4 text-amber-700" />{{ container.name }}</span><span class="text-xs text-slate-600">{{ container.status || container.state }}</span></router-link>
    </section>
    <section class="cyber-card space-y-4">
      <div class="flex items-center justify-between"><div><h2 class="font-semibold">Recent activity</h2><p class="text-xs text-slate-500">The latest changes recorded by DockTun.</p></div><router-link to="/logs?tab=audit" class="text-sm text-indigo-700">View audit log →</router-link></div>
      <p v-if="auditError" class="text-sm text-amber-800">{{ auditError }} {{ events.length ? 'Previous activity is shown.' : '' }}</p>
      <p v-else-if="!events.length" class="text-sm text-slate-500">No recent activity to show.</p>
      <div v-for="(event, index) in events" :key="event.id || index" class="flex flex-wrap justify-between gap-2 border-t border-slate-100 pt-3 text-sm"><span class="text-slate-700">{{ event.details || event.action }}</span><time class="text-xs text-slate-500">{{ eventTime(event.created_at || event.timestamp) }}</time></div>
    </section>
  </div>
</template>
