import test from 'node:test'
import assert from 'node:assert/strict'
import {
  parseCpuPercent,
  parseMemoryBytes,
  parseMemoryUsage,
  parsePrimaryPortNumber,
  getDisplayPort,
  parseUptimeSeconds,
  sortContainers,
  calculateStackSelection,
  toggleStackSelection,
  calculateTableSelection,
  toggleTableSelection,
  loadCollapsedStacks,
  saveCollapsedStacks,
  toggleStackCollapse,
  collapseAllStacks,
  expandAllStacks,
} from './containers-table.mjs'

test('CPU parsing handles standard, fractional, multi-core, stopped and malformed inputs', () => {
  // Standard valid percentages
  assert.equal(parseCpuPercent('1.8%'), 1.8)
  assert.equal(parseCpuPercent('0.0%'), 0.0)
  assert.equal(parseCpuPercent('100%'), 100)
  assert.equal(parseCpuPercent(' 45.2 % '), 45.2)

  // Multi-core containers (>100%)
  assert.equal(parseCpuPercent('250.4%'), 250.4)

  // Stopped containers return 0
  assert.equal(parseCpuPercent('50.0%', false), 0)

  // Malformed non-CPU strings (N/A, empty, memory units accidentally passed)
  assert.equal(parseCpuPercent('N/A'), 0)
  assert.equal(parseCpuPercent('—'), 0)
  assert.equal(parseCpuPercent(''), 0)
  assert.equal(parseCpuPercent(undefined), 0)
  assert.equal(parseCpuPercent(null), 0)
  assert.equal(parseCpuPercent('1.2 GB'), 0)
  assert.equal(parseCpuPercent('512 MB'), 0)
  assert.equal(parseCpuPercent('10 KB'), 0)
  assert.equal(parseCpuPercent('invalid'), 0)
})

test('Memory bytes parsing accurately converts units and ignores percentages and malformed values', () => {
  // Bytes conversion across units
  assert.equal(parseMemoryBytes('100 B'), 100)
  assert.equal(parseMemoryBytes('10 KB'), 10 * 1024)
  assert.equal(parseMemoryBytes('512 MB'), 512 * 1024 * 1024)
  assert.equal(parseMemoryBytes('1.2 GB'), 1.2 * 1024 * 1024 * 1024)
  assert.equal(parseMemoryBytes('1.5 GiB'), 1.5 * 1024 * 1024 * 1024)
  assert.equal(parseMemoryBytes('2 TB'), 2 * 1024 * 1024 * 1024 * 1024)

  // Ratio formats: extracts used part
  assert.equal(parseMemoryBytes('12.5MiB / 1.95GiB'), 12.5 * 1024 * 1024)
  assert.equal(parseMemoryBytes('512 MB / 2 GB'), 512 * 1024 * 1024)

  // Rejects pure percentages and malformed strings
  assert.equal(parseMemoryBytes('1.8%'), 0)
  assert.equal(parseMemoryBytes('0.0%'), 0)
  assert.equal(parseMemoryBytes('N/A'), 0)
  assert.equal(parseMemoryBytes('—'), 0)
  assert.equal(parseMemoryBytes(''), 0)
  assert.equal(parseMemoryBytes(undefined), 0)
  assert.equal(parseMemoryBytes(null), 0)
  assert.equal(parseMemoryBytes('512 MB', false), 0) // stopped
})

test('Memory usage parsing returns structured used, total, and percentage ratio', () => {
  // Ratio format
  const ratio = parseMemoryUsage('512 MB / 1024 MB')
  assert.equal(ratio.used, '512 MB')
  assert.equal(ratio.total, '1024 MB')
  assert.equal(ratio.percent, 50)

  // Pure percent format
  const pct = parseMemoryUsage('1.8%')
  assert.equal(pct.used, '1.8%')
  assert.equal(pct.total, '')
  assert.equal(pct.percent, 1.8)

  // Single quantity format
  const single = parseMemoryUsage('1.2 GB')
  assert.equal(single.used, '1.2 GB')
  assert.equal(single.total, '')
  assert.equal(single.percent, 0)

  // Malformed and stopped
  assert.deepEqual(parseMemoryUsage('N/A'), { used: 'N/A', total: '', percent: 0 })
  assert.deepEqual(parseMemoryUsage('', true), { used: '—', total: '', percent: 0 })
  assert.deepEqual(parseMemoryUsage('512 MB', false), { used: '—', total: '', percent: 0 })
})

