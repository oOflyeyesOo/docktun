<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { ArrowUpCircle, Check, ExternalLink, Loader2, Package, RefreshCw, Server, ShieldCheck, Terminal, XCircle } from 'lucide-vue-next'
import { useApi } from '../composables/useApi'
const { apiFetch } = useApi()
interface Service { id: string; name: string; kind: string; app?: string; image: string; image_id: string; version?: string; candidate?: string; candidate_version?: string; running: boolean; health?: string; available: boolean; checked: boolean; result?: string; message?: string }
const services = ref<Service[]>([]), job = ref<any>({ phase: 'idle', services: [] })
const selected = ref<Record<string, boolean>>({})
const loading = ref(true), submitting = ref(false), reconnecting = ref(false)
const loadError = ref('')
const reloadedJob = ref(sessionStorage.getItem('docktun-services-reloaded') || '')
const error = ref(''), expanded = ref(''), logs = ref(''), logsLoading = ref(false)
let timer: ReturnType<typeof setInterval> | undefined
let refreshing = false, initialized = false, lastCheck = '', logsRevision = 0
const busy = computed(() => submitting.value || ['checking', 'updating'].includes(job.value.phase))
const chosen = computed(() => services.value.filter(s => selected.value[s.name]))
const available = computed(() => services.value.filter(s => s.available).length)
const progress = computed(() => Math.round(100 * (job.value.services || []).filter((s: any) => ['updated', 'current'].includes(s.result)).length / Math.max(1, job.value.services?.length || 0)))
const needsReload = computed(() => job.value.phase === 'complete' && job.value.action === 'update' && job.value.updated_at !== reloadedJob.value && (job.value.services || []).some((s: any) => s.kind === 'core' && s.result === 'updated'))
function reloadDashboard() { sessionStorage.setItem('docktun-services-reloaded', job.value.updated_at); window.location.reload() }
const names: Record<string, string> = { core: 'DockTun', zeroconf: 'Local access', cloudflare: 'Cloudflare connector', tailscale: 'Tailscale companion', wireguard: 'WireGuard', 'tailscale-app': 'Tailscale app device' }
const phaseLabel = computed(() => ({ idle: 'Ready', checking: 'Checking images', updating: 'Installing updates', complete: 'Complete', failed: 'Needs attention' }[job.value.phase as string] || 'Ready'))
async function refresh() {
  if (refreshing) return
  refreshing = true
  try {
    const state = await apiFetch<any>('/api/services')
    services.value = state.services || []; job.value = state.job
    if (!initialized) { for (const s of services.value) selected.value[s.name] = !s.checked || s.available; initialized = true }
    if (job.value.phase === 'complete' && job.value.updated_at !== lastCheck) {
      selected.value = Object.fromEntries(services.value.map(s => [s.name, s.available])); lastCheck = job.value.updated_at
    }
    reconnecting.value = false; loadError.value = ''
  } catch (e: any) {
    if (busy.value) reconnecting.value = true
    else loadError.value = e.message || 'Could not load DockTun services'
  } finally { loading.value = false; refreshing = false }
}
async function act(action: 'check' | 'update', ids?: string[]) {
  submitting.value = true; error.value = ''
  try { job.value = await apiFetch('/api/services/' + action, { method: 'POST', body: JSON.stringify({ ids: ids || (action === 'check' ? [] : chosen.value.map(s => s.id)) }) }) }
  catch (e: any) { error.value = e.message }
  finally { submitting.value = false; await refresh() }
}
async function showLogs(service: Service) {
  const revision = ++logsRevision
  if (expanded.value === service.name) { expanded.value = ''; logsLoading.value = false; return }
  expanded.value = service.name; logs.value = ''; logsLoading.value = true
  try {
    const result = await apiFetch<{ logs: string }>(`/api/docker/containers/${service.id}/logs?tail=100`)
    if (revision === logsRevision) logs.value = result.logs || 'No log output yet.'
  } catch (e: any) { if (revision === logsRevision) logs.value = e.message }
  finally { if (revision === logsRevision) logsLoading.value = false }
}

