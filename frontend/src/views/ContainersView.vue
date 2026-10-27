<script setup lang="ts">
import { getPrimaryPort, cleanContainerName, getMdnsPortSuffix } from '../lib/containerPorts.mjs'
import ContainerExposure from "../components/ContainerExposure.vue"
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
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
  Code2,
  ArrowUpCircle,
  Sparkles,
  Download,
  Rocket,
  History,
  GitBranch,
  KeyRound,
  Package,
  ChevronDown,
  ChevronRight,
  CheckSquare,
  Database,
  Box,
  Film,
  Key,
  Tv
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
const filter = ref<'all' | 'running' | 'stopped' | 'local' | 'public' | 'private' | 'exposed' | 'updates'>('all')

const checkingUpdates = ref(false)
const checkingSingleUpdate = ref<Record<string, boolean>>({})
const updatingContainer = ref<Record<string, boolean>>({})
const showUpdateConfirmModal = ref<{ container: ContainerItem } | null>(null)
const updateResultModal = ref<{ name: string; output: string; success: boolean; error?: string } | null>(null)

const availableUpdatesCount = computed(() => {
  return containers.value.filter((c) => c.update_available).length
})

const actionLoading = ref<Record<string, string>>({})
const exposeForm = ref<Record<string, ExposureSettings>>({})
const exposeSaving = ref<Record<string, boolean>>({})

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
    const pPort = getPrimaryPort(c.ports)
    const settings: ExposureSettings = {
      mode: c.exposure_mode || c.exposure?.mode || 'internal',
      hostname: c.hostname || c.exposure?.hostname || '',
      target: c.target || c.exposure?.target || `http://${c.name}:${pPort}`,
      private_type: c.private_type || c.exposure?.private_type || '',
      private_host: c.private_host || c.exposure?.private_host || '',
      private_port: c.private_port || c.exposure?.private_port || parseInt(pPort, 10) || 80,
      notes: c.notes || c.exposure?.notes || '',
      zero_trust_enabled: c.zero_trust_enabled || c.exposure?.zero_trust_enabled || false,
      zero_trust_emails: c.zero_trust_emails || c.exposure?.zero_trust_emails || '',
      zero_trust_auth_type: c.zero_trust_auth_type || c.exposure?.zero_trust_auth_type || 'email',
      zero_trust_session: c.zero_trust_session || c.exposure?.zero_trust_session || '24h',
    }
    exposeForm.value[id] = settings
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
const selectedTab = ref<'overview' | 'access' | 'compose' | 'env' | 'files' | 'logs' | 'exec'>('overview')
const detailLoading = ref(false)
const networkActionLoading = ref(false)
const projectScaffolding = ref(false)

// Exec / Terminal state
const execCmd = ref('')
const execOutput = ref('')
const execLoading = ref(false)
const execExitCode = ref<number | null>(null)
const execHistory = ref<string[]>([])

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

// ==========================================
// Launch Modal & Unified Stack State
// ==========================================
const showLaunchModal = ref(false)
const launchTab = ref<'compose' | 'templates' | 'dockerrun' | 'gitstack'>('compose')

// Direct Compose Stack State
const defaultComposeExample = `services:
  my-app:
    image: nginx:alpine
    container_name: my-app
    restart: unless-stopped
    ports:
      - "8080:80"
    networks:
      - cloudflared

networks:
  cloudflared:
    external: true`

const composeDeployForm = ref({
  name: '',
  path: '',
  compose_content: '',
  env_content: '',
  show_env: false,
})
const composeDeploying = ref(false)
const composeTemplateCategory = ref<'all' | 'web' | 'database' | 'monitoring' | 'media' | 'utility'>('all')
const composeTemplateSearch = ref('')

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

interface ComposeTemplate {
  id: string
  name: string
  category: 'web' | 'database' | 'monitoring' | 'media' | 'utility'
  iconComponent: any
  description: string
  ports: string[]
  compose: string
  env?: string
}

const COMPOSE_TEMPLATES: ComposeTemplate[] = [
  {
    id: 'nginx-server',
    name: 'Nginx Web Server',
    category: 'web',
    iconComponent: Globe,
    description: 'High-performance HTTP server & reverse proxy with volume persistence and cloudflared network integration.',
    ports: ['8080:80'],
    compose: `services:
  nginx:
    image: nginx:alpine
    container_name: nginx-server
    restart: unless-stopped
    ports:
      - "8080:80"
    volumes:
      - ./html:/usr/share/nginx/html:ro
    networks:
      - cloudflared

networks:
  cloudflared:
    external: true
`,
  },
  {
    id: 'postgres-db',
    name: 'PostgreSQL 16 & pgAdmin 4',
    category: 'database',
    iconComponent: Database,
    description: 'Robust SQL database engine bundled with pgAdmin 4 web GUI administration dashboard.',
    ports: ['5432:5432', '5050:80'],
    compose: `services:
  postgres:
    image: postgres:16-alpine
    container_name: postgres-db
    restart: unless-stopped
    environment:
      POSTGRES_USER: \${POSTGRES_USER:-docktun}
      POSTGRES_PASSWORD: \${POSTGRES_PASSWORD:-ChangeMeSecurePassword123!}
      POSTGRES_DB: \${POSTGRES_DB:-appdb}
    volumes:
      - postgres_data:/var/lib/postgresql/data
    networks:
      - cloudflared

  pgadmin:
    image: dpage/pgadmin4:latest
    container_name: pgadmin
    restart: unless-stopped
    environment:
      PGADMIN_DEFAULT_EMAIL: \${PGADMIN_EMAIL:-admin@docktun.local}
      PGADMIN_DEFAULT_PASSWORD: \${PGADMIN_PASSWORD:-AdminPassword123!}
    ports:
      - "5050:80"
    depends_on:
      - postgres
    networks:
      - cloudflared

volumes:
  postgres_data:

networks:
  cloudflared:
    external: true
`,
    env: `POSTGRES_USER=docktun
POSTGRES_PASSWORD=ChangeMeSecurePassword123!
POSTGRES_DB=appdb
PGADMIN_EMAIL=admin@docktun.local
PGADMIN_PASSWORD=AdminPassword123!
`,
  },
  {
    id: 'redis-cache',
    name: 'Redis 7 & Commander GUI',
    category: 'database',
    iconComponent: Cpu,
    description: 'In-memory key-value data structure store & cache with Redis Commander browser explorer.',
    ports: ['6379:6379', '8081:8081'],
    compose: `services:
  redis:
    image: redis:7-alpine
    container_name: redis-cache
    restart: unless-stopped
    command: redis-server --appendonly yes
    volumes:
      - redis_data:/data
    networks:
      - cloudflared

  redis-commander:
    image: rediscommander/redis-commander:latest
    container_name: redis-commander
    restart: unless-stopped
    environment:
      - REDIS_HOSTS=local:redis:6379
    ports:
      - "8081:8081"
    depends_on:
      - redis
    networks:
      - cloudflared

volumes:
  redis_data:

networks:
  cloudflared:
    external: true
`,
  },
  {
    id: 'uptime-kuma',
    name: 'Uptime Kuma',
    category: 'monitoring',
    iconComponent: Activity,
    description: 'Self-hosted monitoring tool with real-time incident reports, latency graphs, and alerts.',
    ports: ['3001:3001'],
    compose: `services:
  uptime-kuma:
    image: louislam/uptime-kuma:1
    container_name: uptime-kuma
    restart: unless-stopped
    volumes:
      - kuma_data:/app/data
    ports:
      - "3001:3001"
    networks:
      - cloudflared

volumes:
  kuma_data:

networks:
  cloudflared:
    external: true
`,
  },
  {
    id: 'portainer',
    name: 'Portainer CE',
    category: 'utility',
    iconComponent: Box,
    description: 'Centralized container management GUI for Docker containers, images, volumes, and networks.',
    ports: ['9443:9443', '9000:9000'],
    compose: `services:
  portainer:
    image: portainer/portainer-ce:latest
    container_name: portainer
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    volumes:
      - /etc/localtime:/etc/localtime:ro
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - portainer_data:/data
    ports:
      - "9443:9443"
      - "9000:9000"
    networks:
      - cloudflared

volumes:
  portainer_data:

networks:
  cloudflared:
    external: true
`,
  },
  {
    id: 'jellyfin',
    name: 'Jellyfin Media Server',
    category: 'media',
    iconComponent: Film,
    description: 'Free Software Media System that lets you collect, manage, and stream your media anywhere.',
    ports: ['8096:8096'],
    compose: `services:
  jellyfin:
    image: jellyfin/jellyfin:latest
    container_name: jellyfin
    restart: unless-stopped
    environment:
      - TZ=America/Chicago
    volumes:
      - jellyfin_config:/config
      - jellyfin_cache:/cache
      - ./media:/media:ro
    ports:
      - "8096:8096"
    networks:
      - cloudflared

volumes:
  jellyfin_config:
  jellyfin_cache:

networks:
  cloudflared:
    external: true
`,
  },
  {
    id: 'nextcloud',
    name: 'Nextcloud Hub',
    category: 'utility',
    iconComponent: HardDrive,
    description: 'Self-hosted productivity suite with file storage, sharing, office documents, and calendar syncing.',
    ports: ['8088:80'],
    compose: `services:
  nextcloud:
    image: nextcloud:latest
    container_name: nextcloud
    restart: unless-stopped
    ports:
      - "8088:80"
    volumes:
      - nextcloud_data:/var/www/html
    environment:
      - NEXTCLOUD_ADMIN_USER=admin
      - NEXTCLOUD_ADMIN_PASSWORD=AdminPasswordChangeMe!
    networks:
      - cloudflared

volumes:
  nextcloud_data:

networks:
  cloudflared:
    external: true
`,
  },
  {
    id: 'vaultwarden',
    name: 'Vaultwarden (Bitwarden)',
    category: 'utility',
    iconComponent: Key,
    description: 'Lightweight Bitwarden-compatible password manager backend written in Rust.',
    ports: ['8080:80'],
    compose: `services:
  vaultwarden:
    image: vaultwarden/server:latest
    container_name: vaultwarden
    restart: unless-stopped
    environment:
      - WEBSOCKET_ENABLED=true
      - SIGNUPS_ALLOWED=true
    volumes:
      - vaultwarden_data:/data
    ports:
      - "8080:80"
    networks:
      - cloudflared

volumes:
  vaultwarden_data:

networks:
  cloudflared:
    external: true
`,
  },
  {
    id: 'mediastack',
    name: 'Media Automation Stack',
    category: 'media',
    iconComponent: Tv,
    description: 'Automated TV, Movie, and Indexer stack combining Sonarr, Radarr, and Prowlarr.',
    ports: ['8989:8989', '7878:7878', '9696:9696'],
    compose: `services:
  sonarr:
    image: lscr.io/linuxserver/sonarr:latest
    container_name: sonarr
    restart: unless-stopped
    environment:
      - PUID=1000
      - PGID=1000
      - TZ=America/Chicago
    volumes:
      - sonarr_config:/config
      - ./tv:/tv
      - ./downloads:/downloads
    ports:
      - "8989:8989"
    networks:
      - cloudflared

  radarr:
    image: lscr.io/linuxserver/radarr:latest
    container_name: radarr
    restart: unless-stopped
    environment:
      - PUID=1000
      - PGID=1000
      - TZ=America/Chicago
    volumes:
      - radarr_config:/config
      - ./movies:/movies
      - ./downloads:/downloads
    ports:
      - "7878:7878"
    networks:
      - cloudflared

  prowlarr:
    image: lscr.io/linuxserver/prowlarr:latest
    container_name: prowlarr
    restart: unless-stopped
    environment:
      - PUID=1000
      - PGID=1000
      - TZ=America/Chicago
    volumes:
      - prowlarr_config:/config
    ports:
      - "9696:9696"
    networks:
      - cloudflared

volumes:
  sonarr_config:
  radarr_config:
  prowlarr_config:

networks:
  cloudflared:
    external: true
`,
  },
]

const filteredTemplates = computed(() => {
  return COMPOSE_TEMPLATES.filter((t) => {
    const matchesCat = composeTemplateCategory.value === 'all' || t.category === composeTemplateCategory.value
    const matchesSearch = !composeTemplateSearch.value.trim() ||
      t.name.toLowerCase().includes(composeTemplateSearch.value.toLowerCase()) ||
      t.description.toLowerCase().includes(composeTemplateSearch.value.toLowerCase())
    return matchesCat && matchesSearch
  })
})

function selectTemplate(tmpl: ComposeTemplate) {
  composeDeployForm.value.name = tmpl.id
  composeDeployForm.value.path = `./projects/${tmpl.id}`
  composeDeployForm.value.compose_content = tmpl.compose
  composeDeployForm.value.env_content = tmpl.env || ''
  if (tmpl.env) {
    composeDeployForm.value.show_env = true
  }
  launchTab.value = 'compose'
}

function insertComposeSnippet(type: 'network' | 'restart' | 'ports' | 'volumes' | string) {
  const snippets: Record<string, string> = {
    network: '    networks:\n      - cloudflared\n\nnetworks:\n  cloudflared:\n    external: true',
    restart: '    restart: unless-stopped',
    ports: '    ports:\n      - "8080:80"',
    volumes: '    volumes:\n      - ./data:/data',
  }
  const content = snippets[type] || type
  composeDeployForm.value.compose_content += '\n' + content
}

