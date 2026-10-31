<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { Plus, RefreshCw, Trash2, ArrowUp, ArrowDown, Code2, Eye, Activity, Globe, Copy, Pause, Play, ExternalLink } from 'lucide-vue-next'
import { useApi } from '../composables/useApi'
import { cloneConfiguration, effectiveOriginOption, originHost, originLocation, readIngressYaml, setOriginOption, validateIngressConfiguration, writeIngressYaml } from '../lib/cloudflare-ingress.mjs'
const props = defineProps<{ remote: boolean; tunnelId?: string; containers?: any[] }>()
const emit = defineEmits<{ saved: [any]; credentials: [] }>()
const { apiFetch } = useApi()
type Row = { key: number; sourceIndex: number | null; value: any; pausedOrigin?: string; probe?: any }
const config = ref<any>(null), rows = ref<Row[]>([])
const loading = ref(false), saving = ref(false), rawMode = ref(false), rawDraft = ref('')
const syncDNS = ref(true), revision = ref(''), sourceContent = ref(''), editingContent = ref('')
const baseline = ref(''), error = ref(''), notice = ref<any>(null), probing = ref<number | null>(null)
let nextKey = 0, requestVersion = 0
const currentConfig = () => ({ ...config.value, ingress: rows.value.map(row => row.value) })
const fingerprint = () => rawMode.value ? rawDraft.value : JSON.stringify(currentConfig())
const dirty = computed(() => !!baseline.value && fingerprint() !== baseline.value)
const validation = computed(() => {
  try {
    const draft = rawMode.value ? props.remote ? JSON.parse(rawDraft.value) : readIngressYaml(rawDraft.value).config : currentConfig()
    const errors = validateIngressConfiguration(draft)
    if (!props.remote && !String(draft?.tunnel || '').trim()) errors.unshift('Enter the local tunnel ID')
    return errors
  } catch (e: any) { return [e.message] }
})
const ready = computed(() => !loading.value && (props.remote ? !!revision.value : config.value !== null || rawMode.value))
function adopt(configuration: any, content = '') {
  config.value = cloneConfiguration(configuration)
  rows.value = (config.value.ingress || []).map((value: any, index: number) => ({ key: nextKey++, sourceIndex: index, value }))
  editingContent.value = content
  rawMode.value = false
  baseline.value = fingerprint()
}
async function load() {
  const version = ++requestVersion
  loading.value = true; error.value = ''
  try {
    if (props.remote) {
      const result = await apiFetch<any>('/api/cloudflare/configuration')
      if (version !== requestVersion) return
      revision.value = result.revision
      adopt(result.config)
    } else {
      const result = await apiFetch<any>('/api/config')
      if (version !== requestVersion) return
      sourceContent.value = result.content || ''
      try {
        const parsed = readIngressYaml(sourceContent.value).config
        if (!parsed.ingress) parsed.ingress = [{ service: 'http_status:404' }]
        if (!parsed.tunnel) parsed.tunnel = props.tunnelId || ''
        if (!parsed['credentials-file']) parsed['credentials-file'] = '/etc/cloudflared/credentials.json'
        adopt(parsed, sourceContent.value)
      } catch (e: any) {
        config.value = null; rawMode.value = true; rawDraft.value = sourceContent.value; baseline.value = fingerprint(); error.value = e.message
      }
    }
  } catch (e: any) { if (version === requestVersion) error.value = e.message }
  finally { if (version === requestVersion) loading.value = false }
}
onMounted(load)
onUnmounted(() => { requestVersion++ })
function reload() {
  if (dirty.value && !confirm('Reload the latest routes and discard this unsaved draft?')) return
  void load()
}
function toggleRaw() {
  error.value = ''
  try {
    const wasDirty = dirty.value
    if (!rawMode.value) {
      rawDraft.value = props.remote ? JSON.stringify(currentConfig(), null, 2) : writeIngressYaml(editingContent.value, currentConfig(), rows.value.map(row => row.sourceIndex))
      rawMode.value = true
    } else {
      const parsed = props.remote ? JSON.parse(rawDraft.value) : readIngressYaml(rawDraft.value).config
      const problems = validateIngressConfiguration(parsed)
      if (problems.length) throw new Error(problems.join('; '))
      adopt(parsed, props.remote ? '' : rawDraft.value)
    }
    if (!wasDirty) baseline.value = fingerprint()
    else baseline.value = '__unapplied-draft__'
  } catch (e: any) { error.value = e.message }
}
function addRoute() {
  const row = { key: nextKey++, sourceIndex: null, value: { hostname: '', service: '' } }
  const last = rows.value.at(-1)?.value
  rows.value.splice(last && !last.hostname && !last.path ? rows.value.length - 1 : rows.value.length, 0, row)
}
const fallback = (index: number) => index === rows.value.length - 1 && !rows.value[index].value.hostname && !rows.value[index].value.path
function move(index: number, delta: number) {
  const destination = index + delta
  if (destination < 0 || destination >= rows.value.length || fallback(index) || fallback(destination)) return
  const [row] = rows.value.splice(index, 1); rows.value.splice(destination, 0, row)
}
function pause(row: Row) {
  if (row.pausedOrigin) { row.value.service = row.pausedOrigin; row.pausedOrigin = undefined }
  else { row.pausedOrigin = row.value.service; row.value.service = 'http_status:503' }
}
async function probe(row: Row) {
  probing.value = row.key
  try { row.probe = await apiFetch('/api/docker/probe', { method: 'POST', body: JSON.stringify({ service: row.value.service, origin_request: { ...config.value?.originRequest, ...row.value.originRequest } }) }) }
  catch (e: any) { row.probe = { reachable: false, error: e.message } }
  finally { probing.value = null }
}
async function copy(value: string) { await navigator.clipboard.writeText(value) }
async function save(restart = false) {
  saving.value = true; error.value = ''; notice.value = null
  try {
    let next = rawMode.value ? props.remote ? JSON.parse(rawDraft.value) : readIngressYaml(rawDraft.value).config : currentConfig()
    const problems = validateIngressConfiguration(next)
    if (problems.length) throw new Error(problems.join('; '))
    let content = ''
    if (props.remote) {
      const response = await apiFetch<any>('/api/cloudflare/configuration', { method: 'PUT', body: JSON.stringify({ config: next, expected_revision: revision.value }) })
      revision.value = response.revision; next = response.config
    } else {
      content = rawMode.value ? rawDraft.value : writeIngressYaml(editingContent.value, next, rows.value.map(row => row.sourceIndex))
      content = content.replace(/\r\n?/g, '\n').replace(/\n?$/, '\n')
      await apiFetch('/api/config', { method: 'PUT', body: JSON.stringify({ content, expected_content: sourceContent.value, sync_dns: false }) })
      sourceContent.value = content; editingContent.value = content
    }
    if (rawMode.value || props.remote) {
      const previousRows = rows.value
      adopt(next, content)
      rows.value.forEach((row, index) => {
        const previous = previousRows[index]
        if (previous?.pausedOrigin && row.value.service === 'http_status:503' && row.value.hostname === previous.value.hostname && row.value.path === previous.value.path) row.pausedOrigin = previous.pausedOrigin
      })
    }
    else {
      config.value = cloneConfiguration(next)
      rows.value.forEach((row, index) => { row.sourceIndex = index })
      baseline.value = fingerprint()
    }
    const warnings: string[] = []
    if (syncDNS.value) {
      try {
        const dns = await apiFetch<any>('/api/cloudflare/sync-all', { method: 'POST' })
        const failed = (dns.synced || []).filter((entry: any) => !entry.success)
        if (failed.length) warnings.push('DNS: ' + failed.map((entry: any) => entry.hostname + ' — ' + entry.error).join('; '))
      } catch (e: any) { warnings.push('DNS sync failed: ' + e.message) }
    }
    if (restart) {
      try { await apiFetch('/api/control/restart', { method: 'POST' }) }
      catch (e: any) { warnings.push('Connector restart failed: ' + e.message) }
    }
    notice.value = { warning: !!warnings.length, text: (props.remote ? 'Cloudflare routes saved.' : 'Local configuration saved.' + (restart ? ' Connector restart requested.' : ' Restart the connector when you want to apply the changes.')) + (warnings.length ? ' ' + warnings.join(' ') : '') }
    emit('saved', { config: next, content })
  } catch (e: any) { error.value = e.message }
  finally { saving.value = false }
}
const advancedFields = (rule: any) => Object.keys(rule).filter(key => !['hostname', 'service', 'path', 'originRequest'].includes(key))
const containerMatch = (target: string) => (props.containers || []).find(item => item.name?.replace(/^\/+/, '') === originHost(target))
</script>

