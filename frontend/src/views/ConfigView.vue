<script setup lang="ts">
import ProviderSetupGuide from '../components/ProviderSetupGuide.vue'
import { ref, reactive, onMounted, onUnmounted, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
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
  Radio,
  Search
} from 'lucide-vue-next'
import { useApi } from '../composables/useApi'

const props = withDefaults(defineProps<{ embedded?: boolean }>(), { embedded: false })
const { apiFetch } = useApi()
const route = useRoute()
const router = useRouter()

interface IngressRule {
  hostname: string
  service: string
  noTLSVerify: boolean
  httpHostHeader?: string
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
const message = ref<{ type: 'success' | 'error'; text: string; action?: string } | null>(null)

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

const connectorToken = ref('')
const connectorPresent = ref(false)
function cleanConnectorToken(raw: string): string {
  let tok = raw.trim()
  const match = tok.match(/--token(?:=|\s+)([^\s"']+)/)
  if (match) return match[1].replace(/["']/g, '').trim()
  if (tok.includes('service install')) {
    const sm = tok.match(/service\s+install\s+([^\s"']+)/)
    if (sm) return sm[1].replace(/["']/g, '').trim()
  }
  return tok.replace(/["']/g, '').trim()
}
async function fetchConnector() {
  try { connectorPresent.value = (await apiFetch<any>('/api/provider/cloudflare/credentials')).has_token } catch {}
}
async function saveConnector(clear = false) {
  saving.value = true
  try {
    const tokenVal = clear ? '' : cleanConnectorToken(connectorToken.value)
    const result = await apiFetch<any>('/api/provider/cloudflare/credentials', { method: 'PUT', body: JSON.stringify({ token: tokenVal }) })
    connectorPresent.value = result.has_token
    connectorToken.value = ''
    if (tunnelStatus.value?.running) {
      await apiFetch('/api/control/restart', { method: 'POST' })
      await fetchStatus()
      message.value = { type: 'success', text: 'Connector credentials updated and Cloudflare Tunnel restarted.' }
    } else {
      message.value = { type: 'success', text: clear ? 'Switched to local YAML credentials.' : 'Credentials saved. Click Enable Cloudflare to start the tunnel.' }
    }
  } catch (e: any) { message.value = { type: 'error', text: e.message } }
  finally { saving.value = false }
}
async function controlProvider(action: 'start' | 'stop') {
  saving.value = true
  try {
    if (action === 'start' && connectorToken.value.trim()) {
      const tokenVal = cleanConnectorToken(connectorToken.value)
      const res = await apiFetch<any>('/api/provider/cloudflare/credentials', { method: 'PUT', body: JSON.stringify({ token: tokenVal }) })
      connectorPresent.value = res.has_token
      connectorToken.value = ''
    }
    await apiFetch(`/api/control/${action}`, { method: 'POST' })
    await fetchStatus()
    message.value = { type: 'success', text: action === 'start' ? 'Cloudflare start requested. Check its status and logs for the connection result.' : 'Cloudflare stopped. Other providers keep their own state.' }
  } catch (e: any) { message.value = { type: 'error', text: e.message } }
  finally { saving.value = false }
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

function cleanYamlVal(val?: string): string {
  if (!val) return ''
  return val.trim().replace(/^["']|["']$/g, '').trim()
}

function parseYaml(content: string) {
  const lines = content.split('\n')
  rules.value = []
  for (const line of lines) {
    const m = line.match(/^tunnel:\s*(.+)/)
    if (m) tunnelId.value = cleanYamlVal(m[1])
    const cm = line.match(/^credentials-file:\s*(.+)/)
    if (cm) credentialsFile.value = cleanYamlVal(cm[1])
    const pm = line.match(/^protocol:\s*(.+)/)
    if (pm) protocol.value = cleanYamlVal(pm[1])
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
    const hhm = line.match(/httpHostHeader:\s*["']?([^"'\s]+)["']?/)

    if (isStandaloneService) {
      if (cur.hostname) {
        rules.value.push({
          hostname: cur.hostname,
          service: cur.service || '',
          noTLSVerify: cur.noTLSVerify || false,
          httpHostHeader: cur.httpHostHeader || '',
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
          httpHostHeader: cur.httpHostHeader || '',
        })
      }
      cur = { hostname: cleanYamlVal(hm[1]) }
    } else if (sm && cur.hostname) {
      cur.service = cleanYamlVal(sm[1])
    } else if (tm) {
      cur.noTLSVerify = true
    } else if (hhm) {
      cur.httpHostHeader = cleanYamlVal(hhm[1])
    }
  }

  if (cur.hostname) {
    rules.value.push({
      hostname: cur.hostname,
      service: cur.service || '',
      noTLSVerify: cur.noTLSVerify || false,
      httpHostHeader: cur.httpHostHeader || '',
    })
  }
}

function generateYaml(): string {
  const tid = cleanYamlVal(tunnelId.value)
  const creds = cleanYamlVal(credentialsFile.value) || '/etc/cloudflared/credentials.json'
  const proto = cleanYamlVal(protocol.value) || 'http2'

  let y = `tunnel: ${tid}\ncredentials-file: ${creds}\nprotocol: ${proto}\n\ningress:\n`
  for (const r of rules.value) {
    if (!r.hostname || !r.service) continue
    // Clean hostname: remove https:// and trailing slashes
    let cleanH = cleanYamlVal(r.hostname).replace(/^https?:\/\//i, '').replace(/\/+$/, '').replace(/:[0-9]+$/, '')
    let target = cleanYamlVal(r.service)
    // Normalize .local to direct container name:
    if (target.includes('.local')) {
      target = target.replace('.local', '')
    }
    // Normalize localhost / 127.0.0.1 to host.docker.internal:
    if (target.includes('localhost') || target.includes('127.0.0.1')) {
      target = target.replace('localhost', 'host.docker.internal').replace('127.0.0.1', 'host.docker.internal')
    }
    y += `  - hostname: ${cleanH}\n    service: ${target}\n`
    if (r.noTLSVerify || r.httpHostHeader || target.startsWith('https://')) {
      y += `    originRequest:\n`
      if (r.noTLSVerify || target.startsWith('https://')) {
        y += `      noTLSVerify: true\n`
      }
      if (r.httpHostHeader) {
        y += `      httpHostHeader: "${cleanYamlVal(r.httpHostHeader)}"\n`
      }
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
      message.value = { type: 'success', text: 'Config saved and Cloudflare start requested. Check connection status and logs.' }
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
    message.value = { type: 'success', text: 'Cloudflare restart requested. Check connection status and logs.' }
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
    if (s.api_token) {
      cfApiToken.value = s.api_token
      localStorage.setItem('docktun_cf_api_token', s.api_token)
    }
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

const dnsInspectHost = ref('')
const dnsInspectLoading = ref(false)
const dnsInspectResult = ref<any>(null)
const dnsReplaceLoading = ref(false)

async function inspectDnsRecord() {
  const host = dnsInspectHost.value.trim()
  if (!host) {
    alert('Please enter or select a hostname to inspect')
    return
  }
  dnsInspectLoading.value = true
  dnsInspectResult.value = null
  try {
    const res = await apiFetch<any>(`/api/cloudflare/dns-check?hostname=${encodeURIComponent(host)}`)
    dnsInspectResult.value = res
  } catch (err: any) {
    alert(err.message || 'Failed to inspect Cloudflare DNS record')
  } finally {
    dnsInspectLoading.value = false
  }
}

async function forceReplaceDnsRecord() {
  const host = dnsInspectHost.value.trim()
  if (!host) return
  if (!confirm(`Are you sure you want to delete any existing old DNS records for ${host} and point it to this DockTun tunnel?`)) return
  dnsReplaceLoading.value = true
  try {
    const res = await apiFetch<any>('/api/cloudflare/dns-replace', {
      method: 'POST',
      body: JSON.stringify({ hostname: host }),
    })
    alert(res.message || 'DNS record successfully replaced!')
    await inspectDnsRecord()
  } catch (err: any) {
    alert(err.message || 'Failed to replace DNS record')
  } finally {
    dnsReplaceLoading.value = false
  }
}

async function syncAllDnsRoutes(force = false) {
  syncLoading.value = true
  syncResults.value = null
  try {
    const url = force ? '/api/cloudflare/sync-all?force=true' : '/api/cloudflare/sync-all'
    const res = await apiFetch<any>(url, { method: 'POST' })
    syncResults.value = res.synced || []
    const successCount = res.synced?.filter((s: any) => s.success).length || 0
    const failList = res.synced?.filter((s: any) => !s.success) || []

    if (failList.length > 0) {
      const errDetails = failList.map((f: any) => f.error ? `${f.hostname}: ${f.error}` : f.hostname).join('; ')
      const isTokenReq = errDetails.toLowerCase().includes('token required')
      message.value = {
        type: 'error',
        text: `DNS Sync: ${successCount} verified, ${failList.length} failed (${errDetails}).`,
        action: isTokenReq ? 'token_required' : 'force_overwrite',
      }
    } else {
      message.value = {
        type: 'success',
        text: `Successfully synced ${successCount} DNS routes in Cloudflare!`,
      }
      setTimeout(() => (message.value = null), 6000)
    }
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

interface AccessApp {
  id: string
  name: string
  domain: string
  type: string
  session_duration: string
  created_at?: string
}

const accessApps = ref<AccessApp[]>([])
const loadingAccessApps = ref(false)
const newAccessAppHost = ref('')
const newAccessAppEmails = ref('')
const newAccessAppAuthType = ref<'email' | 'domain' | 'everyone' | 'ip' | 'bypass_ip'>('email')
const newAccessAppSession = ref('24h')
const creatingAccessApp = ref(false)

async function fetchAccessApps() {
  if (!cfApiHasToken.value && !cfApiToken.value) return
  loadingAccessApps.value = true
  try {
    const data = await apiFetch<AccessApp[]>('/api/cloudflare/access-apps')
    if (Array.isArray(data)) {
      accessApps.value = data
    }
  } catch (err: any) {
    console.error('Failed to query Zero Trust Access applications:', err)
  } finally {
    loadingAccessApps.value = false
  }
}

async function createAccessApp() {
  const host = newAccessAppHost.value.trim()
  if (!host) {
    alert('Please enter a hostname (e.g. sonarr.yourdomain.com)')
    return
  }
  creatingAccessApp.value = true
  try {
    await apiFetch('/api/cloudflare/access-apps', {
      method: 'POST',
      body: JSON.stringify({
        hostname: host,
        auth_type: newAccessAppAuthType.value,
        emails: newAccessAppEmails.value.trim(),
        session_duration: newAccessAppSession.value,
      }),
    })
    newAccessAppHost.value = ''
    newAccessAppEmails.value = ''
    await fetchAccessApps()
  } catch (err: any) {
    alert(err.message || 'Failed to create Zero Trust Access application')
  } finally {
    creatingAccessApp.value = false
  }
}

async function deleteAccessApp(id: string, name: string) {
  if (!confirm(`Remove Zero Trust Access protection for ${name}?`)) return
  try {
    await apiFetch(`/api/cloudflare/access-apps/${id}`, { method: 'DELETE' })
    await fetchAccessApps()
  } catch (err: any) {
    alert(err.message || 'Failed to delete Zero Trust application')
  }
}

let metricsTimer: any = null

function setSubTab(tab: 'overview' | 'ingress' | 'tcp' | 'api' | 'backups') {
  activeTab.value = tab
  router.replace({ query: { ...route.query, sub: tab } })
}

watch(
  () => route.query.sub,
  (newSub) => {
    if (newSub && ['overview', 'ingress', 'tcp', 'api', 'backups', 'health'].includes(newSub as string)) {
      activeTab.value = newSub === 'health' ? 'overview' : (newSub as any)
    }
  }
)

onMounted(async () => {
  await fetchConnector()
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
    fetchAccessApps()
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
        <div class="flex items-center gap-2 text-[10px] font-semibold uppercase tracking-[0.22em] text-slate-600">
          <Globe class="h-3.5 w-3.5 text-cyan-600" />
          <span>Cloudflare Tunnel Hub</span>
        </div>
        <h1 class="text-2xl font-bold tracking-tight text-slate-900">Cloudflare Tunnel</h1>
        <p class="text-xs text-slate-600">
          Manage remote edge routing, ingress hostnames, live edge traffic, and client TCP access.
        </p>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <button
          @click="restartTunnel"
          :disabled="saving"
          class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-amber-700 hover:border-amber-600 hover:bg-amber-50/40"
          title="Restart Cloudflare Tunnel daemon"
        >
          <RotateCcw class="h-3.5 w-3.5" :class="saving ? 'animate-spin' : ''" />
          <span>Restart Tunnel</span>
        </button>
        <button
          @click="loadConfig"
          class="cyber-btn flex items-center gap-1.5 text-xs text-slate-700"
        >
          <RefreshCw class="h-3.5 w-3.5" />
          <span>Reload</span>
        </button>
      </div>
    </div>
    <!-- Mini actions bar when embedded in RemoteAccessView -->
    <div v-else class="flex flex-wrap items-center justify-between gap-3 pb-1">
      <div class="flex items-center gap-2 text-xs text-slate-600">
        <span class="inline-flex items-center gap-1.5 font-medium" :class="tunnelStatus?.running ? 'text-emerald-700' : 'text-slate-500'">
          <span class="h-2 w-2 rounded-full" :class="tunnelStatus?.running ? 'bg-emerald-500 animate-pulse' : 'bg-slate-400'"></span>
          {{ tunnelStatus?.running ? 'Cloudflare Tunnel Running' : 'Cloudflare Tunnel Stopped' }}
        </span>
        <span v-if="connectorPresent" class="text-slate-300">·</span>
        <span v-if="connectorPresent" class="inline-flex items-center gap-1 text-[11px] text-emerald-700 font-medium">✓ Connector token active</span>
      </div>
      <div class="flex items-center gap-2">
        <button
          @click="controlProvider(tunnelStatus?.running ? 'stop' : 'start')"
          :disabled="saving"
          class="cyber-btn-secondary text-xs"
        >
          {{ saving ? 'Working…' : tunnelStatus?.running ? 'Disable Cloudflare' : 'Enable Cloudflare' }}
        </button>
        <button
          @click="restartTunnel"
          :disabled="saving"
          class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-amber-700 hover:border-amber-600 hover:bg-amber-50/40"
          title="Restart Cloudflare Tunnel daemon"
        >
          <RotateCcw class="h-3.5 w-3.5" :class="saving ? 'animate-spin' : ''" />
          <span>Restart Tunnel</span>
        </button>
        <button
          @click="loadConfig"
          class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-slate-700"
        >
          <RefreshCw class="h-3.5 w-3.5" />
          <span>Reload</span>
        </button>
      </div>
    </div>

    <!-- Navigation Tabs -->
    <div class="flex items-center gap-1 overflow-x-auto border-b border-slate-200 bg-white/60 p-1 rounded-xl text-xs">
      <button
        @click="setSubTab('overview')"
        class="flex items-center gap-2 rounded-lg px-4 py-2 font-medium transition-colors whitespace-nowrap"
        :class="activeTab === 'overview' ? 'bg-slate-100 text-cyan-600 shadow-sm' : 'text-slate-600 hover:text-slate-800'"
      >
        <Activity class="h-3.5 w-3.5" />
        <span>Overview & Diagnostics</span>
      </button>

      <button
        @click="setSubTab('ingress')"
        class="flex items-center gap-2 rounded-lg px-4 py-2 font-medium transition-colors whitespace-nowrap"
        :class="activeTab === 'ingress' ? 'bg-slate-100 text-cyan-600 shadow-sm' : 'text-slate-600 hover:text-slate-800'"
      >
        <Layers class="h-3.5 w-3.5" />
        <span>Ingress Routes ({{ rules.length }})</span>
      </button>

      <button
        @click="setSubTab('tcp')"
        class="flex items-center gap-2 rounded-lg px-4 py-2 font-medium transition-colors whitespace-nowrap"
        :class="activeTab === 'tcp' ? 'bg-slate-100 text-cyan-600 shadow-sm' : 'text-slate-600 hover:text-slate-800'"
      >
        <Terminal class="h-3.5 w-3.5" />
        <span>TCP Client Access</span>
      </button>

      <button
        @click="setSubTab('api')"
        class="flex items-center gap-2 rounded-lg px-4 py-2 font-medium transition-colors whitespace-nowrap"
        :class="activeTab === 'api' ? 'bg-slate-100 text-cyan-600 shadow-sm' : 'text-slate-600 hover:text-slate-800'"
      >
        <KeyRound class="h-3.5 w-3.5" />
        <span>Cloudflare API & Edge</span>
        <span v-if="cfRemoteTunnel" class="h-1.5 w-1.5 rounded-full bg-emerald-400"></span>
      </button>

      <button
        @click="setSubTab('backups')"
        class="flex items-center gap-2 rounded-lg px-4 py-2 font-medium transition-colors whitespace-nowrap"
        :class="activeTab === 'backups' ? 'bg-slate-100 text-cyan-600 shadow-sm' : 'text-slate-600 hover:text-slate-800'"
      >
        <Archive class="h-3.5 w-3.5" />
        <span>Backups ({{ backups.length }})</span>
      </button>
    </div>

    <!-- Alert / Toast Feedback -->
    <div
      v-if="message"
      class="rounded-lg border p-3 text-xs font-mono flex flex-col sm:flex-row sm:items-center justify-between gap-2"
      :class="message.type === 'success' ? 'border-emerald-500/40 bg-emerald-50/30 text-emerald-700' : 'border-red-500/40 bg-red-50/30 text-red-700'"
    >
      <div class="flex items-center gap-2">
        <CheckCircle2 v-if="message.type === 'success'" class="h-4 w-4 text-emerald-600 shrink-0" />
        <AlertCircle v-else class="h-4 w-4 text-red-600 shrink-0" />
        <span>{{ message.text }}</span>
      </div>
      <div class="flex items-center gap-2 shrink-0">
        <button
          v-if="message.action === 'token_required'"
          @click="setSubTab('api')"
          class="cyber-btn-secondary text-[11px] px-2.5 py-1 text-cyan-700 border-cyan-300 bg-cyan-100/60 hover:bg-cyan-200/60 font-sans font-bold flex items-center gap-1"
          title="Open Cloudflare API & Edge tab to enter your API Token"
        >
          <KeyRound class="h-3 w-3 text-cyan-600" />
          <span>Enter API Token (Tab 4)</span>
        </button>
        <button
          v-else-if="message.action === 'force_overwrite'"
          @click="syncAllDnsRoutes(true)"
          :disabled="syncLoading"
          class="cyber-btn-secondary text-[11px] px-2.5 py-1 text-red-700 border-red-300 bg-red-100/60 hover:bg-red-200/60 font-sans font-bold flex items-center gap-1"
          title="Delete conflicting old records and recreate clean CNAME for this tunnel"
        >
          <RotateCcw class="h-3 w-3 text-red-600" :class="syncLoading ? 'animate-spin' : ''" />
          <span>Force Overwrite Now</span>
        </button>
        <button @click="message = null" class="text-slate-600 hover:text-slate-800">
          &times;
        </button>
      </div>
    </div>

    <!-- ======================================================== -->
    <!-- TAB 1: OVERVIEW & DIAGNOSTICS                            -->
    <!-- ======================================================== -->
    <div v-if="activeTab === 'overview'" class="space-y-5">
      <!-- Quick Telemetry Row (Shown when standalone page) -->
      <div v-if="!embedded" class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <!-- Tunnel Status -->
        <div class="cyber-card p-4 space-y-2">
          <div class="flex items-center justify-between text-xs text-slate-600">
            <span class="uppercase tracking-wider font-semibold text-[10px]">Tunnel Identity</span>
            <Globe class="h-4 w-4 text-cyan-600" />
          </div>
          <div class="font-mono text-sm font-bold text-slate-900 truncate" :title="tunnelId">
            {{ tunnelId || 'Loading…' }}
          </div>
          <div class="flex items-center gap-2 text-[11px] text-slate-600">
            <span class="rounded bg-slate-100 px-1.5 py-0.5 text-slate-700 font-mono">{{ protocol }}</span>
            <span
              class="flex items-center gap-1 font-semibold"
              :class="tunnelStatus?.running ? 'text-emerald-600' : 'text-red-600'"
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
          <div class="flex items-center justify-between text-xs text-slate-600">
            <span class="uppercase tracking-wider font-semibold text-[10px]">Total Routed Requests</span>
            <Activity class="h-4 w-4 text-emerald-600" />
          </div>
          <div class="text-2xl font-bold font-mono text-emerald-600">
            {{ tunnelStatus?.running ? (metrics?.total_requests?.toLocaleString() || '0') : '0' }}
          </div>
          <p class="text-[11px] text-slate-500">Cumulative ingress proxy traffic</p>
        </div>

        <!-- Active Connections -->
        <div class="cyber-card p-4 space-y-2">
          <div class="flex items-center justify-between text-xs text-slate-600">
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
          <div class="flex items-center justify-between text-xs text-slate-600">
            <span class="uppercase tracking-wider font-semibold text-[10px]">Public Hostnames</span>
            <Layers class="h-4 w-4 text-amber-600" />
          </div>
          <div class="text-2xl font-bold font-mono text-amber-700">
            {{ rules.length }}
          </div>
          <p class="text-[11px] text-slate-500">Active ingress mappings</p>
        </div>
      </div>

      <!-- Compact Live Traffic Row (When embedded in Remote Access) -->
      <div v-else class="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <div class="cyber-card p-4 space-y-2">
          <div class="flex items-center justify-between text-xs text-slate-600">
            <span class="uppercase tracking-wider font-semibold text-[10px]">Total Routed Requests</span>
            <Activity class="h-4 w-4 text-emerald-600" />
          </div>
          <div class="text-2xl font-bold font-mono text-emerald-600">
            {{ tunnelStatus?.running ? (metrics?.total_requests?.toLocaleString() || '0') : '0' }}
          </div>
          <p class="text-[11px] text-slate-500">Cumulative ingress proxy traffic</p>
        </div>

        <div class="cyber-card p-4 space-y-2">
          <div class="flex items-center justify-between text-xs text-slate-600">
            <span class="uppercase tracking-wider font-semibold text-[10px]">Active Edge Conns</span>
            <Network class="h-4 w-4 text-violet-400" />
          </div>
          <div class="text-2xl font-bold font-mono text-violet-300">
            {{ tunnelStatus?.running ? (metrics?.active_connections ?? 0) : 0 }}
          </div>
          <p class="text-[11px] text-slate-500">QUIC / HTTP2 streams to Cloudflare edge</p>
        </div>
      </div>

      <!-- Cloudflare Connector Control & Token -->
      <section class="cyber-card space-y-3">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 class="font-semibold text-slate-900">Cloudflare connector</h2>
            <p class="text-sm text-slate-600">{{ tunnelStatus?.running ? 'Running' : 'Stopped' }} · Independent of Tailscale and WireGuard.</p>
          </div>
          <button class="cyber-btn" :disabled="saving" @click="controlProvider(tunnelStatus?.running ? 'stop' : 'start')">
            {{ saving ? 'Working…' : tunnelStatus?.running ? 'Disable Cloudflare' : 'Enable Cloudflare' }}
          </button>
        </div>
        <p class="text-sm text-slate-600">
          Use a tunnel connector token for a remotely managed tunnel, or keep a local YAML configuration and credentials file. Token-based tunnels use public-hostname rules managed in Cloudflare. The local ingress editor applies to locally managed tunnels. The Cloudflare API token in Tab 4 is a separate credential for DNS and account operations.
        </p>
        <label class="block text-sm space-y-1">
          <span>Tunnel connector token</span>
          <input
            v-model="connectorToken"
            type="password"
            autocomplete="off"
            class="cyber-input w-full"
            :placeholder="connectorPresent ? 'Token saved — enter a replacement' : 'Paste tunnel connector token'"
          />
        </label>
        <div class="flex flex-wrap items-center gap-2">
          <button class="cyber-btn-secondary text-xs" :disabled="saving || !connectorToken.trim()" @click="saveConnector()">Save connector token</button>
          <button v-if="connectorPresent" class="cyber-btn-secondary text-xs" :disabled="saving" @click="saveConnector(true)">Use local YAML credentials</button>
          <span v-if="connectorPresent" class="inline-flex items-center gap-1 text-xs text-emerald-600 font-medium ml-1">✓ Connector token active</span>
        </div>
      </section>

      <!-- Cloudflare Setup & Credentials Guide -->
      <ProviderSetupGuide provider="cloudflare" :collapsible="true" :default-open="!connectorPresent" />

      <!-- Diagnostic Alert Banner when Tunnel is Degraded -->
      <div
        v-if="tunnelStatus && !tunnelStatus.running && !['stopped', 'created', 'not installed'].includes(tunnelStatus.status)"
        class="rounded-lg border border-red-900/60 bg-red-50/40 p-4 text-xs text-red-200 flex flex-col sm:flex-row sm:items-start justify-between gap-3"
      >
        <div class="flex items-start gap-2.5">
          <AlertCircle class="h-4 w-4 text-red-600 mt-0.5 shrink-0" />
          <div class="space-y-1.5">
            <p class="font-semibold text-red-700">
              Cloudflare Tunnel Daemon is {{ tunnelStatus.status || 'degraded' }}
            </p>
            <p v-if="tunnelStatus.last_error" class="font-mono text-[11px] text-red-700/90 bg-red-50/70 p-2.5 rounded border border-red-900/40 whitespace-pre-wrap leading-relaxed">
              {{ tunnelStatus.last_error }}
            </p>
            <p v-else class="text-slate-600 text-[11px]">
              The container is not active. Check credentials in /etc/cloudflared/credentials.json or restart the tunnel daemon.
            </p>
          </div>
        </div>
        <button
          @click="restartTunnel"
          :disabled="saving"
          class="cyber-btn text-xs flex items-center gap-1.5 py-1 px-3 border-red-800 bg-red-50/60 hover:bg-red-100/70 text-red-200 shrink-0 self-start sm:self-auto"
        >
          <RotateCcw class="h-3 w-3" :class="saving ? 'animate-spin' : ''" />
          <span>Restart Daemon</span>
        </button>
      </div>

      <!-- HTTP Status Code Breakdown -->
      <div v-if="metrics?.response_codes && Object.keys(metrics.response_codes).length" class="cyber-card p-4 space-y-3">
        <h3 class="text-xs font-bold uppercase tracking-wider text-slate-600">Edge Response Codes</h3>
        <div class="grid grid-cols-2 sm:grid-cols-4 md:grid-cols-6 gap-3">
          <div
            v-for="(count, code) in metrics.response_codes"
            :key="code"
            class="rounded border border-slate-200 bg-slate-50 p-2.5 text-center font-mono"
          >
            <div
              class="text-xs font-bold"
              :class="String(code).startsWith('2') ? 'text-emerald-600' : String(code).startsWith('4') ? 'text-amber-600' : 'text-red-600'"
            >
              HTTP {{ code }}
            </div>
            <div class="text-sm font-bold text-slate-800 mt-1">{{ count }}</div>
          </div>
        </div>
      </div>

      <!-- Ingress Service Health & Latency -->
      <div class="cyber-card p-5 space-y-4">
        <div class="flex items-center justify-between border-b border-slate-200 pb-3">
          <div class="flex items-center gap-2">
            <Activity class="h-4 w-4 text-cyan-600" />
            <h3 class="text-sm font-bold text-slate-900">Live Ingress Target Health & Latency</h3>
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

        <div v-else class="divide-y divide-slate-200/80">
          <div
            v-for="h in health"
            :key="h.hostname"
            class="flex items-center justify-between py-2.5 text-xs font-mono"
          >
            <div>
              <div class="font-bold text-slate-800 flex items-center gap-2">
                <span>{{ h.hostname }}</span>
                <a
                  :href="`https://${h.hostname}`"
                  target="_blank"
                  rel="noopener"
                  class="text-slate-500 hover:text-cyan-700"
                >
                  <ExternalLink class="h-3 w-3" />
                </a>
              </div>
              <div class="text-[11px] text-slate-500 flex items-center gap-2">
                <span>{{ h.service }}</span>
                <router-link
                  v-if="extractTargetHost(h.service)"
                  :to="'/containers?name=' + encodeURIComponent(extractTargetHost(h.service))"
                  class="text-cyan-600 hover:underline text-[10px]"
                  :title="`Inspect container '${extractTargetHost(h.service)}'`"
                >
                  (inspect container)
                </router-link>
              </div>
            </div>

            <div class="flex items-center gap-3">
              <span class="text-slate-600">{{ h.latency }}</span>
              <span
                class="rounded border px-2 py-0.5 text-[10px] uppercase font-semibold"
                :class="h.status === 'up' ? 'border-emerald-500/40 bg-emerald-500/10 text-emerald-600' : 'border-red-500/40 bg-red-500/10 text-red-600'"
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
      <div v-if="connectorPresent" class="rounded-xl border border-emerald-500/30 bg-emerald-50/40 p-3.5 flex items-start gap-2.5 text-xs text-emerald-800">
        <CheckCircle2 class="h-4 w-4 text-emerald-600 shrink-0 mt-0.5" />
        <div>
          <span class="font-bold">Connector token active:</span>
          Your tunnel connector token is active. Cloudflare routes incoming traffic to your public hostnames using remote configurations defined in the Cloudflare Zero Trust dashboard. Local YAML rules below apply when running a locally managed credentials file tunnel.
        </div>
      </div>
      <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-200 pb-3">
        <div>
          <h2 class="text-base font-bold text-slate-900 flex items-center gap-2">
            <Layers class="h-4 w-4 text-cyan-600" />
            <span>Cloudflare Ingress Rules</span>
          </h2>
          <p class="text-xs text-slate-600">
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
            @click="syncAllDnsRoutes(false)"
            :disabled="syncLoading"
            class="cyber-btn-secondary text-xs flex items-center gap-1.5 text-cyan-700 border-cyan-800 bg-cyan-50/30 hover:bg-cyan-100/50"
            title="Automatically create or verify DNS CNAME records in Cloudflare for all hostnames above"
          >
            <RefreshCw class="h-3.5 w-3.5" :class="syncLoading ? 'animate-spin' : ''" />
            <span>{{ syncLoading ? 'Syncing DNS…' : 'Sync to Cloudflare DNS' }}</span>
          </button>
          <button
            @click="syncAllDnsRoutes(true)"
            :disabled="syncLoading"
            class="cyber-btn-secondary text-xs flex items-center gap-1.5 text-amber-700 border-amber-300 bg-amber-50/40 hover:bg-amber-100/60"
            title="Force overwrite and delete any conflicting old DNS records in Cloudflare"
          >
            <RotateCcw class="h-3.5 w-3.5 text-amber-600" :class="syncLoading ? 'animate-spin' : ''" />
            <span>Force Overwrite DNS</span>
          </button>
          <button
            @click="save(true)"
            :disabled="saving"
            class="cyber-btn text-xs flex items-center gap-1.5 text-emerald-700 border-emerald-600 bg-emerald-50/40 hover:bg-emerald-100/50 font-medium"
            title="Save config and restart tunnel container so changes are active immediately"
          >
            <RotateCcw class="h-3.5 w-3.5" :class="saving ? 'animate-spin' : ''" />
            <span>{{ saving ? 'Saving & Applying...' : 'Save & Apply to Tunnel' }}</span>
          </button>
          <button
            @click="save(false)"
            :disabled="saving"
            class="cyber-btn-secondary text-xs flex items-center gap-1.5 text-slate-600"
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
          class="w-full rounded-xl border border-slate-200 bg-slate-50 p-4 font-mono text-xs text-slate-800 focus:border-cyan-500 focus:outline-none leading-relaxed"
        ></textarea>
      </div>

      <!-- Visual Rules Cards View -->
      <div v-else class="space-y-3">
        <!-- Security & Cloudflare Access Advice -->
        <div class="rounded-xl border border-cyan-800/40 bg-slate-50 p-4 flex flex-col sm:flex-row items-start gap-3.5">
          <ShieldCheck class="h-5 w-5 text-cyan-600 mt-0.5 flex-shrink-0" />
          <div class="space-y-1 text-xs">
            <div class="font-bold text-slate-800">Exposing Admin Panels & Security Hardening</div>
            <p class="text-slate-600 leading-relaxed">
              If you expose the DockTun management dashboard publicly, we strongly recommend placing it behind
              <strong class="text-cyan-700">Cloudflare Zero Trust (Access)</strong> in your Cloudflare dashboard (Applications &rarr; Add an Application).
              Access requires One-Time PIN email verification or Google/GitHub SSO at the Cloudflare edge before any traffic reaches your server.
              Alternatively, you can safely <strong class="text-amber-700">delete the DockTun rule below</strong> to keep DockTun internal-only and access it via your local network (<code class="text-slate-700 font-mono">http://&lt;lan-ip&gt;:8080</code> or <code class="text-slate-700 font-mono">http://docktun.local</code>).
            </p>
          </div>
        </div>
        <div
          v-for="(rule, i) in rules"
          :key="i"
          class="cyber-card p-4 flex flex-col md:flex-row md:items-center justify-between gap-4"
          :class="isDocktunRule(rule) ? 'border-cyan-500/30 bg-slate-50/90' : ''"
        >
          <div class="flex-1 grid grid-cols-1 md:grid-cols-2 gap-4">
            <!-- Hostname Input -->
            <div>
              <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">
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
                  class="rounded-lg p-2 text-slate-600 hover:text-cyan-700 hover:bg-slate-100 transition"
                  title="Open site in new tab"
                >
                  <ExternalLink class="h-4 w-4" />
                </a>
              </div>
            </div>

            <!-- Service Target Input -->
            <div>
              <div class="flex items-center justify-between mb-1">
                <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-600">
                  Local Service Target
                </label>
                <router-link
                  v-if="extractTargetHost(rule.service)"
                  :to="'/containers?name=' + encodeURIComponent(extractTargetHost(rule.service))"
                  class="text-[10px] text-cyan-600 hover:underline font-mono flex items-center gap-1"
                  :title="`Inspect container '${extractTargetHost(rule.service)}'`"
                >
                  <Server class="w-3 h-3 text-cyan-600" />
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
            <div class="col-span-full flex flex-wrap items-center justify-between text-xs text-slate-600 pt-1 gap-2">
              <div class="flex flex-wrap items-center gap-3">
                <label class="flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    v-model="rule.noTLSVerify"
                    class="rounded bg-slate-50"
                  />
                  <span class="text-[11px] font-mono">noTLSVerify (Disable TLS origin check)</span>
                </label>

                <!-- Test Origin Button -->
                <button
                  v-if="!rule.service.startsWith('http_status:')"
                  type="button"
                  @click="probeOriginRule(rule)"
                  :disabled="probingHost === rule.hostname"
                  class="cyber-btn-secondary text-[11px] font-mono py-0.5 px-2 flex items-center gap-1.5 hover:text-cyan-700"
                  title="Test HTTP connectivity to origin container"
                >
                  <RotateCcw v-if="probingHost === rule.hostname" class="h-3 w-3 animate-spin text-cyan-600" />
                  <Activity v-else class="h-3 w-3 text-cyan-600" />
                  <span>{{ probingHost === rule.hostname ? 'Testing...' : 'Test Origin' }}</span>
                </button>

                <!-- Probe feedback badge -->
                <span
                  v-if="probeResults[rule.hostname]"
                  class="rounded px-2 py-0.5 text-[10px] font-mono font-bold"
                  :class="probeResults[rule.hostname].reachable ? 'bg-emerald-50/80 text-emerald-600 border border-emerald-800/50' : 'bg-red-50/80 text-red-600 border border-red-800/50'"
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
                  :class="rule.service.startsWith('http_status:') ? 'bg-amber-50 text-amber-700 border border-amber-800 hover:bg-amber-100/60' : 'bg-white text-slate-600 border border-slate-200 hover:text-slate-800'"
                  :title="rule.service.startsWith('http_status:') ? 'Resume routing to container' : 'Temporarily pause rule with HTTP 503 maintenance mode'"
                >
                  {{ rule.service.startsWith('http_status:') ? '⚠ MAINTENANCE (503) • CLICK TO RESUME' : 'Pause (503 Mode)' }}
                </button>

                <span
                  v-if="isDocktunRule(rule)"
                  class="inline-flex items-center gap-1 rounded bg-cyan-50/60 border border-cyan-800/40 px-2 py-0.5 text-[10px] font-mono text-cyan-700 font-semibold"
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
          <div class="p-2 rounded-lg bg-amber-500/10 border border-amber-500/30 text-amber-600 mt-0.5 shrink-0">
            <Info class="h-5 w-5" />
          </div>
          <div class="space-y-2 text-xs flex-1">
            <div class="flex items-center gap-2">
              <h3 class="font-bold text-slate-900 text-sm">Protocol Architecture: Free Tier Limitations & Workarounds</h3>
              <span class="text-[10px] uppercase font-mono px-2 py-0.5 rounded bg-amber-500/20 text-amber-700 border border-amber-500/30">
                Architectural Note
              </span>
            </div>
            <p class="text-slate-700 leading-relaxed">
              Does arbitrary <strong class="text-slate-900">TCP/UDP</strong> work natively on Cloudflare's Free Tier? 
              <strong class="text-amber-700">No, the command below is a WebSocket encapsulation workaround.</strong>
              Cloudflare edge public proxies listen only on HTTP/HTTPS ports (80/443).
            </p>
            <div class="grid grid-cols-1 md:grid-cols-2 gap-3 pt-1">
              <div class="p-3 rounded-lg bg-slate-50/70 border border-slate-200 space-y-1.5">
                <div class="text-[11px] font-semibold text-cyan-700 flex items-center gap-1.5">
                  <Terminal class="h-3.5 w-3.5" />
                  <span>The `cloudflared access tcp` Workaround</span>
                </div>
                <p class="text-[11px] text-slate-600 leading-normal">
                  Wraps raw TCP into a WebSocket stream over port 443 through Cloudflare edge.
                  <strong class="text-slate-700 block mt-1">Requirement:</strong> The client machine must run <code class="text-cyan-600 font-mono">cloudflared access tcp</code> locally. Excellent for personal SSH, RDP, and DB admin.
                </p>
              </div>

              <div class="p-3 rounded-lg bg-slate-50/70 border border-slate-200 space-y-1.5">
                <div class="text-[11px] font-semibold text-rose-700 flex items-center gap-1.5">
                  <Gamepad2 class="h-3.5 w-3.5" />
                  <span>Gaming & Raw UDP (Minecraft, Rust, Palworld)</span>
                </div>
                <p class="text-[11px] text-slate-600 leading-normal">
                  Standard game clients cannot connect to Cloudflare Free tunnels without client-side software. Raw public TCP/UDP requires <strong class="text-slate-700">Cloudflare Spectrum</strong> (Enterprise/paid).
                </p>
              </div>
            </div>

            <div class="p-3 rounded-lg bg-indigo-50/40 border border-indigo-500/30 text-[11px] text-slate-700 flex flex-col sm:flex-row sm:items-center justify-between gap-2 mt-1">
              <div class="flex items-center gap-2">
                <Gamepad2 class="h-4 w-4 text-indigo-600 shrink-0" />
                <span>
                  <strong>Need public game hosting without player software?</strong>
                  Use <strong class="text-indigo-700">Playit.gg</strong> (Anycast public TCP/UDP) or invite friends to your private <strong class="text-purple-700">Tailscale</strong> network.
                </span>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div class="cyber-card p-5 space-y-4 max-w-3xl">
        <div class="border-b border-slate-200 pb-3">
          <h2 class="text-base font-bold text-slate-900 flex items-center gap-2">
            <Terminal class="h-4 w-4 text-cyan-600" />
            <span>TCP Client Access Helper (SSH, RDP, Databases)</span>
          </h2>
          <p class="text-xs text-slate-600 mt-1">
            To connect to non-HTTP services routed through Cloudflare Access, run the generated
            <code class="text-cyan-700">cloudflared access tcp</code> command on your local client computer (laptop or desktop).
          </p>
        </div>

        <div class="grid gap-4 md:grid-cols-2">
          <div>
            <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">
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
            <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">
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
            <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">
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
          <button @click="generateTcpCommand" class="cyber-btn text-xs text-cyan-700">
            Generate Client Command
          </button>
        </div>

        <!-- Output Code Box -->
        <div v-if="generatedTcpCmd" class="mt-4 rounded-xl border border-cyan-500/30 bg-slate-50 p-4 relative">
          <div class="flex items-center justify-between pb-2 mb-2 border-b border-slate-200 text-[11px] text-slate-600 font-mono">
            <span>Run on your client machine:</span>
            <button
              @click="copyText('tcp', generatedTcpCmd)"
              class="cyber-btn-sm text-xs"
            >
              <Check v-if="copiedKey === 'tcp'" class="h-3 w-3 text-emerald-600 mr-1" />
              <Copy v-else class="h-3 w-3 mr-1" />
              <span>Copy</span>
            </button>
          </div>
          <pre class="font-mono text-xs text-cyan-700 whitespace-pre-wrap leading-relaxed">{{ generatedTcpCmd }}</pre>
        </div>
      </div>
    </div>

    <!-- ======================================================== -->
    <!-- TAB 4: CLOUDFLARE API & REMOTE EDGE                      -->
    <!-- ======================================================== -->
    <div v-if="activeTab === 'api'" class="space-y-5">
      <div class="cyber-card p-5 space-y-4 max-w-3xl">
        <div class="border-b border-slate-200 pb-3">
          <h2 class="text-base font-bold text-slate-900 flex items-center gap-2">
            <KeyRound class="h-4 w-4 text-cyan-600" />
            <span>Cloudflare v4 REST API Integration</span>
          </h2>
          <p class="text-xs text-slate-600 mt-1">
            Connect your Cloudflare API Token to query remote tunnel edge status, active edge connectors, and verify DNS records.
          </p>
        </div>

        <div class="grid gap-4 md:grid-cols-2">
          <div class="col-span-full">
            <div class="flex items-center justify-between mb-1">
              <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-600">
                Cloudflare API Token
              </label>
              <span v-if="cfApiHasToken" class="text-[10px] text-emerald-600 font-mono flex items-center gap-1">
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
            <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">
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
            <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">
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
            class="cyber-btn text-xs text-cyan-700"
          >
            <RefreshCw v-if="cfApiLoading" class="h-3 w-3 animate-spin mr-1" />
            <span>Save & Query Cloudflare API</span>
          </button>
          <button
            v-if="cfApiHasToken || cfApiToken"
            @click="fetchRemoteTunnelInfo"
            :disabled="cfApiLoading"
            class="cyber-btn-secondary text-xs text-slate-700 flex items-center gap-1.5"
          >
            <RefreshCw class="h-3 w-3" :class="cfApiLoading ? 'animate-spin' : ''" />
            <span>Query Remote Status</span>
          </button>
          <span v-if="cfApiSuccess" class="text-xs text-emerald-600 font-medium">Credentials saved!</span>
        </div>

        <div v-if="cfApiError" class="rounded-lg border border-red-500/40 bg-red-50/20 p-3 text-xs text-red-700 font-mono">
          {{ cfApiError }}
        </div>

        <!-- Automated DNS Management Actions Card -->
        <div class="rounded-xl border border-cyan-900/40 bg-cyan-50/20 p-4 space-y-3">
          <div class="flex items-center gap-2">
            <Zap class="h-4 w-4 text-cyan-600" />
            <h3 class="text-xs font-bold uppercase tracking-wider text-cyan-700">Automated DNS Route Management</h3>
          </div>
          <p class="text-xs text-slate-600">
            DockTun automatically uses your API Token to create or update Cloudflare CNAME records (<code class="text-cyan-700 font-mono">{{ tunnelId || 'tunnel-id' }}.cfargotunnel.com</code>) whenever you expose containers or save ingress routes.
          </p>

          <div class="flex flex-wrap items-center gap-3 pt-1">
            <button
              @click="syncAllDnsRoutes(false)"
              :disabled="syncLoading || (!cfApiToken && !cfApiHasToken)"
              class="cyber-btn text-xs flex items-center gap-1.5 text-cyan-700 border-cyan-600 bg-cyan-50/40 hover:bg-cyan-100/60 disabled:opacity-50"
              title="Verify and update DNS records for all configured ingress routes"
            >
              <RefreshCw class="h-3.5 w-3.5" :class="syncLoading ? 'animate-spin' : ''" />
              <span>{{ syncLoading ? 'Syncing DNS…' : 'Sync All Ingress Routes' }}</span>
            </button>

            <button
              @click="syncAllDnsRoutes(true)"
              :disabled="syncLoading || (!cfApiToken && !cfApiHasToken)"
              class="cyber-btn-secondary text-xs flex items-center gap-1.5 text-amber-700 border-amber-300 hover:bg-amber-50 disabled:opacity-50"
              title="Purge conflicting old DNS records from other tunnels and recreate fresh CNAMEs"
            >
              <RotateCcw class="h-3.5 w-3.5 text-amber-600" />
              <span>Force Overwrite & Purge Old Records</span>
            </button>
          </div>

          <!-- DNS Record Conflict Inspector & Overwrite Tool -->
          <div class="pt-3 border-t border-cyan-900/30 space-y-3">
            <div class="text-[11px] font-semibold text-slate-800 flex items-center gap-1.5">
              <Search class="h-3.5 w-3.5 text-cyan-600" />
              <span>Subdomain DNS Record Inspector & Conflict Resolver:</span>
            </div>
            <p class="text-[11px] text-slate-600">
              Check live Cloudflare DNS records for any subdomain (e.g. <code class="font-mono text-cyan-700">mediastorm.domain.com</code>). If an old tunnel or previous record still holds the hostname, you can purge it and route to this DockTun tunnel in 1 click.
            </p>

            <div class="flex flex-col sm:flex-row sm:items-center gap-2">
              <input
                v-model="dnsInspectHost"
                type="text"
                placeholder="e.g. mediastorm.yourdomain.com"
                class="cyber-input text-xs font-mono max-w-sm flex-1"
                @keyup.enter="inspectDnsRecord"
              />
              <button
                @click="inspectDnsRecord"
                :disabled="dnsInspectLoading || !dnsInspectHost.trim()"
                class="cyber-btn text-xs flex items-center gap-1.5 text-cyan-700 border-cyan-600 bg-cyan-50/40 hover:bg-cyan-100/60 disabled:opacity-50"
              >
                <Search class="h-3.5 w-3.5" :class="dnsInspectLoading ? 'animate-spin' : ''" />
                <span>{{ dnsInspectLoading ? 'Checking…' : 'Inspect DNS' }}</span>
              </button>
            </div>

            <!-- Inspection Result Card -->
            <div
              v-if="dnsInspectResult"
              class="rounded-lg border p-3 text-xs font-mono space-y-2.5 transition"
              :class="dnsInspectResult.is_configured_correct ? 'border-emerald-200 bg-emerald-50/40' : dnsInspectResult.has_conflict ? 'border-amber-300 bg-amber-50/40' : 'border-slate-200 bg-slate-50'"
            >
              <div class="flex items-center justify-between">
                <span class="font-bold text-slate-800">{{ dnsInspectResult.clean_host }}</span>
                <span
                  class="px-2 py-0.5 rounded text-[10px] font-bold uppercase tracking-wider"
                  :class="dnsInspectResult.is_configured_correct ? 'bg-emerald-100 text-emerald-800 border border-emerald-300' : dnsInspectResult.has_conflict ? 'bg-amber-100 text-amber-800 border border-amber-300' : 'bg-slate-200 text-slate-700'"
                >
                  {{ dnsInspectResult.is_configured_correct ? 'Active on This Tunnel' : dnsInspectResult.has_conflict ? 'Conflict Detected' : 'No Record Found' }}
                </span>
              </div>

              <div class="space-y-1 text-[11px] text-slate-600">
                <div class="flex items-center gap-2">
                  <span class="text-slate-500">Expected DockTun Target:</span>
                  <span class="font-bold text-indigo-700">{{ dnsInspectResult.expected_target }}</span>
                </div>
                <div v-if="dnsInspectResult.conflict_details" class="text-amber-800 font-medium">
                  Status: {{ dnsInspectResult.conflict_details }}
                </div>
              </div>

              <!-- Found Records in Cloudflare -->
              <div v-if="dnsInspectResult.records?.length > 0" class="pt-1 border-t border-slate-200/60 space-y-1">
                <div class="text-[10px] uppercase font-semibold text-slate-500">Records currently in Cloudflare zone:</div>
                <div
                  v-for="rec in dnsInspectResult.records"
                  :key="rec.id"
                  class="flex items-center justify-between p-1.5 rounded bg-white/80 border border-slate-200 text-[11px]"
                >
                  <div>
                    <span class="px-1 py-0.2 rounded font-bold text-[9px] bg-slate-100 text-slate-700 mr-2">{{ rec.type }}</span>
                    <span class="text-slate-900 font-bold mr-1">{{ rec.name }}</span>
                    <span class="text-slate-500">&rarr;</span>
                    <span class="text-slate-700 ml-1 font-mono">{{ rec.content }}</span>
                  </div>
                  <span class="text-[10px] text-slate-400">{{ rec.proxied ? 'Proxied' : 'DNS Only' }}</span>
                </div>
              </div>

              <!-- Overwrite / Replace Action Button -->
              <div class="pt-2 flex items-center justify-between">
                <span class="text-[11px] text-slate-500">
                  {{ dnsInspectResult.is_configured_correct ? 'Everything is correctly routed to this tunnel.' : 'Replace with this DockTun tunnel?' }}
                </span>
                <button
                  v-if="!dnsInspectResult.is_configured_correct"
                  @click="forceReplaceDnsRecord"
                  :disabled="dnsReplaceLoading"
                  class="cyber-btn text-xs flex items-center gap-1.5 text-white bg-indigo-600 hover:bg-indigo-700 px-3 py-1.5 rounded-lg font-sans font-medium"
                >
                  <RotateCcw class="h-3 w-3" :class="dnsReplaceLoading ? 'animate-spin' : ''" />
                  <span>{{ dnsReplaceLoading ? 'Replacing…' : 'Delete Old Record & Route to DockTun' }}</span>
                </button>
              </div>
            </div>
          </div>

          <!-- Wildcard Route Creator -->
          <div class="pt-3 border-t border-cyan-900/30 space-y-2">
            <div class="text-[11px] font-semibold text-slate-700">
              1-Click Wildcard Route (*.yourdomain.com):
            </div>
            <p class="text-[11px] text-slate-600">
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
                class="cyber-btn text-xs flex items-center gap-1.5 text-emerald-700 border-emerald-600 bg-emerald-50/40 hover:bg-emerald-100/60 disabled:opacity-50"
              >
                <CheckCircle2 class="h-3.5 w-3.5" :class="wildcardLoading ? 'animate-spin' : ''" />
                <span>{{ wildcardLoading ? 'Creating…' : 'Create Wildcard DNS (*.domain)' }}</span>
              </button>
            </div>
          </div>
        </div>

        <!-- Remote Tunnel Details from Cloudflare API -->
        <div v-if="cfRemoteTunnel" class="rounded-xl border border-slate-200 bg-slate-50 p-4 space-y-3 font-mono text-xs">
          <div class="flex items-center justify-between border-b border-slate-200 pb-2">
            <span class="text-slate-600 font-bold uppercase text-[10px]">Cloudflare Edge Status:</span>
            <span class="rounded bg-emerald-500/20 text-emerald-700 border border-emerald-500/40 px-2 py-0.5 text-[10px] font-bold">
              {{ cfRemoteTunnel.status || 'HEALTHY' }}
            </span>
          </div>

          <div class="grid grid-cols-1 sm:grid-cols-2 gap-2 text-[11px]">
            <div><span class="text-slate-500">Name:</span> <span class="text-slate-800">{{ cfRemoteTunnel.name }}</span></div>
            <div><span class="text-slate-500">Created:</span> <span class="text-slate-800">{{ cfRemoteTunnel.created_at?.slice(0, 10) }}</span></div>
            <div><span class="text-slate-500">Remote Connections:</span> <span class="text-cyan-600 font-bold">{{ cfRemoteTunnel.connections?.length || 0 }}</span></div>
            <div><span class="text-slate-500">Remote Client:</span> <span class="text-slate-700">{{ cfRemoteTunnel.remote_config ? 'Remote Managed' : 'Local Config Managed' }}</span></div>
          </div>

          <!-- Active Remote Edge Connectors Table -->
          <div v-if="cfRemoteTunnel.connections && cfRemoteTunnel.connections.length" class="space-y-1.5 pt-2">
            <div class="text-[10px] uppercase font-bold text-slate-600">Active Edge Colo Nodes:</div>
            <div
              v-for="(conn, idx) in cfRemoteTunnel.connections"
              :key="idx"
              class="flex items-center justify-between rounded bg-white/60 p-2 text-[11px]"
            >
              <div class="flex items-center gap-2">
                <span class="h-2 w-2 rounded-full bg-emerald-400"></span>
                <span class="text-slate-700 font-bold">{{ conn.colo_name }}</span>
                <span class="text-slate-500 font-mono">({{ conn.origin_ip }})</span>
              </div>
              <span class="text-slate-600 text-[10px]">{{ conn.client_version || 'cloudflared' }}</span>
            </div>
          </div>
        </div>

        <!-- Zero Trust Access Applications Card -->
        <div class="rounded-xl border border-indigo-900/40 bg-indigo-50/20 p-4 space-y-3">
          <div class="flex items-center justify-between">
            <div class="flex items-center gap-2">
              <ShieldCheck class="h-4 w-4 text-indigo-600" />
              <h3 class="text-xs font-bold uppercase tracking-wider text-indigo-700">
                Cloudflare Zero Trust Access (Optional Protection Wall)
              </h3>
            </div>
            <button
              @click="fetchAccessApps"
              :disabled="loadingAccessApps || (!cfApiToken && !cfApiHasToken)"
              class="cyber-btn-secondary text-[11px] py-1 px-2 text-slate-700 flex items-center gap-1"
            >
              <RefreshCw class="h-3 w-3" :class="loadingAccessApps ? 'animate-spin' : ''" />
              <span>Refresh Apps</span>
            </button>
          </div>
          <p class="text-xs text-slate-600">
            Zero Trust Access intercepts requests at Cloudflare edge with an authentication wall (Email PIN / SSO) before allowing traffic to your containers. It is entirely <strong class="text-slate-700">optional per route</strong> and disabled by default.
          </p>

          <!-- Active Zero Trust Applications List -->
          <div v-if="accessApps.length" class="space-y-2 pt-1">
            <div class="text-[10px] uppercase font-bold text-slate-600">Protected Hostnames:</div>
            <div
              v-for="app in accessApps"
              :key="app.id"
              class="flex flex-col sm:flex-row sm:items-center justify-between gap-2 rounded-lg border border-slate-200 bg-white/60 p-2.5 text-xs font-mono"
            >
              <div class="flex items-center gap-2">
                <ShieldCheck class="h-4 w-4 text-emerald-600 shrink-0" />
                <div>
                  <div class="text-slate-800 font-semibold">{{ app.domain }}</div>
                  <div class="text-[10px] text-slate-500">{{ app.name }} &bull; Session: {{ app.session_duration }}</div>
                </div>
              </div>
              <div class="flex items-center gap-2">
                <span class="rounded bg-indigo-500/10 border border-indigo-500/30 text-indigo-700 px-2 py-0.5 text-[10px]">
                  ACTIVE
                </span>
                <button
                  @click="deleteAccessApp(app.id, app.domain)"
                  class="cyber-btn-danger !py-1 !px-2 text-xs"
                  title="Remove Zero Trust protection"
                >
                  <Trash2 class="h-3.5 w-3.5" />
                </button>
              </div>
            </div>
          </div>
          <div v-else-if="!loadingAccessApps" class="text-[11px] text-slate-500 italic py-1">
            No routes currently protected by Zero Trust Access. Enable protection on individual container routes or add one below.
          </div>

          <!-- Add / Protect New Hostname Form -->
          <div class="pt-3 border-t border-indigo-900/30 space-y-3">
            <div class="text-[11px] font-semibold text-slate-700 flex items-center justify-between">
              <span>Protect Route with Zero Trust Wall:</span>
              <span class="text-[10px] text-indigo-600 font-mono">Edge Authentication Policy</span>
            </div>

            <div class="grid grid-cols-1 sm:grid-cols-3 gap-2 text-xs">
              <div>
                <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">Target Hostname</label>
                <input
                  v-model="newAccessAppHost"
                  type="text"
                  placeholder="app.yourdomain.com"
                  class="cyber-input text-xs font-mono w-full"
                />
              </div>

              <div>
                <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">Authentication Method</label>
                <select v-model="newAccessAppAuthType" class="cyber-input text-xs w-full">
                  <option value="email">Email OTP PIN (Specific Users)</option>
                  <option value="domain">Corporate / Domain Whitelist</option>
                  <option value="everyone">Any Authenticated User / Everyone</option>
                  <option value="ip">IP / CIDR Network Whitelist</option>
                  <option value="bypass_ip">IP / CIDR Bypass (No Prompt)</option>
                </select>
              </div>

              <div>
                <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">Session Timeout</label>
                <select v-model="newAccessAppSession" class="cyber-input text-xs w-full">
                  <option value="15m">15 Minutes</option>
                  <option value="1h">1 Hour</option>
                  <option value="24h">24 Hours (Default)</option>
                  <option value="7d">7 Days</option>
                  <option value="30d">30 Days</option>
                </select>
              </div>
            </div>

            <!-- Dynamic Input based on Auth Type -->
            <div v-if="newAccessAppAuthType !== 'everyone'">
              <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">
                <span v-if="newAccessAppAuthType === 'email'">Allowed Email Addresses (Comma-separated)</span>
                <span v-else-if="newAccessAppAuthType === 'domain'">Allowed Email Domains (e.g. mycompany.com)</span>
                <span v-else-if="newAccessAppAuthType === 'ip'">Allowed Client IP / CIDRs</span>
                <span v-else-if="newAccessAppAuthType === 'bypass_ip'">Bypass Client IP / CIDRs (Unauthenticated)</span>
              </label>
              <input
                v-model="newAccessAppEmails"
                type="text"
                :placeholder="
                  newAccessAppAuthType === 'email' ? 'admin@example.com, developer@company.com' :
                  newAccessAppAuthType === 'domain' ? 'company.com, myfamily.org' :
                  newAccessAppAuthType === 'ip' ? '192.168.1.0/24, 73.120.45.6/32' :
                  '73.120.45.6/32, 10.0.0.0/8'
                "
                class="cyber-input text-xs font-mono w-full"
              />
            </div>
            <div v-else class="text-[11px] text-indigo-700/80 bg-indigo-50/30 border border-indigo-900/40 rounded p-2">
              All users who successfully log in using any Identity Provider (IdP) configured in your Cloudflare Zero Trust dashboard will be granted access.
            </div>

            <button
              @click="createAccessApp"
              :disabled="creatingAccessApp || !newAccessAppHost || (!cfApiToken && !cfApiHasToken)"
              class="cyber-btn text-xs flex items-center gap-1.5 text-indigo-700 border-indigo-600 bg-indigo-50/40 hover:bg-indigo-100/60 disabled:opacity-50"
            >
              <ShieldCheck class="h-3.5 w-3.5" :class="creatingAccessApp ? 'animate-spin' : ''" />
              <span>{{ creatingAccessApp ? 'Applying Zero Trust…' : 'Apply Zero Trust Wall' }}</span>
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- ======================================================== -->
    <!-- TAB 5: CONFIG BACKUPS                                    -->
    <!-- ======================================================== -->
    <div v-if="activeTab === 'backups'" class="space-y-4">
      <div class="flex items-center justify-between border-b border-slate-200 pb-3">
        <div>
          <h2 class="text-base font-bold text-slate-900 flex items-center gap-2">
            <Archive class="h-4 w-4 text-cyan-600" />
            <span>Config Snapshots & Backups</span>
          </h2>
          <p class="text-xs text-slate-600">
            DockTun automatically creates timestamped backups before each write to <code class="text-slate-700">config.yml</code>.
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
            class="cyber-btn text-xs flex items-center gap-1.5 text-cyan-700 border-cyan-700 bg-cyan-50/40 hover:bg-cyan-100/50"
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
              <div class="font-bold text-slate-800">Backup #{{ b.id }}</div>
              <div class="text-[11px] text-slate-500">{{ b.created_at }}</div>
            </div>
          </div>

          <div class="flex items-center gap-2">
            <button
              @click="restoreBackup(b)"
              class="cyber-btn-sm text-xs text-cyan-700 hover:border-cyan-500"
            >
              Restore Snapshot
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
