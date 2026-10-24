<script setup lang="ts">
import { ref, computed, nextTick, onMounted, onUnmounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  RotateCcw,
  Copy,
  Check,
  ScrollText,
  Download,
  Shield,
  Activity,
  Search,
  Filter,
  ChevronLeft,
  ChevronRight,
  ExternalLink,
  Eye,
  X,
  FileText
} from 'lucide-vue-next'
import { useAuthStore } from '../stores/auth'
import { useApi } from '../composables/useApi'

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const { apiFetch } = useApi()

// Top Navigation Tab
const activeTab = ref<'stream' | 'audit'>((route.query.tab as string) === 'audit' ? 'audit' : 'stream')

watch(
  () => route.query.tab,
  (newTab) => {
    if (newTab === 'audit' || newTab === 'stream') {
      activeTab.value = newTab
    }
  }
)

function setTab(tab: 'stream' | 'audit') {
  activeTab.value = tab
  router.replace({ query: { ...route.query, tab } })
}

// ============================================================================
// 1. REALTIME STREAMING LOGS STATE
// ============================================================================
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
  ws.onopen = () => {
    wsStatus.value = 'OPEN'
  }
  ws.onclose = () => {
    wsStatus.value = 'CLOSED'
    if (selectedSource.value === 'cloudflared' && activeTab.value === 'stream') {
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

const filteredWsLogs = computed(() => {
  let result = logs.value
  if (filter.value !== 'all') result = result.filter((l) => l.message.toLowerCase().includes(filter.value))
  if (search.value) {
    const s = search.value.toLowerCase()
    result = result.filter((l) => l.message.toLowerCase().includes(s))
  }
  return result
})

const filteredContainerLogs = computed(() => {
  if (!containerLogText.value) return []
  let lines = containerLogText.value.split('\n')
  if (filter.value !== 'all') {
    lines = lines.filter((l) => l.toLowerCase().includes(filter.value))
  }
  if (search.value) {
    const s = search.value.toLowerCase()
    lines = lines.filter((l) => l.toLowerCase().includes(s))
  }
  return lines
})

function getLogClass(msg: string) {
  if (msg.includes('ERR') || msg.includes('error') || msg.includes('FAIL')) return 'text-red-600 font-semibold'
  if (msg.includes('WRN') || msg.includes('warn')) return 'text-amber-600'
  return 'text-slate-700'
}

function copyAllLogs() {
  const text =
    selectedSource.value === 'cloudflared'
      ? filteredWsLogs.value.map((l) => `${l.time} ${l.message}`).join('\n')
      : filteredContainerLogs.value.join('\n')
  navigator.clipboard.writeText(text)
  copied.value = true
  setTimeout(() => (copied.value = false), 2000)
}

function downloadLogs() {
  const text =
    selectedSource.value === 'cloudflared'
      ? filteredWsLogs.value.map((l) => `${l.time} ${l.message}`).join('\n')
      : filteredContainerLogs.value.join('\n')
  const blob = new Blob([text], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${selectedSource.value === 'cloudflared' ? 'tunnel' : 'container'}-logs-${new Date().toISOString().slice(0, 10)}.log`
  a.click()
  URL.revokeObjectURL(url)
}

const currentSourceLabel = computed(() => {
  if (selectedSource.value === 'cloudflared') return 'Cloudflare Tunnel (cloudflared)'
  const c = containers.value.find((item) => item.id === selectedSource.value)
  return c ? c.name : selectedSource.value
})

// ============================================================================
// 2. AUDIT TRAIL STATE
// ============================================================================
const auditEntries = ref<any[]>([])
const auditTotal = ref(0)
const auditPage = ref(0)
const auditLimit = ref(20)
const auditFilterAction = ref('')
const auditSearch = ref('')
const auditLoading = ref(false)
const auditCopied = ref(false)
const selectedAuditDetail = ref<any | null>(null)

const auditActionOptions = [
  { value: '', label: 'All Actions' },
  { value: 'login', label: 'Logins' },
  { value: 'logout', label: 'Logouts' },
  { value: 'restart', label: 'Tunnel Restarts' },
  { value: 'config_edit', label: 'Config Edits' },
  { value: 'project_create', label: 'Project Created' },
  { value: 'project_up', label: 'Stack Compose Up' },
  { value: 'project_down', label: 'Stack Compose Down' },
  { value: 'project_restart', label: 'Stack Restart' },
  { value: 'project_deploy', label: 'Git Deployments' },
  { value: 'project_compose_save', label: 'Compose YAML Edits' },
  { value: 'docker_run', label: 'Docker Run Executed' },
]

async function fetchAudit() {
  auditLoading.value = true
  try {
    const params = new URLSearchParams({
      limit: String(auditLimit.value),
      offset: String(auditPage.value * auditLimit.value),
    })
    if (auditFilterAction.value) params.set('action', auditFilterAction.value)
    const data = await apiFetch<{ entries: any[]; total: number }>(`/api/audit?${params}`)
    auditEntries.value = data?.entries || []
    auditTotal.value = data?.total || 0
  } catch (err) {
    auditEntries.value = []
    auditTotal.value = 0
  } finally {
    auditLoading.value = false
  }
}

watch([auditPage, auditLimit, auditFilterAction], () => {
  fetchAudit()
})

const filteredAuditEntries = computed(() => {
  if (!auditSearch.value.trim()) return auditEntries.value
  const q = auditSearch.value.toLowerCase()
  return auditEntries.value.filter(
    (e) =>
      (e.action && e.action.toLowerCase().includes(q)) ||
      (e.detail && e.detail.toLowerCase().includes(q)) ||
      (e.ip_address && e.ip_address.toLowerCase().includes(q)) ||
      (e.created_at && e.created_at.toLowerCase().includes(q))
  )
})

function getActionBadgeStyle(action: string) {
  if (action === 'login') return 'bg-emerald-950/80 text-emerald-600 border-emerald-800/60'
  if (action === 'logout' || action.includes('delete')) return 'bg-rose-950/80 text-rose-600 border-rose-800/60'
  if (action.includes('project') || action.includes('compose')) return 'bg-indigo-950/80 text-indigo-700 border-indigo-800/60'
  if (action.includes('deploy') || action.includes('run')) return 'bg-violet-950/80 text-violet-300 border-violet-800/60'
  if (action.includes('config')) return 'bg-cyan-950/80 text-cyan-600 border-cyan-800/60'
  return 'bg-slate-100 text-slate-700 border-slate-300'
}

function copyAuditData() {
  const jsonStr = JSON.stringify(auditEntries.value, null, 2)
  navigator.clipboard.writeText(jsonStr)
  auditCopied.value = true
  setTimeout(() => (auditCopied.value = false), 2000)
}

function exportAuditJson() {
  const jsonStr = JSON.stringify(auditEntries.value, null, 2)
  const blob = new Blob([jsonStr], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `docktun-audit-${new Date().toISOString().slice(0, 10)}.json`
  a.click()
  URL.revokeObjectURL(url)
}

// Lifecycle Hooks
onMounted(async () => {
  await fetchContainers()
  if (activeTab.value === 'stream') {
    connectWs()
  } else {
    fetchAudit()
  }
})

watch(activeTab, (tab) => {
  if (tab === 'stream') {
    if (selectedSource.value === 'cloudflared' && (!ws || ws.readyState !== WebSocket.OPEN)) {
      connectWs()
    }
  } else if (tab === 'audit') {
    fetchAudit()
  }
})

onUnmounted(() => {
  if (ws) ws.close()
  if (containerPollTimer) clearInterval(containerPollTimer)
})
</script>

<template>
  <div class="space-y-4 h-full flex flex-col">
    <!-- Header with Dual Tabs -->
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between border-b border-slate-200 pb-3">
      <div>
        <p class="text-[10px] font-semibold uppercase tracking-[0.2em] text-slate-600">Activity & Telemetry</p>
        <h1 class="text-xl lg:text-2xl font-bold tracking-tight text-slate-900 flex items-center gap-2">
          <component :is="activeTab === 'stream' ? ScrollText : Shield" class="h-6 w-6 text-cyan-600" />
          <span>{{ activeTab === 'stream' ? 'Realtime Logs' : 'Audit Trail' }}</span>
        </h1>
      </div>

      <!-- Tab Switcher -->
      <div class="flex items-center gap-2">
        <div class="flex rounded-lg border border-slate-200 bg-white/80 p-1">
          <button
            @click="setTab('stream')"
            class="flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-semibold uppercase tracking-wider transition-all"
            :class="activeTab === 'stream' ? 'bg-cyan-950/80 text-cyan-700 border border-cyan-800/80 shadow-sm' : 'text-slate-600 hover:text-slate-800'"
          >
            <ScrollText class="h-3.5 w-3.5" />
            <span>Realtime Logs</span>
          </button>
          <button
            @click="setTab('audit')"
            class="flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-semibold uppercase tracking-wider transition-all"
            :class="activeTab === 'audit' ? 'bg-indigo-950/80 text-indigo-700 border border-indigo-800/80 shadow-sm' : 'text-slate-600 hover:text-slate-800'"
          >
            <Shield class="h-3.5 w-3.5" />
            <span>Audit Trail</span>
          </button>
        </div>
      </div>
    </div>

    <!-- ===================================================================== -->
    <!-- TAB 1: REALTIME LOGS STREAM                                           -->
    <!-- ===================================================================== -->
    <div v-if="activeTab === 'stream'" class="space-y-4 flex-1 flex flex-col">
      <!-- Service Selector & Connection Badge -->
      <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 bg-white/60 border border-slate-200 rounded-lg p-3 text-xs">
        <div class="flex flex-wrap items-center gap-2">
          <label class="text-xs text-slate-600 font-medium">Service Source:</label>
          <select
            v-model="selectedSource"
            class="cyber-input text-xs font-mono py-1.5 px-3 min-w-[240px] border-cyan-800/80 bg-white"
          >
            <option value="cloudflared">Tunnel Daemon (cloudflared)</option>
            <optgroup label="Docker Containers (Isolated)">
              <option v-for="c in containers" :key="c.id" :value="c.id">
                {{ c.name }}
              </option>
            </optgroup>
          </select>

          <span
            v-if="selectedSource === 'cloudflared'"
            class="rounded px-2.5 py-1 text-[10px] font-mono font-bold uppercase tracking-wider"
            :class="wsStatus === 'OPEN' ? 'bg-emerald-950/80 text-emerald-600 border border-emerald-800/60' : 'bg-red-950/80 text-red-600 border border-red-800/60'"
          >
            WS: {{ wsStatus }}
          </span>
          <span
            v-else
            class="rounded px-2.5 py-1 text-[10px] font-mono font-bold uppercase tracking-wider bg-cyan-950/80 text-cyan-600 border border-cyan-800/60"
          >
            POLLING: 3S
          </span>
        </div>

        <div class="flex items-center gap-2">
          <button
            @click="copyAllLogs"
            class="cyber-btn-secondary text-xs flex items-center gap-1.5 py-1 px-3"
            title="Copy filtered logs"
          >
            <Check v-if="copied" class="h-3.5 w-3.5 text-emerald-600" />
            <Copy v-else class="h-3.5 w-3.5" />
            <span>{{ copied ? 'Copied' : 'Copy' }}</span>
          </button>
          <button
            @click="downloadLogs"
            class="cyber-btn-secondary text-xs flex items-center gap-1.5 py-1 px-3 text-cyan-700"
            title="Download logs as .log file"
          >
            <Download class="h-3.5 w-3.5" />
            <span>Download</span>
          </button>
          <button
            v-if="selectedSource !== 'cloudflared'"
            @click="fetchContainerLogs"
            :disabled="loadingContainerLogs"
            class="cyber-btn-secondary text-xs flex items-center gap-1.5 py-1 px-3"
          >
            <RotateCcw class="h-3.5 w-3.5" :class="loadingContainerLogs ? 'animate-spin' : ''" />
            <span>Refresh</span>
          </button>
        </div>
      </div>

      <!-- Filters & Search Toolbar -->
      <div class="flex flex-col sm:flex-row gap-2 sm:items-center">
        <div class="flex border border-slate-200 rounded-lg overflow-hidden bg-slate-50">
          <button
            v-for="f in ['all', 'error', 'warn', 'info']"
            :key="f"
            @click="filter = f"
            :class="filter === f ? 'bg-cyan-950/80 text-cyan-700 font-bold border-r border-slate-200' : 'text-slate-600 hover:text-slate-800 border-r border-slate-200'"
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
          <label class="flex items-center gap-1.5 text-xs text-slate-600 whitespace-nowrap cursor-pointer">
            <input type="checkbox" v-model="autoScroll" class="rounded bg-slate-50" />
            <span>Auto-tail</span>
          </label>
        </div>
      </div>

      <!-- Log Viewer Box -->
      <div ref="logContainer" class="flex-1 cyber-card !p-0 overflow-auto min-h-[460px] max-h-[68vh] border border-slate-200 bg-slate-50 font-mono text-xs">
        <!-- 1. Cloudflared WS Stream -->
        <template v-if="selectedSource === 'cloudflared'">
          <div
            v-for="(log, i) in filteredWsLogs"
            :key="i"
            class="px-4 py-1 border-b border-slate-200/40 hover:bg-white/60 flex items-start gap-2"
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
            class="px-4 py-1 border-b border-slate-200/40 hover:bg-white/60 flex items-start gap-3"
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

    <!-- ===================================================================== -->
    <!-- TAB 2: AUDIT TRAIL                                                    -->
    <!-- ===================================================================== -->
    <div v-else-if="activeTab === 'audit'" class="space-y-4 flex-1 flex flex-col">
      <!-- Audit Filter Bar -->
      <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 bg-white/60 border border-slate-200 rounded-lg p-3">
        <div class="flex flex-wrap items-center gap-2">
          <label class="text-xs text-slate-600 font-medium">Filter Action:</label>
          <select
            v-model="auditFilterAction"
            class="cyber-input text-xs font-mono py-1.5 px-3 min-w-[200px] border-indigo-800/80 bg-white"
          >
            <option v-for="opt in auditActionOptions" :key="opt.value" :value="opt.value">
              {{ opt.label }}
            </option>
          </select>

          <!-- Search Query in Detail / IP -->
          <div class="relative">
            <Search class="absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500" />
            <input
              v-model="auditSearch"
              placeholder="Search detail, origin IP..."
              class="cyber-input !py-1.5 pl-8 !text-xs font-mono w-48 sm:w-64"
            />
          </div>
        </div>

        <div class="flex items-center gap-2">
          <button
            @click="copyAuditData"
            class="cyber-btn-secondary text-xs flex items-center gap-1.5 py-1 px-3"
            title="Copy current audit entries JSON"
          >
            <Check v-if="auditCopied" class="h-3.5 w-3.5 text-emerald-600" />
            <Copy v-else class="h-3.5 w-3.5" />
            <span>{{ auditCopied ? 'Copied' : 'Copy' }}</span>
          </button>
          <button
            @click="exportAuditJson"
            class="cyber-btn-secondary text-xs flex items-center gap-1.5 py-1 px-3 text-cyan-700"
            title="Export audit logs to JSON"
          >
            <Download class="h-3.5 w-3.5" />
            <span>Export</span>
          </button>
          <button
            @click="fetchAudit"
            :disabled="auditLoading"
            class="cyber-btn-secondary text-xs flex items-center gap-1.5 py-1 px-3"
            title="Refresh audit records"
          >
            <RotateCcw class="h-3.5 w-3.5" :class="auditLoading ? 'animate-spin' : ''" />
            <span>Refresh</span>
          </button>
        </div>
      </div>

      <!-- Audit Table -->
      <div class="cyber-card !p-0 overflow-x-auto border border-slate-200 bg-slate-50 flex-1 min-h-[440px]">
        <table class="w-full text-xs font-mono">
          <thead>
            <tr class="border-b border-slate-200 bg-white/90 text-left">
              <th class="px-4 py-3 text-slate-600 uppercase tracking-widest font-semibold">Timestamp</th>
              <th class="px-4 py-3 text-slate-600 uppercase tracking-widest font-semibold">Action</th>
              <th class="px-4 py-3 text-slate-600 uppercase tracking-widest font-semibold">Detail</th>
              <th class="px-4 py-3 text-slate-600 uppercase tracking-widest font-semibold hidden md:table-cell">Origin IP</th>
              <th class="px-4 py-3 text-slate-600 uppercase tracking-widest font-semibold text-right">Inspect</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-200/50">
            <tr
              v-for="e in filteredAuditEntries"
              :key="e.id"
              class="hover:bg-white/60 transition-colors"
            >
              <td class="px-4 py-2.5 text-slate-600 whitespace-nowrap">
                {{ e.created_at }}
              </td>
              <td class="px-4 py-2.5 whitespace-nowrap">
                <span
                  class="rounded border px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider"
                  :class="getActionBadgeStyle(e.action)"
                >
                  {{ e.action }}
                </span>
              </td>
              <td class="px-4 py-2.5 text-slate-700 max-w-md truncate" :title="e.detail">
                {{ e.detail?.split('\n')[0] || '—' }}
                <span v-if="e.detail && e.detail.includes('\n')" class="text-slate-500 text-[10px] ml-1">
                  (+{{ e.detail.split('\n').length - 1 }} lines)
                </span>
              </td>
              <td class="px-4 py-2.5 text-slate-600 hidden md:table-cell whitespace-nowrap">
                {{ e.ip_address || 'local' }}
              </td>
              <td class="px-4 py-2.5 text-right whitespace-nowrap">
                <button
                  @click="selectedAuditDetail = e"
                  class="cyber-btn-secondary !px-2 !py-0.5 !text-[10px] text-cyan-700 hover:text-cyan-200"
                  title="View full audit payload and output"
                >
                  <Eye class="h-3 w-3 inline mr-1" /> View
                </button>
              </td>
            </tr>
            <tr v-if="!filteredAuditEntries.length">
              <td colspan="5" class="px-4 py-16 text-center text-slate-500 tracking-wider">
                {{ auditLoading ? '// LOADING AUDIT LOGS...' : '// NO AUDIT RECORDS MATCH' }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- Pagination Footer -->
      <div class="flex flex-col sm:flex-row items-center justify-between gap-3 text-xs text-slate-600 px-1">
        <div class="flex items-center gap-3">
          <span>Total Records: <strong class="text-slate-800 font-mono">{{ auditTotal }}</strong></span>
          <span class="text-slate-600">|</span>
          <div class="flex items-center gap-1.5">
            <span>Per page:</span>
            <select
              v-model="auditLimit"
              class="cyber-input !py-0.5 !px-2 !text-xs !w-auto border-slate-200 bg-slate-50 font-mono"
            >
              <option :value="10">10</option>
              <option :value="20">20</option>
              <option :value="50">50</option>
              <option :value="100">100</option>
            </select>
          </div>
        </div>

        <div class="flex items-center gap-2">
          <span class="font-mono text-slate-500 mr-2">
            Page {{ auditPage + 1 }} of {{ Math.max(1, Math.ceil(auditTotal / auditLimit)) }}
          </span>
          <button
            @click="auditPage--"
            :disabled="auditPage === 0"
            class="cyber-btn-secondary !px-3 !py-1 text-xs disabled:opacity-30 flex items-center gap-1"
          >
            <ChevronLeft class="h-3.5 w-3.5" />
            <span>Prev</span>
          </button>
          <button
            @click="auditPage++"
            :disabled="(auditPage + 1) * auditLimit >= auditTotal"
            class="cyber-btn-secondary !px-3 !py-1 text-xs disabled:opacity-30 flex items-center gap-1"
          >
            <span>Next</span>
            <ChevronRight class="h-3.5 w-3.5" />
          </button>
        </div>
      </div>
    </div>

    <!-- Audit Detail Modal -->
    <div
      v-if="selectedAuditDetail"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-sm p-4"
      @click.self="selectedAuditDetail = null"
    >
      <div class="cyber-card max-w-2xl w-full max-h-[85vh] flex flex-col border-cyan-500/40 p-5 space-y-4">
        <div class="flex items-center justify-between border-b border-slate-200 pb-3">
          <div class="flex items-center gap-2">
            <Shield class="h-5 w-5 text-cyan-600" />
            <h3 class="text-sm font-bold text-slate-900 font-mono">
              Audit Event #{{ selectedAuditDetail.id }}
            </h3>
          </div>
          <button
            @click="selectedAuditDetail = null"
            class="text-slate-600 hover:text-slate-800 transition"
          >
            <X class="h-4 w-4" />
          </button>
        </div>

        <div class="grid grid-cols-2 gap-3 text-xs font-mono">
          <div>
            <span class="text-slate-500">Action:</span>
            <span
              class="ml-2 rounded border px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider"
              :class="getActionBadgeStyle(selectedAuditDetail.action)"
            >
              {{ selectedAuditDetail.action }}
            </span>
          </div>
          <div>
            <span class="text-slate-500">Timestamp:</span>
            <span class="ml-2 text-slate-700">{{ selectedAuditDetail.created_at }}</span>
          </div>
          <div>
            <span class="text-slate-500">Origin IP:</span>
            <span class="ml-2 text-slate-700">{{ selectedAuditDetail.ip_address || 'local' }}</span>
          </div>
          <div>
            <span class="text-slate-500">User ID:</span>
            <span class="ml-2 text-slate-700">{{ selectedAuditDetail.user_id || 'System / Anonymous' }}</span>
          </div>
        </div>

        <div class="space-y-1.5 flex-1 flex flex-col min-h-0">
          <label class="text-[10px] font-semibold uppercase tracking-widest text-slate-600">
            Action Detail & Output Payload
          </label>
          <pre class="flex-1 overflow-auto rounded-lg border border-slate-200 bg-slate-50 p-3 text-xs font-mono text-slate-800 whitespace-pre-wrap break-all max-h-80">{{ selectedAuditDetail.detail || '(No details recorded)' }}</pre>
        </div>

        <div class="flex justify-end pt-2">
          <button
            @click="selectedAuditDetail = null"
            class="cyber-btn !px-4 !py-1.5 text-xs text-slate-700"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
