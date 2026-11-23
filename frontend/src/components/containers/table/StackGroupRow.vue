<script setup lang="ts">
import { ref } from 'vue'
import {
  ChevronDown,
  ChevronRight,
  Layers,
  Server,
  Play,
  Square,
  RotateCcw,
  Code2,
  MoreVertical,
  Rocket,
  History,
  Trash2,
  ArrowUpCircle,
  ShieldCheck,
  Globe
} from 'lucide-vue-next'

export interface ContainerGroup {
  key: string
  name: string
  isStack: boolean
  projectId?: number
  projectPath?: string
  containers: any[]
  runningCount: number
  totalCount: number
  totalCpu: string
  hasUpdates: boolean
  hasPublic: boolean
  hasZeroTrust: boolean
}

const props = defineProps<{
  group: ContainerGroup
  isCollapsed: boolean
  isSelected: boolean
  isIndeterminate?: boolean
  stackActionLoading?: Record<number, boolean>
}>()

const emit = defineEmits<{
  (e: 'toggle-collapse', key: string): void
  (e: 'toggle-select-stack', group: ContainerGroup): void
  (e: 'stack-action', projectId: number, action: 'up' | 'down' | 'restart'): void
  (e: 'open-compose', group: ContainerGroup): void
  (e: 'deploy-stack', projectId: number, name: string): void
  (e: 'stack-history', projectId: number, name: string): void
  (e: 'delete-stack', projectId: number, name: string, path?: string): void
}>()

const showMenu = ref(false)

function handleMenuAction(action: () => void) {
  showMenu.value = false
  action()
}
</script>

