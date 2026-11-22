package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"docktun/internal/config"
)

type migrationDocker struct {
	containers map[string]*updateContainer
	networks   map[string]string
	calls      [][]string
	version    string
	legacyCLI  bool
	before     func([]string) error
	created    int
}

func migrationFixture(t *testing.T) (*ProviderContainers, *migrationDocker, updateContainer) {
	t.Helper()
	var original updateContainer
	if err := json.Unmarshal([]byte(`{"Id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","Name":"/docktun-cloudflared","Config":{"Labels":{}},"HostConfig":{"NetworkMode":"z-origin"},"State":{"Running":true},"NetworkSettings":{"Networks":{"z-origin":{"NetworkID":"origin-id","Aliases":["connector-origin"]},"a-private":{"NetworkID":"private-id","Aliases":["connector-private"]}}}}`), &original); err != nil {
		t.Fatal(err)
	}
	fake := &migrationDocker{containers: map[string]*updateContainer{original.ID: &original}, networks: map[string]string{"origin-id": "z-origin", "private-id": "a-private"}, version: "1.48"}
	p := NewProviderContainers(&config.Config{DBPath: filepath.Join(t.TempDir(), "app.db")})
	p.run = fake.run
	// Return a distinct inspection snapshot, just as Docker's API would.
	raw, _ := json.Marshal(original)
	var snapshot updateContainer
	json.Unmarshal(raw, &snapshot)
	return p, fake, snapshot
}

func (fake *migrationDocker) find(target string) (*updateContainer, error) {
	for id, container := range fake.containers {
		if id == target || strings.TrimPrefix(container.Name, "/") == target {
			return container, nil
		}
	}
	return nil, fmt.Errorf("No such container: %s", target)
}

func (fake *migrationDocker) attach(container *updateContainer, network string, options []string) {
	if strings.HasPrefix(network, "name=") {
		for _, part := range strings.Split(network, ",") {
			key, value, _ := strings.Cut(part, "=")
			if key == "name" {
				network = value
			}
			if key == "gw-priority" {
				options = append(options, "--gw-priority", value)
			}
		}
	}
	name := fake.networks[network]
	if name == "" {
		name = network
	}
	endpoint := map[string]any{"NetworkID": network}
	ipam := map[string]any{}
	var aliases, linkLocal []any
	for i := 0; i+1 < len(options); i++ {
		switch options[i] {
		case "--alias", "--network-alias":
			aliases = append(aliases, options[i+1])
		case "--ip":
			ipam["IPv4Address"] = options[i+1]
		case "--ip6":
			ipam["IPv6Address"] = options[i+1]
		case "--link-local-ip":
			linkLocal = append(linkLocal, options[i+1])
		case "--gw-priority":
			var priority float64
			fmt.Sscanf(options[i+1], "%f", &priority)
			endpoint["GwPriority"] = priority
		}
	}
	if len(aliases) > 0 {
		endpoint["Aliases"] = aliases
	}
	if len(linkLocal) > 0 {
		ipam["LinkLocalIPs"] = linkLocal
	}
	if len(ipam) > 0 {
		endpoint["IPAMConfig"] = ipam
	}
	container.NetworkSettings.Networks[name] = endpoint
}

