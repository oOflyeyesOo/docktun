<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  cpu?: string
  memory?: string
  running?: boolean
}>()

const cpuPercent = computed<number>(() => {
  if (!props.running || !props.cpu) return 0
  const val = parseFloat(props.cpu.replace('%', '').trim())
  return isNaN(val) ? 0 : Math.min(Math.max(val, 0), 100)
})

const cpuColorClass = computed(() => {
  if (cpuPercent.value >= 80) return 'bg-rose-500'
  if (cpuPercent.value >= 50) return 'bg-amber-500'
  return 'bg-emerald-500'
})

const memoryInfo = computed(() => {
  if (!props.running || !props.memory) {
    return { used: '—', total: '', percent: 0 }
  }
  const parts = props.memory.split('/')
  const used = parts[0]?.trim() || '—'
  const total = parts[1]?.trim() || ''

  // Attempt to parse bytes for a ratio if both exist
  let percent = 0
  if (parts.length === 2) {
    const parseSize = (str: string): number => {
      const match = str.match(/^([\d.]+)\s*([a-zA-Z]+)?$/)
      if (!match) return 0
      const num = parseFloat(match[1])
      const unit = (match[2] || '').toUpperCase()
      if (unit.startsWith('G')) return num * 1024
      if (unit.startsWith('M')) return num
      if (unit.startsWith('K')) return num / 1024
      return num / (1024 * 1024)
    }
    const uNum = parseSize(used)
    const tNum = parseSize(total)
    if (tNum > 0) {
      percent = Math.min(Math.max(Math.round((uNum / tNum) * 100), 0), 100)
    }
  }

  return { used, total, percent }
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
