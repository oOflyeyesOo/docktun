<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { HardDrive, Trash2, RotateCcw, ShieldCheck, KeyRound, Globe, CheckCircle2, AlertCircle, Sparkles } from 'lucide-vue-next'
import { useApi } from '../composables/useApi'

const { apiFetch } = useApi()

// Password State
const form = reactive({ current_password: '', new_password: '', confirm: '' })
const message = ref<{ type: string; text: string } | null>(null)
const loading = ref(false)

// Docker System Disk State
const dfData = ref<any>(null)
const dfLoading = ref(false)
const pruneLoading = ref(false)
const pruneMessage = ref<string | null>(null)

// Cloudflare Tunnel & System Status
const systemStatus = ref<any>(null)

async function fetchStatus() {
  try {
    systemStatus.value = await apiFetch('/api/status')
  } catch {}
}

async function fetchDF() {
  dfLoading.value = true
  try {
    dfData.value = await apiFetch('/api/docker/system/df')
  } catch (e: any) {
    console.error('Failed to fetch Docker df', e)
  } finally {
    dfLoading.value = false
  }
}

async function pruneSystem() {
  if (!confirm('This will remove all stopped containers, unused networks, dangling images, and build cache. Proceed?')) {
    return
  }
  pruneLoading.value = true
  pruneMessage.value = null
  try {
    const res: any = await apiFetch('/api/docker/system/prune', { method: 'POST' })
    pruneMessage.value = res.output || 'System pruned successfully!'
    await fetchDF()
  } catch (e: any) {
    pruneMessage.value = 'Prune failed: ' + e.message
  } finally {
    pruneLoading.value = false
  }
}

async function changePassword() {
  if (form.new_password !== form.confirm) {
    message.value = { type: 'error', text: 'Passwords do not match' }
    return
  }
  loading.value = true
  message.value = null
  try {
    await apiFetch('/api/settings/password', { method: 'POST', body: JSON.stringify(form) })
    message.value = { type: 'success', text: 'PASSWORD UPDATED SUCCESSFULLY' }
    form.current_password = ''
    form.new_password = ''
    form.confirm = ''
  } catch (e: any) {
    message.value = { type: 'error', text: e.message }
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  fetchStatus()
  fetchDF()
})
</script>

