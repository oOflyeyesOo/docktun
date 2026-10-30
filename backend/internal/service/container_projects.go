package service

import (
	"context"
	"path"
	"strings"
	"time"
)

type projRef struct {
	ID         int
	Name, Path string
}

type containerProjects struct {
	byIdentity map[string]projRef
	byName     map[string]projRef
	ambiguous  map[string]bool
	own        updateContainer
}

func newContainerProjects(own updateContainer) *containerProjects {
	return &containerProjects{byIdentity: map[string]projRef{}, byName: map[string]projRef{}, ambiguous: map[string]bool{}, own: own}
}

func (p *containerProjects) identity(name, directory string) string {
	if directory != "" {
		// Compose invoked inside DockTun records container paths; an external
		// invocation records daemon-host paths. Compare their real mount source.
		if host, err := composeHostSource(directory, p.own, ""); err == nil {
			directory = host
		}
		directory = path.Clean(directory)
	}
	return strings.ToLower(name) + "\x00" + directory
}

func (p *containerProjects) add(ref projRef) {
	key := p.identity(ref.Name, ref.Path)
	if _, exists := p.byIdentity[key]; !exists {
		p.byIdentity[key] = ref
	}
	name := strings.ToLower(ref.Name)
	if previous, exists := p.byName[name]; exists && p.identity(previous.Name, previous.Path) != key {
		p.ambiguous[name] = true
	}
	if !p.ambiguous[name] {
		p.byName[name] = ref
	}
}

func (s *DockerContainers) loadContainerProjects() *containerProjects {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	own, _ := selfContainer(ctx)
	index := newContainerProjects(own)
	if s.db == nil {
		return index
	}
	rows, err := s.db.Query(`SELECT id, name, path FROM projects ORDER BY id`)
	if err != nil {
		return index
	}
	defer rows.Close()
	for rows.Next() {
		var ref projRef
		if rows.Scan(&ref.ID, &ref.Name, &ref.Path) == nil {
			index.add(ref)
		}
	}
	return index
}

// Docker Compose labels define membership. Display names, shared networks,
// directory names and container-name prefixes cannot establish stack ownership.
func (s *DockerContainers) assignContainerProject(c *Container, labels map[string]string, index *containerProjects) {
	c.ComposeSources, c.ComposeSourceError = nil, ""
	c.ComposeProject = strings.TrimSpace(labels["com.docker.compose.project"])
	c.ComposeService = labels["com.docker.compose.service"]
	c.ComposeWorkingDir = labels["com.docker.compose.project.working_dir"]
	c.ComposeConfigFiles = labels["com.docker.compose.project.config_files"]
	c.ProjectID, c.ProjectName, c.ProjectPath = 0, c.ComposeProject, c.ComposeWorkingDir
	if c.ComposeProject == "" {
		c.ProjectPath = ""
		return
	}
	var ref projRef
	var found bool
	if c.ComposeWorkingDir != "" {
		ref, found = index.byIdentity[index.identity(c.ComposeProject, c.ComposeWorkingDir)]
	} else if !index.ambiguous[strings.ToLower(c.ComposeProject)] {
		ref, found = index.byName[strings.ToLower(c.ComposeProject)]
	}
	if !found && c.ComposeWorkingDir != "" && s.db != nil {
		// Register the actual name/path pair. Never borrow another project's ID
		// by folder alone or invent a local folder for an inaccessible host stack.
		_, err := s.db.Exec(`INSERT INTO projects (name, path) SELECT ?, ? WHERE NOT EXISTS (SELECT 1 FROM projects WHERE lower(name)=lower(?) AND path=?)`, c.ComposeProject, c.ComposeWorkingDir, c.ComposeProject, c.ComposeWorkingDir)
		if err == nil {
			err = s.db.QueryRow(`SELECT id, name, path FROM projects WHERE lower(name)=lower(?) AND path=? ORDER BY id LIMIT 1`, c.ComposeProject, c.ComposeWorkingDir).Scan(&ref.ID, &ref.Name, &ref.Path)
			found = err == nil
			if found {
				index.add(ref)
			}
		}
	}
	if found {
		c.ProjectID, c.ProjectPath = ref.ID, ref.Path
		rememberComposeSources(s.db, ref.ID, c.ComposeWorkingDir, c.ComposeConfigFiles)
		if s.db != nil {
			var err error
			c.ComposeSources, err = storedComposeSources(s.db, ref.ID)
			if err != nil {
				c.ComposeSourceError = err.Error()
			}
		}
	}
}
