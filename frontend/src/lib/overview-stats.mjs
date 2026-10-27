const mode = c => c.exposure_mode || c.exposure?.mode || 'internal'
const provider = c => c.private_type || c.exposure?.private_type || ''
export const needsAttention = c => ['restarting', 'dead'].includes(c.state) || /unhealthy/i.test(c.status || '')

export function summarizeInventory(containers = []) {
  const apps = containers.filter(c => ['public', 'private'].includes(mode(c)))
  const nodes = new Set(apps.filter(c => provider(c) === 'tailscale').map(c => c.exposure?.tailscale_node).filter(Boolean))
  return {
    total: containers.length,
    running: containers.filter(c => c.running).length,
    stopped: containers.filter(c => !c.running).length,
    paused: containers.filter(c => c.state === 'paused').length,
    attention: containers.filter(needsAttention),
    updates: containers.filter(c => c.update_available).length,
    stacks: new Set(containers.map(c => c.project_name).filter(Boolean)).size,
    exposed: apps.length,
    cloudflare: apps.filter(c => mode(c) === 'public').length,
    tailscale: apps.filter(c => mode(c) === 'private' && provider(c) === 'tailscale').length,
    wireguard: apps.filter(c => mode(c) === 'private' && provider(c) === 'wireguard').length,
    tailscaleProxiesRunning: containers.filter(c => c.running && nodes.has((c.name || '').replace(/^\//, ''))).length,
  }
}

// Docker's 100% CPU is one core. Normalize only when the engine's CPU count
// is known; preserve missing samples instead of presenting them as idle.
export function summarizeUsage(metrics, engineCPUs) {
  const running = (metrics?.containers || []).filter(c => c.state === 'running')
  const sampled = running.map(c => ({ ...c, cpu: typeof c.cpu_percent === 'string' && c.cpu_percent.trim() ? Number.parseFloat(c.cpu_percent) : NaN }))
    .filter(c => Number.isFinite(c.cpu) && c.cpu >= 0)
  const totalCPU = sampled.length ? sampled.reduce((sum, c) => sum + c.cpu, 0) : null
  const host = metrics?.host
  const capacity = (used, total) => Number.isFinite(used) && used >= 0 && Number.isFinite(total) && total > 0 ? used / total * 100 : null
  return {
    cpuPercent: totalCPU !== null && engineCPUs > 0 ? totalCPU / engineCPUs : null,
    cpuCores: totalCPU === null ? null : totalCPU / 100,
    sampled: sampled.length,
    running: running.length,
    memoryPercent: capacity(host?.mem_used, host?.mem_total),
    diskPercent: capacity(host?.disk_used, host?.disk_total),
    busiest: sampled.sort((a, b) => b.cpu - a.cpu || a.name.localeCompare(b.name)).slice(0, 5),
  }
}

export function formatBytes(value) {
  if (!Number.isFinite(value) || value < 0) return '—'
  if (value === 0) return '0 B'
  const power = Math.max(0, Math.min(Math.floor(Math.log(value) / Math.log(1024)), 4))
  return `${(value / 1024 ** power).toFixed(power ? 1 : 0)} ${['B', 'KiB', 'MiB', 'GiB', 'TiB'][power]}`
}

export function formatUptime(seconds) {
  if (!Number.isFinite(seconds) || seconds <= 0) return '—'
  const minutes = Math.floor(seconds / 60)
  const days = Math.floor(minutes / 1440)
  const hours = Math.floor(minutes % 1440 / 60)
  if (days) return `${days}d ${hours}h`
  if (hours) return `${hours}h ${minutes % 60}m`
  return minutes ? `${minutes}m` : '< 1m'
}
