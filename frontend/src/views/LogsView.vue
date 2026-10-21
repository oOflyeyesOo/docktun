<script setup lang="ts">
import { ref, computed, nextTick, onMounted, onUnmounted, watch } from 'vue'
import { RotateCcw, Copy, Check, ScrollText } from 'lucide-vue-next'
import { useAuthStore } from '../stores/auth'
import { useApi } from '../composables/useApi'

const authStore = useAuthStore()
const { apiFetch } = useApi()

// Log state
const selectedSource = ref('cloudflared')
const containers = ref<any[]>([])
const logs = ref<{ time: string; message: string }[]>([])
const containerLogText = ref('')
const filter = ref('all')
const search = ref('')
const autoScroll = ref(true)
const logContainer = ref<HTMLElement>()
const wsStatus = ref('DISCONNECTED')
const loadingContainerLogs = ref(false)
const copied = ref(false)

const wsUrl = `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/api/logs/ws?token=${authStore.token}`
let ws: WebSocket | null = null
let containerPollTimer: any = null

function connectWs() {
  if (ws) {
    ws.close()
    ws = null
  }
  ws = new WebSocket(wsUrl)
  wsStatus.value = 'CONNECTING'
  ws.onopen = () => { wsStatus.value = 'OPEN' }
  ws.onclose = () => {
    wsStatus.value = 'CLOSED'
    if (selectedSource.value === 'cloudflared') {
      setTimeout(connectWs, 3000)
    }
  }
  ws.onmessage = (e) => {
    try {
      const entry = JSON.parse(e.data)
      logs.value.push(entry)
      if (logs.value.length > 2000) logs.value.shift()
      if (autoScroll.value) {
        nextTick(() => {
          if (logContainer.value) logContainer.value.scrollTop = logContainer.value.scrollHeight
        })
      }
    } catch {}
  }
}

async function fetchContainers() {
  try {
    containers.value = await apiFetch<any[]>('/api/docker/containers')
  } catch (e) {
    console.error('Failed to load containers for log selector', e)
  }
}

async function fetchContainerLogs() {
  if (selectedSource.value === 'cloudflared') return
  loadingContainerLogs.value = true
  try {
    const res = await apiFetch<{ logs: string }>(
      `/api/docker/containers/${selectedSource.value}/logs?tail=300&timestamps=true`
    )
    containerLogText.value = res.logs || 'No logs recorded for this service container.'
    if (autoScroll.value) {
      nextTick(() => {
        if (logContainer.value) logContainer.value.scrollTop = logContainer.value.scrollHeight
      })
    }
  } catch (e: any) {
    containerLogText.value = 'Failed to load container logs: ' + e.message
  } finally {
    loadingContainerLogs.value = false
  }
}

watch(selectedSource, (newSource) => {
  if (containerPollTimer) {
    clearInterval(containerPollTimer)
    containerPollTimer = null
  }

  if (newSource === 'cloudflared') {
    connectWs()
  } else {
    if (ws) {
      ws.close()
      ws = null
      wsStatus.value = 'STANDBY'
    }
    fetchContainerLogs()
    containerPollTimer = setInterval(fetchContainerLogs, 3000)
  }
})

onMounted(async () => {
  await fetchContainers()
  connectWs()
})

onUnmounted(() => {
  if (ws) ws.close()
  if (containerPollTimer) clearInterval(containerPollTimer)
})

const filteredWsLogs = computed(() => {
  let result = logs.value
  if (filter.value !== 'all') result = result.filter(l => l.message.toLowerCase().includes(filter.value))
  if (search.value) {
    const s = search.value.toLowerCase()
    result = result.filter(l => l.message.toLowerCase().includes(s))
  }
  return result
})

const filteredContainerLogs = computed(() => {
  if (!containerLogText.value) return []
  let lines = containerLogText.value.split('\n')
  if (filter.value !== 'all') {
    lines = lines.filter(l => l.toLowerCase().includes(filter.value))
  }
  if (search.value) {
    const s = search.value.toLowerCase()
    lines = lines.filter(l => l.toLowerCase().includes(s))
  }
  return lines
})

function getLogClass(msg: string) {
  if (msg.includes('ERR') || msg.includes('error') || msg.includes('FAIL')) return 'text-red-400 font-semibold'
  if (msg.includes('WRN') || msg.includes('warn')) return 'text-amber-400'
  return 'text-slate-300'
}

function copyAllLogs() {
  const text = selectedSource.value === 'cloudflared'
    ? filteredWsLogs.value.map(l => `${l.time} ${l.message}`).join('\n')
    : filteredContainerLogs.value.join('\n')
  navigator.clipboard.writeText(text)
  copied.value = true
  setTimeout(() => (copied.value = false), 2000)
}

const currentSourceLabel = computed(() => {
  if (selectedSource.value === 'cloudflared') return 'Cloudflare Tunnel (cloudflared)'
  const c = containers.value.find(item => item.id === selectedSource.value)
  return c ? `${c.name} (${c.image})` : selectedSource.value
})
</script>

