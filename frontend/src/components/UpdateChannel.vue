<script setup lang="ts">
import { onMounted, ref, computed } from 'vue'
import { useApi } from '../composables/useApi'
import {
  RefreshCw,
  ArrowUpCircle,
  CheckCircle2,
  AlertCircle,
  Sparkles,
  ShieldCheck,
  Zap,
  Clock,
  Layers,
  X,
  ExternalLink
} from 'lucide-vue-next'

const { apiFetch } = useApi()

const channelState = ref<any>(null)
const updateState = ref<any>(null)
const channel = ref('stable')
const busy = ref(false)
const checking = ref(false)
const installing = ref(false)
const reconnecting = ref(false)
const showConfirmModal = ref(false)
const error = ref('')
const message = ref('')

async function fetchStatus() {
  try {
    const [chanData, statusData] = await Promise.all([
      apiFetch('/api/self-update/channel'),
      apiFetch('/api/self-update')
    ])
    channelState.value = chanData
    channel.value = chanData.channel || 'stable'
    updateState.value = statusData
  } catch (e: any) {
    error.value = e.message
  }
}

onMounted(() => {
  fetchStatus()
})

async function saveChannel() {
  busy.value = true
  error.value = ''
  message.value = ''
  try {
    channelState.value = await apiFetch('/api/self-update/channel', {
      method: 'PUT',
      body: JSON.stringify({ channel: channel.value })
    })
    message.value = 'Update channel updated. Click Check for Updates to evaluate candidates.'
    const statusData = await apiFetch('/api/self-update')
    updateState.value = statusData
  } catch (e: any) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}

async function checkForUpdates() {
  checking.value = true
  error.value = ''
  message.value = ''
  try {
    const res = await apiFetch('/api/self-update/check', { method: 'POST' })
    updateState.value = res
    if (res.available) {
      message.value = 'A new DockTun update is available and verified!'
    } else {
      message.value = 'DockTun is running the latest verified release.'
    }
  } catch (e: any) {
    error.value = e.message
  } finally {
    checking.value = false
  }
}

async function startInstall() {
  showConfirmModal.value = false
  installing.value = true
  error.value = ''
  message.value = 'Initiating update worker. State and database are being preserved...'

  try {
    const res = await apiFetch('/api/self-update/install', { method: 'POST' })
    updateState.value = res
    pollReconnection()
  } catch (e: any) {
    error.value = e.message
    installing.value = false
  }
}

function pollReconnection() {
  reconnecting.value = true
  let attempts = 0
  const maxAttempts = 60

  const interval = setInterval(async () => {
    attempts++
    try {
      const res = await fetch('/healthz', { cache: 'no-store' })
      if (res.ok) {
        clearInterval(interval)
        message.value = 'Update successful! Reloading dashboard...'
        setTimeout(() => {
          window.location.reload()
        }, 1500)
      }
    } catch {
      // Waiting for container to finish restarting
    }

    if (attempts >= maxAttempts) {
      clearInterval(interval)
      reconnecting.value = false
      installing.value = false
      error.value = 'Reconnection timed out. Please refresh the page manually once the container restarts.'
    }
  }, 2500)
}

const isUpdateAvailable = computed(() => {
  return updateState.value?.available === true
})

const lastCheckedText = computed(() => {
  if (!updateState.value?.updated_at) return ''
  try {
    const d = new Date(updateState.value.updated_at)
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  } catch {
    return ''
  }
})
</script>

