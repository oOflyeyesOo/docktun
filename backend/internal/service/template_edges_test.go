package service

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTemplateCredentialsStayScopedToReferencedProvider(t *testing.T) {
	compose := `services:
  db1:
    image: postgres:16-alpine
    environment: {POSTGRES_PASSWORD: changeme}
  app1:
    image: alpine
    environment: {DB_HOST: db1, DB_PASSWORD: changeme, ADMIN_PASSWORD: changeme}
  db2:
    image: postgres:16-alpine
    environment: {POSTGRES_PASSWORD: changeme}
  app2:
    image: alpine
    environment: {DB_HOST: db2, DB_PASSWORD: changeme}
  archive:
    image: alpine
    environment: {BACKUP_PASSWORD: changeme}
`
	schema, err := ConfigureTemplate(TemplateSetupInput{Compose: compose})
	if err != nil || len(schema.CredentialLinks) != 2 {
		t.Fatal(schema.CredentialLinks, err)
	}
	links := map[string]string{}
	for _, link := range schema.CredentialLinks {
		uses := strings.Join(link.Uses, ",")
		if strings.Contains(uses, "archive") || strings.Contains(uses, "ADMIN_PASSWORD") || len(link.Fields) != 2 {
			t.Fatal("unrelated credentials were linked", link)
		}
		if strings.Contains(uses, "db1") {
			if strings.Contains(uses, "db2") || strings.Contains(uses, "app2") {
				t.Fatal("database accounts crossed", link)
			}
			links[link.ID] = "first-database-secret"
		} else {
			links[link.ID] = "second-database-secret"
		}
	}
	configured, err := ConfigureTemplate(TemplateSetupInput{Compose: compose, Apply: true, CredentialLinks: links})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	yaml.Unmarshal([]byte(configured.Compose), &doc)
	services := doc["services"].(map[string]any)
	for _, item := range []struct{ service, key, want string }{
		{"db1", "POSTGRES_PASSWORD", "first-database-secret"}, {"app1", "DB_PASSWORD", "first-database-secret"},
		{"db2", "POSTGRES_PASSWORD", "second-database-secret"}, {"app2", "DB_PASSWORD", "second-database-secret"},
		{"app1", "ADMIN_PASSWORD", "changeme"}, {"archive", "BACKUP_PASSWORD", "changeme"},
	} {
		environment := services[item.service].(map[string]any)["environment"].(map[string]any)
		if environment[item.key] != item.want {
			t.Fatal(item, environment)
		}
	}
}

func TestTemplateRootAndAppDatabaseAccountsRemainIndependent(t *testing.T) {
	compose := `services:
  database:
    image: mysql:8
    environment: {MYSQL_USER: appuser, MYSQL_PASSWORD: sample, MYSQL_ROOT_PASSWORD: sample}
  app:
    image: alpine
    environment: {DATABASE_URL: "mysql://appuser:sample@database/app"}
  admin:
    image: alpine
    environment: {DATABASE_URL: "mysql://root:sample@database/mysql"}
`
	schema, err := ConfigureTemplate(TemplateSetupInput{Compose: compose})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, link := range schema.CredentialLinks {
		if link.Kind != "password" {
			continue
		}
		count++
		uses := strings.Join(link.Uses, ",")
		if len(link.Fields) != 1 || (strings.Contains(uses, "app ·") && strings.Contains(uses, "admin ·")) {
			t.Fatal("root and app accounts linked", link)
		}
	}
	if count != 2 {
		t.Fatal(schema.CredentialLinks)
	}
}

func TestSharedEnvironmentCredentialsRetainProvenProviderLinks(t *testing.T) {
	compose := "services:\n  database:\n    image: postgres:16-alpine\n    environment:\n      POSTGRES_PASSWORD: ${DB_CREDENTIAL}\n  app:\n    image: alpine\n    environment: {DB_HOST: database, DB_PASSWORD: sample}\n"
	input := TemplateSetupInput{Compose: compose, Env: "DB_CREDENTIAL=sample\n"}
	schema, err := ConfigureTemplate(input)
	if err != nil || len(schema.CredentialLinks) != 1 {
		t.Fatal(schema, err)
	}
	link := schema.CredentialLinks[0]
	if link.Kind != "password" || !strings.Contains(strings.Join(link.Fields, ","), "var:DB_CREDENTIAL") {
		t.Fatal(link)
	}
	input.Apply, input.CredentialLinks = true, map[string]string{link.ID: "new-secret"}
	result, err := ConfigureTemplate(input)
	if err != nil || !strings.Contains(result.Env, "new-secret") || !strings.Contains(result.Compose, "DB_PASSWORD: new-secret") || !strings.Contains(result.Compose, "${DB_CREDENTIAL}") {
		t.Fatal(result, err)
	}
	rootSchema, err := ConfigureTemplate(TemplateSetupInput{Compose: "services:\n  database:\n    image: mongo:8\n    environment: {MONGO_INITDB_ROOT_USERNAME: '${ROOT_NAME}', MONGO_INITDB_ROOT_PASSWORD: '${ROOT_SECRET}'}\n  app:\n    image: alpine\n    environment: {DATABASE_URL: 'mongodb://admin:sample@database/app'}\n", Env: "ROOT_NAME=admin\nROOT_SECRET=sample\n"})
	if err != nil || len(rootSchema.CredentialLinks) != 2 {
		t.Fatal("shared root-account identity was lost", rootSchema.CredentialLinks, err)
	}
}

