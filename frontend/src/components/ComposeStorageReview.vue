<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useApi } from '../composables/useApi'
const props = defineProps<{ input: { name: string; path: string; compose_content: string; env_content: string; files?: Record<string, any>; compatibility?: boolean } }>()
const emit = defineEmits<{ ready: [boolean] }>()
const { apiFetch } = useApi()
const result = ref<any>(null)
const error = ref('')
const checking = ref(false)
const key = computed(() => JSON.stringify(props.input))
let revision = 0
watch(key, () => { revision++; result.value = null; error.value = ''; emit('ready', false) }, { flush: 'sync', immediate: true })
onUnmounted(() => { revision++ })
async function review() {
  const request = ++revision
  checking.value = true; result.value = null; error.value = ''; emit('ready', false)
  try {
    const response = await apiFetch('/api/projects/review', { method: 'POST', body: JSON.stringify(props.input) })
    if (request !== revision) return
    result.value = response; emit('ready', !!response.ready)
  } catch (e: any) { if (request === revision) error.value = e.message }
  finally { checking.value = false }
}
defineExpose({ review })
</script>

<template>
  <section class="rounded-xl border border-slate-200 bg-slate-50 p-4 space-y-3">
    <div class="flex items-center justify-between gap-3">
      <div><h3 class="font-semibold text-sm text-slate-900">{{ input.compatibility ? 'App checks' : 'Storage review' }}</h3><p class="text-xs text-slate-500">Check Docker-host storage and app requirements before deployment.</p></div>
      <button type="button" class="cyber-btn-secondary text-xs" :disabled="checking || !input.name.trim() || !input.compose_content.trim()" @click="review">{{ checking ? 'Checking app…' : input.compatibility ? 'Check app' : 'Review storage' }}</button>
    </div>
    <p v-if="error" role="alert" class="text-xs text-red-700">{{ error }}</p>
    <template v-if="result">
      <p class="text-xs text-slate-600">Compose folder inside DockTun: <code>{{ result.directory }}</code>. Bind sources below refer to the Docker host.</p>
      <div v-if="result.mounts?.length" class="overflow-x-auto">
        <table class="w-full text-xs text-left"><thead class="text-slate-500"><tr><th class="p-2">Service</th><th class="p-2">Host source / volume</th><th class="p-2">Container destination</th><th class="p-2">Access</th><th class="p-2">Check</th></tr></thead>
          <tbody><tr v-for="(mount,index) in result.mounts" :key="index" class="border-t border-slate-200"><td class="p-2">{{ mount.service }}</td><td class="p-2 font-mono break-all">{{ mount.source || '(anonymous volume)' }}<span class="block font-sans text-slate-500">{{ mount.type }}</span></td><td class="p-2 font-mono">{{ mount.target }}</td><td class="p-2">{{ mount.access }}</td><td class="p-2">{{ mount.status }}</td></tr></tbody>
        </table>
      </div>
      <div v-if="result.checks?.length" class="space-y-2"><div v-for="(check,index) in result.checks" :key="index" class="rounded-lg border p-2 text-xs" :class="check.status === 'failed' ? 'border-red-200 text-red-800 bg-red-50' : check.status === 'warning' ? 'border-amber-200 text-amber-800 bg-amber-50' : 'border-emerald-200 text-emerald-800 bg-emerald-50'"><strong>{{ check.service }} · {{ check.check }}</strong><p class="mt-1">{{ check.message }}</p></div></div>
      <ul v-if="result.problems?.length" role="alert" class="list-disc pl-5 text-xs text-amber-800 space-y-1"><li v-for="(problem,index) in result.problems" :key="index">{{ problem }}</li></ul>
      <p v-if="result.ready" class="text-xs font-medium text-emerald-700">Required checks passed. Review any notes above; deployment checks again.</p>
      <p v-else class="text-xs text-slate-600">Correct the paths or variables in the editor, then review again. Missing host folders are never created automatically for reviewed stacks.</p>
    </template>
    <p class="text-[11px] text-slate-500">Host checks are read-only and confirm existence and type. The app's UID/GID still determines its read/write permissions. Named volumes are managed separately by Docker.</p>
  </section>
</template>
