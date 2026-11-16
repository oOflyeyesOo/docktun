<script setup lang="ts">
import { ArrowUpCircle, X, CheckCircle2, AlertCircle } from 'lucide-vue-next'

export interface UpdateContainerItem {
  id: string
  name: string
  image: string
  project_name?: string
  connected_to_cloudflared?: boolean
  [key: string]: any
}

export interface UpdateResultData {
  name: string
  output: string
  success: boolean
  error?: string
}

export interface UpdatePlanCheck {
  id: string
  label: string
  status: string
  message: string
}

export interface UpdatePlanData {
  ready?: boolean
  rollback_scope?: string
  checks?: UpdatePlanCheck[]
}

defineProps<{
  confirmContainer?: UpdateContainerItem | null
  result?: UpdateResultData | null
  updatePlan?: UpdatePlanData | null
  updatePlanLoading?: boolean
  updatePlanError?: string | null
}>()

const emit = defineEmits<{
  (e: 'close-confirm'): void
  (e: 'close-result'): void
  (e: 'confirm', container: UpdateContainerItem): void
}>()

let confirmBackdropMouseDown = false
let resultBackdropMouseDown = false

function onConfirmBackdropMouseDown(e: MouseEvent) {
  confirmBackdropMouseDown = e.target === e.currentTarget
}

function onConfirmBackdropClick(e: MouseEvent) {
  const selection = window.getSelection()?.toString()
  if (selection && selection.length > 0) {
    confirmBackdropMouseDown = false
    return
  }
  if (confirmBackdropMouseDown && e.target === e.currentTarget) {
    emit('close-confirm')
  }
  confirmBackdropMouseDown = false
}

function onResultBackdropMouseDown(e: MouseEvent) {
  resultBackdropMouseDown = e.target === e.currentTarget
}

function onResultBackdropClick(e: MouseEvent) {
  const selection = window.getSelection()?.toString()
  if (selection && selection.length > 0) {
    resultBackdropMouseDown = false
    return
  }
  if (resultBackdropMouseDown && e.target === e.currentTarget) {
    emit('close-result')
  }
  resultBackdropMouseDown = false
}
</script>

