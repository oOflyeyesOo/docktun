<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useApi } from '../composables/useApi'
const props = defineProps<{ input: { compose_content: string; env_content: string }; source: string; files: Record<string, any> }>()
const emit = defineEmits<{ apply: [Record<string, any>]; dirty: [boolean]; source: [string]; environment: [string] }>()
const { apiFetch } = useApi()
const plan = ref<any>(null), local = ref<Record<string, any>>({}), error = ref(''), busy = ref(false), reveal = ref(false), newPath = ref('')
let version = 0, timer: ReturnType<typeof setTimeout> | undefined
watch(() => JSON.stringify(props.files), () => { local.value = JSON.parse(JSON.stringify(props.files)) }, { immediate: true, flush: 'sync' })
watch(() => JSON.stringify(local.value), current => emit('dirty', current !== JSON.stringify(props.files)), { flush: 'sync' })
watch(() => JSON.stringify([props.input, props.source]), () => { version++; clearTimeout(timer); timer = setTimeout(inspect, 300) }, { immediate: true })
onUnmounted(() => { version++; clearTimeout(timer) })
const names = computed(() => Object.keys(local.value).sort())
const invalid = computed(() => Object.values(local.value).some(file => file.secret && (!file.content.trim() || file.sample)))
async function inspect() {
  const request = ++version
  try { const response = await apiFetch('/api/templates/files', { method: 'POST', body: JSON.stringify(props.input) }); if (request === version) plan.value = response }
  catch (e: any) { if (request === version) error.value = e.message }
}
async function fetchFiles() {
  const request = ++version; busy.value = true; error.value = ''
  try {
    const response = await apiFetch('/api/templates/files', { method: 'POST', body: JSON.stringify({ ...props.input, source_url: props.source, fetch: true }) })
    if (request !== version) return
    plan.value = response
    for (const [name, file] of Object.entries(response.files)) if (!local.value[name]) local.value[name] = file
    if (response.source_url) emit('source', response.source_url)
  } catch (e: any) { if (request === version) error.value = e.message }
  finally { busy.value = false }
}
function create(path: string, secret = false) {
  if (!path.trim() || local.value[path]) return
  local.value[path.trim()] = { content: '', secret: secret || /(^|\/)(secrets?)(\/|$)|password|token|private[-_]key/i.test(path) }; newPath.value = ''
}
function generate(path: string) {
  local.value[path].content = Array.from(crypto.getRandomValues(new Uint8Array(24)), byte => byte.toString(16).padStart(2, '0')).join(''); local.value[path].sample = false
}
function edited(path: string) { local.value[path].sample = false }
function owner(path: string, key: string, event: Event) {
  const value = (event.target as HTMLInputElement).value
  if (value === '') delete local.value[path][key]
  else local.value[path][key] = Number(value)
}
</script>
<template>
  <section class="rounded-xl border border-slate-200 bg-slate-50 p-4 space-y-3">
    <div class="flex items-center justify-between gap-2 flex-wrap"><div><h3 class="text-sm font-semibold text-slate-900">Supporting files &amp; secrets</h3><p class="text-xs text-slate-600 mt-1">Configuration files and build assets are saved with this stack. Nothing is written until deployment.</p></div><button v-if="source" type="button" class="cyber-btn-secondary text-xs" :disabled="busy" @click="fetchFiles">{{ busy ? 'Fetching files…' : 'Fetch repository files' }}</button></div>
    <p v-if="source" class="text-[10px] text-slate-500 break-all">Pinned source: {{ source }}</p>
    <p v-if="error" role="alert" class="text-xs text-red-700">{{ error }}</p>
    <div v-for="requirement in plan?.requirements || []" :key="requirement.path" class="flex flex-wrap items-center justify-between gap-2 text-xs text-slate-600"><span><code>{{ requirement.path }}</code> · {{ requirement.kind }} · {{ requirement.required ? 'required' : 'optional' }} {{ local[requirement.path] ? '✓ added' : '' }}</span><button v-if="requirement.path !== '.env' && !requirement.kind.includes('folder') && !local[requirement.path]" type="button" class="cyber-btn-secondary text-xs" @click="create(requirement.path, requirement.secret)">Create file</button></div>
    <button v-if="plan?.env_example" type="button" class="cyber-btn-secondary text-xs" @click="emit('environment', plan.env_example)">Use repository Environment example in app settings</button>
    <p v-if="plan?.requirements?.some(r => r.path === '.env')" class="text-xs text-slate-600">This app reads the stack Environment file. Its values are editable in app settings above; an example can supply the initial fields.</p>
    <p v-for="note in plan?.notes || []" :key="note" class="text-xs text-amber-800">{{ note }}</p>
    <p v-if="!plan?.requirements?.length && !names.length" class="text-xs text-slate-500">No separate files are declared by this template. Add a file below if the app's instructions require one.</p>
    <label v-if="names.some(name => local[name].secret)" class="flex items-center gap-2 text-xs text-slate-600"><input v-model="reveal" type="checkbox">Show and edit secret file contents</label>
    <details v-for="name in names" :key="name" class="rounded-lg border border-slate-200 bg-white p-3 text-xs">
      <summary class="cursor-pointer font-medium text-slate-800 break-all">{{ name }} · {{ local[name].secret ? 'secret' : 'project file' }}{{ local[name].sample ? ' · replace public sample' : '' }}</summary>
      <div class="pt-3 space-y-2">
        <p v-if="local[name].source" class="text-[10px] text-slate-500 break-all">{{ local[name].source }}</p>
        <p v-if="local[name].encoding === 'base64'" class="text-xs text-slate-500">Binary repository asset; preserved without text conversion.</p>
        <textarea v-else-if="!local[name].secret || reveal" v-model="local[name].content" :aria-label="`Contents of ${name}`" class="cyber-input text-xs font-mono text-slate-800" rows="5" spellcheck="false" @input="edited(name)"></textarea>
        <p v-else class="text-slate-500">{{ local[name].content ? 'Secret content is hidden.' : 'Provide secret content or generate a new password.' }}</p>
        <button v-if="local[name].secret && /pass(word|wd)?/i.test(name) && !/hash|bcrypt|argon|pem|crt|salt|key/i.test(name)" type="button" class="cyber-btn-secondary text-xs" @click="generate(name)">Generate secret password</button>
        <label class="flex gap-2 items-center text-slate-600"><input v-model="local[name].secret" type="checkbox">Private secret file (mode 600)</label>
        <details class="text-slate-600"><summary class="cursor-pointer">File owner for the app (optional)</summary><p class="text-[11px] mt-2">Use the app's numeric UID/GID when a non-root app must read a private secret. Existing files are never changed by this form.</p><div class="grid grid-cols-2 gap-2 mt-2"><label>Owner UID<input :value="local[name].owner_uid ?? ''" :aria-label="`Owner UID for ${name}`" class="cyber-input text-xs" inputmode="numeric" @input="owner(name, 'owner_uid', $event)"></label><label>Owner GID<input :value="local[name].owner_gid ?? ''" :aria-label="`Owner GID for ${name}`" class="cyber-input text-xs" inputmode="numeric" @input="owner(name, 'owner_gid', $event)"></label></div></details>
        <button type="button" class="text-xs text-red-700" @click="delete local[name]">Remove from draft</button>
      </div>
    </details>
    <div class="flex gap-2"><input v-model="newPath" aria-label="New supporting file path" placeholder="Add project-relative file, for example secrets/db-password.txt" class="cyber-input text-xs min-w-0"><button type="button" class="cyber-btn-secondary text-xs" :disabled="!newPath.trim()" @click="create(newPath)">Add file</button></div>
    <p v-if="invalid" class="text-xs text-amber-800">Fill secret files and replace fetched public sample secrets before applying.</p>
    <button v-if="names.length || Object.keys(files).length" type="button" class="cyber-btn-secondary text-xs w-full" :disabled="busy || invalid" @click="emit('apply', JSON.parse(JSON.stringify(local)))">Apply files &amp; check app</button>
  </section>
</template>
