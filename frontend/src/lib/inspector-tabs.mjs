export function getExportFilename(rawName = '', fallbackId = '', extension = '') {
  const base = (rawName || fallbackId || 'container').replace(/^\//, '')
  return `${base}${extension}`
}

export function createCommandHistory(initialHistory = []) {
  const history = [...initialHistory]
  let historyIndex = -1
  let savedDraft = ''

  return {
    get history() {
      return history
    },
    get index() {
      return historyIndex
    },
    push(command) {
      const trimmed = (command || '').trim()
      if (!trimmed) return
      const idx = history.indexOf(trimmed)
      if (idx !== -1) {
        history.splice(idx, 1)
      }
      history.unshift(trimmed)
      historyIndex = -1
      savedDraft = ''
    },
    navigateUp(currentInput = '') {
      if (history.length === 0) return currentInput
      if (historyIndex === -1) {
        savedDraft = currentInput
        historyIndex = 0
        return history[0]
      }
      if (historyIndex < history.length - 1) {
        historyIndex++
        return history[historyIndex]
      }
      return history[historyIndex]
    },
    navigateDown() {
      if (historyIndex === -1) return savedDraft
      if (historyIndex > 0) {
        historyIndex--
        return history[historyIndex]
      }
      if (historyIndex === 0) {
        historyIndex = -1
        return savedDraft
      }
      return savedDraft
    },
    resetNavigation() {
      historyIndex = -1
      savedDraft = ''
    },
  }
}

export function exitCodeBadgeClass(code) {
  if (code === 0) {
    return 'bg-emerald-50 text-emerald-600 border border-emerald-800'
  }
  return 'bg-rose-50 text-rose-600 border border-rose-800'
}

export function formatFileSize(bytes) {
  if (typeof bytes !== 'number' || isNaN(bytes) || bytes < 0) return '0 B'
  if (bytes < 1024) return `${bytes} B`
  const kb = bytes / 1024
  if (kb < 1024) return `${kb.toFixed(1)} KB`
  const mb = kb / 1024
  if (mb < 1024) return `${mb.toFixed(1)} MB`
  const gb = mb / 1024
  return `${gb.toFixed(1)} GB`
}

export function filterFileList(files, query) {
  if (!Array.isArray(files)) return []
  const q = (query || '').trim().toLowerCase()
  if (!q) return files
  return files.filter(f => (f.name || '').toLowerCase().includes(q))
}
