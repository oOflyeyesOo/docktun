import { onUnmounted } from 'vue'
import { createClipboardFeedback } from '../lib/clipboard-feedback.mjs'

export function useClipboardFeedback<T>(
  onChange: (key: T | null) => void,
  onError: (message: string) => void = message => window.alert(message),
) {
  const feedback = createClipboardFeedback({ onChange, onError })
  onUnmounted(feedback.dispose)
  return feedback
}
