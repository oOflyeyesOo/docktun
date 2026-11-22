<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import {
  FolderOpen,
  Folder,
  FileText,
  Plus,
  RotateCcw,
  Save,
  Trash2,
  HardDrive,
  Copy,
  Check,
  Download,
  Search,
} from 'lucide-vue-next'
import { useApi } from '../../../../composables/useApi'
import { createAsyncScope } from '../../../../lib/async-lifecycle.mjs'
import { formatFileSize, filterFileList } from '../../../../lib/inspector-tabs.mjs'

interface FileItem {
  name: string
  path: string
  is_dir: boolean
  size: number
  mod_time: string
}

interface MountItem {
  source: string
  destination: string
  mode?: string
  rw?: boolean
}

const props = defineProps<{
  container: {
    id: string
    name: string
    project_id?: number
    project_path?: string
    mounts?: MountItem[]
  }
  projectFilesWritable: boolean
  projectScaffolding?: boolean
}>()

const emit = defineEmits<{
  (e: 'scaffold'): void
}>()

const { apiFetch } = useApi()

const currentDir = ref('')
const fileList = ref<FileItem[]>([])
const fileSearch = ref('')
const filesLoading = ref(false)
const editingFile = ref<{ projectId: number; path: string; content: string } | null>(null)
const editingFileSaving = ref(false)
const editingFileMsg = ref('')
const copiedFileContent = ref(false)
const showNewItemModal = ref(false)
const newItemName = ref('')
const newItemIsDir = ref(false)
const fileScope = createAsyncScope()
let copyTimer: ReturnType<typeof setTimeout> | undefined

const filteredFileList = computed(() => {
  return filterFileList(fileList.value, fileSearch.value)
})

async function loadContainerFiles(projectId: number, path = '') {
  const current = fileScope.begin()
  editingFileSaving.value = false
  copiedFileContent.value = false
  filesLoading.value = true
  currentDir.value = path
  fileList.value = []
  fileSearch.value = ''
  editingFileMsg.value = ''
  editingFile.value = null
  try {
    const res = await apiFetch<FileItem[]>(
      `/api/projects/${projectId}/files?path=${encodeURIComponent(path)}`
    )
    if (current()) {
      fileList.value = res || []
    }
  } catch (e: any) {
    if (current()) {
      fileList.value = []
      editingFileMsg.value = 'Failed to list files: ' + e.message
    }
  } finally {
    if (current()) {
      filesLoading.value = false
    }
  }
}

async function openContainerFile(f: FileItem) {
  if (!props.container.project_id) return
  const projectId = props.container.project_id
  if (f.is_dir) {
    loadContainerFiles(projectId, f.path)
    return
  }
  const current = fileScope.begin()
  editingFileSaving.value = false
  copiedFileContent.value = false
  editingFile.value = null
  editingFileMsg.value = ''
  filesLoading.value = true
  try {
    const res = await apiFetch<{ content: string }>(
      `/api/projects/${projectId}/file?path=${encodeURIComponent(f.path)}`
    )
    if (current()) {
      editingFile.value = { projectId, path: f.path, content: res.content || '' }
    }
  } catch (e: any) {
    if (current()) {
      editingFileMsg.value = 'Failed to read file: ' + e.message
    }
  } finally {
    if (current()) {
      filesLoading.value = false
    }
  }
}

async function saveCurrentContainerFile() {
  const file = editingFile.value
  if (!props.projectFilesWritable || !file || props.container.project_id !== file.projectId || editingFileSaving.value) return
  const current = fileScope.capture()
  editingFileSaving.value = true
  editingFileMsg.value = ''
  try {
    await apiFetch(`/api/projects/${file.projectId}/file?path=${encodeURIComponent(file.path)}`, {
      method: 'PUT',
      body: JSON.stringify({ content: file.content }),
    })
    if (current()) {
      editingFileMsg.value = 'File saved successfully!'
    }
  } catch (e: any) {
    if (current()) {
      editingFileMsg.value = 'Save failed: ' + e.message
    }
  } finally {
    if (current()) {
      editingFileSaving.value = false
    }
  }
}

