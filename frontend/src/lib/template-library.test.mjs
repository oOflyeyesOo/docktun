import test from 'node:test'
import assert from 'node:assert/strict'
import { createCatalogLoader, defaultStorageChoices, storageGroups, mergeLibraryEntries, libraryCategory } from './template-library.mjs'

test('new app data defaults to created storage and shared read-only mounts retain one choice', () => {
  const mounts = [{ id: 'app', source: '/path/to/config', target: '/data', can_manage: true }, { id: 'worker', source: '/path/to/config', target: '/shared', can_manage: true, read_only: true }]
  assert.equal(storageGroups(mounts).length, 1)
  assert.deepEqual(defaultStorageChoices(mounts), { app: { mode: 'managed', source: '' }, worker: { mode: 'managed', source: '' } })
})

test('files, sockets and related directory layouts are not silently replaced with empty volumes', () => {
  const mounts = [{ id: 'socket', source: '/var/run/docker.sock', can_manage: false }, { id: 'root', source: '/data/app', can_manage: true }, { id: 'child', source: '/data/app/files', can_manage: true }]
  for (const choice of Object.values(defaultStorageChoices(mounts))) assert.equal(choice.mode, 'host')
  assert.equal(storageGroups(mounts).find(group => group.id === 'root').related, true)
  for (const source of ['/', '/proc', '/sys/fs/cgroup', '/dev/dri', '/etc', '/var/lib/docker']) {
    assert.equal(defaultStorageChoices([{ id: 'host', source, can_manage: true }]).host.mode, 'host')
  }
})

test('catalog loads coalesce, reuse data on reopening, refresh and retry failures', async () => {
  let calls = 0, now = 100
  const load = createCatalogLoader(() => now, 50)
  const fetcher = async () => { calls++; return [{ id: String(calls) }] }
  const [a, b] = await Promise.all([load('public', fetcher), load('public', fetcher)])
  assert.equal(calls, 1); assert.equal(a, b)
  await load('public', fetcher); assert.equal(calls, 1)
  await load('public', fetcher, true); assert.equal(calls, 2)
  now += 51
  await assert.rejects(load('public', async () => { throw new Error('offline') }), /offline/)
  assert.equal((await load('public', fetcher))[0].id, '3')
  await assert.rejects(load('broken', async () => ({})), /app list/)
  assert.deepEqual(await load('broken', async () => []), [])
})

test('catalog refresh deduplicates entries and normalized categories preserve useful groups', () => {
  const item = { collection: 'pinned-source', id: 'a', description: 'old' }
  assert.deepEqual(mergeLibraryEntries([item], [{ ...item, description: 'new' }, { ...item, description: 'latest' }]), [{ ...item, description: 'latest' }])
  assert.equal(libraryCategory({ categories: ['Webserver'] }), 'network')
  assert.equal(libraryCategory({ categories: ['Database'] }), 'data')
  assert.equal(libraryCategory({ categories: ['Bookmarks', 'Tools'] }), 'productivity')
  assert.equal(libraryCategory({ categories: ['Other', 'Tools'] }), 'utilities')
  assert.equal(libraryCategory({ name: 'Uptime Kuma', categories: ['Tools', 'Network'] }), 'monitoring')
  assert.equal(libraryCategory({ name: 'Gitea', categories: ['Web', 'Tools'] }), 'development')
  assert.equal(libraryCategory({ name: 'Digital tools', categories: ['Tools'] }), 'utilities')
  assert.equal(libraryCategory({ categories: ['Homeautomation'] }), 'home')
  const old = 'https://raw.githubusercontent.com/owner/catalog/aaaaaaaa/apps.json'
  const updated = old.replace('aaaaaaaa', 'bbbbbbbb')
  const refreshed = mergeLibraryEntries([{ collection: old, id: 'removed' }, { collection: 'another-source', id: 'keep' }], [{ collection: updated, id: 'new' }])
  assert.deepEqual(refreshed.map(item => item.id), ['keep', 'new'])
})
