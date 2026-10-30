export function containerStack(container = {}) {
  const name = container.compose_project || container.project_name || ''
  if (!name) return { key: '__standalone__', name: 'Standalone Containers' }
  // The Compose project label is the engine-wide stack identity. Different
  // source folders can contribute services to that same project.
  if (container.compose_project) return { key: `compose:${JSON.stringify(name)}`, name }
  const identity = container.project_id || [name, container.project_path || container.compose_working_dir || '']
  return { key: `compose:${JSON.stringify(identity)}`, name }
}

export function stackProjectID(containers = []) {
  const id = containers[0]?.project_id
  return id && containers.every(container => container.project_id === id) ? id : undefined
}
