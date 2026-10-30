<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { Box, Search, ArrowDownToLine, ExternalLink, Globe, Database, Cpu, Activity, Film, HardDrive, Key, Tv } from 'lucide-vue-next'
import { useApi } from '../composables/useApi'
import { composeTemplates } from '../lib/compose-templates.mjs'
const icons: Record<string, any> = { Box, Globe, Database, Cpu, Activity, Film, HardDrive, Key, Tv }
const description = (text: string) => text.replace(/\[([^\]]+)\]\(https?:\/\/[^)]+\)/g, '$1')
const emit = defineEmits<{ select: [any] }>()
const { apiFetch } = useApi()
const bundled = composeTemplates.map(t => ({ ...t, source: 'DockTun', categories: [t.category], bundled: true, documentation: '', unavailable: '', url: '', collection: '' }))
const remote = ref<any[]>([])
const search = ref('')
const source = ref('All sources')
const category = ref('All categories')
const url = ref('')
const error = ref('')
const loading = ref(false)
const loaded = ref(false)
const visible = ref(36)
let active = true
onUnmounted(() => { active = false })
const items = computed(() => [...bundled, ...remote.value])
const sources = computed(() => ['All sources', ...new Set(items.value.map(t => t.source))])
const categories = computed(() => ['All categories', ...new Set(items.value.flatMap(t => t.categories || []).map(c => c.toLowerCase()))].sort((a, b) => a === 'All categories' ? -1 : b === 'All categories' ? 1 : a.localeCompare(b)))
const filtered = computed(() => items.value.filter(t =>
  (source.value === 'All sources' || source.value === t.source) &&
  (category.value === 'All categories' || (t.categories || []).some(c => c.toLowerCase() === category.value)) &&
  `${t.name} ${t.description} ${(t.categories || []).join(' ')} ${t.source}`.toLowerCase().includes(search.value.trim().toLowerCase())
))
watch([search, source, category], () => { visible.value = 36 })
function merge(entries: any[]) {
  const known = new Set(remote.value.map(t => `${t.collection || t.source}:${t.id}`))
  remote.value.push(...entries.filter(t => !known.has(`${t.collection || t.source}:${t.id}`)))
}
async function catalog() {
  loading.value = true; error.value = ''
  const results = await Promise.allSettled([
    apiFetch<any[]>('/api/templates/catalog?url=' + encodeURIComponent('https://raw.githubusercontent.com/Lissy93/portainer-templates/main/templates.json')),
    apiFetch<any[]>('/api/templates/catalog'),
  ])
  if (!active) return
  for (const result of results) {
    if (result.status === 'fulfilled') merge(result.value)
    else error.value += (error.value ? ' ' : '') + result.reason.message
  }
  loaded.value = results.every(result => result.status === 'fulfilled')
  loading.value = false
}
async function importTemplate(item: any) {
  if (item.bundled) {
    emit('select', { name: item.id, compose_content: item.compose, env_content: item.env || '', source_url: 'bundled:' + item.id }); return
  }
  loading.value = true; error.value = ''
  try {
    const response = await apiFetch('/api/templates/import', { method: 'POST', body: JSON.stringify(item.collection ? { collection: item.collection, id: item.id } : { url: item.url }) })
    if (active) emit('select', { ...response, name: item.name.replace(/[^a-z0-9_-]+/gi, '-').replace(/^-+|-+$/g, '').toLowerCase() || 'imported-app' })
  } catch (e: any) { if (active) error.value = e.message } finally { loading.value = false }
}
async function importURL() {
  if (/\.json$/i.test(url.value.trim())) {
    loading.value = true; error.value = ''
    try {
      const entries = await apiFetch<any[]>('/api/templates/catalog?url=' + encodeURIComponent(url.value.trim()))
      if (active) { merge(entries); source.value = entries[0]?.source || 'All sources'; search.value = ''; category.value = 'All categories' }
    } catch (e: any) { if (active) error.value = e.message } finally { loading.value = false }
  } else await importTemplate({ url: url.value.trim(), name: 'imported-app' })
}
</script>
<template>
  <section class="space-y-4">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div><h3 class="font-semibold text-slate-900">App library</h3><p class="text-xs text-slate-600 mt-1">Compose starters and community apps, together. Choose a template, edit its settings, then review storage before deploying.</p></div>
      <button class="cyber-btn-secondary text-xs shrink-0" :disabled="loading || loaded" @click="catalog"><ArrowDownToLine class="h-3.5 w-3.5 inline mr-1" />{{ loading ? 'Loading…' : loaded ? 'Community catalogs loaded' : 'Load community catalogs' }}</button>
    </div>
    <details class="rounded-xl border border-slate-200 bg-slate-50 p-3 text-xs text-slate-600">
      <summary class="cursor-pointer font-medium text-slate-800">Collections &amp; import from URL</summary>
      <p class="mt-2"><a href="https://github.com/Lissy93/portainer-templates" target="_blank" rel="noopener" class="text-indigo-600 underline">Lissy93's Portainer collection</a> combines hundreds of community apps, with upstream descriptions and categories. <a href="https://github.com/docker/awesome-compose" target="_blank" rel="noopener" class="text-indigo-600 underline">Docker Awesome Compose</a> provides development examples; some need additional repository files. DockTun's included starters are available offline.</p>
      <p class="mt-2">Add another public GitHub Portainer catalog (.json), Compose file (.yml/.yaml). Upstream storage paths must be checked on your host. Nothing is deployed or exposed by selecting a template.</p>
      <div class="flex flex-col sm:flex-row gap-2 mt-3"><input v-model="url" class="cyber-input flex-1 min-w-0 text-xs" placeholder="Public GitHub template or catalog URL" aria-label="Public template or catalog URL"><button class="cyber-btn-secondary text-xs" :disabled="loading || !url.trim()" @click="importURL">Import URL</button></div>
    </details>
    <p v-if="error" role="alert" class="text-xs text-red-700 rounded-lg border border-red-200 p-3">{{ error }}</p>
    <div class="flex flex-wrap gap-2">
      <div class="relative flex-1 min-w-48"><Search class="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-slate-500" /><input v-model="search" class="cyber-input text-xs pl-8 w-full" aria-label="Search app library" placeholder="Search apps, descriptions, categories…"></div>
      <select v-model="source" aria-label="Template source" class="cyber-input text-xs w-full sm:w-64"><option v-for="s in sources" :key="s">{{ s }}</option></select>
      <select v-model="category" aria-label="Template category" class="cyber-input text-xs capitalize w-full sm:w-44"><option v-for="c in categories" :key="c">{{ c }}</option></select>
    </div>
    <p class="text-xs text-slate-500">{{ filtered.length }} templates · {{ Math.min(visible, filtered.length) }} shown <span v-if="!loaded">· Load community catalogs for more apps</span></p>
    <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
      <article v-for="item in filtered.slice(0, visible)" :key="`${item.collection || item.source}:${item.id}`" class="rounded-xl border border-slate-200 bg-slate-50/70 p-4 flex flex-col gap-3 hover:border-cyan-400 transition">
        <div class="flex items-start gap-2.5"><div class="rounded-lg bg-cyan-50 border border-cyan-200 p-2 text-cyan-700"><component :is="icons[item.icon] || Box" class="h-4 w-4" /></div><div class="min-w-0"><h4 class="text-sm font-semibold text-slate-900 break-words">{{ item.name }}</h4><p class="text-[10px] text-indigo-600 mt-0.5">{{ item.source }}</p></div></div>
        <p class="text-xs text-slate-600 leading-relaxed whitespace-pre-wrap break-words line-clamp-4">{{ description(item.description) }}</p>
        <details v-if="item.description.length > 200" class="text-xs text-slate-600"><summary class="cursor-pointer text-indigo-600">Full description</summary><p class="mt-2 leading-relaxed break-words">{{ description(item.description) }}</p></details>
        <div class="flex flex-wrap gap-1"><span v-for="c in item.categories" :key="c" class="rounded bg-white border border-slate-200 px-1.5 py-0.5 text-[10px] text-slate-600 capitalize">{{ c }}</span><span v-for="port in item.ports || []" :key="port" class="text-[10px] font-mono text-cyan-700 border border-cyan-100 rounded px-1.5 py-0.5">{{ port }}</span></div>
        <p v-if="item.unavailable" class="text-xs text-amber-800">{{ item.unavailable }}</p>
        <div class="pt-3 mt-auto border-t border-slate-200 flex items-center justify-between gap-2"><a v-if="item.documentation" :href="item.documentation" target="_blank" rel="noopener" class="text-[11px] text-indigo-600 hover:underline">Source <ExternalLink class="h-3 w-3 inline" /></a><span v-else class="text-[11px] text-slate-500">Included starter</span><button class="cyber-btn-secondary text-xs" :disabled="loading || !!item.unavailable" @click="importTemplate(item)">Use template</button></div>
      </article>
    </div>
    <p v-if="!filtered.length" class="text-sm text-slate-500 py-8 text-center">No templates match these filters.</p>
    <button v-if="filtered.length > visible" class="cyber-btn-secondary text-xs w-full" @click="visible += 36">Show 36 more</button>
  </section>
</template>
