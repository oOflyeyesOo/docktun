<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { Server, Layers, RefreshCw, Plus, ArrowRight, AlertCircle } from 'lucide-vue-next'
import { useApi } from '../composables/useApi'
const { apiFetch } = useApi()
const containers = ref<any[]>([])
const events = ref<any[]>([])
const loading = ref(true)
const error = ref('')
const stats = computed(() => ({
  running: containers.value.filter(c => c.running).length,
  stopped: containers.value.filter(c => !c.running).length,
  updates: containers.value.filter(c => c.update_available).length,
  stacks: new Set(containers.value.map(c => c.project_name).filter(Boolean)).size,
}))
const attention = computed(() => containers.value.filter(c => c.state === 'restarting' || c.state === 'dead' || /unhealthy/i.test(c.status || '')).slice(0, 8))
const exposed = computed(() => containers.value.filter(c => ['public', 'private'].includes(c.exposure_mode)))
let pending = false
let timer: ReturnType<typeof setInterval>
async function refresh() {
  if (pending) return
  pending = true
  loading.value = true
  try {
    const [inventory, audit] = await Promise.allSettled([
      apiFetch<any[]>('/api/docker/containers'),
      apiFetch<{ entries: any[] }>('/api/audit?limit=6'),
    ])
    if (inventory.status === 'fulfilled') { containers.value = inventory.value; error.value = '' }
    else { error.value = inventory.reason?.message || 'Unable to load Docker containers.' }
    if (audit.status === 'fulfilled') events.value = audit.value.entries || []
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
      <div><h1 class="text-2xl font-bold text-slate-900">Docker overview</h1><p class="mt-1 text-sm text-slate-600">Containers, stacks, and the work that needs your attention.</p></div>
      <div class="flex gap-2">
        <button class="cyber-btn-secondary" :disabled="loading" @click="refresh" aria-label="Refresh overview"><RefreshCw class="h-4 w-4" :class="{ 'animate-spin': loading }" /></button>
        <router-link to="/containers?launch=true" class="cyber-btn inline-flex items-center gap-2"><Plus class="h-4 w-4" />Create container</router-link>
      </div>
    </header>
    <p v-if="error" role="alert" class="rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-700">{{ error }}</p>
    <div v-if="!error" class="grid grid-cols-2 gap-4 lg:grid-cols-4">
      <router-link v-for="item in [{ label: 'Running', value: stats.running, filter: 'running' }, { label: 'Stopped', value: stats.stopped, filter: 'stopped' }, { label: 'Updates available', value: stats.updates, filter: 'updates' }, { label: 'Compose stacks', value: stats.stacks, filter: 'all' }]" :key="item.label" :to="{ path: '/containers', query: { filter: item.filter } }" class="cyber-card block hover:border-indigo-300">
        <p class="text-sm text-slate-600">{{ item.label }}</p><p class="mt-2 text-3xl font-semibold text-slate-900">{{ item.value }}</p>
      </router-link>
    </div>
    <section class="cyber-card space-y-4">
      <div class="flex items-center justify-between"><h2 class="font-semibold text-slate-900">Needs attention</h2><router-link to="/containers" class="text-sm text-indigo-700">All containers →</router-link></div>
      <p v-if="error" class="text-sm text-slate-600">Refresh to check container health.</p>
      <p v-else-if="!attention.length" class="text-sm text-slate-600">{{ loading && !containers.length ? 'Loading containers…' : containers.length ? 'No restarting or unhealthy containers reported.' : 'No containers found. Create a container or import a Compose stack to get started.' }}</p>
      <router-link v-for="container in attention" :key="container.id" :to="{ path: '/containers', query: { name: container.name } }" class="flex items-center justify-between gap-4 rounded-lg border border-amber-200 bg-amber-50 p-3">
        <span class="flex items-center gap-2 text-sm font-medium"><AlertCircle class="h-4 w-4 text-amber-700" />{{ container.name }}</span><span class="text-xs text-slate-600">{{ container.status }}</span>
      </router-link>
    </section>
    <div class="grid gap-6 lg:grid-cols-2">
      <section class="cyber-card space-y-4">
        <div class="flex items-center justify-between"><h2 class="font-semibold text-slate-900">Container workspace</h2><Server class="h-5 w-5 text-indigo-600" /></div>
        <p class="text-sm text-slate-600">Manage running apps, Compose files, logs, terminals, and updates in one container inspector.</p>
        <router-link to="/containers" class="inline-flex items-center gap-2 text-sm font-medium text-indigo-700">Open containers <ArrowRight class="h-4 w-4" /></router-link>
        <div class="border-t border-slate-200 pt-4 text-sm text-slate-600">Use <router-link to="/storage" class="text-indigo-700 underline">Storage</router-link> for files and volumes, or <router-link to="/metrics" class="text-indigo-700 underline">Metrics</router-link> for resource usage.</div>
      </section>
      <section class="cyber-card space-y-4">
        <h2 class="font-semibold text-slate-900">Optional exposure</h2>
        <p class="text-sm text-slate-600">{{ exposed.length }} container{{ exposed.length === 1 ? '' : 's' }} with a saved remote access method.</p>
        <p class="text-sm text-slate-600">Cloudflare, Tailscale, and WireGuard are ways to expose an app. Choose a method in that container’s Access tab when you need one.</p>
        <router-link to="/access" class="inline-flex items-center gap-2 text-sm font-medium text-indigo-700">Configure providers <ArrowRight class="h-4 w-4" /></router-link>
      </section>
    </div>
    <section class="cyber-card space-y-4">
      <div class="flex items-center justify-between"><h2 class="font-semibold text-slate-900">Recent activity</h2><router-link to="/logs?tab=audit" class="text-sm text-indigo-700">View audit log →</router-link></div>
      <p v-if="!events.length" class="text-sm text-slate-500">No recent activity to show.</p>
      <div v-for="(event, index) in events" :key="event.id || index" class="flex flex-wrap justify-between gap-2 border-t border-slate-100 pt-3 text-sm">
        <span class="text-slate-700">{{ event.details || event.action }}</span><time class="text-xs text-slate-500">{{ eventTime(event.created_at || event.timestamp) }}</time>
      </div>
    </section>
  </div>
</template>