func TestAdvancedPortsDoNotBorrowMetadataFromNextEditablePort(t *testing.T) {
	compose := `services:
  app:
    image: alpine
    ports:
      - {target: 8000, published: "18000-18010", protocol: tcp}
      - {target: 8001, published: "18011", protocol: sctp}
      - {target: 9000, published: "19000", protocol: udp}
      - "${BIND_IP:-127.0.0.1}:18080:80"
`
	schema, err := ConfigureTemplate(TemplateSetupInput{Compose: compose})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	values := map[string]string{}
	for _, field := range schema.Fields {
		if field.Kind != "port" {
			continue
		}
		count++
		if len(field.PortUses) != 1 {
			t.Fatal(field)
		}
		if field.Value == "19000" && field.PortUses[0].Protocol == "udp" {
			values[field.ID] = "19001"
		} else if field.Value == "18080" && field.PortUses[0].IP == "${BIND_IP:-127.0.0.1}" {
			values[field.ID] = "18081"
		} else {
			t.Fatal("port metadata is associated with the wrong field", field)
		}
	}
	if count != 2 {
		t.Fatal(schema.Fields)
	}
	configured, err := ConfigureTemplate(TemplateSetupInput{Compose: compose, Apply: true, Values: values})
	if err != nil || !strings.Contains(configured.Compose, "18000-18010") || !strings.Contains(configured.Compose, "sctp") || !strings.Contains(configured.Compose, "${BIND_IP:-127.0.0.1}:18081:80") || !strings.Contains(configured.Compose, "19001") {
		t.Fatal(configured.Compose, err)
	}
}

func TestAutoDependenciesDoNotWaitForDisabledOrUnknownHealthchecks(t *testing.T) {
	for _, health := range []string{"{test: [NONE]}", "{test: []}", "{disable: true, test: [CMD, true]}", "{disable: TRUE, test: [CMD, true]}", "{interval: 10s}"} {
		compose := "services:\n  db:\n    image: postgres:16-alpine\n    healthcheck: " + health + "\n  app:\n    image: alpine\n    environment: {DB_HOST: db}\n"
		schema, err := ConfigureTemplate(TemplateSetupInput{Compose: compose})
		if err != nil || len(schema.Dependencies) != 1 || schema.Dependencies[0].Healthy {
			t.Fatal(health, schema, err)
		}
		result, err := ConfigureTemplate(TemplateSetupInput{Compose: compose, Apply: true, ConnectDependencies: true})
		if err != nil || strings.Contains(result.Compose, "service_healthy") || !strings.Contains(result.Compose, "service_started") {
			t.Fatal(health, result.Compose, err)
		}
	}
}

func TestNativeReadOnlySuffixIsAnOptionRatherThanDestinationPath(t *testing.T) {
	content, err := portainerCompose([]byte(`{"type":1,"title":"Clock files","image":"alpine:3.23","volumes":[{"bind":" /etc/timezone","container":"/etc/timezone:ro"},{"bind":"/etc/localtime","container":"/etc/localtime:ro"},{"container":"/data:rw"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := ConfigureTemplate(TemplateSetupInput{Compose: content})
	if err != nil || len(schema.Storage) != 2 {
		t.Fatal(schema, err)
	}
	for _, mount := range schema.Storage {
		if mount.Source != mount.Target || !mount.ReadOnly || mount.CanManage || strings.Contains(mount.Target, ":") {
			t.Fatal("host clock file was offered as an empty data volume", mount)
		}
	}
	if _, err := portainerCompose([]byte(`{"type":1,"title":"Advanced mount","image":"alpine","volumes":[{"container":"/data:ro,z"}]}`)); err == nil {
		t.Fatal("silently imported unsupported inline mount options")
	}
}
