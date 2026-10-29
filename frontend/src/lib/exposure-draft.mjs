// Exposure settings are flat scalar fields. Compare sorted keys so API property
// order does not make an untouched form appear to contain unsaved changes.
export function settingsEqual(a, b) {
  if (Object.is(a, b)) return true
  if (!a || !b || typeof a !== 'object' || typeof b !== 'object') return false
  const keys = Object.keys(a).sort()
  const otherKeys = Object.keys(b).sort()
  return keys.length === otherKeys.length && keys.every((key, index) => key === otherKeys[index] && Object.is(a[key], b[key]))
}

// Refresh pristine forms when remote discovery fills a cold cache. Keep a dirty
// form and its original baseline together until the user saves or discards it.
export function syncExposureDraft(current, baseline, fresh) {
  if (current == null || settingsEqual(current, baseline)) {
    return { draft: { ...fresh }, baseline: { ...fresh } }
  }
  return { draft: current, baseline }
}
