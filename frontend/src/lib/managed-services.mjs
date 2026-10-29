export function matchesServiceID(id = '', serviceID = '') {
  return id.length >= 12 && serviceID.length >= 12 && (id === serviceID || id.startsWith(serviceID) || serviceID.startsWith(id))
}

export function applicationContainers(containers = [], services = []) {
  return containers.filter(container => !services.some(service => matchesServiceID(container.id, service.id) || (service.id?.length >= 12 && service.name && service.name === (container.name || '').replace(/^\//, ''))))
}
