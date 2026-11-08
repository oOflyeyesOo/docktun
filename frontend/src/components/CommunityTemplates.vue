<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { Box, Search, RefreshCw, ExternalLink, Globe, Database, Cpu, Activity, Film, HardDrive, Key, Tv, LayoutGrid, Notebook, Code2, Wrench, Home, ChevronRight, Loader2, CheckCircle2 } from 'lucide-vue-next'
import { useApi } from '../composables/useApi'
import { templateTier } from '../lib/template-tiers.mjs'
import { composeTemplates } from '../lib/compose-templates.mjs'
import { libraryCategories, libraryCategory, librarySource, loadLibraryCatalog, mergeLibraryEntries } from '../lib/template-library.mjs'
const icons: Record<string, any> = { Box, Globe, Database, Cpu, Activity, Film, HardDrive, Key, Tv, LayoutGrid, Notebook, Code2, Wrench, Home }
const emit = defineEmits<{ select: [any] }>()
const { apiFetch } = useApi()
const bundled = composeTemplates.map(t => ({ ...t, source: 'DockTun', categories: [t.category], bundled: true, documentation: '', unavailable: t.unavailable || '', collection: '' }))
const remote = ref<any[]>([])
const search = ref(''), source = ref('all'), category = ref('all'), sort = ref('recommended'), url = ref('')
const includeAdvanced = ref(false), catalogLoading = ref(false), importing = ref('')
const error = ref(''), warnings = ref<string[]>([]), visible = ref(24), readySources = ref(0)
const sourceOptions = [{ id: 'all', name: 'All sources' }, { id: 'starters', name: 'Included starters' }, { id: 'community', name: 'Community apps' }, { id: 'examples', name: 'Compose examples' }]
const catalogs = [
  { name: 'Community apps', path: '/api/templates/catalog?url=' + encodeURIComponent('https://raw.githubusercontent.com/Lissy93/portainer-templates/main/templates.json') },
  { name: 'Compose examples', path: '/api/templates/catalog' },
]
let active = true
onUnmounted(() => { active = false })
onMounted(() => { void catalog() })
const items = computed(() => [...bundled, ...remote.value])
const available = computed(() => items.value.filter(item => includeAdvanced.value || !item.unavailable))
const baseFiltered = computed(() => available.value.filter(item =>
  (source.value === 'all' || librarySource(item) === source.value) &&
  (item.name + ' ' + item.description + ' ' + (item.categories || []).join(' ') + ' ' + item.source).toLowerCase().includes(search.value.trim().toLowerCase())
))
const categories = computed(() => libraryCategories.map(group => ({ ...group, count: baseFiltered.value.filter(item => group.id === 'all' || libraryCategory(item) === group.id).length })))
const filtered = computed(() => baseFiltered.value.filter(item => category.value === 'all' || libraryCategory(item) === category.value).sort((a, b) => {
  if (sort.value === 'recommended') {
    const rank = (item: any) => (item.bundled ? 0 : item.unavailable ? 3 : item.collection ? 1 : 2)
    if (rank(a) !== rank(b)) return rank(a) - rank(b)
  }
  return a.name.localeCompare(b.name)
}))
const heading = computed(() => search.value ? 'Search results' : category.value === 'all' ? 'Explore the library' : libraryCategories.find(group => group.id === category.value)?.name)
watch([search, source, category, sort, includeAdvanced], () => { visible.value = 24 })
const description = (text: string) => text.replace(/\[([^\]]+)\]\(https?:\/\/[^)]+\)/g, '$1')
const groupIcon = (item: any) => icons[item.icon] || icons[libraryCategories.find(group => group.id === libraryCategory(item))?.icon || 'Box']
const sourceLabel = (item: any) => item.bundled ? 'Included starter' : item.collection ? 'Community' : 'Compose example'
function resetFilters() { search.value = ''; source.value = 'all'; category.value = 'all'; includeAdvanced.value = false }
async function catalog(refresh = false) {
  catalogLoading.value = true; warnings.value = []; readySources.value = 0
  await Promise.all(catalogs.map(async entry => {
    try {
      const entries = await loadLibraryCatalog(entry.path, () => apiFetch<any[]>(entry.path), refresh)
      if (active) { remote.value = mergeLibraryEntries(remote.value, entries); readySources.value++ }
    } catch (e: any) { if (active) warnings.value.push(entry.name + ': ' + e.message) }
  }))
  if (active) catalogLoading.value = false
}
async function importTemplate(item: any) {
  if (item.unavailable) { error.value = item.unavailable; return }
  if (item.bundled) {
    emit('select', { name: item.id, compose_content: item.compose, env_content: item.env || '', source_url: 'bundled:' + item.id }); return
  }
  importing.value = item.id || item.name; error.value = ''
  try {
    const response = await apiFetch('/api/templates/import', { method: 'POST', body: JSON.stringify(item.collection ? { collection: item.collection, id: item.id } : { url: item.url }) })
    if (active) emit('select', { ...response, name: item.name.replace(/[^a-z0-9_-]+/gi, '-').replace(/^-+|-+$/g, '').toLowerCase() || 'imported-app' })
  } catch (e: any) { if (active) error.value = e.message } finally { importing.value = '' }
}
async function importURL() {
  if (/\.json$/i.test(url.value.trim())) {
    importing.value = 'collection'; error.value = ''
    try {
      const entries = await apiFetch<any[]>('/api/templates/catalog?url=' + encodeURIComponent(url.value.trim()))
      if (active) { remote.value = mergeLibraryEntries(remote.value, entries); resetFilters(); source.value = 'community'; url.value = '' }
    } catch (e: any) { if (active) error.value = e.message } finally { importing.value = '' }
  } else await importTemplate({ url: url.value.trim(), name: 'imported-app' })
}
</script>

