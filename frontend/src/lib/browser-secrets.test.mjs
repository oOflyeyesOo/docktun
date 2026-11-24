import test from 'node:test'
import assert from 'node:assert/strict'
import { clearLegacyCloudflareToken } from './browser-secrets.mjs'

test('upgrade removes the legacy provider secret without logging out or changing preferences', () => {
  const saved = new Map([
    ['docktun_cf_api_token', 'legacy-secret'],
    ['token', 'active-session'],
    ['refreshToken', 'refresh-session'],
    ['docktun_group_by_stack', 'false'],
  ])
  clearLegacyCloudflareToken(() => ({ removeItem: key => saved.delete(key) }))
  clearLegacyCloudflareToken(() => ({ removeItem: key => saved.delete(key) }))
  assert.deepEqual([...saved], [
    ['token', 'active-session'],
    ['refreshToken', 'refresh-session'],
    ['docktun_group_by_stack', 'false'],
  ])
})

test('unavailable or denied browser storage does not stop startup', () => {
  assert.doesNotThrow(() => clearLegacyCloudflareToken(() => { throw new Error('storage unavailable') }))
  assert.doesNotThrow(() => clearLegacyCloudflareToken(() => ({ removeItem() { throw new Error('denied') } })))
})