function closeFileEditor() {
  fileScope.invalidate()
  editingFile.value = null
  filesLoading.value = editingFileSaving.value = false
  copiedFileContent.value = false
}

async function copyFileContent() {
  if (!editingFile.value) return
  const current = fileScope.capture()
  try {
    await navigator.clipboard.writeText(editingFile.value.content)
    if (!current()) return
    copiedFileContent.value = true
    if (copyTimer) clearTimeout(copyTimer)
    copyTimer = setTimeout(() => {
      copiedFileContent.value = false
    }, 2000)
  } catch (error: any) {
    if (current()) editingFileMsg.value = 'Failed to copy file: ' + error.message
  }
}

function downloadFile() {
  if (!editingFile.value) return
  const blob = new Blob([editingFile.value.content], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  const parts = editingFile.value.path.split('/')
  a.download = parts[parts.length - 1] || 'file'
  a.click()
  URL.revokeObjectURL(url)
}

async function createContainerFileItem() {
  if (!props.projectFilesWritable || !props.container.project_id || !newItemName.value.trim()) return
  const targetPath = currentDir.value ? `${currentDir.value}/${newItemName.value.trim()}` : newItemName.value.trim()
  const projectId = props.container.project_id
  const directory = currentDir.value
  const current = fileScope.capture()
  try {
    await apiFetch(`/api/projects/${projectId}/file`, {
      method: 'POST',
      body: JSON.stringify({
        path: targetPath,
        is_dir: newItemIsDir.value,
        content: '',
      }),
    })
    if (!current()) return
    showNewItemModal.value = false
    newItemName.value = ''
    loadContainerFiles(projectId, directory)
  } catch (e: any) {
    if (current()) alert('Create failed: ' + e.message)
  }
}

async function deleteContainerFile(path: string) {
  if (!props.projectFilesWritable || !props.container.project_id) return
  if (!confirm(`Delete ${path}?`)) return
  const projectId = props.container.project_id
  const directory = currentDir.value
  const current = fileScope.capture()
  try {
    await apiFetch(`/api/projects/${projectId}/file?path=${encodeURIComponent(path)}`, {
      method: 'DELETE',
    })
    if (current()) loadContainerFiles(projectId, directory)
  } catch (e: any) {
    if (current()) alert('Delete failed: ' + e.message)
  }
}

watch(
  () => [props.container.id, props.container.project_id],
  () => {
    const newId = props.container.project_id
    fileScope.invalidate()
    showNewItemModal.value = false
    newItemName.value = ''
    copiedFileContent.value = false
    filesLoading.value = editingFileSaving.value = false
    editingFileMsg.value = ''
    if (newId) {
      loadContainerFiles(newId, '')
    } else {
      fileList.value = []
      editingFile.value = null
    }
  },
  { flush: 'sync' }
)

onMounted(() => {
  if (props.container.project_id) {
    loadContainerFiles(props.container.project_id, '')
  }
})
onUnmounted(() => {
  fileScope.dispose()
  if (copyTimer) clearTimeout(copyTimer)
})
</script>

<template>
  <div class="space-y-4">
    <!-- If Container is Linked to a Project -->
    <div v-if="container.project_id" class="space-y-4">
      <!-- Header & Breadcrumbs -->
      <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-200 pb-3">
        <div class="flex items-center gap-2 text-xs font-mono">
          <FolderOpen class="h-4 w-4 text-amber-600" />
          <span class="text-slate-600 cursor-pointer hover:text-slate-800" @click="loadContainerFiles(container.project_id, '')">
            {{ container.project_path }}
          </span>
          <span v-if="currentDir" class="text-slate-600">/</span>
          <span v-if="currentDir" class="text-cyan-700">{{ currentDir }}</span>
        </div>

        <div class="flex items-center gap-2">
          <button
            v-if="currentDir"
            @click="loadContainerFiles(container.project_id, currentDir.split('/').slice(0, -1).join('/'))"
            class="cyber-btn-secondary text-xs"
          >
            Up One Level
          </button>
          <button
            @click="showNewItemModal = true"
            :disabled="!projectFilesWritable"
            class="cyber-btn text-xs flex items-center gap-1.5"
          >
            <Plus class="h-3.5 w-3.5" />
            <span>New Item</span>
          </button>
          <button
            @click="loadContainerFiles(container.project_id, currentDir)"
            :disabled="filesLoading"
            class="cyber-btn-secondary text-xs"
          >
            <RotateCcw class="h-3 w-3" :class="filesLoading ? 'animate-spin' : ''" />
          </button>
        </div>
      </div>

      <p
        v-if="editingFileMsg"
        role="status"
        class="text-xs font-medium"
        :class="editingFileMsg.startsWith('Failed') || editingFileMsg.startsWith('Save failed') ? 'text-red-600' : 'text-emerald-600'"
      >
        {{ editingFileMsg }}
      </p>

      <!-- Active File Editor if editing a file -->
      <div v-if="editingFile" class="rounded-xl border border-slate-200 bg-slate-50 p-4 space-y-3">
        <div class="flex items-center justify-between border-b border-slate-200 pb-2">
          <div class="flex items-center gap-2 text-xs font-mono text-cyan-700">
            <FileText class="h-4 w-4 text-cyan-600" />
            <span>{{ editingFile.path }}</span>
          </div>
          <div class="flex items-center gap-2">
            <button
              @click="copyFileContent"
              class="cyber-btn-secondary text-xs flex items-center gap-1"
              title="Copy file content"
            >
              <Check v-if="copiedFileContent" class="h-3 w-3 text-emerald-600" />
              <Copy v-else class="h-3 w-3" />
              <span>{{ copiedFileContent ? 'Copied' : 'Copy' }}</span>
            </button>
            <button
              @click="downloadFile"
              class="cyber-btn-secondary text-xs flex items-center gap-1"
              title="Download file"
            >
              <Download class="h-3 w-3" />
              <span>Download</span>
            </button>
            <button
              @click="saveCurrentContainerFile"
              :disabled="editingFileSaving || !projectFilesWritable"
              class="cyber-btn text-xs flex items-center gap-1.5 text-cyan-700"
            >
              <Save class="h-3 w-3" />
              <span>Save</span>
            </button>
            <button
              @click="closeFileEditor"
              class="cyber-btn-secondary text-xs"
            >
              Close Editor
            </button>
          </div>
        </div>
        <textarea
          v-model="editingFile.content"
          :readonly="!projectFilesWritable"
          rows="16"
          spellcheck="false"
          class="w-full rounded border border-slate-200 bg-white p-3 font-mono text-xs text-slate-800 focus:outline-none"
        ></textarea>
      </div>

      <!-- File List View -->
      <div v-else class="space-y-2">
        <!-- Quick File Filter Input -->
        <div v-if="fileList.length > 2 || fileSearch" class="flex items-center justify-between gap-3 bg-white/60 border border-slate-200/80 rounded-lg px-3 py-1.5">
          <div class="flex items-center gap-2 flex-1">
            <Search class="h-3.5 w-3.5 text-slate-500" />
            <input
              v-model="fileSearch"
              type="text"
              placeholder="Filter files by name..."
              class="w-full bg-transparent text-xs text-slate-800 placeholder-slate-500 focus:outline-none font-mono"
            />
          </div>
          <button
            v-if="fileSearch"
            @click="fileSearch = ''"
            class="text-slate-500 hover:text-slate-700 text-xs"
          >
            Clear
          </button>
        </div>

        <div class="rounded-xl border border-slate-200 bg-slate-50 divide-y divide-slate-200/80">
          <div v-if="filteredFileList.length === 0 && !filesLoading" class="p-8 text-center text-xs text-slate-500">
            {{ fileSearch ? 'No files matching query.' : 'Folder is empty.' }}
          </div>
          <div
            v-for="item in filteredFileList"
            :key="item.path"
            class="flex items-center justify-between px-4 py-2.5 text-xs hover:bg-white/60 transition-colors"
          >
            <div
              class="flex items-center gap-2.5 cursor-pointer font-mono truncate"
              @click="openContainerFile(item)"
            >
              <Folder v-if="item.is_dir" class="h-4 w-4 text-amber-600 shrink-0" />
              <FileText v-else class="h-4 w-4 text-slate-600 shrink-0" />
              <span :class="item.is_dir ? 'text-slate-800 font-semibold hover:text-cyan-700' : 'text-slate-700 hover:text-cyan-700'">
                {{ item.name }}
              </span>
            </div>

            <div class="flex items-center gap-4 text-slate-500 font-mono text-[11px]">
              <span v-if="!item.is_dir">{{ formatFileSize(item.size) }}</span>
              <span>{{ item.mod_time }}</span>
              <button
                @click="deleteContainerFile(item.path)"
                :disabled="!projectFilesWritable"
                class="text-slate-600 hover:text-red-600 transition-colors"
                title="Delete file"
              >
                <Trash2 class="h-3.5 w-3.5" />
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- New Item Modal -->
      <div v-if="showNewItemModal" class="fixed inset-0 z-50 flex items-center justify-center bg-slate-50/80 p-4">
        <div class="w-full max-w-sm rounded-xl border border-slate-200 bg-white p-5 space-y-4">
          <h4 class="text-sm font-bold text-slate-900">Create New Item</h4>
          <div>
            <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">Item Name</label>
            <input
              v-model="newItemName"
              placeholder="e.g. data.json or logs"
              class="cyber-input w-full font-mono text-xs"
              @keyup.enter="createContainerFileItem"
            />
          </div>
          <div class="flex items-center gap-2">
            <input id="is-dir" type="checkbox" v-model="newItemIsDir" class="rounded bg-slate-50" />
            <label for="is-dir" class="text-xs text-slate-700">Create as Directory</label>
          </div>
          <div class="flex justify-end gap-2 pt-2">
            <button @click="showNewItemModal = false" class="cyber-btn-secondary text-xs">Cancel</button>
            <button @click="createContainerFileItem" class="cyber-btn text-xs text-cyan-700">Create</button>
          </div>
        </div>
      </div>
    </div>

    <!-- Standalone Container Storage View -->
    <div v-else class="space-y-4">
      <div class="rounded-xl border border-slate-200 bg-slate-50 p-5 space-y-3">
        <h3 class="text-xs font-bold uppercase tracking-wider text-slate-600 flex items-center gap-2">
          <HardDrive class="h-4 w-4 text-amber-600" /> Container Mounts & Storage
        </h3>
        <div v-if="container.mounts && container.mounts.length > 0" class="space-y-2 font-mono text-xs">
          <div
            v-for="(m, i) in container.mounts"
            :key="i"
            class="rounded border border-slate-200 bg-white/60 p-3 space-y-1"
          >
            <div class="flex items-center justify-between">
              <span class="text-slate-600">Host Path:</span>
              <span class="text-cyan-600">{{ m.source }}</span>
            </div>
            <div class="flex items-center justify-between">
              <span class="text-slate-600">Container Path:</span>
              <span class="text-emerald-600">{{ m.destination }}</span>
            </div>
            <div class="flex items-center justify-between text-[11px] text-slate-500 pt-1">
              <span>Permissions:</span>
              <span>{{ m.rw ? 'Read/Write' : 'Read-Only' }}</span>
            </div>
          </div>
        </div>
        <p v-else class="text-xs text-slate-500">No storage mounts configured.</p>
      </div>

      <div class="rounded-xl border border-slate-200 bg-slate-50 p-6 text-center space-y-3">
        <FolderOpen class="mx-auto h-8 w-8 text-amber-600" />
        <p class="text-xs text-slate-600">Want full web file browsing and editing for this container?</p>
        <button
          @click="emit('scaffold')"
          :disabled="projectScaffolding"
          class="cyber-btn text-xs text-cyan-700 border-cyan-500 bg-cyan-50/60"
        >
          Generate Project File Tree
        </button>
      </div>
    </div>
  </div>
</template>
