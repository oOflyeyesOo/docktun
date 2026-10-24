<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  HardDrive,
  Trash2,
  RotateCcw,
  Sparkles,
  Layers,
  Box,
  Database,
  Search,
  Filter,
  CheckCircle2,
  AlertCircle,
  AlertTriangle,
  Info,
  Copy,
  Check,
  X,
  ExternalLink,
  ChevronRight,
  ShieldAlert,
  Server,
  FileCode2,
  RefreshCw,
  FolderOpen,
  Folder,
  FileText,
  Upload,
  Download,
  FolderPlus,
  FilePlus,
  ArrowLeft,
  ChevronLeft,
  Save,
  Terminal,
  FileCode,
  FileArchive,
  FileImage
} from 'lucide-vue-next'
import { useApi } from '../composables/useApi'

const route = useRoute()
const router = useRouter()
const { apiFetch } = useApi()

// Primary State
const loading = ref(true)
const refreshing = ref(false)
const overview = ref<any>(null)
const volumes = ref<any[]>([])
const images = ref<any[]>([])
const containers = ref<any[]>([])
const activeTab = ref<'files' | 'volumes' | 'images' | 'containers' | 'system' | 'docktun'>('files')

// ---------------------------------------------------------------------------
// 1. FILE MANAGER STATE
// ---------------------------------------------------------------------------
const storageTargets = ref<any[]>([])
const targetsLoading = ref(false)
const selectedTarget = ref<any | null>(null)
const currentPath = ref('')
const filesList = ref<any[]>([])
const filesLoading = ref(false)
const fileSearch = ref('')

// File Editor
const activeEditorFile = ref<any | null>(null)
const editorContent = ref('')
const editorLoading = ref(false)
const editorSaving = ref(false)

// New Item Modal
const showNewItemModal = ref(false)
const newItemName = ref('')
const newItemIsDir = ref(false)
const creatingItem = ref(false)

// Upload Modal
const showUploadModal = ref(false)
const uploadFileInput = ref<HTMLInputElement | null>(null)
const selectedUploadFile = ref<File | null>(null)
const uploading = ref(false)

// ---------------------------------------------------------------------------
// 2. RESOURCE LISTS FILTER STATE
// ---------------------------------------------------------------------------
const volumeSearch = ref('')
const volumeFilter = ref<'all' | 'dangling' | 'in_use'>('all')

const imageSearch = ref('')
const imageFilter = ref<'all' | 'unused' | 'in_use' | 'dangling'>('all')

const containerSearch = ref('')
const containerFilter = ref<'all' | 'stopped' | 'running'>('all')

// Modals
const showReclaimModal = ref(false)
const reclaimLoading = ref(false)
const reclaimResults = ref<string[]>([])
const reclaimOptions = ref({
  prune_images_dangling: true,
  prune_images_all: false,
  prune_containers: true,
  prune_volumes: false,
  prune_build_cache: true
})

const selectedItemInspect = ref<any | null>(null)
const inspectType = ref<'volume' | 'image' | null>(null)

// Action loading per item
const actionLoading = ref<Record<string, boolean>>({})
const copiedKey = ref('')

// Vacuum State
const vacuumLoading = ref(false)
const vacuumMessage = ref<string | null>(null)

// Feedback message
const toastMessage = ref<{ type: 'success' | 'error' | 'info'; text: string } | null>(null)
let toastTimer: any = null

function showToast(text: string, type: 'success' | 'error' | 'info' = 'success') {
  if (toastTimer) clearTimeout(toastTimer)
  toastMessage.value = { type, text }
  toastTimer = setTimeout(() => {
    toastMessage.value = null
  }, 4000)
}

function copyText(key: string, val: string) {
  navigator.clipboard.writeText(val)
  copiedKey.value = key
  setTimeout(() => (copiedKey.value = ''), 2000)
}

// ---------------------------------------------------------------------------
// API DATA FETCHING
// ---------------------------------------------------------------------------
async function fetchOverview() {
  try {
    overview.value = await apiFetch('/api/storage/overview')
  } catch (e: any) {
    console.error('Failed to load storage overview', e)
  }
}

async function fetchVolumes() {
  try {
    volumes.value = await apiFetch<any[]>('/api/storage/volumes')
  } catch (e: any) {
    console.error('Failed to load volumes', e)
  }
}

async function fetchImages() {
  try {
    images.value = await apiFetch<any[]>('/api/storage/images')
  } catch (e: any) {
    console.error('Failed to load images', e)
  }
}

async function fetchContainers() {
  try {
    containers.value = await apiFetch<any[]>('/api/storage/containers')
  } catch (e: any) {
    console.error('Failed to load container storage', e)
  }
}

async function fetchTargets() {
  targetsLoading.value = true
  try {
    storageTargets.value = await apiFetch<any[]>('/api/storage/files/targets')
    // Select first target by default if not set
    if (!selectedTarget.value && storageTargets.value.length > 0) {
      // Prefer volume or project
      const preferred = storageTargets.value.find(t => t.type === 'volume') || storageTargets.value[0]
      await selectTarget(preferred)
    }
  } catch (e: any) {
    console.error('Failed to load storage targets', e)
  } finally {
    targetsLoading.value = false
  }
}

async function refreshAll() {
  refreshing.value = true
  await Promise.all([
    fetchOverview(),
    fetchVolumes(),
    fetchImages(),
    fetchContainers(),
    fetchTargets()
  ])
  if (selectedTarget.value) {
    await loadDirectory(currentPath.value)
  }
  setTimeout(() => (refreshing.value = false), 300)
}

// ---------------------------------------------------------------------------
// FILE MANAGER METHODS
// ---------------------------------------------------------------------------
async function selectTarget(target: any) {
  selectedTarget.value = target
  currentPath.value = ''
  await loadDirectory('')
}

async function loadDirectory(path: string) {
  if (!selectedTarget.value) return
  filesLoading.value = true
  currentPath.value = path
  try {
    const params = new URLSearchParams({
      target_type: selectedTarget.value.type,
      target_id: selectedTarget.value.id,
      path: path
    })
    filesList.value = await apiFetch<any[]>(`/api/storage/files/list?${params}`)
  } catch (e: any) {
    showToast(e.message || 'Failed to list directory', 'error')
    filesList.value = []
  } finally {
    filesLoading.value = false
  }
}

function browseTargetFromOutside(type: string, id: string) {
  activeTab.value = 'files'
  const found = storageTargets.value.find(t => t.type === type && t.id === id)
  if (found) {
    selectTarget(found)
  } else {
    // If targets not yet loaded or not found, make placeholder
    const placeholder = { type, id, name: id, description: `${type}: ${id}`, root_path: '/' }
    selectTarget(placeholder)
  }
}

function navigateDir(dir: any) {
  loadDirectory(dir.path)
}

function navigateUp() {
  if (!currentPath.value) return
  const parts = currentPath.value.split('/').filter(Boolean)
  parts.pop()
  loadDirectory(parts.join('/'))
}

const breadcrumbs = computed(() => {
  const list = [{ name: 'Root', path: '' }]
  if (!currentPath.value) return list
  const parts = currentPath.value.split('/').filter(Boolean)
  let accum = ''
  for (const part of parts) {
    accum = accum ? `${accum}/${part}` : part
    list.push({ name: part, path: accum })
  }
  return list
})

const filteredFiles = computed(() => {
  if (!fileSearch.value.trim()) return filesList.value
  const q = fileSearch.value.toLowerCase()
  return filesList.value.filter(f => f.name.toLowerCase().includes(q))
})

// File Type Icons
function getFileIcon(file: any) {
  if (file.is_dir) return Folder
  const ext = (file.extension || '').toLowerCase()
  if (['.yml', '.yaml', '.json', '.env', '.conf', '.toml', '.xml', '.ini'].includes(ext)) return FileCode2
  if (['.js', '.ts', '.vue', '.go', '.py', '.sh', '.bash', '.css', '.html', '.php', '.rs', '.c', '.cpp'].includes(ext)) return FileCode
  if (['.tar', '.gz', '.zip', '.rar', '.7z', '.bz2'].includes(ext)) return FileArchive
  if (['.png', '.jpg', '.jpeg', '.svg', '.gif', '.webp', '.ico'].includes(ext)) return FileImage
  return FileText
}

// Read / Edit File
async function openFile(file: any) {
  if (file.is_dir) {
    navigateDir(file)
    return
  }

  editorLoading.value = true
  activeEditorFile.value = file
  editorContent.value = ''
  try {
    const params = new URLSearchParams({
      target_type: selectedTarget.value.type,
      target_id: selectedTarget.value.id,
      path: file.path
    })
    const res = await apiFetch<any>(`/api/storage/files/read?${params}`)
    if (res.is_binary) {
      alert(`"${file.name}" is a binary file (${res.size_str}) and cannot be edited in the browser. You can download it instead.`)
      activeEditorFile.value = null
      return
    }
    editorContent.value = res.content || ''
  } catch (e: any) {
    showToast(e.message || 'Failed to read file', 'error')
    activeEditorFile.value = null
  } finally {
    editorLoading.value = false
  }
}

async function saveActiveFile() {
  if (!activeEditorFile.value || !selectedTarget.value) return
  editorSaving.value = true
  try {
    await apiFetch('/api/storage/files/write', {
      method: 'POST',
      body: JSON.stringify({
        target_type: selectedTarget.value.type,
        target_id: selectedTarget.value.id,
        path: activeEditorFile.value.path,
        content: editorContent.value
      })
    })
    showToast(`File "${activeEditorFile.value.name}" saved successfully`)
    activeEditorFile.value = null
    await loadDirectory(currentPath.value)
  } catch (e: any) {
    showToast(e.message || 'Failed to save file', 'error')
  } finally {
    editorSaving.value = false
  }
}

