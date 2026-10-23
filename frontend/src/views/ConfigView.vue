<script setup lang="ts">
import { ref, reactive, onMounted, onUnmounted, computed } from 'vue'
import { useRoute } from 'vue-router'
import {
  Globe,
  Shield,
  ShieldCheck,
  Layers,
  Activity,
  Terminal,
  Archive,
  RotateCcw,
  Plus,
  Trash2,
  ExternalLink,
  Copy,
  Check,
  AlertCircle,
  CheckCircle2,
  Clock,
  HardDrive,
  Lock,
  Settings,
  Code,
  Eye,
  RefreshCw,
  Server,
  Zap,
  KeyRound,
  Network,
  Gamepad2,
  Info,
  Radio
} from 'lucide-vue-next'
import { useApi } from '../composables/useApi'

const props = withDefaults(defineProps<{ embedded?: boolean }>(), { embedded: false })
const { apiFetch } = useApi()
const route = useRoute()

interface IngressRule {
  hostname: string
  service: string
  noTLSVerify: boolean
}

interface BackupItem {
  id: number
  created_at: string
  content: string
}

interface MetricsData {
  total_requests?: number
  active_connections?: number
  response_codes?: Record<string, number>
}

interface HealthItem {
  hostname: string
  service: string
  status: string
  latency: string
}

// Active Tab
const activeTab = ref<'overview' | 'ingress' | 'tcp' | 'api' | 'backups'>('overview')

// Config & Rules State
const rules = ref<IngressRule[]>([])
const tunnelId = ref('')
const credentialsFile = ref('/etc/cloudflared/credentials.json')
const protocol = ref('http2')
const rawYaml = ref('')
const isRawEditor = ref(false)
const saving = ref(false)
const message = ref<{ type: 'success' | 'error'; text: string } | null>(null)

// Metrics & Health State
const metrics = ref<MetricsData | null>(null)
const health = ref<HealthItem[]>([])
const healthLoading = ref(false)
const tunnelStatus = ref<any>(null)

async function fetchStatus() {
  try {
    tunnelStatus.value = await apiFetch('/api/status')
  } catch {}
}

// Backups State
const backups = ref<BackupItem[]>([])
const backupsLoading = ref(false)

// TCP Command Generator State
const tcpForm = reactive({
  hostname: '',
  local_url: 'localhost:2222',
  mode: 'foreground' as 'foreground' | 'nohup' | 'systemd'
})
const generatedTcpCmd = ref('')

// Cloudflare API Remote Info State
const cfApiToken = ref(localStorage.getItem('docktun_cf_api_token') || '')
const cfAccountId = ref(localStorage.getItem('docktun_cf_account_id') || '')
const cfZoneId = ref(localStorage.getItem('docktun_cf_zone_id') || '')
const cfApiHasToken = ref(false)
const cfApiMaskedToken = ref('')
const cfRemoteTunnel = ref<any>(null)
const cfApiLoading = ref(false)
const cfApiError = ref('')
const cfApiSuccess = ref(false)

// Clipboard Feedback
const copiedKey = ref<string | null>(null)
function copyText(key: string, text: string) {
  navigator.clipboard.writeText(text)
  copiedKey.value = key
  setTimeout(() => {
    if (copiedKey.value === key) copiedKey.value = null
  }, 2000)
}

function isDocktunRule(rule: IngressRule) {
  return rule.service.includes('docktun-frontend') || rule.service.includes('docktun:3001') || rule.service.includes('docktun:')
}

