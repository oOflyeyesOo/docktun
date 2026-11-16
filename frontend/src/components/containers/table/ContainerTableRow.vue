<script setup lang="ts">
import { ref } from 'vue'
import {
  Play,
  Square,
  RotateCcw,
  ScrollText,
  Terminal,
  FolderOpen,
  Code2,
  FileCode,
  RefreshCw,
  MoreVertical,
  Check,
  Copy,
  ExternalLink,
  ShieldCheck,
  Globe,
  ArrowUpCircle,
  Network
} from 'lucide-vue-next'
import ContainerSparklineGauge from './ContainerSparklineGauge.vue'
import { hasLocalWebLink, localAddress } from '../../../lib/containerPorts.mjs'
import { getDisplayPort } from '../../../lib/containers-table.mjs'

export interface ContainerRowItem {
  id: string
  name: string
  image: string
  status: string
  state: string
  ports: string
  command?: string
  running: boolean
  cpu?: string
  memory?: string
  network?: string
  uptime?: string
  project_id?: number
  project_name?: string
  project_path?: string
  exposure_mode?: string
  hostname?: string
  zero_trust_enabled?: boolean
  connected_to_cloudflared?: boolean
  update_available?: boolean
  update_checked_at?: string
  update_error?: string
  [key: string]: any
}

const props = defineProps<{
  container: ContainerRowItem
  isSelected: boolean
  isStackChild?: boolean
  isLastChild?: boolean
  actionLoading?: Record<string, string | boolean>
  checkingSingleUpdate?: Record<string, boolean>
  updatingContainer?: Record<string, boolean>
}>()

const emit = defineEmits<{
  (e: 'toggle-select', id: string, selected: boolean): void
  (e: 'action', container: ContainerRowItem, action: 'start' | 'stop' | 'restart'): void
  (e: 'open-inspector', container: ContainerRowItem, tab?: 'overview' | 'access' | 'compose' | 'env' | 'files' | 'logs' | 'exec' | 'inspect'): void
  (e: 'check-update', container: ContainerRowItem): void
  (e: 'confirm-update', container: ContainerRowItem): void
}>()

const copied = ref(false)
const showMenu = ref(false)

function copyId() {
  if (!props.container.id) return
  navigator.clipboard.writeText(props.container.id).then(() => {
    copied.value = true
    setTimeout(() => {
      copied.value = false
    }, 2000)
  })
}

function handleMenuAction(action: () => void) {
  showMenu.value = false
  action()
}
</script>

