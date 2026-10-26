import assert from 'node:assert/strict'
import test from 'node:test'
import { getPrimaryPort } from './containerPorts.mjs'

test('exposure uses container TCP port rather than the published host port', () => {
  assert.equal(getPrimaryPort('127.0.0.1:18083->3001/tcp'), '3001')
  assert.equal(getPrimaryPort('[::]:8443->443/tcp, 0.0.0.0:8080->80/tcp'), '443')
  assert.equal(getPrimaryPort('53/udp, 9000/tcp'), '9000')
  assert.equal(getPrimaryPort('8000-8001/tcp'), '8000')
  assert.equal(getPrimaryPort(''), '80')
})
