<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { ChevronRight, FolderOpen, Play, RefreshCcw, Square, Trash2, ServerCog, ShieldCheck, Globe, Lock, Network, Activity, HardDrive, ArrowUpRight } from 'lucide-vue-next'
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
  exposure_mode?: string
  hostname?: string
  target?: string
  private_type?: string
  private_host?: string
  private_port?: number
  notes?: string
  cpu?: string
  memory?: string
  network?: string
  pids?: number
  restart_count?: number
  uptime?: string
  exposure?: {
    mode: string
    hostname?: string
    target?: string
    private_type?: string
    private_host?: string
    private_port?: number
    notes?: string
  }
}

const containers = ref<ContainerItem[]>([])
const loading = ref(false)
const filter = ref<'all' | 'running' | 'stopped'>('all')
const selectedId = ref<string | null>(null)
const logs = ref('')
const logsLoading = ref(false)
const exposing = ref<Record<string, boolean>>({})
const settings = ref<Record<string, any>>({})

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
    containers.value.forEach((container) => {
      const mode = container.exposure_mode || container.exposure?.mode || 'internal'
      settings.value[container.id] = {
        mode,
        hostname: container.hostname || container.exposure?.hostname || '',
        target: container.target || container.exposure?.target || '',
        private_type: container.private_type || container.exposure?.private_type || 'tailscale',
        private_host: container.private_host || container.exposure?.private_host || '',
        private_port: container.private_port || container.exposure?.private_port || 80,
        notes: container.notes || container.exposure?.notes || '',
      }
    })
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

async function saveExposure(container: ContainerItem) {
  exposing.value[container.id] = true
  try {
    const config = settings.value[container.id] || {
      mode: container.exposure_mode || 'internal',
      hostname: container.hostname || '',
      target: container.target || '',
      private_type: container.private_type || 'tailscale',
      private_host: container.private_host || '',
      private_port: container.private_port || 80,
      notes: container.notes || '',
    }

    await apiFetch(`/api/docker/containers/${container.id}/exposure`, {
      method: 'POST',
      body: JSON.stringify(config),
    })
    await fetchContainers()
  } catch (error: any) {
    console.error(error)
  } finally {
    exposing.value[container.id] = false
  }
}

function statusClass(running: boolean) {
  return running ? 'bg-emerald-500/10 text-emerald-300 border-emerald-500/30' : 'bg-slate-800 text-slate-300 border-slate-700'
}

function modeClass(mode: string) {
  switch (mode) {
    case 'public': return 'bg-cyan-500/10 text-cyan-300 border-cyan-500/40'
    case 'private': return 'bg-violet-500/10 text-violet-300 border-violet-500/40'
    default: return 'bg-slate-800 text-slate-300 border-slate-700'
  }
}

onMounted(() => {
  fetchContainers()
  const timer = setInterval(fetchContainers, 10000)
  onUnmounted(() => clearInterval(timer))
})
</script>

