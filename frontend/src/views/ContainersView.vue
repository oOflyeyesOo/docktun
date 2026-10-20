<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ChevronRight, FolderOpen, Play, RefreshCcw, Square, Trash2 } from 'lucide-vue-next'
import { useApi } from '../composables/useApi'

const { apiFetch } = useApi()

interface ContainerItem {
  id: string
  name: string
  image: string
  status: string
  state: string
  ports: string
  command: string
  running: boolean
}

const containers = ref<ContainerItem[]>([])
const loading = ref(false)
const filter = ref<'all' | 'running' | 'stopped'>('all')
const selectedId = ref<string | null>(null)
const logs = ref('')
const logsLoading = ref(false)

const filteredContainers = computed(() => {
  if (filter.value === 'running') return containers.value.filter((c) => c.running)
  if (filter.value === 'stopped') return containers.value.filter((c) => !c.running)
  return containers.value
})

const summary = computed(() => {
  const total = containers.value.length
  const running = containers.value.filter((c) => c.running).length
  return { total, running, stopped: total - running }
})

async function fetchContainers() {
  loading.value = true
  try {
    containers.value = await apiFetch('/api/docker/containers')
  } catch (error: any) {
    console.error(error)
  } finally {
    loading.value = false
  }
}

async function runAction(id: string, action: 'start' | 'stop' | 'restart') {
  try {
    await apiFetch(`/api/docker/containers/${id}/${action}`, { method: 'POST' })
    await fetchContainers()
  } catch (error: any) {
    console.error(error)
  }
}

async function removeContainer(id: string) {
  if (!confirm('Remove this container?')) return
  try {
    await apiFetch(`/api/docker/containers/${id}/remove`, {
      method: 'POST',
      body: JSON.stringify({ force: true }),
    })
    await fetchContainers()
    if (selectedId.value === id) {
      selectedId.value = null
      logs.value = ''
    }
  } catch (error: any) {
    console.error(error)
  }
}

async function openLogs(id: string) {
  selectedId.value = id
  logsLoading.value = true
  logs.value = ''

  try {
    const res = await apiFetch<{ logs: string }>(`/api/docker/containers/${id}/logs?tail=100`)
    logs.value = res.logs
  } catch (error: any) {
    logs.value = error.message
  } finally {
    logsLoading.value = false
  }
}

function statusClass(running: boolean) {
  return running ? 'bg-emerald-500/10 text-emerald-300 border-emerald-500/30' : 'bg-slate-800 text-slate-300 border-slate-700'
}

