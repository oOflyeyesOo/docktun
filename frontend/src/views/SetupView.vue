<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import {
  ShieldCheck,
  Globe,
  Server,
  Layers,
  KeyRound,
  CheckCircle2,
  AlertCircle,
  RotateCcw,
  Rocket,
  ChevronRight,
  ChevronLeft,
  Sparkles,
  Wifi,
  Radio,
  Lock,
  Cpu,
  HardDrive,
  Eye,
  EyeOff,
  Bell
} from 'lucide-vue-next'
import { useAuthStore } from '../stores/auth'
import { useApi } from '../composables/useApi'

const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()
const { apiFetch } = useApi()

// Current Wizard Step (1: Account, 2: Tunnel, 3: Containers, 4: Notifications, 5: Review)
const step = ref(1)
const loading = ref(true)
const submitting = ref(false)
const submitStep = ref('')
const errorMessage = ref<string | null>(null)

// System status fetched from backend
const systemStatus = ref<any>({
  completed: false,
  has_admin_user: false,
  admin_username: 'admin',
  host_os: 'linux',
  host_arch: 'amd64',
  num_cpu: 4,
  docker_version: 'Loading...',
  docker_connected: false,
  tunnel_configured: false,
  tunnel_type: 'none',
  discovered_containers: [],
  discovered_stacks: [],
})

// Wizard Form State
const form = reactive({
  setup_token: '',
  admin_username: 'admin',
  admin_password: '',
  confirm_password: '',
  show_password: false,

  // Tunnel Config
  tunnel_type: 'none' as 'cloudflare' | 'wireguard' | 'tailscale' | 'none',
  cloudflare_mode: 'token' as 'token' | 'api' | 'existing',
  cloudflare_token: '',
  cloudflare_api_token: '',
  cloudflare_account_id: '',
  cloudflare_tunnel_name: 'docktun',
  cloudflare_tunnel_id: '',
  wireguard_server_url: '',
  wireguard_port: 51820,
  tailscale_auth_key: '',

  // Container Import Config
  selected_container_ids: {} as Record<string, boolean>,
  connect_network: false,
  auto_import_stacks: true,

  // Notifications
  enable_notifications: false,
  notification_type: 'discord' as 'discord' | 'telegram' | 'slack' | 'ntfy' | 'webhook',
  webhook_url: '',
  telegram_bot_token: '',
  telegram_chat_id: '',
})

// Password validation
const passwordMismatch = computed(() => {
  return form.admin_password && form.confirm_password && form.admin_password !== form.confirm_password
})

const passwordValid = computed(() => {
  if (systemStatus.value.has_admin_user && !form.admin_password) return true
  return new TextEncoder().encode(form.admin_password).length >= 12 && new TextEncoder().encode(form.admin_password).length <= 72 && !passwordMismatch.value
})

// Container Selection Helpers
const selectableContainers = computed(() => {
  return (systemStatus.value.discovered_containers || []).filter((c: any) => !c.is_system)
})

const selectedContainerCount = computed(() => {
  return Object.values(form.selected_container_ids).filter(Boolean).length
})

function selectAllContainers() {
  selectableContainers.value.forEach((c: any) => {
    form.selected_container_ids[c.id] = true
  })
}

function deselectAllContainers() {
  form.selected_container_ids = {}
}

async function loadSetupStatus() {
  loading.value = true
  errorMessage.value = null
  try {
    const force = route.query.force === 'true'
    const res = await fetch(`/api/setup/status`)
    if (res.ok) {
      let data = await res.json()
      if (data.completed && !authStore.isAuthenticated) {
        await router.replace('/login')
        return
      }
      if (authStore.isAuthenticated) data = await apiFetch<any>('/api/setup/details')
      systemStatus.value = data
      form.tunnel_type = data.tunnel_type || 'none'
      if (data.has_admin_user) step.value = 2
      if (data.admin_username) {
        form.admin_username = data.admin_username
      }

      // Pre-select non-system containers by default for easy onboarding
      if (data.discovered_containers && Array.isArray(data.discovered_containers)) {
        data.discovered_containers.forEach((c: any) => {
          if (!c.is_system && c.running) {
            form.selected_container_ids[c.id] = true
          }
        })
      }

      // If already completed and not forced, redirect to /
      if (data.completed && !force) {
        router.push('/')
      }
    }
  } catch (err: any) {
    console.error('Failed to load setup status:', err)
    errorMessage.value = 'Could not communicate with DockTun backend API.'
  } finally {
    loading.value = false
  }
}

