<script setup lang="ts">
import { computed, ref } from 'vue'
import {
  ArrowUpDown,
  ArrowUp,
  ArrowDown,
  ChevronsUpDown,
  ChevronsDownUp,
  Server,
  Play,
  Code2,
  Layers
} from 'lucide-vue-next'
import StackGroupRow, { type ContainerGroup } from './StackGroupRow.vue'
import ContainerTableRow, { type ContainerRowItem } from './ContainerTableRow.vue'
import {
  sortContainers,
  calculateStackSelection,
  calculateTableSelection,
  filterContainerGroups,
  formatTableSummary
} from '../../../lib/containers-table.mjs'

const props = defineProps<{
  groups: ContainerGroup[]
  flatContainers: ContainerRowItem[]
  groupByStack: boolean
  selectedContainerIds: Record<string, boolean>
  collapsedStacks: Record<string, boolean>
  isFilterActive?: boolean
  actionLoading?: Record<string, string | boolean>
  stackActionLoading?: Record<number, boolean>
  checkingSingleUpdate?: Record<string, boolean>
  updatingContainer?: Record<string, boolean>
}>()

const emit = defineEmits<{
  (e: 'toggle-select', id: string, selected: boolean): void
  (e: 'toggle-select-all', selected: boolean): void
  (e: 'toggle-select-stack', group: ContainerGroup): void
  (e: 'toggle-collapse', key: string): void
  (e: 'expand-all-stacks'): void
  (e: 'collapse-all-stacks'): void
  (e: 'action', container: ContainerRowItem, action: 'start' | 'stop' | 'restart'): void
  (e: 'stack-action', projectId: number, action: 'up' | 'down' | 'restart'): void
  (e: 'open-compose', group: ContainerGroup): void
  (e: 'deploy-stack', projectId: number, name: string): void
  (e: 'stack-history', projectId: number, name: string): void
  (e: 'delete-stack', projectId: number, name: string, path?: string): void
  (e: 'open-inspector', container: ContainerRowItem, tab?: 'overview' | 'access' | 'compose' | 'env' | 'files' | 'logs' | 'exec' | 'inspect'): void
  (e: 'check-update', container: ContainerRowItem): void
  (e: 'confirm-update', container: ContainerRowItem): void
}>()

export type SortField = 'name' | 'image' | 'ports' | 'exposure' | 'ingress' | 'cpu' | 'memory' | 'uptime'

function safeGetItem(key: string, fallback: string): string {
  try {
    return localStorage.getItem(key) || fallback
  } catch {
    return fallback
  }
}

const sortBy = ref<SortField>((safeGetItem('docktun:table_sort_by', 'name') as SortField))
const sortOrder = ref<'asc' | 'desc'>((safeGetItem('docktun:table_sort_order', 'asc') as 'asc' | 'desc'))

function toggleSort(field: SortField) {
  if (sortBy.value === field || (field === 'ingress' && sortBy.value === 'exposure') || (field === 'exposure' && sortBy.value === 'ingress')) {
    sortOrder.value = sortOrder.value === 'asc' ? 'desc' : 'asc'
  } else {
    sortBy.value = field
    sortOrder.value = 'asc'
  }
  try {
    localStorage.setItem('docktun:table_sort_by', sortBy.value)
    localStorage.setItem('docktun:table_sort_order', sortOrder.value)
  } catch (e) {
    console.warn('Failed to save table sort settings', e)
  }
}

function sortContainerList(list: ContainerRowItem[]): ContainerRowItem[] {
  return sortContainers(list, sortBy.value, sortOrder.value)
}

const sortedGroups = computed<ContainerGroup[]>(() => {
  return props.groups.map((group) => ({
    ...group,
    containers: sortContainerList(group.containers),
  }))
})

const displayGroups = computed<ContainerGroup[]>(() => {
  let list = sortedGroups.value
  if (props.isFilterActive) {
    list = filterContainerGroups(list, 'active_filter', '')
    list = list.filter((g) => g && g.containers && g.containers.length > 0)
  }
  return list
})

const sortedFlatContainers = computed<ContainerRowItem[]>(() => {
  return sortContainerList(props.flatContainers)
})

const tableSelection = computed(() => {
  return calculateTableSelection(props.flatContainers, props.selectedContainerIds)
})

