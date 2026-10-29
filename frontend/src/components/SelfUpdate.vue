<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { useApi } from '../composables/useApi'
const { apiFetch } = useApi()
const state = ref<any>({ phase: 'idle' })
const working = ref(false)
const error = ref('')
const reconnecting = ref(false)
const runningBuild = ref<any>(null)
const busy = computed(() => working.value || ['checking', 'pulling', 'restarting'].includes(state.value.phase))
let timer: ReturnType<typeof setInterval> | undefined
async function refresh() {
  try { state.value = await apiFetch('/api/self-update'); runningBuild.value = state.value.running; reconnecting.value = false }
  catch { if (busy.value) reconnecting.value = true }
}
async function act(action: string) {
  working.value = true; error.value = ''
  try { state.value = await apiFetch('/api/self-update/' + action, { method: 'POST' }) }
  catch (e: any) { error.value = e.message; await refresh() }
  finally { working.value = false }
}
function reload() { window.location.reload() }
onMounted(() => { refresh(); timer = setInterval(refresh, 4000) })
onUnmounted(() => { if (timer) clearInterval(timer) })
</script>

<template>
  <section class="cyber-panel p-5 space-y-3">
    <h2 class="font-semibold text-lg">Update DockTun</h2>
    <div class="rounded-lg border border-slate-200 bg-slate-50 p-3 text-sm space-y-1" aria-label="Running DockTun build">
      <p>Running revision: <code>{{ runningBuild?.revision ? runningBuild.revision.slice(0, 12) : 'Unknown / local build' }}</code></p>
      <p v-if="runningBuild?.image" class="break-all text-xs text-slate-600">Image: {{ runningBuild.image }}</p>
      <p v-if="runningBuild?.image_id" class="break-all text-xs text-slate-600">Installed image ID: {{ runningBuild.image_id }}</p>
      <p v-if="runningBuild?.error" class="text-xs text-amber-800">{{ runningBuild.error }}</p>
      <p class="text-xs text-slate-600">This identifies the running container. Pulling or rebuilding an image does not replace it until installation or recreation completes.</p>
    </div>
    <p class="text-sm text-slate-600">Install the published image here without a Git pull. Your settings, storage mounts, ports and networks are preserved. The dashboard briefly disconnects during installation.</p>
    <p role="status" class="text-sm">{{ reconnecting ? 'DockTun is restarting. Reconnecting…' : state.message || 'Check for a published update.' }}</p>
    <p v-if="error" role="alert" class="text-sm text-red-700">{{ error }}</p>
    <div class="flex flex-wrap gap-3">
      <button class="cyber-btn-secondary" :disabled="busy" @click="act('check')">{{ working ? 'Working…' : 'Check for updates' }}</button>
      <button class="cyber-btn-primary" :disabled="busy || !state.available" @click="act('install')">Install update</button>
      <button v-if="state.phase === 'complete'" class="cyber-btn-secondary" @click="reload">Reload dashboard</button>
    </div>
    <p class="text-xs text-slate-500">If the replacement fails its health check, DockTun restores the previous container. This does not undo changes to stored data made by the new version.</p>
  </section>
</template>
