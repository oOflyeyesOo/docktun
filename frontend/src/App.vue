<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { Menu, X } from 'lucide-vue-next'
import AppSidebar from './components/AppSidebar.vue'

const route = useRoute()
const showSidebar = computed(() => route.path !== '/login')
const mobileOpen = ref(false)

onMounted(() => {
  mobileOpen.value = false
})
</script>

<template>
  <div class="flex h-screen bg-slate-950 text-slate-100">
    <div v-if="showSidebar" class="fixed inset-x-0 top-0 z-40 flex items-center justify-between border-b border-slate-800 bg-slate-950/95 px-4 py-3 backdrop-blur lg:hidden">
      <div class="flex items-center gap-2">
        <img src="/icon.png" alt="Tunlify" class="h-5 w-5" />
        <span class="text-sm font-medium tracking-[0.14em] text-slate-200">TUNLIFY</span>
      </div>
      <button @click="mobileOpen = !mobileOpen" class="text-slate-200">
        <Menu v-if="!mobileOpen" class="h-5 w-5" />
        <X v-else class="h-5 w-5" />
      </button>
    </div>

    <div v-if="showSidebar && mobileOpen" class="fixed inset-0 z-30 bg-slate-950/70 lg:hidden" @click="mobileOpen = false"></div>

    <div v-if="showSidebar" :class="mobileOpen ? 'translate-x-0' : '-translate-x-full'" class="fixed inset-y-0 left-0 z-30 transition-transform duration-200 lg:translate-x-0 lg:static">
      <AppSidebar @navigate="mobileOpen = false" />
    </div>

    <main class="flex-1 overflow-auto p-4 pt-16 lg:p-6 lg:pt-6">
      <router-view />
    </main>

    <div class="scanlines"></div>
  </div>
</template>
