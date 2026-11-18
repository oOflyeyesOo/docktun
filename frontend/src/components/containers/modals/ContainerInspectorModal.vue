<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useApi } from '../../../composables/useApi'
import {
  Activity,
  Globe,
  FileCode,
  Sliders,
  FolderOpen,
  ScrollText,
  Terminal,
  Code2,
  X,
  Play,
  Square,
  RotateCcw,
  Check,
  Copy,
  Cpu,
  Layers,
  ArrowUpDown,
  HardDrive,
  Sparkles,
  Info,
  CheckCircle2,
  ArrowUpCircle,
  Wifi,
  ExternalLink,
  Rocket,
  History,
  Trash2,
  Save,
  GitBranch,
  Plus,
} from 'lucide-vue-next'
import { hasLocalWebLink, localAddress } from '../../../lib/containerPorts.mjs'
import { parseMemoryUsage, getMemoryUsageColor } from '../../../lib/containers-table.mjs'
import ContainerExposure from '../../ContainerExposure.vue'
import InspectorLogsTab from '../inspector/tabs/InspectorLogsTab.vue'
import InspectorTerminalTab from '../inspector/tabs/InspectorTerminalTab.vue'
import InspectorFilesTab from '../inspector/tabs/InspectorFilesTab.vue'
import InspectorEnvTab from '../inspector/tabs/InspectorEnvTab.vue'
import InspectorInspectTab from '../inspector/tabs/InspectorInspectTab.vue'

export interface ContainerDetail {
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
  zero_trust_enabled?: boolean
  zero_trust_emails?: string
  zero_trust_auth_type?: string
  zero_trust_session?: string
  cpu?: string
  memory?: string
  network?: string
  block_io?: string
  pids?: number
  restart_count?: number
  uptime?: string
  exposure?: any
  project_id?: number
  project_name?: string
  project_path?: string
  compose_project?: string
  compose_service?: string
  compose_working_dir?: string
  compose_sources?: string[]
  compose_config_files?: string
  compose_source_error?: string
  mounts?: Array<{ source: string; destination: string; mode?: string; rw?: boolean }>
  env?: string[]
  networks?: string[]
  ip_address?: string
  gateway?: string
  connected_to_cloudflared?: boolean
  update_available?: boolean
  update_error?: string
  update_checked_at?: string
  current_digest?: string
  latest_digest?: string
  [key: string]: any
}

const props = defineProps<{
  container: ContainerDetail | null
  activeTab: 'overview' | 'access' | 'compose' | 'env' | 'files' | 'logs' | 'exec' | 'inspect'
  actionLoading?: Record<string, string | boolean>
  stackActionLoading?: Record<number, boolean>
  projectFileAccess?: any
  projectFilesWritable?: boolean
  composeFilename?: string
  composeContent?: string
  composeLoading?: boolean
  composeSaving?: boolean
  composeMsg?: string
  composeEditor?: any
  currentProject?: any
  projectGitSaving?: boolean
  projectScaffolding?: boolean
  detailLoading?: boolean
  disconnectingNetwork?: Record<string, boolean>
  checkingSingleUpdate?: Record<string, boolean>
  updatingContainer?: Record<string, boolean>
  exposeSaving?: Record<string, boolean>
  exposeMsg?: Record<string, any>
  getExpose?: (id: string) => any
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'update:activeTab', tab: 'overview' | 'access' | 'compose' | 'env' | 'files' | 'logs' | 'exec' | 'inspect'): void
  (e: 'run-action', container: ContainerDetail, action: 'start' | 'stop' | 'restart'): void
  (e: 'run-stack-action', projectId: number, action: 'up' | 'down' | 'restart'): void
  (e: 'deploy-stack', projectId: number, name: string): void
  (e: 'stack-history', projectId: number, name: string): void
  (e: 'delete-stack', projectId: number, name: string, path?: string): void
  (e: 'save-compose', restart: boolean): void
  (e: 'update:composeContent', val: string): void
  (e: 'save-project-git'): void
  (e: 'check-single-update', container: ContainerDetail): void
  (e: 'confirm-update', container: ContainerDetail): void
  (e: 'disconnect-network', container: ContainerDetail, network: string): void
  (e: 'scaffold-project'): void
  (e: 'save-exposure', container: ContainerDetail): void
  (e: 'refresh-containers'): void
}>()

const copiedKey = ref('')
const showGitSettings = ref(false)

function copyToClipboard(key: string, text?: string) {
  if (!text) return
  navigator.clipboard.writeText(text).then(() => {
    copiedKey.value = key
    setTimeout(() => {
      copiedKey.value = ''
    }, 2000)
  })
}

function parseCpuPercent(cpuStr?: string): number {
  if (!cpuStr) return 0
  const n = parseFloat(cpuStr.replace('%', ''))
  return isNaN(n) ? 0 : Math.min(n, 100)
}

const { apiFetch } = useApi()

// Resource limits tuning state
const resourcesSaving = ref(false)
const resourcesMsg = ref<{ type: 'success' | 'error'; text: string } | null>(null)
let resourcesMsgTimer: any = null

const resourceDraft = ref({
  cpus: 0,
  memory_mode: 'unlimited' as 'unlimited' | '256m' | '512m' | '1g' | '2g' | '4g' | 'custom',
  memory_custom_mb: 512,
  restart_policy: 'unless-stopped'
})

function initResourceDraft() {
  if (!props.container) return
  const res = props.container.resources || {}
  const cpus = res.cpus || 0
  const memBytes = res.memory_bytes || 0
  let policy = res.restart_policy || props.container.restart_policy || 'unless-stopped'
  if (policy.startsWith('on-failure')) policy = 'on-failure'

  let memMode: 'unlimited' | '256m' | '512m' | '1g' | '2g' | '4g' | 'custom' = 'unlimited'
  let customMb = 512

  if (!memBytes || memBytes === 0) {
    memMode = 'unlimited'
  } else if (memBytes === 256 * 1024 * 1024) {
    memMode = '256m'
  } else if (memBytes === 512 * 1024 * 1024) {
    memMode = '512m'
  } else if (memBytes === 1024 * 1024 * 1024) {
    memMode = '1g'
  } else if (memBytes === 2048 * 1024 * 1024) {
    memMode = '2g'
  } else if (memBytes === 4096 * 1024 * 1024) {
    memMode = '4g'
  } else {
    memMode = 'custom'
    customMb = Math.round(memBytes / (1024 * 1024))
  }

  resourceDraft.value = {
    cpus,
    memory_mode: memMode,
    memory_custom_mb: customMb,
    restart_policy: policy
  }
}

watch(() => props.container, () => {
  initResourceDraft()
}, { immediate: true })

async function applyResourceLimits() {
  if (!props.container) return
  resourcesSaving.value = true
  if (resourcesMsgTimer) clearTimeout(resourcesMsgTimer)
  resourcesMsg.value = null

  let memBytes = 0
  if (resourceDraft.value.memory_mode === '256m') memBytes = 256 * 1024 * 1024
  else if (resourceDraft.value.memory_mode === '512m') memBytes = 512 * 1024 * 1024
  else if (resourceDraft.value.memory_mode === '1g') memBytes = 1024 * 1024 * 1024
  else if (resourceDraft.value.memory_mode === '2g') memBytes = 2048 * 1024 * 1024
  else if (resourceDraft.value.memory_mode === '4g') memBytes = 4096 * 1024 * 1024
  else if (resourceDraft.value.memory_mode === 'custom') memBytes = Math.max(4, resourceDraft.value.memory_custom_mb) * 1024 * 1024

  try {
    const res: any = await apiFetch(`/api/docker/containers/${props.container.id}/resources`, {
      method: 'POST',
      body: JSON.stringify({
        cpus: Number(resourceDraft.value.cpus) || 0,
        memory_bytes: memBytes,
        restart_policy: resourceDraft.value.restart_policy
      })
    })

    if (props.container) {
      if (res.resources) {
        props.container.resources = res.resources
        props.container.restart_policy = res.resources.restart_policy
      }
    }
    resourcesMsg.value = {
      type: 'success',
      text: 'Live resource limits updated via docker update (no restart required)!'
    }
    emit('refresh-containers')
  } catch (err: any) {
    resourcesMsg.value = {
      type: 'error',
      text: err.message || 'Failed to update resource limits'
    }
  } finally {
    resourcesSaving.value = false
    resourcesMsgTimer = setTimeout(() => {
      resourcesMsg.value = null
    }, 5000)
  }
}

