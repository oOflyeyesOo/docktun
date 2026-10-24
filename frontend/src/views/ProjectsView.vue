<script setup lang="ts">
import { ref, computed, watch, nextTick, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import {
  Server,
  Plus,
  Trash2,
  Play,
  Square,
  RotateCcw,
  Rocket,
  Pencil,
  History,
  FileCode,
  KeyRound,
  FolderOpen,
  Terminal,
  Check,
  X,
  ArrowLeft,
  RefreshCw,
  FileText,
  Save,
  Folder,
  HardDrive,
  Sparkles,
  Package,
  Search,
  Layers
} from 'lucide-vue-next'
import { useApi } from '../composables/useApi'

const { apiFetch } = useApi()

interface Project {
  id: number
  name: string
  path: string
  repo_url: string
  git_username: string
  git_token: string
  created_at: string
}

interface FileItem {
  name: string
  path: string
  is_dir: boolean
  size: number
  mod_time: string
}

const projects = ref<Project[]>([])
const loading = ref(false)
const actionLoading = ref<Record<number, boolean>>({})
const showForm = ref(false)
const editingId = ref<number | null>(null)
const form = ref({ name: '', path: '', repo_url: '', git_username: '', git_token: '' })
const deployRef = ref('')
const deployTarget = ref<number | null>(null)
const output = ref('')
const outputEl = ref<HTMLElement | null>(null)
const history = ref<any[]>([])
const historyTarget = ref<number | null>(null)

// --- Modal State for Project Manager (Compose, .env, Files) ---
const activeProject = ref<Project | null>(null)
const activeTab = ref<'compose' | 'env' | 'files'>('compose')

// Compose State
const composeFilename = ref('docker-compose.yml')
const composeContent = ref('')
const composeLoading = ref(false)
const composeSaving = ref(false)
const composeMsg = ref('')

// .env State
const envContent = ref('')
const envLoading = ref(false)
const envSaving = ref(false)
const envMsg = ref('')

// File Access State
const currentDir = ref('')
const fileList = ref<FileItem[]>([])
const filesLoading = ref(false)
const editingFile = ref<{ path: string; content: string } | null>(null)
const editingFileSaving = ref(false)
const editingFileMsg = ref('')
const showNewItemModal = ref(false)
const newItemName = ref('')
const newItemIsDir = ref(false)

// --- Docker Run State ---
const showDockerRun = ref(false)
const dockerRunCmd = ref('docker run -d --name my-app -p 8080:80 nginx:alpine')
const dockerRunMode = ref<'compose' | 'direct'>('compose')
const dockerRunCloudflared = ref(false)
const dockerRunCreateProject = ref(true)
const dockerRunCustomName = ref('')
const dockerRunLoading = ref(false)
const dockerRunJob = ref<any>(null)
const dockerRunLines = ref<string[]>([])
const dockerRunEl = ref<HTMLElement | null>(null)

// -------------------------------------------------------------
// Core Project Actions
// -------------------------------------------------------------
async function load() {
  loading.value = true
  try {
    const res = await apiFetch<Project[]>('/api/projects')
    projects.value = Array.isArray(res) ? res : []
  } catch {
    projects.value = []
  } finally {
    loading.value = false
  }
}

function openAdd() {
  editingId.value = null
  form.value = { name: '', path: '', repo_url: '', git_username: '', git_token: '' }
  showForm.value = true
}

function openEdit(p: Project) {
  editingId.value = p.id
  form.value = { name: p.name, path: p.path, repo_url: p.repo_url, git_username: p.git_username, git_token: '' }
  showForm.value = true
}

async function save() {
  if (!form.value.name || !form.value.path) return
  try {
    if (editingId.value) {
      await apiFetch(`/api/projects/${editingId.value}`, { method: 'PUT', body: JSON.stringify(form.value) })
    } else {
      await apiFetch('/api/projects', { method: 'POST', body: JSON.stringify(form.value) })
    }
    showForm.value = false
    load()
  } catch {}
}

async function remove(id: number) {
  if (!confirm('Delete this project?')) return
  await apiFetch(`/api/projects/${id}`, { method: 'DELETE' })
  load()
}

async function action(id: number, act: string, body?: object) {
  if (actionLoading.value[id]) return
  actionLoading.value[id] = true
  output.value = ''
  try {
    await apiFetch(`/api/projects/${id}/${act}`, {
      method: 'POST',
      body: body ? JSON.stringify(body) : undefined,
    })
    const poll = setInterval(async () => {
      try {
        const res: any = await apiFetch(`/api/projects/${id}/output`)
        output.value = (res.lines || []).join('\n')
        nextTick(() => {
          if (outputEl.value) outputEl.value.scrollTop = outputEl.value.scrollHeight
        })
        if (res.done) {
          clearInterval(poll)
          actionLoading.value[id] = false
          deployTarget.value = null
          deployRef.value = ''
        }
      } catch {
        clearInterval(poll)
        actionLoading.value[id] = false
      }
    }, 500)
  } catch (e: any) {
    output.value = e.message
    actionLoading.value[id] = false
    deployTarget.value = null
    deployRef.value = ''
  }
}

function startDeploy(p: Project) {
  if (!p.repo_url) {
    action(p.id, 'deploy', {})
    return
  }
  deployTarget.value = p.id
  deployRef.value = ''
}

function confirmDeploy() {
  if (deployTarget.value && deployRef.value) {
    action(deployTarget.value, 'deploy', { ref: deployRef.value })
  }
}

async function toggleHistory(id: number) {
  if (historyTarget.value === id) {
    historyTarget.value = null
    return
  }
  historyTarget.value = id
  try {
    history.value = await apiFetch(`/api/projects/${id}/history`)
  } catch {
    history.value = []
  }
}

// -------------------------------------------------------------
// Compose & .env & File Access Modal
// -------------------------------------------------------------
function openProjectManager(p: Project, tab: 'compose' | 'env' | 'files') {
  activeProject.value = p
  activeTab.value = tab
  if (tab === 'compose') loadCompose(p.id)
  else if (tab === 'env') loadEnv(p.id)
  else if (tab === 'files') loadFiles(p.id, '')
}

function closeProjectManager() {
  activeProject.value = null
  editingFile.value = null
  composeMsg.value = ''
  envMsg.value = ''
  editingFileMsg.value = ''
}

// --- Compose Functions ---
async function loadCompose(id: number) {
  composeLoading.value = true
  composeMsg.value = ''
  try {
    const res: any = await apiFetch(`/api/projects/${id}/compose`)
    composeFilename.value = res.filename || 'docker-compose.yml'
    composeContent.value = res.content || ''
  } catch (e: any) {
    composeMsg.value = 'Failed to load compose: ' + e.message
  } finally {
    composeLoading.value = false
  }
}

async function saveCompose(restart = false) {
  if (!activeProject.value) return
  composeSaving.value = true
  composeMsg.value = ''
  try {
    await apiFetch(`/api/projects/${activeProject.value.id}/compose`, {
      method: 'PUT',
      body: JSON.stringify({
        filename: composeFilename.value,
        content: composeContent.value,
      }),
    })
    composeMsg.value = 'Saved successfully!'
    if (restart) {
      action(activeProject.value.id, 'restart')
      closeProjectManager()
    } else {
      setTimeout(() => (composeMsg.value = ''), 3000)
    }
  } catch (e: any) {
    composeMsg.value = 'Save failed: ' + e.message
  } finally {
    composeSaving.value = false
  }
}

// --- .env Functions ---
async function loadEnv(id: number) {
  envLoading.value = true
  envMsg.value = ''
  try {
    const res: any = await apiFetch(`/api/projects/${id}/env`)
    envContent.value = res.content || ''
  } catch (e: any) {
    envMsg.value = 'Failed to load .env: ' + e.message
  } finally {
    envLoading.value = false
  }
}

async function saveEnv(restart = false) {
  if (!activeProject.value) return
  envSaving.value = true
  envMsg.value = ''
  try {
    await apiFetch(`/api/projects/${activeProject.value.id}/env`, {
      method: 'PUT',
      body: JSON.stringify({ content: envContent.value }),
    })
    envMsg.value = 'Saved successfully!'
    if (restart) {
      action(activeProject.value.id, 'restart')
      closeProjectManager()
    } else {
      setTimeout(() => (envMsg.value = ''), 3000)
    }
  } catch (e: any) {
    envMsg.value = 'Save failed: ' + e.message
  } finally {
    envSaving.value = false
  }
}

// --- File Access Functions ---
async function loadFiles(id: number, path = '') {
  filesLoading.value = true
  currentDir.value = path
  editingFile.value = null
  try {
    const res: any = await apiFetch(`/api/projects/${id}/files?path=${encodeURIComponent(path)}`)
    fileList.value = res || []
  } catch {
    fileList.value = []
  } finally {
    filesLoading.value = false
  }
}

async function openFile(f: FileItem) {
  if (!activeProject.value) return
  if (f.is_dir) {
    loadFiles(activeProject.value.id, f.path)
    return
  }
  filesLoading.value = true
  try {
    const res: any = await apiFetch(`/api/projects/${activeProject.value.id}/file?path=${encodeURIComponent(f.path)}`)
    editingFile.value = { path: f.path, content: res.content || '' }
  } catch (e: any) {
    alert('Failed to read file: ' + e.message)
  } finally {
    filesLoading.value = false
  }
}

async function saveCurrentFile() {
  if (!activeProject.value || !editingFile.value) return
  editingFileSaving.value = true
  editingFileMsg.value = ''
  try {
    await apiFetch(`/api/projects/${activeProject.value.id}/file`, {
      method: 'PUT',
      body: JSON.stringify({
        path: editingFile.value.path,
        content: editingFile.value.content,
      }),
    })
    editingFileMsg.value = 'File saved!'
    setTimeout(() => (editingFileMsg.value = ''), 3000)
  } catch (e: any) {
    editingFileMsg.value = 'Failed to save: ' + e.message
  } finally {
    editingFileSaving.value = false
  }
}

async function deleteFileItem(f: FileItem) {
  if (!activeProject.value) return
  const msg = f.is_dir ? `Delete directory '${f.name}' and all contents?` : `Delete file '${f.name}'?`
  if (!confirm(msg)) return
  try {
    await apiFetch(`/api/projects/${activeProject.value.id}/file?path=${encodeURIComponent(f.path)}`, {
      method: 'DELETE',
    })
    loadFiles(activeProject.value.id, currentDir.value)
  } catch (e: any) {
    alert('Delete failed: ' + e.message)
  }
}

async function createNewFileItem() {
  if (!activeProject.value || !newItemName.value.trim()) return
  const targetPath = currentDir.value
    ? currentDir.value + '/' + newItemName.value.trim()
    : newItemName.value.trim()
  try {
    await apiFetch(`/api/projects/${activeProject.value.id}/file`, {
      method: 'POST',
      body: JSON.stringify({
        path: targetPath,
        content: '',
        is_dir: newItemIsDir.value,
      }),
    })
    showNewItemModal.value = false
    newItemName.value = ''
    loadFiles(activeProject.value.id, currentDir.value)
  } catch (e: any) {
    alert('Failed to create: ' + e.message)
  }
}

function navigateUp() {
  if (!activeProject.value || !currentDir.value) return
  const parts = currentDir.value.split('/')
  parts.pop()
  loadFiles(activeProject.value.id, parts.join('/'))
}

function formatBytes(bytes: number) {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
}

// -------------------------------------------------------------
// Docker Run Modal
// -------------------------------------------------------------
function openDockerRun() {
  showDockerRun.value = true
  dockerRunLines.value = []
  dockerRunJob.value = null
}

function insertSnippet(snip: string) {
  dockerRunCmd.value = (dockerRunCmd.value.trim() + ' ' + snip).trim()
}

async function runDockerCommand() {
  if (!dockerRunCmd.value.trim()) return
  dockerRunLoading.value = true
  dockerRunLines.value = []
  dockerRunJob.value = null

  try {
    const res: any = await apiFetch('/api/docker/run', {
      method: 'POST',
      body: JSON.stringify({
        command: dockerRunCmd.value.trim(),
        mode: dockerRunMode.value,
        connect_cloudflared: dockerRunCloudflared.value,
        create_project: dockerRunCreateProject.value,
        project_name: dockerRunCustomName.value.trim(),
      }),
    })

    const jobId = res.job_id
    const poll = setInterval(async () => {
      try {
        const job: any = await apiFetch(`/api/docker/run/output/${jobId}`)
        dockerRunJob.value = job
        dockerRunLines.value = job.lines || []
        nextTick(() => {
          if (dockerRunEl.value) dockerRunEl.value.scrollTop = dockerRunEl.value.scrollHeight
        })
        if (job.done) {
          clearInterval(poll)
          dockerRunLoading.value = false
          load() // refresh projects
        }
      } catch {
        clearInterval(poll)
        dockerRunLoading.value = false
      }
    }, 500)
  } catch (e: any) {
    dockerRunLines.value = ['[ERROR] ' + e.message]
    dockerRunLoading.value = false
  }
}

const route = useRoute()

function checkRouteParams() {
  if (route.query.id) {
    const targetId = Number(route.query.id)
    const p = projects.value.find((item) => item.id === targetId)
    if (p) {
      openProjectManager(p, (route.query.tab as any) || 'compose')
    }
  }
}

watch(() => route.query, () => {
  checkRouteParams()
})

// -------------------------------------------------------------
// App Templates / App Catalog Store
// -------------------------------------------------------------
interface AppTemplate {
  id: string
  name: string
  title: string
  category: string
  description: string
  defaultPort: number
  compose: string
}

const showCatalog = ref(false)
const selectedTemplate = ref<AppTemplate | null>(null)
const catalogSearch = ref('')
const catalogCategory = ref('All')
const templateForm = ref({ name: '', path: '', compose: '' })
const deployingTemplate = ref(false)

const appTemplates: AppTemplate[] = [
  {
    id: 'vaultwarden',
    name: 'vaultwarden',
    title: 'Vaultwarden',
    category: 'Security',
    description: 'Lightweight Bitwarden-compatible password & secrets manager in Rust.',
    defaultPort: 80,
    compose: `services:
  vaultwarden:
    image: vaultwarden/server:latest
    container_name: vaultwarden
    restart: unless-stopped
    environment:
      - WEBSOCKET_ENABLED=true
    volumes:
      - ./data:/data
    networks:
      - cloudflared

networks:
  cloudflared:
    external: true`,
  },
  {
    id: 'uptime-kuma',
    name: 'uptime-kuma',
    title: 'Uptime Kuma',
    category: 'Monitoring',
    description: 'Self-hosted monitoring tool with status pages, alerts, and ping charts.',
    defaultPort: 3001,
    compose: `services:
  uptime-kuma:
    image: louislam/uptime-kuma:1
    container_name: uptime-kuma
    restart: unless-stopped
    volumes:
      - ./data:/app/data
    networks:
      - cloudflared

networks:
  cloudflared:
    external: true`,
  },
  {
    id: 'it-tools',
    name: 'it-tools',
    title: 'IT-Tools',
    category: 'Utilities',
    description: '50+ handy tools for developers and sysadmins (JWT, Hash, UUID, Base64).',
    defaultPort: 80,
    compose: `services:
  it-tools:
    image: corentinth/it-tools:latest
    container_name: it-tools
    restart: unless-stopped
    networks:
      - cloudflared

networks:
  cloudflared:
    external: true`,
  },
  {
    id: 'nextcloud',
    name: 'nextcloud',
    title: 'Nextcloud Hub',
    category: 'Productivity',
    description: 'Self-hosted productivity suite for cloud files, docs, calendar, and contacts.',
    defaultPort: 80,
    compose: `services:
  nextcloud:
    image: nextcloud:apache
    container_name: nextcloud
    restart: unless-stopped
    environment:
      - OVERWRITEPROTOCOL=https
    volumes:
      - ./data:/var/www/html
    networks:
      - cloudflared

networks:
  cloudflared:
    external: true`,
  },
  {
    id: 'nginx-proxy-manager',
    name: 'nginx-proxy-manager',
    title: 'Nginx Proxy Manager',
    category: 'Networking',
    description: 'Reverse proxy management dashboard with free Let’s Encrypt SSL UI.',
    defaultPort: 81,
    compose: `services:
  npm:
    image: jc21/nginx-proxy-manager:latest
    container_name: nginx-proxy-manager
    restart: unless-stopped
    ports:
      - "80:80"
      - "81:81"
      - "443:443"
    volumes:
      - ./data:/data
      - ./letsencrypt:/etc/letsencrypt
    networks:
      - cloudflared

networks:
  cloudflared:
    external: true`,
  },
  {
    id: 'jellyfin',
    name: 'jellyfin',
    title: 'Jellyfin Media System',
    category: 'Media',
    description: 'The free software media system for movies, shows, music, and live TV.',
    defaultPort: 8096,
    compose: `services:
  jellyfin:
    image: jellyfin/jellyfin:latest
    container_name: jellyfin
    restart: unless-stopped
    volumes:
      - ./config:/config
      - ./cache:/cache
      - ./media:/media
    networks:
      - cloudflared

networks:
  cloudflared:
    external: true`,
  },
  {
    id: 'homeassistant',
    name: 'homeassistant',
    title: 'Home Assistant',
    category: 'Smart Home',
    description: 'Open source home automation putting local control and privacy first.',
    defaultPort: 8123,
    compose: `services:
  homeassistant:
    image: ghcr.io/home-assistant/home-assistant:stable
    container_name: homeassistant
    restart: unless-stopped
    volumes:
      - ./config:/config
    networks:
      - cloudflared

networks:
  cloudflared:
    external: true`,
  },
  {
    id: 'dozzle',
    name: 'dozzle',
    title: 'Dozzle Log Viewer',
    category: 'Monitoring',
    description: 'Simple, lightweight real-time log viewer for Docker containers.',
    defaultPort: 8080,
    compose: `services:
  dozzle:
    image: amir20/dozzle:latest
    container_name: dozzle
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    networks:
      - cloudflared

networks:
  cloudflared:
    external: true`,
  },
  {
    id: 'filebrowser',
    name: 'filebrowser',
    title: 'FileBrowser',
    category: 'Utilities',
    description: 'Web-based file manager to upload, download, and share files securely.',
    defaultPort: 80,
    compose: `services:
  filebrowser:
    image: filebrowser/filebrowser:latest
    container_name: filebrowser
    restart: unless-stopped
    volumes:
      - ./srv:/srv
      - ./data/filebrowser.db:/database/filebrowser.db
      - ./data/settings.json:/config/settings.json
    networks:
      - cloudflared

networks:
  cloudflared:
    external: true`,
  },
  {
    id: 'ghost',
    name: 'ghost',
    title: 'Ghost CMS',
    category: 'Publishing',
    description: 'Professional publishing platform for independent creators & blogs.',
    defaultPort: 2368,
    compose: `services:
  ghost:
    image: ghost:5-alpine
    container_name: ghost
    restart: unless-stopped
    environment:
      - NODE_ENV=production
    volumes:
      - ./content:/var/lib/ghost/content
    networks:
      - cloudflared

networks:
  cloudflared:
    external: true`,
  },
]

const catalogCategories = ['All', 'Security', 'Monitoring', 'Utilities', 'Productivity', 'Media', 'Smart Home', 'Publishing', 'Networking']

const filteredTemplates = computed(() => {
  return appTemplates.filter((t) => {
    if (catalogCategory.value !== 'All' && t.category !== catalogCategory.value) return false
    if (!catalogSearch.value.trim()) return true
    const q = catalogSearch.value.toLowerCase()
    return t.title.toLowerCase().includes(q) || t.description.toLowerCase().includes(q) || t.category.toLowerCase().includes(q)
  })
})

function openCatalog() {
  selectedTemplate.value = null
  catalogSearch.value = ''
  catalogCategory.value = 'All'
  showCatalog.value = true
}

function selectTemplate(t: AppTemplate) {
  selectedTemplate.value = t
  templateForm.value = {
    name: t.name,
    path: `/opt/projects/${t.name}`,
    compose: t.compose,
  }
}

async function deployFromTemplate() {
  if (!templateForm.value.name || !templateForm.value.path || !templateForm.value.compose) return
  deployingTemplate.value = true
  output.value = `// DEPLOYING APP TEMPLATE: ${templateForm.value.name.toUpperCase()}...\n`
  try {
    const proj: any = await apiFetch('/api/projects', {
      method: 'POST',
      body: JSON.stringify({
        name: templateForm.value.name,
        path: templateForm.value.path,
      }),
    })
    const projId = proj.id

    await apiFetch(`/api/projects/${projId}/compose`, {
      method: 'PUT',
      body: JSON.stringify({
        filename: 'docker-compose.yml',
        content: templateForm.value.compose,
      }),
    })

    await apiFetch(`/api/projects/${projId}/up`, { method: 'POST' })
    showCatalog.value = false
    selectedTemplate.value = null
    await load()

    const poll = setInterval(async () => {
      try {
        const res: any = await apiFetch(`/api/projects/${projId}/output`)
        output.value = (res.lines || []).join('\n')
        if (outputEl.value) outputEl.value.scrollTop = outputEl.value.scrollHeight
        if (res.done) clearInterval(poll)
      } catch {
        clearInterval(poll)
      }
    }, 1500)
  } catch (err: any) {
    alert('Failed to deploy template: ' + (err.message || err))
  } finally {
    deployingTemplate.value = false
  }
}

onMounted(async () => {
  await load()
  checkRouteParams()
})
</script>

<template>
  <div class="space-y-6">
    <!-- Top Header -->
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h1 class="cyber-heading text-xl lg:text-2xl">Projects & Docker</h1>
        <p class="text-xs text-muted font-mono mt-1">// Manage Compose stacks, containers, and volumes</p>
      </div>
      <div class="flex items-center gap-2">
        <button @click="openCatalog" class="cyber-btn flex items-center gap-2 text-cyan-700 border-cyan-800 bg-cyan-950/40 hover:bg-cyan-900/50" title="Deploy pre-configured popular apps with 1 click">
          <Sparkles class="w-4 h-4 text-cyan-600" /> App Catalog
        </button>
        <button @click="openDockerRun" class="cyber-btn flex items-center gap-2">
          <Terminal class="w-4 h-4" :stroke-width="2" /> Docker Run
        </button>
        <button @click="openAdd" class="cyber-btn-secondary flex items-center gap-2">
          <Plus class="w-4 h-4" :stroke-width="1.5" /> Add Project
        </button>
      </div>
    </div>

    <!-- Add/Edit Form -->
    <div v-if="showForm" class="cyber-card space-y-3">
      <p class="text-sm text-neon uppercase tracking-wider font-mono">{{ editingId ? 'Edit' : 'New' }} Project</p>
      <input v-model="form.name" placeholder="Project name (e.g. vaultwarden)" class="cyber-input w-full" />
      <input v-model="form.path" placeholder="/opt/projects/vaultwarden" class="cyber-input w-full" />
      <input v-model="form.repo_url" placeholder="https://github.com/user/repo.git (optional)" class="cyber-input w-full" />
      <div class="grid grid-cols-2 gap-2">
        <input v-model="form.git_username" placeholder="Git username (optional)" class="cyber-input" />
        <input v-model="form.git_token" type="password" placeholder="Git token (optional)" class="cyber-input" />
      </div>
      <div class="flex gap-2">
        <button @click="save" class="cyber-btn flex items-center gap-2">
          <Plus class="w-4 h-4" /> {{ editingId ? 'Save' : 'Create' }}
        </button>
        <button @click="showForm = false" class="cyber-btn-secondary">Cancel</button>
      </div>
    </div>

    <!-- Deploy Modal -->
    <div v-if="deployTarget" class="cyber-card space-y-3 border-neon">
      <p class="text-sm text-neon uppercase tracking-wider font-mono">Deploy — Enter branch or tag</p>
      <div class="flex gap-2">
        <input v-model="deployRef" placeholder="main / v1.0.0" class="cyber-input flex-1" @keyup.enter="confirmDeploy" :disabled="actionLoading[deployTarget]" />
        <button @click="confirmDeploy" :disabled="!deployRef || actionLoading[deployTarget]" class="cyber-btn flex items-center gap-2">
          <Rocket class="w-4 h-4" /> Deploy
        </button>
        <button @click="deployTarget = null" :disabled="actionLoading[deployTarget]" class="cyber-btn-secondary">Cancel</button>
      </div>
    </div>

    <!-- Output Stream Panel -->
    <div v-if="output || Object.values(actionLoading).some(v => v)" class="cyber-card">
      <div class="flex items-center justify-between mb-2">
        <p v-if="Object.values(actionLoading).some(v => v)" class="text-xs text-neon animate-pulse font-mono">// RUNNING ACTION...</p>
        <p v-else class="text-xs text-muted font-mono">// OUTPUT CONSOLE</p>
        <button @click="output = ''" class="text-xs text-muted hover:text-slate-900 font-mono">[ CLEAR ]</button>
      </div>
      <pre ref="outputEl" class="text-xs text-muted font-mono whitespace-pre-wrap max-h-80 overflow-y-auto bg-void/50 p-3 border border-border/50">{{ output }}</pre>
    </div>

    <!-- Project List -->
    <div class="space-y-3">
      <div v-for="p in projects" :key="p.id" class="cyber-card hover:border-border/80 transition-all">
        <div class="flex flex-col md:flex-row md:items-center justify-between gap-4">
          <div class="min-w-0">
            <div class="flex items-center gap-2">
              <span class="inline-block w-2 h-2 rounded-full bg-neon shadow-neon"></span>
              <p class="text-sm text-slate-900 font-mono font-bold tracking-wide truncate">{{ p.name }}</p>
            </div>
            <p class="text-xs text-muted mt-1 font-mono truncate">{{ p.path }}</p>
            <p v-if="p.repo_url" class="text-xs text-muted/60 mt-0.5 font-mono truncate">{{ p.repo_url }}</p>
          </div>

          <!-- Quick Action Buttons -->
          <div class="flex flex-wrap items-center gap-1.5">
            <!-- Stack Control -->
            <button @click="action(p.id, 'up')" :disabled="actionLoading[p.id]" class="cyber-btn-sm" title="docker compose up -d">
              <Play class="w-3.5 h-3.5 text-neon" />
            </button>
            <button @click="action(p.id, 'down')" :disabled="actionLoading[p.id]" class="cyber-btn-sm" title="docker compose down">
              <Square class="w-3.5 h-3.5 text-danger" />
            </button>
            <button @click="action(p.id, 'restart')" :disabled="actionLoading[p.id]" class="cyber-btn-sm" title="docker compose restart">
              <RotateCcw class="w-3.5 h-3.5" />
            </button>
            <button @click="startDeploy(p)" :disabled="actionLoading[p.id]" class="cyber-btn-sm text-neon" title="Deploy / Pull">
              <Rocket class="w-3.5 h-3.5" />
            </button>

            <span class="w-px h-5 bg-border mx-1"></span>

            <!-- Compose & .env & Files -->
            <button @click="openProjectManager(p, 'compose')" class="cyber-btn-sm text-cyan-600" title="Edit docker-compose.yml">
              <FileCode class="w-3.5 h-3.5" />
              <span class="hidden sm:inline text-xs ml-1 font-mono">Compose</span>
            </button>
            <button @click="openProjectManager(p, 'env')" class="cyber-btn-sm text-yellow-400" title="Edit .env file">
              <KeyRound class="w-3.5 h-3.5" />
              <span class="hidden sm:inline text-xs ml-1 font-mono">.env</span>
            </button>
            <button @click="openProjectManager(p, 'files')" class="cyber-btn-sm text-purple-600" title="Project Files Explorer">
              <FolderOpen class="w-3.5 h-3.5" />
              <span class="hidden sm:inline text-xs ml-1 font-mono">Files</span>
            </button>
            <router-link
              :to="'/containers?search=' + encodeURIComponent(p.name)"
              class="cyber-btn-sm text-cyan-700 flex items-center gap-1"
              title="Inspect containers in this stack"
            >
              <Server class="w-3.5 h-3.5 text-emerald-600" />
              <span class="hidden sm:inline text-xs ml-1 font-mono">Containers</span>
            </router-link>

            <span class="w-px h-5 bg-border mx-1"></span>

            <!-- Settings / History / Delete -->
            <button @click="toggleHistory(p.id)" class="cyber-btn-sm" title="Audit History">
              <History class="w-3.5 h-3.5" />
            </button>
            <button @click="openEdit(p)" class="cyber-btn-sm" title="Edit project metadata">
              <Pencil class="w-3.5 h-3.5" />
            </button>
            <button @click="remove(p.id)" class="cyber-btn-sm text-danger hover:border-danger" title="Delete Project">
              <Trash2 class="w-3.5 h-3.5" />
            </button>
          </div>
        </div>

        <!-- History Drawer -->
        <div v-if="historyTarget === p.id" class="mt-3 border-t border-border pt-3">
          <div v-for="h in history" :key="h.id" class="py-1">
            <div class="flex justify-between text-xs text-muted" :class="h.action === 'project_deploy' && 'cursor-pointer'" @click="h.action === 'project_deploy' && (h._open = !h._open)">
              <span class="font-mono">{{ h.action }}</span>
              <span class="font-mono text-muted/70">{{ h.created_at }}</span>
            </div>
            <pre v-if="h._open && h.action === 'project_deploy'" class="text-xs text-muted/70 font-mono whitespace-pre-wrap mt-1 ml-2 border-l border-border pl-2">{{ h.detail }}</pre>
          </div>
          <p v-if="!history.length" class="text-xs text-muted font-mono">// NO HISTORY</p>
        </div>
      </div>

      <div v-if="!projects.length && !loading" class="cyber-card text-center text-muted py-12 tracking-wider text-sm font-mono">
        // NO DOCKER PROJECTS FOUND. USE "+ DOCKER RUN" OR "+ ADD PROJECT" TO GET STARTED.
      </div>
    </div>

    <!-- ============================================================= -->
    <!-- UNIFIED PROJECT MANAGER MODAL (Compose, .env, Files)           -->
    <!-- ============================================================= -->
    <div v-if="activeProject" class="fixed inset-0 z-50 bg-void/80 backdrop-blur-sm flex items-center justify-center p-4">
      <div class="cyber-card w-full max-w-4xl max-h-[90vh] flex flex-col border-neon shadow-neon p-0 overflow-hidden">
        <!-- Modal Header -->
        <div class="p-4 border-b border-border bg-surface flex items-center justify-between">
          <div>
            <div class="flex items-center gap-2">
              <span class="text-xs text-neon uppercase tracking-wider font-mono">// PROJECT MANAGER</span>
              <span class="text-muted">/</span>
              <span class="text-sm text-slate-900 font-mono font-bold">{{ activeProject.name }}</span>
            </div>
            <p class="text-xs text-muted font-mono mt-0.5 truncate">{{ activeProject.path }}</p>
          </div>
          <button @click="closeProjectManager" class="text-muted hover:text-slate-900 transition-colors">
            <X class="w-5 h-5" />
          </button>
        </div>

        <!-- Navigation Tabs -->
        <div class="flex items-center border-b border-border bg-surface/50 px-4 gap-2">
          <button
            @click="activeTab = 'compose'; loadCompose(activeProject.id)"
            class="px-4 py-2.5 text-xs font-mono uppercase tracking-wider flex items-center gap-2 transition-all border-b-2"
            :class="activeTab === 'compose' ? 'border-neon text-neon bg-neon/5' : 'border-transparent text-muted hover:text-slate-900'"
          >
            <FileCode class="w-4 h-4" /> docker-compose.yml
          </button>
          <button
            @click="activeTab = 'env'; loadEnv(activeProject.id)"
            class="px-4 py-2.5 text-xs font-mono uppercase tracking-wider flex items-center gap-2 transition-all border-b-2"
            :class="activeTab === 'env' ? 'border-neon text-neon bg-neon/5' : 'border-transparent text-muted hover:text-slate-900'"
          >
            <KeyRound class="w-4 h-4" /> .env
          </button>
          <button
            @click="activeTab = 'files'; loadFiles(activeProject.id, '')"
            class="px-4 py-2.5 text-xs font-mono uppercase tracking-wider flex items-center gap-2 transition-all border-b-2"
            :class="activeTab === 'files' ? 'border-neon text-neon bg-neon/5' : 'border-transparent text-muted hover:text-slate-900'"
          >
            <FolderOpen class="w-4 h-4" /> File Browser
          </button>
        </div>

        <!-- Tab 1: Compose Editor -->
        <div v-if="activeTab === 'compose'" class="p-4 flex-1 flex flex-col space-y-3 overflow-hidden">
          <div class="flex items-center justify-between">
            <p class="text-xs text-muted font-mono">Editing: <span class="text-cyan-600 font-bold">{{ composeFilename }}</span></p>
            <span v-if="composeMsg" class="text-xs text-neon font-mono">{{ composeMsg }}</span>
          </div>

          <textarea
            v-model="composeContent"
            rows="16"
            placeholder="# Write your docker-compose.yml here"
            class="cyber-input flex-1 w-full font-mono text-xs text-slate-900 bg-void/90 leading-relaxed p-4 border border-border focus:border-neon resize-none overflow-y-auto"
            spellcheck="false"
          ></textarea>

          <div class="flex items-center justify-between pt-2">
            <div class="flex items-center gap-2">
              <button @click="saveCompose(false)" :disabled="composeSaving" class="cyber-btn flex items-center gap-2">
                <Save class="w-4 h-4" /> Save
              </button>
              <button @click="saveCompose(true)" :disabled="composeSaving" class="cyber-btn-secondary flex items-center gap-2">
                <RotateCcw class="w-4 h-4" /> Save & Restart
              </button>
            </div>
            <button @click="loadCompose(activeProject.id)" :disabled="composeLoading" class="cyber-btn-secondary text-xs flex items-center gap-1">
              <RefreshCw class="w-3.5 h-3.5" :class="composeLoading && 'animate-spin'" /> Reload
            </button>
          </div>
        </div>

        <!-- Tab 2: .env Editor -->
        <div v-if="activeTab === 'env'" class="p-4 flex-1 flex flex-col space-y-3 overflow-hidden">
          <div class="flex items-center justify-between">
            <p class="text-xs text-muted font-mono">Environment Variables for stack (.env)</p>
            <span v-if="envMsg" class="text-xs text-neon font-mono">{{ envMsg }}</span>
          </div>

          <textarea
            v-model="envContent"
            rows="16"
            placeholder="# KEY=VALUE&#10;PORT=8080&#10;DEBUG=true"
            class="cyber-input flex-1 w-full font-mono text-xs text-yellow-300 bg-void/90 leading-relaxed p-4 border border-border focus:border-neon resize-none overflow-y-auto"
            spellcheck="false"
          ></textarea>

          <div class="flex items-center justify-between pt-2">
            <div class="flex items-center gap-2">
              <button @click="saveEnv(false)" :disabled="envSaving" class="cyber-btn flex items-center gap-2">
                <Save class="w-4 h-4" /> Save .env
              </button>
              <button @click="saveEnv(true)" :disabled="envSaving" class="cyber-btn-secondary flex items-center gap-2">
                <RotateCcw class="w-4 h-4" /> Save & Restart
              </button>
            </div>
            <button @click="loadEnv(activeProject.id)" :disabled="envLoading" class="cyber-btn-secondary text-xs flex items-center gap-1">
              <RefreshCw class="w-3.5 h-3.5" :class="envLoading && 'animate-spin'" /> Reload
            </button>
          </div>
        </div>

        <!-- Tab 3: File Browser -->
        <div v-if="activeTab === 'files'" class="p-4 flex-1 flex flex-col space-y-3 overflow-hidden">
          <!-- File Sub-editor View -->
          <div v-if="editingFile" class="flex-1 flex flex-col space-y-3">
            <div class="flex items-center justify-between">
              <div class="flex items-center gap-2">
                <button @click="editingFile = null" class="cyber-btn-sm flex items-center gap-1 text-muted hover:text-slate-900">
                  <ArrowLeft class="w-3.5 h-3.5" /> Back
                </button>
                <span class="text-xs text-slate-900 font-mono font-bold">{{ editingFile.path }}</span>
              </div>
              <span v-if="editingFileMsg" class="text-xs text-neon font-mono">{{ editingFileMsg }}</span>
            </div>

            <textarea
              v-model="editingFile.content"
              rows="16"
              class="cyber-input flex-1 w-full font-mono text-xs text-slate-800 bg-void/90 leading-relaxed p-4 border border-border focus:border-neon resize-none overflow-y-auto"
              spellcheck="false"
            ></textarea>

            <div class="flex items-center gap-2 pt-2">
              <button @click="saveCurrentFile" :disabled="editingFileSaving" class="cyber-btn flex items-center gap-2">
                <Save class="w-4 h-4" /> Save File
              </button>
              <button @click="editingFile = null" class="cyber-btn-secondary">Close</button>
            </div>
          </div>

          <!-- File Explorer List View -->
          <div v-else class="flex-1 flex flex-col space-y-3 overflow-hidden">
            <!-- Breadcrumbs & New File Buttons -->
            <div class="flex items-center justify-between">
              <div class="flex items-center gap-2 text-xs font-mono">
                <button @click="loadFiles(activeProject.id, '')" class="text-neon hover:underline">[ROOT]</button>
                <span v-if="currentDir" class="text-muted">/</span>
                <span v-if="currentDir" class="text-slate-800">{{ currentDir }}</span>
              </div>
              <div class="flex items-center gap-2">
                <button @click="showNewItemModal = true; newItemIsDir = false" class="cyber-btn-sm flex items-center gap-1">
                  <Plus class="w-3 h-3" /> New File
                </button>
                <button @click="showNewItemModal = true; newItemIsDir = true" class="cyber-btn-sm flex items-center gap-1">
                  <Plus class="w-3 h-3" /> New Dir
                </button>
                <button @click="loadFiles(activeProject.id, currentDir)" class="cyber-btn-sm">
                  <RefreshCw class="w-3.5 h-3.5" :class="filesLoading && 'animate-spin'" />
                </button>
              </div>
            </div>

            <!-- New File/Dir Inline Prompt -->
            <div v-if="showNewItemModal" class="cyber-card p-3 border-neon flex items-center gap-2">
              <span class="text-xs text-neon font-mono">New {{ newItemIsDir ? 'Directory' : 'File' }}:</span>
              <input v-model="newItemName" placeholder="filename.txt" class="cyber-input py-1 text-xs flex-1" @keyup.enter="createNewFileItem" />
              <button @click="createNewFileItem" class="cyber-btn py-1 px-3 text-xs">Create</button>
              <button @click="showNewItemModal = false" class="cyber-btn-secondary py-1 px-3 text-xs">Cancel</button>
            </div>

            <!-- Directory Items Table -->
            <div class="flex-1 overflow-y-auto border border-border bg-void/50 rounded">
              <table class="w-full text-left text-xs font-mono">
                <thead class="border-b border-border bg-surface text-muted uppercase">
                  <tr>
                    <th class="p-2.5">Name</th>
                    <th class="p-2.5 w-24">Size</th>
                    <th class="p-2.5 w-44 hidden sm:table-cell">Modified</th>
                    <th class="p-2.5 w-16 text-right">Action</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-border/40">
                  <!-- Up directory row -->
                  <tr v-if="currentDir" @click="navigateUp" class="hover:bg-neon/5 cursor-pointer text-muted hover:text-neon">
                    <td colspan="4" class="p-2 flex items-center gap-2">
                      <Folder class="w-4 h-4 text-neon" /> .. (up directory)
                    </td>
                  </tr>

                  <tr v-for="f in fileList" :key="f.path" class="hover:bg-neon/5 transition-colors group">
                    <td class="p-2.5 cursor-pointer" @click="openFile(f)">
                      <div class="flex items-center gap-2">
                        <Folder v-if="f.is_dir" class="w-4 h-4 text-cyan-600" />
                        <FileCode v-else-if="f.name.endsWith('.yml') || f.name.endsWith('.yaml')" class="w-4 h-4 text-neon" />
                        <FileText v-else class="w-4 h-4 text-muted group-hover:text-slate-800" />
                        <span :class="f.is_dir ? 'text-cyan-700 font-bold' : 'text-slate-800'">{{ f.name }}</span>
                      </div>
                    </td>
                    <td class="p-2.5 text-muted">{{ f.is_dir ? '-' : formatBytes(f.size) }}</td>
                    <td class="p-2.5 text-muted hidden sm:table-cell">{{ f.mod_time }}</td>
                    <td class="p-2.5 text-right">
                      <button @click.stop="deleteFileItem(f)" class="text-muted hover:text-danger p-1" title="Delete">
                        <Trash2 class="w-3.5 h-3.5" />
                      </button>
                    </td>
                  </tr>

                  <tr v-if="!fileList.length && !filesLoading">
                    <td colspan="4" class="p-6 text-center text-muted font-mono">// EMPTY DIRECTORY</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- ============================================================= -->
    <!-- DOCKER RUN MODAL                                               -->
    <!-- ============================================================= -->
    <div v-if="showDockerRun" class="fixed inset-0 z-50 bg-void/80 backdrop-blur-sm flex items-center justify-center p-4">
      <div class="cyber-card w-full max-w-3xl max-h-[90vh] flex flex-col border-neon shadow-neon p-0 overflow-hidden">
        <!-- Modal Header -->
        <div class="p-4 border-b border-border bg-surface flex items-center justify-between">
          <div class="flex items-center gap-2">
            <Terminal class="w-5 h-5 text-neon" />
            <h2 class="font-display text-base text-neon uppercase tracking-wider">Docker Run</h2>
          </div>
          <button @click="showDockerRun = false" class="text-muted hover:text-slate-900 transition-colors">
            <X class="w-5 h-5" />
          </button>
        </div>

        <div class="p-5 flex-1 overflow-y-auto space-y-4">
          <p class="text-xs text-muted font-mono leading-relaxed">
            Execute any <code class="text-neon">docker run</code> command. DockTun can automatically convert it into a managed Compose stack so you can edit its Compose file and .env later!
          </p>

          <!-- Command Input -->
          <div>
            <label class="text-xs text-muted uppercase font-mono block mb-1">Command</label>
            <textarea
              v-model="dockerRunCmd"
              rows="3"
              placeholder="docker run -d --name my-app -p 8080:80 nginx:alpine"
              class="cyber-input w-full font-mono text-xs text-neon bg-void p-3 border border-border focus:border-neon resize-none"
              spellcheck="false"
            ></textarea>
          </div>

          <!-- Snippet Chips -->
          <div>
            <label class="text-xs text-muted uppercase font-mono block mb-1">Quick Flag Snippets</label>
            <div class="flex flex-wrap gap-1.5">
              <button @click="insertSnippet('--name my-service')" class="cyber-btn-sm text-xs font-mono">+ --name</button>
              <button @click="insertSnippet('-p 8080:80')" class="cyber-btn-sm text-xs font-mono">+ -p (port)</button>
              <button @click="insertSnippet('-v /data:/app')" class="cyber-btn-sm text-xs font-mono">+ -v (volume)</button>
              <button @click="insertSnippet('-e KEY=val')" class="cyber-btn-sm text-xs font-mono">+ -e (env)</button>
              <button @click="insertSnippet('--restart unless-stopped')" class="cyber-btn-sm text-xs font-mono">+ --restart</button>
              <button @click="insertSnippet('--network cloudflared')" class="cyber-btn-sm text-xs font-mono">+ --network cloudflared</button>
            </div>
          </div>

          <!-- Mode & Network Options -->
          <div class="grid grid-cols-1 md:grid-cols-2 gap-3 pt-2">
            <div class="cyber-card p-3 border-border hover:border-neon/50 cursor-pointer" @click="dockerRunMode = 'compose'">
              <div class="flex items-center gap-2">
                <input type="radio" value="compose" v-model="dockerRunMode" class="text-neon" />
                <span class="text-xs font-mono font-bold text-slate-800">Convert to Compose Stack</span>
              </div>
              <p class="text-[11px] text-muted font-mono mt-1 ml-5">
                Generates a docker-compose.yml in DockTun. Best for editing compose files, .env, and full management.
              </p>
            </div>

            <div class="cyber-card p-3 border-border hover:border-neon/50 cursor-pointer" @click="dockerRunMode = 'direct'">
              <div class="flex items-center gap-2">
                <input type="radio" value="direct" v-model="dockerRunMode" class="text-neon" />
                <span class="text-xs font-mono font-bold text-slate-800">Direct Docker Run</span>
              </div>
              <p class="text-[11px] text-muted font-mono mt-1 ml-5">
                Executes the raw docker run CLI command. Great for utility containers or one-off tasks.
              </p>
            </div>
          </div>

          <!-- Cloudflare Network & Project Options -->
          <div class="space-y-2 pt-1 font-mono text-xs">
            <label class="flex items-center gap-2 cursor-pointer text-slate-700">
              <input type="checkbox" v-model="dockerRunCloudflared" class="accent-neon" />
              <span>Connect to Docker network <code class="text-cyan-600">cloudflared</code> (allows Cloudflare Tunnel ingress)</span>
            </label>
            <label class="flex items-center gap-2 cursor-pointer text-slate-700">
              <input type="checkbox" v-model="dockerRunCreateProject" class="accent-neon" />
              <span>Register container as a DockTun Project</span>
            </label>
          </div>

          <!-- Execution Console -->
          <div v-if="dockerRunLines.length || dockerRunLoading" class="cyber-card p-3 bg-void border-border space-y-2">
            <div class="flex items-center justify-between">
              <span v-if="dockerRunLoading" class="text-xs text-neon animate-pulse font-mono">// EXECUTING DOCKER RUN...</span>
              <span v-else-if="dockerRunJob?.exit_code === 0" class="text-xs text-neon font-mono">✔ EXECUTION SUCCESSFUL</span>
              <span v-else class="text-xs text-danger font-mono">✖ EXECUTION FAILED</span>

              <button @click="dockerRunLines = []" class="text-xs text-muted hover:text-slate-900 font-mono">[ CLEAR ]</button>
            </div>

            <pre ref="dockerRunEl" class="text-xs text-muted font-mono whitespace-pre-wrap max-h-56 overflow-y-auto leading-relaxed">{{ dockerRunLines.join('\n') }}</pre>

            <div v-if="dockerRunJob?.done && dockerRunJob?.project_id" class="pt-2 border-t border-border flex items-center justify-between">
              <span class="text-xs text-neon font-mono">Project '{{ dockerRunJob.project_name }}' is ready!</span>
              <button
                @click="showDockerRun = false; openProjectManager({ id: dockerRunJob.project_id, name: dockerRunJob.project_name, path: '/opt/projects/' + dockerRunJob.project_name, repo_url: '', git_username: '', git_token: '', created_at: '' }, 'compose')"
                class="cyber-btn-sm text-neon flex items-center gap-1"
              >
                <FileCode class="w-3.5 h-3.5" /> Open Compose File
              </button>
            </div>
          </div>
        </div>

        <!-- Modal Footer -->
        <div class="p-4 border-t border-border bg-surface flex items-center justify-between">
          <button @click="showDockerRun = false" class="cyber-btn-secondary">Cancel</button>
          <button @click="runDockerCommand" :disabled="dockerRunLoading || !dockerRunCmd.trim()" class="cyber-btn flex items-center gap-2">
            <Terminal class="w-4 h-4" /> Run Container
          </button>
        </div>
      </div>
    </div>

    <!-- ============================================================= -->
    <!-- APP TEMPLATES CATALOG MODAL (Portainer / Cosmos Style)         -->
    <!-- ============================================================= -->
    <div v-if="showCatalog" class="fixed inset-0 z-50 flex items-center justify-center bg-void/80 p-4 backdrop-blur-sm" @click.self="showCatalog = false">
      <div class="cyber-card w-full max-w-4xl max-h-[90vh] flex flex-col p-0 overflow-hidden border-cyan-800 shadow-sm">
        <!-- Catalog Header -->
        <div class="p-5 border-b border-border bg-surface flex items-center justify-between">
          <div class="flex items-center gap-3">
            <div class="rounded-lg bg-cyan-950/80 border border-cyan-800 p-2 text-cyan-700">
              <Sparkles class="w-5 h-5 text-cyan-600" />
            </div>
            <div>
              <h2 class="text-base font-bold text-slate-900 font-mono tracking-wide">1-Click App Catalog</h2>
              <p class="text-xs text-muted font-mono mt-0.5">Pre-configured self-hosted apps attached to the Cloudflare network</p>
            </div>
          </div>
          <button @click="showCatalog = false" class="text-muted hover:text-slate-900 transition">
            <X class="w-5 h-5" />
          </button>
        </div>

        <!-- Catalog Body -->
        <div class="p-5 flex-1 overflow-y-auto space-y-4">
          <!-- When no template selected: browse catalog -->
          <div v-if="!selectedTemplate" class="space-y-4">
            <!-- Search & Category Filters -->
            <div class="flex flex-col sm:flex-row items-stretch sm:items-center justify-between gap-3">
              <div class="relative flex-1">
                <Search class="w-4 h-4 text-muted absolute left-3 top-2.5" />
                <input
                  v-model="catalogSearch"
                  placeholder="Search templates (e.g. Vaultwarden, Monitoring, Nextcloud)..."
                  class="cyber-input w-full pl-9 text-xs"
                />
              </div>

              <!-- Categories -->
              <div class="flex items-center gap-1.5 overflow-x-auto pb-1 text-xs font-mono">
                <button
                  v-for="cat in catalogCategories"
                  :key="cat"
                  @click="catalogCategory = cat"
                  class="rounded px-2.5 py-1 transition whitespace-nowrap"
                  :class="catalogCategory === cat ? 'bg-cyan-950 text-cyan-700 border border-cyan-700' : 'bg-surface text-muted hover:text-slate-900'"
                >
                  {{ cat }}
                </button>
              </div>
            </div>

            <!-- Templates Grid -->
            <div class="grid grid-cols-1 md:grid-cols-2 gap-3 pt-1">
              <div
                v-for="tmpl in filteredTemplates"
                :key="tmpl.id"
                @click="selectTemplate(tmpl)"
                class="cyber-card p-4 hover:border-cyan-500/70 cursor-pointer transition flex flex-col justify-between group bg-void/60 hover:bg-void/90"
              >
                <div>
                  <div class="flex items-center justify-between mb-2">
                    <span class="text-xs font-bold text-slate-900 font-mono group-hover:text-cyan-700 transition">
                      {{ tmpl.title }}
                    </span>
                    <span class="rounded bg-white border border-slate-200 px-2 py-0.5 text-[10px] font-mono text-cyan-600">
                      {{ tmpl.category }}
                    </span>
                  </div>
                  <p class="text-xs text-muted leading-relaxed">
                    {{ tmpl.description }}
                  </p>
                </div>

                <div class="mt-4 pt-3 border-t border-border/50 flex items-center justify-between text-xs font-mono">
                  <span class="text-muted/70 text-[11px]">Port: {{ tmpl.defaultPort }}</span>
                  <span class="text-cyan-600 font-bold group-hover:translate-x-0.5 transition flex items-center gap-1">
                    <span>Deploy</span>
                    <span>&rarr;</span>
                  </span>
                </div>
              </div>
            </div>

            <div v-if="filteredTemplates.length === 0" class="py-12 text-center text-xs text-muted font-mono">
              // NO TEMPLATES MATCH YOUR SEARCH
            </div>
          </div>

          <!-- When template selected: customize and deploy -->
          <div v-else class="space-y-4">
            <div class="flex items-center justify-between pb-3 border-b border-border">
              <div class="flex items-center gap-2">
                <button @click="selectedTemplate = null" class="cyber-btn-sm text-muted hover:text-slate-900 flex items-center gap-1">
                  <ArrowLeft class="w-3.5 h-3.5" /> Back to Catalog
                </button>
                <h3 class="text-sm font-bold text-cyan-700 font-mono ml-2">Deploy {{ selectedTemplate.title }}</h3>
              </div>
              <span class="rounded bg-cyan-950 border border-cyan-800 px-2.5 py-0.5 text-xs font-mono text-cyan-700">
                {{ selectedTemplate.category }}
              </span>
            </div>

            <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div>
                <label class="block text-[10px] uppercase font-mono text-muted mb-1">Project / Stack Name</label>
                <input v-model="templateForm.name" class="cyber-input w-full font-mono text-xs" />
              </div>
              <div>
                <label class="block text-[10px] uppercase font-mono text-muted mb-1">Host Directory Path</label>
                <input v-model="templateForm.path" class="cyber-input w-full font-mono text-xs" />
              </div>
            </div>

            <div>
              <div class="flex items-center justify-between mb-1">
                <label class="block text-[10px] uppercase font-mono text-muted">docker-compose.yml configuration</label>
                <span class="text-[10px] text-emerald-600 font-mono">✔ Configured with cloudflared network</span>
              </div>
              <textarea
                v-model="templateForm.compose"
                rows="12"
                class="cyber-input w-full font-mono text-xs leading-relaxed"
                spellcheck="false"
              ></textarea>
            </div>
          </div>
        </div>

        <!-- Catalog Footer -->
        <div class="p-4 border-t border-border bg-surface flex items-center justify-between">
          <button @click="showCatalog = false" class="cyber-btn-secondary">Close</button>

          <button
            v-if="selectedTemplate"
            @click="deployFromTemplate"
            :disabled="deployingTemplate || !templateForm.name || !templateForm.path"
            class="cyber-btn flex items-center gap-2 text-cyan-700 border-cyan-600 bg-cyan-950/40 hover:bg-cyan-900/50"
          >
            <RotateCcw v-if="deployingTemplate" class="w-4 h-4 animate-spin" />
            <Rocket v-else class="w-4 h-4 text-emerald-600" />
            <span>{{ deployingTemplate ? 'Deploying...' : 'Deploy & Start App' }}</span>
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
