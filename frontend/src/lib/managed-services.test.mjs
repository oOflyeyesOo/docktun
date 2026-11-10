import test from 'node:test'
import assert from 'node:assert/strict'
import { applicationContainers, applicationProjects, matchesServiceID } from './managed-services.mjs'

test('verified service IDs hide managed infrastructure regardless of names', () => {
  const core = 'a'.repeat(64), app = 'b'.repeat(64)
  assert.deepEqual(applicationContainers([{id: core.slice(0,12), name:'custom-dashboard'}, {id:app,name:'docktun-backup'}], [{id:core}]), [{id:app,name:'docktun-backup'}])
})
test('names and tiny ID fragments cannot hide unrelated apps', () => {
  const apps = [{id:'b'.repeat(12),name:'tailscale'}, {id:'c'.repeat(12),name:'wireguard'}]
  assert.deepEqual(applicationContainers(apps, [{id:'b',name:'tailscale'}]), apps)
  assert.equal(matchesServiceID('', ''), false)
})

test('a verified service remains separated while its replacement ID is propagating', () => {
  assert.deepEqual(applicationContainers([{id:'c'.repeat(64),name:'custom-core'}], [{id:'a'.repeat(64),name:'custom-core'}]), [])
})

test('the verified internal stack has no empty application row', () => {
  const id = 'a'.repeat(64)
  const projects = [{id: 1, name: 'docktun', path: '/host/docktun'}, {id: 2, name: 'docktun', path: '/host/other'}]
  const core = {id, compose_project: 'docktun', compose_working_dir: '/host/docktun'}
  const services = [{id, kind: 'core'}]
  assert.deepEqual(applicationProjects(projects, [core], services), [projects[1]])
  assert.deepEqual(applicationProjects(projects, [{...core, project_id: 1}], services), [projects[1]])
  assert.deepEqual(applicationProjects(projects, [core, {id: 'b'.repeat(64), project_id: 1}], services), projects)
  assert.deepEqual(applicationProjects(projects, [core], []), projects)
})