func (fake *migrationDocker) run(_ context.Context, args ...string) ([]byte, error) {
	fake.calls = append(fake.calls, append([]string{}, args...))
	if fake.before != nil {
		if err := fake.before(args); err != nil {
			return nil, err
		}
	}
	if args[0] == "version" {
		return []byte(fake.version), nil
	}
	if strings.Join(args, " ") == "network connect --help" {
		if fake.legacyCLI {
			return []byte("--ip --alias"), nil
		}
		return []byte("--ip --alias --gw-priority"), nil
	}
	if args[0] == "create" {
		fake.created++
		container := &updateContainer{ID: fmt.Sprintf("%064x", fake.created), Config: map[string]any{"Labels": map[string]any{}}, HostConfig: map[string]any{}}
		container.NetworkSettings.Networks = map[string]map[string]any{}
		network := "none"
		for i := 1; i+1 < len(args); i++ {
			switch args[i] {
			case "--name":
				container.Name = "/" + args[i+1]
			case "--network":
				network = args[i+1]
				container.HostConfig["NetworkMode"] = network
			case "--label":
				key, value, _ := strings.Cut(args[i+1], "=")
				container.Config["Labels"].(map[string]any)[key] = value
			}
		}
		if network != "none" && network != "host" && !strings.HasPrefix(network, "container:") {
			fake.attach(container, network, args)
		}
		fake.containers[container.ID] = container
		return []byte(container.ID), nil
	}
	if args[0] == "network" {
		target, err := fake.find(args[len(args)-1])
		if err != nil {
			return nil, err
		}
		network := args[len(args)-2]
		if args[1] == "connect" {
			fake.attach(target, network, args[2:len(args)-2])
		} else {
			delete(target.NetworkSettings.Networks, fake.networks[network])
		}
		return nil, nil
	}
	target, err := fake.find(args[len(args)-1])
	if args[0] == "rename" {
		target, err = fake.find(args[1])
	}
	if err != nil {
		return nil, err
	}
	switch args[0] {
	case "inspect":
		return json.Marshal([]updateContainer{*target})
	case "stop":
		target.State.Running = false
	case "start":
		target.State.Running = true
	case "rename":
		target.Name = "/" + args[2]
	case "rm":
		delete(fake.containers, target.ID)
	}
	return nil, nil
}

func migrationDefinition() []string {
	return []string{"create", "--name", "docktun-cloudflared", "--network", "default-network", "--add-host", "host.docker.internal:host-gateway", "cloudflare/cloudflared:latest"}
}

func staticOriginal(original *updateContainer) {
	original.NetworkSettings.Networks["z-origin"]["IPAMConfig"] = map[string]any{"IPv4Address": "172.27.0.4", "IPv6Address": "fd00::4", "LinkLocalIPs": []any{"169.254.2.4"}}
	original.NetworkSettings.Networks["z-origin"]["GwPriority"] = float64(10)
}

func assertOriginalRestored(t *testing.T, fake *migrationDocker, original updateContainer) {
	t.Helper()
	current, err := fake.find("docktun-cloudflared")
	if err != nil || current.ID != original.ID || current.State.Running != original.State.Running {
		t.Fatalf("original state was not restored: %#v %v", current, err)
	}
	if !reflect.DeepEqual(current.NetworkSettings.Networks, original.NetworkSettings.Networks) {
		t.Fatalf("original endpoints changed: %#v expected %#v", current.NetworkSettings.Networks, original.NetworkSettings.Networks)
	}
}

func TestCloudflareMigrationPreservesStaticEndpointsAndGateway(t *testing.T) {
	p, fake, original := migrationFixture(t)
	staticOriginal(&original)
	staticOriginal(fake.containers[original.ID])
	if err := p.recreateCloudflare(context.Background(), original, migrationDefinition()); err != nil {
		t.Fatal(err)
	}
	replacement, _ := fake.find("docktun-cloudflared")
	if replacement.ID == original.ID || fake.containers[original.ID] != nil {
		t.Fatal("original was not replaced and finalized")
	}
	if !reflect.DeepEqual(replacement.NetworkSettings.Networks, original.NetworkSettings.Networks) {
		t.Fatalf("replacement endpoints differ: %#v expected %#v", replacement.NetworkSettings.Networks, original.NetworkSettings.Networks)
	}
	if replacement.HostConfig["NetworkMode"] != "name=origin-id,gw-priority=10" {
		t.Fatal("primary network and gateway priority changed")
	}
	file, _ := p.migrationPath()
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("completed journal remains", err)
	}
}

