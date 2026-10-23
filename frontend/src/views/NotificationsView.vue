<script setup lang="ts">
import { ref, reactive, computed } from 'vue'
import {
  Bell,
  Check,
  Send,
  Save,
  AlertTriangle,
  RotateCcw,
  CheckCircle2,
  Globe,
  Server,
  Rocket,
  Cpu,
  Layers,
  ShieldCheck,
  Radio,
  ExternalLink,
  Info
} from 'lucide-vue-next'
import { useApi } from '../composables/useApi'

const { apiFetch } = useApi()

interface NotificationConfig {
  enabled: boolean
  type: 'discord' | 'telegram' | 'slack' | 'ntfy' | 'webhook'
  webhook_url: string
  telegram_bot_token?: string
  telegram_chat_id?: string
  notify_tunnel_down: boolean
  notify_container_stop: boolean
  notify_deploy_finish: boolean
  notify_high_usage: boolean
  cpu_threshold_percent: number
  memory_threshold_percent: number
  last_tested_at?: string
  last_status?: string
}

const form = reactive<NotificationConfig>({
  enabled: false,
  type: 'discord',
  webhook_url: '',
  telegram_bot_token: '',
  telegram_chat_id: '',
  notify_tunnel_down: true,
  notify_container_stop: true,
  notify_deploy_finish: true,
  notify_high_usage: true,
  cpu_threshold_percent: 85,
  memory_threshold_percent: 85,
  last_tested_at: '',
  last_status: '',
})

const loading = ref(false)
const saving = ref(false)
const testing = ref(false)
const message = ref<{ type: 'success' | 'error'; text: string } | null>(null)

const channelOptions = [
  {
    id: 'discord',
    name: 'Discord',
    desc: 'Rich embed webhook alerts to a Discord channel',
    color: 'hover:border-indigo-500 hover:bg-indigo-950/20',
    selectedBorder: 'border-indigo-500 bg-indigo-950/30 text-indigo-300',
    placeholder: 'https://discord.com/api/webhooks/...',
  },
  {
    id: 'telegram',
    name: 'Telegram',
    desc: 'Instant direct messages via Telegram Bot API',
    color: 'hover:border-sky-500 hover:bg-sky-950/20',
    selectedBorder: 'border-sky-500 bg-sky-950/30 text-sky-300',
    placeholder: 'Bot Token & Chat ID',
  },
  {
    id: 'slack',
    name: 'Slack',
    desc: 'Incoming Webhook messages to your Slack channel',
    color: 'hover:border-emerald-500 hover:bg-emerald-950/20',
    selectedBorder: 'border-emerald-500 bg-emerald-950/30 text-emerald-300',
    placeholder: 'https://hooks.slack.com/services/...',
  },
  {
    id: 'ntfy',
    name: 'Ntfy.sh',
    desc: 'Self-hosted or cloud push notifications to mobile/desktop',
    color: 'hover:border-amber-500 hover:bg-amber-950/20',
    selectedBorder: 'border-amber-500 bg-amber-950/30 text-amber-300',
    placeholder: 'https://ntfy.sh/my_docktun_alerts',
  },
  {
    id: 'webhook',
    name: 'Generic Webhook',
    desc: 'Custom HTTP POST JSON payload to any server or API',
    color: 'hover:border-cyan-500 hover:bg-cyan-950/20',
    selectedBorder: 'border-cyan-500 bg-cyan-950/30 text-cyan-300',
    placeholder: 'https://api.yourdomain.com/webhook',
  },
]