const router = useRouter()

const isDockTun = computed(() => {
  if (!props.container) return false
  const name = (props.container.name || '').toLowerCase()
  const image = (props.container.image || '').toLowerCase()
  const project = (props.container.compose_project || props.container.project_name || '').toLowerCase()
  return name === 'docktun' || name.includes('docktun') || image.includes('docktun') || project === 'docktun'
})

function openDockTunUpdater() {
  emit('close')
  if (router) {
    router.push('/settings#docktun-updater')
  } else {
    window.location.href = '/settings#docktun-updater'
  }
}

function onKeyDown(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    emit('close')
  }
}

onMounted(() => {
  window.addEventListener('keydown', onKeyDown)
})

onUnmounted(() => {
  window.removeEventListener('keydown', onKeyDown)
})

function selectTab(tab: 'overview' | 'access' | 'compose' | 'env' | 'files' | 'logs' | 'exec' | 'inspect') {
  emit('update:activeTab', tab)
}

let backdropMouseDown = false

function onBackdropMouseDown(e: MouseEvent) {
  backdropMouseDown = e.target === e.currentTarget
}

function onBackdropClick(e: MouseEvent) {
  const selection = window.getSelection()?.toString()
  if (selection && selection.length > 0) {
    backdropMouseDown = false
    return
  }
  if (backdropMouseDown && e.target === e.currentTarget) {
    emit('close')
  }
  backdropMouseDown = false
}
</script>

