<script setup lang="ts">
import { ref } from 'vue'
import { Archive, Download, Upload, AlertTriangle, CheckCircle2, AlertCircle, X, ShieldAlert } from 'lucide-vue-next'
import { useApi } from '../composables/useApi'

const { apiFetch, apiResponse } = useApi()

const downloading = ref(false)
const restoring = ref(false)
const showModal = ref(false)
const restoreFile = ref<File | null>(null)
const confirmWarning = ref(false)
const message = ref<{ type: 'success' | 'error'; text: string } | null>(null)
const modalError = ref<string | null>(null)

async function downloadBackup() {
  downloading.value = true
  message.value = null
  try {
    const res = await apiResponse('/api/system/backup/export')
    if (!res.ok) {
      const errText = await res.text()
      throw new Error(errText || 'Failed to download disaster recovery archive')
    }
    const blob = await res.blob()
    const contentDisposition = res.headers.get('Content-Disposition') || ''
    let filename = 'docktun-backup.tar.gz'
    const match = contentDisposition.match(/filename="?([^";]+)"?/)
    if (match && match[1]) {
      filename = match[1]
    }
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = filename
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)
    URL.revokeObjectURL(url)
    message.value = {
      type: 'success',
      text: `Disaster recovery archive downloaded successfully (${filename}). Keep it in a secure offsite location.`
    }
  } catch (e: any) {
    message.value = { type: 'error', text: e.message || 'Export failed' }
  } finally {
    downloading.value = false
  }
}

function openRestoreModal() {
  showModal.value = true
  confirmWarning.value = false
  restoreFile.value = null
  modalError.value = null
}

function closeRestoreModal() {
  if (restoring.value) return
  showModal.value = false
  confirmWarning.value = false
  restoreFile.value = null
  modalError.value = null
}

function onFileSelect(event: Event) {
  const target = event.target as HTMLInputElement
  if (target.files && target.files.length > 0) {
    restoreFile.value = target.files[0]
  } else {
    restoreFile.value = null
  }
}

async function executeRestore() {
  if (!restoreFile.value || !confirmWarning.value) return
  restoring.value = true
  modalError.value = null
  try {
    const formData = new FormData()
    formData.append('backup', restoreFile.value)

    const res = await apiFetch<{ status?: string; message?: string }>('/api/system/backup/restore', {
      method: 'POST',
      body: formData,
    })

    showModal.value = false
    message.value = {
      type: 'success',
      text: res?.message || 'Disaster recovery archive restored successfully. Please reload the dashboard.'
    }
  } catch (e: any) {
    modalError.value = e.message || 'Restore failed'
  } finally {
    restoring.value = false
  }
}
</script>