test('Port parsing handles port ranges, UDP binds, multiple published ports and missing values', () => {
  // Ranges
  assert.equal(parsePrimaryPortNumber('8080-8082/tcp'), 8080)
  assert.equal(parsePrimaryPortNumber('0.0.0.0:8080-8082->8080-8082/tcp'), 8080)

  // UDP binds
  assert.equal(parsePrimaryPortNumber('53/udp'), 53)
  assert.equal(parsePrimaryPortNumber('0.0.0.0:5353->53/udp'), 5353)

  // Multiple published ports
  assert.equal(parsePrimaryPortNumber('53/udp, 0.0.0.0:8080->80/tcp, 0.0.0.0:8443->443/tcp'), 53)
  // Display port prefers TCP web port over UDP
  assert.equal(getDisplayPort('53/udp, 0.0.0.0:8080->80/tcp, 0.0.0.0:8443->443/tcp'), '0.0.0.0:8080->80/tcp')

  // Missing or malformed
  assert.equal(parsePrimaryPortNumber(''), Infinity)
  assert.equal(parsePrimaryPortNumber(undefined), Infinity)
  assert.equal(parsePrimaryPortNumber(null), Infinity)
  assert.equal(parsePrimaryPortNumber('invalid-port'), Infinity)
  assert.equal(getDisplayPort(''), '—')
  assert.equal(getDisplayPort(null), '—')
})

