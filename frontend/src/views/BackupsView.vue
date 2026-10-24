<script setup lang="ts">
import { ref, onMounted } from 'vue'
import {
  Archive,
  Plus,
  Download,
  Trash2,
  Eye,
  RotateCcw,
  X,
  RefreshCw,
  Check,
  AlertCircle,
  FileText
} from 'lucide-vue-next'
import { useApi } from '../composables/useApi'

const { apiFetch } = useApi()

interface BackupItem {
  id: number
  content: string
  user_id: number
  created_at: string
}

const backups = ref<BackupItem[]>([])
const loading = ref(false)
const creating = ref(false)
const selectedBackup = ref<BackupItem | null>(null)
const previewContent = ref('')
const message = ref('')
const errorMessage = ref('')

async function load() {
  loading.value = true
  errorMessage.value = ''
  try {
    const res = await apiFetch<BackupItem[]>('/api/config/backups')
    backups.value = Array.isArray(res) ? res : []
  } catch (err: any) {
    backups.value = []
    errorMessage.value = err.message || 'Failed to load backups'
  } finally {
    loading.value = false
  }
}

async function createBackup() {
  creating.value = true
  errorMessage.value = ''
  try {
    await apiFetch('/api/config/backups', { method: 'POST' })
    message.value = 'Config backup created successfully'
    setTimeout(() => (message.value = ''), 4000)
    await load()
  } catch (e: any) {
    errorMessage.value = 'Failed to create backup: ' + e.message
  } finally {
    creating.value = false
  }
}

async function showPreview(id: number) {
  try {
    const b = await apiFetch<BackupItem>(`/api/config/backups/${id}`)
    selectedBackup.value = b
    previewContent.value = b.content
  } catch (e: any) {
    alert('Failed to load backup: ' + e.message)
  }
}

async function restore(id: number) {
  if (!confirm('Restore this config backup? Current cloudflared config will be overwritten.')) return
  try {
    const b = await apiFetch<BackupItem>(`/api/config/backups/${id}`)
    await apiFetch('/api/config', { method: 'PUT', body: JSON.stringify({ content: b.content }) })
    message.value = 'Configuration restored successfully'
    selectedBackup.value = null
    previewContent.value = ''
    setTimeout(() => (message.value = ''), 4000)
    await load()
  } catch (e: any) {
    alert('Failed to restore: ' + e.message)
  }
}

async function remove(id: number) {
  if (!confirm('Delete this backup?')) return
  try {
    await apiFetch(`/api/config/backups/${id}`, { method: 'DELETE' })
    if (selectedBackup.value?.id === id) {
      selectedBackup.value = null
      previewContent.value = ''
    }
    await load()
  } catch (e: any) {
    alert('Failed to delete backup: ' + e.message)
  }
}

