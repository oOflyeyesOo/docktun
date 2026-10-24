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

// ---------------------------------------------------------------------------
// PRE-DELETION INFORMATION & CONFIRMATION MODAL STATE
// ---------------------------------------------------------------------------
interface PendingDeleteInfo {
  title: string
  subtitle?: string
  targetType: 'volume' | 'volume_prune' | 'image' | 'image_prune' | 'file' | 'container_prune' | 'system_prune'
  details: { label: string; value: string; highlight?: boolean }[]
  itemsList?: { name: string; size?: string; info?: string }[]
  warning: string
  confirmButtonText: string
  onConfirm: () => Promise<void>
}

const pendingDelete = ref<PendingDeleteInfo | null>(null)
const deletingInProgress = ref(false)

async function executePendingDelete() {
  if (!pendingDelete.value) return
  deletingInProgress.value = true
  try {
    await pendingDelete.value.onConfirm()
    pendingDelete.value = null
  } catch (e: any) {
    showToast(e.message || 'Operation failed', 'error')
  } finally {
    deletingInProgress.value = false
  }
}

// Delete Item in File Manager with full details first
function deleteStorageFile(file: any) {
  if (!selectedTarget.value) return
  pendingDelete.value = {
    title: file.is_dir ? 'Delete Directory & Contents' : 'Delete File',
    subtitle: 'Storage File Manager Deletion',
    targetType: 'file',
    details: [
      { label: 'Storage Target', value: `${selectedTarget.value.type.toUpperCase()}: ${selectedTarget.value.name}`, highlight: true },
      { label: 'File / Path', value: file.path },
      { label: 'Item Type', value: file.is_dir ? 'Directory (recursive removal)' : 'File' },
      { label: 'Size on Disk', value: file.is_dir ? 'Folder' : file.size_str }
    ],
    warning: file.is_dir
      ? 'CRITICAL: The directory and ALL nested files and subdirectories will be permanently deleted from this storage location.'
      : 'This file will be permanently deleted from storage. This operation cannot be undone.',
    confirmButtonText: file.is_dir ? 'Permanently Delete Directory' : 'Permanently Delete File',
    onConfirm: async () => {
      const params = new URLSearchParams({
        target_type: selectedTarget.value.type,
        target_id: selectedTarget.value.id,
        path: file.path
      })
      await apiFetch(`/api/storage/files/delete?${params}`, { method: 'DELETE' })
      showToast(`Deleted ${file.name}`)
      await loadDirectory(currentPath.value)
    }
  }
}

// ---------------------------------------------------------------------------
// VOLUME & IMAGE & CONTAINER PRUNE ACTIONS WITH FULL DETAILS
// ---------------------------------------------------------------------------
function deleteVolume(vol: any) {
  if (vol.in_use) {
    showToast(`Cannot delete volume "${vol.name}" because it is actively mounted by container(s): ${vol.containers.join(', ')}`, 'error')
    return
  }

  pendingDelete.value = {
    title: 'Delete Docker Volume',
    subtitle: 'Permanent Storage Removal',
    targetType: 'volume',
    details: [
      { label: 'Volume Name', value: vol.name, highlight: true },
      { label: 'Volume Classification', value: vol.is_anonymous ? 'Anonymous Hash Volume' : 'Named Persistent Volume' },
      { label: 'Size on Host', value: vol.size || 'N/A' },
      { label: 'Driver & Scope', value: `${vol.driver} • ${vol.scope}` },
      { label: 'Mountpoint on Host', value: vol.mountpoint },
      { label: 'Attached Containers', value: vol.containers?.length ? vol.containers.join(', ') : 'None (Dangling / Unmounted)' }
    ],
    warning: 'CRITICAL DATA LOSS WARNING: All database tables, user uploads, application state, and persistent records stored inside this Docker volume will be permanently erased from your hard drive.',
    confirmButtonText: 'Permanently Delete Volume',
    onConfirm: async () => {
      actionLoading.value[vol.name] = true
      try {
        await apiFetch(`/api/storage/volumes/${encodeURIComponent(vol.name)}`, { method: 'DELETE' })
        showToast(`Volume ${vol.name} deleted successfully`)
        await fetchVolumes()
        await fetchOverview()
        await fetchTargets()
      } finally {
        delete actionLoading.value[vol.name]
      }
    }
  }
}

function pruneVolumes() {
  const danglingList = volumes.value.filter(v => !v.in_use)
  if (danglingList.length === 0) {
    showToast('No dangling volumes to prune. All volumes are actively attached.', 'info')
    return
  }

  pendingDelete.value = {
    title: 'Prune All Dangling Volumes',
    subtitle: 'Bulk Persistent Storage Deletion',
    targetType: 'volume_prune',
    details: [
      { label: 'Dangling Volumes Found', value: `${danglingList.length} volumes`, highlight: true },
      { label: 'Estimated Space to Reclaim', value: overview.value?.reclaimable_str || 'Estimated from DF' }
    ],
    itemsList: danglingList.map(v => ({
      name: v.name,
      size: v.size || 'N/A',
      info: v.is_anonymous ? 'Anonymous Volume' : 'Named Volume'
    })),
    warning: 'CRITICAL WARNING: These volumes are not mounted to a running container RIGHT NOW. If they belong to stopped databases (Postgres/MySQL), Nextcloud (aio-data), or previous stack tests, ALL their data will be permanently wiped.',
    confirmButtonText: `Prune ${danglingList.length} Dangling Volumes`,
    onConfirm: async () => {
      loading.value = true
      try {
        const res: any = await apiFetch('/api/storage/volumes/prune', { method: 'POST' })
        showToast(res.output || 'Dangling volumes pruned')
        await fetchVolumes()
        await fetchOverview()
        await fetchTargets()
      } finally {
        loading.value = false
      }
    }
  }
}

function deleteImage(img: any, force = false) {
  pendingDelete.value = {
    title: 'Delete Docker Image',
    subtitle: 'Remove Local Image Layers',
    targetType: 'image',
    details: [
      { label: 'Repository & Tag', value: `${img.repository}:${img.tag}`, highlight: true },
      { label: 'Image ID', value: img.id.slice(0, 12) },
      { label: 'Virtual Disk Size', value: img.size },
      { label: 'In-Use Status', value: img.in_use ? `In-Use by: ${img.containers.join(', ')}` : 'Unused (Safe to remove)' },
      { label: 'Created', value: img.created_since || img.created_at }
    ],
    warning: img.in_use
      ? `WARNING: This image is actively used by running container(s): ${img.containers.join(', ')}. Force deletion will untag the image layers.`
      : 'This image will be removed from your host Docker cache. It can be re-pulled later if needed.',
    confirmButtonText: img.in_use ? 'Force Remove Image' : 'Delete Image',
    onConfirm: async () => {
      actionLoading.value[img.id] = true
      try {
        await apiFetch(`/api/storage/images/${img.id}?force=${img.in_use || force}`, { method: 'DELETE' })
        showToast(`Image deleted successfully`)
        await fetchImages()
        await fetchOverview()
      } finally {
        delete actionLoading.value[img.id]
      }
    }
  }
}

function pruneImages(all = false) {
  pendingDelete.value = {
    title: all ? 'Prune All Unused Images' : 'Prune Dangling Images',
    subtitle: 'Image Storage Maintenance',
    targetType: 'image_prune',
    details: [
      { label: 'Scope', value: all ? 'All images without active running containers' : 'Only untagged (<none>:<none>) dangling layers', highlight: true },
      { label: 'Target Count', value: all ? `${overview.value?.counts?.unused_images || 0} images` : `${overview.value?.counts?.dangling_images || 0} dangling` }
    ],
    warning: all
      ? 'All downloaded images not currently used by a running container will be removed. If you start those containers later, Docker will re-download the images.'
      : 'Untagged intermediate build layers (<none>:<none>) will be safely removed.',
    confirmButtonText: all ? 'Prune All Unused Images' : 'Prune Dangling Images',
    onConfirm: async () => {
      loading.value = true
      try {
        const res: any = await apiFetch(`/api/storage/images/prune?all=${all}`, { method: 'POST' })
        showToast(res.output || 'Images pruned')
        await fetchImages()
        await fetchOverview()
      } finally {
        loading.value = false
      }
    }
  }
}