// Create Item
async function executeCreateItem() {
  if (!newItemName.value.trim() || !selectedTarget.value) return
  creatingItem.value = true
  const itemPath = currentPath.value
    ? `${currentPath.value}/${newItemName.value.trim()}`
    : newItemName.value.trim()

  try {
    await apiFetch('/api/storage/files/create', {
      method: 'POST',
      body: JSON.stringify({
        target_type: selectedTarget.value.type,
        target_id: selectedTarget.value.id,
        path: itemPath,
        is_dir: newItemIsDir.value
      })
    })
    showToast(`Created ${newItemIsDir.value ? 'directory' : 'file'} "${newItemName.value}"`)
    showNewItemModal.value = false
    newItemName.value = ''
    await loadDirectory(currentPath.value)
  } catch (e: any) {
    showToast(e.message || 'Creation failed', 'error')
  } finally {
    creatingItem.value = false
  }
}

// Delete Item
async function deleteStorageFile(file: any) {
  const confirmed = confirm(
    `Are you sure you want to permanently delete "${file.name}" from ${selectedTarget.value.name}?`
  )
  if (!confirmed) return

  try {
    const params = new URLSearchParams({
      target_type: selectedTarget.value.type,
      target_id: selectedTarget.value.id,
      path: file.path
    })
    await apiFetch(`/api/storage/files/delete?${params}`, { method: 'DELETE' })
    showToast(`Deleted ${file.name}`)
    await loadDirectory(currentPath.value)
  } catch (e: any) {
    showToast(e.message || 'Deletion failed', 'error')
  }
}

// Download File
function downloadStorageFile(file: any) {
  if (!selectedTarget.value) return
  const params = new URLSearchParams({
    target_type: selectedTarget.value.type,
    target_id: selectedTarget.value.id,
    path: file.path
  })
  window.open(`/api/storage/files/download?${params}`, '_blank')
}

// Upload File
function onFileSelected(e: Event) {
  const target = e.target as HTMLInputElement
  if (target.files && target.files.length > 0) {
    selectedUploadFile.value = target.files[0]
  }
}

async function executeUpload() {
  if (!selectedUploadFile.value || !selectedTarget.value) return
  uploading.value = true

  const formData = new FormData()
  formData.append('target_type', selectedTarget.value.type)
  formData.append('target_id', selectedTarget.value.id)
  formData.append('dir_path', currentPath.value)
  formData.append('file', selectedUploadFile.value)

  try {
    const res = await fetch('/api/storage/files/upload', {
      method: 'POST',
      body: formData,
      headers: {
        Authorization: `Bearer ${localStorage.getItem('token') || ''}`
      }
    })
    if (!res.ok) {
      const err = await res.json().catch(() => ({ message: 'Upload failed' }))
      throw new Error(err.message || 'Upload failed')
    }
    showToast(`Uploaded "${selectedUploadFile.value.name}" successfully`)
    showUploadModal.value = false
    selectedUploadFile.value = null
    await loadDirectory(currentPath.value)
  } catch (e: any) {
    showToast(e.message || 'Upload failed', 'error')
  } finally {
    uploading.value = false
  }
}

// ---------------------------------------------------------------------------
// VOLUME & IMAGE & CONTAINER PRUNE ACTIONS
// ---------------------------------------------------------------------------
async function deleteVolume(vol: any) {
  if (vol.in_use) {
    alert(`Cannot delete volume "${vol.name}" because it is currently attached to running container(s): ${vol.containers.join(', ')}`)
    return
  }
  const confirmed = confirm(
    `WARNING: This is permanent!\nAre you sure you want to permanently delete Docker volume "${vol.name}"?\nAny persistent database files or data inside this volume will be lost.`
  )
  if (!confirmed) return

  actionLoading.value[vol.name] = true
  try {
    await apiFetch(`/api/storage/volumes/${encodeURIComponent(vol.name)}`, { method: 'DELETE' })
    showToast(`Volume ${vol.name} deleted successfully`)
    await fetchVolumes()
    await fetchOverview()
    await fetchTargets()
  } catch (e: any) {
    showToast(e.message || 'Failed to delete volume', 'error')
  } finally {
    delete actionLoading.value[vol.name]
  }
}

async function pruneVolumes() {
  const confirmed = confirm(
    'CRITICAL WARNING:\nThis will permanently delete ALL dangling Docker volumes that are not attached to an active container.\n\nIf you have stopped database containers or previous test data, this data will be destroyed.\n\nProceed?'
  )
  if (!confirmed) return

  loading.value = true
  try {
    const res: any = await apiFetch('/api/storage/volumes/prune', { method: 'POST' })
    showToast(res.output || 'Dangling volumes pruned')
    await fetchVolumes()
    await fetchOverview()
    await fetchTargets()
  } catch (e: any) {
    showToast(e.message || 'Prune failed', 'error')
  } finally {
    loading.value = false
  }
}

async function deleteImage(img: any, force = false) {
  if (img.in_use && !force) {
    const tryForce = confirm(
      `Image ${img.repository}:${img.tag} is in use by container(s): ${img.containers.join(', ')}.\nDo you want to force remove it?`
    )
    if (!tryForce) return
    force = true
  } else {
    if (!confirm(`Delete image ${img.repository}:${img.tag} (${img.id.slice(0, 12)})?`)) return
  }

  actionLoading.value[img.id] = true
  try {
    await apiFetch(`/api/storage/images/${img.id}?force=${force}`, { method: 'DELETE' })
    showToast(`Image deleted`)
    await fetchImages()
    await fetchOverview()
  } catch (e: any) {
    showToast(e.message || 'Failed to delete image', 'error')
  } finally {
    delete actionLoading.value[img.id]
  }
}

async function pruneImages(all = false) {
  const msg = all
    ? 'This will remove all images that are not used by any container (including tagged images).'
    : 'This will remove dangling images (<none>:<none>).'
  if (!confirm(msg + ' Proceed?')) return

  loading.value = true
  try {
    const res: any = await apiFetch(`/api/storage/images/prune?all=${all}`, { method: 'POST' })
    showToast(res.output || 'Images pruned')
    await fetchImages()
    await fetchOverview()
  } catch (e: any) {
    showToast(e.message || 'Prune failed', 'error')
  } finally {
    loading.value = false
  }
}

async function pruneContainers() {
  if (!confirm('This will remove all stopped and exited containers. Proceed?')) return
  loading.value = true
  try {
    const res: any = await apiFetch('/api/storage/containers/prune', { method: 'POST' })
    showToast(res.output || 'Stopped containers pruned')
    await fetchContainers()
    await fetchOverview()
  } catch (e: any) {
    showToast(e.message || 'Prune failed', 'error')
  } finally {
    loading.value = false
  }
}

async function pruneBuildCache(all = false) {
  if (!confirm('Clean build cache? This is safe and only affects Docker build speed.')) return
  loading.value = true
  try {
    const res: any = await apiFetch(`/api/storage/build-cache/prune?all=${all}`, { method: 'POST' })
    showToast(res.output || 'Build cache pruned')
    await fetchOverview()
  } catch (e: any) {
    showToast(e.message || 'Prune failed', 'error')
  } finally {
    loading.value = false
  }
}

async function vacuumDatabase() {
  vacuumLoading.value = true
  vacuumMessage.value = null
  try {
    const res: any = await apiFetch('/api/storage/vacuum-db', { method: 'POST' })
    vacuumMessage.value = res.message || 'Database vacuum completed'
    showToast(res.message || 'Database vacuumed')
    await fetchOverview()
  } catch (e: any) {
    vacuumMessage.value = 'Vacuum error: ' + e.message
    showToast(e.message || 'Vacuum failed', 'error')
  } finally {
    vacuumLoading.value = false
  }
}

async function executeBatchReclaim() {
  if (reclaimOptions.value.prune_volumes) {
    const sure = confirm(
      'ATTENTION: You have "Dangling Volumes" selected in Reclaim.\nAny unmounted database or application volumes will be permanently wiped.\n\nAre you sure you want to proceed?'
    )
    if (!sure) return
  }

  reclaimLoading.value = true
  reclaimResults.value = []
  try {
    const res: any = await apiFetch('/api/storage/reclaim', {
      method: 'POST',
      body: JSON.stringify(reclaimOptions.value)
    })
    reclaimResults.value = res.outputs || []
    showToast('Reclaim operation finished!')
    await refreshAll()
  } catch (e: any) {
    reclaimResults.value = ['Error during reclaim: ' + e.message]
    showToast(e.message || 'Reclaim failed', 'error')
  } finally {
    reclaimLoading.value = false
  }
}

// Filters
const filteredVolumes = computed(() => {
  return volumes.value.filter((v) => {
    if (volumeFilter.value === 'dangling' && v.in_use) return false
    if (volumeFilter.value === 'in_use' && !v.in_use) return false
    if (!volumeSearch.value.trim()) return true
    const q = volumeSearch.value.toLowerCase()
    return (
      v.name.toLowerCase().includes(q) ||
      (v.containers && v.containers.some((c: string) => c.toLowerCase().includes(q)))
    )
  })
})

const filteredImages = computed(() => {
  return images.value.filter((img) => {
    if (imageFilter.value === 'unused' && img.in_use) return false
    if (imageFilter.value === 'in_use' && !img.in_use) return false
    if (imageFilter.value === 'dangling' && !img.is_dangling) return false
    if (!imageSearch.value.trim()) return true
    const q = imageSearch.value.toLowerCase()
    return (
      img.repository.toLowerCase().includes(q) ||
      img.tag.toLowerCase().includes(q) ||
      img.id.toLowerCase().includes(q)
    )
  })
})

const filteredContainers = computed(() => {
  return containers.value.filter((c) => {
    const isRunning = stringsEqual(c.state, 'running')
    if (containerFilter.value === 'running' && !isRunning) return false
    if (containerFilter.value === 'stopped' && isRunning) return false
    if (!containerSearch.value.trim()) return true
    const q = containerSearch.value.toLowerCase()
    return (
      c.name.toLowerCase().includes(q) ||
      c.image.toLowerCase().includes(q) ||
      c.id.toLowerCase().includes(q)
    )
  })
})

