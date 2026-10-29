import assert from 'node:assert/strict'
import test from 'node:test'
import { getPrimaryPort, cleanContainerName, getMdnsPortSuffix } from './containerPorts.mjs'

test('exposure uses container TCP port rather than the published host port', () => {
  assert.equal(getPrimaryPort('127.0.0.1:18083->3001/tcp'), '3001')
  assert.equal(getPrimaryPort('[::]:8443->443/tcp, 0.0.0.0:8080->80/tcp'), '443')
  assert.equal(getPrimaryPort('53/udp, 9000/tcp'), '9000')
  assert.equal(getPrimaryPort('8000-8001/tcp'), '8000')
  assert.equal(getPrimaryPort(''), '80')
})

test('cleanContainerName strips leading slash and lowercases', () => {
  assert.equal(cleanContainerName('/nuvio-web'), 'nuvio-web')
  assert.equal(cleanContainerName('My-App'), 'my-app')
  assert.equal(cleanContainerName(''), '')
})

test('getMdnsPortSuffix always returns empty for portless mDNS gateway routing', () => {
  assert.equal(getMdnsPortSuffix('0.0.0.0:80->80/tcp'), '')
  assert.equal(getMdnsPortSuffix('8080->80/tcp'), '')
  assert.equal(getMdnsPortSuffix('127.0.0.1:3000->3000/tcp'), '')
  assert.equal(getMdnsPortSuffix('80/tcp'), '')
  assert.equal(getMdnsPortSuffix(''), '')
})
