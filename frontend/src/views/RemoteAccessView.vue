<script setup lang="ts">
import ProviderSetupGuide from '../components/ProviderSetupGuide.vue'
import { ref, onMounted, computed, watch } from 'vue'
import { remoteAccessTab, cloudflareCardStatus } from '../lib/tunnel-provider.mjs'
import { useRoute, useRouter } from 'vue-router'
import {
  Globe,
  Radio,
  Shield,
  ShieldCheck,
  CheckCircle2,
  AlertCircle,
  RefreshCw,
  Copy,
  Check,
  Download,
  ExternalLink,
  Play,
  Square,
  RotateCcw,
  Plus,
  Trash2,
  Users,
  Smartphone,
  Laptop,
  Server,
  Info,
  KeyRound,
  Network,
  Cpu,
  Layers,
  ArrowRight,
  QrCode,
  Share2,
  Lock,
  Box
} from 'lucide-vue-next'
import { useApi } from '../composables/useApi'
import ConfigView from './ConfigView.vue'

const { apiFetch } = useApi()
const route = useRoute()
const router = useRouter()

// Active Top-Level Tab: 'cloudflare' | 'tailscale' | 'wireguard'
const activeTab = ref<'overview' | 'cloudflare' | 'tailscale' | 'wireguard'>('overview')

// Overview Data
interface OverviewData {
  cloudflare: {
    status: string
    running: boolean
    tunnel_id: string
    tunnel_name: string
    ingress_count: number
    routes_known?: boolean
    configuration_source?: string
    last_error?: string
    protocol: string
  }
  tailscale: {
    installed: boolean
    running: boolean
    backend_state: string
    tailscale_ips: string[]
    magic_dns: string
    peers_count: number
  }
  wireguard: {
    installed: boolean
    running: boolean
    interface: string
    port: number
    peers_count: number
    server_ip: string
  }
}

const overview = ref<OverviewData | null>(null)
const loadingOverview = ref(false)

// Tailscale State
interface TailscalePeer {
  id: string
  hostname: string
  dns_name: string
  tailscale_ips: string[]
  os: string
  online: boolean
  last_seen?: string
}

interface TailscaleStatus {
  last_error?: string
  installed: boolean
  running: boolean
  version: string
  backend_state: string
  auth_url?: string
  node_key?: string
  hostname: string
  dns_name: string
  tailscale_ips: string[]
  peers: TailscalePeer[]
}

const tailscale = ref<TailscaleStatus | null>(null)
const loadingTailscale = ref(false)
const tailscaleAuthKey = ref('')
const tailscaleHostname = ref('docktun')
const tailscaleConnecting = ref(false)
const tailscaleActionLoading = ref(false)
const tailscaleMsg = ref<{ type: 'success' | 'error'; text: string } | null>(null)

// Tailscale Subnet Router & Exit Node Settings
const tsAdvertiseRoutes = ref(false)
const tsAdvertiseExitNode = ref(false)
const tsSavingOptions = ref(false)

// WireGuard State
interface WireguardPeer {
  name: string
  config_text?: string
  qr_code_data_url?: string
  allowed_ips: string
  latest_handshake?: string
  transfer_rx?: string
  transfer_tx?: string
  endpoint?: string
  public_key?: string
}

interface WireguardStatus {
  installed: boolean
  running: boolean
  interface: string
  port: number
  public_key: string
  server_url: string
  peers: WireguardPeer[]
}

const wireguard = ref<WireguardStatus | null>(null)
const loadingWireguard = ref(false)
const wireguardActionLoading = ref(false)
const selectedWgPeer = ref<WireguardPeer | null>(null)
const showQrModal = ref(false)
const showConfigModal = ref(false)
const showAddPeerModal = ref(false)
const newPeerName = ref('')
const newPeerAllowedIPs = ref<'containers' | 'all'>('containers')
const addingPeer = ref(false)
const copied = ref<Record<string, boolean>>({})

// Container Exposures State
interface ContainerExposure {
  id: string
  name: string
  image: string
  running: boolean
  ports: number[]
  exposure_mode: string
  private_type: string
  private_port: number
  hostname: string
  target: string
  tailscale_url?: string
  wireguard_url?: string
  cloudflare_url?: string
  zero_trust_enabled?: boolean
  zero_trust_emails?: string
  zero_trust_auth_type?: string
  zero_trust_session?: string
}

const containerExposures = ref<ContainerExposure[]>([])
const loadingContainers = ref(false)

const tailscaleContainers = computed(() => {
  return containerExposures.value.filter(
    (c) => c.exposure_mode === 'private' && c.private_type === 'tailscale'
  )
})

const wireguardContainers = computed(() => {
  return containerExposures.value.filter(
    (c) => c.exposure_mode === 'private' && c.private_type === 'wireguard'
  )
})

// Load Overview
async function fetchOverview() {
  loadingOverview.value = true
  try {
    overview.value = await apiFetch<OverviewData>('/api/remote-access/overview')
  } catch (err: any) {
    console.error('Failed to load remote access overview:', err)
  } finally {
    loadingOverview.value = false
  }
}

// Load Container Exposures
async function fetchContainerExposures() {
  loadingContainers.value = true
  try {
    containerExposures.value = await apiFetch<ContainerExposure[]>('/api/remote-access/containers')
  } catch (err: any) {
    console.error('Failed to load container exposures:', err)
  } finally {
    loadingContainers.value = false
  }
}

// Load Tailscale Details
async function fetchTailscale() {
  loadingTailscale.value = true
  try {
    tailscale.value = await apiFetch<TailscaleStatus>('/api/remote-access/tailscale/status')
    if (tailscale.value?.hostname) {
      tailscaleHostname.value = tailscale.value.hostname
    }
  } catch (err: any) {
    console.error('Failed to load Tailscale status:', err)
  } finally {
    loadingTailscale.value = false
  }
}

async function connectTailscale() {
  tailscaleConnecting.value = true
  tailscaleMsg.value = null
  try {
    await apiFetch('/api/remote-access/tailscale/up', {
      method: 'POST',
      body: JSON.stringify({
        auth_key: tailscaleAuthKey.value,
        hostname: tailscaleHostname.value,
      }),
    })
    tailscaleMsg.value = { type: 'success', text: 'Tailscale connected successfully!' }
    tailscaleAuthKey.value = ''
    await fetchTailscale()
    await fetchOverview()
  } catch (err: any) {
    tailscaleMsg.value = { type: 'error', text: err.message || 'Failed to connect Tailscale' }
  } finally {
    tailscaleConnecting.value = false
  }
}