function pruneContainers() {
  pendingDelete.value = {
    title: 'Prune Stopped Containers',
    subtitle: 'Container Lifecycle Maintenance',
    targetType: 'container_prune',
    details: [
      { label: 'Stopped Containers Count', value: `${overview.value?.counts?.stopped_containers || 0} containers`, highlight: true }
    ],
    warning: 'All exited or stopped container instances will be removed. Their writable layer changes (not in volumes) will be deleted. Running containers are completely untouched.',
    confirmButtonText: 'Prune Stopped Containers',
    onConfirm: async () => {
      loading.value = true
      try {
        const res: any = await apiFetch('/api/storage/containers/prune', { method: 'POST' })
        showToast(res.output || 'Stopped containers pruned')
        await fetchContainers()
        await fetchOverview()
      } finally {
        loading.value = false
      }
    }
  }
}

function pruneBuildCache(all = false) {
  pendingDelete.value = {
    title: all ? 'Prune All Build Cache' : 'Prune Dangling Build Cache',
    subtitle: 'Docker Builder Cache Maintenance',
    targetType: 'image_prune',
    details: [
      { label: 'Build Cache Entries', value: `${overview.value?.counts?.build_cache_count || 0} items`, highlight: true },
      { label: 'Mode', value: all ? 'All build cache objects' : 'Dangling build steps only' }
    ],
    warning: 'Pruning build cache is completely safe and does not affect any running containers or images. Next builds will compile from scratch.',
    confirmButtonText: all ? 'Prune All Build Cache' : 'Prune Dangling Cache',
    onConfirm: async () => {
      loading.value = true
      try {
        const res: any = await apiFetch(`/api/storage/build-cache/prune?all=${all}`, { method: 'POST' })
        showToast(res.output || 'Build cache pruned')
        await fetchOverview()
      } finally {
        loading.value = false
      }
    }
  }
}

function pruneSystem() {
  pendingDelete.value = {
    title: 'Complete Docker System Prune',
    subtitle: 'Host-Wide Clean Up',
    targetType: 'system_prune',
    details: [
      { label: 'Cleaned Resources', value: 'Stopped containers, unused networks, dangling images, build cache', highlight: true },
      { label: 'Reclaimable Space', value: overview.value?.reclaimable_str || 'Estimated from DF' },
      { label: 'Protected Resources', value: 'Active running containers and all named volumes are preserved' }
    ],
    warning: 'This runs "docker system prune -f" across your entire Docker host. All inactive container instances, unused bridge networks, and dangling image layers will be cleaned up.',
    confirmButtonText: 'Execute System Prune (-f)',
    onConfirm: async () => {
      systemPruning.value = true
      try {
        const res: any = await apiFetch('/api/docker/system/prune', { method: 'POST' })
        showToast(res.output || 'Docker system pruned successfully')
        await refreshAll()
      } finally {
        systemPruning.value = false
      }
    }
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
    const danglingList = volumes.value.filter(v => !v.in_use)
    pendingDelete.value = {
      title: 'Smart Reclaim Confirmation (Dangling Volumes Selected)',
      subtitle: 'Batch Cleanup',
      targetType: 'volume_prune',
      details: [
        { label: 'Selected Tasks', value: 'Images, Containers, Build Cache + Dangling Volumes', highlight: true },
        { label: 'Volumes to be Destroyed', value: `${danglingList.length} unmounted volumes` }
      ],
      itemsList: danglingList.map(v => ({ name: v.name, size: v.size || 'N/A', info: v.is_anonymous ? 'Anonymous' : 'Named' })),
      warning: 'CRITICAL: You have selected "Prune Dangling Volumes". Any databases or files inside unattached volumes will be permanently lost.',
      confirmButtonText: 'Proceed with Smart Reclaim',
      onConfirm: async () => {
        await runBatchReclaimInternal()
      }
    }
    return
  }

  await runBatchReclaimInternal()
}

