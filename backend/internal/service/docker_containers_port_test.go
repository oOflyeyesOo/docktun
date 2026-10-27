package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanPortNumber(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"80", "80"},
		{"8080", "8080"},
		{"80/tcp", "80"},
		{"53/udp", "53"},
		{"127.0.0.1", ""}, // IPv4 must NOT be cleaned to port number
		{"0.0.0.0", ""},
		{"localhost", ""},
		{"invalid", ""},
		{"70000", ""}, // Out of port range
		{"0", ""},
	}

	for _, tt := range tests {
		actual := cleanPortNumber(tt.input)
		if actual != tt.expected {
			t.Errorf("cleanPortNumber(%q) = %q; want %q", tt.input, actual, tt.expected)
		}
	}
}

func TestExtractAllPortNumbers(t *testing.T) {
	// Crucial test: 127.0.0.1 must NEVER produce [127, 1]
	res1 := extractAllPortNumbers("127.0.0.1")
	if len(res1) != 0 {
		t.Errorf("extractAllPortNumbers('127.0.0.1') = %v; want []", res1)
	}

	res2 := extractAllPortNumbers("127.0.0.1:8191:8191")
	if len(res2) != 2 || res2[0] != "8191" || res2[1] != "8191" {
		t.Errorf("extractAllPortNumbers('127.0.0.1:8191:8191') = %v; want [8191, 8191]", res2)
	}

	res3 := extractAllPortNumbers(":::8080:80")
	if len(res3) != 2 || res3[0] != "8080" || res3[1] != "80" {
		t.Errorf("extractAllPortNumbers(':::8080:80') = %v; want [8080, 80]", res3)
	}
}

func TestParsePortMapping(t *testing.T) {
	tests := []struct {
		raw      string
		ok       bool
		wantHost string
		wantCont string
		wantIP   string
		compose  string
	}{
		{
			raw:      "8080:80",
			ok:       true,
			wantHost: "8080",
			wantCont: "80",
			wantIP:   "",
			compose:  "8080:80",
		},
		{
			raw:      "127.0.0.1:8191:8191",
			ok:       true,
			wantHost: "8191",
			wantCont: "8191",
			wantIP:   "127.0.0.1",
			compose:  "127.0.0.1:8191:8191",
		},
		{
			raw:      "127.0.0.1:8191",
			ok:       true,
			wantHost: "8191",
			wantCont: "8191",
			wantIP:   "127.0.0.1",
			compose:  "127.0.0.1:8191:8191",
		},
		{
			raw:      "0.0.0.0:80->80/tcp",
			ok:       true,
			wantHost: "80",
			wantCont: "80",
			wantIP:   "",
			compose:  "80:80",
		},
		{
			raw:      "127.0.0.1:8191->8191/tcp",
			ok:       true,
			wantHost: "8191",
			wantCont: "8191",
			wantIP:   "127.0.0.1",
			compose:  "127.0.0.1:8191:8191",
		},
		{
			raw:      "[::1]:8191->8191/tcp",
			ok:       true,
			wantHost: "8191",
			wantCont: "8191",
			wantIP:   "::1",
			compose:  "[::1]:8191:8191",
		},
		{
			raw:      ":::8191:8191",
			ok:       true,
			wantHost: "8191",
			wantCont: "8191",
			wantIP:   "",
			compose:  "8191:8191",
		},
		{
			raw:      "80",
			ok:       true,
			wantHost: "80",
			wantCont: "80",
			wantIP:   "",
			compose:  "80:80",
		},
	}

	for _, tt := range tests {
		parsed, ok := parsePortMapping(tt.raw)
		if ok != tt.ok {
			t.Errorf("parsePortMapping(%q) ok = %v; want %v", tt.raw, ok, tt.ok)
			continue
		}
		if !ok {
			continue
		}
		if parsed.HostPort != tt.wantHost || parsed.ContainerPort != tt.wantCont || parsed.HostIP != tt.wantIP {
			t.Errorf("parsePortMapping(%q) = %+v; want HostPort=%q ContainerPort=%q HostIP=%q",
				tt.raw, parsed, tt.wantHost, tt.wantCont, tt.wantIP)
		}
		if comp := parsed.FormatForCompose(); comp != tt.compose {
			t.Errorf("FormatForCompose() for %q = %q; want %q", tt.raw, comp, tt.compose)
		}
	}
}

func TestSanitizeComposeFile_Port127Healing(t *testing.T) {
	tempDir := t.TempDir()
	composePath := filepath.Join(tempDir, "docker-compose.yml")

	input := `services:
  nuvio:
    image: nuvio:latest
    ports:
      - "127:1"
  flaresolverr:
    image: flaresolverr:latest
    ports:
      - "127:8191"
  web:
    image: nginx:alpine
    ports:
      - "127.0.0.1:8080:80"
`
	if err := os.WriteFile(composePath, []byte(input), 0644); err != nil {
		t.Fatalf("failed to write temp compose: %v", err)
	}

	modified := SanitizeComposeFile(composePath)
	if !modified {
		t.Fatalf("expected SanitizeComposeFile to return modified=true")
	}

	resultBytes, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("failed to read result: %v", err)
	}
	result := string(resultBytes)

	// Verify "127:1" corrupted artifact was commented out
	if strings.Contains(result, `- "127:1"`) {
		t.Errorf("result should not contain '- \"127:1\"':\n%s", result)
	}
	if !strings.Contains(result, "# removed corrupted 127.0.0.1 artifact (127:1)") {
		t.Errorf("result should contain comment about removed 127:1 artifact:\n%s", result)
	}

	// Verify "127:8191" was healed to "127.0.0.1:8191:8191"
	if strings.Contains(result, `- "127:8191"`) {
		t.Errorf("result should not contain '- \"127:8191\"':\n%s", result)
	}
	if !strings.Contains(result, `- "127.0.0.1:8191:8191"`) {
		t.Errorf("result should contain healed port '- \"127.0.0.1:8191:8191\"':\n%s", result)
	}

	// Verify "127.0.0.1:8080:80" was preserved intact
	if !strings.Contains(result, `- "127.0.0.1:8080:80"`) {
		t.Errorf("result should preserve '- \"127.0.0.1:8080:80\"':\n%s", result)
	}
}
