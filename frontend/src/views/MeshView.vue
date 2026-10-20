<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { Globe, Lock, RefreshCcw, ShieldCheck } from 'lucide-vue-next'
import { useApi } from '../composables/useApi'

const { apiFetch } = useApi()

const mode = ref<'none' | 'public' | 'tailscale' | 'wireguard'>('public')
const tunnelName = ref('')
const hostname = ref('docktun.example.com')
const service = ref('http://app:3000')
const tailscaleAuthKey = ref('')
const tailscaleHostname = ref('docktun')
const tailscaleLoginServer = ref('https://controlplane.tailscale.com')
const wireguardPrivateKey = ref('')
const wireguardInterface = ref('wg0')
const wireguardListenPort = ref(51820)
const saving = ref(false)
const message = ref('')

async function loadMesh() {
  try {
    const cfg = await apiFetch<{ mode?: string; tunnel_name?: string; public?: { hostname?: string; service?: string }; tailscale?: { auth_key?: string; hostname?: string; login_server?: string }; wireguard?: { private_key?: string; interface?: string; listen_port?: number } }>('/api/mesh')
    mode.value = (cfg.mode as any) || 'public'
    tunnelName.value = cfg.tunnel_name || ''
    hostname.value = cfg.public?.hostname || hostname.value
    service.value = cfg.public?.service || service.value
    tailscaleAuthKey.value = cfg.tailscale?.auth_key || ''
    tailscaleHostname.value = cfg.tailscale?.hostname || tailscaleHostname.value
    tailscaleLoginServer.value = cfg.tailscale?.login_server || tailscaleLoginServer.value
    wireguardPrivateKey.value = cfg.wireguard?.private_key || ''
    wireguardInterface.value = cfg.wireguard?.interface || wireguardInterface.value
    wireguardListenPort.value = cfg.wireguard?.listen_port || wireguardListenPort.value
  } catch {
    mode.value = 'public'
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
    message.value = 'Access settings saved.'
  } catch (error: any) {
    message.value = error.message || 'Failed to save.'
  } finally {
    saving.value = false
  }
}

onMounted(() => loadMesh())
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between gap-3">
      <div>
        <p class="text-[10px] uppercase tracking-[0.22em] text-slate-400">Network access</p>
        <h1 class="text-2xl font-semibold text-slate-100">Expose containers</h1>
      </div>
      <button @click="saveMesh" :disabled="saving" class="cyber-btn">
        <RefreshCcw class="mr-2 h-4 w-4" :stroke-width="1.8" /> {{ saving ? 'Saving…' : 'Save' }}
      </button>
    </div>

    <div v-if="message" class="rounded border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-200">{{ message }}</div>

    <div class="grid gap-3 md:grid-cols-4">
      <button @click="mode = 'public'" class="cyber-card-glow text-left" :class="mode === 'public' ? 'border-emerald-500/60' : ''">
        <Globe class="mb-3 h-5 w-5 text-emerald-300" />
        <div class="text-[10px] uppercase tracking-[0.2em] text-slate-400">Public</div>
        <div class="mt-2 text-sm font-medium text-slate-100">Cloudflare</div>
      </button>
      <button @click="mode = 'tailscale'" class="cyber-card-glow text-left" :class="mode === 'tailscale' ? 'border-sky-500/60' : ''">
        <ShieldCheck class="mb-3 h-5 w-5 text-sky-300" />
        <div class="text-[10px] uppercase tracking-[0.2em] text-slate-400">Private</div>
        <div class="mt-2 text-sm font-medium text-slate-100">Tailscale</div>
      </button>
      <button @click="mode = 'wireguard'" class="cyber-card-glow text-left" :class="mode === 'wireguard' ? 'border-violet-500/60' : ''">
        <Lock class="mb-3 h-5 w-5 text-violet-300" />
        <div class="text-[10px] uppercase tracking-[0.2em] text-slate-400">Private</div>
        <div class="mt-2 text-sm font-medium text-slate-100">WireGuard</div>
      </button>
      <button @click="mode = 'none'" class="cyber-card-glow text-left" :class="mode === 'none' ? 'border-slate-500/80' : ''">
        <div class="mb-3 h-5 w-5 rounded-full border border-slate-500" />
        <div class="text-[10px] uppercase tracking-[0.2em] text-slate-400">Disabled</div>
        <div class="mt-2 text-sm font-medium text-slate-100">Off</div>
      </button>
    </div>

    <div class="cyber-card space-y-4">
      <div v-if="mode === 'public'" class="space-y-4">
        <div>
          <label class="mb-1 block text-[10px] uppercase tracking-[0.18em] text-slate-400">Tunnel name</label>
          <input v-model="tunnelName" class="cyber-input" placeholder="my-tunnel" />
        </div>
        <div>
          <label class="mb-1 block text-[10px] uppercase tracking-[0.18em] text-slate-400">Public hostname</label>
          <input v-model="hostname" class="cyber-input" placeholder="app.example.com" />
        </div>
        <div>
          <label class="mb-1 block text-[10px] uppercase tracking-[0.18em] text-slate-400">Service target</label>
          <input v-model="service" class="cyber-input" placeholder="http://my-container:3000" />
        </div>
      </div>

      <div v-else-if="mode === 'tailscale'" class="space-y-4">
        <div>
          <label class="mb-1 block text-[10px] uppercase tracking-[0.18em] text-slate-400">Auth key</label>
          <input v-model="tailscaleAuthKey" class="cyber-input" placeholder="tskey-auth-..." />
        </div>
        <div>
          <label class="mb-1 block text-[10px] uppercase tracking-[0.18em] text-slate-400">Hostname</label>
          <input v-model="tailscaleHostname" class="cyber-input" placeholder="docktun" />
        </div>
        <div>
          <label class="mb-1 block text-[10px] uppercase tracking-[0.18em] text-slate-400">Login server</label>
          <input v-model="tailscaleLoginServer" class="cyber-input" placeholder="https://controlplane.tailscale.com" />
        </div>
      </div>

      <div v-else-if="mode === 'wireguard'" class="space-y-4">
        <div>
          <label class="mb-1 block text-[10px] uppercase tracking-[0.18em] text-slate-400">Private key</label>
          <input v-model="wireguardPrivateKey" class="cyber-input" placeholder="privateKey==" />
        </div>
        <div>
          <label class="mb-1 block text-[10px] uppercase tracking-[0.18em] text-slate-400">Interface</label>
          <input v-model="wireguardInterface" class="cyber-input" placeholder="wg0" />
        </div>
        <div>
          <label class="mb-1 block text-[10px] uppercase tracking-[0.18em] text-slate-400">Listen port</label>
          <input v-model.number="wireguardListenPort" type="number" class="cyber-input" placeholder="51820" />
        </div>
      </div>

      <div v-else class="text-sm text-slate-400">
        Public and private access are both disabled. Turn on a mode to expose a container.
      </div>
    </div>
  </div>
</template>
