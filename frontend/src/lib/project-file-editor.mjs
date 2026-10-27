// Async editor state belongs to one project and one load generation.
export function createProjectFileEditor(label, defaultFilename = '') {
  return {
    projectId: null, revision: 0, loaded: false, loading: false, saving: false,
    content: '', filename: defaultFilename, message: '', error: false,
    invalidate() {
      this.revision++
      this.projectId = null
      this.loaded = this.loading = this.saving = this.error = false
      this.content = this.message = ''
      this.filename = defaultFilename
    },
    async load(projectId, reader) {
      this.invalidate()
      const revision = this.revision
      this.projectId = projectId
      this.loading = true
      try {
        const result = await reader(projectId)
        if (this.revision !== revision) return
        this.content = result.content || ''
        this.filename = result.filename || defaultFilename
        this.loaded = true
      } catch (error) {
        if (this.revision !== revision) return
        this.error = true
        this.message = `Failed to load ${label}: ${error.message}`
      } finally {
        if (this.revision === revision) this.loading = false
      }
    },
    async save(writer, restarter) {
      if (!this.loaded || this.loading || this.saving || !this.projectId) return false
      const { projectId, revision, content, filename } = this
      this.saving = true
      this.message = ''
      this.error = false
      try {
        await writer(projectId, { content, filename })
        if (restarter) await restarter(projectId)
        if (this.revision === revision) this.message = restarter ? `${label} saved and stack restarting...` : `${label} saved successfully!`
        return true
      } catch (error) {
        if (this.revision === revision) {
          this.error = true
          this.message = `Save failed: ${error.message}`
        }
        return false
      } finally {
        if (this.revision === revision) this.saving = false
      }
    },
  }
}