async function load() {
  loading.value = true
  try {
    const data = await apiFetch<NotificationConfig>('/api/notifications')
    if (data) {
      Object.assign(form, data)
    }
  } catch (err: any) {
    console.error('Failed to load notifications config:', err)
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  message.value = null
  try {
    await apiFetch('/api/notifications', {
      method: 'PUT',
      body: JSON.stringify(form),
    })
    message.value = { type: 'success', text: 'Alert configuration saved successfully.' }
    setTimeout(() => {
      if (message.value?.type === 'success') message.value = null
    }, 4000)
  } catch (e: any) {
    message.value = { type: 'error', text: e.message || 'Failed to save configuration.' }
  } finally {
    saving.value = false
  }
}

async function testAlert() {
  testing.value = true
  message.value = null
  try {
    // Save first to make sure test uses current input values
    await apiFetch('/api/notifications', {
      method: 'PUT',
      body: JSON.stringify(form),
    })
    const res: any = await apiFetch('/api/notifications/test', { method: 'POST' })
    form.last_tested_at = res.timestamp || new Date().toISOString()
    form.last_status = res.status_msg || 'Sent successfully'
    message.value = {
      type: 'success',
      text: `Test alert dispatched successfully to ${form.type.toUpperCase()}!`,
    }
  } catch (e: any) {
    message.value = {
      type: 'error',
      text: e.message || 'Webhook test delivery failed. Please verify your URL or tokens.',
    }
    form.last_status = 'Delivery Failed: ' + (e.message || 'Unknown error')
  } finally {
    testing.value = false
  }
}

load()
</script>

<template>
  <div class="space-y-6 max-w-4xl">
    <!-- Header -->
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between border-b border-slate-800 pb-4">
      <div>
        <div class="flex items-center gap-2 text-[10px] font-semibold uppercase tracking-[0.22em] text-slate-400">
          <Bell class="h-3.5 w-3.5 text-cyan-400" />
          <span>Incident & Health Alerts</span>
        </div>
        <h1 class="text-2xl font-bold tracking-tight text-slate-100 flex items-center gap-2">
          <span>Alerts & Notifications</span>
        </h1>
        <p class="text-xs text-slate-400">
          Receive realtime alerts for tunnel outages, container crashes, deploy events, and host resource spikes.
        </p>
      </div>

      <!-- Master Toggle & Status Badge -->
      <div class="flex items-center gap-3">
        <span
          class="rounded-full px-2.5 py-1 text-[10px] font-mono font-bold uppercase tracking-wider"
          :class="form.enabled ? 'bg-emerald-950/80 text-emerald-400 border border-emerald-800/60' : 'bg-slate-800 text-slate-400 border border-slate-700'"
        >
          {{ form.enabled ? 'ALERTS ACTIVE' : 'ALERTS DISABLED' }}
        </span>

        <label class="relative inline-flex items-center cursor-pointer">
          <input
            type="checkbox"
            v-model="form.enabled"
            class="sr-only peer"
          />
          <div
            class="w-11 h-6 bg-slate-800 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-slate-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-emerald-500"
          ></div>
        </label>
      </div>
    </div>

    <!-- Feedback Message Banner -->
    <div
      v-if="message"
      class="border p-3 rounded-lg text-xs font-mono flex items-center justify-between"
      :class="message.type === 'success' ? 'border-emerald-500/50 bg-emerald-950/20 text-emerald-300' : 'border-rose-500/50 bg-rose-950/20 text-rose-300'"
    >
      <div class="flex items-center gap-2">
        <CheckCircle2 v-if="message.type === 'success'" class="h-4 w-4 text-emerald-400" />
        <AlertTriangle v-else class="h-4 w-4 text-rose-400" />
        <span>{{ message.text }}</span>
      </div>
      <button @click="message = null" class="text-slate-400 hover:text-slate-200">✕</button>
    </div>

    <!-- 1. Notification Channel Selection -->
    <div class="cyber-card space-y-4">
      <div class="border-b border-slate-800 pb-2">
        <h2 class="text-xs font-bold uppercase tracking-wider text-slate-300 flex items-center gap-2">
          <Radio class="h-4 w-4 text-cyan-400" />
          <span>1. Choose Notification Channel</span>
        </h2>
        <p class="text-[11px] text-slate-400">Select where alerts and test pings will be dispatched.</p>
      </div>

      <div class="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 gap-3">
        <button
          v-for="c in channelOptions"
          :key="c.id"
          type="button"
          @click="form.type = c.id as any"
          class="flex flex-col text-left p-3 rounded-xl border transition-all cursor-pointer relative"
          :class="[
            form.type === c.id ? c.selectedBorder : 'border-slate-800 bg-slate-900/50 text-slate-400 ' + c.color
          ]"
        >
          <div class="flex items-center justify-between">
            <span class="text-xs font-bold font-mono">{{ c.name }}</span>
            <span
              v-if="form.type === c.id"
              class="h-2 w-2 rounded-full bg-cyan-400"
            ></span>
          </div>
          <span class="text-[11px] text-slate-400 mt-1">{{ c.desc }}</span>
        </button>
      </div>
    </div>

    <!-- 2. Channel Credentials & Endpoint -->
    <div class="cyber-card space-y-4">
      <div class="border-b border-slate-800 pb-2">
        <h2 class="text-xs font-bold uppercase tracking-wider text-slate-300 flex items-center gap-2">
          <Globe class="h-4 w-4 text-cyan-400" />
          <span>2. Channel Endpoint & Credentials</span>
        </h2>
        <p class="text-[11px] text-slate-400">Provide the destination URL or bot credentials for {{ form.type.toUpperCase() }}.</p>
      </div>

      <!-- Telegram Specific Fields -->
      <template v-if="form.type === 'telegram'">
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div class="space-y-1.5">
            <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-400">
              Telegram Bot Token
            </label>
            <input
              v-model="form.telegram_bot_token"
              type="text"
              placeholder="123456789:ABCdefGhIJKlmNoPQRsTUVwxyZ"
              class="cyber-input text-xs font-mono"
            />
            <p class="text-[10px] text-slate-500">Obtained from @BotFather on Telegram.</p>
          </div>

          <div class="space-y-1.5">
            <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-400">
              Telegram Chat ID
            </label>
            <input
              v-model="form.telegram_chat_id"
              type="text"
              placeholder="-100123456789 or 987654321"
              class="cyber-input text-xs font-mono"
            />
            <p class="text-[10px] text-slate-500">Your user or group chat ID.</p>
          </div>
        </div>
      </template>

      <!-- Webhook URL Field (Discord, Slack, Ntfy, Generic Webhook) -->
      <div v-else class="space-y-1.5">
        <div class="flex items-center justify-between">
          <label class="block text-[10px] font-semibold uppercase tracking-wider text-slate-400">
            {{ form.type === 'ntfy' ? 'Ntfy Topic URL' : 'Webhook URL' }}
          </label>
          <span class="text-[10px] text-slate-500 font-mono">
            {{ form.type === 'ntfy' ? 'https://ntfy.sh/<topic>' : 'HTTP POST Endpoint' }}
          </span>
        </div>
        <input
          v-model="form.webhook_url"
          type="text"
          :placeholder="
            form.type === 'discord'
              ? 'https://discord.com/api/webhooks/...'
              : form.type === 'slack'
              ? 'https://hooks.slack.com/services/...'
              : form.type === 'ntfy'
              ? 'https://ntfy.sh/my_docktun_alerts'
              : 'https://api.yourdomain.com/webhook'
          "
          class="cyber-input text-xs font-mono"
        />
      </div>

      <!-- Test & Diagnostic Status Info -->
      <div v-if="form.last_tested_at" class="rounded-lg bg-slate-950 border border-slate-800 p-3 flex flex-col sm:flex-row sm:items-center justify-between gap-2 text-xs font-mono">
        <div class="flex items-center gap-2">
          <Info class="h-3.5 w-3.5 text-slate-400" />
          <span class="text-slate-400">Last Status:</span>
          <span :class="form.last_status?.includes('Error') || form.last_status?.includes('Failed') ? 'text-rose-400' : 'text-emerald-400 font-bold'">
            {{ form.last_status || 'Unknown' }}
          </span>
        </div>
        <div class="text-[10px] text-slate-500">
          Last tested: {{ form.last_tested_at }}
        </div>
      </div>
    </div>

    <!-- 3. Alert Event Subscriptions -->
    <div class="cyber-card space-y-4">
      <div class="border-b border-slate-800 pb-2">
        <h2 class="text-xs font-bold uppercase tracking-wider text-slate-300 flex items-center gap-2">
          <ShieldCheck class="h-4 w-4 text-cyan-400" />
          <span>3. Event Subscriptions & Trigger Rules</span>
        </h2>
        <p class="text-[11px] text-slate-400">Select which occurrences should generate an instant notification.</p>
      </div>

      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <!-- Tunnel Outage -->
        <label class="flex items-start gap-3 p-3 rounded-lg border border-slate-800 bg-slate-900/40 hover:border-slate-700 transition cursor-pointer">
          <input
            type="checkbox"
            v-model="form.notify_tunnel_down"
            class="h-4 w-4 mt-0.5 rounded border-slate-700 bg-slate-950 text-cyan-500 focus:ring-cyan-500 cursor-pointer"
          />
          <div class="space-y-0.5">
            <span class="text-xs font-bold text-slate-200 flex items-center gap-1.5">
              <Globe class="h-3.5 w-3.5 text-cyan-400" />
              <span>Cloudflare Tunnel Outage</span>
            </span>
            <p class="text-[11px] text-slate-400">
              Triggers immediately if the cloudflared connector disconnects or enters an error state.
            </p>
          </div>
        </label>

        <!-- Container Stopped / Crashed -->
        <label class="flex items-start gap-3 p-3 rounded-lg border border-slate-800 bg-slate-900/40 hover:border-slate-700 transition cursor-pointer">
          <input
            type="checkbox"
            v-model="form.notify_container_stop"
            class="h-4 w-4 mt-0.5 rounded border-slate-700 bg-slate-950 text-cyan-500 focus:ring-cyan-500 cursor-pointer"
          />
          <div class="space-y-0.5">
            <span class="text-xs font-bold text-slate-200 flex items-center gap-1.5">
              <Server class="h-3.5 w-3.5 text-rose-400" />
              <span>Container Crashes & Exits</span>
            </span>
            <p class="text-[11px] text-slate-400">
              Triggers when any container stops unexpectedly or fails health checks.
            </p>
          </div>
        </label>

        <!-- Compose Deployment Finished -->
        <label class="flex items-start gap-3 p-3 rounded-lg border border-slate-800 bg-slate-900/40 hover:border-slate-700 transition cursor-pointer">
          <input
            type="checkbox"
            v-model="form.notify_deploy_finish"
            class="h-4 w-4 mt-0.5 rounded border-slate-700 bg-slate-950 text-cyan-500 focus:ring-cyan-500 cursor-pointer"
          />
          <div class="space-y-0.5">
            <span class="text-xs font-bold text-slate-200 flex items-center gap-1.5">
              <Rocket class="h-3.5 w-3.5 text-indigo-400" />
              <span>Compose & Git Deployments</span>
            </span>
            <p class="text-[11px] text-slate-400">
              Triggers when a stack deployment completes (success or failure log output).
            </p>
          </div>
        </label>

        <!-- High Host Resource Usage -->
        <label class="flex items-start gap-3 p-3 rounded-lg border border-slate-800 bg-slate-900/40 hover:border-slate-700 transition cursor-pointer">
          <input
            type="checkbox"
            v-model="form.notify_high_usage"
            class="h-4 w-4 mt-0.5 rounded border-slate-700 bg-slate-950 text-cyan-500 focus:ring-cyan-500 cursor-pointer"
          />
          <div class="space-y-0.5">
            <span class="text-xs font-bold text-slate-200 flex items-center gap-1.5">
              <Cpu class="h-3.5 w-3.5 text-amber-400" />
              <span>High CPU & RAM Thresholds</span>
            </span>
            <p class="text-[11px] text-slate-400">
              Alerts when host load exceeds your defined warning threshold.
            </p>
          </div>
        </label>
      </div>

      <!-- Threshold Controls (when notify_high_usage is active) -->
      <div v-if="form.notify_high_usage" class="pt-3 border-t border-slate-800/80 grid grid-cols-1 sm:grid-cols-2 gap-4">
        <div>
          <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-400">
            CPU Alert Threshold (%): <span class="text-cyan-300 font-mono font-bold">{{ form.cpu_threshold_percent }}%</span>
          </label>
          <input
            type="range"
            min="50"
            max="98"
            step="5"
            v-model.number="form.cpu_threshold_percent"
            class="w-full accent-cyan-400 cursor-pointer"
          />
        </div>
        <div>
          <label class="mb-1 block text-[10px] font-semibold uppercase tracking-wider text-slate-400">
            RAM Alert Threshold (%): <span class="text-emerald-300 font-mono font-bold">{{ form.memory_threshold_percent }}%</span>
          </label>
          <input
            type="range"
            min="50"
            max="98"
            step="5"
            v-model.number="form.memory_threshold_percent"
            class="w-full accent-emerald-400 cursor-pointer"
          />
        </div>
      </div>
    </div>

    <!-- Action Buttons -->
    <div class="flex flex-wrap items-center justify-between gap-3 pt-2">
      <div class="text-[11px] text-slate-500">
        Changes take effect immediately across all background daemon monitors.
      </div>

      <div class="flex items-center gap-3">
        <button
          type="button"
          @click="testAlert"
          :disabled="testing || (!form.webhook_url && !form.telegram_bot_token)"
          class="cyber-btn-secondary flex items-center gap-1.5 text-xs py-2 px-4 text-cyan-300 disabled:opacity-40"
          title="Send a sample notification right now to test the connection"
        >
          <RotateCcw v-if="testing" class="h-3.5 w-3.5 animate-spin" />
          <Send v-else class="h-3.5 w-3.5" />
          <span>{{ testing ? 'Sending Test…' : 'Test Alert' }}</span>
        </button>

        <button
          type="button"
          @click="save"
          :disabled="saving"
          class="cyber-btn flex items-center gap-1.5 text-xs py-2 px-5 text-white bg-indigo-600/80 hover:bg-indigo-600 border-indigo-500"
        >
          <RotateCcw v-if="saving" class="h-3.5 w-3.5 animate-spin" />
          <Save v-else class="h-3.5 w-3.5" />
          <span>{{ saving ? 'Saving…' : 'Save Alerts Configuration' }}</span>
        </button>
      </div>
    </div>
  </div>
</template>