<template>
  <section class="space-y-4" aria-label="Cloudflare ingress editor">
    <header class="rounded-2xl border border-slate-200 bg-white p-4 sm:p-5">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div><p class="text-[10px] uppercase tracking-wider font-semibold text-indigo-600">{{ remote ? 'Cloudflare-managed configuration' : 'Local YAML configuration' }}</p><h2 class="mt-1 text-lg font-semibold text-slate-900">Hostname routes &amp; origin services</h2><p class="mt-1 text-xs leading-relaxed text-slate-500">Route to a Docker container, another VM, a LAN address, or a reachable hostname. Targets are used exactly as entered.</p></div>
        <button type="button" class="cyber-btn-secondary text-xs" :disabled="loading || saving" @click="reload"><RefreshCw class="inline h-3.5 w-3.5 mr-1" :class="{ 'animate-spin': loading }" />Reload routes</button>
      </div>
      <div class="mt-3 flex flex-wrap gap-2 text-[11px] text-slate-500"><span class="rounded-md bg-slate-50 px-2 py-1">Container: <code>http://app:8080</code></span><span class="rounded-md bg-slate-50 px-2 py-1">VM / LAN: <code>http://192.168.1.50:8080</code></span><span class="rounded-md bg-slate-50 px-2 py-1">HTTPS: <code>https://server.lan:8443</code></span></div>
      <p class="mt-3 text-[11px] leading-relaxed text-slate-500">The cloudflared connector must be able to reach the target. <code>localhost</code> means the connector itself. Rule order matters; the first matching hostname and path wins.</p>
    </header>
    <p v-if="loading" role="status" class="text-xs text-slate-500">Loading the current route configuration…</p>
    <div v-if="error" role="alert" class="rounded-xl border border-red-200 bg-red-50 p-3 text-xs text-red-800 break-words">{{ error }}<button v-if="remote && !revision" type="button" class="ml-2 underline font-semibold" @click="emit('credentials')">API credentials</button></div>
    <p v-if="notice && (!dirty || notice.warning)" role="status" class="rounded-xl border p-3 text-xs break-words" :class="notice.warning ? 'border-amber-200 bg-amber-50 text-amber-900' : 'border-emerald-200 bg-emerald-50 text-emerald-800'">{{ notice.text }}</p>
    <template v-if="ready">
      <div class="flex flex-wrap items-center justify-between gap-3"><span class="text-xs text-slate-500">{{ rows.length }} rules including fallback <span v-if="dirty" class="font-semibold text-indigo-600">· Unsaved changes</span></span><div class="flex gap-2"><button type="button" class="cyber-btn-secondary text-xs" :disabled="saving" @click="toggleRaw"><Eye v-if="rawMode" class="inline h-3.5 w-3.5 mr-1" /><Code2 v-else class="inline h-3.5 w-3.5 mr-1" />{{ rawMode ? 'Visual editor' : remote ? 'Full configuration JSON' : 'Full configuration YAML' }}</button><button v-if="!rawMode" type="button" class="cyber-btn text-xs" :disabled="saving" @click="addRoute"><Plus class="inline h-3.5 w-3.5 mr-1" />Add route</button></div></div>
      <textarea v-if="rawMode" v-model="rawDraft" :aria-label="remote ? 'Full tunnel configuration JSON' : 'Full tunnel configuration YAML'" spellcheck="false" rows="22" class="cyber-input w-full font-mono text-xs leading-relaxed" :disabled="saving"></textarea>
      <template v-else-if="config">
        <div v-if="!remote" class="rounded-xl border border-slate-200 bg-slate-50/50 p-3 grid gap-3 sm:grid-cols-2"><label class="text-xs text-slate-600">Local tunnel ID<input v-model="config.tunnel" aria-label="Local tunnel ID" class="cyber-input mt-1 text-xs font-mono" :disabled="saving"></label><label class="text-xs text-slate-600">Credentials file<input v-model="config['credentials-file']" aria-label="Credentials file" class="cyber-input mt-1 text-xs font-mono"  :disabled="saving"></label><label class="text-xs font-medium text-slate-600">Connector transport<select :value="config.protocol || 'auto'" class="cyber-input mt-1 text-xs" :disabled="saving" @change="config.protocol = ($event.target as HTMLSelectElement).value"><option value="auto">Automatic</option><option value="quic">QUIC</option><option value="http2">HTTP/2</option></select></label></div>
        <article v-for="(row, index) in rows" :key="row.key" class="rounded-2xl border border-slate-200 bg-white p-4 space-y-3" :class="{ 'bg-slate-50/60': fallback(index) }">
          <div class="flex flex-wrap items-center justify-between gap-2"><div class="flex items-center gap-2"><span class="rounded-md bg-slate-100 px-2 py-1 text-[10px] font-semibold text-slate-600">{{ fallback(index) ? 'Fallback' : 'Rule ' + (index + 1) }}</span><span class="text-[11px] text-slate-500">{{ fallback(index) ? 'Handles requests that do not match a route above' : originLocation(row.value.service, containers) }}</span></div><div v-if="!fallback(index)" class="flex gap-1"><button type="button" class="rounded p-1.5 text-slate-500 hover:bg-slate-100 disabled:opacity-30" :aria-label="'Move rule ' + (index+1) + ' up'" :disabled="saving || index === 0" @click="move(index,-1)"><ArrowUp class="h-3.5 w-3.5" /></button><button type="button" class="rounded p-1.5 text-slate-500 hover:bg-slate-100 disabled:opacity-30" :aria-label="'Move rule ' + (index+1) + ' down'" :disabled="saving || index >= rows.length-2" @click="move(index,1)"><ArrowDown class="h-3.5 w-3.5" /></button><button type="button" class="rounded p-1.5 text-red-500 hover:bg-red-50" :aria-label="'Delete rule ' + (index+1)" :disabled="saving" @click="rows.splice(index,1)"><Trash2 class="h-3.5 w-3.5" /></button></div></div>
          <div v-if="!fallback(index)" class="grid gap-3 md:grid-cols-2"><label class="text-xs font-medium text-slate-600">Public hostname<input v-model="row.value.hostname" :aria-label="'Public hostname for rule ' + (index+1)" placeholder="app.example.com" class="cyber-input mt-1 font-mono text-xs" :disabled="saving"></label><label class="text-xs font-medium text-slate-600">Path match <span class="font-normal text-slate-400">(optional regex)</span><input v-model="row.value.path" :aria-label="'Path match for rule ' + (index+1)" placeholder="/api/.*" class="cyber-input mt-1 font-mono text-xs" :disabled="saving"></label></div>
          <div><div class="flex items-center justify-between gap-2"><label class="text-xs font-medium text-slate-600" :for="'origin-' + row.key">{{ fallback(index) ? 'Fallback service / response' : 'Origin service URL' }}</label><div class="flex gap-2"><router-link v-if="containerMatch(row.value.service)" :to="'/containers?name=' + encodeURIComponent(originHost(row.value.service))" class="text-[10px] text-indigo-600">Inspect container</router-link><button type="button" class="text-slate-400 hover:text-indigo-600" :aria-label="'Copy origin for rule ' + (index+1)" @click="copy(row.value.service)"><Copy class="h-3 w-3" /></button></div></div><textarea :id="'origin-' + row.key" v-model="row.value.service" :aria-label="'Origin service for rule ' + (index+1)" rows="2" spellcheck="false" placeholder="http://192.168.1.50:8080" class="cyber-input mt-1 w-full resize-y font-mono text-xs leading-relaxed break-all" :disabled="saving"></textarea></div>
          <template v-if="!fallback(index)">
            <div class="grid gap-3 sm:grid-cols-2"><label class="text-xs text-slate-600">HTTP Host header <span class="text-slate-400">(optional)</span><input :value="row.value.originRequest?.httpHostHeader || ''" :aria-label="'HTTP Host header for rule ' + (index+1)" :placeholder="config.originRequest?.httpHostHeader ? 'Inherited: ' + config.originRequest.httpHostHeader : 'app.internal.example'" class="cyber-input mt-1 font-mono text-xs" :disabled="saving" @input="setOriginOption(row.value,'httpHostHeader',($event.target as HTMLInputElement).value)"></label><label class="text-xs text-slate-600">TLS origin server name <span class="text-slate-400">(optional)</span><input :value="row.value.originRequest?.originServerName || ''" :aria-label="'TLS server name for rule ' + (index+1)" :placeholder="config.originRequest?.originServerName ? 'Inherited: ' + config.originRequest.originServerName : 'Name on the origin certificate'" class="cyber-input mt-1 font-mono text-xs" :disabled="saving" @input="setOriginOption(row.value,'originServerName',($event.target as HTMLInputElement).value)"></label></div>
            <div class="flex flex-wrap items-center justify-between gap-3"><label class="flex items-center gap-2 text-[11px] text-slate-600"><input type="checkbox" :checked="effectiveOriginOption(config,row.value,'noTLSVerify') === true" :aria-label="'Disable TLS verification for rule ' + (index+1)" :disabled="saving" @change="setOriginOption(row.value,'noTLSVerify',($event.target as HTMLInputElement).checked)">Disable TLS certificate verification <span class="text-slate-400">(explicit override)</span></label><div class="flex flex-wrap gap-2"><button v-if="/^https?:\/\//.test(row.value.service)" type="button" class="cyber-btn-secondary text-[11px]" :disabled="saving || probing === row.key" @click="probe(row)"><Activity class="inline h-3 w-3 mr-1" />{{ probing === row.key ? 'Testing…' : 'Test from DockTun' }}</button><button v-if="row.pausedOrigin || !row.value.service.startsWith('http_status:')" type="button" class="cyber-btn-secondary text-[11px]" :disabled="saving" @click="pause(row)"><Play v-if="row.pausedOrigin" class="inline h-3 w-3 mr-1" /><Pause v-else class="inline h-3 w-3 mr-1" />{{ row.pausedOrigin ? 'Restore origin' : 'Pause with 503' }}</button></div></div>
            <p v-if="row.probe" role="status" class="rounded-lg p-2 text-[11px] break-words" :class="row.probe.reachable ? 'bg-emerald-50 text-emerald-800' : 'bg-amber-50 text-amber-900'">{{ row.probe.reachable ? 'DockTun reached this origin: HTTP ' + row.probe.status_code + ' · ' + row.probe.latency_ms + ' ms' : row.probe.error }} This checks DockTun’s network path; the connector may have different routing.</p>
          </template>
            <details v-if="row.value.originRequest || advancedFields(row.value).length" class="text-[11px] text-slate-500"><summary class="cursor-pointer font-medium">All rule options · preserved when saving</summary><pre class="mt-2 rounded-lg bg-slate-50 p-3 whitespace-pre-wrap break-all">{{ JSON.stringify(row.value,null,2) }}</pre></details>
        </article>
        <details v-if="Object.keys(config).some(key => !['ingress','tunnel','credentials-file','protocol'].includes(key))" class="rounded-xl border border-slate-200 p-3 text-xs text-slate-600"><summary class="cursor-pointer font-medium">Global and additional tunnel settings · preserved</summary><pre class="mt-2 whitespace-pre-wrap break-all">{{ JSON.stringify(Object.fromEntries(Object.entries(config).filter(([key])=>key!=='ingress')),null,2) }}</pre></details>
      </template>
      <ul v-if="validation.length && !loading" class="rounded-xl border border-amber-200 bg-amber-50 p-3 pl-7 list-disc text-xs text-amber-900"><li v-for="problem in validation" :key="problem">{{ problem }}</li></ul>
      <footer class="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-slate-200 bg-white p-3">
        <label class="flex items-center gap-2 text-xs text-slate-600"><input v-model="syncDNS" type="checkbox" :disabled="saving">Create / verify DNS records <span class="text-slate-400">(no forced overwrite)</span></label>
        <div class="flex flex-wrap gap-2"><button type="button" class="cyber-btn text-xs" :disabled="saving || !ready || !!validation.length" @click="save(false)">{{ saving ? 'Saving…' : syncDNS ? 'Save & sync DNS' : remote ? 'Save Cloudflare routes' : 'Save configuration' }}</button><button v-if="!remote" type="button" class="cyber-btn-secondary text-xs" :disabled="saving || !ready || !!validation.length" @click="save(true)">Save &amp; restart connector</button></div>
      </footer>
      <p class="text-[11px] text-slate-500">{{ remote ? 'Cloudflare-managed changes apply through the API; no Docker container registration is required.' : 'Local saves are backed up. Saving without restart does not start a disabled connector.' }} <a href="https://developers.cloudflare.com/tunnel/get-started/" target="_blank" rel="noopener" class="text-indigo-600 underline">Origin setup guide <ExternalLink class="inline h-2.5 w-2.5" /></a></p>
    </template>
  </section>
</template>

