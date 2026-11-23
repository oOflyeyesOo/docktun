<script setup lang="ts">
import { useClipboardFeedback } from '../composables/useClipboardFeedback'
import { ref, reactive, toRefs, computed, onMounted } from 'vue'
import {
  FolderOpen,
  FileCode,
  History,
  RotateCcw,
  Check,
  Copy,
  Plus,
  Trash2,
  Save,
  AlertCircle,
  Clock,
  Sparkles,
  ArrowLeft,
  Eye,
  CheckCircle2,
  Folder
} from 'lucide-vue-next'
import { useApi } from '../composables/useApi'
import { createProjectFileEditor } from '../lib/project-file-editor.mjs'

interface Project {
  id: number
  name: string
  path: string
  repo_url?: string
  created_at?: string
}

interface Revision {
  id: number
  project_id: number
  filename: string
  content: string
  message: string
  user_id: number
  created_at: string
  size: number
}

const { apiFetch } = useApi()

// Project State
const projects = ref<Project[]>([])
const loading = ref(false)
const selectedProject = ref<Project | null>(null)
const projectSearch = ref('')

// Compose Editor State
const composeEditor = reactive(createProjectFileEditor('Compose', 'docker-compose.yml'))
const { filename: composeFilename, content: composeContent, loading: editorLoading, saving: editorSaving } = toRefs(composeEditor)
const composeMessage = ref('')
const editorSuccessMsg = ref('')
const editorErrorMsg = ref('')
let selectionRevision = 0

// Revisions History State
const revisions = ref<Revision[]>([])
const revisionsLoading = ref(false)
const revisionsError = ref('')
const rollingBackId = ref<number | null>(null)
const previewRevision = ref<Revision | null>(null)
const copiedKey = ref('')

const filteredProjects = computed(() => {
  const q = projectSearch.value.trim().toLowerCase()
  if (!q) return projects.value
  return projects.value.filter(p => p.name.toLowerCase().includes(q) || p.path.toLowerCase().includes(q))
})

onMounted(async () => {
  await loadProjects()
})

async function loadProjects() {
  loading.value = true
  try {
    const res = await apiFetch<Project[]>('/api/projects')
    projects.value = Array.isArray(res) ? res : []
    if (projects.value.length > 0 && !selectedProject.value) {
      selectProject(projects.value[0])
    }
  } catch (err: any) {
    editorErrorMsg.value = err.message || 'Failed to load projects'
  } finally {
    loading.value = false
  }
}

async function selectProject(p: Project) {
  projectClipboard.reset()
  selectionRevision++
  selectedProject.value = p
  revisions.value = []
  previewRevision.value = null
  editorSuccessMsg.value = ''
  editorErrorMsg.value = ''
  await Promise.all([loadCompose(p.id), loadRevisions(p.id)])
}

async function loadCompose(projectId: number) {
  await composeEditor.load(projectId, (id: number) => apiFetch(`/api/projects/${id}/compose`))
  if (composeEditor.projectId === projectId && composeEditor.error) editorErrorMsg.value = composeEditor.message
}

async function loadRevisions(projectId: number) {
  const revision = selectionRevision
  revisionsLoading.value = true
  revisionsError.value = ''
  try {
    const res = await apiFetch<Revision[]>(`/api/projects/${projectId}/revisions`)
    if (revision === selectionRevision) revisions.value = Array.isArray(res) ? res : []
  } catch (err: any) {
    if (revision === selectionRevision) revisionsError.value = err.message || 'Failed to load compose revisions'
  } finally {
    if (revision === selectionRevision) revisionsLoading.value = false
  }
}

async function saveCompose() {
  if (!selectedProject.value || composeEditor.projectId !== selectedProject.value.id) return
  const revision = selectionRevision
  const message = composeMessage.value
  editorSuccessMsg.value = ''
  editorErrorMsg.value = ''
  const saved = await composeEditor.save((id: number, body: any) => apiFetch(`/api/projects/${id}/compose`, {
    method: 'PUT',
    body: JSON.stringify({ ...body, message: message || 'Updated via Compose Editor' })
  }))
  if (revision !== selectionRevision) return
  if (saved) {
    editorSuccessMsg.value = 'Compose file saved and revision created successfully!'
    composeMessage.value = ''
    await loadRevisions(selectedProject.value.id)
  } else if (composeEditor.error) {
    editorErrorMsg.value = composeEditor.message
  }
}

