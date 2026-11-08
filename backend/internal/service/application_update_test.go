package service

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApplicationReplacementPreservesDatabaseDefinition(t *testing.T) {
	var old updateContainer
	raw := `{"Id":"0123456789abcdef0123456789abcdef","Name":"/app","Config":{"Image":"example:latest","Entrypoint":["/custom-entry"],"Cmd":["serve"],"Env":["DATABASE_URL=postgres://user:literal$secret@postgres:5432/app","PATH=/custom/bin"],"Healthcheck":{"Test":["CMD","check-db"]}},"HostConfig":{"Binds":["db-data:/data:rw"],"RestartPolicy":{"Name":"no"},"GroupAdd":["1000"],"CapDrop":["ALL"]},"Mounts":[{"Type":"volume","Name":"db-data","Destination":"/data","RW":true}],"NetworkSettings":{"Networks":{"db-net":{"Aliases":["app","0123456789ab"]},"ingress":{"Aliases":["app"]}}}}`
	if err := json.Unmarshal([]byte(raw), &old); err != nil {
		t.Fatal(err)
	}
	body := replacementCreateBody(old, "sha256:new", false)
	encoded, _ := json.Marshal(body)
	text := string(encoded)
	for _, want := range []string{"/custom-entry", "literal$secret", "PATH=/custom/bin", "GroupAdd", "CapDrop", "check-db", "db-net", "ingress", "db-data"} {
		if !strings.Contains(text, want) {
			t.Fatal("lost", want)
		}
	}
	if strings.Contains(text, "DOCKTUN_CONTAINER") {
		t.Fatal("injected core identity into application")
	}
	next := old
	if err := verifyReplacementStorageNetworks(old, next); err != nil {
		t.Fatal(err)
	}
	next.Mounts = nil
	if verifyReplacementStorageNetworks(old, next) == nil {
		t.Fatal("lost database mount accepted")
	}
	next = old
	next.NetworkSettings.Networks = map[string]map[string]any{"ingress": {"Aliases": []any{"app"}}}
	if verifyReplacementStorageNetworks(old, next) == nil {
		t.Fatal("lost database network accepted")
	}
}
func TestCoreUpdateRequiresPersistentDatabaseDirectory(t *testing.T) {
	var c updateContainer
	json.Unmarshal([]byte(`{"Config":{"Env":["DB_PATH=/database/auth.db"]},"Mounts":[{"Type":"volume","Name":"progress","Destination":"/app/data","RW":true}]}`), &c)
	if validateCorePersistence(c) == nil {
		t.Fatal("ephemeral authentication database accepted")
	}
	json.Unmarshal([]byte(`{"Config":{"Env":["DB_PATH=/app/data/auth.db"]},"Mounts":[{"Type":"volume","Name":"data","Destination":"/app/data","RW":true}]}`), &c)
	if err := validateCorePersistence(c); err != nil {
		t.Fatal(err)
	}
}
