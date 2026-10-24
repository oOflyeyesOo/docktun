<script setup lang="ts">
import { ref, reactive } from 'vue'
import { useRouter } from 'vue-router'
import { Lock, User, AlertCircle, ArrowRight, Loader2 } from 'lucide-vue-next'
import { useAuthStore } from '../stores/auth'
import { useApi } from '../composables/useApi'

const authStore = useAuthStore()
const router = useRouter()
const { apiFetch } = useApi()

const form = reactive({ username: '', password: '' })
const error = ref('')
const loading = ref(false)

async function handleLogin() {
  error.value = ''
  loading.value = true
  try {
    const data = await apiFetch<{ access_token: string }>('/auth/login', {
      method: 'POST',
      body: JSON.stringify(form),
    })
    authStore.setAuth(data.access_token, form.username)
    router.push('/')
  } catch (e: any) {
    error.value = e.message || 'Invalid username or password'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="min-h-screen flex items-center justify-center bg-slate-50 p-4 sm:p-6 relative overflow-hidden">
    <!-- Subtle Background Lighting -->
    <div class="absolute -top-40 -left-40 w-96 h-96 bg-indigo-500/10 rounded-full blur-3xl pointer-events-none"></div>
    <div class="absolute -bottom-40 -right-40 w-96 h-96 bg-cyan-500/10 rounded-full blur-3xl pointer-events-none"></div>

    <div class="w-full max-w-md relative z-10">
      <div class="rounded-2xl border border-slate-200/80 bg-white/70 p-7 sm:p-8 shadow-sm backdrop-blur-xl">
        <!-- Logo & Title -->
        <div class="text-center mb-8">
          <div class="inline-flex items-center justify-center p-3 rounded-2xl bg-indigo-600/10 border border-indigo-500/20 mb-4 shadow-sm">
            <img src="/icon.png" alt="DockTun" class="w-10 h-10 object-contain" />
          </div>
          <h1 class="text-3xl font-semibold tracking-tight text-slate-900">Welcome to DockTun</h1>
          <p class="text-xs text-slate-600 mt-1.5">Your infrastructure, in one calm workspace.</p>
        </div>

        <!-- Error Notification -->
        <div
          v-if="error"
          class="mb-6 p-3.5 rounded-xl border border-rose-500/30 bg-rose-500/10 text-rose-700 text-xs flex items-center gap-2.5 transition animate-shake"
        >
          <AlertCircle class="w-4 h-4 shrink-0 text-rose-600" />
          <span>{{ error }}</span>
        </div>

        <!-- Login Form -->
        <form @submit.prevent="handleLogin" class="space-y-4">
          <div class="space-y-1.5">
            <label for="login-username" class="block text-xs font-medium text-slate-700">Username</label>
            <div class="relative">
              <div class="absolute inset-y-0 left-0 pl-3.5 flex items-center pointer-events-none text-slate-500">
                <User class="w-4 h-4" />
              </div>
              <input
                id="login-username"
                v-model="form.username"
                type="text"
                autocomplete="username"
                required
                placeholder="Enter username"
                class="w-full pl-10 pr-3.5 py-2.5 bg-slate-50/80 border border-slate-200 rounded-xl text-sm text-slate-900 placeholder:text-slate-500 focus:outline-none focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 transition"
                autofocus
              />
            </div>
          </div>

          <div class="space-y-1.5">
            <label for="login-password" class="block text-xs font-medium text-slate-700">Password</label>
            <div class="relative">
              <div class="absolute inset-y-0 left-0 pl-3.5 flex items-center pointer-events-none text-slate-500">
                <Lock class="w-4 h-4" />
              </div>
              <input
                id="login-password"
                v-model="form.password"
                type="password"
                autocomplete="current-password"
                required
                placeholder="Enter password"
                class="w-full pl-10 pr-3.5 py-2.5 bg-slate-50/80 border border-slate-200 rounded-xl text-sm text-slate-900 placeholder:text-slate-500 focus:outline-none focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 transition"
              />
            </div>
          </div>

          <button
            type="submit"
            :disabled="loading"
            class="w-full mt-2 inline-flex items-center justify-center gap-2 rounded-xl bg-indigo-600 hover:bg-indigo-500 px-4 py-2.5 text-sm font-semibold text-white shadow-sm transition duration-150 focus:outline-none focus:ring-2 focus:ring-indigo-500/40 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            <Loader2 v-if="loading" class="w-4 h-4 animate-spin" />
            <span v-else>Sign In</span>
            <ArrowRight v-if="!loading" class="w-4 h-4" />
          </button>
        </form>

        <!-- Footer -->
        <div class="mt-8 pt-5 border-t border-slate-200/60 text-center">
          <p class="text-[11px] text-slate-500">
            Self-Hosted Infrastructure Management
          </p>
        </div>
      </div>
    </div>
  </div>
</template>
