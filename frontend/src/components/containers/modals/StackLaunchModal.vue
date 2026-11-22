<script setup lang="ts">
import { ref, computed, nextTick, watch, onUnmounted } from 'vue'
import {
  Rocket,
  X,
  FileCode,
  Sparkles,
  Terminal,
  GitBranch,
  RotateCcw,
  FileText,
  ChevronDown,
  Play,
  Package,
} from 'lucide-vue-next'
import { createAsyncScope, createSerialPoller } from '../../../lib/async-lifecycle.mjs'
import { defaultComposeExample } from '../../../lib/compose-templates.mjs'
import { useApi } from '../../../composables/useApi'
import TemplateSetup from '../../TemplateSetup.vue'
import TemplateFiles from '../../TemplateFiles.vue'
import ComposeStorageReview from '../../ComposeStorageReview.vue'
import CommunityTemplates from '../../CommunityTemplates.vue'

const props = defineProps<{
  modelValue: boolean
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'close'): void
  (e: 'deployed', payload: { projectId: number; name: string; operationId?: string }): void
  (e: 'refresh'): void
}>()

const { apiFetch } = useApi()
const launchScope = createAsyncScope()

const launchTab = ref<'compose' | 'templates' | 'dockerrun' | 'gitstack'>('compose')

// Direct Compose Stack State
const composeDeployForm = ref({
  name: '',
  path: '',
  compose_content: '',
  env_content: '',
  show_env: false,
})
const composeDeploying = ref(false)
const composeStorageReady = ref(false)
const composeTemplateSource = ref('')
const composeProjectSource = ref('')
const composeFiles = ref<Record<string, any>>({})
const composeFilesDirty = ref(false)
const composeCompatibility = ref(true)
const composeReview = ref<any>(null)
const composeSetupDirty = ref(false)

async function applyTemplateFiles(files: Record<string, any>) {
  const current = launchScope.capture()
  composeFiles.value = files
  composeFilesDirty.value = false
  await nextTick()
  if (current()) await composeReview.value?.review()
}

async function applyTemplateSetup(result: any) {
  const current = launchScope.capture()
  composeDeployForm.value.compose_content = result.compose_content
  composeDeployForm.value.env_content = result.env_content
  await nextTick()
  if (current()) await composeReview.value?.review()
}

function selectCommunityTemplate(template: any) {
  composeDeployForm.value.name = template.name
  composeDeployForm.value.path = ''
  composeDeployForm.value.compose_content = template.compose_content
  composeDeployForm.value.env_content = template.env_content || ''
  composeDeployForm.value.show_env = true
  composeTemplateSource.value = template.source_url
  composeProjectSource.value = template.project_source || ''
  composeFiles.value = {}
  composeFilesDirty.value = false
  launchTab.value = 'compose'
}

function insertComposeSnippet(type: 'network' | 'restart' | 'ports' | 'volumes' | string) {
  const snippets: Record<string, string> = {
    network: '    networks:\n      - cloudflared\n\nnetworks:\n  cloudflared:\n    external: true',
    restart: '    restart: unless-stopped',
    ports: '    ports:\n      - "8080:80"',
    volumes: '    volumes:\n      - ./data:/data',
  }
  const content = snippets[type] || type
  composeDeployForm.value.compose_content += '\n' + content
}

async function deployComposeStack() {
  if (composeDeploying.value || dockerRunLoading.value || newProjectSaving.value) return
  if (composeTemplateSource.value && (composeSetupDirty.value || composeFilesDirty.value)) {
    alert('Apply the app setup settings before deploying')
    return
  }
  if (isPastedDockerRun.value) {
    convertComposeDockerRun()
  }
  const name = composeDeployForm.value.name.trim()
  if (!name) {
    alert('Stack name is required.')
    return
  }
  if (!composeDeployForm.value.compose_content.trim()) {
    alert('docker-compose.yml content cannot be empty.')
    return
  }
  if (!composeStorageReady.value) {
    alert('Review storage after editing this stack before deploying.')
    return
  }
  const current = launchScope.begin()
  composeDeploying.value = true
  try {
    const res: any = await apiFetch('/api/projects', {
      method: 'POST',
      body: JSON.stringify({
        name: name,
        path: composeDeployForm.value.path.trim(),
        compose_content: composeDeployForm.value.compose_content,
        env_content: composeDeployForm.value.env_content,
        auto_start: true,
        review_storage: true,
        template_source: composeTemplateSource.value,
        files: composeFiles.value,
        compatibility: Boolean(composeTemplateSource.value) && composeCompatibility.value,
      }),
    })
    if (!current()) return
    closeModal()
    emit('refresh')
    if (res.operation_error) {
      alert('Stack saved. Could not start the stack. ' + res.operation_error)
      return
    }
    if (res.id) {
      emit('deployed', { projectId: res.id, name, operationId: res.operation_id })
    }
  } catch (err: any) {
    if (current()) alert('Failed to deploy Compose stack: ' + (err.message || err))
  } finally {
    if (current()) composeDeploying.value = false
  }
}