onMounted(() => {
  fetchContainers()
  const timer = setInterval(fetchContainers, 10000)
  onUnmounted(() => clearInterval(timer))
})
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between gap-3">
      <div>
        <p class="text-[10px] uppercase tracking-[0.22em] text-slate-400">Docker</p>
        <h1 class="text-2xl font-semibold text-slate-100">Containers</h1>
      </div>
      <button @click="fetchContainers" class="cyber-btn">
        <RefreshCcw class="mr-2 h-3.5 w-3.5" :stroke-width="1.8" /> Refresh
      </button>
    </div>

    <div class="grid gap-3 md:grid-cols-3">
      <div class="cyber-card-glow">
        <div class="text-[10px] uppercase tracking-[0.18em] text-slate-400">Total</div>
        <div class="mt-2 text-2xl font-semibold text-slate-100">{{ summary.total }}</div>
      </div>
      <div class="cyber-card-glow border-emerald-900/60 bg-emerald-950/20">
        <div class="text-[10px] uppercase tracking-[0.18em] text-emerald-300">Running</div>
        <div class="mt-2 text-2xl font-semibold text-emerald-200">{{ summary.running }}</div>
      </div>
      <div class="cyber-card-glow">
        <div class="text-[10px] uppercase tracking-[0.18em] text-slate-400">Stopped</div>
        <div class="mt-2 text-2xl font-semibold text-slate-100">{{ summary.stopped }}</div>
      </div>
    </div>

    <div class="cyber-card">
      <div class="flex flex-wrap gap-2">
        <button
          v-for="option in ['all', 'running', 'stopped'] as const"
          :key="option"
          @click="filter = option"
          class="rounded-full border px-2.5 py-1 text-[10px] uppercase tracking-[0.18em] transition"
          :class="filter === option ? 'border-slate-200 bg-slate-100 text-slate-900' : 'border-slate-700 bg-slate-950 text-slate-300 hover:border-slate-500 hover:text-slate-100'"
        >
          {{ option }}
        </button>
      </div>
    </div>

    <div class="cyber-card overflow-hidden p-0">
      <div v-if="loading" class="p-6 text-sm text-slate-400">Loading containers…</div>
      <div v-else-if="filteredContainers.length === 0" class="p-6 text-sm text-slate-400">No containers found.</div>
      <div v-else class="divide-y divide-slate-800">
        <div v-for="container in filteredContainers" :key="container.id" class="flex flex-col gap-4 p-4 lg:flex-row lg:items-center lg:justify-between">
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-2">
              <span class="inline-flex h-2.5 w-2.5 rounded-full" :class="container.running ? 'bg-emerald-400' : 'bg-slate-500'" />
              <span class="truncate text-sm font-medium text-slate-100">{{ container.name }}</span>
              <span class="rounded-full border px-2 py-0.5 text-[10px] uppercase tracking-[0.16em]" :class="statusClass(container.running)">
                {{ container.running ? 'running' : 'stopped' }}
              </span>
            </div>
            <div class="mt-2 text-xs text-slate-400">
              {{ container.image }}
            </div>
            <div class="mt-2 flex flex-wrap gap-2 text-[11px] text-slate-400">
              <span class="rounded border border-slate-700 bg-slate-950 px-1.5 py-0.5">{{ container.state }}</span>
              <span>{{ container.ports || 'No ports exposed' }}</span>
            </div>
          </div>

          <div class="flex flex-wrap items-center gap-2">
            <button v-if="!container.running" @click="runAction(container.id, 'start')" class="cyber-btn-secondary text-emerald-300">
              <Play class="mr-1.5 h-3.5 w-3.5" :stroke-width="2" /> Start
            </button>
            <button v-else @click="runAction(container.id, 'stop')" class="cyber-btn-secondary text-amber-300">
              <Square class="mr-1.5 h-3.5 w-3.5" :stroke-width="2" /> Stop
            </button>
            <button @click="runAction(container.id, 'restart')" class="cyber-btn-secondary">
              <RefreshCcw class="mr-1.5 h-3.5 w-3.5" :stroke-width="2" /> Restart
            </button>
            <button @click="openLogs(container.id)" class="cyber-btn-secondary">
              <FolderOpen class="mr-1.5 h-3.5 w-3.5" :stroke-width="2" /> Logs
            </button>
            <button @click="removeContainer(container.id)" class="cyber-btn-danger">
              <Trash2 class="mr-1.5 h-3.5 w-3.5" :stroke-width="2" /> Remove
            </button>
          </div>
        </div>
      </div>
    </div>

    <div v-if="selectedId" class="cyber-card">
      <div class="mb-3 flex items-center justify-between gap-3">
        <div class="flex items-center gap-2 text-sm font-medium text-slate-100">
          <span class="text-[10px] uppercase tracking-[0.18em] text-slate-400">Logs</span>
          <span>{{ selectedId }}</span>
        </div>
        <button @click="selectedId = null" class="text-[10px] uppercase tracking-[0.18em] text-slate-400 hover:text-slate-200">Close</button>
      </div>
      <pre v-if="logsLoading" class="min-h-[120px] whitespace-pre-wrap text-xs text-slate-400">Loading logs…</pre>
      <pre v-else class="max-h-[320px] overflow-auto whitespace-pre-wrap text-xs text-slate-300">{{ logs || 'No logs available.' }}</pre>
    </div>

    <div class="flex justify-end">
      <router-link to="/projects" class="inline-flex items-center gap-2 text-xs uppercase tracking-[0.2em] text-slate-400 transition hover:text-slate-200">
        Project view <ChevronRight class="h-3.5 w-3.5" :stroke-width="2" />
      </router-link>
    </div>
  </div>
</template>