<template>
  <section class="cyber-card space-y-5">
    <!-- Header -->
    <div class="flex flex-wrap items-center justify-between gap-3 border-b border-slate-200 pb-3">
      <div>
        <div class="flex items-center gap-2">
          <Sparkles class="h-5 w-5 text-cyan-600" />
          <h2 class="text-base font-bold text-slate-900">DockTun System Updates</h2>
        </div>
        <p class="text-xs text-slate-600 mt-0.5">
          Check for updates, switch release channels, and upgrade DockTun with zero downtime for your containers.
        </p>
      </div>

      <!-- Main Action: Check for updates -->
      <div class="flex items-center gap-2">
        <button
          @click="checkForUpdates"
          :disabled="checking || installing || reconnecting"
          class="cyber-btn flex items-center gap-1.5 text-xs text-cyan-600 border-cyan-500 bg-cyan-50/70 hover:bg-cyan-100 font-semibold"
        >
          <RefreshCw class="h-3.5 w-3.5" :class="checking ? 'animate-spin' : ''" />
          <span>{{ checking ? 'Checking Registry…' : 'Check for Updates' }}</span>
        </button>
      </div>
    </div>

    <!-- Active Version & Channel Overview Cards -->
    <div class="grid grid-cols-1 md:grid-cols-3 gap-3">
      <!-- Current Image / Target -->
      <div class="rounded-lg border border-slate-200 bg-slate-50 p-3.5 space-y-1 font-mono text-xs">
        <div class="flex items-center justify-between text-slate-500 font-sans text-[11px]">
          <span>Current Target Image</span>
          <Layers class="h-3.5 w-3.5 text-slate-400" />
        </div>
        <div class="font-bold text-slate-800 truncate" :title="channelState?.image || updateState?.image">
          {{ channelState?.image || updateState?.image || 'ghcr.io/ooflyeyesoo/docktun:latest' }}
        </div>
        <div class="text-[10px] text-slate-500 font-sans flex items-center gap-1">
          <ShieldCheck class="h-3 w-3 text-emerald-600" />
          <span>{{ channelState?.signed_updates ? 'Official Cosign Verified' : 'Operator Managed' }}</span>
        </div>
      </div>

      <!-- Update Status Card -->
      <div class="rounded-lg border border-slate-200 bg-slate-50 p-3.5 space-y-1 text-xs">
        <div class="flex items-center justify-between text-slate-500 text-[11px]">
          <span>System Status</span>
          <Clock class="h-3.5 w-3.5 text-slate-400" />
        </div>
        <div class="flex items-center gap-2 font-bold">
          <span
            v-if="isUpdateAvailable"
            class="inline-flex items-center gap-1.5 rounded-full bg-amber-100 text-amber-700 px-2 py-0.5 text-xs"
          >
            <ArrowUpCircle class="h-3.5 w-3.5 text-amber-600" />
            <span>Update Available</span>
          </span>
          <span
            v-else-if="updateState?.checked"
            class="inline-flex items-center gap-1.5 rounded-full bg-emerald-100 text-emerald-700 px-2 py-0.5 text-xs"
          >
            <CheckCircle2 class="h-3.5 w-3.5 text-emerald-600" />
            <span>Up to Date</span>
          </span>
          <span v-else class="text-slate-600">
            Click Check to scan
          </span>
        </div>
        <div class="text-[10px] text-slate-500">
          <span v-if="lastCheckedText">Last checked today at {{ lastCheckedText }}</span>
          <span v-else>No check recorded yet</span>
        </div>
      </div>

      <!-- Channel Selector -->
      <div class="rounded-lg border border-slate-200 bg-slate-50 p-3.5 space-y-1.5 text-xs">
        <div class="text-[11px] text-slate-500">Release Channel</div>
        <div class="flex items-center gap-2">
          <select v-model="channel" class="cyber-input py-1 text-xs font-semibold">
            <option value="stable">Stable (Reviewed)</option>
            <option value="beta">Beta (Pre-release)</option>
            <option value="edge">Edge (Main CI)</option>
          </select>
          <button
            @click="saveChannel"
            :disabled="busy || !channelState || channel === channelState?.channel"
            class="cyber-btn-secondary py-1 px-2.5 text-xs shrink-0"
          >
            Save
          </button>
        </div>
      </div>
    </div>

    <!-- UPDATE AVAILABLE CALLOUT BANNER -->
    <div
      v-if="isUpdateAvailable && !installing && !reconnecting"
      class="rounded-xl border border-amber-400/80 bg-gradient-to-r from-amber-50 to-orange-50 p-4 shadow-sm space-y-3"
    >
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex items-center gap-3">
          <div class="rounded-lg bg-amber-500/20 p-2 text-amber-600 border border-amber-500/30">
            <ArrowUpCircle class="h-6 w-6" />
          </div>
          <div>
            <h3 class="text-sm font-bold text-slate-900">New DockTun Build Ready to Install</h3>
            <p class="text-xs text-slate-600 font-mono mt-0.5">
              Target: {{ updateState.image }}
            </p>
          </div>
        </div>

        <button
          @click="showConfirmModal = true"
          class="cyber-btn flex items-center gap-2 text-xs text-white border-amber-600 bg-gradient-to-r from-amber-600 to-orange-600 hover:from-amber-500 hover:to-orange-500 font-bold shadow-md shadow-amber-900/20 py-2 px-4"
        >
          <Zap class="h-4 w-4" />
          <span>Install Update Now</span>
        </button>
      </div>

      <p class="text-xs text-slate-700 leading-relaxed border-t border-amber-200/60 pt-2">
        DockTun will safely stage the new image, transfer active tunnel credentials and database records, cleanly swap the management container, and gracefully reconnect. Your deployed projects and application containers will remain running uninterrupted.
      </p>
    </div>

    <!-- INSTALLATION / RECONNECTION IN PROGRESS -->
    <div
      v-if="installing || reconnecting"
      class="rounded-xl border border-cyan-400/80 bg-gradient-to-r from-cyan-50 to-blue-50 p-5 space-y-3 shadow-sm"
    >
      <div class="flex items-center gap-3">
        <RefreshCw class="h-6 w-6 animate-spin text-cyan-600 shrink-0" />
        <div>
          <h3 class="text-sm font-bold text-slate-900">
            {{ reconnecting ? 'DockTun is Reconnecting…' : 'Installing DockTun Update…' }}
          </h3>
          <p class="text-xs text-slate-600">
            {{ message || 'Swapping container and awaiting health check. This typically takes 5–15 seconds.' }}
          </p>
        </div>
      </div>
      <div class="w-full bg-slate-200 rounded-full h-1.5 overflow-hidden">
        <div class="bg-cyan-600 h-1.5 rounded-full animate-pulse w-full"></div>
      </div>
    </div>

    <!-- Messages / Alerts -->
    <div v-if="error" class="rounded-lg border border-red-500/40 bg-red-50 p-3 text-xs text-red-700 flex items-start gap-2">
      <AlertCircle class="h-4 w-4 text-red-600 shrink-0 mt-0.5" />
      <div>{{ error }}</div>
    </div>

    <div v-else-if="message && !isUpdateAvailable && !installing && !reconnecting" class="rounded-lg border border-emerald-500/40 bg-emerald-50 p-3 text-xs text-emerald-800 flex items-start gap-2">
      <CheckCircle2 class="h-4 w-4 text-emerald-600 shrink-0 mt-0.5" />
      <div>{{ message }}</div>
    </div>

    <!-- INSTALL CONFIRMATION MODAL -->
    <div
      v-if="showConfirmModal"
      class="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/60 p-4 backdrop-blur-xs"
      @click.self="showConfirmModal = false"
    >
      <div class="w-full max-w-md rounded-xl border border-amber-500/40 bg-white p-6 shadow-xl space-y-4">
        <div class="flex items-start justify-between border-b border-slate-200 pb-3">
          <div class="flex items-center gap-2.5 text-amber-700 font-bold text-sm">
            <ArrowUpCircle class="h-5 w-5 text-amber-600" />
            <span>Confirm In-App DockTun Update</span>
          </div>
          <button @click="showConfirmModal = false" class="text-slate-400 hover:text-slate-600">
            <X class="h-5 w-5" />
          </button>
        </div>

        <div class="text-xs text-slate-700 space-y-2 leading-relaxed">
          <p>
            You are about to update DockTun to the latest image on the <strong class="font-mono text-slate-900">{{ channel }}</strong> channel:
          </p>
          <div class="rounded-lg border border-slate-200 bg-slate-50 p-2.5 font-mono text-[11px] text-cyan-800 break-all">
            {{ updateState?.image }}
          </div>
          <ul class="list-disc list-inside space-y-1 text-slate-600 pt-1">
            <li>Database, credentials, and settings are preserved.</li>
            <li>All running containers & tunnels remain online.</li>
            <li>The web dashboard will briefly disconnect and auto-reload.</li>
          </ul>
        </div>

        <div class="flex items-center justify-end gap-2 pt-2">
          <button @click="showConfirmModal = false" class="cyber-btn-secondary text-xs">
            Cancel
          </button>
          <button
            @click="startInstall"
            class="cyber-btn flex items-center gap-1.5 text-xs text-white border-amber-600 bg-amber-600 hover:bg-amber-500 font-bold shadow-sm"
          >
            <Zap class="h-3.5 w-3.5" />
            <span>Proceed with Update</span>
          </button>
        </div>
      </div>
    </div>
  </section>
</template>
