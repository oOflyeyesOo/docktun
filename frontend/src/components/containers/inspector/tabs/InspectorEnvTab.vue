<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { Sliders, RotateCcw, Save, Copy, Check, Search, X } from 'lucide-vue-next'
import { useApi } from '../../../../composables/useApi'
import { filterEnvContent } from '../../../../lib/containers-table.mjs'

const props = defineProps<{
  container: {
    id: string
    name: string
    project_id?: number
    project_path?: string
  }
  projectFilesWritable: boolean
  projectScaffolding?: boolean
}>()

const emit = defineEmits<{
  (e: 'scaffold'): void
  (e: 'restarted'): void
}>()

const { apiFetch } = useApi()

const envContent = ref('')
const envLoading = ref(false)
const envSaving = ref(false)
const envMsg = ref('')
const envLoaded = ref(false)
const searchQuery = ref('')
const copied = ref(false)

const filteredLines = computed(() => {
  return filterEnvContent(envContent.value, searchQuery.value)
})

function copyEnv() {
  if (!envContent.value) return
  navigator.clipboard.writeText(envContent.value).then(() => {
    copied.value = true
    setTimeout(() => {
      copied.value = false
    }, 2000)
  })
}

async function loadEnv() {
  if (!props.container.project_id) return
  envLoading.value = true
  envMsg.value = ''
  try {
    const res = await apiFetch<{ content?: string; error?: string } | string>(
      `/api/projects/${props.container.project_id}/env`
    )
    if (typeof res === 'string') {
      envContent.value = res
    } else {
      envContent.value = res.content || ''
    }
    envLoaded.value = true
  } catch (err: any) {
    envMsg.value = 'Failed to load .env: ' + err.message
  } finally {
    envLoading.value = false
  }
}

async function saveEnv(restart = false) {
  if (!props.projectFilesWritable || !props.container.project_id || envSaving.value) return
  envSaving.value = true
  envMsg.value = ''
  try {
    await apiFetch(`/api/projects/${props.container.project_id}/env`, {
      method: 'PUT',
      body: JSON.stringify({ content: envContent.value }),
    })
    envMsg.value = restart ? 'Saved! Restarting stack...' : 'Saved successfully!'
    if (restart) {
      await apiFetch(`/api/projects/${props.container.project_id}/action/restart`, {
        method: 'POST',
      })
      emit('restarted')
      envMsg.value = 'Saved and restarted!'
    }
  } catch (err: any) {
    envMsg.value = 'Save failed: ' + err.message
  } finally {
    envSaving.value = false
  }
}

watch(
  () => props.container.project_id,
  (newId) => {
    if (newId) {
      loadEnv()
    } else {
      envContent.value = ''
      envLoaded.value = false
    }
  }
)

onMounted(() => {
  if (props.container.project_id) {
    loadEnv()
  }
})
</script>