<template>
  <tr class="bg-slate-50/90 border-b border-t border-slate-200/90 hover:bg-slate-100/70 transition-colors select-none">
    <td colspan="8" class="py-2 px-3">
      <div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-2.5">
        <!-- Left Section: Checkbox, Chevron, Title, Health Badge, Ingress -->
        <div class="flex items-center gap-2.5 flex-wrap">
          <!-- Stack multi-select checkbox -->
          <input
            type="checkbox"
            :checked="isSelected"
            :indeterminate.prop="isIndeterminate"
            :disabled="group.totalCount === 0"
            @change="emit('toggle-select-stack', group)"
            class="h-3.5 w-3.5 rounded border-slate-300 bg-white text-cyan-600 focus:ring-cyan-500"
            :class="group.totalCount === 0 ? 'opacity-30 cursor-not-allowed' : 'cursor-pointer'"
            :title="group.totalCount === 0 ? 'No containers in stack' : `Select all containers in stack ${group.name}`"
          />

          <!-- Collapse toggle button -->
          <button
            type="button"
            @click="emit('toggle-collapse', group.key)"
            class="flex items-center gap-1.5 group cursor-pointer text-left focus:outline-none"
            :title="isCollapsed ? 'Expand stack' : 'Collapse stack'"
          >
            <component
              :is="isCollapsed ? ChevronRight : ChevronDown"
              class="h-4 w-4 text-slate-400 group-hover:text-slate-600 transition-colors"
            />
            <component
              :is="group.isStack ? Layers : Server"
              class="h-4 w-4"
              :class="group.isStack ? 'text-indigo-600' : 'text-slate-500'"
            />
            <span class="font-mono font-bold text-xs text-slate-900 group-hover:text-cyan-800 transition-colors truncate max-w-[200px] sm:max-w-xs" :title="group.name">
              {{ group.name }}
            </span>
          </button>

          <!-- Health Count Badge -->
          <span
            class="rounded-full px-2 py-0.5 text-[10px] font-mono font-semibold"
            :class="group.totalCount === 0
              ? 'bg-slate-100 text-slate-600 border border-slate-200'
              : group.runningCount === group.totalCount && group.totalCount > 0
                ? 'bg-emerald-50 text-emerald-700 border border-emerald-200'
                : group.runningCount > 0
                  ? 'bg-amber-50 text-amber-700 border border-amber-200'
                  : 'bg-rose-50 text-rose-700 border border-rose-200'"
          >
            <template v-if="group.totalCount === 0">
              Down (0 containers)
            </template>
            <template v-else>
              {{ group.runningCount }}/{{ group.totalCount }} running
            </template>
          </span>

          <!-- Total CPU -->
          <span
            v-if="group.runningCount > 0 && group.totalCpu !== '0.0%'"
            class="rounded-full bg-slate-100 border border-slate-200 px-2 py-0.5 text-[10px] font-mono text-slate-700"
            title="Total CPU used across all stack containers"
          >
            CPU {{ group.totalCpu }}
          </span>

          <!-- Updates Available -->
          <span
            v-if="group.hasUpdates"
            class="rounded-full bg-amber-50 border border-amber-200 px-2 py-0.5 text-[10px] font-semibold text-amber-700 flex items-center gap-1 shadow-2xs"
          >
            <ArrowUpCircle class="h-2.5 w-2.5 text-amber-600" />
            <span>Updates</span>
          </span>

          <!-- Zero Trust / Public Badge -->
          <span
            v-if="group.hasZeroTrust"
            class="rounded-full bg-indigo-50 border border-indigo-200 px-2 py-0.5 text-[10px] font-semibold text-indigo-700 flex items-center gap-1 shadow-2xs"
            title="Zero Trust Protected Ingress"
          >
            <ShieldCheck class="h-2.5 w-2.5 text-indigo-600" />
            <span>Zero Trust</span>
          </span>
          <span
            v-else-if="group.hasPublic"
            class="rounded-full bg-cyan-50 border border-cyan-200 px-2 py-0.5 text-[10px] font-semibold text-cyan-700 flex items-center gap-1 shadow-2xs"
            title="Public Cloudflare Tunnel Ingress"
          >
            <Globe class="h-2.5 w-2.5 text-cyan-600" />
            <span>Public Ingress</span>
          </span>
        </div>

        <!-- Right Section: Compact Unified Stack Control Group -->
        <div v-if="group.projectId" class="flex items-center gap-1.5 self-end sm:self-center">
          <div class="inline-flex items-center rounded-lg border border-slate-200 bg-white shadow-2xs divide-x divide-slate-100 p-0.5">
            <!-- Stack Lifecycle Actions: Up, Down, Restart -->
            <div class="flex items-center">
              <!-- Up -->
              <button
                type="button"
                @click="emit('stack-action', group.projectId, 'up')"
                :disabled="!!stackActionLoading?.[group.projectId]"
                class="inline-flex items-center gap-1 rounded px-2 py-1 text-[11px] font-medium text-slate-700 hover:text-emerald-700 hover:bg-emerald-50 transition-colors disabled:opacity-50 cursor-pointer"
                title="Start stack (docker compose up -d)"
              >
                <Play class="h-3 w-3 fill-current text-emerald-600" />
                <span>Up</span>
              </button>

              <!-- Down -->
              <button
                type="button"
                @click="emit('stack-action', group.projectId, 'down')"
                :disabled="group.totalCount === 0 || !!stackActionLoading?.[group.projectId]"
                class="inline-flex items-center gap-1 rounded px-2 py-1 text-[11px] font-medium transition-colors"
                :class="group.totalCount === 0 ? 'text-slate-400 opacity-40 cursor-not-allowed' : 'text-slate-700 hover:text-amber-700 hover:bg-amber-50 cursor-pointer disabled:opacity-50'"
                :title="group.totalCount === 0 ? 'Stack is already down' : 'Stop stack (docker compose down)'"
              >
                <Square class="h-3 w-3 fill-current" :class="group.totalCount === 0 ? 'text-slate-300' : 'text-amber-500'" />
                <span>Down</span>
              </button>

              <!-- Restart -->
              <button
                type="button"
                @click="emit('stack-action', group.projectId, 'restart')"
                :disabled="group.totalCount === 0 || !!stackActionLoading?.[group.projectId]"
                class="inline-flex items-center gap-1 rounded px-2 py-1 text-[11px] font-medium transition-colors"
                :class="group.totalCount === 0 ? 'text-slate-400 opacity-40 cursor-not-allowed' : 'text-slate-700 hover:text-cyan-700 hover:bg-cyan-50 cursor-pointer disabled:opacity-50'"
                :title="group.totalCount === 0 ? 'Stack is not running' : 'Restart stack (docker compose restart)'"
              >
                <RotateCcw
                  class="h-3 w-3"
                  :class="[
                    group.totalCount === 0 ? 'text-slate-300' : 'text-cyan-600',
                    stackActionLoading?.[group.projectId] ? 'animate-spin' : ''
                  ]"
                />
                <span>Restart</span>
              </button>
            </div>

            <!-- Compose & Overflow Actions -->
            <div class="flex items-center pl-1">
              <!-- Compose Editor Shortcut -->
              <button
                type="button"
                @click="emit('open-compose', group)"
                class="inline-flex items-center gap-1 rounded px-2 py-1 text-[11px] font-medium text-slate-700 hover:text-indigo-700 hover:bg-indigo-50 transition-colors cursor-pointer"
                title="Edit docker-compose.yml"
              >
                <Code2 class="h-3 w-3 text-indigo-500" />
                <span class="hidden md:inline">Compose</span>
              </button>

              <!-- Stack Overflow Menu -->
              <div class="relative inline-block text-left">
                <button
                  type="button"
                  @click="showMenu = !showMenu"
                  class="rounded p-1 text-slate-400 hover:bg-slate-100 hover:text-slate-700 transition-colors cursor-pointer"
                  title="More stack actions"
                >
                  <MoreVertical class="h-3.5 w-3.5" />
                </button>

                <!-- Menu Popover Backdrop -->
                <div
                  v-if="showMenu"
                  class="fixed inset-0 z-40"
                  @click="showMenu = false"
                ></div>

                <!-- Menu Popover -->
                <div
                  v-if="showMenu"
                  class="absolute right-0 top-full mt-1 z-50 w-44 rounded-lg border border-slate-200 bg-white py-1 shadow-lg text-left"
                >
                  <!-- Deploy Branch/Tag -->
                  <button
                    type="button"
                    @click="handleMenuAction(() => emit('deploy-stack', group.projectId!, group.name))"
                    :disabled="!!stackActionLoading?.[group.projectId]"
                    class="w-full flex items-center gap-2 px-3 py-1.5 text-xs text-slate-700 hover:bg-slate-50 transition-colors cursor-pointer"
                  >
                    <Rocket class="h-3.5 w-3.5 text-indigo-600" />
                    <span>Deploy Ref…</span>
                  </button>

                  <!-- Audit History -->
                  <button
                    type="button"
                    @click="handleMenuAction(() => emit('stack-history', group.projectId!, group.name))"
                    class="w-full flex items-center gap-2 px-3 py-1.5 text-xs text-slate-700 hover:bg-slate-50 transition-colors cursor-pointer"
                  >
                    <History class="h-3.5 w-3.5 text-slate-500" />
                    <span>Action History</span>
                  </button>

                  <!-- Delete Stack -->
                  <button
                    v-if="group.name !== 'docktun'"
                    type="button"
                    @click="handleMenuAction(() => emit('delete-stack', group.projectId!, group.name, group.projectPath))"
                    :disabled="!!stackActionLoading?.[group.projectId]"
                    class="w-full flex items-center gap-2 px-3 py-1.5 text-xs text-red-600 hover:bg-red-50 transition-colors cursor-pointer"
                  >
                    <Trash2 class="h-3.5 w-3.5 text-red-500" />
                    <span>Delete Stack…</span>
                  </button>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </td>
  </tr>
</template>
