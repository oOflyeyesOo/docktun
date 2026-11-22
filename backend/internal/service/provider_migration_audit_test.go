package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCloudflareAuthenticationMigrationKeepsNetworksAndRestoresFailures(t *testing.T) {
	for _, fail := range []string{"", "create", "network connect", "start"} {
		t.Run(fail, func(t *testing.T) {
			p := NewProviderContainers(nil)
			var old updateContainer
			if err := json.Unmarshal([]byte(`{"Id":"0123456789abcdef","HostConfig":{"NetworkMode":"cloudflared"},"State":{"Running":true},"NetworkSettings":{"Networks":{"cloudflared":{"NetworkID":"original-network","Aliases":["0123456789ab","origin-alias"]},"private":{"NetworkID":"private-network","Aliases":["private-alias"]}}}}`), &old); err != nil {
				t.Fatal(err)
			}
			var calls []string
			replacementCreated, originalRestored, originalRemoved := false, false, false
			injected := false
			p.run = func(_ context.Context, args ...string) ([]byte, error) {
				command := strings.Join(args, " ")
				calls = append(calls, command)
				if fail != "" && !injected && strings.HasPrefix(command, fail) && (fail != "start" || replacementCreated) {
					injected = true
					return nil, errors.New("fixture failure")
				}
				switch args[0] {
				case "create":
					replacementCreated = true
					if args[4] != "original-network" {
						t.Fatalf("replacement lost the original primary network: %v", args)
					}
				case "inspect":
					return []byte(`[{"State":{"Running":true}}]`), nil
				case "rename":
					if strings.Contains(args[1], "-auth-backup-") {
						originalRestored = true
					}
				case "rm":
					if len(args) == 2 && strings.Contains(args[1], "-auth-backup-") {
						originalRemoved = true
					}
				}
				return nil, nil
			}
			err := p.recreateCloudflare(context.Background(), old, []string{"create", "--name", "docktun-cloudflared", "--network", "docktun", "cloudflare/cloudflared:latest"})
			if fail != "" {
				if err == nil || !originalRestored || originalRemoved {
					t.Fatalf("failed migration did not retain/restore original: %v, calls %v", err, calls)
				}
				if !strings.Contains(calls[len(calls)-1], "start docktun-cloudflared") {
					t.Fatalf("original running state not restored: %v", calls)
				}
				return
			}
			if err != nil || !originalRemoved || originalRestored {
				t.Fatalf("migration failed: %v, calls %v", err, calls)
			}
			all := strings.Join(calls, "\n")
			for _, required := range []string{"create --name docktun-cloudflared --network original-network --network-alias origin-alias", "network connect --alias private-alias private-network docktun-cloudflared"} {
				if !strings.Contains(all, required) {
					t.Fatalf("original network/alias missing: %s, calls %v", required, calls)
				}
			}
			if strings.Contains(all, "alias 0123456789ab") {
				t.Fatal("copied obsolete container ID alias")
			}
		})
	}
}

func TestCloudflareAuthenticationMigrationRefusesStaticAddressBeforeStopping(t *testing.T) {
	p := NewProviderContainers(nil)
	var old updateContainer
	if err := json.Unmarshal([]byte(`{"NetworkSettings":{"Networks":{"cloudflared":{"IPAMConfig":{"IPv4Address":"172.27.0.4"}}}}}`), &old); err != nil {
		t.Fatal(err)
	}
	p.run = func(context.Context, ...string) ([]byte, error) {
		t.Fatal("static address migration mutated the original")
		return nil, nil
	}
	if err := p.recreateCloudflare(context.Background(), old, nil); err == nil {
		t.Fatal("expected an explicit migration error for static network configuration")
	}
}

func TestDatabaseMajorDetectsAliasedImagesAndMissingVersions(t *testing.T) {
	for _, tc := range []struct {
		image         string
		env           []string
		family, major string
	}{
		{"timescale/timescaledb:latest-pg17", []string{"PG_MAJOR=17"}, "postgres", "17"},
		{"company/renamed-db:next", []string{"MYSQL_MAJOR=8.4"}, "mysql", "8.4"},
		{"postgres:latest", nil, "postgres", ""},
		{"company/application:latest", nil, "", ""},
	} {
		family, major := databaseMajor(tc.image, tc.env)
		if family != tc.family || major != tc.major {
			t.Fatalf("%s detected %s/%s, expected %s/%s", tc.image, family, major, tc.family, tc.major)
		}
	}
}

