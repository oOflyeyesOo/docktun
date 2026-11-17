import test from 'node:test'
import assert from 'node:assert/strict'
import {
  getExportFilename,
  createCommandHistory,
  exitCodeBadgeClass,
  formatFileSize,
  filterFileList,
} from './inspector-tabs.mjs'

test('getExportFilename correctly formats container export filenames and strips leading slash', () => {
  assert.equal(getExportFilename('/web-app', '1234567890ab', '-logs.txt'), 'web-app-logs.txt')
  assert.equal(getExportFilename('database', 'abcdef123456', '-inspect.json'), 'database-inspect.json')
  assert.equal(getExportFilename('', 'abcdef123456', '-logs.txt'), 'abcdef123456-logs.txt')
  assert.equal(getExportFilename('', '', '-logs.txt'), 'container-logs.txt')
})

test('command history navigation cycles upwards and downwards through history', () => {
  const manager = createCommandHistory()
  manager.push('ls -la')
  manager.push('cat /etc/hosts')
  manager.push('uptime')

  // History order: uptime (0), cat /etc/hosts (1), ls -la (2)
  assert.deepEqual(manager.history, ['uptime', 'cat /etc/hosts', 'ls -la'])

  // User typed a draft command and presses ArrowUp
  const up1 = manager.navigateUp('cur')
  assert.equal(up1, 'uptime')
  assert.equal(manager.index, 0)

  const up2 = manager.navigateUp(up1)
  assert.equal(up2, 'cat /etc/hosts')
  assert.equal(manager.index, 1)

  const up3 = manager.navigateUp(up2)
  assert.equal(up3, 'ls -la')
  assert.equal(manager.index, 2)

  // Stays at oldest
  const up4 = manager.navigateUp(up3)
  assert.equal(up4, 'ls -la')
  assert.equal(manager.index, 2)

  // Navigate back down
  const down1 = manager.navigateDown()
  assert.equal(down1, 'cat /etc/hosts')
  assert.equal(manager.index, 1)

  const down2 = manager.navigateDown()
  assert.equal(down2, 'uptime')
  assert.equal(manager.index, 0)

  // Back to draft
  const down3 = manager.navigateDown()
  assert.equal(down3, 'cur')
  assert.equal(manager.index, -1)

  // Further down stays at draft
  const down4 = manager.navigateDown()
  assert.equal(down4, 'cur')
})

test('command history deduplicates and moves executed commands to top', () => {
  const manager = createCommandHistory()
  manager.push('ls')
  manager.push('pwd')
  manager.push('ls')

  assert.deepEqual(manager.history, ['ls', 'pwd'])
  assert.equal(manager.history.length, 2)
})

test('exit code classification returns emerald for 0 and rose for non-zero codes', () => {
  assert.match(exitCodeBadgeClass(0), /emerald/)
  assert.doesNotMatch(exitCodeBadgeClass(0), /rose/)

  assert.match(exitCodeBadgeClass(1), /rose/)
  assert.match(exitCodeBadgeClass(137), /rose/)
  assert.match(exitCodeBadgeClass(255), /rose/)
  assert.doesNotMatch(exitCodeBadgeClass(1), /emerald/)
})

test('formatFileSize converts byte values into readable units', () => {
  assert.equal(formatFileSize(0), '0 B')
  assert.equal(formatFileSize(512), '512 B')
  assert.equal(formatFileSize(1024), '1.0 KB')
  assert.equal(formatFileSize(2048), '2.0 KB')
  assert.equal(formatFileSize(1048576), '1.0 MB')
  assert.equal(formatFileSize(1073741824), '1.0 GB')
  assert.equal(formatFileSize(-10), '0 B')
  assert.equal(formatFileSize(NaN), '0 B')
})

test('filterFileList filters files by name case-insensitively', () => {
  const files = [
    { name: 'docker-compose.yml', is_dir: false },
    { name: '.env', is_dir: false },
    { name: 'data', is_dir: true },
    { name: 'config.json', is_dir: false },
  ]

  assert.deepEqual(filterFileList(files, ''), files)
  assert.deepEqual(filterFileList(files, 'compose'), [{ name: 'docker-compose.yml', is_dir: false }])
  assert.deepEqual(filterFileList(files, '.ENV'), [{ name: '.env', is_dir: false }])
  assert.deepEqual(filterFileList(files, 'nonexistent'), [])
})