async function deployComposeStack() {
  if (isPastedDockerRun.value) {
    convertComposeDockerRun()
  }
  const name = composeDeployForm.value.name.trim()
  if (!name) {
    alert('Stack name is required.')
    return
  }
  if (!composeDeployForm.value.compose_content.trim()) {
    alert('docker-compose.yml content cannot be empty.')
    return
  }
  composeDeploying.value = true
  try {
    const res: any = await apiFetch('/api/projects', {
      method: 'POST',
      body: JSON.stringify({
        name: name,
        path: composeDeployForm.value.path.trim() || `./projects/${name}`,
        compose_content: composeDeployForm.value.compose_content,
        env_content: composeDeployForm.value.env_content,
        auto_start: true,
      }),
    })
    showLaunchModal.value = false
    await fetchContainers()
    if (res.id) {
      startStackOutputPolling(res.id, `Deploying Compose Stack: ${name}`)
    }
  } catch (err: any) {
    alert('Failed to deploy Compose stack: ' + (err.message || err))
  } finally {
    composeDeploying.value = false
  }
}

// Docker Run State
const defaultExampleCmd = 'docker run -d --name my-app -p 8080:80 nginx:alpine'
const dockerRunCmd = ref('')
const dockerRunMode = ref<'compose' | 'direct'>('compose')
const dockerRunCloudflared = ref(false)
const dockerRunCreateProject = ref(true)
const dockerRunCustomName = ref('')
const dockerRunLoading = ref(false)
const dockerRunJob = ref<any>(null)
const dockerRunLines = ref<string[]>([])

// Backdrop tracking to prevent modal from force-closing on text selection drag
let backdropMouseDown = false
function onBackdropMouseDown(e: MouseEvent) {
  backdropMouseDown = (e.target === e.currentTarget)
}
function onBackdropClick(e: MouseEvent, closeFn: () => void) {
  // Never close if user has an active text selection from dragging
  const selection = window.getSelection()?.toString()
  if (selection && selection.length > 0) {
    backdropMouseDown = false
    return
  }
  if (backdropMouseDown && e.target === e.currentTarget) {
    closeFn()
  }
  backdropMouseDown = false
}

function onLaunchModalBackdropClick() {
  // Prevent accidental dismissal if the user has typed or pasted anything in the launch modal
  if (!composeDeployForm.value.compose_content.trim() && !dockerRunCmd.value.trim()) {
    showLaunchModal.value = false
  }
}

