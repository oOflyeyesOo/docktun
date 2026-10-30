<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useApi } from '../composables/useApi'
const props = defineProps<{ input: { compose_content: string; env_content: string } }>()
const emit = defineEmits<{ apply: [any]; dirty: [boolean] }>()
const { apiFetch } = useApi()
const schema = ref<any>(null)
const values = ref<Record<string, string>>({})
const storage = ref<Record<string, { mode: string; source: string }>>({})
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
    storage.value = Object.fromEntries(response.storage.map((mount: any) => [mount.id, { mode: 'host', source: mount.source }]))
  } catch (e: any) { if (request === version) { error.value = e.message; if (!schema.value) emit('dirty', false) } } finally { if (request === version) busy.value = false }
}
watch(() => JSON.stringify({ values: values.value, storage: storage.value }), current => { emit('dirty', current !== appliedForm.value) }, { flush: 'sync' })
const unapplied = computed(() => JSON.stringify({ values: values.value, storage: storage.value }) !== appliedForm.value)
const credentials = computed(() => (schema.value?.fields || []).filter((f: any) => f.secret || /user|email|login/i.test(f.name)))
const settings = computed(() => (schema.value?.fields || []).filter((f: any) => !credentials.value.includes(f)))
const passwords = computed(() => (schema.value?.fields || []).filter((f: any) => f.password))
const samplePasswords = computed(() => passwords.value.filter((f: any) => /^(change[_-]?me|adminpassword|replace[_-]?me|your[_-])|^(password[0-9!]*|secret|admin)$/i.test(values.value[f.id] || '')))
function generate(id: string) {
  const bytes = crypto.getRandomValues(new Uint8Array(24))
  values.value[id] = Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')
}
function sharedStorage(mount: any) {
  for (const other of schema.value.storage) if (other.source === mount.source && (storage.value[mount.id].mode !== 'managed' || other.can_manage)) storage.value[other.id] = { ...storage.value[mount.id] }
}
function automaticStorage() {
  for (const mount of schema.value.storage) if (mount.can_manage && !mount.read_only && /^\/(config|data|app\/data|var\/lib|opt)(\/|$)/.test(mount.target) && schema.value.storage.filter((other: any) => other.source === mount.source).every((other: any) => other.can_manage)) {
    storage.value[mount.id].mode = 'managed'; sharedStorage(mount)
  }
}
async function apply() {
  const request = ++version; busy.value = true; error.value = ''
  const edits = Object.fromEntries(schema.value.fields.filter((f: any) => !f.id.startsWith('var:') || values.value[f.id] !== '' || f.value !== '').map((f: any) => [f.id, values.value[f.id]]))
  try {
    const response = await apiFetch('/api/templates/setup', { method: 'POST', body: JSON.stringify({ ...sourceDraft, apply: true, values: edits, storage: storage.value }) })
    if (request === version) {
      appliedKey = JSON.stringify({ compose_content: response.compose_content, env_content: response.env_content })
      appliedForm.value = JSON.stringify({ values: values.value, storage: storage.value })
      emit('dirty', false); emit('apply', response)
    }
  } catch (e: any) { if (request === version) { error.value = e.message; if (!schema.value) emit('dirty', false) } } finally { if (request === version) busy.value = false }
}
</script>
<template>
  <section class="rounded-xl border border-indigo-200 bg-indigo-50/30 p-4 space-y-4">
    <div><h3 class="font-semibold text-sm text-slate-900">Set up this app</h3><p class="text-xs text-slate-600 mt-1">Choose storage, login details and ports here. Apply your choices to check the app before deploying.</p></div>
    <p v-if="error" role="alert" class="text-xs text-red-700">{{ error }}. The advanced editor is still available below.</p>
    <p v-if="busy && !schema" class="text-xs text-slate-500">Reading template settings…</p>
    <template v-if="schema">
      <div v-if="schema.storage.length" class="space-y-3">
        <div class="flex flex-wrap justify-between gap-2 items-center"><h4 class="text-xs font-semibold text-slate-800">Storage</h4><button type="button" class="cyber-btn-secondary text-xs" :disabled="busy" @click="automaticStorage">Automatic storage for new app data</button></div>
        <p class="text-xs text-slate-600">Automatic storage uses persistent Docker volumes created on deployment. Existing files are not copied. For your media, downloads or existing configuration, choose its folder on the Docker host.</p>
        <div v-for="mount in schema.storage" :key="mount.id" class="rounded-lg border border-slate-200 bg-white p-3 space-y-2">
          <p class="text-xs font-medium text-slate-800">{{ mount.service }} → {{ mount.target }} <span class="text-slate-500">({{ mount.read_only ? 'read only' : 'read/write' }})</span></p>
          <select v-model="storage[mount.id].mode" :aria-label="`Storage for ${mount.service} ${mount.target}`" class="cyber-input text-xs" :disabled="busy" @change="sharedStorage(mount)"><option value="host">Use a folder or file on my Docker host</option><option v-if="mount.can_manage" value="managed">Create persistent storage automatically</option></select>
          <input v-if="storage[mount.id].mode === 'host'" v-model="storage[mount.id].source" :aria-label="`Host source for ${mount.service} ${mount.target}`" class="cyber-input text-xs font-mono" placeholder="For example /volume1/docker/my-app/config" :disabled="busy" @input="sharedStorage(mount)">
          <p class="text-[11px] text-slate-500">{{ storage[mount.id].mode === 'managed' ? 'Docker will create a new persistent volume for this app. No host folder needs to be created.' : mount.hint }}</p>
        </div>
      </div>
      <p v-else class="text-xs text-slate-600">Named storage volumes in this template are created automatically by Docker. Any extra file requirements are checked before deployment.</p>
      <div v-for="group in [{name:'Login & credentials', fields:credentials}, {name:'Ports & other settings', fields:settings}]" :key="group.name" class="space-y-2">
        <template v-if="group.fields.length">
          <div class="flex flex-wrap items-center justify-between gap-2"><h4 class="text-xs font-semibold text-slate-800">{{ group.name }}</h4><button v-if="group.name === 'Login & credentials' && passwords.length" type="button" class="cyber-btn-secondary text-xs" :disabled="busy" @click="passwords.forEach(f => generate(f.id))">Generate passwords</button></div>
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-3"><div v-for="field in group.fields" :key="field.id" class="space-y-1">
            <label :for="`setup-${field.id}`" class="block text-xs font-medium text-slate-700">{{ field.name }} <span class="text-[10px] text-slate-500">{{ field.service }}{{ field.required ? ' · needs a value' : '' }}</span></label>
            <div class="flex gap-1"><input :id="`setup-${field.id}`" v-model="values[field.id]" :type="field.secret && !showSecrets ? 'password' : 'text'" class="cyber-input text-xs min-w-0" :inputmode="field.kind === 'port' ? 'numeric' : undefined" :disabled="busy" autocomplete="off" :min="field.kind === 'port' ? 1 : undefined" :max="field.kind === 'port' ? 65535 : undefined"><button v-if="field.password" type="button" class="cyber-btn-secondary text-[10px]" :disabled="busy" @click="generate(field.id)">Generate</button><button v-if="field.name === 'TZ'" type="button" class="cyber-btn-secondary text-[10px]" :disabled="busy" @click="values[field.id] = Intl.DateTimeFormat().resolvedOptions().timeZone">Use mine</button></div>
            <p class="text-[10px] text-slate-500">{{ field.hint }}</p>
          </div></div>
        </template>
      </div>
      <label v-if="credentials.some(f => f.secret)" class="flex items-center gap-2 text-xs text-slate-600"><input v-model="showSecrets" type="checkbox">Show credentials</label>
      <p v-if="samplePasswords.length" role="alert" class="text-xs text-amber-800">Replace sample passwords or use Generate passwords before applying.</p>
      <details v-if="schema.notes.length" class="text-xs text-slate-600"><summary class="cursor-pointer font-medium">App setup instructions</summary><p v-for="(note,index) in schema.notes" :key="index" class="mt-2 whitespace-pre-wrap break-words">{{ note }}</p><p class="mt-2">Generated passwords are for new accounts. API tokens, encryption keys and password hashes must follow the app's instructions.</p></details>
      <p v-if="unapplied && !busy" class="text-xs text-indigo-700">Setup changes need to be applied and checked before deployment.</p>
      <button type="button" class="cyber-btn text-xs w-full" :disabled="busy || !!samplePasswords.length" @click="apply">{{ busy ? 'Applying settings…' : 'Apply settings & check app' }}</button>
    </template>
  </section>
</template>
