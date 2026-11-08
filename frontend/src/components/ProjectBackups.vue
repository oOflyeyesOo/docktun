<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { Archive, Download, Upload } from 'lucide-vue-next'
import { useApi } from '../composables/useApi'
const { apiFetch, apiResponse } = useApi()
const projects = ref<any[]>([]), loading = ref(true), busy = ref(false), error = ref(''), message = ref('')
const restore = ref({ name: '', path: '' }), archive = ref<File | null>(null)
async function load() {
  try { projects.value = await apiFetch<any[]>('/api/projects') }
  catch (e: any) { error.value = e.message || 'Could not load projects' }
  finally { loading.value = false }
}
async function download(project: any) {
  error.value = ''
  try {
    const response = await apiResponse(`/api/projects/${project.id}/backup`)
    if (!response.ok) throw new Error(await response.text())
    const blob = await response.blob(), url = URL.createObjectURL(blob), link = document.createElement('a')
    link.href = url; link.download = `${project.name}.tar.gz`; link.click(); URL.revokeObjectURL(url)
  } catch (e: any) { error.value = e.message || 'Could not create project backup' }
}
async function restoreProject() {
  if (!archive.value || !restore.value.name.trim() || !restore.value.path.trim()) return
  busy.value = true; error.value = ''; message.value = ''
  try {
    const form = new FormData(); form.append('name', restore.value.name); form.append('path', restore.value.path); form.append('backup', archive.value)
    await apiFetch('/api/projects/restore', { method: 'POST', body: form })
    message.value = 'Project restored. Review its Compose settings before starting it.'
    restore.value = { name: '', path: '' }; archive.value = null; await load()
  } catch (e: any) { error.value = e.message || 'Could not restore project backup' }
  finally { busy.value = false }
}
onMounted(load)
</script>
<template>
  <section class="cyber-card space-y-4">
    <div><div class="flex items-center gap-2"><Archive class="w-4 h-4 text-neon" /><h2 class="text-xs text-muted uppercase tracking-widest font-mono">// Project Backup &amp; Restore</h2></div><p class="mt-1 text-xs text-muted">Export Compose, environment, and supporting files into a portable archive. Backups can contain secrets; store them securely.</p></div>
    <p v-if="loading" class="text-xs text-muted">Loading projects…</p>
    <div v-else class="space-y-2">
      <div v-for="project in projects" :key="project.id" class="flex items-center justify-between gap-3 border border-slate-200 p-2"><div class="min-w-0"><p class="truncate text-xs font-semibold text-slate-800">{{ project.name }}</p><p class="truncate text-[10px] text-muted">{{ project.path }}</p></div><button type="button" class="cyber-btn-secondary text-xs" @click="download(project)"><Download class="w-3 h-3" />Export</button></div>
      <p v-if="!projects.length" class="text-xs text-muted">No registered projects.</p>
    </div>
    <div class="space-y-2 border-t border-slate-200 pt-3"><h3 class="text-xs font-semibold text-slate-800">Restore to a new project</h3><div class="grid grid-cols-1 sm:grid-cols-2 gap-2"><input v-model="restore.name" class="cyber-input text-xs" placeholder="New project name"><input v-model="restore.path" class="cyber-input text-xs" placeholder="/opt/projects/new-project"></div><input type="file" accept=".gz,.tgz,application/gzip" class="block w-full text-xs" @change="archive = ($event.target as HTMLInputElement).files?.[0] || null"><button type="button" class="cyber-btn w-full" :disabled="busy || !archive || !restore.name || !restore.path" @click="restoreProject"><Upload class="w-3 h-3" />{{ busy ? 'RESTORING…' : 'RESTORE PROJECT' }}</button></div>
    <p v-if="error" role="alert" class="text-xs text-red-600">{{ error }}</p><p v-if="message" class="text-xs text-emerald-600">{{ message }}</p>
  </section>
</template>
