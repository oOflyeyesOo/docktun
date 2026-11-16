<script setup lang="ts">
import { Trash2, RotateCcw } from 'lucide-vue-next'

interface StackDeleteData {
  projectId: number
  projectName: string
  path?: string
}

defineProps<{
  modal: StackDeleteData
  deleteDown: boolean
  removeFiles: boolean
  loading: boolean
  folderChecking: boolean
  folderAvailable: boolean
  folderReason: string
}>()

const emit = defineEmits<{
  (e: 'update:deleteDown', val: boolean): void
  (e: 'update:removeFiles', val: boolean): void
  (e: 'confirm'): void
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
    class="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/60 p-4 backdrop-blur-xs"
    @mousedown="onBackdropMouseDown"
    @click="onBackdropClick"
  >
    <div
      class="w-full max-w-md rounded-xl border border-rose-200 bg-white p-6 shadow-xl space-y-4"
      @mousedown.stop
      @mouseup.stop
      @click.stop
    >
      <div class="flex items-start gap-3">
        <div class="rounded-full bg-rose-100 p-2 text-rose-600 shrink-0">
          <Trash2 class="h-5 w-5" />
        </div>
        <div>
          <h3 class="text-base font-bold text-slate-900">Delete Stack</h3>
          <p class="text-xs text-slate-600 mt-0.5">
            Are you sure you want to delete stack <strong class="text-slate-900 font-mono">{{ modal.projectName }}</strong>?
          </p>
        </div>
      </div>

      <div class="space-y-3 rounded-lg border border-slate-200 bg-slate-50 p-3.5 text-xs">
        <label class="flex items-start gap-2 cursor-pointer">
          <input
            type="checkbox"
            :checked="deleteDown"
            @change="emit('update:deleteDown', ($event.target as HTMLInputElement).checked)"
            class="mt-0.5 rounded border-slate-300 text-rose-600 focus:ring-rose-500 cursor-pointer"
          />
          <div>
            <span class="font-semibold text-slate-800">Stop and remove stack containers</span>
            <p class="text-slate-500 text-[11px]">
              Stops and removes this stack’s containers and Compose networks. Volumes, images, and external networks are kept. Uncheck to forget the saved project only; existing containers will still appear in the container list.
            </p>
          </div>
        </label>

        <label
          class="flex items-start gap-2 pt-2 border-t border-slate-200"
          :class="folderAvailable && !loading ? 'cursor-pointer' : 'cursor-not-allowed'"
        >
          <input
            type="checkbox"
            :checked="removeFiles"
            @change="emit('update:removeFiles', ($event.target as HTMLInputElement).checked)"
            :disabled="folderChecking || !folderAvailable || loading"
            aria-describedby="delete-stack-folder-help"
            class="mt-0.5 rounded border-slate-300 text-rose-600 focus:ring-rose-500 disabled:opacity-40 disabled:cursor-not-allowed"
          />
          <div>
            <span class="font-semibold" :class="folderAvailable ? 'text-rose-700' : 'text-slate-400'">Delete project folder from disk</span>
            <p id="delete-stack-folder-help" role="status" aria-live="polite" class="text-slate-500 text-[11px]">
              <template v-if="folderChecking">Checking whether the project folder can be removed…</template>
              <template v-else-if="!folderAvailable">{{ folderReason }}</template>
              <template v-else>
                Permanently removes <code class="font-mono">{{ modal.path }}</code>, including files stored inside it. Only folders inside DockTun’s projects folder can be removed.
              </template>
            </p>
          </div>
        </label>
      </div>

      <div class="flex items-center justify-end gap-2 pt-2">
        <button
          type="button"
          @click="emit('close')"
          class="cyber-btn-secondary text-xs"
          :disabled="loading"
        >
          Cancel
        </button>
        <button
          type="button"
          @click="emit('confirm')"
          class="cyber-btn flex items-center gap-1.5 text-xs text-white bg-rose-600 hover:bg-rose-700 font-semibold"
          :disabled="loading"
        >
          <RotateCcw v-if="loading" class="h-3.5 w-3.5 animate-spin" />
          <Trash2 v-else class="h-3.5 w-3.5" />
          <span>{{ loading ? 'Deleting…' : 'Delete Stack' }}</span>
        </button>
      </div>
    </div>
  </div>
</template>
