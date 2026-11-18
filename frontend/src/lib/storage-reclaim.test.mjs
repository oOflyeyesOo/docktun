import test from 'node:test'
import assert from 'node:assert/strict'

function formatBytesClient(bytes) {
  if (!bytes || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(1024))
  const val = (bytes / Math.pow(1024, i)).toFixed(2)
  return `${val} ${units[i]}`
}

function computeReclaimEstimate(options, preview) {
  if (!preview) return '0 B'
  let totalBytes = 0
  if (options.prune_images_all) {
    totalBytes += preview.images_reclaim_bytes || 0
  } else if (options.prune_images_dangling) {
    totalBytes += Math.round((preview.images_reclaim_bytes || 0) * 0.4)
  }
  if (options.prune_containers) {
    totalBytes += preview.containers_reclaim_bytes || 0
  }
  if (options.prune_volumes) {
    totalBytes += preview.volumes_reclaim_bytes || 0
  }
  if (options.prune_build_cache) {
    totalBytes += preview.build_cache_reclaim_bytes || 0
  }
  if (totalBytes === 0) return '0 B'
  return formatBytesClient(totalBytes)
}

function convertMemoryPresetToBytes(mode, customMb = 512) {
  switch (mode) {
    case '256m': return 256 * 1024 * 1024
    case '512m': return 512 * 1024 * 1024
    case '1g': return 1024 * 1024 * 1024
    case '2g': return 2048 * 1024 * 1024
    case '4g': return 4096 * 1024 * 1024
    case 'custom': return Math.max(4, customMb) * 1024 * 1024
    case 'unlimited':
    default:
      return 0
  }
}

test('formatBytesClient formats byte values into accurate SI units', () => {
  assert.equal(formatBytesClient(0), '0 B')
  assert.equal(formatBytesClient(-100), '0 B')
  assert.equal(formatBytesClient(1024), '1.00 KB')
  assert.equal(formatBytesClient(1048576), '1.00 MB')
  assert.equal(formatBytesClient(1073741824), '1.00 GB')
  assert.equal(formatBytesClient(1073741824 * 10), '10.00 GB')
})

test('computeReclaimEstimate dynamically aggregates reclaimable bytes per selected options', () => {
  const preview = {
    images_reclaim_bytes: 10 * 1024 * 1024 * 1024,      // 10 GB
    containers_reclaim_bytes: 500 * 1024 * 1024,        // 500 MB
    volumes_reclaim_bytes: 20 * 1024 * 1024 * 1024,     // 20 GB
    build_cache_reclaim_bytes: 5 * 1024 * 1024 * 1024   // 5 GB
  }

  // All unchecked
  assert.equal(computeReclaimEstimate({
    prune_images_dangling: false,
    prune_images_all: false,
    prune_containers: false,
    prune_volumes: false,
    prune_build_cache: false
  }, preview), '0 B')

  // Dangling images + build cache (default safe profile)
  const safeProfile = computeReclaimEstimate({
    prune_images_dangling: true,
    prune_images_all: false,
    prune_containers: false,
    prune_volumes: false,
    prune_build_cache: true
  }, preview)
  // 4GB dangling + 5GB cache = 9GB
  assert.equal(safeProfile, '9.00 GB')

  // All unused images + stopped containers + build cache + volumes
  const fullProfile = computeReclaimEstimate({
    prune_images_dangling: false,
    prune_images_all: true,
    prune_containers: true,
    prune_volumes: true,
    prune_build_cache: true
  }, preview)
  // 10GB + 0.488GB + 20GB + 5GB = ~35.49 GB
  assert.ok(fullProfile.includes('35.49 GB'))
})

test('convertMemoryPresetToBytes maps presets and custom memory values correctly', () => {
  assert.equal(convertMemoryPresetToBytes('unlimited'), 0)
  assert.equal(convertMemoryPresetToBytes('256m'), 268435456)
  assert.equal(convertMemoryPresetToBytes('512m'), 536870912)
  assert.equal(convertMemoryPresetToBytes('1g'), 1073741824)
  assert.equal(convertMemoryPresetToBytes('2g'), 2147483648)
  assert.equal(convertMemoryPresetToBytes('4g'), 4294967296)
  assert.equal(convertMemoryPresetToBytes('custom', 768), 768 * 1024 * 1024)
  // Enforces 4MB minimum for custom
  assert.equal(convertMemoryPresetToBytes('custom', 1), 4 * 1024 * 1024)
})