function stringsEqual(a: string, b: string) {
  return (a || '').toLowerCase() === (b || '').toLowerCase()
}

onMounted(async () => {
  loading.value = true
  await refreshAll()
  loading.value = false
})
</script>

<template>
  <div class="space-y-6">
    <!-- Toast Feedback -->
    <div
      v-if="toastMessage"
      class="toast"
      :class="toastMessage.type === 'error' ? 'toast-error' : (toastMessage.type === 'info' ? 'toast-info' : 'toast-success')"
    >
      <CheckCircle2 v-if="toastMessage.type === 'success'" class="h-4 w-4 shrink-0 text-emerald-400" />
      <AlertCircle v-else class="h-4 w-4 shrink-0 text-rose-400" />
      <span>{{ toastMessage.text }}</span>
      <button @click="toastMessage = null" class="ml-2 hover:opacity-75">
        <X class="h-3.5 w-3.5" />
      </button>
    </div>

    <!-- Header Section -->
    <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between border-b border-slate-800/80 pb-4">
      <div>
        <div class="flex items-center gap-2">
          <p class="text-[11px] font-semibold uppercase tracking-wider text-indigo-400">Host & Docker Infrastructure</p>
          <span class="rounded bg-slate-800 px-1.5 py-0.2 text-[10px] font-mono text-slate-300">File & Storage Center</span>
        </div>
        <h1 class="text-2xl font-bold tracking-tight text-white flex items-center gap-2.5 mt-1">
          <HardDrive class="h-6 w-6 text-indigo-400" />
          <span>Storage & File Manager</span>
        </h1>
      </div>

      <div class="flex items-center gap-2 flex-wrap">
        <!-- Smart Reclaim Button -->
        <button
          @click="showReclaimModal = true"
          class="cyber-btn flex items-center gap-2 bg-indigo-600 hover:bg-indigo-500 text-white font-semibold shadow-lg shadow-indigo-600/20"
        >
          <Sparkles class="h-4 w-4 text-amber-300" />
          <span>Smart Reclaim</span>
        </button>

        <button
          @click="vacuumDatabase"
          :disabled="vacuumLoading"
          class="cyber-btn-secondary text-xs flex items-center gap-1.5"
          title="Vacuum SQLite Database to reclaim free pages"
        >
          <Database class="h-3.5 w-3.5 text-cyan-400" :class="vacuumLoading ? 'animate-spin' : ''" />
          <span>{{ vacuumLoading ? 'Vacuuming…' : 'Vacuum DB' }}</span>
        </button>

        <button
          @click="refreshAll"
          :disabled="refreshing"
          class="cyber-btn-secondary text-xs flex items-center gap-1.5"
          title="Refresh storage statistics"
        >
          <RotateCcw class="h-3.5 w-3.5" :class="refreshing ? 'animate-spin' : ''" />
          <span>Refresh</span>
        </button>
      </div>
    </div>

    <!-- Reclaimable Space Hero Banner -->
    <div class="cyber-card bg-slate-900/80 border-slate-800/90 relative overflow-hidden">
      <div class="absolute -right-20 -top-20 w-64 h-64 bg-indigo-500/10 rounded-full blur-3xl pointer-events-none"></div>

      <div class="grid grid-cols-1 lg:grid-cols-12 gap-5 items-center">
        <!-- Reclaimable Metric & Breakdown -->
        <div class="lg:col-span-7 space-y-2.5">
          <div class="flex items-center gap-2">
            <span class="text-[10px] font-bold font-mono tracking-widest uppercase text-amber-400/90">
              RESOURCES &bull; RECLAIMABLE SPACE
            </span>
          </div>

          <div class="flex items-baseline gap-3">
            <span class="text-3xl sm:text-4xl font-extrabold font-mono text-amber-300 tracking-tight">
              {{ overview?.reclaimable_str || '0 B' }}
            </span>
            <span class="text-xs text-slate-400 font-medium">potential recovery</span>
          </div>

          <!-- Breakdown Tags -->
          <div class="flex flex-wrap items-center gap-2 pt-1 text-xs font-mono">
            <span class="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-slate-950 border border-slate-800 text-slate-300">
              <Layers class="h-3 w-3 text-cyan-400" />
              <span>{{ overview?.counts?.unused_images || 0 }} unused images</span>
            </span>

            <span class="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-slate-950 border border-slate-800 text-slate-300">
              <Box class="h-3 w-3 text-indigo-400" />
              <span>{{ overview?.counts?.stopped_containers || 0 }} stopped containers</span>
            </span>

            <span class="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-slate-950 border border-slate-800 text-slate-300">
              <HardDrive class="h-3 w-3 text-amber-400" />
              <span>{{ overview?.counts?.dangling_volumes || 0 }} dangling volumes</span>
            </span>

            <span v-if="overview?.counts?.build_cache_count" class="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-slate-950 border border-slate-800 text-slate-300">
              <Sparkles class="h-3 w-3 text-purple-400" />
              <span>{{ overview?.counts?.build_cache_count }} build cache objects</span>
            </span>
          </div>
        </div>

        <!-- Host Disk Space Gauge -->
        <div class="lg:col-span-5 rounded-xl bg-slate-950/70 border border-slate-800/90 p-4 space-y-2.5">
          <div class="flex items-center justify-between text-xs">
            <span class="text-slate-400 font-medium">Host Filesystem Disk</span>
            <span class="font-mono text-slate-200 font-bold">
              {{ overview?.host_disk?.used_str || '—' }} / {{ overview?.host_disk?.total_str || '—' }}
            </span>
          </div>

          <!-- Bar -->
          <div class="h-2 w-full overflow-hidden rounded-full bg-slate-800">
            <div
              class="h-full rounded-full transition-all duration-300"
              :class="
                (overview?.host_disk?.percent || 0) > 85
                  ? 'bg-rose-500'
                  : (overview?.host_disk?.percent || 0) > 70
                    ? 'bg-amber-500'
                    : 'bg-emerald-500'
              "
              :style="{ width: `${Math.min(overview?.host_disk?.percent || 0, 100)}%` }"
            ></div>
          </div>

          <div class="flex items-center justify-between text-[11px] font-mono text-slate-400">
            <span>Free: <strong class="text-emerald-400">{{ overview?.host_disk?.free_str || '—' }}</strong></span>
            <span>Usage: <strong class="text-slate-200">{{ (overview?.host_disk?.percent || 0).toFixed(1) }}%</strong></span>
          </div>
        </div>
      </div>
    </div>

    <!-- Navigation Tabs -->
    <div class="flex items-center justify-between border-b border-slate-800 pb-2 flex-wrap gap-2">
      <div class="flex items-center gap-1 rounded-xl bg-slate-900/80 p-1 border border-slate-800 text-xs flex-wrap">
        <!-- 1. File Explorer Tab (Primary) -->
        <button
          @click="activeTab = 'files'"
          class="flex items-center gap-1.5 px-3.5 py-1.5 rounded-lg font-semibold transition"
          :class="activeTab === 'files' ? 'bg-indigo-600 text-white shadow-sm ring-1 ring-indigo-500/40' : 'text-slate-400 hover:text-slate-200'"
        >
          <FolderOpen class="h-3.5 w-3.5 text-cyan-300" />
          <span>File Explorer</span>
          <span class="rounded bg-indigo-950/80 px-1.5 py-0.2 text-[10px] font-mono text-cyan-300">Live</span>
        </button>

        <!-- 2. Volumes Tab -->
        <button
          @click="activeTab = 'volumes'"
          class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg font-medium transition"
          :class="activeTab === 'volumes' ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-400 hover:text-slate-200'"
        >
          <HardDrive class="h-3.5 w-3.5 text-amber-400" />
          <span>Volumes</span>
          <span class="rounded bg-slate-950 px-1.5 py-0.2 text-[10px] font-mono">{{ volumes.length }}</span>
        </button>

        <!-- 3. Images Tab -->
        <button
          @click="activeTab = 'images'"
          class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg font-medium transition"
          :class="activeTab === 'images' ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-400 hover:text-slate-200'"
        >
          <Layers class="h-3.5 w-3.5 text-cyan-400" />
          <span>Images</span>
          <span class="rounded bg-slate-950 px-1.5 py-0.2 text-[10px] font-mono">{{ images.length }}</span>
        </button>

        <!-- 4. Containers Tab -->
        <button
          @click="activeTab = 'containers'"
          class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg font-medium transition"
          :class="activeTab === 'containers' ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-400 hover:text-slate-200'"
        >
          <Box class="h-3.5 w-3.5 text-indigo-400" />
          <span>Containers</span>
          <span class="rounded bg-slate-950 px-1.5 py-0.2 text-[10px] font-mono">{{ containers.length }}</span>
        </button>

        <!-- 5. Build Cache Tab -->
        <button
          @click="activeTab = 'system'"
          class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg font-medium transition"
          :class="activeTab === 'system' ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-400 hover:text-slate-200'"
        >
          <Sparkles class="h-3.5 w-3.5 text-purple-400" />
          <span>Build Cache & DF</span>
        </button>

        <!-- 6. DockTun App DB -->
        <button
          @click="activeTab = 'docktun'"
          class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg font-medium transition"
          :class="activeTab === 'docktun' ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-400 hover:text-slate-200'"
        >
          <Database class="h-3.5 w-3.5 text-emerald-400" />
          <span>DockTun DB</span>
        </button>
      </div>
    </div>

    <!-- ===================================================================== -->
    <!-- TAB 1: FULL FEATURED FILE EXPLORER                                    -->
    <!-- ===================================================================== -->
    <div v-if="activeTab === 'files'" class="space-y-4">
      <div class="grid grid-cols-1 lg:grid-cols-12 gap-4 items-start">
        <!-- Left Column: Storage Target Picker (Volumes, Containers, Projects, Host) -->
        <div class="lg:col-span-4 space-y-3">
          <div class="cyber-card !p-3.5 space-y-3">
            <div class="flex items-center justify-between border-b border-slate-800 pb-2">
              <span class="text-xs font-bold text-white uppercase tracking-wider flex items-center gap-1.5">
                <HardDrive class="h-3.5 w-3.5 text-indigo-400" />
                <span>Storage Target</span>
              </span>
              <span class="text-[10px] text-slate-400 font-mono">{{ storageTargets.length }} targets</span>
            </div>

            <!-- Target Selector List -->
            <div class="space-y-1 max-h-[580px] overflow-y-auto pr-1">
              <!-- Group 1: Docker Volumes -->
              <div class="pt-1 pb-1">
                <span class="text-[10px] font-bold font-mono uppercase tracking-wider text-amber-400 px-2 block mb-1">
                  Docker Volumes
                </span>
                <div
                  v-for="t in storageTargets.filter(t => t.type === 'volume')"
                  :key="t.id"
                  @click="selectTarget(t)"
                  class="flex items-center justify-between px-2.5 py-2 rounded-lg cursor-pointer transition text-xs font-mono group"
                  :class="selectedTarget?.type === t.type && selectedTarget?.id === t.id ? 'bg-indigo-600 text-white shadow-sm' : 'hover:bg-slate-800/80 text-slate-300'"
                >
                  <div class="truncate pr-2">
                    <div class="font-bold truncate flex items-center gap-1.5">
                      <HardDrive class="h-3 w-3 shrink-0" :class="selectedTarget?.id === t.id ? 'text-white' : 'text-amber-400'" />
                      <span class="truncate">{{ t.name }}</span>
                    </div>
                    <span class="text-[10px] opacity-75 truncate block">{{ t.description }}</span>
                  </div>
                  <ChevronRight class="h-3.5 w-3.5 shrink-0 opacity-50 group-hover:opacity-100" />
                </div>
              </div>

              <!-- Group 2: Docker Containers -->
              <div class="pt-2 pb-1 border-t border-slate-800/60">
                <span class="text-[10px] font-bold font-mono uppercase tracking-wider text-cyan-400 px-2 block mb-1">
                  Containers Filesystem
                </span>
                <div
                  v-for="t in storageTargets.filter(t => t.type === 'container')"
                  :key="t.id"
                  @click="selectTarget(t)"
                  class="flex items-center justify-between px-2.5 py-2 rounded-lg cursor-pointer transition text-xs font-mono group"
                  :class="selectedTarget?.type === t.type && selectedTarget?.id === t.id ? 'bg-indigo-600 text-white shadow-sm' : 'hover:bg-slate-800/80 text-slate-300'"
                >
                  <div class="truncate pr-2">
                    <div class="font-bold truncate flex items-center gap-1.5">
                      <Box class="h-3 w-3 shrink-0" :class="selectedTarget?.id === t.id ? 'text-white' : 'text-cyan-400'" />
                      <span class="truncate">{{ t.name }}</span>
                    </div>
                    <span class="text-[10px] opacity-75 truncate block">{{ t.description }}</span>
                  </div>
                  <ChevronRight class="h-3.5 w-3.5 shrink-0 opacity-50 group-hover:opacity-100" />
                </div>
              </div>

              <!-- Group 3: Host & Projects -->
              <div class="pt-2 pb-1 border-t border-slate-800/60">
                <span class="text-[10px] font-bold font-mono uppercase tracking-wider text-emerald-400 px-2 block mb-1">
                  Host & Project Dirs
                </span>
                <div
                  v-for="t in storageTargets.filter(t => t.type === 'docktun' || t.type === 'project')"
                  :key="t.id"
                  @click="selectTarget(t)"
                  class="flex items-center justify-between px-2.5 py-2 rounded-lg cursor-pointer transition text-xs font-mono group"
                  :class="selectedTarget?.type === t.type && selectedTarget?.id === t.id ? 'bg-indigo-600 text-white shadow-sm' : 'hover:bg-slate-800/80 text-slate-300'"
                >
                  <div class="truncate pr-2">
                    <div class="font-bold truncate flex items-center gap-1.5">
                      <Folder class="h-3 w-3 shrink-0" :class="selectedTarget?.id === t.id ? 'text-white' : 'text-emerald-400'" />
                      <span class="truncate">{{ t.name }}</span>
                    </div>
                    <span class="text-[10px] opacity-75 truncate block">{{ t.description }}</span>
                  </div>
                  <ChevronRight class="h-3.5 w-3.5 shrink-0 opacity-50 group-hover:opacity-100" />
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- Right Column: Interactive File Browser & Breadcrumbs -->
        <div class="lg:col-span-8 space-y-3">
          <div class="cyber-card !p-4 space-y-3 bg-slate-950">
            <!-- Active Target Info & Action Toolbar -->
            <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-800 pb-3">
              <div>
                <div class="flex items-center gap-2">
                  <span
                    class="rounded px-1.5 py-0.2 text-[10px] font-mono font-bold uppercase"
                    :class="selectedTarget?.type === 'volume' ? 'bg-amber-500/10 text-amber-300 border border-amber-500/30' : (selectedTarget?.type === 'container' ? 'bg-cyan-500/10 text-cyan-300 border border-cyan-500/30' : 'bg-emerald-500/10 text-emerald-300 border border-emerald-500/30')"
                  >
                    {{ selectedTarget?.type || 'Target' }}
                  </span>
                  <h2 class="text-sm font-bold text-white font-mono truncate max-w-sm" :title="selectedTarget?.name">
                    {{ selectedTarget?.name || 'Select a Storage Target' }}
                  </h2>
                </div>
                <p class="text-[11px] text-slate-400 font-mono mt-0.5 truncate">{{ selectedTarget?.description }}</p>
              </div>

              <!-- File Action Buttons -->
              <div class="flex items-center gap-2 flex-wrap">
                <button
                  @click="newItemIsDir = false; newItemName = ''; showNewItemModal = true"
                  class="cyber-btn-secondary !py-1.5 !px-2.5 text-xs flex items-center gap-1.5 text-slate-200"
                  title="Create new file in current directory"
                >
                  <FilePlus class="h-3.5 w-3.5 text-indigo-400" />
                  <span>New File</span>
                </button>

                <button
                  @click="newItemIsDir = true; newItemName = ''; showNewItemModal = true"
                  class="cyber-btn-secondary !py-1.5 !px-2.5 text-xs flex items-center gap-1.5 text-slate-200"
                  title="Create new folder in current directory"
                >
                  <FolderPlus class="h-3.5 w-3.5 text-amber-400" />
                  <span>New Folder</span>
                </button>

                <button
                  @click="showUploadModal = true"
                  class="cyber-btn-secondary !py-1.5 !px-2.5 text-xs flex items-center gap-1.5 text-cyan-300"
                  title="Upload a file to this directory"
                >
                  <Upload class="h-3.5 w-3.5" />
                  <span>Upload</span>
                </button>

                <button
                  @click="loadDirectory(currentPath)"
                  :disabled="filesLoading"
                  class="cyber-btn-secondary !py-1.5 !px-2 text-xs"
                  title="Refresh directory listing"
                >
                  <RotateCcw class="h-3.5 w-3.5" :class="filesLoading ? 'animate-spin' : ''" />
                </button>
              </div>
            </div>

            <!-- Path Breadcrumb Bar & Quick Search -->
            <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-2 bg-slate-900/60 p-2.5 rounded-lg border border-slate-800 text-xs font-mono">
              <!-- Breadcrumbs -->
              <div class="flex items-center gap-1.5 flex-wrap min-w-0">
                <button
                  @click="navigateUp"
                  :disabled="!currentPath"
                  class="p-1 rounded bg-slate-800 hover:bg-slate-700 text-slate-300 disabled:opacity-30 disabled:cursor-not-allowed"
                  title="Go up one level"
                >
                  <ChevronLeft class="h-3.5 w-3.5" />
                </button>

                <div class="flex items-center gap-1 flex-wrap">
                  <span
                    v-for="(crumb, idx) in breadcrumbs"
                    :key="crumb.path"
                    class="flex items-center gap-1"
                  >
                    <button
                      @click="loadDirectory(crumb.path)"
                      class="hover:text-indigo-400 transition"
                      :class="idx === breadcrumbs.length - 1 ? 'text-white font-bold' : 'text-slate-400'"
                    >
                      {{ crumb.name }}
                    </button>
                    <span v-if="idx < breadcrumbs.length - 1" class="text-slate-600">/</span>
                  </span>
                </div>
              </div>

              <!-- Filter Search Input -->
              <div class="relative w-40 sm:w-48 shrink-0">
                <Search class="h-3.5 w-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-slate-500" />
                <input
                  v-model="fileSearch"
                  type="text"
                  placeholder="Filter files…"
                  class="w-full pl-8 pr-2.5 py-1 text-xs bg-slate-950 border border-slate-800 rounded-lg text-slate-200 placeholder:text-slate-500 font-mono"
                />
              </div>
            </div>

            <!-- File Explorer Table -->
            <div class="overflow-x-auto border border-slate-800 rounded-lg bg-slate-950 min-h-[380px]">
              <table class="w-full text-xs font-mono">
                <thead>
                  <tr class="border-b border-slate-800 bg-slate-900/80 text-left text-slate-400">
                    <th class="px-4 py-2.5 font-semibold uppercase tracking-wider">Name</th>
                    <th class="px-4 py-2.5 font-semibold uppercase tracking-wider">Size</th>
                    <th class="px-4 py-2.5 font-semibold uppercase tracking-wider">Modified</th>
                    <th class="px-4 py-2.5 font-semibold uppercase tracking-wider text-right">Actions</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-slate-800/60">
                  <!-- Up Dir Row -->
                  <tr
                    v-if="currentPath"
                    @click="navigateUp"
                    class="hover:bg-slate-900/60 cursor-pointer transition text-indigo-300"
                  >
                    <td colspan="4" class="px-4 py-2 flex items-center gap-2 font-bold">
                      <Folder class="h-4 w-4 text-amber-400" />
                      <span>.. (Up one level)</span>
                    </td>
                  </tr>

                  <!-- File / Folder Rows -->
                  <tr
                    v-for="file in filteredFiles"
                    :key="file.path"
                    class="hover:bg-slate-900/50 transition-colors group"
                  >
                    <!-- Name & Icon -->
                    <td class="px-4 py-2.5">
                      <div
                        @click="openFile(file)"
                        class="flex items-center gap-2 cursor-pointer font-medium hover:text-indigo-400 max-w-sm truncate"
                        :class="file.is_dir ? 'text-white font-bold' : 'text-slate-200'"
                      >
                        <component
                          :is="getFileIcon(file)"
                          class="h-4 w-4 shrink-0"
                          :class="file.is_dir ? 'text-amber-400 fill-amber-400/20' : 'text-slate-400'"
                        />
                        <span class="truncate">{{ file.name }}</span>
                      </div>
                    </td>

                    <!-- Size -->
                    <td class="px-4 py-2.5 whitespace-nowrap text-slate-400">
                      {{ file.is_dir ? '—' : file.size_str }}
                    </td>

                    <!-- Modified Date -->
                    <td class="px-4 py-2.5 whitespace-nowrap text-slate-400 text-[11px]">
                      {{ file.mod_time || '—' }}
                    </td>

                    <!-- Actions -->
                    <td class="px-4 py-2.5 text-right whitespace-nowrap">
                      <div class="flex items-center justify-end gap-1">
                        <button
                          v-if="!file.is_dir"
                          @click="openFile(file)"
                          class="cyber-btn-secondary !px-2 !py-0.5 !text-[11px] text-cyan-300"
                          title="View / Edit file"
                        >
                          Edit
                        </button>

                        <button
                          v-if="!file.is_dir"
                          @click="downloadStorageFile(file)"
                          class="p-1 rounded text-slate-400 hover:text-indigo-300 hover:bg-slate-800"
                          title="Download file"
                        >
                          <Download class="h-3.5 w-3.5" />
                        </button>

                        <button
                          @click="deleteStorageFile(file)"
                          class="p-1 rounded text-slate-400 hover:text-rose-400 hover:bg-rose-500/10"
                          title="Delete file or directory"
                        >
                          <Trash2 class="h-3.5 w-3.5" />
                        </button>
                      </div>
                    </td>
                  </tr>

                  <!-- Empty directory state -->
                  <tr v-if="filteredFiles.length === 0 && !filesLoading">
                    <td colspan="4" class="px-4 py-12 text-center text-slate-500">
                      {{ fileSearch ? 'No files match your search filter.' : 'This directory is empty.' }}
                    </td>
                  </tr>

                  <!-- Loading state -->
                  <tr v-if="filesLoading">
                    <td colspan="4" class="px-4 py-12 text-center text-slate-400">
                      <RotateCcw class="h-5 w-5 animate-spin mx-auto mb-2 text-indigo-400" />
                      <span>Browsing storage...</span>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- ===================================================================== -->
    <!-- TAB 2: VOLUMES                                                        -->
    <!-- ===================================================================== -->
    <div v-if="activeTab === 'volumes'" class="space-y-4">
      <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 bg-slate-900/60 border border-slate-800 rounded-xl p-3 text-xs">
        <div class="flex flex-wrap items-center gap-2">
          <div class="relative w-48 sm:w-64">
            <Search class="h-3.5 w-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-slate-500" />
            <input
              v-model="volumeSearch"
              type="text"
              placeholder="Search volume name or container…"
              class="w-full pl-8 pr-2.5 py-1.5 text-xs bg-slate-950 border border-slate-800 rounded-lg text-slate-200 placeholder:text-slate-500 font-mono"
            />
          </div>

          <select
            v-model="volumeFilter"
            class="text-xs bg-slate-950 border border-slate-800 rounded-lg px-3 py-1.5 text-slate-300 font-mono"
          >
            <option value="all">All Volumes ({{ volumes.length }})</option>
            <option value="dangling">Dangling / Unused ({{ overview?.counts?.dangling_volumes || 0 }})</option>
            <option value="in_use">In-Use by Running Containers</option>
          </select>
        </div>

        <div class="flex items-center gap-2">
          <button
            @click="pruneVolumes"
            class="cyber-btn-danger text-xs flex items-center gap-1.5 py-1.5 px-3"
            title="Remove all dangling volumes that are not attached to active containers"
          >
            <Trash2 class="h-3.5 w-3.5" />
            <span>Prune Dangling Volumes</span>
          </button>
        </div>
      </div>

      <!-- Volume Table -->
      <div class="cyber-card !p-0 overflow-x-auto border border-slate-800 bg-slate-950">
        <table class="w-full text-xs font-mono">
          <thead>
            <tr class="border-b border-slate-800 bg-slate-900/80 text-left text-slate-400">
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Volume Name</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Status</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Size</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Driver / Scope</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Attached Containers</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider text-right">Actions</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-800/60">
            <tr
              v-for="v in filteredVolumes"
              :key="v.name"
              class="hover:bg-slate-900/50 transition-colors"
            >
              <td class="px-4 py-3 text-slate-200 max-w-xs truncate" :title="v.name">
                <div class="flex items-center gap-2">
                  <span
                    v-if="v.is_anonymous"
                    class="px-1.5 py-0.2 rounded bg-amber-500/10 text-amber-400 border border-amber-500/30 text-[9px] uppercase shrink-0"
                    title="Anonymous hash volume"
                  >
                    Anon Hash
                  </span>
                  <span class="font-bold text-white truncate">{{ v.name }}</span>
                </div>
              </td>

              <td class="px-4 py-3 whitespace-nowrap">
                <span
                  class="rounded-full px-2 py-0.5 text-[10px] font-semibold"
                  :class="v.in_use ? 'bg-emerald-500/10 text-emerald-400 border border-emerald-500/30' : 'bg-amber-500/10 text-amber-300 border border-amber-500/30'"
                >
                  {{ v.in_use ? 'In-Use' : 'Dangling (Unused)' }}
                </span>
              </td>

              <td class="px-4 py-3 whitespace-nowrap text-slate-300 font-bold">
                {{ v.size || 'N/A' }}
              </td>

              <td class="px-4 py-3 whitespace-nowrap text-slate-400 text-[11px]">
                {{ v.driver }} &bull; {{ v.scope }}
              </td>

              <td class="px-4 py-3 text-slate-300">
                <div v-if="v.containers && v.containers.length" class="flex flex-wrap gap-1">
                  <router-link
                    v-for="c in v.containers"
                    :key="c"
                    :to="'/containers?name=' + encodeURIComponent(c)"
                    class="rounded bg-slate-900 border border-slate-800 px-1.5 py-0.2 text-[10px] text-indigo-300 hover:text-white"
                  >
                    {{ c }}
                  </router-link>
                </div>
                <span v-else class="text-slate-500 text-[11px]">—</span>
              </td>

              <td class="px-4 py-3 text-right whitespace-nowrap">
                <div class="flex items-center justify-end gap-1.5">
                  <!-- Browse Files Button (Direct link to File Explorer!) -->
                  <button
                    @click="browseTargetFromOutside('volume', v.name)"
                    class="cyber-btn-secondary !px-2.5 !py-1 text-[11px] text-cyan-300 flex items-center gap-1"
                    title="Browse and edit files inside this Docker volume"
                  >
                    <FolderOpen class="h-3 w-3" />
                    <span>Browse Files</span>
                  </button>

                  <button
                    @click="selectedItemInspect = v; inspectType = 'volume'"
                    class="cyber-btn-secondary !px-2 !py-1 text-[11px] text-slate-300"
                    title="Inspect mountpoint"
                  >
                    Inspect
                  </button>

                  <button
                    @click="deleteVolume(v)"
                    :disabled="v.in_use || !!actionLoading[v.name]"
                    class="cyber-btn-danger !px-2 !py-1 text-[11px]"
                    title="Delete volume permanently"
                  >
                    <Trash2 class="h-3 w-3" />
                  </button>
                </div>
              </td>
            </tr>

            <tr v-if="filteredVolumes.length === 0">
              <td colspan="6" class="px-4 py-12 text-center text-slate-500">
                No Docker volumes match the current criteria.
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- ===================================================================== -->
    <!-- TAB 3: IMAGES                                                         -->
    <!-- ===================================================================== -->
    <div v-if="activeTab === 'images'" class="space-y-4">
      <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 bg-slate-900/60 border border-slate-800 rounded-xl p-3 text-xs">
        <div class="flex flex-wrap items-center gap-2">
          <div class="relative w-48 sm:w-64">
            <Search class="h-3.5 w-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-slate-500" />
            <input
              v-model="imageSearch"
              type="text"
              placeholder="Search repository, tag, or ID…"
              class="w-full pl-8 pr-2.5 py-1.5 text-xs bg-slate-950 border border-slate-800 rounded-lg text-slate-200 placeholder:text-slate-500 font-mono"
            />
          </div>

          <select
            v-model="imageFilter"
            class="text-xs bg-slate-950 border border-slate-800 rounded-lg px-3 py-1.5 text-slate-300 font-mono"
          >
            <option value="all">All Images ({{ images.length }})</option>
            <option value="unused">Unused Images ({{ overview?.counts?.unused_images || 0 }})</option>
            <option value="dangling">Dangling &lt;none&gt; ({{ overview?.counts?.dangling_images || 0 }})</option>
            <option value="in_use">In-Use Images</option>
          </select>
        </div>

        <div class="flex items-center gap-2">
          <button
            @click="pruneImages(false)"
            class="cyber-btn-secondary text-xs flex items-center gap-1.5 py-1.5 px-3"
            title="Remove dangling images (<none>:<none>)"
          >
            <Trash2 class="h-3.5 w-3.5 text-amber-400" />
            <span>Prune Dangling</span>
          </button>

          <button
            @click="pruneImages(true)"
            class="cyber-btn-danger text-xs flex items-center gap-1.5 py-1.5 px-3"
            title="Remove all unused images"
          >
            <Trash2 class="h-3.5 w-3.5" />
            <span>Prune All Unused</span>
          </button>
        </div>
      </div>

      <!-- Images Table -->
      <div class="cyber-card !p-0 overflow-x-auto border border-slate-800 bg-slate-950">
        <table class="w-full text-xs font-mono">
          <thead>
            <tr class="border-b border-slate-800 bg-slate-900/80 text-left text-slate-400">
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Repository:Tag</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Image ID</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Size</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Created</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Status</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">In-Use By Containers</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider text-right">Actions</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-800/60">
            <tr
              v-for="img in filteredImages"
              :key="img.id + img.repository"
              class="hover:bg-slate-900/50 transition-colors"
            >
              <td class="px-4 py-3 text-slate-200">
                <div class="flex items-center gap-2">
                  <span
                    v-if="img.is_dangling"
                    class="px-1.5 py-0.2 rounded bg-amber-500/10 text-amber-400 border border-amber-500/30 text-[9px] uppercase shrink-0"
                  >
                    Dangling
                  </span>
                  <span class="font-bold text-white truncate max-w-xs" :title="`${img.repository}:${img.tag}`">
                    {{ img.repository }}:<span class="text-cyan-400">{{ img.tag }}</span>
                  </span>
                </div>
              </td>

              <td class="px-4 py-3 whitespace-nowrap text-slate-400 text-[11px]">
                {{ img.id.slice(0, 12) }}
              </td>

              <td class="px-4 py-3 whitespace-nowrap text-slate-300 font-bold">
                {{ img.size }}
              </td>

              <td class="px-4 py-3 whitespace-nowrap text-slate-400 text-[11px]">
                {{ img.created_since || img.created_at }}
              </td>

              <td class="px-4 py-3 whitespace-nowrap">
                <span
                  class="rounded-full px-2 py-0.5 text-[10px] font-semibold"
                  :class="img.in_use ? 'bg-emerald-500/10 text-emerald-400 border border-emerald-500/30' : 'bg-slate-800 text-slate-400 border border-slate-700'"
                >
                  {{ img.in_use ? 'In-Use' : 'Unused' }}
                </span>
              </td>

              <td class="px-4 py-3 text-slate-300">
                <div v-if="img.containers && img.containers.length" class="flex flex-wrap gap-1">
                  <router-link
                    v-for="c in img.containers"
                    :key="c"
                    :to="'/containers?name=' + encodeURIComponent(c)"
                    class="rounded bg-slate-900 border border-slate-800 px-1.5 py-0.2 text-[10px] text-indigo-300 hover:text-white"
                  >
                    {{ c }}
                  </router-link>
                </div>
                <span v-else class="text-slate-500 text-[11px]">—</span>
              </td>

              <td class="px-4 py-3 text-right whitespace-nowrap">
                <div class="flex items-center justify-end gap-1.5">
                  <button
                    @click="copyText(img.id, img.id)"
                    class="p-1.5 rounded-lg text-slate-400 hover:bg-slate-800 hover:text-slate-200"
                    title="Copy Image ID"
                  >
                    <Check v-if="copiedKey === img.id" class="h-3.5 w-3.5 text-emerald-400" />
                    <Copy v-else class="h-3.5 w-3.5" />
                  </button>

                  <button
                    @click="deleteImage(img, false)"
                    :disabled="!!actionLoading[img.id]"
                    class="cyber-btn-danger !px-2 !py-1 text-[11px]"
                    title="Delete image"
                  >
                    <Trash2 class="h-3 w-3" />
                  </button>
                </div>
              </td>
            </tr>

            <tr v-if="filteredImages.length === 0">
              <td colspan="7" class="px-4 py-12 text-center text-slate-500">
                No Docker images match the current criteria.
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- ===================================================================== -->
    <!-- TAB 4: CONTAINERS STORAGE                                             -->
    <!-- ===================================================================== -->
    <div v-if="activeTab === 'containers'" class="space-y-4">
      <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 bg-slate-900/60 border border-slate-800 rounded-xl p-3 text-xs">
        <div class="flex flex-wrap items-center gap-2">
          <div class="relative w-48 sm:w-64">
            <Search class="h-3.5 w-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-slate-500" />
            <input
              v-model="containerSearch"
              type="text"
              placeholder="Search container or image…"
              class="w-full pl-8 pr-2.5 py-1.5 text-xs bg-slate-950 border border-slate-800 rounded-lg text-slate-200 placeholder:text-slate-500 font-mono"
            />
          </div>

          <select
            v-model="containerFilter"
            class="text-xs bg-slate-950 border border-slate-800 rounded-lg px-3 py-1.5 text-slate-300 font-mono"
          >
            <option value="all">All Containers ({{ containers.length }})</option>
            <option value="stopped">Stopped / Exited Only ({{ overview?.counts?.stopped_containers || 0 }})</option>
            <option value="running">Running Only</option>
          </select>
        </div>

        <button
          @click="pruneContainers"
          class="cyber-btn-danger text-xs flex items-center gap-1.5 py-1.5 px-3"
          title="Remove all stopped containers"
        >
          <Trash2 class="h-3.5 w-3.5" />
          <span>Prune Stopped Containers</span>
        </button>
      </div>

      <!-- Containers Table -->
      <div class="cyber-card !p-0 overflow-x-auto border border-slate-800 bg-slate-950">
        <table class="w-full text-xs font-mono">
          <thead>
            <tr class="border-b border-slate-800 bg-slate-900/80 text-left text-slate-400">
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Container Name</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Image</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">State</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Writable Layer (RW)</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Virtual RootFS</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider text-right">Actions</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-800/60">
            <tr
              v-for="c in filteredContainers"
              :key="c.id"
              class="hover:bg-slate-900/50 transition-colors"
            >
              <td class="px-4 py-3 text-slate-200">
                <router-link
                  :to="'/containers?id=' + encodeURIComponent(c.id)"
                  class="font-bold text-white hover:text-indigo-400 hover:underline"
                >
                  {{ c.name }}
                </router-link>
              </td>

              <td class="px-4 py-3 text-slate-400 max-w-xs truncate" :title="c.image">
                {{ c.image }}
              </td>

              <td class="px-4 py-3 whitespace-nowrap">
                <span
                  class="rounded-full px-2 py-0.5 text-[10px] font-semibold"
                  :class="stringsEqual(c.state, 'running') ? 'bg-emerald-500/10 text-emerald-400 border border-emerald-500/30' : 'bg-slate-800 text-slate-400 border border-slate-700'"
                >
                  {{ c.state }}
                </span>
              </td>

              <td class="px-4 py-3 whitespace-nowrap text-amber-300 font-bold">
                {{ c.size_rw }}
              </td>

              <td class="px-4 py-3 whitespace-nowrap text-slate-300">
                {{ c.size_rootfs }}
              </td>

              <td class="px-4 py-3 text-right whitespace-nowrap">
                <div class="flex items-center justify-end gap-1.5">
                  <button
                    @click="browseTargetFromOutside('container', c.name)"
                    class="cyber-btn-secondary !px-2.5 !py-1 text-[11px] text-cyan-300 flex items-center gap-1"
                    title="Browse inside container filesystem"
                  >
                    <FolderOpen class="h-3 w-3" />
                    <span>Browse Files</span>
                  </button>

                  <router-link
                    :to="'/containers?id=' + encodeURIComponent(c.id)"
                    class="cyber-btn-secondary !px-2.5 !py-1 text-[11px] text-indigo-300"
                  >
                    Control Center &rarr;
                  </router-link>
                </div>
              </td>
            </tr>

            <tr v-if="filteredContainers.length === 0">
              <td colspan="6" class="px-4 py-12 text-center text-slate-500">
                No containers match the current filter.
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- ===================================================================== -->
    <!-- TAB 5: SYSTEM DF & BUILD CACHE                                        -->
    <!-- ===================================================================== -->
    <div v-if="activeTab === 'system'" class="space-y-4">
      <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
        <!-- Structured Docker DF -->
        <div class="cyber-card space-y-3">
          <div class="flex items-center justify-between border-b border-slate-800 pb-2.5">
            <h3 class="text-xs font-bold uppercase tracking-wider text-slate-200 flex items-center gap-2">
              <Layers class="h-4 w-4 text-cyan-400" />
              <span>Docker System Disk Usage (DF)</span>
            </h3>
          </div>

          <div class="space-y-2">
            <div
              v-for="df in (overview?.docker_df || [])"
              :key="df.type"
              class="flex items-center justify-between p-3 rounded-lg border border-slate-800 bg-slate-950 font-mono text-xs"
            >
              <div>
                <span class="text-white font-bold block">{{ df.type }}</span>
                <span class="text-slate-400 text-[11px]">{{ df.total_count }} total ({{ df.active_count }} active)</span>
              </div>
              <div class="text-right">
                <span class="text-slate-200 font-bold block">{{ df.size }}</span>
                <span class="text-amber-400 text-[11px] font-semibold">Reclaimable: {{ df.reclaimable }}</span>
              </div>
            </div>
          </div>
        </div>

        <!-- Build Cache Actions -->
        <div class="cyber-card space-y-4">
          <div class="flex items-center justify-between border-b border-slate-800 pb-2.5">
            <h3 class="text-xs font-bold uppercase tracking-wider text-slate-200 flex items-center gap-2">
              <Sparkles class="h-4 w-4 text-purple-400" />
              <span>Build Cache Maintenance</span>
            </h3>
          </div>

          <p class="text-xs text-slate-400 leading-relaxed">
            Docker caches layers created during image builds. Pruning build cache frees disk space safely without affecting running containers.
          </p>

          <div class="p-3 rounded-lg bg-slate-950 border border-slate-800 text-xs font-mono flex items-center justify-between">
            <span class="text-slate-400">Build Cache Entries:</span>
            <span class="text-purple-300 font-bold">{{ overview?.counts?.build_cache_count || 0 }} items</span>
          </div>

          <div class="flex flex-col sm:flex-row gap-2 pt-2">
            <button
              @click="pruneBuildCache(false)"
              class="cyber-btn-secondary flex-1 text-xs py-2"
            >
              Prune Dangling Build Cache
            </button>
            <button
              @click="pruneBuildCache(true)"
              class="cyber-btn-danger flex-1 text-xs py-2"
            >
              Prune All Build Cache
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- ===================================================================== -->
    <!-- TAB 6: DOCKTUN APP STORAGE & SQLITE VACUUM                            -->
    <!-- ===================================================================== -->
    <div v-if="activeTab === 'docktun'" class="space-y-4">
      <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
        <!-- SQLite Database -->
        <div class="cyber-card space-y-4">
          <div class="flex items-center justify-between border-b border-slate-800 pb-2.5">
            <h3 class="text-xs font-bold uppercase tracking-wider text-slate-200 flex items-center gap-2">
              <Database class="h-4 w-4 text-emerald-400" />
              <span>SQLite App Database</span>
            </h3>
          </div>

          <div class="space-y-2.5 text-xs font-mono">
            <div class="flex items-center justify-between p-2.5 rounded-lg bg-slate-950 border border-slate-800">
              <span class="text-slate-400">Database File Path:</span>
              <span class="text-slate-200 truncate max-w-xs">{{ overview?.docktun_storage?.db_path }}</span>
            </div>

            <div class="flex items-center justify-between p-2.5 rounded-lg bg-slate-950 border border-slate-800">
              <span class="text-slate-400">Database Size on Disk:</span>
              <span class="text-emerald-400 font-bold">{{ overview?.docktun_storage?.db_size_str }}</span>
            </div>

            <div class="flex items-center justify-between p-2.5 rounded-lg bg-slate-950 border border-slate-800">
              <span class="text-slate-400">Audit Trail Records:</span>
              <span class="text-indigo-400 font-bold">{{ overview?.docktun_storage?.audit_count }} records</span>
            </div>
          </div>

          <div v-if="vacuumMessage" class="p-3 rounded-lg border border-emerald-500/30 bg-emerald-500/10 text-emerald-300 text-xs font-mono">
            {{ vacuumMessage }}
          </div>

          <div class="pt-2 border-t border-slate-800">
            <button
              @click="vacuumDatabase"
              :disabled="vacuumLoading"
              class="cyber-btn w-full text-xs py-2.5 flex items-center justify-center gap-2"
            >
              <RotateCcw v-if="vacuumLoading" class="h-3.5 w-3.5 animate-spin" />
              <Database v-else class="h-3.5 w-3.5" />
              <span>{{ vacuumLoading ? 'Vacuuming Database…' : 'Vacuum SQLite Database (Reclaim Pages)' }}</span>
            </button>
          </div>
        </div>

        <!-- Backups & Cloudflare -->
        <div class="cyber-card space-y-4">
          <div class="flex items-center justify-between border-b border-slate-800 pb-2.5">
            <h3 class="text-xs font-bold uppercase tracking-wider text-slate-200 flex items-center gap-2">
              <FolderOpen class="h-4 w-4 text-cyan-400" />
              <span>Tunnel Backups & Configs</span>
            </h3>
          </div>

          <p class="text-xs text-slate-400 leading-relaxed">
            DockTun automatically creates timestamped YAML backups every time an ingress route is modified.
          </p>

          <div class="space-y-2.5 text-xs font-mono">
            <div class="flex items-center justify-between p-2.5 rounded-lg bg-slate-950 border border-slate-800">
              <span class="text-slate-400">Total Backups Count:</span>
              <span class="text-cyan-400 font-bold">{{ overview?.docktun_storage?.backups_count || 0 }} files</span>
            </div>

            <div class="flex items-center justify-between p-2.5 rounded-lg bg-slate-950 border border-slate-800">
              <span class="text-slate-400">Total Backups Disk Size:</span>
              <span class="text-slate-200 font-bold">{{ overview?.docktun_storage?.backups_str || '0 B' }}</span>
            </div>
          </div>

          <div class="pt-2 border-t border-slate-800">
            <router-link
              to="/access?tab=cloudflare"
              class="cyber-btn-secondary w-full text-center text-xs flex items-center justify-center gap-1.5 py-2"
            >
              <span>Manage Backups in Remote Access &rarr;</span>
            </router-link>
          </div>
        </div>
      </div>
    </div>

    <!-- ===================================================================== -->
    <!-- MODAL: BUILT-IN FILE VIEWER & CODE EDITOR                             -->
    <!-- ===================================================================== -->
    <div
      v-if="activeEditorFile"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/85 backdrop-blur-md p-3 sm:p-5"
    >
      <div class="cyber-card max-w-4xl w-full h-[85vh] flex flex-col p-4 sm:p-5 space-y-3 border-indigo-500/50 bg-slate-950">
        <!-- Editor Header -->
        <div class="flex items-center justify-between border-b border-slate-800 pb-3 shrink-0">
          <div class="flex items-center gap-2 truncate pr-2">
            <FileCode class="h-5 w-5 text-indigo-400 shrink-0" />
            <div class="truncate">
              <h3 class="text-sm font-bold text-white font-mono truncate">
                {{ activeEditorFile.name }}
              </h3>
              <p class="text-[11px] text-slate-400 font-mono truncate">
                {{ selectedTarget?.name }} : {{ activeEditorFile.path }} ({{ activeEditorFile.size_str }})
              </p>
            </div>
          </div>

          <div class="flex items-center gap-2 shrink-0">
            <button
              @click="downloadStorageFile(activeEditorFile)"
              class="cyber-btn-secondary !py-1 !px-2.5 text-xs flex items-center gap-1 text-slate-300"
              title="Download file"
            >
              <Download class="h-3.5 w-3.5" />
              <span>Download</span>
            </button>

            <button
              @click="saveActiveFile"
              :disabled="editorSaving"
              class="cyber-btn !py-1 !px-3 text-xs flex items-center gap-1.5 bg-indigo-600 hover:bg-indigo-500 text-white font-semibold"
            >
              <RotateCcw v-if="editorSaving" class="h-3.5 w-3.5 animate-spin" />
              <Save v-else class="h-3.5 w-3.5" />
              <span>{{ editorSaving ? 'Saving…' : 'Save File' }}</span>
            </button>

            <button
              @click="activeEditorFile = null"
              class="p-1.5 rounded-lg text-slate-400 hover:text-white hover:bg-slate-800"
            >
              <X class="h-4 w-4" />
            </button>
          </div>
        </div>

        <!-- Editor Code Textarea -->
        <div class="flex-1 min-h-0 relative rounded-lg border border-slate-800 bg-slate-950 overflow-hidden">
          <textarea
            v-model="editorContent"
            spellcheck="false"
            class="w-full h-full p-4 font-mono text-xs bg-slate-950 text-slate-100 resize-none focus:outline-none focus:ring-1 focus:ring-indigo-500/40 leading-relaxed selection:bg-indigo-600/40"
          ></textarea>
        </div>

        <!-- Editor Footer -->
        <div class="flex items-center justify-between text-[11px] font-mono text-slate-400 shrink-0 pt-1">
          <span>Target: {{ selectedTarget?.type }} / {{ selectedTarget?.name }}</span>
          <span>{{ editorContent.split('\n').length }} lines &bull; {{ editorContent.length }} characters</span>
        </div>
      </div>
    </div>

    <!-- ===================================================================== -->
    <!-- MODAL: NEW FILE / FOLDER                                              -->
    <!-- ===================================================================== -->
    <div
      v-if="showNewItemModal"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-sm p-4"
      @click.self="showNewItemModal = false"
    >
      <div class="cyber-card max-w-md w-full p-5 space-y-4">
        <div class="flex items-center justify-between border-b border-slate-800 pb-3">
          <div class="flex items-center gap-2">
            <component :is="newItemIsDir ? FolderPlus : FilePlus" class="h-5 w-5 text-indigo-400" />
            <h3 class="text-sm font-bold text-white font-mono">
              Create New {{ newItemIsDir ? 'Directory' : 'File' }}
            </h3>
          </div>
          <button @click="showNewItemModal = false" class="text-slate-400 hover:text-white">
            <X class="h-4 w-4" />
          </button>
        </div>

        <div class="space-y-3 text-xs font-mono">
          <div class="p-2.5 rounded bg-slate-950 border border-slate-800 text-slate-400">
            <span>Location: </span>
            <span class="text-white">{{ selectedTarget?.name }}/{{ currentPath || '(root)' }}</span>
          </div>

          <div class="space-y-1">
            <label class="text-[11px] text-slate-300 font-semibold">{{ newItemIsDir ? 'Folder Name' : 'File Name (e.g. config.json, script.sh)' }}</label>
            <input
              v-model="newItemName"
              @keydown.enter="executeCreateItem"
              type="text"
              autofocus
              :placeholder="newItemIsDir ? 'my-folder' : 'config.yml'"
              class="w-full px-3 py-2 bg-slate-950 border border-slate-800 rounded-lg text-slate-100 placeholder:text-slate-500 focus:outline-none focus:border-indigo-500 font-mono text-xs"
            />
          </div>
        </div>

        <div class="flex items-center justify-end gap-2 pt-2 border-t border-slate-800">
          <button @click="showNewItemModal = false" class="cyber-btn-secondary text-xs">
            Cancel
          </button>
          <button
            @click="executeCreateItem"
            :disabled="creatingItem || !newItemName.trim()"
            class="cyber-btn text-xs bg-indigo-600 hover:bg-indigo-500 text-white font-semibold"
          >
            {{ creatingItem ? 'Creating…' : 'Create' }}
          </button>
        </div>
      </div>
    </div>

    <!-- ===================================================================== -->
    <!-- MODAL: UPLOAD FILE                                                    -->
    <!-- ===================================================================== -->
    <div
      v-if="showUploadModal"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-sm p-4"
      @click.self="showUploadModal = false"
    >
      <div class="cyber-card max-w-md w-full p-5 space-y-4">
        <div class="flex items-center justify-between border-b border-slate-800 pb-3">
          <div class="flex items-center gap-2">
            <Upload class="h-5 w-5 text-cyan-400" />
            <h3 class="text-sm font-bold text-white font-mono">
              Upload File to Storage
            </h3>
          </div>
          <button @click="showUploadModal = false" class="text-slate-400 hover:text-white">
            <X class="h-4 w-4" />
          </button>
        </div>

        <div class="space-y-3 text-xs font-mono">
          <div class="p-2.5 rounded bg-slate-950 border border-slate-800 text-slate-400">
            <span>Destination: </span>
            <span class="text-white">{{ selectedTarget?.name }}/{{ currentPath || '(root)' }}</span>
          </div>

          <div class="space-y-1">
            <label class="text-[11px] text-slate-300 font-semibold">Select File from Computer</label>
            <input
              type="file"
              @change="onFileSelected"
              class="w-full text-xs text-slate-300 file:mr-3 file:py-2 file:px-3 file:rounded-lg file:border-0 file:text-xs file:font-semibold file:bg-indigo-600 file:text-white hover:file:bg-indigo-500 cursor-pointer"
            />
          </div>

          <div v-if="selectedUploadFile" class="text-[11px] text-emerald-400">
            Ready to upload: {{ selectedUploadFile.name }} ({{ Math.round(selectedUploadFile.size / 1024) }} KB)
          </div>
        </div>

        <div class="flex items-center justify-end gap-2 pt-2 border-t border-slate-800">
          <button @click="showUploadModal = false" class="cyber-btn-secondary text-xs">
            Cancel
          </button>
          <button
            @click="executeUpload"
            :disabled="uploading || !selectedUploadFile"
            class="cyber-btn text-xs bg-cyan-600 hover:bg-cyan-500 text-white font-semibold"
          >
            {{ uploading ? 'Uploading…' : 'Start Upload' }}
          </button>
        </div>
      </div>
    </div>

    <!-- ===================================================================== -->
    <!-- MODAL: SMART RECLAIM                                                  -->
    <!-- ===================================================================== -->
    <div
      v-if="showReclaimModal"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-sm p-4"
      @click.self="showReclaimModal = false"
    >
      <div class="cyber-card max-w-lg w-full border-amber-500/40 p-5 space-y-4">
        <div class="flex items-center justify-between border-b border-slate-800 pb-3">
          <div class="flex items-center gap-2">
            <Sparkles class="h-5 w-5 text-amber-400" />
            <div>
              <h3 class="text-sm font-bold text-white font-mono">Smart Disk Reclaim</h3>
              <p class="text-[11px] text-slate-400">Select resources to safely clean up</p>
            </div>
          </div>
          <button @click="showReclaimModal = false" class="text-slate-400 hover:text-white">
            <X class="h-4 w-4" />
          </button>
        </div>

        <div class="space-y-3 text-xs">
          <!-- Option 1: Dangling Images -->
          <label class="flex items-start gap-3 p-3 rounded-lg border border-slate-800 bg-slate-950 hover:bg-slate-900 cursor-pointer">
            <input type="checkbox" v-model="reclaimOptions.prune_images_dangling" class="mt-0.5 rounded bg-slate-800 text-indigo-600" />
            <div>
              <span class="font-bold text-white block">Prune Dangling Images (&lt;none&gt;)</span>
              <span class="text-slate-400 text-[11px]">Removes untagged image layers left over from builds. Completely safe.</span>
            </div>
          </label>

          <!-- Option 2: All Unused Images -->
          <label class="flex items-start gap-3 p-3 rounded-lg border border-slate-800 bg-slate-950 hover:bg-slate-900 cursor-pointer">
            <input type="checkbox" v-model="reclaimOptions.prune_images_all" class="mt-0.5 rounded bg-slate-800 text-indigo-600" />
            <div>
              <span class="font-bold text-white block">Prune All Unused Images</span>
              <span class="text-slate-400 text-[11px]">Removes any image that does not have an active container currently running.</span>
            </div>
          </label>

          <!-- Option 3: Stopped Containers -->
          <label class="flex items-start gap-3 p-3 rounded-lg border border-slate-800 bg-slate-950 hover:bg-slate-900 cursor-pointer">
            <input type="checkbox" v-model="reclaimOptions.prune_containers" class="mt-0.5 rounded bg-slate-800 text-indigo-600" />
            <div>
              <span class="font-bold text-white block">Prune Stopped & Exited Containers</span>
              <span class="text-slate-400 text-[11px]">Cleans up old container execution instances that are stopped.</span>
            </div>
          </label>

          <!-- Option 4: Build Cache -->
          <label class="flex items-start gap-3 p-3 rounded-lg border border-slate-800 bg-slate-950 hover:bg-slate-900 cursor-pointer">
            <input type="checkbox" v-model="reclaimOptions.prune_build_cache" class="mt-0.5 rounded bg-slate-800 text-indigo-600" />
            <div>
              <span class="font-bold text-white block">Prune Docker Build Cache</span>
              <span class="text-slate-400 text-[11px]">Cleans up cached build steps and layers. Completely safe.</span>
            </div>
          </label>

          <!-- Option 5: Dangling Volumes (With Warning) -->
          <label class="flex items-start gap-3 p-3 rounded-lg border border-amber-900/50 bg-amber-950/20 hover:bg-amber-950/30 cursor-pointer">
            <input type="checkbox" v-model="reclaimOptions.prune_volumes" class="mt-0.5 rounded bg-slate-800 text-amber-500" />
            <div>
              <span class="font-bold text-amber-300 block flex items-center gap-1.5">
                <AlertTriangle class="h-3.5 w-3.5 text-amber-400" />
                Prune Dangling Volumes (CAUTION)
              </span>
              <span class="text-amber-200/80 text-[11px] block mt-0.5">
                Permanently deletes unattached volumes. If you have databases or Nextcloud data in unmounted volumes, that data will be lost!
              </span>
            </div>
          </label>
        </div>

        <!-- Output Log if run -->
        <div v-if="reclaimResults.length" class="space-y-1">
          <label class="text-[10px] uppercase font-mono text-slate-400">Execution Output</label>
          <pre class="p-2.5 rounded-lg bg-slate-950 border border-slate-800 text-[11px] font-mono text-slate-300 max-h-40 overflow-y-auto whitespace-pre-wrap">{{ reclaimResults.join('\n\n') }}</pre>
        </div>

        <div class="flex items-center justify-between pt-3 border-t border-slate-800">
          <button @click="showReclaimModal = false" class="cyber-btn-secondary text-xs">
            Close
          </button>

          <button
            @click="executeBatchReclaim"
            :disabled="reclaimLoading"
            class="cyber-btn flex items-center gap-2 bg-amber-600 hover:bg-amber-500 text-white font-semibold"
          >
            <RotateCcw v-if="reclaimLoading" class="h-3.5 w-3.5 animate-spin" />
            <Sparkles v-else class="h-3.5 w-3.5" />
            <span>{{ reclaimLoading ? 'Reclaiming Disk…' : 'Execute Reclaim' }}</span>
          </button>
        </div>
      </div>
    </div>

    <!-- ===================================================================== -->
    <!-- MODAL: INSPECT VOLUME OR IMAGE                                        -->
    <!-- ===================================================================== -->
    <div
      v-if="selectedItemInspect"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-sm p-4"
      @click.self="selectedItemInspect = null"
    >
      <div class="cyber-card max-w-lg w-full p-5 space-y-4">
        <div class="flex items-center justify-between border-b border-slate-800 pb-3">
          <div class="flex items-center gap-2">
            <component :is="inspectType === 'volume' ? HardDrive : Layers" class="h-5 w-5 text-indigo-400" />
            <h3 class="text-sm font-bold text-white font-mono">
              {{ inspectType === 'volume' ? 'Volume Details' : 'Image Details' }}
            </h3>
          </div>
          <button @click="selectedItemInspect = null" class="text-slate-400 hover:text-white">
            <X class="h-4 w-4" />
          </button>
        </div>

        <!-- Volume Inspector -->
        <div v-if="inspectType === 'volume'" class="space-y-2 text-xs font-mono">
          <div class="p-2.5 rounded bg-slate-950 border border-slate-800">
            <span class="text-slate-400 block text-[10px] uppercase">Name</span>
            <span class="text-white font-bold break-all">{{ selectedItemInspect.name }}</span>
          </div>

          <div class="p-2.5 rounded bg-slate-950 border border-slate-800">
            <span class="text-slate-400 block text-[10px] uppercase">Mountpoint on Host</span>
            <span class="text-cyan-300 break-all">{{ selectedItemInspect.mountpoint }}</span>
          </div>

          <div class="grid grid-cols-2 gap-2">
            <div class="p-2.5 rounded bg-slate-950 border border-slate-800">
              <span class="text-slate-400 block text-[10px] uppercase">Driver</span>
              <span class="text-slate-200">{{ selectedItemInspect.driver }}</span>
            </div>
            <div class="p-2.5 rounded bg-slate-950 border border-slate-800">
              <span class="text-slate-400 block text-[10px] uppercase">Scope</span>
              <span class="text-slate-200">{{ selectedItemInspect.scope }}</span>
            </div>
          </div>

          <div class="p-2.5 rounded bg-slate-950 border border-slate-800">
            <span class="text-slate-400 block text-[10px] uppercase">Attached Containers</span>
            <span v-if="selectedItemInspect.containers && selectedItemInspect.containers.length" class="text-emerald-400">
              {{ selectedItemInspect.containers.join(', ') }}
            </span>
            <span v-else class="text-amber-400 font-semibold">None (Dangling / Unmounted)</span>
          </div>
        </div>

        <div class="flex justify-end pt-2 border-t border-slate-800">
          <button @click="selectedItemInspect = null" class="cyber-btn-secondary text-xs">
            Close
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