<template>
  <tr
    class="border-b border-slate-100 hover:bg-slate-50/80 transition-colors group text-xs text-slate-800"
    :class="{ 'bg-cyan-50/20': isSelected }"
  >
    <!-- 1. Select & Status -->
    <td class="py-2.5 px-3 whitespace-nowrap">
      <div class="flex items-center gap-2">
        <input
          type="checkbox"
          :checked="isSelected"
          @change="emit('toggle-select', container.id, ($event.target as HTMLInputElement).checked)"
          class="h-3.5 w-3.5 rounded border-slate-300 bg-slate-50 text-cyan-600 focus:ring-cyan-500 cursor-pointer"
          title="Select container"
        />

        <span
          class="relative flex h-2.5 w-2.5 shrink-0"
          :title="container.running ? `Running: ${container.status || 'Active'}` : `Stopped: ${container.status || 'Exited'}`"
        >
          <span
            v-if="container.running"
            class="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75"
          ></span>
          <span
            class="relative inline-flex h-2.5 w-2.5 rounded-full"
            :class="container.running ? 'bg-emerald-500' : 'bg-slate-300'"
          ></span>
        </span>
      </div>
    </td>

    <!-- 2. Name & Stack Badge -->
    <td class="py-2.5 px-3 max-w-[240px]">
      <div class="flex items-center gap-2">
        <!-- Tree guideline branch if stack child -->
        <span
          v-if="isStackChild"
          class="font-mono text-slate-400 select-none text-xs"
        >
          {{ isLastChild ? '└─' : '├─' }}
        </span>

        <div class="truncate">
          <div class="flex items-center gap-1.5">
            <button
              type="button"
              @click="emit('open-inspector', container, 'overview')"
              class="font-mono font-semibold text-slate-900 hover:text-cyan-600 truncate transition-colors text-left cursor-pointer"
              :title="`Inspect ${container.name}`"
            >
              {{ container.name }}
            </button>

            <!-- Truncated ID & Copy -->
            <button
              type="button"
              @click="copyId"
              class="opacity-0 group-hover:opacity-100 text-slate-400 hover:text-slate-600 transition-opacity p-0.5"
              :title="`Copy ID: ${container.id}`"
            >
              <Check v-if="copied" class="h-3 w-3 text-emerald-600" />
              <Copy v-else class="h-3 w-3" />
            </button>
          </div>

          <div class="flex items-center gap-1 text-[10px] text-slate-500 font-mono">
            <span>{{ (container.id || '').slice(0, 10) }}</span>
            <span v-if="!isStackChild && container.project_name" class="rounded bg-slate-100 px-1 text-slate-600">
              {{ container.project_name }}
            </span>
          </div>
        </div>
      </div>
    </td>

    <!-- 3. Image & Version -->
    <td class="py-2.5 px-3 max-w-[180px]">
      <div class="flex items-center gap-1.5 truncate">
        <span class="font-mono text-[11px] text-slate-600 truncate" :title="container.image">
          {{ container.image }}
        </span>

        <!-- Update Available Indicator -->
        <button
          v-if="container.update_available"
          type="button"
          @click="emit('confirm-update', container)"
          class="shrink-0 flex items-center gap-0.5 rounded-full bg-amber-50 border border-amber-300 px-1.5 py-0.2 text-[9px] font-bold text-amber-700 hover:bg-amber-100 transition-colors animate-pulse cursor-pointer"
          title="Update available! Click to recreate with new image"
        >
          <ArrowUpCircle class="h-2.5 w-2.5 text-amber-600" />
          <span>Update</span>
        </button>
      </div>
    </td>

    <!-- 4. Published Ports -->
    <td class="py-2.5 px-3 max-w-[160px]">
      <div class="flex items-center gap-1.5 flex-wrap">
        <template v-if="container.ports">
          <!-- Web link if HTTP web port published -->
          <a
            v-if="hasLocalWebLink(container.ports)"
            :href="localAddress(container)"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex items-center gap-1 rounded bg-cyan-50/80 border border-cyan-200 px-1.5 py-0.5 font-mono text-[10px] font-medium text-cyan-800 hover:bg-cyan-100 hover:text-cyan-900 transition-colors"
            title="Open local web service"
          >
            <span>{{ getDisplayPort(container.ports) }}</span>
            <ExternalLink class="h-2.5 w-2.5 text-cyan-600" />
          </a>
          <span
            v-else
            class="font-mono text-[10px] text-slate-600 truncate"
            :title="container.ports"
          >
            {{ container.ports }}
          </span>
        </template>
        <span v-else class="text-slate-400 font-mono text-[11px]">—</span>
      </div>
    </td>

    <!-- 5. Exposure & Tunnel -->
    <td class="py-2.5 px-3 max-w-[160px]">
      <div class="flex items-center gap-1.5 flex-wrap">
        <!-- Public Cloudflare Hostname -->
        <a
          v-if="container.exposure_mode === 'public' && container.hostname"
          :href="`https://${container.hostname}`"
          target="_blank"
          rel="noopener noreferrer"
          class="inline-flex items-center gap-1 rounded-full bg-emerald-50 border border-emerald-300 px-2 py-0.5 text-[10px] font-medium text-emerald-800 hover:bg-emerald-100 transition-colors truncate max-w-[150px]"
          :title="`Public tunnel: https://${container.hostname}`"
        >
          <Globe class="h-2.5 w-2.5 text-emerald-600 shrink-0" />
          <span class="truncate">{{ container.hostname }}</span>
        </a>

        <!-- Zero Trust Protected -->
        <button
          v-else-if="container.zero_trust_enabled"
          type="button"
          @click="emit('open-inspector', container, 'access')"
          class="inline-flex items-center gap-1 rounded-full bg-indigo-50 border border-indigo-200 px-2 py-0.5 text-[10px] font-medium text-indigo-700 hover:bg-indigo-100 transition-colors cursor-pointer"
          title="Zero Trust Access Policy active"
        >
          <ShieldCheck class="h-2.5 w-2.5 text-indigo-600" />
          <span>Zero Trust</span>
        </button>

        <!-- Connected to Cloudflare bridge -->
        <button
          v-else-if="container.connected_to_cloudflared"
          type="button"
          @click="emit('open-inspector', container, 'access')"
          class="inline-flex items-center gap-1 rounded-full bg-cyan-50 border border-cyan-200 px-2 py-0.5 text-[10px] font-medium text-cyan-700 hover:bg-cyan-100 transition-colors cursor-pointer"
          title="Connected to cloudflared network"
        >
          <Network class="h-2.5 w-2.5 text-cyan-600" />
          <span>Tunnel Net</span>
        </button>

        <!-- Internal / Unexposed -->
        <button
          v-else
          type="button"
          @click="emit('open-inspector', container, 'access')"
          class="text-[10px] text-slate-400 hover:text-slate-600 font-medium transition-colors cursor-pointer"
          title="Internal only. Click to configure ingress exposure"
        >
          Internal
        </button>
      </div>
    </td>

    <!-- 6. CPU & Memory Sparkline -->
    <td class="py-2.5 px-3">
      <ContainerSparklineGauge
        :cpu="container.cpu"
        :memory="container.memory"
        :running="container.running"
      />
    </td>

    <!-- 7. Uptime -->
    <td class="py-2.5 px-3 whitespace-nowrap text-slate-600 font-mono text-[11px]">
      {{ container.uptime || container.status || '—' }}
    </td>

    <!-- 8. Quick Actions & Menu -->
    <td class="py-2.5 px-3 text-right whitespace-nowrap">
      <div class="flex items-center justify-end gap-1">
        <!-- Start / Stop Button -->
        <button
          v-if="!container.running"
          type="button"
          @click="emit('action', container, 'start')"
          :disabled="!!actionLoading?.[container.id]"
          class="rounded p-1 text-emerald-600 hover:bg-emerald-50 hover:text-emerald-700 transition-colors cursor-pointer"
          title="Start container"
        >
          <Play class="h-3.5 w-3.5 fill-current" />
        </button>
        <button
          v-else
          type="button"
          @click="emit('action', container, 'stop')"
          :disabled="!!actionLoading?.[container.id]"
          class="rounded p-1 text-slate-400 hover:bg-amber-50 hover:text-amber-600 transition-colors cursor-pointer"
          title="Stop container"
        >
          <Square class="h-3.5 w-3.5 fill-current" />
        </button>

        <!-- Logs Shortcut -->
        <button
          type="button"
          @click="emit('open-inspector', container, 'logs')"
          class="rounded p-1 text-slate-400 hover:bg-cyan-50 hover:text-cyan-700 transition-colors cursor-pointer"
          title="View realtime logs"
        >
          <ScrollText class="h-3.5 w-3.5" />
        </button>

        <!-- Exec Terminal Shortcut (running only) -->
        <button
          v-if="container.running"
          type="button"
          @click="emit('open-inspector', container, 'exec')"
          class="rounded p-1 text-slate-400 hover:bg-cyan-50 hover:text-cyan-700 transition-colors cursor-pointer"
          title="Interactive Terminal"
        >
          <Terminal class="h-3.5 w-3.5" />
        </button>

        <!-- Overflow Dropdown Menu -->
        <div class="relative inline-block text-left">
          <button
            type="button"
            @click="showMenu = !showMenu"
            class="rounded p-1 text-slate-400 hover:bg-slate-100 hover:text-slate-700 transition-colors cursor-pointer"
            title="More container actions"
          >
            <MoreVertical class="h-3.5 w-3.5" />
          </button>

          <!-- Dropdown Popover -->
          <div
            v-if="showMenu"
            class="fixed inset-0 z-40"
            @click="showMenu = false"
          ></div>

          <div
            v-if="showMenu"
            class="absolute right-0 top-full mt-1 z-50 w-44 rounded-lg border border-slate-200 bg-white py-1 shadow-lg text-left"
          >
            <!-- Restart -->
            <button
              type="button"
              @click="handleMenuAction(() => emit('action', container, 'restart'))"
              class="w-full flex items-center gap-2 px-3 py-1.5 text-xs text-slate-700 hover:bg-slate-50 transition-colors cursor-pointer"
            >
              <RotateCcw class="h-3.5 w-3.5 text-slate-500" />
              <span>Restart</span>
            </button>

            <!-- Files -->
            <button
              type="button"
              @click="handleMenuAction(() => emit('open-inspector', container, 'files'))"
              class="w-full flex items-center gap-2 px-3 py-1.5 text-xs text-slate-700 hover:bg-slate-50 transition-colors cursor-pointer"
            >
              <FolderOpen class="h-3.5 w-3.5 text-slate-500" />
              <span>Browse Files</span>
            </button>

            <!-- Compose (if project) -->
            <button
              v-if="container.project_id"
              type="button"
              @click="handleMenuAction(() => emit('open-inspector', container, 'compose'))"
              class="w-full flex items-center gap-2 px-3 py-1.5 text-xs text-slate-700 hover:bg-slate-50 transition-colors cursor-pointer"
            >
              <FileCode class="h-3.5 w-3.5 text-slate-500" />
              <span>Edit Compose</span>
            </button>

            <!-- Check Updates -->
            <button
              type="button"
              @click="handleMenuAction(() => emit('check-update', container))"
              :disabled="!!checkingSingleUpdate?.[container.id]"
              class="w-full flex items-center gap-2 px-3 py-1.5 text-xs text-slate-700 hover:bg-slate-50 transition-colors cursor-pointer"
            >
              <RefreshCw
                class="h-3.5 w-3.5 text-slate-500"
                :class="{ 'animate-spin': checkingSingleUpdate?.[container.id] }"
              />
              <span>Check for Updates</span>
            </button>

            <!-- Inspect JSON -->
            <button
              type="button"
              @click="handleMenuAction(() => emit('open-inspector', container, 'inspect'))"
              class="w-full flex items-center gap-2 px-3 py-1.5 text-xs text-slate-700 hover:bg-slate-50 transition-colors cursor-pointer"
            >
              <Code2 class="h-3.5 w-3.5 text-slate-500" />
              <span>Inspect JSON</span>
            </button>
          </div>
        </div>
      </div>
    </td>
  </tr>
</template>