async function runBatchReclaimInternal() {
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
      <CheckCircle2 v-if="toastMessage.type === 'success'" class="h-4 w-4 shrink-0 text-emerald-600" />
      <AlertCircle v-else class="h-4 w-4 shrink-0 text-rose-600" />
      <span>{{ toastMessage.text }}</span>
      <button @click="toastMessage = null" class="ml-2 hover:opacity-75">
        <X class="h-3.5 w-3.5" />
      </button>
    </div>

    <!-- Header Section -->
    <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between border-b border-slate-200/80 pb-4">
      <div>
        <div class="flex items-center gap-2">
          <p class="text-[11px] font-semibold uppercase tracking-wider text-indigo-600">Host & Docker Infrastructure</p>
          <span class="rounded bg-slate-100 px-1.5 py-0.2 text-[10px] font-mono text-slate-700">File & Storage Center</span>
        </div>
        <h1 class="text-2xl font-bold tracking-tight text-slate-900 flex items-center gap-2.5 mt-1">
          <HardDrive class="h-6 w-6 text-indigo-600" />
          <span>Storage & File Manager</span>
        </h1>
      </div>

      <div class="flex items-center gap-2 flex-wrap">
        <!-- Smart Reclaim Button -->
        <button
          @click="showReclaimModal = true"
          class="cyber-btn flex items-center gap-2 bg-indigo-600 hover:bg-indigo-500 text-white font-semibold shadow-lg shadow-indigo-600/20"
        >
          <Sparkles class="h-4 w-4 text-amber-700" />
          <span>Smart Reclaim</span>
        </button>

        <button
          @click="vacuumDatabase"
          :disabled="vacuumLoading"
          class="cyber-btn-secondary text-xs flex items-center gap-1.5"
          title="Vacuum SQLite Database to reclaim free pages"
        >
          <Database class="h-3.5 w-3.5 text-cyan-600" :class="vacuumLoading ? 'animate-spin' : ''" />
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
    <div class="cyber-card bg-white/80 border-slate-200/90 relative overflow-hidden">
      <div class="absolute -right-20 -top-20 w-64 h-64 bg-indigo-500/10 rounded-full blur-3xl pointer-events-none"></div>

      <div class="grid grid-cols-1 lg:grid-cols-12 gap-5 items-center">
        <!-- Reclaimable Metric & Breakdown -->
        <div class="lg:col-span-7 space-y-2.5">
          <div class="flex items-center gap-2">
            <span class="text-[10px] font-bold font-mono tracking-widest uppercase text-amber-600/90">
              RESOURCES &bull; RECLAIMABLE SPACE
            </span>
          </div>

          <div class="flex items-baseline gap-3">
            <span class="text-3xl sm:text-4xl font-extrabold font-mono text-amber-700 tracking-tight">
              {{ overview?.reclaimable_str || '0 B' }}
            </span>
            <span class="text-xs text-slate-600 font-medium">potential recovery</span>
          </div>

          <!-- Breakdown Tags -->
          <div class="flex flex-wrap items-center gap-2 pt-1 text-xs font-mono">
            <span class="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-slate-50 border border-slate-200 text-slate-700">
              <Layers class="h-3 w-3 text-cyan-600" />
              <span>{{ overview?.counts?.unused_images || 0 }} unused images</span>
            </span>

            <span class="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-slate-50 border border-slate-200 text-slate-700">
              <Box class="h-3 w-3 text-indigo-600" />
              <span>{{ overview?.counts?.stopped_containers || 0 }} stopped containers</span>
            </span>

            <span class="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-slate-50 border border-slate-200 text-slate-700">
              <HardDrive class="h-3 w-3 text-amber-600" />
              <span>{{ overview?.counts?.dangling_volumes || 0 }} dangling volumes</span>
            </span>

            <span v-if="overview?.counts?.build_cache_count" class="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-slate-50 border border-slate-200 text-slate-700">
              <Sparkles class="h-3 w-3 text-purple-600" />
              <span>{{ overview?.counts?.build_cache_count }} build cache objects</span>
            </span>
          </div>
        </div>

        <!-- Host Disk Space Gauge -->
        <div class="lg:col-span-5 rounded-xl bg-slate-50/70 border border-slate-200/90 p-4 space-y-2.5">
          <div class="flex items-center justify-between text-xs">
            <span class="text-slate-600 font-medium">Host Filesystem Disk</span>
            <span class="font-mono text-slate-800 font-bold">
              {{ overview?.host_disk?.used_str || '—' }} / {{ overview?.host_disk?.total_str || '—' }}
            </span>
          </div>

          <!-- Bar -->
          <div class="h-2 w-full overflow-hidden rounded-full bg-slate-100">
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

          <div class="flex items-center justify-between text-[11px] font-mono text-slate-600">
            <span>Free: <strong class="text-emerald-600">{{ overview?.host_disk?.free_str || '—' }}</strong></span>
            <span>Usage: <strong class="text-slate-800">{{ (overview?.host_disk?.percent || 0).toFixed(1) }}%</strong></span>
          </div>
        </div>
      </div>
    </div>

    <!-- Navigation Tabs -->
    <div class="flex items-center justify-between border-b border-slate-200 pb-2 flex-wrap gap-2">
      <div class="flex items-center gap-1 rounded-xl bg-white/80 p-1 border border-slate-200 text-xs flex-wrap">
        <!-- 1. File Explorer Tab (Primary) -->
        <button
          @click="activeTab = 'files'"
          class="flex items-center gap-1.5 px-3.5 py-1.5 rounded-lg font-semibold transition"
          :class="activeTab === 'files' ? 'bg-indigo-600 text-white shadow-sm ring-1 ring-indigo-500/40' : 'text-slate-600 hover:text-slate-800'"
        >
          <FolderOpen class="h-3.5 w-3.5 text-cyan-700" />
          <span>File Explorer</span>
          <span class="rounded bg-indigo-50/80 px-1.5 py-0.2 text-[10px] font-mono text-cyan-700">Live</span>
        </button>

        <!-- 2. Volumes Tab -->
        <button
          @click="activeTab = 'volumes'"
          class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg font-medium transition"
          :class="activeTab === 'volumes' ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-600 hover:text-slate-800'"
        >
          <HardDrive class="h-3.5 w-3.5 text-amber-600" />
          <span>Volumes</span>
          <span class="rounded bg-slate-50 px-1.5 py-0.2 text-[10px] font-mono">{{ volumes.length }}</span>
        </button>

        <!-- 3. Images Tab -->
        <button
          @click="activeTab = 'images'"
          class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg font-medium transition"
          :class="activeTab === 'images' ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-600 hover:text-slate-800'"
        >
          <Layers class="h-3.5 w-3.5 text-cyan-600" />
          <span>Images</span>
          <span class="rounded bg-slate-50 px-1.5 py-0.2 text-[10px] font-mono">{{ images.length }}</span>
        </button>

        <!-- 4. Containers Tab -->
        <button
          @click="activeTab = 'containers'"
          class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg font-medium transition"
          :class="activeTab === 'containers' ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-600 hover:text-slate-800'"
        >
          <Box class="h-3.5 w-3.5 text-indigo-600" />
          <span>Containers</span>
          <span class="rounded bg-slate-50 px-1.5 py-0.2 text-[10px] font-mono">{{ containers.length }}</span>
        </button>

        <!-- 5. Build Cache Tab -->
        <button
          @click="activeTab = 'system'"
          class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg font-medium transition"
          :class="activeTab === 'system' ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-600 hover:text-slate-800'"
        >
          <Sparkles class="h-3.5 w-3.5 text-purple-600" />
          <span>Build Cache & DF</span>
        </button>

        <!-- 6. DockTun App DB -->
        <button
          @click="activeTab = 'docktun'"
          class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg font-medium transition"
          :class="activeTab === 'docktun' ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-600 hover:text-slate-800'"
        >
          <Database class="h-3.5 w-3.5 text-emerald-600" />
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
            <div class="flex items-center justify-between border-b border-slate-200 pb-2">
              <span class="text-xs font-bold text-slate-900 uppercase tracking-wider flex items-center gap-1.5">
                <HardDrive class="h-3.5 w-3.5 text-indigo-600" />
                <span>Storage Target</span>
              </span>
              <span class="text-[10px] text-slate-600 font-mono">{{ storageTargets.length }} targets</span>
            </div>

            <!-- Target Selector List -->
            <div class="space-y-1 max-h-[580px] overflow-y-auto pr-1">
              <!-- Group 1: Docker Volumes -->
              <div class="pt-1 pb-1">
                <span class="text-[10px] font-bold font-mono uppercase tracking-wider text-amber-600 px-2 block mb-1">
                  Docker Volumes
                </span>
                <div
                  v-for="t in storageTargets.filter(t => t.type === 'volume')"
                  :key="t.id"
                  @click="selectTarget(t)"
                  class="flex items-center justify-between px-2.5 py-2 rounded-lg cursor-pointer transition text-xs font-mono group"
                  :class="selectedTarget?.type === t.type && selectedTarget?.id === t.id ? 'bg-indigo-600 text-white shadow-sm' : 'hover:bg-slate-100/80 text-slate-700'"
                >
                  <div class="truncate pr-2">
                    <div class="font-bold truncate flex items-center gap-1.5">
                      <HardDrive class="h-3 w-3 shrink-0" :class="selectedTarget?.id === t.id ? 'text-slate-900' : 'text-amber-600'" />
                      <span class="truncate">{{ t.name }}</span>
                    </div>
                    <span class="text-[10px] opacity-75 truncate block">{{ t.description }}</span>
                  </div>
                  <ChevronRight class="h-3.5 w-3.5 shrink-0 opacity-50 group-hover:opacity-100" />
                </div>
              </div>

              <!-- Group 2: Docker Containers -->
              <div class="pt-2 pb-1 border-t border-slate-200/60">
                <span class="text-[10px] font-bold font-mono uppercase tracking-wider text-cyan-600 px-2 block mb-1">
                  Containers Filesystem
                </span>
                <div
                  v-for="t in storageTargets.filter(t => t.type === 'container')"
                  :key="t.id"
                  @click="selectTarget(t)"
                  class="flex items-center justify-between px-2.5 py-2 rounded-lg cursor-pointer transition text-xs font-mono group"
                  :class="selectedTarget?.type === t.type && selectedTarget?.id === t.id ? 'bg-indigo-600 text-white shadow-sm' : 'hover:bg-slate-100/80 text-slate-700'"
                >
                  <div class="truncate pr-2">
                    <div class="font-bold truncate flex items-center gap-1.5">
                      <Box class="h-3 w-3 shrink-0" :class="selectedTarget?.id === t.id ? 'text-slate-900' : 'text-cyan-600'" />
                      <span class="truncate">{{ t.name }}</span>
                    </div>
                    <span class="text-[10px] opacity-75 truncate block">{{ t.description }}</span>
                  </div>
                  <ChevronRight class="h-3.5 w-3.5 shrink-0 opacity-50 group-hover:opacity-100" />
                </div>
              </div>

              <!-- Group 3: Host & Projects -->
              <div class="pt-2 pb-1 border-t border-slate-200/60">
                <span class="text-[10px] font-bold font-mono uppercase tracking-wider text-emerald-600 px-2 block mb-1">
                  Host & Project Dirs
                </span>
                <div
                  v-for="t in storageTargets.filter(t => t.type === 'docktun' || t.type === 'project')"
                  :key="t.id"
                  @click="selectTarget(t)"
                  class="flex items-center justify-between px-2.5 py-2 rounded-lg cursor-pointer transition text-xs font-mono group"
                  :class="selectedTarget?.type === t.type && selectedTarget?.id === t.id ? 'bg-indigo-600 text-white shadow-sm' : 'hover:bg-slate-100/80 text-slate-700'"
                >
                  <div class="truncate pr-2">
                    <div class="font-bold truncate flex items-center gap-1.5">
                      <Folder class="h-3 w-3 shrink-0" :class="selectedTarget?.id === t.id ? 'text-slate-900' : 'text-emerald-600'" />
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
          <div class="cyber-card !p-4 space-y-3 bg-slate-50">
            <!-- Active Target Info & Action Toolbar -->
            <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-200 pb-3">
              <div>
                <div class="flex items-center gap-2">
                  <span
                    class="rounded px-1.5 py-0.2 text-[10px] font-mono font-bold uppercase"
                    :class="selectedTarget?.type === 'volume' ? 'bg-amber-500/10 text-amber-700 border border-amber-500/30' : (selectedTarget?.type === 'container' ? 'bg-cyan-500/10 text-cyan-700 border border-cyan-500/30' : 'bg-emerald-500/10 text-emerald-700 border border-emerald-500/30')"
                  >
                    {{ selectedTarget?.type || 'Target' }}
                  </span>
                  <h2 class="text-sm font-bold text-slate-900 font-mono truncate max-w-sm" :title="selectedTarget?.name">
                    {{ selectedTarget?.name || 'Select a Storage Target' }}
                  </h2>
                </div>
                <p class="text-[11px] text-slate-600 font-mono mt-0.5 truncate">{{ selectedTarget?.description }}</p>
              </div>

              <!-- File Action Buttons -->
              <div class="flex items-center gap-2 flex-wrap">
                <button
                  @click="newItemIsDir = false; newItemName = ''; showNewItemModal = true"
                  class="cyber-btn-secondary !py-1.5 !px-2.5 text-xs flex items-center gap-1.5 text-slate-800"
                  title="Create new file in current directory"
                >
                  <FilePlus class="h-3.5 w-3.5 text-indigo-600" />
                  <span>New File</span>
                </button>

                <button
                  @click="newItemIsDir = true; newItemName = ''; showNewItemModal = true"
                  class="cyber-btn-secondary !py-1.5 !px-2.5 text-xs flex items-center gap-1.5 text-slate-800"
                  title="Create new folder in current directory"
                >
                  <FolderPlus class="h-3.5 w-3.5 text-amber-600" />
                  <span>New Folder</span>
                </button>

                <button
                  @click="showUploadModal = true"
                  class="cyber-btn-secondary !py-1.5 !px-2.5 text-xs flex items-center gap-1.5 text-cyan-700"
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
            <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-2 bg-white/60 p-2.5 rounded-lg border border-slate-200 text-xs font-mono">
              <!-- Breadcrumbs -->
              <div class="flex items-center gap-1.5 flex-wrap min-w-0">
                <button
                  @click="navigateUp"
                  :disabled="!currentPath"
                  class="p-1 rounded bg-slate-100 hover:bg-slate-200 text-slate-700 disabled:opacity-30 disabled:cursor-not-allowed"
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
                      class="hover:text-indigo-600 transition"
                      :class="idx === breadcrumbs.length - 1 ? 'text-slate-900 font-bold' : 'text-slate-600'"
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
                  class="w-full pl-8 pr-2.5 py-1 text-xs bg-slate-50 border border-slate-200 rounded-lg text-slate-800 placeholder:text-slate-500 font-mono"
                />
              </div>
            </div>

            <!-- File Explorer Table -->
            <div class="overflow-x-auto border border-slate-200 rounded-lg bg-slate-50 min-h-[380px]">
              <table class="w-full text-xs font-mono">
                <thead>
                  <tr class="border-b border-slate-200 bg-white/80 text-left text-slate-600">
                    <th class="px-4 py-2.5 font-semibold uppercase tracking-wider">Name</th>
                    <th class="px-4 py-2.5 font-semibold uppercase tracking-wider">Size</th>
                    <th class="px-4 py-2.5 font-semibold uppercase tracking-wider">Modified</th>
                    <th class="px-4 py-2.5 font-semibold uppercase tracking-wider text-right">Actions</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-slate-200/60">
                  <!-- Up Dir Row -->
                  <tr
                    v-if="currentPath"
                    @click="navigateUp"
                    class="hover:bg-white/60 cursor-pointer transition text-indigo-700"
                  >
                    <td colspan="4" class="px-4 py-2 flex items-center gap-2 font-bold">
                      <Folder class="h-4 w-4 text-amber-600" />
                      <span>.. (Up one level)</span>
                    </td>
                  </tr>

                  <!-- File / Folder Rows -->
                  <tr
                    v-for="file in filteredFiles"
                    :key="file.path"
                    class="hover:bg-white/50 transition-colors group"
                  >
                    <!-- Name & Icon -->
                    <td class="px-4 py-2.5">
                      <div
                        @click="openFile(file)"
                        class="flex items-center gap-2 cursor-pointer font-medium hover:text-indigo-600 max-w-sm truncate"
                        :class="file.is_dir ? 'text-slate-900 font-bold' : 'text-slate-800'"
                      >
                        <component
                          :is="getFileIcon(file)"
                          class="h-4 w-4 shrink-0"
                          :class="file.is_dir ? 'text-amber-600 fill-amber-400/20' : 'text-slate-600'"
                        />
                        <span class="truncate">{{ file.name }}</span>
                      </div>
                    </td>

                    <!-- Size -->
                    <td class="px-4 py-2.5 whitespace-nowrap text-slate-600">
                      {{ file.is_dir ? '—' : file.size_str }}
                    </td>

                    <!-- Modified Date -->
                    <td class="px-4 py-2.5 whitespace-nowrap text-slate-600 text-[11px]">
                      {{ file.mod_time || '—' }}
                    </td>

                    <!-- Actions -->
                    <td class="px-4 py-2.5 text-right whitespace-nowrap">
                      <div class="flex items-center justify-end gap-1">
                        <button
                          v-if="!file.is_dir"
                          @click="openFile(file)"
                          class="cyber-btn-secondary !px-2 !py-0.5 !text-[11px] text-cyan-700"
                          title="View / Edit file"
                        >
                          Edit
                        </button>

                        <button
                          v-if="!file.is_dir"
                          @click="downloadStorageFile(file)"
                          class="p-1 rounded text-slate-600 hover:text-indigo-700 hover:bg-slate-100"
                          title="Download file"
                        >
                          <Download class="h-3.5 w-3.5" />
                        </button>

                        <button
                          @click="deleteStorageFile(file)"
                          class="p-1 rounded text-slate-600 hover:text-rose-600 hover:bg-rose-500/10"
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
                    <td colspan="4" class="px-4 py-12 text-center text-slate-600">
                      <RotateCcw class="h-5 w-5 animate-spin mx-auto mb-2 text-indigo-600" />
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
      <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 bg-white/60 border border-slate-200 rounded-xl p-3 text-xs">
        <div class="flex flex-wrap items-center gap-2">
          <div class="relative w-48 sm:w-64">
            <Search class="h-3.5 w-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-slate-500" />
            <input
              v-model="volumeSearch"
              type="text"
              placeholder="Search volume name or container…"
              class="w-full pl-8 pr-2.5 py-1.5 text-xs bg-slate-50 border border-slate-200 rounded-lg text-slate-800 placeholder:text-slate-500 font-mono"
            />
          </div>

          <select
            v-model="volumeFilter"
            class="text-xs bg-slate-50 border border-slate-200 rounded-lg px-3 py-1.5 text-slate-700 font-mono"
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
      <div class="cyber-card !p-0 overflow-x-auto border border-slate-200 bg-slate-50">
        <table class="w-full text-xs font-mono">
          <thead>
            <tr class="border-b border-slate-200 bg-white/80 text-left text-slate-600">
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Volume Name</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Status</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Size</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Driver / Scope</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Attached Containers</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider text-right">Actions</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-200/60">
            <tr
              v-for="v in filteredVolumes"
              :key="v.name"
              class="hover:bg-white/50 transition-colors"
            >
              <td class="px-4 py-3 text-slate-800 max-w-xs truncate" :title="v.name">
                <div class="flex items-center gap-2">
                  <span
                    v-if="v.is_anonymous"
                    class="px-1.5 py-0.2 rounded bg-amber-500/10 text-amber-600 border border-amber-500/30 text-[9px] uppercase shrink-0"
                    title="Anonymous hash volume"
                  >
                    Anon Hash
                  </span>
                  <span class="font-bold text-slate-900 truncate">{{ v.name }}</span>
                </div>
              </td>

              <td class="px-4 py-3 whitespace-nowrap">
                <span
                  class="rounded-full px-2 py-0.5 text-[10px] font-semibold"
                  :class="v.in_use ? 'bg-emerald-500/10 text-emerald-600 border border-emerald-500/30' : 'bg-amber-500/10 text-amber-700 border border-amber-500/30'"
                >
                  {{ v.in_use ? 'In-Use' : 'Dangling (Unused)' }}
                </span>
              </td>

              <td class="px-4 py-3 whitespace-nowrap text-slate-700 font-bold">
                {{ v.size || 'N/A' }}
              </td>

              <td class="px-4 py-3 whitespace-nowrap text-slate-600 text-[11px]">
                {{ v.driver }} &bull; {{ v.scope }}
              </td>

              <td class="px-4 py-3 text-slate-700">
                <div v-if="v.containers && v.containers.length" class="flex flex-wrap gap-1">
                  <router-link
                    v-for="c in v.containers"
                    :key="c"
                    :to="'/containers?name=' + encodeURIComponent(c)"
                    class="rounded bg-white border border-slate-200 px-1.5 py-0.2 text-[10px] text-indigo-700 hover:text-slate-900"
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
                    class="cyber-btn-secondary !px-2.5 !py-1 text-[11px] text-cyan-700 flex items-center gap-1"
                    title="Browse and edit files inside this Docker volume"
                  >
                    <FolderOpen class="h-3 w-3" />
                    <span>Browse Files</span>
                  </button>

                  <button
                    @click="selectedItemInspect = v; inspectType = 'volume'"
                    class="cyber-btn-secondary !px-2 !py-1 text-[11px] text-slate-700"
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
      <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 bg-white/60 border border-slate-200 rounded-xl p-3 text-xs">
        <div class="flex flex-wrap items-center gap-2">
          <div class="relative w-48 sm:w-64">
            <Search class="h-3.5 w-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-slate-500" />
            <input
              v-model="imageSearch"
              type="text"
              placeholder="Search repository, tag, or ID…"
              class="w-full pl-8 pr-2.5 py-1.5 text-xs bg-slate-50 border border-slate-200 rounded-lg text-slate-800 placeholder:text-slate-500 font-mono"
            />
          </div>

          <select
            v-model="imageFilter"
            class="text-xs bg-slate-50 border border-slate-200 rounded-lg px-3 py-1.5 text-slate-700 font-mono"
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
            <Trash2 class="h-3.5 w-3.5 text-amber-600" />
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
      <div class="cyber-card !p-0 overflow-x-auto border border-slate-200 bg-slate-50">
        <table class="w-full text-xs font-mono">
          <thead>
            <tr class="border-b border-slate-200 bg-white/80 text-left text-slate-600">
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Repository:Tag</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Image ID</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Size</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Created</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Status</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">In-Use By Containers</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider text-right">Actions</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-200/60">
            <tr
              v-for="img in filteredImages"
              :key="img.id + img.repository"
              class="hover:bg-white/50 transition-colors"
            >
              <td class="px-4 py-3 text-slate-800">
                <div class="flex items-center gap-2">
                  <span
                    v-if="img.is_dangling"
                    class="px-1.5 py-0.2 rounded bg-amber-500/10 text-amber-600 border border-amber-500/30 text-[9px] uppercase shrink-0"
                  >
                    Dangling
                  </span>
                  <span class="font-bold text-slate-900 truncate max-w-xs" :title="`${img.repository}:${img.tag}`">
                    {{ img.repository }}:<span class="text-cyan-600">{{ img.tag }}</span>
                  </span>
                </div>
              </td>

              <td class="px-4 py-3 whitespace-nowrap text-slate-600 text-[11px]">
                {{ img.id.slice(0, 12) }}
              </td>

              <td class="px-4 py-3 whitespace-nowrap text-slate-700 font-bold">
                {{ img.size }}
              </td>

              <td class="px-4 py-3 whitespace-nowrap text-slate-600 text-[11px]">
                {{ img.created_since || img.created_at }}
              </td>

              <td class="px-4 py-3 whitespace-nowrap">
                <span
                  class="rounded-full px-2 py-0.5 text-[10px] font-semibold"
                  :class="img.in_use ? 'bg-emerald-500/10 text-emerald-600 border border-emerald-500/30' : 'bg-slate-100 text-slate-600 border border-slate-300'"
                >
                  {{ img.in_use ? 'In-Use' : 'Unused' }}
                </span>
              </td>

              <td class="px-4 py-3 text-slate-700">
                <div v-if="img.containers && img.containers.length" class="flex flex-wrap gap-1">
                  <router-link
                    v-for="c in img.containers"
                    :key="c"
                    :to="'/containers?name=' + encodeURIComponent(c)"
                    class="rounded bg-white border border-slate-200 px-1.5 py-0.2 text-[10px] text-indigo-700 hover:text-slate-900"
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
                    class="p-1.5 rounded-lg text-slate-600 hover:bg-slate-100 hover:text-slate-800"
                    title="Copy Image ID"
                  >
                    <Check v-if="copiedKey === img.id" class="h-3.5 w-3.5 text-emerald-600" />
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
      <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 bg-white/60 border border-slate-200 rounded-xl p-3 text-xs">
        <div class="flex flex-wrap items-center gap-2">
          <div class="relative w-48 sm:w-64">
            <Search class="h-3.5 w-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-slate-500" />
            <input
              v-model="containerSearch"
              type="text"
              placeholder="Search container or image…"
              class="w-full pl-8 pr-2.5 py-1.5 text-xs bg-slate-50 border border-slate-200 rounded-lg text-slate-800 placeholder:text-slate-500 font-mono"
            />
          </div>

          <select
            v-model="containerFilter"
            class="text-xs bg-slate-50 border border-slate-200 rounded-lg px-3 py-1.5 text-slate-700 font-mono"
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
      <div class="cyber-card !p-0 overflow-x-auto border border-slate-200 bg-slate-50">
        <table class="w-full text-xs font-mono">
          <thead>
            <tr class="border-b border-slate-200 bg-white/80 text-left text-slate-600">
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Container Name</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Image</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">State</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Writable Layer (RW)</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider">Virtual RootFS</th>
              <th class="px-4 py-3 font-semibold uppercase tracking-wider text-right">Actions</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-200/60">
            <tr
              v-for="c in filteredContainers"
              :key="c.id"
              class="hover:bg-white/50 transition-colors"
            >
              <td class="px-4 py-3 text-slate-800">
                <router-link
                  :to="'/containers?id=' + encodeURIComponent(c.id)"
                  class="font-bold text-slate-900 hover:text-indigo-600 hover:underline"
                >
                  {{ c.name }}
                </router-link>
              </td>

              <td class="px-4 py-3 text-slate-600 max-w-xs truncate" :title="c.image">
                {{ c.image }}
              </td>

              <td class="px-4 py-3 whitespace-nowrap">
                <span
                  class="rounded-full px-2 py-0.5 text-[10px] font-semibold"
                  :class="stringsEqual(c.state, 'running') ? 'bg-emerald-500/10 text-emerald-600 border border-emerald-500/30' : 'bg-slate-100 text-slate-600 border border-slate-300'"
                >
                  {{ c.state }}
                </span>
              </td>

              <td class="px-4 py-3 whitespace-nowrap text-amber-700 font-bold">
                {{ c.size_rw }}
              </td>

              <td class="px-4 py-3 whitespace-nowrap text-slate-700">
                {{ c.size_rootfs }}
              </td>

              <td class="px-4 py-3 text-right whitespace-nowrap">
                <div class="flex items-center justify-end gap-1.5">
                  <button
                    @click="browseTargetFromOutside('container', c.name)"
                    class="cyber-btn-secondary !px-2.5 !py-1 text-[11px] text-cyan-700 flex items-center gap-1"
                    title="Browse inside container filesystem"
                  >
                    <FolderOpen class="h-3 w-3" />
                    <span>Browse Files</span>
                  </button>

                  <router-link
                    :to="'/containers?id=' + encodeURIComponent(c.id)"
                    class="cyber-btn-secondary !px-2.5 !py-1 text-[11px] text-indigo-700"
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
          <div class="flex items-center justify-between border-b border-slate-200 pb-2.5">
            <h3 class="text-xs font-bold uppercase tracking-wider text-slate-800 flex items-center gap-2">
              <Layers class="h-4 w-4 text-cyan-600" />
              <span>Docker System Disk Usage (DF)</span>
            </h3>
          </div>

          <div class="space-y-2">
            <div
              v-for="df in (overview?.docker_df || [])"
              :key="df.type"
              class="flex items-center justify-between p-3 rounded-lg border border-slate-200 bg-slate-50 font-mono text-xs"
            >
              <div>
                <span class="text-slate-900 font-bold block">{{ df.type }}</span>
                <span class="text-slate-600 text-[11px]">{{ df.total_count }} total ({{ df.active_count }} active)</span>
              </div>
              <div class="text-right">
                <span class="text-slate-800 font-bold block">{{ df.size }}</span>
                <span class="text-amber-600 text-[11px] font-semibold">Reclaimable: {{ df.reclaimable }}</span>
              </div>
            </div>
          </div>
        </div>

        <!-- Build Cache Actions -->
        <div class="cyber-card space-y-4">
          <div class="flex items-center justify-between border-b border-slate-200 pb-2.5">
            <h3 class="text-xs font-bold uppercase tracking-wider text-slate-800 flex items-center gap-2">
              <Sparkles class="h-4 w-4 text-purple-600" />
              <span>Build Cache Maintenance</span>
            </h3>
          </div>

          <p class="text-xs text-slate-600 leading-relaxed">
            Docker caches layers created during image builds. Pruning build cache frees disk space safely without affecting running containers.
          </p>

          <div class="p-3 rounded-lg bg-slate-50 border border-slate-200 text-xs font-mono flex items-center justify-between">
            <span class="text-slate-600">Build Cache Entries:</span>
            <span class="text-purple-700 font-bold">{{ overview?.counts?.build_cache_count || 0 }} items</span>
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

        <!-- Complete System Prune Card (Moved from Settings) -->
        <div class="cyber-card space-y-4 md:col-span-2 border-amber-900/40 bg-white/60">
          <div class="flex items-center justify-between border-b border-slate-200 pb-2.5">
            <h3 class="text-xs font-bold uppercase tracking-wider text-amber-700 flex items-center gap-2">
              <Trash2 class="h-4 w-4 text-amber-600" />
              <span>Full Host Resource Prune (System-Wide Cleanup)</span>
            </h3>

            <button
              @click="pruneSystem"
              :disabled="systemPruning"
              class="cyber-btn-danger text-xs flex items-center gap-1.5 py-1.5 px-3"
              title="Remove stopped containers, unused networks, dangling images, and build cache"
            >
              <RotateCcw v-if="systemPruning" class="h-3.5 w-3.5 animate-spin" />
              <Trash2 v-else class="h-3.5 w-3.5" />
              <span>{{ systemPruning ? 'Pruning Host...' : 'Prune Unused Resources (-f)' }}</span>
            </button>
          </div>

          <p class="text-xs text-slate-600 leading-relaxed">
            Quickly frees up host disk space by removing all stopped containers, unused networks, and dangling image layers in a single pass. Active running containers and named volumes are safely preserved.
          </p>
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
          <div class="flex items-center justify-between border-b border-slate-200 pb-2.5">
            <h3 class="text-xs font-bold uppercase tracking-wider text-slate-800 flex items-center gap-2">
              <Database class="h-4 w-4 text-emerald-600" />
              <span>SQLite App Database</span>
            </h3>
          </div>

          <div class="space-y-2.5 text-xs font-mono">
            <div class="flex items-center justify-between p-2.5 rounded-lg bg-slate-50 border border-slate-200">
              <span class="text-slate-600">Database File Path:</span>
              <span class="text-slate-800 truncate max-w-xs">{{ overview?.docktun_storage?.db_path }}</span>
            </div>

            <div class="flex items-center justify-between p-2.5 rounded-lg bg-slate-50 border border-slate-200">
              <span class="text-slate-600">Database Size on Disk:</span>
              <span class="text-emerald-600 font-bold">{{ overview?.docktun_storage?.db_size_str }}</span>
            </div>

            <div class="flex items-center justify-between p-2.5 rounded-lg bg-slate-50 border border-slate-200">
              <span class="text-slate-600">Audit Trail Records:</span>
              <span class="text-indigo-600 font-bold">{{ overview?.docktun_storage?.audit_count }} records</span>
            </div>
          </div>

          <div v-if="vacuumMessage" class="p-3 rounded-lg border border-emerald-500/30 bg-emerald-500/10 text-emerald-700 text-xs font-mono">
            {{ vacuumMessage }}
          </div>

          <div class="pt-2 border-t border-slate-200">
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
          <div class="flex items-center justify-between border-b border-slate-200 pb-2.5">
            <h3 class="text-xs font-bold uppercase tracking-wider text-slate-800 flex items-center gap-2">
              <FolderOpen class="h-4 w-4 text-cyan-600" />
              <span>Tunnel Backups & Configs</span>
            </h3>
          </div>

          <p class="text-xs text-slate-600 leading-relaxed">
            DockTun automatically creates timestamped YAML backups every time an ingress route is modified.
          </p>

          <div class="space-y-2.5 text-xs font-mono">
            <div class="flex items-center justify-between p-2.5 rounded-lg bg-slate-50 border border-slate-200">
              <span class="text-slate-600">Total Backups Count:</span>
              <span class="text-cyan-600 font-bold">{{ overview?.docktun_storage?.backups_count || 0 }} files</span>
            </div>

            <div class="flex items-center justify-between p-2.5 rounded-lg bg-slate-50 border border-slate-200">
              <span class="text-slate-600">Total Backups Disk Size:</span>
              <span class="text-slate-800 font-bold">{{ overview?.docktun_storage?.backups_str || '0 B' }}</span>
            </div>
          </div>

          <div class="pt-2 border-t border-slate-200">
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
      <div class="cyber-card max-w-4xl w-full h-[85vh] flex flex-col p-4 sm:p-5 space-y-3 border-indigo-500/50 bg-slate-50">
        <!-- Editor Header -->
        <div class="flex items-center justify-between border-b border-slate-200 pb-3 shrink-0">
          <div class="flex items-center gap-2 truncate pr-2">
            <FileCode class="h-5 w-5 text-indigo-600 shrink-0" />
            <div class="truncate">
              <h3 class="text-sm font-bold text-slate-900 font-mono truncate">
                {{ activeEditorFile.name }}
              </h3>
              <p class="text-[11px] text-slate-600 font-mono truncate">
                {{ selectedTarget?.name }} : {{ activeEditorFile.path }} ({{ activeEditorFile.size_str }})
              </p>
            </div>
          </div>

          <div class="flex items-center gap-2 shrink-0">
            <button
              @click="downloadStorageFile(activeEditorFile)"
              class="cyber-btn-secondary !py-1 !px-2.5 text-xs flex items-center gap-1 text-slate-700"
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
              class="p-1.5 rounded-lg text-slate-600 hover:text-slate-900 hover:bg-slate-100"
            >
              <X class="h-4 w-4" />
            </button>
          </div>
        </div>

        <!-- Editor Code Textarea -->
        <div class="flex-1 min-h-0 relative rounded-lg border border-slate-200 bg-slate-50 overflow-hidden">
          <textarea
            v-model="editorContent"
            spellcheck="false"
            class="w-full h-full p-4 font-mono text-xs bg-slate-50 text-slate-900 resize-none focus:outline-none focus:ring-1 focus:ring-indigo-500/40 leading-relaxed selection:bg-indigo-600/40"
          ></textarea>
        </div>

        <!-- Editor Footer -->
        <div class="flex items-center justify-between text-[11px] font-mono text-slate-600 shrink-0 pt-1">
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
        <div class="flex items-center justify-between border-b border-slate-200 pb-3">
          <div class="flex items-center gap-2">
            <component :is="newItemIsDir ? FolderPlus : FilePlus" class="h-5 w-5 text-indigo-600" />
            <h3 class="text-sm font-bold text-slate-900 font-mono">
              Create New {{ newItemIsDir ? 'Directory' : 'File' }}
            </h3>
          </div>
          <button @click="showNewItemModal = false" class="text-slate-600 hover:text-slate-900">
            <X class="h-4 w-4" />
          </button>
        </div>

        <div class="space-y-3 text-xs font-mono">
          <div class="p-2.5 rounded bg-slate-50 border border-slate-200 text-slate-600">
            <span>Location: </span>
            <span class="text-slate-900">{{ selectedTarget?.name }}/{{ currentPath || '(root)' }}</span>
          </div>

          <div class="space-y-1">
            <label class="text-[11px] text-slate-700 font-semibold">{{ newItemIsDir ? 'Folder Name' : 'File Name (e.g. config.json, script.sh)' }}</label>
            <input
              v-model="newItemName"
              @keydown.enter="executeCreateItem"
              type="text"
              autofocus
              :placeholder="newItemIsDir ? 'my-folder' : 'config.yml'"
              class="w-full px-3 py-2 bg-slate-50 border border-slate-200 rounded-lg text-slate-900 placeholder:text-slate-500 focus:outline-none focus:border-indigo-500 font-mono text-xs"
            />
          </div>
        </div>

        <div class="flex items-center justify-end gap-2 pt-2 border-t border-slate-200">
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
        <div class="flex items-center justify-between border-b border-slate-200 pb-3">
          <div class="flex items-center gap-2">
            <Upload class="h-5 w-5 text-cyan-600" />
            <h3 class="text-sm font-bold text-slate-900 font-mono">
              Upload File to Storage
            </h3>
          </div>
          <button @click="showUploadModal = false" class="text-slate-600 hover:text-slate-900">
            <X class="h-4 w-4" />
          </button>
        </div>

        <div class="space-y-3 text-xs font-mono">
          <div class="p-2.5 rounded bg-slate-50 border border-slate-200 text-slate-600">
            <span>Destination: </span>
            <span class="text-slate-900">{{ selectedTarget?.name }}/{{ currentPath || '(root)' }}</span>
          </div>

          <div class="space-y-1">
            <label class="text-[11px] text-slate-700 font-semibold">Select File from Computer</label>
            <input
              type="file"
              @change="onFileSelected"
              class="w-full text-xs text-slate-700 file:mr-3 file:py-2 file:px-3 file:rounded-lg file:border-0 file:text-xs file:font-semibold file:bg-indigo-600 file:text-slate-900 hover:file:bg-indigo-500 cursor-pointer"
            />
          </div>

          <div v-if="selectedUploadFile" class="text-[11px] text-emerald-600">
            Ready to upload: {{ selectedUploadFile.name }} ({{ Math.round(selectedUploadFile.size / 1024) }} KB)
          </div>
        </div>

        <div class="flex items-center justify-end gap-2 pt-2 border-t border-slate-200">
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
        <div class="flex items-center justify-between border-b border-slate-200 pb-3">
          <div class="flex items-center gap-2">
            <Sparkles class="h-5 w-5 text-amber-600" />
            <div>
              <h3 class="text-sm font-bold text-slate-900 font-mono">Smart Disk Reclaim</h3>
              <p class="text-[11px] text-slate-600">Select resources to safely clean up</p>
            </div>
          </div>
          <button @click="showReclaimModal = false" class="text-slate-600 hover:text-slate-900">
            <X class="h-4 w-4" />
          </button>
        </div>

        <div class="space-y-3 text-xs">
          <!-- Option 1: Dangling Images -->
          <label class="flex items-start gap-3 p-3 rounded-lg border border-slate-200 bg-slate-50 hover:bg-white cursor-pointer">
            <input type="checkbox" v-model="reclaimOptions.prune_images_dangling" class="mt-0.5 rounded bg-slate-100 text-indigo-600" />
            <div>
              <span class="font-bold text-slate-900 block">Prune Dangling Images (&lt;none&gt;)</span>
              <span class="text-slate-600 text-[11px]">Removes untagged image layers left over from builds. Completely safe.</span>
            </div>
          </label>

          <!-- Option 2: All Unused Images -->
          <label class="flex items-start gap-3 p-3 rounded-lg border border-slate-200 bg-slate-50 hover:bg-white cursor-pointer">
            <input type="checkbox" v-model="reclaimOptions.prune_images_all" class="mt-0.5 rounded bg-slate-100 text-indigo-600" />
            <div>
              <span class="font-bold text-slate-900 block">Prune All Unused Images</span>
              <span class="text-slate-600 text-[11px]">Removes any image that does not have an active container currently running.</span>
            </div>
          </label>

          <!-- Option 3: Stopped Containers -->
          <label class="flex items-start gap-3 p-3 rounded-lg border border-slate-200 bg-slate-50 hover:bg-white cursor-pointer">
            <input type="checkbox" v-model="reclaimOptions.prune_containers" class="mt-0.5 rounded bg-slate-100 text-indigo-600" />
            <div>
              <span class="font-bold text-slate-900 block">Prune Stopped & Exited Containers</span>
              <span class="text-slate-600 text-[11px]">Cleans up old container execution instances that are stopped.</span>
            </div>
          </label>

          <!-- Option 4: Build Cache -->
          <label class="flex items-start gap-3 p-3 rounded-lg border border-slate-200 bg-slate-50 hover:bg-white cursor-pointer">
            <input type="checkbox" v-model="reclaimOptions.prune_build_cache" class="mt-0.5 rounded bg-slate-100 text-indigo-600" />
            <div>
              <span class="font-bold text-slate-900 block">Prune Docker Build Cache</span>
              <span class="text-slate-600 text-[11px]">Cleans up cached build steps and layers. Completely safe.</span>
            </div>
          </label>

          <!-- Option 5: Dangling Volumes (With Warning) -->
          <label class="flex items-start gap-3 p-3 rounded-lg border border-amber-900/50 bg-amber-50/20 hover:bg-amber-50/30 cursor-pointer">
            <input type="checkbox" v-model="reclaimOptions.prune_volumes" class="mt-0.5 rounded bg-slate-100 text-amber-500" />
            <div>
              <span class="font-bold text-amber-700 block flex items-center gap-1.5">
                <AlertTriangle class="h-3.5 w-3.5 text-amber-600" />
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
          <label class="text-[10px] uppercase font-mono text-slate-600">Execution Output</label>
          <pre class="p-2.5 rounded-lg bg-slate-50 border border-slate-200 text-[11px] font-mono text-slate-700 max-h-40 overflow-y-auto whitespace-pre-wrap">{{ reclaimResults.join('\n\n') }}</pre>
        </div>

        <div class="flex items-center justify-between pt-3 border-t border-slate-200">
          <button @click="showReclaimModal = false" class="cyber-btn-secondary text-xs">
            Close
          </button>

          <button
            @click="executeBatchReclaim"
            :disabled="reclaimLoading"
            class="cyber-btn flex items-center gap-2 bg-amber-600 hover:bg-amber-500 text-slate-900 font-semibold"
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
        <div class="flex items-center justify-between border-b border-slate-200 pb-3">
          <div class="flex items-center gap-2">
            <component :is="inspectType === 'volume' ? HardDrive : Layers" class="h-5 w-5 text-indigo-600" />
            <h3 class="text-sm font-bold text-slate-900 font-mono">
              {{ inspectType === 'volume' ? 'Volume Details' : 'Image Details' }}
            </h3>
          </div>
          <button @click="selectedItemInspect = null" class="text-slate-600 hover:text-slate-900">
            <X class="h-4 w-4" />
          </button>
        </div>

        <!-- Volume Inspector -->
        <div v-if="inspectType === 'volume'" class="space-y-2 text-xs font-mono">
          <div class="p-2.5 rounded bg-slate-50 border border-slate-200">
            <span class="text-slate-600 block text-[10px] uppercase">Name</span>
            <span class="text-slate-900 font-bold break-all">{{ selectedItemInspect.name }}</span>
          </div>

          <div class="p-2.5 rounded bg-slate-50 border border-slate-200">
            <span class="text-slate-600 block text-[10px] uppercase">Mountpoint on Host</span>
            <span class="text-cyan-700 break-all">{{ selectedItemInspect.mountpoint }}</span>
          </div>

          <div class="grid grid-cols-2 gap-2">
            <div class="p-2.5 rounded bg-slate-50 border border-slate-200">
              <span class="text-slate-600 block text-[10px] uppercase">Driver</span>
              <span class="text-slate-800">{{ selectedItemInspect.driver }}</span>
            </div>
            <div class="p-2.5 rounded bg-slate-50 border border-slate-200">
              <span class="text-slate-600 block text-[10px] uppercase">Scope</span>
              <span class="text-slate-800">{{ selectedItemInspect.scope }}</span>
            </div>
          </div>

          <div class="p-2.5 rounded bg-slate-50 border border-slate-200">
            <span class="text-slate-600 block text-[10px] uppercase">Attached Containers</span>
            <span v-if="selectedItemInspect.containers && selectedItemInspect.containers.length" class="text-emerald-600">
              {{ selectedItemInspect.containers.join(', ') }}
            </span>
            <span v-else class="text-amber-600 font-semibold">None (Dangling / Unmounted)</span>
          </div>
        </div>

        <div class="flex justify-end pt-2 border-t border-slate-200">
          <button @click="selectedItemInspect = null" class="cyber-btn-secondary text-xs">
            Close
          </button>
        </div>
      </div>
    </div>

    <!-- ===================================================================== -->
    <!-- MODAL: DETAILED PRE-DELETION INFORMATION & CONFIRMATION               -->
    <!-- ===================================================================== -->
    <div
      v-if="pendingDelete"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/85 backdrop-blur-md p-4"
      @click.self="pendingDelete = null"
    >
      <div class="cyber-card max-w-xl w-full border-rose-500/50 bg-slate-50 p-5 space-y-4 shadow-sm">
        <!-- Header -->
        <div class="flex items-start justify-between border-b border-slate-200 pb-3">
          <div class="flex items-center gap-2.5">
            <div class="p-2 rounded-lg bg-rose-500/10 border border-rose-500/30 text-rose-600">
              <AlertTriangle class="h-5 w-5" />
            </div>
            <div>
              <h3 class="text-sm font-bold text-slate-900 font-mono">{{ pendingDelete.title }}</h3>
              <p class="text-[11px] text-slate-600 font-mono">{{ pendingDelete.subtitle || 'Review before final deletion' }}</p>
            </div>
          </div>
          <button @click="pendingDelete = null" class="text-slate-600 hover:text-slate-900">
            <X class="h-4 w-4" />
          </button>
        </div>

        <!-- Details Grid -->
        <div class="space-y-2 text-xs font-mono">
          <div
            v-for="(det, idx) in pendingDelete.details"
            :key="idx"
            class="flex items-start justify-between p-2 rounded bg-white/60 border border-slate-200/80 gap-2"
          >
            <span class="text-slate-600 shrink-0">{{ det.label }}:</span>
            <span
              class="text-right break-all font-semibold"
              :class="det.highlight ? 'text-amber-700' : 'text-slate-800'"
            >
              {{ det.value }}
            </span>
          </div>
        </div>

        <!-- Scrollable List of Affected Items (if pruning bulk items) -->
        <div v-if="pendingDelete.itemsList && pendingDelete.itemsList.length" class="space-y-1.5">
          <div class="flex items-center justify-between text-[11px] font-mono text-slate-600">
            <span>Items to be Permanently Removed ({{ pendingDelete.itemsList.length }}):</span>
          </div>
          <div class="max-h-44 overflow-y-auto space-y-1 border border-slate-200 rounded-lg p-2 bg-white/40 text-xs font-mono">
            <div
              v-for="(it, i) in pendingDelete.itemsList"
              :key="i"
              class="flex items-center justify-between py-1 px-1.5 rounded hover:bg-slate-100/50"
            >
              <span class="text-slate-800 truncate pr-2" :title="it.name">{{ it.name }}</span>
              <div class="flex items-center gap-2 shrink-0">
                <span v-if="it.info" class="text-[10px] text-slate-500">{{ it.info }}</span>
                <span class="text-amber-600 font-bold text-[11px]">{{ it.size }}</span>
              </div>
            </div>
          </div>
        </div>

        <!-- High-Impact Danger Callout -->
        <div class="p-3 rounded-lg border border-rose-500/30 bg-rose-50/20 text-xs font-mono flex items-start gap-2.5 text-rose-700">
          <ShieldAlert class="h-4 w-4 shrink-0 text-rose-600 mt-0.5" />
          <div class="leading-relaxed">
            {{ pendingDelete.warning }}
          </div>
        </div>

        <!-- Footer Actions -->
        <div class="flex items-center justify-between pt-3 border-t border-slate-200">
          <button
            @click="pendingDelete = null"
            :disabled="deletingInProgress"
            class="cyber-btn-secondary text-xs"
          >
            Cancel
          </button>

          <button
            @click="executePendingDelete"
            :disabled="deletingInProgress"
            class="cyber-btn flex items-center gap-2 bg-rose-600 hover:bg-rose-500 text-white font-semibold text-xs shadow-lg shadow-rose-600/20"
          >
            <RotateCcw v-if="deletingInProgress" class="h-3.5 w-3.5 animate-spin" />
            <Trash2 v-else class="h-3.5 w-3.5" />
            <span>{{ deletingInProgress ? 'Deleting...' : pendingDelete.confirmButtonText }}</span>
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
