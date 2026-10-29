package service

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Keep the recorded Compose host path intact. File access uses the path that
// Docker actually mounted inside this DockTun instance instead.
type projectDirectory struct {
	Path, Mount, Relative string
	ReadOnly              bool
	CreateManagedBase     bool
}

func pathWithin(directory, base string) bool {
	return directory == base || strings.HasPrefix(directory, strings.TrimRight(base, "/")+"/")
}

func projectHostDirectory(directory string, own updateContainer) (projectDirectory, bool, error) {
	directory = path.Clean(directory)
	best := -1
	for i, mount := range own.Mounts {
		if mount.Source != "" && pathWithin(directory, path.Clean(mount.Source)) && (best < 0 || len(path.Clean(mount.Source)) > len(path.Clean(own.Mounts[best].Source))) {
			best = i
		}
	}
	if best < 0 {
		return projectDirectory{}, false, nil
	}
	mount := own.Mounts[best]
	if (mount.Type != "bind" && mount.Type != "volume") || !path.IsAbs(mount.Source) || !path.IsAbs(mount.Destination) {
		return projectDirectory{}, true, fmt.Errorf("project folder has no supported persistent Docker mount")
	}
	relative := strings.TrimPrefix(strings.TrimPrefix(directory, path.Clean(mount.Source)), "/")
	mapped := path.Join(mount.Destination, relative)
	// A nested container mount may hide part of the host directory. Never open
	// that unrelated content merely because a parent Source matched.
	reverse := -1
	for i, candidate := range own.Mounts {
		if pathWithin(mapped, path.Clean(candidate.Destination)) && (reverse < 0 || len(path.Clean(candidate.Destination)) > len(path.Clean(own.Mounts[reverse].Destination))) {
			reverse = i
		}
	}
	if reverse < 0 {
		return projectDirectory{}, true, fmt.Errorf("project folder is not visible through its Docker mount")
	}
	visible := own.Mounts[reverse]
	suffix := strings.TrimPrefix(strings.TrimPrefix(mapped, path.Clean(visible.Destination)), "/")
	if (visible.Type != "bind" && visible.Type != "volume") || path.Join(visible.Source, suffix) != directory {
		return projectDirectory{}, true, fmt.Errorf("project folder is hidden by another Docker mount at %s", visible.Destination)
	}
	return projectDirectory{Path: mapped, Mount: path.Clean(visible.Destination), Relative: suffix, ReadOnly: !visible.RW}, true, nil
}

func managedProjectBase(directory string) (string, string, bool) {
	for _, base := range []string{"/opt/projects", "./projects"} {
		absolute, err := filepath.Abs(base)
		if err == nil && directory != absolute && pathWithin(filepath.ToSlash(directory), filepath.ToSlash(absolute)) {
			relative, err := filepath.Rel(absolute, directory)
			if err == nil {
				return absolute, relative, true
			}
		}
	}
	return "", "", false
}

func (s *Projects) resolveProjectDirectory(stored string, create bool) (projectDirectory, error) {
	_, socketErr := os.Stat("/var/run/docker.sock")
	required := socketErr == nil
	if s.requireMountInspection != nil { required = s.requireMountInspection() }
	return s.resolveProjectDirectoryWithInspection(stored, create, required)
}