<template>
  <section class="cyber-card space-y-4">
    <!-- Header -->
    <div class="flex items-center justify-between border-b border-border pb-3">
      <div class="flex items-center gap-2">
        <ShieldAlert class="w-4 h-4 text-neon" />
        <h2 class="text-xs text-muted uppercase tracking-widest font-mono">// Disaster Recovery Archive</h2>
      </div>
    </div>

    <p class="text-xs text-muted leading-relaxed">
      Consolidated single-bundle backup and restore for full system disaster recovery. Packages the DockTun SQLite database
      (<code class="text-neon font-mono">app.db</code>), Cloudflare tunnel configuration (<code class="text-neon font-mono">/etc/cloudflared</code>),
      and project Compose stacks (<code class="text-neon font-mono">/opt/projects</code>) into a verified, checksum-protected archive.
    </p>

    <!-- Global Notice Message -->
    <div
      v-if="message"
      class="border p-3 text-xs font-mono flex items-center gap-2"
      :class="message.type === 'success' ? 'border-neon text-neon bg-neon/5' : 'border-danger text-danger bg-danger/5'"
    >
      <CheckCircle2 v-if="message.type === 'success'" class="w-4 h-4 text-emerald-500 shrink-0" />
      <AlertCircle v-else class="w-4 h-4 text-red-500 shrink-0" />
      <span class="break-all">{{ message.text }}</span>
    </div>

    <!-- Action Buttons -->
    <div class="flex flex-col sm:flex-row gap-3 pt-2">
      <!-- 1-Click Download Button -->
      <button
        type="button"
        @click="downloadBackup"
        :disabled="downloading || restoring"
        class="cyber-btn flex items-center justify-center gap-2 flex-1 py-2 text-xs"
      >
        <Download class="w-3.5 h-3.5" :class="{ 'animate-bounce': downloading }" />
        <span>{{ downloading ? 'CREATING BACKUP ARCHIVE…' : 'DOWNLOAD DISASTER RECOVERY ARCHIVE' }}</span>
      </button>

      <!-- Restore Modal Trigger Button -->
      <button
        type="button"
        @click="openRestoreModal"
        :disabled="downloading || restoring"
        class="cyber-btn-secondary flex items-center justify-center gap-2 flex-1 py-2 text-xs border border-border"
      >
        <Upload class="w-3.5 h-3.5" />
        <span>RESTORE FROM ARCHIVE</span>
      </button>
    </div>

    <!-- Restore Upload Modal with Warning Prompt -->
    <div
      v-if="showModal"
      class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/75 backdrop-blur-sm"
    >
      <div class="cyber-card max-w-lg w-full space-y-4 border border-danger/50 shadow-2xl bg-slate-900/95 text-slate-100">
        <!-- Modal Header -->
        <div class="flex items-center justify-between border-b border-border pb-3">
          <div class="flex items-center gap-2 text-danger">
            <AlertTriangle class="w-5 h-5 text-red-500" />
            <h3 class="text-xs uppercase tracking-widest font-mono font-bold text-red-400">
              CRITICAL: Disaster Recovery Restore
            </h3>
          </div>
          <button
            type="button"
            @click="closeRestoreModal"
            :disabled="restoring"
            class="text-muted hover:text-white transition-colors"
          >
            <X class="w-4 h-4" />
          </button>
        </div>

        <!-- Warning Prompt -->
        <div class="border border-red-500/40 bg-red-950/30 p-3 space-y-2 text-xs">
          <div class="flex items-start gap-2">
            <AlertTriangle class="w-4 h-4 text-red-500 shrink-0 mt-0.5" />
            <p class="font-mono text-red-300 font-semibold leading-relaxed">
              WARNING: This operation will overwrite and replace the active SQLite database, Cloudflare tunnel configurations, and Compose stacks on this DockTun instance.
            </p>
          </div>
          <p class="text-[11px] text-muted font-mono leading-relaxed pl-6">
            Make sure you have downloaded a current backup before restoring. Any changes made since the archive was created will be permanently overwritten.
          </p>
        </div>

        <!-- File Upload Field -->
        <div class="space-y-1.5 pt-1">
          <label class="text-[10px] text-muted uppercase tracking-widest font-mono block">
            Select Backup Archive (.tar.gz)
          </label>
          <input
            type="file"
            accept=".tar.gz,.tgz,application/gzip,application/x-gzip"
            @change="onFileSelect"
            :disabled="restoring"
            class="block w-full text-xs font-mono text-slate-300 file:mr-3 file:py-1.5 file:px-3 file:border-0 file:text-xs file:font-mono file:bg-slate-800 file:text-neon hover:file:bg-slate-700 cursor-pointer border border-border rounded-none p-1.5 bg-black/40"
          />
        </div>

        <!-- Confirmation Checkbox -->
        <div class="flex items-start gap-2 pt-2">
          <input
            id="confirm-overwrite"
            type="checkbox"
            v-model="confirmWarning"
            :disabled="restoring"
            class="mt-1 cyber-checkbox cursor-pointer"
          />
          <label for="confirm-overwrite" class="text-xs font-mono text-slate-300 select-none cursor-pointer">
            I understand that this action is irreversible and will overwrite the current system configuration and database.
          </label>
        </div>

        <!-- Error Message in Modal -->
        <div
          v-if="modalError"
          class="border border-danger text-danger bg-danger/10 p-2.5 text-xs font-mono flex items-center gap-2"
        >
          <AlertCircle class="w-4 h-4 text-red-500 shrink-0" />
          <span class="break-all">{{ modalError }}</span>
        </div>

        <!-- Modal Actions -->
        <div class="flex items-center justify-end gap-3 pt-3 border-t border-border">
          <button
            type="button"
            @click="closeRestoreModal"
            :disabled="restoring"
            class="cyber-btn-secondary text-xs px-4 py-2"
          >
            CANCEL
          </button>
          <button
            type="button"
            @click="executeRestore"
            :disabled="!restoreFile || !confirmWarning || restoring"
            class="cyber-btn bg-danger/20 border-danger hover:bg-danger/30 text-xs px-4 py-2 flex items-center gap-2"
          >
            <Upload class="w-3.5 h-3.5" :class="{ 'animate-spin': restoring }" />
            <span>{{ restoring ? 'VALIDATING & RESTORING…' : 'CONFIRM & RESTORE' }}</span>
          </button>
        </div>
      </div>
    </div>
  </section>
</template>