test('Sorting algorithm correctly orders containers by name, image, ports, CPU, memory, uptime, and ingress', () => {
  const containers = [
    {
      id: 'c1',
      name: '/web-service',
      image: 'nginx:1.25',
      ports: '0.0.0.0:8080->80/tcp',
      cpu: '2.5%',
      memory: '512 MB',
      uptime: 'Up 2 hours',
      running: true,
      hostname: 'app.example.com',
      exposure_mode: 'public',
    },
    {
      id: 'c2',
      name: 'api-service',
      image: 'node:20-alpine',
      ports: '0.0.0.0:3000->3000/tcp',
      cpu: '15.0%',
      memory: '1.2 GB',
      uptime: 'Up 3 days',
      running: true,
      hostname: '',
      exposure_mode: 'internal',
    },
    {
      id: 'c3',
      name: 'database',
      image: 'postgres:16',
      ports: '5432/tcp',
      cpu: '0.5%',
      memory: '10 KB',
      uptime: 'Exited (0) 10 minutes ago',
      running: false,
      hostname: '',
      exposure_mode: 'internal',
    },
    {
      id: 'c4',
      name: 'dns-resolver',
      image: 'coredns:latest',
      ports: '53/udp',
      cpu: '0.0%',
      memory: '100 B',
      uptime: 'Up 5 minutes',
      running: true,
      hostname: 'dns.internal',
      exposure_mode: 'private',
    },
  ]

  // 1. Sort by name (case-insensitive, strips leading slash: api-service < database < dns-resolver < web-service)
  const byNameAsc = sortContainers(containers, 'name', 'asc')
  assert.deepEqual(byNameAsc.map((c) => c.name), ['api-service', 'database', 'dns-resolver', '/web-service'])

  const byNameDesc = sortContainers(containers, 'name', 'desc')
  assert.deepEqual(byNameDesc.map((c) => c.name), ['/web-service', 'dns-resolver', 'database', 'api-service'])

  // 2. Sort by image
  const byImageAsc = sortContainers(containers, 'image', 'asc')
  assert.deepEqual(byImageAsc.map((c) => c.id), ['c4', 'c1', 'c2', 'c3'])

  // 3. Sort by ports (numeric: 53 < 3000 < 5432 < 8080)
  const byPortsAsc = sortContainers(containers, 'ports', 'asc')
  assert.deepEqual(byPortsAsc.map((c) => c.id), ['c4', 'c2', 'c3', 'c1'])

  // 4. Sort by CPU (running: c3 stopped = 0%, c4 = 0%, c1 = 2.5%, c2 = 15.0%)
  const byCpuAsc = sortContainers(containers, 'cpu', 'asc')
  assert.equal(byCpuAsc[byCpuAsc.length - 1].id, 'c2')
  assert.equal(byCpuAsc[byCpuAsc.length - 2].id, 'c1')

  const byCpuDesc = sortContainers(containers, 'cpu', 'desc')
  assert.equal(byCpuDesc[0].id, 'c2')
  assert.equal(byCpuDesc[1].id, 'c1')

  // 5. Sort by memory (c3 stopped = 0, c4 = 100B, c1 = 512MB, c2 = 1.2GB)
  const byMemAsc = sortContainers(containers, 'memory', 'asc')
  assert.equal(byMemAsc[0].id, 'c3')
  assert.equal(byMemAsc[1].id, 'c4')
  assert.equal(byMemAsc[2].id, 'c1')
  assert.equal(byMemAsc[3].id, 'c2')

  // 6. Sort by uptime (c3 stopped = 0, c4 = 5 min, c1 = 2 hours, c2 = 3 days)
  const byUptimeAsc = sortContainers(containers, 'uptime', 'asc')
  assert.deepEqual(byUptimeAsc.map((c) => c.id), ['c3', 'c4', 'c1', 'c2'])

  const byUptimeDesc = sortContainers(containers, 'uptime', 'desc')
  assert.deepEqual(byUptimeDesc.map((c) => c.id), ['c2', 'c1', 'c4', 'c3'])

  // 7. Sort by ingress / exposure (app.example.com < dns.internal < internal)
  const byIngressAsc = sortContainers(containers, 'ingress', 'asc')
  assert.equal(byIngressAsc[0].hostname, 'app.example.com')
  assert.equal(byIngressAsc[1].hostname, 'dns.internal')
  assert.equal(byIngressAsc[2].exposure_mode, 'internal')
  assert.equal(byIngressAsc[3].exposure_mode, 'internal')

  const byExposureAsc = sortContainers(containers, 'exposure', 'asc')
  assert.deepEqual(byIngressAsc, byExposureAsc)

  const byIngressDesc = sortContainers(containers, 'ingress', 'desc')
  assert.equal(byIngressDesc[3].hostname, 'app.example.com')
  assert.equal(byIngressDesc[2].hostname, 'dns.internal')
})

test('Stack selection logic handles select all, partial indeterminate math, and isolated containers', () => {
  const stack = [{ id: 'app-1' }, { id: 'app-2' }, { id: 'app-3' }]

  // 1. None selected
  let state = calculateStackSelection(stack, {})
  assert.equal(state.isSelected, false)
  assert.equal(state.isIndeterminate, false)
  assert.equal(state.selectedCount, 0)
  assert.equal(state.totalCount, 3)

  // 2. Partial select (indeterminate)
  state = calculateStackSelection(stack, { 'app-1': true })
  assert.equal(state.isSelected, false)
  assert.equal(state.isIndeterminate, true)
  assert.equal(state.selectedCount, 1)

  // 3. All selected
  state = calculateStackSelection(stack, { 'app-1': true, 'app-2': true, 'app-3': true })
  assert.equal(state.isSelected, true)
  assert.equal(state.isIndeterminate, false)
  assert.equal(state.selectedCount, 3)

  // 4. Toggle stack when partial -> selects all
  let toggled = toggleStackSelection(stack, { 'app-1': true })
  assert.deepEqual(toggled, { 'app-1': true, 'app-2': true, 'app-3': true })

  // 5. Toggle stack when all selected -> deselects all in this stack
  toggled = toggleStackSelection(stack, { 'app-1': true, 'app-2': true, 'app-3': true, 'other-isolated': true })
  assert.deepEqual(toggled, { 'other-isolated': true }) // preserves isolated container

  // 6. Empty stack
  const emptyState = calculateStackSelection([], { 'app-1': true })
  assert.equal(emptyState.isSelected, false)
  assert.equal(emptyState.isIndeterminate, false)
  assert.equal(emptyState.selectedCount, 0)
})