func TestCloudflareMigrationOwnershipRequiresManagedIdentityOrSharedTrustedImage(t *testing.T) {
	core := managedFixture("docktun-core", nil)
	core.Mounts = append(core.Mounts, struct {
		Type, Source, Name, Destination, Mode, Propagation string
		RW                                                 bool
	}{Type: "volume", Name: "instance-config", Destination: "/etc/cloudflared", RW: true})
	for _, tc := range []struct {
		name, image, volume string
		owned               bool
	}{
		{"legacy official", "cloudflare/cloudflared:latest", "instance-config", true},
		{"canonical official", "docker.io/cloudflare/cloudflared:latest", "instance-config", true},
		{"neighbor state", "cloudflare/cloudflared:latest", "neighbor-config", false},
		{"unrelated image", "company/application:latest", "instance-config", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			existing := managedFixture("docktun-cloudflared", nil)
			existing.Config["Image"] = tc.image
			if err := json.Unmarshal([]byte(`{"Mounts":[{"Type":"volume","Name":"`+tc.volume+`","Destination":"/etc/cloudflared"}]}`), &existing); err != nil {
				t.Fatal(err)
			}
			if got := cloudflareMigrationOwned(existing, core, "/etc/cloudflared/config.yml"); got != tc.owned {
				t.Fatalf("ownership=%v, expected %v", got, tc.owned)
			}
		})
	}
}

func TestCloudflareMigrationRetainsPrimaryNetwork(t *testing.T) {
	p := NewProviderContainers(nil)
	var old updateContainer
	if err := json.Unmarshal([]byte(`{"HostConfig":{"NetworkMode":"z-origin"},"NetworkSettings":{"Networks":{"a-other":{"NetworkID":"other-id"},"z-origin":{"NetworkID":"origin-id"}}}}`), &old); err != nil {
		t.Fatal(err)
	}
	p.run = func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "create" && args[4] != "origin-id" {
			t.Fatalf("changed original primary network: %v", args)
		}
		if args[0] == "inspect" {
			return []byte(`[{"State":{"Running":true}}]`), nil
		}
		return nil, nil
	}
	if err := p.recreateCloudflare(context.Background(), old, []string{"create", "--name", "docktun-cloudflared", "--network", "docktun", "cloudflare/cloudflared:latest"}); err != nil {
		t.Fatal(err)
	}
}

func TestCloudflareMigrationPreservesExplicitOriginResolvers(t *testing.T) {
	p := NewProviderContainers(nil)
	var old updateContainer
	if err := json.Unmarshal([]byte(`{"HostConfig":{"NetworkMode":"host","Dns":["192.0.2.53"],"DnsSearch":["fixture.invalid"],"DnsOptions":["ndots:0"],"ExtraHosts":["custom-origin:192.0.2.14"]}}`), &old); err != nil {
		t.Fatal(err)
	}
	p.run = func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "create" {
			command := strings.Join(args, " ")
			for _, flag := range []string{"--dns 192.0.2.53", "--dns-search fixture.invalid", "--dns-option ndots:0", "--add-host custom-origin:192.0.2.14"} {
				if !strings.Contains(command, flag) {
					t.Fatalf("replacement lost resolver setting %q: %v", flag, args)
				}
			}
			if strings.Contains(command, "host.docker.internal") {
				t.Fatalf("replacement changed explicitly configured host entries: %v", args)
			}
		}
		if args[0] == "inspect" {
			return []byte(`[{"State":{"Running":true}}]`), nil
		}
		return nil, nil
	}
	if err := p.recreateCloudflare(context.Background(), old, []string{"create", "--name", "docktun-cloudflared", "--network", "docktun", "--add-host", "host.docker.internal:host-gateway", "cloudflare/cloudflared:latest"}); err != nil {
		t.Fatal(err)
	}
}