async function rollbackTo(rev: Revision) {
  if (!selectedProject.value) return
  if (!confirm(`Are you sure you want to rollback ${selectedProject.value.name} to Revision #${rev.id} (${rev.created_at})? This will replace the active compose file.`)) {
    return
  }

  const projectId = selectedProject.value.id
  const revision = selectionRevision
  rollingBackId.value = rev.id
  editorSuccessMsg.value = ''
  editorErrorMsg.value = ''
  try {
    const res: any = await apiFetch(`/api/projects/${projectId}/revisions/${rev.id}/rollback`, {
      method: 'POST'
    })
    if (revision !== selectionRevision) return
    editorSuccessMsg.value = res.message || `Successfully rolled back to Revision #${rev.id}`
    composeContent.value = rev.content
    composeFilename.value = rev.filename || composeFilename.value
    previewRevision.value = null
    await loadRevisions(selectedProject.value.id)
  } catch (err: any) {
    if (revision === selectionRevision) editorErrorMsg.value = err.message || 'Rollback failed'
  } finally {
    rollingBackId.value = null
  }
}

const projectClipboard = useClipboardFeedback<string>(key => { copiedKey.value = key ?? '' }, message => { editorErrorMsg.value = message })
function copyYAML(text: string, key: string) {
  return projectClipboard.copy(key, text)
}

function formatDate(iso: string) {
  if (!iso) return ''
  try {
    const d = new Date(iso)
    return d.toLocaleString()
  } catch {
    return iso
  }
}
</script>

