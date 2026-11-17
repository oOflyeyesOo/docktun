import { cleanContainerName } from './containerPorts.mjs'

/**
 * Parses CPU percentage string safely into a float.
 * Handles '1.8%', '0.0%', 'N/A', '1.2 GB' (rejects memory unit), undefined, missing, etc.
 *
 * @param {string | undefined | null} cpuStr
 * @param {boolean} [running=true]
 * @returns {number}
 */
export function parseCpuPercent(cpuStr, running = true) {
  if (!running || !cpuStr || typeof cpuStr !== 'string') return 0
  const trimmed = cpuStr.trim()
  if (!trimmed || trimmed === 'N/A' || trimmed === '—' || trimmed === 'null' || trimmed === 'undefined') {
    return 0
  }
  // Reject memory strings that might be passed erroneously
  if (/[a-zA-Z]/.test(trimmed.replace('%', ''))) {
    return 0
  }
  const n = parseFloat(trimmed.replace('%', ''))
  return isNaN(n) ? 0 : Math.max(n, 0)
}

/**
 * Converts memory string (e.g. '1.2 GB', '512 MB', '10 KB', '12.5MiB / 1.95GiB', '100 B') to bytes.
 * Returns 0 for invalid, missing, or pure percentage strings ('1.8%', '0.0%', 'N/A').
 *
 * @param {string | undefined | null} memStr
 * @param {boolean} [running=true]
 * @returns {number}
 */
export function parseMemoryBytes(memStr, running = true) {
  if (!running || !memStr || typeof memStr !== 'string') return 0
  const trimmed = memStr.trim()
  if (!trimmed || trimmed === 'N/A' || trimmed === '—' || trimmed === 'null' || trimmed === 'undefined') {
    return 0
  }
  // If memory has used / total format, take the used portion
  const usedPart = trimmed.split('/')[0].trim()
  // Reject pure percentage strings
  if (usedPart.endsWith('%') || /^\d+(\.\d+)?%$/.test(usedPart)) {
    return 0
  }

  const match = usedPart.match(/^([\d.]+)\s*([a-zA-Z]+)?$/)
  if (!match) return 0

  const num = parseFloat(match[1])
  if (isNaN(num)) return 0

  const unit = (match[2] || '').toUpperCase()
  if (unit.startsWith('T')) return num * 1024 * 1024 * 1024 * 1024
  if (unit.startsWith('G')) return num * 1024 * 1024 * 1024
  if (unit.startsWith('M')) return num * 1024 * 1024
  if (unit.startsWith('K')) return num * 1024
  if (unit.startsWith('B')) return num

  return num
}

/**
 * Parses memory string into structured usage object with used, total, and percent.
 *
 * @param {string | undefined | null} memStr
 * @param {boolean} [running=true]
 * @returns {{ used: string, total: string, percent: number }}
 */
export function parseMemoryUsage(memStr, running = true) {
  if (!running || !memStr || typeof memStr !== 'string') {
    return { used: '—', total: '', percent: 0 }
  }
  const trimmed = memStr.trim()
  if (!trimmed || trimmed === 'N/A' || trimmed === '—') {
    return { used: 'N/A', total: '', percent: 0 }
  }

  // Pure percentage case, e.g. '1.8%' or '0.0%'
  if (/^([\d.]+)\s*%$/.test(trimmed)) {
    const pct = parseFloat(trimmed.replace('%', ''))
    return {
      used: trimmed,
      total: '',
      percent: isNaN(pct) ? 0 : Math.min(Math.max(pct, 0), 100),
    }
  }

  const parts = trimmed.split('/')
  const used = parts[0]?.trim() || '—'
  const total = parts[1]?.trim() || ''

  let percent = 0
  if (parts.length >= 2) {
    const uBytes = parseMemoryBytes(used, true)
    const tBytes = parseMemoryBytes(total, true)
    if (tBytes > 0) {
      percent = Math.min(Math.max(Math.round((uBytes / tBytes) * 100), 0), 100)
    }
  }

  return { used, total, percent }
}

/**
 * Extracts primary port number for sorting and comparison.
 * Handles ranges ('8080-8082/tcp'), host published bindings ('0.0.0.0:8080->80/tcp'),
 * UDP binds ('53/udp'), multiple published ports, and missing ports.
 *
 * @param {string | undefined | null} ports
 * @returns {number} returns port number or Infinity if no port found
 */
