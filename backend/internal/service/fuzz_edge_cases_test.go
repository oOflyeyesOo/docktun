package service

import (
	"docktun/internal/config"
	"docktun/internal/db"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"gopkg.in/yaml.v3"
)

// 1. Compose YAML Edge Cases
func TestComposeYAMLMalformedAndEdgeCases(t *testing.T) {
	// Missing version is valid in modern compose spec
	validNoVersion := `services:
  web:
    image: nginx:alpine
    ports:
      - "8080:80"
`
	setup, err := ConfigureTemplate(TemplateSetupInput{Compose: validNoVersion})
	if err != nil {
		t.Fatalf("expected missing version to be supported, got error: %v", err)
	}
	if len(setup.Fields) == 0 {
		t.Fatalf("expected fields to be extracted, got none")
	}

	// Invalid services blocks: null, list, scalar, missing
	invalidServicesCases := []struct {
		name    string
		compose string
	}{
		{"services is null", "services: null\n"},
		{"services is tilde", "services: ~\n"},
		{"services is list", "services:\n  - web\n  - db\n"},
		{"services is string", "services: \"just a string\"\n"},
		{"services is number", "services: 42\n"},
		{"missing services block", "version: '3.8'\nnetworks:\n  default:\n"},
		{"root is scalar", "\"not a mapping\"\n"},
		{"root is list", "- item1\n- item2\n"},
		{"empty content", ""},
		{"whitespace content", "   \n\t  \n"},
		{"syntax error", "services:\n  app:\n    [invalid: yaml\n"},
	}

	for _, tc := range invalidServicesCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ConfigureTemplate(TemplateSetupInput{Compose: tc.compose})
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}

	// Null ports and edge-case ports
	nullAndEdgePorts := `services:
  app:
    image: alpine:3.23
    ports:
      - null
      - ~
      - ""
      - ":"
      - ":80"
      - "80:"
      - "abc:def"
      - "70000:80"
      - "80:70000"
      - "80:80/invalid_proto"
      - {target: null}
      - {published: null}
      - {target: 0}
      - {target: 65536}
      - {target: 8080, published: 8080, protocol: "tcp"}
`
	res, err := ConfigureTemplate(TemplateSetupInput{Compose: nullAndEdgePorts})
	if err != nil {
		t.Fatalf("unexpected error parsing compose with null/edge ports: %v", err)
	}
	// Only the valid port (8080) should be editable
	portFields := 0
	for _, f := range res.Fields {
		if f.Kind == "port" {
			portFields++
			if f.Value != "8080" {
				t.Errorf("unexpected port field value: %s", f.Value)
			}
		}
	}
	if portFields != 1 {
		t.Errorf("expected exactly 1 valid editable port field, got %d", portFields)
	}

	// Null volumes and null environments
	nullVolsAndEnv := `services:
  app:
    image: alpine:3.23
    environment:
      - null
      - ""
      - "NO_EQUALS"
      - "VALID=hello"
    volumes:
      - null
      - ""
      - "/host/path:/container/path:ro"
`
	res2, err := ConfigureTemplate(TemplateSetupInput{Compose: nullVolsAndEnv})
	if err != nil {
		t.Fatalf("unexpected error parsing compose with null volumes and env: %v", err)
	}
	if len(res2.Storage) != 1 || res2.Storage[0].Source != "/host/path" {
		t.Errorf("expected 1 storage mount, got: %+v", res2.Storage)
	}
}

