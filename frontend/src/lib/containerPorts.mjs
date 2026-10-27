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

export function getMdnsPortSuffix(ports = '') {
  if (!ports) return ''
  for (const entry of ports.split(',')) {
    const trimmed = entry.trim()
    const match = trimmed.match(/(?::|^)(\d+)->/)
    if (match) {
      const hostPort = match[1]
      if (hostPort === '80') return ''
      return `:${hostPort}`
    }
  }
  return ''
}