export function parsePrimaryPortNumber(ports) {
  if (!ports || typeof ports !== 'string') return Infinity
  const entries = ports.split(',')

  for (const entry of entries) {
    const trimmed = entry.trim()
    if (!trimmed) continue

    // 1. Host published port binding e.g. "0.0.0.0:8080->80/tcp" or "[::]:8080->80/tcp"
    const hostMatch = trimmed.match(/:(\d+)(?:-\d+)?->/)
    if (hostMatch) {
      const p = parseInt(hostMatch[1], 10)
      if (!isNaN(p)) return p
    }

    // 2. Container port mapping e.g. "->80/tcp" or "->8080-8082/tcp"
    const containerMatch = trimmed.match(/->(\d+)(?:-\d+)?/)
    if (containerMatch) {
      const p = parseInt(containerMatch[1], 10)
      if (!isNaN(p)) return p
    }

    // 3. Range or direct port binding e.g. "8080-8082/tcp" or "80/tcp" or "53/udp"
    const directMatch = trimmed.match(/^(\d+)(?:-\d+)?\/(?:tcp|udp)\b/)
    if (directMatch) {
      const p = parseInt(directMatch[1], 10)
      if (!isNaN(p)) return p
    }

    // 4. Any numeric port match as fallback
    const genericMatch = trimmed.match(/(\d+)/)
    if (genericMatch) {
      const p = parseInt(genericMatch[1], 10)
      if (!isNaN(p)) return p
    }
  }

  return Infinity
}

/**
 * Returns the best web port entry from a potentially multi-port string.
 *
 * @param {string | undefined | null} ports
 * @returns {string}
 */
export function getDisplayPort(ports) {
  if (!ports || typeof ports !== 'string') return '—'
  const entries = ports.split(',').map((e) => e.trim()).filter(Boolean)
  if (entries.length === 0) return '—'

  // Prefer entries with TCP and web-like ports
  const webEntry = entries.find((e) => {
    return e.includes('/tcp') && !/(?:21|22|25|53|3306|5432|6379)\/tcp/.test(e)
  })

  return webEntry || entries[0]
}

/**
 * Parses uptime string to approximate seconds for sorting.
 * e.g. "Up 3 days", "Up 2 hours", "Up 5 minutes", "Exited (0) 2 hours ago"
 *
 * @param {string | undefined | null} uptime
 * @param {boolean} [running=true]
 * @returns {number}
 */
export function parseUptimeSeconds(uptime, running = true) {
  if (!running || !uptime || typeof uptime !== 'string') return 0
  const trimmed = uptime.trim()
  if (!trimmed.toLowerCase().startsWith('up')) return 0

  let totalSeconds = 0
  const dayMatch = trimmed.match(/(\d+)\s*day/i)
  if (dayMatch) totalSeconds += parseInt(dayMatch[1], 10) * 86400

  const hourMatch = trimmed.match(/(\d+)\s*hour/i)
  if (hourMatch) totalSeconds += parseInt(hourMatch[1], 10) * 3600
  else if (/about an hour/i.test(trimmed)) totalSeconds += 3600

  const minMatch = trimmed.match(/(\d+)\s*min/i)
  if (minMatch) totalSeconds += parseInt(minMatch[1], 10) * 60
  else if (/about a minute/i.test(trimmed)) totalSeconds += 60

  const secMatch = trimmed.match(/(\d+)\s*sec/i)
  if (secMatch) totalSeconds += parseInt(secMatch[1], 10)
  else if (/less than a second/i.test(trimmed)) totalSeconds += 1

  return totalSeconds > 0 ? totalSeconds : 1
}

/**
 * Sorts containers by field and direction safely.
 * Fields: 'name', 'image', 'ports', 'cpu', 'memory', 'uptime', 'ingress' / 'exposure'
 *
 * @param {Array<any>} list
 * @param {string} sortBy
 * @param {'asc' | 'desc'} sortOrder
 * @returns {Array<any>}
 */
