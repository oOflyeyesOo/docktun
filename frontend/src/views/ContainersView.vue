<script setup lang="ts">
import { applicationContainers, applicationProjects, matchesServiceID } from '../lib/managed-services.mjs'
import { createProjectFileEditor } from '../lib/project-file-editor.mjs'
import { createAsyncScope, createSerialPoller } from '../lib/async-lifecycle.mjs'
import { settingsEqual, syncExposureDraft } from '../lib/exposure-draft.mjs'
import { getPrimaryPort, hasLocalWebLink, localAddress } from '../lib/containerPorts.mjs'
import { containerStack, stackProjectID } from '../lib/container-stacks.mjs'
import {
  parseCpuPercent,
  parseMemoryUsage,
  loadCollapsedStacks,
  saveCollapsedStacks,
  toggleStackCollapse as toggleStackCollapseHelper,
  collapseAllStacks as collapseAllStacksHelper,
  expandAllStacks as expandAllStacksHelper,
  calculateStackSelection,
  toggleStackSelection,
  calculateTableSelection,
  toggleTableSelection,
  shouldIncludeEmptyProjects,
} from '../lib/containers-table.mjs'
import ContainerInspectorModal from '../components/containers/modals/ContainerInspectorModal.vue'
import ViewDensityToggle from '../components/containers/table/ViewDensityToggle.vue'
import ContainersTable from '../components/containers/table/ContainersTable.vue'
import StackOutputModal from '../components/containers/modals/StackOutputModal.vue'
import StackDeployModal from '../components/containers/modals/StackDeployModal.vue'
import StackHistoryModal from '../components/containers/modals/StackHistoryModal.vue'
import StackDeleteModal from '../components/containers/modals/StackDeleteModal.vue'
import ContainerUpdateModal from '../components/containers/modals/ContainerUpdateModal.vue'
import StackLaunchModal from '../components/containers/modals/StackLaunchModal.vue'
import { computed, onMounted, onUnmounted, reactive, ref, toRefs, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  Server,
  Play,
  Square,
  RotateCcw,
  Trash2,
  Shield,
  ShieldCheck,
  Globe,
  ExternalLink,
  Cpu,
  Layers,
  ArrowUpDown,
  Search,
  Clock,
  HardDrive,
  Plus,
  Copy,
  Check,
  Network,
  Terminal,
  Sliders,
  Code2,
  ArrowUpCircle,
  Sparkles,
  Rocket,
  History,
  ChevronDown,
  ChevronRight,
  CheckSquare,
  X
} from 'lucide-vue-next'
import { useApi } from '../composables/useApi'

const { apiFetch } = useApi()

