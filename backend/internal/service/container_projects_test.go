package service

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestCreateProjectDoesNotRenameOrOverwriteExistingRecords(t *testing.T) {
	svc := projectPathService(t)
	folder := t.TempDir()
	original, err := svc.Create("photos", folder, "https://example.invalid/private", "owner", "keep-token")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, directory string }{
		{"archive", folder}, {"photos", filepath.Join(folder, "different")}, {"PHOTOS", filepath.Join(folder, "different")}, {"photos", folder},
	} {
		if _, err := svc.Create(tc.name, tc.directory, "", "", ""); !errors.Is(err, ErrProjectConflict) {
			t.Fatalf("expected conflict for %+v, got %v", tc, err)
		}
	}
	var name, directory, token string
	if err := svc.db.QueryRow(`SELECT name,path,git_token FROM projects WHERE id=?`, original.ID).Scan(&name, &directory, &token); err != nil {
		t.Fatal(err)
	}
	if name != "photos" || directory != folder || token != "keep-token" {
		t.Fatalf("existing project changed: %s %s", name, directory)
	}
}

func TestComposeSourcesKeepOrderAndSurviveStreamedRecreation(t *testing.T) {
	svc := projectPathService(t)
	rememberComposeSources(svc.db, 1, "/volume1/stacks/photos", "/volume1/stacks/photos/compose.yml,/volume1/stacks/photos/override.yml")
	rememberComposeSources(svc.db, 1, "/stack-files/photos", "-")
	files, err := storedComposeSources(svc.db, 1)
	if err != nil || len(files) != 2 || files[0] != "compose.yml" || files[1] != "override.yml" {
		t.Fatalf("source order lost: %v %v", files, err)
	}
	rememberComposeSources(svc.db, 2, "/stacks/photos", "/stacks/neighbor/compose.yml")
	if _, err := storedComposeSources(svc.db, 2); err == nil {
		t.Fatal("outside source silently fell back to a different Compose file")
	}
	if files, err := storedComposeSources(svc.db, 3); err != nil || len(files) != 0 {
		t.Fatal("newly authored stack lost normal file discovery")
	}
}

func TestContainerProjectIdentityUsesLabelsAndSource(t *testing.T) {
	index := newContainerProjects(updateContainer{})
	index.add(projRef{1, "archive", "/stacks/shared"})
	index.add(projRef{2, "photos", "/stacks/photos"})
	index.add(projRef{3, "photos-db", "/stacks/other"})
	svc := &DockerContainers{}
	for _, tc := range []struct {
		name, project, directory string
		wantID                   int
	}{
		{"photos-postgres", "photos", "/stacks/photos", 2},
		{"custom-db", "photos", "/stacks/photos", 2},
		{"photos-postgres", "archive", "/stacks/shared", 1},
		{"photos-postgres", "photos", "/stacks/shared", 0},
		{"photos-postgres", "photos", "/another/photos", 0},
		{"photos-postgres", "", "", 0},
	} {
		c := Container{Name: tc.name}
		labels := map[string]string{"com.docker.compose.project": tc.project, "com.docker.compose.project.working_dir": tc.directory, "com.docker.compose.service": "database", "com.docker.compose.project.config_files": "/stacks/compose.yml,/stacks/override.yml"}
		svc.assignContainerProject(&c, labels, index)
		if c.ProjectID != tc.wantID || c.ProjectName != tc.project || c.ComposeService != "database" || c.ComposeConfigFiles != labels["com.docker.compose.project.config_files"] {
			t.Fatalf("%+v => %+v", tc, c)
		}
	}
}

func TestContainerProjectMountAliasesAndAmbiguousNames(t *testing.T) {
	var own updateContainer
	if err := json.Unmarshal([]byte(`{"Mounts":[{"Type":"bind","Source":"/volume1/docker","Destination":"/mounted-stacks","RW":true}]}`), &own); err != nil {
		t.Fatal(err)
	}
	index := newContainerProjects(own)
	index.add(projRef{1, "photos", "/volume1/docker/photos"})
	svc := &DockerContainers{}
	c := Container{}
	svc.assignContainerProject(&c, map[string]string{"com.docker.compose.project": "photos", "com.docker.compose.project.working_dir": "/mounted-stacks/photos"}, index)
	if c.ProjectID != 1 || c.ProjectPath != "/volume1/docker/photos" {
		t.Fatalf("mount alias lost original registered source: %+v", c)
	}
	index.add(projRef{2, "photos", "/volume2/photos"})
	svc.assignContainerProject(&c, map[string]string{"com.docker.compose.project": "photos"}, index)
	if c.ProjectID != 0 {
		t.Fatal("ambiguous source guessed without a directory")
	}
}

func TestContainerProjectRegistersActualPairWithoutChangingSavedProjects(t *testing.T) {
	projects := projectPathService(t)
	_, err := projects.db.Exec(`INSERT INTO projects(name,path) VALUES ('archive','/stacks/shared'),('photos','/stacks/old')`)
	if err != nil {
		t.Fatal(err)
	}
	svc := &DockerContainers{db: projects.db}
	index := newContainerProjects(updateContainer{})
	index.add(projRef{1, "archive", "/stacks/shared"})
	index.add(projRef{2, "photos", "/stacks/old"})
	labels := map[string]string{"com.docker.compose.project": "photos", "com.docker.compose.project.working_dir": "/stacks/shared"}
	c := Container{Name: "photos-postgres"}
	svc.assignContainerProject(&c, labels, index)
	if c.ProjectID <= 2 || c.ProjectName != "photos" || c.ProjectPath != "/stacks/shared" {
		t.Fatalf("wrong saved project adopted: %+v", c)
	}
	id := c.ProjectID
	svc.assignContainerProject(&c, labels, index)
	if c.ProjectID != id {
		t.Fatal("repeated discovery changed identity")
	}
	var count int
	_ = projects.db.QueryRow(`SELECT count(*) FROM projects`).Scan(&count)
	if count != 3 {
		t.Fatalf("unexpected project records: %d", count)
	}
}