function extractTargetHost(service: string): string {
  if (!service) return ''
  try {
    let s = service.trim()
    if (!s.includes('://')) {
      s = 'http://' + s
    }
    const u = new URL(s)
    return u.hostname
  } catch {
    return service.replace(/^https?:\/\//, '').split(':')[0]
  }
}

// Origin Probe & Maintenance State
const probingHost = ref<string | null>(null)
const probeResults = ref<Record<string, { reachable: boolean; status_code: number; latency_ms: number; error: string }>>({})
const pausedServices = ref<Record<string, string>>({})

async function probeOriginRule(rule: IngressRule) {
  if (!rule.service || rule.service.startsWith('http_status:')) return
  probingHost.value = rule.hostname
  try {
    const res: any = await apiFetch('/api/docker/probe', {
      method: 'POST',
      body: JSON.stringify({ service: rule.service }),
    })
    probeResults.value[rule.hostname] = res
  } catch (err: any) {
    probeResults.value[rule.hostname] = {
      reachable: false,
      status_code: 0,
      latency_ms: 0,
      error: err.message || 'Probe error',
    }
  } finally {
    probingHost.value = null
  }
}

function toggleRulePause(rule: IngressRule) {
  if (rule.service.startsWith('http_status:')) {
    rule.service = pausedServices.value[rule.hostname] || `http://${rule.hostname.split('.')[0]}:80`
  } else {
    pausedServices.value[rule.hostname] = rule.service
    rule.service = 'http_status:503'
  }
}

// Load YAML Config
async function loadConfig() {
  try {
    const data = await apiFetch<{ content: string }>('/api/config')
    rawYaml.value = data.content
    parseYaml(data.content)
  } catch (e: any) {
    message.value = { type: 'error', text: 'Failed to load config: ' + e.message }
  }
}

function parseYaml(content: string) {
  const lines = content.split('\n')
  rules.value = []
  for (const line of lines) {
    const m = line.match(/^tunnel:\s*(.+)/)
    if (m) tunnelId.value = m[1].trim()
    const cm = line.match(/^credentials-file:\s*(.+)/)
    if (cm) credentialsFile.value = cm[1].trim()
    const pm = line.match(/^protocol:\s*(.+)/)
    if (pm) protocol.value = pm[1].trim()
  }

  let inIngress = false
  let cur: Partial<IngressRule> = {}
  for (const line of lines) {
    if (line.match(/^ingress:/)) {
      inIngress = true
      continue
    }
    if (!inIngress) continue

    const isStandaloneService = line.match(/^\s*-\s*service:\s*["']?([^"'\s]+)["']?/)
    const hm = line.match(/^\s*-\s*hostname:\s*["']?([^"'\s]+)["']?/)
    const sm = line.match(/^\s*service:\s*["']?([^"'\s]+)["']?/)
    const tm = line.match(/noTLSVerify:\s*true/)

    if (isStandaloneService) {
      if (cur.hostname) {
        rules.value.push({
          hostname: cur.hostname,
          service: cur.service || '',
          noTLSVerify: cur.noTLSVerify || false,
        })
        cur = {}
      }
      // If standalone service is not http_status:404, we could preserve it, but http_status:404 is the default catchall
      continue
    }

    if (hm) {
      if (cur.hostname) {
        rules.value.push({
          hostname: cur.hostname,
          service: cur.service || '',
          noTLSVerify: cur.noTLSVerify || false,
        })
      }
      cur = { hostname: hm[1].trim() }
    } else if (sm && cur.hostname) {
      cur.service = sm[1].trim()
    } else if (tm) {
      cur.noTLSVerify = true
    }
  }

  if (cur.hostname) {
    rules.value.push({
      hostname: cur.hostname,
      service: cur.service || '',
      noTLSVerify: cur.noTLSVerify || false,
    })
  }
}

function generateYaml(): string {
  let y = `tunnel: "${tunnelId.value}"\ncredentials-file: "${credentialsFile.value || '/etc/cloudflared/credentials.json'}"\nprotocol: ${protocol.value || 'http2'}\n\ningress:\n`
  for (const r of rules.value) {
    if (!r.hostname || !r.service) continue
    // Clean hostname: remove https:// and trailing slashes
    let cleanH = r.hostname.trim().replace(/^https?:\/\//i, '').replace(/\/+$/, '').replace(/:[0-9]+$/, '')
    let target = r.service.trim().replace(/^["']|["']$/g, '')
    // Normalize .local to direct container name:
    if (target.includes('.local')) {
      target = target.replace('.local', '')
    }
    // Normalize localhost / 127.0.0.1 to host.docker.internal:
    if (target.includes('localhost') || target.includes('127.0.0.1')) {
      target = target.replace('localhost', 'host.docker.internal').replace('127.0.0.1', 'host.docker.internal')
    }
    y += `  - hostname: "${cleanH}"\n    service: "${target}"\n`
    if (r.noTLSVerify || target.startsWith('https://')) {
      y += `    originRequest:\n      noTLSVerify: true\n`
    }
  }
  return y + `  - service: "http_status:404"\n`
}

function addRule() {
  rules.value.push({ hostname: '', service: '', noTLSVerify: false })
}

function removeRule(i: number) {
  if (isDocktunRule(rules.value[i])) {
    if (!confirm('Are you sure you want to remove public tunnel access for DockTun? DockTun will no longer be accessible from the public internet, and will only be accessible locally on your LAN (port 8080 or http://docktun.local).')) {
      return
    }
  }
  rules.value.splice(i, 1)
}

async function save(restart = true) {
  saving.value = true
  message.value = null
  const contentToSave = isRawEditor.value ? rawYaml.value : generateYaml()
  try {
    await apiFetch('/api/config', {
      method: 'PUT',
      body: JSON.stringify({ content: contentToSave }),
    })
    rawYaml.value = contentToSave
    parseYaml(contentToSave)

    if (restart) {
      await apiFetch('/api/control/restart', { method: 'POST' })
      message.value = { type: 'success', text: 'Config saved and Cloudflare tunnel restarted! Routes are now active.' }
    } else {
      message.value = { type: 'success', text: 'Ingress configuration saved successfully!' }
    }
    setTimeout(() => (message.value = null), 6000)
    loadBackups()
  } catch (e: any) {
    message.value = { type: 'error', text: e.message }
  } finally {
    saving.value = false
  }
}

async function restartTunnel() {
  saving.value = true
  try {
    await apiFetch('/api/control/restart', { method: 'POST' })
    message.value = { type: 'success', text: 'Cloudflare tunnel daemon restarted!' }
    setTimeout(() => (message.value = null), 3500)
  } catch (e: any) {
    message.value = { type: 'error', text: 'Restart failed: ' + e.message }
  } finally {
    saving.value = false
  }
}

// Fetch Metrics & Health
async function fetchMetrics() {
  try {
    metrics.value = await apiFetch<MetricsData>('/api/metrics')
  } catch {}
}

async function fetchHealth() {
  healthLoading.value = true
  try {
    health.value = (await apiFetch<HealthItem[]>('/api/health')) || []
  } catch {
    health.value = []
  } finally {
    healthLoading.value = false
  }
}

// Fetch Backups
async function loadBackups() {
  backupsLoading.value = true
  try {
    backups.value = (await apiFetch<BackupItem[]>('/api/config/backups')) || []
  } catch {
    backups.value = []
  } finally {
    backupsLoading.value = false
  }
}

async function createManualBackup() {
  backupsLoading.value = true
  try {
    await apiFetch('/api/config/backups', { method: 'POST' })
    message.value = { type: 'success', text: 'Config backup snapshot created.' }
    await loadBackups()
  } catch (e: any) {
    message.value = { type: 'error', text: 'Failed to create backup: ' + e.message }
  } finally {
    backupsLoading.value = false
  }
}

async function restoreBackup(b: BackupItem) {
  if (!confirm(`Restore backup from ${b.created_at}? This will overwrite the current ingress config.`)) return
  rawYaml.value = b.content
  parseYaml(b.content)
  await save(true)
}

// Generate TCP Command
async function generateTcpCommand() {
  try {
    const data = await apiFetch<{ command: string }>('/api/tcp-access/generate', {
      method: 'POST',
      body: JSON.stringify(tcpForm),
    })
    generatedTcpCmd.value = data.command
  } catch (e: any) {
    generatedTcpCmd.value = `cloudflared access tcp --hostname ${tcpForm.hostname} --url ${tcpForm.local_url}`
  }
}

// Cloudflare Remote API Integration & Automated DNS
const syncLoading = ref(false)
const wildcardDomainInput = ref('')
const wildcardLoading = ref(false)
const syncResults = ref<any[] | null>(null)

async function loadBackendCfSettings() {
  try {
    const s = await apiFetch<any>('/api/cloudflare/settings')
    if (s.account_id && !cfAccountId.value) cfAccountId.value = s.account_id
    if (s.zone_id && !cfZoneId.value) cfZoneId.value = s.zone_id
    if (s.has_token) {
      cfApiHasToken.value = true
      cfApiMaskedToken.value = s.masked_token || '••••••••'
    }
  } catch {}
}

async function saveCfApiCredentials() {
  cfApiLoading.value = true
  cfApiError.value = ''
  try {
    if (cfApiToken.value) {
      localStorage.setItem('docktun_cf_api_token', cfApiToken.value)
    }
    if (cfAccountId.value) {
      localStorage.setItem('docktun_cf_account_id', cfAccountId.value)
    }
    if (cfZoneId.value) {
      localStorage.setItem('docktun_cf_zone_id', cfZoneId.value)
    }

    await apiFetch('/api/cloudflare/settings', {
      method: 'POST',
      body: JSON.stringify({
        api_token: cfApiToken.value,
        account_id: cfAccountId.value,
        zone_id: cfZoneId.value,
      }),
    })

    cfApiSuccess.value = true
    await loadBackendCfSettings()
    await fetchRemoteTunnelInfo()
    setTimeout(() => (cfApiSuccess.value = false), 3500)
  } catch (err: any) {
    cfApiError.value = 'Failed to save settings: ' + err.message
  } finally {
    cfApiLoading.value = false
  }
}

async function syncAllDnsRoutes() {
  syncLoading.value = true
  syncResults.value = null
  try {
    const res = await apiFetch<any>('/api/cloudflare/sync-all', { method: 'POST' })
    syncResults.value = res.synced || []
    const successCount = res.synced?.filter((s: any) => s.success).length || 0
    message.value = {
      type: 'success',
      text: `Successfully synced ${successCount} DNS routes in Cloudflare!`,
    }
    setTimeout(() => (message.value = null), 6000)
  } catch (err: any) {
    message.value = { type: 'error', text: 'Cloudflare DNS sync failed: ' + err.message }
  } finally {
    syncLoading.value = false
  }
}

async function createWildcardRoute() {
  const dom = wildcardDomainInput.value.trim()
  if (!dom) {
    alert('Please enter your root domain (e.g. yourdomain.com)')
    return
  }
  wildcardLoading.value = true
  try {
    const res = await apiFetch<any>('/api/cloudflare/create-wildcard', {
      method: 'POST',
      body: JSON.stringify({ domain: dom }),
    })
    message.value = { type: 'success', text: res.message || `Wildcard *.${dom} created in Cloudflare!` }
    setTimeout(() => (message.value = null), 6000)
  } catch (err: any) {
    message.value = { type: 'error', text: 'Failed to create wildcard: ' + err.message }
  } finally {
    wildcardLoading.value = false
  }
}

async function fetchRemoteTunnelInfo() {
  cfApiLoading.value = true
  cfApiError.value = ''
  try {
    const data = await apiFetch<any>('/api/cloudflare/tunnel')
    if (data && !data.error) {
      cfRemoteTunnel.value = data
    } else if (data && data.error) {
      cfApiError.value = data.error
    }
  } catch (err: any) {
    if (cfApiHasToken.value || cfApiToken.value) {
      cfApiError.value = 'Cloudflare API query: ' + err.message
    }
  } finally {
    cfApiLoading.value = false
  }
}

let metricsTimer: any = null

onMounted(async () => {
  const sub = route.query.sub as string
  if (sub && ['overview', 'ingress', 'tcp', 'api', 'backups', 'health'].includes(sub)) {
    if (sub === 'health') activeTab.value = 'overview'
    else activeTab.value = sub as any
  }

  await loadConfig()
  await loadBackendCfSettings()
  fetchStatus()
  fetchMetrics()
  fetchHealth()
  loadBackups()
  if (cfApiHasToken.value || cfApiToken.value) {
    fetchRemoteTunnelInfo()
  }

  // Poll telemetry every 10s
  metricsTimer = setInterval(() => {
    if (document.visibilityState === 'visible') {
      fetchStatus()
      fetchMetrics()
    }
  }, 10000)
})

onUnmounted(() => {
  if (metricsTimer) clearInterval(metricsTimer)
})
</script>

<template>
  <div class="space-y-6">
    <!-- Header (shown when standalone page) -->
    <div v-if="!embedded" class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <div class="flex items-center gap-2 text-[10px] font-semibold uppercase tracking-[0.22em] text-slate-400">
          <Globe class="h-3.5 w-3.5 text-cyan-400" />
          <span>Cloudflare Tunnel Hub</span>
        </div>
        <h1 class="text-2xl font-bold tracking-tight text-slate-100">Cloudflare Tunnel</h1>
        <p class="text-xs text-slate-400">
          Manage remote edge routing, ingress hostnames, live edge traffic, and client TCP access.
        </p>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <button
          @click="restartTunnel"
          :disabled="saving"
          class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-amber-300 hover:border-amber-600 hover:bg-amber-950/40"
          title="Restart Cloudflare Tunnel daemon"
        >
          <RotateCcw class="h-3.5 w-3.5" :class="saving ? 'animate-spin' : ''" />
          <span>Restart Tunnel</span>
        </button>
        <button
          @click="loadConfig"
          class="cyber-btn flex items-center gap-1.5 text-xs text-slate-300"
        >
          <RefreshCw class="h-3.5 w-3.5" />
          <span>Reload</span>
        </button>
      </div>
    </div>
    <!-- Mini actions bar when embedded in RemoteAccessView -->
    <div v-else class="flex items-center justify-end gap-2 pb-1">
      <button
        @click="restartTunnel"
        :disabled="saving"
        class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-amber-300 hover:border-amber-600 hover:bg-amber-950/40"
        title="Restart Cloudflare Tunnel daemon"
      >
        <RotateCcw class="h-3.5 w-3.5" :class="saving ? 'animate-spin' : ''" />
        <span>Restart Tunnel</span>
      </button>
      <button
        @click="loadConfig"
        class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-slate-300"
      >
        <RefreshCw class="h-3.5 w-3.5" />
        <span>Reload</span>
      </button>
    </div>

    <!-- Alert / Toast Feedback -->
    <div
      v-if="message"
      class="rounded-lg border p-3 text-xs font-mono flex items-center justify-between"
      :class="message.type === 'success' ? 'border-emerald-500/40 bg-emerald-950/30 text-emerald-300' : 'border-red-500/40 bg-red-950/30 text-red-300'"
    >
      <div class="flex items-center gap-2">
        <CheckCircle2 v-if="message.type === 'success'" class="h-4 w-4 text-emerald-400" />
        <AlertCircle v-else class="h-4 w-4 text-red-400" />
        <span>{{ message.text }}</span>
      </div>
      <button @click="message = null" class="text-slate-400 hover:text-slate-200">
        &times;
      </button>
    </div>

    <!-- Navigation Tabs -->
    <div class="flex items-center gap-1 overflow-x-auto border-b border-slate-800 bg-slate-900/60 p-1 rounded-xl text-xs">
      <button
        @click="activeTab = 'overview'"
        class="flex items-center gap-2 rounded-lg px-4 py-2 font-medium transition-colors whitespace-nowrap"
        :class="activeTab === 'overview' ? 'bg-slate-800 text-cyan-400 shadow-sm' : 'text-slate-400 hover:text-slate-200'"
      >
        <Activity class="h-3.5 w-3.5" />
        <span>Overview & Diagnostics</span>
      </button>

      <button
        @click="activeTab = 'ingress'"
        class="flex items-center gap-2 rounded-lg px-4 py-2 font-medium transition-colors whitespace-nowrap"
        :class="activeTab === 'ingress' ? 'bg-slate-800 text-cyan-400 shadow-sm' : 'text-slate-400 hover:text-slate-200'"
      >
        <Layers class="h-3.5 w-3.5" />
        <span>Ingress Routes ({{ rules.length }})</span>
      </button>

      <button
        @click="activeTab = 'tcp'"
        class="flex items-center gap-2 rounded-lg px-4 py-2 font-medium transition-colors whitespace-nowrap"
        :class="activeTab === 'tcp' ? 'bg-slate-800 text-cyan-400 shadow-sm' : 'text-slate-400 hover:text-slate-200'"
      >
        <Terminal class="h-3.5 w-3.5" />
        <span>TCP Client Access</span>
      </button>

      <button
        @click="activeTab = 'api'"
        class="flex items-center gap-2 rounded-lg px-4 py-2 font-medium transition-colors whitespace-nowrap"
        :class="activeTab === 'api' ? 'bg-slate-800 text-cyan-400 shadow-sm' : 'text-slate-400 hover:text-slate-200'"
      >
        <KeyRound class="h-3.5 w-3.5" />
        <span>Cloudflare API & Edge</span>
        <span v-if="cfRemoteTunnel" class="h-1.5 w-1.5 rounded-full bg-emerald-400"></span>
      </button>

      <button
        @click="activeTab = 'backups'"
        class="flex items-center gap-2 rounded-lg px-4 py-2 font-medium transition-colors whitespace-nowrap"
        :class="activeTab === 'backups' ? 'bg-slate-800 text-cyan-400 shadow-sm' : 'text-slate-400 hover:text-slate-200'"
      >
        <Archive class="h-3.5 w-3.5" />
        <span>Backups ({{ backups.length }})</span>
      </button>
    </div>

    <!-- ======================================================== -->
    <!-- TAB 1: OVERVIEW & DIAGNOSTICS                            -->
    <!-- ======================================================== -->
    <div v-if="activeTab === 'overview'" class="space-y-5">
      <!-- Quick Telemetry Row (Shown when standalone page) -->
      <div v-if="!embedded" class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <!-- Tunnel Status -->
        <div class="cyber-card p-4 space-y-2">
          <div class="flex items-center justify-between text-xs text-slate-400">
            <span class="uppercase tracking-wider font-semibold text-[10px]">Tunnel Identity</span>
            <Globe class="h-4 w-4 text-cyan-400" />
          </div>
          <div class="font-mono text-sm font-bold text-slate-100 truncate" :title="tunnelId">
            {{ tunnelId || 'Loading…' }}
          </div>
          <div class="flex items-center gap-2 text-[11px] text-slate-400">
            <span class="rounded bg-slate-800 px-1.5 py-0.5 text-slate-300 font-mono">{{ protocol }}</span>
            <span
              class="flex items-center gap-1 font-semibold"
              :class="tunnelStatus?.running ? 'text-emerald-400' : 'text-red-400'"
            >
              <span
                class="h-2 w-2 rounded-full"
                :class="tunnelStatus?.running ? 'bg-emerald-400 animate-pulse' : 'bg-red-400'"
              ></span>
              {{ tunnelStatus?.running ? 'Active' : (tunnelStatus?.status ? `Degraded (${tunnelStatus.status})` : 'Degraded') }}
            </span>
          </div>
        </div>

        <!-- Total Requests -->
        <div class="cyber-card p-4 space-y-2">
          <div class="flex items-center justify-between text-xs text-slate-400">
            <span class="uppercase tracking-wider font-semibold text-[10px]">Total Routed Requests</span>
            <Activity class="h-4 w-4 text-emerald-400" />
          </div>
          <div class="text-2xl font-bold font-mono text-emerald-400">
            {{ tunnelStatus?.running ? (metrics?.total_requests?.toLocaleString() || '0') : '0' }}
          </div>
          <p class="text-[11px] text-slate-500">Cumulative ingress proxy traffic</p>
        </div>

        <!-- Active Connections -->
        <div class="cyber-card p-4 space-y-2">
          <div class="flex items-center justify-between text-xs text-slate-400">
            <span class="uppercase tracking-wider font-semibold text-[10px]">Active Edge Conns</span>
            <Network class="h-4 w-4 text-violet-400" />
          </div>
          <div class="text-2xl font-bold font-mono text-violet-300">
            {{ tunnelStatus?.running ? (metrics?.active_connections ?? 0) : 0 }}
          </div>
          <p class="text-[11px] text-slate-500">QUIC / HTTP2 streams to Cloudflare edge</p>
        </div>

        <!-- Active Hostnames -->
        <div class="cyber-card p-4 space-y-2">
          <div class="flex items-center justify-between text-xs text-slate-400">
            <span class="uppercase tracking-wider font-semibold text-[10px]">Public Hostnames</span>
            <Layers class="h-4 w-4 text-amber-400" />
          </div>
          <div class="text-2xl font-bold font-mono text-amber-300">
            {{ rules.length }}
          </div>
          <p class="text-[11px] text-slate-500">Active ingress mappings</p>
        </div>
      </div>

      <!-- Compact Live Traffic Row (When embedded in Remote Access) -->
      <div v-else class="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <div class="cyber-card p-4 space-y-2">
          <div class="flex items-center justify-between text-xs text-slate-400">
            <span class="uppercase tracking-wider font-semibold text-[10px]">Total Routed Requests</span>
            <Activity class="h-4 w-4 text-emerald-400" />
          </div>
          <div class="text-2xl font-bold font-mono text-emerald-400">
            {{ tunnelStatus?.running ? (metrics?.total_requests?.toLocaleString() || '0') : '0' }}
          </div>
          <p class="text-[11px] text-slate-500">Cumulative ingress proxy traffic</p>
        </div>

        <div class="cyber-card p-4 space-y-2">
          <div class="flex items-center justify-between text-xs text-slate-400">
            <span class="uppercase tracking-wider font-semibold text-[10px]">Active Edge Conns</span>
            <Network class="h-4 w-4 text-violet-400" />
          </div>
          <div class="text-2xl font-bold font-mono text-violet-300">
            {{ tunnelStatus?.running ? (metrics?.active_connections ?? 0) : 0 }}
          </div>
          <p class="text-[11px] text-slate-500">QUIC / HTTP2 streams to Cloudflare edge</p>
        </div>
      </div>

      <!-- Diagnostic Alert Banner when Tunnel is Degraded -->
      <div
        v-if="tunnelStatus && !tunnelStatus.running"
        class="rounded-lg border border-red-900/60 bg-red-950/40 p-4 text-xs text-red-200 flex flex-col sm:flex-row sm:items-start justify-between gap-3"
      >
        <div class="flex items-start gap-2.5">
          <AlertCircle class="h-4 w-4 text-red-400 mt-0.5 shrink-0" />
          <div class="space-y-1.5">
            <p class="font-semibold text-red-300">
              Cloudflare Tunnel Daemon is {{ tunnelStatus.status || 'degraded' }}
            </p>
            <p v-if="tunnelStatus.last_error" class="font-mono text-[11px] text-red-300/90 bg-red-950/70 p-2.5 rounded border border-red-900/40 whitespace-pre-wrap leading-relaxed">
              {{ tunnelStatus.last_error }}
            </p>
            <p v-else class="text-slate-400 text-[11px]">
              The container is not active. Check credentials in /etc/cloudflared/credentials.json or restart the tunnel daemon.
            </p>
          </div>
        </div>
        <button
          @click="restartTunnel"
          :disabled="saving"
          class="cyber-btn text-xs flex items-center gap-1.5 py-1 px-3 border-red-800 bg-red-950/60 hover:bg-red-900/70 text-red-200 shrink-0 self-start sm:self-auto"
        >
          <RotateCcw class="h-3 w-3" :class="saving ? 'animate-spin' : ''" />
          <span>Restart Daemon</span>
        </button>
      </div>

      <!-- HTTP Status Code Breakdown -->
      <div v-if="metrics?.response_codes && Object.keys(metrics.response_codes).length" class="cyber-card p-4 space-y-3">
        <h3 class="text-xs font-bold uppercase tracking-wider text-slate-400">Edge Response Codes</h3>
        <div class="grid grid-cols-2 sm:grid-cols-4 md:grid-cols-6 gap-3">
          <div
            v-for="(count, code) in metrics.response_codes"
            :key="code"
            class="rounded border border-slate-800 bg-slate-950 p-2.5 text-center font-mono"
          >
            <div
              class="text-xs font-bold"
              :class="String(code).startsWith('2') ? 'text-emerald-400' : String(code).startsWith('4') ? 'text-amber-400' : 'text-red-400'"
            >
              HTTP {{ code }}
            </div>
            <div class="text-sm font-bold text-slate-200 mt-1">{{ count }}</div>
          </div>
        </div>
      </div>

      <!-- Ingress Service Health & Latency -->
      <div class="cyber-card p-5 space-y-4">
        <div class="flex items-center justify-between border-b border-slate-800 pb-3">
          <div class="flex items-center gap-2">
            <Activity class="h-4 w-4 text-cyan-400" />
            <h3 class="text-sm font-bold text-slate-100">Live Ingress Target Health & Latency</h3>
          </div>
          <button
            @click="fetchHealth"
            :disabled="healthLoading"
            class="cyber-btn-secondary text-xs flex items-center gap-1.5"
          >
            <RefreshCw class="h-3 w-3" :class="healthLoading ? 'animate-spin' : ''" />
            <span>Check Latency</span>
          </button>
        </div>

        <div v-if="health.length === 0 && !healthLoading" class="text-xs text-slate-500 py-4 text-center font-mono">
          No active ingress targets responding.
        </div>

        <div v-else class="divide-y divide-slate-800/80">
          <div
            v-for="h in health"
            :key="h.hostname"
            class="flex items-center justify-between py-2.5 text-xs font-mono"
          >
            <div>
              <div class="font-bold text-slate-200 flex items-center gap-2">
                <span>{{ h.hostname }}</span>
                <a
                  :href="`https://${h.hostname}`"
                  target="_blank"
                  rel="noopener"
                  class="text-slate-500 hover:text-cyan-300"
                >
                  <ExternalLink class="h-3 w-3" />
                </a>
              </div>
              <div class="text-[11px] text-slate-500 flex items-center gap-2">
                <span>{{ h.service }}</span>
                <router-link
                  v-if="extractTargetHost(h.service)"
                  :to="'/containers?name=' + encodeURIComponent(extractTargetHost(h.service))"
                  class="text-cyan-400 hover:underline text-[10px]"
                  :title="`Inspect container '${extractTargetHost(h.service)}'`"
                >
                  (inspect container)
                </router-link>
              </div>
            </div>

            <div class="flex items-center gap-3">
              <span class="text-slate-400">{{ h.latency }}</span>
              <span
                class="rounded border px-2 py-0.5 text-[10px] uppercase font-semibold"
                :class="h.status === 'up' ? 'border-emerald-500/40 bg-emerald-500/10 text-emerald-400' : 'border-red-500/40 bg-red-500/10 text-red-400'"
              >
                {{ h.status }}
              </span>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- ======================================================== -->
    <!-- TAB 2: INGRESS ROUTING RULES                             -->
    <!-- ======================================================== -->
    <div v-if="activeTab === 'ingress'" class="space-y-4">
      <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-800 pb-3">
        <div>
          <h2 class="text-base font-bold text-slate-100 flex items-center gap-2">
            <Layers class="h-4 w-4 text-cyan-400" />
            <span>Cloudflare Ingress Rules</span>
          </h2>
          <p class="text-xs text-slate-400">
            Maps public domain hostnames to local Docker containers or internal network targets.
          </p>
        </div>

        <div class="flex items-center gap-2">
          <button
            @click="isRawEditor = !isRawEditor"
            class="cyber-btn-secondary text-xs flex items-center gap-1.5"
          >
            <Code v-if="!isRawEditor" class="h-3.5 w-3.5" />
            <Eye v-else class="h-3.5 w-3.5" />
            <span>{{ isRawEditor ? 'Visual View' : 'Raw YAML' }}</span>
          </button>
          <button
            v-if="!isRawEditor"
            @click="addRule"
            class="cyber-btn text-xs flex items-center gap-1.5"
          >
            <Plus class="h-3.5 w-3.5" />
            <span>Add Rule</span>
          </button>
          <button
            @click="syncAllDnsRoutes"
            :disabled="syncLoading"
            class="cyber-btn-secondary text-xs flex items-center gap-1.5 text-cyan-300 border-cyan-800 bg-cyan-950/30 hover:bg-cyan-900/50"
            title="Automatically create or verify DNS CNAME records in Cloudflare for all hostnames above"
          >
            <RefreshCw class="h-3.5 w-3.5" :class="syncLoading ? 'animate-spin' : ''" />
            <span>{{ syncLoading ? 'Syncing DNS…' : 'Sync to Cloudflare DNS' }}</span>
          </button>
          <button
            @click="save(true)"
            :disabled="saving"
            class="cyber-btn text-xs flex items-center gap-1.5 text-emerald-300 border-emerald-600 bg-emerald-950/40 hover:bg-emerald-900/50 font-medium"
            title="Save config and restart tunnel container so changes are active immediately"
          >
            <RotateCcw class="h-3.5 w-3.5" :class="saving ? 'animate-spin' : ''" />
            <span>{{ saving ? 'Saving & Applying...' : 'Save & Apply to Tunnel' }}</span>
          </button>
          <button
            @click="save(false)"
            :disabled="saving"
            class="cyber-btn-secondary text-xs flex items-center gap-1.5 text-slate-400"
            title="Save YAML config without restarting the tunnel"
          >
            <span>Save Only</span>
          </button>
        </div>
      </div>

      <!-- Raw YAML Editor View -->
      <div v-if="isRawEditor" class="space-y-3">
        <textarea
          v-model="rawYaml"
          rows="18"
          spellcheck="false"
          class="w-full rounded-xl border border-slate-800 bg-slate-950 p-4 font-mono text-xs text-slate-200 focus:border-cyan-500 focus:outline-none leading-relaxed"
        ></textarea>
      </div>

      <!-- Visual Rules Cards View -->
      <div v-else class="space-y-3">
        <!-- Security & Cloudflare Access Advice -->
        <div class="rounded-xl border border-cyan-800/40 bg-slate-950 p-4 flex flex-col sm:flex-row items-start gap-3.5">
          <ShieldCheck class="h-5 w-5 text-cyan-400 mt-0.5 flex-shrink-0" />
          <div class="space-y-1 text-xs">
            <div class="font-bold text-slate-200">Exposing Admin Panels & Security Hardening</div>
            <p class="text-slate-400 leading-relaxed">
              If you expose the DockTun management dashboard publicly, we strongly recommend placing it behind
              <strong class="text-cyan-300">Cloudflare Zero Trust (Access)</strong> in your Cloudflare dashboard (Applications &rarr; Add an Application).
              Access requires One-Time PIN email verification or Google/GitHub SSO at the Cloudflare edge before any traffic reaches your server.
              Alternatively, you can safely <strong class="text-amber-300">delete the DockTun rule below</strong> to keep DockTun internal-only and access it via your local network (<code class="text-slate-300 font-mono">http://&lt;lan-ip&gt;:8080</code> or <code class="text-slate-300 font-mono">http://docktun.local</code>).
            </p>
          </div>
        </div>
        <div
          v-for="(rule, i) in rules"
          :key="i"
          class="cyber-card p-4 flex flex-col md:flex-row md:items-center justify-between gap-4"
          :class="isDocktunRule(rule) ? 'border-cyan-500/30 bg-slate-950/90' : ''"
        >
          <div class="flex-1 grid grid-cols-1 md:grid-cols-2 gap-4">
            <!-- Hostname Input -->
            <div>
              <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-400">
                Public Hostname (FQDN)
              </label>
              <div class="flex items-center gap-2">
                <input
                  v-model="rule.hostname"
                  type="text"
                  placeholder="app.example.com"
                  class="cyber-input text-xs font-mono"
                />
                <a
                  v-if="rule.hostname"
                  :href="`https://${rule.hostname}`"
                  target="_blank"
                  rel="noopener"
                  class="rounded-lg p-2 text-slate-400 hover:text-cyan-300 hover:bg-slate-800 transition"
                  title="Open site in new tab"
                >
                  <ExternalLink class="h-4 w-4" />
                </a>
              </div>
            </div>

            <!-- Service Target Input -->
            <div>
              <div class="flex items-center justify-between mb-1">
                <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-400">
                  Local Service Target
                </label>
                <router-link
                  v-if="extractTargetHost(rule.service)"
                  :to="'/containers?name=' + encodeURIComponent(extractTargetHost(rule.service))"
                  class="text-[10px] text-cyan-400 hover:underline font-mono flex items-center gap-1"
                  :title="`Inspect container '${extractTargetHost(rule.service)}'`"
                >
                  <Server class="w-3 h-3 text-cyan-400" />
                  <span>Inspect Container &rarr;</span>
                </router-link>
              </div>
              <input
                v-model="rule.service"
                type="text"
                placeholder="http://container-name:80"
                class="cyber-input text-xs font-mono"
              />
            </div>

            <!-- Options Row -->
            <div class="col-span-full flex flex-wrap items-center justify-between text-xs text-slate-400 pt-1 gap-2">
              <div class="flex flex-wrap items-center gap-3">
                <label class="flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    v-model="rule.noTLSVerify"
                    class="rounded bg-slate-950"
                  />
                  <span class="text-[11px] font-mono">noTLSVerify (Disable TLS origin check)</span>
                </label>

                <!-- Test Origin Button -->
                <button
                  v-if="!rule.service.startsWith('http_status:')"
                  type="button"
                  @click="probeOriginRule(rule)"
                  :disabled="probingHost === rule.hostname"
                  class="cyber-btn-secondary text-[11px] font-mono py-0.5 px-2 flex items-center gap-1.5 hover:text-cyan-300"
                  title="Test HTTP connectivity to origin container"
                >
                  <RotateCcw v-if="probingHost === rule.hostname" class="h-3 w-3 animate-spin text-cyan-400" />
                  <Activity v-else class="h-3 w-3 text-cyan-400" />
                  <span>{{ probingHost === rule.hostname ? 'Testing...' : 'Test Origin' }}</span>
                </button>

                <!-- Probe feedback badge -->
                <span
                  v-if="probeResults[rule.hostname]"
                  class="rounded px-2 py-0.5 text-[10px] font-mono font-bold"
                  :class="probeResults[rule.hostname].reachable ? 'bg-emerald-950/80 text-emerald-400 border border-emerald-800/50' : 'bg-red-950/80 text-red-400 border border-red-800/50'"
                >
                  {{ probeResults[rule.hostname].reachable ? `UP ${probeResults[rule.hostname].latency_ms}ms • HTTP ${probeResults[rule.hostname].status_code}` : (probeResults[rule.hostname].error || 'TIMEOUT') }}
                </span>
              </div>

              <div class="flex items-center gap-2">
                <!-- Maintenance / Pause Toggle -->
                <button
                  type="button"
                  @click="toggleRulePause(rule)"
                  class="rounded px-2 py-0.5 text-[10px] font-mono font-semibold transition"
                  :class="rule.service.startsWith('http_status:') ? 'bg-amber-950 text-amber-300 border border-amber-800 hover:bg-amber-900/60' : 'bg-slate-900 text-slate-400 border border-slate-800 hover:text-slate-200'"
                  :title="rule.service.startsWith('http_status:') ? 'Resume routing to container' : 'Temporarily pause rule with HTTP 503 maintenance mode'"
                >
                  {{ rule.service.startsWith('http_status:') ? '⚠ MAINTENANCE (503) • CLICK TO RESUME' : 'Pause (503 Mode)' }}
                </button>

                <span
                  v-if="isDocktunRule(rule)"
                  class="inline-flex items-center gap-1 rounded bg-cyan-950/60 border border-cyan-800/40 px-2 py-0.5 text-[10px] font-mono text-cyan-300 font-semibold"
                >
                  <Sliders class="h-3 w-3" />
                  DockTun Dashboard
                </span>
              </div>
            </div>
          </div>

          <!-- Delete Rule Button -->
          <div class="flex md:flex-col justify-end pt-2 md:pt-0">
            <button
              @click="removeRule(i)"
              class="cyber-btn-danger !px-2.5 !py-2 text-xs"
              title="Delete rule"
            >
              <Trash2 class="h-4 w-4" />
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- ======================================================== -->
    <!-- TAB 3: TCP CLIENT ACCESS HELPER                          -->
    <!-- ======================================================== -->
    <div v-if="activeTab === 'tcp'" class="space-y-5">
      <!-- Architectural Note: Free Tier TCP/UDP Reality, Workarounds & Playit.gg -->
      <div class="cyber-card p-5 border-amber-500/30 bg-gradient-to-br from-slate-900/90 via-slate-950 to-amber-950/20 max-w-3xl space-y-3">
        <div class="flex items-start gap-3">
          <div class="p-2 rounded-lg bg-amber-500/10 border border-amber-500/30 text-amber-400 mt-0.5 shrink-0">
            <Info class="h-5 w-5" />
          </div>
          <div class="space-y-2 text-xs flex-1">
            <div class="flex items-center gap-2">
              <h3 class="font-bold text-white text-sm">Protocol Architecture: Free Tier Limitations & Workarounds</h3>
              <span class="text-[10px] uppercase font-mono px-2 py-0.5 rounded bg-amber-500/20 text-amber-300 border border-amber-500/30">
                Architectural Note
              </span>
            </div>
            <p class="text-slate-300 leading-relaxed">
              Does arbitrary <strong class="text-white">TCP/UDP</strong> work natively on Cloudflare's Free Tier? 
              <strong class="text-amber-300">No, the command below is a WebSocket encapsulation workaround.</strong>
              Cloudflare edge public proxies listen only on HTTP/HTTPS ports (80/443).
            </p>
            <div class="grid grid-cols-1 md:grid-cols-2 gap-3 pt-1">
              <div class="p-3 rounded-lg bg-slate-950/70 border border-slate-800 space-y-1.5">
                <div class="text-[11px] font-semibold text-cyan-300 flex items-center gap-1.5">
                  <Terminal class="h-3.5 w-3.5" />
                  <span>The `cloudflared access tcp` Workaround</span>
                </div>
                <p class="text-[11px] text-slate-400 leading-normal">
                  Wraps raw TCP into a WebSocket stream over port 443 through Cloudflare edge.
                  <strong class="text-slate-300 block mt-1">Requirement:</strong> The client machine must run <code class="text-cyan-400 font-mono">cloudflared access tcp</code> locally. Excellent for personal SSH, RDP, and DB admin.
                </p>
              </div>

              <div class="p-3 rounded-lg bg-slate-950/70 border border-slate-800 space-y-1.5">
                <div class="text-[11px] font-semibold text-rose-300 flex items-center gap-1.5">
                  <Gamepad2 class="h-3.5 w-3.5" />
                  <span>Gaming & Raw UDP (Minecraft, Rust, Palworld)</span>
                </div>
                <p class="text-[11px] text-slate-400 leading-normal">
                  Standard game clients cannot connect to Cloudflare Free tunnels without client-side software. Raw public TCP/UDP requires <strong class="text-slate-300">Cloudflare Spectrum</strong> (Enterprise/paid).
                </p>
              </div>
            </div>

            <div class="p-3 rounded-lg bg-indigo-950/40 border border-indigo-500/30 text-[11px] text-slate-300 flex flex-col sm:flex-row sm:items-center justify-between gap-2 mt-1">
              <div class="flex items-center gap-2">
                <Gamepad2 class="h-4 w-4 text-indigo-400 shrink-0" />
                <span>
                  <strong>Need public game hosting without player software?</strong>
                  Use <strong class="text-indigo-300">Playit.gg</strong> (Anycast public TCP/UDP) or invite friends to your private <strong class="text-purple-300">Tailscale</strong> network.
                </span>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div class="cyber-card p-5 space-y-4 max-w-3xl">
        <div class="border-b border-slate-800 pb-3">
          <h2 class="text-base font-bold text-slate-100 flex items-center gap-2">
            <Terminal class="h-4 w-4 text-cyan-400" />
            <span>TCP Client Access Helper (SSH, RDP, Databases)</span>
          </h2>
          <p class="text-xs text-slate-400 mt-1">
            To connect to non-HTTP services routed through Cloudflare Access, run the generated
            <code class="text-cyan-300">cloudflared access tcp</code> command on your local client computer (laptop or desktop).
          </p>
        </div>

        <div class="grid gap-4 md:grid-cols-2">
          <div>
            <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-400">
              Cloudflare Hostname (--hostname)
            </label>
            <input
              v-model="tcpForm.hostname"
              type="text"
              placeholder="ssh.example.com or db.example.com"
              class="cyber-input text-xs font-mono"
            />
          </div>

          <div>
            <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-400">
              Local Client Binding (--url)
            </label>
            <input
              v-model="tcpForm.local_url"
              type="text"
              placeholder="localhost:2222 or 127.0.0.1:5432"
              class="cyber-input text-xs font-mono"
            />
          </div>

          <div class="col-span-full">
            <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-wider text-slate-400">
              Execution Mode
            </label>
            <div class="flex gap-4 text-xs font-mono">
              <label class="flex items-center gap-2 cursor-pointer">
                <input type="radio" v-model="tcpForm.mode" value="foreground" class="accent-cyan-400" />
                <span>Foreground (Interactive shell)</span>
              </label>
              <label class="flex items-center gap-2 cursor-pointer">
                <input type="radio" v-model="tcpForm.mode" value="nohup" class="accent-cyan-400" />
                <span>Nohup (Background process)</span>
              </label>
              <label class="flex items-center gap-2 cursor-pointer">
                <input type="radio" v-model="tcpForm.mode" value="systemd" class="accent-cyan-400" />
                <span>Systemd (Persistent service unit)</span>
              </label>
            </div>
          </div>
        </div>

        <div class="pt-2">
          <button @click="generateTcpCommand" class="cyber-btn text-xs text-cyan-300">
            Generate Client Command
          </button>
        </div>

        <!-- Output Code Box -->
        <div v-if="generatedTcpCmd" class="mt-4 rounded-xl border border-cyan-500/30 bg-slate-950 p-4 relative">
          <div class="flex items-center justify-between pb-2 mb-2 border-b border-slate-800 text-[11px] text-slate-400 font-mono">
            <span>Run on your client machine:</span>
            <button
              @click="copyText('tcp', generatedTcpCmd)"
              class="cyber-btn-sm text-xs"
            >
              <Check v-if="copiedKey === 'tcp'" class="h-3 w-3 text-emerald-400 mr-1" />
              <Copy v-else class="h-3 w-3 mr-1" />
              <span>Copy</span>
            </button>
          </div>
          <pre class="font-mono text-xs text-cyan-300 whitespace-pre-wrap leading-relaxed">{{ generatedTcpCmd }}</pre>
        </div>
      </div>
    </div>

    <!-- ======================================================== -->
    <!-- TAB 4: CLOUDFLARE API & REMOTE EDGE                      -->
    <!-- ======================================================== -->
    <div v-if="activeTab === 'api'" class="space-y-5">
      <div class="cyber-card p-5 space-y-4 max-w-3xl">
        <div class="border-b border-slate-800 pb-3">
          <h2 class="text-base font-bold text-slate-100 flex items-center gap-2">
            <KeyRound class="h-4 w-4 text-cyan-400" />
            <span>Cloudflare v4 REST API Integration</span>
          </h2>
          <p class="text-xs text-slate-400 mt-1">
            Connect your Cloudflare API Token to query remote tunnel edge status, active edge connectors, and verify DNS records.
          </p>
        </div>

        <div class="grid gap-4 md:grid-cols-2">
          <div class="col-span-full">
            <div class="flex items-center justify-between mb-1">
              <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-400">
                Cloudflare API Token
              </label>
              <span v-if="cfApiHasToken" class="text-[10px] text-emerald-400 font-mono flex items-center gap-1">
                <CheckCircle2 class="h-3 w-3" />
                Detected from file/environment ({{ cfApiMaskedToken }})
              </span>
            </div>
            <input
              v-model="cfApiToken"
              type="password"
              :placeholder="cfApiHasToken ? 'Configured: ' + cfApiMaskedToken + ' (enter new value to override)' : 'Bearer Token with Cloudflare Tunnel & DNS permissions'"
              class="cyber-input text-xs font-mono"
            />
          </div>

          <div>
            <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-400">
              Cloudflare Account ID
            </label>
            <input
              v-model="cfAccountId"
              type="text"
              placeholder="e.g. 7a8b9c..."
              class="cyber-input text-xs font-mono"
            />
          </div>

          <div>
            <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-400">
              Cloudflare Zone ID (Optional)
            </label>
            <input
              v-model="cfZoneId"
              type="text"
              placeholder="For DNS CNAME verification"
              class="cyber-input text-xs font-mono"
            />
          </div>
        </div>

        <div class="flex items-center gap-3 pt-2">
          <button
            @click="saveCfApiCredentials"
            :disabled="cfApiLoading"
            class="cyber-btn text-xs text-cyan-300"
          >
            <RefreshCw v-if="cfApiLoading" class="h-3 w-3 animate-spin mr-1" />
            <span>Save & Query Cloudflare API</span>
          </button>
          <button
            v-if="cfApiHasToken || cfApiToken"
            @click="fetchRemoteTunnelInfo"
            :disabled="cfApiLoading"
            class="cyber-btn-secondary text-xs text-slate-300 flex items-center gap-1.5"
          >
            <RefreshCw class="h-3 w-3" :class="cfApiLoading ? 'animate-spin' : ''" />
            <span>Query Remote Status</span>
          </button>
          <span v-if="cfApiSuccess" class="text-xs text-emerald-400 font-medium">Credentials saved!</span>
        </div>

        <div v-if="cfApiError" class="rounded-lg border border-red-500/40 bg-red-950/20 p-3 text-xs text-red-300 font-mono">
          {{ cfApiError }}
        </div>

        <!-- Automated DNS Management Actions Card -->
        <div class="rounded-xl border border-cyan-900/40 bg-cyan-950/20 p-4 space-y-3">
          <div class="flex items-center gap-2">
            <Zap class="h-4 w-4 text-cyan-400" />
            <h3 class="text-xs font-bold uppercase tracking-wider text-cyan-300">Automated DNS Route Management</h3>
          </div>
          <p class="text-xs text-slate-400">
            DockTun automatically uses your API Token to create or update Cloudflare CNAME records (<code class="text-cyan-300 font-mono">{{ tunnelId || 'tunnel-id' }}.cfargotunnel.com</code>) whenever you expose containers or save ingress routes.
          </p>

          <div class="flex flex-wrap items-center gap-3 pt-1">
            <button
              @click="syncAllDnsRoutes"
              :disabled="syncLoading || (!cfApiToken && !cfApiHasToken)"
              class="cyber-btn text-xs flex items-center gap-1.5 text-cyan-300 border-cyan-600 bg-cyan-950/40 hover:bg-cyan-900/60 disabled:opacity-50"
            >
              <RefreshCw class="h-3.5 w-3.5" :class="syncLoading ? 'animate-spin' : ''" />
              <span>{{ syncLoading ? 'Syncing DNS…' : 'Sync All Ingress Routes to Cloudflare DNS' }}</span>
            </button>
          </div>

          <!-- Wildcard Route Creator -->
          <div class="pt-3 border-t border-cyan-900/30 space-y-2">
            <div class="text-[11px] font-semibold text-slate-300">
              1-Click Wildcard Route (*.yourdomain.com):
            </div>
            <p class="text-[11px] text-slate-400">
              Creates a wildcard CNAME in Cloudflare DNS so any subdomain you ever add routes to this tunnel automatically with zero configuration.
            </p>
            <div class="flex flex-col sm:flex-row sm:items-center gap-2.5">
              <input
                v-model="wildcardDomainInput"
                type="text"
                placeholder="e.g. yourdomain.com"
                class="cyber-input text-xs font-mono max-w-xs"
              />
              <button
                @click="createWildcardRoute"
                :disabled="wildcardLoading || (!cfApiToken && !cfApiHasToken) || !wildcardDomainInput"
                class="cyber-btn text-xs flex items-center gap-1.5 text-emerald-300 border-emerald-600 bg-emerald-950/40 hover:bg-emerald-900/60 disabled:opacity-50"
              >
                <CheckCircle2 class="h-3.5 w-3.5" :class="wildcardLoading ? 'animate-spin' : ''" />
                <span>{{ wildcardLoading ? 'Creating…' : 'Create Wildcard DNS (*.domain)' }}</span>
              </button>
            </div>
          </div>
        </div>

        <!-- Remote Tunnel Details from Cloudflare API -->
        <div v-if="cfRemoteTunnel" class="rounded-xl border border-slate-800 bg-slate-950 p-4 space-y-3 font-mono text-xs">
          <div class="flex items-center justify-between border-b border-slate-800 pb-2">
            <span class="text-slate-400 font-bold uppercase text-[10px]">Cloudflare Edge Status:</span>
            <span class="rounded bg-emerald-500/20 text-emerald-300 border border-emerald-500/40 px-2 py-0.5 text-[10px] font-bold">
              {{ cfRemoteTunnel.status || 'HEALTHY' }}
            </span>
          </div>

          <div class="grid grid-cols-1 sm:grid-cols-2 gap-2 text-[11px]">
            <div><span class="text-slate-500">Name:</span> <span class="text-slate-200">{{ cfRemoteTunnel.name }}</span></div>
            <div><span class="text-slate-500">Created:</span> <span class="text-slate-200">{{ cfRemoteTunnel.created_at?.slice(0, 10) }}</span></div>
            <div><span class="text-slate-500">Remote Connections:</span> <span class="text-cyan-400 font-bold">{{ cfRemoteTunnel.connections?.length || 0 }}</span></div>
            <div><span class="text-slate-500">Remote Client:</span> <span class="text-slate-300">{{ cfRemoteTunnel.remote_config ? 'Remote Managed' : 'Local Config Managed' }}</span></div>
          </div>

          <!-- Active Remote Edge Connectors Table -->
          <div v-if="cfRemoteTunnel.connections && cfRemoteTunnel.connections.length" class="space-y-1.5 pt-2">
            <div class="text-[10px] uppercase font-bold text-slate-400">Active Edge Colo Nodes:</div>
            <div
              v-for="(conn, idx) in cfRemoteTunnel.connections"
              :key="idx"
              class="flex items-center justify-between rounded bg-slate-900/60 p-2 text-[11px]"
            >
              <div class="flex items-center gap-2">
                <span class="h-2 w-2 rounded-full bg-emerald-400"></span>
                <span class="text-slate-300 font-bold">{{ conn.colo_name }}</span>
                <span class="text-slate-500 font-mono">({{ conn.origin_ip }})</span>
              </div>
              <span class="text-slate-400 text-[10px]">{{ conn.client_version || 'cloudflared' }}</span>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- ======================================================== -->
    <!-- TAB 5: CONFIG BACKUPS                                    -->
    <!-- ======================================================== -->
    <div v-if="activeTab === 'backups'" class="space-y-4">
      <div class="flex items-center justify-between border-b border-slate-800 pb-3">
        <div>
          <h2 class="text-base font-bold text-slate-100 flex items-center gap-2">
            <Archive class="h-4 w-4 text-cyan-400" />
            <span>Config Snapshots & Backups</span>
          </h2>
          <p class="text-xs text-slate-400">
            DockTun automatically creates timestamped backups before each write to <code class="text-slate-300">config.yml</code>.
          </p>
        </div>

        <div class="flex items-center gap-2">
          <button
            @click="loadBackups"
            :disabled="backupsLoading"
            class="cyber-btn-secondary text-xs flex items-center gap-1.5"
          >
            <RefreshCw class="h-3 w-3" :class="backupsLoading ? 'animate-spin' : ''" />
            <span>Refresh</span>
          </button>
          <button
            @click="createManualBackup"
            :disabled="backupsLoading"
            class="cyber-btn text-xs flex items-center gap-1.5 text-cyan-300 border-cyan-700 bg-cyan-950/40 hover:bg-cyan-900/50"
          >
            <Plus class="h-3 w-3" />
            <span>Create Snapshot</span>
          </button>
        </div>
      </div>

      <div v-if="backups.length === 0 && !backupsLoading" class="cyber-card p-8 text-center text-xs text-slate-500">
        No configuration backups recorded yet.
      </div>

      <div class="space-y-2">
        <div
          v-for="b in backups"
          :key="b.id"
          class="cyber-card p-3 flex items-center justify-between text-xs font-mono"
        >
          <div class="flex items-center gap-3">
            <Archive class="h-4 w-4 text-slate-500" />
            <div>
              <div class="font-bold text-slate-200">Backup #{{ b.id }}</div>
              <div class="text-[11px] text-slate-500">{{ b.created_at }}</div>
            </div>
          </div>

          <div class="flex items-center gap-2">
            <button
              @click="restoreBackup(b)"
              class="cyber-btn-sm text-xs text-cyan-300 hover:border-cyan-500"
            >
              Restore Snapshot
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