<template>
  <div class="space-y-6">
    <!-- Header -->
    <div class="flex items-center justify-between flex-wrap gap-4 border-b border-slate-200 pb-4">
      <div>
        <h1 class="text-xl font-bold text-slate-900 font-mono flex items-center gap-2">
          <FileCode class="h-6 w-6 text-indigo-600" />
          <span>Compose Stacks &amp; Version History</span>
        </h1>
        <p class="text-xs text-slate-500 mt-1">
          Manage Docker Compose project files with versioned revisions, audit trail, and 1-click rollback.
        </p>
      </div>

      <div class="flex items-center gap-2">
        <router-link to="/containers" class="cyber-btn-secondary text-xs flex items-center gap-1.5 py-1.5 px-3">
          <ArrowLeft class="h-3.5 w-3.5" />
          <span>Containers &amp; Stacks View</span>
        </router-link>
      </div>
    </div>

    <!-- Main Content Area: Left Projects Sidebar, Center Editor, Right Revisions History -->
    <div class="grid grid-cols-1 lg:grid-cols-12 gap-5 items-start">
      <!-- 1. Left Sidebar: Project List -->
      <div class="lg:col-span-3 space-y-3">
        <div class="cyber-card !p-3 space-y-3">
          <div class="flex items-center justify-between border-b border-slate-200 pb-2">
            <h3 class="text-xs font-bold uppercase tracking-wider text-slate-800 flex items-center gap-1.5">
              <Folder class="h-3.5 w-3.5 text-indigo-600" />
              <span>Projects ({{ projects.length }})</span>
            </h3>
          </div>

          <input
            v-model="projectSearch"
            type="text"
            placeholder="Search projects..."
            class="cyber-input text-xs w-full !py-1.5 !px-2.5"
          />

          <div class="space-y-1 max-h-[650px] overflow-y-auto">
            <button
              v-for="p in filteredProjects"
              :key="p.id"
              @click="selectProject(p)"
              class="w-full text-left p-2.5 rounded-lg border text-xs transition flex flex-col gap-0.5"
              :class="selectedProject?.id === p.id ? 'bg-indigo-50 border-indigo-300 text-indigo-900 shadow-sm font-semibold' : 'border-slate-200 hover:bg-slate-50 text-slate-700'"
            >
              <div class="flex items-center justify-between w-full">
                <span class="truncate">{{ p.name }}</span>
                <span class="text-[10px] text-slate-400 font-mono">#{{ p.id }}</span>
              </div>
              <span class="text-[10px] text-slate-500 truncate font-mono">{{ p.path }}</span>
            </button>

            <div v-if="filteredProjects.length === 0" class="p-4 text-center text-xs text-slate-500 space-y-2">
              <p>No matching projects found.</p>
              <router-link
                to="/containers"
                class="inline-flex items-center gap-1.5 rounded-md bg-indigo-50 border border-indigo-200 px-2.5 py-1 text-xs font-semibold text-indigo-700 hover:bg-indigo-100 transition-colors"
              >
                <span>Launch in Containers</span>
                <span>&rarr;</span>
              </router-link>
            </div>
          </div>
        </div>
      </div>

      <!-- 2. Middle Column: Compose YAML Editor -->
      <div class="lg:col-span-5 space-y-3">
        <div class="cyber-card space-y-3">
          <div class="flex items-center justify-between border-b border-slate-200 pb-2 flex-wrap gap-2">
            <div>
              <h3 class="text-xs font-bold text-slate-900 font-mono flex items-center gap-1.5">
                <FileCode class="h-4 w-4 text-indigo-600" />
                <span>{{ selectedProject ? selectedProject.name : 'Select a Project' }}</span>
              </h3>
              <p class="text-[10px] text-slate-500 font-mono">{{ composeFilename }}</p>
            </div>

            <div class="flex items-center gap-2">
              <button
                @click="copyYAML(composeContent, 'editor')"
                :disabled="!composeContent"
                class="cyber-btn-secondary text-xs flex items-center gap-1 !py-1 !px-2.5"
                title="Copy current YAML"
              >
                <Check v-if="copiedKey === 'editor'" class="h-3 w-3 text-emerald-500" />
                <Copy v-else class="h-3 w-3" />
                <span>{{ copiedKey === 'editor' ? 'Copied' : 'Copy' }}</span>
              </button>

              <button
                @click="saveCompose"
                :disabled="editorSaving || editorLoading || !composeEditor.loaded || !selectedProject"
                class="cyber-btn text-xs flex items-center gap-1.5 !py-1 !px-3"
              >
                <RotateCcw v-if="editorSaving" class="h-3.5 w-3.5 animate-spin" />
                <Save v-else class="h-3.5 w-3.5" />
                <span>{{ editorSaving ? 'Saving...' : 'Save & Snapshot' }}</span>
              </button>
            </div>
          </div>

          <!-- Alerts -->
          <div v-if="editorSuccessMsg" class="p-2.5 rounded-lg border border-emerald-200 bg-emerald-50 text-emerald-800 text-xs font-mono flex items-center gap-2">
            <CheckCircle2 class="h-4 w-4 text-emerald-600 shrink-0" />
            <span>{{ editorSuccessMsg }}</span>
          </div>
          <div v-if="editorErrorMsg" class="p-2.5 rounded-lg border border-rose-200 bg-rose-50 text-rose-800 text-xs font-mono flex items-center gap-2">
            <AlertCircle class="h-4 w-4 text-rose-600 shrink-0" />
            <span>{{ editorErrorMsg }}</span>
          </div>

          <!-- Commit / Change Note Input -->
          <div class="space-y-1">
            <label class="text-[11px] text-slate-600 font-mono">Revision message (optional):</label>
            <input
              v-model="composeMessage"
              type="text"
              placeholder="e.g. Bump postgres to v16, expose port 8080"
              class="cyber-input text-xs w-full !py-1 !px-2.5 font-mono"
            />
          </div>

          <!-- Code Editor Area -->
          <div class="relative min-h-[460px] border border-slate-200 rounded-lg overflow-hidden bg-slate-900">
            <textarea
              v-model="composeContent"
              spellcheck="false"
              placeholder="version: '3.8'..."
              class="w-full h-full min-h-[460px] p-3 text-xs font-mono text-emerald-400 bg-slate-900 resize-y focus:outline-none focus:ring-1 focus:ring-indigo-500 leading-relaxed selection:bg-indigo-600"
            ></textarea>
          </div>
        </div>
      </div>

      <!-- 3. Right Column: Version History & Rollback -->
      <div class="lg:col-span-4 space-y-3">
        <div class="cyber-card space-y-3">
          <div class="flex items-center justify-between border-b border-slate-200 pb-2">
            <h3 class="text-xs font-bold uppercase tracking-wider text-slate-800 flex items-center gap-1.5">
              <History class="h-3.5 w-3.5 text-indigo-600" />
              <span>Revision History</span>
              <span class="rounded bg-indigo-50 border border-indigo-200 text-indigo-700 text-[10px] px-1.5 py-0.2 font-mono">
                {{ revisions.length }}
              </span>
            </h3>

            <button
              @click="selectedProject && loadRevisions(selectedProject.id)"
              :disabled="revisionsLoading || !selectedProject"
              class="text-slate-500 hover:text-slate-800 text-xs p-1 rounded"
              title="Refresh revisions"
            >
              <RotateCcw class="h-3.5 w-3.5" :class="revisionsLoading ? 'animate-spin' : ''" />
            </button>
          </div>

          <p class="text-[11px] text-slate-500">
            Every compose change creates an immutable revision. Rollback restores the configuration instantly.
          </p>

          <div v-if="revisionsLoading" class="py-12 text-center text-slate-500 text-xs font-mono">
            <RotateCcw class="h-5 w-5 animate-spin mx-auto text-indigo-600 mb-2" />
            Loading revisions...
          </div>

          <div v-else-if="revisions.length === 0" class="py-12 text-center text-slate-400 text-xs font-mono border border-dashed border-slate-200 rounded-lg">
            No revisions recorded yet. Save an edit to create the first version!
          </div>

          <!-- Revisions Timeline List -->
          <div v-else class="space-y-2.5 max-h-[600px] overflow-y-auto pr-1">
            <div
              v-for="rev in revisions"
              :key="rev.id"
              class="p-3 rounded-lg border text-xs font-mono space-y-2 transition"
              :class="previewRevision?.id === rev.id ? 'border-indigo-400 bg-indigo-50/40' : 'border-slate-200 bg-white hover:border-slate-300'"
            >
              <div class="flex items-center justify-between">
                <span class="font-bold text-slate-900 flex items-center gap-1.5">
                  <span class="rounded bg-slate-100 px-1.5 py-0.5 text-[11px] text-slate-700">Rev #{{ rev.id }}</span>
                  <span class="text-slate-500 text-[10px] font-normal truncate max-w-[120px]">{{ rev.filename }}</span>
                </span>
                <span class="text-[10px] text-slate-400 flex items-center gap-1">
                  <Clock class="h-3 w-3" />
                  {{ formatDate(rev.created_at) }}
                </span>
              </div>

              <div class="text-[11px] text-slate-700 font-sans italic bg-slate-50 p-1.5 rounded border border-slate-100">
                {{ rev.message || 'No description provided' }}
              </div>

              <div class="flex items-center justify-between text-[10px] text-slate-500 pt-1 border-t border-slate-100">
                <span>{{ rev.size }} bytes</span>

                <div class="flex items-center gap-1.5">
                  <button
                    @click="previewRevision = previewRevision?.id === rev.id ? null : rev"
                    class="cyber-btn-secondary !py-0.5 !px-2 text-[10px] flex items-center gap-1"
                  >
                    <Eye class="h-2.5 w-2.5" />
                    <span>{{ previewRevision?.id === rev.id ? 'Close' : 'View' }}</span>
                  </button>

                  <button
                    @click="rollbackTo(rev)"
                    :disabled="rollingBackId === rev.id"
                    class="cyber-btn !py-0.5 !px-2 text-[10px] bg-rose-600 hover:bg-rose-500 text-white flex items-center gap-1"
                    title="Rollback compose file to this version"
                  >
                    <RotateCcw class="h-2.5 w-2.5" :class="rollingBackId === rev.id ? 'animate-spin' : ''" />
                    <span>Rollback</span>
                  </button>
                </div>
              </div>

              <!-- Inline Preview of Revision Content -->
              <div v-if="previewRevision?.id === rev.id" class="pt-2 border-t border-indigo-200 space-y-1.5">
                <div class="flex items-center justify-between text-[10px]">
                  <span class="font-bold text-indigo-900">Snapshot Content (Rev #{{ rev.id }})</span>
                  <button @click="copyYAML(rev.content, 'rev-' + rev.id)" class="text-indigo-600 hover:underline">
                    {{ copiedKey === 'rev-' + rev.id ? 'Copied!' : 'Copy YAML' }}
                  </button>
                </div>
                <pre class="p-2.5 rounded bg-slate-900 text-slate-200 text-[10px] font-mono max-h-48 overflow-y-auto leading-relaxed">{{ rev.content }}</pre>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
