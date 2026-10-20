<script setup lang="ts">
import { ref } from 'vue'
import {
  Archive,
  Plus,
  Download,
  Trash2,
  Eye,
  RotateCcw,
  X,
  RefreshCw,
  Check
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

async function load() {
  loading.value = true
  try {
    const res = await apiFetch<BackupItem[]>('/api/config/backups')
    backups.value = Array.isArray(res) ? res : []
  } catch {
    backups.value = []
  } finally {
    loading.value = false
  }
}

async function createBackup() {
  creating.value = true
  try {
    await apiFetch('/api/config/backups', { method: 'POST' })
    message.value = 'BACKUP CREATED SUCCESSFULLY'
    setTimeout(() => (message.value = ''), 4000)
    await load()
  } catch (e: any) {
    alert('Failed to create backup: ' + e.message)
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
    message.value = 'CONFIG RESTORED SUCCESSFULLY'
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

load()
</script>

<template>
  <div class="space-y-6">
    <!-- Header -->
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h1 class="cyber-heading text-xl lg:text-2xl flex items-center gap-2">
          <Archive class="w-6 h-6 text-neon" /> Config Backups
        </h1>
        <p class="text-xs text-muted font-mono mt-1">// Automated and manual Cloudflare Tunnel config snapshots</p>
      </div>

      <div class="flex items-center gap-2">
        <button @click="load" :disabled="loading" class="cyber-btn-secondary flex items-center gap-2" title="Refresh">
          <RefreshCw class="w-4 h-4" :class="loading && 'animate-spin'" /> Refresh
        </button>
        <button @click="createBackup" :disabled="creating" class="cyber-btn flex items-center gap-2">
          <Plus class="w-4 h-4" /> {{ creating ? 'Creating...' : 'Create Backup' }}
        </button>
      </div>
    </div>

    <!-- Alert Banner -->
    <div v-if="message" class="cyber-card p-3 border-neon bg-neon/10 text-neon text-xs font-mono flex items-center gap-2 shadow-neon">
      <Check class="w-4 h-4" /> [OK] {{ message }}
    </div>

    <!-- Backups List -->
    <div class="space-y-2">
      <div
        v-for="b in backups"
        :key="b.id"
        class="cyber-card flex flex-col sm:flex-row sm:items-center justify-between gap-3 hover:border-border/80 transition-all"
        :class="selectedBackup?.id === b.id && 'border-neon'"
      >
        <div class="flex items-center gap-3">
          <div class="w-8 h-8 rounded bg-surface border border-border flex items-center justify-center text-xs font-mono text-neon font-bold">
            #{{ b.id }}
          </div>
          <div>
            <span class="text-xs text-gray-200 font-mono font-bold tracking-wide">{{ b.created_at }}</span>
            <p class="text-[11px] text-muted font-mono mt-0.5">
              {{ getLineCount(b.content) }} lines &bull; {{ b.content.length }} bytes
            </p>
          </div>
        </div>

        <div class="flex items-center gap-1.5 self-end sm:self-auto">
          <button @click="showPreview(b.id)" class="cyber-btn-sm text-cyan-400 flex items-center gap-1" title="View Backup">
            <Eye class="w-3.5 h-3.5" /> <span class="text-xs font-mono">View</span>
          </button>
          <button @click="downloadBackup(b)" class="cyber-btn-sm text-muted hover:text-white flex items-center gap-1" title="Download .yml">
            <Download class="w-3.5 h-3.5" /> <span class="hidden sm:inline text-xs font-mono">Download</span>
          </button>
          <button @click="restore(b.id)" class="cyber-btn-sm text-neon flex items-center gap-1" title="Restore this configuration">
            <RotateCcw class="w-3.5 h-3.5" /> <span class="text-xs font-mono">Restore</span>
          </button>
          <button @click="remove(b.id)" class="cyber-btn-sm text-danger hover:border-danger" title="Delete Backup">
            <Trash2 class="w-3.5 h-3.5" />
          </button>
        </div>
      </div>

      <!-- Empty State -->
      <div v-if="!backups.length && !loading" class="cyber-card text-center py-12 space-y-3">
        <Archive class="w-10 h-10 text-muted mx-auto opacity-50" />
        <p class="text-sm text-muted font-mono">// NO CONFIG BACKUPS FOUND</p>
        <p class="text-xs text-muted/70 font-mono max-w-md mx-auto">
          Backups are automatically saved whenever you modify the Cloudflare Tunnel config, or you can create one manually right now.
        </p>
        <button @click="createBackup" :disabled="creating" class="cyber-btn inline-flex items-center gap-2 mt-2">
          <Plus class="w-4 h-4" /> Create First Backup Now
        </button>
      </div>
    </div>

    <!-- Preview Panel -->
    <div v-if="selectedBackup" class="cyber-card space-y-3 border-neon shadow-neon">
      <div class="flex items-center justify-between border-b border-border pb-3">
        <div>
          <div class="flex items-center gap-2">
            <span class="text-xs text-neon uppercase tracking-wider font-mono">// BACKUP PREVIEW</span>
            <span class="text-muted font-mono">/</span>
            <span class="text-xs text-white font-mono font-bold">Snapshot #{{ selectedBackup.id }}</span>
          </div>
          <p class="text-xs text-muted font-mono mt-0.5">{{ selectedBackup.created_at }}</p>
        </div>
        <div class="flex items-center gap-2">
          <button @click="downloadBackup(selectedBackup)" class="cyber-btn-secondary text-xs flex items-center gap-1">
            <Download class="w-3.5 h-3.5" /> Download
          </button>
          <button @click="restore(selectedBackup.id)" class="cyber-btn text-xs flex items-center gap-1">
            <RotateCcw class="w-3.5 h-3.5" /> Restore This Backup
          </button>
          <button @click="selectedBackup = null; previewContent = ''" class="text-muted hover:text-white p-1">
            <X class="w-5 h-5" />
          </button>
        </div>
      </div>

      <pre class="font-mono text-xs text-neon/90 bg-void p-4 border border-border overflow-auto max-h-96 leading-relaxed whitespace-pre-wrap">{{ previewContent }}</pre>
    </div>
  </div>
</template>
