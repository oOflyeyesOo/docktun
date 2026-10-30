import test from 'node:test'
import assert from 'node:assert/strict'
import { containerStack, stackProjectID } from './container-stacks.mjs'

test('stack membership uses Compose identity rather than application naming', () => {
  const app = { name: 'photo-app', compose_project: 'photos', compose_working_dir: '/stacks/photos' }
  const database = { ...app, name: 'custom-database' }
  assert.deepEqual(containerStack(app), containerStack(database))
  assert.equal(containerStack({ name: 'photos-postgres' }).key, '__standalone__')
  assert.notEqual(containerStack(app).key, containerStack({ ...app, compose_project: 'archive' }).key)
  assert.equal(containerStack(app).key, containerStack({ ...app, compose_working_dir: '/neighbor/photos' }).key)
  assert.equal(containerStack({ ...app, project_name: 'wrong-alias' }).name, 'photos')
  assert.equal(containerStack({ ...app, project_id: 1 }).key, containerStack({ ...app, project_id: 2 }).key)
})

test('shared project controls require one unambiguous registered source', () => {
  assert.equal(stackProjectID([{ project_id: 1 }, { project_id: 1 }]), 1)
  assert.equal(stackProjectID([{ project_id: 1 }, { project_id: 2 }]), undefined)
  assert.equal(stackProjectID([{ project_id: 1 }, {}]), undefined)
  assert.equal(stackProjectID([]), undefined)
})
