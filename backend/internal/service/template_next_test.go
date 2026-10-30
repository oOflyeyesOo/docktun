package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const templateDependencyFixture = `services:
  client:
    image: postgres:16-alpine
    environment:
      DATABASE_URL: postgres://testuser:old-password@database/testdb
      PUBLIC_URL: http://localhost:18080
    ports:
      - "18080:80"
  database:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: testuser
      POSTGRES_PASSWORD: old-password
      POSTGRES_DB: testdb
    healthcheck:
      test: [CMD-SHELL, 'pg_isready -U $${POSTGRES_USER}']
`

func TestDependencySetupLinksCredentialsAndEncodedURLs(t *testing.T) {
	input := TemplateSetupInput{Compose: templateDependencyFixture, ConnectDependencies: true}
	schema, err := ConfigureTemplate(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(schema.Dependencies) != 1 || schema.Dependencies[0].Target != "database" || !schema.Dependencies[0].Healthy {
		t.Fatalf("dependency not detected: %+v", schema.Dependencies)
	}
	if len(schema.CredentialLinks) != 3 {
		t.Fatalf("expected username, password and database links: %+v", schema.CredentialLinks)
	}
	input.Apply = true
	input.CredentialLinks = map[string]string{}
	for _, link := range schema.CredentialLinks {
		value := ""
		switch link.Kind {
		case "username":
			value = "easy_user"
		case "database":
			value = "easy_db"
		case "password":
			value = `p$'\#word\`
		}
		input.CredentialLinks[link.ID] = value
	}
	result, err := ConfigureTemplate(input)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	yaml.Unmarshal([]byte(result.Compose), &doc)
	services := doc["services"].(map[string]any)
	client := services["client"].(map[string]any)
	env := client["environment"].(map[string]any)
	address, err := url.Parse(strings.ReplaceAll(env["DATABASE_URL"].(string), "$$", "$"))
	if err != nil {
		t.Fatal(err)
	}
	password, _ := address.User.Password()
	if password != `p$'\#word\` || address.User.Username() != "easy_user" || address.Path != "/easy_db" {
		t.Fatalf("linked URI did not retain credentials: %s", address)
	}
	dependency := client["depends_on"].(map[string]any)["database"].(map[string]any)
	if dependency["condition"] != "service_healthy" {
		t.Fatal(dependency)
	}
	dbEnv := services["database"].(map[string]any)["environment"].(map[string]any)
	if dbEnv["POSTGRES_PASSWORD"] != `p$$'\#word\` {
		t.Fatal(dbEnv)
	}
	input.Compose = strings.Replace(templateDependencyFixture, "    healthcheck:", "    depends_on: [client]\n    healthcheck:", 1)
	if _, err = ConfigureTemplate(input); err == nil {
		t.Fatal("allowed dependency cycle")
	}
}

func TestPortMetadataIncludesVariablesTCPUDPAndIPv6(t *testing.T) {
	schema, err := ConfigureTemplate(TemplateSetupInput{Compose: `services:
  dns:
    image: alpine
    ports:
      - "${HOST_PORT:-18530}:53/tcp"
      - "${HOST_PORT:-18530}:53/udp"
      - "[::1]:18081:80"
`})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, field := range schema.Fields {
		if field.ID == "var:HOST_PORT" {
			if field.Kind != "port" || len(field.PortUses) != 2 {
				t.Fatal(field)
			}
			found = true
		}
		if len(field.PortUses) > 0 && field.PortUses[0].IP == "::1" {
			if field.Value != "18081" {
				t.Fatal(field)
			}
		}
	}
	if !found {
		t.Fatal(schema.Fields)
	}
}

func TestSupportingFilesAreConfinedPrivateAndNeverOverwrite(t *testing.T) {
	for _, name := range []string{"../escape", "/tmp/escape", ".env", "compose.yml", ".git/config", `a\b`, "a/../file"} {
		if _, err := decodeTemplateFiles(map[string]TemplateProjectFile{name: {Content: "data"}}); err == nil {
			t.Fatalf("accepted unsafe supporting file %q", name)
		}
	}
	directory := t.TempDir()
	files := map[string]TemplateProjectFile{"secrets/password.txt": {Content: "private", Secret: true}, "config/app.conf": {Content: "public"}}
	staged, err := writeStagedTemplateFiles(directory, files)
	if err != nil || len(staged) < 2 {
		t.Fatal(staged, err)
	}
	info, _ := os.Stat(filepath.Join(directory, "secrets/password.txt"))
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	svc := projectPathService(t)
	project, err := svc.Create("assets", t.TempDir(), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.SaveTemplateFiles(project.ID, files); err != nil {
		t.Fatal(err)
	}
	files["secrets/password.txt"] = TemplateProjectFile{Content: "replacement", Secret: true}
	if err = svc.SaveTemplateFiles(project.ID, files); err == nil {
		t.Fatal("overwrote existing secret")
	}
}

func TestExistingSupportingFileReviewUsesActualPermissions(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "settings.txt"), []byte("same"), 0600); err != nil {
		t.Fatal(err)
	}
	input := ComposeReviewInput{Compose: "services: {}", Files: map[string]TemplateProjectFile{"settings.txt": {Content: "same"}}}
	temporary := t.TempDir()
	_, _, err := prepareTemplateFileReview(input, directory, temporary, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(filepath.Join(temporary, "settings.txt"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("review used new-file permissions instead of existing permissions", info.Mode())
	}
	if err := os.Chmod(filepath.Join(directory, "settings.txt"), 0644); err != nil {
		t.Fatal(err)
	}
	input.Files["settings.txt"] = TemplateProjectFile{Content: "same", Secret: true}
	if _, _, err = prepareTemplateFileReview(input, directory, t.TempDir(), map[string]any{}); err == nil {
		t.Fatal("accepted an existing secret readable by other users")
	}
}

func TestNativePostgresTemplateUsesMatchingMajorVersionStorage(t *testing.T) {
	for _, image := range []string{"postgres", "postgres:latest", "postgres:18", "docker.io/library/postgres:18.1-alpine", "postgres:17-alpine", "example/postgres:18", "postgres@sha256:abc"} {
		raw, _ := json.Marshal(map[string]any{"type": 1, "title": "Database", "image": image, "volumes": []any{map[string]any{"container": "/var/lib/postgresql/data"}}})
		content, err := portainerCompose(raw)
		if err != nil {
			t.Fatal(err)
		}
		wantModern := image == "postgres" || image == "postgres:latest" || image == "postgres:18" || image == "docker.io/library/postgres:18.1-alpine"
		if strings.Contains(content, "target: /var/lib/postgresql/data") == wantModern {
			t.Fatal(image, content)
		}
	}
	content, err := portainerCompose([]byte(`{"type":1,"title":"Database","image":"postgres:18","env":[{"name":"PGDATA","default":"/var/lib/postgresql/data/custom"}],"volumes":[{"container":"/var/lib/postgresql/data"}]}`))
	if err != nil || !strings.Contains(content, "target: /var/lib/postgresql/data") {
		t.Fatal("changed explicit PGDATA", content, err)
	}
}

func TestRequiredFilesPinRepositoryAndPreserveBuildAssets(t *testing.T) {
	library := NewTemplateLibrary()
	revision := strings.Repeat("a", 40)
	library.client.Transport = templateTransport(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
			t.Fatal("credentials forwarded")
		}
		body := ""
		if req.URL.Host == "api.github.com" {
			body = `{"tree":[{"path":"app/Dockerfile","type":"blob","mode":"100644","size":20},{"path":"app/start.sh","type":"blob","mode":"100755","size":10},{"path":"app/nginx.conf","type":"blob","mode":"100644","size":10},{"path":"other/password.txt","type":"blob","mode":"100644","size":10}]}`
		} else if strings.HasSuffix(req.URL.Path, "Dockerfile") {
			body = "FROM alpine:3.23\nCOPY start.sh /start.sh\n"
		} else {
			body = "example"
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})
	result, err := library.TemplateFiles(context.Background(), TemplateFilesInput{Compose: "services:\n  app:\n    build: .\n    volumes: [./nginx.conf:/etc/nginx/nginx.conf:ro]\n", Source: "https://raw.githubusercontent.com/example/repo/" + revision + "/app/compose.yml", Fetch: true})
	if err != nil || len(result.Files) != 3 || !result.Files["start.sh"].Executable || !strings.Contains(result.Files["nginx.conf"].Source, revision) {
		t.Fatalf("wrong source snapshot: %+v %v", result, err)
	}
	plan, _, err := planTemplateFiles(TemplateFilesInput{Compose: "services:\n  db:\n    image: postgres\nsecrets:\n  password:\n    file: ./secrets/db-password.txt\n"})
	if err != nil || len(plan.Requirements) != 1 || !plan.Requirements[0].Secret {
		t.Fatal(plan, err)
	}
}

func TestArchitectureMetadataAndDeclaredPermissions(t *testing.T) {
	platforms := manifestTemplatePlatforms([]byte(`[{"Descriptor":{"platform":{"os":"linux","architecture":"arm64","variant":"v8"}}},{"Descriptor":{"platform":{"os":"linux","architecture":"amd64"}}}]`))
	if len(platforms) != 2 || !templatePlatformMatches("linux/arm64", platforms[0]) || templatePlatformMatches("linux/amd64", platforms[0]) {
		t.Fatal(platforms)
	}
	user, _ := declaredTemplateUser(map[string]any{"environment": map[string]any{"PUID": "1026", "PGID": "100"}}, "")
	if user != "1026:100" {
		t.Fatal(user)
	}
	raw, _ := json.Marshal(platforms)
	if len(raw) == 0 {
		t.Fatal("no platforms")
	}
}

func TestPinnedImportHandlesBranchNamesWithSlashes(t *testing.T) {
	library := NewTemplateLibrary()
	library.client.Transport = templateTransport(func(req *http.Request) (*http.Response, error) {
		status, body := 200, `{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
		if strings.HasSuffix(req.URL.Path, "/commits/feature") {
			status, body = 404, "missing"
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})
	pinned, err := library.pinFile(context.Background(), "https://raw.githubusercontent.com/a/b/feature/setup/compose.yml")
	if err != nil || pinned != "https://raw.githubusercontent.com/a/b/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/compose.yml" {
		t.Fatal(pinned, err)
	}
}

func TestFetchedEnvironmentSeedsSettingsWithoutOverwritingRootEnvironment(t *testing.T) {
	library := NewTemplateLibrary()
	revision := strings.Repeat("a", 40)
	library.client.Transport = templateTransport(func(req *http.Request) (*http.Response, error) {
		body := "DB_USER=example\n"
		if req.URL.Host == "api.github.com" {
			body = `{"tree":[{"path":"app/.env","type":"blob","mode":"100644","size":15}]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})
	result, err := library.TemplateFiles(context.Background(), TemplateFilesInput{Compose: "services:\n  app:\n    image: alpine\n    env_file: .env\n", Source: "https://raw.githubusercontent.com/a/b/" + revision + "/app/compose.yml", Fetch: true})
	if err != nil || result.EnvExample != "DB_USER=example\n" || len(result.Files) != 0 {
		t.Fatal(result, err)
	}
}
