<script setup lang="ts">
import { ref, onMounted } from 'vue'
import {
  Globe,
  Lock,
  RotateCcw,
  Shield,
  ShieldCheck,
  CheckCircle2,
  AlertCircle,
  RefreshCw,
  Radio,
  ExternalLink,
  Sliders,
  Check
} from 'lucide-vue-next'
import { useApi } from '../composables/useApi'

const { apiFetch } = useApi()

const mode = ref<'none' | 'public' | 'tailscale' | 'wireguard'>('public')
const tunnelName = ref('')
const tunnelId = ref('')
const hostname = ref('')
const service = ref('http://docktun:3001')
const tailscaleAuthKey = ref('')
const tailscaleHostname = ref('docktun-node')
const tailscaleLoginServer = ref('https://controlplane.tailscale.com')
const wireguardPrivateKey = ref('')
const wireguardInterface = ref('wg0')
const wireguardListenPort = ref(51820)
const saving = ref(false)
const syncingTunnel = ref(false)
const message = ref('')
const messageType = ref<'success' | 'error'>('success')
const activeStatus = ref<any>(null)

async function loadMesh() {
  try {
    const [cfg, stat] = await Promise.all([
      apiFetch<any>('/api/mesh').catch(() => null),
      apiFetch<any>('/api/status').catch(() => null),
    ])

    if (stat) {
      activeStatus.value = stat
      if (stat.tunnel_name) tunnelName.value = stat.tunnel_name
      if (stat.tunnel_id) tunnelId.value = stat.tunnel_id
    }

    if (cfg) {
      mode.value = cfg.mode || 'public'
      if (cfg.tunnel_name) tunnelName.value = cfg.tunnel_name
      if (cfg.tunnel_id) tunnelId.value = cfg.tunnel_id
      if (cfg.public) {
        if (cfg.public.hostname) hostname.value = cfg.public.hostname
        if (cfg.public.service) service.value = cfg.public.service
      }
      if (cfg.tailscale) {
        tailscaleAuthKey.value = cfg.tailscale.auth_key || ''
        tailscaleHostname.value = cfg.tailscale.hostname || 'docktun-node'
        tailscaleLoginServer.value = cfg.tailscale.login_server || 'https://controlplane.tailscale.com'
      }
      if (cfg.wireguard) {
        wireguardPrivateKey.value = cfg.wireguard.private_key || ''
        wireguardInterface.value = cfg.wireguard.interface || 'wg0'
        wireguardListenPort.value = cfg.wireguard.listen_port || 51820
      }
    }

    // Auto-fill from active tunnel config if fields are still blank
    if (!hostname.value || !tunnelName.value) {
      await syncFromActiveTunnel(false)
    }
  } catch {
    mode.value = 'public'
  }
}

async function syncFromActiveTunnel(showFeedback = true) {
  syncingTunnel.value = true
  try {
    const [stat, cfgRes] = await Promise.all([
      apiFetch<any>('/api/status').catch(() => null),
      apiFetch<any>('/api/config').catch(() => null),
    ])

    if (stat) {
      activeStatus.value = stat
      if (stat.tunnel_name) tunnelName.value = stat.tunnel_name
      else if (stat.tunnel_id) tunnelName.value = stat.tunnel_id
      if (stat.tunnel_id) tunnelId.value = stat.tunnel_id
    }

    if (cfgRes && cfgRes.content) {
      // Parse ingress rules from active yaml
      const lines = cfgRes.content.split('\n')
      let foundHost = ''
      let foundSvc = ''
      for (let i = 0; i < lines.length; i++) {
        const line = lines[i].trim()
        const hostMatch = line.match(/^-\s*hostname:\s*["']?([^"'\s]+)["']?/)
        if (hostMatch && !foundHost) {
          foundHost = hostMatch[1]
        }
        const svcMatch = line.match(/^service:\s*["']?([^"'\s]+)["']?/)
        if (svcMatch && !foundSvc && svcMatch[1] !== 'http_status:404') {
          foundSvc = svcMatch[1]
        }
        if (foundHost && foundSvc) break
      }

      if (foundHost) hostname.value = foundHost
      if (foundSvc) service.value = foundSvc
    }

    if (showFeedback) {
      messageType.value = 'success'
      message.value = 'Parameters successfully auto-filled from active Cloudflare Tunnel configuration!'
      setTimeout(() => (message.value = ''), 4000)
    }
  } catch (e: any) {
    if (showFeedback) {
      messageType.value = 'error'
      message.value = 'Failed to sync parameters: ' + e.message
    }
  } finally {
    syncingTunnel.value = false
  }
}

