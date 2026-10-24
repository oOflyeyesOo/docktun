import { useAuthStore } from '../stores/auth'
import { useRouter } from 'vue-router'

const inFlightGets = new Map<string, Promise<any>>()

function normalizeApiError(res: Response, fallback: string) {
  return res.text().then((text) => {
    if (!text) return fallback
    try {
      const data = JSON.parse(text)
      return data?.error || data?.message || fallback
    } catch {
      return text || fallback
    }
  })
}

export const useApi = () => {
  const authStore = useAuthStore()
  const router = useRouter()

  const apiFetch = async <T>(path: string, opts: RequestInit = {}): Promise<T> => {
    const isGet = !opts.method || opts.method.toUpperCase() === 'GET'
    const cacheKey = isGet ? `${authStore.token || 'anon'}:${path}` : ''

    if (isGet && inFlightGets.has(cacheKey)) {
      return inFlightGets.get(cacheKey) as Promise<T>
    }

    const execFetch = async (): Promise<T> => {
      const headers: Record<string, string> = {
        'Content-Type': 'application/json',
        ...(opts.headers as Record<string, string>),
      }

      if (authStore.token) {
        headers['Authorization'] = `Bearer ${authStore.token}`
      }

      const res = await fetch(path, { ...opts, headers })

      if (res.status === 401) {
        authStore.logout()
        if (router && typeof router.push === 'function') {
          router.push('/login')
        } else {
          window.location.href = '/login'
        }
        throw new Error('Unauthorized')
      }

      if (!res.ok) {
        const message = await normalizeApiError(res, 'Request failed')
        throw new Error(message)
      }

      const text = await res.text()
      if (!text) return undefined as T
      try {
        return JSON.parse(text) as T
      } catch {
        return text as unknown as T
      }
    }

    if (isGet) {
      const reqPromise = execFetch().finally(() => {
        inFlightGets.delete(cacheKey)
      })
      inFlightGets.set(cacheKey, reqPromise)
      return reqPromise
    }

    return execFetch()
  }

  return { apiFetch }
}