func (s *Projects) resolveProjectDirectoryWithInspection(stored string, create, requireInspection bool) (projectDirectory, error) {
	if strings.TrimSpace(stored) == "" {
		return projectDirectory{}, fmt.Errorf("project has no folder configured")
	}
	absolute, err := filepath.Abs(stored)
	if err != nil {
		return projectDirectory{}, err
	}
	directory := projectDirectory{Path: absolute}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inspector := NewProviderContainers(nil)
	if s.runDocker != nil {
		inspector.run = s.runDocker
	}
	if own, inspectErr := inspector.self(ctx); inspectErr == nil {
		mapped, found, mapErr := projectHostDirectory(filepath.ToSlash(absolute), own)
		if mapErr != nil {
			return projectDirectory{}, mapErr
		}
		if found {
			for _, mount := range own.Mounts {
				if pathWithin(filepath.ToSlash(absolute), path.Clean(mount.Destination)) && mapped.Path != filepath.ToSlash(absolute) {
					return projectDirectory{}, fmt.Errorf("stack path %s matches both a host source and a different container mount; use distinct host and container mount paths before accessing files", stored)
				}
			}
			directory = mapped
		} else {
			// Already-local paths (managed stacks and native Compose labels) do
			// not need reverse translation, but honor a read-only mount.
			best := -1
			for i, mount := range own.Mounts {
				if pathWithin(filepath.ToSlash(absolute), path.Clean(mount.Destination)) && (best < 0 || len(mount.Destination) > len(own.Mounts[best].Destination)) {
					best = i
				}
			}
			if best >= 0 {
				mount := own.Mounts[best]
				directory.Mount = mount.Destination
				directory.Relative = strings.TrimPrefix(strings.TrimPrefix(filepath.ToSlash(absolute), path.Clean(mount.Destination)), "/")
				directory.ReadOnly = !mount.RW
			}
		}
	} else if requireInspection {
		// A mounted Docker socket means these paths belong to a containerized
		// installation. An old container-only shadow folder is not evidence of
		// the host stack when self-inspection is unavailable.
		return projectDirectory{}, fmt.Errorf("cannot verify DockTun's mounted stack paths; restore Docker access and check DOCKTUN_CONTAINER before accessing project files: %w", inspectErr)
	}
	root, err := directory.open(false)
	if err == nil {
		root.Close()
		return directory, nil
	}
	// Only a deliberate save/create in DockTun's managed projects area may
	// create a missing stack. Host labels must never create shadow host paths.
	if create && os.IsNotExist(err) {
		if base, relative, managed := managedProjectBase(absolute); managed && directory.Path == absolute {
			if directory.Mount == "" {
				directory.Mount, directory.Relative, directory.CreateManagedBase = base, relative, true
			}
			return directory, nil
		}
	}
	if os.IsNotExist(err) {
		if directory.Mount != "" {
			return projectDirectory{}, fmt.Errorf("stack folder %s maps to %s, but that folder is missing inside DockTun; check the existing mount and host folder before accessing files: %w", stored, directory.Path, err)
		}
		return projectDirectory{}, fmt.Errorf("stack folder %s is not mounted or is missing inside DockTun; mount that host folder into DockTun (for example %s:/opt/projects/%s) before editing files or using Compose: %w", stored, stored, filepath.Base(absolute), err)
	}
	return projectDirectory{}, fmt.Errorf("cannot access stack folder %s inside DockTun: %w", stored, err)
}

func (directory projectDirectory) open(create bool) (*os.Root, error) {
	if create && directory.ReadOnly {
		return nil, fmt.Errorf("stack folder %s is mounted read-only in DockTun", directory.Path)
	}
	if directory.Mount != "" {
		if create && directory.CreateManagedBase {
			if err := os.MkdirAll(directory.Mount, 0755); err != nil {
				return nil, err
			}
		}
		base, err := os.OpenRoot(directory.Mount)
		if err != nil {
			return nil, err
		}
		defer base.Close()
		relative := directory.Relative
		if relative == "" {
			relative = "."
		}
		if create {
			if err := base.MkdirAll(relative, 0755); err != nil {
				return nil, err
			}
		}
		return base.OpenRoot(relative)
	}
	if create {
		if err := os.MkdirAll(directory.Path, 0755); err != nil {
			return nil, err
		}
	}
	return os.OpenRoot(directory.Path)
}

type ProjectFileAccess struct {
	Path         string `json:"path"`
	ResolvedPath string `json:"resolved_path,omitempty"`
	Available    bool   `json:"available"`
	Writable     bool   `json:"writable"`
	Reason       string `json:"reason,omitempty"`
}

func (s *Projects) FileAccess(id int) (ProjectFileAccess, error) {
	project, err := s.GetByID(id)
	if err != nil {
		return ProjectFileAccess{}, err
	}
	access := ProjectFileAccess{Path: project.Path}
	directory, err := s.resolveProjectDirectory(project.Path, false)
	if err != nil {
		access.Reason = err.Error()
		return access, nil
	}
	access.ResolvedPath, access.Available, access.Writable = directory.Path, true, !directory.ReadOnly
	if directory.ReadOnly {
		access.Reason = "The stack folder is mounted read-only in DockTun. Mount it read-write to edit files."
	}
	return access, nil
}
