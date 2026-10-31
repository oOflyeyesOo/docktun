<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { Plus, Save, Trash2 } from 'lucide-vue-next'
import { useApi } from '../composables/useApi'

interface Route { id: string; host: string; target: string; port: number; enabled: boolean }
interface Config { engine: 'caddy' | 'traefik' | 'nginx'; enabled: boolean; http_port: number; https_port: number; network: string; routes: Route[]; running: boolean }
const { apiFetch } = useApi()
const config = ref<Config>({ engine: 'caddy', enabled: false, http_port: 80, https_port: 443, network: 'docktun-proxy', routes: [], running: false })
const loading = ref(true), saving = ref(false), error = ref(''), message = ref('')
const engineHelp: Record<Config['engine'], string> = {
  caddy: 'Simple default with automatic HTTPS when DNS and certificates are available.',
  traefik: 'Dynamic, label-friendly routing for users managing many Docker services.',
  nginx: 'Traditional, explicit configuration for familiar Nginx deployments.',
}

async function load() {
  try { config.value = await apiFetch<Config>('/api/local-proxy') }
  catch (e: any) { error.value = e.message || 'Could not load local proxy settings' }
  finally { loading.value = false }
}
function addRoute() {
  config.value.routes.push({ id: `route-${Date.now()}`, host: '', target: '', port: 80, enabled: true })
}
function removeRoute(index: number) { config.value.routes.splice(index, 1) }
async function save() {
  saving.value = true; error.value = ''; message.value = ''
  try { config.value = await apiFetch<Config>('/api/local-proxy', { method: 'PUT', body: JSON.stringify(config.value) }); message.value = config.value.enabled ? `${config.value.engine} local proxy is running.` : 'Local proxy disabled.' }
  catch (e: any) { error.value = e.message || 'Could not apply local proxy settings' }
  finally { saving.value = false }
}
onMounted(load)
</script>

<template>
  <section class="cyber-card space-y-4">
    <div>
      <h2 class="text-xs text-muted uppercase tracking-widest font-mono">// Local Reverse Proxy</h2>
      <p class="mt-1 text-xs text-muted">Optional local routing for hostnames. DockTun creates and uses a dedicated shared Docker network; the proxy stays disabled until you enable it.</p>
    </div>
    <p v-if="loading" class="text-xs text-muted">Loading proxy settings…</p>
    <template v-else>
      <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
        <label class="space-y-1 text-xs text-muted">Proxy engine<select v-model="config.engine" class="cyber-input w-full"><option value="caddy">Caddy</option><option value="traefik">Traefik</option><option value="nginx">Nginx</option></select></label>
        <label class="flex items-center gap-2 text-xs text-slate-700 sm:pt-6"><input v-model="config.enabled" type="checkbox" :disabled="saving">Enable proxy <span v-if="config.running" class="text-emerald-600">(running)</span></label>
      </div>
      <div class="border border-indigo-100 bg-indigo-50/60 p-3 text-xs text-slate-700">
        <p><strong>{{ config.engine.charAt(0).toUpperCase() + config.engine.slice(1) }}</strong>: {{ engineHelp[config.engine] }}</p>
        <p class="mt-1 text-muted">Choose one engine at a time. Changing engines recreates the managed proxy with the same routes.</p>
      </div>
      <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
        <label class="space-y-1 text-xs text-muted">HTTP port<input v-model.number="config.http_port" type="number" min="1" max="65535" class="cyber-input w-full"><span class="block text-[10px]">Host port clients use for plain HTTP.</span></label>
        <label class="space-y-1 text-xs text-muted">HTTPS port<input v-model.number="config.https_port" type="number" min="1" max="65535" class="cyber-input w-full"><span class="block text-[10px]">Host port clients use for HTTPS. Avoid ports already in use.</span></label>
        <label class="space-y-1 text-xs text-muted">Docker network<input v-model="config.network" class="cyber-input w-full"><span class="block text-[10px]">Dedicated bridge shared by the proxy and selected targets.</span></label>
      </div>
      <div class="space-y-2">
        <div><div class="flex items-center justify-between"><h3 class="text-xs font-semibold text-slate-800">Routes</h3><button type="button" class="cyber-btn-secondary text-xs" @click="addRoute"><Plus class="w-3 h-3" />Add route</button></div><p class="mt-1 text-[10px] text-muted">Each route maps a hostname to a container's internal HTTP port. It does not publish that container port directly.</p></div>
        <div v-for="(route, index) in config.routes" :key="route.id" class="grid grid-cols-1 sm:grid-cols-[1fr_1fr_90px_auto_auto] gap-2 items-end border border-slate-200 p-2">
          <label class="text-[10px] text-muted">Hostname<input v-model="route.host" class="cyber-input w-full text-xs" placeholder="app.home"><span class="block mt-1">Resolve this name to the DockTun host.</span></label>
          <label class="text-[10px] text-muted">Container/service<input v-model="route.target" class="cyber-input w-full text-xs" placeholder="my-app"><span class="block mt-1">Docker container name, not a public URL.</span></label>
          <label class="text-[10px] text-muted">Internal port<input v-model.number="route.port" type="number" min="1" max="65535" class="cyber-input w-full text-xs"><span class="block mt-1">Port the app listens on inside Docker.</span></label>
          <label class="flex items-center gap-1 text-[10px] text-muted pb-2"><input v-model="route.enabled" type="checkbox">Enabled</label>
          <button type="button" class="cyber-btn-secondary text-xs pb-2" aria-label="Remove route" @click="removeRoute(index)"><Trash2 class="w-3 h-3" /></button>
        </div>
        <div class="border border-slate-200 bg-slate-50 p-3 text-[10px] text-muted">
          <p class="font-semibold text-slate-700">Example</p>
          <p class="mt-1"><code>jellyfin.home</code> → <code>jellyfin</code> → <code>8096</code></p>
          <p class="mt-1">After applying, browse to <code>http://jellyfin.home:{{ config.http_port }}</code> or <code>https://jellyfin.home:{{ config.https_port }}</code>. Your LAN DNS or hosts file must resolve <code>jellyfin.home</code> to the Docker host.</p>
        </div>
        <p v-if="!config.routes.length" class="text-xs text-muted">No routes configured.</p>
      </div>
      <p v-if="error" role="alert" class="text-xs text-red-600">{{ error }}</p>
      <p v-if="message" class="text-xs text-emerald-600">{{ message }}</p>
      <button type="button" class="cyber-btn w-full" :disabled="saving" @click="save"><Save class="w-3 h-3" />{{ saving ? 'APPLYING…' : 'APPLY LOCAL PROXY' }}</button>
    </template>
  </section>
</template>
