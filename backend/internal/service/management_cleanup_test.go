package service

import (
	"encoding/json"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func linkFixture(t *testing.T, text string) databaseLinkContainer {
	t.Helper()
	var c databaseLinkContainer
	if err := json.Unmarshal([]byte(text), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDatabaseRecoveryRequiresExplicitUnambiguousDependency(t *testing.T) {
	target := linkFixture(t, `{"Name":"/app","Config":{"Env":[],"Labels":{"com.docker.compose.project":"stack"}},"NetworkSettings":{"Networks":{"app_net":{}}}}`)
	db := linkFixture(t, `{"Name":"/stack-postgres","State":{"Running":true},"Config":{"Labels":{"com.docker.compose.project":"stack","com.docker.compose.service":"postgres"}},"NetworkSettings":{"Networks":{"db_net":{"Aliases":["postgres"]},"docktun-ts-private":{"Aliases":["postgres"]}}}}`)
	if got := databaseLinkNetworks(target, []databaseLinkContainer{db}); len(got) != 0 {
		t.Fatal("guessed a database without configuration", got)
	}
	target.Config.Env = []string{"DATABASE_URL=postgres://user:p%40ss@postgres:5432/app"}
	if got := databaseLinkNetworks(target, []databaseLinkContainer{db}); !reflect.DeepEqual(got, []string{"db_net"}) {
		t.Fatal(got)
	}
	unrelated := db
	unrelated.Name = "/other-db"
	unrelated.Config.Labels = map[string]string{"com.docker.compose.project": "other", "com.docker.compose.service": "postgres"}
	if got := databaseLinkNetworks(target, []databaseLinkContainer{unrelated}); len(got) != 0 {
		t.Fatal("selected another stack", got)
	}
	ambiguous := db
	ambiguous.Name = "/second-db"
	if got := databaseLinkNetworks(target, []databaseLinkContainer{db, ambiguous}); len(got) != 0 {
		t.Fatal("selected ambiguous dependency", got)
	}
	db.State.Running = false
	if got := databaseLinkNetworks(target, []databaseLinkContainer{db}); len(got) != 0 {
		t.Fatal("selected stopped dependency", got)
	}
}

func TestComposeVolumeRepairPreservesDocumentAndHostFiles(t *testing.T) {
	original := []byte("services:\n  app:\n    image: alpine\n    volumes:\n      - existing:/old\n      - missing:/data\n      - ./settings.yml:/etc/settings.yml:ro\n      - type: volume\n        source: longform\n        target: /long\nvolumes:\n  existing: {external: true}\nnetworks:\n  private: {internal: true}\n")
	result, changed := repairComposeVolumes(original)
	if !changed {
		t.Fatal("missing volumes not repaired")
	}
	var doc map[string]any
	if err := yaml.Unmarshal(result, &doc); err != nil {
		t.Fatal(err)
	}
	volumes := doc["volumes"].(map[string]any)
	if len(volumes) != 3 || volumes["missing"] == nil || volumes["longform"] == nil {
		t.Fatal(string(result))
	}
	if volumes["existing"].(map[string]any)["external"] != true || len(doc["networks"].(map[string]any)) != 1 {
		t.Fatal("changed existing definitions", string(result))
	}
	if _, again := repairComposeVolumes(result); again {
		t.Fatal("repair is not idempotent")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if !SanitizeComposeVolumes(path) {
		t.Fatal("repair failed")
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.yml")); !os.IsNotExist(err) {
		t.Fatal("created a directory for a file mount")
	}
	backups, _ := filepath.Glob(path + ".bak-*")
	if len(backups) != 1 {
		t.Fatal("missing backup")
	}
	saved, _ := os.ReadFile(backups[0])
	if string(saved) != string(original) {
		t.Fatal("backup changed")
	}
}

func TestComposeVolumeRepairKeepsExistingInlineAndInvalidYAML(t *testing.T) {
	for _, source := range []string{
		"services: {app: {image: alpine, volumes: ['data:/data']}}\nvolumes: {data: {}}\n",
		"services: [broken\n",
		"services: {}\nvolumes: {}\nvolumes: {}\n",
		"services: {app: {image: alpine, volumes: ['/host/file:/file', '${DATA_PATH}:/data']}}\n",
	} {
		result, changed := repairComposeVolumes([]byte(source))
		if changed || !strings.EqualFold(string(result), source) {
			t.Fatal("modified existing/invalid document", string(result))
		}
	}
}
