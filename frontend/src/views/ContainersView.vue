<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { FolderCog, Play, RefreshCcw, Square, Trash2 } from 'lucide-vue-next'
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
  if (filter.value === 'running') return containers.value.filter(c => c.running)
  if (filter.value === 'stopped') return containers.value.filter(c => !c.running)
  return containers.value
})

const summary = computed(() => {
  const total = containers.value.length
  const running = containers.value.filter(c => c.running).length
  const stopped = total - running
  return { total, running, stopped }
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

onMounted(() => {
  fetchContainers()
  setInterval(fetchContainers, 10000)
})
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between gap-3">
      <div>
        <p class="text-xs uppercase tracking-[0.2em] text-slate-400">Docker</p>
        <h1 class="text-2xl font-semibold text-slate-100">Containers</h1>
      </div>
      <button @click="fetchContainers" class="inline-flex items-center gap-2 rounded border border-slate-700 bg-slate-900 px-3 py-2 text-sm text-slate-200 hover:border-slate-500 hover:bg-slate-800">
        <RefreshCcw class="h-4 w-4" /> Refresh
      </button>
    </div>

    <div class="grid gap-3 md:grid-cols-3">
      <div class="rounded border border-slate-800 bg-slate-900/80 p-4">
        <div class="text-xs uppercase tracking-[0.2em] text-slate-400">Total</div>
        <div class="mt-3 text-2xl font-semibold text-slate-50">{{ summary.total }}</div>
      </div>
      <div class="rounded border border-emerald-900/80 bg-emerald-950/40 p-4">
        <div class="text-xs uppercase tracking-[0.2em] text-emerald-300">Running</div>
        <div class="mt-3 text-2xl font-semibold text-emerald-200">{{ summary.running }}</div>
      </div>
      <div class="rounded border border-slate-800 bg-slate-900/80 p-4">
        <div class="text-xs uppercase tracking-[0.2em] text-slate-400">Stopped</div>
        <div class="mt-3 text-2xl font-semibold text-slate-100">{{ summary.stopped }}</div>
      </div>
    </div>

    <div class="rounded border border-slate-800 bg-slate-900/80 p-3">
      <div class="flex flex-wrap items-center gap-2">
        <button
          v-for="option in ['all', 'running', 'stopped'] as const"
          :key="option"
          @click="filter = option"
          class="rounded px-2.5 py-1.5 text-xs uppercase tracking-[0.18em] transition"
          :class="filter === option ? 'bg-slate-100 text-slate-900' : 'bg-slate-800 text-slate-300 hover:bg-slate-700'"
        >
          {{ option }}
        </button>
      </div>
    </div>

    <div class="rounded border border-slate-800 bg-slate-900/80 overflow-hidden">
      <div v-if="loading" class="p-6 text-sm text-slate-400">Loading containers…</div>
      <div v-else-if="filteredContainers.length === 0" class="p-6 text-sm text-slate-400">No containers found.</div>
      <div v-else class="divide-y divide-slate-800">
        <div v-for="container in filteredContainers" :key="container.id" class="flex flex-col gap-3 p-4 md:flex-row md:items-center md:justify-between">
          <div class="min-w-0">
            <div class="flex items-center gap-2">
              <span class="inline-flex h-2.5 w-2.5 rounded-full" :class="container.running ? 'bg-emerald-400' : 'bg-slate-500'" />
              <span class="truncate text-sm font-medium text-slate-100">{{ container.name }}</span>
            </div>
            <div class="mt-1 text-xs text-slate-400">
              {{ container.image }}
            </div>
            <div class="mt-2 flex flex-wrap items-center gap-2 text-[11px] text-slate-400">
              <span class="rounded border border-slate-700 px-1.5 py-0.5">{{ container.state }}</span>
              <span>{{ container.ports || 'No ports published' }}</span>
            </div>
          </div>

          <div class="flex flex-wrap items-center gap-2">
            <button v-if="!container.running" @click="runAction(container.id, 'start')" class="inline-flex items-center gap-1.5 rounded border border-emerald-700 bg-emerald-950/40 px-2.5 py-1.5 text-xs text-emerald-200 hover:border-emerald-500">
              <Play class="h-3.5 w-3.5" /> Start
            </button>
            <button v-else @click="runAction(container.id, 'stop')" class="inline-flex items-center gap-1.5 rounded border border-amber-700 bg-amber-950/40 px-2.5 py-1.5 text-xs text-amber-200 hover:border-amber-500">
              <Square class="h-3.5 w-3.5" /> Stop
            </button>
            <button @click="runAction(container.id, 'restart')" class="inline-flex items-center gap-1.5 rounded border border-sky-700 bg-sky-950/40 px-2.5 py-1.5 text-xs text-sky-200 hover:border-sky-500">
              <RefreshCcw class="h-3.5 w-3.5" /> Restart
            </button>
            <button @click="openLogs(container.id)" class="inline-flex items-center gap-1.5 rounded border border-slate-700 bg-slate-800 px-2.5 py-1.5 text-xs text-slate-200 hover:border-slate-500">
              <FolderCog class="h-3.5 w-3.5" /> Logs
            </button>
            <button @click="removeContainer(container.id)" class="inline-flex items-center gap-1.5 rounded border border-red-800 bg-red-950/40 px-2.5 py-1.5 text-xs text-red-200 hover:border-red-600">
              <Trash2 class="h-3.5 w-3.5" /> Remove
            </button>
          </div>
        </div>
      </div>
    </div>

    <div v-if="selectedId" class="rounded border border-slate-800 bg-slate-900/80 p-4">
      <div class="mb-3 flex items-center justify-between gap-3">
        <div class="text-sm font-medium text-slate-100">Logs — {{ selectedId }}</div>
        <button @click="selectedId = null" class="text-xs uppercase tracking-[0.2em] text-slate-400 hover:text-slate-200">Close</button>
      </div>
      <pre v-if="logsLoading" class="min-h-[120px] whitespace-pre-wrap text-xs text-slate-400">Loading logs…</pre>
      <pre v-else class="max-h-[320px] overflow-auto whitespace-pre-wrap text-xs text-slate-300">{{ logs || 'No logs available.' }}</pre>
    </div>
  </div>
</template>
