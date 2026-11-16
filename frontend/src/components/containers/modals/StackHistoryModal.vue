<script setup lang="ts">
import { History, X } from 'lucide-vue-next'

interface HistoryItem {
  action?: string
  created_at?: string
  timestamp?: string
  detail?: string
  ip?: string
}

interface StackHistoryData {
  projectName: string
  items: HistoryItem[]
}

defineProps<{
  modal: StackHistoryData
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
      class="w-full max-w-2xl rounded-xl border border-slate-300 bg-white p-6 shadow-sm space-y-4"
      @mousedown.stop
      @mouseup.stop
      @click.stop
    >
      <div class="flex items-start justify-between border-b border-slate-200 pb-3">
        <div class="flex items-center gap-2.5">
          <History class="h-5 w-5 text-cyan-600" />
          <div>
            <h3 class="text-base font-bold text-slate-900">Stack Action History</h3>
            <p class="text-xs text-slate-600 font-mono">{{ modal.projectName }}</p>
          </div>
        </div>
        <button @click="emit('close')" class="text-slate-600 hover:text-slate-800">
          <X class="h-5 w-5" />
        </button>
      </div>

      <div v-if="modal.items.length === 0" class="py-8 text-center text-xs text-slate-500">
        No recorded actions found for this stack.
      </div>

      <div v-else class="max-h-80 overflow-y-auto divide-y divide-slate-200/80 pr-1">
        <div
          v-for="(item, idx) in modal.items"
          :key="idx"
          class="py-2.5 flex items-start justify-between gap-3 text-xs"
        >
          <div>
            <div class="flex items-center gap-2">
              <span class="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-[10px] font-bold text-cyan-700 uppercase">
                {{ item.action || 'Action' }}
              </span>
              <span class="text-slate-700 font-mono">{{ item.created_at || item.timestamp }}</span>
            </div>
            <div v-if="item.detail" class="mt-1 font-mono text-[11px] text-slate-600 line-clamp-2">
              {{ item.detail }}
            </div>
          </div>
          <span class="text-[10px] text-slate-500 font-mono shrink-0">{{ item.ip || 'Local' }}</span>
        </div>
      </div>

      <div class="flex items-center justify-end pt-2">
        <button @click="emit('close')" class="cyber-btn text-xs">Close</button>
      </div>
    </div>
  </div>
</template>