func TestCloudflareMigrationRestoresOriginalOnFailures(t *testing.T) {
	for _, fail := range []string{"rename", "network disconnect", "create", "network connect", "start"} {
		t.Run(fail, func(t *testing.T) {
			p, fake, original := migrationFixture(t)
			staticOriginal(&original)
			staticOriginal(fake.containers[original.ID])
			injected := false
			fake.before = func(args []string) error {
				if !injected && args[len(args)-1] != "--help" && strings.HasPrefix(strings.Join(args, " "), fail) {
					injected = true
					return errors.New("fixture failure")
				}
				return nil
			}
			if err := p.recreateCloudflare(context.Background(), original, migrationDefinition()); err == nil || !injected {
				t.Fatal("expected injected migration failure", err)
			}
			assertOriginalRestored(t, fake, original)
			if len(fake.containers) != 1 {
				t.Fatal("failed replacement remains")
			}
		})
	}
}

func interruptMigration(t *testing.T, p *ProviderContainers, fake *migrationDocker, original updateContainer, command string) {
	t.Helper()
	fake.before = func(args []string) error {
		if args[len(args)-1] != "--help" && strings.HasPrefix(strings.Join(args, " "), command) {
			panic("simulated process interruption")
		}
		return nil
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("interruption was not exercised")
			}
		}()
		p.recreateCloudflare(context.Background(), original, migrationDefinition())
	}()
	fake.before = nil
}

func TestCloudflareMigrationJournalRecoversAfterInterruption(t *testing.T) {
	for _, command := range []string{"create", "network connect", "start", "rm"} {
		t.Run(command, func(t *testing.T) {
			p, fake, original := migrationFixture(t)
			staticOriginal(&original)
			staticOriginal(fake.containers[original.ID])
			interruptMigration(t, p, fake, original, command)
			// Simulate a new provider instance after the server process restarts.
			restarted := NewProviderContainers(p.cfg)
			restarted.run = fake.run
			if err := restarted.recoverCloudflareMigration(context.Background()); err != nil {
				t.Fatal(err)
			}
			if command == "rm" {
				current, _ := fake.find("docktun-cloudflared")
				if current.ID == original.ID || fake.containers[original.ID] != nil || !current.State.Running {
					t.Fatal("committed replacement was not finalized")
				}
			} else {
				assertOriginalRestored(t, fake, original)
			}
		})
	}
}

func TestCloudflareMigrationRecoveryRetriesAndRejectsReusedName(t *testing.T) {
	p, fake, original := migrationFixture(t)
	staticOriginal(&original)
	staticOriginal(fake.containers[original.ID])
	interruptMigration(t, p, fake, original, "start")
	replacement, _ := fake.find("docktun-cloudflared")
	delete(fake.containers, replacement.ID)
	unrelated := &updateContainer{ID: strings.Repeat("c", 64), Name: "/docktun-cloudflared", Config: map[string]any{}}
	fake.containers[unrelated.ID] = unrelated
	if err := p.recoverCloudflareMigration(context.Background()); err == nil || fake.containers[unrelated.ID] == nil {
		t.Fatal("reused name authorized deletion")
	}
	delete(fake.containers, unrelated.ID)
	fake.before = func(args []string) error {
		if args[0] == "network" && args[1] == "connect" && args[len(args)-1] != "--help" {
			return errors.New("network temporarily unavailable")
		}
		return nil
	}
	if err := p.recoverCloudflareMigration(context.Background()); err == nil {
		t.Fatal("expected recovery failure")
	}
	file, _ := p.migrationPath()
	if _, err := os.Stat(file); err != nil {
		t.Fatal("recovery discarded its durable snapshot", err)
	}
	fake.before = nil
	if _, err := p.ensure(context.Background(), "cloudflare", false); err != nil {
		t.Fatal("prepare did not recover the pending migration", err)
	}
	assertOriginalRestored(t, fake, original)
}

