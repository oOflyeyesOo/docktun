<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  Server,
  Play,
  Square,
  RotateCcw,
  Trash2,
  ScrollText,
  Shield,
  ShieldAlert,
  ShieldCheck,
  Globe,
  Lock,
  ExternalLink,
  Cpu,
  Layers,
  ArrowUpDown,
  Search,
  CheckCircle2,
  AlertCircle,
  X,
  Info,
  Clock,
  HardDrive,
  Folder,
  FolderOpen,
  FileCode,
  FileText,
  Save,
  Plus,
  RefreshCw,
  Copy,
  Check,
  Network,
  Wifi,
  Terminal,
  Sliders,
  Link2,
  Radio,
  Activity,
  CornerDownRight,
  Code2
} from 'lucide-vue-next'
import { useApi } from '../composables/useApi'

const { apiFetch } = useApi()

interface ExposureSettings {
  mode: string
  hostname?: string
  target?: string
  private_type?: string
  private_host?: string
  private_port?: number
  notes?: string
}

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
  block_io?: string
  pids?: number
  restart_count?: number
  uptime?: string
  exposure?: ExposureSettings
  project_id?: number
  project_name?: string
  project_path?: string
  compose_file?: string
  connected_to_cloudflared?: boolean
}

interface ContainerDetail extends ContainerItem {
  env?: string[]
  mounts?: Array<{ source: string; destination: string; mode: string; rw: boolean }>
  ip_address?: string
  gateway?: string
  mac_address?: string
  networks?: string[]
  exit_code?: number
}

interface FileItem {
  name: string
  path: string
  is_dir: boolean
  size: number
  mod_time: string
}

const containers = ref<ContainerItem[]>([])
const loading = ref(false)
const searchQuery = ref('')
const filter = ref<'all' | 'running' | 'stopped' | 'local' | 'public' | 'private'>('all')

const actionLoading = ref<Record<string, string>>({})
const exposeDrawerId = ref<string | null>(null)
const exposeForm = ref<Record<string, ExposureSettings>>({})
const exposeSaving = ref<Record<string, boolean>>({})

function getExpose(id?: string): ExposureSettings {
  if (!id) {
    return {
      mode: 'internal',
      hostname: '',
      target: '',
      private_type: 'tailscale',
      private_host: '',
      private_port: 80,
      notes: '',
    }
  }
  if (!exposeForm.value[id]) {
    exposeForm.value[id] = {
      mode: 'internal',
      hostname: '',
      target: '',
      private_type: 'tailscale',
      private_host: '',
      private_port: 80,
      notes: '',
    }
  }
  return exposeForm.value[id]
}

// ==========================================
// Container Detail & Control Center State
// ==========================================
const selectedContainer = ref<ContainerDetail | null>(null)
const selectedTab = ref<'overview' | 'access' | 'compose' | 'env' | 'files' | 'logs'>('overview')
const detailLoading = ref(false)
const networkActionLoading = ref(false)
const projectScaffolding = ref(false)

// Compose state
const composeFilename = ref('docker-compose.yml')
const composeContent = ref('')
const composeLoading = ref(false)
const composeSaving = ref(false)
const composeMsg = ref('')

// .env state
const envContent = ref('')
const envLoading = ref(false)
const envSaving = ref(false)
const envMsg = ref('')

// Files state
const currentDir = ref('')
const fileList = ref<FileItem[]>([])
const filesLoading = ref(false)
const editingFile = ref<{ path: string; content: string } | null>(null)
const editingFileSaving = ref(false)
const editingFileMsg = ref('')
const showNewItemModal = ref(false)
const newItemName = ref('')
const newItemIsDir = ref(false)

// Logs state in Control Center
const modalLogsText = ref('')
const modalLogsLoading = ref(false)
const modalLogsTail = ref(200)
const modalLogsWrap = ref(true)
const modalLogsAutoRefresh = ref(false)
const modalLogsTimestamps = ref(false)
const modalLogsFilter = ref('')
let modalLogsTimer: any = null

const filteredModalLogs = computed(() => {
  if (!modalLogsText.value) return ''
  if (!modalLogsFilter.value.trim()) return modalLogsText.value
  const query = modalLogsFilter.value.toLowerCase()
  return modalLogsText.value
    .split('\n')
    .filter((line) => line.toLowerCase().includes(query))
    .join('\n')
})

const matchingLinesCount = computed(() => {
  if (!modalLogsText.value || !modalLogsFilter.value.trim()) return null
  const total = modalLogsText.value.split('\n').length
  const matched = filteredModalLogs.value ? filteredModalLogs.value.split('\n').length : 0
  return { matched, total }
})

const disconnectingNetwork = ref<Record<string, boolean>>({})

// Copied feedback helper
const copiedKey = ref<string | null>(null)
function copyToClipboard(key: string, text: string) {
  navigator.clipboard.writeText(text)
  copiedKey.value = key
  setTimeout(() => {
    if (copiedKey.value === key) copiedKey.value = null
  }, 2000)
}

const filteredContainers = computed(() => {
  return containers.value.filter((c) => {
    if (filter.value === 'running' && !c.running) return false
    if (filter.value === 'stopped' && c.running) return false
    if (filter.value === 'local' && (!c.running || c.exposure_mode === 'public' || c.exposure_mode === 'private')) return false
    if (filter.value === 'public' && c.exposure_mode !== 'public') return false
    if (filter.value === 'private' && c.exposure_mode !== 'private') return false

    if (!searchQuery.value) return true
    const q = searchQuery.value.toLowerCase()
    return (
      c.name.toLowerCase().includes(q) ||
      c.image.toLowerCase().includes(q) ||
      (c.hostname && c.hostname.toLowerCase().includes(q)) ||
      (c.ports && c.ports.toLowerCase().includes(q)) ||
      (c.project_name && c.project_name.toLowerCase().includes(q))
    )
  })
})

const summary = computed(() => {
  const total = containers.value.length
  const running = containers.value.filter((c) => c.running).length
  const publicCount = containers.value.filter((c) => c.exposure_mode === 'public').length
  const privateCount = containers.value.filter((c) => c.exposure_mode === 'private').length
  const localCount = containers.value.filter((c) => c.running && c.exposure_mode !== 'public' && c.exposure_mode !== 'private').length

  let totalCpu = 0
  containers.value.forEach((c) => {
    if (c.running && c.cpu) {
      const parsed = parseFloat(c.cpu.replace('%', ''))
      if (!isNaN(parsed)) totalCpu += parsed
    }
  })

  return {
    total,
    running,
    stopped: total - running,
    localCount,
    publicCount,
    privateCount,
    cpuUsage: totalCpu.toFixed(1) + '%',
  }
})

function parseCpuPercent(cpuStr?: string): number {
  if (!cpuStr) return 0
  const n = parseFloat(cpuStr.replace('%', ''))
  return isNaN(n) ? 0 : Math.min(n, 100)
}

function parseMemoryUsage(memStr?: string): { used: string; total: string; percent: number } {
  if (!memStr) return { used: '—', total: '—', percent: 0 }
  const parts = memStr.split('/')
  if (parts.length >= 2) {
    const used = parts[0].trim()
    const total = parts[1].trim()
    return { used, total, percent: 0 }
  }
  return { used: memStr, total: '', percent: 0 }
}

async function fetchContainers() {
  try {
    const data = await apiFetch<ContainerItem[]>('/api/docker/containers')
    containers.value = data || []
    containers.value.forEach((c) => {
      if (!exposeForm.value[c.id]) {
        exposeForm.value[c.id] = {
          mode: c.exposure_mode || c.exposure?.mode || 'internal',
          hostname: c.hostname || c.exposure?.hostname || '',
          target: c.target || c.exposure?.target || `http://${c.name}:80`,
          private_type: c.private_type || c.exposure?.private_type || 'tailscale',
          private_host: c.private_host || c.exposure?.private_host || '',
          private_port: c.private_port || c.exposure?.private_port || 80,
          notes: c.notes || c.exposure?.notes || '',
        }
      }
    })

    // If modal is open, sync basic stats into selectedContainer
    if (selectedContainer.value) {
      const updated = containers.value.find((c) => c.id === selectedContainer.value?.id)
      if (updated) {
        selectedContainer.value.cpu = updated.cpu
        selectedContainer.value.memory = updated.memory
        selectedContainer.value.network = updated.network
        selectedContainer.value.block_io = updated.block_io
        selectedContainer.value.pids = updated.pids
        selectedContainer.value.running = updated.running
        selectedContainer.value.status = updated.status
        selectedContainer.value.connected_to_cloudflared = updated.connected_to_cloudflared
        if (updated.project_id && !selectedContainer.value.project_id) {
          selectedContainer.value.project_id = updated.project_id
          selectedContainer.value.project_name = updated.project_name
          selectedContainer.value.project_path = updated.project_path
        }
      }
    }
  } catch (error) {
    console.error('Failed to load containers:', error)
  }
}

async function runAction(container: ContainerItem, action: 'start' | 'stop' | 'restart') {
  actionLoading.value[container.id] = action
  try {
    await apiFetch(`/api/docker/containers/${container.id}/${action}`, { method: 'POST' })
    await fetchContainers()
  } catch (error: any) {
    alert(error.message || `Failed to ${action} container`)
  } finally {
    delete actionLoading.value[container.id]
  }
}

async function removeContainer(container: ContainerItem) {
  if (!confirm(`Are you sure you want to permanently delete container "${container.name}"?`)) return
  actionLoading.value[container.id] = 'remove'
  try {
    await apiFetch(`/api/docker/containers/${container.id}/remove`, {
      method: 'POST',
      body: JSON.stringify({ force: true }),
    })
    if (selectedContainer.value?.id === container.id) {
      closeContainerModal()
    }
    await fetchContainers()
  } catch (error: any) {
    alert(error.message || 'Failed to remove container')
  } finally {
    delete actionLoading.value[container.id]
  }
}

function toggleExposeDrawer(container: ContainerItem) {
  if (exposeDrawerId.value === container.id) {
    exposeDrawerId.value = null
  } else {
    exposeDrawerId.value = container.id
    if (!exposeForm.value[container.id]) {
      exposeForm.value[container.id] = {
        mode: container.exposure_mode || 'internal',
        hostname: container.hostname || '',
        target: container.target || `http://${container.name}:80`,
        private_type: container.private_type || 'tailscale',
        private_host: container.private_host || '',
        private_port: container.private_port || 80,
        notes: container.notes || '',
      }
    }
  }
}

async function saveExposure(container: ContainerItem) {
  exposeSaving.value[container.id] = true
  try {
    const payload = getExpose(container.id)
    await apiFetch(`/api/docker/containers/${container.id}/exposure`, {
      method: 'POST',
      body: JSON.stringify(payload),
    })
    await fetchContainers()
    exposeDrawerId.value = null
  } catch (error: any) {
    alert(error.message || 'Failed to save exposure settings')
  } finally {
    exposeSaving.value[container.id] = false
  }
}