const isAllSelected = computed(() => tableSelection.value.isAllSelected)
const isIndeterminate = computed(() => tableSelection.value.isIndeterminate)
const totalContainersCount = computed(() => tableSelection.value.totalCount)
const selectedCount = computed(() => tableSelection.value.selectedCount)

const allStacksCollapsed = computed(() => {
  const stackGroups = displayGroups.value.filter((g) => g.isStack)
  return stackGroups.length > 0 && stackGroups.every((g) => props.collapsedStacks[g.key])
})

const summaryText = computed(() => {
  return formatTableSummary(displayGroups.value.length, totalContainersCount.value)
})
</script>

<template>
  <div class="rounded-xl border border-slate-200 bg-white shadow-sm overflow-hidden">
    <!-- Sub-header bar for quick expand/collapse and count -->
    <div
      v-if="groupByStack && displayGroups.some((g) => g.isStack)"
      class="flex items-center justify-between border-b border-slate-200 bg-slate-50/80 px-4 py-2 text-xs text-slate-600"
    >
      <div class="flex items-center gap-2">
        <span class="font-semibold text-slate-700">
          {{ summaryText }}
        </span>
      </div>

      <div class="flex items-center gap-2">
        <button
          v-if="!allStacksCollapsed"
          type="button"
          @click="emit('collapse-all-stacks')"
          class="inline-flex items-center gap-1 rounded px-2 py-0.5 text-[11px] font-medium text-slate-600 hover:text-slate-900 hover:bg-slate-200/60 transition-colors cursor-pointer"
        >
          <ChevronsDownUp class="h-3.5 w-3.5 text-slate-500" />
          <span>Collapse All</span>
        </button>
        <button
          v-else
          type="button"
          @click="emit('expand-all-stacks')"
          class="inline-flex items-center gap-1 rounded px-2 py-0.5 text-[11px] font-medium text-slate-600 hover:text-slate-900 hover:bg-slate-200/60 transition-colors cursor-pointer"
        >
          <ChevronsUpDown class="h-3.5 w-3.5 text-slate-500" />
          <span>Expand All</span>
        </button>
      </div>
    </div>

    <!-- Table Container -->
    <div class="overflow-x-auto">
      <table class="w-full text-left border-collapse text-xs">
        <!-- Sticky Header -->
        <thead class="sticky top-0 z-10 border-b border-slate-200 bg-slate-100 text-slate-700 font-semibold select-none shadow-2xs">
          <tr>
            <!-- Col 1: Select & Status -->
            <th class="py-2.5 px-3 w-12 whitespace-nowrap">
              <input
                type="checkbox"
                :checked="isAllSelected"
                :indeterminate.prop="isIndeterminate"
                @change="emit('toggle-select-all', ($event.target as HTMLInputElement).checked)"
                class="h-3.5 w-3.5 rounded border-slate-300 bg-white text-cyan-600 focus:ring-cyan-500 cursor-pointer"
                title="Select all containers"
              />
            </th>

            <!-- Col 2: Name & Stack -->
            <th class="py-2.5 px-3 min-w-[200px] max-w-[260px]">
              <button
                type="button"
                @click="toggleSort('name')"
                class="flex items-center gap-1 hover:text-cyan-800 transition-colors cursor-pointer text-left font-semibold"
              >
                <span>Name & Service</span>
                <ArrowUp v-if="sortBy === 'name' && sortOrder === 'asc'" class="h-3 w-3 text-cyan-600" />
                <ArrowDown v-else-if="sortBy === 'name' && sortOrder === 'desc'" class="h-3 w-3 text-cyan-600" />
                <ArrowUpDown v-else class="h-3 w-3 text-slate-400 opacity-60" />
              </button>
            </th>

            <!-- Col 3: Image & Version -->
            <th class="py-2.5 px-3 min-w-[160px] max-w-[200px]">
              <button
                type="button"
                @click="toggleSort('image')"
                class="flex items-center gap-1 hover:text-cyan-800 transition-colors cursor-pointer text-left font-semibold"
              >
                <span>Image & Version</span>
                <ArrowUp v-if="sortBy === 'image' && sortOrder === 'asc'" class="h-3 w-3 text-cyan-600" />
                <ArrowDown v-else-if="sortBy === 'image' && sortOrder === 'desc'" class="h-3 w-3 text-cyan-600" />
                <ArrowUpDown v-else class="h-3 w-3 text-slate-400 opacity-60" />
              </button>
            </th>

            <!-- Col 4: Published Ports -->
            <th class="py-2.5 px-3 min-w-[140px] max-w-[180px]">
              <button
                type="button"
                @click="toggleSort('ports')"
                class="flex items-center gap-1 hover:text-cyan-800 transition-colors cursor-pointer text-left font-semibold"
              >
                <span>Published Ports</span>
                <ArrowUp v-if="sortBy === 'ports' && sortOrder === 'asc'" class="h-3 w-3 text-cyan-600" />
                <ArrowDown v-else-if="sortBy === 'ports' && sortOrder === 'desc'" class="h-3 w-3 text-cyan-600" />
                <ArrowUpDown v-else class="h-3 w-3 text-slate-400 opacity-60" />
              </button>
            </th>

            <!-- Col 5: Exposure & Tunnel -->
            <th class="py-2.5 px-3 min-w-[150px] max-w-[190px]">
              <button
                type="button"
                @click="toggleSort('ingress')"
                class="flex items-center gap-1 hover:text-cyan-800 transition-colors cursor-pointer text-left font-semibold"
              >
                <span>Ingress & Exposure</span>
                <ArrowUp v-if="(sortBy === 'exposure' || sortBy === 'ingress') && sortOrder === 'asc'" class="h-3 w-3 text-cyan-600" />
                <ArrowDown v-else-if="(sortBy === 'exposure' || sortBy === 'ingress') && sortOrder === 'desc'" class="h-3 w-3 text-cyan-600" />
                <ArrowUpDown v-else class="h-3 w-3 text-slate-400 opacity-60" />
              </button>
            </th>

            <!-- Col 6: CPU & Memory -->
            <th class="py-2.5 px-3 w-48">
              <button
                type="button"
                @click="toggleSort('cpu')"
                class="flex items-center gap-1 hover:text-cyan-800 transition-colors cursor-pointer text-left font-semibold"
              >
                <span>CPU / RAM</span>
                <ArrowUp v-if="(sortBy === 'cpu' || sortBy === 'memory') && sortOrder === 'asc'" class="h-3 w-3 text-cyan-600" />
                <ArrowDown v-else-if="(sortBy === 'cpu' || sortBy === 'memory') && sortOrder === 'desc'" class="h-3 w-3 text-cyan-600" />
                <ArrowUpDown v-else class="h-3 w-3 text-slate-400 opacity-60" />
              </button>
            </th>

            <!-- Col 7: Uptime -->
            <th class="py-2.5 px-3 w-28 whitespace-nowrap">
              <button
                type="button"
                @click="toggleSort('uptime')"
                class="flex items-center gap-1 hover:text-cyan-800 transition-colors cursor-pointer text-left font-semibold"
              >
                <span>Uptime</span>
                <ArrowUp v-if="sortBy === 'uptime' && sortOrder === 'asc'" class="h-3 w-3 text-cyan-600" />
                <ArrowDown v-else-if="sortBy === 'uptime' && sortOrder === 'desc'" class="h-3 w-3 text-cyan-600" />
                <ArrowUpDown v-else class="h-3 w-3 text-slate-400 opacity-60" />
              </button>
            </th>

            <!-- Col 8: Quick Actions -->
            <th class="py-2.5 px-3 w-28 text-right font-semibold">
              <span>Actions</span>
            </th>
          </tr>
        </thead>

        <!-- Table Body -->
        <tbody class="divide-y divide-slate-100">
          <!-- Mode A: Grouped by Compose Stacks -->
          <template v-if="groupByStack">
            <template v-for="group in displayGroups" :key="group.key">
              <!-- Stack Group Row (rendered for each stack) -->
              <StackGroupRow
                :group="group"
                :is-collapsed="Boolean(collapsedStacks[group.key])"
                :is-selected="calculateStackSelection(group.containers, selectedContainerIds).isSelected"
                :is-indeterminate="calculateStackSelection(group.containers, selectedContainerIds).isIndeterminate"
                :stack-action-loading="stackActionLoading"
                @toggle-collapse="emit('toggle-collapse', $event)"
                @toggle-select-stack="emit('toggle-select-stack', $event)"
                @stack-action="(pId, action) => emit('stack-action', pId, action)"
                @open-compose="emit('open-compose', $event)"
                @deploy-stack="(pId, name) => emit('deploy-stack', pId, name)"
                @stack-history="(pId, name) => emit('stack-history', pId, name)"
                @delete-stack="(pId, name, path) => emit('delete-stack', pId, name, path)"
              />

              <!-- Child Containers of the Stack (if expanded) -->
              <template v-if="!collapsedStacks[group.key]">
                <!-- Empty Stack Fallback Row -->
                <tr v-if="group.containers.length === 0" class="bg-slate-50/50 border-b border-slate-100">
                  <td colspan="8" class="py-4 px-6 text-center">
                    <div class="flex flex-col items-center justify-center gap-1.5 text-xs text-slate-500">
                      <div class="flex items-center gap-1.5 text-slate-600 font-medium">
                        <Layers class="h-3.5 w-3.5 text-slate-400" />
                        <span>No active containers in this stack.</span>
                      </div>
                      <p class="text-[11px] text-slate-400">The stack is down. Click "Up" to start containers.</p>
                      <div class="flex items-center gap-2 mt-1">
                        <button
                          v-if="group.projectId"
                          type="button"
                          @click="emit('stack-action', group.projectId, 'up')"
                          :disabled="!!stackActionLoading?.[group.projectId]"
                          class="inline-flex items-center gap-1 rounded bg-emerald-50 border border-emerald-300 px-2.5 py-1 text-xs font-semibold text-emerald-700 hover:bg-emerald-100 transition-colors cursor-pointer"
                          title="Start stack (docker compose up -d)"
                        >
                          <Play class="h-3 w-3 fill-current" />
                          <span>Start Stack</span>
                        </button>
                        <button
                          v-if="group.projectId"
                          type="button"
                          @click="emit('open-compose', group)"
                          class="inline-flex items-center gap-1 rounded bg-white border border-slate-200 px-2.5 py-1 text-xs font-medium text-slate-600 hover:bg-slate-50 transition-colors cursor-pointer"
                          title="Edit docker-compose.yml"
                        >
                          <Code2 class="h-3 w-3 text-indigo-500" />
                          <span>Compose</span>
                        </button>
                      </div>
                    </div>
                  </td>
                </tr>

                <ContainerTableRow
                  v-else
                  v-for="(container, idx) in group.containers"
                  :key="container.id"
                  :container="container"
                  :is-selected="Boolean(selectedContainerIds[container.id])"
                  :is-stack-child="group.isStack"
                  :is-last-child="idx === group.containers.length - 1"
                  :action-loading="actionLoading"
                  :checking-single-update="checkingSingleUpdate"
                  :updating-container="updatingContainer"
                  @toggle-select="(id, sel) => emit('toggle-select', id, sel)"
                  @action="(c, a) => emit('action', c, a)"
                  @open-inspector="(c, tab) => emit('open-inspector', c, tab)"
                  @check-update="(c) => emit('check-update', c)"
                  @confirm-update="(c) => emit('confirm-update', c)"
                />
              </template>
            </template>
          </template>

          <!-- Mode B: Flat List of Containers -->
          <template v-else>
            <ContainerTableRow
              v-for="container in sortedFlatContainers"
              :key="container.id"
              :container="container"
              :is-selected="Boolean(selectedContainerIds[container.id])"
              :is-stack-child="false"
              :action-loading="actionLoading"
              :checking-single-update="checkingSingleUpdate"
              :updating-container="updatingContainer"
              @toggle-select="(id, sel) => emit('toggle-select', id, sel)"
              @action="(c, a) => emit('action', c, a)"
              @open-inspector="(c, tab) => emit('open-inspector', c, tab)"
              @check-update="(c) => emit('check-update', c)"
              @confirm-update="(c) => emit('confirm-update', c)"
            />
          </template>

          <!-- Empty state when no rows -->
          <tr v-if="(groupByStack && displayGroups.length === 0) || (!groupByStack && flatContainers.length === 0)">
            <td colspan="8" class="py-12 text-center text-slate-500">
              <Server class="mx-auto h-8 w-8 text-slate-400 mb-2" />
              <p class="text-sm font-medium text-slate-700">No containers match the current filter.</p>
              <p class="mt-1 text-xs text-slate-500">Try adjusting your search terms or status filters.</p>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
