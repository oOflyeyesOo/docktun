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
  shouldIncludeEmptyProjects,
  filterContainerGroups,
  containerPresentation,
  formatStackCount,
  formatContainerCount,
  formatTableSummary,
  formatImageIdentifier,
  getMemoryUsageColor,
  filterEnvContent,
} from './containers-table.mjs'

test('inactive stacks remain manageable with a saved flat preference and restore it on return', () => {
  const active = { name: 'running', containers: [{ id: 'app' }], totalCount: 1 }
  const down = { name: 'archived', containers: [], totalCount: 0 }
  const groups = [active, down]
  const flatPreference = false

  const inactive = filterContainerGroups(groups, 'inactive')
  const inactiveView = containerPresentation('inactive', flatPreference, inactive, [])
  assert.equal(inactiveView.groupByStack, true)
  assert.equal(inactiveView.empty, false, 'down projects provide stack actions despite no container rows')
  assert.deepEqual(inactive, [down])

  const noSearchMatches = filterContainerGroups(groups, 'inactive', 'missing')
  assert.equal(containerPresentation('inactive', flatPreference, noSearchMatches, []).empty, true)

  const activeGroups = filterContainerGroups(groups, 'all')
  const returnedView = containerPresentation('all', flatPreference, activeGroups, active.containers)
  assert.equal(returnedView.groupByStack, false, 'inactive view must not change the saved flat preference')
  assert.equal(returnedView.empty, false)
  assert.equal(containerPresentation('all', true, [], []).empty, true)
})

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

test('Memory color thresholds correctly return emerald, amber, and rose classes', () => {
  // <70% emerald
  assert.equal(getMemoryUsageColor(0), 'bg-emerald-500')
  assert.equal(getMemoryUsageColor(50), 'bg-emerald-500')
  assert.equal(getMemoryUsageColor(69.9), 'bg-emerald-500')

  // 70-85% amber
  assert.equal(getMemoryUsageColor(70), 'bg-amber-500')
  assert.equal(getMemoryUsageColor(75), 'bg-amber-500')
  assert.equal(getMemoryUsageColor(85), 'bg-amber-500')

  // >85% rose
  assert.equal(getMemoryUsageColor(85.1), 'bg-rose-500')
  assert.equal(getMemoryUsageColor(90), 'bg-rose-500')
  assert.equal(getMemoryUsageColor(100), 'bg-rose-500')
})

test('filterEnvContent accurately filters .env key-value lines', () => {
  const envSample = [
    '# Database configuration',
    'DB_HOST=localhost',
    'DB_PORT=5432',
    'DB_USER=docktun_admin',
    'DB_PASS=secret123',
    '',
    '# App settings',
    'APP_ENV=production',
    'PORT=8080',
    'LOG_LEVEL=info',
  ].join('\n')

  // Search by key
  const dbMatches = filterEnvContent(envSample, 'DB_')
  assert.equal(dbMatches.length, 4)
  assert.equal(dbMatches[0].lineNum, 2)
  assert.equal(dbMatches[0].line, 'DB_HOST=localhost')

  // Search by value (case-insensitive)
  const prodMatches = filterEnvContent(envSample, 'PRODUCTION')
  assert.equal(prodMatches.length, 1)
  assert.equal(prodMatches[0].line, 'APP_ENV=production')

  // Empty or whitespace query
  assert.deepEqual(filterEnvContent(envSample, ''), [])
  assert.deepEqual(filterEnvContent(envSample, '   '), [])
  assert.deepEqual(filterEnvContent('', 'PORT'), [])

  // Non-matching query
  assert.deepEqual(filterEnvContent(envSample, 'NON_EXISTENT_KEY'), [])
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

  // 7. Null/undefined selectedContainerIds does not throw
  assert.deepEqual(calculateStackSelection(stack, null), {
    isSelected: false,
    isIndeterminate: false,
    selectedCount: 0,
    totalCount: 3,
  })
  assert.deepEqual(toggleStackSelection(stack, null), {
    'app-1': true,
    'app-2': true,
    'app-3': true,
  })

  // 8. Gracefully ignores null or malformed containers in stack
  const messyStack = [{ id: 'app-1' }, null, undefined, { notAnId: 'xyz' }, { id: 'app-2' }]
  const messyState = calculateStackSelection(messyStack, { 'app-1': true, 'app-2': true })
  assert.equal(messyState.isSelected, true)
  assert.equal(messyState.isIndeterminate, false)
  assert.equal(messyState.selectedCount, 2)
  assert.equal(messyState.totalCount, 2)
})