func TestCloudflareMigrationRetainsReplacementWhenOriginalIsMissing(t *testing.T) {
	p, fake, original := migrationFixture(t)
	interruptMigration(t, p, fake, original, "start")
	replacement, _ := fake.find("docktun-cloudflared")
	delete(fake.containers, original.ID)
	if err := p.recoverCloudflareMigration(context.Background()); err == nil {
		t.Fatal("expected a missing original recovery error")
	}
	if fake.containers[replacement.ID] == nil {
		t.Fatal("recovery removed the only remaining connector")
	}
}

func TestCloudflareMigrationSharedNamespaceUsesResolvedTarget(t *testing.T) {
	for _, running := range []bool{true, false} {
		t.Run(fmt.Sprint(running), func(t *testing.T) {
			p, fake, original := migrationFixture(t)
			target := &updateContainer{ID: strings.Repeat("d", 64), Name: "/user-vpn", Config: map[string]any{}}
			target.State.Running = running
			fake.containers[target.ID] = target
			original.HostConfig["NetworkMode"] = "container:user-vpn"
			original.HostConfig["Dns"] = []any{"192.0.2.53"}
			original.NetworkSettings.Networks = nil
			fake.containers[original.ID].HostConfig = original.HostConfig
			fake.containers[original.ID].NetworkSettings.Networks = nil
			args := append([]string{}, migrationDefinition()...)
			args = append(args[:len(args)-1], "--hostname", "old-host", "--dns", "192.0.2.53", args[len(args)-1])
			err := p.recreateCloudflare(context.Background(), original, args)
			if !running {
				if err == nil || fake.created != 0 || !fake.containers[original.ID].State.Running {
					t.Fatal("stopped namespace target did not fail before mutation")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			replacement, _ := fake.find("docktun-cloudflared")
			if replacement.HostConfig["NetworkMode"] != "container:"+target.ID {
				t.Fatal("namespace name was not resolved to its original target identity")
			}
			for _, call := range fake.calls {
				if call[0] == "create" && (strings.Contains(strings.Join(call, " "), "--dns") || strings.Contains(strings.Join(call, " "), "--hostname") || strings.Contains(strings.Join(call, " "), "--add-host")) {
					t.Fatal("Docker-incompatible namespace options remain", call)
				}
			}
		})
	}
}

func TestCloudflareMigrationGatewayCompatibilityFailsBeforeMutation(t *testing.T) {
	for _, legacyCLI := range []bool{false, true} {
		p, fake, original := migrationFixture(t)
		original.NetworkSettings.Networks["z-origin"]["GwPriority"] = float64(7)
		if legacyCLI {
			fake.legacyCLI = true
		} else {
			fake.version = "1.47"
		}
		if err := p.recreateCloudflare(context.Background(), original, migrationDefinition()); err == nil || !fake.containers[original.ID].State.Running || fake.created != 0 {
			t.Fatal("unsupported gateway priority mutated the original")
		}
	}
}

func TestCloudflareMigrationKeepsExplicitResolverSettings(t *testing.T) {
	p, fake, original := migrationFixture(t)
	original.HostConfig["Dns"] = []any{"192.0.2.53"}
	original.HostConfig["DnsSearch"] = []any{"fixture.invalid"}
	original.HostConfig["DnsOptions"] = []any{"ndots:0"}
	original.HostConfig["ExtraHosts"] = []any{"custom-origin:192.0.2.14"}
	if err := p.recreateCloudflare(context.Background(), original, migrationDefinition()); err != nil {
		t.Fatal(err)
	}
	for _, call := range fake.calls {
		if call[0] != "create" {
			continue
		}
		command := strings.Join(call, " ")
		for _, expected := range []string{"--dns 192.0.2.53", "--dns-search fixture.invalid", "--dns-option ndots:0", "--add-host custom-origin:192.0.2.14", "--network origin-id"} {
			if !strings.Contains(command, expected) {
				t.Fatal("original resolver or primary network setting missing", call)
			}
		}
		if strings.Contains(command, "host.docker.internal") {
			t.Fatal("generated resolver default replaced explicit original configuration")
		}
	}
}
