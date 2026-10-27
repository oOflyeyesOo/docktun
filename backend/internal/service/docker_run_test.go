package service

import (
	"context"
	"fmt"
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

func TestGenerateComposeYAML_NamedVolumesAndPrivileged(t *testing.T) {
	cmd := `docker run -d \
  --name aio-stack \
  --privileged \
  --restart unless-stopped \
  -v aio-data:/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -p 3000:3000 \
  ghcr.io/ooflyeyesoo/aiostreams-aio:latest`

	parsed, err := ParseDockerRunCommand(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !parsed.Privileged {
		t.Errorf("expected Privileged to be true")
	}
	yaml := parsed.GenerateComposeYAML()
	if !strings.Contains(yaml, "privileged: true") {
		t.Errorf("expected 'privileged: true' in YAML, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "volumes:\n  aio-data:\n    name: aio-data") {
		t.Errorf("expected top-level 'volumes:\n  aio-data:\n    name: aio-data' in YAML, got:\n%s", yaml)
	}
	if strings.Contains(yaml, "name: /var/run/docker.sock") {
		t.Errorf("bind mount /var/run/docker.sock should not be declared in top-level volumes, got:\n%s", yaml)
	}
}

func TestLaunchNameCollisionNeverDeletesOrStartsContainers(t *testing.T) {
	for _, mode := range []string{"compose", "direct"} {
		svc := &DockerRunService{jobs: make(map[int]*DockerRunJob)}
		calls := 0
		svc.runDocker = func(ctx context.Context, args ...string) ([]byte, error) {
			calls++
			if strings.Join(args, " ") != "container inspect existing-app" {
				t.Fatalf("launch mutated existing resources: %v", args)
			}
			return []byte(`[{"Id":"existing"}]`), nil
		}
		job, err := svc.StartRun("docker run --name existing-app alpine", mode, false, false, "", nil)
		if err == nil || !strings.Contains(err.Error(), "already exists") || job != nil || len(svc.jobs) != 0 || calls != 1 {
			t.Fatalf("collision started a job: %+v %v", job, err)
		}
	}
}

func TestNameCheckDistinguishesMissingContainerFromUnavailableDaemon(t *testing.T) {
	svc := &DockerRunService{}
	for _, message := range []string{"No such container: new-app", "No such object: new-app"} {
		svc.runDocker = func(context.Context, ...string) ([]byte, error) { return nil, fmt.Errorf("%s", message) }
		if err := svc.checkContainerNameAvailable("new-app"); err != nil {
			t.Fatal(err)
		}
	}
	svc.runDocker = func(context.Context, ...string) ([]byte, error) {
		return nil, fmt.Errorf("Cannot connect to daemon: no such file or directory")
	}
	if err := svc.checkContainerNameAvailable("new-app"); err == nil {
		t.Fatal("daemon failure treated as a free name")
	}
}