<template>
  <div class="space-y-4 h-full flex flex-col">
    <!-- Header -->
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between border-b border-slate-800 pb-4">
      <div>
        <p class="text-[10px] font-semibold uppercase tracking-[0.2em] text-slate-400">Isolated Service Output</p>
        <h1 class="text-xl lg:text-2xl font-bold tracking-tight text-slate-100 flex items-center gap-2">
          <ScrollText class="h-6 w-6 text-cyan-400" />
          <span>Realtime Logs</span>
        </h1>
      </div>

      <!-- Service Selector -->
      <div class="flex flex-wrap items-center gap-2">
        <label class="text-xs text-slate-400 font-medium">Service Source:</label>
        <select
          v-model="selectedSource"
          class="cyber-input text-xs font-mono py-1.5 px-3 min-w-[240px] border-cyan-800/80 bg-slate-900"
        >
          <option value="cloudflared">Tunnel Daemon (cloudflared)</option>
          <optgroup label="Docker Containers (Isolated)">
            <option v-for="c in containers" :key="c.id" :value="c.id">
              {{ c.name }} ({{ c.image }})
            </option>
          </optgroup>
        </select>

        <span
          v-if="selectedSource === 'cloudflared'"
          class="rounded px-2.5 py-1 text-[10px] font-mono font-bold uppercase tracking-wider"
          :class="wsStatus === 'OPEN' ? 'bg-emerald-950/80 text-emerald-400 border border-emerald-800/60' : 'bg-red-950/80 text-red-400 border border-red-800/60'"
        >
          WS: {{ wsStatus }}
        </span>
        <span
          v-else
          class="rounded px-2.5 py-1 text-[10px] font-mono font-bold uppercase tracking-wider bg-cyan-950/80 text-cyan-400 border border-cyan-800/60"
        >
          POLLING: 3S
        </span>
      </div>
    </div>

    <!-- Active Source Indicator Banner -->
    <div class="flex items-center justify-between bg-slate-900/60 border border-slate-800 rounded-lg px-4 py-2 text-xs">
      <div class="flex items-center gap-2">
        <span class="h-2 w-2 rounded-full bg-emerald-400 animate-pulse"></span>
        <span class="text-slate-400">Streaming isolated stdout/stderr for:</span>
        <span class="font-mono font-bold text-slate-100">{{ currentSourceLabel }}</span>
      </div>
      <div class="flex items-center gap-2">
        <button
          @click="copyAllLogs"
          class="cyber-btn-secondary text-xs flex items-center gap-1.5 py-1"
          title="Copy filtered logs"
        >
          <Check v-if="copied" class="h-3.5 w-3.5 text-emerald-400" />
          <Copy v-else class="h-3.5 w-3.5" />
          <span>{{ copied ? 'Copied' : 'Copy' }}</span>
        </button>
        <button
          v-if="selectedSource !== 'cloudflared'"
          @click="fetchContainerLogs"
          :disabled="loadingContainerLogs"
          class="cyber-btn-secondary text-xs flex items-center gap-1.5 py-1"
        >
          <RotateCcw class="h-3.5 w-3.5" :class="loadingContainerLogs ? 'animate-spin' : ''" />
          <span>Refresh</span>
        </button>
      </div>
    </div>

    <!-- Filters & Search Toolbar -->
    <div class="flex flex-col sm:flex-row gap-2 sm:items-center">
      <div class="flex border border-slate-800 rounded-lg overflow-hidden bg-slate-950">
        <button
          v-for="f in ['all', 'error', 'warn', 'info']"
          :key="f"
          @click="filter = f"
          :class="filter === f ? 'bg-cyan-950/80 text-cyan-300 font-bold border-r border-slate-800' : 'text-slate-400 hover:text-slate-200 border-r border-slate-800'"
          class="px-3 py-1.5 text-xs uppercase tracking-wider transition-colors last:border-r-0"
        >
          {{ f }}
        </button>
      </div>
      <div class="flex gap-2 sm:ml-auto items-center">
        <input
          v-model="search"
          placeholder="grep in logs..."
          class="cyber-input !py-1.5 flex-1 sm:!w-56 !text-xs font-mono"
        />
        <label class="flex items-center gap-1.5 text-xs text-slate-400 whitespace-nowrap cursor-pointer">
          <input type="checkbox" v-model="autoScroll" class="rounded bg-slate-950" />
          <span>Auto-tail</span>
        </label>
      </div>
    </div>

    <!-- Log Viewer Box -->
    <div ref="logContainer" class="flex-1 cyber-card !p-0 overflow-auto min-h-[460px] max-h-[68vh] border border-slate-800 bg-slate-950">
      <!-- 1. Cloudflared WS Stream -->
      <template v-if="selectedSource === 'cloudflared'">
        <div
          v-for="(log, i) in filteredWsLogs"
          :key="i"
          class="px-4 py-1 border-b border-slate-800/40 hover:bg-slate-900/60 text-xs font-mono flex items-start gap-2"
        >
          <span class="text-slate-500 whitespace-nowrap select-none">{{ log.time?.split('T')[1]?.slice(0, 8) || log.time }}</span>
          <span :class="getLogClass(log.message)" class="break-all">{{ log.message }}</span>
        </div>
        <div v-if="!filteredWsLogs.length" class="text-slate-500 text-center py-16 text-xs tracking-wider font-mono">
          // AWAITING CLOUDFLARED OUTPUT STREAM...
        </div>
      </template>

      <!-- 2. Individual Docker Container Output -->
      <template v-else>
        <div
          v-for="(line, idx) in filteredContainerLogs"
          :key="idx"
          class="px-4 py-1 border-b border-slate-800/40 hover:bg-slate-900/60 text-xs font-mono flex items-start gap-3"
        >
          <span class="text-slate-600 select-none text-[10px] w-8 text-right shrink-0">{{ idx + 1 }}</span>
          <span :class="getLogClass(line)" class="break-all">{{ line }}</span>
        </div>
        <div v-if="!filteredContainerLogs.length" class="text-slate-500 text-center py-16 text-xs tracking-wider font-mono">
          {{ search || filter !== 'all' ? '// NO MATCHING LOG ENTRIES' : '// NO SERVICE OUTPUT RECORDED' }}
        </div>
      </template>
    </div>
  </div>
</template>
