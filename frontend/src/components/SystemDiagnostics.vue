<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useApi } from '../composables/useApi'
const { apiFetch } = useApi()
const report = ref<{ checked_at: string; checks: Array<{name: string; ok: boolean; message: string}> } | null>(null)
const loading = ref(false)
const error = ref('')
async function refresh() {
  if (loading.value) return
  loading.value = true
  error.value = ''
  try { report.value = await apiFetch('/api/diagnostics') }
  catch (e: any) { error.value = e.message || 'Unable to load diagnostics'; report.value = null }
  finally { loading.value = false }
}
onMounted(refresh)
</script>

<template>
  <section class="cyber-card space-y-4" aria-label="System diagnostics" aria-live="polite" :aria-busy="loading">
    <div class="flex items-center justify-between gap-3">
      <h2 class="font-semibold text-slate-900">System diagnostics</h2>
      <button class="cyber-btn-secondary" :disabled="loading" @click="refresh">{{ loading ? 'Checking…' : 'Check again' }}</button>
    </div>
    <p class="text-sm text-muted">Checks run from DockTun. A ready dashboard can still have an unavailable Docker engine.</p>
    <p v-if="error" role="alert" class="text-danger">{{ error }}</p>
    <div v-for="check in report?.checks || []" :key="check.name" class="border-t border-border pt-3">
      <div class="flex justify-between gap-3"><span class="font-medium">{{ check.name }}</span><span :class="check.ok ? 'text-emerald-700' : 'text-danger'">{{ check.ok ? 'Available' : 'Needs attention' }}</span></div>
      <p class="text-sm text-muted mt-1">{{ check.message }}</p>
    </div>
    <p v-if="report" class="text-xs text-muted">Last checked {{ new Date(report.checked_at).toLocaleString() }}. Refresh to check the current state.</p>
  </section>
</template>