export function sortContainers(list, sortBy = 'name', sortOrder = 'asc') {
  if (!Array.isArray(list)) return []

  const orderMultiplier = sortOrder === 'desc' ? -1 : 1

  return [...list].sort((a, b) => {
    if (!a && !b) return 0
    if (!a) return 1
    if (!b) return -1

    let comparison = 0

    switch (sortBy) {
      case 'name': {
        const nameA = cleanContainerName(a.name || '')
        const nameB = cleanContainerName(b.name || '')
        comparison = nameA.localeCompare(nameB, undefined, { sensitivity: 'base' })
        break
      }
      case 'image': {
        const imgA = a.image || ''
        const imgB = b.image || ''
        comparison = imgA.localeCompare(imgB, undefined, { sensitivity: 'base' })
        break
      }
      case 'ports': {
        const pA = parsePrimaryPortNumber(a.ports)
        const pB = parsePrimaryPortNumber(b.ports)
        if (pA !== pB) {
          comparison = pA - pB
        } else {
          comparison = (a.ports || '').localeCompare(b.ports || '')
        }
        break
      }
      case 'cpu': {
        const cpuA = parseCpuPercent(a.cpu, a.running)
        const cpuB = parseCpuPercent(b.cpu, b.running)
        comparison = cpuA - cpuB
        break
      }
      case 'memory': {
        const memA = parseMemoryBytes(a.memory, a.running)
        const memB = parseMemoryBytes(b.memory, b.running)
        comparison = memA - memB
        break
      }
      case 'uptime': {
        const upA = parseUptimeSeconds(a.uptime || a.status, a.running)
        const upB = parseUptimeSeconds(b.uptime || b.status, b.running)
        if (upA !== upB) {
          comparison = upA - upB
        } else {
          const strA = a.uptime || a.status || ''
          const strB = b.uptime || b.status || ''
          comparison = strA.localeCompare(strB)
        }
        break
      }
      case 'exposure':
      case 'ingress': {
        const hostA = a.hostname || a.exposure_mode || ''
        const hostB = b.hostname || b.exposure_mode || ''
        comparison = hostA.localeCompare(hostB, undefined, { sensitivity: 'base' })
        break
      }
      default:
        comparison = 0
    }

    return comparison * orderMultiplier
  })
}

/**
 * Calculates stack selection status: isSelected, isIndeterminate, counts.
 *
 * @param {Array<{ id: string }>} groupContainers
 * @param {Record<string, boolean>} selectedContainerIds
 * @returns {{ isSelected: boolean, isIndeterminate: boolean, selectedCount: number, totalCount: number }}
 */
export function calculateStackSelection(groupContainers, selectedContainerIds = {}) {
  if (!Array.isArray(groupContainers) || groupContainers.length === 0) {
    return { isSelected: false, isIndeterminate: false, selectedCount: 0, totalCount: 0 }
  }

  const totalCount = groupContainers.length
  let selectedCount = 0

  for (const c of groupContainers) {
    if (c && c.id && selectedContainerIds[c.id]) {
      selectedCount++
    }
  }

  const isSelected = totalCount > 0 && selectedCount === totalCount
  const isIndeterminate = selectedCount > 0 && selectedCount < totalCount

  return { isSelected, isIndeterminate, selectedCount, totalCount }
}

/**
 * Toggles selection for an entire stack.
 * If all are selected, deselects all in the stack.
 * If none or partially selected, selects all in the stack.
 *
 * @param {Array<{ id: string }>} groupContainers
 * @param {Record<string, boolean>} selectedContainerIds
 * @returns {Record<string, boolean>} new selection map
 */
export function toggleStackSelection(groupContainers, selectedContainerIds = {}) {
  const next = { ...selectedContainerIds }
  if (!Array.isArray(groupContainers) || groupContainers.length === 0) {
    return next
  }

  const { isSelected } = calculateStackSelection(groupContainers, next)

  for (const c of groupContainers) {
    if (!c || !c.id) continue
    if (isSelected) {
      delete next[c.id]
    } else {
      next[c.id] = true
    }
  }

  return next
}

/**
 * Calculates table-wide selection status across all flat containers.
 *
 * @param {Array<{ id: string }>} flatContainers
 * @param {Record<string, boolean>} selectedContainerIds
 * @returns {{ isAllSelected: boolean, isIndeterminate: boolean, selectedCount: number, totalCount: number }}
 */
