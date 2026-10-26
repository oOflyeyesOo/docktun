<script setup lang="ts">
import { ref, reactive } from 'vue'
import { RotateCcw, ShieldCheck, KeyRound, Globe, CheckCircle2, AlertCircle, Sparkles } from 'lucide-vue-next'
import SelfUpdate from '../components/SelfUpdate.vue'
import SystemDiagnostics from '../components/SystemDiagnostics.vue'
import { useApi } from '../composables/useApi'

const { apiFetch } = useApi()

// Password State
const form = reactive({ current_password: '', new_password: '', confirm: '' })
const message = ref<{ type: string; text: string } | null>(null)
const loading = ref(false)

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

</script>

<template>
  <div class="space-y-6">
    <div>
      <h1 class="cyber-heading text-xl lg:text-2xl">Settings</h1>
      <p class="text-xs text-muted font-mono mt-1">Manage DockTun updates, account security, and diagnostics.</p>
    </div>

    <SelfUpdate />
    <SystemDiagnostics />

    <div class="grid grid-cols-1 lg:grid-cols-2 gap-6">

      <!-- 2. Cloudflare Tunnel Environment Overview -->


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
          <CheckCircle2 v-if="message.type === 'success'" class="w-4 h-4 text-emerald-600" />
          <AlertCircle v-else class="w-4 h-4 text-red-600" />
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
          <Sparkles class="w-4 h-4 text-cyan-600" />
          <h2 class="text-xs font-bold uppercase tracking-wider text-slate-800">First-Run Setup & Onboarding</h2>
        </div>

        <p class="text-xs text-muted leading-relaxed">
          Revisit initial Docker discovery and import settings.
        </p>

        <div class="p-3 rounded border border-border/60 bg-void/50 text-xs font-mono space-y-1">
          <span class="text-cyan-700 font-bold block">Guided Reconfiguration Wizard</span>
          <span class="text-muted text-[11px]">Exposure providers can be configured separately from the Exposure providers page.</span>
        </div>

        <router-link
          to="/setup?force=true"
          class="cyber-btn-secondary w-full text-center text-xs flex items-center justify-center gap-2 py-2 text-cyan-700 hover:text-cyan-200"
        >
          <RotateCcw class="w-3.5 h-3.5" />
          <span>Launch Setup Wizard &rarr;</span>
        </router-link>
      </div>
    </div>
  </div>
</template>
