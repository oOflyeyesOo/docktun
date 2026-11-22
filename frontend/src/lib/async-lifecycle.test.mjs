import test from 'node:test'
import assert from 'node:assert/strict'
import { createAsyncScope, createSerialPoller } from './async-lifecycle.mjs'

function deferred() {
  let resolve, reject
  const promise = new Promise((done, fail) => { resolve = done; reject = fail })
  return { promise, resolve, reject }
}

function timers() {
  let id = 0
  const pending = new Map()
  return {
    pending,
    schedule(callback, delay) { pending.set(++id, { callback, delay }); return id },
    cancel(timer) { pending.delete(timer) },
    fire() {
      const [key, timer] = pending.entries().next().value
      pending.delete(key)
      return timer.callback()
    },
  }
}

test('a later file or log request owns state even when an earlier request finishes last', async () => {
  const scope = createAsyncScope()
  const first = deferred()
  const state = { content: '', loading: false }
  async function load(promise) {
    const current = scope.begin()
    state.loading = true
    try {
      const content = await promise
      if (current()) state.content = content
    } finally {
      if (current()) state.loading = false
    }
  }
  const oldRead = load(first.promise)
  await load(Promise.resolve('second file'))
  first.resolve('first file')
  await oldRead
  assert.deepEqual(state, { content: 'second file', loading: false })
})

test('navigation and disposal discard in-flight mutation and error callbacks', async () => {
  const scope = createAsyncScope()
  const write = deferred()
  const current = scope.capture()
  let navigated = false
  const pending = write.promise.then(() => { if (current()) navigated = true })
  scope.invalidate()
  write.resolve()
  await pending
  assert.equal(navigated, false)
  const read = deferred()
  const latest = scope.begin()
  let errorText = ''
  const failed = read.promise.catch(error => { if (latest()) errorText = error.message })
  scope.dispose()
  read.reject(new Error('closed view'))
  await failed
  assert.equal(errorText, '')
  assert.equal(scope.begin()(), false)
})

test('polling waits for each request and a stopped response cannot apply or reschedule', async () => {
  const clock = timers()
  const response = deferred()
  let requests = 0
  let applied = false
  const poller = createSerialPoller(async current => {
    requests++
    await response.promise
    if (current()) applied = true
  }, 1000, clock)
  poller.start()
  const pending = clock.fire()
  assert.equal(requests, 1)
  assert.equal(clock.pending.size, 0)
  poller.stop()
  response.resolve()
  await pending
  assert.equal(applied, false)
  assert.equal(clock.pending.size, 0)
})

test('restarting polling does not overlap the outstanding request and completed jobs stop', async () => {
  const clock = timers()
  const oldResponse = deferred()
  let requests = 0
  const poller = createSerialPoller(async () => {
    requests++
    if (requests === 1) await oldResponse.promise
    return requests === 2
  }, 1000, clock)
  poller.start()
  const pending = clock.fire()
  poller.start()
  await clock.fire()
  assert.equal(requests, 1)
  assert.equal(clock.pending.size, 1)
  oldResponse.resolve()
  await pending
  await clock.fire()
  assert.equal(requests, 2)
  assert.equal(clock.pending.size, 0)
})

test('stopping a failed request suppresses its error handler', async () => {
  const clock = timers()
  const response = deferred()
  const errors = []
  const poller = createSerialPoller(() => response.promise, 1000, {
    ...clock, onError: error => errors.push(error.message),
  })
  poller.start()
  const pending = clock.fire()
  poller.stop()
  response.reject(new Error('old request'))
  await pending
  assert.deepEqual(errors, [])
  assert.equal(clock.pending.size, 0)
})
