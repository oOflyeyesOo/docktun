package service

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const guidedSetupFixture = `# App instructions stay available.
services:
  app:
    image: alpine:3.23
    environment:
      SHARED: ${APP_PASSWORD:?Choose a password}
      PUID: "1000"
      TZ: America/Chicago
    ports:
      - "127.0.0.1:8080:80/tcp"
    volumes:
      - /path/to/config:/data
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - /volume1/settings.yml:/etc/app/settings.yml:ro
      - /etc/localtime:/etc/localtime:ro
  worker:
    image: alpine:3.23
    environment:
      - SHARED=${APP_PASSWORD:?Choose a password}
      - USERNAME=sample-user
    volumes:
      - type: bind
        source: /path/to/config
        target: /shared
        read_only: true
        bind:
          create_host_path: false
`

func TestGuidedSetupKeepsSharedCredentialsAndStorage(t *testing.T) {
	input := TemplateSetupInput{Compose: guidedSetupFixture, Env: "# Keep this comment\nEXTRA=unchanged\n"}
	schema, err := ConfigureTemplate(input)
	if err != nil {
		t.Fatal(err)
	}
	passwords := 0
	input.Values = map[string]string{}
	for _, field := range schema.Fields {
		if field.Password {
			passwords++
			if !field.Secret || !field.Required || field.ID != "var:APP_PASSWORD" {
				t.Fatalf("password metadata: %+v", field)
			}
		}
		if field.Name == "Host port → 80/tcp" {
			input.Values[field.ID] = "8181"
		}
		if field.Name == "USERNAME" {
			input.Values[field.ID] = "test$literal"
		}
	}
	if passwords != 1 {
		t.Fatalf("shared password exposed %d times", passwords)
	}
	input.Values["var:APP_PASSWORD"] = `p$'\#word`
	input.Storage = map[string]SetupStorageChoice{}
	for _, mount := range schema.Storage {
		if mount.Source == "/path/to/config" {
			if !mount.CanManage {
				t.Fatal(mount)
			}
			input.Storage[mount.ID] = SetupStorageChoice{Mode: "managed"}
		} else if mount.CanManage {
			t.Fatalf("file/socket offered automatic storage: %+v", mount)
		}
	}
	input.Apply = true
	result, err := ConfigureTemplate(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Compose, "App instructions stay available") || !strings.Contains(result.Compose, "127.0.0.1:8181:80/tcp") || !strings.Contains(result.Compose, "USERNAME=test$$literal") || !strings.Contains(result.Env, "# Keep this comment") {
		t.Fatalf("settings lost: %+v", result)
	}
	values, err := setupEnv(result.Env)
	if err != nil || values["APP_PASSWORD"] != `p$'\#word` || values["EXTRA"] != "unchanged" {
		t.Fatalf("Environment values changed: %+v %v", values, err)
	}
	var doc map[string]any
	if err = yaml.Unmarshal([]byte(result.Compose), &doc); err != nil {
		t.Fatal(err)
	}
	volumes := doc["volumes"].(map[string]any)
	if len(volumes) != 1 {
		t.Fatalf("shared source became separate volumes: %+v", volumes)
	}
	services := doc["services"].(map[string]any)
	one := services["app"].(map[string]any)["volumes"].([]any)[0].(map[string]any)
	two := services["worker"].(map[string]any)["volumes"].([]any)[0].(map[string]any)
	if one["source"] != two["source"] || two["read_only"] != true || one["type"] != "volume" {
		t.Fatalf("shared access lost: %+v %+v", one, two)
	}
}

func TestGuidedSetupRejectsLossyMountConversionsAndStaleFields(t *testing.T) {
	schema, err := ConfigureTemplate(TemplateSetupInput{Compose: guidedSetupFixture})
	if err != nil {
		t.Fatal(err)
	}
	for _, mount := range schema.Storage {
		if mount.CanManage {
			continue
		}
		if _, err = ConfigureTemplate(TemplateSetupInput{Compose: guidedSetupFixture, Apply: true, Storage: map[string]SetupStorageChoice{mount.ID: {Mode: "managed"}}}); err == nil {
			t.Fatalf("converted special mount %+v", mount)
		}
	}
	for _, input := range []TemplateSetupInput{
		{Compose: guidedSetupFixture, Apply: true, Values: map[string]string{"stale": "bad"}},
		{Compose: guidedSetupFixture, Apply: true, Values: map[string]string{"var:APP_PASSWORD": "bad\nINJECTED=value"}},
		{Compose: guidedSetupFixture, Apply: true, Storage: map[string]SetupStorageChoice{"mount:app:0": {Mode: "managed"}}},
		{Compose: strings.ReplaceAll(guidedSetupFixture, "/path/to/config\n", "/path/to/config/child\n"), Apply: true, Storage: map[string]SetupStorageChoice{"mount:app:0": {Mode: "managed"}, "mount:worker:0": {Mode: "managed"}}},
		{Compose: guidedSetupFixture, Env: "SECRET=${ANOTHER_VALUE}\n"},
	} {
		if _, err = ConfigureTemplate(input); err == nil {
			t.Fatalf("accepted unsafe/lossy setup %+v", input)
		}
	}
}

func TestGuidedSetupPreservesUneditedHostVariablesAndAliases(t *testing.T) {
	compose := `services:
  one:
    image: alpine
    environment: &shared
      TZ: UTC
      TOKEN: ${TOKEN:-original}
    volumes:
      - ${MEDIA_PATH:-/volume1/media}:/media:ro
  two:
    image: alpine
    environment: *shared
`
	result, err := ConfigureTemplate(TemplateSetupInput{Compose: compose, Apply: true, Storage: map[string]SetupStorageChoice{"mount:one:0": {Mode: "host", Source: "${MEDIA_PATH:-/volume1/media}"}}})
	if err != nil || !strings.Contains(result.Compose, "${MEDIA_PATH:-/volume1/media}") || strings.Contains(result.Compose, "$${MEDIA_PATH") || !strings.Contains(result.Compose, "*shared") {
		t.Fatalf("existing interpolation/aliases changed: %+v %v", result, err)
	}
}

func TestGuidedSetupDisplaysComposeDefaultWithAnEmptyEnvironmentValue(t *testing.T) {
	schema, err := ConfigureTemplate(TemplateSetupInput{Compose: "services:\n  app:\n    image: alpine\n    environment:\n      TZ: ${TZ:-America/Chicago}\n", Env: "TZ=''\n"})
	if err != nil || len(schema.Fields) != 1 || schema.Fields[0].Value != "America/Chicago" || schema.Fields[0].Required {
		t.Fatalf("wrong effective default: %+v %v", schema, err)
	}
}
