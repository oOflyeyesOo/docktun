<script setup lang="ts">
import { ref, computed } from 'vue'
import { Code2, Copy, Check, Search, Download } from 'lucide-vue-next'

const props = defineProps<{
  container: any
  containerId: string
}>()

const searchQuery = ref('')
const copied = ref(false)

const formattedJson = computed(() => {
  try {
    return JSON.stringify(props.container, null, 2)
  } catch {
    return '{}'
  }
})

const filteredLines = computed(() => {
  const lines = formattedJson.value.split('\n')
  if (!searchQuery.value.trim()) return lines
  const q = searchQuery.value.toLowerCase()
  return lines.filter((l) => l.toLowerCase().includes(q))
})

async function copyJson() {
  try {
    await navigator.clipboard.writeText(formattedJson.value)
    copied.value = true
    setTimeout(() => {
      copied.value = false
    }, 2000)
  } catch (err) {
    console.error('Failed to copy JSON:', err)
  }
}

function downloadJson() {
  const json = formattedJson.value
  if (!json) return
  const blob = new Blob([json], { type: 'application/json;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  const name = (props.container?.name || props.containerId || 'container').replace(/^\//, '')
  a.download = `${name}-inspect.json`
  a.click()
  URL.revokeObjectURL(url)
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-200 pb-3">
      <div>
        <h3 class="text-sm font-bold text-slate-900 flex items-center gap-2 font-mono">
          <Code2 class="h-4 w-4 text-cyan-600" />
          <span>Docker Inspect JSON: {{ container?.name || containerId }}</span>
        </h3>
        <p class="text-[11px] text-slate-600 font-mono mt-0.5">
          Complete low-level configuration, network settings, and state reported by Docker daemon
        </p>
      </div>

      <div class="flex items-center gap-2">
        <button
          @click="copyJson"
          class="cyber-btn-secondary text-xs flex items-center gap-1.5"
          title="Copy raw JSON"
        >
          <Check v-if="copied" class="h-3.5 w-3.5 text-emerald-600" />
          <Copy v-else class="h-3.5 w-3.5" />
          <span>{{ copied ? 'Copied' : 'Copy JSON' }}</span>
        </button>

        <button
          @click="downloadJson"
          class="cyber-btn-secondary text-xs flex items-center gap-1.5"
          title="Download JSON"
        >
          <Download class="h-3.5 w-3.5" />
          <span>Download JSON</span>
        </button>
      </div>
    </div>

    <!-- Search filter -->
    <div class="flex items-center gap-2 bg-white border border-slate-200 rounded-lg px-3 py-1.5">
      <Search class="h-3.5 w-3.5 text-slate-400" />
      <input
        v-model="searchQuery"
        type="text"
        placeholder="Search inspect properties (e.g. IPAddress, Mounts, Env)..."
        class="w-full bg-transparent text-xs text-slate-800 placeholder-slate-400 focus:outline-none font-mono"
      />
      <button
        v-if="searchQuery"
        @click="searchQuery = ''"
        class="text-slate-400 hover:text-slate-600 text-xs"
      >
        Clear
      </button>
    </div>

    <!-- JSON Viewer -->
    <div class="max-h-[60vh] overflow-y-auto rounded-lg border border-slate-200 bg-slate-900 p-4 font-mono text-xs leading-relaxed text-slate-100 shadow-inner">
      <pre class="whitespace-pre overflow-x-auto text-[11px]"><code class="text-emerald-400">{{ filteredLines.join('\n') }}</code></pre>
    </div>
  </div>
</template>