function convertDockerRunToComposeYaml(cmd: string): { name: string; yaml: string } {
  const cleanCmd = cmd.replace(/\\\r?\n/g, ' ').replace(/\s+/g, ' ').trim()
  const tokens = cleanCmd.match(/(?:[^\s"']+|"[^"]*"|'[^']*')+/g) || []

  let name = ''
  let image = ''
  const ports: string[] = []
  const volumes: string[] = []
  const env: string[] = []
  let restart = 'unless-stopped'
  let network = 'cloudflared'
  let privileged = false

  let i = 0
  if (tokens[0] === 'docker' || tokens[0] === 'podman') i++
  if (tokens[i] === 'run' || tokens[i] === 'create') i++

  for (; i < tokens.length; i++) {
    const rawT = tokens[i]
    const t = rawT.replace(/^["']|["']$/g, '')
    if (t === '--name' && i + 1 < tokens.length) {
      name = tokens[++i].replace(/^["']|["']$/g, '')
    } else if (t.startsWith('--name=')) {
      name = t.substring(7).replace(/^["']|["']$/g, '')
    } else if ((t === '-p' || t === '--publish') && i + 1 < tokens.length) {
      ports.push(tokens[++i].replace(/^["']|["']$/g, ''))
    } else if (t.startsWith('-p=') || t.startsWith('--publish=')) {
      ports.push(t.substring(t.indexOf('=') + 1).replace(/^["']|["']$/g, ''))
    } else if ((t === '-v' || t === '--volume') && i + 1 < tokens.length) {
      volumes.push(tokens[++i].replace(/^["']|["']$/g, ''))
    } else if (t.startsWith('-v=') || t.startsWith('--volume=')) {
      volumes.push(t.substring(t.indexOf('=') + 1).replace(/^["']|["']$/g, ''))
    } else if ((t === '-e' || t === '--env') && i + 1 < tokens.length) {
      env.push(tokens[++i].replace(/^["']|["']$/g, ''))
    } else if (t.startsWith('-e=') || t.startsWith('--env=')) {
      env.push(t.substring(t.indexOf('=') + 1).replace(/^["']|["']$/g, ''))
    } else if (t === '--restart' && i + 1 < tokens.length) {
      restart = tokens[++i].replace(/^["']|["']$/g, '')
    } else if (t.startsWith('--restart=')) {
      restart = t.substring(10).replace(/^["']|["']$/g, '')
    } else if (t === '--privileged') {
      privileged = true
    } else if ((t === '--network' || t === '--net') && i + 1 < tokens.length) {
      network = tokens[++i].replace(/^["']|["']$/g, '')
    } else if (t.startsWith('--network=') || t.startsWith('--net=')) {
      network = t.substring(t.indexOf('=') + 1).replace(/^["']|["']$/g, '')
    } else if (!t.startsWith('-')) {
      image = t
    }
  }

  if (!name && image) {
    const parts = image.split('/')
    const imgPart = parts[parts.length - 1].split(':')[0]
    name = imgPart || 'app'
  }
  if (!name) name = 'app'

  let yaml = `services:\n  ${name}:\n    image: ${image || 'nginx:alpine'}\n    container_name: ${name}\n    restart: ${restart}\n`
  if (privileged) yaml += '    privileged: true\n'
  if (ports.length) {
    yaml += '    ports:\n'
    ports.forEach((p) => (yaml += `      - "${p}"\n`))
  }
  if (volumes.length) {
    yaml += '    volumes:\n'
    volumes.forEach((v) => (yaml += `      - "${v}"\n`))
  }
  if (env.length) {
    yaml += '    environment:\n'
    env.forEach((e) => (yaml += `      - ${e}\n`))
  }
  if (network && network !== 'host' && network !== 'none' && network !== 'bridge') {
    yaml += `    networks:\n      - ${network}\n\nnetworks:\n  ${network}:\n    external: true\n`
  }

  const namedVols = volumes
    .map((v) => v.split(':')[0])
    .filter((v) => v && !v.startsWith('/') && !v.startsWith('./') && !v.startsWith('../') && !v.startsWith('~') && !v.includes('\\'))
  if (namedVols.length) {
    yaml += '\nvolumes:\n'
    const seen = new Set<string>()
    namedVols.forEach((nv) => {
      if (!seen.has(nv)) {
        seen.add(nv)
        yaml += `  ${nv}:\n    name: ${nv}\n`
      }
    })
  }

  return { name, yaml }
}

const isPastedDockerRun = computed(() => {
  const c = composeDeployForm.value.compose_content.trim()
  if (!c) return false
  if (c.startsWith('services:') || c.startsWith('version:')) return false
  const lower = c.toLowerCase()
  return (
    lower.startsWith('docker run') ||
    lower.startsWith('docker create') ||
    lower.startsWith('docker pull') ||
    lower.startsWith('run -') ||
    (c.includes('--name') && c.includes('-p ') && !c.includes(':'))
  )
})

function convertComposeDockerRun() {
  const result = convertDockerRunToComposeYaml(composeDeployForm.value.compose_content)
  composeDeployForm.value.compose_content = result.yaml
  if (!composeDeployForm.value.name.trim() || composeDeployForm.value.name === 'my-stack') {
    composeDeployForm.value.name = result.name
  }
}

function onComposeFocus() {
  if (composeDeployForm.value.compose_content.trim() === defaultComposeExample.trim()) {
    composeDeployForm.value.compose_content = ''
  }
}

function onComposePaste() {
  if (composeDeployForm.value.compose_content.trim() === defaultComposeExample.trim()) {
    composeDeployForm.value.compose_content = ''
  }
}

function onDockerRunFocus() {
  if (dockerRunCmd.value.trim() === defaultExampleCmd.trim()) {
    dockerRunCmd.value = ''
  }
}

function onDockerRunPaste() {
  if (dockerRunCmd.value.trim() === defaultExampleCmd.trim()) {
    dockerRunCmd.value = ''
  }
}

// Git / Compose Stack Creator State
const newProjectForm = ref({
  name: '',
  path: '',
  repo_url: '',
  git_username: '',
  git_token: '',
})
const newProjectSaving = ref(false)

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
const showGitSettings = ref(false)

// Stack Operations & Output Polling
const stackActionLoading = ref<Record<number, boolean>>({})
const stackOutputModal = ref<{ title: string; lines: string[]; done: boolean; projectId: number } | null>(null)
let stackPollTimer: any = null

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
    if (c.running && c.cpu) {
      const parsed = parseFloat(c.cpu.replace('%', ''))
      if (!isNaN(parsed)) totalCpu += parsed
    }
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
const collapsedStacks = ref<Record<string, boolean>>(
  JSON.parse(localStorage.getItem('docktun_collapsed_stacks') || '{}')
)

function toggleGroupByStack() {
  groupByStack.value = !groupByStack.value
  localStorage.setItem('docktun_group_by_stack', String(groupByStack.value))
}

function toggleStackCollapse(key: string) {
  collapsedStacks.value[key] = !collapsedStacks.value[key]
  localStorage.setItem('docktun_collapsed_stacks', JSON.stringify(collapsedStacks.value))
}

function collapseAllStacks() {
  containerGroups.value.forEach((g) => {
    collapsedStacks.value[g.key] = true
  })
  localStorage.setItem('docktun_collapsed_stacks', JSON.stringify(collapsedStacks.value))
}

function expandAllStacks() {
  collapsedStacks.value = {}
  localStorage.setItem('docktun_collapsed_stacks', '{}')
}

// Batch Multi-Select State
const selectedContainerIds = ref<Record<string, boolean>>({})
const batchLoading = ref(false)

const selectedCount = computed(() => {
  return Object.values(selectedContainerIds.value).filter(Boolean).length
})

const allSelected = computed(() => {
  if (filteredContainers.value.length === 0) return false
  return filteredContainers.value.every((c) => selectedContainerIds.value[c.id])
})

function toggleSelectAll() {
  if (allSelected.value) {
    selectedContainerIds.value = {}
  } else {
    const next: Record<string, boolean> = {}
    filteredContainers.value.forEach((c) => {
      next[c.id] = true
    })
    selectedContainerIds.value = next
  }
}

function toggleSelectStack(group: ContainerGroup) {
  const allInGroup = group.containers.every((c) => selectedContainerIds.value[c.id])
  group.containers.forEach((c) => {
    if (allInGroup) {
      delete selectedContainerIds.value[c.id]
    } else {
      selectedContainerIds.value[c.id] = true
    }
  })
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
      if (c.running && c.cpu) {
        const p = parseFloat(c.cpu.replace('%', ''))
        if (!isNaN(p)) cpuSum += p
      }
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
    let key = c.project_name ? c.project_name.trim() : ''
    const lower = c.name.toLowerCase()
    if (
      lower.startsWith('docktun') ||
      lower === 'wireguard' ||
      lower === 'cloudflared' ||
      lower === 'tailscale' ||
      lower === 'wg-easy' ||
      lower === 'zeroconf' ||
      key === 'docktun' ||
      key === 'tun'
    ) {
      key = 'docktun'
    }
    if (!key) {
      key = '__standalone__'
    }
    if (!map.has(key)) {
      map.set(key, [])
    }
    map.get(key)!.push(c)
  })

  const groups: ContainerGroup[] = []
  map.forEach((items, key) => {
    let cpuSum = 0
    items.forEach((c) => {
      if (c.running && c.cpu) {
        const p = parseFloat(c.cpu.replace('%', ''))
        if (!isNaN(p)) cpuSum += p
      }
    })

    const isStack = key !== '__standalone__'
    const firstWithProj = items.find((c) => c.project_id)
    const projectId = firstWithProj?.project_id
    const projectPath = firstWithProj?.project_path

    let groupName = isStack ? key : 'Standalone Containers'
    if (key === 'docktun') {
      groupName = 'DockTun Core'
    }

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

  // Sort: DockTun Core first, other Stacks alphabetically, Standalone at the bottom
  groups.sort((a, b) => {
    if (a.key === 'docktun') return -1
    if (b.key === 'docktun') return 1
    if (a.isStack && !b.isStack) return -1
    if (!a.isStack && b.isStack) return 1
    return a.name.localeCompare(b.name)
  })

  return groups
})

async function fetchContainers() {
  try {
    const data = await apiFetch<ContainerItem[]>('/api/docker/containers')
    containers.value = data || []
    containers.value.forEach((c) => {
      if (!exposeForm.value[c.id]) {
        exposeForm.value[c.id] = {
          mode: c.exposure_mode || c.exposure?.mode || 'internal',
          hostname: c.hostname || c.exposure?.hostname || '',
          target: c.target || c.exposure?.target || `http://${c.name}:${getPrimaryPort(c.ports)}`,
          private_type: c.private_type || c.exposure?.private_type || '',
          private_host: c.private_host || c.exposure?.private_host || '',
          private_port: c.private_port || c.exposure?.private_port || Number(getPrimaryPort(c.ports)),
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
    const saved = await apiFetch<ExposureSettings>(`/api/docker/containers/${container.id}/exposure`)
    Object.assign(payload, saved)
    await fetchContainers()
    if (selectedContainer.value && selectedContainer.value.id === container.id) {
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

function confirmUpdate(container: ContainerItem) {
  showUpdateConfirmModal.value = { container }
}

async function performUpdate(container: ContainerItem) {
  showUpdateConfirmModal.value = null
  updatingContainer.value[container.id] = true
  try {
    const res = await apiFetch(`/api/docker/containers/${container.id}/update`, { method: 'POST' })
    const isDocktunSelf = container.name.toLowerCase().includes('docktun') || (res.output && res.output.includes('self-update'))
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
      if (selectedContainer.value && selectedContainer.value.id === container.id) {
        await openContainerModal(container, selectedTab.value)
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
  try {
    const payload = getExpose(container.id)
    await apiFetch(`/api/docker/containers/${container.id}/exposure`, {
      method: 'POST',
      body: JSON.stringify(payload),
    })
    await fetchContainers()
    if (selectedContainer.value && selectedContainer.value.id === container.id) {
      selectedContainer.value.connected_to_cloudflared = payload.mode === 'public'
      selectedContainer.value.exposure_mode = payload.mode
      selectedContainer.value.hostname = payload.hostname
      selectedContainer.value.target = payload.target
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
  } finally {
    exposeSaving.value[container.id] = false
  }
}

// ==========================================
// Control Center Modal Logic
// ==========================================
async function openContainerModal(c: ContainerItem, tab: 'overview' | 'access' | 'compose' | 'env' | 'files' | 'logs' | 'exec' = 'overview') {
  selectedContainer.value = { ...c } as ContainerDetail
  selectedTab.value = tab
  detailLoading.value = true

  const pPort = getPrimaryPort(c.ports)
  if (!exposeForm.value[c.id]) {
    exposeForm.value[c.id] = {
      mode: c.exposure_mode || 'internal',
      hostname: c.hostname || '',
      target: c.target || `http://${c.name}:${pPort}`,
      private_type: c.private_type || '',
      private_host: c.private_host || '',
      private_port: c.private_port || parseInt(pPort, 10) || 80,
      notes: c.notes || '',
    }
  }

  try {
    const detail = await apiFetch<ContainerDetail>(`/api/docker/containers/${c.id}`)
    selectedContainer.value = detail
    if (detail.connected_to_cloudflared !== undefined) {
      c.connected_to_cloudflared = detail.connected_to_cloudflared
    }
    const currentMode = detail.exposure_mode || detail.exposure?.mode || c.exposure_mode || c.exposure?.mode
    if (currentMode) {
      const pPort = getPrimaryPort(detail.ports || c.ports)
      const formVal: ExposureSettings = {
        mode: currentMode,
        hostname: detail.hostname || detail.exposure?.hostname || c.hostname || c.exposure?.hostname || '',
        target: detail.target || detail.exposure?.target || c.target || c.exposure?.target || `http://${detail.name}:${pPort}`,
        private_type: detail.private_type || detail.exposure?.private_type || c.private_type || c.exposure?.private_type || '',
        private_host: detail.private_host || detail.exposure?.private_host || c.private_host || c.exposure?.private_host || '',
        private_port: detail.private_port || detail.exposure?.private_port || c.private_port || c.exposure?.private_port || parseInt(pPort, 10) || 80,
        notes: detail.notes || detail.exposure?.notes || c.notes || c.exposure?.notes || '',
        zero_trust_enabled: detail.zero_trust_enabled || detail.exposure?.zero_trust_enabled || c.zero_trust_enabled || c.exposure?.zero_trust_enabled || false,
        zero_trust_emails: detail.zero_trust_emails || detail.exposure?.zero_trust_emails || c.zero_trust_emails || c.exposure?.zero_trust_emails || '',
        zero_trust_auth_type: detail.zero_trust_auth_type || detail.exposure?.zero_trust_auth_type || c.zero_trust_auth_type || c.exposure?.zero_trust_auth_type || 'email',
        zero_trust_session: detail.zero_trust_session || detail.exposure?.zero_trust_session || c.zero_trust_session || c.exposure?.zero_trust_session || '24h',
      }
      exposeForm.value[c.id] = formVal
      if (detail.id) exposeForm.value[detail.id] = formVal
    }
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

function handleTabChange(tab: 'overview' | 'access' | 'compose' | 'env' | 'files' | 'logs' | 'exec') {
  selectedTab.value = tab
  if (!selectedContainer.value) return

  if (tab === 'compose') {
    if (selectedContainer.value.project_id) {
      loadContainerCompose(selectedContainer.value.project_id)
      loadProjectInfo(selectedContainer.value.project_id)
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

// ==========================================
// Stack & Launcher Helper Functions
// ==========================================
async function loadProjectInfo(projectId: number) {
  try {
    const list: any[] = await apiFetch('/api/projects')
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
  } catch (e: any) {
    alert('Failed to save Git configuration: ' + (e.message || e))
  } finally {
    projectGitSaving.value = false
  }
}

function startStackOutputPolling(projectId: number, title: string) {
  stackOutputModal.value = { title, lines: ['Initializing stack action...'], done: false, projectId }
  if (stackPollTimer) clearInterval(stackPollTimer)
  stackPollTimer = setInterval(async () => {
    try {
      const res: any = await apiFetch(`/api/projects/${projectId}/output`)
      if (stackOutputModal.value && res.lines && res.lines.length) {
        stackOutputModal.value.lines = res.lines
      }
      if (res.done) {
        if (stackOutputModal.value) stackOutputModal.value.done = true
        clearInterval(stackPollTimer)
        stackPollTimer = null
        delete stackActionLoading.value[projectId]
        await fetchContainers()
      }
    } catch {
      clearInterval(stackPollTimer)
      stackPollTimer = null
      delete stackActionLoading.value[projectId]
    }
  }, 1000)
}

async function runStackAction(projectId: number, action: 'up' | 'down' | 'restart') {
  if (!projectId) {
    alert('This stack does not have an active compose project ID linked yet.')
    return
  }
  if (!confirm(`Are you sure you want to run compose "${action}" on this stack?`)) return
  stackActionLoading.value[projectId] = true
  try {
    await apiFetch(`/api/projects/${projectId}/action/${action}`, { method: 'POST' })
    startStackOutputPolling(projectId, `Compose ${action.toUpperCase()}`)
  } catch (e: any) {
    alert(`Stack action "${action}" failed: ` + (e.message || e))
    delete stackActionLoading.value[projectId]
  }
}

function openDeployPrompt(projectId: number, projectName: string) {
  showDeployPromptModal.value = { projectId, projectName }
  deployBranchName.value = 'main'
}

async function confirmDeploy() {
  if (!showDeployPromptModal.value) return
  const pId = showDeployPromptModal.value.projectId
  const branch = deployBranchName.value.trim() || 'main'
  showDeployPromptModal.value = null

  stackActionLoading.value[pId] = true
  try {
    await apiFetch(`/api/projects/${pId}/action/deploy`, {
      method: 'POST',
      body: JSON.stringify({ ref: branch }),
    })
    startStackOutputPolling(pId, `Deploy Branch/Tag: ${branch}`)
  } catch (e: any) {
    alert('Deploy failed: ' + (e.message || e))
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

function openDeleteStackPrompt(projectId: number, projectName: string, path?: string) {
  showDeleteStackModal.value = { projectId, projectName, path }
  deleteStackDown.value = true
  deleteStackRemoveFiles.value = false
}

async function confirmDeleteStack() {
  if (!showDeleteStackModal.value) return
  const { projectId } = showDeleteStackModal.value
  deleteStackLoading.value = true
  try {
    const downParam = deleteStackDown.value ? 'true' : 'false'
    const removeFilesParam = deleteStackRemoveFiles.value ? 'true' : 'false'
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
  dockerRunLines.value = []
  dockerRunJob.value = null
}

function insertDockerSnippet(snip: string) {
  if (dockerRunCmd.value.trim() === defaultExampleCmd.trim()) {
    dockerRunCmd.value = ''
  }
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
        dockerRunLines.value = job.lines || job.output || []
        if (job.done || job.status === 'completed' || job.status === 'failed') {
          clearInterval(poll)
          dockerRunLoading.value = false
          await fetchContainers()
        }
      } catch {
        clearInterval(poll)
        dockerRunLoading.value = false
      }
    }, 1000)
  } catch (e: any) {
    dockerRunLoading.value = false
    alert(e.message || 'Failed to execute Docker run')
  }
}

async function submitNewProject() {
  if (!newProjectForm.value.name.trim() || !newProjectForm.value.path.trim()) {
    alert('Stack name and folder path are required.')
    return
  }
  newProjectSaving.value = true
  try {
    await apiFetch('/api/projects', {
      method: 'POST',
      body: JSON.stringify(newProjectForm.value),
    })
    showLaunchModal.value = false
    newProjectForm.value = { name: '', path: '', repo_url: '', git_username: '', git_token: '' }
    await fetchContainers()
    alert('Stack created successfully!')
  } catch (e: any) {
    alert('Failed to create stack: ' + (e.message || e))
  } finally {
    newProjectSaving.value = false
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

async function runContainerExec() {
  if (!selectedContainer.value || !execCmd.value.trim() || execLoading.value) return
  const cmd = execCmd.value.trim()
  execLoading.value = true
  execOutput.value = `// EXECUTING: ${cmd}...\n`
  execExitCode.value = null
  if (!execHistory.value.includes(cmd)) {
    execHistory.value.unshift(cmd)
    if (execHistory.value.length > 8) execHistory.value.pop()
  }
  try {
    const res: any = await apiFetch(`/api/docker/containers/${selectedContainer.value.id}/exec`, {
      method: 'POST',
      body: JSON.stringify({ command: cmd }),
    })
    execOutput.value = res.output || '(Command completed with no output)'
    execExitCode.value = res.exit_code
  } catch (err: any) {
    execOutput.value = `ERROR: ${err.message || err}`
    execExitCode.value = 1
  } finally {
    execLoading.value = false
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
let timer: any = null
const route = useRoute()
const router = useRouter()

function checkRouteParams() {
  const targetId = (route.query.id as string) || ''
  const targetName = (route.query.name as string) || ''
  const initialTab = (route.query.tab as any) || 'overview'

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
  if (modalLogsTimer) clearInterval(modalLogsTimer)
  if (stackPollTimer) clearInterval(stackPollTimer)
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
        <button @click="filter = 'exposed'" class="rounded-md px-3 py-1.5 text-xs font-semibold uppercase tracking-wider transition-colors" :class="filter === 'exposed' ? 'bg-slate-100 text-cyan-700' : 'text-slate-600 hover:text-slate-800'">
          Exposed ({{ summary.publicCount + summary.privateCount }})
        </button>
        <button
          @click="filter = 'updates'"
          class="rounded-md px-3 py-1.5 text-xs font-semibold uppercase tracking-wider transition-colors flex items-center gap-1.5"
          :class="filter === 'updates' ? 'bg-amber-50/70 text-amber-700 border border-amber-500/40 shadow-sm' : 'text-slate-600 hover:text-slate-800'"
        >
          <ArrowUpCircle class="h-3 w-3 text-amber-600" />
          <span>Updates</span>
          <span
            v-if="availableUpdatesCount > 0"
            class="rounded-full bg-amber-500/20 px-1.5 py-0.2 text-[10px] font-bold text-amber-700 border border-amber-500/40"
          >
            {{ availableUpdatesCount }}
          </span>
        </button>
        <button
          @click="filter = 'stopped'"
          class="rounded-md px-3 py-1.5 text-xs font-semibold uppercase tracking-wider transition-colors"
          :class="filter === 'stopped' ? 'bg-slate-100 text-slate-700 shadow-sm' : 'text-slate-600 hover:text-slate-800'"
        >
          Stopped ({{ summary.stopped }})
        </button>
      </div>

      <!-- Controls: Grouping, Selection & Search -->
      <div class="flex flex-wrap items-center gap-2">
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

        <!-- Expand / Collapse All (active when groupByStack is true) -->
        <template v-if="groupByStack">
          <button
            @click="expandAllStacks"
            class="cyber-btn-secondary text-xs px-2.5 py-1.5 text-slate-600 hover:text-slate-800"
            title="Expand all stack groups"
          >
            Expand All
          </button>
          <button
            @click="collapseAllStacks"
            class="cyber-btn-secondary text-xs px-2.5 py-1.5 text-slate-600 hover:text-slate-800"
            title="Collapse all stack groups"
          >
            Collapse All
          </button>
        </template>

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
                  :is="group.key === 'docktun' ? ShieldCheck : group.isStack ? Layers : Server"
                  class="h-4 w-4"
                  :class="group.key === 'docktun' ? 'text-cyan-600' : group.isStack ? 'text-indigo-600' : 'text-slate-600'"
                />
                <span class="text-sm font-bold text-slate-900 font-mono group-hover:text-cyan-700 transition-colors">
                  {{ group.name }}
                </span>
                <span
                  v-if="group.key === 'docktun'"
                  class="rounded-full bg-cyan-500/10 border border-cyan-500/30 px-2 py-0.5 text-[10px] font-semibold text-cyan-700 font-mono"
                >
                  System Core
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
                @click="openContainerModal(group.containers[0], 'compose')"
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
                @click="openDeleteStackPrompt(group.projectId, group.name, group.containers[0]?.project_path)"
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
                :title="`Open stack '${container.project_name}' compose & controls`"
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
                  :href="`http://${cleanContainerName(container.name)}.local${getMdnsPortSuffix(container.ports)}`"
                  target="_blank"
                  rel="noopener"
                  @click.stop
                  class="inline-flex items-center gap-1 text-indigo-600 hover:text-indigo-700 hover:underline font-semibold"
                >
                  <span>http://{{ cleanContainerName(container.name) }}.local{{ getMdnsPortSuffix(container.ports) }}</span>
                  <ExternalLink class="h-3 w-3" />
                </a>
                <button
                  @click.stop="copyToClipboard('mdns-' + container.id, `http://${cleanContainerName(container.name)}.local${getMdnsPortSuffix(container.ports)}`)"
                  class="text-slate-400 hover:text-slate-600 ml-0.5"
                  title="Copy local .local URL"
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

    <!-- ======================================================== -->
    <!-- CONTAINER CONTROL CENTER (MODAL / FULL INSPECTOR)        -->
    <!-- ======================================================== -->
    <div
      v-if="selectedContainer"
      class="fixed inset-0 z-50 flex items-center justify-center bg-slate-50/85 p-2 sm:p-4 backdrop-blur-md"
      @mousedown="onBackdropMouseDown"
      @click="onBackdropClick($event, closeContainerModal)"
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
              :title="selectedContainer.running ? 'Running' : 'Stopped'"
            >
              <span
                v-if="selectedContainer.running"
                class="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75"
              ></span>
              <span
                class="relative inline-flex h-3 w-3 rounded-full"
                :class="selectedContainer.running ? 'bg-emerald-500' : 'bg-slate-300'"
              ></span>
            </span>

            <div>
              <div class="flex items-center gap-2">
                <h2 class="text-base font-bold text-slate-900 font-mono">{{ selectedContainer.name }}</h2>
                <span class="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] font-mono text-slate-600">
                  {{ selectedContainer.id.slice(0, 12) }}
                </span>
                <button
                  @click="copyToClipboard('cid', selectedContainer.id)"
                  class="text-slate-500 hover:text-slate-700"
                  title="Copy full Container ID"
                >
                  <Check v-if="copiedKey === 'cid'" class="h-3 w-3 text-emerald-600" />
                  <Copy v-else class="h-3 w-3" />
                </button>
              </div>
              <p class="text-[11px] text-slate-600 font-mono truncate max-w-md">{{ selectedContainer.image }}</p>
            </div>
          </div>

          <!-- Top Quick Actions -->
          <div class="flex items-center gap-2">
            <button
              v-if="!selectedContainer.running"
              @click="runAction(selectedContainer, 'start')"
              :disabled="!!actionLoading[selectedContainer.id]"
              class="cyber-btn flex items-center gap-1.5 text-xs text-emerald-700"
            >
              <Play class="h-3 w-3 fill-current" />
              <span>Start</span>
            </button>
            <button
              v-if="selectedContainer.running"
              @click="runAction(selectedContainer, 'stop')"
              :disabled="!!actionLoading[selectedContainer.id]"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-amber-700"
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
              class="rounded-lg p-1 text-slate-600 hover:bg-slate-100 hover:text-slate-800"
            >
              <X class="h-5 w-5" />
            </button>
          </div>
        </div>

        <!-- Modal Navigation Tabs -->
        <div class="flex items-center gap-1 overflow-x-auto border-b border-slate-200 bg-slate-50/80 px-4 py-2 text-xs">
          <button
            @click="handleTabChange('overview')"
            class="flex items-center gap-2 rounded-lg px-3 py-1.5 font-medium transition-colors whitespace-nowrap"
            :class="selectedTab === 'overview' ? 'bg-cyan-50/60 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
          >
            <Activity class="h-3.5 w-3.5" />
            <span>Overview</span>
          </button>

          <button
            @click="handleTabChange('access')"
            class="flex items-center gap-2 rounded-lg px-3 py-1.5 font-medium transition-colors whitespace-nowrap"
            :class="selectedTab === 'access' ? 'bg-cyan-50/60 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
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
            :class="selectedTab === 'compose' ? 'bg-cyan-50/60 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
          >
            <FileCode class="h-3.5 w-3.5" />
            <span>Compose</span>
            <span
              v-if="selectedContainer.project_id"
              class="rounded bg-cyan-100/60 px-1 py-0.2 text-[9px] text-cyan-700 font-mono"
            >
              stack
            </span>
          </button>

          <button
            @click="handleTabChange('env')"
            class="flex items-center gap-2 rounded-lg px-3 py-1.5 font-medium transition-colors whitespace-nowrap"
            :class="selectedTab === 'env' ? 'bg-cyan-50/60 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
          >
            <Sliders class="h-3.5 w-3.5" />
            <span>Environment (.env)</span>
          </button>

          <button
            @click="handleTabChange('files')"
            class="flex items-center gap-2 rounded-lg px-3 py-1.5 font-medium transition-colors whitespace-nowrap"
            :class="selectedTab === 'files' ? 'bg-cyan-50/60 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
          >
            <FolderOpen class="h-3.5 w-3.5" />
            <span>Files & Storage</span>
          </button>

          <button
            @click="handleTabChange('logs')"
            class="flex items-center gap-2 rounded-lg px-3 py-1.5 font-medium transition-colors whitespace-nowrap"
            :class="selectedTab === 'logs' ? 'bg-cyan-50/60 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
          >
            <ScrollText class="h-3.5 w-3.5" />
            <span>Realtime Logs</span>
          </button>

          <button
            @click="handleTabChange('exec')"
            class="flex items-center gap-2 rounded-lg px-3 py-1.5 font-medium transition-colors whitespace-nowrap"
            :class="selectedTab === 'exec' ? 'bg-cyan-50/60 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
          >
            <Terminal class="h-3.5 w-3.5" />
            <span>Terminal (Exec)</span>
          </button>
        </div>

        <!-- Tab Body Container -->
        <div class="flex-1 overflow-y-auto p-5 text-slate-800">
          <!-- Loading indicator -->
          <div v-if="detailLoading" class="flex items-center justify-center py-16 text-slate-600 text-sm">
            <RotateCcw class="h-5 w-5 animate-spin mr-2 text-cyan-600" />
            <span>Loading container telemetry & metadata…</span>
          </div>

          <!-- ============================================== -->
          <!-- TAB 1: OVERVIEW & TELEMETRY                    -->
          <!-- ============================================== -->
          <div v-else-if="selectedTab === 'overview'" class="space-y-5">
            <!-- Telemetry Gauges -->
            <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <!-- CPU (Click to view full Metrics) -->
              <router-link
                :to="`/metrics?container=${encodeURIComponent(selectedContainer.name)}`"
                class="rounded-lg border border-slate-200 bg-slate-50 p-3 hover:border-cyan-400 hover:shadow-xs transition group block"
                :title="`Open live telemetry in Metrics for ${selectedContainer.name}`"
              >
                <div class="flex items-center justify-between text-xs text-slate-600 mb-1">
                  <span class="flex items-center gap-1.5 font-semibold uppercase tracking-wider text-[10px] group-hover:text-cyan-600 transition">
                    <Cpu class="h-3.5 w-3.5 text-cyan-600" /> CPU Usage &rarr;
                  </span>
                  <span class="font-mono text-cyan-600 font-bold">{{ selectedContainer.cpu || '0.0%' }}</span>
                </div>
                <div class="h-2 w-full overflow-hidden rounded-full bg-slate-100">
                  <div
                    class="h-full bg-cyan-400 transition-all duration-300"
                    :style="{ width: `${parseCpuPercent(selectedContainer.cpu)}%` }"
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
                    {{ parseMemoryUsage(selectedContainer.memory).used }}
                  </span>
                </div>
                <div class="text-[10px] text-slate-500 font-mono truncate">
                  Limit: {{ parseMemoryUsage(selectedContainer.memory).total || 'Unlimited' }}
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
                  {{ selectedContainer.network || '0B / 0B' }}
                </div>
              </div>

              <!-- Disk / Block I/O -->
              <div class="rounded-lg border border-slate-200 bg-slate-50 p-3">
                <div class="flex items-center justify-between text-xs text-slate-600 mb-1">
                  <span class="flex items-center gap-1.5 font-semibold uppercase tracking-wider text-[10px]">
                    <HardDrive class="h-3.5 w-3.5 text-amber-600" /> Block I/O
                  </span>
                  <span class="text-[10px] text-slate-500 font-mono">{{ selectedContainer.pids || 0 }} pids</span>
                </div>
                <div class="font-mono text-xs text-slate-800 truncate">
                  {{ selectedContainer.block_io || '0B / 0B' }}
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
                    @click="checkSingleUpdate(selectedContainer)"
                    :disabled="!!checkingSingleUpdate[selectedContainer.id]"
                    class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-amber-700 hover:border-amber-500/50"
                  >
                    <RotateCcw class="h-3 w-3" :class="checkingSingleUpdate[selectedContainer.id] ? 'animate-spin' : ''" />
                    <span>{{ checkingSingleUpdate[selectedContainer.id] ? 'Checking…' : 'Check for Update' }}</span>
                  </button>
                  <button
                    v-if="selectedContainer.update_available"
                    @click="confirmUpdate(selectedContainer)"
                    :disabled="!!updatingContainer[selectedContainer.id]"
                    class="cyber-btn flex items-center gap-1.5 text-xs text-amber-200 border-amber-500 bg-amber-50/70 hover:bg-amber-100"
                  >
                    <ArrowUpCircle class="h-3.5 w-3.5 text-amber-600" :class="updatingContainer[selectedContainer.id] ? 'animate-spin' : ''" />
                    <span>{{ updatingContainer[selectedContainer.id] ? 'Updating…' : 'Update to Latest' }}</span>
                  </button>
                </div>
              </div>

              <div class="grid grid-cols-1 md:grid-cols-2 gap-3 text-xs font-mono">
                <div>
                  <span class="text-slate-500">Image Tag:</span>
                  <span class="ml-2 text-cyan-700 font-bold">{{ selectedContainer.image }}</span>
                </div>
                <div>
                  <span class="text-slate-500">Registry Status:</span>
                  <span v-if="selectedContainer.update_available" class="ml-2 inline-flex items-center gap-1 text-amber-600 font-bold animate-pulse">
                    <ArrowUpCircle class="h-3.5 w-3.5" />
                    Update Available on Registry
                  </span>
                  <span v-else-if="selectedContainer.update_error && (selectedContainer.update_error.includes('built locally') || selectedContainer.update_error.includes('Local'))" class="ml-2 inline-flex items-center gap-1 text-cyan-700 font-semibold">
                    <Info class="h-3.5 w-3.5" />
                    Local / Git Managed
                  </span>
                  <span v-else-if="selectedContainer.update_checked_at && !selectedContainer.update_error" class="ml-2 inline-flex items-center gap-1 text-emerald-600 font-bold">
                    <CheckCircle2 class="h-3.5 w-3.5" />
                    Up to Date
                  </span>
                  <span v-else class="ml-2 text-slate-500">
                    Not checked yet
                  </span>
                </div>
                <div v-if="selectedContainer.current_digest" class="truncate">
                  <span class="text-slate-500">Current Digest:</span>
                  <span class="ml-2 text-slate-700 truncate" :title="selectedContainer.current_digest">{{ selectedContainer.current_digest.substring(0, 19) }}…</span>
                </div>
                <div v-if="selectedContainer.latest_digest" class="truncate">
                  <span class="text-slate-500">Latest Remote Digest:</span>
                  <span class="ml-2 text-amber-700 truncate" :title="selectedContainer.latest_digest">{{ selectedContainer.latest_digest.substring(0, 19) }}…</span>
                </div>
                <div v-if="selectedContainer.update_checked_at" class="md:col-span-2 text-slate-500 text-[11px]">
                  Last checked: {{ selectedContainer.update_checked_at }}
                </div>
                <div v-if="selectedContainer.update_error" class="md:col-span-2 text-[11px]" :class="(selectedContainer.update_error.includes('built locally') || selectedContainer.update_error.includes('Local')) ? 'text-cyan-700/90' : 'text-amber-600/90'">
                  Notice: {{ selectedContainer.update_error }}
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
                  <span class="ml-2 text-slate-800">{{ selectedContainer.uptime || selectedContainer.status }}</span>
                </div>
                <div>
                  <span class="text-slate-500">Restart Count:</span>
                  <span class="ml-2 text-slate-800">{{ selectedContainer.restart_count || 0 }}</span>
                </div>
                <div>
                  <span class="text-slate-500">IP Address:</span>
                  <span class="ml-2 text-slate-800">{{ selectedContainer.ip_address || 'Host / Dynamic' }}</span>
                </div>
                <div>
                  <span class="text-slate-500">Gateway:</span>
                  <span class="ml-2 text-slate-800">{{ selectedContainer.gateway || '—' }}</span>
                </div>
                <div>
                  <span class="text-slate-500">Assigned Networks:</span>
                  <span class="ml-2 text-slate-800">{{ selectedContainer.networks?.join(', ') || 'bridge' }}</span>
                </div>
                <div>
                  <span class="text-slate-500">Cloudflared Network:</span>
                  <span class="ml-2" :class="selectedContainer.connected_to_cloudflared ? 'text-emerald-600 font-bold' : 'text-slate-600'">
                    {{ selectedContainer.connected_to_cloudflared ? 'Connected (Internal routing active)' : 'Not Connected' }}
                  </span>
                </div>
                <div class="md:col-span-2">
                  <span class="text-slate-500">Command:</span>
                  <span class="ml-2 text-slate-700 break-all">{{ selectedContainer.command || '—' }}</span>
                </div>
                <div class="md:col-span-2">
                  <span class="text-slate-500">Published Ports:</span>
                  <span class="ml-2 text-cyan-700">{{ selectedContainer.ports || 'None (Internal only)' }}</span>
                </div>
              </div>
            </div>

            <!-- Volumes & Mounts -->
            <div class="rounded-lg border border-slate-200 bg-slate-50 p-4 space-y-3">
              <h3 class="text-xs font-bold uppercase tracking-wider text-slate-600 flex items-center gap-2">
                <HardDrive class="h-4 w-4 text-amber-600" /> Volumes & Mounts
              </h3>
              <div v-if="selectedContainer.mounts && selectedContainer.mounts.length > 0" class="space-y-2">
                <div
                  v-for="(m, i) in selectedContainer.mounts"
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
                  <Sliders class="h-4 w-4 text-violet-400" /> Environment Variables ({{ selectedContainer.env?.length || 0 }})
                </h3>
              </div>
              <div v-if="selectedContainer.env && selectedContainer.env.length > 0" class="max-h-56 overflow-y-auto space-y-1 font-mono text-xs">
                <div
                  v-for="(e, i) in selectedContainer.env"
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
          <div v-else-if="selectedTab === 'access'" class="space-y-5">
            <!-- 1. PRIMARY LOCAL ACCESS (mDNS .local) -->
            <div class="cyber-card space-y-3.5 border-indigo-200 bg-gradient-to-br from-indigo-50/60 to-white">
              <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-2 border-b border-indigo-100 pb-3">
                <div class="flex items-center gap-2">
                  <Wifi class="h-4 w-4 text-indigo-600" />
                  <div>
                    <h3 class="text-sm font-bold text-slate-900">Local LAN Access (mDNS)</h3>
                    <p class="text-xs text-slate-500">
                      Reach this container directly from any computer, phone, or tablet on your Wi-Fi with zero configuration.
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
                    http://{{ cleanContainerName(selectedContainer.name) }}.local{{ getMdnsPortSuffix(selectedContainer.ports) }}
                  </div>
                </div>

                <div class="flex items-center gap-2">
                  <a
                    :href="`http://${cleanContainerName(selectedContainer.name)}.local${getMdnsPortSuffix(selectedContainer.ports)}`"
                    target="_blank"
                    rel="noopener"
                    class="cyber-btn flex items-center gap-1.5 text-xs text-white bg-indigo-600 hover:bg-indigo-700"
                  >
                    <span>Open</span>
                    <ExternalLink class="h-3 w-3" />
                  </a>
                  <button
                    @click="copyToClipboard('mdns', `http://${cleanContainerName(selectedContainer.name)}.local${getMdnsPortSuffix(selectedContainer.ports)}`)"
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
                  <span class="font-mono font-semibold text-slate-800">{{ selectedContainer.ports || 'None' }}</span>
                </div>
                <div class="rounded border border-slate-200 bg-slate-50/80 p-2">
                  <span class="text-slate-500 text-[11px] block">Docker networks:</span>
                  <span class="font-mono font-semibold text-slate-800 truncate block">{{ selectedContainer.networks?.join(', ') || 'bridge' }}</span>
                </div>
              </div>
            </div>

            <!-- 2. REMOTE EXPOSURE (Cloudflare, Tailscale, WireGuard) -->
            <ContainerExposure :container="selectedContainer" :settings="getExpose(selectedContainer.id)" :saving="exposeSaving[selectedContainer.id]" :message="exposeMsg[selectedContainer.id]" @save="saveExposure(selectedContainer)" />
          </div>

          <!-- ============================================== -->
          <!-- TAB 3: COMPOSE (docker-compose.yml)            -->
          <!-- ============================================== -->
          <div v-else-if="selectedTab === 'compose'" class="space-y-4">
            <!-- If Container is Linked to a Project -->
            <div v-if="selectedContainer.project_id" class="space-y-4">
              <div class="flex flex-col gap-3 border-b border-slate-200 pb-3">
                <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-2">
                  <div>
                    <h3 class="text-sm font-bold text-slate-900 flex items-center gap-2">
                      <FileCode class="h-4 w-4 text-cyan-600" />
                      <span>Stack Compose: {{ selectedContainer.project_name || selectedContainer.name }}</span>
                    </h3>
                    <p class="text-xs text-slate-600 font-mono">
                      {{ selectedContainer.project_path }}/{{ composeFilename }}
                    </p>
                  </div>

                  <!-- Stack Action Buttons -->
                  <div class="flex flex-wrap items-center gap-1.5">
                    <span v-if="composeMsg" class="text-xs text-emerald-600 font-medium mr-1">
                      {{ composeMsg }}
                    </span>

                    <button
                      @click="runStackAction(selectedContainer.project_id, 'up')"
                      :disabled="!!stackActionLoading[selectedContainer.project_id]"
                      class="cyber-btn-secondary text-xs flex items-center gap-1 text-emerald-600 hover:border-emerald-500/60 hover:bg-emerald-50/30"
                      title="docker compose up -d"
                    >
                      <Play class="h-3 w-3 fill-current" />
                      <span>Stack Up</span>
                    </button>

                    <button
                      @click="runStackAction(selectedContainer.project_id, 'down')"
                      :disabled="!!stackActionLoading[selectedContainer.project_id]"
                      class="cyber-btn-secondary text-xs flex items-center gap-1 text-amber-600 hover:border-amber-500/60 hover:bg-amber-50/30"
                      title="docker compose down"
                    >
                      <Square class="h-3 w-3 fill-current" />
                      <span>Stack Down</span>
                    </button>

                    <button
                      @click="runStackAction(selectedContainer.project_id, 'restart')"
                      :disabled="!!stackActionLoading[selectedContainer.project_id]"
                      class="cyber-btn-secondary text-xs flex items-center gap-1 text-cyan-700 hover:border-cyan-500/60 hover:bg-cyan-50/30"
                      title="docker compose restart"
                    >
                      <RotateCcw class="h-3 w-3" :class="stackActionLoading[selectedContainer.project_id] ? 'animate-spin' : ''" />
                      <span>Restart</span>
                    </button>

                    <button
                      @click="openDeployPrompt(selectedContainer.project_id, selectedContainer.project_name || selectedContainer.name)"
                      :disabled="!!stackActionLoading[selectedContainer.project_id]"
                      class="cyber-btn-secondary text-xs flex items-center gap-1 text-violet-300 hover:border-violet-500/60 hover:bg-violet-950/30"
                      title="Git pull & docker compose up -d --build"
                    >
                      <Rocket class="h-3 w-3 text-violet-400" />
                      <span>Deploy / Pull</span>
                    </button>

                    <button
                      @click="openStackHistory(selectedContainer.project_id, selectedContainer.project_name || selectedContainer.name)"
                      class="cyber-btn-secondary text-xs flex items-center gap-1 text-slate-700"
                      title="View deployment & stack action history"
                    >
                      <History class="h-3 w-3 text-slate-600" />
                      <span>History</span>
                    </button>

                    <button
                      v-if="selectedContainer.project_name !== 'docktun'"
                      @click="openDeleteStackPrompt(selectedContainer.project_id, selectedContainer.project_name || selectedContainer.name, selectedContainer.project_path)"
                      :disabled="!!stackActionLoading[selectedContainer.project_id]"
                      class="cyber-btn-secondary text-xs flex items-center gap-1 text-rose-600 hover:border-rose-400 hover:bg-rose-50/50"
                      title="Delete this stack"
                    >
                      <Trash2 class="h-3 w-3" />
                      <span>Delete Stack</span>
                    </button>

                    <button
                      @click="saveContainerCompose(false)"
                      :disabled="composeSaving"
                      class="cyber-btn text-xs flex items-center gap-1 text-cyan-700 border-cyan-600 bg-cyan-50/40 hover:bg-cyan-100/50"
                      title="Save edits to docker-compose.yml"
                    >
                      <Save class="h-3 w-3" />
                      <span>Save YAML</span>
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
                          @click="saveProjectGit"
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
                  v-model="composeContent"
                  rows="18"
                  spellcheck="false"
                  placeholder="Loading compose file…"
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
                  @click="scaffoldProject"
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
          <div v-else-if="selectedTab === 'env'" class="space-y-4">
            <div v-if="selectedContainer.project_id" class="space-y-4">
              <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-2 border-b border-slate-200 pb-3">
                <div>
                  <h3 class="text-sm font-bold text-slate-900 flex items-center gap-2">
                    <Sliders class="h-4 w-4 text-violet-400" />
                    <span>Environment File (.env)</span>
                  </h3>
                  <p class="text-xs text-slate-600 font-mono">
                    {{ selectedContainer.project_path }}/.env
                  </p>
                </div>

                <div class="flex items-center gap-2">
                  <span v-if="envMsg" class="text-xs text-emerald-600 font-medium">
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
                    class="cyber-btn text-xs flex items-center gap-1.5 text-cyan-700 border-cyan-600 bg-cyan-50/40 hover:bg-cyan-100/50"
                  >
                    <Save class="h-3.5 w-3.5" />
                    <span>Save</span>
                  </button>
                  <button
                    @click="saveContainerEnv(true)"
                    :disabled="envSaving"
                    class="cyber-btn text-xs flex items-center gap-1.5 text-emerald-700 border-emerald-600 bg-emerald-50/40 hover:bg-emerald-100/50"
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
                class="w-full rounded-lg border border-slate-200 bg-slate-50 p-4 font-mono text-xs text-slate-800 focus:border-violet-500 focus:outline-none leading-relaxed"
              ></textarea>
            </div>

            <div v-else class="rounded-xl border border-slate-200 bg-slate-50 p-8 text-center space-y-4">
              <Sliders class="mx-auto h-10 w-10 text-violet-400" />
              <div class="max-w-md mx-auto space-y-1">
                <h3 class="text-base font-bold text-slate-900">Standalone Container</h3>
                <p class="text-xs text-slate-600">
                  Generate a project to manage persistent environment variables in a dedicated <code class="text-violet-300">.env</code> file.
                </p>
              </div>
              <button
                @click="scaffoldProject"
                :disabled="projectScaffolding"
                class="cyber-btn flex items-center gap-2 mx-auto text-xs text-cyan-700 border-cyan-500 bg-cyan-50/60 px-5 py-2.5"
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
              <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-200 pb-3">
                <div class="flex items-center gap-2 text-xs font-mono">
                  <FolderOpen class="h-4 w-4 text-amber-600" />
                  <span class="text-slate-600 cursor-pointer hover:text-slate-800" @click="loadContainerFiles(selectedContainer.project_id, '')">
                    {{ selectedContainer.project_path }}
                  </span>
                  <span v-if="currentDir" class="text-slate-600">/</span>
                  <span v-if="currentDir" class="text-cyan-700">{{ currentDir }}</span>
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
              <div v-if="editingFile" class="rounded-xl border border-slate-200 bg-slate-50 p-4 space-y-3">
                <div class="flex items-center justify-between border-b border-slate-200 pb-2">
                  <div class="flex items-center gap-2 text-xs font-mono text-cyan-700">
                    <FileText class="h-4 w-4 text-cyan-600" />
                    <span>{{ editingFile.path }}</span>
                  </div>
                  <div class="flex items-center gap-2">
                    <span v-if="editingFileMsg" class="text-xs text-emerald-600 font-medium">{{ editingFileMsg }}</span>
                    <button
                      @click="saveCurrentContainerFile"
                      :disabled="editingFileSaving"
                      class="cyber-btn text-xs flex items-center gap-1.5 text-cyan-700"
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
                  class="w-full rounded border border-slate-200 bg-white p-3 font-mono text-xs text-slate-800 focus:outline-none"
                ></textarea>
              </div>

              <!-- File List View -->
              <div v-else class="rounded-xl border border-slate-200 bg-slate-50 divide-y divide-slate-200/80">
                <div v-if="fileList.length === 0 && !filesLoading" class="p-8 text-center text-xs text-slate-500">
                  Folder is empty.
                </div>
                <div
                  v-for="item in fileList"
                  :key="item.path"
                  class="flex items-center justify-between px-4 py-2.5 text-xs hover:bg-white/60 transition-colors"
                >
                  <div
                    class="flex items-center gap-2.5 cursor-pointer font-mono truncate"
                    @click="openContainerFile(item)"
                  >
                    <Folder v-if="item.is_dir" class="h-4 w-4 text-amber-600 shrink-0" />
                    <FileText v-else class="h-4 w-4 text-slate-600 shrink-0" />
                    <span :class="item.is_dir ? 'text-slate-800 font-semibold hover:text-cyan-700' : 'text-slate-700 hover:text-cyan-700'">
                      {{ item.name }}
                    </span>
                  </div>

                  <div class="flex items-center gap-4 text-slate-500 font-mono text-[11px]">
                    <span v-if="!item.is_dir">{{ (item.size / 1024).toFixed(1) }} KB</span>
                    <span>{{ item.mod_time }}</span>
                    <button
                      @click="deleteContainerFile(item.path)"
                      class="text-slate-600 hover:text-red-600 transition-colors"
                      title="Delete file"
                    >
                      <Trash2 class="h-3.5 w-3.5" />
                    </button>
                  </div>
                </div>
              </div>

              <!-- New Item Modal -->
              <div v-if="showNewItemModal" class="fixed inset-0 z-50 flex items-center justify-center bg-slate-50/80 p-4">
                <div class="w-full max-w-sm rounded-xl border border-slate-200 bg-white p-5 space-y-4">
                  <h4 class="text-sm font-bold text-slate-900">Create New Item</h4>
                  <div>
                    <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">Item Name</label>
                    <input
                      v-model="newItemName"
                      type="text"
                      placeholder="filename.txt or folder_name"
                      class="cyber-input text-xs font-mono"
                    />
                  </div>
                  <div class="flex items-center gap-2 text-xs text-slate-700">
                    <input type="checkbox" id="isDirCheck" v-model="newItemIsDir" class="rounded bg-slate-50" />
                    <label for="isDirCheck">Create as directory</label>
                  </div>
                  <div class="flex justify-end gap-2 pt-2">
                    <button @click="showNewItemModal = false" class="cyber-btn-secondary text-xs">Cancel</button>
                    <button @click="createContainerFileItem" class="cyber-btn text-xs text-cyan-700">Create</button>
                  </div>
                </div>
              </div>
            </div>

            <div v-else class="space-y-4">
              <div class="rounded-xl border border-slate-200 bg-slate-50 p-5 space-y-3">
                <h3 class="text-xs font-bold uppercase tracking-wider text-slate-600 flex items-center gap-2">
                  <HardDrive class="h-4 w-4 text-amber-600" /> Container Mounts & Storage
                </h3>
                <div v-if="selectedContainer.mounts && selectedContainer.mounts.length > 0" class="space-y-2 font-mono text-xs">
                  <div
                    v-for="(m, i) in selectedContainer.mounts"
                    :key="i"
                    class="rounded border border-slate-200 bg-white/60 p-3 space-y-1"
                  >
                    <div class="flex items-center justify-between">
                      <span class="text-slate-600">Host Path:</span>
                      <span class="text-cyan-600">{{ m.source }}</span>
                    </div>
                    <div class="flex items-center justify-between">
                      <span class="text-slate-600">Container Path:</span>
                      <span class="text-emerald-600">{{ m.destination }}</span>
                    </div>
                    <div class="flex items-center justify-between text-[11px] text-slate-500 pt-1">
                      <span>Permissions:</span>
                      <span>{{ m.rw ? 'Read/Write' : 'Read-Only' }}</span>
                    </div>
                  </div>
                </div>
                <p v-else class="text-xs text-slate-500">No storage mounts configured.</p>
              </div>

              <div class="rounded-xl border border-slate-200 bg-slate-50 p-6 text-center space-y-3">
                <FolderOpen class="mx-auto h-8 w-8 text-amber-600" />
                <p class="text-xs text-slate-600">Want full web file browsing and editing for this container?</p>
                <button
                  @click="scaffoldProject"
                  :disabled="projectScaffolding"
                  class="cyber-btn text-xs text-cyan-700 border-cyan-500 bg-cyan-50/60"
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
            <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-200 pb-3">
              <div class="space-y-0.5">
                <div class="flex flex-wrap items-center gap-2">
                  <ScrollText class="h-4 w-4 text-cyan-600" />
                  <span class="text-sm font-bold text-slate-900 font-mono">{{ selectedContainer.name }}</span>
                  <span class="rounded bg-slate-100 px-2 py-0.5 text-[10px] text-slate-600 font-mono">{{ selectedContainer.image }}</span>
                  <span class="inline-flex items-center gap-1 rounded bg-emerald-50/60 border border-emerald-800/50 px-2 py-0.5 text-[10px] text-emerald-600 font-medium">
                    <span class="h-1.5 w-1.5 rounded-full bg-emerald-400 animate-pulse"></span>
                    Isolated Service Logs
                  </span>
                </div>
                <p class="text-[11px] text-slate-500 font-mono">
                  Streaming stdout & stderr exclusively for container <span class="text-slate-600 font-bold">{{ selectedContainer.id?.slice(0, 12) }}</span>
                </p>
              </div>

              <div class="flex flex-wrap items-center gap-2">
                <label class="flex items-center gap-1.5 text-xs text-slate-600 cursor-pointer">
                  <input type="checkbox" v-model="modalLogsAutoRefresh" class="rounded bg-slate-50" />
                  <span>Live (3s)</span>
                </label>

                <label class="flex items-center gap-1.5 text-xs text-slate-600 cursor-pointer">
                  <input type="checkbox" v-model="modalLogsTimestamps" @change="fetchModalLogs" class="rounded bg-slate-50" />
                  <span>Timestamps</span>
                </label>

                <label class="flex items-center gap-1.5 text-xs text-slate-600 cursor-pointer">
                  <input type="checkbox" v-model="modalLogsWrap" class="rounded bg-slate-50" />
                  <span>Wrap</span>
                </label>

                <select
                  v-model.number="modalLogsTail"
                  @change="fetchModalLogs"
                  class="rounded border border-slate-300 bg-slate-50 px-2 py-1 text-xs text-slate-700 font-mono"
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
                  <Check v-if="copiedKey === 'logs'" class="h-3 w-3 text-emerald-600" />
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
            <div class="flex items-center justify-between gap-3 bg-white/60 border border-slate-200/80 rounded-lg px-3 py-1.5">
              <div class="flex items-center gap-2 flex-1">
                <Search class="h-3.5 w-3.5 text-slate-500" />
                <input
                  v-model="modalLogsFilter"
                  type="text"
                  placeholder="Filter service logs (case-insensitive substring)..."
                  class="w-full bg-transparent text-xs text-slate-800 placeholder-slate-500 focus:outline-none font-mono"
                />
              </div>
              <span v-if="matchingLinesCount" class="text-[10px] text-cyan-600 font-mono whitespace-nowrap">
                {{ matchingLinesCount.matched }} / {{ matchingLinesCount.total }} lines
              </span>
              <button
                v-if="modalLogsFilter"
                @click="modalLogsFilter = ''"
                class="text-slate-500 hover:text-slate-700 text-xs"
              >
                Clear
              </button>
            </div>

            <!-- Terminal Output Box -->
            <div
              class="max-h-[58vh] overflow-y-auto rounded-lg border border-slate-200 bg-slate-50 p-4 font-mono text-xs leading-5 text-slate-700"
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

          <!-- ============================================== -->
          <!-- TAB 7: TERMINAL (CONTAINER EXEC)               -->
          <!-- ============================================== -->
          <div v-else-if="selectedTab === 'exec'" class="space-y-4">
            <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-200 pb-3">
              <div>
                <h3 class="text-sm font-bold text-slate-900 flex items-center gap-2 font-mono">
                  <Terminal class="h-4 w-4 text-cyan-600" />
                  <span>Interactive Container Exec: {{ selectedContainer.name }}</span>
                </h3>
                <p class="text-[11px] text-slate-600 font-mono mt-0.5">
                  Execute commands directly inside container via Docker exec
                </p>
              </div>

              <!-- Quick command chips -->
              <div class="flex flex-wrap items-center gap-1.5 text-xs font-mono">
                <span class="text-[10px] text-slate-500 uppercase tracking-wider mr-1">Presets:</span>
                <button
                  v-for="preset in ['ls -la', 'ps aux', 'env', 'df -h', 'ip a', 'uptime']"
                  :key="preset"
                  @click="execCmd = preset; runContainerExec()"
                  class="rounded bg-white border border-slate-200 px-2 py-0.5 text-[11px] text-slate-700 hover:border-cyan-700 hover:text-cyan-700 transition"
                >
                  {{ preset }}
                </button>
              </div>
            </div>

            <!-- Command Input Bar -->
            <div class="flex items-center gap-2">
              <div class="relative flex-1">
                <span class="absolute left-3 top-2.5 font-mono text-cyan-600 text-xs font-bold">$</span>
                <input
                  v-model="execCmd"
                  @keyup.enter="runContainerExec"
                  placeholder="Enter command to run (e.g. ls -la, cat /etc/resolv.conf, curl localhost)..."
                  class="cyber-input w-full pl-8 font-mono text-xs text-cyan-200"
                  :disabled="execLoading"
                />
              </div>

              <button
                @click="runContainerExec"
                :disabled="execLoading || !execCmd.trim()"
                class="cyber-btn flex items-center gap-1.5 text-xs text-cyan-700 border-cyan-600 bg-cyan-50/40 hover:bg-cyan-100/50"
              >
                <RotateCcw v-if="execLoading" class="h-3.5 w-3.5 animate-spin" />
                <Play v-else class="h-3.5 w-3.5 fill-current" />
                <span>{{ execLoading ? 'Running...' : 'Execute' }}</span>
              </button>

              <button
                v-if="execOutput"
                @click="execOutput = ''; execExitCode = null"
                class="cyber-btn-secondary text-xs"
                title="Clear output"
              >
                Clear
              </button>
            </div>

            <!-- Output Box -->
            <div class="space-y-2">
              <div class="flex items-center justify-between text-xs font-mono text-slate-600">
                <span class="text-[10px] uppercase tracking-wider">Console Output</span>
                <span
                  v-if="execExitCode !== null"
                  class="rounded px-2 py-0.5 text-[10px] font-bold"
                  :class="execExitCode === 0 ? 'bg-emerald-50 text-emerald-600 border border-emerald-800' : 'bg-red-50 text-red-600 border border-red-800'"
                >
                  Exit Code: {{ execExitCode }}
                </span>
              </div>

              <div class="max-h-[50vh] min-h-[160px] overflow-y-auto rounded-lg border border-slate-200 bg-slate-50 p-4 font-mono text-xs leading-relaxed text-slate-800 shadow-inner">
                <pre v-if="execOutput" class="whitespace-pre-wrap break-all">{{ execOutput }}</pre>
                <p v-else class="text-slate-600 italic">No command has been executed yet. Enter a command or click a preset above.</p>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- ============================================== -->
    <!-- UPDATE CONFIRMATION MODAL                      -->
    <!-- ============================================== -->
    <div
      v-if="showUpdateConfirmModal"
      class="fixed inset-0 z-50 flex items-center justify-center bg-slate-50/85 p-4 backdrop-blur-md"
      @mousedown="onBackdropMouseDown"
      @click="onBackdropClick($event, () => showUpdateConfirmModal = null)"
    >
      <div class="w-full max-w-lg rounded-xl border border-amber-500/40 bg-white p-6 shadow-sm space-y-5" @mousedown.stop @mouseup.stop @click.stop>
        <div class="flex items-start justify-between">
          <div class="flex items-center gap-3">
            <div class="rounded-lg bg-amber-500/20 p-2.5 text-amber-600 border border-amber-500/30">
              <ArrowUpCircle class="h-6 w-6" />
            </div>
            <div>
              <h3 class="text-lg font-bold text-slate-900">Update Container</h3>
              <p class="text-xs text-slate-600 font-mono">{{ showUpdateConfirmModal.container.name }}</p>
            </div>
          </div>
          <button @click="showUpdateConfirmModal = null" class="text-slate-600 hover:text-slate-800">
            <X class="h-5 w-5" />
          </button>
        </div>

        <div class="space-y-3 rounded-lg border border-slate-200 bg-slate-50 p-4 text-xs font-mono">
          <div class="flex justify-between">
            <span class="text-slate-500 font-sans">Target Image:</span>
            <span class="text-cyan-700 font-bold">{{ showUpdateConfirmModal.container.image }}</span>
          </div>
          <div v-if="showUpdateConfirmModal.container.project_name" class="flex justify-between">
            <span class="text-slate-500 font-sans">Stack / Compose:</span>
            <span class="text-emerald-600">{{ showUpdateConfirmModal.container.project_name }}</span>
          </div>
          <div class="flex justify-between">
            <span class="text-slate-500 font-sans">Cloudflared Network:</span>
            <span :class="showUpdateConfirmModal.container.connected_to_cloudflared ? 'text-emerald-600 font-bold' : 'text-slate-600'">
              {{ showUpdateConfirmModal.container.connected_to_cloudflared ? 'Yes (Will maintain connection)' : 'No' }}
            </span>
          </div>
        </div>

        <p class="text-xs text-slate-700 leading-relaxed">
          DockTun will pull the latest image layers from the remote registry, cleanly recreate the container with your existing volume mounts, environment variables, published ports, and network routes, then verify health.
        </p>

        <div class="flex items-center justify-end gap-3 pt-2">
          <button
            @click="showUpdateConfirmModal = null"
            class="cyber-btn-secondary text-xs"
          >
            Cancel
          </button>
          <button
            @click="performUpdate(showUpdateConfirmModal.container)"
            class="cyber-btn flex items-center gap-2 text-xs text-amber-200 border-amber-500/80 bg-gradient-to-r from-amber-600 to-orange-600 hover:from-amber-500 hover:to-orange-500 font-bold shadow-lg shadow-amber-900/30"
          >
            <ArrowUpCircle class="h-4 w-4" />
            <span>Pull & Recreate Container</span>
          </button>
        </div>
      </div>
    </div>

    <!-- ============================================== -->
    <!-- UPDATE RESULT MODAL                            -->
    <!-- ============================================== -->
    <div
      v-if="updateResultModal"
      class="fixed inset-0 z-50 flex items-center justify-center bg-slate-50/85 p-4 backdrop-blur-md"
      @mousedown="onBackdropMouseDown"
      @click="onBackdropClick($event, () => updateResultModal = null)"
    >
      <div
        class="w-full max-w-2xl rounded-xl border bg-white p-6 shadow-sm space-y-4"
        :class="updateResultModal.success ? 'border-emerald-500/40' : 'border-red-500/40'"
        @mousedown.stop
        @mouseup.stop
        @click.stop
      >
        <div class="flex items-start justify-between">
          <div class="flex items-center gap-3">
            <div
              class="rounded-lg p-2.5 border"
              :class="updateResultModal.success ? 'bg-emerald-500/20 text-emerald-600 border-emerald-500/30' : 'bg-red-500/20 text-red-600 border-red-500/30'"
            >
              <CheckCircle2 v-if="updateResultModal.success" class="h-6 w-6" />
              <AlertCircle v-else class="h-6 w-6" />
            </div>
            <div>
              <h3 class="text-lg font-bold text-slate-900">
                {{ updateResultModal.success ? 'Container Updated Successfully' : 'Update Failed' }}
              </h3>
              <p class="text-xs text-slate-600 font-mono">{{ updateResultModal.name }}</p>
            </div>
          </div>
          <button @click="updateResultModal = null" class="text-slate-600 hover:text-slate-800">
            <X class="h-5 w-5" />
          </button>
        </div>

        <div class="max-h-72 overflow-y-auto rounded-lg border border-slate-200 bg-slate-50 p-4 font-mono text-xs leading-relaxed text-slate-700 shadow-inner">
          <pre class="whitespace-pre-wrap break-all">{{ updateResultModal.output }}</pre>
        </div>

        <div class="flex items-center justify-end">
          <button
            @click="updateResultModal = null"
            class="cyber-btn text-xs"
          >
            Close
          </button>
        </div>
      </div>
    </div>

    <!-- ============================================== -->
    <!-- LAUNCH MODAL (DOCKER RUN & GIT STACK LAUNCHER) -->
    <!-- ============================================== -->
    <div
      v-if="showLaunchModal"
      class="fixed inset-0 z-50 flex items-center justify-center bg-slate-50/85 p-4 backdrop-blur-md"
      @mousedown="onBackdropMouseDown"
      @click="onBackdropClick($event, onLaunchModalBackdropClick)"
    >
      <div
        class="w-full max-w-4xl rounded-xl border border-slate-300 bg-white p-6 shadow-sm space-y-4 max-h-[92vh] flex flex-col"
        @mousedown.stop
        @mouseup.stop
        @click.stop
      >
        <div class="flex items-start justify-between border-b border-slate-200 pb-3 shrink-0">
          <div>
            <h3 class="text-lg font-bold text-slate-900 flex items-center gap-2">
              <Rocket class="h-5 w-5 text-cyan-600" />
              <span>Deploy New Container or Stack</span>
            </h3>
            <p class="text-xs text-slate-600">Launch a Docker Compose stack, use 1-click templates, execute Docker run commands, or connect a Git repository.</p>
          </div>
          <button @click="showLaunchModal = false" class="text-slate-600 hover:text-slate-800">
            <X class="h-5 w-5" />
          </button>
        </div>

        <!-- Mode Tabs -->
        <div class="flex gap-2 border-b border-slate-200 pb-2 shrink-0 overflow-x-auto">
          <button
            @click="launchTab = 'compose'"
            class="flex items-center gap-2 rounded px-3 py-1.5 text-xs font-semibold transition shrink-0"
            :class="launchTab === 'compose' ? 'bg-cyan-50/80 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
          >
            <FileCode class="h-3.5 w-3.5" />
            <span>Docker Compose (YAML)</span>
          </button>
          <button
            @click="launchTab = 'templates'"
            class="flex items-center gap-2 rounded px-3 py-1.5 text-xs font-semibold transition shrink-0"
            :class="launchTab === 'templates' ? 'bg-cyan-50/80 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
          >
            <Sparkles class="h-3.5 w-3.5 text-amber-600" />
            <span>App Templates</span>
          </button>
          <button
            @click="launchTab = 'dockerrun'"
            class="flex items-center gap-2 rounded px-3 py-1.5 text-xs font-semibold transition shrink-0"
            :class="launchTab === 'dockerrun' ? 'bg-cyan-50/80 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
          >
            <Terminal class="h-3.5 w-3.5" />
            <span>Docker Run Command</span>
          </button>
          <button
            @click="launchTab = 'gitstack'"
            class="flex items-center gap-2 rounded px-3 py-1.5 text-xs font-semibold transition shrink-0"
            :class="launchTab === 'gitstack' ? 'bg-cyan-50/80 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
          >
            <GitBranch class="h-3.5 w-3.5" />
            <span>Git / Remote Stack</span>
          </button>
        </div>

        <!-- TAB 1: Docker Compose YAML Editor -->
        <div v-if="launchTab === 'compose'" class="space-y-4 overflow-y-auto pr-1 flex-1">
          <div class="grid gap-3 sm:grid-cols-2 text-xs">
            <div>
              <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">
                Stack / Project Name <span class="text-rose-600">*</span>
              </label>
              <input
                v-model="composeDeployForm.name"
                class="cyber-input text-xs font-mono"
                placeholder="e.g. my-stack"
              />
            </div>
            <div>
              <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">
                Host Working Directory Path (Optional)
              </label>
              <input
                v-model="composeDeployForm.path"
                class="cyber-input text-xs font-mono"
                :placeholder="composeDeployForm.name ? `./projects/${composeDeployForm.name}` : './projects/my-stack'"
              />
            </div>
          </div>

          <div>
            <!-- Banner when docker run command is pasted in Compose YAML tab -->
            <div v-if="isPastedDockerRun" class="mb-2.5 rounded-lg border border-cyan-500/60 bg-cyan-50/80 p-2.5 flex flex-col sm:flex-row items-start sm:items-center justify-between gap-2 text-xs">
              <div class="flex items-center gap-2 text-cyan-800">
                <Sparkles class="h-4 w-4 text-cyan-600 shrink-0" />
                <span><strong>Docker Run command detected!</strong> Convert to clean Compose YAML, or DockTun will auto-convert when you deploy.</span>
              </div>
              <div class="flex items-center gap-2 shrink-0">
                <button
                  type="button"
                  @click="convertComposeDockerRun"
                  class="cyber-btn py-1 px-2.5 text-[11px] text-cyan-700 bg-white border-cyan-600 font-bold"
                >
                  Convert to Compose YAML
                </button>
                <button
                  type="button"
                  @click="launchTab = 'dockerrun'; dockerRunCmd = composeDeployForm.compose_content"
                  class="text-[11px] text-slate-600 hover:text-slate-900 underline font-mono"
                >
                  Switch to Docker Run
                </button>
              </div>
            </div>

            <div class="flex flex-wrap items-center justify-between gap-1.5 mb-1.5">
              <div class="flex items-center gap-2">
                <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-600">
                  docker-compose.yml Content <span class="text-rose-600">*</span>
                </label>
                <button
                  type="button"
                  @click="composeDeployForm.compose_content = defaultComposeExample; if (!composeDeployForm.name) composeDeployForm.name = 'my-app'"
                  class="text-[10px] text-cyan-600 hover:text-cyan-800 underline font-mono"
                >
                  Insert Example
                </button>
                <button
                  v-if="composeDeployForm.compose_content"
                  type="button"
                  @click="composeDeployForm.compose_content = ''"
                  class="text-[10px] text-slate-500 hover:text-rose-600 underline font-mono"
                >
                  Clear
                </button>
              </div>
              <div class="flex flex-wrap items-center gap-1 text-xs">
                <span class="text-[10px] uppercase font-semibold text-slate-500 mr-0.5">Quick Insert:</span>
                <button
                  type="button"
                  @click="insertComposeSnippet('network')"
                  class="rounded border border-slate-200 bg-slate-50 px-1.5 py-0.5 font-mono text-[9px] text-cyan-700 hover:border-cyan-600 hover:bg-white transition"
                >
                  + Cloudflared Net
                </button>
                <button
                  type="button"
                  @click="insertComposeSnippet('restart')"
                  class="rounded border border-slate-200 bg-slate-50 px-1.5 py-0.5 font-mono text-[9px] text-cyan-700 hover:border-cyan-600 hover:bg-white transition"
                >
                  + Restart
                </button>
                <button
                  type="button"
                  @click="insertComposeSnippet('ports')"
                  class="rounded border border-slate-200 bg-slate-50 px-1.5 py-0.5 font-mono text-[9px] text-cyan-700 hover:border-cyan-600 hover:bg-white transition"
                >
                  + Port Map
                </button>
                <button
                  type="button"
                  @click="insertComposeSnippet('volumes')"
                  class="rounded border border-slate-200 bg-slate-50 px-1.5 py-0.5 font-mono text-[9px] text-cyan-700 hover:border-cyan-600 hover:bg-white transition"
                >
                  + Volume
                </button>
              </div>
            </div>
            <textarea
              v-model="composeDeployForm.compose_content"
              rows="12"
              class="cyber-input font-mono text-xs w-full leading-relaxed bg-slate-50 border-slate-200 text-cyan-100"
              placeholder="services:&#10;  app:&#10;    image: nginx:alpine&#10;    restart: unless-stopped"
              spellcheck="false"
              @focus="onComposeFocus"
              @paste="onComposePaste"
            ></textarea>
          </div>

          <!-- Collapsible .env Editor -->
          <div class="border border-slate-200 rounded-lg bg-slate-50/60 p-3 space-y-2">
            <button
              type="button"
              @click="composeDeployForm.show_env = !composeDeployForm.show_env"
              class="w-full flex items-center justify-between text-xs font-semibold text-slate-700 hover:text-slate-900"
            >
              <div class="flex items-center gap-2">
                <FileText class="h-3.5 w-3.5 text-amber-600" />
                <span>Environment Variables (.env file)</span>
                <span class="text-[9px] uppercase px-1.5 py-0.2 rounded bg-slate-100 text-slate-600">Optional</span>
              </div>
              <ChevronDown class="h-4 w-4 transition-transform" :class="composeDeployForm.show_env ? 'rotate-180' : ''" />
            </button>
            <div v-if="composeDeployForm.show_env" class="pt-2">
              <textarea
                v-model="composeDeployForm.env_content"
                rows="4"
                class="cyber-input font-mono text-xs w-full leading-relaxed bg-slate-50 border-slate-200 text-amber-200"
                placeholder="KEY=value&#10;POSTGRES_PASSWORD=secret"
                spellcheck="false"
              ></textarea>
            </div>
          </div>

          <div class="flex items-center justify-end gap-3 pt-2 border-t border-slate-200">
            <button @click="showLaunchModal = false" class="cyber-btn-secondary text-xs">Cancel</button>
            <button
              @click="deployComposeStack"
              :disabled="composeDeploying || !composeDeployForm.name.trim() || !composeDeployForm.compose_content.trim()"
              class="cyber-btn flex items-center gap-2 text-xs text-cyan-700 border-cyan-500/80 bg-cyan-50/40 hover:bg-cyan-100/60 font-bold"
            >
              <RotateCcw v-if="composeDeploying" class="h-3.5 w-3.5 animate-spin" />
              <Rocket v-else class="h-3.5 w-3.5" />
              <span>{{ composeDeploying ? 'Deploying Stack…' : 'Deploy Compose Stack (up -d)' }}</span>
            </button>
          </div>
        </div>

        <!-- TAB 2: Compose App Templates -->
        <div v-if="launchTab === 'templates'" class="space-y-4 overflow-y-auto pr-1 flex-1">
          <div class="flex flex-col sm:flex-row items-center justify-between gap-3">
            <!-- Category Pills -->
            <div class="flex items-center gap-1.5 overflow-x-auto w-full sm:w-auto">
              <button
                v-for="cat in ['all', 'web', 'database', 'monitoring', 'media', 'utility']"
                :key="cat"
                @click="composeTemplateCategory = cat as any"
                class="px-2.5 py-1 rounded text-[11px] font-semibold uppercase tracking-wider transition"
                :class="composeTemplateCategory === cat ? 'bg-cyan-500/20 text-cyan-700 border border-cyan-500/40' : 'text-slate-600 hover:text-slate-800 border border-slate-200 bg-white/60'"
              >
                {{ cat }}
              </button>
            </div>

            <!-- Search Filter -->
            <div class="relative w-full sm:w-64">
              <Search class="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-slate-500" />
              <input
                v-model="composeTemplateSearch"
                type="text"
                placeholder="Search templates…"
                class="cyber-input text-xs pl-8 w-full"
              />
            </div>
          </div>

          <!-- Templates Grid -->
          <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
            <div
              v-for="tmpl in filteredTemplates"
              :key="tmpl.id"
              class="rounded-xl border border-slate-200 bg-slate-50/70 p-3.5 flex flex-col justify-between hover:border-cyan-500/50 hover:bg-white/40 transition group"
            >
              <div class="space-y-2">
                <div class="flex items-start justify-between">
                  <div class="flex items-center gap-2">
                    <div class="rounded-lg bg-cyan-50/80 border border-cyan-800/40 p-2 text-cyan-700">
                      <component :is="tmpl.iconComponent" class="h-4 w-4" />
                    </div>
                    <div>
                      <div class="text-xs font-bold text-slate-900 group-hover:text-cyan-700 transition">
                        {{ tmpl.name }}
                      </div>
                      <span class="text-[9px] uppercase tracking-wider font-semibold px-1.5 py-0.2 rounded bg-slate-100 text-slate-600">
                        {{ tmpl.category }}
                      </span>
                    </div>
                  </div>
                </div>

                <p class="text-[11px] text-slate-600 leading-relaxed">
                  {{ tmpl.description }}
                </p>

                <div v-if="tmpl.ports.length" class="flex flex-wrap gap-1 pt-1">
                  <span
                    v-for="port in tmpl.ports"
                    :key="port"
                    class="rounded bg-white border border-slate-200 px-1.5 py-0.2 text-[9px] font-mono text-cyan-600"
                  >
                    {{ port }}
                  </span>
                </div>
              </div>

              <div class="pt-3 mt-2 border-t border-slate-200/80 flex items-center justify-between">
                <span class="text-[10px] text-slate-500 font-mono">1-click starter</span>
                <button
                  type="button"
                  @click="selectTemplate(tmpl)"
                  class="cyber-btn text-[11px] py-1 px-2.5 text-cyan-700 border-cyan-600 bg-cyan-50/30 hover:bg-cyan-100/60 flex items-center gap-1.5"
                >
                  <Rocket class="h-3 w-3" />
                  <span>Use Template</span>
                </button>
              </div>
            </div>
          </div>
        </div>

        <!-- TAB 3: Docker Run -->
        <div v-if="launchTab === 'dockerrun'" class="space-y-4 overflow-y-auto pr-1 flex-1">
          <div>
            <div class="flex items-center justify-between mb-1">
              <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-600">
                Docker Run Command
              </label>
              <div class="flex items-center gap-2">
                <button
                  type="button"
                  @click="dockerRunCmd = defaultExampleCmd"
                  class="text-[10px] text-cyan-600 hover:text-cyan-800 underline font-mono"
                >
                  Insert Example
                </button>
                <button
                  v-if="dockerRunCmd"
                  type="button"
                  @click="dockerRunCmd = ''"
                  class="text-[10px] text-slate-500 hover:text-rose-600 underline font-mono"
                >
                  Clear
                </button>
              </div>
            </div>
            <textarea
              v-model="dockerRunCmd"
              rows="3"
              class="cyber-input font-mono text-xs w-full leading-relaxed"
              :placeholder="defaultExampleCmd"
              @focus="onDockerRunFocus"
              @paste="onDockerRunPaste"
            ></textarea>
          </div>

          <!-- Snippet helper buttons -->
          <div class="flex flex-wrap items-center gap-1.5 text-xs">
            <span class="text-[10px] font-semibold uppercase tracking-wider text-slate-500 mr-1">Insert:</span>
            <button
              v-for="snip in ['--restart unless-stopped', '-p 8080:80', '-v /data:/data', '-e KEY=val', '--network cloudflared']"
              :key="snip"
              type="button"
              @click="insertDockerSnippet(snip)"
              class="rounded border border-slate-200 bg-slate-50 px-2 py-0.5 font-mono text-[10px] text-cyan-700 hover:border-cyan-600 hover:bg-white transition"
            >
              + {{ snip }}
            </button>
          </div>

          <div class="grid gap-3 sm:grid-cols-2 text-xs">
            <div>
              <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">Execution Mode</label>
              <div class="space-y-1">
                <label class="flex items-center gap-2 cursor-pointer text-slate-700">
                  <input type="radio" v-model="dockerRunMode" value="compose" class="accent-cyan-500" />
                  <span>Convert to Compose Stack (Recommended)</span>
                </label>
                <label class="flex items-center gap-2 cursor-pointer text-slate-700">
                  <input type="radio" v-model="dockerRunMode" value="direct" class="accent-cyan-500" />
                  <span>Direct Docker Run</span>
                </label>
              </div>
            </div>

            <div>
              <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">Custom Project / Stack Name</label>
              <input v-model="dockerRunCustomName" class="cyber-input text-xs" placeholder="Leave blank to infer from --name" />
            </div>
          </div>

          <!-- Output Log -->
          <div v-if="dockerRunLines.length > 0 || dockerRunLoading" class="max-h-48 overflow-y-auto rounded-lg border border-slate-200 bg-slate-50 p-3 font-mono text-[11px] text-slate-700 leading-relaxed shadow-inner">
            <div v-for="(line, idx) in dockerRunLines" :key="idx">{{ line }}</div>
            <div v-if="dockerRunLoading" class="text-cyan-600 animate-pulse mt-1">&rarr; Running command...</div>
          </div>

          <div class="flex items-center justify-end gap-3 pt-2 border-t border-slate-200">
            <button @click="showLaunchModal = false" class="cyber-btn-secondary text-xs">Cancel</button>
            <button
              @click="runDockerCommand"
              :disabled="dockerRunLoading || !dockerRunCmd.trim()"
              class="cyber-btn flex items-center gap-2 text-xs text-cyan-700 border-cyan-500/80 bg-cyan-50/40 hover:bg-cyan-100/60 font-bold"
            >
              <RotateCcw v-if="dockerRunLoading" class="h-3.5 w-3.5 animate-spin" />
              <Play v-else class="h-3.5 w-3.5 fill-current" />
              <span>{{ dockerRunLoading ? 'Executing…' : 'Run Container' }}</span>
            </button>
          </div>
        </div>

        <!-- TAB 4: Git / Compose Stack -->
        <div v-if="launchTab === 'gitstack'" class="space-y-4 overflow-y-auto pr-1 flex-1">
          <div class="grid gap-3 sm:grid-cols-2 text-xs">
            <div>
              <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">Stack Name</label>
              <input v-model="newProjectForm.name" class="cyber-input text-xs font-mono" placeholder="e.g. mediastorm" />
            </div>
            <div>
              <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">Host Directory Path</label>
              <input v-model="newProjectForm.path" class="cyber-input text-xs font-mono" placeholder="./projects/mediastorm" />
            </div>
            <div class="sm:col-span-2">
              <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">Git Repository URL (Optional)</label>
              <input v-model="newProjectForm.repo_url" class="cyber-input text-xs font-mono" placeholder="https://github.com/owner/repo.git" />
            </div>
            <div>
              <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">Git Username (Optional)</label>
              <input v-model="newProjectForm.git_username" class="cyber-input text-xs font-mono" placeholder="oauth2 or username" />
            </div>
            <div>
              <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">Git Token / PAT (Optional)</label>
              <input v-model="newProjectForm.git_token" type="password" class="cyber-input text-xs font-mono" placeholder="ghp_••••••••••••" />
            </div>
          </div>

          <div class="flex items-center justify-end gap-3 pt-2 border-t border-slate-200">
            <button @click="showLaunchModal = false" class="cyber-btn-secondary text-xs">Cancel</button>
            <button
              @click="submitNewProject"
              :disabled="newProjectSaving || !newProjectForm.name.trim() || !newProjectForm.path.trim()"
              class="cyber-btn flex items-center gap-2 text-xs text-emerald-700 border-emerald-500/80 bg-emerald-50/40 hover:bg-emerald-100/60 font-bold"
            >
              <RotateCcw v-if="newProjectSaving" class="h-3.5 w-3.5 animate-spin" />
              <Package v-else class="h-3.5 w-3.5" />
              <span>{{ newProjectSaving ? 'Creating…' : 'Register & Initialize Stack' }}</span>
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- ============================================== -->
    <!-- STACK OUTPUT STREAM MODAL                      -->
    <!-- ============================================== -->
    <div
      v-if="stackOutputModal"
      class="fixed inset-0 z-50 flex items-center justify-center bg-slate-50/85 p-4 backdrop-blur-md"
      @mousedown="onBackdropMouseDown"
      @click="onBackdropClick($event, () => stackOutputModal = null)"
    >
      <div class="w-full max-w-3xl rounded-xl border border-slate-300 bg-white p-6 shadow-sm space-y-4" @mousedown.stop @mouseup.stop @click.stop>
        <div class="flex items-start justify-between border-b border-slate-200 pb-3">
          <div class="flex items-center gap-3">
            <div class="rounded-lg p-2 bg-slate-100 text-cyan-600">
              <Terminal class="h-5 w-5" />
            </div>
            <div>
              <h3 class="text-base font-bold text-slate-900">{{ stackOutputModal.title }}</h3>
              <p class="text-xs text-slate-600 font-mono">
                {{ stackOutputModal.done ? 'Action finished.' : 'Action in progress… streaming output.' }}
              </p>
            </div>
          </div>
          <button @click="stackOutputModal = null" class="text-slate-600 hover:text-slate-800">
            <X class="h-5 w-5" />
          </button>
        </div>

        <div class="max-h-96 overflow-y-auto rounded-lg border border-slate-200 bg-slate-50 p-4 font-mono text-xs leading-relaxed text-slate-700 shadow-inner">
          <div v-for="(line, idx) in stackOutputModal.lines" :key="idx" class="whitespace-pre-wrap break-all">{{ line }}</div>
          <div v-if="!stackOutputModal.done" class="text-cyan-600 animate-pulse mt-2">&rarr; Running docker-compose command...</div>
        </div>

        <div class="flex items-center justify-between text-xs">
          <span :class="stackOutputFailed ? 'text-rose-600 font-semibold' : (stackOutputModal.done ? 'text-emerald-600' : 'text-amber-600')">
            {{ !stackOutputModal.done ? '● Processing...' : (stackOutputFailed ? '✗ Action Failed' : '✓ Completed') }}
          </span>
          <button @click="stackOutputModal = null" class="cyber-btn text-xs">Close</button>
        </div>
      </div>
    </div>

    <!-- ============================================== -->
    <!-- DEPLOY PROMPT MODAL                            -->
    <!-- ============================================== -->
    <div
      v-if="showDeployPromptModal"
      class="fixed inset-0 z-50 flex items-center justify-center bg-slate-50/85 p-4 backdrop-blur-md"
      @mousedown="onBackdropMouseDown"
      @click="onBackdropClick($event, () => showDeployPromptModal = null)"
    >
      <div class="w-full max-w-md rounded-xl border border-violet-500/40 bg-white p-6 shadow-sm space-y-4" @mousedown.stop @mouseup.stop @click.stop>
        <div class="flex items-start justify-between border-b border-slate-200 pb-3">
          <div class="flex items-center gap-2 text-violet-300 font-bold">
            <Rocket class="h-5 w-5" />
            <span>Deploy Stack: {{ showDeployPromptModal.projectName }}</span>
          </div>
          <button @click="showDeployPromptModal = null" class="text-slate-600 hover:text-slate-800">
            <X class="h-5 w-5" />
          </button>
        </div>

        <div class="space-y-2 text-xs">
          <label class="block font-semibold uppercase tracking-wider text-slate-600">Git Branch, Tag, or Ref</label>
          <input
            v-model="deployBranchName"
            class="cyber-input font-mono text-xs"
            placeholder="main"
            @keyup.enter="confirmDeploy"
          />
          <p class="text-[11px] text-slate-500">
            DockTun will fetch remote changes, checkout this ref, git pull, and execute <code class="text-cyan-600 font-mono">docker compose up -d --build</code>.
          </p>
        </div>

        <div class="flex items-center justify-end gap-3 pt-2">
          <button @click="showDeployPromptModal = null" class="cyber-btn-secondary text-xs">Cancel</button>
          <button
            @click="confirmDeploy"
            class="cyber-btn flex items-center gap-1.5 text-xs text-violet-300 border-violet-600 bg-violet-950/40 hover:bg-violet-900/60 font-bold"
          >
            <Rocket class="h-3.5 w-3.5" />
            <span>Start Deployment</span>
          </button>
        </div>
      </div>
    </div>

    <!-- ============================================== -->
    <!-- STACK HISTORY MODAL                            -->
    <!-- ============================================== -->
    <div
      v-if="stackHistoryModal"
      class="fixed inset-0 z-50 flex items-center justify-center bg-slate-50/85 p-4 backdrop-blur-md"
      @mousedown="onBackdropMouseDown"
      @click="onBackdropClick($event, () => stackHistoryModal = null)"
    >
      <div class="w-full max-w-2xl rounded-xl border border-slate-300 bg-white p-6 shadow-sm space-y-4" @mousedown.stop @mouseup.stop @click.stop>
        <div class="flex items-start justify-between border-b border-slate-200 pb-3">
          <div class="flex items-center gap-2.5">
            <History class="h-5 w-5 text-cyan-600" />
            <div>
              <h3 class="text-base font-bold text-slate-900">Stack Action History</h3>
              <p class="text-xs text-slate-600 font-mono">{{ stackHistoryModal.projectName }}</p>
            </div>
          </div>
          <button @click="stackHistoryModal = null" class="text-slate-600 hover:text-slate-800">
            <X class="h-5 w-5" />
          </button>
        </div>

        <div v-if="stackHistoryModal.items.length === 0" class="py-8 text-center text-xs text-slate-500">
          No recorded actions found for this stack.
        </div>

        <div v-else class="max-h-80 overflow-y-auto divide-y divide-slate-200/80 pr-1">
          <div
            v-for="(item, idx) in stackHistoryModal.items"
            :key="idx"
            class="py-2.5 flex items-start justify-between gap-3 text-xs"
          >
            <div>
              <div class="flex items-center gap-2">
                <span class="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-[10px] font-bold text-cyan-700 uppercase">
                  {{ item.action || 'Action' }}
                </span>
                <span class="text-slate-700 font-mono">{{ item.created_at || item.timestamp }}</span>
              </div>
              <div v-if="item.detail" class="mt-1 font-mono text-[11px] text-slate-600 line-clamp-2">
                {{ item.detail }}
              </div>
            </div>
            <span class="text-[10px] text-slate-500 font-mono shrink-0">{{ item.ip || 'Local' }}</span>
          </div>
        </div>

        <div class="flex items-center justify-end pt-2">
          <button @click="stackHistoryModal = null" class="cyber-btn text-xs">Close</button>
        </div>
      </div>
    </div>

    <!-- ============================================== -->
    <!-- DELETE STACK CONFIRMATION MODAL               -->
    <!-- ============================================== -->
    <div
      v-if="showDeleteStackModal"
      class="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/60 p-4 backdrop-blur-xs"
      @mousedown="onBackdropMouseDown"
      @click="onBackdropClick($event, () => showDeleteStackModal = null)"
    >
      <div class="w-full max-w-md rounded-xl border border-rose-200 bg-white p-6 shadow-xl space-y-4" @mousedown.stop @mouseup.stop @click.stop>
        <div class="flex items-start gap-3">
          <div class="rounded-full bg-rose-100 p-2 text-rose-600 shrink-0">
            <Trash2 class="h-5 w-5" />
          </div>
          <div>
            <h3 class="text-base font-bold text-slate-900">Delete Stack</h3>
            <p class="text-xs text-slate-600 mt-0.5">
              Are you sure you want to delete stack <strong class="text-slate-900 font-mono">{{ showDeleteStackModal.projectName }}</strong>?
            </p>
          </div>
        </div>

        <div class="space-y-3 rounded-lg border border-slate-200 bg-slate-50 p-3.5 text-xs">
          <label class="flex items-start gap-2 cursor-pointer">
            <input type="checkbox" v-model="deleteStackDown" class="mt-0.5 rounded border-slate-300 text-rose-600 focus:ring-rose-500 cursor-pointer" />
            <div>
              <span class="font-semibold text-slate-800">Stop and remove stack containers</span>
              <p class="text-slate-500 text-[11px]">Stops and removes this stack’s containers and Compose networks. Volumes, images, and external networks are kept. Uncheck to forget the saved project only; existing containers will still appear in the container list.</p>
            </div>
          </label>

          <label class="flex items-start gap-2 cursor-pointer pt-2 border-t border-slate-200">
            <input type="checkbox" v-model="deleteStackRemoveFiles" class="mt-0.5 rounded border-slate-300 text-rose-600 focus:ring-rose-500 cursor-pointer" />
            <div>
              <span class="font-semibold text-rose-700">Delete project folder from disk</span>
              <p class="text-slate-500 text-[11px]">Permanently removes <code class="font-mono">{{ showDeleteStackModal.path || 'the project directory' }}</code>, including files stored inside it. Only folders inside DockTun’s projects folder can be removed.</p>
            </div>
          </label>
        </div>

        <div class="flex items-center justify-end gap-2 pt-2">
          <button
            type="button"
            @click="showDeleteStackModal = null"
            class="cyber-btn-secondary text-xs"
            :disabled="deleteStackLoading"
          >
            Cancel
          </button>
          <button
            type="button"
            @click="confirmDeleteStack"
            class="cyber-btn flex items-center gap-1.5 text-xs text-white bg-rose-600 hover:bg-rose-700 font-semibold"
            :disabled="deleteStackLoading"
          >
            <RotateCcw v-if="deleteStackLoading" class="h-3.5 w-3.5 animate-spin" />
            <Trash2 v-else class="h-3.5 w-3.5" />
            <span>{{ deleteStackLoading ? 'Deleting…' : 'Delete Stack' }}</span>
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
