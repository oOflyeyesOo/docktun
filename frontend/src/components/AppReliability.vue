<script setup lang="ts">
import { ref, watch } from 'vue'
import { useApi } from '../composables/useApi'
const props=defineProps<{containerId:string;containerName:string}>()
const {apiFetch}=useApi()
const plan=ref<any>(null), preview=ref<any>(null), events=ref<any[]>([]), incidents=ref<any[]>([]), readiness=ref(''), busy=ref(false), error=ref(''), message=ref('')
let version=0
async function load(){const v=++version;busy.value=true;error.value='';plan.value=null;preview.value=null;events.value=[];incidents.value=[];try{const [p,h,i]=await Promise.all([apiFetch('/api/docker/containers/'+props.containerId+'/update-plan'),apiFetch('/api/operations?target='+encodeURIComponent('container:'+props.containerName)),apiFetch('/api/notifications/deliveries?target='+encodeURIComponent('container:'+props.containerName))]);if(v!==version)return;plan.value=p;readiness.value=p.readiness_url||'';events.value=h;incidents.value=i}catch(e:any){if(v===version)error.value=e.message}finally{if(v===version)busy.value=false}}
async function save(){const id=props.containerId,v=version;error.value='';try{await apiFetch('/api/docker/containers/'+id+'/readiness',{method:'PUT',body:JSON.stringify({url:readiness.value})});if(v!==version)return;message.value='Readiness settings saved';await load()}catch(e:any){if(v===version)error.value=e.message}}
async function inspectImport(){const id=props.containerId,v=version;error.value='';try{const result=await apiFetch('/api/docker/containers/'+id+'/import-preview');if(v===version)preview.value=result}catch(e:any){if(v===version)error.value=e.message}}
watch(()=>props.containerId,()=>{message.value='';void load()},{immediate:true})
</script>
<template>
 <section class="rounded-xl border border-slate-200 bg-white p-4 space-y-3" aria-label="Application reliability">
 <div class="flex justify-between gap-3"><h3 class="font-semibold text-sm">Readiness, dependencies &amp; maintenance</h3><button class="cyber-btn-secondary text-xs" :disabled="busy" @click="load">Refresh</button></div>
 <p v-if="busy" role="status" class="text-xs">Inspecting the current definition…</p><p v-if="error" role="alert" class="text-red-600 text-xs">{{error}}</p><p v-if="message" class="text-emerald-700 text-xs">{{message}}</p>
 <div v-for="check in plan?.checks||[]" :key="check.id" class="border-t border-slate-100 pt-2 text-xs"><div class="flex justify-between"><strong>{{check.label}}</strong><span :class="check.status==='failed'?'text-red-600':check.status==='warning'?'text-amber-700':'text-emerald-700'">{{check.status}}</span></div><p class="mt-1 text-slate-600">{{check.message}}</p></div>
 <form class="space-y-2" @submit.prevent="save"><label class="block text-xs font-medium">Optional HTTP readiness URL (probed from DockTun)</label><input v-model="readiness" type="url" class="cyber-input w-full text-xs" placeholder="http://reachable-app:8080/ready"><p class="text-xs text-muted">Use an endpoint that checks the app and database. Leave empty to use only the container's existing Docker healthcheck/startup state. URL credentials and query secrets are not stored.</p><button class="cyber-btn-secondary text-xs">Save readiness check</button></form>
 <details><summary class="cursor-pointer text-xs font-medium" @click="!preview && inspectImport()">Preview stack import and storage</summary><p class="text-xs text-muted mt-2">This inspection does not create folders, modify networks, or adopt the stack.</p><pre v-if="preview" class="text-[11px] whitespace-pre-wrap max-h-64 overflow-auto mt-2">{{JSON.stringify(preview,null,2)}}</pre></details>
 <details><summary class="text-xs font-medium cursor-pointer">Maintenance history ({{events.length}})</summary><p v-for="event in events" :key="event.id" class="text-xs border-t py-2 mt-2">{{new Date(event.updated_at*1000).toLocaleString()}} · {{event.phase}} · {{event.message}}</p></details>
 <details><summary class="text-xs font-medium cursor-pointer">App incidents &amp; deliveries ({{incidents.length}})</summary><p v-for="incident in incidents" :key="incident.id" class="text-xs border-t py-2 mt-2">{{new Date(incident.created_at*1000).toLocaleString()}} · {{incident.kind}} · {{incident.phase}} · {{incident.message}}</p></details><p class="text-[11px] text-amber-800">{{plan?.rollback_scope}}</p>
 </section>
</template>
