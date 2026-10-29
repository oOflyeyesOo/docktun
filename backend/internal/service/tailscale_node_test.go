package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func fakeNodeRunner(app string, calls *[]string) func(context.Context, ...string) ([]byte, error) {
	return func(_ context.Context, args ...string) ([]byte, error) {
		cmd := strings.Join(args, " ")
		*calls = append(*calls, cmd)
		node := tailscaleNodeName(app)
		if cmd == "container inspect "+node || cmd == "container inspect docktun-tailscale" {
			return []byte(fmt.Sprintf(`[{"State":{"Running":true},"Config":{"Labels":{"%s":%q}}}]`, tailscaleOwnerLabel, app)), nil
		}
		if cmd == "container inspect "+app {
			return []byte(fmt.Sprintf(`[{"State":{"Running":true},"NetworkSettings":{"Networks":{%q:{}}}}]`, node)), nil
		}
		if strings.Contains(cmd, "inspect") {
			return []byte(fmt.Sprintf(`[{"Labels":{"%s":%q}}]`, tailscaleOwnerLabel, app)), nil
		}
		if strings.Contains(cmd, "serve status") {
			return []byte(`{}`), nil
		}
		if strings.Contains(cmd, "status --json") {
			return []byte(fmt.Sprintf(`{"BackendState":"Running","Self":{"DNSName":%q}}`, app+".example.ts.net.")), nil
		}
		return nil, nil
	}
}

func TestDedicatedTailscaleDevicesAndPortReuse(t *testing.T) {
	setup := setupFixture(t)
	svc := &DockerContainers{db: setup.db}
	nodes := map[string]bool{}
	for _, app := range []string{"first", "second"} {
		var calls []string
		svc.exposureDocker = fakeNodeRunner(app, &calls)
		next := ExposureSettings{Mode: "private", PrivateType: "tailscale", PrivatePort: 80, Target: app + ":8080", TailscaleNode: "untrusted-node"}
		undo, err := svc.syncTailscaleExposure(app, ExposureSettings{}, &next)
		if err != nil {
			t.Fatal(err)
		}
		if next.PrivateHost != app+".example.ts.net" || next.TailscaleNode != tailscaleNodeName(app) {
			t.Fatal(next)
		}
		if nodes[next.TailscaleNode] {
			t.Fatal("apps share a device")
		}
		nodes[next.TailscaleNode] = true
		if err = svc.saveExposureToDB(app, next); err != nil {
			t.Fatal(err)
		}
		if got := svc.getExposureFromDB(app); got.TailscaleNode != next.TailscaleNode {
			t.Fatal(got)
		}
		if got := svc.loadAllExposuresFromDB()[app]; got.TailscaleNode != next.TailscaleNode {
			t.Fatal(got)
		}
		all := strings.Join(calls, "\n")
		if !strings.Contains(all, "exec "+next.TailscaleNode+" tailscale serve --bg --tcp=80 tcp://"+app+":8080") || strings.Contains(all, "docktun-tailscale") {
			t.Fatal(all)
		}
		undo()
		if !strings.HasSuffix(calls[len(calls)-1], "--tcp=80 off") {
			t.Fatal(calls)
		}
		if _, err = svc.syncTailscaleExposure(app, next, &ExposureSettings{Mode: "internal"}); err != nil {
			t.Fatal(err)
		}
		if calls[len(calls)-1] != "stop "+next.TailscaleNode {
			t.Fatal(calls)
		}
	}
}

func TestTailscaleNodeOwnershipAndIsolation(t *testing.T) {
	var calls []string
	svc := &DockerContainers{exposureDocker: func(_ context.Context, args ...string) ([]byte, error) {
		cmd := strings.Join(args, " ")
		calls = append(calls, cmd)
		if cmd == "container inspect app" {
			return []byte(`[{"State":{"Running":true},"NetworkSettings":{"Networks":{}}}]`), nil
		}
		if strings.Contains(cmd, "inspect") {
			return []byte("No such object"), fmt.Errorf("No such object")
		}
		return nil, nil
	}}
	if err := svc.ensureNodeResources("app"); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(calls, "\n")
	for _, bad := range []string{"/var/run/docker.sock", "--privileged", "--publish", "NET_ADMIN", "--network host", "--advertise-routes"} {
		if strings.Contains(all, bad) {
			t.Fatal(all)
		}
	}
	for _, want := range []string{"--tun=userspace-networking", "--cap-drop ALL", "-state:/var/lib/tailscale", "network connect " + tailscaleNodeName("app") + " app"} {
		if !strings.Contains(all, want) {
			t.Fatal(all)
		}
	}
	svc.exposureDocker = func(context.Context, ...string) ([]byte, error) { return []byte(`[{"Config":{"Labels":{}}}]`), nil }
	if _, err := svc.ownedTailscaleNode("app"); err == nil {
		t.Fatal("accepted unowned container")
	}
	for _, target := range []string{"other:80", "app:0", "app:65536", "http://app:80/path", "app:80;touch bad"} {
		if _, err := tailscaleTarget("app", ExposureSettings{PrivatePort: 80, Target: target}); err == nil {
			t.Fatal(target)
		}
	}
}

