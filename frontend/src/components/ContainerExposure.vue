<script setup lang="ts">
import { computed,ref,watch } from 'vue'
import { useApi } from '../composables/useApi'
import ContainerTailscale from './ContainerTailscale.vue'
const props = defineProps<{
  container: { id: string; name: string; ports?: string }
  settings: any
  saving?: boolean
  message?: { type: string; text: string }
}>()
const emit=defineEmits<{ save: [] }>()
const {apiFetch}=useApi();const plan=ref<any>(null),checking=ref(false),error=ref('')
watch(()=>JSON.stringify([props.container.id,props.settings]),()=>{plan.value=null})
async function preview(){checking.value=true;error.value='';const snapshot=JSON.stringify([props.container.id,props.settings]);try{const result=await apiFetch('/api/docker/containers/'+props.container.id+'/exposure-plan',{method:'POST',body:JSON.stringify(props.settings)});if(snapshot===JSON.stringify([props.container.id,props.settings]))plan.value=result}catch(e:any){error.value=e.message}finally{checking.value=false}}
function apply(){if(!plan.value?.ready)return;if(props.settings.mode==='public'&&!props.settings.zero_trust_enabled&&!window.confirm('Publish this app without Cloudflare Access protection? Confirm the application has the authentication you need.'))return;emit('save')}

const provider = computed({
  get: () => props.settings.mode === 'public' ? 'cloudflare' : props.settings.mode === 'private' ? props.settings.private_type : 'none',
  set: (value: string) => {
    props.settings.mode = value === 'none' ? 'internal' : value === 'cloudflare' ? 'public' : 'private'
    props.settings.private_type = ['tailscale', 'wireguard', 'mesh'].includes(value) ? value : ''
  },
})
</script>

<template>
  <section class="cyber-card space-y-5">
    <div>
      <h3 class="text-base font-semibold text-slate-900">Expose this container</h3>
      <p class="mt-1 text-sm text-slate-600">Optional remote access. Choose how people should reach this app.</p>
    </div>
    <label class="block space-y-1 text-sm">
      <span>Exposure method</span>
      <select v-model="provider" class="cyber-input w-full">
        <option value="none">None</option>
        <option value="cloudflare">Cloudflare (Argo Tunnel) — public hostname</option>
        <option value="tailscale">Tailscale — private app device</option>
        <option value="wireguard">WireGuard — existing VPN address</option>
        <option v-if="settings.private_type === 'mesh'" value="mesh">Existing custom mesh</option>
      </select>
    </label>
    <p v-if="provider === 'none'" class="text-sm text-slate-600">No DockTun-managed remote route. Existing published Docker ports and local networking keep their own access settings.</p>
    <template v-else-if="provider === 'cloudflare'">
      <p class="text-sm text-slate-600">Requires a configured Cloudflare tunnel (Argo Tunnel) and domain. For a connector-token tunnel, publishing here also needs an API token with account tunnel Edit/Write and zone DNS Edit permissions. A connector token or DNS-only zone token cannot publish routes. <router-link to="/access?tab=cloudflare" class="text-indigo-700 underline">Provider settings and token instructions</router-link></p>
      <label class="block space-y-1 text-sm"><span>Public hostname</span><input v-model="settings.hostname" class="cyber-input w-full" placeholder="app.example.com" /></label>
      <label class="block space-y-1 text-sm"><span>Container target</span><input v-model="settings.target" class="cyber-input w-full" :placeholder="`http://${container.name}:8080`" /></label>
      <details class="rounded-lg border border-slate-200 p-3 space-y-3">
        <summary class="cursor-pointer text-sm font-medium">Cloudflare Access protection</summary>
        <label class="flex items-center gap-2 text-sm"><input v-model="settings.zero_trust_enabled" type="checkbox" /> Require Cloudflare Access authentication</label>
        <template v-if="settings.zero_trust_enabled">
          <label class="block text-sm">Authentication<select v-model="settings.zero_trust_auth_type" class="cyber-input w-full"><option value="email">Email</option><option value="domain">Email domain</option><option value="everyone">Any authenticated user</option><option value="ip">IP / CIDR allowlist</option><option value="bypass_ip">IP / CIDR bypass</option></select></label>
          <label v-if="settings.zero_trust_auth_type !== 'everyone'" class="block text-sm">Allowed emails, domains, or IP ranges<input v-model="settings.zero_trust_emails" class="cyber-input w-full" placeholder="person@example.com" /></label>
          <label class="block text-sm">Session duration<select v-model="settings.zero_trust_session" class="cyber-input w-full"><option value="1h">1 hour</option><option value="12h">12 hours</option><option value="24h">24 hours</option><option value="168h">7 days</option></select></label>
        </template>
      </details>
    </template>
    <template v-else-if="provider === 'tailscale'">
      <ContainerTailscale :key="container.id" :container-id="container.id" />
      <label class="block space-y-1 text-sm"><span>Container target (name:internal port)</span><input v-model="settings.target" class="cyber-input w-full" :placeholder="`${container.name}:8080`" /></label>
      <label class="block space-y-1 text-sm"><span>Port on this app’s Tailscale device</span><input v-model.number="settings.private_port" type="number" min="1" max="65535" class="cyber-input w-full" /></label>
      <p v-if="settings.private_host" class="text-sm text-slate-600">Device address: <span class="font-mono">{{ settings.private_host }}</span></p>
    </template>
    <template v-else>
      <p class="text-sm text-slate-600">Use an address already reachable through your VPN. Configure routing and any required port publishing in your VPN setup. <router-link to="/access?tab=wireguard" class="text-indigo-700 underline">Provider settings</router-link></p>
      <label class="block space-y-1 text-sm"><span>VPN address</span><input v-model="settings.private_host" class="cyber-input w-full" placeholder="VPN-reachable host or IP" /></label>
      <label class="block space-y-1 text-sm"><span>Access port</span><input v-model.number="settings.private_port" type="number" min="1" max="65535" class="cyber-input w-full" /></label>
    </template>
    <div class="space-y-2 text-xs"><button type="button" class="cyber-btn-secondary" :disabled="checking || saving" @click="preview">{{checking?'Checking…':'Preview access changes'}}</button><p v-if="error" class="text-red-700">{{error}}</p><template v-if="plan"><p v-for="check in plan.checks" :key="check.id" :class="check.status==='failed'?'text-red-700':check.status==='warning'?'text-amber-800':'text-emerald-700'">{{check.label}}: {{check.message}}</p><p v-for="change in plan.changes" :key="change">{{change}}</p><p v-for="note in plan.limitations" :key="note" class="text-muted">{{note}}</p></template></div>
 <div class="flex flex-wrap items-center gap-3 border-t border-slate-200 pt-4">
      <button type="button" class="cyber-btn" :disabled="saving || !plan?.ready" @click="apply">{{ saving ? 'Saving…' : 'Save exposure' }}</button>
      <p v-if="message" role="status" class="text-sm" :class="message.type === 'error' ? 'text-red-700' : 'text-emerald-700'">{{ message.text }}</p>
    </div>
  </section>
</template>