<template>
  <div class="space-y-4">
    <div v-if="container.project_id" class="space-y-4">
      <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-2 border-b border-slate-200 pb-3">
        <div>
          <h3 class="text-sm font-bold text-slate-900 flex items-center gap-2">
            <Sliders class="h-4 w-4 text-violet-400" />
            <span>Environment File (.env)</span>
          </h3>
          <p class="text-xs text-slate-600 font-mono">
            {{ container.project_path }}/.env
          </p>
        </div>

        <div class="flex items-center gap-2">
          <span
            v-if="envMsg"
            class="text-xs font-medium"
            :class="envMsg.startsWith('Failed') || envMsg.startsWith('Save failed') ? 'text-red-600' : 'text-emerald-600'"
          >
            {{ envMsg }}
          </span>
          <button
            @click="loadEnv"
            :disabled="envLoading"
            class="cyber-btn-secondary text-xs flex items-center gap-1"
          >
            <RotateCcw class="h-3 w-3" :class="envLoading ? 'animate-spin' : ''" />
            <span>Reload</span>
          </button>
          <button
            @click="copyEnv"
            :disabled="!envContent"
            class="cyber-btn-secondary text-xs flex items-center gap-1.5"
            title="Copy .env file"
          >
            <Check v-if="copied" class="h-3.5 w-3.5 text-emerald-600" />
            <Copy v-else class="h-3.5 w-3.5" />
            <span>{{ copied ? 'Copied' : 'Copy .env' }}</span>
          </button>
          <button
            @click="saveEnv(false)"
            :disabled="envSaving || envLoading || !envLoaded || !projectFilesWritable"
            class="cyber-btn text-xs flex items-center gap-1.5 text-cyan-700 border-cyan-600 bg-cyan-50/40 hover:bg-cyan-100/50"
          >
            <Save class="h-3.5 w-3.5" />
            <span>Save</span>
          </button>
          <button
            @click="saveEnv(true)"
            :disabled="envSaving || envLoading || !envLoaded || !projectFilesWritable"
            class="cyber-btn text-xs flex items-center gap-1.5 text-emerald-700 border-emerald-600 bg-emerald-50/40 hover:bg-emerald-100/50"
          >
            <RotateCcw class="h-3.5 w-3.5" />
            <span>Save & Restart</span>
          </button>
        </div>
      </div>

      <!-- Quick Filter / Search Bar -->
      <div class="flex items-center gap-2 bg-white border border-slate-200 rounded-lg px-3 py-1.5">
        <Search class="h-3.5 w-3.5 text-slate-400 shrink-0" />
        <input
          v-model="searchQuery"
          type="text"
          placeholder="Filter keys or values in .env…"
          class="w-full bg-transparent text-xs text-slate-800 placeholder-slate-400 focus:outline-none font-mono"
        />
        <span v-if="searchQuery.trim()" class="text-[11px] text-slate-400 font-mono whitespace-nowrap">
          {{ filteredLines.length }} {{ filteredLines.length === 1 ? 'match' : 'matches' }}
        </span>
        <button
          v-if="searchQuery"
          @click="searchQuery = ''"
          class="text-slate-400 hover:text-slate-600 text-xs shrink-0"
        >
          Clear
        </button>
      </div>

      <!-- Filtered Results Preview when Search Query is Active -->
      <div v-if="searchQuery.trim()" class="rounded-lg border border-violet-200 bg-violet-50/40 p-3 space-y-2">
        <div class="flex items-center justify-between text-xs text-slate-600">
          <span class="font-semibold text-violet-700">
            Matching variables ({{ filteredLines.length }}):
          </span>
          <span class="text-[11px] text-slate-500 font-mono">Quick search preview</span>
        </div>
        <div v-if="filteredLines.length > 0" class="max-h-48 overflow-y-auto space-y-1 font-mono text-xs">
          <div
            v-for="item in filteredLines"
            :key="item.lineNum"
            class="rounded bg-white px-2 py-1 border border-slate-200 text-slate-800 flex items-start gap-2"
          >
            <span class="text-slate-500 select-none text-[10px] w-6 shrink-0 text-right">{{ item.lineNum }}:</span>
            <span class="break-all">{{ item.line }}</span>
          </div>
        </div>
        <div v-else class="text-xs text-slate-500 italic py-1">
          No matching keys or values found for "{{ searchQuery }}"
        </div>
      </div>

      <!-- .env Editor -->
      <textarea
        v-model="envContent"
        :readonly="!projectFilesWritable"
        :disabled="!envLoaded || envSaving"
        rows="18"
        spellcheck="false"
        placeholder="# Enter KEY=VALUE environment variables&#10;PORT=8080&#10;DEBUG=false"
        class="w-full rounded-lg border border-slate-200 bg-slate-50 p-4 font-mono text-xs text-slate-800 focus:border-violet-500 focus:outline-none leading-relaxed"
      ></textarea>
    </div>

    <div v-else class="rounded-xl border border-slate-200 bg-slate-50 p-8 text-center space-y-4">
      <Sliders class="mx-auto h-10 w-10 text-violet-400" />
      <div class="max-w-md mx-auto space-y-1">
        <h3 class="text-base font-bold text-slate-900">Standalone Container</h3>
        <p class="text-xs text-slate-600">
          Generate a project to manage persistent environment variables in a dedicated <code class="text-violet-500 font-mono">.env</code> file.
        </p>
      </div>
      <button
        @click="emit('scaffold')"
        :disabled="projectScaffolding"
        class="cyber-btn flex items-center gap-2 mx-auto text-xs text-cyan-700 border-cyan-500 bg-cyan-50/60 px-5 py-2.5"
      >
        <span>Generate Project & .env</span>
      </button>
    </div>
  </div>
</template>
