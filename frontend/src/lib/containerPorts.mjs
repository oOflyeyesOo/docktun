// Exposure reaches the app over a Docker network, so use its container port.
export function getPrimaryPort(ports = '') {
  for (const entry of ports.split(',')) {
    const match = entry.trim().match(/(?:->|^)(\d+)(?:-\d+)?\/tcp\b/)
    if (match) return match[1]
  }
  return '80'
}

export function cleanContainerName(name = '') {
  return name.replace(/^\//, '').toLowerCase()
}

// Local mDNS (.local) domains are routed portlessly via the DockTun ZeroConf reverse proxy gateway on port 80.
export function getMdnsPortSuffix(_ports = '') {
  return ''
}

// DNS names are useful for non-web services too, but opening a database or UDP
// listener in a browser is misleading. Keep custom TCP web ports working.
const nonWebPorts = new Set([21, 22, 25, 53, 110, 139, 143, 389, 445, 465, 587, 636, 853, 993, 995, 1433, 1521, 2049, 2375, 2376, 3306, 5432, 5671, 5672, 6379, 6380, 11211, 27017, 27018, 27019])
export function hasLocalWebLink(ports = '') {
  return ports.split(',').some(entry => {
    const match = entry.trim().match(/(?:->|^)(\d+)(?:-\d+)?\/tcp\b/)
    return match && !nonWebPorts.has(Number(match[1]))
  })
}

export function localAddress(container = {}) {
  const hostname = `${cleanContainerName(container.name)}.local`
  return hasLocalWebLink(container.ports) ? `http://${hostname}` : hostname
}
