import test from 'node:test'
import assert from 'node:assert/strict'
import { defaultComposeExample, composeTemplates } from './compose-templates.mjs'

test('new-stack examples use Compose networking without requiring an exposure provider', () => {
  assert.equal(new Set(composeTemplates.map(template => template.id)).size, composeTemplates.length)
  for (const compose of [defaultComposeExample, ...composeTemplates.map(template => template.compose)]) {
    assert.match(compose, /^services:/)
    assert.doesNotMatch(compose, /cloudflare|tailscale|wireguard|external:\s*true/i)
  }
  const postgres = composeTemplates.find(template => template.id === 'postgres-db')
  assert.match(postgres.compose, /depends_on:\s*\n\s*- postgres/)
  assert.match(postgres.compose, /postgres_data:/)
  assert.deepEqual(postgres.ports, ['5050:80'])
  assert.deepEqual(composeTemplates.find(template => template.id === 'redis-cache').ports, ['8081:8081'])
  assert.deepEqual(composeTemplates.find(template => template.id === 'movie-night').ports, ['3000:3000'])
})

test('unpublished Movie Night companion is not advertised as deployable', () => {
 assert.match(composeTemplates.find(t => t.id === 'movie-night').unavailable, /not published/)
})
