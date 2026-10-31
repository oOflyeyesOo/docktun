package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComposeBindSourcesUseActualDaemonMounts(t *testing.T) {
	var own updateContainer
	json.Unmarshal([]byte(`{"Mounts":[{"Type":"bind","Source":"/volume1/docker/projects","Destination":"/opt/projects"},{"Type":"volume","Source":"/var/lib/docker/volumes/custom-config/_data","Name":"custom-config","Destination":"/opt/projects/example/config"},{"Type":"volume","Source":"/var/lib/docker/volumes/app-data/_data","Name":"app-data","Destination":"/app/data"}]}`), &own)
	raw := `{"services":{"app":{"image":"alpine","volumes":[{"type":"bind","source":"/opt/projects/example/content","target":"/content","read_only":true},{"type":"bind","source":"/opt/projects/example/config/settings.yml","target":"/settings.yml"},{"type":"bind","source":"/app/data/shared","target":"/shared"},{"type":"bind","source":"/media","target":"/media"},{"type":"bind","source":"/opt/projects-other","target":"/other"},{"type":"volume","source":"data","target":"/data"}]}},"volumes":{"data":{}},"secrets":{"key":{"file":"/local/key"}}}`
	var document map[string]any
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatal(err)
	}
	if !hasComposeBindSources(document) {
		t.Fatal("bind mounts not detected")
	}
	if err := remapComposeBindSources(document, own, "/opt/projects/example"); err != nil {
		t.Fatal(err)
	}
	services := document["services"].(map[string]any)
	volumes := services["app"].(map[string]any)["volumes"].([]any)
	want := []string{"/volume1/docker/projects/example/content", "/var/lib/docker/volumes/custom-config/_data/settings.yml", "/var/lib/docker/volumes/app-data/_data/shared", "/media", "/opt/projects-other", "data"}
	for i, value := range volumes {
		volume := value.(map[string]any)
		if volume["source"] != want[i] {
			t.Fatalf("wrong host source: %+v", volume)
		}
	}
	if volumes[0].(map[string]any)["read_only"] != true {
		t.Fatal("read-only mount changed")
	}
	if document["secrets"].(map[string]any)["key"].(map[string]any)["file"] != "/local/key" {
		t.Fatal("unreferenced resource definition changed")
	}
}

func TestComposeRefusesUnmappedProjectOrTemporaryMount(t *testing.T) {
	for _, inspection := range []string{`{"Mounts":[]}`, `{"Mounts":[{"Type":"bind","Source":"/host/projects","Destination":"/opt/projects"},{"Type":"tmpfs","Destination":"/opt/projects/example"}]}`} {
		var own updateContainer
		json.Unmarshal([]byte(inspection), &own)
		var document map[string]any
		json.Unmarshal([]byte(`{"services":{"app":{"volumes":[{"type":"bind","source":"/opt/projects/example/content","target":"/content"}]}}}`), &document)
		if err := remapComposeBindSources(document, own, "/opt/projects/example"); err == nil {
			t.Fatal("invented a host path for unavailable project data")
		}
	}
}

func TestComposeRootMountAndNamedVolumeStayDistinct(t *testing.T) {
	var own updateContainer
	json.Unmarshal([]byte(`{"Mounts":[{"Type":"bind","Source":"/host-root","Destination":"/"}]}`), &own)
	var document map[string]any
	json.Unmarshal([]byte(`{"services":{"app":{"volumes":[{"type":"bind","source":"/project/content","target":"/content"}]}}}`), &document)
	if err := remapComposeBindSources(document, own, "/project"); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(document)
	if !strings.Contains(string(encoded), `/host-root/project/content`) {
		t.Fatal("root mount prefix was lost")
	}
}

func TestComposeFileSecretsAndConfigsBecomeMappedReadOnlyMounts(t *testing.T) {
	var own updateContainer
	json.Unmarshal([]byte(`{"Mounts":[{"Type":"bind","Source":"/volume1/projects","Destination":"/opt/projects"}]}`), &own)
	var document map[string]any
	json.Unmarshal([]byte(`{"services":{"app":{"secrets":[{"source":"key"},{"source":"environment-key"}],"configs":[{"source":"settings","target":"/etc/app.conf"}]}},"secrets":{"key":{"file":"/opt/projects/example/key"},"environment-key":{"environment":"MY_SECRET"}},"configs":{"settings":{"file":"/opt/projects/example/settings"}}}`), &document)
	if !hasComposeBindSources(document) {
		t.Fatal("file resources not recognized")
	}
	if err := remapComposeBindSources(document, own, "/opt/projects/example"); err != nil {
		t.Fatal(err)
	}
	app := document["services"].(map[string]any)["app"].(map[string]any)
	mounts := app["volumes"].([]any)
	if len(mounts) != 2 {
		t.Fatalf("missing file mounts: %v", mounts)
	}
	for i, target := range []string{"/run/secrets/key", "/etc/app.conf"} {
		mount := mounts[i].(map[string]any)
		if mount["target"] != target || mount["read_only"] != true || !strings.HasPrefix(mount["source"].(string), "/volume1/projects/example/") {
			t.Fatalf("invalid resource mount: %v", mount)
		}
		if mount["bind"].(map[string]any)["create_host_path"] != false {
			t.Fatal("missing secret could silently become an empty directory")
		}
	}
	if len(app["secrets"].([]any)) != 1 {
		t.Fatal("environment-backed secret was rewritten")
	}
}

