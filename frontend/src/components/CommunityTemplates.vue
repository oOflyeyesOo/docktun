<script setup lang="ts">
import { onUnmounted, ref } from 'vue'
import { useApi } from '../composables/useApi'
const emit = defineEmits<{ select: [any] }>()
const { apiFetch } = useApi()
const items = ref<any[]>([])
const url = ref('')
const search = ref('')
const error = ref('')
const loading = ref(false)
let active = true
onUnmounted(() => { active = false })
async function catalog() {
  loading.value = true; error.value = ''
  try { const response = await apiFetch('/api/templates/catalog'); if (active) items.value = response } catch (e: any) { if (active) error.value = e.message } finally { loading.value = false }
}
async function importCompose(address: string, name = '') {
  loading.value = true; error.value = ''
  try {
    const response = await apiFetch('/api/templates/import', { method: 'POST', body: JSON.stringify({ url: address }) })
    if (!active) return
    emit('select', { ...response, name: name.replace(/[^a-z0-9_-]+/gi, '-').toLowerCase() || 'imported-app' })
  } catch (e: any) { error.value = e.message } finally { loading.value = false }
}
</script>
<template>
  <section class="rounded-xl border border-indigo-200 bg-indigo-50/40 p-4 space-y-3">
    <div class="flex flex-wrap items-center justify-between gap-2"><h3 class="font-semibold text-sm text-slate-900">Import a community example</h3><button class="cyber-btn-secondary text-xs" :disabled="loading" @click="catalog">{{ loading ? 'Loading…' : 'Browse Docker Awesome Compose' }}</button></div>
    <p class="text-xs text-slate-600">Docker's examples are intended for development. Review passwords, storage and any required repository files before deploying. Selecting an example only opens the editor.</p>
    <div class="flex flex-col sm:flex-row gap-2"><input v-model="url" class="cyber-input flex-1 text-xs" placeholder="Public GitHub Compose file URL (.yml or .yaml)" aria-label="Public Compose URL"><button class="cyber-btn-secondary text-xs" :disabled="loading || !url.trim()" @click="importCompose(url)">Import Compose</button></div>
    <p v-if="error" role="alert" class="text-xs text-red-700">{{ error }}</p>
    <template v-if="items.length">
      <input v-model="search" class="cyber-input w-full text-xs" placeholder="Search Docker Awesome Compose" aria-label="Search community examples">
      <div class="grid sm:grid-cols-2 gap-2 max-h-64 overflow-y-auto"><div v-for="item in items.filter(item => item.name.includes(search.toLowerCase()))" :key="item.url" class="rounded-lg border border-slate-200 bg-white p-3 flex items-center justify-between gap-2"><div><p class="font-medium text-xs text-slate-800 capitalize">{{ item.name }}</p><a :href="item.documentation" target="_blank" rel="noopener" class="text-xs text-indigo-600 hover:underline">Upstream instructions ↗</a></div><button class="cyber-btn-secondary text-xs" :disabled="loading" @click="importCompose(item.url, item.name)">Use example</button></div></div>
    </template>
  </section>
</template>