async function saveMesh() {
  saving.value = true
  message.value = ''

  try {
    await apiFetch('/api/mesh', {
      method: 'PUT',
      body: JSON.stringify({
        mode: mode.value,
        tunnel_name: tunnelName.value,
        tunnel_id: tunnelId.value,
        public: { hostname: hostname.value, service: service.value },
        tailscale: {
          auth_key: tailscaleAuthKey.value,
          hostname: tailscaleHostname.value,
          login_server: tailscaleLoginServer.value,
        },
        wireguard: {
          private_key: wireguardPrivateKey.value,
          interface: wireguardInterface.value,
          listen_port: wireguardListenPort.value,
        },
      }),
    })
    messageType.value = 'success'
    message.value = 'Mesh & network access configuration saved successfully.'
    setTimeout(() => (message.value = ''), 4000)
  } catch (error: any) {
    messageType.value = 'error'
    message.value = error.message || 'Failed to save configuration.'
  } finally {
    saving.value = false
  }
}

onMounted(() => loadMesh())
</script>

<template>
  <div class="space-y-6">
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <p class="text-[10px] font-semibold uppercase tracking-[0.22em] text-slate-600">Encrypted Transport</p>
        <h1 class="text-2xl font-bold tracking-tight text-slate-900">Private Mesh & Routing</h1>
      </div>
      <button @click="saveMesh" :disabled="saving" class="cyber-btn flex items-center gap-2">
        <RotateCcw class="h-4 w-4" :class="saving ? 'animate-spin' : ''" />
        <span>{{ saving ? 'Saving…' : 'Save Changes' }}</span>
      </button>
    </div>

    <!-- Alert Banner -->
    <div
      v-if="message"
      class="flex items-center gap-2.5 rounded-lg border px-4 py-3 text-xs"
      :class="messageType === 'success' ? 'border-emerald-500/30 bg-emerald-50/40 text-emerald-700' : 'border-red-500/30 bg-red-50/40 text-red-700'"
    >
      <CheckCircle2 v-if="messageType === 'success'" class="h-4 w-4 flex-shrink-0" />
      <AlertCircle v-else class="h-4 w-4 flex-shrink-0" />
      <span>{{ message }}</span>
    </div>

    <!-- Mode Selector Cards -->
    <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
      <button
        @click="mode = 'public'"
        class="cyber-card-glow text-left transition"
        :class="mode === 'public' ? 'border-cyan-500 bg-cyan-50/20' : ''"
      >
        <Globe class="mb-3 h-5 w-5 text-cyan-600" />
        <div class="text-[10px] font-bold uppercase tracking-[0.2em] text-slate-600">Public Ingress</div>
        <div class="mt-1 text-sm font-semibold text-slate-900">Cloudflare Tunnel</div>
        <p class="mt-2 text-[11px] text-slate-500 leading-relaxed">Expose containers to the world with zero open ports via Cloudflare edge.</p>
      </button>

      <button
        @click="mode = 'tailscale'"
        class="cyber-card-glow text-left transition"
        :class="mode === 'tailscale' ? 'border-sky-500 bg-sky-950/20' : ''"
      >
        <ShieldCheck class="mb-3 h-5 w-5 text-sky-400" />
        <div class="text-[10px] font-bold uppercase tracking-[0.2em] text-slate-600">Private Mesh</div>
        <div class="mt-1 text-sm font-semibold text-slate-900">Tailscale</div>
        <p class="mt-2 text-[11px] text-slate-500 leading-relaxed">Join your Tailscale tailnet for peer-to-peer WireGuard mesh access.</p>
      </button>

      <button
        @click="mode = 'wireguard'"
        class="cyber-card-glow text-left transition"
        :class="mode === 'wireguard' ? 'border-violet-500 bg-violet-950/20' : ''"
      >
        <Lock class="mb-3 h-5 w-5 text-violet-400" />
        <div class="text-[10px] font-bold uppercase tracking-[0.2em] text-slate-600">Direct VPN</div>
        <div class="mt-1 text-sm font-semibold text-slate-900">WireGuard</div>
        <p class="mt-2 text-[11px] text-slate-500 leading-relaxed">Point-to-point native kernel WireGuard tunnel interface.</p>
      </button>

      <button
        @click="mode = 'none'"
        class="cyber-card-glow text-left transition"
        :class="mode === 'none' ? 'border-slate-500 bg-white/60' : ''"
      >
        <div class="mb-3 flex h-5 w-5 items-center justify-center rounded-full border border-slate-400">
          <span class="h-2 w-2 rounded-full bg-slate-300"></span>
        </div>
        <div class="text-[10px] font-bold uppercase tracking-[0.2em] text-slate-600">Local Only</div>
        <div class="mt-1 text-sm font-semibold text-slate-900">Isolated</div>
        <p class="mt-2 text-[11px] text-slate-500 leading-relaxed">Disable overlay routing. Keep containers bound to local Docker networks.</p>
      </button>
    </div>

    <!-- Active Mode Configuration Panel -->
    <div class="cyber-card space-y-4">
      <div v-if="mode === 'public'" class="space-y-4">
        <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-200 pb-3">
          <div>
            <h2 class="text-xs font-bold uppercase tracking-[0.2em] text-cyan-600">Cloudflare Tunnel Parameters</h2>
            <p class="mt-1 text-xs text-slate-600">Auto-filled from your active Cloudflare Tunnel configuration & environment.</p>
          </div>
          <button
            @click="syncFromActiveTunnel(true)"
            :disabled="syncingTunnel"
            class="flex items-center gap-1.5 self-start sm:self-auto rounded border border-cyan-500/40 bg-cyan-50/40 px-3 py-1.5 text-xs font-semibold text-cyan-700 hover:bg-cyan-100/50 transition"
            title="Auto-fill values from active Cloudflare Tunnel configuration"
          >
            <RefreshCw class="h-3.5 w-3.5" :class="syncingTunnel ? 'animate-spin' : ''" />
            <span>Sync from Active Tunnel</span>
          </button>
        </div>

        <!-- Live Tunnel Connection Badge -->
        <div v-if="activeStatus" class="flex flex-wrap items-center gap-3 rounded-lg border border-cyan-900/40 bg-cyan-50/20 p-3 text-xs">
          <div class="flex items-center gap-1.5">
            <Radio class="h-3.5 w-3.5 text-emerald-600 animate-pulse" />
            <span class="text-slate-600">Tunnel Status:</span>
            <span class="font-bold text-emerald-600 font-mono uppercase">{{ activeStatus.status || 'Active' }}</span>
          </div>
          <div v-if="activeStatus.tunnel_id" class="flex items-center gap-1.5 text-slate-600 font-mono">
            <span>UUID:</span>
            <span class="text-cyan-700">{{ activeStatus.tunnel_id }}</span>
          </div>
          <div v-if="activeStatus.protocol" class="flex items-center gap-1.5 text-slate-600 font-mono">
            <span>Protocol:</span>
            <span class="text-slate-700">{{ activeStatus.protocol }}</span>
          </div>
        </div>

        <div class="grid gap-4 md:grid-cols-2">
          <div>
            <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-600">Tunnel Name or UUID</label>
            <input v-model="tunnelName" class="cyber-input font-mono" placeholder="docktun-tunnel" />
            <p class="mt-1 text-[10px] text-slate-500">Auto-populated from active cloudflared credentials/environment.</p>
          </div>
          <div>
            <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-600">Default Public Ingress Hostname</label>
            <input v-model="hostname" class="cyber-input font-mono" placeholder="app.yourdomain.com" />
            <p class="mt-1 text-[10px] text-slate-500">Auto-populated from first active ingress rule in cloudflared config.</p>
          </div>
          <div class="md:col-span-2">
            <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-600">Default Upstream Service Target</label>
            <input v-model="service" class="cyber-input font-mono" placeholder="http://docktun:3001" />
            <p class="mt-1 text-[10px] text-slate-500">Internal Docker container address or port routed to by Cloudflare edge.</p>
          </div>
        </div>
      </div>

      <div v-else-if="mode === 'tailscale'" class="space-y-4">
        <div class="border-b border-slate-200 pb-3">
          <h2 class="text-xs font-bold uppercase tracking-[0.2em] text-sky-400">Tailscale Authentication</h2>
          <p class="mt-1 text-xs text-slate-600">Connect this host or container cluster to your private Tailscale tailnet.</p>
        </div>
        <div class="grid gap-4 md:grid-cols-2">
          <div class="md:col-span-2">
            <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-600">Auth Key (Reusable or Ephemeral)</label>
            <input v-model="tailscaleAuthKey" type="password" class="cyber-input" placeholder="tskey-auth-k..." />
          </div>
          <div>
            <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-600">Tailnet Node Hostname</label>
            <input v-model="tailscaleHostname" class="cyber-input" placeholder="docktun-node" />
          </div>
          <div>
            <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-600">Control Plane / Headscale Server</label>
            <input v-model="tailscaleLoginServer" class="cyber-input" placeholder="https://controlplane.tailscale.com" />
          </div>
        </div>
      </div>

      <div v-else-if="mode === 'wireguard'" class="space-y-4">
        <div class="border-b border-slate-200 pb-3">
          <h2 class="text-xs font-bold uppercase tracking-[0.2em] text-violet-400">WireGuard Interface Settings</h2>
          <p class="mt-1 text-xs text-slate-600">Native point-to-point WireGuard tunnel interface configuration.</p>
        </div>
        <div class="grid gap-4 md:grid-cols-2">
          <div class="md:col-span-2">
            <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-600">Private Key</label>
            <input v-model="wireguardPrivateKey" type="password" class="cyber-input" placeholder="e.g. aGFzZWN1cmVwcml2YXRla2V5...==" />
          </div>
          <div>
            <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-600">Network Interface</label>
            <input v-model="wireguardInterface" class="cyber-input" placeholder="wg0" />
          </div>
          <div>
            <label class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-600">Listen UDP Port</label>
            <input v-model.number="wireguardListenPort" type="number" class="cyber-input" placeholder="51820" />
          </div>
        </div>
      </div>

      <div v-else class="py-4 text-center text-xs text-slate-600 space-y-2">
        <p class="font-semibold text-slate-700">Local Only & Isolated Mode Active</p>
        <p class="text-slate-500 max-w-md mx-auto">Overlay networking and public Cloudflare tunnels are disabled for the core dashboard. All containers operate within isolated Docker network bridges and local LAN access.</p>
      </div>
    </div>
  </div>
</template>
