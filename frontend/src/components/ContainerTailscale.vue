<script setup lang="ts">
import { computed, ref } from 'vue'
import { useApi } from '../composables/useApi'
const props = defineProps<{ containerId: string }>()
const { apiFetch } = useApi()
const status = ref<{ state: string; auth_url?: string; address?: string }>()
const busy = ref(false)
const error = ref('')
const loginURL = computed(() => {
  try { const u = new URL(status.value?.auth_url || ''); return u.protocol === 'https:' ? u.href : '' }
  catch { return '' }
})
async function connect() {
  busy.value = true
  error.value = ''
  try { status.value = await apiFetch(`/api/docker/containers/${encodeURIComponent(props.containerId)}/tailscale`, { method: 'POST' }) }
  catch (e: any) { error.value = e.message || 'Unable to connect this device' }
  finally { busy.value = false }
}
</script>

<template>
  <div class="rounded-lg border border-purple-200 bg-purple-50 p-3 space-y-2 text-xs">
    <router-link to="/access?tab=tailscale" class="block text-indigo-700 underline">Setup, sign-in and approval instructions →</router-link>
    <p>This app gets its own Tailscale device and private address. Only the TCP port you choose is forwarded. No subnet routes or other containers are exposed.</p>
    <button type="button" class="cyber-btn" :disabled="busy" @click="connect">{{ busy ? 'Connecting…' : status ? 'Check connection' : 'Connect this app to Tailscale' }}</button>
    <a v-if="loginURL && status?.state !== 'Running'" :href="loginURL" target="_blank" rel="noopener noreferrer" class="block underline">Sign in this app’s device →</a>
    <p v-if="status?.state === 'Running'">Connected: {{ status.address }}. Save the access settings to enable the selected port.</p>
    <p v-else-if="status">{{ status.state }}. After signing in, check the connection and save.</p>
    <p v-if="error" class="text-red-700">{{ error }}</p>
  </div>
</template>