export function calculateTableSelection(flatContainers, selectedContainerIds = {}) {
  if (!Array.isArray(flatContainers) || flatContainers.length === 0) {
    return { isAllSelected: false, isIndeterminate: false, selectedCount: 0, totalCount: 0 }
  }

  const totalCount = flatContainers.length
  let selectedCount = 0

  for (const c of flatContainers) {
    if (c && c.id && selectedContainerIds[c.id]) {
      selectedCount++
    }
  }

  const isAllSelected = totalCount > 0 && selectedCount === totalCount
  const isIndeterminate = selectedCount > 0 && selectedCount < totalCount

  return { isAllSelected, isIndeterminate, selectedCount, totalCount }
}

/**
 * Toggles table-wide select all.
 *
 * @param {Array<{ id: string }>} flatContainers
 * @param {Record<string, boolean>} selectedContainerIds
 * @param {boolean} [forceSelected]
 * @returns {Record<string, boolean>}
 */
export function toggleTableSelection(flatContainers, selectedContainerIds = {}, forceSelected) {
  if (!Array.isArray(flatContainers)) return {}

  const { isAllSelected } = calculateTableSelection(flatContainers, selectedContainerIds)

  if (forceSelected === false || (forceSelected === undefined && isAllSelected)) {
    return {}
  }

  const next = {}
  for (const c of flatContainers) {
    if (c && c.id) {
      next[c.id] = true
    }
  }
  return next
}

/**
 * Safely loads collapsed stacks map from storage.
 * Recovers gracefully from malformed JSON and writes back healthy default.
 *
 * @param {Storage | undefined | null} storage
 * @returns {Record<string, boolean>}
 */
export function loadCollapsedStacks(storage) {
  if (!storage || typeof storage.getItem !== 'function') {
    return {}
  }
  try {
    const raw = storage.getItem('docktun_collapsed_stacks')
    if (!raw) return {}
    const parsed = JSON.parse(raw)
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed
    }
    // Malformed structure (e.g. parsed to array, number, boolean)
    storage.setItem('docktun_collapsed_stacks', '{}')
    return {}
  } catch (err) {
    // Malformed JSON string, self-heal
    try {
      storage.setItem('docktun_collapsed_stacks', '{}')
    } catch {
      // ignore storage write errors
    }
    return {}
  }
}

/**
 * Safely saves collapsed stacks map to storage.
 *
 * @param {Storage | undefined | null} storage
 * @param {Record<string, boolean>} collapsedMap
 */
export function saveCollapsedStacks(storage, collapsedMap) {
  if (!storage || typeof storage.setItem !== 'function') return
  try {
    storage.setItem('docktun_collapsed_stacks', JSON.stringify(collapsedMap || {}))
  } catch {
    // Handle QuotaExceededError or security exceptions
  }
}

/**
 * Inverts the collapse state of a specific stack key.
 *
 * @param {Record<string, boolean>} collapsedMap
 * @param {string} key
 * @returns {Record<string, boolean>}
 */
export function toggleStackCollapse(collapsedMap, key) {
  return {
    ...collapsedMap,
    [key]: !collapsedMap?.[key],
  }
}

/**
 * Collapses all stacks.
 *
 * @param {Array<{ key: string }>} groups
 * @returns {Record<string, boolean>}
 */
export function collapseAllStacks(groups) {
  const next = {}
  if (Array.isArray(groups)) {
    for (const g of groups) {
      if (g && g.key) {
        next[g.key] = true
      }
    }
  }
  return next
}

/**
 * Expands all stacks.
 *
 * @returns {Record<string, boolean>}
 */
export function expandAllStacks() {
  return {}
}

/**
 * Determines whether registered Compose projects with 0 running/stopped containers
 * should be included in the container groups.
 * Empty projects are only included when viewing all containers with no active search query.
 *
 * @param {string | { statusFilter?: string, filter?: string, searchQuery?: string, search?: string }} [statusFilterOrOptions='all']
 * @param {string} [maybeSearchQuery='']
 * @returns {boolean}
 */
