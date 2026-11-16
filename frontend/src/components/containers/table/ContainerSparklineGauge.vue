<script setup lang="ts">
import { computed } from 'vue'
import { parseCpuPercent, parseMemoryUsage } from '../../../lib/containers-table.mjs'

const props = defineProps<{
  cpu?: string
  memory?: string
  running?: boolean
}>()

const cpuPercent = computed<number>(() => {
  const val = parseCpuPercent(props.cpu, props.running)
  return Math.min(Math.max(val, 0), 100)
})

const cpuColorClass = computed(() => {
  if (cpuPercent.value >= 80) return 'bg-rose-500'
  if (cpuPercent.value >= 50) return 'bg-amber-500'
  return 'bg-emerald-500'
})

const memoryInfo = computed(() => {
  return parseMemoryUsage(props.memory, props.running)
})

const memoryColorClass = computed(() => {
  if (memoryInfo.value.percent >= 80) return 'bg-rose-500'
  if (memoryInfo.value.percent >= 50) return 'bg-amber-500'
  return 'bg-cyan-600'
})
</script>

<template>
  <div
    class="flex items-center gap-3 font-mono text-[11px] tabular-nums select-none"
    :title="running ? `CPU: ${cpu || '0%'} | Memory: ${memory || '—'}` : 'Container stopped'"
  >
    <!-- CPU Gauge -->
    <div class="flex items-center gap-1.5 w-20">
      <span class="w-9 text-right text-slate-700 truncate">
        {{ running && cpu ? (cpu.endsWith('%') ? cpu : `${cpu}%`) : '—' }}
      </span>
      <div class="h-1.5 w-9 flex-1 rounded-full bg-slate-200 overflow-hidden">
        <div
          v-if="running && cpuPercent > 0"
          class="h-full rounded-full transition-all duration-300"
          :class="cpuColorClass"
          :style="{ width: `${Math.max(cpuPercent, 5)}%` }"
        ></div>
      </div>
    </div>

    <!-- Memory Gauge -->
    <div class="flex items-center gap-1.5 w-24">
      <span class="w-13 text-right text-slate-700 truncate" :title="memoryInfo.total ? `${memoryInfo.used} / ${memoryInfo.total}` : memoryInfo.used">
        {{ memoryInfo.used }}
      </span>
      <div class="h-1.5 w-9 flex-1 rounded-full bg-slate-200 overflow-hidden">
        <div
          v-if="running && memoryInfo.percent > 0"
          class="h-full rounded-full transition-all duration-300"
          :class="memoryColorClass"
          :style="{ width: `${Math.max(memoryInfo.percent, 5)}%` }"
        ></div>
      </div>
    </div>
  </div>
</template>
