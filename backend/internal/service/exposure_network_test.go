package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"docktun/internal/config"
)

func TestExposureNetworkRollbackPreservesExistingMembership(t *testing.T) {
	t.Setenv("DOCKER_NETWORK", "custom-tunnel")
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			var calls []string
			svc := &DockerContainers{networkDocker: func(_ context.Context, args ...string) ([]byte, error) {
				cmd := strings.Join(args, " ")
				calls = append(calls, cmd)
				switch {
				case strings.HasPrefix(cmd, "network ls "):
					return []byte("tunnel-id\tcustom-tunnel"), nil
				case strings.HasPrefix(cmd, "inspect --format "):
					if existing {
						return []byte(`{"user-network":{"NetworkID":"keep"},"custom-tunnel":{"NetworkID":"tunnel-id"}}`), nil
					}
					return []byte(`{"user-network":{"NetworkID":"keep"},"cloudflared":{"NetworkID":"legacy-keep"}}`), nil
				case cmd == "network connect tunnel-id app" || cmd == "network disconnect tunnel-id app":
					return nil, nil
				default:
					t.Fatalf("unexpected network mutation: %s", cmd)
					return nil, nil
				}
			}}
			undo, err := svc.connectExposureNetwork("app")
			if err != nil {
				t.Fatal(err)
			}
			undo()
			mutations := 0
			for _, call := range calls {
				if strings.HasPrefix(call, "network connect ") || strings.HasPrefix(call, "network disconnect ") {
					mutations++
				}
			}
			want := 2
			if existing {
				want = 0
			}
			if mutations != want {
				t.Fatalf("rollback changed pre-existing membership: %v", calls)
			}
		})
	}
}

func TestExposureNetworkStatusMatchesExactConfiguredName(t *testing.T) {
	for _, tc := range []struct {
		networks, desired string
		want              bool
	}{
		{"docktun-shadow,user-network", "docktun", false},
		{"old-cloudflared-net", "cloudflared", false},
		{"user-network,custom-tunnel", "custom-tunnel", true},
		{"custom-tunnel-extra", "custom-tunnel", false},
	} {
		if got := exactNetworkMembership(tc.networks, tc.desired); got != tc.want {
			t.Fatalf("%q membership in %q=%t", tc.desired, tc.networks, got)
		}
	}
}

func TestExposureUsesActualCustomConnectorNetwork(t *testing.T) {
	t.Setenv("DOCKER_NETWORK", "")
	svc := &DockerContainers{networkDocker: func(_ context.Context, args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "inspect --format {{json .NetworkSettings.Networks}} docktun-cloudflared" {
			return []byte(`{"synology-tunnel":{"NetworkID":"shared"}}`), nil
		}
		t.Fatalf("guessed an unrelated network: %v", args)
		return nil, nil
	}}
	if got := svc.exposureNetworkName(); got != "synology-tunnel" {
		t.Fatal(got)
	}
}

func TestReconcileIngressNetworks(t *testing.T) {
	t.Setenv("DOCKER_NETWORK", "custom-tunnel")
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yml")
	yamlContent := `tunnel: test-tunnel-id
credentials-file: /etc/cloudflared/credentials.json
ingress:
  - hostname: app1.example.com
    service: http://running-app:8080
  - hostname: app2.example.com
    service: already-connected:3000
  - hostname: app3.example.com
    service: stopped-app:8000
  - hostname: lan.example.com
    service: http://192.168.1.100:80
  - hostname: local.example.com
    service: http://localhost:8080
  - service: http_status:404
`
	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}

	var connectedCalls []string
	svc := &DockerContainers{
		configMgr: NewConfigManager(&config.Config{CloudflaredConfig: cfgPath}, nil),
		networkDocker: func(_ context.Context, args ...string) ([]byte, error) {
			cmd := strings.Join(args, " ")
			switch {
			case strings.HasPrefix(cmd, "network ls "):
				return []byte("tunnel-id\tcustom-tunnel"), nil
			case cmd == "inspect --format {{.State.Running}} running-app":
				return []byte("true"), nil
			case cmd == "inspect --format {{json .NetworkSettings.Networks}} running-app":
				return []byte(`{"bridge":{"NetworkID":"bridge-net"}}`), nil
			case cmd == "inspect --format {{.State.Running}} already-connected":
				return []byte("true"), nil
			case cmd == "inspect --format {{json .NetworkSettings.Networks}} already-connected":
				return []byte(`{"custom-tunnel":{"NetworkID":"tunnel-id"}}`), nil
			case cmd == "inspect --format {{.State.Running}} stopped-app":
				return []byte("false"), nil
			case strings.HasPrefix(cmd, "network connect "):
				connectedCalls = append(connectedCalls, cmd)
				return nil, nil
			default:
				t.Fatalf("unexpected docker command: %s", cmd)
				return nil, nil
			}
		},
	}

	res, err := svc.ReconcileIngressNetworksDetailed(context.Background())
	if err != nil {
		t.Fatalf("ReconcileIngressNetworks failed: %v", err)
	}

	if len(res.Connected) != 1 || res.Connected[0] != "running-app" {
		t.Fatalf("expected Connected=[running-app], got: %v", res.Connected)
	}
	if len(res.AlreadyConnected) != 1 || res.AlreadyConnected[0] != "already-connected" {
		t.Fatalf("expected AlreadyConnected=[already-connected], got: %v", res.AlreadyConnected)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "stopped-app" {
		t.Fatalf("expected Skipped=[stopped-app], got: %v", res.Skipped)
	}

	expectedConnect := "network connect tunnel-id running-app"
	if len(connectedCalls) != 1 || connectedCalls[0] != expectedConnect {
		t.Fatalf("expected calls [%s], got: %v", expectedConnect, connectedCalls)
	}

	// Verify ReconcileIngressNetworks wrapper returns nil error
	if err := svc.ReconcileIngressNetworks(context.Background()); err != nil {
		t.Fatalf("ReconcileIngressNetworks wrapper returned error: %v", err)
	}
}

func TestExtractTargetContainer(t *testing.T) {
	cases := []struct {
		target   string
		expected string
	}{
		{"http://web:8080", "web"},
		{"https://my-service:443", "my-service"},
		{"app.local:3000", "app.local"},
		{"container_one:9000/path", "container_one"},
		{"tcp://db:5432", "db"},
		{"http://user:pass@service:80", "service"},
		{"http_status:404", ""},
		{"http://127.0.0.1:8080", ""},
		{"http://localhost:3000", ""},
		{"192.168.1.1:80", ""},
		{"http://[::1]:8080", ""},
		{"", ""},
	}

	for _, tc := range cases {
		got := extractTargetContainer(tc.target)
		if got != tc.expected {
			t.Errorf("extractTargetContainer(%q) = %q; want %q", tc.target, got, tc.expected)
		}
	}
}
