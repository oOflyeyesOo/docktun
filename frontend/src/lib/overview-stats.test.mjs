import test from 'node:test'
import assert from 'node:assert/strict'
import { summarizeInventory, summarizeUsage, formatBytes, formatUptime } from './overview-stats.mjs'

test('overview reports non-running states and counts per-app Tailscale proxies independently of shared companion', () => {
  const stats = summarizeInventory([
    { name: 'web', running: true, project_name: 'stack', exposure_mode: 'private', private_type: 'tailscale', exposure: { tailscale_node: 'app-proxy' } },
    { name: 'app-proxy', running: true, project_name: 'stack' },
    { name: 'docktun-tailscale', running: false, state: 'created' },
    { name: 'paused', running: false, state: 'paused' },
    { name: 'retry', running: false, state: 'restarting' },
    { name: 'vpn-app', running: true, exposure_mode: 'private', private_type: 'wireguard' },
    { name: 'public-app', running: true, exposure_mode: 'public', status: 'Up (unhealthy)', update_available: true },
  ])
  assert.equal(stats.running, 4)
  assert.equal(stats.stopped, 3)
  assert.equal(stats.paused, 1)
  assert.equal(stats.attention.length, 2)
  assert.equal(stats.stacks, 1)
  assert.equal(stats.exposed, 3)
  assert.equal(stats.tailscaleProxiesRunning, 1)
  assert.equal(stats.cloudflare, 1)
  assert.equal(stats.wireguard, 1)
})

test('CPU capacity accounts for multiple cores and excludes stale samples on stopped containers', () => {
  const metrics = { containers: [
    { name: 'one', state: 'running', cpu_percent: '150.00%' },
    { name: 'two', state: 'running', cpu_percent: '50.00%' },
    { name: 'stopped', state: 'exited', cpu_percent: '400%' },
  ] }
  assert.equal(summarizeUsage(metrics, 4).cpuPercent, 50)
  assert.equal(summarizeUsage(metrics, null).cpuPercent, null)
  assert.equal(summarizeUsage(metrics, null).cpuCores, 2)
  assert.equal(summarizeUsage(metrics, 4).busiest[0].name, 'one')
})

test('unavailable usage stays unknown; an actual zero sample remains valid', () => {
  assert.equal(summarizeUsage(null, 8).cpuPercent, null)
  assert.equal(summarizeUsage({ containers: [{ name: 'missing', state: 'running', cpu_percent: '' }] }, 8).cpuPercent, null)
  const usage = summarizeUsage({ containers: [{ name: 'idle', state: 'running', cpu_percent: '0.00%' }], host: { mem_used: 0, mem_total: 1024, disk_used: 0, disk_total: 0 } }, 8)
  assert.equal(usage.cpuPercent, 0)
  assert.equal(usage.memoryPercent, 0)
  assert.equal(usage.diskPercent, null)
  assert.equal(formatBytes(undefined), '—')
  assert.equal(formatBytes(0), '0 B')
  assert.equal(formatBytes(1024 ** 3), '1.0 GiB')
  assert.equal(formatUptime(90000), '1d 1h')
  assert.equal(formatUptime(0), '—')
})