async function saveTailscaleOptions() {
  tsSavingOptions.value = true
  tailscaleMsg.value = null
  try {
    const routes = tsAdvertiseRoutes.value ? '172.18.0.0/16,172.17.0.0/16' : ''
    await apiFetch('/api/remote-access/tailscale/options', {
      method: 'POST',
      body: JSON.stringify({
        advertise_routes: routes,
        advertise_exit_node: tsAdvertiseExitNode.value,
      }),
    })
    tailscaleMsg.value = { type: 'success', text: 'Tailscale routing options updated!' }
  } catch (err: any) {
    tailscaleMsg.value = { type: 'error', text: err.message || 'Failed to update Tailscale options' }
  } finally {
    tsSavingOptions.value = false
  }
}

async function controlTailscale(action: string) {
  tailscaleActionLoading.value = true
  tailscaleMsg.value = null
  try {
    await apiFetch('/api/remote-access/tailscale/control', {
      method: 'POST',
      body: JSON.stringify({ action }),
    })
    await fetchTailscale()
    await fetchOverview()
  } catch (err: any) {
    tailscaleMsg.value = { type: 'error', text: err.message || `Failed to ${action} Tailscale` }
  } finally {
    tailscaleActionLoading.value = false
  }
}

// Load WireGuard Details
async function fetchWireguard() {
  loadingWireguard.value = true
  try {
    wireguard.value = await apiFetch<WireguardStatus>('/api/remote-access/wireguard/status')
  } catch (err: any) {
    console.error('Failed to load WireGuard status:', err)
  } finally {
    loadingWireguard.value = false
  }
}

async function controlWireguard(action: string) {
  wireguardActionLoading.value = true
  try {
    await apiFetch('/api/remote-access/wireguard/control', {
      method: 'POST',
      body: JSON.stringify({ action }),
    })
    await fetchWireguard()
    await fetchOverview()
  } catch (err: any) {
    alert(err.message || `Failed to ${action} WireGuard`)
  } finally {
    wireguardActionLoading.value = false
  }
}

async function handleAddWireguardPeer() {
  if (!newPeerName.value) return
  addingPeer.value = true
  try {
    const newPeer = await apiFetch<WireguardPeer>('/api/remote-access/wireguard/peer', {
      method: 'POST',
      body: JSON.stringify({
        name: newPeerName.value,
        allowed_ips_mode: newPeerAllowedIPs.value === 'all' ? 'all' : 'containers',
      }),
    })
    showAddPeerModal.value = false
    newPeerName.value = ''
    await fetchWireguard()
    await fetchOverview()
    if (newPeer) {
      openPeerQr(newPeer)
    }
  } catch (err: any) {
    alert(err.message || 'Failed to generate WireGuard peer')
  } finally {
    addingPeer.value = false
  }
}

async function deleteWireguardPeer(name: string) {
  if (!confirm(`Are you sure you want to delete peer ${name}?`)) return
  try {
    await apiFetch(`/api/remote-access/wireguard/peer/${encodeURIComponent(name)}`, {
      method: 'DELETE',
    })
    await fetchWireguard()
    await fetchOverview()
  } catch (err: any) {
    alert(err.message || 'Failed to delete WireGuard peer')
  }
}

function openPeerQr(peer: WireguardPeer) {
  selectedWgPeer.value = peer
  showQrModal.value = true
}

function openPeerConfig(peer: WireguardPeer) {
  selectedWgPeer.value = peer
  showConfigModal.value = true
}

