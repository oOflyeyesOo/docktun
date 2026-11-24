// Older versions cached the Cloudflare API token in the browser. Tokens now
// stay on the server. Remove only that obsolete key, preserving login/settings.
export function clearLegacyCloudflareToken(getStorage = () => globalThis.localStorage) {
  try {
    getStorage().removeItem('docktun_cf_api_token')
  } catch {
    // Restricted browser storage must not prevent the application starting.
  }
}