// ==========================================
// Control Center Modal Logic
// ==========================================
async function openContainerModal(c: ContainerItem, tab: 'overview' | 'access' | 'compose' | 'env' | 'files' | 'logs' = 'overview') {
  selectedContainer.value = { ...c } as ContainerDetail
  selectedTab.value = tab
  detailLoading.value = true

  if (!exposeForm.value[c.id]) {
    exposeForm.value[c.id] = {
      mode: c.exposure_mode || 'internal',
      hostname: c.hostname || '',
      target: c.target || `http://${c.name}:80`,
      private_type: c.private_type || 'tailscale',
      private_host: c.private_host || '',
      private_port: c.private_port || 80,
      notes: c.notes || '',
    }
  }

  try {
    const detail = await apiFetch<ContainerDetail>(`/api/docker/containers/${c.id}`)
    selectedContainer.value = detail
  } catch (err: any) {
    console.error('Failed to load container details:', err)
  } finally {
    detailLoading.value = false
  }

  // Load content for active tab
  handleTabChange(tab)
}

function closeContainerModal() {
  selectedContainer.value = null
  editingFile.value = null
  composeMsg.value = ''
  envMsg.value = ''
  editingFileMsg.value = ''
  if (modalLogsTimer) {
    clearInterval(modalLogsTimer)
    modalLogsTimer = null
  }
}

function handleTabChange(tab: 'overview' | 'access' | 'compose' | 'env' | 'files' | 'logs') {
  selectedTab.value = tab
  if (!selectedContainer.value) return

  if (tab === 'compose') {
    if (selectedContainer.value.project_id) {
      loadContainerCompose(selectedContainer.value.project_id)
    }
  } else if (tab === 'env') {
    if (selectedContainer.value.project_id) {
      loadContainerEnv(selectedContainer.value.project_id)
    }
  } else if (tab === 'files') {
    if (selectedContainer.value.project_id) {
      loadContainerFiles(selectedContainer.value.project_id, '')
    }
  } else if (tab === 'logs') {
    fetchModalLogs()
  }
}

async function toggleCloudflaredNetwork() {
  if (!selectedContainer.value) return
  networkActionLoading.value = true
  const isConnected = !!selectedContainer.value.connected_to_cloudflared
  const endpoint = isConnected ? 'disconnect' : 'connect'
  try {
    await apiFetch(`/api/docker/containers/${selectedContainer.value.id}/network/${endpoint}`, {
      method: 'POST',
      body: JSON.stringify({ network: 'cloudflared' }),
    })
    selectedContainer.value.connected_to_cloudflared = !isConnected
    await fetchContainers()
  } catch (e: any) {
    alert(`Network action failed: ${e.message}`)
  } finally {
    networkActionLoading.value = false
  }
}

async function scaffoldProject() {
  if (!selectedContainer.value) return
  projectScaffolding.value = true
  try {
    const res: any = await apiFetch(`/api/docker/containers/${selectedContainer.value.id}/project`, {
      method: 'POST',
    })
    if (res && res.project_id) {
      selectedContainer.value.project_id = res.project_id
      await fetchContainers()
      if (selectedTab.value === 'compose') {
        await loadContainerCompose(res.project_id)
      } else if (selectedTab.value === 'env') {
        await loadContainerEnv(res.project_id)
      } else if (selectedTab.value === 'files') {
        await loadContainerFiles(res.project_id, '')
      }
    }
  } catch (e: any) {
    alert(`Failed to create project: ${e.message}`)
  } finally {
    projectScaffolding.value = false
  }
}

// --- Compose Functions ---
async function loadContainerCompose(projectId: number) {
  composeLoading.value = true
  composeMsg.value = ''
  try {
    const res: any = await apiFetch(`/api/projects/${projectId}/compose`)
    composeFilename.value = res.filename || 'docker-compose.yml'
    composeContent.value = res.content || ''
  } catch (e: any) {
    composeMsg.value = 'Failed to load compose: ' + e.message
  } finally {
    composeLoading.value = false
  }
}

async function saveContainerCompose(restart = false) {
  if (!selectedContainer.value?.project_id) return
  composeSaving.value = true
  composeMsg.value = ''
  try {
    await apiFetch(`/api/projects/${selectedContainer.value.project_id}/compose`, {
      method: 'PUT',
      body: JSON.stringify({
        filename: composeFilename.value,
        content: composeContent.value,
      }),
    })
    composeMsg.value = 'Compose saved successfully!'
    if (restart) {
      await apiFetch(`/api/projects/${selectedContainer.value.project_id}/action/restart`, { method: 'POST' })
      composeMsg.value = 'Compose saved and stack restarting...'
      await fetchContainers()
    }
    setTimeout(() => (composeMsg.value = ''), 3500)
  } catch (e: any) {
    composeMsg.value = 'Save failed: ' + e.message
  } finally {
    composeSaving.value = false
  }
}

// --- .env Functions ---
async function loadContainerEnv(projectId: number) {
  envLoading.value = true
  envMsg.value = ''
  try {
    const res: any = await apiFetch(`/api/projects/${projectId}/env`)
    envContent.value = res.content || ''
  } catch (e: any) {
    envMsg.value = 'Failed to load .env: ' + e.message
  } finally {
    envLoading.value = false
  }
}

async function saveContainerEnv(restart = false) {
  if (!selectedContainer.value?.project_id) return
  envSaving.value = true
  envMsg.value = ''
  try {
    await apiFetch(`/api/projects/${selectedContainer.value.project_id}/env`, {
      method: 'PUT',
      body: JSON.stringify({ content: envContent.value }),
    })
    envMsg.value = '.env saved successfully!'
    if (restart) {
      await apiFetch(`/api/projects/${selectedContainer.value.project_id}/action/restart`, { method: 'POST' })
      envMsg.value = '.env saved and stack restarting...'
      await fetchContainers()
    }
    setTimeout(() => (envMsg.value = ''), 3500)
  } catch (e: any) {
    envMsg.value = 'Save failed: ' + e.message
  } finally {
    envSaving.value = false
  }
}

// --- Files Functions ---
async function loadContainerFiles(projectId: number, path = '') {
  filesLoading.value = true
  currentDir.value = path
  editingFile.value = null
  try {
    const res: any = await apiFetch(`/api/projects/${projectId}/files?path=${encodeURIComponent(path)}`)
    fileList.value = res || []
  } catch {
    fileList.value = []
  } finally {
    filesLoading.value = false
  }
}

async function openContainerFile(f: FileItem) {
  if (!selectedContainer.value?.project_id) return
  if (f.is_dir) {
    loadContainerFiles(selectedContainer.value.project_id, f.path)
    return
  }
  filesLoading.value = true
  try {
    const res: any = await apiFetch(`/api/projects/${selectedContainer.value.project_id}/file?path=${encodeURIComponent(f.path)}`)
    editingFile.value = { path: f.path, content: res.content || '' }
  } catch (e: any) {
    alert('Failed to read file: ' + e.message)
  } finally {
    filesLoading.value = false
  }
}

async function saveCurrentContainerFile() {
  if (!selectedContainer.value?.project_id || !editingFile.value) return
  editingFileSaving.value = true
  editingFileMsg.value = ''
  try {
    await apiFetch(`/api/projects/${selectedContainer.value.project_id}/file`, {
      method: 'PUT',
      body: JSON.stringify({
        path: editingFile.value.path,
        content: editingFile.value.content,
      }),
    })
    editingFileMsg.value = 'File saved successfully!'
    setTimeout(() => (editingFileMsg.value = ''), 3000)
  } catch (e: any) {
    editingFileMsg.value = 'Save failed: ' + e.message
  } finally {
    editingFileSaving.value = false
  }
}

async function createContainerFileItem() {
  if (!selectedContainer.value?.project_id || !newItemName.value) return
  const targetPath = currentDir.value ? `${currentDir.value}/${newItemName.value}` : newItemName.value
  try {
    await apiFetch(`/api/projects/${selectedContainer.value.project_id}/file`, {
      method: 'POST',
      body: JSON.stringify({
        path: targetPath,
        is_dir: newItemIsDir.value,
        content: '',
      }),
    })
    showNewItemModal.value = false
    newItemName.value = ''
    loadContainerFiles(selectedContainer.value.project_id, currentDir.value)
  } catch (e: any) {
    alert('Create failed: ' + e.message)
  }
}

async function deleteContainerFile(path: string) {
  if (!selectedContainer.value?.project_id) return
  if (!confirm(`Delete ${path}?`)) return
  try {
    await apiFetch(`/api/projects/${selectedContainer.value.project_id}/file?path=${encodeURIComponent(path)}`, {
      method: 'DELETE',
    })
    loadContainerFiles(selectedContainer.value.project_id, currentDir.value)
  } catch (e: any) {
    alert('Delete failed: ' + e.message)
  }
}

// --- Logs Functions ---
async function fetchModalLogs() {
  if (!selectedContainer.value) return
  modalLogsLoading.value = true
  try {
    const res = await apiFetch<{ logs: string }>(
      `/api/docker/containers/${selectedContainer.value.id}/logs?tail=${modalLogsTail.value}&timestamps=${modalLogsTimestamps.value}`
    )
    modalLogsText.value = res.logs || 'No logs recorded.'
  } catch (error: any) {
    modalLogsText.value = 'Failed to load logs: ' + error.message
  } finally {
    modalLogsLoading.value = false
  }
}

async function disconnectFromCloudflared(container: any) {
  if (!container) return
  disconnectingNetwork.value[container.id] = true
  try {
    await apiFetch(`/api/docker/containers/${container.id}/network/disconnect`, {
      method: 'POST',
      body: JSON.stringify({ network: 'cloudflared' }),
    })
    container.connected_to_cloudflared = false
    if (selectedContainer.value && selectedContainer.value.id === container.id) {
      selectedContainer.value.connected_to_cloudflared = false
    }
    await fetchContainers()
  } catch (e: any) {
    alert('Failed to disconnect from Cloudflare network: ' + e.message)
  } finally {
    disconnectingNetwork.value[container.id] = false
  }
}

watch(modalLogsAutoRefresh, (enabled) => {
  if (modalLogsTimer) {
    clearInterval(modalLogsTimer)
    modalLogsTimer = null
  }
  if (enabled) {
    modalLogsTimer = setInterval(() => {
      if (selectedTab.value === 'logs' && selectedContainer.value) {
        fetchModalLogs()
      }
    }, 3000)
  }
})

// Extract first published port if any
function getPrimaryPort(portsStr?: string): string {
  if (!portsStr) return '80'
  const match = portsStr.match(/:(\d+)->/)
  if (match) return match[1]
  const plainMatch = portsStr.match(/(\d+)\/tcp/)
  if (plainMatch) return plainMatch[1]
  return '80'
}

let timer: any = null

