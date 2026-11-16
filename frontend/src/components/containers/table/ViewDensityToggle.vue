<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'
import { Table, LayoutGrid } from 'lucide-vue-next'

const props = defineProps<{
  modelValue: 'table' | 'grid'
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', val: 'table' | 'grid'): void
}>()

function setMode(mode: 'table' | 'grid') {
  if (props.modelValue !== mode) {
    emit('update:modelValue', mode)
    try {
      localStorage.setItem('docktun:view_mode', mode)
    } catch (e) {
      console.warn('Failed to save view mode to localStorage', e)
    }
  }
}

function handleKeydown(e: KeyboardEvent) {
  if (e.altKey && (e.key === 'v' || e.key === 'V')) {
    e.preventDefault()
    setMode(props.modelValue === 'table' ? 'grid' : 'table')
  }
}

onMounted(() => {
  window.addEventListener('keydown', handleKeydown)
})

onUnmounted(() => {
  window.removeEventListener('keydown', handleKeydown)
})
</script>

<template>
  <div
    class="inline-flex items-center rounded-lg border border-slate-200 bg-slate-100 p-0.5 text-xs shadow-inner"
    role="group"
    aria-label="View density toggle"
  >
    <button
      type="button"
      @click="setMode('table')"
      :class="[
        'flex items-center gap-1.5 rounded-md px-2.5 py-1 transition-all cursor-pointer select-none',
        modelValue === 'table'
          ? 'bg-white text-cyan-800 font-semibold shadow-xs border border-slate-200/80'
          : 'text-slate-600 hover:text-slate-900 border border-transparent'
      ]"
      title="High-Density Table View (Alt+V)"
      :aria-pressed="modelValue === 'table'"
    >
      <Table class="h-3.5 w-3.5" />
      <span class="hidden sm:inline">Table</span>
    </button>

    <button
      type="button"
      @click="setMode('grid')"
      :class="[
        'flex items-center gap-1.5 rounded-md px-2.5 py-1 transition-all cursor-pointer select-none',
        modelValue === 'grid'
          ? 'bg-white text-cyan-800 font-semibold shadow-xs border border-slate-200/80'
          : 'text-slate-600 hover:text-slate-900 border border-transparent'
      ]"
      title="Cards Grid View (Alt+V)"
      :aria-pressed="modelValue === 'grid'"
    >
      <LayoutGrid class="h-3.5 w-3.5" />
      <span class="hidden sm:inline">Grid</span>
    </button>
  </div>
</template>