async function finishSetup() {
  submitting.value = true
  errorMessage.value = null
  submitStep.value = 'Configuring administrator account...'

  try {
    const selectedIds = Object.keys(form.selected_container_ids).filter((id) => form.selected_container_ids[id])

    const payload = {
      setup_token: form.setup_token,
      admin_username: form.admin_username,
      admin_password: systemStatus.value.has_admin_user ? '' : form.admin_password,
      tunnel: {
        type: form.tunnel_type,
        cloudflare_token: form.cloudflare_mode === 'token' ? form.cloudflare_token : '',
        cloudflare_api_token: form.cloudflare_mode === 'api' ? form.cloudflare_api_token : '',
        cloudflare_account_id: form.cloudflare_mode === 'api' ? form.cloudflare_account_id : '',
        cloudflare_tunnel_name: form.cloudflare_mode === 'api' ? form.cloudflare_tunnel_name : '',
        cloudflare_tunnel_id: form.cloudflare_mode === 'api' ? form.cloudflare_tunnel_id : '',
        wireguard_server_url: form.wireguard_server_url,
        wireguard_port: form.wireguard_port,
        tailscale_auth_key: form.tailscale_auth_key,
      },
      import_containers: {
        auto_import_stacks: form.auto_import_stacks,
        connect_network: form.connect_network,
        container_ids: selectedIds,
      },
      notifications: {
        enabled: form.enable_notifications,
        type: form.notification_type,
        webhook_url: form.webhook_url,
      },
    }

    submitStep.value = 'Applying tunnel routes and connecting container networks...'
    const res = await fetch(systemStatus.value.has_admin_user ? '/api/setup/reconfigure' : '/api/setup/complete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', ...(authStore.token ? { Authorization: `Bearer ${authStore.token}` } : {}) },
      body: JSON.stringify(payload),
    })

    if (!res.ok) {
      const err = await res.json()
      throw new Error(err.error || 'Setup completion failed')
    }

    const data = await res.json()
    submitStep.value = 'Finalizing session credentials...'

    // Store JWT token if returned
    if (data.access_token) {
      authStore.setAuth(data.access_token, data.username || form.admin_username)
    }

    submitStep.value = 'Ready! Redirecting to Dashboard...'
    setTimeout(() => {
      router.push('/')
    }, 1200)
  } catch (err: any) {
    errorMessage.value = err.message || 'Setup encountered an error.'
    submitting.value = false
  }
}

onMounted(() => {
  loadSetupStatus()
})
</script>