interface ExposureSettings {
  tailscale_node?: string
  mode: string
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
  exposure?: ExposureSettings
  project_id?: number
  project_name?: string
  project_path?: string
  compose_project?: string
  compose_service?: string
  compose_working_dir?: string
  compose_config_files?: string
  compose_sources?: string[]
  compose_source_error?: string
  compose_file?: string
  connected_to_cloudflared?: boolean
  update_available?: boolean
  latest_digest?: string
  current_digest?: string
  update_checked_at?: string
  update_error?: string
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


const containers = ref<ContainerItem[]>([])
const managedServices = ref<Array<{id: string; name: string; kind: string}>>([])
const registeredProjects = ref<Array<{id: number; name: string; path: string}>>([])
const serviceInventoryError = ref('')
let inventoryReady = false
const loading = ref(false)
const searchQuery = ref('')
const filter = ref<'all' | 'running' | 'stopped' | 'local' | 'public' | 'private' | 'exposed' | 'updates'>('all')

const checkingUpdates = ref(false)
const checkingSingleUpdate = ref<Record<string, boolean>>({})
const updatingContainer = ref<Record<string, boolean>>({})
const showUpdateConfirmModal = ref<{ container: ContainerItem } | null>(null)
const updatePlan=ref<any>(null),updatePlanLoading=ref(false),updatePlanError=ref('')
let updatePlanVersion=0
const updateResultModal = ref<{ name: string; output: string; success: boolean; error?: string } | null>(null)

const availableUpdatesCount = computed(() => {
  return containers.value.filter((c) => c.update_available).length
})

const actionLoading = ref<Record<string, string>>({})
const exposeForm = ref<Record<string, ExposureSettings>>({})
const exposureBaselines = ref<Record<string, ExposureSettings>>({})
const exposeSaving = ref<Record<string, boolean>>({})

function exposureFromContainer(c: ContainerItem): ExposureSettings {
  const saved = c.exposure || ({} as ExposureSettings)
  const port = Number(getPrimaryPort(c.ports)) || 80
  return {
    mode: saved.mode || c.exposure_mode || 'internal',
    hostname: saved.hostname ?? c.hostname ?? '',
    target: saved.target || c.target || `http://${c.name}:${port}`,
    private_type: saved.private_type ?? c.private_type ?? '',
    private_host: saved.private_host ?? c.private_host ?? '',
    private_port: saved.private_port || c.private_port || port,
    notes: saved.notes ?? c.notes ?? '',
    zero_trust_enabled: saved.zero_trust_enabled ?? c.zero_trust_enabled ?? false,
    zero_trust_emails: saved.zero_trust_emails ?? c.zero_trust_emails ?? '',
    zero_trust_auth_type: saved.zero_trust_auth_type || c.zero_trust_auth_type || 'email',
    zero_trust_session: saved.zero_trust_session || c.zero_trust_session || '24h',
  }
}

function sameContainerID(a = "", b = "") {
  return !!a && !!b && (a === b || a.startsWith(b) || b.startsWith(a))
}

function refreshExposureDraft(c: ContainerItem) {
  const keys = [...new Set([c.id, ...Object.keys(exposeForm.value).filter(id => sameContainerID(id, c.id))])]
  const key = keys.find(id => exposeForm.value[id]) || c.id
  const result = syncExposureDraft(exposeForm.value[key], exposureBaselines.value[key], exposureFromContainer(c))
  for (const id of keys) {
    exposeForm.value[id] = result.draft
    exposureBaselines.value[id] = result.baseline
  }
}

function getExpose(id?: string): ExposureSettings {
  if (!id) {
    return {
      mode: 'internal',
      hostname: '',
      target: '',
      private_type: '',
      private_host: '',
      private_port: 80,
      notes: '',
    }
  }

  // 1. Direct match
  if (exposeForm.value[id]) {
    return exposeForm.value[id]
  }

  // 2. Prefix match (e.g. 12-char short ID vs 64-char full SHA256 ID)
  for (const k of Object.keys(exposeForm.value)) {
    if (k.startsWith(id) || id.startsWith(k)) {
      return exposeForm.value[k]
    }
  }

  // 3. Fallback: resolve from selectedContainer or containers list
  const c = (selectedContainer.value && (selectedContainer.value.id === id || selectedContainer.value.id.startsWith(id) || id.startsWith(selectedContainer.value.id) || selectedContainer.value.name === id))
    ? selectedContainer.value
    : containers.value.find((item) => item.id === id || item.id.startsWith(id) || id.startsWith(item.id) || item.name === id)

  if (c) {
    const settings = exposureFromContainer(c)
    exposeForm.value[id] = settings
    exposureBaselines.value[id] = { ...settings }
    return settings
  }

  exposeForm.value[id] = {
    mode: 'internal',
    hostname: '',
    target: '',
    private_type: '',
    private_host: '',
    private_port: 80,
    notes: '',
  }
  return exposeForm.value[id]
}

// ==========================================
// Container Detail & Control Center State
// ==========================================
const selectedContainer = ref<ContainerDetail | null>(null)
const selectedTab = ref<'overview' | 'access' | 'compose' | 'env' | 'files' | 'logs' | 'exec' | 'inspect'>('overview')
const detailLoading = ref(false)
const projectScaffolding = ref(false)

// Compose state
const composeEditor = reactive(createProjectFileEditor('Compose', 'docker-compose.yml'))
const { filename: composeFilename, content: composeContent, loading: composeLoading, saving: composeSaving, message: composeMsg } = toRefs(composeEditor)
const projectFileAccess = ref<{ path: string; resolved_path?: string; available: boolean; writable: boolean; reason?: string } | null>(null)
const projectFilesWritable = computed(() => !!projectFileAccess.value?.available && !!projectFileAccess.value?.writable)
let selectionRevision = 0
function resetProjectEditors() {
  projectFileAccess.value = null
  composeEditor.invalidate()
  currentProject.value = null
}

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

// Managed Services Banner State
const showServicesBanner = ref(localStorage.getItem('docktun_hide_services_banner') !== 'true')
function dismissServicesBanner() {
  showServicesBanner.value = false
  try {
    localStorage.setItem('docktun_hide_services_banner', 'true')
  } catch {}
}

// ==========================================
// Launch Modal & Unified Stack State
// ==========================================
const showLaunchModal = ref(false)

function onStackDeployed(payload: { projectId: number; name: string; operationId?: string }) {
  stackActionScope.invalidate()
  fetchContainers()
  startStackOutputPolling(payload.projectId, `Deploying Compose Stack: ${payload.name}`, payload.operationId)
}

// Cloudflare Settings Cache for Zero Trust
const cfSettings = ref<{ has_token: boolean; masked_token?: string; api_token?: string } | null>(null)
async function fetchCfSettings() {
  try {
    const s = await apiFetch<any>('/api/cloudflare/settings')
    cfSettings.value = s
    if (s.api_token) {
      localStorage.setItem('docktun_cf_api_token', s.api_token)
    }
  } catch {}
}

// Active Project Metadata in Control Center
const currentProject = ref<{
  id: number
  name: string
  path: string
  repo_url: string
  git_username: string
  git_token: string
} | null>(null)
const projectGitSaving = ref(false)

// Stack Operations & Output Polling
const stackActionLoading = ref<Record<number, boolean>>({})
const stackOutputModal = ref<{ title: string; lines: string[]; done: boolean; projectId: number } | null>(null)
let stackPoller: ReturnType<typeof createSerialPoller> | null = null
const stackActionScope = createAsyncScope()

const stackOutputFailed = computed(() => {
  if (!stackOutputModal.value || !stackOutputModal.value.lines) return false
  return stackOutputModal.value.lines.some((l) =>
    l.includes('[ERROR]') ||
    l.includes('unable to prepare context') ||
    l.includes('exit status') ||
    l.includes('Bind for') ||
    l.includes('failed to set up container networking') ||
    l.toLowerCase().includes('error response from daemon') ||
    l.toLowerCase().includes('failed to')
  )
})

const showDeployPromptModal = ref<{ projectId: number; projectName: string } | null>(null)
const deployBranchName = ref('main')

const stackHistoryModal = ref<{ projectName: string; items: any[] } | null>(null)

const showDeleteStackModal = ref<{ projectId: number; projectName: string; path?: string } | null>(null)
const deleteStackDown = ref(true)
const deleteStackRemoveFiles = ref(false)
const deleteStackLoading = ref(false)
const deleteStackFolderChecking = ref(false)
const deleteStackFolderAvailable = ref(false)
const deleteStackFolderReason = ref('')

const filteredContainers = computed(() => {
  return containers.value.filter((c) => {
    if (filter.value === 'running' && !c.running) return false
    if (filter.value === 'stopped' && c.running) return false
    if (filter.value === 'local' && (!c.running || c.exposure_mode === 'public' || c.exposure_mode === 'private')) return false
    if (filter.value === 'public' && c.exposure_mode !== 'public') return false
    if (filter.value === 'private' && c.exposure_mode !== 'private') return false
    if (filter.value === 'exposed' && c.exposure_mode !== 'public' && c.exposure_mode !== 'private') return false
    if (filter.value === 'updates' && !c.update_available) return false

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

  let totalCpu = 0
  containers.value.forEach((c) => {
    totalCpu += parseCpuPercent(c.cpu, c.running)
  })

  return {
    total,
    running,
    stopped: total - running,
    publicCount,
    privateCount,
    stackCount: containerGroups.value.filter((g) => g.isStack).length,
    cpuUsage: totalCpu.toFixed(1) + '%',
  }
})

// parseCpuPercent and parseMemoryUsage imported from ../lib/containers-table.mjs

interface ContainerGroup {
  key: string
  name: string
  isStack: boolean
  projectId?: number
  projectPath?: string
  containers: ContainerItem[]
  runningCount: number
  totalCount: number
  totalCpu: string
  hasUpdates: boolean
  hasPublic: boolean
  hasZeroTrust: boolean
}

// Group By Stack & Collapsible States
const groupByStack = ref(localStorage.getItem('docktun_group_by_stack') !== 'false')
const collapsedStacks = ref<Record<string, boolean>>(loadCollapsedStacks(localStorage))

function toggleGroupByStack() {
  groupByStack.value = !groupByStack.value
  try {
    localStorage.setItem('docktun_group_by_stack', String(groupByStack.value))
  } catch {}
}

function toggleStackCollapse(key: string) {
  collapsedStacks.value = toggleStackCollapseHelper(collapsedStacks.value, key)
  saveCollapsedStacks(localStorage, collapsedStacks.value)
}

function collapseAllStacks() {
  collapsedStacks.value = collapseAllStacksHelper(containerGroups.value)
  saveCollapsedStacks(localStorage, collapsedStacks.value)
}

function expandAllStacks() {
  collapsedStacks.value = expandAllStacksHelper()
  saveCollapsedStacks(localStorage, collapsedStacks.value)
}

// View Density Mode (table vs grid)
const viewMode = ref<'table' | 'grid'>((localStorage.getItem('docktun:view_mode') as 'table' | 'grid') || 'table')

// Batch Multi-Select State
const selectedContainerIds = ref<Record<string, boolean>>({})
const batchLoading = ref(false)

function toggleSelectContainer(id: string, selected: boolean) {
  if (selected) {
    selectedContainerIds.value[id] = true
  } else {
    delete selectedContainerIds.value[id]
  }
}

const selectedCount = computed(() => {
  return filteredContainers.value.filter((c) => selectedContainerIds.value[c.id]).length
})

const allSelected = computed(() => {
  if (filteredContainers.value.length === 0) return false
  return filteredContainers.value.every((c) => selectedContainerIds.value[c.id])
})

function toggleSelectAll(selected?: boolean) {
  selectedContainerIds.value = toggleTableSelection(filteredContainers.value, selectedContainerIds.value, selected)
}

function toggleSelectStack(group: ContainerGroup) {
  selectedContainerIds.value = toggleStackSelection(group.containers, selectedContainerIds.value)
}

async function runBatchAction(action: 'start' | 'stop' | 'restart') {
  const ids = Object.keys(selectedContainerIds.value).filter((id) => selectedContainerIds.value[id])
  if (ids.length === 0) return

  if (!confirm(`Are you sure you want to ${action.toUpperCase()} ${ids.length} selected container(s)?`)) return

  batchLoading.value = true
  try {
    await apiFetch('/api/docker/containers/batch', {
      method: 'POST',
      body: JSON.stringify({ ids, action }),
    })
    await fetchContainers()
    selectedContainerIds.value = {}
  } catch (err: any) {
    alert(`Batch ${action} failed: ` + (err.message || err))
  } finally {
    batchLoading.value = false
  }
}

async function runBatchCheckUpdates() {
  const ids = Object.keys(selectedContainerIds.value).filter((id) => selectedContainerIds.value[id])
  if (ids.length === 0) return
  batchLoading.value = true
  try {
    for (const id of ids) {
      try {
        await apiFetch(`/api/docker/containers/${id}/check-update`, { method: 'POST' })
      } catch {}
    }
    await fetchContainers()
  } finally {
    batchLoading.value = false
  }
}

// Compute Container Groups
const containerGroups = computed<ContainerGroup[]>(() => {
  const list = filteredContainers.value
  if (!groupByStack.value) {
    let cpuSum = 0
    list.forEach((c) => {
      cpuSum += parseCpuPercent(c.cpu, c.running)
    })
    return [
      {
        key: 'all',
        name: 'All Containers',
        isStack: false,
        containers: list,
        runningCount: list.filter((c) => c.running).length,
        totalCount: list.length,
        totalCpu: cpuSum.toFixed(1) + '%',
        hasUpdates: list.some((c) => c.update_available),
        hasPublic: list.some((c) => c.exposure_mode === 'public'),
        hasZeroTrust: list.some((c) => c.zero_trust_enabled),
      },
    ]
  }

  const map = new Map<string, ContainerItem[]>()
  list.forEach((c) => {
    const key = containerStack(c).key
    if (!map.has(key)) {
      map.set(key, [])
    }
    map.get(key)!.push(c)
  })

  const groups: ContainerGroup[] = []
  map.forEach((items, key) => {
    let cpuSum = 0
    items.forEach((c) => {
      cpuSum += parseCpuPercent(c.cpu, c.running)
    })

    const isStack = key !== '__standalone__'
    const firstWithProj = items.find((c) => c.project_id)
    const projectId = stackProjectID(items)
    const projectPath = projectId ? firstWithProj?.project_path : undefined

    const groupName = containerStack(items[0]).name

    groups.push({
      key,
      name: groupName,
      isStack,
      projectId,
      projectPath,
      containers: items,
      runningCount: items.filter((c) => c.running).length,
      totalCount: items.length,
      totalCpu: cpuSum.toFixed(1) + '%',
      hasUpdates: items.some((c) => c.update_available),
      hasPublic: items.some((c) => c.exposure_mode === 'public'),
      hasZeroTrust: items.some((c) => c.zero_trust_enabled),
    })
  })

  // Include registered Compose projects with 0 running/stopped containers so they are visible and manageable
  // Only include empty registered projects when viewing all containers with no search query active
  if (shouldIncludeEmptyProjects(filter.value, searchQuery.value)) {
    registeredProjects.value.forEach((p) => {
      const existing = groups.find((g) => g.projectId === p.id || (g.isStack && g.name.toLowerCase() === p.name.toLowerCase()))
      if (!existing) {
        groups.push({
          key: `project:${p.id}`,
          name: p.name,
          isStack: true,
          projectId: p.id,
          projectPath: p.path,
          containers: [],
          runningCount: 0,
          totalCount: 0,
          totalCpu: '0.0%',
          hasUpdates: false,
          hasPublic: false,
          hasZeroTrust: false,
        })
      }
    })
  }

  // Sort stacks alphabetically, with standalone apps at the bottom.
  groups.sort((a, b) => {
    if (a.isStack && !b.isStack) return -1
    if (!a.isStack && b.isStack) return 1
    return a.name.localeCompare(b.name)
  })

  // Ensure empty stacks (0 containers matching active filter/search) NEVER render when a filter tab or search is active
  if (!shouldIncludeEmptyProjects(filter.value, searchQuery.value)) {
    return groups.filter((g) => g.containers && g.containers.length > 0)
  }

  return groups
})

async function fetchContainers() {
  try {
    const [data, maintenance, projectsList] = await Promise.all([
      apiFetch<ContainerItem[]>('/api/docker/containers'),
      apiFetch<any>('/api/services').catch((error: any) => { serviceInventoryError.value = error.message; return null }),
      apiFetch<any[]>('/api/projects').catch(() => []),
    ])
    if (maintenance) { managedServices.value = maintenance.services || []; serviceInventoryError.value = '' }
    if (Array.isArray(projectsList)) { registeredProjects.value = applicationProjects(projectsList, data || [], managedServices.value) }
    containers.value = applicationContainers(data || [], managedServices.value)
    inventoryReady = true
    containers.value.forEach(refreshExposureDraft)

    // If modal is open, sync basic stats into selectedContainer
    if (selectedContainer.value) {
      const updated = containers.value.find((c) => sameContainerID(c.id, selectedContainer.value?.id))
      if (updated) {
        selectedContainer.value.cpu = updated.cpu
        selectedContainer.value.memory = updated.memory
        selectedContainer.value.network = updated.network
        selectedContainer.value.block_io = updated.block_io
        selectedContainer.value.pids = updated.pids
        selectedContainer.value.running = updated.running
        selectedContainer.value.status = updated.status
        selectedContainer.value.connected_to_cloudflared = updated.connected_to_cloudflared
        selectedContainer.value.exposure = updated.exposure
        selectedContainer.value.exposure_mode = updated.exposure_mode
        selectedContainer.value.hostname = updated.hostname
        selectedContainer.value.target = updated.target
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

async function checkAllContainerUpdates() {
  checkingUpdates.value = true
  try {
    const results = await apiFetch('/api/docker/containers/check-updates', { method: 'POST' })
    await fetchContainers()
    const updatesFound = (results || []).filter((r: any) => r.update_available).length
    if (updatesFound > 0) {
      alert(`Update check complete: ${updatesFound} container${updatesFound > 1 ? 's have' : ' has'} updates available!`)
    } else {
      alert('All containers are up to date with remote registries.')
    }
  } catch (error: any) {
    alert(error.message || 'Failed to check updates')
  } finally {
    checkingUpdates.value = false
  }
}

async function checkSingleUpdate(container: ContainerItem) {
  checkingSingleUpdate.value[container.id] = true
  try {
    const res = await apiFetch(`/api/docker/containers/${container.id}/check-update`, { method: 'POST' })
    await fetchContainers()
    if (selectedContainer.value && (selectedContainer.value.id === container.id || sameContainerID(selectedContainer.value.id, container.id))) {
      selectedContainer.value.update_available = res.update_available
      selectedContainer.value.latest_digest = res.latest_digest
      selectedContainer.value.current_digest = res.current_digest
      selectedContainer.value.update_checked_at = res.checked_at
      selectedContainer.value.update_error = res.error
    }
    if (res.update_available) {
      alert(`Update available for ${container.name}!`)
    } else if (res.error) {
      if (res.error.includes('DockTun is built locally') || res.error.includes('Local build or private image')) {
        alert(res.error)
      } else {
        alert(`Update check notice for ${container.name}: ${res.error}`)
      }
    } else {
      alert(`${container.name} is up to date!`)
    }
  } catch (error: any) {
    alert(error.message || `Failed to check update for ${container.name}`)
  } finally {
    delete checkingSingleUpdate.value[container.id]
  }
}

async function confirmUpdate(container: ContainerItem) {
  showUpdateConfirmModal.value = { container };updatePlan.value=null;updatePlanError.value='';updatePlanLoading.value=true
  const v=++updatePlanVersion
  try{const result=await apiFetch('/api/docker/containers/'+container.id+'/update-plan',{method:'POST'});if(v===updatePlanVersion && showUpdateConfirmModal.value?.container.id===container.id)updatePlan.value=result}
  catch(e:any){if(v===updatePlanVersion)updatePlanError.value=e.message}
  finally{if(v===updatePlanVersion)updatePlanLoading.value=false}
}

async function performUpdate(container: ContainerItem) {
  if(!updatePlan.value?.ready || updatePlanLoading.value)return
  const expected=updatePlan.value.candidate_image
  showUpdateConfirmModal.value = null
  updatingContainer.value[container.id] = true
  try {
    const res = await apiFetch<{ status?: string; output?: string; container_id?: string; container_name?: string }>(
      `/api/docker/containers/${container.id}/update`,
      { method:'POST',body:JSON.stringify({expected_image_id:expected}) }
    )
    const isDocktunSelf = res.output && res.output.includes('self-update')
    updateResultModal.value = {
      name: container.name,
      output: res.output || 'Container updated successfully.',
      success: true,
    }
    if (isDocktunSelf) {
      setTimeout(() => {
        window.location.reload()
      }, 4500)
    } else {
      await fetchContainers()
      const isCurrent = selectedContainer.value && (
        sameContainerID(selectedContainer.value.id, container.id) ||
        selectedContainer.value.name === container.name ||
        (res?.container_id && sameContainerID(selectedContainer.value.id, res.container_id))
      )
      if (isCurrent) {
        const fresh = containers.value.find((c) =>
          (res?.container_id && sameContainerID(c.id, res.container_id)) ||
          c.name === container.name ||
          sameContainerID(c.id, container.id)
        )
        if (fresh) {
          await openContainerModal(fresh, selectedTab.value)
        } else if (res?.container_id) {
          await openContainerModal({ ...container, id: res.container_id }, selectedTab.value)
        }
      }
    }
  } catch (error: any) {
    updateResultModal.value = {
      name: container.name,
      output: error.message || 'Update failed.',
      success: false,
      error: error.message,
    }
  } finally {
    delete updatingContainer.value[container.id]
  }
}

const exposeMsg = ref<Record<string, { type: 'success' | 'error'; text: string }>>({})

async function saveExposure(container: ContainerItem) {
  exposeSaving.value[container.id] = true
  const rawPayload = { ...getExpose(container.id) }
  const payload: ExposureSettings = {
    ...rawPayload,
    private_port: rawPayload.private_port ? Number(rawPayload.private_port) || 80 : 80,
  }
  try {
    await apiFetch(`/api/docker/containers/${container.id}/exposure`, {
      method: 'POST',
      body: JSON.stringify(payload),
    })
    await fetchContainers()
    const saved = containers.value.find((item) => sameContainerID(item.id, container.id))
    if (saved) {
      const fresh = exposureFromContainer(saved)
      const pristine = settingsEqual(getExpose(container.id), payload)
      for (const id of new Set([container.id, saved.id, ...Object.keys(exposeForm.value).filter(id => sameContainerID(id, container.id))])) {
        if (pristine) exposeForm.value[id] = { ...fresh }
        exposureBaselines.value[id] = { ...fresh }
      }
    }
    exposeMsg.value[container.id] = {
      type: 'success',
      text: 'Exposure settings saved.',
    }
    setTimeout(() => {
      delete exposeMsg.value[container.id]
    }, 6000)
  } catch (error: any) {
    exposeMsg.value[container.id] = { type: 'error', text: 'Save failed: ' + error.message }
    await fetchContainers() // An accepted route can remain after a separate DNS failure.
  } finally {
    exposeSaving.value[container.id] = false
  }
}

// ==========================================
// Control Center Modal Logic
// ==========================================
async function openContainerModal(c: ContainerItem, tab: 'overview' | 'access' | 'compose' | 'env' | 'files' | 'logs' | 'exec' | 'inspect' = 'overview') {
  const selection = ++selectionRevision
  resetProjectEditors()
  selectedContainer.value = { ...c } as ContainerDetail
  selectedTab.value = tab
  detailLoading.value = true

  refreshExposureDraft(c)

  try {
    const detail = await apiFetch<ContainerDetail>(`/api/docker/containers/${c.id}`)
    if (selection !== selectionRevision) return
    selectedContainer.value = detail
    if (detail.connected_to_cloudflared !== undefined) {
      c.connected_to_cloudflared = detail.connected_to_cloudflared
    }
    refreshExposureDraft({ ...c, ...detail })
  } catch (err: any) {
    console.error('Failed to load container details:', err)
  } finally {
    if (selection === selectionRevision) detailLoading.value = false
  }

  // Load content for active tab
  if (selection === selectionRevision) handleTabChange(selectedTab.value)
}

function openStackComposeModal(projectId: number, projectName: string, projectPath?: string) {
  selectionRevision++
  resetProjectEditors()
  const dummy: any = {
    id: `project-${projectId}`,
    name: projectName,
    image: 'compose-stack',
    status: 'stopped',
    running: false,
    project_id: projectId,
    project_name: projectName,
    project_path: projectPath || `/opt/projects/${projectName}`,
  }
  selectedContainer.value = dummy
  selectedTab.value = 'compose'
  handleTabChange('compose')
}

function closeContainerModal() {
  selectionRevision++
  resetProjectEditors()
  selectedContainer.value = null
  composeMsg.value = ''
}

function handleTabChange(tab: 'overview' | 'access' | 'compose' | 'env' | 'files' | 'logs' | 'exec' | 'inspect') {
  selectedTab.value = tab
  if (!selectedContainer.value) return

  if (tab === 'compose') {
    if (selectedContainer.value.project_id) {
      loadContainerCompose(selectedContainer.value.project_id)
      loadProjectInfo(selectedContainer.value.project_id)
    }
  }
}

watch(selectedTab, (newTab) => {
  handleTabChange(newTab)
})

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
      }
    }
  } catch (e: any) {
    alert(`Failed to create project: ${e.message}`)
  } finally {
    projectScaffolding.value = false
  }
}

// --- Compose Functions ---
async function loadProjectFileAccess(projectId: number) {
  const selection = selectionRevision
  const access = await apiFetch<any>(`/api/projects/${projectId}/file-access`)
  if (selection === selectionRevision && selectedContainer.value?.project_id === projectId) projectFileAccess.value = access
  if (!access.available) throw new Error(access.reason || 'Mount this stack folder in DockTun to access its files.')
  return access
}

async function loadContainerCompose(projectId: number) {
  await composeEditor.load(projectId, async (id) => {
    await loadProjectFileAccess(id)
    return apiFetch(`/api/projects/${id}/compose`)
  })
}

async function saveContainerCompose(restart = false) {
  if (!projectFilesWritable.value || selectedContainer.value?.project_id !== composeEditor.projectId) return
  const saved = await composeEditor.save(
    (id, body) => apiFetch(`/api/projects/${id}/compose`, { method: 'PUT', body: JSON.stringify(body) }),
    restart ? (id) => apiFetch(`/api/projects/${id}/action/restart`, { method: 'POST' }) : undefined,
  )
  if (saved && restart) await fetchContainers()
}

// ==========================================
// Stack & Launcher Helper Functions
// ==========================================
async function loadProjectInfo(projectId: number) {
  const selection = selectionRevision
  try {
    const list: any[] = await apiFetch('/api/projects')
    if (selection !== selectionRevision || selectedContainer.value?.project_id !== projectId) return
    if (Array.isArray(list)) {
      const found = list.find((p) => p.id === projectId)
      if (found) {
        currentProject.value = {
          id: found.id,
          name: found.name,
          path: found.path,
          repo_url: found.repo_url || '',
          git_username: found.git_username || '',
          git_token: found.git_token || '',
        }
      }
    }
  } catch (err) {
    console.error('Failed to load project info:', err)
  }
}

async function saveProjectGit() {
  if (!currentProject.value) return
  projectGitSaving.value = true
  try {
    await apiFetch(`/api/projects/${currentProject.value.id}`, {
      method: 'PUT',
      body: JSON.stringify(currentProject.value),
    })
    alert('Git configuration saved successfully!')
    if (selectedContainer.value) {
      checkSingleUpdate(selectedContainer.value)
    }
  } catch (e: any) {
    alert('Failed to save Git configuration: ' + (e.message || e))
  } finally {
    projectGitSaving.value = false
  }
}

function stopStackOutputPolling() {
  stackPoller?.stop()
  stackPoller = null
  if (stackOutputModal.value) delete stackActionLoading.value[stackOutputModal.value.projectId]
}

function closeStackOutput() {
  stackActionScope.invalidate()
  stopStackOutputPolling()
  stackOutputModal.value = null
}

function startStackOutputPolling(projectId: number, title: string, operationId?: string) {
  stopStackOutputPolling()
  stackActionLoading.value[projectId] = true
  stackOutputModal.value = { title, lines: ['Initializing stack action...'], done: false, projectId }
  const modal = stackOutputModal.value
  const query = operationId ? '?operation_id=' + encodeURIComponent(operationId) : ''
  stackPoller = createSerialPoller(async (current: () => boolean) => {
    const res: any = await apiFetch(`/api/projects/${projectId}/output${query}`)
    if (!current() || stackOutputModal.value !== modal) return true
    modal.lines = res.lines || []
    if (res.done) {
      modal.done = true
      delete stackActionLoading.value[projectId]
      await fetchContainers()
      return true
    }
    return false
  }, 1000, {
    onError(error: any) {
      if (stackOutputModal.value !== modal) return
      modal.lines.push('[ERROR] ' + (error.message || 'Could not read operation output'))
      modal.done = true
      delete stackActionLoading.value[projectId]
    },
  })
  stackPoller.start(1000)
}

async function runStackAction(projectId: number, action: 'up' | 'down' | 'restart') {
  if (!projectId) {
    alert('This stack does not have an active compose project ID linked yet.')
    return
  }
  if (stackActionLoading.value[projectId]) return
  if (!confirm(`Are you sure you want to run compose "${action}" on this stack?`)) return
  stackActionLoading.value[projectId] = true
  const current = stackActionScope.begin()
  try {
    const res: any = await apiFetch(`/api/projects/${projectId}/action/${action}`, { method: 'POST' })
    if (!current()) { delete stackActionLoading.value[projectId]; return }
    startStackOutputPolling(projectId, `Compose ${action.toUpperCase()}`, res.operation_id)
  } catch (e: any) {
    if (current()) alert(`Stack action "${action}" failed: ` + (e.message || e))
    delete stackActionLoading.value[projectId]
  }
}

function openDeployPrompt(projectId: number, projectName: string) {
  showDeployPromptModal.value = { projectId, projectName }
  deployBranchName.value = 'main'
}

async function confirmDeploy(optionalBranch?: string) {
  if (!showDeployPromptModal.value) return
  const pId = showDeployPromptModal.value.projectId
  if (stackActionLoading.value[pId]) return
  const branch = (typeof optionalBranch === 'string' && optionalBranch.trim()) ? optionalBranch.trim() : (deployBranchName.value.trim() || 'main')
  showDeployPromptModal.value = null

  stackActionLoading.value[pId] = true
  const current = stackActionScope.begin()
  try {
    const res: any = await apiFetch(`/api/projects/${pId}/action/deploy`, {
      method: 'POST',
      body: JSON.stringify({ ref: branch }),
    })
    if (!current()) { delete stackActionLoading.value[pId]; return }
    startStackOutputPolling(pId, `Deploy Branch/Tag: ${branch}`, res.operation_id)
  } catch (e: any) {
    if (current()) alert('Deploy failed: ' + (e.message || e))
    delete stackActionLoading.value[pId]
  }
}

async function openStackHistory(projectId: number, projectName: string) {
  try {
    const res: any = await apiFetch(`/api/projects/${projectId}/history`)
    stackHistoryModal.value = {
      projectName: projectName || `Project #${projectId}`,
      items: Array.isArray(res) ? res : [],
    }
  } catch (e: any) {
    alert('Failed to load history: ' + (e.message || e))
  }
}

async function openDeleteStackPrompt(projectId: number, projectName: string, path?: string) {
  showDeleteStackModal.value = { projectId, projectName, path }
  const dialog = showDeleteStackModal.value
  deleteStackDown.value = true
  deleteStackRemoveFiles.value = false
  deleteStackFolderAvailable.value = false
  deleteStackFolderReason.value = ''
  deleteStackFolderChecking.value = true
  try {
    const options = await apiFetch<{ path: string; can_remove_files: boolean; reason?: string }>(`/api/projects/${projectId}/delete-options`)
    if (showDeleteStackModal.value?.projectId !== projectId || showDeleteStackModal.value !== dialog) return
    deleteStackFolderAvailable.value = options.can_remove_files === true
    deleteStackFolderReason.value = options.reason || 'Folder removal is unavailable. You can still remove the stack containers.'
    showDeleteStackModal.value.path = options.path
  } catch {
    if (showDeleteStackModal.value?.projectId !== projectId || showDeleteStackModal.value !== dialog) return
    deleteStackFolderReason.value = 'Could not check this folder. You can still remove the stack containers.'
  } finally {
    if (showDeleteStackModal.value === dialog) deleteStackFolderChecking.value = false
  }
}

async function confirmDeleteStack() {
  if (!showDeleteStackModal.value) return
  const { projectId } = showDeleteStackModal.value
  deleteStackLoading.value = true
  try {
    const downParam = deleteStackDown.value ? 'true' : 'false'
    const removeFilesParam = deleteStackRemoveFiles.value && deleteStackFolderAvailable.value && !deleteStackFolderChecking.value ? 'true' : 'false'
    await apiFetch(`/api/projects/${projectId}?down=${downParam}&remove_files=${removeFilesParam}`, {
      method: 'DELETE',
    })
    showDeleteStackModal.value = null
    if (selectedContainer.value && selectedContainer.value.project_id === projectId) {
      selectedContainer.value = null
    }
    await fetchContainers()
  } catch (err: any) {
    alert('Failed to delete stack: ' + (err.message || err))
  } finally {
    deleteStackLoading.value = false
  }
}

function openLaunchModal() {
  showLaunchModal.value = true
}

async function disconnectContainerNetwork(container: ContainerItem, network: string) {
  if (container.exposure?.tailscale_node === network) return
  if (!confirm(`Disconnect ${container.name} from ${network}? Connections using this network may stop working. The network and other containers are kept.`)) return
  disconnectingNetwork.value[container.id] = true
  try {
    await apiFetch(`/api/docker/containers/${container.id}/network/disconnect`, { method: 'POST', body: JSON.stringify({ network }) })
    container.networks = container.networks?.filter((name) => name !== network)
    if (selectedContainer.value?.id === container.id) selectedContainer.value.networks = container.networks
    await fetchContainers()
  } catch (e: any) { alert('Failed to disconnect network: ' + e.message) }
  finally { delete disconnectingNetwork.value[container.id] }
}


// Extract first published port if any
let timer: any = null
const route = useRoute()
const router = useRouter()

function checkRouteParams() {
  if (!inventoryReady) return
  const targetId = (route.query.id as string) || ''
  const targetName = (route.query.name as string) || ''
  const initialTab = (route.query.tab as any) || 'overview'
  if (managedServices.value.some(service => matchesServiceID(targetId, service.id) || service.name === targetName.replace(/^\//, ''))) { router.replace('/settings#docktun-services'); return }

  if (targetId || targetName) {
    const cleanTargetName = targetName.replace(/^\//, '').toLowerCase()
    const target = containers.value.find((c) => {
      if (targetId) {
        if (c.id === targetId || c.id.startsWith(targetId) || targetId.startsWith(c.id)) return true
      }
      if (cleanTargetName) {
        const cName = c.name.replace(/^\//, '').toLowerCase()
        return cName === cleanTargetName
      }
      return false
    })
    if (target) {
      openContainerModal(target, initialTab)
    } else {
      // Fallback: If not found in current containers list, fetch directly from backend by targetId or name
      const lookupKey = targetId || cleanTargetName
      if (lookupKey && !selectedContainer.value) {
        detailLoading.value = true
        apiFetch<ContainerDetail>(`/api/docker/containers/${encodeURIComponent(lookupKey)}`)
          .then((detail) => {
            if (detail && detail.id) {
              openContainerModal(detail, initialTab)
            }
          })
          .catch((err) => {
            console.warn('Could not auto-open container from query:', lookupKey, err)
          })
          .finally(() => {
            detailLoading.value = false
          })
      }
    }
  }

  // Support opening stack directly via ?project=<id> or ?id=<project_id>
  const targetProjectId = route.query.project || (route.query.id && !containers.value.some((c) => c.id === (route.query.id as string)) ? route.query.id : null)
  if (targetProjectId) {
    const pId = parseInt(targetProjectId as string, 10)
    if (!isNaN(pId)) {
      const pTarget = containers.value.find((c) => c.project_id === pId)
      if (pTarget) {
        openContainerModal(pTarget, 'compose')
      }
    }
  }

  if (route.query.launch === 'true') {
    openLaunchModal()
  }

  if (route.query.filter && ['all', 'running', 'stopped', 'local', 'public', 'private', 'updates'].includes(route.query.filter as string)) {
    filter.value = route.query.filter as any
  }
  if (route.query.search) {
    searchQuery.value = route.query.search as string
  }
}

watch(() => route.query, () => {
  checkRouteParams()
})

onMounted(async () => {
  checkRouteParams()
  fetchCfSettings()
  loading.value = true
  try {
    await fetchContainers()
  } finally {
    loading.value = false
  }
  checkRouteParams()

  // Live polling every 4 seconds
  timer = setInterval(() => {
    if (document.visibilityState === 'visible') {
      fetchContainers()
    }
  }, 4000)
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
  stackActionScope.dispose()
  stopStackOutputPolling()
})
</script>

<template>
  <div class="space-y-6">
    <!-- Header -->
    <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <div class="flex items-center gap-2 text-[10px] font-semibold uppercase tracking-[0.22em] text-slate-600">
          <Layers class="h-3.5 w-3.5 text-cyan-600" />
          <span>Container Orchestration</span>
        </div>
        <h1 class="text-2xl font-bold tracking-tight text-slate-900">Containers & Stacks</h1>
        <p class="text-xs text-slate-600">
          Manage standalone containers, docker-compose stacks, git deployments, and network ingress.
        </p>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <button
          @click="openLaunchModal"
          class="cyber-btn flex items-center gap-1.5 text-xs text-cyan-700 border-cyan-500/50 bg-cyan-50/40 hover:bg-cyan-100/60 shadow-sm"
          title="Run a new Docker container or deploy a Compose stack"
        >
          <Plus class="h-3.5 w-3.5 text-cyan-600" />
          <span>Deploy / Launch</span>
        </button>

        <button
          @click="checkAllContainerUpdates"
          :disabled="checkingUpdates || loading"
          class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-amber-700 hover:border-amber-500/60 hover:bg-amber-50/30"
          title="Check remote registries for newer image tags on all containers"
        >
          <Sparkles class="h-3.5 w-3.5 text-amber-600" :class="checkingUpdates ? 'animate-spin' : ''" />
          <span>{{ checkingUpdates ? 'Checking…' : 'Check Updates' }}</span>
          <span
            v-if="availableUpdatesCount > 0"
            class="ml-1 rounded-full bg-amber-500/20 px-1.5 py-0.2 text-[10px] font-bold text-amber-700 border border-amber-500/40"
          >
            {{ availableUpdatesCount }}
          </span>
        </button>



        <button
          @click="fetchContainers"
          :disabled="loading"
          class="cyber-btn flex items-center gap-1.5 text-xs text-slate-700"
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
        <div class="flex items-center justify-between text-xs text-slate-600">
          <span>Total Containers</span>
          <Server class="h-4 w-4 text-slate-500" />
        </div>
        <div class="mt-2 text-2xl font-bold text-slate-900 font-mono">{{ summary.total }}</div>
      </div>

      <div class="cyber-card flex flex-col justify-between p-3.5">
        <div class="flex items-center justify-between text-xs text-emerald-600">
          <span>Running</span>
          <Play class="h-4 w-4 text-emerald-500 fill-emerald-500" />
        </div>
        <div class="mt-2 text-2xl font-bold text-emerald-600 font-mono">{{ summary.running }}</div>
      </div>

      <div class="cyber-card flex flex-col justify-between p-3.5">
        <div class="flex items-center justify-between text-xs text-slate-600">
          <span>Stopped</span>
          <Square class="h-4 w-4 text-slate-500 fill-slate-500" />
        </div>
        <div class="mt-2 text-2xl font-bold text-slate-600 font-mono">{{ summary.stopped }}</div>
      </div>

      <div class="cyber-card flex flex-col justify-between p-3.5">
        <div class="flex items-center justify-between text-xs text-indigo-600">
          <span>Compose Stacks</span>
          <Layers class="h-4 w-4 text-indigo-600" />
        </div>
        <div class="mt-2 text-2xl font-bold text-indigo-700 font-mono">{{ summary.stackCount }}</div>
      </div>



      <div class="cyber-card flex flex-col justify-between p-3.5">
        <div class="flex items-center justify-between text-xs text-cyan-600">
          <span>Exposed</span>
          <Globe class="h-4 w-4 text-cyan-600" />
        </div>
        <div class="mt-2 text-2xl font-bold text-cyan-600 font-mono">{{ summary.publicCount + summary.privateCount }}</div>
      </div>

      <router-link
        to="/metrics"
        class="cyber-card flex flex-col justify-between p-3.5 hover:border-amber-400 hover:shadow-xs transition group cursor-pointer"
        title="View full host & container resource metrics"
      >
        <div class="flex items-center justify-between text-xs text-amber-600">
          <span class="group-hover:underline">Host CPU Load</span>
          <Cpu class="h-4 w-4 text-amber-600" />
        </div>
        <div class="mt-2 flex items-baseline justify-between">
          <div class="text-2xl font-bold text-amber-600 font-mono">{{ summary.cpuUsage }}</div>
          <span class="text-[10px] text-amber-700/80 font-mono group-hover:underline">Metrics &rarr;</span>
        </div>
      </router-link>
    </div>

      <!-- Dismissible compact banner for DockTun managed services -->
      <div
        v-if="showServicesBanner"
        class="flex items-center justify-between gap-3 rounded-lg border border-indigo-100 bg-indigo-50/50 px-3.5 py-2 text-xs transition-all"
      >
        <div class="flex items-center gap-2 text-slate-600 min-w-0">
          <Sparkles class="h-3.5 w-3.5 text-indigo-500 shrink-0" />
          <span class="truncate">Your apps and stacks. DockTun and its managed services live in Settings.</span>
          <router-link to="/settings#docktun-services" class="font-semibold text-indigo-700 hover:text-indigo-900 shrink-0 ml-1 inline-flex items-center gap-0.5">
            <span>DockTun services</span>
            <span>&rarr;</span>
          </router-link>
        </div>
        <button
          type="button"
          @click="dismissServicesBanner"
          class="rounded p-1 text-slate-400 hover:bg-indigo-100/60 hover:text-slate-700 transition-colors shrink-0 cursor-pointer"
          title="Dismiss notice"
        >
          <X class="h-3.5 w-3.5" />
        </button>
      </div>
      <p v-if="serviceInventoryError" role="status" class="text-xs text-amber-800">Could not refresh service ownership. {{ serviceInventoryError }}</p>
    <!-- Filter & Search Bar -->
    <div class="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
      <!-- Filter tabs -->
      <div class="flex flex-wrap gap-1 rounded-lg border border-slate-200 bg-white/60 p-1">
        <button
          @click="filter = 'all'"
          class="rounded-md px-3 py-1.5 text-xs font-semibold uppercase tracking-wider transition-colors"
          :class="filter === 'all' ? 'bg-slate-100 text-cyan-600 shadow-sm' : 'text-slate-600 hover:text-slate-800'"
        >
          All ({{ containers.length }})
        </button>
        <button
          @click="filter = 'running'"
          class="rounded-md px-3 py-1.5 text-xs font-semibold uppercase tracking-wider transition-colors"
          :class="filter === 'running' ? 'bg-slate-100 text-emerald-600 shadow-sm' : 'text-slate-600 hover:text-slate-800'"
        >
          Running ({{ summary.running }})
        </button>
        <button
          @click="filter = 'exposed'"
          class="rounded-md px-3 py-1.5 text-xs font-semibold uppercase tracking-wider transition-colors"
          :class="filter === 'exposed' ? 'bg-slate-100 text-cyan-700 shadow-sm' : 'text-slate-600 hover:text-slate-800'"
        >
          Exposed ({{ summary.publicCount + summary.privateCount }})
        </button>
        <button
          @click="filter = 'updates'"
          class="rounded-md px-3 py-1.5 text-xs font-semibold uppercase tracking-wider transition-colors flex items-center gap-1.5"
          :class="filter === 'updates' ? 'bg-amber-500/15 text-amber-700 border border-amber-500/30 shadow-sm' : 'text-slate-600 hover:text-slate-800'"
        >
          <ArrowUpCircle class="h-3.5 w-3.5 text-amber-600" />
          <span>Updates</span>
          <span
            v-if="availableUpdatesCount > 0"
            class="rounded-full bg-amber-500/20 px-1.5 py-0.2 text-[10px] font-bold text-amber-700 border border-amber-500/40"
          >
            {{ availableUpdatesCount }}
          </span>
          <span v-else class="text-[10px] text-slate-500 font-mono">(0)</span>
        </button>
        <button
          @click="filter = 'stopped'"
          class="rounded-md px-3 py-1.5 text-xs font-semibold uppercase tracking-wider transition-colors"
          :class="filter === 'stopped' ? 'bg-slate-100 text-slate-700 shadow-sm' : 'text-slate-600 hover:text-slate-800'"
        >
          Stopped ({{ summary.stopped }})
        </button>
      </div>

      <!-- Controls: Density, Grouping, Selection & Search -->
      <div class="flex flex-wrap items-center gap-2">
        <!-- View Density Toggle (Table vs Grid) -->
        <ViewDensityToggle v-model="viewMode" />

        <!-- Group by Stack Toggle -->
        <button
          @click="toggleGroupByStack"
          class="cyber-btn-secondary flex items-center gap-1.5 text-xs py-1.5 px-3"
          :class="groupByStack ? 'border-indigo-500/60 bg-indigo-50/40 text-indigo-700' : 'text-slate-600 hover:text-slate-800'"
          title="Toggle grouping containers by Compose Stacks"
        >
          <Layers class="h-3.5 w-3.5" :class="groupByStack ? 'text-indigo-600' : 'text-slate-500'" />
          <span>{{ groupByStack ? 'Grouped by Stack' : 'Flat List' }}</span>
        </button>

        <!-- Search box -->
        <div class="relative w-full sm:w-60">
          <Search class="absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500" />
          <input
            v-model="searchQuery"
            type="text"
            placeholder="Search name, image, stack..."
            class="cyber-input pl-9 text-xs"
          />
        </div>
      </div>
    </div>

    <!-- Sticky Batch Action Toolbar (When 1 or more containers selected) -->
    <transition
      enter-active-class="transition duration-150 ease-out"
      enter-from-class="transform -translate-y-2 opacity-0"
      enter-to-class="transform translate-y-0 opacity-100"
      leave-active-class="transition duration-100 ease-in"
      leave-from-class="transform translate-y-0 opacity-100"
      leave-to-class="transform -translate-y-2 opacity-0"
    >
      <div
        v-if="selectedCount > 0"
        class="sticky top-4 z-40 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-cyan-500/50 bg-white/95 p-3 shadow-sm backdrop-blur-md"
      >
        <div class="flex items-center gap-3">
          <div class="flex items-center gap-2">
            <CheckSquare class="h-4 w-4 text-cyan-600" />
            <span class="text-xs font-bold text-slate-900 font-mono tracking-wider">
              {{ selectedCount }} SELECTED
            </span>
          </div>
          <button
            @click="toggleSelectAll"
            class="text-[11px] font-medium text-cyan-600 hover:text-cyan-700 underline underline-offset-2 cursor-pointer"
          >
            {{ allSelected ? 'Deselect all' : 'Select all' }}
          </button>
        </div>

        <div class="flex flex-wrap items-center gap-2">
          <button
            @click="runBatchAction('start')"
            :disabled="batchLoading"
            class="cyber-btn flex items-center gap-1.5 text-xs text-emerald-700 border-emerald-500/40 hover:bg-emerald-50/40"
            title="Start selected containers"
          >
            <Play class="h-3 w-3 fill-current" />
            <span>Start</span>
          </button>
          <button
            @click="runBatchAction('stop')"
            :disabled="batchLoading"
            class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-amber-700 hover:border-amber-500/40 hover:bg-amber-50/40"
            title="Stop selected containers"
          >
            <Square class="h-3 w-3 fill-current" />
            <span>Stop</span>
          </button>
          <button
            @click="runBatchAction('restart')"
            :disabled="batchLoading"
            class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-cyan-700"
            title="Restart selected containers"
          >
            <RotateCcw class="h-3 w-3" :class="batchLoading ? 'animate-spin' : ''" />
            <span>Restart</span>
          </button>
          <button
            @click="runBatchCheckUpdates"
            :disabled="batchLoading"
            class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-violet-300 hover:border-violet-500/40 hover:bg-violet-950/40"
            title="Check remote registries for updates on selected containers"
          >
            <Sparkles class="h-3 w-3 text-violet-400" />
            <span>Check Updates</span>
          </button>
          <button
            @click="selectedContainerIds = {}"
            class="cyber-btn-secondary text-xs text-slate-600 hover:text-slate-800"
          >
            Clear
          </button>
        </div>
      </div>
    </transition>

    <!-- High-Density Table View -->
    <ContainersTable
      v-if="viewMode === 'table'"
      :groups="containerGroups"
      :flat-containers="filteredContainers"
      :group-by-stack="groupByStack"
      :selected-container-ids="selectedContainerIds"
      :collapsed-stacks="collapsedStacks"
      :is-filter-active="filter !== 'all' || Boolean(searchQuery.trim())"
      :action-loading="actionLoading"
      :stack-action-loading="stackActionLoading"
      :checking-single-update="checkingSingleUpdate"
      :updating-container="updatingContainer"
      @toggle-select="toggleSelectContainer"
      @toggle-select-all="toggleSelectAll"
      @toggle-select-stack="toggleSelectStack"
      @toggle-collapse="toggleStackCollapse"
      @expand-all-stacks="expandAllStacks"
      @collapse-all-stacks="collapseAllStacks"
      @action="runAction"
      @stack-action="runStackAction"
      @open-compose="(group) => group.containers.length > 0 ? openContainerModal(group.containers[0], 'compose') : openStackComposeModal(group.projectId, group.name, group.projectPath)"
      @deploy-stack="openDeployPrompt"
      @stack-history="openStackHistory"
      @delete-stack="openDeleteStackPrompt"
      @open-inspector="(c, tab) => openContainerModal(c, tab || 'overview')"
      @check-update="checkSingleUpdate"
      @confirm-update="confirmUpdate"
    />

    <!-- Cards Grid View (viewMode === 'grid') -->
    <template v-else>
      <!-- Empty state -->
      <div v-if="filteredContainers.length === 0 && !loading" class="cyber-card py-12 text-center text-slate-600">
        <Server class="mx-auto h-8 w-8 text-slate-600 mb-2" />
        <p class="text-sm font-medium text-slate-700">No containers match the current filter.</p>
        <p class="mt-1 text-xs text-slate-500">Try adjusting your search terms or status filters.</p>
      </div>

      <!-- Containers List (Grouped by Compose Stack or Flat List) -->
      <div class="space-y-6">
      <div
        v-for="group in containerGroups"
        :key="group.key"
        class="space-y-3"
      >
        <!-- Stack Header (Rendered when groupByStack is true) -->
        <div
          v-if="groupByStack"
          class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 rounded-xl border border-slate-200 bg-white/80 px-4 py-3 shadow-md transition-colors hover:border-slate-300/80"
        >
          <!-- Left: Checkbox, Toggle Chevron, Stack Title & Metrics -->
          <div class="flex items-center gap-3 flex-wrap">
            <!-- Select all in stack checkbox -->
            <input
              type="checkbox"
              :checked="group.containers.length > 0 && group.containers.every((c) => selectedContainerIds[c.id])"
              @change="toggleSelectStack(group)"
              class="h-4 w-4 rounded border-slate-300 bg-slate-50 text-cyan-500 focus:ring-cyan-500 cursor-pointer"
              title="Select all containers in this stack"
            />

            <!-- Toggle collapse button -->
            <button
              @click="toggleStackCollapse(group.key)"
              class="flex items-center gap-2 group cursor-pointer text-left focus:outline-none"
            >
              <component
                :is="collapsedStacks[group.key] ? ChevronRight : ChevronDown"
                class="h-4 w-4 text-slate-600 group-hover:text-cyan-600 transition-colors"
              />
              <div class="flex items-center gap-2">
                <component
                  :is="group.isStack ? Layers : Server"
                  class="h-4 w-4"
                  :class="group.isStack ? 'text-indigo-600' : 'text-slate-600'"
                />
                <span class="text-sm font-bold text-slate-900 font-mono group-hover:text-cyan-700 transition-colors">
                  {{ group.name }}
                </span>

              </div>
            </button>

            <!-- Stack Metrics & Badges -->
            <div class="flex flex-wrap items-center gap-1.5 text-xs">
              <!-- Running Count Badge -->
              <span
                class="rounded-full px-2 py-0.5 text-[10px] font-mono font-semibold"
                :class="group.runningCount === group.totalCount && group.totalCount > 0
                  ? 'bg-emerald-500/10 text-emerald-600 border border-emerald-500/30'
                  : group.runningCount > 0
                    ? 'bg-amber-500/10 text-amber-600 border border-amber-500/30'
                    : 'bg-slate-100 text-slate-600 border border-slate-300'"
              >
                {{ group.runningCount }}/{{ group.totalCount }} running
              </span>

              <!-- Total CPU -->
              <span
                v-if="group.runningCount > 0"
                class="rounded-full bg-slate-100/80 border border-slate-300/60 px-2 py-0.5 text-[10px] font-mono text-cyan-700"
                title="Total CPU used by this stack"
              >
                CPU: {{ group.totalCpu }}
              </span>

              <!-- Updates Badge -->
              <span
                v-if="group.hasUpdates"
                class="rounded-full bg-amber-500/15 border border-amber-500/40 px-2 py-0.5 text-[10px] font-bold text-amber-700 flex items-center gap-1 animate-pulse"
              >
                <ArrowUpCircle class="h-2.5 w-2.5 text-amber-600" /> Update
              </span>

              <!-- Public / Zero Trust Badges -->
              <span
                v-if="group.hasZeroTrust"
                class="rounded-full bg-indigo-500/15 border border-indigo-500/40 px-2 py-0.5 text-[10px] font-semibold text-indigo-700 flex items-center gap-1"
                title="Zero Trust Protected Ingress"
              >
                <ShieldCheck class="h-2.5 w-2.5" /> Zero Trust
              </span>
              <span
                v-else-if="group.hasPublic"
                class="rounded-full bg-cyan-500/15 border border-cyan-500/40 px-2 py-0.5 text-[10px] font-semibold text-cyan-700 flex items-center gap-1"
                title="Public Cloudflare Tunnel Ingress"
              >
                <Globe class="h-2.5 w-2.5" /> Public
              </span>
            </div>
          </div>

          <!-- Right: Stack Compose Actions (if projectId exists) -->
          <div class="flex flex-wrap items-center gap-1.5 sm:self-center">
            <template v-if="group.projectId">
              <button
                @click="group.containers.length > 0 ? openContainerModal(group.containers[0], 'compose') : openStackComposeModal(group.projectId, group.name, group.projectPath)"
                class="cyber-btn-secondary flex items-center gap-1 text-[11px] py-1 px-2.5 text-cyan-700 hover:border-cyan-500/50"
                title="Edit Compose YAML & environment variables"
              >
                <Code2 class="h-3 w-3" />
                <span>Compose</span>
              </button>

              <button
                @click="runStackAction(group.projectId, 'up')"
                :disabled="stackActionLoading[group.projectId]"
                class="cyber-btn flex items-center gap-1 text-[11px] py-1 px-2.5 text-emerald-700 border-emerald-500/40 hover:bg-emerald-50/40"
                title="docker compose up -d"
              >
                <Play class="h-3 w-3 fill-current" />
                <span>Up</span>
              </button>

              <button
                @click="runStackAction(group.projectId, 'restart')"
                :disabled="stackActionLoading[group.projectId]"
                class="cyber-btn-secondary flex items-center gap-1 text-[11px] py-1 px-2.5 text-cyan-700"
                title="docker compose restart"
              >
                <RotateCcw class="h-3 w-3" :class="stackActionLoading[group.projectId] ? 'animate-spin' : ''" />
                <span>Restart</span>
              </button>

              <button
                @click="openDeployPrompt(group.projectId, group.name)"
                :disabled="stackActionLoading[group.projectId]"
                class="cyber-btn-secondary flex items-center gap-1 text-[11px] py-1 px-2.5 text-indigo-700 hover:border-indigo-500/50 hover:bg-indigo-50/30"
                title="Git pull & docker compose up --build"
              >
                <Rocket class="h-3 w-3" />
                <span>Deploy</span>
              </button>

              <button
                @click="runStackAction(group.projectId, 'down')"
                :disabled="stackActionLoading[group.projectId]"
                class="cyber-btn-secondary flex items-center gap-1 text-[11px] py-1 px-2 text-rose-600 hover:border-rose-500/50 hover:bg-rose-50/30"
                title="docker compose down"
              >
                <Square class="h-3 w-3 fill-current" />
                <span>Down</span>
              </button>

              <button
                @click="openStackHistory(group.projectId, group.name)"
                class="cyber-btn-secondary text-[11px] py-1 px-2 text-slate-600 hover:text-slate-800"
                title="View stack deploy history"
              >
                <History class="h-3 w-3" />
              </button>

              <button
                v-if="group.key !== 'docktun'"
                @click="openDeleteStackPrompt(group.projectId, group.name, group.projectPath || group.containers[0]?.project_path)"
                :disabled="stackActionLoading[group.projectId]"
                class="cyber-btn-secondary flex items-center gap-1 text-[11px] py-1 px-2 text-rose-600 hover:border-rose-400 hover:bg-rose-50/60"
                title="Delete this stack"
              >
                <Trash2 class="h-3 w-3" />
                <span>Delete</span>
              </button>
            </template>

            <!-- Quick collapse button -->
            <button
              @click="toggleStackCollapse(group.key)"
              class="text-xs text-slate-600 hover:text-slate-800 px-1 py-0.5 ml-1"
              :title="collapsedStacks[group.key] ? 'Expand stack' : 'Collapse stack'"
            >
              <span class="text-[11px] font-mono">{{ collapsedStacks[group.key] ? 'Show' : 'Hide' }}</span>
            </button>
          </div>
        </div>

        <!-- Containers in this Stack / Group (Collapsible) -->
        <div
          v-show="!collapsedStacks[group.key]"
          class="space-y-3"
          :class="groupByStack ? 'pl-2 sm:pl-4 border-l-2 border-slate-200/60' : ''"
        >
          <!-- Empty Stack State (0 running or stopped containers) -->
          <div
            v-if="group.containers.length === 0"
            class="rounded-xl border border-dashed border-slate-300 bg-slate-50/70 p-4 text-center"
          >
            <p class="text-xs text-slate-700 font-mono font-medium">No active containers found in this stack.</p>
            <p class="text-[11px] text-slate-500 mt-1">The stack may be stopped or an earlier deployment may have encountered an issue.</p>
            <div class="mt-3 flex flex-wrap items-center justify-center gap-2">
              <button
                v-if="group.projectId"
                @click="runStackAction(group.projectId, 'up')"
                :disabled="stackActionLoading[group.projectId]"
                class="cyber-btn flex items-center gap-1.5 text-xs py-1 px-3 text-emerald-700 border-emerald-500/40 hover:bg-emerald-50/40"
              >
                <Play class="h-3 w-3 fill-current" />
                <span>Start Stack (up -d)</span>
              </button>
              <button
                v-if="group.projectId"
                @click="openStackHistory(group.projectId, group.name)"
                class="cyber-btn-secondary flex items-center gap-1.5 text-xs py-1 px-3 text-slate-600"
              >
                <History class="h-3 w-3" />
                <span>View Output / Logs</span>
              </button>
              <button
                v-if="group.projectId"
                @click="openStackComposeModal(group.projectId, group.name, group.projectPath)"
                class="cyber-btn-secondary flex items-center gap-1.5 text-xs py-1 px-3 text-cyan-700"
              >
                <Code2 class="h-3 w-3" />
                <span>Edit Compose</span>
              </button>
              <button
                v-if="group.projectId && group.key !== 'docktun'"
                @click="openDeleteStackPrompt(group.projectId, group.name, group.projectPath)"
                :disabled="stackActionLoading[group.projectId]"
                class="cyber-btn-secondary flex items-center gap-1.5 text-xs py-1 px-3 text-rose-600 hover:border-rose-400"
              >
                <Trash2 class="h-3 w-3" />
                <span>Delete Stack</span>
              </button>
            </div>
          </div>
          <div
            v-for="container in group.containers"
            :key="container.id"
            class="cyber-card transition-all duration-200 hover:border-slate-300"
            :class="selectedContainerIds[container.id] ? 'border-cyan-500/60 bg-cyan-50/15' : ''"
          >
            <div class="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
              <!-- Left: Checkbox + Identity & Status (Clickable to open Control Center) -->
              <div class="flex items-start gap-3 flex-1 min-w-0">
                <!-- Multi-select checkbox -->
                <input
                  type="checkbox"
                  v-model="selectedContainerIds[container.id]"
                  class="h-4 w-4 mt-1 rounded border-slate-300 bg-slate-50 text-cyan-500 focus:ring-cyan-500 cursor-pointer flex-shrink-0"
                  :title="`Select ${container.name}`"
                  @click.stop
                />

                <div
                  class="space-y-2 cursor-pointer group flex-1 min-w-0"
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
                  :class="container.running ? 'bg-emerald-500' : 'bg-slate-300'"
                ></span>
              </span>

              <span class="text-base font-bold text-slate-900 font-mono group-hover:text-cyan-600 transition-colors flex items-center gap-1.5">
                {{ container.name }}
                <Sliders class="h-3.5 w-3.5 text-slate-500 opacity-0 group-hover:opacity-100 transition-opacity" />
              </span>

              <!-- Status Badge -->
              <span
                class="rounded border px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wider"
                :class="container.running ? 'bg-emerald-500/10 text-emerald-600 border-emerald-500/30' : 'bg-slate-100 text-slate-600 border-slate-300'"
              >
                {{ container.running ? 'Running' : 'Stopped' }}
              </span>

              <!-- Stack / Project Badge -->
              <button
                v-if="container.project_id"
                @click.stop="openContainerModal(container, 'compose')"
                class="inline-flex items-center gap-1 rounded border border-cyan-500/30 bg-cyan-500/10 px-2 py-0.5 text-[10px] font-semibold text-cyan-700 font-mono hover:bg-cyan-500/20 hover:border-cyan-400 transition cursor-pointer"
                :title="`Compose project: ${container.compose_project || container.project_name}\nSource folder: ${container.compose_working_dir || container.project_path || 'Not reported by Docker'}`"
              >
                <Layers class="h-3 w-3" />
                <span>Stack: {{ container.project_name }}</span>
              </button>
              <span
                v-else-if="container.project_name"
                class="inline-flex items-center gap-1 rounded border border-cyan-500/30 bg-cyan-500/10 px-2 py-0.5 text-[10px] font-semibold text-cyan-700 font-mono"
                title="Part of compose project"
              >
                <Layers class="h-3 w-3" />
                <span>Stack: {{ container.project_name }}</span>
              </span>

              <!-- Exposure / Local Badge -->
              <span
                v-if="container.exposure_mode === 'public'"
                class="inline-flex items-center gap-1.5 rounded-full border border-cyan-500/40 bg-cyan-500/10 px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-cyan-700"
                title="Cloudflare exposure configured"
              >
                <Globe class="h-3 w-3" />
                <span>Cloudflare</span>
              </span>
              <span
                v-if="container.exposure_mode === 'public' && container.zero_trust_enabled"
                class="inline-flex items-center gap-1 rounded-full border border-indigo-500/40 bg-indigo-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-indigo-700"
                :title="`Protected with Cloudflare Zero Trust Access (${container.zero_trust_auth_type || 'email'})`"
              >
                <ShieldCheck class="h-3 w-3" />
                <span>Zero Trust ({{ container.zero_trust_auth_type === 'bypass_ip' ? 'Bypass' : container.zero_trust_auth_type === 'everyone' ? 'Everyone' : container.zero_trust_auth_type === 'domain' ? 'Domain' : container.zero_trust_auth_type === 'ip' ? 'IP' : 'OTP' }})</span>
              </span>
              <span
                v-else-if="container.exposure_mode === 'private'"
                class="inline-flex items-center gap-1.5 rounded-full border border-violet-500/40 bg-violet-500/10 px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-violet-300"
              >
                <Shield class="h-3 w-3" />
                <span>{{ container.private_type || 'Private exposure' }}</span>
              </span>
              <!-- Update Available Badge -->
              <span
                v-if="container.update_available"
                class="inline-flex items-center gap-1.5 rounded-full border border-amber-500/50 bg-amber-500/15 px-2.5 py-0.5 text-[10px] font-bold uppercase tracking-wider text-amber-700 animate-pulse shadow-sm"
                title="Newer version available on registry"
              >
                <ArrowUpCircle class="h-3 w-3 text-amber-600" />
                <span>Update Available</span>
              </span>
            </div>

            <!-- Status & Restarts -->
            <div class="flex flex-wrap items-center gap-3 text-xs text-slate-600">
              <span class="flex items-center gap-1 text-slate-600">
                <Clock class="h-3 w-3 text-slate-500" /> {{ container.uptime || container.status }}
              </span>
              <span v-if="container.restart_count" class="text-amber-600/90">
                • Restarts: {{ container.restart_count }}
              </span>
            </div>

            <!-- Local Address (Default) and Public Ingress if Exposed -->
            <div class="flex flex-wrap items-center gap-3 pt-1 text-xs">
              <div v-if="container.running" class="flex items-center gap-1.5 font-mono">
                <span class="text-slate-500 font-sans text-[11px]">Local:</span>
                <a
                  v-if="hasLocalWebLink(container.ports)"
                  :href="localAddress(container)"
                  target="_blank"
                  rel="noopener"
                  @click.stop
                  class="inline-flex items-center gap-1 text-indigo-600 hover:text-indigo-700 hover:underline font-semibold"
                >
                  <span>{{ localAddress(container) }}</span>
                  <ExternalLink class="h-3 w-3" />
                </a>
                <span v-else class="text-slate-600">{{ localAddress(container) }}</span>
                <button
                  @click.stop="copyToClipboard('mdns-' + container.id, localAddress(container))"
                  class="text-slate-400 hover:text-slate-600 ml-0.5"
                  title="Copy local address"
                >
                  <Check v-if="copiedKey === 'mdns-' + container.id" class="h-3 w-3 text-emerald-600" />
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
                  class="inline-flex items-center gap-1 text-cyan-600 hover:text-cyan-700 hover:underline font-semibold"
                >
                  <span>https://{{ container.hostname }}</span>
                  <ExternalLink class="h-3 w-3" />
                </a>
              </div>
            </div>
          </div>
        </div>

        <!-- Right: Control Actions -->
          <div class="flex flex-wrap items-center gap-2">
            <!-- Full Control Center Trigger Button -->
            <button
              @click="openContainerModal(container, 'overview')"
              class="cyber-btn flex items-center gap-1.5 text-xs text-cyan-700 border-cyan-600/50 bg-cyan-50/40 hover:bg-cyan-100/50"
              title="Open Container Control Center (Overview, Compose, .env, Files, Logs)"
            >
              <Sliders class="h-3.5 w-3.5" />
              <span>Manage</span>
            </button>

            <!-- 1-Click Update Button (Shown when update available) -->
            <button
              v-if="container.update_available"
              @click="confirmUpdate(container)"
              :disabled="!!updatingContainer[container.id]"
              class="cyber-btn flex items-center gap-1.5 text-xs text-amber-200 border-amber-500/80 bg-gradient-to-r from-amber-950/80 to-orange-950/80 hover:from-amber-900 hover:to-orange-900 shadow-md transition"
              title="Pull latest image and recreate container cleanly"
            >
              <ArrowUpCircle class="h-3.5 w-3.5 text-amber-600" :class="updatingContainer[container.id] ? 'animate-spin' : ''" />
              <span>{{ updatingContainer[container.id] ? 'Updating…' : 'Update' }}</span>
            </button>

            <button
              v-if="!container.running"
              @click="runAction(container, 'start')"
              :disabled="!!actionLoading[container.id]"
              class="cyber-btn flex items-center gap-1.5 text-xs text-emerald-700 hover:border-emerald-600 hover:bg-emerald-50/40"
            >
              <Play class="h-3.5 w-3.5 fill-current" />
              <span>{{ actionLoading[container.id] === 'start' ? 'Starting…' : 'Start' }}</span>
            </button>

            <button
              v-if="container.running"
              @click="runAction(container, 'stop')"
              :disabled="!!actionLoading[container.id]"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-amber-700 hover:border-amber-600 hover:bg-amber-50/40"
            >
              <Square class="h-3.5 w-3.5 fill-current" />
              <span>{{ actionLoading[container.id] === 'stop' ? 'Stopping…' : 'Stop' }}</span>
            </button>

            <details class="relative">
              <summary class="cyber-btn-secondary cursor-pointer text-xs">More</summary>
              <div class="absolute right-0 z-20 mt-2 flex min-w-40 flex-col gap-1 rounded-lg border border-slate-200 bg-white p-2 shadow-lg" @click="($event.currentTarget as HTMLElement).parentElement?.removeAttribute('open')">
                <button class="cyber-btn-secondary text-xs" @click="openContainerModal(container, 'access')">Exposure</button>
                <button class="cyber-btn-secondary text-xs" @click="openContainerModal(container, 'logs')">Logs</button>
                <button v-if="container.running" class="cyber-btn-secondary text-xs" @click="openContainerModal(container, 'exec')">Terminal</button>
                <button class="cyber-btn-secondary text-xs" :disabled="!!actionLoading[container.id]" @click="runAction(container, 'restart')">Restart</button>
                <button class="cyber-btn-danger text-xs" :disabled="!!actionLoading[container.id]" @click="removeContainer(container)">Delete container</button>
              </div>
            </details>
          </div>
        </div>

        <!-- Live Telemetry Row (Clickable) -->
        <div
          class="mt-4 grid grid-cols-2 gap-3 sm:grid-cols-4 rounded-lg border border-slate-200/80 bg-slate-50/60 p-3 cursor-pointer hover:border-slate-300 transition-colors"
          @click="openContainerModal(container, 'overview')"
        >
          <!-- CPU (Click to view in Metrics) -->
          <div
            class="space-y-1 hover:bg-slate-100/80 p-1 -m-1 rounded transition group/cpu"
            @click.stop="router.push(`/metrics?container=${encodeURIComponent(container.name)}`)"
            :title="`Open real-time metrics for ${container.name}`"
          >
            <div class="flex items-center justify-between text-[10px] font-semibold uppercase tracking-wider text-slate-600">
              <span class="flex items-center gap-1 group-hover/cpu:text-indigo-600 transition"><Cpu class="h-3 w-3 text-cyan-600" /> CPU &rarr;</span>
              <span class="font-mono text-slate-800 group-hover/cpu:text-indigo-600 font-bold transition">{{ container.cpu || (container.running ? '0.0%' : '—') }}</span>
            </div>
            <div class="h-1.5 w-full overflow-hidden rounded-full bg-slate-100">
              <div
                class="h-full bg-cyan-400 transition-all duration-300"
                :style="{ width: `${parseCpuPercent(container.cpu)}%` }"
              ></div>
            </div>
          </div>

          <!-- Memory -->
          <div class="space-y-1">
            <div class="flex items-center justify-between text-[10px] font-semibold uppercase tracking-wider text-slate-600">
              <span class="flex items-center gap-1"><Layers class="h-3 w-3 text-emerald-600" /> RAM</span>
              <span class="font-mono text-slate-800 truncate max-w-[110px]" :title="container.memory">
                {{ parseMemoryUsage(container.memory).used }}
              </span>
            </div>
            <div class="text-[10px] text-slate-500 truncate">
              {{ parseMemoryUsage(container.memory).total ? `Limit: ${parseMemoryUsage(container.memory).total}` : (container.running ? 'Active' : '—') }}
            </div>
          </div>

          <!-- Network I/O -->
          <div class="space-y-1">
            <div class="flex items-center justify-between text-[10px] font-semibold uppercase tracking-wider text-slate-600">
              <span class="flex items-center gap-1"><ArrowUpDown class="h-3 w-3 text-violet-400" /> Net I/O</span>
            </div>
            <div class="font-mono text-xs text-slate-800 truncate" :title="container.network">
              {{ container.network || (container.running ? '0B / 0B' : '—') }}
            </div>
          </div>

          <!-- Ports / PIDs -->
          <div class="space-y-1">
            <div class="flex items-center justify-between text-[10px] font-semibold uppercase tracking-wider text-slate-600">
              <span class="flex items-center gap-1"><HardDrive class="h-3 w-3 text-amber-600" /> Ports</span>
              <span v-if="container.pids" class="text-[10px] text-slate-600 font-mono">{{ container.pids }} pids</span>
            </div>
            <div class="font-mono text-xs text-slate-800 truncate" :title="container.ports">
              {{ container.ports || '—' }}
            </div>
          </div>
        </div>

        <!-- Inline Expose / Exposure Config Drawer -->

      </div>
        </div>
      </div>
    </div>
    </template>

    <!-- ======================================================== -->
    <!-- CONTAINER CONTROL CENTER (MODAL / FULL INSPECTOR)        -->
    <!-- ======================================================== -->
    <ContainerInspectorModal
      v-if="selectedContainer"
      :container="selectedContainer"
      v-model:active-tab="selectedTab"
      :action-loading="actionLoading"
      :stack-action-loading="stackActionLoading"
      :project-file-access="projectFileAccess"
      :project-files-writable="projectFilesWritable"
      :compose-filename="composeFilename"
      v-model:compose-content="composeContent"
      :compose-loading="composeLoading"
      :compose-saving="composeSaving"
      :compose-msg="composeMsg"
      :compose-editor="composeEditor"
      :current-project="currentProject"
      :project-git-saving="projectGitSaving"
      :project-scaffolding="projectScaffolding"
      :detail-loading="detailLoading"
      :disconnecting-network="disconnectingNetwork"
      :checking-single-update="checkingSingleUpdate"
      :updating-container="updatingContainer"
      :expose-saving="exposeSaving"
      :expose-msg="exposeMsg"
      :get-expose="getExpose"
      @close="closeContainerModal"
      @run-action="runAction"
      @run-stack-action="runStackAction"
      @deploy-stack="openDeployPrompt"
      @stack-history="openStackHistory"
      @delete-stack="openDeleteStackPrompt"
      @save-compose="saveContainerCompose"
      @save-project-git="saveProjectGit"
      @check-single-update="checkSingleUpdate"
      @confirm-update="confirmUpdate"
      @disconnect-network="disconnectContainerNetwork"
      @scaffold-project="scaffoldProject"
      @save-exposure="saveExposure"
      @refresh-containers="fetchContainers"
    />

    <!-- ============================================== -->
    <!-- CONTAINER UPDATE MODALS (CONFIRM & RESULT)    -->
    <!-- ============================================== -->
    <ContainerUpdateModal
      :confirm-container="showUpdateConfirmModal?.container"
      :result="updateResultModal"
      :update-plan="updatePlan"
      :update-plan-loading="updatePlanLoading"
      :update-plan-error="updatePlanError"
      @close-confirm="showUpdateConfirmModal = null"
      @close-result="updateResultModal = null"
      @confirm="performUpdate"
    />
    <!-- ============================================== -->
    <!-- LAUNCH MODAL (DOCKER RUN & GIT STACK LAUNCHER) -->
    <!-- ============================================== -->
    <StackLaunchModal
      v-model="showLaunchModal"
      @deployed="onStackDeployed"
      @refresh="fetchContainers"
      @close="showLaunchModal = false"
    />

    <!-- ============================================== -->
    <!-- STACK OUTPUT STREAM MODAL                      -->
    <!-- ============================================== -->
    <StackOutputModal
      v-if="stackOutputModal"
      :modal="stackOutputModal"
      :failed="stackOutputFailed"
      @close="closeStackOutput"
    />

    <!-- ============================================== -->
    <!-- DEPLOY PROMPT MODAL                            -->
    <!-- ============================================== -->
    <StackDeployModal
      v-if="showDeployPromptModal"
      :modal="showDeployPromptModal"
      v-model="deployBranchName"
      @confirm="confirmDeploy"
      @close="showDeployPromptModal = null"
    />

    <!-- ============================================== -->
    <!-- STACK HISTORY MODAL                            -->
    <!-- ============================================== -->
    <StackHistoryModal
      v-if="stackHistoryModal"
      :modal="stackHistoryModal"
      @close="stackHistoryModal = null"
    />

    <!-- ============================================== -->
    <!-- DELETE STACK CONFIRMATION MODAL               -->
    <!-- ============================================== -->
    <StackDeleteModal
      v-if="showDeleteStackModal"
      :modal="showDeleteStackModal"
      v-model:delete-down="deleteStackDown"
      v-model:remove-files="deleteStackRemoveFiles"
      :loading="deleteStackLoading"
      :folder-checking="deleteStackFolderChecking"
      :folder-available="deleteStackFolderAvailable"
      :folder-reason="deleteStackFolderReason"
      @confirm="confirmDeleteStack"
      @close="showDeleteStackModal = null"
    />
  </div>
</template>
