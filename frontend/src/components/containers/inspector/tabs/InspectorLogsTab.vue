<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { ScrollText, RotateCcw, Search, Check, Copy, Download, Trash2 } from 'lucide-vue-next'
import { createAsyncScope, createSerialPoller } from '../../../../lib/async-lifecycle.mjs'
import { useApi } from '../../../../composables/useApi'

const props = defineProps<{
  containerId: string
  containerName: string
  containerImage: string
}>()

const { apiFetch } = useApi()

const modalLogsText = ref('')
const modalLogsLoading = ref(false)
const modalLogsTail = ref(200)
const modalLogsWrap = ref(true)
const modalLogsAutoRefresh = ref(false)
const modalLogsTimestamps = ref(false)
const modalLogsFilter = ref('')
const copiedLogs = ref(false)
const logScope = createAsyncScope()
let copyTimer: ReturnType<typeof setTimeout> | undefined
const logPoller = createSerialPoller(async (pollCurrent: () => boolean) => {
  await loadLogs(pollCurrent)
}, 3000)

const filteredModalLogs = computed(() => {
  if (!modalLogsText.value) return ''
  if (!modalLogsFilter.value.trim()) return modalLogsText.value
  const query = modalLogsFilter.value.toLowerCase()
  return modalLogsText.value
    .split('\n')
    .filter((line) => line.toLowerCase().includes(query))
    .join('\n')
})

const matchingLinesCount = computed(() => {
  if (!modalLogsText.value || !modalLogsFilter.value.trim()) return null
  const total = modalLogsText.value.split('\n').length
  const matched = filteredModalLogs.value ? filteredModalLogs.value.split('\n').length : 0
  return { matched, total }
})

async function loadLogs(pollCurrent = () => true) {
  if (!props.containerId) return
  const current = logScope.begin()
  const containerId = props.containerId
  modalLogsLoading.value = true
  try {
    const res = await apiFetch<{ logs: string }>(
      `/api/docker/containers/${containerId}/logs?tail=${modalLogsTail.value}&timestamps=${modalLogsTimestamps.value}`
    )
    if (current() && pollCurrent()) modalLogsText.value = res.logs || 'No logs recorded.'
  } catch (error: any) {
    if (current() && pollCurrent()) modalLogsText.value = 'Failed to load logs: ' + error.message
  } finally {
    if (current() && pollCurrent()) modalLogsLoading.value = false
  }
}

function fetchModalLogs() {
  return loadLogs()
}

async function copyLogs() {
  if (!filteredModalLogs.value) return
  const current = logScope.capture()
  try {
    await navigator.clipboard.writeText(filteredModalLogs.value)
    if (!current()) return
    copiedLogs.value = true
    if (copyTimer) clearTimeout(copyTimer)
    copyTimer = setTimeout(() => {
      copiedLogs.value = false
    }, 2000)
  } catch (e) {
    console.error('Failed to copy logs:', e)
  }
}

