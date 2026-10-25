export function remoteAccessTab(queryTab, mode) {
  const tabs = ['cloudflare', 'tailscale', 'wireguard']
  if (tabs.includes(queryTab)) return queryTab
  if (mode === 'cloudflare') return 'cloudflare'
  if (mode === 'tailscale' || mode === 'wireguard') return mode
  return 'tailscale'
}

export function cloudflareEnabled(status) {
  return Boolean(status && status.status !== 'disabled')
}