<template>
  <div class="min-h-screen bg-slate-50 text-slate-900 flex flex-col justify-between p-4 sm:p-6 lg:p-8 font-sans selection:bg-cyan-500 selection:text-black">
    <!-- Top Header -->
    <header class="max-w-4xl mx-auto w-full flex items-center justify-between border-b border-slate-200/80 pb-4">
      <div class="flex items-center gap-3">
        <div class="p-2 rounded-xl bg-indigo-500/10 border border-indigo-500/20 text-indigo-600">
          <img src="/icon.png" alt="DockTun" class="h-7 w-7 object-contain" />
        </div>
        <div>
          <div class="text-base font-bold tracking-tight text-slate-900 flex items-center gap-2">
            DockTun
            <span class="text-[10px] font-medium px-2 py-0.5 rounded-full bg-cyan-500/10 text-cyan-700 border border-cyan-500/30 font-mono">
              Setup Wizard
            </span>
          </div>
          <div class="text-xs text-slate-600">First-Run Installation & Tunnel Onboarding</div>
        </div>
      </div>

      <!-- System Diagnostic Pill -->
      <div class="hidden sm:flex items-center gap-2 text-xs font-mono text-slate-600 bg-white border border-slate-200 rounded-lg px-3 py-1.5">
        <Cpu class="h-3.5 w-3.5 text-cyan-600" />
        <span>{{ systemStatus.host_os }}/{{ systemStatus.host_arch }} • {{ systemStatus.num_cpu }} cores</span>
      </div>
    </header>

    <!-- Main Wizard Card Container -->
    <main class="max-w-4xl mx-auto w-full my-6 flex-1 flex flex-col justify-center">
      <!-- Loading State -->
      <div v-if="loading" class="cyber-card py-24 text-center space-y-3">
        <RotateCcw class="h-8 w-8 text-cyan-600 animate-spin mx-auto" />
        <p class="text-sm font-mono text-slate-700">Discovering Docker host environment & containers...</p>
      </div>

      <div v-else class="cyber-card !p-6 sm:!p-8 space-y-6 relative overflow-hidden border-cyan-800/40 shadow-sm">
        <!-- Error Banner -->
        <div
          v-if="errorMessage"
          class="p-3.5 rounded-lg border border-rose-500/50 bg-rose-50/20 text-rose-700 text-xs font-mono flex items-center gap-2"
        >
          <AlertCircle class="h-4 w-4 shrink-0 text-rose-600" />
          <span>{{ errorMessage }}</span>
        </div>

        <!-- Step Indicator Bar -->
        <div class="grid grid-cols-4 gap-2 border-b border-slate-200 pb-5">
          <div
            v-for="(st, idx) in [
              { num: 1, title: 'Admin Account', icon: KeyRound },
              { num: 2, title: 'Tunnel Mode', icon: Globe },
              { num: 3, title: 'Import Containers', icon: Server },
              { num: 4, title: 'Review & Launch', icon: Rocket },
            ]"
            :key="st.num"
            class="flex items-center gap-2 text-xs transition-colors"
            :class="step >= st.num ? 'text-cyan-600 font-semibold' : 'text-slate-600'"
          >
            <div
              class="h-6 w-6 rounded-full flex items-center justify-center font-mono text-[11px] border shrink-0 transition-all"
              :class="
                step === st.num
                  ? 'border-cyan-400 bg-cyan-50 text-cyan-700 shadow-sm'
                  : step > st.num
                  ? 'border-emerald-500 bg-emerald-50 text-emerald-600'
                  : 'border-slate-200 bg-white text-slate-600'
              "
            >
              <CheckCircle2 v-if="step > st.num" class="h-3.5 w-3.5" />
              <span v-else>{{ st.num }}</span>
            </div>
            <span class="hidden sm:inline truncate">{{ st.title }}</span>
          </div>
        </div>

        <!-- ================================================================= -->
        <!-- STEP 1: ADMIN ACCOUNT                                             -->
        <!-- ================================================================= -->
        <div v-if="step === 1" class="space-y-5 animate-fade-in">
          <label class="block text-sm">Setup token
            <input v-model="form.setup_token" type="password" autocomplete="off" class="cyber-input w-full mt-2" placeholder="Paste the token from data/setup.token on the host" />
            <span class="block text-xs text-slate-600 mt-2">Retrieve it with: docker compose exec docktun cat /app/data/setup.token</span>
          </label>
          <div>
            <h2 class="text-lg font-bold text-slate-900 flex items-center gap-2">
              <KeyRound class="h-5 w-5 text-cyan-600" />
              <span>Step 1: Administrator Account</span>
            </h2>
            <p class="text-xs text-slate-600 mt-1">
              Configure your primary credentials to secure access to the DockTun management dashboard.
            </p>
          </div>

          <!-- Notice if admin already set in env -->
          <div
            v-if="systemStatus.has_admin_user"
            class="p-3 rounded-lg border border-indigo-500/40 bg-indigo-50/20 text-indigo-700 text-xs font-mono flex items-center gap-2"
          >
            <CheckCircle2 class="h-4 w-4 text-indigo-600 shrink-0" />
            <span>Account credentials are managed in Settings. This wizard only changes configuration.</span>
          </div>

          <div class="grid grid-cols-1 sm:grid-cols-2 gap-4 max-w-xl">
            <div class="space-y-1.5 sm:col-span-2">
              <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-600 font-mono">
                Administrator Username
              </label>
              <input
                v-model="form.admin_username"
                type="text"
                placeholder="admin"
                class="cyber-input text-xs font-mono"
              />
            </div>

            <div class="space-y-1.5">
              <div class="flex items-center justify-between">
                <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-600 font-mono">
                  Password
                </label>
                <button
                  type="button"
                  @click="form.show_password = !form.show_password"
                  class="text-[10px] text-slate-500 hover:text-slate-700 flex items-center gap-1 font-mono"
                >
                  <component :is="form.show_password ? EyeOff : Eye" class="h-3 w-3" />
                  <span>{{ form.show_password ? 'Hide' : 'Show' }}</span>
                </button>
              </div>
              <input
                v-model="form.admin_password"
                :type="form.show_password ? 'text' : 'password'"
                placeholder="••••••••••••"
                class="cyber-input text-xs font-mono"
              />
            </div>

            <div class="space-y-1.5">
              <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-600 font-mono">
                Confirm Password
              </label>
              <input
                v-model="form.confirm_password"
                :type="form.show_password ? 'text' : 'password'"
                placeholder="••••••••••••"
                class="cyber-input text-xs font-mono"
                :class="passwordMismatch ? 'border-rose-500 focus:border-rose-500' : ''"
              />
            </div>
          </div>

          <p v-if="passwordMismatch" class="text-rose-600 text-xs font-mono">
            ⚠️ Passwords do not match.
          </p>
        </div>

        <!-- ================================================================= -->
        <!-- STEP 2: TUNNEL ACTIVATION                                         -->
        <!-- ================================================================= -->
        <div v-else-if="step === 2" class="space-y-5 animate-fade-in">
          <div>
            <h2 class="text-lg font-bold text-slate-900 flex items-center gap-2">
              <Globe class="h-5 w-5 text-cyan-600" />
              <span>Step 2: Remote Access (Optional)</span>
            </h2>
            <p class="text-xs text-slate-600 mt-1">
              You can skip remote access entirely. Choose a provider only if you want to configure it; exposing individual containers is a separate choice.
            </p>
          </div>

          <!-- Tunnel Mode Grid -->
          <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
            <button
              type="button"
              @click="form.tunnel_type = 'cloudflare'"
              class="flex flex-col text-left p-3.5 rounded-xl border transition-all cursor-pointer relative"
              :class="
                form.tunnel_type === 'cloudflare'
                  ? 'border-cyan-500 bg-cyan-50/40 text-cyan-700 shadow-md ring-1 ring-cyan-500'
                  : 'border-slate-200 bg-white/40 text-slate-600 hover:border-slate-300'
              "
            >
              <div class="flex items-center justify-between">
                <Globe class="h-5 w-5 text-cyan-600" />
                <span class="text-[9px] uppercase px-1.5 py-0.2 rounded bg-cyan-500/20 text-cyan-700 font-mono font-bold">Public access</span>
              </div>
              <span class="text-xs font-bold font-mono mt-2 text-slate-900">Cloudflare Tunnel</span>
              <span class="text-[11px] text-slate-600 mt-1 leading-snug">
                Zero Trust edge tunnel. Public ingress, custom domains, and access policies with no port forwarding.
              </span>
            </button>

            <button
              type="button"
              @click="form.tunnel_type = 'wireguard'"
              class="flex flex-col text-left p-3.5 rounded-xl border transition-all cursor-pointer relative"
              :class="
                form.tunnel_type === 'wireguard'
                  ? 'border-indigo-500 bg-indigo-50/40 text-indigo-700 shadow-md ring-1 ring-indigo-500'
                  : 'border-slate-200 bg-white/40 text-slate-600 hover:border-slate-300'
              "
            >
              <div class="flex items-center justify-between">
                <ShieldCheck class="h-5 w-5 text-indigo-600" />
                <span class="text-[9px] uppercase px-1.5 py-0.2 rounded bg-indigo-500/20 text-indigo-700 font-mono">P2P VPN</span>
              </div>
              <span class="text-xs font-bold font-mono mt-2 text-slate-900">WireGuard</span>
              <span class="text-[11px] text-slate-600 mt-1 leading-snug">
                High-speed encrypted VPN tunnel for private direct access to host and containers.
              </span>
            </button>

            <button
              type="button"
              @click="form.tunnel_type = 'tailscale'"
              class="flex flex-col text-left p-3.5 rounded-xl border transition-all cursor-pointer relative"
              :class="
                form.tunnel_type === 'tailscale'
                  ? 'border-violet-500 bg-violet-950/40 text-violet-300 shadow-md ring-1 ring-violet-500'
                  : 'border-slate-200 bg-white/40 text-slate-600 hover:border-slate-300'
              "
            >
              <div class="flex items-center justify-between">
                <Radio class="h-5 w-5 text-violet-400" />
                <span class="text-[9px] uppercase px-1.5 py-0.2 rounded bg-violet-500/20 text-violet-300 font-mono">Mesh</span>
              </div>
              <span class="text-xs font-bold font-mono mt-2 text-slate-900">Tailscale</span>
              <span class="text-[11px] text-slate-600 mt-1 leading-snug">
                Zero-config mesh network. Connects your machines securely over WireGuard under your tailnet.
              </span>
            </button>

            <button
              type="button"
              @click="form.tunnel_type = 'none'"
              class="flex flex-col text-left p-3.5 rounded-xl border transition-all cursor-pointer relative"
              :class="
                form.tunnel_type === 'none'
                  ? 'border-slate-400 bg-white/80 text-slate-800 shadow-md ring-1 ring-slate-600'
                  : 'border-slate-200 bg-white/40 text-slate-600 hover:border-slate-300'
              "
            >
              <div class="flex items-center justify-between">
                <Wifi class="h-5 w-5 text-slate-600" />
                <span class="text-[9px] uppercase px-1.5 py-0.2 rounded bg-slate-100 text-slate-600 font-mono">Local</span>
              </div>
              <span class="text-xs font-bold font-mono mt-2 text-slate-900">No remote access</span>
              <span class="text-[11px] text-slate-600 mt-1 leading-snug">
                Manage your containers locally. No tunnel or VPN is required; existing local access stays as configured.
              </span>
            </button>
          </div>

          <!-- Cloudflare Setup Sub-fields -->
          <div v-if="form.tunnel_type === 'cloudflare'" class="p-4 rounded-xl border border-slate-200 bg-white/40 space-y-4">
            <div class="flex items-center gap-3 border-b border-slate-200 pb-3 text-xs">
              <label class="text-slate-600 font-medium">Cloudflare Setup Method:</label>
              <div class="flex gap-2">
                <button
                  type="button"
                  @click="form.cloudflare_mode = 'token'"
                  class="px-2.5 py-1 rounded text-xs font-mono font-semibold"
                  :class="form.cloudflare_mode === 'token' ? 'bg-cyan-500/20 text-cyan-700 border border-cyan-500/40' : 'text-slate-600 hover:text-slate-800'"
                >
                  Tunnel Token (Quick)
                </button>
                <button
                  type="button"
                  @click="form.cloudflare_mode = 'api'"
                  class="px-2.5 py-1 rounded text-xs font-mono font-semibold"
                  :class="form.cloudflare_mode === 'api' ? 'bg-cyan-500/20 text-cyan-700 border border-cyan-500/40' : 'text-slate-600 hover:text-slate-800'"
                >
                  API Token & DNS Automation
                </button>
                <button
                  type="button"
                  @click="form.cloudflare_mode = 'existing'"
                  class="px-2.5 py-1 rounded text-xs font-mono font-semibold"
                  :class="form.cloudflare_mode === 'existing' ? 'bg-cyan-500/20 text-cyan-700 border border-cyan-500/40' : 'text-slate-600 hover:text-slate-800'"
                >
                  Mounted Credentials
                </button>
              </div>
            </div>

            <!-- Option A: Tunnel Token -->
            <div v-if="form.cloudflare_mode === 'token'" class="space-y-2">
              <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-600 font-mono">
                Cloudflare Tunnel Run Token
              </label>
              <input
                v-model="form.cloudflare_token"
                type="text"
                placeholder="eyJhIjoiNj..."
                class="cyber-input text-xs font-mono"
              />
              <p class="text-[11px] text-slate-600">
                Obtained from Cloudflare Zero Trust Dashboard → Networks → Tunnels → Copy command token.
              </p>
            </div>

            <!-- Option B: Cloudflare API Token -->
            <div v-else-if="form.cloudflare_mode === 'api'" class="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div class="space-y-1 sm:col-span-2">
                <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-600 font-mono">
                  Cloudflare API Token
                </label>
                <input
                  v-model="form.cloudflare_api_token"
                  type="password"
                  placeholder="Bearer Token with Cloudflare Tunnel & DNS Edit permissions"
                  class="cyber-input text-xs font-mono"
                />
              </div>
              <div class="space-y-1">
                <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-600 font-mono">
                  Cloudflare Account ID
                </label>
                <input
                  v-model="form.cloudflare_account_id"
                  type="text"
                  placeholder="32-character hexadecimal account ID"
                  class="cyber-input text-xs font-mono"
                />
              </div>
              <div class="space-y-1">
                <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-600 font-mono">
                  Tunnel Name
                </label>
                <input
                  v-model="form.cloudflare_tunnel_name"
                  type="text"
                  placeholder="docktun"
                  class="cyber-input text-xs font-mono"
                />
              </div>
            </div>

            <!-- Option C: Mounted Credentials -->
            <div v-else class="text-xs font-mono text-slate-600 space-y-1">
              <p class="text-emerald-600 font-semibold">✓ DockTun will use files mounted to <code class="text-slate-800">/etc/cloudflared/config.yml</code> and credentials JSON.</p>
              <p class="text-[11px] text-slate-500">No additional tokens required.</p>
            </div>
          </div>

          <!-- Tailscale Setup -->
          <div v-else-if="form.tunnel_type === 'tailscale'" class="p-4 rounded-xl border border-slate-200 bg-white/40 space-y-2">
            <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-600 font-mono">
              Tailscale Auth Key (Reusable or Ephemeral)
            </label>
            <input
              v-model="form.tailscale_auth_key"
              type="password"
              placeholder="tskey-auth-..."
              class="cyber-input text-xs font-mono"
            />
            <p class="text-[11px] text-slate-600">
              Generated in Tailscale Admin Console → Settings → Personal Settings → Keys.
            </p>
          </div>

          <!-- WireGuard Setup -->
          <div v-else-if="form.tunnel_type === 'wireguard'" class="p-4 rounded-xl border border-slate-200 bg-white/40 grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div class="space-y-1">
              <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-600 font-mono">
                Server URL / Public IP
              </label>
              <input
                v-model="form.wireguard_server_url"
                type="text"
                placeholder="vpn.example.com or auto"
                class="cyber-input text-xs font-mono"
              />
            </div>
            <div class="space-y-1">
              <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-600 font-mono">
                WireGuard Port
              </label>
              <input
                v-model.number="form.wireguard_port"
                type="number"
                placeholder="51820"
                class="cyber-input text-xs font-mono"
              />
            </div>
          </div>
        </div>

        <!-- ================================================================= -->
        <!-- STEP 3: DISCOVERED CONTAINERS & IMPORT                            -->
        <!-- ================================================================= -->
        <div v-else-if="step === 3" class="space-y-5 animate-fade-in">
          <div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-2">
            <div>
              <h2 class="text-lg font-bold text-slate-900 flex items-center gap-2">
                <Server class="h-5 w-5 text-cyan-600" />
                <span>Step 3: Discover & Import Existing Containers</span>
              </h2>
              <p class="text-xs text-slate-600 mt-1">
                Found {{ selectableContainers.length }} previous container(s) running on your Docker daemon. Select which to import into DockTun.
              </p>
            </div>

            <div class="flex items-center gap-2">
              <button
                type="button"
                @click="selectAllContainers"
                class="text-xs text-cyan-600 hover:text-cyan-700 underline font-mono"
              >
                Select All
              </button>
              <span class="text-slate-600">|</span>
              <button
                type="button"
                @click="deselectAllContainers"
                class="text-xs text-slate-600 hover:text-slate-800 underline font-mono"
              >
                Clear
              </button>
            </div>
          </div>

          <!-- Import Toggles -->
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-3 p-3.5 rounded-xl border border-slate-200 bg-white/60">
            <label class="flex items-start gap-2.5 cursor-pointer">
              <input
                type="checkbox"
                v-model="form.connect_network"
                class="h-4 w-4 mt-0.5 rounded border-slate-300 bg-slate-50 text-cyan-500 focus:ring-cyan-500"
              />
              <div class="space-y-0.5">
                <span class="text-xs font-bold text-slate-800">Connect to Tunnel Network</span>
                <p class="text-[11px] text-slate-600">
                  Optionally connects selected containers to the DockTun network. This does not publish them or enable remote access.
                </p>
              </div>
            </label>

            <label class="flex items-start gap-2.5 cursor-pointer">
              <input
                type="checkbox"
                v-model="form.auto_import_stacks"
                class="h-4 w-4 mt-0.5 rounded border-slate-300 bg-slate-50 text-indigo-500 focus:ring-indigo-500"
              />
              <div class="space-y-0.5">
                <span class="text-xs font-bold text-slate-800">Auto-Register Compose Stacks</span>
                <p class="text-[11px] text-slate-600">
                  Imports Compose working directories into DockTun so you can edit YAML and restart stacks directly.
                </p>
              </div>
            </label>
          </div>

          <!-- Containers List -->
          <div v-if="selectableContainers.length > 0" class="max-h-72 overflow-y-auto space-y-2 pr-1">
            <div
              v-for="c in selectableContainers"
              :key="c.id"
              class="flex items-center justify-between p-3 rounded-lg border transition-all cursor-pointer"
              :class="
                form.selected_container_ids[c.id]
                  ? 'border-cyan-500/60 bg-cyan-50/20'
                  : 'border-slate-200 bg-white/30 opacity-70 hover:opacity-100'
              "
              @click="form.selected_container_ids[c.id] = !form.selected_container_ids[c.id]"
            >
              <div class="flex items-center gap-3">
                <input
                  type="checkbox"
                  v-model="form.selected_container_ids[c.id]"
                  class="h-4 w-4 rounded border-slate-300 bg-slate-50 text-cyan-500 focus:ring-cyan-500"
                  @click.stop
                />
                <div>
                  <div class="text-xs font-bold text-slate-900 font-mono flex items-center gap-2">
                    <span>{{ c.name }}</span>
                    <span
                      class="text-[9px] px-1.5 py-0.2 rounded font-mono font-semibold"
                      :class="c.running ? 'bg-emerald-500/20 text-emerald-600' : 'bg-slate-100 text-slate-600'"
                    >
                      {{ c.running ? 'Running' : 'Stopped' }}
                    </span>
                  </div>
                  <div class="text-[11px] text-slate-600 font-mono truncate max-w-sm">
                    {{ c.image }} • Ports: {{ c.ports || 'internal' }}
                  </div>
                </div>
              </div>

              <div class="text-right text-xs font-mono">
                <span class="rounded bg-slate-100 px-2 py-0.5 text-[10px] text-indigo-700 border border-slate-300">
                  Stack: {{ c.suggested_stack }}
                </span>
              </div>
            </div>
          </div>

          <div v-else class="text-center py-10 text-slate-500 font-mono text-xs border border-dashed border-slate-200 rounded-xl">
            // No third-party containers found on this Docker daemon yet.
            <div class="text-[11px] text-slate-600 mt-1">You can deploy new containers using templates right after setup.</div>
          </div>
        </div>

        <!-- ================================================================= -->
        <!-- STEP 4: REVIEW & LAUNCH                                           -->
        <!-- ================================================================= -->
        <div v-else-if="step === 4" class="space-y-5 animate-fade-in">
          <div>
            <h2 class="text-lg font-bold text-slate-900 flex items-center gap-2">
              <Rocket class="h-5 w-5 text-cyan-600" />
              <span>Step 4: Review Configuration & Launch</span>
            </h2>
            <p class="text-xs text-slate-600 mt-1">
              Verify your setup preferences before DockTun initializes the environment and launches your dashboard.
            </p>
          </div>

          <!-- Summary Cards -->
          <div class="grid grid-cols-1 sm:grid-cols-3 gap-3 font-mono text-xs">
            <div class="p-3.5 rounded-xl border border-slate-200 bg-white/60 space-y-1">
              <span class="text-slate-600 uppercase text-[10px]">Admin Account</span>
              <div class="text-sm font-bold text-slate-900">{{ form.admin_username }}</div>
              <div class="text-[11px] text-emerald-600">✓ Password Protected</div>
            </div>

            <div class="p-3.5 rounded-xl border border-slate-200 bg-white/60 space-y-1">
              <span class="text-slate-600 uppercase text-[10px]">Remote Access</span>
              <div class="text-sm font-bold text-cyan-700 capitalize">{{ form.tunnel_type }}</div>
              <div class="text-[11px] text-slate-600">
                {{ form.tunnel_type === 'cloudflare' ? 'Zero Trust Edge Tunnel' : form.tunnel_type === 'none' ? 'No remote access' : 'Encrypted VPN' }}
              </div>
            </div>

            <div class="p-3.5 rounded-xl border border-slate-200 bg-white/60 space-y-1">
              <span class="text-slate-600 uppercase text-[10px]">Imported Containers</span>
              <div class="text-sm font-bold text-indigo-700">{{ selectedContainerCount }} Selected</div>
              <div class="text-[11px]" :class="form.connect_network ? 'text-cyan-600' : 'text-slate-500'">
                {{ form.connect_network ? 'Bridge Network Attached' : 'Network Unmodified' }}
              </div>
            </div>
          </div>

          <!-- Optional Notification Webhook Section -->
          <div class="p-4 rounded-xl border border-slate-200 bg-white/40 space-y-3">
            <label class="flex items-center gap-2.5 cursor-pointer">
              <input
                type="checkbox"
                v-model="form.enable_notifications"
                class="h-4 w-4 rounded border-slate-300 bg-slate-50 text-cyan-500 focus:ring-cyan-500"
              />
              <span class="text-xs font-bold text-slate-800 flex items-center gap-1.5">
                <Bell class="h-3.5 w-3.5 text-cyan-600" />
                <span>Configure Incident Webhook (Optional)</span>
              </span>
            </label>

            <div v-if="form.enable_notifications" class="space-y-3 pl-6 pt-1">
              <div class="flex gap-3 text-xs">
                <label v-for="t in ['discord', 'telegram', 'slack', 'ntfy']" :key="t" class="flex items-center gap-1.5 font-mono capitalize cursor-pointer">
                  <input type="radio" v-model="form.notification_type" :value="t" class="accent-cyan-400" />
                  <span>{{ t }}</span>
                </label>
              </div>
              <input
                v-model="form.webhook_url"
                type="text"
                :placeholder="form.notification_type === 'ntfy' ? 'https://ntfy.sh/my_topic' : 'Webhook URL...'"
                class="cyber-input text-xs font-mono"
              />
            </div>
          </div>

          <!-- Submitting Progress Banner -->
          <div v-if="submitting" class="p-4 rounded-xl border border-cyan-500/50 bg-cyan-50/30 text-cyan-700 flex items-center gap-3 text-xs font-mono">
            <RotateCcw class="h-5 w-5 animate-spin text-cyan-600 shrink-0" />
            <div class="space-y-0.5">
              <div class="font-bold">INITIALIZING DOCKTUN...</div>
              <div>{{ submitStep }}</div>
            </div>
          </div>
        </div>

        <!-- Wizard Navigation Footer -->
        <div class="flex items-center justify-between pt-4 border-t border-slate-200">
          <button
            v-if="step > (systemStatus.has_admin_user ? 2 : 1)"
            type="button"
            @click="step--"
            :disabled="submitting"
            class="cyber-btn-secondary text-xs flex items-center gap-1.5 py-2 px-4"
          >
            <ChevronLeft class="h-3.5 w-3.5" />
            <span>Back</span>
          </button>
          <div v-else></div>

          <div class="flex items-center gap-3">
            <button
              v-if="step < 4"
              type="button"
              @click="step++"
              :disabled="step === 1 && !passwordValid"
              class="cyber-btn text-xs flex items-center gap-1.5 py-2 px-5 text-slate-900 bg-cyan-600/80 hover:bg-cyan-600 border-cyan-500 disabled:opacity-40"
            >
              <span>Next Step</span>
              <ChevronRight class="h-3.5 w-3.5" />
            </button>

            <button
              v-else
              type="button"
              @click="finishSetup"
              :disabled="submitting || !passwordValid"
              class="cyber-btn text-xs flex items-center gap-2 py-2 px-6 text-slate-900 bg-gradient-to-r from-cyan-600 to-indigo-600 hover:from-cyan-500 hover:to-indigo-500 border-cyan-400 font-bold shadow-lg"
            >
              <RotateCcw v-if="submitting" class="h-4 w-4 animate-spin" />
              <Rocket v-else class="h-4 w-4" />
              <span>{{ submitting ? 'Launching...' : 'Complete Setup & Launch DockTun' }}</span>
            </button>
          </div>
        </div>
      </div>
    </main>

    <!-- Footer -->
    <footer class="max-w-4xl mx-auto w-full text-center text-xs text-slate-500 font-mono py-2">
      DockTun • Self-hosted Cloudflare Tunnel & Docker Compose Management Dashboard
    </footer>
  </div>
</template>

<style scoped>
@keyframes fadeIn {
  from { opacity: 0; transform: translateY(4px); }
  to { opacity: 1; transform: translateY(0); }
}
.animate-fade-in {
  animation: fadeIn 0.15s ease-out forwards;
}
</style>
