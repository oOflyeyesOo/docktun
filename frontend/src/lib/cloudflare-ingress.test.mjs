import test from 'node:test'
import assert from 'node:assert/strict'
import { readIngressYaml, writeIngressYaml, validateIngressConfiguration, originLocation, effectiveOriginOption, setOriginOption } from './cloudflare-ingress.mjs'

const source = `# Keep operator notes
tunnel: original
protocol: quic
originRequest:
  connectTimeout: 15s
  noTLSVerify: true
warp-routing:
  enabled: true
ingress:
  # VM with a special path
  - hostname: app.example.com
    path: /api/.*
    service: https://192.168.1.50:8443
    originRequest:
      originServerName: app.internal
      noTLSVerify: false
      access:
        required: true
  - path: /health
    service: http://other.local:8080
  - service: http_status:503
`

test('editing a VM preserves advanced settings, path-only routes, comments and fallback', () => {
  const { config } = readIngressYaml(source)
  config.ingress[0].service = 'https://192.168.1.60:9443'
  const text = writeIngressYaml(source, config, [0,1,2])
  assert.deepEqual(readIngressYaml(text).config, config)
  assert.match(text, /Keep operator notes/)
  assert.match(text, /VM with a special path/)
  assert.equal(config.ingress[0].originRequest.noTLSVerify, false)
  assert.equal(config.ingress[2].service, 'http_status:503')
})

test('rule reordering retains the right advanced settings and comments', () => {
  const { config } = readIngressYaml(source)
  config.ingress = [config.ingress[1], config.ingress[0], config.ingress[2]]
  const text = writeIngressYaml(source, config, [1,0,2])
  assert.deepEqual(readIngressYaml(text).config, config)
  assert.match(text, /VM with a special path\n\s*- hostname: app.example.com/)
})

test('external origins including IPv6, local DNS and loopback remain exactly as entered', () => {
  for (const target of ['http://192.168.1.50:8080','https://server.local:8443','http://localhost:8080','http://127.0.0.1:9000','http://[fd00::5]:8080']) {
    const { config } = readIngressYaml(source)
    config.ingress[0] = { hostname: 'vm.example.com', service: target }
    assert.deepEqual(validateIngressConfiguration(config), [])
    const saved = readIngressYaml(writeIngressYaml(source, config, [0,1,2])).config
    assert.equal(saved.ingress[0].service, target)
    assert.equal(saved.ingress[0].originRequest, undefined)
  }
})

test('TLS overrides respect inherited settings and do not turn verification off for HTTPS', () => {
  const config = { originRequest: { noTLSVerify: true } }, rule = { service: 'https://vm:443' }
  assert.equal(effectiveOriginOption(config,rule,'noTLSVerify'), true)
  setOriginOption(rule,'noTLSVerify',false)
  setOriginOption(rule,'httpHostHeader','app.internal')
  assert.equal(effectiveOriginOption(config,rule,'noTLSVerify'), false)
  setOriginOption(rule,'httpHostHeader','')
  assert.deepEqual(rule.originRequest, { noTLSVerify: false })
})

test('validation prevents hidden or malformed fallbacks without requiring Docker', () => {
  assert.ok(validateIngressConfiguration({ingress:[{path:'/api',service:'http://vm:80'}]}).length)
  assert.ok(validateIngressConfiguration({ingress:[{service:'http_status:404'},{hostname:'a',service:'http://a:80'}]}).length)
  assert.ok(validateIngressConfiguration({ingress:[{hostname:'https://bad',service:'http://a:80'},{service:'http_status:404'}]}).length)
  assert.deepEqual(validateIngressConfiguration(readIngressYaml(source).config), [])
  assert.throws(() => readIngressYaml('ingress: [broken'))
})

test('origin labels only call known containers Docker containers', () => {
  assert.equal(originLocation('http://192.168.1.50:8080'), 'Network / VM origin')
  assert.equal(originLocation('http://server.local:8080'), 'Network / VM origin')
  assert.equal(originLocation('http://app:80',[{name:'/app'}]), 'Docker container')
  assert.equal(originLocation('http://localhost:80'), 'Connector loopback')
})
