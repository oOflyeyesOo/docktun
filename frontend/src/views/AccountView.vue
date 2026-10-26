<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'
const router = useRouter()
const auth = useAuthStore()
const form = reactive({ setup_token: '', admin_username: 'admin', admin_password: '', confirm: '' })
const busy = ref(false)
const error = ref('')
onMounted(async () => {
  try { const r = await fetch('/api/setup/status'); const state = await r.json(); if (state.completed) router.replace('/login') }
  catch { error.value = 'Unable to reach DockTun. Refresh to try again.' }
})
async function createAccount() {
  error.value = ''
  if (form.admin_password !== form.confirm) { error.value = 'Passwords do not match.'; return }
  busy.value = true
  try {
    const response = await fetch('/api/setup/complete', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ setup_token: form.setup_token, admin_username: form.admin_username, admin_password: form.admin_password }) })
    const result = await response.json()
    if (!response.ok) throw new Error(result.error || 'Unable to create account.')
    auth.setAuth(result.access_token, result.username, result.refresh_token)
    router.replace('/')
  } catch (e: any) { error.value = e.message }
  finally { busy.value = false }
}
</script>

<template>
  <div class="min-h-screen flex items-center justify-center p-6">
    <form class="cyber-card w-full max-w-md space-y-5" @submit.prevent="createAccount">
      <h1 class="text-xl font-semibold">Create administrator account</h1>
      <p class="text-sm text-slate-600">Secure your Docker workspace. Exposure methods can be enabled independently whenever you need them.</p>
      <label class="block text-sm space-y-1"><span>Ownership code</span><input v-model="form.setup_token" type="password" required autocomplete="off" class="cyber-input w-full" /></label>
      <p class="text-xs text-slate-600">Read the code on your server: <code class="block mt-1 break-all">docker exec docktun cat /app/data/setup.token</code></p>
      <label class="block text-sm space-y-1"><span>Username</span><input v-model="form.admin_username" required maxlength="128" autocomplete="username" class="cyber-input w-full" /></label>
      <label class="block text-sm space-y-1"><span>Password (12–72 bytes)</span><input v-model="form.admin_password" type="password" required minlength="12" autocomplete="new-password" class="cyber-input w-full" /></label>
      <label class="block text-sm space-y-1"><span>Confirm password</span><input v-model="form.confirm" type="password" required autocomplete="new-password" class="cyber-input w-full" /></label>
      <p v-if="error" role="alert" class="text-sm text-rose-700">{{ error }}</p>
      <button class="cyber-btn w-full" :disabled="busy">{{ busy ? 'Creating account…' : 'Create account' }}</button>
      <router-link to="/login" class="block text-center text-sm text-indigo-700">Sign in</router-link>
    </form>
  </div>
</template>
