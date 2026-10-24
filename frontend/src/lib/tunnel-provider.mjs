export function remoteAccessTab(queryTab, mode) {
  const tabs = ['cloudflare', 'tailscale', 'wireguard']
  if (tabs.includes(queryTab)) return queryTab
  if (mode === 'tailscale' || mode === 'wireguard') return mode
  return 'cloudflare'
}

export function cloudflareEnabled(status) {
  return Boolean(status && status.status !== 'disabled')
}
