import { test } from 'node:test'
import assert from 'node:assert/strict'
import { remoteAccessTab, cloudflareEnabled } from './tunnel-provider.mjs'

test('onboarding selection opens the matching remote access tab', () => {
  assert.equal(remoteAccessTab(undefined, 'tailscale'), 'tailscale')
  assert.equal(remoteAccessTab(undefined, 'wireguard'), 'wireguard')
  assert.equal(remoteAccessTab(undefined, 'public'), 'cloudflare')
  assert.equal(remoteAccessTab('cloudflare', 'tailscale'), 'cloudflare')
  assert.equal(remoteAccessTab('invalid', 'tailscale'), 'tailscale')
})

test('disabled Cloudflare has no degraded warning or controls', () => {
  assert.equal(cloudflareEnabled(null), false)
  assert.equal(cloudflareEnabled({status: 'disabled'}), false)
  assert.equal(cloudflareEnabled({status: 'not found'}), true)
  assert.equal(cloudflareEnabled({status: 'running'}), true)
})

test('no provider selection opens a neutral overview', () => {
 assert.equal(remoteAccessTab(undefined, 'none'), 'overview')
 assert.equal(remoteAccessTab(undefined, undefined), 'overview')
 assert.equal(remoteAccessTab(undefined, 'cloudflare'), 'cloudflare')
})
