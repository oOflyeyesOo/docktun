export const libraryCategories = [
  { id: 'all', name: 'All apps', icon: 'LayoutGrid' },
  { id: 'media', name: 'Media', icon: 'Film', match: /media|video|music|audio|torrent|download|photo|\bbooks?\b|gaming|games|entertainment/i },
  { id: 'productivity', name: 'Productivity', icon: 'Notebook', match: /productivity|bookmark|note|office|calendar|document|communication|wiki|finance/i },
  { id: 'data', name: 'Data & storage', icon: 'Database', match: /database|storage|backup|file|sync/i },
  { id: 'monitoring', name: 'Monitoring', icon: 'Activity', match: /monitor|uptime|dashboard|logging|analytics/i },
  { id: 'network', name: 'Networking', icon: 'Globe', match: /network|proxy|dns|vpn|web.?server|security|adguard|pi.?hole|\bnginx\b|\bapache\b/i },
  { id: 'home', name: 'Home & IoT', icon: 'Home', match: /home.?automation|home.?assistant|smart.?home|homekit|zigbee|mqtt|\biot\b/i },
  { id: 'development', name: 'Development', icon: 'Code2', match: /development|developer|devops|\bgit|code|automation/i },
  { id: 'utilities', name: 'Utilities', icon: 'Wrench' },
]

export function libraryCategory(item) {
  const tags = [...(item.categories || (item.category ? [item.category] : [])), item.name || '']
  return libraryCategories.find(group => group.match && tags.some(tag => group.match.test(tag)))?.id || 'utilities'
}

export function librarySource(item) {
  return item.bundled ? 'starters' : item.collection ? 'community' : 'examples'
}

export function mergeLibraryEntries(current, incoming) {
  const family = item => {
    if (!item.collection) return item.source
    try {
      const url = new URL(item.collection), parts = url.pathname.split('/').filter(Boolean)
      if (url.hostname === 'raw.githubusercontent.com' && parts.length >= 4) return url.hostname + '/' + parts.slice(0, 2).concat(parts.slice(3)).join('/')
    } catch {}
    return item.collection
  }
  // A refresh replaces that catalog snapshot, including removed entries, even
  // when its pinned revision changes. Other custom collections are retained.
  const refreshed = new Set(incoming.map(family))
  const entries = new Map(current.filter(item => !refreshed.has(family(item))).map(item => [family(item) + ':' + item.id, item]))
  for (const item of incoming) entries.set(family(item) + ':' + item.id, item)
  return [...entries.values()]
}

// Public catalog data only. Reopening the dialog reuses successful results;
// simultaneous mounts share one request and failed requests can be retried.
export function createCatalogLoader(now = Date.now, ttl = 300000) {
  const cache = new Map()
  return async function load(key, fetcher, refresh = false) {
    const previous = cache.get(key)
    if (previous?.pending) return previous.pending
    if (!refresh && previous?.items && now() - previous.loadedAt < ttl) return previous.items
    const entry = { ...previous }
    entry.pending = Promise.resolve().then(fetcher).then(items => {
      if (!Array.isArray(items)) throw new Error('Catalog did not return an app list')
      cache.set(key, { items, loadedAt: now() })
      return items
    }).catch(error => {
      if (previous?.items) cache.set(key, { items: previous.items, loadedAt: previous.loadedAt })
      else cache.delete(key)
      throw error
    })
    cache.set(key, entry)
    return entry.pending
  }
}

export const loadLibraryCatalog = createCatalogLoader()

export function storageGroups(mounts) {
  const groups = new Map()
  for (const mount of mounts) {
    if (!groups.has(mount.source)) groups.set(mount.source, { id: mount.id, source: mount.source, members: [] })
    groups.get(mount.source).members.push(mount)
  }
  return [...groups.values()].map(group => {
    const source = group.source.replace(/\/+$/, '')
    const related = mounts.some(mount => {
      const other = mount.source.replace(/\/+$/, '')
      return other !== source && (source.startsWith(other + '/') || other.startsWith(source + '/'))
    })
    const system = source === '' || source === '/etc' || source === '/var/lib/docker' || /^\/(proc|sys|dev|run|var\/run)(\/|$)/.test(source)
    return { ...group, related, automatic: !system && !related && group.members.every(mount => mount.can_manage) }
  })
}

export function defaultStorageChoices(mounts) {
  return Object.fromEntries(storageGroups(mounts).flatMap(group => group.members.map(mount => [mount.id, {
    mode: group.automatic ? 'managed' : 'host',
    source: group.automatic ? '' : mount.source,
  }])))
}