<template>
  <section class="space-y-5">
    <header class="rounded-2xl border border-indigo-100 bg-gradient-to-br from-indigo-50 via-white to-cyan-50 p-4">
      <div class="flex items-start justify-between gap-4">
        <div>
          <h3 class="text-xl font-semibold tracking-tight text-slate-900">Find your next app</h3>
          <p class="mt-1 max-w-xl text-xs leading-relaxed text-slate-600">Choose an app, make it yours, and deploy. App data storage is created for you.</p>
        </div>
        <button type="button" class="rounded-xl border border-white bg-white/80 p-2.5 text-slate-500 hover:text-indigo-600 disabled:opacity-50" aria-label="Refresh app library" title="Refresh app library" :disabled="catalogLoading || !!importing" @click="catalog(true)"><RefreshCw class="h-4 w-4" :class="{ 'animate-spin': catalogLoading }" /></button>
      </div>
      <div class="mt-3 flex flex-wrap items-center gap-x-4 gap-y-2 text-[11px] text-slate-500" role="status">
        <span class="font-medium text-slate-700">{{ available.length }} apps to explore</span>
        <span class="flex items-center gap-1.5"><Loader2 v-if="catalogLoading" class="h-3.5 w-3.5 animate-spin" /><CheckCircle2 v-else-if="readySources === catalogs.length" class="h-3.5 w-3.5 text-emerald-600" />{{ catalogLoading ? 'Loading community catalogs automatically…' : readySources === catalogs.length ? 'Community catalogs ready' : 'Included starters available offline' }}</span>
        <span>Exposure is optional</span>
      </div>
    </header>
    <div v-if="warnings.length" class="rounded-xl border border-amber-200 bg-amber-50 p-3 text-xs text-amber-900">
      <p class="font-medium">Some catalogs could not load. You can still use the available apps.</p><p v-for="warning in warnings" :key="warning" class="mt-1 break-words">{{ warning }}</p><button type="button" class="mt-2 font-semibold underline disabled:opacity-50" :disabled="catalogLoading" @click="catalog(true)">Retry catalogs</button>
    </div>
    <p v-if="error" role="alert" class="rounded-xl border border-red-200 bg-red-50 p-3 text-xs text-red-700">{{ error }}</p>
    <div class="grid gap-5 lg:grid-cols-[175px_minmax(0,1fr)]">
      <aside class="space-y-4">
        <nav aria-label="App categories" class="flex gap-1.5 overflow-x-auto pb-1 lg:flex-col lg:overflow-visible">
          <button v-for="group in categories" :key="group.id" type="button" class="flex shrink-0 items-center gap-2 rounded-xl px-3 py-2.5 text-xs transition lg:w-full" :class="category === group.id ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-600 hover:bg-slate-100'" :aria-pressed="category === group.id" @click="category = group.id">
            <component :is="icons[group.icon]" class="h-4 w-4 shrink-0" /><span class="whitespace-nowrap">{{ group.name }}</span><span class="ml-auto text-[10px] opacity-70">{{ group.count }}</span>
          </button>
        </nav>
        <details class="rounded-xl border border-slate-200 bg-slate-50/70 p-3 text-xs text-slate-600">
          <summary class="cursor-pointer font-medium text-slate-800">Collections &amp; import</summary>
          <p class="mt-3 leading-relaxed"><a href="https://github.com/Lissy93/portainer-templates" target="_blank" rel="noopener" class="text-indigo-600 underline">Portainer community apps</a> and <a href="https://github.com/docker/awesome-compose" target="_blank" rel="noopener" class="text-indigo-600 underline">Docker Awesome Compose</a> load automatically. Included starters also work offline.</p>
          <p class="mt-2 leading-relaxed">Add a public GitHub catalog or Compose file. Examples may need supporting files before deployment.</p>
          <form class="mt-3 space-y-2" @submit.prevent="importURL"><input v-model="url" type="url" class="cyber-input w-full text-xs" placeholder="Public GitHub URL" aria-label="Public template or catalog URL" required><button type="submit" class="cyber-btn-secondary w-full text-xs" :disabled="!!importing || !url.trim()">Add source</button></form>
        </details>
      </aside>
      <div class="min-w-0 space-y-4">
        <div class="relative"><Search class="absolute left-3.5 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" /><input v-model="search" class="cyber-input w-full rounded-xl bg-white py-3 pl-10 text-sm" aria-label="Search app library" placeholder="Search apps, descriptions, or categories…"></div>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="flex flex-wrap gap-1 rounded-lg bg-slate-100 p-1" aria-label="Template sources"><button v-for="option in sourceOptions" :key="option.id" type="button" class="rounded-md px-2.5 py-1.5 text-[11px] transition" :class="source === option.id ? 'bg-white font-medium text-slate-900 shadow-sm' : 'text-slate-500 hover:text-slate-800'" :aria-pressed="source === option.id" @click="source = option.id">{{ option.name }}</button></div>
          <select v-model="sort" class="rounded-lg border border-slate-200 bg-white px-2 py-1.5 text-xs text-slate-600" aria-label="Sort app library"><option value="recommended">Starters first</option><option value="name">Name A–Z</option></select>
        </div>
        <div class="flex flex-wrap items-center justify-between gap-2 border-b border-slate-100 pb-3"><div><h4 class="text-sm font-semibold text-slate-800">{{ heading }}</h4><p class="mt-0.5 text-[11px] text-slate-500">{{ filtered.length }} apps · showing {{ Math.min(visible, filtered.length) }}</p></div><label class="flex items-center gap-1.5 text-[11px] text-slate-500"><input v-model="includeAdvanced" type="checkbox" class="rounded">Include apps needing manual setup</label></div>
        <div class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
          <article v-for="item in filtered.slice(0, visible)" :key="(item.collection || item.source) + ':' + item.id" class="group flex flex-col rounded-2xl border border-slate-200 bg-white p-4 transition hover:border-indigo-300 hover:shadow-md">
            <div class="flex items-start justify-between gap-2"><div class="rounded-xl p-2.5" :class="item.bundled ? 'bg-indigo-50 text-indigo-600' : item.collection ? 'bg-cyan-50 text-cyan-700' : 'bg-amber-50 text-amber-700'"><component :is="groupIcon(item)" class="h-5 w-5" /></div><span class="rounded-full bg-slate-50 px-2 py-1 text-[9px] font-medium text-slate-500">{{ templateTier(item).label }}</span></div>
            <h4 class="mt-3 text-sm font-semibold leading-snug text-slate-900 break-words">{{ item.name }}</h4>
            <p class="mt-2 line-clamp-3 text-xs leading-relaxed text-slate-500 break-words">{{ description(item.description) }}</p>
            <details v-if="item.description.length > 180" class="mt-2 text-[11px] text-slate-500"><summary class="cursor-pointer text-indigo-600">More about this app</summary><p class="mt-2 whitespace-pre-wrap leading-relaxed break-words">{{ description(item.description) }}</p></details>
            <div class="mt-3 flex flex-wrap gap-1"><span v-for="tag in (item.categories || []).slice(0, 2)" :key="tag" class="rounded-md bg-slate-50 px-1.5 py-0.5 text-[9px] capitalize text-slate-500">{{ tag }}</span></div>
            <p v-if="item.unavailable" class="mt-3 text-[11px] leading-relaxed text-amber-800">{{ item.unavailable }}</p>
            <p class="text-[10px] text-slate-500 mt-2">{{ templateTier(item).description }}</p><div class="mt-auto pt-4"><button type="button" class="flex w-full items-center justify-between rounded-lg bg-slate-50 px-3 py-2 text-xs font-semibold text-indigo-600 transition group-hover:bg-indigo-50 hover:!bg-indigo-100 disabled:opacity-50" :disabled="!!importing || !!item.unavailable" @click="importTemplate(item)"><span>{{ importing === item.id ? 'Preparing app…' : item.unavailable ? 'Manual setup needed' : 'Set up app' }}</span><Loader2 v-if="importing === item.id" class="h-3.5 w-3.5 animate-spin" /><ChevronRight v-else class="h-3.5 w-3.5" /></button><a v-if="item.documentation" :href="item.documentation" target="_blank" rel="noopener" class="mt-2 flex items-center gap-1 truncate text-[9px] text-slate-400 hover:text-indigo-600" :title="item.source">{{ item.source }}<ExternalLink class="h-2.5 w-2.5 shrink-0" /></a><span v-else class="mt-2 block text-[9px] text-slate-400">Included with DockTun</span></div>
          </article>
        </div>
        <div v-if="!filtered.length" class="rounded-2xl border border-dashed border-slate-200 py-12 text-center"><Search class="mx-auto h-6 w-6 text-slate-300" /><p class="mt-3 text-sm font-medium text-slate-700">No apps match your filters</p><button type="button" class="mt-2 text-xs font-medium text-indigo-600" @click="resetFilters">Clear filters</button></div>
        <button v-if="filtered.length > visible" type="button" class="cyber-btn-secondary w-full text-xs" @click="visible += 24">Show 24 more apps</button>
      </div>
    </div>
  </section>
</template>
