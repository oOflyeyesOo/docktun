export function updateMonitorOutcome(state) {
  if (state?.phase === 'complete') return 'complete'
  if (state?.phase === 'failed' || state?.phase === 'rolled_back') return 'failed'
  if (['pulling', 'checking', 'restarting'].includes(state?.phase)) return 'pending'
  return 'idle'
}
