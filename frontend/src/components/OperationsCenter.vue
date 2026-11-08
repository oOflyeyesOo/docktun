<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useApi } from '../composables/useApi'
const { apiFetch, apiResponse } = useApi()
const operations = ref<any[]>([]), deliveries = ref<any[]>([]), preview = ref<any>(null)
const loading = ref(false), error = ref('')
async function load() {
 loading.value = true; error.value = ''
 try { const [jobs, alerts] = await Promise.all([apiFetch('/api/operations'), apiFetch('/api/notifications/deliveries')]); operations.value = jobs; deliveries.value = alerts }
 catch (e: any) { error.value = e.message || 'Unable to read operational state' }
 finally { loading.value = false }
}
async function support() { error.value = ''; try { preview.value = await apiFetch('/api/support-bundles', {method:'POST', body:JSON.stringify({include_operations:true})}) } catch(e: any) {error.value=e.message} }
async function download() { if (!preview.value) return; try {const response=await apiResponse('/api/support-bundles/'+preview.value.id);const url=URL.createObjectURL(await response.blob());const a=document.createElement('a');a.href=url;a.download='docktun-support.json';a.click();URL.revokeObjectURL(url)}catch(e:any){error.value=e.message} }
async function retry(id:string) {try {await apiFetch('/api/notifications/deliveries/'+id+'/retry',{method:'POST'});await load()}catch(e:any){error.value=e.message}}
async function acknowledge(id:string) {if(!confirm('Confirm that you inspected the current container and recovered any interrupted replacement. This releases the operation lock; it does not rerun the operation.'))return;try{await apiFetch('/api/operations/'+id+'/acknowledge',{method:'POST'});await load()}catch(e:any){error.value=e.message}}
onMounted(load)
</script>
<template>
 <section class="cyber-card space-y-4" aria-label="Operations and support">
  <div class="flex flex-wrap justify-between gap-3"><div><h2 class="font-semibold text-slate-900">Operations &amp; recovery history</h2><p class="text-xs text-muted mt-1">Durable maintenance state and notification delivery outcomes.</p></div><button class="cyber-btn-secondary" :disabled="loading" @click="load">Refresh</button></div>
  <p v-if="loading" role="status">Reading current state…</p><p v-if="error" role="alert" class="text-red-600 text-sm">{{ error }}</p>
  <div class="space-y-2 max-h-80 overflow-auto"><article v-for="job in operations" :key="job.id" class="border border-slate-200 rounded-lg p-3 text-xs"><div class="flex justify-between gap-2"><strong>{{ job.kind }} · {{ job.target }}</strong><span>{{ job.phase }}</span></div><p class="mt-1">{{ job.message }}</p><p v-for="resource in job.resources || []" :key="resource" class="mt-1 break-all">Recovery resource: {{ resource }}</p><p class="text-muted mt-1">{{ new Date(job.updated_at * 1000).toLocaleString() }} · {{ job.id }}</p><button v-if="job.phase==='interrupted'" class="cyber-btn-secondary mt-2" @click="acknowledge(job.id)">Acknowledge inspected recovery</button></article><p v-if="!operations.length && !loading" class="text-xs text-muted">No recorded operations yet.</p></div>
  <details><summary class="cursor-pointer text-sm font-medium">Alert delivery history ({{ deliveries.length }})</summary><div v-for="delivery in deliveries" :key="delivery.id" class="border-t border-slate-200 py-2 text-xs mt-2"><div class="flex justify-between gap-2"><span>{{ delivery.kind }}</span><strong>{{ delivery.phase }} · {{ delivery.attempts }} failed attempts</strong></div><p class="mt-1">{{ delivery.message }}</p><p v-if="delivery.last_error" class="text-amber-700">{{ delivery.last_error }}</p><button v-if="delivery.phase==='failed'" class="cyber-btn-secondary mt-1" @click="retry(delivery.id)">Retry exhausted delivery</button></div></details>
  <div class="border-t border-slate-200 pt-3"><h3 class="font-medium text-sm">Support bundle</h3><p class="text-xs text-muted mt-1">Preview a metadata-only export. Environment values, credentials, app data, raw logs and host mount source paths are excluded.</p><button class="cyber-btn-secondary mt-2" @click="support">Generate preview</button><div v-if="preview" class="mt-3 space-y-2"><pre class="max-h-72 overflow-auto text-[11px] whitespace-pre-wrap bg-slate-50 p-3 rounded">{{ JSON.stringify(preview.report,null,2) }}</pre><p class="text-xs break-all">SHA-256: {{ preview.sha256 }}</p><button class="cyber-btn" @click="download">Download this preview</button></div></div>
 </section>
</template>