onMounted(async () => {
  loading.value = true
  await fetchContainers()
  loading.value = false

  // Live polling every 4 seconds
  timer = setInterval(() => {
    if (document.visibilityState === 'visible') {
      fetchContainers()
    }
  }, 4000)
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
  if (modalLogsTimer) clearInterval(modalLogsTimer)
})
</script>

<template>
  <div class="space-y-6">
    <!-- Header -->
    <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <div class="flex items-center gap-2 text-[10px] font-semibold uppercase tracking-[0.22em] text-slate-400">
          <Layers class="h-3.5 w-3.5 text-cyan-400" />
          <span>Container Orchestration</span>
        </div>
        <h1 class="text-2xl font-bold tracking-tight text-slate-100">Containers</h1>
        <p class="text-xs text-slate-400">
          Manage standalone containers, docker-compose stacks, ingress routing, and network connectivity.
        </p>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <button
          @click="fetchContainers"
          :disabled="loading"
          class="cyber-btn flex items-center gap-1.5 text-xs text-slate-300"
          title="Refresh container status"
        >
          <RotateCcw class="h-3.5 w-3.5" :class="loading ? 'animate-spin' : ''" />
          <span>Refresh</span>
        </button>
      </div>
    </div>

    <!-- Quick Stats Cards -->
    <div class="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
      <div class="cyber-card flex flex-col justify-between p-3.5">
        <div class="flex items-center justify-between text-xs text-slate-400">
          <span>Total Containers</span>
          <Server class="h-4 w-4 text-slate-500" />
        </div>
        <div class="mt-2 text-2xl font-bold text-slate-100 font-mono">{{ summary.total }}</div>
      </div>

      <div class="cyber-card flex flex-col justify-between p-3.5">
        <div class="flex items-center justify-between text-xs text-emerald-400">
          <span>Running</span>
          <Play class="h-4 w-4 text-emerald-500 fill-emerald-500" />
        </div>
        <div class="mt-2 text-2xl font-bold text-emerald-400 font-mono">{{ summary.running }}</div>
      </div>

      <div class="cyber-card flex flex-col justify-between p-3.5">
        <div class="flex items-center justify-between text-xs text-slate-400">
          <span>Stopped</span>
          <Square class="h-4 w-4 text-slate-500 fill-slate-500" />
        </div>
        <div class="mt-2 text-2xl font-bold text-slate-400 font-mono">{{ summary.stopped }}</div>
      </div>

      <div class="cyber-card flex flex-col justify-between p-3.5">
        <div class="flex items-center justify-between text-xs text-violet-400">
          <span>Local (mDNS)</span>
          <Wifi class="h-4 w-4 text-violet-400" />
        </div>
        <div class="mt-2 text-2xl font-bold text-violet-300 font-mono">{{ summary.localCount }}</div>
      </div>

      <div class="cyber-card flex flex-col justify-between p-3.5">
        <div class="flex items-center justify-between text-xs text-cyan-400">
          <span>Public Ingress</span>
          <Globe class="h-4 w-4 text-cyan-400" />
        </div>
        <div class="mt-2 text-2xl font-bold text-cyan-400 font-mono">{{ summary.publicCount }}</div>
      </div>

      <div class="cyber-card flex flex-col justify-between p-3.5">
        <div class="flex items-center justify-between text-xs text-amber-400">
          <span>Host CPU Load</span>
          <Cpu class="h-4 w-4 text-amber-400" />
        </div>
        <div class="mt-2 text-2xl font-bold text-amber-400 font-mono">{{ summary.cpuUsage }}</div>
      </div>
    </div>

    <!-- Filter & Search Bar -->
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <!-- Filter tabs -->
      <div class="flex flex-wrap gap-1 rounded-lg border border-slate-800 bg-slate-900/60 p-1">
        <button
          @click="filter = 'all'"
          class="rounded-md px-3 py-1.5 text-xs font-semibold uppercase tracking-wider transition-colors"
          :class="filter === 'all' ? 'bg-slate-800 text-cyan-400 shadow-sm' : 'text-slate-400 hover:text-slate-200'"
        >
          All ({{ containers.length }})
        </button>
        <button
          @click="filter = 'running'"
          class="rounded-md px-3 py-1.5 text-xs font-semibold uppercase tracking-wider transition-colors"
          :class="filter === 'running' ? 'bg-slate-800 text-emerald-400 shadow-sm' : 'text-slate-400 hover:text-slate-200'"
        >
          Running ({{ summary.running }})
        </button>
        <button
          @click="filter = 'local'"
          class="rounded-md px-3 py-1.5 text-xs font-semibold uppercase tracking-wider transition-colors"
          :class="filter === 'local' ? 'bg-slate-800 text-violet-300 shadow-sm' : 'text-slate-400 hover:text-slate-200'"
        >
          Local mDNS ({{ summary.localCount }})
        </button>
        <button
          @click="filter = 'public'"
          class="rounded-md px-3 py-1.5 text-xs font-semibold uppercase tracking-wider transition-colors"
          :class="filter === 'public' ? 'bg-slate-800 text-cyan-300 shadow-sm' : 'text-slate-400 hover:text-slate-200'"
        >
          Public ({{ summary.publicCount }})
        </button>
        <button
          @click="filter = 'stopped'"
          class="rounded-md px-3 py-1.5 text-xs font-semibold uppercase tracking-wider transition-colors"
          :class="filter === 'stopped' ? 'bg-slate-800 text-slate-300 shadow-sm' : 'text-slate-400 hover:text-slate-200'"
        >
          Stopped ({{ summary.stopped }})
        </button>
      </div>

      <!-- Search box -->
      <div class="relative w-full sm:w-64">
        <Search class="absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500" />
        <input
          v-model="searchQuery"
          type="text"
          placeholder="Search name, image, port, stack..."
          class="cyber-input pl-9 text-xs"
        />
      </div>
    </div>

    <!-- Empty state -->
    <div v-if="filteredContainers.length === 0 && !loading" class="cyber-card py-12 text-center text-slate-400">
      <Server class="mx-auto h-8 w-8 text-slate-600 mb-2" />
      <p class="text-sm font-medium text-slate-300">No containers match the current filter.</p>
      <p class="mt-1 text-xs text-slate-500">Try adjusting your search terms or status filters.</p>
    </div>

    <!-- Containers List -->
    <div class="space-y-4">
      <div
        v-for="container in filteredContainers"
        :key="container.id"
        class="cyber-card transition-all duration-200 hover:border-slate-700"
      >
        <div class="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
          <!-- Left: Identity & Status (Clickable to open Control Center) -->
          <div
            class="space-y-2 cursor-pointer group"
            @click="openContainerModal(container, 'overview')"
            title="Click to open Container Control Center"
          >
            <div class="flex flex-wrap items-center gap-2.5">
              <!-- Pulsing status dot -->
              <span
                class="relative flex h-2.5 w-2.5"
                :title="container.running ? 'Running' : 'Stopped'"
              >
                <span
                  v-if="container.running"
                  class="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75"
                ></span>
                <span
                  class="relative inline-flex h-2.5 w-2.5 rounded-full"
                  :class="container.running ? 'bg-emerald-500' : 'bg-slate-600'"
                ></span>
              </span>

              <span class="text-base font-bold text-slate-100 font-mono group-hover:text-cyan-400 transition-colors flex items-center gap-1.5">
                {{ container.name }}
                <Sliders class="h-3.5 w-3.5 text-slate-500 opacity-0 group-hover:opacity-100 transition-opacity" />
              </span>

              <!-- Status Badge -->
              <span
                class="rounded border px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wider"
                :class="container.running ? 'bg-emerald-500/10 text-emerald-400 border-emerald-500/30' : 'bg-slate-800 text-slate-400 border-slate-700'"
              >
                {{ container.running ? 'Running' : 'Stopped' }}
              </span>

              <!-- Stack / Project Badge -->
              <span
                v-if="container.project_name"
                class="inline-flex items-center gap-1 rounded border border-cyan-500/30 bg-cyan-500/10 px-2 py-0.5 text-[10px] font-semibold text-cyan-300 font-mono"
                title="Part of compose project"
              >
                <Layers class="h-3 w-3" />
                <span>Stack: {{ container.project_name }}</span>
              </span>

              <!-- Exposure / Local Badge -->
              <span
                v-if="container.exposure_mode === 'public'"
                class="inline-flex items-center gap-1.5 rounded-full border border-cyan-500/40 bg-cyan-500/10 px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-cyan-300"
                title="Public Cloudflare Tunnel Ingress Active"
              >
                <Globe class="h-3 w-3" />
                <span>Public Ingress</span>
              </span>
              <span
                v-else-if="container.exposure_mode === 'private'"
                class="inline-flex items-center gap-1.5 rounded-full border border-violet-500/40 bg-violet-500/10 px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-violet-300"
              >
                <Shield class="h-3 w-3" />
                <span>Private Mesh ({{ container.private_type || 'tailscale' }})</span>
              </span>
              <span
                v-else-if="container.running"
                class="inline-flex items-center gap-1.5 rounded-full border border-violet-500/30 bg-violet-500/10 px-2.5 py-0.5 text-[10px] font-semibold text-violet-300 font-mono"
                title="Local mDNS Active"
              >
                <Wifi class="h-3 w-3" />
                <span>{{ container.name }}.local</span>
              </span>
              <span
                v-else
                class="inline-flex items-center gap-1.5 rounded-full border border-slate-800 bg-slate-900/60 px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-slate-500"
              >
                <Lock class="h-3 w-3 text-slate-500" />
                <span>Internal</span>
              </span>
            </div>

            <!-- Image & Command -->
            <div class="flex flex-wrap items-center gap-3 text-xs text-slate-400">
              <span class="font-mono text-slate-300">{{ container.image }}</span>
              <span>•</span>
              <span class="flex items-center gap-1 text-slate-400">
                <Clock class="h-3 w-3 text-slate-500" /> {{ container.uptime || container.status }}
              </span>
              <span v-if="container.restart_count" class="text-amber-400/90">
                • Restarts: {{ container.restart_count }}
              </span>
            </div>

            <!-- Local Address (Default) and Public Ingress if Exposed -->
            <div class="flex flex-wrap items-center gap-3 pt-1 text-xs">
              <div class="flex items-center gap-1.5 font-mono">
                <span class="text-slate-500 font-sans text-[11px]">Local:</span>
                <a
                  :href="`http://${container.name}.local`"
                  target="_blank"
                  rel="noopener"
                  @click.stop
                  class="inline-flex items-center gap-1 text-violet-300 hover:text-violet-200 hover:underline font-semibold"
                  title="Open local .local address"
                >
                  <span>http://{{ container.name }}.local</span>
                  <ExternalLink class="h-3 w-3 text-violet-400/80" />
                </a>
                <button
                  @click.stop="copyToClipboard('mdns-' + container.id, `http://${container.name}.local`)"
                  class="text-slate-500 hover:text-slate-300 ml-0.5"
                  title="Copy local .local URL"
                >
                  <Check v-if="copiedKey === 'mdns-' + container.id" class="h-3 w-3 text-emerald-400" />
                  <Copy v-else class="h-3 w-3" />
                </button>
              </div>

              <div v-if="container.exposure_mode === 'public' && container.hostname" class="flex items-center gap-1.5 font-mono">
                <span class="text-slate-500 font-sans text-[11px]">Public:</span>
                <a
                  :href="`https://${container.hostname}`"
                  target="_blank"
                  rel="noopener"
                  @click.stop
                  class="inline-flex items-center gap-1 text-cyan-400 hover:text-cyan-300 hover:underline font-semibold"
                >
                  <span>https://{{ container.hostname }}</span>
                  <ExternalLink class="h-3 w-3" />
                </a>
              </div>
            </div>
          </div>

          <!-- Right: Control Actions -->
          <div class="flex flex-wrap items-center gap-2">
            <!-- Full Control Center Trigger Button -->
            <button
              @click="openContainerModal(container, 'overview')"
              class="cyber-btn flex items-center gap-1.5 text-xs text-cyan-300 border-cyan-600/50 bg-cyan-950/40 hover:bg-cyan-900/50"
              title="Open Container Control Center (Overview, Compose, .env, Files, Logs)"
            >
              <Sliders class="h-3.5 w-3.5" />
              <span>Control Center</span>
            </button>

            <button
              v-if="!container.running"
              @click="runAction(container, 'start')"
              :disabled="!!actionLoading[container.id]"
              class="cyber-btn flex items-center gap-1.5 text-xs text-emerald-300 hover:border-emerald-600 hover:bg-emerald-950/40"
            >
              <Play class="h-3.5 w-3.5 fill-current" />
              <span>{{ actionLoading[container.id] === 'start' ? 'Starting…' : 'Start' }}</span>
            </button>

            <button
              v-if="container.running"
              @click="runAction(container, 'stop')"
              :disabled="!!actionLoading[container.id]"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-amber-300 hover:border-amber-600 hover:bg-amber-950/40"
            >
              <Square class="h-3.5 w-3.5 fill-current" />
              <span>{{ actionLoading[container.id] === 'stop' ? 'Stopping…' : 'Stop' }}</span>
            </button>

            <button
              @click="runAction(container, 'restart')"
              :disabled="!!actionLoading[container.id]"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs"
            >
              <RotateCcw class="h-3.5 w-3.5" :class="actionLoading[container.id] === 'restart' ? 'animate-spin' : ''" />
              <span>Restart</span>
            </button>

            <button
              @click="toggleExposeDrawer(container)"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs"
              :class="exposeDrawerId === container.id ? 'border-cyan-500 text-cyan-300' : ''"
            >
              <Globe class="h-3.5 w-3.5 text-cyan-400" />
              <span>Expose</span>
            </button>

            <button
              @click="openContainerModal(container, 'logs')"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs"
            >
              <ScrollText class="h-3.5 w-3.5 text-slate-400" />
              <span>Logs</span>
            </button>

            <button
              @click="removeContainer(container)"
              :disabled="!!actionLoading[container.id]"
              class="cyber-btn-danger flex items-center gap-1.5 text-xs"
              title="Delete container"
            >
              <Trash2 class="h-3.5 w-3.5" />
            </button>
          </div>
        </div>

        <!-- Live Telemetry Row (Clickable) -->
        <div
          class="mt-4 grid grid-cols-2 gap-3 sm:grid-cols-4 rounded-lg border border-slate-800/80 bg-slate-950/60 p-3 cursor-pointer hover:border-slate-700 transition-colors"
          @click="openContainerModal(container, 'overview')"
        >
          <!-- CPU -->
          <div class="space-y-1">
            <div class="flex items-center justify-between text-[10px] font-semibold uppercase tracking-wider text-slate-400">
              <span class="flex items-center gap-1"><Cpu class="h-3 w-3 text-cyan-400" /> CPU</span>
              <span class="font-mono text-slate-200">{{ container.cpu || (container.running ? '0.0%' : '—') }}</span>
            </div>
            <div class="h-1.5 w-full overflow-hidden rounded-full bg-slate-800">
              <div
                class="h-full bg-cyan-400 transition-all duration-300"
                :style="{ width: `${parseCpuPercent(container.cpu)}%` }"
              ></div>
            </div>
          </div>

          <!-- Memory -->
          <div class="space-y-1">
            <div class="flex items-center justify-between text-[10px] font-semibold uppercase tracking-wider text-slate-400">
              <span class="flex items-center gap-1"><Layers class="h-3 w-3 text-emerald-400" /> RAM</span>
              <span class="font-mono text-slate-200 truncate max-w-[110px]" :title="container.memory">
                {{ parseMemoryUsage(container.memory).used }}
              </span>
            </div>
            <div class="text-[10px] text-slate-500 truncate">
              {{ parseMemoryUsage(container.memory).total ? `Limit: ${parseMemoryUsage(container.memory).total}` : (container.running ? 'Active' : '—') }}
            </div>
          </div>

          <!-- Network I/O -->
          <div class="space-y-1">
            <div class="flex items-center justify-between text-[10px] font-semibold uppercase tracking-wider text-slate-400">
              <span class="flex items-center gap-1"><ArrowUpDown class="h-3 w-3 text-violet-400" /> Net I/O</span>
            </div>
            <div class="font-mono text-xs text-slate-200 truncate" :title="container.network">
              {{ container.network || (container.running ? '0B / 0B' : '—') }}
            </div>
          </div>

          <!-- Ports / PIDs -->
          <div class="space-y-1">
            <div class="flex items-center justify-between text-[10px] font-semibold uppercase tracking-wider text-slate-400">
              <span class="flex items-center gap-1"><HardDrive class="h-3 w-3 text-amber-400" /> Ports</span>
              <span v-if="container.pids" class="text-[10px] text-slate-400 font-mono">{{ container.pids }} pids</span>
            </div>
            <div class="font-mono text-xs text-slate-200 truncate" :title="container.ports">
              {{ container.ports || '—' }}
            </div>
          </div>
        </div>

        <!-- Inline Expose / Exposure Config Drawer -->
        <div v-if="exposeDrawerId === container.id" class="mt-4 rounded-xl border border-cyan-500/30 bg-slate-950 p-4">
          <div class="flex items-center justify-between border-b border-slate-800 pb-3">
            <div class="flex items-center gap-2">
              <Globe class="h-4 w-4 text-cyan-400" />
              <h3 class="text-xs font-bold uppercase tracking-[0.18em] text-slate-200">
                Exposure & Ingress Configuration: {{ container.name }}
              </h3>
            </div>
            <button @click="exposeDrawerId = null" class="text-slate-400 hover:text-slate-200">
              <X class="h-4 w-4" />
            </button>
          </div>

          <div class="mt-4 grid gap-4 md:grid-cols-3">
            <div>
              <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-400">
                Access Mode
              </label>
              <select v-model="getExpose(container.id).mode" class="cyber-input">
                <option value="internal">Internal (No Ingress)</option>
                <option value="public">Public (Cloudflare Tunnel)</option>
                <option value="private">Private (Tailscale / WireGuard)</option>
              </select>
            </div>

            <!-- Public Mode Inputs -->
            <template v-if="getExpose(container.id).mode === 'public'">
              <div>
                <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-400">
                  Public Hostname (FQDN)
                </label>
                <input
                  v-model="getExpose(container.id).hostname"
                  type="text"
                  placeholder="app.yourdomain.com"
                  class="cyber-input"
                />
              </div>
              <div>
                <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-400">
                  Service Target URL
                </label>
                <input
                  v-model="getExpose(container.id).target"
                  type="text"
                  :placeholder="`http://${container.name}:80`"
                  class="cyber-input"
                />
              </div>
            </template>

            <!-- Private Mode Inputs -->
            <template v-if="getExpose(container.id).mode === 'private'">
              <div>
                <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-400">
                  Private Mesh Type
                </label>
                <select v-model="getExpose(container.id).private_type" class="cyber-input">
                  <option value="tailscale">Tailscale</option>
                  <option value="wireguard">WireGuard</option>
                  <option value="mesh">Custom Mesh</option>
                </select>
              </div>
              <div>
                <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-400">
                  Private Host / IP
                </label>
                <input
                  v-model="getExpose(container.id).private_host"
                  type="text"
                  placeholder="100.x.y.z or 10.0.0.x"
                  class="cyber-input"
                />
              </div>
              <div>
                <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-400">
                  Private Port
                </label>
                <input
                  v-model.number="getExpose(container.id).private_port"
                  type="number"
                  placeholder="8080"
                  class="cyber-input"
                />
              </div>
            </template>
          </div>

          <div class="mt-4 flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 pt-3 border-t border-slate-900">
            <p class="text-xs text-slate-400">
              <span v-if="getExpose(container.id).mode === 'public'" class="text-cyan-300">
                Saving public mode automatically connects the container to 'cloudflared' network and configures tunnel routing.
              </span>
              <span v-else-if="getExpose(container.id).mode === 'private'" class="text-violet-300">
                Routes traffic privately through your encrypted mesh network.
              </span>
              <span v-else class="text-slate-500">
                Removes public ingress rules for this container. Accessible inside Docker network.
              </span>
            </p>
            <div class="flex items-center gap-2">
              <button @click="exposeDrawerId = null" class="cyber-btn-secondary text-xs">
                Cancel
              </button>
              <button
                @click="saveExposure(container)"
                :disabled="exposeSaving[container.id]"
                class="cyber-btn flex items-center gap-1.5 text-xs text-cyan-300 border-cyan-600 bg-cyan-950/40 hover:bg-cyan-900/50"
              >
                <ShieldCheck class="h-3.5 w-3.5" />
                <span>{{ exposeSaving[container.id] ? 'Saving & Applying…' : 'Save & Apply Ingress' }}</span>
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- ======================================================== -->
    <!-- CONTAINER CONTROL CENTER (MODAL / FULL INSPECTOR)        -->
    <!-- ======================================================== -->
    <div
      v-if="selectedContainer"
      class="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/85 p-2 sm:p-4 backdrop-blur-md"
      @click.self="closeContainerModal"
    >
      <div class="flex max-h-[92vh] w-full max-w-5xl flex-col rounded-xl border border-slate-700 bg-slate-900 shadow-2xl overflow-hidden">
        <!-- Modal Top Bar -->
        <div class="flex flex-wrap items-center justify-between border-b border-slate-800 bg-slate-950 px-5 py-3 gap-3">
          <div class="flex items-center gap-3">
            <span
              class="relative flex h-3 w-3"
              :title="selectedContainer.running ? 'Running' : 'Stopped'"
            >
              <span
                v-if="selectedContainer.running"
                class="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75"
              ></span>
              <span
                class="relative inline-flex h-3 w-3 rounded-full"
                :class="selectedContainer.running ? 'bg-emerald-500' : 'bg-slate-600'"
              ></span>
            </span>

            <div>
              <div class="flex items-center gap-2">
                <h2 class="text-base font-bold text-slate-100 font-mono">{{ selectedContainer.name }}</h2>
                <span class="rounded bg-slate-800 px-1.5 py-0.5 text-[10px] font-mono text-slate-400">
                  {{ selectedContainer.id.slice(0, 12) }}
                </span>
                <button
                  @click="copyToClipboard('cid', selectedContainer.id)"
                  class="text-slate-500 hover:text-slate-300"
                  title="Copy full Container ID"
                >
                  <Check v-if="copiedKey === 'cid'" class="h-3 w-3 text-emerald-400" />
                  <Copy v-else class="h-3 w-3" />
                </button>
              </div>
              <p class="text-[11px] text-slate-400 font-mono truncate max-w-md">{{ selectedContainer.image }}</p>
            </div>
          </div>

          <!-- Top Quick Actions -->
          <div class="flex items-center gap-2">
            <button
              v-if="!selectedContainer.running"
              @click="runAction(selectedContainer, 'start')"
              :disabled="!!actionLoading[selectedContainer.id]"
              class="cyber-btn flex items-center gap-1.5 text-xs text-emerald-300"
            >
              <Play class="h-3 w-3 fill-current" />
              <span>Start</span>
            </button>
            <button
              v-if="selectedContainer.running"
              @click="runAction(selectedContainer, 'stop')"
              :disabled="!!actionLoading[selectedContainer.id]"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-amber-300"
            >
              <Square class="h-3 w-3 fill-current" />
              <span>Stop</span>
            </button>
            <button
              @click="runAction(selectedContainer, 'restart')"
              :disabled="!!actionLoading[selectedContainer.id]"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs"
            >
              <RotateCcw class="h-3 w-3" :class="actionLoading[selectedContainer.id] === 'restart' ? 'animate-spin' : ''" />
              <span>Restart</span>
            </button>
            <button
              @click="closeContainerModal"
              class="rounded-lg p-1 text-slate-400 hover:bg-slate-800 hover:text-slate-200"
            >
              <X class="h-5 w-5" />
            </button>
          </div>
        </div>

        <!-- Modal Navigation Tabs -->
        <div class="flex items-center gap-1 overflow-x-auto border-b border-slate-800 bg-slate-950/80 px-4 py-2 text-xs">
          <button
            @click="handleTabChange('overview')"
            class="flex items-center gap-2 rounded-lg px-3 py-1.5 font-medium transition-colors whitespace-nowrap"
            :class="selectedTab === 'overview' ? 'bg-cyan-950/60 text-cyan-300 border border-cyan-800/60' : 'text-slate-400 hover:text-slate-200'"
          >
            <Activity class="h-3.5 w-3.5" />
            <span>Overview</span>
          </button>

          <button
            @click="handleTabChange('access')"
            class="flex items-center gap-2 rounded-lg px-3 py-1.5 font-medium transition-colors whitespace-nowrap"
            :class="selectedTab === 'access' ? 'bg-cyan-950/60 text-cyan-300 border border-cyan-800/60' : 'text-slate-400 hover:text-slate-200'"
          >
            <Globe class="h-3.5 w-3.5" />
            <span>Access & Networks</span>
            <span
              v-if="selectedContainer.connected_to_cloudflared"
              class="h-1.5 w-1.5 rounded-full bg-emerald-400"
              title="Cloudflared network connected"
            ></span>
          </button>

          <button
            @click="handleTabChange('compose')"
            class="flex items-center gap-2 rounded-lg px-3 py-1.5 font-medium transition-colors whitespace-nowrap"
            :class="selectedTab === 'compose' ? 'bg-cyan-950/60 text-cyan-300 border border-cyan-800/60' : 'text-slate-400 hover:text-slate-200'"
          >
            <FileCode class="h-3.5 w-3.5" />
            <span>Compose</span>
            <span
              v-if="selectedContainer.project_id"
              class="rounded bg-cyan-900/60 px-1 py-0.2 text-[9px] text-cyan-300 font-mono"
            >
              stack
            </span>
          </button>

          <button
            @click="handleTabChange('env')"
            class="flex items-center gap-2 rounded-lg px-3 py-1.5 font-medium transition-colors whitespace-nowrap"
            :class="selectedTab === 'env' ? 'bg-cyan-950/60 text-cyan-300 border border-cyan-800/60' : 'text-slate-400 hover:text-slate-200'"
          >
            <Sliders class="h-3.5 w-3.5" />
            <span>Environment (.env)</span>
          </button>

          <button
            @click="handleTabChange('files')"
            class="flex items-center gap-2 rounded-lg px-3 py-1.5 font-medium transition-colors whitespace-nowrap"
            :class="selectedTab === 'files' ? 'bg-cyan-950/60 text-cyan-300 border border-cyan-800/60' : 'text-slate-400 hover:text-slate-200'"
          >
            <FolderOpen class="h-3.5 w-3.5" />
            <span>Files & Storage</span>
          </button>

          <button
            @click="handleTabChange('logs')"
            class="flex items-center gap-2 rounded-lg px-3 py-1.5 font-medium transition-colors whitespace-nowrap"
            :class="selectedTab === 'logs' ? 'bg-cyan-950/60 text-cyan-300 border border-cyan-800/60' : 'text-slate-400 hover:text-slate-200'"
          >
            <ScrollText class="h-3.5 w-3.5" />
            <span>Realtime Logs</span>
          </button>
        </div>

        <!-- Tab Body Container -->
        <div class="flex-1 overflow-y-auto p-5 text-slate-200">
          <!-- Loading indicator -->
          <div v-if="detailLoading" class="flex items-center justify-center py-16 text-slate-400 text-sm">
            <RotateCcw class="h-5 w-5 animate-spin mr-2 text-cyan-400" />
            <span>Loading container telemetry & metadata…</span>
          </div>

          <!-- ============================================== -->
          <!-- TAB 1: OVERVIEW & TELEMETRY                    -->
          <!-- ============================================== -->
          <div v-else-if="selectedTab === 'overview'" class="space-y-5">
            <!-- Telemetry Gauges -->
            <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <!-- CPU -->
              <div class="rounded-lg border border-slate-800 bg-slate-950 p-3">
                <div class="flex items-center justify-between text-xs text-slate-400 mb-1">
                  <span class="flex items-center gap-1.5 font-semibold uppercase tracking-wider text-[10px]">
                    <Cpu class="h-3.5 w-3.5 text-cyan-400" /> CPU Usage
                  </span>
                  <span class="font-mono text-cyan-400 font-bold">{{ selectedContainer.cpu || '0.0%' }}</span>
                </div>
                <div class="h-2 w-full overflow-hidden rounded-full bg-slate-800">
                  <div
                    class="h-full bg-cyan-400 transition-all duration-300"
                    :style="{ width: `${parseCpuPercent(selectedContainer.cpu)}%` }"
                  ></div>
                </div>
              </div>

              <!-- RAM -->
              <div class="rounded-lg border border-slate-800 bg-slate-950 p-3">
                <div class="flex items-center justify-between text-xs text-slate-400 mb-1">
                  <span class="flex items-center gap-1.5 font-semibold uppercase tracking-wider text-[10px]">
                    <Layers class="h-3.5 w-3.5 text-emerald-400" /> Memory
                  </span>
                  <span class="font-mono text-emerald-400 font-bold truncate max-w-[90px]">
                    {{ parseMemoryUsage(selectedContainer.memory).used }}
                  </span>
                </div>
                <div class="text-[10px] text-slate-500 font-mono truncate">
                  Limit: {{ parseMemoryUsage(selectedContainer.memory).total || 'Unlimited' }}
                </div>
              </div>

              <!-- Network I/O -->
              <div class="rounded-lg border border-slate-800 bg-slate-950 p-3">
                <div class="flex items-center justify-between text-xs text-slate-400 mb-1">
                  <span class="flex items-center gap-1.5 font-semibold uppercase tracking-wider text-[10px]">
                    <ArrowUpDown class="h-3.5 w-3.5 text-violet-400" /> Net Traffic
                  </span>
                </div>
                <div class="font-mono text-xs text-slate-200 truncate">
                  {{ selectedContainer.network || '0B / 0B' }}
                </div>
              </div>

              <!-- Disk / Block I/O -->
              <div class="rounded-lg border border-slate-800 bg-slate-950 p-3">
                <div class="flex items-center justify-between text-xs text-slate-400 mb-1">
                  <span class="flex items-center gap-1.5 font-semibold uppercase tracking-wider text-[10px]">
                    <HardDrive class="h-3.5 w-3.5 text-amber-400" /> Block I/O
                  </span>
                  <span class="text-[10px] text-slate-500 font-mono">{{ selectedContainer.pids || 0 }} pids</span>
                </div>
                <div class="font-mono text-xs text-slate-200 truncate">
                  {{ selectedContainer.block_io || '0B / 0B' }}
                </div>
              </div>
            </div>

            <!-- Specifications Table -->
            <div class="rounded-lg border border-slate-800 bg-slate-950 p-4 space-y-3">
              <h3 class="text-xs font-bold uppercase tracking-wider text-slate-400 flex items-center gap-2">
                <Info class="h-4 w-4 text-cyan-400" /> Container Specifications
              </h3>
              <div class="grid grid-cols-1 md:grid-cols-2 gap-3 text-xs font-mono">
                <div>
                  <span class="text-slate-500">Status / Uptime:</span>
                  <span class="ml-2 text-slate-200">{{ selectedContainer.uptime || selectedContainer.status }}</span>
                </div>
                <div>
                  <span class="text-slate-500">Restart Count:</span>
                  <span class="ml-2 text-slate-200">{{ selectedContainer.restart_count || 0 }}</span>
                </div>
                <div>
                  <span class="text-slate-500">IP Address:</span>
                  <span class="ml-2 text-slate-200">{{ selectedContainer.ip_address || 'Host / Dynamic' }}</span>
                </div>
                <div>
                  <span class="text-slate-500">Gateway:</span>
                  <span class="ml-2 text-slate-200">{{ selectedContainer.gateway || '—' }}</span>
                </div>
                <div>
                  <span class="text-slate-500">Assigned Networks:</span>
                  <span class="ml-2 text-slate-200">{{ selectedContainer.networks?.join(', ') || 'bridge' }}</span>
                </div>
                <div>
                  <span class="text-slate-500">Cloudflared Network:</span>
                  <span class="ml-2" :class="selectedContainer.connected_to_cloudflared ? 'text-emerald-400 font-bold' : 'text-slate-400'">
                    {{ selectedContainer.connected_to_cloudflared ? 'Connected (Internal routing active)' : 'Not Connected' }}
                  </span>
                </div>
                <div class="md:col-span-2">
                  <span class="text-slate-500">Command:</span>
                  <span class="ml-2 text-slate-300 break-all">{{ selectedContainer.command || '—' }}</span>
                </div>
                <div class="md:col-span-2">
                  <span class="text-slate-500">Published Ports:</span>
                  <span class="ml-2 text-cyan-300">{{ selectedContainer.ports || 'None (Internal only)' }}</span>
                </div>
              </div>
            </div>

            <!-- Volumes & Mounts -->
            <div class="rounded-lg border border-slate-800 bg-slate-950 p-4 space-y-3">
              <h3 class="text-xs font-bold uppercase tracking-wider text-slate-400 flex items-center gap-2">
                <HardDrive class="h-4 w-4 text-amber-400" /> Volumes & Mounts
              </h3>
              <div v-if="selectedContainer.mounts && selectedContainer.mounts.length > 0" class="space-y-2">
                <div
                  v-for="(m, i) in selectedContainer.mounts"
                  :key="i"
                  class="flex flex-col sm:flex-row sm:items-center justify-between rounded border border-slate-800/80 bg-slate-900/60 p-2.5 text-xs font-mono gap-1"
                >
                  <div class="flex items-center gap-2 truncate">
                    <span class="text-cyan-400 truncate" :title="m.source">{{ m.source }}</span>
                    <span class="text-slate-500">➔</span>
                    <span class="text-emerald-400 truncate" :title="m.destination">{{ m.destination }}</span>
                  </div>
                  <span class="text-[10px] rounded px-1.5 py-0.5 bg-slate-800 text-slate-400 self-start sm:self-auto">
                    {{ m.mode || (m.rw ? 'Read/Write' : 'Read-Only') }}
                  </span>
                </div>
              </div>
              <p v-else class="text-xs text-slate-500">No persistent volume mounts mapped to this container.</p>
            </div>

            <!-- Environment Variables Table -->
            <div class="rounded-lg border border-slate-800 bg-slate-950 p-4 space-y-3">
              <div class="flex items-center justify-between">
                <h3 class="text-xs font-bold uppercase tracking-wider text-slate-400 flex items-center gap-2">
                  <Sliders class="h-4 w-4 text-violet-400" /> Environment Variables ({{ selectedContainer.env?.length || 0 }})
                </h3>
              </div>
              <div v-if="selectedContainer.env && selectedContainer.env.length > 0" class="max-h-56 overflow-y-auto space-y-1 font-mono text-xs">
                <div
                  v-for="(e, i) in selectedContainer.env"
                  :key="i"
                  class="rounded bg-slate-900/80 px-2 py-1 text-slate-300 break-all hover:bg-slate-800/60"
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
          <div v-else-if="selectedTab === 'access'" class="space-y-5">
            <!-- 1. PRIMARY LOCAL ACCESS (mDNS .local) -->
            <div class="rounded-xl border border-violet-500/30 bg-slate-950 p-5 space-y-4">
              <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-2 border-b border-slate-800 pb-3">
                <div class="flex items-center gap-2">
                  <Wifi class="h-5 w-5 text-violet-400" />
                  <div>
                    <h4 class="text-sm font-bold text-slate-100">Local LAN Access (mDNS)</h4>
                    <p class="text-xs text-slate-400">
                      Reach this container directly from any computer, phone, or tablet on your Wi-Fi with zero configuration.
                    </p>
                  </div>
                </div>
                <span class="rounded bg-violet-950/60 border border-violet-800/50 px-2.5 py-0.5 text-[10px] font-mono font-semibold text-violet-300">
                  Default (Internal Only)
                </span>
              </div>

              <!-- Primary URL Box -->
              <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 rounded-lg border border-violet-500/20 bg-violet-950/20 p-4">
                <div class="space-y-1">
                  <div class="text-[10px] uppercase tracking-wider font-semibold text-violet-300">Portless LAN Address</div>
                  <div class="flex items-center gap-2">
                    <span class="font-mono text-base font-bold text-slate-100">
                      http://{{ selectedContainer.name }}.local
                    </span>
                  </div>
                </div>

                <div class="flex items-center gap-2">
                  <a
                    :href="`http://${selectedContainer.name}.local`"
                    target="_blank"
                    rel="noopener"
                    class="cyber-btn flex items-center gap-1.5 text-xs text-violet-300 border-violet-700 bg-violet-950/40 hover:bg-violet-900/50"
                  >
                    <span>Open</span>
                    <ExternalLink class="h-3 w-3" />
                  </a>
                  <button
                    @click="copyToClipboard('mdns', `http://${selectedContainer.name}.local`)"
                    class="cyber-btn-secondary text-xs flex items-center gap-1.5"
                  >
                    <Check v-if="copiedKey === 'mdns'" class="h-3 w-3 text-emerald-400" />
                    <Copy v-else class="h-3 w-3" />
                    <span>Copy</span>
                  </button>
                </div>
              </div>

              <!-- Compact Internal Networking Details -->
              <div class="rounded-lg border border-slate-800/80 bg-slate-900/40 p-3 space-y-2">
                <div class="flex flex-wrap items-center justify-between gap-3 text-xs font-mono">
                  <div class="flex items-center gap-2">
                    <span class="text-slate-500 font-sans text-[11px] uppercase tracking-wider font-semibold">Docker DNS:</span>
                    <span class="text-emerald-400 font-bold">http://{{ selectedContainer.name }}:{{ getPrimaryPort(selectedContainer.ports) || '80' }}</span>
                  </div>
                  <div class="flex items-center gap-2">
                    <span class="text-slate-500 font-sans text-[11px] uppercase tracking-wider font-semibold">Host Port Mapping:</span>
                    <span :class="selectedContainer.ports ? 'text-amber-300 font-bold' : 'text-slate-500 font-sans'">
                      {{ selectedContainer.ports || 'None (mDNS proxy only)' }}
                    </span>
                  </div>
                </div>
                <p class="text-[11px] text-slate-500 font-sans border-t border-slate-800/60 pt-2 leading-relaxed">
                  <span class="text-slate-400 font-semibold">Note:</span> Docker DNS & host port mappings are internal routing details for container-to-container links. On your local network, browse directly with zero port numbers via <span class="text-violet-300 font-mono font-bold">http://{{ selectedContainer.name }}.local</span>.
                </p>
              </div>
            </div>

            <!-- Cloudflared Network Pruning Notice (If connected without public route) -->
            <div
              v-if="selectedContainer.connected_to_cloudflared && getExpose(selectedContainer.id).mode !== 'public'"
              class="rounded-xl border border-amber-900/40 bg-amber-950/20 p-4 flex flex-col sm:flex-row sm:items-center justify-between gap-3"
            >
              <div class="space-y-1">
                <div class="flex items-center gap-1.5 text-xs font-bold text-amber-300">
                  <ShieldAlert class="h-4 w-4" />
                  <span>Attached to Cloudflared Network (No Public Ingress)</span>
                </div>
                <p class="text-xs text-slate-400 leading-relaxed">
                  This container is attached to the Docker <code class="text-slate-300 font-mono">cloudflared</code> network, but has no public tunnel route configured. Disconnect it to keep it completely internal.
                </p>
              </div>
              <button
                @click="disconnectFromCloudflared(selectedContainer)"
                :disabled="disconnectingNetwork[selectedContainer.id]"
                class="cyber-btn-secondary text-xs text-amber-300 border-amber-800 hover:bg-amber-900/40 whitespace-nowrap self-start sm:self-auto flex items-center gap-1.5"
              >
                <RotateCcw v-if="disconnectingNetwork[selectedContainer.id]" class="h-3 w-3 animate-spin" />
                <span>{{ disconnectingNetwork[selectedContainer.id] ? 'Disconnecting…' : 'Disconnect from Cloudflare' }}</span>
              </button>
            </div>

            <!-- 2. OPTIONAL PUBLIC INGRESS (Cloudflare Tunnel) -->
            <div class="rounded-xl border border-slate-800 bg-slate-950 p-5 space-y-4">
              <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-2 border-b border-slate-800 pb-3">
                <div class="flex items-center gap-2">
                  <Globe class="h-5 w-5 text-cyan-400" />
                  <div>
                    <h4 class="text-sm font-bold text-slate-100">Public Ingress & Remote Access</h4>
                    <p class="text-xs text-slate-400">
                      Optionally expose this container to the global internet via Cloudflare Tunnel.
                    </p>
                  </div>
                </div>
                <span
                  class="rounded-full px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider self-start sm:self-auto"
                  :class="getExpose(selectedContainer.id).mode === 'public' ? 'bg-cyan-500/20 text-cyan-300 border border-cyan-500/40' : 'bg-slate-800 text-slate-400 border border-slate-700'"
                >
                  {{ getExpose(selectedContainer.id).mode === 'public' ? 'Public Tunnel Active' : 'Internal Only' }}
                </span>
              </div>

              <div class="grid gap-4 md:grid-cols-3">
                <div>
                  <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-400">Access Mode</label>
                  <select v-model="getExpose(selectedContainer.id).mode" class="cyber-input text-xs">
                    <option value="internal">Internal Only (Local mDNS)</option>
                    <option value="public">Public (Cloudflare Tunnel)</option>
                    <option value="private">Private Mesh (Tailscale/WireGuard)</option>
                  </select>
                </div>

                <div v-if="getExpose(selectedContainer.id).mode === 'public'">
                  <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-400">Public Hostname (FQDN)</label>
                  <input
                    v-model="getExpose(selectedContainer.id).hostname"
                    type="text"
                    placeholder="app.yourdomain.com"
                    class="cyber-input text-xs font-mono"
                  />
                </div>

                <div v-if="getExpose(selectedContainer.id).mode === 'public'">
                  <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-400">Tunnel Service Target</label>
                  <input
                    v-model="getExpose(selectedContainer.id).target"
                    type="text"
                    :placeholder="`http://${selectedContainer.name}:80`"
                    class="cyber-input text-xs font-mono"
                  />
                </div>

                <!-- Private Mesh Options -->
                <template v-if="getExpose(selectedContainer.id).mode === 'private'">
                  <div>
                    <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-400">Mesh Type</label>
                    <select v-model="getExpose(selectedContainer.id).private_type" class="cyber-input text-xs">
                      <option value="tailscale">Tailscale</option>
                      <option value="wireguard">WireGuard</option>
                    </select>
                  </div>
                  <div>
                    <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-400">Private Host / IP</label>
                    <input
                      v-model="getExpose(selectedContainer.id).private_host"
                      type="text"
                      placeholder="100.x.y.z"
                      class="cyber-input text-xs font-mono"
                    />
                  </div>
                  <div>
                    <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-400">Private Port</label>
                    <input
                      v-model.number="getExpose(selectedContainer.id).private_port"
                      type="number"
                      placeholder="8080"
                      class="cyber-input text-xs font-mono"
                    />
                  </div>
                </template>
              </div>

              <!-- Internal Mode Note -->
              <div v-if="getExpose(selectedContainer.id).mode === 'internal'" class="rounded border border-slate-800 bg-slate-900/40 p-3 text-xs text-slate-400">
                <span class="text-slate-300 font-semibold">🔒 Internal Only:</span>
                This container is kept private to your local network. It is not connected to Cloudflare and has no public internet route.
              </div>

              <!-- Active Public Link preview -->
              <div v-if="getExpose(selectedContainer.id).mode === 'public' && getExpose(selectedContainer.id).hostname" class="rounded border border-cyan-900/50 bg-cyan-950/30 p-3 flex items-center justify-between">
                <div class="flex items-center gap-2 text-xs">
                  <span class="text-slate-400">Live URL:</span>
                  <a
                    :href="`https://${getExpose(selectedContainer.id).hostname}`"
                    target="_blank"
                    rel="noopener"
                    class="font-mono text-cyan-300 hover:underline flex items-center gap-1 font-bold"
                  >
                    https://{{ getExpose(selectedContainer.id).hostname }}
                    <ExternalLink class="h-3 w-3" />
                  </a>
                </div>
                <button
                  @click="copyToClipboard('puburl', `https://${getExpose(selectedContainer.id).hostname}`)"
                  class="cyber-btn-sm text-xs"
                >
                  <Check v-if="copiedKey === 'puburl'" class="h-3 w-3 text-emerald-400 mr-1" />
                  <Copy v-else class="h-3 w-3 mr-1" />
                  <span>Copy</span>
                </button>
              </div>

              <div class="flex items-center justify-between pt-2 border-t border-slate-900">
                <span class="text-xs text-slate-500">
                  <span v-if="getExpose(selectedContainer.id).mode === 'public'">
                    Connecting this container to Cloudflare Tunnel and applying routing.
                  </span>
                  <span v-else>
                    Disconnects container from Cloudflare tunnel network.
                  </span>
                </span>
                <button
                  @click="saveExposure(selectedContainer)"
                  :disabled="exposeSaving[selectedContainer.id]"
                  class="cyber-btn flex items-center gap-1.5 text-xs text-cyan-300 border-cyan-600 bg-cyan-950/40 hover:bg-cyan-900/50"
                >
                  <ShieldCheck class="h-3.5 w-3.5" />
                  <span>{{ exposeSaving[selectedContainer.id] ? 'Saving…' : 'Save Access Mode' }}</span>
                </button>
              </div>
            </div>
          </div>

          <!-- ============================================== -->
          <!-- TAB 3: COMPOSE (docker-compose.yml)            -->
          <!-- ============================================== -->
          <div v-else-if="selectedTab === 'compose'" class="space-y-4">
            <!-- If Container is Linked to a Project -->
            <div v-if="selectedContainer.project_id" class="space-y-4">
              <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-2 border-b border-slate-800 pb-3">
                <div>
                  <h3 class="text-sm font-bold text-slate-100 flex items-center gap-2">
                    <FileCode class="h-4 w-4 text-cyan-400" />
                    <span>Stack Compose: {{ selectedContainer.project_name || selectedContainer.name }}</span>
                  </h3>
                  <p class="text-xs text-slate-400 font-mono">
                    {{ selectedContainer.project_path }}/{{ composeFilename }}
                  </p>
                </div>

                <div class="flex items-center gap-2">
                  <span v-if="composeMsg" class="text-xs text-emerald-400 font-medium">
                    {{ composeMsg }}
                  </span>
                  <button
                    @click="loadContainerCompose(selectedContainer.project_id)"
                    :disabled="composeLoading"
                    class="cyber-btn-secondary text-xs flex items-center gap-1"
                  >
                    <RotateCcw class="h-3 w-3" :class="composeLoading ? 'animate-spin' : ''" />
                    <span>Reload</span>
                  </button>
                  <button
                    @click="saveContainerCompose(false)"
                    :disabled="composeSaving"
                    class="cyber-btn text-xs flex items-center gap-1.5 text-cyan-300 border-cyan-600 bg-cyan-950/40 hover:bg-cyan-900/50"
                  >
                    <Save class="h-3.5 w-3.5" />
                    <span>Save</span>
                  </button>
                  <button
                    @click="saveContainerCompose(true)"
                    :disabled="composeSaving"
                    class="cyber-btn text-xs flex items-center gap-1.5 text-emerald-300 border-emerald-600 bg-emerald-950/40 hover:bg-emerald-900/50"
                    title="Save compose file and restart docker-compose stack"
                  >
                    <RotateCcw class="h-3.5 w-3.5" />
                    <span>Save & Restart</span>
                  </button>
                </div>
              </div>

              <!-- Compose Code Editor -->
              <div class="relative">
                <textarea
                  v-model="composeContent"
                  rows="18"
                  spellcheck="false"
                  placeholder="Loading compose file…"
                  class="w-full rounded-lg border border-slate-800 bg-slate-950 p-4 font-mono text-xs text-slate-200 focus:border-cyan-500 focus:outline-none leading-relaxed"
                ></textarea>
              </div>
            </div>

            <!-- If Container is Standalone (Offer Scaffolding) -->
            <div v-else class="rounded-xl border border-slate-800 bg-slate-950 p-8 text-center space-y-4">
              <Code2 class="mx-auto h-10 w-10 text-cyan-400" />
              <div class="max-w-md mx-auto space-y-1">
                <h3 class="text-base font-bold text-slate-100">Standalone Container</h3>
                <p class="text-xs text-slate-400">
                  This container was created standalone (e.g. via <code class="text-cyan-300">docker run</code>)
                  and is not currently tracked by a Compose project.
                </p>
                <p class="text-xs text-slate-500">
                  DockTun can automatically generate a clean <code class="text-slate-300">docker-compose.yml</code>
                  mirroring this container's image, volume mounts, environment variables, and ports, and link it as a project.
                </p>
              </div>

              <div class="pt-2">
                <button
                  @click="scaffoldProject"
                  :disabled="projectScaffolding"
                  class="cyber-btn flex items-center gap-2 mx-auto text-xs text-cyan-300 border-cyan-500 bg-cyan-950/60 hover:bg-cyan-900/60 px-5 py-2.5"
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
          <div v-else-if="selectedTab === 'env'" class="space-y-4">
            <div v-if="selectedContainer.project_id" class="space-y-4">
              <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-2 border-b border-slate-800 pb-3">
                <div>
                  <h3 class="text-sm font-bold text-slate-100 flex items-center gap-2">
                    <Sliders class="h-4 w-4 text-violet-400" />
                    <span>Environment File (.env)</span>
                  </h3>
                  <p class="text-xs text-slate-400 font-mono">
                    {{ selectedContainer.project_path }}/.env
                  </p>
                </div>

                <div class="flex items-center gap-2">
                  <span v-if="envMsg" class="text-xs text-emerald-400 font-medium">
                    {{ envMsg }}
                  </span>
                  <button
                    @click="loadContainerEnv(selectedContainer.project_id)"
                    :disabled="envLoading"
                    class="cyber-btn-secondary text-xs flex items-center gap-1"
                  >
                    <RotateCcw class="h-3 w-3" :class="envLoading ? 'animate-spin' : ''" />
                    <span>Reload</span>
                  </button>
                  <button
                    @click="saveContainerEnv(false)"
                    :disabled="envSaving"
                    class="cyber-btn text-xs flex items-center gap-1.5 text-cyan-300 border-cyan-600 bg-cyan-950/40 hover:bg-cyan-900/50"
                  >
                    <Save class="h-3.5 w-3.5" />
                    <span>Save</span>
                  </button>
                  <button
                    @click="saveContainerEnv(true)"
                    :disabled="envSaving"
                    class="cyber-btn text-xs flex items-center gap-1.5 text-emerald-300 border-emerald-600 bg-emerald-950/40 hover:bg-emerald-900/50"
                  >
                    <RotateCcw class="h-3.5 w-3.5" />
                    <span>Save & Restart</span>
                  </button>
                </div>
              </div>

              <!-- .env Editor -->
              <textarea
                v-model="envContent"
                rows="18"
                spellcheck="false"
                placeholder="# Enter KEY=VALUE environment variables&#10;PORT=8080&#10;DEBUG=false"
                class="w-full rounded-lg border border-slate-800 bg-slate-950 p-4 font-mono text-xs text-slate-200 focus:border-violet-500 focus:outline-none leading-relaxed"
              ></textarea>
            </div>

            <div v-else class="rounded-xl border border-slate-800 bg-slate-950 p-8 text-center space-y-4">
              <Sliders class="mx-auto h-10 w-10 text-violet-400" />
              <div class="max-w-md mx-auto space-y-1">
                <h3 class="text-base font-bold text-slate-100">Standalone Container</h3>
                <p class="text-xs text-slate-400">
                  Generate a project to manage persistent environment variables in a dedicated <code class="text-violet-300">.env</code> file.
                </p>
              </div>
              <button
                @click="scaffoldProject"
                :disabled="projectScaffolding"
                class="cyber-btn flex items-center gap-2 mx-auto text-xs text-cyan-300 border-cyan-500 bg-cyan-950/60 px-5 py-2.5"
              >
                <span>Generate Project & .env</span>
              </button>
            </div>
          </div>

          <!-- ============================================== -->
          <!-- TAB 5: FILES & STORAGE                         -->
          <!-- ============================================== -->
          <div v-else-if="selectedTab === 'files'" class="space-y-4">
            <div v-if="selectedContainer.project_id" class="space-y-4">
              <!-- Header & Breadcrumbs -->
              <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-800 pb-3">
                <div class="flex items-center gap-2 text-xs font-mono">
                  <FolderOpen class="h-4 w-4 text-amber-400" />
                  <span class="text-slate-400 cursor-pointer hover:text-slate-200" @click="loadContainerFiles(selectedContainer.project_id, '')">
                    {{ selectedContainer.project_path }}
                  </span>
                  <span v-if="currentDir" class="text-slate-600">/</span>
                  <span v-if="currentDir" class="text-cyan-300">{{ currentDir }}</span>
                </div>

                <div class="flex items-center gap-2">
                  <button
                    v-if="currentDir"
                    @click="loadContainerFiles(selectedContainer.project_id, currentDir.split('/').slice(0, -1).join('/'))"
                    class="cyber-btn-secondary text-xs"
                  >
                    Up One Level
                  </button>
                  <button
                    @click="showNewItemModal = true"
                    class="cyber-btn text-xs flex items-center gap-1.5"
                  >
                    <Plus class="h-3.5 w-3.5" />
                    <span>New Item</span>
                  </button>
                  <button
                    @click="loadContainerFiles(selectedContainer.project_id, currentDir)"
                    :disabled="filesLoading"
                    class="cyber-btn-secondary text-xs"
                  >
                    <RotateCcw class="h-3 w-3" :class="filesLoading ? 'animate-spin' : ''" />
                  </button>
                </div>
              </div>

              <!-- Active File Editor if editing a file -->
              <div v-if="editingFile" class="rounded-xl border border-slate-800 bg-slate-950 p-4 space-y-3">
                <div class="flex items-center justify-between border-b border-slate-800 pb-2">
                  <div class="flex items-center gap-2 text-xs font-mono text-cyan-300">
                    <FileText class="h-4 w-4 text-cyan-400" />
                    <span>{{ editingFile.path }}</span>
                  </div>
                  <div class="flex items-center gap-2">
                    <span v-if="editingFileMsg" class="text-xs text-emerald-400 font-medium">{{ editingFileMsg }}</span>
                    <button
                      @click="saveCurrentContainerFile"
                      :disabled="editingFileSaving"
                      class="cyber-btn text-xs flex items-center gap-1.5 text-cyan-300"
                    >
                      <Save class="h-3 w-3" />
                      <span>Save</span>
                    </button>
                    <button
                      @click="editingFile = null"
                      class="cyber-btn-secondary text-xs"
                    >
                      Close Editor
                    </button>
                  </div>
                </div>
                <textarea
                  v-model="editingFile.content"
                  rows="16"
                  spellcheck="false"
                  class="w-full rounded border border-slate-800 bg-slate-900 p-3 font-mono text-xs text-slate-200 focus:outline-none"
                ></textarea>
              </div>

              <!-- File List View -->
              <div v-else class="rounded-xl border border-slate-800 bg-slate-950 divide-y divide-slate-800/80">
                <div v-if="fileList.length === 0 && !filesLoading" class="p-8 text-center text-xs text-slate-500">
                  Folder is empty.
                </div>
                <div
                  v-for="item in fileList"
                  :key="item.path"
                  class="flex items-center justify-between px-4 py-2.5 text-xs hover:bg-slate-900/60 transition-colors"
                >
                  <div
                    class="flex items-center gap-2.5 cursor-pointer font-mono truncate"
                    @click="openContainerFile(item)"
                  >
                    <Folder v-if="item.is_dir" class="h-4 w-4 text-amber-400 shrink-0" />
                    <FileText v-else class="h-4 w-4 text-slate-400 shrink-0" />
                    <span :class="item.is_dir ? 'text-slate-200 font-semibold hover:text-cyan-300' : 'text-slate-300 hover:text-cyan-300'">
                      {{ item.name }}
                    </span>
                  </div>

                  <div class="flex items-center gap-4 text-slate-500 font-mono text-[11px]">
                    <span v-if="!item.is_dir">{{ (item.size / 1024).toFixed(1) }} KB</span>
                    <span>{{ item.mod_time }}</span>
                    <button
                      @click="deleteContainerFile(item.path)"
                      class="text-slate-600 hover:text-red-400 transition-colors"
                      title="Delete file"
                    >
                      <Trash2 class="h-3.5 w-3.5" />
                    </button>
                  </div>
                </div>
              </div>

              <!-- New Item Modal -->
              <div v-if="showNewItemModal" class="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/80 p-4">
                <div class="w-full max-w-sm rounded-xl border border-slate-800 bg-slate-900 p-5 space-y-4">
                  <h4 class="text-sm font-bold text-slate-100">Create New Item</h4>
                  <div>
                    <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-400">Item Name</label>
                    <input
                      v-model="newItemName"
                      type="text"
                      placeholder="filename.txt or folder_name"
                      class="cyber-input text-xs font-mono"
                    />
                  </div>
                  <div class="flex items-center gap-2 text-xs text-slate-300">
                    <input type="checkbox" id="isDirCheck" v-model="newItemIsDir" class="rounded bg-slate-950" />
                    <label for="isDirCheck">Create as directory</label>
                  </div>
                  <div class="flex justify-end gap-2 pt-2">
                    <button @click="showNewItemModal = false" class="cyber-btn-secondary text-xs">Cancel</button>
                    <button @click="createContainerFileItem" class="cyber-btn text-xs text-cyan-300">Create</button>
                  </div>
                </div>
              </div>
            </div>

            <div v-else class="space-y-4">
              <div class="rounded-xl border border-slate-800 bg-slate-950 p-5 space-y-3">
                <h3 class="text-xs font-bold uppercase tracking-wider text-slate-400 flex items-center gap-2">
                  <HardDrive class="h-4 w-4 text-amber-400" /> Container Mounts & Storage
                </h3>
                <div v-if="selectedContainer.mounts && selectedContainer.mounts.length > 0" class="space-y-2 font-mono text-xs">
                  <div
                    v-for="(m, i) in selectedContainer.mounts"
                    :key="i"
                    class="rounded border border-slate-800 bg-slate-900/60 p-3 space-y-1"
                  >
                    <div class="flex items-center justify-between">
                      <span class="text-slate-400">Host Path:</span>
                      <span class="text-cyan-400">{{ m.source }}</span>
                    </div>
                    <div class="flex items-center justify-between">
                      <span class="text-slate-400">Container Path:</span>
                      <span class="text-emerald-400">{{ m.destination }}</span>
                    </div>
                    <div class="flex items-center justify-between text-[11px] text-slate-500 pt-1">
                      <span>Permissions:</span>
                      <span>{{ m.rw ? 'Read/Write' : 'Read-Only' }}</span>
                    </div>
                  </div>
                </div>
                <p v-else class="text-xs text-slate-500">No storage mounts configured.</p>
              </div>

              <div class="rounded-xl border border-slate-800 bg-slate-950 p-6 text-center space-y-3">
                <FolderOpen class="mx-auto h-8 w-8 text-amber-400" />
                <p class="text-xs text-slate-400">Want full web file browsing and editing for this container?</p>
                <button
                  @click="scaffoldProject"
                  :disabled="projectScaffolding"
                  class="cyber-btn text-xs text-cyan-300 border-cyan-500 bg-cyan-950/60"
                >
                  Generate Project File Tree
                </button>
              </div>
            </div>
          </div>

          <!-- ============================================== -->
          <!-- TAB 6: REALTIME LOGS                           -->
          <!-- ============================================== -->
          <div v-else-if="selectedTab === 'logs'" class="space-y-3">
            <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-800 pb-3">
              <div class="space-y-0.5">
                <div class="flex flex-wrap items-center gap-2">
                  <ScrollText class="h-4 w-4 text-cyan-400" />
                  <span class="text-sm font-bold text-slate-100 font-mono">{{ selectedContainer.name }}</span>
                  <span class="rounded bg-slate-800 px-2 py-0.5 text-[10px] text-slate-400 font-mono">{{ selectedContainer.image }}</span>
                  <span class="inline-flex items-center gap-1 rounded bg-emerald-950/60 border border-emerald-800/50 px-2 py-0.5 text-[10px] text-emerald-400 font-medium">
                    <span class="h-1.5 w-1.5 rounded-full bg-emerald-400 animate-pulse"></span>
                    Isolated Service Logs
                  </span>
                </div>
                <p class="text-[11px] text-slate-500 font-mono">
                  Streaming stdout & stderr exclusively for container <span class="text-slate-400 font-bold">{{ selectedContainer.id?.slice(0, 12) }}</span>
                </p>
              </div>

              <div class="flex flex-wrap items-center gap-2">
                <label class="flex items-center gap-1.5 text-xs text-slate-400 cursor-pointer">
                  <input type="checkbox" v-model="modalLogsAutoRefresh" class="rounded bg-slate-950" />
                  <span>Live (3s)</span>
                </label>

                <label class="flex items-center gap-1.5 text-xs text-slate-400 cursor-pointer">
                  <input type="checkbox" v-model="modalLogsTimestamps" @change="fetchModalLogs" class="rounded bg-slate-950" />
                  <span>Timestamps</span>
                </label>

                <label class="flex items-center gap-1.5 text-xs text-slate-400 cursor-pointer">
                  <input type="checkbox" v-model="modalLogsWrap" class="rounded bg-slate-950" />
                  <span>Wrap</span>
                </label>

                <select
                  v-model.number="modalLogsTail"
                  @change="fetchModalLogs"
                  class="rounded border border-slate-700 bg-slate-950 px-2 py-1 text-xs text-slate-300 font-mono"
                >
                  <option :value="100">100 lines</option>
                  <option :value="200">200 lines</option>
                  <option :value="500">500 lines</option>
                  <option :value="1000">1000 lines</option>
                </select>

                <button
                  @click="copyToClipboard('logs', filteredModalLogs)"
                  class="cyber-btn-secondary text-xs flex items-center gap-1"
                  title="Copy logs"
                >
                  <Check v-if="copiedKey === 'logs'" class="h-3 w-3 text-emerald-400" />
                  <Copy v-else class="h-3 w-3" />
                  <span>Copy</span>
                </button>

                <button
                  @click="fetchModalLogs"
                  :disabled="modalLogsLoading"
                  class="cyber-btn-secondary text-xs flex items-center gap-1"
                >
                  <RotateCcw class="h-3 w-3" :class="modalLogsLoading ? 'animate-spin' : ''" />
                  <span>Refresh</span>
                </button>
              </div>
            </div>

            <!-- Filter Input Strip -->
            <div class="flex items-center justify-between gap-3 bg-slate-900/60 border border-slate-800/80 rounded-lg px-3 py-1.5">
              <div class="flex items-center gap-2 flex-1">
                <Search class="h-3.5 w-3.5 text-slate-500" />
                <input
                  v-model="modalLogsFilter"
                  type="text"
                  placeholder="Filter service logs (case-insensitive substring)..."
                  class="w-full bg-transparent text-xs text-slate-200 placeholder-slate-500 focus:outline-none font-mono"
                />
              </div>
              <span v-if="matchingLinesCount" class="text-[10px] text-cyan-400 font-mono whitespace-nowrap">
                {{ matchingLinesCount.matched }} / {{ matchingLinesCount.total }} lines
              </span>
              <button
                v-if="modalLogsFilter"
                @click="modalLogsFilter = ''"
                class="text-slate-500 hover:text-slate-300 text-xs"
              >
                Clear
              </button>
            </div>

            <!-- Terminal Output Box -->
            <div
              class="max-h-[58vh] overflow-y-auto rounded-lg border border-slate-800 bg-slate-950 p-4 font-mono text-xs leading-5 text-slate-300"
            >
              <div v-if="modalLogsLoading" class="text-slate-500 flex items-center gap-2">
                <RotateCcw class="h-3.5 w-3.5 animate-spin" /> Fetching latest logs for {{ selectedContainer.name }}…
              </div>
              <div v-else-if="!filteredModalLogs" class="text-slate-500 text-center py-6">
                {{ modalLogsFilter ? 'No lines matching filter query.' : 'No logs recorded for this service container.' }}
              </div>
              <pre
                v-else
                class="font-mono text-[11px] leading-relaxed"
                :class="modalLogsWrap ? 'whitespace-pre-wrap break-all' : 'whitespace-pre overflow-x-auto'"
              >{{ filteredModalLogs }}</pre>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
