<script setup lang="ts">
import { ref, reactive } from 'vue'
import { RotateCcw, ShieldCheck, KeyRound, Globe, CheckCircle2, AlertCircle, Sparkles } from 'lucide-vue-next'
import UpdateChannel from '../components/UpdateChannel.vue'
import ManagedServices from '../components/ManagedServices.vue'
import SystemDiagnostics from '../components/SystemDiagnostics.vue'
import LocalProxy from '../components/LocalProxy.vue'
import OperationsCenter from '../components/OperationsCenter.vue'
import RecoveryCenter from '../components/RecoveryCenter.vue'
import ProjectBackups from '../components/ProjectBackups.vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { useApi } from '../composables/useApi'

const { apiFetch } = useApi()
const router = useRouter(), auth = useAuthStore()

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
    auth.logout(); await router.push('/login')
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

    <UpdateChannel />
    <ManagedServices />
    <SystemDiagnostics />
    <LocalProxy />
    <ProjectBackups />
    <RecoveryCenter />
    <OperationsCenter />

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


    </div>
  </div>
</template>
