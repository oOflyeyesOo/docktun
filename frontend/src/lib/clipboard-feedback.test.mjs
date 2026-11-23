import test from 'node:test'
import assert from 'node:assert/strict'
import { clipboardFailureMessage, createClipboardFeedback } from './clipboard-feedback.mjs'

function deferred() {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

function fixture(writeText) {
  const changes = [], errors = [], timers = new Map(), cancelled = []
  let nextTimer = 0
  const feedback = createClipboardFeedback({
    writeText,
    onChange: value => changes.push(value),
    onError: message => errors.push(message),
    schedule(callback) { timers.set(++nextTimer, callback); return nextTimer },
    cancel(id) { cancelled.push(id) },
  })
  return { feedback, changes, errors, timers, cancelled }
}

test('copy feedback waits for the browser write and expires after success', async () => {
  const write = deferred(), texts = []
  const f = fixture(text => { texts.push(text); return write.promise })
  const pending = f.feedback.copy('yaml', 'services:\n  app: {}')
  assert.deepEqual(f.changes, [null], 'no successful copy before permission/write completes')
  assert.equal(f.timers.size, 0)
  write.resolve()
  assert.equal(await pending, true)
  assert.deepEqual(texts, ['services:\n  app: {}'])
  assert.deepEqual(f.changes, [null, 'yaml'])
  f.timers.get(1)()
  assert.deepEqual(f.changes, [null, 'yaml', null])
  assert.deepEqual(f.errors, [])
})

test('denied and unavailable clipboard writes show actionable failure without success', async () => {
  for (const writeText of [() => Promise.reject(new Error('permission denied')), () => { throw new TypeError('clipboard unavailable') }]) {
    const f = fixture(writeText)
    assert.equal(await f.feedback.copy('logs', 'private content'), false)
    assert.deepEqual(f.changes, [null])
    assert.deepEqual(f.errors, [clipboardFailureMessage])
    assert.equal(f.timers.size, 0)
    assert.ok(!f.errors[0].includes('private content'))
  }
})

test('a missing browser clipboard API is handled by the default writer', async () => {
  const original = Object.getOwnPropertyDescriptor(globalThis, 'navigator')
  try {
    Object.defineProperty(globalThis, 'navigator', { value: {}, configurable: true })
    const changes = [], errors = []
    const feedback = createClipboardFeedback({ onChange: value => changes.push(value), onError: message => errors.push(message) })
    assert.equal(await feedback.copy('address', 'fixture.local'), false)
    assert.deepEqual(changes, [null])
    assert.deepEqual(errors, [clipboardFailureMessage])
  } finally {
    if (original) Object.defineProperty(globalThis, 'navigator', original)
    else delete globalThis.navigator
  }
})

test('older completion or failure cannot replace a newer copy result', async () => {
  for (const failOlder of [false, true]) {
    const first = deferred(), second = deferred()
    const writes = [first, second]
    const f = fixture(() => writes.shift().promise)
    const oldCopy = f.feedback.copy('old', 'first')
    const latestCopy = f.feedback.copy('latest', 'second')
    second.resolve()
    assert.equal(await latestCopy, true)
    if (failOlder) first.reject(new Error('old permission denial'))
    else first.resolve()
    assert.equal(await oldCopy, false)
    assert.deepEqual(f.changes, [null, null, 'latest'])
    assert.deepEqual(f.errors, [])
  }
})

test('repeated copies of the same item get a full new feedback interval', async () => {
  const f = fixture(() => Promise.resolve())
  await f.feedback.copy('same', 'first')
  await f.feedback.copy('same', 'second')
  assert.deepEqual(f.cancelled, [1])
  const before = [...f.changes]
  f.timers.get(1)() // A queued callback must remain harmless even after cancellation.
  assert.deepEqual(f.changes, before)
  f.feedback.dispose()
  assert.deepEqual(f.cancelled, [1, 2], 'stale callback must not lose the current timer handle')
  f.timers.get(2)()
  assert.deepEqual(f.changes, before, 'unmounted feedback receives no timer callbacks')
})

test('navigation reset and unmount suppress pending success and errors', async () => {
  const write = deferred()
  const f = fixture(() => write.promise)
  const pending = f.feedback.copy('old-project', 'text')
  f.feedback.reset()
  const before = [...f.changes]
  write.resolve()
  assert.equal(await pending, false)
  assert.deepEqual(f.changes, before)

  const rejected = deferred()
  const gone = fixture(() => rejected.promise)
  const unmountedCopy = gone.feedback.copy('logs', 'text')
  gone.feedback.dispose()
  rejected.reject(new Error('late denial'))
  assert.equal(await unmountedCopy, false)
  assert.deepEqual(gone.changes, [null])
  assert.deepEqual(gone.errors, [])
  assert.equal(await gone.feedback.copy('closed', 'text'), false)
})