<template>
  <div class="space-y-6">
    <div>
      <h1 class="cyber-heading text-xl lg:text-2xl">System Settings & Maintenance</h1>
      <p class="text-xs text-muted font-mono mt-1">// Manage Docker host disk space, credentials, and tunnel configuration</p>
    </div>

    <div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
      <!-- 1. Docker Host Storage & Prune (Reclaimer) -->
      <div class="cyber-card space-y-4">
        <div class="flex items-center justify-between border-b border-border pb-3">
          <div class="flex items-center gap-2">
            <HardDrive class="w-4 h-4 text-cyan-400" />
            <h2 class="text-xs font-bold uppercase tracking-wider text-gray-200">Docker Host Disk Space</h2>
          </div>
          <div class="flex items-center gap-2">
            <router-link to="/storage" class="text-xs text-indigo-400 hover:text-indigo-300 hover:underline font-mono flex items-center gap-1">
              <span>Full Storage Tab &rarr;</span>
            </router-link>
            <button @click="fetchDF" :disabled="dfLoading" class="cyber-btn-sm text-xs flex items-center gap-1">
              <RotateCcw class="w-3 h-3" :class="dfLoading ? 'animate-spin' : ''" /> Refresh
            </button>
          </div>
        </div>

        <p class="text-xs text-muted leading-relaxed">
          Reclaim disk space by cleaning up unused container layers, stopped containers, and dangling images.
        </p>

        <!-- Structured DF Items -->
        <div v-if="dfData?.items?.length" class="space-y-2">
          <div
            v-for="(item, idx) in dfData.items"
            :key="idx"
            class="flex items-center justify-between p-2.5 rounded border border-border/60 bg-void/50 text-xs font-mono"
          >
            <div>
              <span class="text-cyan-300 font-bold block">{{ item.Type || 'Resource' }}</span>
              <span class="text-muted text-[11px]">{{ item.TotalCount || 0 }} total ({{ item.Active || 0 }} active)</span>
            </div>
            <div class="text-right">
              <span class="text-gray-200 font-bold block">{{ item.Size || '0B' }}</span>
              <span class="text-emerald-400 text-[11px] font-semibold">Reclaimable: {{ item.Reclaimable || '0B' }}</span>
            </div>
          </div>
        </div>

        <!-- Raw fallback if items not parsed -->
        <pre v-else-if="dfData?.raw" class="text-xs text-muted font-mono whitespace-pre-wrap bg-void p-3 rounded border border-border/50 max-h-48 overflow-y-auto">{{ dfData.raw }}</pre>

        <div v-if="pruneMessage" class="p-3 rounded border border-cyan-800 bg-cyan-950/40 text-xs text-cyan-300 font-mono whitespace-pre-wrap">
          {{ pruneMessage }}
        </div>

        <div class="pt-2 flex items-center justify-between border-t border-border">
          <span class="text-[11px] text-muted font-mono">// Safe prune (-f)</span>
          <button
            @click="pruneSystem"
            :disabled="pruneLoading"
            class="cyber-btn flex items-center gap-2 text-amber-300 border-amber-600 bg-amber-950/40 hover:bg-amber-900/50 text-xs"
            title="Remove unused containers, networks, and images"
          >
            <RotateCcw v-if="pruneLoading" class="w-3.5 h-3.5 animate-spin" />
            <Trash2 v-else class="w-3.5 h-3.5" />
            <span>{{ pruneLoading ? 'Pruning Host...' : 'Prune Unused Resources' }}</span>
          </button>
        </div>
      </div>

      <!-- 2. Cloudflare Tunnel Environment Overview -->
      <div class="cyber-card space-y-4">
        <div class="flex items-center justify-between border-b border-border pb-3">
          <div class="flex items-center gap-2">
            <Globe class="w-4 h-4 text-cyan-400" />
            <h2 class="text-xs font-bold uppercase tracking-wider text-gray-200">Cloudflare Tunnel Status</h2>
          </div>
          <router-link to="/access?tab=cloudflare" class="text-xs text-indigo-400 hover:underline font-mono">
            Full Config &rarr;
          </router-link>
        </div>

        <div class="space-y-2.5 text-xs font-mono">
          <div class="flex items-center justify-between p-2.5 rounded bg-void/50 border border-border/50">
            <span class="text-muted">Tunnel Identity:</span>
            <span class="text-cyan-300 font-bold truncate max-w-[200px]" :title="systemStatus?.tunnel_id || 'Active'">
              {{ systemStatus?.tunnel_id || systemStatus?.tunnel_name || 'docktun' }}
            </span>
          </div>

          <div class="flex items-center justify-between p-2.5 rounded bg-void/50 border border-border/50">
            <span class="text-muted">Protocol & Architecture:</span>
            <span class="text-slate-200 font-bold">{{ systemStatus?.protocol || 'HTTP/2' }} • 2-Container Unified</span>
          </div>

          <div class="flex items-center justify-between p-2.5 rounded bg-void/50 border border-border/50">
            <span class="text-muted">High Availability Conns:</span>
            <span class="text-emerald-400 font-bold">{{ systemStatus?.ha_connections || 4 }} Edge Links</span>
          </div>

          <div class="flex items-center justify-between p-2.5 rounded bg-void/50 border border-border/50">
            <span class="text-muted">Active Ingress Hostnames:</span>
            <span class="text-cyan-400 font-bold">{{ systemStatus?.hostnames?.length || 0 }} Domains</span>
          </div>
        </div>

        <div class="pt-2 border-t border-border">
          <router-link
            to="/access?tab=cloudflare"
            class="cyber-btn-secondary w-full text-center text-xs flex items-center justify-center gap-2 py-2 text-indigo-300"
          >
            <ShieldCheck class="w-3.5 h-3.5 text-indigo-400" />
            <span>Manage Cloudflare API Keys & Ingress Rules &rarr;</span>
          </router-link>
        </div>
      </div>

      <!-- 3. Change Password -->
      <div class="cyber-card space-y-4">
        <div class="flex items-center gap-2 border-b border-border pb-3">
          <KeyRound class="w-4 h-4 text-neon" />
          <h2 class="text-xs text-muted uppercase tracking-widest font-mono">// Admin Password</h2>
        </div>

        <div
          v-if="message"
          class="border p-3 text-xs font-mono flex items-center gap-2"
          :class="message.type === 'success' ? 'border-neon text-neon bg-neon/5' : 'border-danger text-danger bg-danger/5'"
        >
          <CheckCircle2 v-if="message.type === 'success'" class="w-4 h-4 text-emerald-400" />
          <AlertCircle v-else class="w-4 h-4 text-red-400" />
          <span>{{ message.text }}</span>
        </div>

        <div class="space-y-3">
          <div class="space-y-1">
            <label class="text-[10px] text-muted uppercase tracking-widest font-mono">Current Password</label>
            <input v-model="form.current_password" type="password" class="cyber-input w-full" />
          </div>
          <div class="space-y-1">
            <label class="text-[10px] text-muted uppercase tracking-widest font-mono">New Password</label>
            <input v-model="form.new_password" type="password" class="cyber-input w-full" />
          </div>
          <div class="space-y-1">
            <label class="text-[10px] text-muted uppercase tracking-widest font-mono">Confirm New Password</label>
            <input v-model="form.confirm" type="password" class="cyber-input w-full" />
          </div>
        </div>

        <button @click="changePassword" :disabled="loading" class="cyber-btn w-full">
          {{ loading ? 'UPDATING PASSWORD...' : 'UPDATE PASSWORD' }}
        </button>
      </div>

      <!-- 4. First-Run Setup & Onboarding Wizard -->
      <div class="cyber-card space-y-4">
        <div class="flex items-center gap-2 border-b border-border pb-3">
          <Sparkles class="w-4 h-4 text-cyan-400" />
          <h2 class="text-xs font-bold uppercase tracking-wider text-gray-200">First-Run Setup & Onboarding</h2>
        </div>

        <p class="text-xs text-muted leading-relaxed">
          Need to reconfigure your Cloudflare tunnel, re-discover previous Docker containers, or switch between WireGuard and Tailscale?
        </p>

        <div class="p-3 rounded border border-border/60 bg-void/50 text-xs font-mono space-y-1">
          <span class="text-cyan-300 font-bold block">Guided Reconfiguration Wizard</span>
          <span class="text-muted text-[11px]">Walks through tunnel activation, network attachments, and container imports step-by-step.</span>
        </div>

        <router-link
          to="/setup?force=true"
          class="cyber-btn-secondary w-full text-center text-xs flex items-center justify-center gap-2 py-2 text-cyan-300 hover:text-cyan-200"
        >
          <RotateCcw class="w-3.5 h-3.5" />
          <span>Launch Setup Wizard &rarr;</span>
        </router-link>
      </div>
    </div>
  </div>
</template>
