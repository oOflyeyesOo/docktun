// A response may update the view only while its request generation is current.
export function createAsyncScope() {
  let revision = 0
  let disposed = false
  function capture() {
    const requestRevision = revision
    return () => !disposed && requestRevision === revision
  }
  return {
    begin() { revision++; return capture() },
    capture,
    invalidate() { revision++ },
    dispose() { disposed = true; revision++ },
  }
}

// Schedule the next poll after the previous request settles, including restarts.
export function createSerialPoller(task, interval, {
  schedule = setTimeout,
  cancel = clearTimeout,
  onError = () => {},
} = {}) {
  let revision = 0
  let timer = null
  let inFlight = false

  function stop() {
    revision++
    if (timer !== null) cancel(timer)
    timer = null
  }

  function start(delay = 0) {
    stop()
    const pollRevision = revision
    const current = () => revision === pollRevision

    async function poll() {
      if (!current()) return
      timer = null
      if (inFlight) {
        timer = schedule(poll, interval)
        return
      }
      inFlight = true
      try {
        if (await task(current) === true) return
      } catch (error) {
        if (current()) onError(error)
        return
      } finally {
        inFlight = false
      }
      if (current()) timer = schedule(poll, interval)
    }

    timer = schedule(poll, delay)
  }

  return { start, stop }
}
