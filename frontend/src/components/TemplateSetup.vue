<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useApi } from '../composables/useApi'
import { defaultStorageChoices, storageGroups as groupStorage } from '../lib/template-library.mjs'
const props = defineProps<{ input: { compose_content: string; env_content: string } }>()
const emit = defineEmits<{ apply: [any]; dirty: [boolean] }>()
const { apiFetch } = useApi()
const schema = ref<any>(null)
const values = ref<Record<string, string>>({})
const storage = ref<Record<string, { mode: string; source: string }>>({})
const linked = ref<Record<string, boolean>>({})
const connect = ref(true)
const portNotes = ref<string[]>([])
const busy = ref(false), error = ref(''), showSecrets = ref(false)
let appliedKey = ''
const appliedForm = ref('')
let sourceDraft = { ...props.input }
let version = 0, timer: ReturnType<typeof setTimeout> | undefined
const key = computed(() => JSON.stringify(props.input))
watch(key, () => {
  if (key.value === appliedKey) { appliedKey = ''; return }
  appliedKey = ''; emit('dirty', true)
  version++; clearTimeout(timer); schema.value = null; error.value = ''
  timer = setTimeout(inspect, 250)
}, { immediate: true, flush: 'sync' })
onUnmounted(() => { version++; clearTimeout(timer) })
async function inspect() {
  const request = ++version; busy.value = true
  try {
    const response = await apiFetch('/api/templates/setup', { method: 'POST', body: JSON.stringify(props.input) })
    if (request !== version) return
    sourceDraft = { ...props.input }; appliedForm.value = ''
    schema.value = response
    values.value = Object.fromEntries(response.fields.map((field: any) => [field.id, field.value]))
    storage.value = defaultStorageChoices(response.storage)
    linked.value = Object.fromEntries((response.credential_links || []).map((link: any) => [link.id, true]))
    connect.value = true; portNotes.value = []
  } catch (e: any) { if (request === version) { error.value = e.message; if (!schema.value) emit('dirty', false) } } finally { if (request === version) busy.value = false }
}
const formSnapshot = computed(() => JSON.stringify({ values: values.value, storage: storage.value, linked: linked.value, connect: connect.value }))
watch(() => formSnapshot.value, current => { emit('dirty', current !== appliedForm.value) }, { flush: 'sync' })
const unapplied = computed(() => formSnapshot.value !== appliedForm.value)
const variables = computed(() => (schema.value?.fields || []).filter((f: any) => f.id.startsWith('var:')))
const credentials = computed(() => (schema.value?.fields || []).filter((f: any) => !f.id.startsWith('var:') && (f.secret || /user|email|login/i.test(f.name))))
const settings = computed(() => (schema.value?.fields || []).filter((f: any) => !f.id.startsWith('var:') && !credentials.value.includes(f)))
const storageGroups = computed(() => groupStorage(schema.value?.storage || []))
const missingHostSources = computed(() => storageGroups.value.some((group: any) => storage.value[group.id]?.mode === 'host' && !storage.value[group.id]?.source?.trim()))
const passwords = computed(() => (schema.value?.fields || []).filter((f: any) => f.password))
const samplePasswords = computed(() => passwords.value.filter((f: any) => /^(change[_-]?me|adminpassword|replace[_-]?me|your[_-])|^(password[0-9!]*|secret|admin)$/i.test(values.value[f.id] || '')))
const compatibility = computed(() => {
  const compose = sourceDraft.compose_content || ''
  const required = (schema.value?.fields || []).filter((field: any) => field.required && !String(values.value[field.id] || '').trim())
  const warnings: string[] = []
  if (/\bprivileged\s*:\s*true\b/i.test(compose)) warnings.push('Requests privileged mode')
  if (/\bnetwork_mode\s*:\s*host\b/i.test(compose)) warnings.push('Uses host networking')
  if (/\bdevices\s*:/i.test(compose)) warnings.push('Requires a host device')
  if (/\b(cap_add|cap_drop)\s*:/i.test(compose)) warnings.push('Changes container capabilities')
  if (schema.value?.storage?.some((mount: any) => /^\/|^[A-Za-z]:[\\/]/.test(String(mount.source || '')))) warnings.push('Contains host-path storage')
  return { required, warnings, score: required.length ? 'review' : warnings.length ? 'caution' : 'ready' }
})
function generate(id: string) {
  const bytes = crypto.getRandomValues(new Uint8Array(24))
  values.value[id] = Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join(''); syncLinked(id)
}
function syncLinked(id: string) {
  for (const link of schema.value?.credential_links || []) if (linked.value[link.id] && link.fields.includes(id)) for (const member of link.fields) values.value[member] = values.value[id]
}
function generatePasswords() {
  const generated = new Set<string>()
  for (const field of passwords.value) {
    const link = schema.value.credential_links?.find((link: any) => linked.value[link.id] && link.fields.includes(field.id))
    if (link && generated.has(link.id)) continue
    generate(field.id); if (link) generated.add(link.id)
  }
}
async function freePorts() {
  const request = ++version; busy.value = true; error.value = ''
  try {
    const response = await apiFetch('/api/templates/ports', { method: 'POST', body: JSON.stringify({ ...sourceDraft, values: values.value }) })
    if (request !== version) return
    Object.assign(values.value, response.values); portNotes.value = response.notes
  } catch (e: any) { if (request === version) error.value = e.message } finally { if (request === version) busy.value = false }
}
function sharedStorage(mount: any) {
  for (const other of schema.value.storage) if (other.source === mount.source && (storage.value[mount.id].mode !== 'managed' || other.can_manage)) storage.value[other.id] = { ...storage.value[mount.id] }
}
async function apply() {
  const request = ++version; busy.value = true; error.value = ''
  const edits = Object.fromEntries(schema.value.fields.filter((f: any) => !f.id.startsWith('var:') || values.value[f.id] !== '' || f.value !== '').map((f: any) => [f.id, values.value[f.id]]))
  try {
    const response = await apiFetch('/api/templates/setup', { method: 'POST', body: JSON.stringify({ ...sourceDraft, apply: true, values: edits, storage: storage.value, connect_dependencies: connect.value, credential_links: Object.fromEntries((schema.value.credential_links || []).filter((link: any) => linked.value[link.id]).map((link: any) => [link.id, values.value[link.fields[0]]])) }) })
    if (request === version) {
      appliedKey = JSON.stringify({ compose_content: response.compose_content, env_content: response.env_content })
      Object.assign(values.value, response.applied_values || {})
      appliedForm.value = formSnapshot.value
      emit('dirty', false); emit('apply', response)
    }
  } catch (e: any) { if (request === version) { error.value = e.message; if (!schema.value) emit('dirty', false) } } finally { if (request === version) busy.value = false }
}
</script>
<template>
  <section class="rounded-2xl border border-slate-200 bg-white p-4 sm:p-5 space-y-5">
    <div><p class="text-[10px] font-semibold uppercase tracking-wider text-indigo-600">Make it yours</p><h3 class="mt-1 font-semibold text-base text-slate-900">App setup</h3><p class="text-xs text-slate-500 mt-1">App data storage is created on deployment. Review the login and connection settings, then check your app.</p></div>
    <p v-if="error" role="alert" class="text-xs text-red-700">{{ error }}. The advanced editor is still available below.</p>
    <p v-if="busy && !schema" class="text-xs text-slate-500">Reading template settings…</p>
    <template v-if="schema">
      <div class="rounded-xl border p-3" :class="compatibility.score === 'ready' ? 'border-emerald-200 bg-emerald-50' : compatibility.score === 'caution' ? 'border-amber-200 bg-amber-50' : 'border-red-200 bg-red-50'">
        <div class="flex items-center justify-between gap-3">
          <h4 class="text-xs font-semibold text-slate-800">Compatibility preflight</h4>
          <span class="rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase" :class="compatibility.score === 'ready' ? 'bg-emerald-100 text-emerald-700' : compatibility.score === 'caution' ? 'bg-amber-100 text-amber-800' : 'bg-red-100 text-red-700'">{{ compatibility.score === 'ready' ? 'Ready' : compatibility.score === 'caution' ? 'Review' : 'Action needed' }}</span>
        </div>
        <p v-if="compatibility.score === 'ready'" class="mt-1 text-xs text-slate-600">No required values or elevated host features were detected in this template.</p>
        <p v-else class="mt-1 text-xs text-slate-600">Review these items before deploying. DockTun can prepare ordinary files and directories, but host devices, privileged access, and host networking need a compatible Docker host.</p>
        <ul v-if="compatibility.required.length || compatibility.warnings.length" class="mt-2 space-y-1 text-xs text-slate-700">
          <li v-for="field in compatibility.required" :key="`required-${field.id}`">Required value missing: <strong>{{ field.name }}</strong> ({{ field.service }})</li>
          <li v-for="warning in compatibility.warnings" :key="warning">{{ warning }}</li>
        </ul>
        <p class="mt-2 text-[10px] text-slate-500">The final deployment review still checks image architecture, disk space, ports, storage paths, and Docker permissions.</p>
      </div>
      <details v-if="schema.dependencies?.length || schema.credential_links?.length" class="rounded-xl border border-slate-200 bg-slate-50/60 p-3 space-y-2">
        <summary class="cursor-pointer text-xs font-semibold text-slate-800">Connected services &amp; linked credentials</summary>
        <p v-for="dependency in schema.dependencies" :key="`${dependency.service}:${dependency.target}`" class="text-xs text-slate-600">{{ dependency.service }} → {{ dependency.target }} · {{ dependency.role }} · {{ dependency.healthy ? 'waits for health check' : 'starts before the app' }}</p>
        <label v-if="schema.dependencies?.some(d => d.suggested)" class="flex gap-2 text-xs text-slate-700"><input v-model="connect" type="checkbox" :disabled="busy">Connect detected database/cache startup dependencies</label>
        <label v-for="link in schema.credential_links" :key="link.id" class="flex items-start gap-2 text-xs text-slate-700"><input v-model="linked[link.id]" type="checkbox" :disabled="busy" @change="syncLinked(link.fields[0])"><span>Keep matching {{ link.kind }} linked: {{ link.uses.join(' ↔ ') }}</span></label>
      </details>
      <div v-if="schema.storage.length" class="space-y-3">
        <h4 class="text-sm font-semibold text-slate-800">Storage</h4>
        <p class="text-xs text-slate-500">New data uses persistent Docker storage. Choose an existing folder only when you want to bring your own files.</p>
        <div v-for="group in storageGroups" :key="group.id" class="rounded-xl border p-3 space-y-2" :class="storage[group.id].mode === 'managed' ? 'border-emerald-200 bg-emerald-50/40' : 'border-slate-200 bg-slate-50/50'">
          <div class="flex flex-wrap items-center justify-between gap-2"><p class="text-xs font-semibold text-slate-800">{{ storage[group.id].mode === 'managed' ? 'App data · created automatically' : group.automatic ? 'Your existing files' : 'Required file or host connection' }}</p><span v-if="group.members.length > 1" class="text-[10px] text-slate-500">Shared by {{ group.members.length }} mounts</span></div>
          <div class="flex flex-wrap gap-1.5"><span v-for="mount in group.members" :key="mount.id" class="rounded-md border border-slate-200/70 bg-white px-2 py-1 text-[10px] text-slate-500">{{ mount.service }} <span class="font-mono text-slate-700">{{ mount.target }}</span> · {{ mount.read_only ? 'read only' : 'read/write' }}</span></div>
          <select v-if="group.automatic" v-model="storage[group.id].mode" :aria-label="`Storage for ${group.members[0].service} ${group.members[0].target}`" class="cyber-input text-xs" :disabled="busy" @change="sharedStorage(group.members[0])"><option value="managed">Create automatically (recommended)</option><option value="host">Use my existing folder</option></select>
          <input v-if="storage[group.id].mode === 'host'" v-model="storage[group.id].source" :aria-label="`Host source for ${group.members[0].service} ${group.members[0].target}`" class="cyber-input text-xs font-mono" placeholder="Choose an existing folder or file on the Docker host" :disabled="busy" @input="sharedStorage(group.members[0])">
          <p class="text-[11px] leading-relaxed text-slate-500">{{ storage[group.id].mode === 'managed' ? 'Docker creates this persistent data volume when you deploy. You do not need to make a folder first. Existing host files are not copied.' : group.related ? 'These mounts share a directory layout. Choose the real host paths to preserve that layout.' : group.automatic ? 'Choose a real folder on your Docker host. App checks confirm that it exists before deployment.' : 'This mount needs a real file, socket, device or advanced configuration. Supporting files below can help with project configuration files.' }}</p>
        </div>
      </div>
      <p v-else class="text-xs text-slate-600">Named storage volumes in this template are created automatically by Docker. Any extra file requirements are checked before deployment.</p>
      <div v-if="variables.length" class="space-y-2 rounded-xl border border-indigo-200 bg-indigo-50/40 p-3">
        <div>
          <h4 class="text-sm font-semibold text-slate-800">Template variables</h4>
          <p class="text-xs text-slate-600">These values come from the imported template and remain editable before deployment.</p>
        </div>
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <div v-for="field in variables" :key="field.id" class="space-y-1">
            <label :for="`setup-${field.id}`" class="block text-xs font-medium text-slate-700">{{ field.name }} <span class="text-[10px] text-slate-500">{{ field.service }}{{ field.required ? ' · required' : ' · optional' }}</span></label>
            <div class="flex gap-1">
              <input :id="`setup-${field.id}`" v-model="values[field.id]" :type="field.secret && !showSecrets ? 'password' : 'text'" class="cyber-input text-xs min-w-0" :disabled="busy" @input="syncLinked(field.id)" autocomplete="off">
              <button v-if="field.password" type="button" class="cyber-btn-secondary text-[10px]" :disabled="busy" @click="generate(field.id)">Generate</button>
            </div>
            <p class="text-[10px] text-slate-500">{{ field.hint }}</p>
          </div>
        </div>
      </div>
      <div v-for="group in [{name:'Login & credentials', fields:credentials}, {name:'Ports & other settings', fields:settings}]" :key="group.name" class="space-y-2">
        <template v-if="group.fields.length">
          <div class="flex flex-wrap items-center justify-between gap-2"><h4 class="text-sm font-semibold text-slate-800">{{ group.name }}</h4><button v-if="group.name === 'Login & credentials' && passwords.length" type="button" class="cyber-btn-secondary text-xs" :disabled="busy" @click="generatePasswords">Generate passwords</button><button v-if="group.name === 'Ports & other settings' && schema.fields.some(f => f.port_uses?.length)" type="button" class="cyber-btn-secondary text-xs" :disabled="busy" @click="freePorts">Find free host ports</button></div>
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-3"><div v-for="field in group.fields" :key="field.id" class="space-y-1">
            <label :for="`setup-${field.id}`" class="block text-xs font-medium text-slate-700">{{ field.name }} <span class="text-[10px] text-slate-500">{{ field.service }}{{ field.required ? ' · needs a value' : '' }}</span></label>
            <div class="flex gap-1"><input :id="`setup-${field.id}`" v-model="values[field.id]" :type="field.secret && !showSecrets ? 'password' : 'text'" class="cyber-input text-xs min-w-0" :inputmode="field.kind === 'port' ? 'numeric' : undefined" :disabled="busy" @input="syncLinked(field.id)" autocomplete="off" :min="field.kind === 'port' ? 1 : undefined" :max="field.kind === 'port' ? 65535 : undefined"><button v-if="field.password" type="button" class="cyber-btn-secondary text-[10px]" :disabled="busy" @click="generate(field.id)">Generate</button><button v-if="field.name === 'TZ'" type="button" class="cyber-btn-secondary text-[10px]" :disabled="busy" @click="values[field.id] = Intl.DateTimeFormat().resolvedOptions().timeZone">Use mine</button></div>
            <p v-if="!field.hint.startsWith('Container setting')" class="text-[10px] text-slate-500">{{ field.hint }}</p>
          </div></div>
          <p v-if="group.name === 'Ports & other settings'" v-for="note in portNotes" :key="note" class="text-xs text-slate-500">{{ note }}</p>
        </template>
      </div>
      <label v-if="credentials.some(f => f.secret) || variables.some(f => f.secret)" class="flex items-center gap-2 text-xs text-slate-600"><input v-model="showSecrets" type="checkbox">Show credentials and template secrets</label>
      <p v-if="samplePasswords.length" role="alert" class="text-xs text-amber-800">Replace sample passwords or use Generate passwords before applying.</p>
      <details v-if="schema.notes.length" class="text-xs text-slate-600"><summary class="cursor-pointer font-medium">App setup instructions</summary><p v-for="(note,index) in schema.notes" :key="index" class="mt-2 whitespace-pre-wrap break-words">{{ note }}</p><p class="mt-2">Generated passwords are for new accounts. API tokens, encryption keys and password hashes must follow the app's instructions.</p></details>
      <p v-if="unapplied && !busy" class="text-xs text-indigo-700">Setup changes need to be applied and checked before deployment.</p>
      <p v-if="missingHostSources" role="alert" class="text-xs text-amber-800">Choose a host folder or switch back to automatic storage before applying.</p>
      <button type="button" class="cyber-btn text-xs w-full" :disabled="busy || !!samplePasswords.length || missingHostSources" @click="apply">{{ busy ? 'Applying settings…' : 'Apply settings & check app' }}</button>
    </template>
  </section>
</template>
