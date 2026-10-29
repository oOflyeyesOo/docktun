package service

import (
	"encoding/json"
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
