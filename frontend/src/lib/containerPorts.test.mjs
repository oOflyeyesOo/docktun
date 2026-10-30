import assert from 'node:assert/strict'
import test from 'node:test'
import { getPrimaryPort, cleanContainerName, getMdnsPortSuffix, hasLocalWebLink, localAddress } from './containerPorts.mjs'

test('local addresses do not promise a web page for database or UDP listeners', () => {
  for (const ports of ['0.0.0.0:5432->5432/tcp, [::]:5432->5432/tcp', '3306/tcp', '6379/tcp', '51820/udp', '53/tcp, 53/udp', '']) {
    assert.equal(hasLocalWebLink(ports), false, ports)
    assert.equal(localAddress({ name: '/Database', ports }), 'database.local')
  }
  for (const ports of ['127.0.0.1:18080->80/tcp', '3001/tcp', '12345/tcp', '5432/tcp, 8080/tcp']) {
    assert.equal(hasLocalWebLink(ports), true, ports)
    assert.equal(localAddress({ name: 'App', ports }), 'http://app.local')
  }
})

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
