<script setup lang="ts">
import { Terminal, X } from 'lucide-vue-next'

interface ModalData {
  title: string
  lines: string[]
  done: boolean
  projectId: number
}

defineProps<{
  modal: ModalData
  failed?: boolean
}>()

const emit = defineEmits<{
  (e: 'close'): void
}>()

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
        <button @click="emit('close')" class="text-slate-600 hover:text-slate-800">
          <X class="h-5 w-5" />
        </button>
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