function downloadPeerConfig(peer: WireguardPeer) {
  if (!peer.config_text) return
  const blob = new Blob([peer.config_text], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${peer.name.toLowerCase().replace(/\s+/g, '_')}.conf`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

// Copy to Clipboard Helper
function copyText(key: string, text: string) {
  navigator.clipboard.writeText(text)
  copied.value[key] = true
  setTimeout(() => {
    delete copied.value[key]
  }, 2000)
}

// All app exposure editing lives in the container inspector.
function openContainerAccess(container: ContainerExposure | null, _mode: string, _provider: string) {
  router.push({ path: '/containers', query: container ? { name: container.name, tab: 'access' } : {} })
}



// Route Query Handling
onMounted(async () => {
  const qTab = route.query.tab as string
  if (qTab && ['cloudflare', 'tailscale', 'wireguard'].includes(qTab)) {
    activeTab.value = qTab as any
  }
  activeTab.value = remoteAccessTab(qTab, 'none')
  await fetchOverview()
  await fetchContainerExposures()
  if (activeTab.value === 'tailscale') await fetchTailscale()
  if (activeTab.value === 'wireguard') await fetchWireguard()
})

watch(
  () => route.query.tab,
  (newTab) => {
    if (newTab && ['cloudflare', 'tailscale', 'wireguard'].includes(newTab as string)) {
      if (activeTab.value !== newTab) {
        switchTab(newTab as any)
      }
    }
  }
)

function switchTab(tab: 'cloudflare' | 'tailscale' | 'wireguard') {
  activeTab.value = tab
  router.replace({ query: { ...route.query, tab } })
  if (tab === 'tailscale' && !tailscale.value) fetchTailscale()
  if (tab === 'wireguard' && !wireguard.value) fetchWireguard()
  fetchContainerExposures()
}
</script>

<template>
  <div class="space-y-6">
    <!-- Header -->
    <div class="flex flex-col md:flex-row md:items-center justify-between gap-4">
      <div class="flex items-center gap-3">
        <div class="rounded-xl border border-indigo-500/20 bg-indigo-500/10 p-2.5 text-indigo-600">
          <Globe class="h-6 w-6" />
        </div>
        <div>
          <h1 class="text-xl font-bold tracking-tight text-slate-900 flex items-center gap-2">
            Exposure providers
          </h1>
          <p class="text-xs text-slate-600">
            Unified management for Cloudflare edge tunnels, Tailscale zero-trust mesh, and WireGuard VPN
          </p>
        </div>
      </div>

      <div class="flex items-center gap-2">
        <button
          @click="fetchOverview(); fetchContainerExposures(); if (activeTab === 'tailscale') fetchTailscale(); if (activeTab === 'wireguard') fetchWireguard();"
          :disabled="loadingOverview"
          class="cyber-btn-secondary text-xs flex items-center gap-1.5"
          title="Refresh Exposure providers status"
        >
          <RefreshCw class="h-3.5 w-3.5" :class="loadingOverview ? 'animate-spin' : ''" />
          <span>Refresh</span>
        </button>
      </div>
    </div>

    <!-- Overview Status Cards (3 Pillars: Cloudflare, Tailscale, WireGuard - Interactive Primary Tabs) -->
    <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
      <!-- 1. Cloudflare Tunnel Card -->
      <button
        @click="switchTab('cloudflare')"
        class="cyber-card p-4 text-left transition relative flex flex-col justify-between"
        :class="activeTab === 'cloudflare' ? 'ring-2 ring-orange-500/50 border-orange-500/70 bg-white/90 shadow-md' : 'bg-slate-50/70 hover:border-slate-300'"
      >
        <div class="flex items-start justify-between w-full mb-3">
          <div class="flex items-center gap-2">
            <div class="p-1.5 rounded-lg bg-orange-500/10 border border-orange-500/30 text-orange-600">
              <Globe class="h-4 w-4" />
            </div>
            <span class="text-xs font-semibold text-slate-800">Cloudflare Tunnel</span>
          </div>
          <span
            v-if="overview?.cloudflare?.running"
            class="inline-flex items-center gap-1 text-[10px] font-semibold px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-600 border border-emerald-500/30"
          >
            <span class="h-1.5 w-1.5 rounded-full bg-emerald-400 animate-pulse"></span>
            CONNECTOR RUNNING
          </span>
          <span
            v-else
            class="inline-flex items-center gap-1 text-[10px] font-semibold px-2 py-0.5 rounded-full bg-slate-100 text-slate-600 border border-slate-300"
          >
            {{ cloudflareCardStatus(overview?.cloudflare) }}
          </span>
        </div>
        <div>
          <div class="text-xl font-bold text-slate-900 font-mono">
            {{ overview?.cloudflare?.routes_known === false ? '—' : overview?.cloudflare?.ingress_count ?? '—' }} <span class="text-xs text-slate-600 font-normal">public routes</span>
          </div>
          <div class="text-[11px] text-slate-500 truncate mt-0.5" :title="overview?.cloudflare?.tunnel_id">
            {{ overview?.cloudflare?.status === 'disabled' ? 'Choose Cloudflare when you need a public hostname.' : overview?.cloudflare?.tunnel_id ? 'Tunnel: ' + overview.cloudflare.tunnel_id.substring(0, 8) + '…' : 'Tunnel identity unavailable' }}
          </div>
          <p v-if="overview?.cloudflare?.configuration_source === 'cloudflare'" class="text-[11px] text-slate-500 mt-1">Cloudflare-managed routes</p>
          <p v-if="overview?.cloudflare?.last_error" class="text-[11px] text-amber-700 mt-1">{{ overview.cloudflare.last_error }}</p>
        </div>
        <div v-if="activeTab === 'cloudflare'" class="mt-3 h-0.5 w-full bg-orange-500 rounded-full"></div>
      </button>

      <!-- 2. Tailscale Card -->
      <button
        @click="switchTab('tailscale')"
        class="cyber-card p-4 text-left transition relative flex flex-col justify-between"
        :class="activeTab === 'tailscale' ? 'ring-2 ring-purple-500/50 border-purple-500/70 bg-white/90 shadow-md' : 'bg-slate-50/70 hover:border-slate-300'"
      >
        <div class="flex items-start justify-between w-full mb-3">
          <div class="flex items-center gap-2">
            <div class="p-1.5 rounded-lg bg-purple-500/10 border border-purple-500/30 text-purple-600">
              <Radio class="h-4 w-4" />
            </div>
            <span class="text-xs font-semibold text-slate-800">Tailscale Mesh</span>
          </div>
          <span class="text-xs text-purple-700">{{ !overview ? 'Loading' : overview.tailscale.running ? 'Running' : overview.tailscale.installed ? 'Stopped' : 'Not installed' }}</span>
        </div>
        <p class="text-xs text-slate-600">Choose Tailscale in each app’s Access tab. Each app gets a separate device; the shared companion can stay stopped.</p>
        <div v-if="activeTab === 'tailscale'" class="mt-3 h-0.5 w-full bg-purple-500 rounded-full"></div>
      </button>

      <!-- 3. WireGuard Card -->
      <button
        @click="switchTab('wireguard')"
        class="cyber-card p-4 text-left transition relative flex flex-col justify-between"
        :class="activeTab === 'wireguard' ? 'ring-2 ring-emerald-500/50 border-emerald-500/70 bg-white/90 shadow-md' : 'bg-slate-50/70 hover:border-slate-300'"
      >
        <div class="flex items-start justify-between w-full mb-3">
          <div class="flex items-center gap-2">
            <div class="p-1.5 rounded-lg bg-emerald-500/10 border border-emerald-500/30 text-emerald-600">
              <Shield class="h-4 w-4" />
            </div>
            <span class="text-xs font-semibold text-slate-800">WireGuard Peer VPN</span>
          </div>
          <span
            v-if="overview?.wireguard?.running"
            class="inline-flex items-center gap-1 text-[10px] font-semibold px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-600 border border-emerald-500/30"
          >
            <span class="h-1.5 w-1.5 rounded-full bg-emerald-400 animate-pulse"></span>
            ACTIVE
          </span>
          <span
            v-else
            class="inline-flex items-center gap-1 text-[10px] font-semibold px-2 py-0.5 rounded-full bg-slate-100 text-slate-600 border border-slate-300"
          >
            {{ overview?.wireguard?.installed ? 'STOPPED' : 'Not installed' }}
          </span>
        </div>
        <div>
          <div class="text-base font-bold text-slate-900 font-mono">
            Port {{ overview?.wireguard?.port || 51820 }}/UDP
          </div>
          <div class="text-[11px] text-slate-500 truncate mt-0.5">
            {{ overview?.wireguard?.peers_count ?? 0 }} client peers configured
          </div>
        </div>
        <div v-if="activeTab === 'wireguard'" class="mt-3 h-0.5 w-full bg-emerald-500 rounded-full"></div>
      </button>
    </div>

    <!-- ========================================================================= -->
    <!-- TAB 1: CLOUDFLARE TUNNEL (Full Integrated Config & Rules Engine) -->
    <!-- ========================================================================= -->
    <section v-if="activeTab === 'overview'" class="cyber-panel p-5 space-y-2"><h2 class="font-semibold">Remote access is optional</h2><p class="text-sm text-slate-600">Configure a provider here when you need it. Choose which apps to expose in Containers → Access. All three methods are optional; you can use DockTun only for Docker management.</p></section>
    <div v-if="activeTab === 'cloudflare'" class="space-y-6">
      <ConfigView :embedded="true" />
    </div>

    <!-- ========================================================================= -->
    <!-- TAB 2: TAILSCALE ZERO-TRUST MESH VPN -->
    <!-- ========================================================================= -->
    <div v-show="activeTab === 'tailscale'" class="space-y-6">
      <ProviderSetupGuide provider="tailscale" />
      <details class="space-y-3">
        <summary class="cursor-pointer text-xs text-slate-600">Advanced: legacy shared device and routing</summary>
      <!-- Tailscale Header Card -->
      <div class="cyber-card space-y-4">
        <div class="flex flex-col md:flex-row md:items-center justify-between gap-4">
          <div class="flex items-center gap-3">
            <div class="rounded-xl border border-purple-500/30 bg-purple-50/40 p-3 text-purple-600">
              <Radio class="h-6 w-6" />
            </div>
            <div>
              <h2 class="text-base font-bold text-slate-900 flex items-center gap-2">
                Tailscale Mesh Network
                <span
                  v-if="tailscale?.backend_state === 'Running'"
                  class="text-[10px] font-semibold px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-600 border border-emerald-500/30"
                >
                  ONLINE
                </span>
                <span
                  v-else-if="tailscale?.backend_state === 'NeedsLogin'"
                  class="text-[10px] font-semibold px-2 py-0.5 rounded-full bg-amber-500/10 text-amber-600 border border-amber-500/30 animate-pulse"
                >
                  AUTH REQUIRED
                </span>
                <span
                  v-else
                  class="text-[10px] font-semibold px-2 py-0.5 rounded-full bg-slate-100 text-slate-600 border border-slate-300"
                >
                  {{ tailscale?.installed ? 'STOPPED' : 'Not installed' }}
                </span>
              </h2>
              <p class="text-xs text-slate-600">
                Encrypted WireGuard-based zero-trust mesh connecting your containers and devices anywhere without open ports.
              </p>
            </div>
          </div>

          <div class="flex items-center gap-2">
            <button
              v-if="!tailscale?.running"
              @click="controlTailscale('start')"
              :disabled="tailscaleActionLoading"
              class="cyber-btn flex items-center gap-1.5 text-xs bg-emerald-600 hover:bg-emerald-500"
            >
              <Play class="h-3.5 w-3.5" />
              <span>Start Tailscale</span>
            </button>
            <button
              v-else
              @click="controlTailscale('stop')"
              :disabled="tailscaleActionLoading"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-rose-700 hover:border-rose-500/50"
            >
              <Square class="h-3.5 w-3.5" />
              <span>Stop Container</span>
            </button>
            <button
              v-if="tailscale?.running"
              @click="controlTailscale('restart')"
              :disabled="tailscaleActionLoading"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-slate-700"
              title="Restart container"
            >
              <RotateCcw class="h-3.5 w-3.5" />
            </button>
          </div>
        </div>

        <!-- Feedback Alert -->
        <div
          v-if="tailscaleMsg"
          class="p-3 rounded-lg text-xs font-mono flex items-center gap-2"
          :class="tailscaleMsg.type === 'success' ? 'bg-emerald-50/60 border border-emerald-500/40 text-emerald-700' : 'bg-rose-50/60 border border-rose-500/40 text-rose-700'"
        >
          <CheckCircle2 v-if="tailscaleMsg.type === 'success'" class="h-4 w-4 shrink-0" />
          <AlertCircle v-else class="h-4 w-4 shrink-0" />
          <span>{{ tailscaleMsg.text }}</span>
        </div>

        <p v-if="tailscale?.last_error" class="p-3 rounded-lg text-xs text-amber-700 bg-amber-50">
          {{ tailscale.last_error }}
        </p>

        <!-- Needs Login Auth URL Banner -->
        <div
          v-if="tailscale?.backend_state === 'NeedsLogin' && tailscale.auth_url"
          class="p-4 rounded-xl border border-amber-500/40 bg-amber-50/40 space-y-3"
        >
          <div class="flex items-center gap-2 text-amber-700 font-semibold text-xs">
            <KeyRound class="h-4 w-4 text-amber-600" /> Complete Node Authentication
          </div>
          <p class="text-xs text-slate-700">
            Click the button below to authorize this DockTun node into your Tailscale tailnet:
          </p>
          <div class="flex items-center gap-3">
            <a
              :href="tailscale.auth_url"
              target="_blank"
              rel="noopener"
              class="cyber-btn inline-flex items-center gap-1.5 text-xs bg-amber-600 hover:bg-amber-500"
            >
              <ExternalLink class="h-3.5 w-3.5" />
              <span>Open Tailscale Login Page</span>
            </a>
            <button
              @click="fetchTailscale()"
              class="cyber-btn-secondary text-xs flex items-center gap-1.5"
            >
              <RefreshCw class="h-3.5 w-3.5" />
              <span>Check Status</span>
            </button>
          </div>
        </div>

        <!-- Live Node Identity Specifications -->
        <div v-if="tailscale?.running" class="grid grid-cols-1 md:grid-cols-3 gap-3 p-4 rounded-xl bg-slate-50/80 border border-slate-200 text-xs font-mono">
          <div>
            <span class="text-slate-500 block text-[10px] uppercase font-semibold">Tailscale IPv4</span>
            <div class="flex items-center gap-2 mt-1">
              <span class="text-purple-700 font-bold text-sm">{{ tailscale.tailscale_ips?.[0] || 'Pending' }}</span>
              <button
                v-if="tailscale.tailscale_ips?.[0]"
                @click="copyText('ts_ip', tailscale.tailscale_ips[0])"
                class="text-slate-500 hover:text-purple-700 transition"
              >
                <Check v-if="copied['ts_ip']" class="h-3.5 w-3.5 text-emerald-600" />
                <Copy v-else class="h-3.5 w-3.5" />
              </button>
            </div>
          </div>

          <div>
            <span class="text-slate-500 block text-[10px] uppercase font-semibold">MagicDNS Domain</span>
            <span class="text-slate-700 block mt-1 truncate" :title="tailscale.dns_name">
              {{ tailscale.dns_name || 'None' }}
            </span>
          </div>

          <div>
            <span class="text-slate-500 block text-[10px] uppercase font-semibold">Node Hostname</span>
            <span class="text-slate-700 block mt-1">{{ tailscale.hostname || 'docktun' }}</span>
          </div>
        </div>

        <!-- Subnet Routing & Exit Node Toggles -->
        <div v-if="tailscale?.running" class="border-t border-slate-200/80 pt-4 space-y-3">
          <h3 class="text-xs font-semibold text-slate-800 flex items-center gap-2">
            <Network class="h-4 w-4 text-purple-600" /> Subnet Routing & Exit Node Features
          </h3>
          <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
            <div class="p-3.5 rounded-xl border border-slate-200 bg-slate-50/60 flex items-center justify-between">
              <div>
                <div class="text-xs font-medium text-slate-800">Advertise Docker Container Subnets</div>
                <div class="text-[11px] text-slate-600">Routes 172.18.0.0/16 and 172.17.0.0/16 directly to your tailnet</div>
              </div>
              <input
                type="checkbox"
                v-model="tsAdvertiseRoutes"
                @change="saveTailscaleOptions"
                class="h-4 w-4 rounded border-slate-300 bg-white text-purple-600 focus:ring-purple-500"
              />
            </div>

            <div class="p-3.5 rounded-xl border border-slate-200 bg-slate-50/60 flex items-center justify-between">
              <div>
                <div class="text-xs font-medium text-slate-800">Advertise as Exit Node</div>
                <div class="text-[11px] text-slate-600">Allows tailnet devices to route all internet traffic through DockTun</div>
              </div>
              <input
                type="checkbox"
                v-model="tsAdvertiseExitNode"
                @change="saveTailscaleOptions"
                class="h-4 w-4 rounded border-slate-300 bg-white text-purple-600 focus:ring-purple-500"
              />
            </div>
          </div>
        </div>

        <!-- Auth Key Form -->
        <div class="border-t border-slate-200/80 pt-4 space-y-3">
          <h3 class="text-xs font-semibold text-slate-800 flex items-center gap-2">
            <KeyRound class="h-3.5 w-3.5 text-purple-600" /> Connect or Re-authenticate with Auth Key
          </h3>
          <div class="grid grid-cols-1 md:grid-cols-3 gap-3">
            <div class="md:col-span-2">
              <label class="block text-[11px] font-medium text-slate-600 mb-1">Tailscale Auth Key (tskey-auth-...)</label>
              <input
                v-model="tailscaleAuthKey"
                type="password"
                placeholder="tskey-auth-k..."
                class="cyber-input text-xs font-mono"
              />
            </div>
            <div>
              <label class="block text-[11px] font-medium text-slate-600 mb-1">Node Hostname</label>
              <input
                v-model="tailscaleHostname"
                type="text"
                placeholder="docktun"
                class="cyber-input text-xs font-mono"
              />
            </div>
          </div>
          <button
            @click="connectTailscale"
            :disabled="tailscaleConnecting"
            class="cyber-btn flex items-center gap-2 text-xs bg-purple-600 hover:bg-purple-500"
          >
            <RefreshCw v-if="tailscaleConnecting" class="h-3.5 w-3.5 animate-spin" />
            <Radio v-else class="h-3.5 w-3.5" />
            <span>{{ tailscaleConnecting ? 'Connecting…' : 'Authenticate & Join Tailnet' }}</span>
          </button>
        </div>
      </div>

      </details>
      <!-- Container Exposures for Tailscale -->
      <div class="cyber-card space-y-3">
        <div class="flex items-center justify-between">
          <div class="flex items-center gap-2">
            <Box class="h-4 w-4 text-purple-600" />
            <h3 class="text-xs font-semibold text-slate-800">Exposed Containers on Tailscale</h3>
            <span class="text-[10px] px-2 py-0.5 rounded-full bg-purple-500/10 text-purple-700 font-mono">
              {{ tailscaleContainers.length }} routed
            </span>
          </div>
          <button
            @click="openContainerAccess(null, 'private', 'tailscale')"
            class="cyber-btn flex items-center gap-1.5 text-xs bg-purple-600 hover:bg-purple-500 py-1 px-2.5"
          >
            <Plus class="h-3.5 w-3.5" />
            <span>Expose Container</span>
          </button>
        </div>

        <div v-if="tailscaleContainers.length === 0" class="py-8 text-center text-xs text-slate-500 border border-dashed border-slate-200 rounded-xl bg-slate-50/40 space-y-2">
          <Box class="h-6 w-6 text-slate-600 mx-auto" />
          <p>No containers currently routed directly through Tailscale mesh.</p>
          <button
            @click="openContainerAccess(null, 'private', 'tailscale')"
            class="cyber-btn-secondary text-xs text-purple-700 hover:border-purple-500/50 inline-flex items-center gap-1.5"
          >
            <Plus class="h-3 w-3" />
            <span>Expose a Container</span>
          </button>
        </div>

        <div v-else class="overflow-x-auto">
          <table class="w-full text-left text-xs font-mono">
            <thead>
              <tr class="border-b border-slate-200 text-[11px] text-slate-600 font-medium">
                <th class="py-2.5 px-3">Container</th>
                <th class="py-2.5 px-3">Status</th>
                <th class="py-2.5 px-3">Internal Port</th>
                <th class="py-2.5 px-3">Tailscale Access URL</th>
                <th class="py-2.5 px-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-slate-200/60">
              <tr v-for="c in tailscaleContainers" :key="c.id" class="hover:bg-white/50 transition">
                <td class="py-2.5 px-3 font-semibold text-slate-900">
                  {{ c.name }}
                  <span class="block text-[10px] text-slate-500 font-normal truncate max-w-xs">{{ c.image }}</span>
                </td>
                <td class="py-2.5 px-3">
                  <span v-if="c.running" class="inline-flex items-center gap-1 text-[10px] font-semibold text-emerald-600">
                    <span class="h-1.5 w-1.5 rounded-full bg-emerald-400"></span> Running
                  </span>
                  <span v-else class="text-slate-500 text-[10px]">Stopped</span>
                </td>
                <td class="py-2.5 px-3 text-slate-700">
                  {{ c.private_port || (c.ports && c.ports.length ? c.ports[0] : 80) }}
                </td>
                <td class="py-2.5 px-3">
                  <div class="flex items-center gap-2">
                    <span class="text-purple-700 font-bold truncate max-w-xs">{{ c.tailscale_url || 'Connect Tailscale to obtain an address' }}</span>
                    <button
                      v-if="c.tailscale_url"
                      @click="copyText('ts_url_' + c.name, c.tailscale_url)"
                      class="text-slate-500 hover:text-purple-700"
                    >
                      <Check v-if="copied['ts_url_' + c.name]" class="h-3.5 w-3.5 text-emerald-600" />
                      <Copy v-else class="h-3.5 w-3.5" />
                    </button>
                  </div>
                </td>
                <td class="py-2.5 px-3 text-right">
                  <button
                    @click="openContainerAccess(c, 'private', c.private_type)"
                    class="cyber-btn-sm text-cyan-700 border-cyan-500/30 hover:bg-cyan-50/60"
                  >
                    Manage exposure
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <!-- Tailnet Peers Table -->
      <div v-if="tailscale?.running && tailscale.peers && tailscale.peers.length > 0" class="cyber-card space-y-3">
        <h3 class="text-xs font-semibold text-slate-800 flex items-center gap-2">
          <Users class="h-4 w-4 text-purple-600" /> Tailnet Connected Devices ({{ tailscale.peers.length }})
        </h3>
        <div class="overflow-x-auto">
          <table class="w-full text-left text-xs font-mono">
            <thead>
              <tr class="border-b border-slate-200 text-[11px] text-slate-600 font-medium">
                <th class="py-2.5 px-3">Device / Hostname</th>
                <th class="py-2.5 px-3">OS</th>
                <th class="py-2.5 px-3">Tailscale IP</th>
                <th class="py-2.5 px-3">MagicDNS</th>
                <th class="py-2.5 px-3">Status</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-slate-200/60">
              <tr v-for="peer in tailscale.peers" :key="peer.id" class="hover:bg-white/50 transition">
                <td class="py-2.5 px-3 text-slate-900 font-semibold flex items-center gap-2">
                  <Smartphone v-if="peer.os === 'iOS' || peer.os === 'android'" class="h-3.5 w-3.5 text-slate-600" />
                  <Laptop v-else-if="peer.os === 'macOS' || peer.os === 'windows'" class="h-3.5 w-3.5 text-slate-600" />
                  <Server v-else class="h-3.5 w-3.5 text-slate-600" />
                  {{ peer.hostname }}
                </td>
                <td class="py-2.5 px-3 text-slate-600">{{ peer.os }}</td>
                <td class="py-2.5 px-3 text-purple-700 font-bold">{{ peer.tailscale_ips?.[0] }}</td>
                <td class="py-2.5 px-3 text-slate-600 truncate max-w-xs">{{ peer.dns_name }}</td>
                <td class="py-2.5 px-3">
                  <span v-if="peer.online" class="inline-flex items-center gap-1 text-[10px] font-semibold text-emerald-600">
                    <span class="h-1.5 w-1.5 rounded-full bg-emerald-400"></span> Online
                  </span>
                  <span v-else class="text-slate-500 text-[10px]">Offline</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- ========================================================================= -->
    <!-- TAB 3: WIREGUARD PEER-TO-PEER VPN -->
    <!-- ========================================================================= -->
    <div v-show="activeTab === 'wireguard'" class="space-y-6">
      <ProviderSetupGuide provider="wireguard" />
      <!-- WireGuard Header Card -->
      <div class="cyber-card space-y-4">
        <div class="flex flex-col md:flex-row md:items-center justify-between gap-4">
          <div class="flex items-center gap-3">
            <div class="rounded-xl border border-emerald-500/30 bg-emerald-50/40 p-3 text-emerald-600">
              <Shield class="h-6 w-6" />
            </div>
            <div>
              <h2 class="text-base font-bold text-slate-900 flex items-center gap-2">
                WireGuard VPN Interface
                <span
                  v-if="wireguard?.running"
                  class="text-[10px] font-semibold px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-600 border border-emerald-500/30"
                >
                  ACTIVE
                </span>
                <span
                  v-else
                  class="text-[10px] font-semibold px-2 py-0.5 rounded-full bg-slate-100 text-slate-600 border border-slate-300"
                >
                  {{ wireguard?.installed ? 'STOPPED' : 'Not installed' }}
                </span>
              </h2>
              <p class="text-xs text-slate-600">
                Configure an existing WireGuard VPN here. Upload its configuration under the WireGuard storage directory before enabling it. Each app keeps its own chosen VPN address and routing.
              </p>
            </div>
          </div>

          <div class="flex items-center gap-2">
            <button
              v-if="!wireguard?.running"
              @click="controlWireguard('start')"
              :disabled="wireguardActionLoading"
              class="cyber-btn flex items-center gap-1.5 text-xs bg-emerald-600 hover:bg-emerald-500"
            >
              <Play class="h-3.5 w-3.5" />
              <span>Enable WireGuard</span>
            </button>
            <button
              v-else
              @click="controlWireguard('stop')"
              :disabled="wireguardActionLoading"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-rose-700 hover:border-rose-500/50"
            >
              <Square class="h-3.5 w-3.5" />
              <span>Stop Container</span>
            </button>
            <button
              v-if="wireguard?.running"
              @click="controlWireguard('restart')"
              :disabled="wireguardActionLoading"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-slate-700"
              title="Restart WireGuard"
            >
              <RotateCcw class="h-3.5 w-3.5" />
            </button>
            <button
              @click="showAddPeerModal = true"
              class="cyber-btn flex items-center gap-1.5 text-xs bg-indigo-600 hover:bg-indigo-500"
            >
              <Plus class="h-3.5 w-3.5" />
              <span>Add Client Peer</span>
            </button>
          </div>
        </div>

        <!-- WireGuard Interface Status -->
        <div v-if="wireguard?.running" class="grid grid-cols-1 md:grid-cols-3 gap-3 p-4 rounded-xl bg-slate-50/80 border border-slate-200 text-xs font-mono">
          <div>
            <span class="text-slate-500 block text-[10px] uppercase font-semibold">Interface</span>
            <span class="text-emerald-700 font-bold block mt-1">{{ wireguard.interface || 'wg0' }}</span>
          </div>
          <div>
            <span class="text-slate-500 block text-[10px] uppercase font-semibold">Listening Port</span>
            <span class="text-slate-800 block mt-1">{{ wireguard.port || 51820 }} / UDP</span>
          </div>
          <div>
            <span class="text-slate-500 block text-[10px] uppercase font-semibold">Server Public Key</span>
            <span class="text-slate-600 block mt-1 truncate" :title="wireguard.public_key">
              {{ wireguard.public_key ? wireguard.public_key.substring(0, 16) + '…' : 'Server Active' }}
            </span>
          </div>
        </div>
      </div>

      <!-- Container Exposures for WireGuard -->
      <div class="cyber-card space-y-3">
        <div class="flex items-center justify-between">
          <div class="flex items-center gap-2">
            <Box class="h-4 w-4 text-emerald-600" />
            <h3 class="text-xs font-semibold text-slate-800">Exposed Containers on WireGuard VPN</h3>
            <span class="text-[10px] px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-700 font-mono">
              {{ wireguardContainers.length }} routed
            </span>
          </div>
          <button
            @click="openContainerAccess(null, 'private', 'wireguard')"
            class="cyber-btn flex items-center gap-1.5 text-xs bg-emerald-600 hover:bg-emerald-500 py-1 px-2.5"
          >
            <Plus class="h-3.5 w-3.5" />
            <span>Expose Container</span>
          </button>
        </div>

        <div v-if="wireguardContainers.length === 0" class="py-8 text-center text-xs text-slate-500 border border-dashed border-slate-200 rounded-xl bg-slate-50/40 space-y-2">
          <Shield class="h-6 w-6 text-slate-600 mx-auto" />
          <p>No containers have a saved WireGuard access address. Configure your VPN routing before adding an address here.</p>
          <button
            @click="openContainerAccess(null, 'private', 'wireguard')"
            class="cyber-btn-secondary text-xs text-emerald-700 hover:border-emerald-500/50 inline-flex items-center gap-1.5"
          >
            <Plus class="h-3 w-3" />
            <span>Expose a Container</span>
          </button>
        </div>

        <div v-else class="overflow-x-auto">
          <table class="w-full text-left text-xs font-mono">
            <thead>
              <tr class="border-b border-slate-200 text-[11px] text-slate-600 font-medium">
                <th class="py-2.5 px-3">Container</th>
                <th class="py-2.5 px-3">Status</th>
                <th class="py-2.5 px-3">Internal Port</th>
                <th class="py-2.5 px-3">WireGuard Access URL</th>
                <th class="py-2.5 px-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-slate-200/60">
              <tr v-for="c in wireguardContainers" :key="c.id" class="hover:bg-white/50 transition">
                <td class="py-2.5 px-3 font-semibold text-slate-900">
                  {{ c.name }}
                  <span class="block text-[10px] text-slate-500 font-normal truncate max-w-xs">{{ c.image }}</span>
                </td>
                <td class="py-2.5 px-3">
                  <span v-if="c.running" class="inline-flex items-center gap-1 text-[10px] font-semibold text-emerald-600">
                    <span class="h-1.5 w-1.5 rounded-full bg-emerald-400"></span> Running
                  </span>
                  <span v-else class="text-slate-500 text-[10px]">Stopped</span>
                </td>
                <td class="py-2.5 px-3 text-slate-700">
                  {{ c.private_port || (c.ports && c.ports.length ? c.ports[0] : 80) }}
                </td>
                <td class="py-2.5 px-3">
                  <div class="flex items-center gap-2">
                    <span class="text-emerald-700 font-bold truncate max-w-xs">{{ c.wireguard_url || 'VPN address not configured' }}</span>
                    <button
                      v-if="c.wireguard_url"
                      @click="copyText('wg_url_' + c.name, c.wireguard_url)"
                      class="text-slate-500 hover:text-emerald-700"
                    >
                      <Check v-if="copied['wg_url_' + c.name]" class="h-3.5 w-3.5 text-emerald-600" />
                      <Copy v-else class="h-3.5 w-3.5" />
                    </button>
                  </div>
                </td>
                <td class="py-2.5 px-3 text-right">
                  <button
                    @click="openContainerAccess(c, 'private', c.private_type)"
                    class="cyber-btn-sm text-cyan-700 border-cyan-500/30 hover:bg-cyan-50/60"
                  >
                    Manage exposure
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <!-- WireGuard Peers Grid -->
      <div v-if="wireguard?.peers && wireguard.peers.length > 0" class="space-y-3">
        <h3 class="text-xs font-semibold text-slate-800 flex items-center gap-2">
          <Users class="h-4 w-4 text-emerald-600" /> Configured Peer Tunnels ({{ wireguard.peers.length }})
        </h3>

        <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
          <div
            v-for="peer in wireguard.peers"
            :key="peer.name"
            class="cyber-card p-4 space-y-3 flex flex-col justify-between"
          >
            <div>
              <div class="flex items-center justify-between mb-2">
                <span class="text-sm font-bold text-slate-900 flex items-center gap-1.5">
                  <Shield class="h-4 w-4 text-emerald-600" />
                  {{ peer.name }}
                </span>
                <span class="text-[10px] px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-600 border border-emerald-500/30 font-mono">
                  {{ peer.allowed_ips || '10.13.13.x' }}
                </span>
              </div>
              <div class="text-[11px] text-slate-600 font-mono space-y-1">
                <div v-if="peer.latest_handshake" class="truncate">
                  <span class="text-slate-500">Handshake:</span> {{ peer.latest_handshake }}
                </div>
                <div v-if="peer.endpoint" class="truncate">
                  <span class="text-slate-500">Endpoint:</span> {{ peer.endpoint }}
                </div>
              </div>
            </div>

            <div class="pt-3 border-t border-slate-200 flex items-center justify-between gap-1.5">
              <button
                v-if="peer.qr_code_data_url"
                @click="openPeerQr(peer)"
                class="cyber-btn-secondary text-xs flex-1 py-1.5 flex items-center justify-center gap-1 text-emerald-700"
              >
                <QrCode class="h-3.5 w-3.5" />
                <span>QR</span>
              </button>
              <button
                v-if="peer.config_text"
                @click="openPeerConfig(peer)"
                class="cyber-btn-secondary text-xs flex-1 py-1.5 flex items-center justify-center gap-1 text-slate-700"
              >
                <span>Config</span>
              </button>
              <button
                v-if="peer.config_text"
                @click="downloadPeerConfig(peer)"
                class="cyber-btn-secondary text-xs p-1.5 text-slate-600 hover:text-slate-900"
                title="Download .conf file"
              >
                <Download class="h-3.5 w-3.5" />
              </button>
              <button
                @click="deleteWireguardPeer(peer.name)"
                class="cyber-btn-secondary text-xs p-1.5 text-rose-600 hover:bg-rose-50/60"
                title="Delete peer"
              >
                <Trash2 class="h-3.5 w-3.5" />
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- ========================================================================= -->
    <!-- MODALS -->
    <!-- ========================================================================= -->

    <!-- Add WireGuard Peer Modal -->
    <div
      v-if="showAddPeerModal"
      class="fixed inset-0 z-50 flex items-center justify-center bg-slate-50/80 backdrop-blur-sm p-4"
    >
      <div class="cyber-card w-full max-w-md p-6 space-y-4 border-slate-300">
        <div class="flex items-center justify-between">
          <h3 class="text-sm font-bold text-slate-900 flex items-center gap-2">
            <Plus class="h-4 w-4 text-emerald-600" />
            Generate New WireGuard Client Peer
          </h3>
          <button @click="showAddPeerModal = false" class="text-slate-600 hover:text-slate-900 text-xs">✕</button>
        </div>

        <div class="space-y-3 text-xs">
          <div>
            <label class="block text-[11px] font-medium text-slate-700 mb-1">Peer Name</label>
            <input
              v-model="newPeerName"
              type="text"
              placeholder="e.g. iPhone, Work-MacBook, iPad"
              class="cyber-input font-mono text-xs"
              autofocus
            />
          </div>

          <div>
            <label class="block text-[11px] font-medium text-slate-700 mb-1">AllowedIPs Routing Mode</label>
            <div class="grid grid-cols-2 gap-2">
              <button
                type="button"
                @click="newPeerAllowedIPs = 'containers'"
                class="p-2.5 rounded-lg border text-left transition"
                :class="newPeerAllowedIPs === 'containers' ? 'border-emerald-500 bg-emerald-50/40 text-emerald-700' : 'border-slate-200 bg-white text-slate-600'"
              >
                <div class="font-semibold text-xs">Containers & LAN</div>
                <div class="text-[10px] text-slate-500">Split tunnel for Docker containers</div>
              </button>

              <button
                type="button"
                @click="newPeerAllowedIPs = 'all'"
                class="p-2.5 rounded-lg border text-left transition"
                :class="newPeerAllowedIPs === 'all' ? 'border-emerald-500 bg-emerald-50/40 text-emerald-700' : 'border-slate-200 bg-white text-slate-600'"
              >
                <div class="font-semibold text-xs">Full Tunnel</div>
                <div class="text-[10px] text-slate-500">Routes 0.0.0.0/0 (all traffic)</div>
              </button>
            </div>
          </div>
        </div>

        <div class="flex items-center justify-end gap-2 pt-2 border-t border-slate-200">
          <button
            @click="showAddPeerModal = false"
            class="cyber-btn-secondary text-xs py-2 px-3"
          >
            Cancel
          </button>
          <button
            @click="handleAddWireguardPeer"
            :disabled="addingPeer || !newPeerName"
            class="cyber-btn text-xs py-2 px-4 bg-emerald-600 hover:bg-emerald-500"
          >
            {{ addingPeer ? 'Generating…' : 'Generate Peer' }}
          </button>
        </div>
      </div>
    </div>

    <!-- QR Code Modal -->
    <div
      v-if="showQrModal && selectedWgPeer"
      class="fixed inset-0 z-50 flex items-center justify-center bg-slate-50/80 backdrop-blur-sm p-4"
    >
      <div class="cyber-card w-full max-w-sm p-5 space-y-4 border-slate-300 text-center">
        <h3 class="text-sm font-bold text-slate-900 flex items-center justify-center gap-2">
          <Shield class="h-4 w-4 text-emerald-600" />
          {{ selectedWgPeer.name }} QR Code
        </h3>
        <p class="text-[11px] text-slate-600">
          Scan this QR code with the WireGuard app on iOS or Android to import instantly.
        </p>

        <div class="bg-white p-3 rounded-2xl inline-block mx-auto border border-slate-300 shadow-xl">
          <img
            v-if="selectedWgPeer.qr_code_data_url"
            :src="selectedWgPeer.qr_code_data_url"
            :alt="`${selectedWgPeer.name} QR Code`"
            class="w-56 h-56 mx-auto object-contain"
          />
        </div>

        <div class="text-xs font-mono text-emerald-600 font-bold">
          Assigned IP: {{ selectedWgPeer.allowed_ips }}
        </div>

        <div class="flex items-center gap-2 pt-2">
          <button
            @click="downloadPeerConfig(selectedWgPeer)"
            class="cyber-btn-secondary flex-1 text-xs py-2 flex items-center justify-center gap-1.5"
          >
            <Download class="h-3.5 w-3.5" />
            <span>Download .conf</span>
          </button>
          <button
            @click="showQrModal = false; selectedWgPeer = null;"
            class="cyber-btn flex-1 text-xs py-2"
          >
            Close
          </button>
        </div>
      </div>
    </div>

    <!-- Config Text Modal -->
    <div
      v-if="showConfigModal && selectedWgPeer"
      class="fixed inset-0 z-50 flex items-center justify-center bg-slate-50/80 backdrop-blur-sm p-4"
    >
      <div class="cyber-card w-full max-w-lg p-5 space-y-4 border-slate-300">
        <div class="flex items-center justify-between">
          <h3 class="text-sm font-bold text-slate-900 flex items-center gap-2">
            <Shield class="h-4 w-4 text-emerald-600" />
            {{ selectedWgPeer.name }} Client Configuration
          </h3>
          <button
            @click="copyText('wg_cfg', selectedWgPeer.config_text || '')"
            class="cyber-btn-secondary text-xs flex items-center gap-1 py-1 px-2.5"
          >
            <Check v-if="copied['wg_cfg']" class="h-3 w-3 text-emerald-600" />
            <Copy v-else class="h-3 w-3" />
            <span>{{ copied['wg_cfg'] ? 'Copied' : 'Copy' }}</span>
          </button>
        </div>

        <textarea
          readonly
          :value="selectedWgPeer.config_text"
          rows="11"
          class="cyber-input font-mono text-xs w-full resize-none p-3 bg-slate-50 text-slate-700"
        ></textarea>

        <div class="flex items-center justify-end gap-2">
          <button
            @click="downloadPeerConfig(selectedWgPeer)"
            class="cyber-btn-secondary text-xs py-2 px-3 flex items-center gap-1.5"
          >
            <Download class="h-3.5 w-3.5" />
            <span>Download .conf</span>
          </button>
          <button
            @click="showConfigModal = false; selectedWgPeer = null;"
            class="cyber-btn text-xs py-2 px-4"
          >
            Close
          </button>
        </div>
      </div>
    </div>


  </div>
</template>
