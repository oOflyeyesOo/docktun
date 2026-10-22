<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
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
  Users,
  Smartphone,
  Laptop,
  Monitor,
  Info,
  KeyRound,
  Network,
  Cpu,
  Layers,
  ArrowRight
} from 'lucide-vue-next'
import { useApi } from '../composables/useApi'
import ConfigView from './ConfigView.vue'

const { apiFetch } = useApi()
const route = useRoute()
const router = useRouter()

// Active Top-Level Tab: 'cloudflare' | 'tailscale' | 'wireguard' | 'zeroconf'
const activeTab = ref<'cloudflare' | 'tailscale' | 'wireguard' | 'zeroconf'>('cloudflare')

// Overview Data
interface OverviewData {
  cloudflare: {
    running: boolean
    tunnel_id: string
    tunnel_name: string
    ingress_count: number
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
  zeroconf: {
    running: boolean
    domain: string
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
const copied = ref<Record<string, boolean>>({})

// ZeroConf State
const zeroconfActionLoading = ref(false)

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

// ZeroConf Control
async function controlZeroconf(action: string) {
  zeroconfActionLoading.value = true
  try {
    await apiFetch('/api/remote-access/zeroconf/control', {
      method: 'POST',
      body: JSON.stringify({ action }),
    })
    await fetchOverview()
  } catch (err: any) {
    alert(err.message || `Failed to ${action} ZeroConf`)
  } finally {
    zeroconfActionLoading.value = false
  }
}

// Route Query Handling
onMounted(async () => {
  const qTab = route.query.tab as string
  if (qTab && ['cloudflare', 'tailscale', 'wireguard', 'zeroconf'].includes(qTab)) {
    activeTab.value = qTab as any
  }
  await fetchOverview()
  if (activeTab.value === 'tailscale') await fetchTailscale()
  if (activeTab.value === 'wireguard') await fetchWireguard()
})

function switchTab(tab: 'cloudflare' | 'tailscale' | 'wireguard' | 'zeroconf') {
  activeTab.value = tab
  router.replace({ query: { ...route.query, tab } })
  if (tab === 'tailscale' && !tailscale.value) fetchTailscale()
  if (tab === 'wireguard' && !wireguard.value) fetchWireguard()
}
</script>

<template>
  <div class="space-y-6">
    <!-- Header -->
    <div class="flex flex-col md:flex-row md:items-center justify-between gap-4">
      <div>
        <div class="flex items-center gap-3">
          <div class="rounded-xl border border-cyan-500/30 bg-cyan-950/40 p-2.5 text-cyan-400">
            <Globe class="h-6 w-6" />
          </div>
          <div>
            <h1 class="text-xl font-bold tracking-tight text-slate-100 flex items-center gap-2">
              Remote Access & Exposure
            </h1>
            <p class="text-xs text-slate-400">
              Unified control for Cloudflare edge tunnels, Tailscale zero-trust mesh, WireGuard VPN, and ZeroConf mDNS
            </p>
          </div>
        </div>
      </div>

      <div class="flex items-center gap-2">
        <button
          @click="fetchOverview(); if (activeTab === 'tailscale') fetchTailscale(); if (activeTab === 'wireguard') fetchWireguard();"
          :disabled="loadingOverview"
          class="cyber-btn-secondary text-xs flex items-center gap-1.5"
          title="Refresh Remote Access status"
        >
          <RefreshCw class="h-3.5 w-3.5" :class="loadingOverview ? 'animate-spin' : ''" />
          <span>Refresh</span>
        </button>
      </div>
    </div>

    <!-- Overview Status Cards -->
    <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
      <!-- 1. Cloudflare Tunnel Card -->
      <button
        @click="switchTab('cloudflare')"
        class="cyber-card p-4 text-left transition hover:border-cyan-500/50 flex flex-col justify-between"
        :class="activeTab === 'cloudflare' ? 'ring-1 ring-cyan-500/60 border-cyan-500/40 bg-slate-900/90' : 'bg-slate-950/70'"
      >
        <div class="flex items-start justify-between w-full mb-2">
          <div class="flex items-center gap-2">
            <div class="p-1.5 rounded-lg bg-orange-500/10 border border-orange-500/30 text-orange-400">
              <Globe class="h-4 w-4" />
            </div>
            <span class="text-xs font-bold uppercase tracking-wider text-slate-300">Cloudflare</span>
          </div>
          <span
            v-if="overview?.cloudflare.running"
            class="inline-flex items-center gap-1 text-[10px] font-bold px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-400 border border-emerald-500/30"
          >
            <span class="h-1.5 w-1.5 rounded-full bg-emerald-400 animate-pulse"></span>
            ACTIVE
          </span>
          <span
            v-else
            class="inline-flex items-center gap-1 text-[10px] font-bold px-2 py-0.5 rounded-full bg-slate-800 text-slate-400 border border-slate-700"
          >
            OFFLINE
          </span>
        </div>
        <div>
          <div class="text-lg font-bold text-slate-100 font-mono">
            {{ overview?.cloudflare.ingress_count ?? 0 }} <span class="text-xs text-slate-400 font-normal">routes</span>
          </div>
          <div class="text-[11px] text-slate-500 truncate" :title="overview?.cloudflare.tunnel_id">
            ID: {{ overview?.cloudflare.tunnel_id ? overview.cloudflare.tunnel_id.substring(0, 8) + '…' : 'None' }}
          </div>
        </div>
      </button>

      <!-- 2. Tailscale Card -->
      <button
        @click="switchTab('tailscale')"
        class="cyber-card p-4 text-left transition hover:border-cyan-500/50 flex flex-col justify-between"
        :class="activeTab === 'tailscale' ? 'ring-1 ring-cyan-500/60 border-cyan-500/40 bg-slate-900/90' : 'bg-slate-950/70'"
      >
        <div class="flex items-start justify-between w-full mb-2">
          <div class="flex items-center gap-2">
            <div class="p-1.5 rounded-lg bg-purple-500/10 border border-purple-500/30 text-purple-400">
              <Radio class="h-4 w-4" />
            </div>
            <span class="text-xs font-bold uppercase tracking-wider text-slate-300">Tailscale</span>
          </div>
          <span
            v-if="overview?.tailscale.backend_state === 'Running'"
            class="inline-flex items-center gap-1 text-[10px] font-bold px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-400 border border-emerald-500/30"
          >
            <span class="h-1.5 w-1.5 rounded-full bg-emerald-400 animate-pulse"></span>
            CONNECTED
          </span>
          <span
            v-else-if="overview?.tailscale.backend_state === 'NeedsLogin'"
            class="inline-flex items-center gap-1 text-[10px] font-bold px-2 py-0.5 rounded-full bg-amber-500/10 text-amber-400 border border-amber-500/30 animate-pulse"
          >
            AUTH REQ
          </span>
          <span
            v-else
            class="inline-flex items-center gap-1 text-[10px] font-bold px-2 py-0.5 rounded-full bg-slate-800 text-slate-400 border border-slate-700"
          >
            {{ overview?.tailscale.installed ? 'STOPPED' : 'NOT INSTALLED' }}
          </span>
        </div>
        <div>
          <div class="text-sm font-bold text-slate-100 font-mono truncate">
            {{ overview?.tailscale.tailscale_ips?.[0] || '100.x.y.z' }}
          </div>
          <div class="text-[11px] text-slate-500 truncate">
            {{ overview?.tailscale.peers_count ?? 0 }} peers connected
          </div>
        </div>
      </button>

      <!-- 3. WireGuard Card -->
      <button
        @click="switchTab('wireguard')"
        class="cyber-card p-4 text-left transition hover:border-cyan-500/50 flex flex-col justify-between"
        :class="activeTab === 'wireguard' ? 'ring-1 ring-cyan-500/60 border-cyan-500/40 bg-slate-900/90' : 'bg-slate-950/70'"
      >
        <div class="flex items-start justify-between w-full mb-2">
          <div class="flex items-center gap-2">
            <div class="p-1.5 rounded-lg bg-emerald-500/10 border border-emerald-500/30 text-emerald-400">
              <Shield class="h-4 w-4" />
            </div>
            <span class="text-xs font-bold uppercase tracking-wider text-slate-300">WireGuard</span>
          </div>
          <span
            v-if="overview?.wireguard.running"
            class="inline-flex items-center gap-1 text-[10px] font-bold px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-400 border border-emerald-500/30"
          >
            <span class="h-1.5 w-1.5 rounded-full bg-emerald-400 animate-pulse"></span>
            ACTIVE
          </span>
          <span
            v-else
            class="inline-flex items-center gap-1 text-[10px] font-bold px-2 py-0.5 rounded-full bg-slate-800 text-slate-400 border border-slate-700"
          >
            {{ overview?.wireguard.installed ? 'STOPPED' : 'NOT INSTALLED' }}
          </span>
        </div>
        <div>
          <div class="text-sm font-bold text-slate-100 font-mono">
            Port {{ overview?.wireguard.port || 51820 }}/UDP
          </div>
          <div class="text-[11px] text-slate-500 truncate">
            {{ overview?.wireguard.peers_count ?? 0 }} configured peers
          </div>
        </div>
      </button>

      <!-- 4. ZeroConf mDNS Card -->
      <button
        @click="switchTab('zeroconf')"
        class="cyber-card p-4 text-left transition hover:border-cyan-500/50 flex flex-col justify-between"
        :class="activeTab === 'zeroconf' ? 'ring-1 ring-cyan-500/60 border-cyan-500/40 bg-slate-900/90' : 'bg-slate-950/70'"
      >
        <div class="flex items-start justify-between w-full mb-2">
          <div class="flex items-center gap-2">
            <div class="p-1.5 rounded-lg bg-cyan-500/10 border border-cyan-500/30 text-cyan-400">
              <Network class="h-4 w-4" />
            </div>
            <span class="text-xs font-bold uppercase tracking-wider text-slate-300">ZeroConf</span>
          </div>
          <span
            v-if="overview?.zeroconf.running"
            class="inline-flex items-center gap-1 text-[10px] font-bold px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-400 border border-emerald-500/30"
          >
            <span class="h-1.5 w-1.5 rounded-full bg-emerald-400 animate-pulse"></span>
            ACTIVE
          </span>
          <span
            v-else
            class="inline-flex items-center gap-1 text-[10px] font-bold px-2 py-0.5 rounded-full bg-slate-800 text-slate-400 border border-slate-700"
          >
            INACTIVE
          </span>
        </div>
        <div>
          <div class="text-sm font-bold text-slate-100 font-mono truncate">
            *.{{ overview?.zeroconf.domain || 'local' }}
          </div>
          <div class="text-[11px] text-slate-500">
            Local LAN broadcast
          </div>
        </div>
      </button>
    </div>

    <!-- Navigation Tab Pill Bar -->
    <div class="flex items-center gap-2 border-b border-slate-800 pb-3 overflow-x-auto">
      <button
        @click="switchTab('cloudflare')"
        class="flex items-center gap-2 px-4 py-2 rounded-lg text-xs font-semibold transition"
        :class="activeTab === 'cloudflare' ? 'bg-orange-500/20 text-orange-300 border border-orange-500/40' : 'text-slate-400 hover:text-slate-200 hover:bg-slate-900'"
      >
        <Globe class="h-3.5 w-3.5" />
        Cloudflare Edge Tunnel
      </button>

      <button
        @click="switchTab('tailscale')"
        class="flex items-center gap-2 px-4 py-2 rounded-lg text-xs font-semibold transition"
        :class="activeTab === 'tailscale' ? 'bg-purple-500/20 text-purple-300 border border-purple-500/40' : 'text-slate-400 hover:text-slate-200 hover:bg-slate-900'"
      >
        <Radio class="h-3.5 w-3.5" />
        Tailscale Mesh VPN
      </button>

      <button
        @click="switchTab('wireguard')"
        class="flex items-center gap-2 px-4 py-2 rounded-lg text-xs font-semibold transition"
        :class="activeTab === 'wireguard' ? 'bg-emerald-500/20 text-emerald-300 border border-emerald-500/40' : 'text-slate-400 hover:text-slate-200 hover:bg-slate-900'"
      >
        <Shield class="h-3.5 w-3.5" />
        WireGuard Peer VPN
      </button>

      <button
        @click="switchTab('zeroconf')"
        class="flex items-center gap-2 px-4 py-2 rounded-lg text-xs font-semibold transition"
        :class="activeTab === 'zeroconf' ? 'bg-cyan-500/20 text-cyan-300 border border-cyan-500/40' : 'text-slate-400 hover:text-slate-200 hover:bg-slate-900'"
      >
        <Network class="h-3.5 w-3.5" />
        ZeroConf (mDNS)
      </button>
    </div>

    <!-- ========================================================================= -->
    <!-- TAB 1: CLOUDFLARE TUNNEL (Full Integrated Config & Rules Engine) -->
    <!-- ========================================================================= -->
    <div v-show="activeTab === 'cloudflare'">
      <ConfigView :embedded="true" />
    </div>

    <!-- ========================================================================= -->
    <!-- TAB 2: TAILSCALE ZERO-TRUST MESH VPN -->
    <!-- ========================================================================= -->
    <div v-show="activeTab === 'tailscale'" class="space-y-6">
      <!-- Tailscale Header Card -->
      <div class="cyber-card p-5 space-y-4">
        <div class="flex flex-col md:flex-row md:items-center justify-between gap-4">
          <div class="flex items-center gap-3">
            <div class="rounded-xl border border-purple-500/30 bg-purple-950/40 p-3 text-purple-400">
              <Radio class="h-6 w-6" />
            </div>
            <div>
              <h2 class="text-base font-bold text-slate-100 flex items-center gap-2">
                Tailscale Mesh Network
                <span
                  v-if="tailscale?.backend_state === 'Running'"
                  class="text-[10px] font-bold px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-400 border border-emerald-500/30"
                >
                  ONLINE
                </span>
                <span
                  v-else-if="tailscale?.backend_state === 'NeedsLogin'"
                  class="text-[10px] font-bold px-2 py-0.5 rounded-full bg-amber-500/10 text-amber-400 border border-amber-500/30 animate-pulse"
                >
                  AUTHENTICATION REQUIRED
                </span>
                <span
                  v-else
                  class="text-[10px] font-bold px-2 py-0.5 rounded-full bg-slate-800 text-slate-400 border border-slate-700"
                >
                  {{ tailscale?.installed ? 'STOPPED' : 'NOT INSTALLED' }}
                </span>
              </h2>
              <p class="text-xs text-slate-400">
                Encrypted wireguard-based mesh VPN connecting your containers and devices anywhere without port forwarding.
              </p>
            </div>
          </div>

          <div class="flex items-center gap-2">
            <button
              v-if="!tailscale?.running"
              @click="controlTailscale('start')"
              :disabled="tailscaleActionLoading"
              class="cyber-btn flex items-center gap-1.5 text-xs text-emerald-300 border-emerald-500/50 bg-emerald-950/60 hover:bg-emerald-900"
            >
              <Play class="h-3.5 w-3.5" />
              <span>Launch / Start Tailscale</span>
            </button>
            <button
              v-else
              @click="controlTailscale('stop')"
              :disabled="tailscaleActionLoading"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-rose-300 hover:border-rose-500/50"
            >
              <Square class="h-3.5 w-3.5" />
              <span>Stop Container</span>
            </button>
            <button
              v-if="tailscale?.running"
              @click="controlTailscale('restart')"
              :disabled="tailscaleActionLoading"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-slate-300"
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
          :class="tailscaleMsg.type === 'success' ? 'bg-emerald-950/60 border border-emerald-500/40 text-emerald-300' : 'bg-rose-950/60 border border-rose-500/40 text-rose-300'"
        >
          <CheckCircle2 v-if="tailscaleMsg.type === 'success'" class="h-4 w-4 shrink-0" />
          <AlertCircle v-else class="h-4 w-4 shrink-0" />
          <span>{{ tailscaleMsg.text }}</span>
        </div>

        <!-- Needs Login Auth URL Banner -->
        <div
          v-if="tailscale?.backend_state === 'NeedsLogin' && tailscale.auth_url"
          class="p-4 rounded-xl border border-amber-500/40 bg-amber-950/40 space-y-3"
        >
          <div class="flex items-center gap-2 text-amber-300 font-bold text-xs uppercase tracking-wider">
            <KeyRound class="h-4 w-4 text-amber-400" /> Complete Node Authentication
          </div>
          <p class="text-xs text-slate-300">
            Click the button below to authorize this DockTun node into your Tailscale tailnet:
          </p>
          <div class="flex items-center gap-3">
            <a
              :href="tailscale.auth_url"
              target="_blank"
              rel="noopener"
              class="cyber-btn inline-flex items-center gap-1.5 text-xs text-amber-200 border-amber-500 bg-amber-900/60 hover:bg-amber-800"
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
        <div v-if="tailscale?.running" class="grid grid-cols-1 md:grid-cols-3 gap-3 p-4 rounded-lg bg-slate-950 border border-slate-800 text-xs font-mono">
          <div>
            <span class="text-slate-500 block text-[10px] uppercase font-bold">Tailscale IPv4</span>
            <div class="flex items-center gap-2 mt-1">
              <span class="text-purple-300 font-bold text-sm">{{ tailscale.tailscale_ips?.[0] || 'Pending' }}</span>
              <button
                v-if="tailscale.tailscale_ips?.[0]"
                @click="copyText('ts_ip', tailscale.tailscale_ips[0])"
                class="text-slate-500 hover:text-purple-300"
              >
                <Check v-if="copied['ts_ip']" class="h-3.5 w-3.5 text-emerald-400" />
                <Copy v-else class="h-3.5 w-3.5" />
              </button>
            </div>
          </div>

          <div>
            <span class="text-slate-500 block text-[10px] uppercase font-bold">MagicDNS FQDN</span>
            <span class="text-slate-300 block mt-1 truncate" :title="tailscale.dns_name">
              {{ tailscale.dns_name || 'None' }}
            </span>
          </div>

          <div>
            <span class="text-slate-500 block text-[10px] uppercase font-bold">Node Hostname</span>
            <span class="text-slate-300 block mt-1">{{ tailscale.hostname || 'docktun' }}</span>
          </div>
        </div>

        <!-- Auth Key Form -->
        <div class="border-t border-slate-800/80 pt-4 space-y-3">
          <h3 class="text-xs font-bold uppercase tracking-wider text-slate-300 flex items-center gap-2">
            <KeyRound class="h-3.5 w-3.5 text-purple-400" /> Connect / Authenticate with Auth Key
          </h3>
          <div class="grid grid-cols-1 md:grid-cols-3 gap-3">
            <div class="md:col-span-2">
              <label class="block text-[10px] font-semibold uppercase text-slate-400 mb-1">Tailscale Auth Key (tskey-auth-...)</label>
              <input
                v-model="tailscaleAuthKey"
                type="password"
                placeholder="tskey-auth-k..."
                class="cyber-input text-xs font-mono"
              />
            </div>
            <div>
              <label class="block text-[10px] font-semibold uppercase text-slate-400 mb-1">Node Hostname</label>
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
            class="cyber-btn flex items-center gap-2 text-xs text-purple-200 border-purple-500/50 bg-purple-950/60 hover:bg-purple-900"
          >
            <RefreshCw v-if="tailscaleConnecting" class="h-3.5 w-3.5 animate-spin" />
            <Radio v-else class="h-3.5 w-3.5" />
            <span>{{ tailscaleConnecting ? 'Connecting…' : 'Authenticate & Join Tailnet' }}</span>
          </button>
        </div>
      </div>

      <!-- Tailnet Peers Table -->
      <div v-if="tailscale?.running && tailscale.peers && tailscale.peers.length > 0" class="cyber-card p-5 space-y-3">
        <h3 class="text-xs font-bold uppercase tracking-wider text-slate-300 flex items-center gap-2">
          <Users class="h-4 w-4 text-purple-400" /> Tailnet Connected Devices ({{ tailscale.peers.length }})
        </h3>
        <div class="overflow-x-auto">
          <table class="w-full text-left text-xs font-mono">
            <thead>
              <tr class="border-b border-slate-800 text-[10px] uppercase text-slate-400 font-semibold tracking-wider">
                <th class="py-2.5 px-3">Device / Hostname</th>
                <th class="py-2.5 px-3">OS</th>
                <th class="py-2.5 px-3">Tailscale IP</th>
                <th class="py-2.5 px-3">MagicDNS</th>
                <th class="py-2.5 px-3">Status</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-slate-800/60">
              <tr v-for="peer in tailscale.peers" :key="peer.id" class="hover:bg-slate-900/50 transition">
                <td class="py-2.5 px-3 text-slate-100 font-bold flex items-center gap-2">
                  <Smartphone v-if="peer.os === 'iOS' || peer.os === 'android'" class="h-3.5 w-3.5 text-slate-400" />
                  <Laptop v-else-if="peer.os === 'macOS' || peer.os === 'windows'" class="h-3.5 w-3.5 text-slate-400" />
                  <Server v-else class="h-3.5 w-3.5 text-slate-400" />
                  {{ peer.hostname }}
                </td>
                <td class="py-2.5 px-3 text-slate-400">{{ peer.os }}</td>
                <td class="py-2.5 px-3 text-purple-300 font-bold">{{ peer.tailscale_ips?.[0] }}</td>
                <td class="py-2.5 px-3 text-slate-400 truncate max-w-xs">{{ peer.dns_name }}</td>
                <td class="py-2.5 px-3">
                  <span v-if="peer.online" class="inline-flex items-center gap-1 text-[10px] font-bold text-emerald-400">
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
      <!-- WireGuard Header Card -->
      <div class="cyber-card p-5 space-y-4">
        <div class="flex flex-col md:flex-row md:items-center justify-between gap-4">
          <div class="flex items-center gap-3">
            <div class="rounded-xl border border-emerald-500/30 bg-emerald-950/40 p-3 text-emerald-400">
              <Shield class="h-6 w-6" />
            </div>
            <div>
              <h2 class="text-base font-bold text-slate-100 flex items-center gap-2">
                WireGuard Tunnel Interface
                <span
                  v-if="wireguard?.running"
                  class="text-[10px] font-bold px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-400 border border-emerald-500/30"
                >
                  ACTIVE
                </span>
                <span
                  v-else
                  class="text-[10px] font-bold px-2 py-0.5 rounded-full bg-slate-800 text-slate-400 border border-slate-700"
                >
                  {{ wireguard?.installed ? 'STOPPED' : 'NOT INSTALLED' }}
                </span>
              </h2>
              <p class="text-xs text-slate-400">
                High-performance point-to-point VPN tunnel with automatic peer configurations and mobile QR codes.
              </p>
            </div>
          </div>

          <div class="flex items-center gap-2">
            <button
              v-if="!wireguard?.running"
              @click="controlWireguard('start')"
              :disabled="wireguardActionLoading"
              class="cyber-btn flex items-center gap-1.5 text-xs text-emerald-300 border-emerald-500/50 bg-emerald-950/60 hover:bg-emerald-900"
            >
              <Play class="h-3.5 w-3.5" />
              <span>Launch / Start WireGuard</span>
            </button>
            <button
              v-else
              @click="controlWireguard('stop')"
              :disabled="wireguardActionLoading"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-rose-300 hover:border-rose-500/50"
            >
              <Square class="h-3.5 w-3.5" />
              <span>Stop Container</span>
            </button>
            <button
              v-if="wireguard?.running"
              @click="controlWireguard('restart')"
              :disabled="wireguardActionLoading"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-slate-300"
              title="Restart WireGuard"
            >
              <RotateCcw class="h-3.5 w-3.5" />
            </button>
          </div>
        </div>

        <!-- WireGuard Interface Status -->
        <div v-if="wireguard?.running" class="grid grid-cols-1 md:grid-cols-3 gap-3 p-4 rounded-lg bg-slate-950 border border-slate-800 text-xs font-mono">
          <div>
            <span class="text-slate-500 block text-[10px] uppercase font-bold">Interface</span>
            <span class="text-emerald-300 font-bold block mt-1">{{ wireguard.interface || 'wg0' }}</span>
          </div>
          <div>
            <span class="text-slate-500 block text-[10px] uppercase font-bold">Listening Port</span>
            <span class="text-slate-200 block mt-1">{{ wireguard.port || 51820 }} / UDP</span>
          </div>
          <div>
            <span class="text-slate-500 block text-[10px] uppercase font-bold">Public Key</span>
            <span class="text-slate-400 block mt-1 truncate" :title="wireguard.public_key">
              {{ wireguard.public_key ? wireguard.public_key.substring(0, 16) + '…' : 'Server Active' }}
            </span>
          </div>
        </div>
      </div>

      <!-- WireGuard Peers Grid -->
      <div v-if="wireguard?.peers && wireguard.peers.length > 0" class="space-y-3">
        <h3 class="text-xs font-bold uppercase tracking-wider text-slate-300 flex items-center gap-2">
          <Users class="h-4 w-4 text-emerald-400" /> Configured Peer Tunnels ({{ wireguard.peers.length }})
        </h3>

        <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
          <div
            v-for="peer in wireguard.peers"
            :key="peer.name"
            class="cyber-card p-4 space-y-3 flex flex-col justify-between"
          >
            <div>
              <div class="flex items-center justify-between mb-2">
                <span class="text-sm font-bold text-slate-100 flex items-center gap-1.5">
                  <Shield class="h-4 w-4 text-emerald-400" />
                  {{ peer.name }}
                </span>
                <span class="text-[10px] px-2 py-0.5 rounded bg-emerald-500/10 text-emerald-400 border border-emerald-500/30 font-mono">
                  {{ peer.allowed_ips || '10.13.13.x' }}
                </span>
              </div>
              <div class="text-[11px] text-slate-400 font-mono space-y-1">
                <div v-if="peer.latest_handshake" class="truncate">
                  <span class="text-slate-500">Handshake:</span> {{ peer.latest_handshake }}
                </div>
                <div v-if="peer.endpoint" class="truncate">
                  <span class="text-slate-500">Endpoint:</span> {{ peer.endpoint }}
                </div>
              </div>
            </div>

            <div class="pt-2 border-t border-slate-800 flex items-center justify-between gap-2">
              <button
                v-if="peer.qr_code_data_url"
                @click="openPeerQr(peer)"
                class="cyber-btn-secondary text-[11px] flex-1 py-1 flex items-center justify-center gap-1 text-emerald-300 hover:border-emerald-500/40"
              >
                <span>QR Code</span>
              </button>
              <button
                v-if="peer.config_text"
                @click="openPeerConfig(peer)"
                class="cyber-btn-secondary text-[11px] flex-1 py-1 flex items-center justify-center gap-1 text-slate-300"
              >
                <span>View Config</span>
              </button>
              <button
                v-if="peer.config_text"
                @click="downloadPeerConfig(peer)"
                class="cyber-btn-secondary text-[11px] p-1.5 text-slate-400 hover:text-cyan-300"
                title="Download .conf file"
              >
                <Download class="h-3.5 w-3.5" />
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- ========================================================================= -->
    <!-- TAB 4: ZEROCONF LOCAL MDNS ROUTER -->
    <!-- ========================================================================= -->
    <div v-show="activeTab === 'zeroconf'" class="space-y-6">
      <div class="cyber-card p-5 space-y-4">
        <div class="flex flex-col md:flex-row md:items-center justify-between gap-4">
          <div class="flex items-center gap-3">
            <div class="rounded-xl border border-cyan-500/30 bg-cyan-950/40 p-3 text-cyan-400">
              <Network class="h-6 w-6" />
            </div>
            <div>
              <h2 class="text-base font-bold text-slate-100 flex items-center gap-2">
                ZeroConf (mDNS / .local) Router
                <span
                  v-if="overview?.zeroconf.running"
                  class="text-[10px] font-bold px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-400 border border-emerald-500/30"
                >
                  ACTIVE
                </span>
                <span
                  v-else
                  class="text-[10px] font-bold px-2 py-0.5 rounded-full bg-slate-800 text-slate-400 border border-slate-700"
                >
                  INACTIVE
                </span>
              </h2>
              <p class="text-xs text-slate-400">
                Zero-configuration multicast DNS router advertising DockTun and local Docker containers on your local LAN without external DNS.
              </p>
            </div>
          </div>

          <div class="flex items-center gap-2">
            <button
              v-if="!overview?.zeroconf.running"
              @click="controlZeroconf('start')"
              :disabled="zeroconfActionLoading"
              class="cyber-btn flex items-center gap-1.5 text-xs text-cyan-300 border-cyan-500/50 bg-cyan-950/60 hover:bg-cyan-900"
            >
              <Play class="h-3.5 w-3.5" />
              <span>Enable ZeroConf</span>
            </button>
            <button
              v-else
              @click="controlZeroconf('stop')"
              :disabled="zeroconfActionLoading"
              class="cyber-btn-secondary flex items-center gap-1.5 text-xs text-rose-300 hover:border-rose-500/50"
            >
              <Square class="h-3.5 w-3.5" />
              <span>Disable ZeroConf</span>
            </button>
          </div>
        </div>

        <div class="p-4 rounded-lg bg-slate-950 border border-slate-800 text-xs font-mono space-y-2">
          <div class="flex items-center justify-between">
            <span class="text-slate-500">Local Access Domain:</span>
            <span class="text-cyan-300 font-bold">http://docktun.local</span>
          </div>
          <div class="flex items-center justify-between">
            <span class="text-slate-500">Network Mode:</span>
            <span class="text-slate-300">host (mDNS multicast 5353/udp)</span>
          </div>
        </div>
      </div>
    </div>

    <!-- ========================================================================= -->
    <!-- MODALS: WireGuard QR Code & Config -->
    <!-- ========================================================================= -->

    <!-- QR Code Modal -->
    <div
      v-if="showQrModal && selectedWgPeer"
      class="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/80 backdrop-blur-sm p-4"
    >
      <div class="cyber-card w-full max-w-sm p-5 space-y-4 border-emerald-500/40 text-center">
        <h3 class="text-sm font-bold text-slate-100 flex items-center justify-center gap-2">
          <Shield class="h-4 w-4 text-emerald-400" />
          {{ selectedWgPeer.name }} QR Code
        </h3>
        <p class="text-[11px] text-slate-400">
          Scan this QR code with the WireGuard app on your phone (iOS / Android) to connect instantly.
        </p>

        <div class="bg-white p-3 rounded-xl inline-block mx-auto border border-slate-700 shadow-xl">
          <img
            v-if="selectedWgPeer.qr_code_data_url"
            :src="selectedWgPeer.qr_code_data_url"
            :alt="`${selectedWgPeer.name} QR Code`"
            class="w-56 h-56 mx-auto"
          />
        </div>

        <div class="text-xs font-mono text-emerald-400 font-bold">
          IP: {{ selectedWgPeer.allowed_ips }}
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
      class="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/80 backdrop-blur-sm p-4"
    >
      <div class="cyber-card w-full max-w-lg p-5 space-y-4 border-slate-700">
        <div class="flex items-center justify-between">
          <h3 class="text-sm font-bold text-slate-100 flex items-center gap-2">
            <Shield class="h-4 w-4 text-emerald-400" />
            {{ selectedWgPeer.name }} Client Configuration
          </h3>
          <button
            @click="copyText('wg_cfg', selectedWgPeer.config_text || '')"
            class="cyber-btn-secondary text-xs flex items-center gap-1 py-1 px-2"
          >
            <Check v-if="copied['wg_cfg']" class="h-3 w-3 text-emerald-400" />
            <Copy v-else class="h-3 w-3" />
            <span>{{ copied['wg_cfg'] ? 'Copied' : 'Copy' }}</span>
          </button>
        </div>

        <textarea
          readonly
          :value="selectedWgPeer.config_text"
          rows="10"
          class="cyber-input font-mono text-xs w-full resize-none p-3 bg-slate-950 text-slate-300"
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
