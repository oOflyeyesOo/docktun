import { test } from 'node:test'
import assert from 'node:assert/strict'
import { authenticatedResponse } from './api-response.mjs'

test('multipart upload keeps browser boundary and retries refreshed authentication', async () => {
  const body = new FormData(); body.append('file', new Blob(['binary']), 'file.bin')
  let calls = 0
  const response = await authenticatedResponse('/api/storage/files/upload', { method: 'POST', body }, { token: 'expired', refreshToken: 'refresh' }, async () => 'new-token', () => assert.fail('unexpected logout'), async (path, options) => {
    assert.equal(path.includes('token='), false)
    assert.equal(options.body, body)
    assert.equal(options.headers.has('Content-Type'), false)
    assert.equal(options.headers.get('Authorization'), calls ? 'Bearer new-token' : 'Bearer expired')
    return new Response('{}', { status: calls++ ? 200 : 401 })
  })
  assert.equal(response.status, 200); assert.equal(calls, 2)
})
test('binary downloads retain exact bytes and never put credentials in URLs', async () => {
  const bytes = new Uint8Array([0, 255, 10, 128])
  const response = await authenticatedResponse('/api/storage/files/download?path=a', {}, { token: 'secret' }, async () => null, () => {}, async (path, options) => {
    assert.equal(path.includes('secret'), false); assert.equal(options.headers.get('Authorization'), 'Bearer secret')
    return new Response(bytes)
  })
  assert.deepEqual(new Uint8Array(await response.arrayBuffer()), bytes)
})
test('failed upload reports server error without logging out a valid session', async () => {
  await assert.rejects(authenticatedResponse('/api/storage/files/upload', {}, { token: 'valid' }, async () => null, () => assert.fail(), async () => new Response('{"error":"disk is full"}', { status: 500 })), /disk is full/)
})
