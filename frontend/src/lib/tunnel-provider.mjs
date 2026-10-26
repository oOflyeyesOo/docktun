export function remoteAccessTab(queryTab, mode) {
  const tabs = ['cloudflare', 'tailscale', 'wireguard']
  if (tabs.includes(queryTab)) return queryTab
  if (mode === 'cloudflare' || mode === 'public') return 'cloudflare'
  if (mode === 'tailscale' || mode === 'wireguard') return mode
  return 'overview'
}

export function cloudflareEnabled(status) {
  return Boolean(status && status.status !== 'disabled')
}

export function cloudflareCardStatus(summary) {
  if (!summary) return 'Loading'
  if (summary.status === 'disabled') return 'Optional'
  if (summary.running) return 'Active'
  return summary.status || 'Unknown'
}