test('Selection math remains 100% accurate when stacks are filtered or collapsed', () => {
  // Scenario: Two stacks with containers, simulating collapsed and filtered views
  const stackA = [
    { id: 'web-1', project_name: 'frontend-stack' },
    { id: 'web-2', project_name: 'frontend-stack' },
  ]
  const stackB = [
    { id: 'db-1', project_name: 'backend-stack' },
    { id: 'db-2', project_name: 'backend-stack' },
    { id: 'redis-1', project_name: 'backend-stack' },
  ]
  const allContainers = [...stackA, ...stackB]

  // 1. Collapsed stack selection math:
  // Stack A is collapsed, Stack B is expanded
  const collapsedMap = { 'frontend-stack': true, 'backend-stack': false }
  assert.equal(collapsedMap['frontend-stack'], true)

  // Selecting in collapsed stack A works accurately
  let selection = {}
  selection = toggleStackSelection(stackA, selection)
  assert.deepEqual(selection, { 'web-1': true, 'web-2': true })

  // Stack A is fully selected, Stack B is completely unselected
  let stackAState = calculateStackSelection(stackA, selection)
  let stackBState = calculateStackSelection(stackB, selection)
  assert.equal(stackAState.isSelected, true)
  assert.equal(stackAState.isIndeterminate, false)
  assert.equal(stackBState.isSelected, false)
  assert.equal(stackBState.isIndeterminate, false)

  // Partial select in Stack B (1 of 3)
  selection['db-1'] = true
  stackBState = calculateStackSelection(stackB, selection)
  assert.equal(stackBState.isSelected, false)
  assert.equal(stackBState.isIndeterminate, true)
  assert.equal(stackBState.selectedCount, 1)
  assert.equal(stackBState.totalCount, 3)

  // Toggling stack B when indeterminate selects all containers in stack B without affecting stack A
  selection = toggleStackSelection(stackB, selection)
  assert.deepEqual(selection, {
    'web-1': true,
    'web-2': true,
    'db-1': true,
    'db-2': true,
    'redis-1': true,
  })

  // 2. Filtered containers selection math:
  // Simulate an active filter (e.g. status='running' or search='db')
  // Visible filtered containers: only db-1 and db-2
  const filteredVisible = [
    { id: 'db-1', project_name: 'backend-stack' },
    { id: 'db-2', project_name: 'backend-stack' },
  ]

  // Even if 'web-1' and 'web-2' remain selected from a previous view,
  // table selection calculation must ONLY reflect the visible filtered containers
  const filteredSelection = { 'web-1': true, 'web-2': true, 'db-1': true }
  const tableState = calculateTableSelection(filteredVisible, filteredSelection)
  assert.equal(tableState.totalCount, 2)
  assert.equal(tableState.selectedCount, 1) // Only db-1 counts towards the visible selection
  assert.equal(tableState.isAllSelected, false)
  assert.equal(tableState.isIndeterminate, true)

  // Selecting all visible filtered containers
  const allFilteredSelected = { 'web-1': true, 'web-2': true, 'db-1': true, 'db-2': true }
  const tableAllState = calculateTableSelection(filteredVisible, allFilteredSelected)
  assert.equal(tableAllState.totalCount, 2)
  assert.equal(tableAllState.selectedCount, 2)
  assert.equal(tableAllState.isAllSelected, true)
  assert.equal(tableAllState.isIndeterminate, false)

  // Toggling table select-all on filtered containers
  const toggledOff = toggleTableSelection(filteredVisible, allFilteredSelected)
  assert.deepEqual(toggledOff, {})

  const toggledOn = toggleTableSelection(filteredVisible, {})
  assert.deepEqual(toggledOn, { 'db-1': true, 'db-2': true })

  // Null/undefined robustness for calculateTableSelection and toggleTableSelection
  assert.deepEqual(calculateTableSelection(filteredVisible, null), {
    isAllSelected: false,
    isIndeterminate: false,
    selectedCount: 0,
    totalCount: 2,
  })
  assert.deepEqual(calculateTableSelection([], null), {
    isAllSelected: false,
    isIndeterminate: false,
    selectedCount: 0,
    totalCount: 0,
  })
  assert.deepEqual(toggleTableSelection(null, {}), {})
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

test('Stack filtering logic excludes empty stacks when status filters or search queries are active', () => {
  // 1. shouldIncludeEmptyProjects predicate
  // Active container filters (all, running, stopped, etc.) strictly reject empty stacks
  assert.equal(shouldIncludeEmptyProjects('all', ''), false)
  assert.equal(shouldIncludeEmptyProjects(undefined, undefined), false)
  assert.equal(shouldIncludeEmptyProjects('', ''), false)
  assert.equal(shouldIncludeEmptyProjects('all', '   '), false)
  assert.equal(shouldIncludeEmptyProjects('ALL', ''), false)
  assert.equal(shouldIncludeEmptyProjects('  all  ', ''), false)
  assert.equal(shouldIncludeEmptyProjects({ statusFilter: 'all', searchQuery: '' }), false)
  assert.equal(shouldIncludeEmptyProjects({ statusFilter: 'ALL', searchQuery: '   ' }), false)
  assert.equal(shouldIncludeEmptyProjects({ filter: 'all' }), false)

  // Explicit 'inactive' mode or includeEmpty / includeInactive flags accept empty stacks
  assert.equal(shouldIncludeEmptyProjects('inactive', ''), true)
  assert.equal(shouldIncludeEmptyProjects('INACTIVE', ''), true)
  assert.equal(shouldIncludeEmptyProjects('  inactive  ', ''), true)
  assert.equal(shouldIncludeEmptyProjects({ statusFilter: 'inactive' }), true)
  assert.equal(shouldIncludeEmptyProjects({ filter: 'inactive' }), true)
  assert.equal(shouldIncludeEmptyProjects({ includeEmpty: true }), true)
  assert.equal(shouldIncludeEmptyProjects({ includeInactive: true }), true)

  // Status filters: strictly false for ALL active filter modes
  const allFilterModes = ['all', 'running', 'stopped', 'exposed', 'updates', 'local', 'public', 'private']
  for (const mode of allFilterModes) {
    assert.equal(shouldIncludeEmptyProjects(mode, ''), false, `mode ${mode} must reject empty projects`)
    assert.equal(shouldIncludeEmptyProjects(mode.toUpperCase(), ''), false, `mode ${mode.toUpperCase()} must reject empty projects`)
    assert.equal(shouldIncludeEmptyProjects(`  ${mode}  `, ''), false)
    assert.equal(shouldIncludeEmptyProjects({ statusFilter: mode }), false)
    assert.equal(shouldIncludeEmptyProjects({ filter: mode }), false)
    assert.equal(shouldIncludeEmptyProjects({ statusFilter: mode, searchQuery: '' }), false)
  }

  // Non-empty search query on active container filters
  assert.equal(shouldIncludeEmptyProjects('all', 'redis'), false)
  assert.equal(shouldIncludeEmptyProjects('all', '  web '), false)
  assert.equal(shouldIncludeEmptyProjects('', 'nginx'), false)
  assert.equal(shouldIncludeEmptyProjects({ statusFilter: 'all', searchQuery: 'stack' }), false)
  assert.equal(shouldIncludeEmptyProjects({ search: 'docktun' }), false)

  // 2. filterContainerGroups with mock groups
  const activeStack = {
    key: 'project:1',
    name: 'web-stack',
    isStack: true,
    projectId: 1,
    containers: [{ id: 'c1', name: 'web', running: true }],
    runningCount: 1,
    totalCount: 1,
  }
  const emptyStackA = {
    key: 'project:2',
    name: 'aio-stack',
    isStack: true,
    projectId: 2,
    containers: [],
    runningCount: 0,
    totalCount: 0,
  }
  const emptyStackB = {
    key: 'project:3',
    name: 'sencho',
    isStack: true,
    projectId: 3,
    containers: [],
    runningCount: 0,
    totalCount: 0,
  }
  const emptyStackWithStaleCount = {
    key: 'project:4',
    name: 'stale-count-stack',
    isStack: true,
    projectId: 4,
    containers: [],
    runningCount: 0,
    totalCount: 5,
  }
  const standaloneGroup = {
    key: '__standalone__',
    name: 'Standalone Containers',
    isStack: false,
    containers: [{ id: 'c2', name: 'redis', running: false }],
    runningCount: 0,
    totalCount: 1,
  }

  const allGroups = [activeStack, emptyStackA, emptyStackB, emptyStackWithStaleCount, standaloneGroup]

  // When filter is 'all', empty registered stacks are strictly omitted so Compose Down does not leave ghost headers
  const allResult = filterContainerGroups(allGroups, 'all', '')
  assert.equal(allResult.length, 2)
  assert.deepEqual(
    allResult.map((g) => g.name),
    ['web-stack', 'Standalone Containers']
  )

  // When filter is 'inactive', ONLY empty stacks are returned
  const inactiveResult = filterContainerGroups(allGroups, 'inactive', '')
  assert.equal(inactiveResult.length, 3)
  assert.deepEqual(
    inactiveResult.map((g) => g.name),
    ['aio-stack', 'sencho', 'stale-count-stack']
  )

  // When filter is 'inactive' and searchQuery is given, filters inactive stacks by name
  const inactiveSearchResult = filterContainerGroups(allGroups, 'inactive', 'aio')
  assert.equal(inactiveSearchResult.length, 1)
  assert.equal(inactiveSearchResult[0].name, 'aio-stack')

  // When explicitInclude flag is set, all groups are returned
  const explicitResult = filterContainerGroups(allGroups, { includeEmpty: true })
  assert.equal(explicitResult.length, 5)

  // Verify EVERY active filter mode strictly eliminates all empty stacks
  for (const mode of allFilterModes) {
    const res = filterContainerGroups(allGroups, mode, '')
    assert.equal(res.length, 2, `filter mode '${mode}' must return only non-empty groups`)
    assert.deepEqual(
      res.map((g) => g.name),
      ['web-stack', 'Standalone Containers'],
      `filter mode '${mode}' included empty stacks!`
    )
    assert.equal(res.some((g) => !g.containers || g.containers.length === 0), false)

    // Also verify with options object format
    const resObj = filterContainerGroups(allGroups, { statusFilter: mode, searchQuery: '' })
    assert.equal(resObj.length, 2)
  }

  // When searchQuery is non-empty on 'all', empty stacks must NEVER be returned
  const searchResult = filterContainerGroups(allGroups, 'all', 'web')
  assert.equal(searchResult.length, 1)
  assert.equal(searchResult[0].name, 'web-stack')
  assert.equal(searchResult.some((g) => g.containers.length === 0), false)

  // Edge cases: handles null, undefined, invalid elements inside groups
  const mixedGroups = [activeStack, null, undefined, {}, { containers: [] }, standaloneGroup]
  const filteredMixed = filterContainerGroups(mixedGroups, 'running')
  assert.equal(filteredMixed.length, 2)
  assert.deepEqual(filteredMixed.map((g) => g.name), ['web-stack', 'Standalone Containers'])

  // Gracefully handles empty or malformed inputs
  assert.deepEqual(filterContainerGroups(null), [])
  assert.deepEqual(filterContainerGroups(undefined), [])
  assert.deepEqual(filterContainerGroups([]), [])
})

test('Singular and plural count formatting for stacks, containers and table summary', () => {
  // 1. Stack count formatting
  assert.equal(formatStackCount(1), '1 stack / group')
  assert.equal(formatStackCount(0), '0 stacks / groups')
  assert.equal(formatStackCount(2), '2 stacks / groups')
  assert.equal(formatStackCount(10), '10 stacks / groups')
  // Edge cases: NaN, null, undefined
  assert.equal(formatStackCount(NaN), '0 stacks / groups')
  assert.equal(formatStackCount(null), '0 stacks / groups')
  assert.equal(formatStackCount(undefined), '0 stacks / groups')

  // 2. Container count formatting
  assert.equal(formatContainerCount(1), '1 container')
  assert.equal(formatContainerCount(0), '0 containers')
  assert.equal(formatContainerCount(2), '2 containers')
  assert.equal(formatContainerCount(42), '42 containers')
  // Edge cases: NaN, null, undefined
  assert.equal(formatContainerCount(NaN), '0 containers')
  assert.equal(formatContainerCount(null), '0 containers')
  assert.equal(formatContainerCount(undefined), '0 containers')

  // 3. Combined table summary line formatting
  // 1 stack, 1 container (both singular)
  assert.equal(formatTableSummary(1, 1), 'Showing 1 stack / group (1 container)')
  // Multiple stacks, 1 container (stack plural, container singular)
  assert.equal(formatTableSummary(2, 1), 'Showing 2 stacks / groups (1 container)')
  // 1 stack, multiple containers (stack singular, container plural)
  assert.equal(formatTableSummary(1, 5), 'Showing 1 stack / group (5 containers)')
  // Multiple stacks, multiple containers (both plural)
  assert.equal(formatTableSummary(3, 12), 'Showing 3 stacks / groups (12 containers)')
  // Zero stacks, zero containers
  assert.equal(formatTableSummary(0, 0), 'Showing 0 stacks / groups (0 containers)')
})

test('formatImageIdentifier correctly formats bare sha/hex IDs and untagged images', () => {
  // 1. Bare 12-char hex ID (e.g. '18328305a553')
  const bare12 = formatImageIdentifier('18328305a553')
  assert.equal(bare12.isUntagged, true)
  assert.equal(bare12.isBareId, true)
  assert.equal(bare12.shortId, '18328305a553')
  assert.equal(bare12.displayName, '18328305a553')

  // 2. Bare 64-char full SHA hex
  const fullHex = '18328305a5534c0e9e1234567890abcdef18328305a5534c0e9e1234567890abcdef'
  const bare64 = formatImageIdentifier(fullHex)
  assert.equal(bare64.isUntagged, true)
  assert.equal(bare64.isBareId, true)
  assert.equal(bare64.shortId, '18328305a553')
  assert.equal(bare64.displayName, '18328305a553')

  // 3. sha256: prefixed hash
  const shaPrefixed = formatImageIdentifier('sha256:18328305a5534c0e9e1234567890abcdef')
  assert.equal(shaPrefixed.isUntagged, true)
  assert.equal(shaPrefixed.isBareId, true)
  assert.equal(shaPrefixed.shortId, '18328305a553')
  assert.equal(shaPrefixed.displayName, '18328305a553')

  // 4. Untagged / placeholder formats
  const noneNone = formatImageIdentifier('<none>:<none>')
  assert.equal(noneNone.isUntagged, true)
  assert.equal(noneNone.isBareId, false)
  assert.equal(noneNone.displayName, 'untagged')

  const none = formatImageIdentifier('<none>')
  assert.equal(none.isUntagged, true)
  assert.equal(none.isBareId, false)

  const empty = formatImageIdentifier('')
  assert.equal(empty.isUntagged, true)
  assert.equal(empty.isBareId, false)

  const nullVal = formatImageIdentifier(null)
  assert.equal(nullVal.isUntagged, true)
  assert.equal(nullVal.isBareId, false)

  // 5. Standard tagged images
  const standard = formatImageIdentifier('nginx:latest')
  assert.equal(standard.isUntagged, false)
  assert.equal(standard.isBareId, false)
  assert.equal(standard.displayName, 'nginx:latest')

  const registry = formatImageIdentifier('cloudflare/cloudflared:2024.1.0')
  assert.equal(registry.isUntagged, false)
  assert.equal(registry.isBareId, false)
  assert.equal(registry.displayName, 'cloudflare/cloudflared:2024.1.0')
})