function downloadBackup(b: BackupItem) {
  const blob = new Blob([b.content], { type: 'text/yaml' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `cloudflared-config-backup-${b.id}.yml`
  a.click()
  URL.revokeObjectURL(url)
}

function getLineCount(str: string) {
  if (!str) return 0
  return str.split('\n').length
}

onMounted(() => {
  load()
})
</script>

<template>
  <div class="space-y-6">
    <!-- Header -->
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <p class="text-[10px] font-semibold uppercase tracking-[0.22em] text-slate-600">Version History</p>
        <h1 class="text-2xl font-bold tracking-tight text-slate-900">Config Backups</h1>
      </div>

      <div class="flex items-center gap-2">
        <button @click="load" :disabled="loading" class="cyber-btn-secondary flex items-center gap-2 text-xs">
          <RefreshCw class="h-3.5 w-3.5" :class="loading ? 'animate-spin' : ''" /> Refresh
        </button>
        <button @click="createBackup" :disabled="creating" class="cyber-btn flex items-center gap-2 text-xs">
          <Plus class="h-3.5 w-3.5" /> {{ creating ? 'Creating...' : 'Create Snapshot' }}
        </button>
      </div>
    </div>

    <!-- Feedback Banners -->
    <div v-if="message" class="rounded-lg border border-emerald-500/30 bg-emerald-50/40 px-4 py-3 text-xs text-emerald-700 flex items-center gap-2">
      <Check class="h-4 w-4 flex-shrink-0" />
      <span>{{ message }}</span>
    </div>

    <div v-if="errorMessage" class="rounded-lg border border-red-500/30 bg-red-50/40 px-4 py-3 text-xs text-red-700 flex items-center gap-2">
      <AlertCircle class="h-4 w-4 flex-shrink-0" />
      <span>{{ errorMessage }}</span>
    </div>

    <!-- Backups List -->
    <div class="space-y-3">
      <div
        v-for="b in backups"
        :key="b.id"
        class="cyber-card flex flex-col sm:flex-row sm:items-center justify-between gap-4 transition hover:border-slate-300"
        :class="selectedBackup?.id === b.id ? 'border-cyan-500' : ''"
      >
        <div class="flex items-center gap-3">
          <div class="flex h-9 w-9 items-center justify-center rounded-lg border border-slate-200 bg-slate-50 font-mono text-xs font-bold text-cyan-600">
            #{{ b.id }}
          </div>
          <div>
            <div class="font-mono text-xs font-bold text-slate-800">{{ b.created_at }}</div>
            <p class="mt-0.5 font-mono text-[11px] text-slate-600">
              {{ getLineCount(b.content) }} lines • {{ b.content.length }} bytes
            </p>
          </div>
        </div>

        <div class="flex flex-wrap items-center gap-2">
          <button @click="showPreview(b.id)" class="cyber-btn-secondary text-xs flex items-center gap-1.5" title="View Backup">
            <Eye class="h-3.5 w-3.5 text-cyan-600" /> <span>View</span>
          </button>
          <button @click="downloadBackup(b)" class="cyber-btn-secondary text-xs flex items-center gap-1.5" title="Download .yml">
            <Download class="h-3.5 w-3.5" /> <span>Download</span>
          </button>
          <button @click="restore(b.id)" class="cyber-btn-secondary text-xs flex items-center gap-1.5 text-emerald-600 hover:border-emerald-600" title="Restore this configuration">
            <RotateCcw class="h-3.5 w-3.5" /> <span>Restore</span>
          </button>
          <button @click="remove(b.id)" class="cyber-btn-danger text-xs p-2" title="Delete Backup">
            <Trash2 class="h-3.5 w-3.5" />
          </button>
        </div>
      </div>

      <!-- Empty State -->
      <div v-if="!backups.length && !loading" class="cyber-card text-center py-12 space-y-3">
        <Archive class="h-10 w-10 text-slate-600 mx-auto" />
        <p class="text-sm font-medium text-slate-700">No Config Backups Found</p>
        <p class="text-xs text-slate-500">Backups are automatically generated when you edit configuration, or you can create one manually above.</p>
      </div>
    </div>

    <!-- Preview Modal -->
    <div
      v-if="selectedBackup"
      class="fixed inset-0 z-50 flex items-center justify-center bg-slate-50/80 p-4 backdrop-blur-sm"
      @click.self="selectedBackup = null"
    >
      <div class="flex max-h-[85vh] w-full max-w-4xl flex-col rounded-xl border border-slate-200 bg-white shadow-sm">
        <div class="flex items-center justify-between border-b border-slate-200 px-5 py-3">
          <div class="flex items-center gap-2">
            <FileText class="h-4 w-4 text-cyan-600" />
            <h3 class="text-sm font-bold text-slate-900 font-mono">Backup #{{ selectedBackup.id }} ({{ selectedBackup.created_at }})</h3>
          </div>
          <div class="flex items-center gap-2">
            <button @click="restore(selectedBackup.id)" class="cyber-btn text-xs flex items-center gap-1 text-emerald-700">
              <RotateCcw class="h-3.5 w-3.5" /> Restore
            </button>
            <button @click="selectedBackup = null" class="text-slate-600 hover:text-slate-800">
              <X class="h-4 w-4" />
            </button>
          </div>
        </div>
        <div class="flex-1 overflow-auto bg-slate-50 p-4 font-mono text-xs leading-5 text-slate-700">
          <pre class="whitespace-pre-wrap">{{ previewContent }}</pre>
        </div>
      </div>
    </div>
  </div>
</template>