// Docker Run State
const defaultExampleCmd = 'docker run -d --name my-app -p 8080:80 nginx:alpine'
const dockerRunCmd = ref('')
const dockerRunMode = ref<'compose' | 'direct'>('compose')
const dockerRunCloudflared = ref(false)
const dockerRunCreateProject = ref(true)
const dockerRunCustomName = ref('')
const dockerRunLoading = ref(false)
const dockerRunJob = ref<any>(null)
const dockerRunLines = ref<string[]>([])
let activeDockerRunJob = ''
const dockerRunPoller = createSerialPoller(async (pollCurrent: () => boolean) => {
  const current = launchScope.capture()
  const jobId = activeDockerRunJob
  const job: any = await apiFetch(`/api/docker/run/output/${jobId}`)
  if (!current() || !pollCurrent()) return true
  dockerRunJob.value = job
  dockerRunLines.value = job.lines || job.output || []
  if (job.done || job.status === 'completed' || job.status === 'failed') {
    dockerRunLoading.value = false
    emit('refresh')
    return true
  }
}, 1000, {
  onError: (error: any) => {
    dockerRunLoading.value = false
    dockerRunLines.value = [...dockerRunLines.value, 'Failed to load command output: ' + error.message]
  },
})

function insertDockerSnippet(snip: string) {
  if (dockerRunCmd.value.trim() === defaultExampleCmd.trim()) {
    dockerRunCmd.value = ''
  }
  dockerRunCmd.value = (dockerRunCmd.value.trim() + ' ' + snip).trim()
}

async function runDockerCommand() {
  if (composeDeploying.value || dockerRunLoading.value || newProjectSaving.value || !dockerRunCmd.value.trim()) return
  dockerRunPoller.stop()
  const current = launchScope.begin()
  dockerRunLoading.value = true
  dockerRunLines.value = []
  dockerRunJob.value = null

  try {
    const res: any = await apiFetch('/api/docker/run', {
      method: 'POST',
      body: JSON.stringify({
        command: dockerRunCmd.value.trim(),
        mode: dockerRunMode.value,
        connect_cloudflared: dockerRunCloudflared.value,
        create_project: dockerRunCreateProject.value,
        project_name: dockerRunCustomName.value.trim(),
      }),
    })
    if (!current()) return
    if (!res.job_id) throw new Error('Command started without an output job ID')
    activeDockerRunJob = String(res.job_id)
    dockerRunPoller.start(1000)
  } catch (e: any) {
    if (!current()) return
    dockerRunLoading.value = false
    alert(e.message || 'Failed to execute Docker run')
  }
}