export function shouldIncludeEmptyProjects(statusFilterOrOptions = 'all', maybeSearchQuery = '') {
  let statusFilter = 'all'
  let searchQuery = ''

  if (typeof statusFilterOrOptions === 'object' && statusFilterOrOptions !== null) {
    statusFilter = statusFilterOrOptions.statusFilter ?? statusFilterOrOptions.filter ?? 'all'
    searchQuery = statusFilterOrOptions.searchQuery ?? statusFilterOrOptions.search ?? ''
  } else {
    statusFilter = statusFilterOrOptions || 'all'
    searchQuery = maybeSearchQuery || ''
  }

  const isDefaultFilter = !statusFilter || statusFilter === 'all'
  const hasSearch = typeof searchQuery === 'string' && searchQuery.trim().length > 0
  return isDefaultFilter && !hasSearch
}

/**
 * Filters container groups based on status filter and search query.
 * Ensures empty stacks (with 0 matching containers) are omitted whenever any filter
 * (stopped, updates, exposed, public, private, local, running) or search query is active.
 *
 * @param {Array<any>} groups
 * @param {string | { statusFilter?: string, filter?: string, searchQuery?: string, search?: string }} [statusFilterOrOptions='all']
 * @param {string} [maybeSearchQuery='']
 * @returns {Array<any>}
 */
export function filterContainerGroups(groups, statusFilterOrOptions = 'all', maybeSearchQuery = '') {
  if (!Array.isArray(groups)) return []
  if (shouldIncludeEmptyProjects(statusFilterOrOptions, maybeSearchQuery)) {
    return [...groups]
  }
  return groups.filter((g) => {
    if (!g) return false
    const count = Array.isArray(g.containers) ? g.containers.length : (g.totalCount ?? 0)
    return count > 0
  })
}

/**
 * Formats stack count with correct singular/plural grammar.
 * e.g. 1 -> "1 stack / group", 2 -> "2 stacks / groups", 0 -> "0 stacks / groups"
 *
 * @param {number} count
 * @returns {string}
 */
export function formatStackCount(count) {
  const n = typeof count === 'number' && !isNaN(count) ? count : 0
  return n === 1 ? '1 stack / group' : `${n} stacks / groups`
}

/**
 * Formats container count with correct singular/plural grammar.
 * e.g. 1 -> "1 container", 2 -> "2 containers", 0 -> "0 containers"
 *
 * @param {number} count
 * @returns {string}
 */
export function formatContainerCount(count) {
  const n = typeof count === 'number' && !isNaN(count) ? count : 0
  return n === 1 ? '1 container' : `${n} containers`
}

/**
 * Formats table sub-header summary line with singular/plural counts.
 * e.g. "Showing 1 stack / group (1 container)"
 *
 * @param {number} stackCount
 * @param {number} containerCount
 * @returns {string}
 */
export function formatTableSummary(stackCount, containerCount) {
  return `Showing ${formatStackCount(stackCount)} (${formatContainerCount(containerCount)})`
}

/**
 * Detects if an image string is an untagged placeholder or bare sha/hex ID.
 * Formats bare IDs cleanly (12-char hex) and flags untagged images.
 * Examples of bare IDs / untagged:
 * - '18328305a553' (12-64 char hex)
 * - 'sha256:18328305a5534c0e9e...'
 * - '<none>:<none>'
 * - '<none>'
 *
 * @param {string | undefined | null} image
 * @returns {{ isUntagged: boolean, isBareId: boolean, shortId: string, displayName: string }}
 */
export function formatImageIdentifier(image) {
  if (!image || typeof image !== 'string') {
    return { isUntagged: true, isBareId: false, shortId: '', displayName: 'untagged' }
  }

  const trimmed = image.trim()
  if (!trimmed || trimmed === '<none>' || trimmed === '<none>:<none>') {
    return { isUntagged: true, isBareId: false, shortId: '', displayName: 'untagged' }
  }

  // Check if image starts with sha256:
  if (trimmed.startsWith('sha256:')) {
    const hex = trimmed.slice(7)
    const shortHex = hex.slice(0, 12)
    return {
      isUntagged: true,
      isBareId: true,
      shortId: shortHex,
      displayName: shortHex,
    }
  }

  // Check if image is a bare hex string (12 or more hex characters)
  if (/^[a-f0-9]{12,}$/i.test(trimmed)) {
    const shortHex = trimmed.slice(0, 12)
    return {
      isUntagged: true,
      isBareId: true,
      shortId: shortHex,
      displayName: shortHex,
    }
  }

  return {
    isUntagged: false,
    isBareId: false,
    shortId: '',
    displayName: trimmed,
  }
}