// 2. Container Names Edge Cases (Unicode, dashes, spaces, casing, and substring collision safety)
func TestContainerNameEdgeCasesAndNoSubstringCollision(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	database, err := db.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	cfgFile := filepath.Join(tempDir, "config.yml")
	initialConfig := `tunnel: test-tunnel-id
credentials-file: /etc/cloudflared/creds.json
ingress:
  - hostname: worker.example.com
    service: http://app-worker:8080
  - hostname: dashboard.example.com
    service: http://app:3000
  - service: http_status:404
`
	if err := os.WriteFile(cfgFile, []byte(initialConfig), 0600); err != nil {
		t.Fatal(err)
	}

	appCfg := &config.Config{CloudflaredConfig: cfgFile}
	cfgMgr := NewConfigManager(appCfg, database)
	containers := NewDockerContainers(appCfg, database, cfgMgr, nil, nil, nil)

	// Case 1: Substring collision safety
	// Container name "app" should NOT overwrite or remove "app-worker"
	exposureSettings := ExposureSettings{
		Mode:     "public",
		Hostname: "newapp.example.com",
		Target:   "http://app:3000",
	}
	err = containers.syncCloudflareIngress("app", exposureSettings)
	if err != nil {
		t.Fatalf("syncCloudflareIngress failed: %v", err)
	}

	// Verify "app-worker" is still present in the updated config
	updatedRaw, err := cfgMgr.Read()
	if err != nil {
		t.Fatal(err)
	}
	var doc cfConfigDoc
	if err := yaml.Unmarshal([]byte(updatedRaw), &doc); err != nil {
		t.Fatal(err)
	}

	foundWorker := false
	foundApp := false
	for _, rule := range doc.Ingress {
		if strings.Contains(rule.Service, "app-worker") {
			foundWorker = true
			if rule.Hostname != "worker.example.com" {
				t.Errorf("worker hostname altered: %s", rule.Hostname)
			}
		}
		if rule.Hostname == "newapp.example.com" {
			foundApp = true
		}
	}
	if !foundWorker {
		t.Errorf("CRITICAL BUG: 'app-worker' ingress rule was deleted when updating 'app'!")
	}
	if !foundApp {
		t.Errorf("newapp ingress rule was not created")
	}

	// Case 2: Uppercase container name lookup in ingress map
	ingressMap := containers.loadCloudflareIngressMap()
	if _, ok := ingressMap["app-worker"]; !ok {
		t.Errorf("expected app-worker in ingress map")
	}
	// Case-insensitive / upper case test
	if _, ok := ingressMap[strings.ToLower("APP-WORKER")]; !ok {
		t.Errorf("expected lowercase lookup to work")
	}

	// Case 3: Container names with dashes, dots, underscores
	edgeNames := []string{
		"docktun-cloudflared-1",
		"app.frontend_v2",
		"my_service-worker",
		"web-api-gateway",
	}
	for _, name := range edgeNames {
		target := containers.normalizeTarget(name, "")
		if !strings.Contains(target, name) {
			t.Errorf("normalizeTarget failed for container %s: %s", name, target)
		}
		extracted := ingressContainer(target)
		if !strings.EqualFold(extracted, name) {
			t.Errorf("ingressContainer failed to roundtrip container name %s, got: %s", name, extracted)
		}
	}
}

// 3. Exposure Edge Cases: cleanHostname, invalid ports, wildcard domains
func TestExposureEdgeCases(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"example.com", "example.com"},
		{"EXAMPLE.COM", "example.com"},
		{"https://app.example.com", "app.example.com"},
		{"http://app.example.com:8080", "app.example.com"},
		{"https://sub.domain.com:443/some/path?query=val#fragment", "sub.domain.com"},
		{"*.example.com", "*.example.com"},
		{"*.sub.domain.org", "*.sub.domain.org"},
		{"   ", ""},
		{"http://", ""},
		{"https://", ""},
		{".app.example.com.", "app.example.com"},
		{"app.example.com.", "app.example.com"},
	}

	for _, tc := range cases {
		actual := cleanHostname(tc.input)
		if actual != tc.expected {
			t.Errorf("cleanHostname(%q) = %q, want %q", tc.input, actual, tc.expected)
		}
	}
}

// 4. Missing or Corrupted Docker Responses
func TestCorruptedDockerStatsAndInspect(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	database, err := db.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	appCfg := &config.Config{}
	containers := NewDockerContainers(appCfg, database, nil, nil, nil, nil)

	// Fuzz stats collector with corrupted outputs
	corruptedStatsOutputs := []string{
		"",
		"   \n\n",
		"not-enough-fields\tone\ttwo",
		"ID1\tname1\t0.5%\t10MB / 100MB\t1kB / 1kB\t0B / 0B\tnot-a-number",
		"\x00\xff\xfe corrupted bytes \t\t\t\t\t\t",
	}

	for _, sample := range corruptedStatsOutputs {
		currentSample := sample
		containers.statsMu.Lock()
		containers.statsCollector = func() map[string]liveStats {
			lines := strings.Split(strings.TrimSpace(currentSample), "\n")
			statsMap := make(map[string]liveStats)
			for _, line := range lines {
				parts := strings.Split(line, "\t")
				if len(parts) < 7 {
					continue
				}
				statsMap[parts[1]] = liveStats{cpu: parts[2]}
			}
			return statsMap
		}
		containers.statsTime = time.Time{} // Force cache refresh
		containers.statsMu.Unlock()

		// Must not panic
		stats := containers.getCachedStats()
		if stats == nil {
			t.Errorf("expected non-nil stats map")
		}

		// Wait for any running background collector to complete before next iteration
		for i := 0; i < 50; i++ {
			if atomic.LoadInt32(&containers.statsRunning) == 0 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}