func TestTailscaleMigrationPreservesUnrelatedRules(t *testing.T) {
	var calls []string
	base := fakeNodeRunner("app", &calls)
	old := ExposureSettings{Mode: "private", PrivateType: "tailscale", PrivatePort: 80, Target: "app:8080"}
	next := old
	svc := &DockerContainers{exposureDocker: func(ctx context.Context, args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "exec docktun-tailscale tailscale serve status --json" {
			return []byte(`{"TCP":{"80":{"TCPForward":"unrelated:8080"}}}`), nil
		}
		return base(ctx, args...)
	}}
	if _, err := svc.syncTailscaleExposure("app", old, &next); err == nil {
		t.Fatal("overwrote unrelated shared rule")
	}
	for _, cmd := range calls {
		if strings.Contains(cmd, "serve --bg") {
			t.Fatal(calls)
		}
	}
	svc.exposureDocker = func(ctx context.Context, args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "exec docktun-tailscale tailscale serve status --json" {
			return json.Marshal(map[string]any{"TCP": map[string]any{"80": map[string]string{"TCPForward": "app:8080"}}})
		}
		return base(ctx, args...)
	}
	if _, err := svc.syncTailscaleExposure("app", old, &next); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "exec docktun-tailscale tailscale serve --bg --tcp=80 off") {
		t.Fatal(calls)
	}
}

func TestTailscaleReconnectOnlySelectedAppAfterRecreation(t *testing.T) {
	setup := setupFixture(t)
	var calls []string
	svc := &DockerContainers{db: setup.db}
	base := fakeNodeRunner("selected", &calls)
	svc.exposureDocker = func(ctx context.Context, args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "container inspect selected" {
			return []byte(`[{"State":{"Running":true},"NetworkSettings":{"Networks":{"original":{}}}}]`), nil
		}
		return base(ctx, args...)
	}
	for _, app := range []string{"selected", "internal", "legacy"} {
		e := ExposureSettings{Mode: "private", PrivateType: "tailscale"}
		if app != "legacy" {
			e.TailscaleNode = tailscaleNodeName(app)
		}
		if app == "internal" {
			e.Mode = "internal"
		}
		if err := svc.saveExposureToDB(app, e); err != nil {
			t.Fatal(err)
		}
	}
	svc.ReconcileTailscaleAppNetworks()
	all := strings.Join(calls, "\n")
	if !strings.Contains(all, "network connect "+tailscaleNodeName("selected")+" selected") {
		t.Fatal(all)
	}
	for _, bad := range []string{"internal", "legacy", "start ", "run ", "create "} {
		if strings.Contains(all, bad) {
			t.Fatal(all)
		}
	}
}

func TestLegacyEmptyServeDoesNotBlockOtherProviders(t *testing.T) {
	for _, mode := range []string{"public", "internal", "private"} {
		t.Run(mode, func(t *testing.T) {
			var calls []string
			svc := &DockerContainers{exposureDocker: func(_ context.Context, args ...string) ([]byte, error) {
				cmd := strings.Join(args, " ")
				calls = append(calls, cmd)
				if cmd == "container inspect docktun-tailscale" {
					return []byte(`[{"State":{"Running":true}}]`), nil
				}
				if strings.Contains(cmd, "serve status --json") {
					return []byte(`{}`), nil
				}
				if strings.HasSuffix(cmd, " off") {
					return []byte("error: failed to remove TCP service config does not exist"), fmt.Errorf("exit status 1")
				}
				return nil, nil
			}}
			previous := ExposureSettings{Mode: "private", PrivateType: "tailscale", PrivatePort: 80, Target: "app:8080"}
			next := ExposureSettings{Mode: mode, PrivateType: "wireguard"}
			undo, err := svc.syncTailscaleExposure("app", previous, &next)
			if err != nil {
				t.Fatalf("empty legacy config blocked provider switch: %v", err)
			}
			undo()
			for _, cmd := range calls {
				if strings.Contains(cmd, "serve --bg") || strings.HasPrefix(cmd, "start ") || strings.HasPrefix(cmd, "stop ") {
					t.Fatal("modified legacy provider despite absent app rule:", calls)
				}
			}
		})
	}
}