<template>
  <!-- UPDATE CONFIRMATION MODAL -->
  <div
    v-if="confirmContainer"
    class="fixed inset-0 z-50 flex items-center justify-center bg-slate-50/85 p-4 backdrop-blur-md"
    @mousedown="onConfirmBackdropMouseDown"
    @click="onConfirmBackdropClick"
  >
    <div
      class="w-full max-w-lg rounded-xl border border-amber-500/40 bg-white p-6 shadow-sm space-y-5"
      @mousedown.stop
      @mouseup.stop
      @click.stop
    >
      <div class="flex items-start justify-between">
        <div class="flex items-center gap-3">
          <div class="rounded-lg bg-amber-500/20 p-2.5 text-amber-600 border border-amber-500/30">
            <ArrowUpCircle class="h-6 w-6" />
          </div>
          <div>
            <h3 class="text-lg font-bold text-slate-900">Update Container</h3>
            <p class="text-xs text-slate-600 font-mono">{{ confirmContainer.name }}</p>
          </div>
        </div>
        <button @click="emit('close-confirm')" class="text-slate-600 hover:text-slate-800">
          <X class="h-5 w-5" />
        </button>
      </div>

      <div v-if="updatePlanLoading || updatePlanError || (updatePlan?.checks && updatePlan.checks.length > 0)" class="space-y-2 text-xs" aria-live="polite">
        <p v-if="updatePlanLoading">Staging candidate image and checking migration safety. Your running container is unchanged.</p>
        <p v-if="updatePlanError" role="alert" class="text-red-600">{{ updatePlanError }}</p>
        <div v-for="check in updatePlan?.checks || []" :key="check.id" class="border-b border-slate-100 pb-2">
          <strong>{{ check.label }} · {{ check.status }}</strong>
          <p class="mt-1">{{ check.message }}</p>
        </div>
        <p v-if="updatePlan?.rollback_scope" class="text-amber-800">{{ updatePlan.rollback_scope }}</p>
      </div>

      <div class="space-y-3 rounded-lg border border-slate-200 bg-slate-50 p-4 text-xs font-mono">
        <div class="flex justify-between">
          <span class="text-slate-500 font-sans">Target Image:</span>
          <span class="text-cyan-700 font-bold">{{ confirmContainer.image }}</span>
        </div>
        <div v-if="confirmContainer.project_name" class="flex justify-between">
          <span class="text-slate-500 font-sans">Stack / Compose:</span>
          <span class="text-emerald-600">{{ confirmContainer.project_name }}</span>
        </div>
        <div class="flex justify-between">
          <span class="text-slate-500 font-sans">Cloudflared Network:</span>
          <span :class="confirmContainer.connected_to_cloudflared ? 'text-emerald-600 font-bold' : 'text-slate-600'">
            {{ confirmContainer.connected_to_cloudflared ? 'Yes (Will maintain connection)' : 'No' }}
          </span>
        </div>
      </div>

      <p class="text-xs text-slate-700 leading-relaxed">
        DockTun will pull the latest image layers from the remote registry, cleanly recreate the container with your existing volume mounts, environment variables, published ports, and network routes, then verify health.
      </p>

      <div class="flex items-center justify-end gap-3 pt-2">
        <button
          @click="emit('close-confirm')"
          class="cyber-btn-secondary text-xs"
        >
          Cancel
        </button>
        <button
          @click="emit('confirm', confirmContainer)"
          :disabled="updatePlanLoading || (updatePlan && !updatePlan.ready) || !!updatePlanError"
          class="cyber-btn flex items-center gap-2 text-xs text-amber-200 border-amber-500/80 bg-gradient-to-r from-amber-600 to-orange-600 hover:from-amber-500 hover:to-orange-500 font-bold shadow-lg shadow-amber-900/30 disabled:opacity-50 disabled:cursor-not-allowed"
        >
          <ArrowUpCircle class="h-4 w-4" />
          <span>Pull & Recreate Container</span>
        </button>
      </div>
    </div>
  </div>

  <!-- UPDATE RESULT MODAL -->
  <div
    v-if="result"
    class="fixed inset-0 z-50 flex items-center justify-center bg-slate-50/85 p-4 backdrop-blur-md"
    @mousedown="onResultBackdropMouseDown"
    @click="onResultBackdropClick"
  >
    <div
      class="w-full max-w-2xl rounded-xl border bg-white p-6 shadow-sm space-y-4"
      :class="result.success ? 'border-emerald-500/40' : 'border-red-500/40'"
      @mousedown.stop
      @mouseup.stop
      @click.stop
    >
      <div class="flex items-start justify-between">
        <div class="flex items-center gap-3">
          <div
            class="rounded-lg p-2.5 border"
            :class="result.success ? 'bg-emerald-500/20 text-emerald-600 border-emerald-500/30' : 'bg-red-500/20 text-red-600 border-red-500/30'"
          >
            <CheckCircle2 v-if="result.success" class="h-6 w-6" />
            <AlertCircle v-else class="h-6 w-6" />
          </div>
          <div>
            <h3 class="text-lg font-bold text-slate-900">
              {{ result.success ? 'Container Updated Successfully' : 'Update Failed' }}
            </h3>
            <p class="text-xs text-slate-600 font-mono">{{ result.name }}</p>
          </div>
        </div>
        <button @click="emit('close-result')" class="text-slate-600 hover:text-slate-800">
          <X class="h-5 w-5" />
        </button>
      </div>

      <div class="max-h-72 overflow-y-auto rounded-lg border border-slate-200 bg-slate-50 p-4 font-mono text-xs leading-relaxed text-slate-700 shadow-inner">
        <pre class="whitespace-pre-wrap break-all">{{ result.output }}</pre>
      </div>

      <div class="flex items-center justify-end">
        <button
          @click="emit('close-result')"
          class="cyber-btn text-xs"
        >
          Close
        </button>
      </div>
    </div>
  </div>
</template>
