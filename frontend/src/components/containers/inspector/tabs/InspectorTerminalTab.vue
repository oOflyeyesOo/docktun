<script setup lang="ts">
import { ref } from 'vue'
import { Terminal, RotateCcw, Play, Check, Copy, Trash2 } from 'lucide-vue-next'
import { useApi } from '../../../../composables/useApi'

const props = defineProps<{
  containerId: string
  containerName: string
}>()

const { apiFetch } = useApi()

const execCmd = ref('')
const execOutput = ref('')
const execLoading = ref(false)
const execExitCode = ref<number | null>(null)
const execHistory = ref<string[]>([])
const historyIndex = ref(-1)
const savedCmd = ref('')
const copiedOutput = ref(false)

const presets = ['ls -la', 'ps aux', 'env', 'df -h', 'ip a', 'uptime']

async function runContainerExec() {
  if (!props.containerId || !execCmd.value.trim() || execLoading.value) return
  const cmd = execCmd.value.trim()
  execLoading.value = true
  execOutput.value = `// EXECUTING: ${cmd}...\n`
  execExitCode.value = null
  const idx = execHistory.value.indexOf(cmd)
  if (idx !== -1) {
    execHistory.value.splice(idx, 1)
  }
  execHistory.value.unshift(cmd)
  historyIndex.value = -1
  savedCmd.value = ''

  try {
    const res = await apiFetch<{ output: string; exit_code: number }>(
      `/api/docker/containers/${props.containerId}/exec`,
      {
        method: 'POST',
        body: JSON.stringify({ command: cmd }),
      }
    )
    execOutput.value = res.output || '(Command completed with no output)'
    execExitCode.value = res.exit_code
  } catch (err: any) {
    execOutput.value = `ERROR: ${err.message || err}`
    execExitCode.value = 1
  } finally {
    execLoading.value = false
  }
}

function runPreset(preset: string) {
  execCmd.value = preset
  runContainerExec()
}

function onHistoryUp() {
  if (!execHistory.value.length) return
  if (historyIndex.value === -1) {
    savedCmd.value = execCmd.value
    historyIndex.value = 0
    execCmd.value = execHistory.value[0]
  } else if (historyIndex.value < execHistory.value.length - 1) {
    historyIndex.value++
    execCmd.value = execHistory.value[historyIndex.value]
  }
}

function onHistoryDown() {
  if (historyIndex.value === -1) return
  if (historyIndex.value > 0) {
    historyIndex.value--
    execCmd.value = execHistory.value[historyIndex.value]
  } else if (historyIndex.value === 0) {
    historyIndex.value = -1
    execCmd.value = savedCmd.value
  }
}

async function copyOutput() {
  if (!execOutput.value) return
  try {
    await navigator.clipboard.writeText(execOutput.value)
    copiedOutput.value = true
    setTimeout(() => {
      copiedOutput.value = false
    }, 2000)
  } catch (err) {
    console.error('Failed to copy output:', err)
  }
}

function clearOutput() {
  execOutput.value = ''
  execExitCode.value = null
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-200 pb-3">
      <div>
        <h3 class="text-sm font-bold text-slate-900 flex items-center gap-2 font-mono">
          <Terminal class="h-4 w-4 text-cyan-600" />
          <span>Interactive Container Exec: {{ containerName }}</span>
        </h3>
        <p class="text-[11px] text-slate-600 font-mono mt-0.5">
          Execute commands directly inside container via Docker exec
        </p>
      </div>

      <!-- Quick command chips -->
      <div class="flex flex-wrap items-center gap-1.5 text-xs font-mono">
        <span class="text-[10px] text-slate-500 uppercase tracking-wider mr-1">Presets:</span>
        <button
          v-for="preset in presets"
          :key="preset"
          @click="runPreset(preset)"
          class="rounded bg-white border border-slate-200 px-2 py-0.5 text-[11px] text-slate-700 hover:border-cyan-700 hover:text-cyan-700 transition cursor-pointer"
        >
          {{ preset }}
        </button>
      </div>
    </div>

    <!-- Command Input Bar -->
    <div class="flex items-center gap-2">
      <div class="relative flex-1">
        <span class="absolute left-3 top-2.5 font-mono text-cyan-600 text-xs font-bold">$</span>
        <input
          v-model="execCmd"
          @keyup.enter="runContainerExec"
          @keydown.up.prevent="onHistoryUp"
          @keydown.down.prevent="onHistoryDown"
          placeholder="Enter command to run (e.g. ls -la, cat /etc/resolv.conf, curl localhost)..."
          class="cyber-input w-full pl-8 font-mono text-xs text-slate-900"
          :disabled="execLoading"
        />
      </div>

      <button
        @click="runContainerExec"
        :disabled="execLoading || !execCmd.trim()"
        class="cyber-btn flex items-center gap-1.5 text-xs text-cyan-700 border-cyan-600 bg-cyan-50/40 hover:bg-cyan-100/50"
      >
        <RotateCcw v-if="execLoading" class="h-3.5 w-3.5 animate-spin" />
        <Play v-else class="h-3.5 w-3.5 fill-current" />
        <span>{{ execLoading ? 'Running...' : 'Execute' }}</span>
      </button>

      <button
        @click="clearOutput"
        :disabled="!execOutput"
        class="cyber-btn-secondary text-xs"
        title="Clear Output"
      >
        Clear Output
      </button>
    </div>

    <!-- Output Box -->
    <div class="space-y-2">
      <div class="flex items-center justify-between text-xs font-mono text-slate-600">
        <div class="flex items-center gap-2">
          <span class="text-[10px] uppercase tracking-wider">Console Output</span>
          <span
            v-if="execExitCode !== null"
            class="rounded px-2 py-0.5 text-[10px] font-bold"
            :class="execExitCode === 0 ? 'bg-emerald-50 text-emerald-600 border border-emerald-800' : 'bg-rose-50 text-rose-600 border border-rose-800'"
          >
            Exit Code: {{ execExitCode }}
          </span>
        </div>

        <div class="flex items-center gap-2">
          <button
            @click="copyOutput"
            :disabled="!execOutput"
            class="cyber-btn-secondary text-xs flex items-center gap-1"
            title="Copy Output"
          >
            <Check v-if="copiedOutput" class="h-3 w-3 text-emerald-600" />
            <Copy v-else class="h-3 w-3" />
            <span>{{ copiedOutput ? 'Copied' : 'Copy Output' }}</span>
          </button>

          <button
            @click="clearOutput"
            :disabled="!execOutput"
            class="cyber-btn-secondary text-xs flex items-center gap-1"
            title="Clear Output"
          >
            <Trash2 class="h-3 w-3" />
            <span>Clear Output</span>
          </button>
        </div>
      </div>

      <div class="max-h-[50vh] min-h-[160px] overflow-y-auto rounded-lg border border-slate-200 bg-slate-50 p-4 font-mono text-xs leading-relaxed text-slate-800 shadow-inner">
        <pre v-if="execOutput" class="whitespace-pre-wrap break-all">{{ execOutput }}</pre>
        <p v-else class="text-slate-600 italic">No command has been executed yet. Enter a command or click a preset above.</p>
      </div>
    </div>
  </div>
</template>