func TestComposeHostPathsPreserveLiteralDollarSigns(t *testing.T) {
	var own updateContainer
	json.Unmarshal([]byte(`{"Mounts":[{"Type":"bind","Source":"/volume1/$shared/projects","Destination":"/opt/projects"}]}`), &own)
	var document map[string]any
	json.Unmarshal([]byte(`{"services":{"app":{"environment":{"LITERAL":"$$HOME"},"volumes":[{"type":"bind","source":"/opt/projects/example/$$folder","target":"/data"}]}}}`), &document)
	if err := remapComposeBindSources(document, own, "/opt/projects/example"); err != nil {
		t.Fatal(err)
	}

	app := document["services"].(map[string]any)["app"].(map[string]any)
	source := app["volumes"].([]any)[0].(map[string]any)["source"]
	if source != "/volume1/$$shared/projects/example/$$folder" {
		t.Fatalf("host path would be interpolated a second time: %v", source)
	}
	if app["environment"].(map[string]any)["LITERAL"] != "$$HOME" {
		t.Fatal("literal environment value changed")
	}
}

func TestValidateComposeReferencedPathsCreatesMissingFiles(t *testing.T) {
	root := t.TempDir()
	document := map[string]any{
		"services": map[string]any{
			"caddy": map[string]any{
				"volumes": []any{
					map[string]any{"type": "bind", "source": filepath.ToSlash(filepath.Join(root, "Caddyfile")), "target": "/etc/caddy/Caddyfile"},
				},
			},
		},
	}
	if err := validateComposeReferencedPaths(document, filepath.ToSlash(root)); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, "Caddyfile"))
	if err != nil || !strings.Contains(string(content), "DockTun app is running") {
		t.Fatalf("expected generated Caddyfile, read error=%v content=%q", err, content)
	}
}

func TestValidateComposeReferencedPathsAcceptsExistingFilesAndDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "config"), 0755); err != nil {
		t.Fatal(err)
	}

	file := filepath.Join(root, "config", "app.env")
	if err := os.WriteFile(file, []byte("KEY=value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	document := map[string]any{
		"services": map[string]any{
			"app": map[string]any{
				"volumes": []any{
					map[string]any{"type": "bind", "source": filepath.ToSlash(filepath.Join(root, "config")), "target": "/etc/app/"},
				},
				"env_file": []any{filepath.ToSlash(file)},
				"build":    map[string]any{"context": filepath.ToSlash(root)},
			},
		},
	}
	if err := validateComposeReferencedPaths(document, filepath.ToSlash(root)); err != nil {
		t.Fatal(err)
	}
}

func TestValidateComposeReferencedPathsCreatesMissingDirectories(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "prometheus")
	document := map[string]any{
		"services": map[string]any{
			"prometheus": map[string]any{
				"volumes": []any{
					map[string]any{"type": "bind", "source": filepath.ToSlash(directory), "target": "/etc/prometheus/"},
				},
			},
		},
	}

	if err := validateComposeReferencedPaths(document, filepath.ToSlash(root)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		t.Fatalf("expected missing directory to be created, stat=%v", err)
	}
}

func TestComposePathLooksLikeFile(t *testing.T) {
	for _, path := range []string{"./Caddyfile", "./Dockerfile", "./config/app.yml"} {
		if !composePathLooksLikeFile(path, "/etc/app/config") {
			t.Fatalf("expected file path: %s", path)
		}
	}
	if composePathLooksLikeFile("./prometheus", "/etc/prometheus") {
		t.Fatal("directory path classified as file")
	}
}

func TestComposePortainerHostSourceUsesProjectStorage(t *testing.T) {
	var own updateContainer
	root := t.TempDir()
	project := filepath.ToSlash(filepath.Join(root, "project"))
	raw := fmt.Sprintf(`{"Mounts":[{"Type":"bind","Source":%q,"Destination":%q}]}`, filepath.ToSlash(root), project)
	if err := json.Unmarshal([]byte(raw), &own); err != nil {
		t.Fatal(err)
	}
	t.Logf("mounts=%+v", own.Mounts)
	source, ok := composePortainerHostSource("/srv/lsio/bazarr/config", own, project)
	if !ok || !strings.Contains(source, "/.docktun/portainer/bazarr/config") {
		t.Fatalf("unexpected Portainer source: %q, mapped=%v", source, ok)
	}
	if info, err := os.Stat(filepath.Join(root, "project", ".docktun", "portainer", "bazarr", "config")); err != nil || !info.IsDir() {
		t.Fatalf("expected Portainer directory scaffold, stat=%v", err)
	}
}

func TestComposeEnvironmentWithDefaultsFillsRequiredVariables(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "compose.yml")
	if err := os.WriteFile(source, []byte("services:\n  app:\n    image: example\n    environment:\n      URL: ${APP_URL:?required}\n      NAME: ${APP_NAME?required}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := composeEnvironmentWithDefaults(source)
	values := strings.Join(env, "\n")
	if !strings.Contains(values, "APP_URL=http://localhost") || !strings.Contains(values, "APP_NAME=docktun-generated") {
		t.Fatalf("required defaults missing: %s", values)
	}
}

func TestComposeEnvironmentWithDefaultsFillsBareVariables(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "compose.yml")
	if err := os.WriteFile(source, []byte("services:\n  app:\n    image: example\n    command: $SERVICE\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(composeEnvironmentWithDefaults(source), "\n"), "SERVICE=app") {
		t.Fatal("bare Compose variable was not given a default")
	}
}
