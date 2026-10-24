<script setup lang="ts">
import { ref, computed, onMounted, watch, nextTick } from 'vue'
import { useRoute } from 'vue-router'
import { Menu, X } from 'lucide-vue-next'
import AppSidebar from './components/AppSidebar.vue'

const route = useRoute()
const showSidebar = computed(() => !['/login', '/setup'].includes(route.path))
const mobileOpen = ref(false)
const mainElement = ref<HTMLElement | null>(null)
watch(() => route.fullPath, async () => {
  mobileOpen.value = false
  await nextTick()
  mainElement.value?.scrollTo({ top: 0 })
})

onMounted(() => {
  mobileOpen.value = false
})
</script>

<template>
  <div class="flex h-screen bg-slate-50 text-slate-900">
    <div v-if="showSidebar" class="fixed inset-x-0 top-0 z-40 flex items-center justify-between border-b border-slate-200 bg-slate-50/95 px-4 py-3 backdrop-blur lg:hidden">
      <div class="flex items-center gap-2">
        <img src="/icon.png" alt="DockTun" class="h-5 w-5" />
        <span class="text-sm font-medium tracking-[0.14em] text-slate-800">DOCKTUN</span>
      </div>
      <button aria-label="Toggle navigation" :aria-expanded="mobileOpen" @click="mobileOpen = !mobileOpen" class="text-slate-800">
        <Menu v-if="!mobileOpen" class="h-5 w-5" />
        <X v-else class="h-5 w-5" />
      </button>
    </div>

    <div v-if="showSidebar && mobileOpen" class="fixed inset-0 z-30 bg-slate-50/70 lg:hidden" @click="mobileOpen = false"></div>

    <div v-if="showSidebar" :class="mobileOpen ? 'translate-x-0' : '-translate-x-full'" class="fixed inset-y-0 top-12 lg:top-0 left-0 z-30 transition-transform duration-200 lg:translate-x-0 lg:static">
      <AppSidebar @navigate="mobileOpen = false" />
    </div>

    <main ref="mainElement" class="workspace-main min-w-0 flex-1 overflow-auto" :class="showSidebar ? 'p-4 pt-20 lg:p-8' : 'p-0'">
      <router-view />
    </main>
  </div>
</template>
