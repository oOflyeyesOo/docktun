<script setup lang="ts">
import { useClipboardFeedback } from '../../../composables/useClipboardFeedback'
import { ref, watch } from 'vue'
import { Terminal, X, Copy, Check } from 'lucide-vue-next'

interface ModalData {
  title: string
  lines: string[]
  done: boolean
  projectId: number
}

const props = defineProps<{
  modal: ModalData
  failed?: boolean
}>()

const emit = defineEmits<{
  (e: 'close'): void
}>()

const copied = ref(false)

const outputClipboard = useClipboardFeedback<boolean>(key => { copied.value = key === true })
watch(() => props.modal, () => outputClipboard.reset())
function copyLogs() {
  return outputClipboard.copy(true, (props.modal.lines || []).join('\n'))
}

let backdropMouseDown = false

function onBackdropMouseDown(e: MouseEvent) {
  backdropMouseDown = e.target === e.currentTarget
}

function onBackdropClick(e: MouseEvent) {
  const selection = window.getSelection()?.toString()
  if (selection && selection.length > 0) {
    backdropMouseDown = false
    return
  }
  if (backdropMouseDown && e.target === e.currentTarget) {
    emit('close')
  }
  backdropMouseDown = false
}
</script>

<template>
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-slate-50/85 p-4 backdrop-blur-md"
    @mousedown="onBackdropMouseDown"
    @click="onBackdropClick"
  >
    <div
      class="w-full max-w-3xl rounded-xl border border-slate-300 bg-white p-6 shadow-sm space-y-4"
      @mousedown.stop
      @mouseup.stop
      @click.stop
    >
      <div class="flex items-start justify-between border-b border-slate-200 pb-3">
        <div class="flex items-center gap-3">
          <div class="rounded-lg p-2 bg-slate-100 text-cyan-600">
            <Terminal class="h-5 w-5" />
          </div>
          <div>
            <h3 class="text-base font-bold text-slate-900">{{ modal.title }}</h3>
            <p class="text-xs text-slate-600 font-mono">
              {{ modal.done ? 'Action finished.' : 'Action in progress… streaming output.' }}
            </p>
          </div>
        </div>
        <div class="flex items-center gap-2">
          <button
            type="button"
            @click="copyLogs"
            :disabled="!modal.lines || modal.lines.length === 0"
            class="inline-flex items-center gap-1 rounded bg-slate-100 hover:bg-slate-200 text-slate-700 px-2.5 py-1 text-xs font-medium transition-colors cursor-pointer disabled:opacity-50"
            title="Copy output logs to clipboard"
          >
            <Check v-if="copied" class="h-3.5 w-3.5 text-emerald-600" />
            <Copy v-else class="h-3.5 w-3.5 text-slate-500" />
            <span>{{ copied ? 'Copied' : 'Copy Logs' }}</span>
          </button>
          <button @click="emit('close')" class="text-slate-600 hover:text-slate-800 p-1">
            <X class="h-5 w-5" />
          </button>
        </div>
      </div>

      <div class="max-h-96 overflow-y-auto rounded-lg border border-slate-200 bg-slate-50 p-4 font-mono text-xs leading-relaxed text-slate-700 shadow-inner">
        <div v-for="(line, idx) in modal.lines" :key="idx" class="whitespace-pre-wrap break-all">{{ line }}</div>
        <div v-if="!modal.done" class="text-cyan-600 animate-pulse mt-2">&rarr; Running docker-compose command...</div>
      </div>

      <div class="flex items-center justify-between text-xs">
        <span :class="failed ? 'text-rose-600 font-semibold' : (modal.done ? 'text-emerald-600' : 'text-amber-600')">
          {{ !modal.done ? '● Processing...' : (failed ? '✗ Action Failed' : '✓ Completed') }}
        </span>
        <button @click="emit('close')" class="cyber-btn text-xs">Close</button>
      </div>
    </div>
  </div>
</template>
