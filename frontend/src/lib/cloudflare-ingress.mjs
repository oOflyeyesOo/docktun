import { Document, isMap, isSeq, parseDocument } from 'yaml'

export const cloneConfiguration = value => JSON.parse(JSON.stringify(value))

export function readIngressYaml(text) {
  const document = text.trim() ? parseDocument(text, { merge: true }) : new Document({})
  if (document.errors.length) throw new Error(document.errors[0].message)
  if (!isMap(document.contents)) throw new Error('Tunnel configuration must be a YAML mapping')
  const config = document.toJS({ maxAliasCount: 100 })
  return { document, config }
}

function patchMap(document, node, previous, next) {
  for (const key of Object.keys(previous)) if (!(key in next)) node.delete(key)
  for (const [key, value] of Object.entries(next)) {
    if (JSON.stringify(previous[key]) === JSON.stringify(value)) continue
    const child = node.get(key, true)
    if (isMap(child) && value && typeof value === 'object' && !Array.isArray(value)) patchMap(document, child, previous[key] || {}, value)
    else node.set(key, document.createNode(value))
  }
}

// Preserve untouched options, global settings, ordering and comments. Indices
// identify the original rules even when users move, edit or delete a route.
export function writeIngressYaml(text, config, indices = []) {
  const { document, config: previous } = readIngressYaml(text)
  const previousRules = previous.ingress || []
  const oldSequence = document.get('ingress', true)
  const sequence = document.createNode([])
  if (isSeq(oldSequence)) {
    sequence.comment = oldSequence.comment
  }
  for (const [index, value] of (config.ingress || []).entries()) {
    const sourceIndex = indices[index]
    const original = isSeq(oldSequence) && Number.isInteger(sourceIndex) ? oldSequence.items[sourceIndex] : null
    const node = original?.clone()
    // YAML attaches a comment immediately before the first rule to the sequence.
    // Move it with that rule rather than relabelling a different origin on reorder.
    if (node && sourceIndex === 0 && oldSequence.commentBefore) node.commentBefore = [oldSequence.commentBefore, node.commentBefore].filter(Boolean).join('\n')
    if (isMap(node)) { patchMap(document, node, previousRules[sourceIndex] || {}, value); sequence.items.push(node) }
    else sequence.items.push(document.createNode(value))
  }
  const other = { ...config }; delete other.ingress
  const oldOther = { ...previous }; delete oldOther.ingress
  patchMap(document, document.contents, oldOther, other)
  document.set('ingress', sequence)
  return document.toString({ lineWidth: 0 })
}

export function validateIngressConfiguration(config) {
  const errors = []
  if (!config || Array.isArray(config) || typeof config !== 'object') return ['Configuration must be an object']
  if (!Array.isArray(config.ingress) || !config.ingress.length) return ['Add a final fallback rule']
  config.ingress.forEach((rule, index) => {
    const prefix = 'Rule ' + (index + 1) + ': '
    if (!rule || Array.isArray(rule) || typeof rule !== 'object') { errors.push(prefix + 'must be an object'); return }
    if (typeof rule.service !== 'string' || !rule.service.trim()) errors.push(prefix + 'enter an origin service')
    else if (!/^(?:[a-z][a-z0-9+.-]*:\/\/\S+|unix(?:\+tls)?:\S+|http_status:[1-5][0-9]{2}|hello_world|bastion)$/.test(rule.service)) errors.push(prefix + 'use a full service URL, Unix socket, hello_world, bastion, or http_status code')
    if (rule.hostname != null && (typeof rule.hostname !== 'string' || /[\s/:]/.test(rule.hostname))) errors.push(prefix + 'enter a hostname without a URL scheme, port or path')
    if (rule.path != null && typeof rule.path !== 'string') errors.push(prefix + 'path must be a regular expression string')
    if (rule.originRequest != null && (typeof rule.originRequest !== 'object' || Array.isArray(rule.originRequest))) errors.push(prefix + 'origin options must be an object')
    const fallback = !rule.hostname && !rule.path
    if (fallback && index !== config.ingress.length - 1) errors.push(prefix + 'an unconditional fallback must be last')
    if (!fallback && index === config.ingress.length - 1) errors.push('The final rule must be a fallback without hostname or path')
  })
  return errors
}

export function setOriginOption(rule, key, value) {
  if (!rule.originRequest) rule.originRequest = {}
  if (value === '' || value === undefined) delete rule.originRequest[key]
  else rule.originRequest[key] = value
  if (!Object.keys(rule.originRequest).length) delete rule.originRequest
}

export function effectiveOriginOption(config, rule, key) {
  return rule.originRequest?.[key] ?? config.originRequest?.[key]
}

export function originHost(target) { try { return new URL(target).hostname.replace(/^\[|\]$/g, '') } catch { return '' } }
export function originLocation(target, containers = []) {
  const host = originHost(target)
  if (!host) return 'Response or special service'
  if (containers.some(item => item.name?.replace(/^\//, '') === host)) return 'Docker container'
  if (host === 'localhost' || host === '127.0.0.1' || host === '::1') return 'Connector loopback'
  if (host === 'host.docker.internal') return 'Docker host'
  return 'Network / VM origin'
}
