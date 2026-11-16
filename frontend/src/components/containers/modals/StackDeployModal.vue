<script setup lang="ts">
import { ref, watch } from 'vue'
import { Rocket, X } from 'lucide-vue-next'

interface StackDeployPromptData {
  projectId: number
  projectName: string
}

const props = withDefaults(
  defineProps<{
    modal: StackDeployPromptData
    modelValue?: string
  }>(),
  {
    modelValue: 'main',
  }
)

const emit = defineEmits<{
  (e: 'update:modelValue', val: string): void
  (e: 'confirm', branch: string): void
  (e: 'close'): void
}>()

const localBranch = ref(props.modelValue || 'main')

watch(
  () => props.modelValue,
  (val) => {
    if (val !== undefined) localBranch.value = val
  }
)

function onInput(val: string) {
  localBranch.value = val
  emit('update:modelValue', val)
}

function handleConfirm() {
  emit('confirm', localBranch.value)
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
      class="w-full max-w-md rounded-xl border border-violet-500/40 bg-white p-6 shadow-sm space-y-4"
      @mousedown.stop
      @mouseup.stop
      @click.stop
    >
      <div class="flex items-start justify-between border-b border-slate-200 pb-3">
        <div class="flex items-center gap-2 text-violet-300 font-bold">
          <Rocket class="h-5 w-5" />
          <span>Deploy Stack: {{ modal.projectName }}</span>
        </div>
        <button @click="emit('close')" class="text-slate-600 hover:text-slate-800">
          <X class="h-5 w-5" />
        </button>
      </div>

      <div class="space-y-2 text-xs">
        <label class="block font-semibold uppercase tracking-wider text-slate-600">Git Branch, Tag, or Ref</label>
        <input
          :value="localBranch"
          @input="onInput(($event.target as HTMLInputElement).value)"
          class="cyber-input font-mono text-xs"
          placeholder="main"
          @keyup.enter="handleConfirm"
        />
        <p class="text-[11px] text-slate-500">
          DockTun will fetch remote changes, checkout this ref, git pull, and execute <code class="text-cyan-600 font-mono">docker compose up -d --build</code>.
        </p>
      </div>

      <div class="flex items-center justify-end gap-3 pt-2">
        <button @click="emit('close')" class="cyber-btn-secondary text-xs">Cancel</button>
        <button
          @click="handleConfirm"
          class="cyber-btn flex items-center gap-1.5 text-xs text-violet-300 border-violet-600 bg-violet-950/40 hover:bg-violet-900/60 font-bold"
        >
          <Rocket class="h-3.5 w-3.5" />
          <span>Start Deployment</span>
        </button>
      </div>
    </div>
  </div>
</template>
