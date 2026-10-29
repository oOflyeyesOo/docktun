package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
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
