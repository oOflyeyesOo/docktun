export async function authenticatedResponse(path, opts, auth, refresh, unauthorized, fetcher = fetch) {
  const headers = new Headers(opts.headers)
  if (!(opts.body instanceof FormData) && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  if (auth.token) headers.set('Authorization', 'Bearer ' + auth.token)
  let response = await fetcher(path, { ...opts, headers })
  if (response.status === 401 && !path.startsWith('/auth/') && auth.refreshToken) {
    const token = await refresh()
    if (token) { headers.set('Authorization', 'Bearer ' + token); response = await fetcher(path, { ...opts, headers }) }
  }
  if (response.status === 401) { unauthorized(); throw new Error('Please sign in again.') }
  if (!response.ok) {
    const text = await response.text()
    let message = text || 'Request failed'
    try { const error = JSON.parse(text); message = error.error || error.message || message } catch {}
    throw new Error(message)
  }
  return response
}