function convertDockerRunToComposeYaml(cmd: string): { name: string; yaml: string } {
  const cleanCmd = cmd.replace(/\\\r?\n/g, ' ').replace(/\s+/g, ' ').trim()
  const tokens = cleanCmd.match(/(?:[^\s"']+|"[^"]*"|'[^']*')+/g) || []

  let name = ''
  let image = ''
  const ports: string[] = []
  const volumes: string[] = []
  const env: string[] = []
  let restart = 'unless-stopped'
  let network = 'cloudflared'
  let privileged = false

  let i = 0
  if (tokens[0] === 'docker' || tokens[0] === 'podman') i++
  if (tokens[i] === 'run' || tokens[i] === 'create') i++

  for (; i < tokens.length; i++) {
    const rawT = tokens[i]
    const t = rawT.replace(/^["']|["']$/g, '')
    if (t === '--name' && i + 1 < tokens.length) {
      name = tokens[++i].replace(/^["']|["']$/g, '')
    } else if (t.startsWith('--name=')) {
      name = t.substring(7).replace(/^["']|["']$/g, '')
    } else if ((t === '-p' || t === '--publish') && i + 1 < tokens.length) {
      ports.push(tokens[++i].replace(/^["']|["']$/g, ''))
    } else if (t.startsWith('-p=') || t.startsWith('--publish=')) {
      ports.push(t.substring(t.indexOf('=') + 1).replace(/^["']|["']$/g, ''))
    } else if ((t === '-v' || t === '--volume') && i + 1 < tokens.length) {
      volumes.push(tokens[++i].replace(/^["']|["']$/g, ''))
    } else if (t.startsWith('-v=') || t.startsWith('--volume=')) {
      volumes.push(t.substring(t.indexOf('=') + 1).replace(/^["']|["']$/g, ''))
    } else if ((t === '-e' || t === '--env') && i + 1 < tokens.length) {
      env.push(tokens[++i].replace(/^["']|["']$/g, ''))
    } else if (t.startsWith('-e=') || t.startsWith('--env=')) {
      env.push(t.substring(t.indexOf('=') + 1).replace(/^["']|["']$/g, ''))
    } else if (t === '--restart' && i + 1 < tokens.length) {
      restart = tokens[++i].replace(/^["']|["']$/g, '')
    } else if (t.startsWith('--restart=')) {
      restart = t.substring(10).replace(/^["']|["']$/g, '')
    } else if (t === '--privileged') {
      privileged = true
    } else if ((t === '--network' || t === '--net') && i + 1 < tokens.length) {
      network = tokens[++i].replace(/^["']|["']$/g, '')
    } else if (t.startsWith('--network=') || t.startsWith('--net=')) {
      network = t.substring(t.indexOf('=') + 1).replace(/^["']|["']$/g, '')
    } else if (!t.startsWith('-')) {
      image = t
    }
  }

  if (!name && image) {
    const parts = image.split('/')
    const imgPart = parts[parts.length - 1].split(':')[0]
    name = imgPart || 'app'
  }
  if (!name) name = 'app'

  let yaml = `services:\n  ${name}:\n    image: ${image || 'nginx:alpine'}\n    container_name: ${name}\n    restart: ${restart}\n`
  if (privileged) yaml += '    privileged: true\n'
  if (ports.length) {
    yaml += '    ports:\n'
    ports.forEach((p) => (yaml += `      - "${p}"\n`))
  }
  if (volumes.length) {
    yaml += '    volumes:\n'
    volumes.forEach((v) => (yaml += `      - "${v}"\n`))
  }
  if (env.length) {
    yaml += '    environment:\n'
    env.forEach((e) => (yaml += `      - ${e}\n`))
  }
  if (network && network !== 'host' && network !== 'none' && network !== 'bridge') {
    yaml += `    networks:\n      - ${network}\n\nnetworks:\n  ${network}:\n    external: true\n`
  }

  const namedVols = volumes
    .map((v) => v.split(':')[0])
    .filter((v) => v && !v.startsWith('/') && !v.startsWith('./') && !v.startsWith('../') && !v.startsWith('~') && !v.includes('\\'))
  if (namedVols.length) {
    yaml += '\nvolumes:\n'
    const seen = new Set<string>()
    namedVols.forEach((nv) => {
      if (!seen.has(nv)) {
        seen.add(nv)
        yaml += `  ${nv}:\n    name: ${nv}\n`
      }
    })
  }

  return { name, yaml }
}

const isPastedDockerRun = computed(() => {
  const c = composeDeployForm.value.compose_content.trim()
  if (!c) return false
  if (c.startsWith('services:') || c.startsWith('version:')) return false
  const lower = c.toLowerCase()
  return (
    lower.startsWith('docker run') ||
    lower.startsWith('docker create') ||
    lower.startsWith('docker pull') ||
    lower.startsWith('run -') ||
    (c.includes('--name') && c.includes('-p ') && !c.includes(':'))
  )
})

function convertComposeDockerRun() {
  const result = convertDockerRunToComposeYaml(composeDeployForm.value.compose_content)
  composeDeployForm.value.compose_content = result.yaml
  if (!composeDeployForm.value.name.trim() || composeDeployForm.value.name === 'my-stack') {
    composeDeployForm.value.name = result.name
  }
}

function onComposeFocus() {
  if (composeDeployForm.value.compose_content.trim() === defaultComposeExample.trim()) {
    composeDeployForm.value.compose_content = ''
  }
}

function onComposePaste() {
  if (composeDeployForm.value.compose_content.trim() === defaultComposeExample.trim()) {
    composeDeployForm.value.compose_content = ''
  }
}

function onDockerRunFocus() {
  if (dockerRunCmd.value.trim() === defaultExampleCmd.trim()) {
    dockerRunCmd.value = ''
  }
}

function onDockerRunPaste() {
  if (dockerRunCmd.value.trim() === defaultExampleCmd.trim()) {
    dockerRunCmd.value = ''
  }
}

// Git / Compose Stack Creator State
const newProjectForm = ref({
  name: '',
  path: '',
  repo_url: '',
  git_username: '',
  git_token: '',
})
const newProjectSaving = ref(false)

async function submitNewProject() {
  if (composeDeploying.value || dockerRunLoading.value || newProjectSaving.value) return
  if (!newProjectForm.value.name.trim() || !newProjectForm.value.path.trim()) {
    alert('Stack name and folder path are required.')
    return
  }
  const current = launchScope.begin()
  newProjectSaving.value = true
  try {
    await apiFetch('/api/projects', {
      method: 'POST',
      body: JSON.stringify(newProjectForm.value),
    })
    if (!current()) return
    closeModal()
    newProjectForm.value = { name: '', path: '', repo_url: '', git_username: '', git_token: '' }
    emit('refresh')
    alert('Stack created successfully!')
  } catch (e: any) {
    if (current()) alert('Failed to create stack: ' + (e.message || e))
  } finally {
    if (current()) newProjectSaving.value = false
  }
}

function resetLaunchMonitoring() {
  launchScope.invalidate()
  dockerRunPoller.stop()
  activeDockerRunJob = ''
  composeDeploying.value = dockerRunLoading.value = newProjectSaving.value = false
}

function closeModal() {
  resetLaunchMonitoring()
  emit('close')
  emit('update:modelValue', false)
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
    if (!composeDeployForm.value.compose_content.trim() && !dockerRunCmd.value.trim()) {
      closeModal()
    }
  }
  backdropMouseDown = false
}

watch(
  () => props.modelValue,
  (val) => {
    resetLaunchMonitoring()
    if (val) {
      dockerRunLines.value = []
      dockerRunJob.value = null
    }
  },
  { flush: 'sync' }
)

onUnmounted(() => {
  launchScope.dispose()
  dockerRunPoller.stop()
})
</script>

<template>
  <div
    v-if="modelValue"
    class="fixed inset-0 z-50 flex items-center justify-center bg-slate-50/85 p-4 backdrop-blur-md"
    @mousedown="onBackdropMouseDown"
    @click="onBackdropClick"
  >
    <div
      class="w-full rounded-2xl border border-slate-200 bg-white p-4 sm:p-6 shadow-xl space-y-4 max-h-[94vh] flex flex-col"
      :class="launchTab === 'templates' ? 'max-w-6xl' : 'max-w-4xl'"
      role="dialog"
      aria-modal="true"
      aria-label="Deploy an app or stack"
      @mousedown.stop
      @mouseup.stop
      @click.stop
    >
      <div class="flex items-start justify-between border-b border-slate-200 pb-3 shrink-0">
        <div>
          <h3 class="text-lg font-bold text-slate-900 flex items-center gap-2">
            <Rocket class="h-5 w-5 text-cyan-600" />
            <span>Deploy New Container or Stack</span>
          </h3>
          <p class="text-xs text-slate-600">
            Launch a Docker Compose stack, use app templates, execute Docker run commands, or connect a Git repository.
          </p>
        </div>
        <button @click="closeModal" class="text-slate-600 hover:text-slate-800">
          <X class="h-5 w-5" />
        </button>
      </div>

      <!-- Mode Tabs -->
      <div class="flex gap-2 border-b border-slate-200 pb-2 shrink-0 overflow-x-auto">
        <button
          @click="launchTab = 'compose'"
          class="flex items-center gap-2 rounded px-3 py-1.5 text-xs font-semibold transition shrink-0"
          :class="launchTab === 'compose' ? 'bg-cyan-50/80 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
        >
          <FileCode class="h-3.5 w-3.5" />
          <span>Docker Compose (YAML)</span>
        </button>
        <button
          @click="launchTab = 'templates'"
          class="flex items-center gap-2 rounded px-3 py-1.5 text-xs font-semibold transition shrink-0"
          :class="launchTab === 'templates' ? 'bg-cyan-50/80 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
        >
          <Sparkles class="h-3.5 w-3.5 text-amber-600" />
          <span>App Templates</span>
        </button>
        <button
          @click="launchTab = 'dockerrun'"
          class="flex items-center gap-2 rounded px-3 py-1.5 text-xs font-semibold transition shrink-0"
          :class="launchTab === 'dockerrun' ? 'bg-cyan-50/80 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
        >
          <Terminal class="h-3.5 w-3.5" />
          <span>Docker Run Command</span>
        </button>
        <button
          @click="launchTab = 'gitstack'"
          class="flex items-center gap-2 rounded px-3 py-1.5 text-xs font-semibold transition shrink-0"
          :class="launchTab === 'gitstack' ? 'bg-cyan-50/80 text-cyan-700 border border-cyan-800/60' : 'text-slate-600 hover:text-slate-800'"
        >
          <GitBranch class="h-3.5 w-3.5" />
          <span>Git / Remote Stack</span>
        </button>
      </div>

      <!-- TAB 1: Docker Compose YAML Editor -->
      <div v-if="launchTab === 'compose'" class="space-y-4 overflow-y-auto pr-1 flex-1">
        <div class="grid gap-3 text-xs" :class="{ 'sm:grid-cols-2': !composeTemplateSource }">
          <div>
            <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">
              Stack / Project Name <span class="text-rose-600">*</span>
            </label>
            <input
              v-model="composeDeployForm.name"
              class="cyber-input text-xs font-mono"
              placeholder="e.g. my-stack"
            />
          </div>
          <div v-if="!composeTemplateSource">
            <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">
              Host Working Directory Path (Optional)
            </label>
            <input
              v-model="composeDeployForm.path"
              class="cyber-input text-xs font-mono"
              placeholder="Leave empty to use DockTun's persistent projects folder"
            />
          </div>
        </div>

        <TemplateSetup
          v-if="composeTemplateSource"
          :input="{ compose_content: composeDeployForm.compose_content, env_content: composeDeployForm.env_content }"
          @apply="applyTemplateSetup"
          @dirty="composeSetupDirty = $event"
        />
        <details :open="!composeTemplateSource" class="rounded-xl border border-slate-200 p-3 space-y-3">
          <summary class="text-xs font-medium text-slate-700 cursor-pointer">Advanced: Compose &amp; Environment</summary>
          <div v-if="composeTemplateSource" class="space-y-1">
            <label class="block text-xs text-slate-600">Project folder (optional)</label>
            <input
              v-model="composeDeployForm.path"
              class="cyber-input text-xs font-mono"
              placeholder="Leave empty to create the project in DockTun's projects storage"
            />
            <p class="text-[11px] text-slate-500">Override only if you want the project files in another mounted folder.</p>
          </div>
          <div>
            <!-- Banner when docker run command is pasted in Compose YAML tab -->
            <div
              v-if="isPastedDockerRun"
              class="mb-2.5 rounded-lg border border-cyan-500/60 bg-cyan-50/80 p-2.5 flex flex-col sm:flex-row items-start sm:items-center justify-between gap-2 text-xs"
            >
              <div class="flex items-center gap-2 text-cyan-800">
                <Sparkles class="h-4 w-4 text-cyan-600 shrink-0" />
                <span><strong>Docker Run command detected!</strong> Convert to clean Compose YAML, or DockTun will auto-convert when you deploy.</span>
              </div>
              <div class="flex items-center gap-2 shrink-0">
                <button
                  type="button"
                  @click="convertComposeDockerRun"
                  class="cyber-btn py-1 px-2.5 text-[11px] text-cyan-700 bg-white border-cyan-600 font-bold"
                >
                  Convert to Compose YAML
                </button>
                <button
                  type="button"
                  @click="launchTab = 'dockerrun'; dockerRunCmd = composeDeployForm.compose_content"
                  class="text-[11px] text-slate-600 hover:text-slate-900 underline font-mono"
                >
                  Switch to Docker Run
                </button>
              </div>
            </div>

            <div class="flex flex-wrap items-center justify-between gap-1.5 mb-1.5">
              <div class="flex items-center gap-2">
                <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-600">
                  docker-compose.yml Content <span class="text-rose-600">*</span>
                </label>
                <button
                  type="button"
                  @click="composeDeployForm.compose_content = defaultComposeExample; if (!composeDeployForm.name) composeDeployForm.name = 'my-app'"
                  class="text-[10px] text-cyan-600 hover:text-cyan-800 underline font-mono"
                >
                  Insert Example
                </button>
                <button
                  v-if="composeDeployForm.compose_content"
                  type="button"
                  @click="composeDeployForm.compose_content = ''"
                  class="text-[10px] text-slate-500 hover:text-rose-600 underline font-mono"
                >
                  Clear
                </button>
              </div>
              <div class="flex flex-wrap items-center gap-1 text-xs">
                <span class="text-[10px] uppercase font-semibold text-slate-500 mr-0.5">Quick Insert:</span>
                <button
                  type="button"
                  @click="insertComposeSnippet('network')"
                  class="rounded border border-slate-200 bg-slate-50 px-1.5 py-0.5 font-mono text-[9px] text-cyan-700 hover:border-cyan-600 hover:bg-white transition"
                >
                  + Cloudflared Net
                </button>
                <button
                  type="button"
                  @click="insertComposeSnippet('restart')"
                  class="rounded border border-slate-200 bg-slate-50 px-1.5 py-0.5 font-mono text-[9px] text-cyan-700 hover:border-cyan-600 hover:bg-white transition"
                >
                  + Restart
                </button>
                <button
                  type="button"
                  @click="insertComposeSnippet('ports')"
                  class="rounded border border-slate-200 bg-slate-50 px-1.5 py-0.5 font-mono text-[9px] text-cyan-700 hover:border-cyan-600 hover:bg-white transition"
                >
                  + Port Map
                </button>
                <button
                  type="button"
                  @click="insertComposeSnippet('volumes')"
                  class="rounded border border-slate-200 bg-slate-50 px-1.5 py-0.5 font-mono text-[9px] text-cyan-700 hover:border-cyan-600 hover:bg-white transition"
                >
                  + Volume
                </button>
              </div>
            </div>
            <textarea
              v-model="composeDeployForm.compose_content"
              rows="12"
              class="cyber-input font-mono text-xs w-full leading-relaxed bg-slate-50 border-slate-200 text-slate-800"
              placeholder="services:&#10;  app:&#10;    image: nginx:alpine&#10;    restart: unless-stopped"
              spellcheck="false"
              @focus="onComposeFocus"
              @paste="onComposePaste"
            ></textarea>
          </div>

          <!-- Collapsible .env Editor -->
          <div class="border border-slate-200 rounded-lg bg-slate-50/60 p-3 space-y-2">
            <button
              type="button"
              @click="composeDeployForm.show_env = !composeDeployForm.show_env"
              class="w-full flex items-center justify-between text-xs font-semibold text-slate-700 hover:text-slate-900"
            >
              <div class="flex items-center gap-2">
                <FileText class="h-3.5 w-3.5 text-amber-600" />
                <span>Environment Variables (.env file)</span>
                <span class="text-[9px] uppercase px-1.5 py-0.2 rounded bg-slate-100 text-slate-600">Optional</span>
              </div>
              <ChevronDown class="h-4 w-4 transition-transform" :class="composeDeployForm.show_env ? 'rotate-180' : ''" />
            </button>
            <div v-if="composeDeployForm.show_env" class="pt-2">
              <textarea
                v-model="composeDeployForm.env_content"
                rows="4"
                class="cyber-input font-mono text-xs w-full leading-relaxed bg-slate-50 border-slate-200 text-slate-800"
                placeholder="KEY=value&#10;POSTGRES_PASSWORD=secret"
                spellcheck="false"
              ></textarea>
            </div>
          </div>
        </details>

        <TemplateFiles
          v-if="composeTemplateSource"
          :input="{ compose_content: composeDeployForm.compose_content, env_content: composeDeployForm.env_content }"
          :source="composeProjectSource"
          :files="composeFiles"
          @apply="applyTemplateFiles"
          @dirty="composeFilesDirty = $event"
          @source="composeProjectSource = $event"
          @environment="composeDeployForm.env_content = $event"
        />
        <label v-if="composeTemplateSource" class="flex items-center gap-2 text-xs text-slate-700">
          <input v-model="composeCompatibility" type="checkbox" />
          <span>Check image architecture, availability, disk space and app permissions</span>
        </label>
        <p v-if="composeTemplateSource" class="text-xs text-slate-500 break-all">
          Template source: {{ composeTemplateSource }}
        </p>
        <ComposeStorageReview
          ref="composeReview"
          :input="{
            name: composeDeployForm.name,
            path: composeDeployForm.path,
            compose_content: composeDeployForm.compose_content,
            env_content: composeDeployForm.env_content,
            files: composeFiles,
            compatibility: Boolean(composeTemplateSource) && composeCompatibility
          }"
          @ready="composeStorageReady = $event"
        />

        <div class="flex items-center justify-end gap-3 pt-2 border-t border-slate-200">
          <button @click="closeModal" class="cyber-btn-secondary text-xs">Cancel</button>
          <button
            @click="deployComposeStack"
            :disabled="composeDeploying || (composeTemplateSource && (composeSetupDirty || composeFilesDirty)) || !composeStorageReady || !composeDeployForm.name.trim() || !composeDeployForm.compose_content.trim()"
            class="cyber-btn flex items-center gap-2 text-xs text-cyan-700 border-cyan-500/80 bg-cyan-50/40 hover:bg-cyan-100/60 font-bold"
          >
            <RotateCcw v-if="composeDeploying" class="h-3.5 w-3.5 animate-spin" />
            <Rocket v-else class="h-3.5 w-3.5" />
            <span>{{ composeDeploying ? 'Deploying Stack…' : 'Deploy Compose Stack (up -d)' }}</span>
          </button>
        </div>
      </div>

      <!-- TAB 2: Compose App Templates -->
      <div v-if="launchTab === 'templates'" class="space-y-4 overflow-y-auto pr-1 flex-1">
        <CommunityTemplates @select="selectCommunityTemplate" />
      </div>

      <!-- TAB 3: Docker Run -->
      <div v-if="launchTab === 'dockerrun'" class="space-y-4 overflow-y-auto pr-1 flex-1">
        <div>
          <div class="flex items-center justify-between mb-1">
            <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-600">
              Docker Run Command
            </label>
            <div class="flex items-center gap-2">
              <button
                type="button"
                @click="dockerRunCmd = defaultExampleCmd"
                class="text-[10px] text-cyan-600 hover:text-cyan-800 underline font-mono"
              >
                Insert Example
              </button>
              <button
                v-if="dockerRunCmd"
                type="button"
                @click="dockerRunCmd = ''"
                class="text-[10px] text-slate-500 hover:text-rose-600 underline font-mono"
              >
                Clear
              </button>
            </div>
          </div>
          <textarea
            v-model="dockerRunCmd"
            rows="3"
            class="cyber-input font-mono text-xs w-full leading-relaxed"
            :placeholder="defaultExampleCmd"
            @focus="onDockerRunFocus"
            @paste="onDockerRunPaste"
          ></textarea>
        </div>

        <!-- Snippet helper buttons -->
        <div class="flex flex-wrap items-center gap-1.5 text-xs">
          <span class="text-[10px] font-semibold uppercase tracking-wider text-slate-500 mr-1">Insert:</span>
          <button
            v-for="snip in ['--restart unless-stopped', '-p 8080:80', '-v /data:/data', '-e KEY=val', '--network cloudflared']"
            :key="snip"
            type="button"
            @click="insertDockerSnippet(snip)"
            class="rounded border border-slate-200 bg-slate-50 px-2 py-0.5 font-mono text-[10px] text-cyan-700 hover:border-cyan-600 hover:bg-white transition"
          >
            + {{ snip }}
          </button>
        </div>

        <div class="grid gap-3 sm:grid-cols-2 text-xs">
          <div>
            <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">Execution Mode</label>
            <div class="space-y-1">
              <label class="flex items-center gap-2 cursor-pointer text-slate-700">
                <input type="radio" v-model="dockerRunMode" value="compose" class="accent-cyan-500" />
                <span>Convert to Compose Stack (Recommended)</span>
              </label>
              <label class="flex items-center gap-2 cursor-pointer text-slate-700">
                <input type="radio" v-model="dockerRunMode" value="direct" class="accent-cyan-500" />
                <span>Direct Docker Run</span>
              </label>
            </div>
          </div>

          <div>
            <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">Custom Project / Stack Name</label>
            <input v-model="dockerRunCustomName" class="cyber-input text-xs" placeholder="Leave blank to infer from --name" />
          </div>
        </div>

        <!-- Output Log -->
        <div
          v-if="dockerRunLines.length > 0 || dockerRunLoading"
          class="max-h-48 overflow-y-auto rounded-lg border border-slate-200 bg-slate-50 p-3 font-mono text-[11px] text-slate-700 leading-relaxed shadow-inner"
        >
          <div v-for="(line, idx) in dockerRunLines" :key="idx">{{ line }}</div>
          <div v-if="dockerRunLoading" class="text-cyan-600 animate-pulse mt-1">&rarr; Running command...</div>
        </div>

        <div class="flex items-center justify-end gap-3 pt-2 border-t border-slate-200">
          <button @click="closeModal" class="cyber-btn-secondary text-xs">Cancel</button>
          <button
            @click="runDockerCommand"
            :disabled="dockerRunLoading || !dockerRunCmd.trim()"
            class="cyber-btn flex items-center gap-2 text-xs text-cyan-700 border-cyan-500/80 bg-cyan-50/40 hover:bg-cyan-100/60 font-bold"
          >
            <RotateCcw v-if="dockerRunLoading" class="h-3.5 w-3.5 animate-spin" />
            <Play v-else class="h-3.5 w-3.5 fill-current" />
            <span>{{ dockerRunLoading ? 'Executing…' : 'Run Container' }}</span>
          </button>
        </div>
      </div>

      <!-- TAB 4: Git / Compose Stack -->
      <div v-if="launchTab === 'gitstack'" class="space-y-4 overflow-y-auto pr-1 flex-1">
        <div class="grid gap-3 sm:grid-cols-2 text-xs">
          <div>
            <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">Stack Name</label>
            <input v-model="newProjectForm.name" class="cyber-input text-xs font-mono" placeholder="e.g. mediastorm" />
          </div>
          <div>
            <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">Host Directory Path</label>
            <input v-model="newProjectForm.path" class="cyber-input text-xs font-mono" placeholder="./projects/mediastorm" />
          </div>
          <div class="sm:col-span-2">
            <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">Git Repository URL (Optional)</label>
            <input v-model="newProjectForm.repo_url" class="cyber-input text-xs font-mono" placeholder="https://github.com/owner/repo.git" />
          </div>
          <div>
            <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">Git Username (Optional)</label>
            <input v-model="newProjectForm.git_username" class="cyber-input text-xs font-mono" placeholder="oauth2 or username" />
          </div>
          <div>
            <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-600">Git Token / PAT (Optional)</label>
            <input v-model="newProjectForm.git_token" type="password" class="cyber-input text-xs font-mono" placeholder="ghp_••••••••••••" />
          </div>
        </div>

        <div class="flex items-center justify-end gap-3 pt-2 border-t border-slate-200">
          <button @click="closeModal" class="cyber-btn-secondary text-xs">Cancel</button>
          <button
            @click="submitNewProject"
            :disabled="newProjectSaving || !newProjectForm.name.trim() || !newProjectForm.path.trim()"
            class="cyber-btn flex items-center gap-2 text-xs text-emerald-700 border-emerald-500/80 bg-emerald-50/40 hover:bg-emerald-100/60 font-bold"
          >
            <RotateCcw v-if="newProjectSaving" class="h-3.5 w-3.5 animate-spin" />
            <Package v-else class="h-3.5 w-3.5" />
            <span>{{ newProjectSaving ? 'Creating…' : 'Register & Initialize Stack' }}</span>
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
