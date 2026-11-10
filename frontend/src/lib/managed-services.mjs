export function matchesServiceID(id = '', serviceID = '') {
  return id.length >= 12 && serviceID.length >= 12 && (id === serviceID || id.startsWith(serviceID) || serviceID.startsWith(id))
}

export function applicationProjects(projects, containers, services) {
  const core = services.find(service => service.kind === 'core')
  const dashboard = core && containers.find(container => matchesServiceID(container.id, core.id))
  if (!dashboard) return projects

  const apps = applicationContainers(containers, services)
  return projects.filter(project => {
    const internal = project.id === dashboard.project_id ||
      (dashboard.compose_working_dir && project.path === dashboard.compose_working_dir && project.name === dashboard.compose_project)
    return !internal || apps.some(container => container.project_id === project.id ||
      (container.compose_working_dir && container.compose_working_dir === project.path && container.compose_project === project.name))
  })
}

export function applicationContainers(containers = [], services = []) {
  return containers.filter(container => !services.some(service => matchesServiceID(container.id, service.id) || (service.id?.length >= 12 && service.name && service.name === (container.name || '').replace(/^\//, ''))))
}