<template>
  <div class="space-y-6">
    <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
      <div>
        <p class="text-[10px] uppercase tracking-[0.22em] text-slate-400">Control plane</p>
        <h1 class="text-2xl font-semibold text-slate-100">Containers</h1>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <button @click="fetchContainers" class="cyber-btn-secondary flex items-center gap-2">
          <RefreshCcw class="h-4 w-4" :stroke-width="1.7" /> Refresh
        </button>
      </div>
    </div>

    <div class="grid gap-3 md:grid-cols-3">
      <div class="cyber-card-glow">
        <p class="text-[10px] uppercase tracking-[0.22em] text-slate-400">Total</p>
        <p class="mt-3 text-2xl font-semibold text-slate-100">{{ summary.total }}</p>
      </div>
      <div class="cyber-card-glow">
        <p class="text-[10px] uppercase tracking-[0.22em] text-slate-400">Running</p>
        <p class="mt-3 text-2xl font-semibold text-emerald-300">{{ summary.running }}</p>
      </div>
      <div class="cyber-card-glow">
        <p class="text-[10px] uppercase tracking-[0.22em] text-slate-400">Stopped</p>
        <p class="mt-3 text-2xl font-semibold text-slate-300">{{ summary.stopped }}</p>
      </div>
    </div>

    <div class="flex items-center gap-2">
      <button @click="filter = 'all'" :class="filter === 'all' ? 'bg-slate-800 text-slate-100' : 'bg-slate-950 text-slate-400'" class="rounded border border-slate-700 px-3 py-1.5 text-[10px] uppercase tracking-[0.2em]">All</button>
      <button @click="filter = 'running'" :class="filter === 'running' ? 'bg-slate-800 text-slate-100' : 'bg-slate-950 text-slate-400'" class="rounded border border-slate-700 px-3 py-1.5 text-[10px] uppercase tracking-[0.2em]">Running</button>
      <button @click="filter = 'stopped'" :class="filter === 'stopped' ? 'bg-slate-800 text-slate-100' : 'bg-slate-950 text-slate-400'" class="rounded border border-slate-700 px-3 py-1.5 text-[10px] uppercase tracking-[0.2em]">Stopped</button>
    </div>

    <div v-if="loading" class="cyber-card text-sm text-slate-300">Loading containers…</div>

    <div v-else class="grid gap-4 xl:grid-cols-[1.4fr_0.6fr]">
      <div class="grid gap-3">
        <div v-for="container in filteredContainers" :key="container.id" class="cyber-card p-4">
          <div class="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
            <div class="flex items-start gap-3">
              <div class="mt-1 rounded bg-slate-900 p-2 text-slate-200">
                <ServerCog class="h-4 w-4" />
              </div>
              <div>
                <div class="flex items-center gap-2">
                  <span class="text-lg font-semibold text-slate-100">{{ container.name }}</span>
                  <span :class="statusClass(container.running)" class="rounded border px-2 py-0.5 text-[10px] uppercase tracking-[0.18em]">{{ container.running ? 'Running' : 'Stopped' }}</span>
                </div>
                <div class="mt-1 flex flex-wrap items-center gap-2 text-xs text-slate-400">
                  <span>{{ container.image }}</span>
                  <span>•</span>
                  <span>{{ container.status }}</span>
                </div>
              </div>
            </div>

            <div class="flex flex-wrap items-center gap-2">
              <span :class="modeClass(container.exposure_mode || container.exposure?.mode || 'internal')" class="rounded border px-2 py-0.5 text-[10px] uppercase tracking-[0.18em]">
                {{ (container.exposure_mode || container.exposure?.mode || 'internal').toUpperCase() }}
              </span>
              <button @click="runAction(container.id, 'start')" v-if="!container.running" class="cyber-btn-secondary flex items-center gap-2">
                <Play class="h-4 w-4" /> Start
              </button>
              <button @click="runAction(container.id, 'stop')" v-if="container.running" class="cyber-btn-secondary flex items-center gap-2">
                <Square class="h-4 w-4" /> Stop
              </button>
              <button @click="runAction(container.id, 'restart')" class="cyber-btn-secondary flex items-center gap-2">
                <RefreshCcw class="h-4 w-4" /> Restart
              </button>
              <button @click="removeContainer(container.id)" class="cyber-btn-danger flex items-center gap-2">
                <Trash2 class="h-4 w-4" /> Remove
              </button>
            </div>
          </div>

          <div class="mt-4 grid gap-3 md:grid-cols-2 xl:grid-cols-4">
            <div class="rounded border border-slate-800 bg-slate-950/60 p-3">
              <p class="text-[10px] uppercase tracking-[0.18em] text-slate-400">Ports</p>
              <p class="mt-2 text-sm text-slate-200">{{ container.ports || '—' }}</p>
            </div>
            <div class="rounded border border-slate-800 bg-slate-950/60 p-3">
              <p class="text-[10px] uppercase tracking-[0.18em] text-slate-400">CPU</p>
              <p class="mt-2 text-sm text-slate-200">{{ container.cpu || '—' }}</p>
            </div>
            <div class="rounded border border-slate-800 bg-slate-950/60 p-3">
              <p class="text-[10px] uppercase tracking-[0.18em] text-slate-400">Memory</p>
              <p class="mt-2 text-sm text-slate-200">{{ container.memory || '—' }}</p>
            </div>
            <div class="rounded border border-slate-800 bg-slate-950/60 p-3">
              <p class="text-[10px] uppercase tracking-[0.18em] text-slate-400">Uptime</p>
              <p class="mt-2 text-sm text-slate-200">{{ container.uptime || '—' }}</p>
            </div>
          </div>

          <div class="mt-4 flex flex-wrap items-center gap-2">
            <button @click="openLogs(container.id)" class="cyber-btn-secondary flex items-center gap-2 text-xs">
              <FolderOpen class="h-4 w-4" /> Logs
            </button>
            <button @click="selectedId = selectedId === container.id ? null : container.id" class="cyber-btn-secondary flex items-center gap-2 text-xs">
              <ChevronRight class="h-4 w-4" /> Expose
            </button>
          </div>

          <div v-if="selectedId === container.id" class="mt-4 rounded border border-slate-800 bg-slate-950/80 p-4">
            <div class="mb-3 flex items-center justify-between">
              <h3 class="text-xs uppercase tracking-[0.22em] text-slate-400">Access settings</h3>
              <button @click="saveExposure(container)" :disabled="exposing[container.id]" class="cyber-btn flex items-center gap-2 text-xs">
                <ShieldCheck class="h-4 w-4" /> {{ exposing[container.id] ? 'Saving…' : 'Save' }}
              </button>
            </div>

            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <label class="mb-2 block text-[10px] uppercase tracking-[0.2em] text-slate-400">Mode</label>
                <select v-model="settings[container.id].mode" class="cyber-input">
                  <option value="internal">Internal</option>
                  <option value="private">Private</option>
                  <option value="public">Public</option>
                </select>
              </div>

              <div v-if="(settings[container.id].mode || 'internal') !== 'internal'">
                <label class="mb-2 block text-[10px] uppercase tracking-[0.2em] text-slate-400">Hostname</label>
                <input v-model="settings[container.id].hostname" class="cyber-input" placeholder="app.example.com" />
              </div>
            </div>

            <div class="mt-4 grid gap-4 md:grid-cols-2" v-if="settings[container.id].mode === 'public'">
              <div>
                <label class="mb-2 block text-[10px] uppercase tracking-[0.2em] text-slate-400">Target</label>
                <input v-model="settings[container.id].target" class="cyber-input" placeholder="http://container-name:8080" />
              </div>
              <div>
                <label class="mb-2 block text-[10px] uppercase tracking-[0.2em] text-slate-400">Notes</label>
                <input v-model="settings[container.id].notes" class="cyber-input" placeholder="Public entry endpoint" />
              </div>
            </div>

            <div class="mt-4 grid gap-4 md:grid-cols-2" v-else-if="settings[container.id].mode === 'private'">
              <div>
                <label class="mb-2 block text-[10px] uppercase tracking-[0.2em] text-slate-400">Private type</label>
                <select v-model="settings[container.id].private_type" class="cyber-input">
                  <option value="tailscale">Tailscale</option>
                  <option value="wireguard">WireGuard</option>
                  <option value="mesh">Mesh</option>
                </select>
              </div>
              <div>
                <label class="mb-2 block text-[10px] uppercase tracking-[0.2em] text-slate-400">Private host</label>
                <input v-model="settings[container.id].private_host" class="cyber-input" placeholder="10.0.0.12" />
              </div>
              <div>
                <label class="mb-2 block text-[10px] uppercase tracking-[0.2em] text-slate-400">Private port</label>
                <input v-model.number="settings[container.id].private_port" type="number" class="cyber-input" placeholder="80" />
              </div>
              <div>
                <label class="mb-2 block text-[10px] uppercase tracking-[0.2em] text-slate-400">Notes</label>
                <input v-model="settings[container.id].notes" class="cyber-input" placeholder="Private service" />
              </div>
            </div>
          </div>
        </div>
      </div>

      <div class="space-y-4">
        <div class="cyber-card p-4">
          <div class="mb-3 flex items-center gap-2 text-slate-200">
            <Globe class="h-4 w-4 text-cyan-300" />
            <h3 class="text-xs uppercase tracking-[0.22em] text-slate-400">Public access</h3>
          </div>
          <div class="space-y-2 text-sm text-slate-300">
            <div class="flex items-center gap-2"><ArrowUpRight class="h-4 w-4 text-cyan-300" /> Public ingress is attached per container</div>
            <div class="flex items-center gap-2"><Lock class="h-4 w-4 text-violet-300" /> Private mode remains internal to mesh/private network</div>
            <div class="flex items-center gap-2"><Network class="h-4 w-4 text-emerald-300" /> Internal mode disables public exposure</div>
          </div>
        </div>

        <div class="cyber-card p-4">
          <div class="mb-3 flex items-center gap-2 text-slate-200">
            <Activity class="h-4 w-4 text-emerald-300" />
            <h3 class="text-xs uppercase tracking-[0.22em] text-slate-400">Logs</h3>
          </div>
          <div v-if="!selectedId" class="text-sm text-slate-400">Select a container to inspect logs.</div>
          <div v-else class="space-y-3">
            <div class="rounded border border-slate-800 bg-slate-950 p-3">
              <div class="mb-2 text-[10px] uppercase tracking-[0.18em] text-slate-400">{{ selectedId }}</div>
              <pre v-if="logsLoading" class="text-xs text-slate-300">Loading…</pre>
              <pre v-else class="max-h-[420px] overflow-auto whitespace-pre-wrap text-xs leading-6 text-slate-300">{{ logs || 'No logs available.' }}</pre>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
