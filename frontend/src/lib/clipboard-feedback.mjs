export const clipboardFailureMessage = 'Could not copy to the clipboard. Allow clipboard access or select and copy the text manually.'

// Keep feedback bound to the latest request, including repeated copies of the
// same item. Browser clipboard access can reject or be unavailable on HTTP.
export function createClipboardFeedback({
  onChange,
  onError = () => {},
  writeText = text => globalThis.navigator.clipboard.writeText(text),
  schedule = setTimeout,
  cancel = clearTimeout,
  duration = 2000,
}) {
  let revision = 0
  let timer = null
  let disposed = false

  function reset() {
    revision++
    if (timer !== null) cancel(timer)
    timer = null
    if (!disposed) onChange(null)
  }

  async function copy(key, text) {
    if (disposed) return false
    reset()
    const request = revision
    const current = () => !disposed && revision === request
    try {
      await writeText(text)
      if (!current()) return false
      onChange(key)
      timer = schedule(() => {
        if (current()) {
          timer = null
          onChange(null)
        }
      }, duration)
      return true
    } catch {
      if (current()) onError(clipboardFailureMessage)
      return false
    }
  }

  function dispose() {
    disposed = true
    reset()
  }

  return { copy, reset, dispose }
}
