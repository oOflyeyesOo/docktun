import test from 'node:test'
import assert from 'node:assert/strict'
import { createProjectFileEditor } from './project-file-editor.mjs'
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r }); return { promise, resolve } }

test('late project response cannot replace the current editor', async () => {
  const editor = createProjectFileEditor('Compose')
  const old = deferred()
  const pending = editor.load(1, () => old.promise)
  await editor.load(2, async () => ({ content: 'stack two' }))
  old.resolve({ content: 'stack one' })
  await pending
  assert.equal(editor.projectId, 2)
  assert.equal(editor.content, 'stack two')
})

test('failed load clears previous content and disables saving', async () => {
  const editor = createProjectFileEditor('Compose')
  await editor.load(1, async () => ({ content: 'old stack' }))
  await editor.load(2, async () => { throw new Error('folder not mounted') })
  assert.equal(editor.content, '')
  assert.equal(editor.loaded, false)
  assert.match(editor.message, /folder not mounted/)
  assert.equal(await editor.save(() => assert.fail('must not write stale content')), false)
})

test('save and restart keep original project after navigating away', async () => {
  const editor = createProjectFileEditor('Compose')
  await editor.load(1, async () => ({ content: 'first', filename: 'compose.yml' }))
  const write = deferred()
  const calls = []
  const pending = editor.save((id, body) => { calls.push([id, body]); return write.promise }, async id => calls.push(id))
  await editor.load(2, async () => ({ content: 'second' }))
  write.resolve()
  assert.equal(await pending, true)
  assert.deepEqual(calls, [[1, { content: 'first', filename: 'compose.yml' }], 1])
  assert.equal(editor.content, 'second')
  assert.equal(editor.message, '')
})

test('empty env remains saveable and closed editor ignores pending load', async () => {
  const editor = createProjectFileEditor('.env')
  await editor.load(1, async () => ({ content: '' }))
  assert.equal(await editor.save(async (id, body) => { assert.equal(id, 1); assert.equal(body.content, '') }), true)
  const read = deferred()
  const pending = editor.load(2, () => read.promise)
  editor.invalidate()
  read.resolve({ content: 'secret' })
  await pending
  assert.equal(editor.content, '')
  assert.equal(editor.loaded, false)
  assert.equal(editor.projectId, null)
})