function downloadLogs() {
  const content = modalLogsFilter.value ? filteredModalLogs.value : modalLogsText.value
  if (!content) return
  const blob = new Blob([content], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  const name = (props.containerName || props.containerId || 'container').replace(/^\//, '')
  a.download = `${name}-logs.txt`
  a.click()
  URL.revokeObjectURL(url)
}

function clearLogsView() {
  logScope.invalidate()
  modalLogsLoading.value = false
  modalLogsText.value = ''
}

watch(modalLogsAutoRefresh, (enabled) => {
  logPoller.stop()
  logScope.invalidate()
  modalLogsLoading.value = false
  if (enabled) logPoller.start(3000)
})

watch(
  () => props.containerId,
  () => {
    logPoller.stop()
    logScope.invalidate()
    modalLogsText.value = ''
    copiedLogs.value = false
    modalLogsLoading.value = false
    if (props.containerId) {
      fetchModalLogs()
      if (modalLogsAutoRefresh.value) logPoller.start(3000)
    }
  },
  { flush: 'sync' }
)

onMounted(() => {
  fetchModalLogs()
})

onUnmounted(() => {
  logScope.dispose()
  logPoller.stop()
  if (copyTimer) clearTimeout(copyTimer)
})

</script>

<template>
  <div class="space-y-3">
    <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-200 pb-3">
      <div class="space-y-0.5">
        <div class="flex flex-wrap items-center gap-2">
          <ScrollText class="h-4 w-4 text-cyan-600" />
          <span class="text-sm font-bold text-slate-900 font-mono">{{ containerName }}</span>
          <span class="rounded bg-slate-100 px-2 py-0.5 text-[10px] text-slate-600 font-mono">{{ containerImage }}</span>
          <span class="inline-flex items-center gap-1 rounded bg-emerald-50/60 border border-emerald-800/50 px-2 py-0.5 text-[10px] text-emerald-600 font-medium">
            <span class="h-1.5 w-1.5 rounded-full bg-emerald-400 animate-pulse"></span>
            Isolated Service Logs
          </span>
        </div>
        <p class="text-[11px] text-slate-500 font-mono">
          Streaming stdout & stderr exclusively for container <span class="text-slate-600 font-bold">{{ containerId.slice(0, 12) }}</span>
        </p>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <label class="flex items-center gap-1.5 text-xs text-slate-600 cursor-pointer">
          <input type="checkbox" v-model="modalLogsAutoRefresh" class="rounded bg-slate-50" />
          <span>Live (3s)</span>
        </label>

        <label class="flex items-center gap-1.5 text-xs text-slate-600 cursor-pointer">
          <input type="checkbox" v-model="modalLogsTimestamps" @change="fetchModalLogs" class="rounded bg-slate-50" />
          <span>Timestamps</span>
        </label>

        <label class="flex items-center gap-1.5 text-xs text-slate-600 cursor-pointer">
          <input type="checkbox" v-model="modalLogsWrap" class="rounded bg-slate-50" />
          <span>Wrap</span>
        </label>

        <select
          v-model.number="modalLogsTail"
          @change="fetchModalLogs"
          class="rounded border border-slate-300 bg-slate-50 px-2 py-1 text-xs text-slate-700 font-mono"
        >
          <option :value="100">100 lines</option>
          <option :value="200">200 lines</option>
          <option :value="500">500 lines</option>
          <option :value="1000">1000 lines</option>
        </select>

        <button
          @click="copyLogs"
          class="cyber-btn-secondary text-xs flex items-center gap-1"
          title="Copy logs"
        >
          <Check v-if="copiedLogs" class="h-3 w-3 text-emerald-600" />
          <Copy v-else class="h-3 w-3" />
          <span>{{ copiedLogs ? 'Copied' : 'Copy' }}</span>
        </button>

        <button
          @click="downloadLogs"
          :disabled="!modalLogsText"
          class="cyber-btn-secondary text-xs flex items-center gap-1"
          title="Download Logs"
        >
          <Download class="h-3 w-3" />
          <span>Download Logs</span>
        </button>

        <button
          @click="clearLogsView"
          :disabled="!modalLogsText"
          class="cyber-btn-secondary text-xs flex items-center gap-1"
          title="Clear View"
        >
          <Trash2 class="h-3 w-3" />
          <span>Clear View</span>
        </button>

        <button
          @click="fetchModalLogs"
          :disabled="modalLogsLoading"
          class="cyber-btn-secondary text-xs flex items-center gap-1"
        >
          <RotateCcw class="h-3 w-3" :class="modalLogsLoading ? 'animate-spin' : ''" />
          <span>Refresh</span>
        </button>
      </div>
    </div>

    <!-- Filter Input Strip -->
    <div class="flex items-center justify-between gap-3 bg-white/60 border border-slate-200/80 rounded-lg px-3 py-1.5">
      <div class="flex items-center gap-2 flex-1">
        <Search class="h-3.5 w-3.5 text-slate-500" />
        <input
          v-model="modalLogsFilter"
          type="text"
          placeholder="Filter service logs (case-insensitive substring)..."
          class="w-full bg-transparent text-xs text-slate-800 placeholder-slate-500 focus:outline-none font-mono"
        />
      </div>
      <span v-if="matchingLinesCount" class="text-[10px] text-cyan-600 font-mono whitespace-nowrap">
        {{ matchingLinesCount.matched }} / {{ matchingLinesCount.total }} lines
      </span>
      <button
        v-if="modalLogsFilter"
        @click="modalLogsFilter = ''"
        class="text-slate-500 hover:text-slate-700 text-xs"
      >
        Clear
      </button>
    </div>

    <!-- Terminal Output Box -->
    <div
      class="max-h-[58vh] overflow-y-auto rounded-lg border border-slate-200 bg-slate-50 p-4 font-mono text-xs leading-5 text-slate-700"
    >
      <div v-if="modalLogsLoading" class="text-slate-500 flex items-center gap-2">
        <RotateCcw class="h-3.5 w-3.5 animate-spin" /> Fetching latest logs for {{ containerName }}…
      </div>
      <div v-else-if="!filteredModalLogs" class="text-slate-500 text-center py-6">
        {{ modalLogsFilter ? 'No lines matching filter query.' : 'No logs recorded for this service container.' }}
      </div>
      <pre
        v-else
        class="font-mono text-[11px] leading-relaxed"
        :class="modalLogsWrap ? 'whitespace-pre-wrap break-all' : 'whitespace-pre overflow-x-auto'"
      >{{ filteredModalLogs }}</pre>
    </div>
  </div>
</template>
