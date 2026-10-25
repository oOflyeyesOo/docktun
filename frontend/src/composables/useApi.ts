import { authenticatedResponse } from '../lib/api-response.mjs'
import { useAuthStore } from '../stores/auth'
import { useRouter } from 'vue-router'

const inFlightGets = new Map<string, Promise<any>>()
let refreshPromise: Promise<string | null> | null = null

async function requestSilentRefresh(authStore: any): Promise<string | null> {
  if (refreshPromise) return refreshPromise
  if (!authStore.refreshToken) return null

  refreshPromise = (async () => {
    try {
      const res = await fetch('/auth/refresh', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ refresh_token: authStore.refreshToken }),
      })
      if (res.ok) {
        const data = await res.json()
        if (data.access_token) {
          authStore.setAuth(data.access_token, authStore.username, data.refresh_token)
          return data.access_token as string
        }
      }
    } catch (e) {
      console.warn('[docktun] silent token refresh error:', e)
    } finally {
      refreshPromise = null
    }
    return null
  })()

  return refreshPromise
}

export const useApi = () => {
  const authStore = useAuthStore()
  const router = useRouter()

  const apiResponse = (path: string, opts: RequestInit = {}): Promise<Response> => authenticatedResponse(path, opts, authStore, () => requestSilentRefresh(authStore), () => { authStore.logout(); router.push('/login') })
  const apiFetch = async <T>(path: string, opts: RequestInit = {}): Promise<T> => {
    const isGet = !opts.method || opts.method.toUpperCase() === 'GET'
    const key = (authStore.token || 'anon') + ':' + path
    if (isGet && inFlightGets.has(key)) return inFlightGets.get(key)
    const request = async (): Promise<T> => {
      const res = await apiResponse(path, opts)
      const text = await res.text()
      if (!text) return undefined as T
      try { return JSON.parse(text) as T } catch { return text as unknown as T }
    }
    if (!isGet) return request()
    const promise = request().finally(() => inFlightGets.delete(key))
    inFlightGets.set(key, promise)
    return promise
  }
  return { apiFetch, apiResponse }
}
