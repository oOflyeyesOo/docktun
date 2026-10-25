package service

import (
	"strings"
	"testing"
)

func TestSplitShellArgs(t *testing.T) {
	cmd := "docker run -d \\\n  --name aiostreams \\\n  -p 3001:3000 \\\n  ghcr.io/viren070/aiostreams:latest"
	args := splitShellArgs(cmd)

	expected := []string{"docker", "run", "-d", "--name", "aiostreams", "-p", "3001:3000", "ghcr.io/viren070/aiostreams:latest"}
	if len(args) != len(expected) {
		t.Fatalf("expected %d args, got %d: %v", len(expected), len(args), args)
	}
	for i, v := range expected {
		if args[i] != v {
			t.Errorf("arg[%d]: expected %q, got %q", i, v, args[i])
		}
	}
}

func TestParseDockerRunCommand_Aiostreams(t *testing.T) {
	cmd := `docker run -d \
  --name aiostreams \
  -p 3001:3000 \
  -e BASE_URL=http://localhost:3001 \
  -e "SECRET_KEY=secret 123" \
  ghcr.io/viren070/aiostreams:latest`

	parsed, err := ParseDockerRunCommand(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.ContainerName != "aiostreams" {
		t.Errorf("expected container name 'aiostreams', got %q", parsed.ContainerName)
	}
	if parsed.Image != "ghcr.io/viren070/aiostreams:latest" {
		t.Errorf("expected image 'ghcr.io/viren070/aiostreams:latest', got %q", parsed.Image)
	}
	if len(parsed.Ports) != 1 || parsed.Ports[0] != "3001:3000" {
		t.Errorf("expected port '3001:3000', got %v", parsed.Ports)
	}
	if len(parsed.Env) != 2 {
		t.Fatalf("expected 2 env vars, got %d: %v", len(parsed.Env), parsed.Env)
	}
	if parsed.Env[0] != "BASE_URL=http://localhost:3001" {
		t.Errorf("expected env[0] BASE_URL, got %q", parsed.Env[0])
	}
	if parsed.Env[1] != "SECRET_KEY=secret 123" {
		t.Errorf("expected env[1] SECRET_KEY, got %q", parsed.Env[1])
	}
}

func TestParseDockerRunCommand_CommandArgs(t *testing.T) {
	cmd := "docker run -d --name runner alpine:latest sh -c 'echo hello world'"
	parsed, err := ParseDockerRunCommand(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Image != "alpine:latest" {
		t.Errorf("expected image 'alpine:latest', got %q", parsed.Image)
	}
	if len(parsed.Command) != 3 || parsed.Command[0] != "sh" || parsed.Command[1] != "-c" || parsed.Command[2] != "echo hello world" {
		t.Errorf("expected command [sh, -c, 'echo hello world'], got %v", parsed.Command)
	}
}

func TestGenerateComposeYAML_HostNetwork(t *testing.T) {
	parsed := &ParsedDockerRun{
		ContainerName: "zeroconf",
		Image:         "zeroconf:latest",
		Network:       "host",
	}
	yaml := parsed.GenerateComposeYAML()
	if !strings.Contains(yaml, "network_mode: host") {
		t.Errorf("expected 'network_mode: host' in YAML, got:\n%s", yaml)
	}
	if strings.Contains(yaml, "external: true") {
		t.Errorf("should not contain 'external: true' for host network mode, got:\n%s", yaml)
	}
}

func TestParseDockerRunCommand_DockerPull(t *testing.T) {
	cmd := "docker pull ghcr.io/ooflyeyesoo/aiostreams-aio:39c3611bfffd26d737876b817c0b857461f441cb"
	parsed, err := ParseDockerRunCommand(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Image != "ghcr.io/ooflyeyesoo/aiostreams-aio:39c3611bfffd26d737876b817c0b857461f441cb" {
		t.Errorf("expected image 'ghcr.io/ooflyeyesoo/aiostreams-aio:39c3611bfffd26d737876b817c0b857461f441cb', got %q", parsed.Image)
	}
	if parsed.ContainerName != "aiostreams-aio" {
		t.Errorf("expected container name 'aiostreams-aio', got %q", parsed.ContainerName)
	}
}

func TestParseDockerRunCommand_BareImage(t *testing.T) {
	cmd := "ghcr.io/ooflyeyesoo/aiostreams-aio:latest"
	parsed, err := ParseDockerRunCommand(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Image != "ghcr.io/ooflyeyesoo/aiostreams-aio:latest" {
		t.Errorf("expected image 'ghcr.io/ooflyeyesoo/aiostreams-aio:latest', got %q", parsed.Image)
	}
	if parsed.ContainerName != "aiostreams-aio" {
		t.Errorf("expected container name 'aiostreams-aio', got %q", parsed.ContainerName)
	}
}