<template>
  <div
    v-if="container"
    class="fixed inset-0 z-50 flex items-center justify-center bg-slate-50/85 p-2 sm:p-4 backdrop-blur-md"
    tabindex="-1"
    @keydown.esc="emit('close')"
    @mousedown="onBackdropMouseDown"
    @click="onBackdropClick"
  >
    <div
      class="flex max-h-[92vh] w-full max-w-5xl flex-col rounded-xl border border-slate-300 bg-white shadow-sm overflow-hidden"
      @mousedown.stop
      @mouseup.stop
      @click.stop
    >
      <!-- Modal Top Bar -->
      <div class="flex flex-wrap items-center justify-between border-b border-slate-200 bg-slate-50 px-5 py-3 gap-3">
        <div class="flex items-center gap-3">
          <span
            class="relative flex h-3 w-3"
            :title="container.running ? 'Running' : 'Stopped'"
          >
            <span
              v-if="container.running"
              class="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75"
            ></span>
            <span
              class="relative inline-flex h-3 w-3 rounded-full"
              :class="container.running ? 'bg-emerald-500' : 'bg-slate-300'"
            ></span>
          </span>

          <div>
            <div class="flex items-center gap-2">
              <h2 class="text-base font-bold text-slate-900 font-mono">{{ container.name }}</h2>
              <span class="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] font-mono text-slate-600">
                {{ container.id.slice(0, 12) }}
              </span>
              <button
                @click="copyToClipboard('cid', container.id)"
                class="text-slate-500 hover:text-slate-700"
                title="Copy full Container ID"
              >
                <Check v-if="copiedKey === 'cid'" class="h-3 w-3 text-emerald-600" />
                <Copy v-else class="h-3 w-3" />
              </button>
            </div>
            <p class="text-[11px] text-slate-600 font-mono truncate max-w-md">{{ container.image }}</p>
          </div>
        </div>

        <!-- Top Quick Actions -->
        <div class="flex items-center gap-2">
          <button
            v-if="!container.running"
            @click="emit('run-action', container, 'start')"
            :disabled="!!actionLoading?.[container.id]"
            class="cyber-btn flex items-center gap-1.5 text-xs text-emerald-700"
          >
            <Play class="h-3 w-3 fill-current" />
            <span>Start</span>
          </button>
          <button
            v-if="container.running"
            @click="emit('run-action', container, 'stop')"
            :disabled="!!actionLoading?.[container.id]"
            class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-amber-700"
          >
            <Square class="h-3 w-3 fill-current" />
            <span>Stop</span>
          </button>
          <button
            @click="emit('run-action', container, 'restart')"
            :disabled="!!actionLoading?.[container.id]"
            class="cyber-btn-secondary flex items-center gap-1.5 text-xs"
          >
            <RotateCcw class="h-3 w-3" :class="actionLoading?.[container.id] === 'restart' ? 'animate-spin' : ''" />
            <span>Restart</span>
          </button>
          <button
            @click="emit('close')"
            class="rounded-lg p-1 text-slate-600 hover:bg-slate-100 hover:text-slate-800"
          >
            <X class="h-5 w-5" />
          </button>
        </div>
      </div>

      <!-- Modal Navigation Tabs -->
      <div class="flex items-center gap-1 overflow-x-auto border-b border-slate-200 bg-slate-50/80 px-4 py-2 text-xs">
        <button
          @click="selectTab('overview')"
          class="flex items-center gap-2 rounded-lg px-3 py-1.5 font-medium transition-colors whitespace-nowrap cursor-pointer"
          :class="activeTab === 'overview' ? 'bg-cyan-50/60 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
        >
          <Activity class="h-3.5 w-3.5" />
          <span>Overview</span>
        </button>

        <button
          @click="selectTab('access')"
          class="flex items-center gap-2 rounded-lg px-3 py-1.5 font-medium transition-colors whitespace-nowrap cursor-pointer"
          :class="activeTab === 'access' ? 'bg-cyan-50/60 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
        >
          <Globe class="h-3.5 w-3.5" />
          <span>Access & Networks</span>
          <span
            v-if="container.connected_to_cloudflared"
            class="h-1.5 w-1.5 rounded-full bg-emerald-400"
            title="Cloudflared network connected"
          ></span>
        </button>

        <button
          @click="selectTab('compose')"
          class="flex items-center gap-2 rounded-lg px-3 py-1.5 font-medium transition-colors whitespace-nowrap cursor-pointer"
          :class="activeTab === 'compose' ? 'bg-cyan-50/60 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
        >
          <FileCode class="h-3.5 w-3.5" />
          <span>Compose</span>
          <span
            v-if="container.project_id"
            class="rounded bg-cyan-100/60 px-1 py-0.2 text-[9px] text-cyan-700 font-mono"
          >
            stack
          </span>
        </button>

        <button
          @click="selectTab('env')"
          class="flex items-center gap-2 rounded-lg px-3 py-1.5 font-medium transition-colors whitespace-nowrap cursor-pointer"
          :class="activeTab === 'env' ? 'bg-cyan-50/60 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
        >
          <Sliders class="h-3.5 w-3.5" />
          <span>Environment (.env)</span>
        </button>

        <button
          @click="selectTab('files')"
          class="flex items-center gap-2 rounded-lg px-3 py-1.5 font-medium transition-colors whitespace-nowrap cursor-pointer"
          :class="activeTab === 'files' ? 'bg-cyan-50/60 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
        >
          <FolderOpen class="h-3.5 w-3.5" />
          <span>Files & Storage</span>
        </button>

        <button
          @click="selectTab('logs')"
          class="flex items-center gap-2 rounded-lg px-3 py-1.5 font-medium transition-colors whitespace-nowrap cursor-pointer"
          :class="activeTab === 'logs' ? 'bg-cyan-50/60 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
        >
          <ScrollText class="h-3.5 w-3.5" />
          <span>Realtime Logs</span>
        </button>

        <button
          @click="selectTab('exec')"
          class="flex items-center gap-2 rounded-lg px-3 py-1.5 font-medium transition-colors whitespace-nowrap cursor-pointer"
          :class="activeTab === 'exec' ? 'bg-cyan-50/60 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
        >
          <Terminal class="h-3.5 w-3.5" />
          <span>Terminal (Exec)</span>
        </button>

        <button
          @click="selectTab('inspect')"
          class="flex items-center gap-2 rounded-lg px-3 py-1.5 font-medium transition-colors whitespace-nowrap cursor-pointer"
          :class="activeTab === 'inspect' ? 'bg-cyan-50/60 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
        >
          <Code2 class="h-3.5 w-3.5" />
          <span>Inspect (JSON)</span>
        </button>
      </div>

      <!-- Tab Body Container -->
      <div class="flex-1 overflow-y-auto p-5 text-slate-800">
        <div
          v-if="['compose', 'env', 'files'].includes(activeTab) && projectFileAccess"
          class="mb-4 rounded-lg border p-3 text-sm"
          :class="projectFileAccess.available && projectFileAccess.writable ? 'border-slate-200 bg-slate-50' : 'border-amber-300 bg-amber-50'"
          role="status"
        >
          <p v-if="!projectFileAccess.available" class="font-semibold">Stack files are unavailable in DockTun</p>
          <p v-else-if="!projectFileAccess.writable" class="font-semibold">Stack files are read-only</p>
          <p v-if="projectFileAccess.reason">{{ projectFileAccess.reason }}</p>
          <p v-if="projectFileAccess.resolved_path && projectFileAccess.resolved_path !== projectFileAccess.path" class="break-all text-xs">
            Host folder {{ projectFileAccess.path }} is available at {{ projectFileAccess.resolved_path }} inside DockTun.
          </p>
          <p v-if="!projectFileAccess.available" class="mt-1 text-xs">
            Container start, stop, logs, and exposure controls remain available. Add the folder mount to DockTun and recreate DockTun to enable file management.
          </p>
        </div>

        <!-- Loading indicator -->
        <div v-if="detailLoading" class="flex items-center justify-center py-16 text-slate-600 text-sm">
          <RotateCcw class="h-5 w-5 animate-spin mr-2 text-cyan-600" />
          <span>Loading container telemetry & metadata…</span>
        </div>

        <!-- ============================================== -->
        <!-- TAB 1: OVERVIEW & TELEMETRY                    -->
        <!-- ============================================== -->
        <div v-else-if="activeTab === 'overview'" class="space-y-5">
          <div class="rounded-lg border border-slate-200 bg-slate-50 p-4 text-xs space-y-2">
            <h3 class="font-bold text-slate-800">Stack membership</h3>
            <template v-if="container.compose_project">
              <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2">
                <dt class="text-slate-500">Compose project</dt>
                <dd class="font-mono break-all">{{ container.compose_project }}</dd>
                <dt class="text-slate-500">Service</dt>
                <dd class="font-mono break-all">{{ container.compose_service || 'Not reported by Docker' }}</dd>
                <dt class="text-slate-500">Source folder</dt>
                <dd class="font-mono break-all">{{ container.compose_working_dir || 'Not reported by Docker' }}</dd>
                <dt class="text-slate-500">Compose files</dt>
                <dd class="font-mono break-all">
                  {{
                    container.compose_sources?.join(', ') ||
                    (container.compose_config_files === '-'
                      ? 'Streamed input; original files not recorded'
                      : container.compose_config_files) ||
                    'Not reported by Docker'
                  }}
                </dd>
              </dl>
              <p class="text-slate-500">
                Docker reports this membership. Container names and shared networks do not place services in the same stack. Different Compose projects stay separate.
              </p>
              <p v-if="container.compose_source_error" class="text-amber-700">{{ container.compose_source_error }}</p>
            </template>
            <p v-else class="text-slate-500">
              Docker reports no Compose project for this container. It is standalone; its name does not associate it with another stack.
            </p>
          </div>

          <!-- Telemetry Gauges -->
          <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <!-- CPU -->
            <router-link
              :to="`/metrics?container=${encodeURIComponent(container.name)}`"
              class="rounded-lg border border-slate-200 bg-slate-50 p-3 hover:border-cyan-400 hover:shadow-xs transition group block"
              :title="`Open live telemetry in Metrics for ${container.name}`"
            >
              <div class="flex items-center justify-between text-xs text-slate-600 mb-1">
                <span class="flex items-center gap-1.5 font-semibold uppercase tracking-wider text-[10px] group-hover:text-cyan-600 transition">
                  <Cpu class="h-3.5 w-3.5 text-cyan-600" /> CPU Usage &rarr;
                </span>
                <span class="font-mono text-cyan-600 font-bold">{{ container.cpu || '0.0%' }}</span>
              </div>
              <div class="h-2 w-full overflow-hidden rounded-full bg-slate-100">
                <div
                  class="h-full bg-cyan-400 transition-all duration-300"
                  :style="{ width: `${parseCpuPercent(container.cpu)}%` }"
                ></div>
              </div>
            </router-link>

            <!-- RAM -->
            <div class="rounded-lg border border-slate-200 bg-slate-50 p-3">
              <div class="flex items-center justify-between text-xs text-slate-600 mb-1">
                <span class="flex items-center gap-1.5 font-semibold uppercase tracking-wider text-[10px]">
                  <Layers class="h-3.5 w-3.5 text-emerald-600" /> Memory
                </span>
                <span class="font-mono text-emerald-600 font-bold truncate max-w-[90px]">
                  {{ parseMemoryUsage(container.memory).used }}
                </span>
              </div>
              <div class="h-2 w-full overflow-hidden rounded-full bg-slate-100 mb-1">
                <div
                  class="h-full transition-all duration-300"
                  :class="getMemoryUsageColor(parseMemoryUsage(container.memory).percent)"
                  :style="{ width: `${Math.min(parseMemoryUsage(container.memory).percent, 100)}%` }"
                ></div>
              </div>
              <div class="flex items-center justify-between text-[10px] text-slate-500 font-mono truncate">
                <span>Limit: {{ parseMemoryUsage(container.memory).total || 'Unlimited' }}</span>
                <span v-if="parseMemoryUsage(container.memory).percent > 0">{{ parseMemoryUsage(container.memory).percent }}%</span>
              </div>
            </div>

            <!-- Network I/O -->
            <div class="rounded-lg border border-slate-200 bg-slate-50 p-3">
              <div class="flex items-center justify-between text-xs text-slate-600 mb-1">
                <span class="flex items-center gap-1.5 font-semibold uppercase tracking-wider text-[10px]">
                  <ArrowUpDown class="h-3.5 w-3.5 text-violet-400" /> Net Traffic
                </span>
              </div>
              <div class="font-mono text-xs text-slate-800 truncate">
                {{ container.network || '0B / 0B' }}
              </div>
            </div>

            <!-- Disk / Block I/O -->
            <div class="rounded-lg border border-slate-200 bg-slate-50 p-3">
              <div class="flex items-center justify-between text-xs text-slate-600 mb-1">
                <span class="flex items-center gap-1.5 font-semibold uppercase tracking-wider text-[10px]">
                  <HardDrive class="h-3.5 w-3.5 text-amber-600" /> Block I/O
                </span>
                <span class="text-[10px] text-slate-500 font-mono">{{ container.pids || 0 }} pids</span>
              </div>
              <div class="font-mono text-xs text-slate-800 truncate">
                {{ container.block_io || '0B / 0B' }}
              </div>
            </div>
          </div>

          <!-- Image & Remote Registry Updates Card -->
          <div class="rounded-lg border border-slate-200 bg-slate-50 p-4 space-y-3">
            <div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-2">
              <h3 class="text-xs font-bold uppercase tracking-wider text-slate-600 flex items-center gap-2">
                <Sparkles class="h-4 w-4 text-amber-600" /> Container Image & Registry Updates
              </h3>
              <div class="flex items-center gap-2">
                <button
                  v-if="isDockTun"
                  @click="openDockTunUpdater"
                  class="cyber-btn flex items-center gap-1.5 text-xs text-cyan-700 border-cyan-500 bg-cyan-50/70 hover:bg-cyan-100"
                  title="Open DockTun Updater in Settings"
                >
                  <Sparkles class="h-3.5 w-3.5 text-cyan-600" />
                  <span>Open DockTun Updater</span>
                </button>
                <button
                  @click="emit('check-single-update', container)"
                  :disabled="!!checkingSingleUpdate?.[container.id]"
                  class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-amber-700 hover:border-amber-500/50"
                >
                  <RotateCcw class="h-3 w-3" :class="checkingSingleUpdate?.[container.id] ? 'animate-spin' : ''" />
                  <span>{{ checkingSingleUpdate?.[container.id] ? 'Checking…' : 'Check for Update' }}</span>
                </button>
                <button
                  v-if="container.update_available && !container.project_id"
                  @click="emit('confirm-update', container)"
                  :disabled="!!updatingContainer?.[container.id]"
                  class="cyber-btn flex items-center gap-1.5 text-xs text-amber-200 border-amber-500 bg-amber-50/70 hover:bg-amber-100"
                >
                  <ArrowUpCircle class="h-3.5 w-3.5 text-amber-600" :class="updatingContainer?.[container.id] ? 'animate-spin' : ''" />
                  <span>{{ updatingContainer?.[container.id] ? 'Updating…' : 'Update to Latest' }}</span>
                </button>
                <button
                  v-if="container.update_available && container.project_id"
                  @click="emit('deploy-stack', container.project_id, container.project_name || container.name)"
                  :disabled="!!stackActionLoading?.[container.project_id]"
                  class="cyber-btn flex items-center gap-1.5 text-xs text-violet-700 border-violet-500 bg-violet-50/70 hover:bg-violet-100"
                  title="Deploy / pull latest Git updates and recreate stack"
                >
                  <Rocket class="h-3.5 w-3.5 text-violet-600" :class="stackActionLoading?.[container.project_id] ? 'animate-spin' : ''" />
                  <span>{{ stackActionLoading?.[container.project_id] ? 'Deploying…' : 'Deploy / Pull Stack' }}</span>
                </button>
              </div>
            </div>

            <div class="grid grid-cols-1 md:grid-cols-2 gap-3 text-xs font-mono">
              <div>
                <span class="text-slate-500">Image Tag:</span>
                <span class="ml-2 text-cyan-700 font-bold">{{ container.image }}</span>
              </div>
              <div>
                <span class="text-slate-500">Upstream Status:</span>
                <span v-if="container.update_available" class="ml-2 inline-flex items-center gap-1 text-amber-600 font-bold animate-pulse">
                  <ArrowUpCircle class="h-3.5 w-3.5" />
                  {{ container.update_error?.toLowerCase().includes('git') ? 'Git Update Available' : 'Update Available on Registry' }}
                </span>
                <span v-else-if="container.update_error && (container.update_error.includes('built locally') || container.update_error.includes('Local') || container.update_error.includes('Git'))" class="ml-2 inline-flex items-center gap-1 text-cyan-700 font-semibold">
                  <Info class="h-3.5 w-3.5" />
                  Local / Git Managed
                </span>
                <span v-else-if="container.update_checked_at && !container.update_available" class="ml-2 inline-flex items-center gap-1 text-emerald-600 font-bold">
                  <CheckCircle2 class="h-3.5 w-3.5" />
                  Up to Date
                </span>
                <span v-else class="ml-2 text-slate-500">
                  Not checked yet
                </span>
              </div>
              <div v-if="container.current_digest" class="truncate">
                <span class="text-slate-500">{{ container.update_error?.toLowerCase().includes('git') ? 'Current Commit:' : 'Current Digest:' }}</span>
                <span class="ml-2 text-slate-700 truncate" :title="container.current_digest">{{ container.current_digest }}</span>
              </div>
              <div v-if="container.latest_digest" class="truncate">
                <span class="text-slate-500">{{ container.update_error?.toLowerCase().includes('git') ? 'Latest Git Commit:' : 'Latest Remote Digest:' }}</span>
                <span class="ml-2 text-amber-700 truncate" :title="container.latest_digest">{{ container.latest_digest }}</span>
              </div>
              <div v-if="container.update_checked_at" class="md:col-span-2 text-slate-500 text-[11px]">
                Last checked: {{ container.update_checked_at }}
              </div>
              <div v-if="container.update_error" class="md:col-span-2 text-[11px]" :class="(container.update_error.includes('built locally') || container.update_error.includes('Local')) ? 'text-cyan-700/90' : 'text-amber-600/90'">
                Notice: {{ container.update_error }}
              </div>
            </div>
          </div>

          <!-- Specifications Table -->
          <div class="rounded-lg border border-slate-200 bg-slate-50 p-4 space-y-3">
            <h3 class="text-xs font-bold uppercase tracking-wider text-slate-600 flex items-center gap-2">
              <Info class="h-4 w-4 text-cyan-600" /> Container Specifications
            </h3>
            <div class="grid grid-cols-1 md:grid-cols-2 gap-3 text-xs font-mono">
              <div>
                <span class="text-slate-500">Status / Uptime:</span>
                <span class="ml-2 text-slate-800">{{ container.uptime || container.status }}</span>
              </div>
              <div>
                <span class="text-slate-500">Restart Count:</span>
                <span class="ml-2 text-slate-800">{{ container.restart_count || 0 }}</span>
              </div>
              <div class="flex items-center gap-1.5">
                <span class="text-slate-500">IP Address:</span>
                <span class="ml-1 text-slate-800">{{ container.ip_address || 'Host / Dynamic' }}</span>
                <button
                  v-if="container.ip_address"
                  @click="copyToClipboard('ip', container.ip_address)"
                  class="text-slate-500 hover:text-slate-700 p-0.5 inline-flex items-center"
                  title="Copy IP Address"
                >
                  <Check v-if="copiedKey === 'ip'" class="h-3 w-3 text-emerald-600" />
                  <Copy v-else class="h-3 w-3" />
                </button>
              </div>
              <div class="flex items-center gap-1.5">
                <span class="text-slate-500">Gateway:</span>
                <span class="ml-1 text-slate-800">{{ container.gateway || '—' }}</span>
                <button
                  v-if="container.gateway && container.gateway !== '—'"
                  @click="copyToClipboard('gateway', container.gateway)"
                  class="text-slate-500 hover:text-slate-700 p-0.5 inline-flex items-center"
                  title="Copy Gateway"
                >
                  <Check v-if="copiedKey === 'gateway'" class="h-3 w-3 text-emerald-600" />
                  <Copy v-else class="h-3 w-3" />
                </button>
              </div>
              <div class="md:col-span-2 space-y-2">
                <span class="text-slate-500">Docker network attachments:</span>
                <div v-for="network in container.networks || []" :key="network" class="flex items-center justify-between gap-3 rounded border border-slate-200 bg-white p-2">
                  <code class="break-all">{{ network }}</code>
                  <span v-if="container.exposure?.tailscale_node === network" class="text-xs text-slate-500">Managed in Access</span>
                  <button
                    v-else-if="network !== 'host' && network !== 'none'"
                    class="cyber-btn-secondary text-xs"
                    :disabled="disconnectingNetwork?.[container.id]"
                    @click="emit('disconnect-network', container, network)"
                  >
                    Disconnect
                  </button>
                </div>
                <p class="text-xs text-slate-500">Disconnect a network only when this container no longer needs it. Active Tailscale app networks are managed in Access.</p>
              </div>
              <div class="md:col-span-2 flex items-start gap-1.5">
                <span class="text-slate-500 shrink-0">Command:</span>
                <span class="ml-1 text-slate-700 break-all">{{ container.command || '—' }}</span>
                <button
                  v-if="container.command && container.command !== '—'"
                  @click="copyToClipboard('cmd', container.command)"
                  class="text-slate-500 hover:text-slate-700 p-0.5 inline-flex items-center shrink-0"
                  title="Copy Command"
                >
                  <Check v-if="copiedKey === 'cmd'" class="h-3 w-3 text-emerald-600" />
                  <Copy v-else class="h-3 w-3" />
                </button>
              </div>
              <div class="md:col-span-2">
                <span class="text-slate-500">Published Ports:</span>
                <span class="ml-2 text-cyan-700">{{ container.ports || 'None (Internal only)' }}</span>
              </div>
            </div>
          </div>

          <!-- Live Resource Limits Tuning Card -->
          <div class="rounded-lg border border-cyan-800/40 bg-slate-50 p-4 space-y-4 shadow-xs">
            <div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-2 border-b border-slate-200/80 pb-3">
              <div>
                <h3 class="text-xs font-bold uppercase tracking-wider text-slate-700 flex items-center gap-2">
                  <Sliders class="h-4 w-4 text-cyan-600" /> Live Resource Limits & Restart Policy
                </h3>
                <p class="text-[11px] text-slate-500 mt-0.5">
                  Dynamically adjust container resources using <code class="text-cyan-700 font-bold">docker update</code>. Applied live without downtime or restart.
                </p>
              </div>
              <div class="flex items-center gap-2">
                <span class="text-[11px] px-2 py-0.5 rounded-full font-mono bg-cyan-100/70 text-cyan-800 border border-cyan-300">
                  Live Tuning
                </span>
              </div>
            </div>

            <!-- Notice / Feedback Toast -->
            <div
              v-if="resourcesMsg"
              class="p-2.5 rounded-lg text-xs font-mono flex items-center justify-between"
              :class="resourcesMsg.type === 'success' ? 'bg-emerald-50 text-emerald-800 border border-emerald-300' : 'bg-rose-50 text-rose-800 border border-rose-300'"
            >
              <div class="flex items-center gap-2">
                <CheckCircle2 v-if="resourcesMsg.type === 'success'" class="h-4 w-4 text-emerald-600 shrink-0" />
                <AlertCircle v-else class="h-4 w-4 text-rose-600 shrink-0" />
                <span>{{ resourcesMsg.text }}</span>
              </div>
              <button @click="resourcesMsg = null" class="text-slate-500 hover:text-slate-700">
                <X class="h-3.5 w-3.5" />
              </button>
            </div>

            <div class="grid grid-cols-1 md:grid-cols-2 gap-4 text-xs">
              <!-- CPU Limits -->
              <div class="space-y-2 p-3 rounded-lg bg-white border border-slate-200">
                <div class="flex items-center justify-between">
                  <label class="font-bold text-slate-800 flex items-center gap-1.5">
                    <Cpu class="h-3.5 w-3.5 text-cyan-600" /> CPU Allocation
                  </label>
                  <span class="font-mono text-[11px] text-cyan-700 font-semibold">
                    {{ resourceDraft.cpus > 0 ? `${resourceDraft.cpus} Cores` : 'Unlimited' }}
                  </span>
                </div>
                <!-- Presets -->
                <div class="flex flex-wrap items-center gap-1.5">
                  <button
                    type="button"
                    @click="resourceDraft.cpus = 0"
                    class="px-2 py-1 rounded text-[11px] font-mono border transition cursor-pointer"
                    :class="resourceDraft.cpus === 0 ? 'bg-cyan-600 text-white border-cyan-600' : 'bg-slate-50 text-slate-700 border-slate-200 hover:border-cyan-400'"
                  >
                    Unlimited
                  </button>
                  <button
                    type="button"
                    @click="resourceDraft.cpus = 0.5"
                    class="px-2 py-1 rounded text-[11px] font-mono border transition cursor-pointer"
                    :class="resourceDraft.cpus === 0.5 ? 'bg-cyan-600 text-white border-cyan-600' : 'bg-slate-50 text-slate-700 border-slate-200 hover:border-cyan-400'"
                  >
                    0.5 CPU
                  </button>
                  <button
                    type="button"
                    @click="resourceDraft.cpus = 1"
                    class="px-2 py-1 rounded text-[11px] font-mono border transition cursor-pointer"
                    :class="resourceDraft.cpus === 1 ? 'bg-cyan-600 text-white border-cyan-600' : 'bg-slate-50 text-slate-700 border-slate-200 hover:border-cyan-400'"
                  >
                    1.0 CPU
                  </button>
                  <button
                    type="button"
                    @click="resourceDraft.cpus = 2"
                    class="px-2 py-1 rounded text-[11px] font-mono border transition cursor-pointer"
                    :class="resourceDraft.cpus === 2 ? 'bg-cyan-600 text-white border-cyan-600' : 'bg-slate-50 text-slate-700 border-slate-200 hover:border-cyan-400'"
                  >
                    2.0 CPUs
                  </button>
                  <button
                    type="button"
                    @click="resourceDraft.cpus = 4"
                    class="px-2 py-1 rounded text-[11px] font-mono border transition cursor-pointer"
                    :class="resourceDraft.cpus === 4 ? 'bg-cyan-600 text-white border-cyan-600' : 'bg-slate-50 text-slate-700 border-slate-200 hover:border-cyan-400'"
                  >
                    4.0 CPUs
                  </button>
                </div>
                <!-- Custom input -->
                <div class="flex items-center gap-2 pt-1">
                  <span class="text-[11px] text-slate-500">Custom cores:</span>
                  <input
                    type="number"
                    v-model.number="resourceDraft.cpus"
                    step="0.25"
                    min="0"
                    max="128"
                    placeholder="0 for unlimited"
                    class="w-24 px-2 py-1 rounded border border-slate-200 text-xs font-mono bg-white text-slate-800 focus:border-cyan-500 focus:outline-none"
                  />
                </div>
              </div>

              <!-- Memory Limits -->
              <div class="space-y-2 p-3 rounded-lg bg-white border border-slate-200">
                <div class="flex items-center justify-between">
                  <label class="font-bold text-slate-800 flex items-center gap-1.5">
                    <Layers class="h-3.5 w-3.5 text-emerald-600" /> Memory Limit (RAM)
                  </label>
                  <span class="font-mono text-[11px] text-emerald-700 font-semibold">
                    {{ resourceDraft.memory_mode === 'unlimited' ? 'Unlimited' : (resourceDraft.memory_mode === 'custom' ? `${resourceDraft.memory_custom_mb} MB` : resourceDraft.memory_mode.toUpperCase()) }}
                  </span>
                </div>
                <!-- Presets -->
                <div class="flex flex-wrap items-center gap-1.5">
                  <button
                    type="button"
                    @click="resourceDraft.memory_mode = 'unlimited'"
                    class="px-2 py-1 rounded text-[11px] font-mono border transition cursor-pointer"
                    :class="resourceDraft.memory_mode === 'unlimited' ? 'bg-emerald-600 text-white border-emerald-600' : 'bg-slate-50 text-slate-700 border-slate-200 hover:border-emerald-400'"
                  >
                    Unlimited
                  </button>
                  <button
                    type="button"
                    @click="resourceDraft.memory_mode = '256m'"
                    class="px-2 py-1 rounded text-[11px] font-mono border transition cursor-pointer"
                    :class="resourceDraft.memory_mode === '256m' ? 'bg-emerald-600 text-white border-emerald-600' : 'bg-slate-50 text-slate-700 border-slate-200 hover:border-emerald-400'"
                  >
                    256M
                  </button>
                  <button
                    type="button"
                    @click="resourceDraft.memory_mode = '512m'"
                    class="px-2 py-1 rounded text-[11px] font-mono border transition cursor-pointer"
                    :class="resourceDraft.memory_mode === '512m' ? 'bg-emerald-600 text-white border-emerald-600' : 'bg-slate-50 text-slate-700 border-slate-200 hover:border-emerald-400'"
                  >
                    512M
                  </button>
                  <button
                    type="button"
                    @click="resourceDraft.memory_mode = '1g'"
                    class="px-2 py-1 rounded text-[11px] font-mono border transition cursor-pointer"
                    :class="resourceDraft.memory_mode === '1g' ? 'bg-emerald-600 text-white border-emerald-600' : 'bg-slate-50 text-slate-700 border-slate-200 hover:border-emerald-400'"
                  >
                    1GB
                  </button>
                  <button
                    type="button"
                    @click="resourceDraft.memory_mode = '2g'"
                    class="px-2 py-1 rounded text-[11px] font-mono border transition cursor-pointer"
                    :class="resourceDraft.memory_mode === '2g' ? 'bg-emerald-600 text-white border-emerald-600' : 'bg-slate-50 text-slate-700 border-slate-200 hover:border-emerald-400'"
                  >
                    2GB
                  </button>
                  <button
                    type="button"
                    @click="resourceDraft.memory_mode = 'custom'"
                    class="px-2 py-1 rounded text-[11px] font-mono border transition cursor-pointer"
                    :class="resourceDraft.memory_mode === 'custom' ? 'bg-emerald-600 text-white border-emerald-600' : 'bg-slate-50 text-slate-700 border-slate-200 hover:border-emerald-400'"
                  >
                    Custom
                  </button>
                </div>
                <!-- Custom input -->
                <div v-if="resourceDraft.memory_mode === 'custom'" class="flex items-center gap-2 pt-1">
                  <span class="text-[11px] text-slate-500">Custom RAM (MB):</span>
                  <input
                    type="number"
                    v-model.number="resourceDraft.memory_custom_mb"
                    step="64"
                    min="16"
                    max="131072"
                    placeholder="e.g. 768"
                    class="w-24 px-2 py-1 rounded border border-slate-200 text-xs font-mono bg-white text-slate-800 focus:border-emerald-500 focus:outline-none"
                  />
                  <span class="text-[11px] text-slate-500">MB</span>
                </div>
              </div>

              <!-- Restart Policy -->
              <div class="space-y-2 p-3 rounded-lg bg-white border border-slate-200 md:col-span-2">
                <div class="flex items-center justify-between">
                  <label class="font-bold text-slate-800 flex items-center gap-1.5">
                    <RotateCcw class="h-3.5 w-3.5 text-violet-600" /> Container Restart Policy
                  </label>
                  <span class="font-mono text-[11px] text-violet-700 font-semibold">
                    Current: {{ container.resources?.restart_policy || container.restart_policy || 'unless-stopped' }}
                  </span>
                </div>
                <div class="grid grid-cols-2 sm:grid-cols-4 gap-2">
                  <label
                    class="flex items-center gap-2 p-2 rounded border cursor-pointer transition text-xs"
                    :class="resourceDraft.restart_policy === 'unless-stopped' ? 'bg-violet-50/70 border-violet-500 text-violet-900 font-semibold' : 'bg-slate-50 border-slate-200 hover:bg-slate-100 text-slate-700'"
                  >
                    <input type="radio" value="unless-stopped" v-model="resourceDraft.restart_policy" class="text-violet-600" />
                    <span>unless-stopped</span>
                  </label>
                  <label
                    class="flex items-center gap-2 p-2 rounded border cursor-pointer transition text-xs"
                    :class="resourceDraft.restart_policy === 'always' ? 'bg-violet-50/70 border-violet-500 text-violet-900 font-semibold' : 'bg-slate-50 border-slate-200 hover:bg-slate-100 text-slate-700'"
                  >
                    <input type="radio" value="always" v-model="resourceDraft.restart_policy" class="text-violet-600" />
                    <span>always</span>
                  </label>
                  <label
                    class="flex items-center gap-2 p-2 rounded border cursor-pointer transition text-xs"
                    :class="resourceDraft.restart_policy === 'on-failure' ? 'bg-violet-50/70 border-violet-500 text-violet-900 font-semibold' : 'bg-slate-50 border-slate-200 hover:bg-slate-100 text-slate-700'"
                  >
                    <input type="radio" value="on-failure" v-model="resourceDraft.restart_policy" class="text-violet-600" />
                    <span>on-failure</span>
                  </label>
                  <label
                    class="flex items-center gap-2 p-2 rounded border cursor-pointer transition text-xs"
                    :class="resourceDraft.restart_policy === 'no' ? 'bg-violet-50/70 border-violet-500 text-violet-900 font-semibold' : 'bg-slate-50 border-slate-200 hover:bg-slate-100 text-slate-700'"
                  >
                    <input type="radio" value="no" v-model="resourceDraft.restart_policy" class="text-violet-600" />
                    <span>no (manual)</span>
                  </label>
                </div>
              </div>
            </div>

            <!-- Footer Action Bar -->
            <div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-2 pt-2 border-t border-slate-200">
              <div class="text-[11px] text-slate-500 font-mono">
                Current:
                <span class="text-slate-700 font-semibold">
                  {{ container.resources?.cpus ? `${container.resources.cpus} CPUs` : 'Unlimited CPU' }} •
                  {{ container.resources?.memory_human || 'Unlimited RAM' }} •
                  {{ container.resources?.restart_policy || container.restart_policy || 'unless-stopped' }}
                </span>
              </div>
              <button
                type="button"
                @click="applyResourceLimits"
                :disabled="resourcesSaving"
                class="cyber-btn flex items-center justify-center gap-1.5 text-xs text-white bg-cyan-600 hover:bg-cyan-500 font-semibold px-3 py-1.5 shadow-sm cursor-pointer"
              >
                <RotateCcw v-if="resourcesSaving" class="h-3.5 w-3.5 animate-spin" />
                <Save v-else class="h-3.5 w-3.5" />
                <span>{{ resourcesSaving ? 'Updating Resources…' : 'Apply Resource Limits' }}</span>
              </button>
            </div>
          </div>

          <!-- Volumes & Mounts -->
          <div class="rounded-lg border border-slate-200 bg-slate-50 p-4 space-y-3">
            <h3 class="text-xs font-bold uppercase tracking-wider text-slate-600 flex items-center gap-2">
              <HardDrive class="h-4 w-4 text-amber-600" /> Volumes & Mounts
            </h3>
            <div v-if="container.mounts && container.mounts.length > 0" class="space-y-2">
              <div
                v-for="(m, i) in container.mounts"
                :key="i"
                class="flex flex-col sm:flex-row sm:items-center justify-between rounded border border-slate-200/80 bg-white/60 p-2.5 text-xs font-mono gap-1"
              >
                <div class="flex items-center gap-2 truncate">
                  <span class="text-cyan-600 truncate" :title="m.source">{{ m.source }}</span>
                  <span class="text-slate-500">➔</span>
                  <span class="text-emerald-600 truncate" :title="m.destination">{{ m.destination }}</span>
                </div>
                <span class="text-[10px] rounded px-1.5 py-0.5 bg-slate-100 text-slate-600 self-start sm:self-auto">
                  {{ m.mode || (m.rw ? 'Read/Write' : 'Read-Only') }}
                </span>
              </div>
            </div>
            <p v-else class="text-xs text-slate-500">No persistent volume mounts mapped to this container.</p>
          </div>

          <!-- Environment Variables Table -->
          <div class="rounded-lg border border-slate-200 bg-slate-50 p-4 space-y-3">
            <div class="flex items-center justify-between">
              <h3 class="text-xs font-bold uppercase tracking-wider text-slate-600 flex items-center gap-2">
                <Sliders class="h-4 w-4 text-violet-400" /> Environment Variables ({{ container.env?.length || 0 }})
              </h3>
            </div>
            <div v-if="container.env && container.env.length > 0" class="max-h-56 overflow-y-auto space-y-1 font-mono text-xs">
              <div
                v-for="(e, i) in container.env"
                :key="i"
                class="rounded bg-white/80 px-2 py-1 text-slate-700 break-all hover:bg-slate-100/60"
              >
                {{ e }}
              </div>
            </div>
            <p v-else class="text-xs text-slate-500">No custom environment variables found.</p>
          </div>
        </div>

        <!-- ============================================== -->
        <!-- TAB 2: ACCESS & NETWORKS                       -->
        <!-- ============================================== -->
        <div v-else-if="activeTab === 'access'" class="space-y-5">
          <!-- 1. PRIMARY LOCAL ACCESS (mDNS .local) -->
          <div class="cyber-card space-y-3.5 border-indigo-200 bg-gradient-to-br from-indigo-50/60 to-white">
            <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-2 border-b border-indigo-100 pb-3">
              <div class="flex items-center gap-2">
                <Wifi class="h-4 w-4 text-indigo-600" />
                <div>
                  <h3 class="text-sm font-bold text-slate-900">Local LAN Access (mDNS)</h3>
                  <p class="text-xs text-slate-500">
                    Local hostnames are advertised when DockTun's local proxy is running. Web services use the portless gateway; other protocols require a published host port and a compatible client.
                  </p>
                </div>
              </div>
              <span class="rounded bg-indigo-100 px-2 py-0.5 text-[10px] font-mono font-semibold text-indigo-700">
                Automatic .local
              </span>
            </div>

            <!-- Local URL Box -->
            <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 rounded-lg border border-indigo-100 bg-white p-3.5 shadow-xs">
              <div class="space-y-0.5">
                <div class="text-[10px] uppercase tracking-wider font-semibold text-indigo-600">Local LAN Address</div>
                <div class="font-mono text-sm font-bold text-slate-900">
                  {{ localAddress(container) }}
                </div>
              </div>

              <div class="flex items-center gap-2">
                <a
                  v-if="hasLocalWebLink(container.ports)"
                  :href="localAddress(container)"
                  target="_blank"
                  rel="noopener"
                  class="cyber-btn flex items-center gap-1.5 text-xs text-white bg-indigo-600 hover:bg-indigo-700"
                >
                  <span>Open</span>
                  <ExternalLink class="h-3 w-3" />
                </a>
                <button
                  @click="copyToClipboard('mdns', localAddress(container))"
                  class="cyber-btn-secondary text-xs flex items-center gap-1.5"
                  title="Copy local mDNS address"
                >
                  <Check v-if="copiedKey === 'mdns'" class="h-3 w-3 text-emerald-600" />
                  <Copy v-else class="h-3 w-3" />
                  <span>{{ copiedKey === 'mdns' ? 'Copied' : 'Copy' }}</span>
                </button>
              </div>
            </div>

            <!-- Internal Network & Ports Info -->
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-2 pt-1 text-xs text-slate-600">
              <div class="rounded border border-slate-200 bg-slate-50/80 p-2">
                <span class="text-slate-500 text-[11px] block">Published host ports:</span>
                <span class="font-mono font-semibold text-slate-800">{{ container.ports || 'None' }}</span>
              </div>
              <div class="rounded border border-slate-200 bg-slate-50/80 p-2">
                <span class="text-slate-500 text-[11px] block">Docker networks:</span>
                <span class="font-mono font-semibold text-slate-800 truncate block">{{ container.networks?.join(', ') || 'bridge' }}</span>
              </div>
            </div>
          </div>

          <!-- 2. REMOTE EXPOSURE (Cloudflare, Tailscale, WireGuard) -->
          <ContainerExposure
            :container="container"
            :settings="getExpose ? getExpose(container.id) : undefined"
            :saving="exposeSaving?.[container.id]"
            :message="exposeMsg?.[container.id]"
            @save="emit('save-exposure', container)"
          />
        </div>

        <!-- ============================================== -->
        <!-- TAB 3: COMPOSE (docker-compose.yml)            -->
        <!-- ============================================== -->
        <div v-else-if="activeTab === 'compose'" class="space-y-4">
          <!-- If Container is Linked to a Project -->
          <div v-if="container.project_id" class="space-y-4">
            <div class="flex flex-col gap-3 border-b border-slate-200 pb-3">
              <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-2">
                <div>
                  <h3 class="text-sm font-bold text-slate-900 flex items-center gap-2">
                    <FileCode class="h-4 w-4 text-cyan-600" />
                    <span>Stack Compose: {{ container.project_name || container.name }}</span>
                  </h3>
                  <p class="text-xs text-slate-600 font-mono">
                    {{ container.project_path }}/{{ composeFilename || 'docker-compose.yml' }}
                  </p>
                </div>

                <!-- Stack Action Buttons -->
                <div class="flex flex-wrap items-center gap-1.5">
                  <span v-if="composeMsg" class="text-xs font-medium mr-1" :class="composeEditor?.error ? 'text-red-600' : 'text-emerald-600'">
                    {{ composeMsg }}
                  </span>

                  <button
                    @click="emit('run-stack-action', container.project_id!, 'up')"
                    :disabled="!!stackActionLoading?.[container.project_id!] || !projectFileAccess?.available"
                    class="cyber-btn-secondary text-xs flex items-center gap-1 text-emerald-600 hover:border-emerald-500/60 hover:bg-emerald-50/30"
                    title="docker compose up -d"
                  >
                    <Play class="h-3 w-3 fill-current" />
                    <span>Stack Up</span>
                  </button>

                  <button
                    @click="emit('run-stack-action', container.project_id!, 'down')"
                    :disabled="!!stackActionLoading?.[container.project_id!] || !projectFileAccess?.available"
                    class="cyber-btn-secondary text-xs flex items-center gap-1 text-amber-600 hover:border-amber-500/60 hover:bg-amber-50/30"
                    title="docker compose down"
                  >
                    <Square class="h-3 w-3 fill-current" />
                    <span>Stack Down</span>
                  </button>

                  <button
                    @click="emit('run-stack-action', container.project_id!, 'restart')"
                    :disabled="!!stackActionLoading?.[container.project_id!] || !projectFileAccess?.available"
                    class="cyber-btn-secondary text-xs flex items-center gap-1 text-cyan-700 hover:border-cyan-500/60 hover:bg-cyan-50/30"
                    title="docker compose restart"
                  >
                    <RotateCcw class="h-3 w-3" :class="stackActionLoading?.[container.project_id!] ? 'animate-spin' : ''" />
                    <span>Restart</span>
                  </button>

                  <button
                    @click="emit('deploy-stack', container.project_id!, container.project_name || container.name)"
                    :disabled="!!stackActionLoading?.[container.project_id!] || !projectFilesWritable"
                    class="cyber-btn-secondary text-xs flex items-center gap-1 text-violet-300 hover:border-violet-500/60 hover:bg-violet-950/30"
                    title="Git pull & docker compose up -d --build"
                  >
                    <Rocket class="h-3 w-3 text-violet-400" />
                    <span>Deploy / Pull</span>
                  </button>

                  <button
                    @click="emit('stack-history', container.project_id!, container.project_name || container.name)"
                    class="cyber-btn-secondary text-xs flex items-center gap-1 text-slate-700"
                    title="View deployment & stack action history"
                  >
                    <History class="h-3 w-3 text-slate-600" />
                    <span>History</span>
                  </button>

                  <button
                    v-if="container.project_name !== 'docktun'"
                    @click="emit('delete-stack', container.project_id!, container.project_name || container.name, container.project_path)"
                    :disabled="!!stackActionLoading?.[container.project_id!]"
                    class="cyber-btn-secondary text-xs flex items-center gap-1 text-rose-600 hover:border-rose-400 hover:bg-rose-50/50"
                    title="Delete this stack"
                  >
                    <Trash2 class="h-3 w-3" />
                    <span>Delete Stack</span>
                  </button>

                  <button
                    @click="copyToClipboard('compose-yaml', composeContent)"
                    :disabled="!composeContent"
                    class="cyber-btn-secondary text-xs flex items-center gap-1"
                    title="Copy Compose YAML to clipboard"
                  >
                    <Check v-if="copiedKey === 'compose-yaml'" class="h-3 w-3 text-emerald-600" />
                    <Copy v-else class="h-3 w-3" />
                    <span>{{ copiedKey === 'compose-yaml' ? 'Copied' : 'Copy YAML' }}</span>
                  </button>

                  <button
                    @click="emit('save-compose', false)"
                    :disabled="composeSaving || composeLoading || !composeEditor?.loaded || !projectFilesWritable"
                    class="cyber-btn text-xs flex items-center gap-1 text-cyan-700 border-cyan-600 bg-cyan-50/40 hover:bg-cyan-100/50"
                    title="Save edits to docker-compose.yml"
                  >
                    <Save class="h-3 w-3" />
                    <span>Save YAML</span>
                  </button>

                  <button
                    @click="emit('save-compose', true)"
                    :disabled="composeSaving || composeLoading || !composeEditor?.loaded || !projectFilesWritable"
                    class="cyber-btn text-xs flex items-center gap-1 text-emerald-700 border-emerald-600 bg-emerald-50/40 hover:bg-emerald-100/50"
                    title="Save edits and recreate stack containers"
                  >
                    <RotateCcw class="h-3 w-3" :class="composeSaving ? 'animate-spin' : ''" />
                    <span>Save & Recreate Stack</span>
                  </button>
                </div>
              </div>

              <!-- Collapsible Git Continuous Deployment Configuration -->
              <div class="rounded-lg border border-slate-200 bg-slate-50/70 p-3 text-xs space-y-2">
                <div class="flex items-center justify-between cursor-pointer" @click="showGitSettings = !showGitSettings">
                  <div class="flex items-center gap-2">
                    <GitBranch class="h-3.5 w-3.5 text-violet-400" />
                    <span class="font-semibold text-slate-800">Git Repository & Auto-Deploy</span>
                    <span v-if="currentProject?.repo_url" class="rounded bg-violet-950/80 border border-violet-800/60 px-1.5 py-0.2 font-mono text-[10px] text-violet-300 truncate max-w-xs">
                      {{ currentProject.repo_url }}
                    </span>
                  </div>
                  <span class="text-[11px] text-slate-500 font-mono">{{ showGitSettings ? 'Hide ▲' : 'Configure Git ▼' }}</span>
                </div>

                <div v-if="showGitSettings && currentProject" class="pt-2 border-t border-slate-200 space-y-2.5">
                  <div class="grid gap-2.5 sm:grid-cols-3">
                    <div class="sm:col-span-3">
                      <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-600 mb-1">Git Repository URL</label>
                      <input v-model="currentProject.repo_url" class="cyber-input text-xs font-mono" placeholder="https://github.com/owner/repository.git" />
                    </div>
                    <div>
                      <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-600 mb-1">Git Username (Optional)</label>
                      <input v-model="currentProject.git_username" class="cyber-input text-xs font-mono" placeholder="oauth2 or username" />
                    </div>
                    <div>
                      <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-600 mb-1">Git Token / PAT (Masked)</label>
                      <input v-model="currentProject.git_token" type="password" class="cyber-input text-xs font-mono" placeholder="ghp_••••••••••••" />
                    </div>
                    <div class="flex items-end">
                      <button
                        @click="emit('save-project-git')"
                        :disabled="projectGitSaving"
                        class="cyber-btn w-full text-xs flex items-center justify-center gap-1.5"
                      >
                        <Save class="h-3.5 w-3.5" />
                        <span>{{ projectGitSaving ? 'Saving…' : 'Save Git Settings' }}</span>
                      </button>
                    </div>
                  </div>
                </div>
              </div>
            </div>

            <!-- Compose Code Editor -->
            <div class="relative">
              <textarea
                :value="composeContent"
                @input="emit('update:composeContent', ($event.target as HTMLTextAreaElement).value)"
                :readonly="!projectFilesWritable"
                :disabled="!composeEditor?.loaded || composeSaving"
                rows="18"
                spellcheck="false"
                :placeholder="composeLoading ? 'Loading Compose file…' : composeEditor?.loaded ? 'Enter Compose YAML' : 'Compose file unavailable. See the error above.'"
                class="w-full rounded-lg border border-slate-200 bg-slate-50 p-4 font-mono text-xs text-slate-800 focus:border-cyan-500 focus:outline-none leading-relaxed"
              ></textarea>
            </div>
          </div>

          <!-- If Container is Standalone (Offer Scaffolding) -->
          <div v-else class="rounded-xl border border-slate-200 bg-slate-50 p-8 text-center space-y-4">
            <Code2 class="mx-auto h-10 w-10 text-cyan-600" />
            <div class="max-w-md mx-auto space-y-1">
              <h3 class="text-base font-bold text-slate-900">Standalone Container</h3>
              <p class="text-xs text-slate-600">
                This container was created standalone (e.g. via <code class="text-cyan-700">docker run</code>)
                and is not currently tracked by a Compose project.
              </p>
              <p class="text-xs text-slate-500">
                DockTun can automatically generate a clean <code class="text-slate-700">docker-compose.yml</code>
                mirroring this container's image, volume mounts, environment variables, and ports, and link it as a project.
              </p>
            </div>

            <div class="pt-2">
              <button
                @click="emit('scaffold-project')"
                :disabled="projectScaffolding"
                class="cyber-btn flex items-center gap-2 mx-auto text-xs text-cyan-700 border-cyan-500 bg-cyan-50/60 hover:bg-cyan-100/60 px-5 py-2.5"
              >
                <RotateCcw v-if="projectScaffolding" class="h-4 w-4 animate-spin" />
                <Plus v-else class="h-4 w-4" />
                <span>{{ projectScaffolding ? 'Scaffolding Project…' : 'Generate Compose & Link Project' }}</span>
              </button>
            </div>
          </div>
        </div>

        <!-- ============================================== -->
        <!-- TAB 4: ENVIRONMENT (.env)                      -->
        <!-- ============================================== -->
        <InspectorEnvTab
          v-else-if="activeTab === 'env'"
          :container="container"
          :project-files-writable="Boolean(projectFilesWritable)"
          :project-scaffolding="projectScaffolding"
          @scaffold="emit('scaffold-project')"
          @restarted="emit('refresh-containers')"
        />

        <!-- ============================================== -->
        <!-- TAB 5: FILES & STORAGE                         -->
        <!-- ============================================== -->
        <InspectorFilesTab
          v-else-if="activeTab === 'files'"
          :container="container"
          :project-files-writable="Boolean(projectFilesWritable)"
          :project-scaffolding="projectScaffolding"
          @scaffold="emit('scaffold-project')"
        />

        <!-- ============================================== -->
        <!-- TAB 6: REALTIME LOGS                           -->
        <!-- ============================================== -->
        <InspectorLogsTab
          v-else-if="activeTab === 'logs'"
          :container-id="container.id"
          :container-name="container.name"
          :container-image="container.image"
        />

        <!-- ============================================== -->
        <!-- TAB 7: TERMINAL (CONTAINER EXEC)               -->
        <!-- ============================================== -->
        <InspectorTerminalTab
          v-else-if="activeTab === 'exec'"
          :container-id="container.id"
          :container-name="container.name"
        />

        <!-- ============================================== -->
        <!-- TAB 8: INSPECT (JSON)                          -->
        <!-- ============================================== -->
        <InspectorInspectTab
          v-else-if="activeTab === 'inspect'"
          :container="container"
          :container-id="container.id"
        />
      </div>
    </div>
  </div>
</template>