onMounted(() => { refresh(); timer = setInterval(refresh, 5000) })
onUnmounted(() => { if (timer) clearInterval(timer) })
</script>

<template>
  <section id="docktun-services" class="overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm" aria-label="DockTun services">
    <div class="border-b border-slate-200 bg-gradient-to-br from-indigo-50 via-white to-slate-50 p-5 sm:p-6">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div class="flex gap-3"><div class="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl bg-indigo-600 text-white shadow-sm"><Package class="h-5 w-5" /></div><div><p class="text-[10px] font-semibold uppercase tracking-[0.2em] text-indigo-600">Maintenance</p><h2 class="mt-0.5 text-xl font-semibold text-slate-900">DockTun services</h2><p class="mt-1 text-sm text-slate-600">Keep the dashboard and its installed companions up to date in one place.</p></div></div>
        <div class="flex flex-wrap gap-2"><button class="cyber-btn-secondary gap-2" :disabled="busy || loading" @click="act('check')"><RefreshCw class="h-4 w-4" :class="job.phase === 'checking' ? 'animate-spin' : ''" />Check for updates</button><button class="cyber-btn-primary gap-2" :disabled="busy || !chosen.length" @click="act('update')"><ArrowUpCircle class="h-4 w-4" />Update selected<span class="rounded bg-white/20 px-1.5 text-xs">{{ chosen.length }}</span></button></div>
      </div>
      <div class="mt-5 flex flex-wrap gap-x-6 gap-y-2 text-xs text-slate-600"><span><strong class="text-slate-900">{{ services.length }}</strong> installed services</span><span><strong class="text-slate-900">{{ services.filter(s => s.running).length }}</strong> running</span><span :class="available ? 'font-medium text-indigo-700' : ''"><strong>{{ available }}</strong> updates available</span></div>
    </div>
    <div v-if="busy || job.phase === 'failed' || reconnecting" class="border-b border-slate-200 px-5 py-4 sm:px-6" :class="job.phase === 'failed' ? 'bg-red-50' : 'bg-indigo-50/50'" role="status" aria-live="polite">
      <div class="flex items-center gap-2 text-sm font-medium" :class="job.phase === 'failed' ? 'text-red-800' : 'text-indigo-800'"><XCircle v-if="job.phase === 'failed'" class="h-4 w-4" /><Loader2 v-else class="h-4 w-4 animate-spin" />{{ reconnecting ? 'Dashboard reconnecting' : phaseLabel }}</div>
      <p class="mt-1 text-sm text-slate-600">{{ reconnecting ? 'The update worker continues independently while DockTun restarts. This page reconnects automatically.' : job.message }}</p>
      <div v-if="job.phase === 'updating'" class="mt-3 h-1.5 overflow-hidden rounded-full bg-indigo-100"><div class="h-full rounded-full bg-indigo-600 transition-all" :style="{ width: progress + '%' }"></div></div>
    </div>
    <p v-if="error || loadError" role="alert" class="m-5 rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-800">{{ error || loadError }}</p>
    <div v-if="loading" class="p-8 text-center text-sm text-slate-500"><Loader2 class="mx-auto mb-2 h-5 w-5 animate-spin" />Loading installed services…</div>
    <div v-else class="divide-y divide-slate-100">
      <article v-for="service in services" :key="service.name" class="px-5 py-4 sm:px-6">
        <div class="flex flex-wrap items-center gap-3">
          <input :id="'select-' + service.id" v-model="selected[service.name]" type="checkbox" :disabled="busy" :aria-label="'Select ' + service.name + ' for update'" class="h-4 w-4 rounded border-slate-300 accent-indigo-600" />
          <div class="flex h-9 w-9 items-center justify-center rounded-lg" :class="service.kind === 'core' ? 'bg-indigo-50 text-indigo-600' : 'bg-slate-100 text-slate-500'"><Server v-if="service.kind === 'core'" class="h-4 w-4" /><ShieldCheck v-else class="h-4 w-4" /></div>
          <div class="min-w-0 flex-1"><label :for="'select-' + service.id" class="cursor-pointer font-semibold text-slate-900">{{ names[service.kind] || service.kind }}<span v-if="service.app" class="ml-1 font-normal text-slate-600">· {{ service.app }}</span></label><p class="mt-0.5 truncate text-xs text-slate-500">{{ service.name }}<span> · {{ service.version || service.image_id.replace('sha256:', '').slice(0, 12) }}</span></p><p v-if="service.available" class="mt-0.5 text-xs text-indigo-600">Available: {{ service.candidate_version || service.candidate?.replace('sha256:', '').slice(0, 12) }}</p></div>
          <span class="rounded-full px-2.5 py-1 text-[11px] font-medium" :class="service.running ? service.health === 'unhealthy' ? 'bg-amber-50 text-amber-800' : 'bg-emerald-50 text-emerald-700' : 'bg-slate-100 text-slate-600'">{{ service.running ? service.health === 'unhealthy' ? 'Unhealthy' : 'Running' : 'Stopped' }}</span>
          <span v-if="busy && ['checking', 'updating'].includes(service.result || '')" class="flex items-center gap-1 text-xs text-indigo-700"><Loader2 class="h-3.5 w-3.5 animate-spin" />{{ service.result === 'checking' ? 'Checking' : 'Updating' }}</span><span v-else-if="service.available" class="rounded-full bg-indigo-50 px-2.5 py-1 text-[11px] font-medium text-indigo-700">Update available</span><span v-else-if="service.checked" class="flex items-center gap-1 text-xs text-slate-500"><Check class="h-3.5 w-3.5" />Current</span>
          <button v-if="service.available" class="cyber-btn-secondary text-xs" :disabled="busy" @click="act('update', [service.id])">Update</button>
        </div>
        <p v-if="service.message" class="mt-2 pl-7 text-xs" :class="service.result === 'failed' ? 'text-red-700' : 'text-slate-500'">{{ service.message }}</p>
        <div class="mt-2 flex flex-wrap items-center gap-4 pl-7 text-xs text-slate-500"><button class="flex items-center gap-1 hover:text-indigo-600" @click="showLogs(service)"><Terminal class="h-3.5 w-3.5" />{{ expanded === service.name ? 'Hide details' : 'Details & logs' }}</button><router-link v-if="['cloudflare','tailscale','tailscale-app','wireguard'].includes(service.kind)" :to="'/access?tab=' + (service.kind === 'tailscale-app' ? 'tailscale' : service.kind)" class="flex items-center gap-1 hover:text-indigo-600">Provider settings<ExternalLink class="h-3 w-3" /></router-link></div>
        <div v-if="expanded === service.name" class="mt-3 space-y-2 rounded-xl border border-slate-200 bg-slate-50 p-3 text-xs"><p class="break-all"><span class="text-slate-500">Update source:</span> {{ service.image }}</p><p class="break-all"><span class="text-slate-500">Installed image:</span> {{ service.image_id }}</p><p v-if="!service.version" class="text-slate-500">This image has no verified version label. Its installed image ID is shown above.</p><pre class="max-h-52 overflow-auto rounded-lg bg-slate-950 p-3 text-[11px] leading-relaxed text-slate-200">{{ logsLoading ? 'Loading logs…' : logs }}</pre></div>
      </article>
    </div>
    <div class="flex flex-wrap items-center justify-between gap-4 border-t border-slate-200 bg-slate-50/70 p-5 sm:px-6">
      <div class="max-w-xl text-xs leading-relaxed text-slate-500"><p class="font-medium text-slate-700">Companions update first. DockTun updates last.</p><p>Stopped services stay stopped. Existing settings, mounts and networks are preserved. A failed replacement restores that service’s previous container; earlier successful updates remain installed. Container rollback does not reverse changes made to stored data.</p><p v-if="job.phase === 'complete'" class="mt-1 text-emerald-700" role="status">{{ job.message }}</p></div>

      <button v-if="needsReload" class="cyber-btn-secondary gap-2" @click="reloadDashboard"><RefreshCw class="h-4 w-4" />Reload dashboard</button>
    </div>
  </section>
</template>