test('Table-wide selection logic computes select-all and indeterminate accurately', () => {
  const flatContainers = [{ id: 'c1' }, { id: 'c2' }, { id: 'c3' }]

  // None selected
  assert.deepEqual(calculateTableSelection(flatContainers, {}), {
    isAllSelected: false,
    isIndeterminate: false,
    selectedCount: 0,
    totalCount: 3,
  })

  // 1 of 3 selected -> indeterminate
  assert.deepEqual(calculateTableSelection(flatContainers, { c2: true }), {
    isAllSelected: false,
    isIndeterminate: true,
    selectedCount: 1,
    totalCount: 3,
  })

  // 3 of 3 selected -> all selected
  assert.deepEqual(calculateTableSelection(flatContainers, { c1: true, c2: true, c3: true }), {
    isAllSelected: true,
    isIndeterminate: false,
    selectedCount: 3,
    totalCount: 3,
  })

  // Toggle table select all: when all selected, deselects all
  let next = toggleTableSelection(flatContainers, { c1: true, c2: true, c3: true })
  assert.deepEqual(next, {})

  // Toggle table select all: when partially selected, selects all
  next = toggleTableSelection(flatContainers, { c1: true })
  assert.deepEqual(next, { c1: true, c2: true, c3: true })
})

test('Accordion collapse state persistence and malformed JSON recovery in localStorage', () => {
  // Mock localStorage implementation
  const store = new Map()
  const mockStorage = {
    getItem(key) {
      return store.get(key) ?? null
    },
    setItem(key, value) {
      store.set(key, String(value))
    },
  }

  // 1. Initial empty storage
  assert.deepEqual(loadCollapsedStacks(mockStorage), {})

  // 2. Normal persistence and save
  saveCollapsedStacks(mockStorage, { 'compose:"myapp"': true })
  assert.deepEqual(loadCollapsedStacks(mockStorage), { 'compose:"myapp"': true })

  // 3. Malformed JSON recovery: corrupted string
  mockStorage.setItem('docktun_collapsed_stacks', '{invalid: json,')
  // Must not throw, returns {}, and heals the corrupted key in storage
  assert.deepEqual(loadCollapsedStacks(mockStorage), {})
  assert.equal(mockStorage.getItem('docktun_collapsed_stacks'), '{}')

  // 4. Malformed JSON recovery: primitive or array types in storage
  mockStorage.setItem('docktun_collapsed_stacks', 'undefined')
  assert.deepEqual(loadCollapsedStacks(mockStorage), {})

  mockStorage.setItem('docktun_collapsed_stacks', '123')
  assert.deepEqual(loadCollapsedStacks(mockStorage), {})

  mockStorage.setItem('docktun_collapsed_stacks', 'null')
  assert.deepEqual(loadCollapsedStacks(mockStorage), {})

  mockStorage.setItem('docktun_collapsed_stacks', '["arr"]')
  assert.deepEqual(loadCollapsedStacks(mockStorage), {})

  // 5. Null or throwing storage does not crash
  assert.deepEqual(loadCollapsedStacks(null), {})
  assert.deepEqual(loadCollapsedStacks(undefined), {})

  const throwingStorage = {
    getItem() {
      throw new Error('Storage access blocked')
    },
    setItem() {
      throw new Error('Quota exceeded')
    },
  }
  assert.deepEqual(loadCollapsedStacks(throwingStorage), {})
  // saving to throwing storage does not throw
  assert.doesNotThrow(() => saveCollapsedStacks(throwingStorage, { key: true }))

  // 6. Accordion toggle, collapse all and expand all
  const groups = [{ key: 'stack-a' }, { key: 'stack-b' }]
  const allCollapsed = collapseAllStacks(groups)
  assert.deepEqual(allCollapsed, { 'stack-a': true, 'stack-b': true })

  const toggledA = toggleStackCollapse(allCollapsed, 'stack-a')
  assert.deepEqual(toggledA, { 'stack-a': false, 'stack-b': true })

  const expandedAll = expandAllStacks()
  assert.deepEqual(expandedAll, {})
})
