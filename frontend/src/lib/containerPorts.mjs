// Exposure reaches the app over a Docker network, so use its container port.
export function getPrimaryPort(ports = '') {
  for (const entry of ports.split(',')) {
    const match = entry.trim().match(/(?:->|^)(\d+)(?:-\d+)?\/tcp\b/)
    if (match) return match[1]
  }
  return '80'
}
