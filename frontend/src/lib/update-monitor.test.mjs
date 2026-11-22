import test from 'node:test'
import assert from 'node:assert/strict'
import { updateMonitorOutcome } from './update-monitor.mjs'

test('worker outcome requires completed phase and preserves rollback failure', () => {
  assert.equal(updateMonitorOutcome({ phase: 'pulling', healthy: true }), 'pending')
  assert.equal(updateMonitorOutcome({ phase: 'restarting', healthy: true }), 'pending')
  assert.equal(updateMonitorOutcome({ phase: 'complete' }), 'complete')
  assert.equal(updateMonitorOutcome({ phase: 'rolled_back' }), 'failed')
  assert.equal(updateMonitorOutcome({ phase: 'failed' }), 'failed')
  assert.equal(updateMonitorOutcome({ phase: 'ready' }), 'idle')
})
