import test from 'node:test'
import assert from 'node:assert/strict'
import { settingsEqual, syncExposureDraft } from './exposure-draft.mjs'

test('exposure equality ignores field order while retaining field values and types', () => {
  assert.equal(settingsEqual({ mode: 'public', hostname: 'app.example.com', private_port: 80 }, { private_port: 80, hostname: 'app.example.com', mode: 'public' }), true)
  assert.equal(settingsEqual({ private_port: 80 }, { private_port: '80' }), false)
  assert.equal(settingsEqual({ enabled: false }, { enabled: true }), false)
  assert.equal(settingsEqual({ hostname: '' }, {}), false)
  assert.equal(settingsEqual(null, {}), false)
})

test('remote discovery refreshes a pristine exposure form after a cold cache', () => {
  const baseline = { mode: 'internal', hostname: '', target: 'http://app:8080' }
  const current = { target: 'http://app:8080', hostname: '', mode: 'internal' }
  const remote = { mode: 'public', hostname: 'app.example.com', target: 'http://app:8080' }
  const result = syncExposureDraft(current, baseline, remote)
  assert.deepEqual(result.draft, remote)
  assert.deepEqual(result.baseline, remote)
  assert.notEqual(result.draft, current)
  assert.deepEqual(current, { target: 'http://app:8080', hostname: '', mode: 'internal' })
})

test('new exposure forms clone the fetched settings into separate draft and baseline objects', () => {
  const fresh = Object.freeze({ mode: 'public', hostname: 'app.example.com', private_port: 80 })
  const result = syncExposureDraft(undefined, undefined, fresh)
  assert.deepEqual(result.draft, fresh)
  assert.deepEqual(result.baseline, fresh)
  assert.notEqual(result.draft, fresh)
  assert.notEqual(result.baseline, fresh)
  assert.notEqual(result.draft, result.baseline)
  result.draft.private_port = 8080
  assert.equal(fresh.private_port, 80)
  assert.equal(result.baseline.private_port, 80)
})

test('remote refresh preserves unsaved port, provider, and hostname edits with their old baseline', () => {
  const baseline = Object.freeze({ mode: 'public', hostname: 'app.example.com', private_port: 80, private_type: '' })
  const fresh = Object.freeze({ mode: 'public', hostname: 'discovered.example.com', private_port: 8080, private_type: '' })
  for (const edit of [{ private_port: 9000 }, { mode: 'private', private_type: 'tailscale' }, { hostname: 'draft.example.com' }]) {
    const current = Object.freeze({ ...baseline, ...edit })
    const result = syncExposureDraft(current, baseline, fresh)
    assert.equal(result.draft, current)
    assert.equal(result.baseline, baseline)
    assert.deepEqual(result.draft, { ...baseline, ...edit })
    assert.equal(fresh.hostname, 'discovered.example.com')
  }
})

test('pristine refresh does not mutate the response or share mutable draft state with its baseline', () => {
  const baseline = Object.freeze({ mode: 'internal', private_port: 80 })
  const current = Object.freeze({ ...baseline })
  const fresh = Object.freeze({ mode: 'public', hostname: 'remote.example.com', private_port: 8080 })
  const result = syncExposureDraft(current, baseline, fresh)
  result.draft.hostname = 'edited.example.com'
  assert.equal(fresh.hostname, 'remote.example.com')
  assert.equal(result.baseline.hostname, 'remote.example.com')
  assert.deepEqual(current, baseline)
})
