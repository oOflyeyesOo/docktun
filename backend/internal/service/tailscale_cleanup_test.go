package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestDormantTailscaleDoesNotBlockOtherProviders(t *testing.T) {
	for _, dedicated := range []bool{false, true} {
		for _, missing := range []bool{false, true} {
			for _, mode := range []string{"public", "internal", "private"} {
				t.Run(fmt.Sprintf("dedicated=%t/missing=%t/%s", dedicated, missing, mode), func(t *testing.T) {
					setup := setupFixture(t)
					var calls []string
					node := "docktun-tailscale"
					previous := ExposureSettings{Mode: "private", PrivateType: "tailscale", PrivatePort: 80, Target: "app:8080"}
					if dedicated {
						node = tailscaleNodeName("app")
						previous.TailscaleNode = node
					}
					base := fakeNodeRunner("app", &calls)
					svc := &DockerContainers{db: setup.db, exposureDocker: func(ctx context.Context, args ...string) ([]byte, error) {
						if strings.Join(args, " ") == "container inspect "+node {
							calls = append(calls, strings.Join(args, " "))
							if missing {
								return []byte("Error: No such container: " + node), fmt.Errorf("exit status 1")
							}
							return []byte(fmt.Sprintf(`[{"State":{"Running":false},"Config":{"Labels":{"%s":"app"}}}]`, tailscaleOwnerLabel)), nil
						}
						return base(ctx, args...)
					}}
					next := ExposureSettings{Mode: mode, PrivateType: "wireguard", Hostname: "app.example.com"}
					if _, err := svc.syncTailscaleExposure("app", previous, &next); err != nil {
						t.Fatalf("dormant Tailscale blocked provider change: %v", err)
					}
					for _, call := range calls {
						for _, forbidden := range []string{"exec " + node + " ", "start " + node, "stop " + node} {
							if strings.HasPrefix(call, forbidden) {
								t.Fatalf("changed dormant provider: %v", calls)
							}
						}
						if strings.Contains(call, " reset") {
							t.Fatalf("reset unrelated Serve routes: %v", calls)
						}
					}
					if dedicated && !strings.Contains(strings.Join(calls, "\n"), "network disconnect "+node+" app") {
						t.Fatalf("dormant helper retained app connectivity: %v", calls)
					}
					if err := svc.saveExposureToDB("app", next); err != nil {
						t.Fatal(err)
					}
					// A new service must recover cleanup from SQLite, not an in-memory flag.
					reloaded := &DockerContainers{db: setup.db}
					pending, err := reloaded.pendingTailscaleRules(node)
					if err != nil || len(pending) != 1 || pending[0] != (tailscaleRuleCleanup{App: "app", Node: node, Port: 80, Target: "app:8080"}) {
						t.Fatalf("cleanup was not persisted: %#v, %v", pending, err)
					}
					if got := reloaded.getExposureFromDB("app"); got.Mode != mode || got.TailscaleNode != "" {
						t.Fatalf("new provider was not saved: %#v", got)
					}
				})
			}
		}
	}
}

func TestPendingTailscaleCleanupIsTargetedAndRetried(t *testing.T) {
	for _, target := range []string{"", "app:8080", "unrelated:8080", "daemon-error"} {
		t.Run(target, func(t *testing.T) {
			setup := setupFixture(t)
			var calls []string
			node := "docktun-tailscale"
			base := fakeNodeRunner("app", &calls)
			svc := &DockerContainers{db: setup.db, exposureDocker: func(ctx context.Context, args ...string) ([]byte, error) {
				if strings.Join(args, " ") == "exec "+node+" tailscale serve status --json" {
					calls = append(calls, strings.Join(args, " "))
					if target == "daemon-error" {
						return []byte("permission denied on control socket"), fmt.Errorf("exit status 1")
					}
					rules := map[string]any{"443": map[string]string{"TCPForward": "other:443"}}
					if target != "" {
						rules["80"] = map[string]string{"TCPForward": target}
					}
					return json.Marshal(map[string]any{"TCP": rules})
				}
				return base(ctx, args...)
			}}
			pending := &tailscaleRuleCleanup{App: "app", Node: node, Port: 80, Target: "app:8080"}
			if err := svc.saveExposureToDB("app", ExposureSettings{Mode: "public", cleanup: pending}); err != nil {
				t.Fatal(err)
			}
			err := svc.cleanPendingTailscaleRules(node)
			blocked := target == "unrelated:8080" || target == "daemon-error"
			if (err != nil) != blocked {
				t.Fatalf("cleanup error = %v, blocked = %t", err, blocked)
			}
			remaining, queryErr := svc.pendingTailscaleRules(node)
			if queryErr != nil || (len(remaining) != 0) != blocked {
				t.Fatalf("pending cleanup lost or retained incorrectly: %#v, %v", remaining, queryErr)
			}
			removed := false
			for _, call := range calls {
				if strings.Contains(call, "serve --bg") {
					if call != "exec "+node+" tailscale serve --bg --tcp=80 off" {
						t.Fatalf("modified an unrelated route: %v", calls)
					}
					removed = true
				}
				if strings.Contains(call, " reset") {
					t.Fatalf("reset all Serve routes: %v", calls)
				}
			}
			if removed != (target == "app:8080") {
				t.Fatalf("wrong target removal, calls: %v", calls)
			}
			if blocked {
				// A later successful inspection must finish the same durable job.
				target = "app:8080"
				if err := svc.cleanPendingTailscaleRules(node); err != nil {
					t.Fatalf("cleanup did not recover: %v", err)
				}
				remaining, err := svc.pendingTailscaleRules(node)
				if err != nil || len(remaining) != 0 {
					t.Fatalf("successful retry retained cleanup: %#v, %v", remaining, err)
				}
			}
		})
	}
}

func TestTailscaleInspectionFailuresDoNotSilentlyDisableRouting(t *testing.T) {
	for _, failure := range []string{"unowned-container", "unowned-network", "permission-denied", "malformed-response"} {
		t.Run(failure, func(t *testing.T) {
			var calls []string
			node := tailscaleNodeName("app")
			base := fakeNodeRunner("app", &calls)
			svc := &DockerContainers{exposureDocker: func(ctx context.Context, args ...string) ([]byte, error) {
				cmd := strings.Join(args, " ")
				if cmd == "container inspect "+node {
					calls = append(calls, cmd)
					switch failure {
					case "unowned-container":
						return []byte(`[{"State":{"Running":false},"Config":{"Labels":{}}}]`), nil
					case "permission-denied":
						return []byte("permission denied accessing Docker socket"), fmt.Errorf("exit status 1")
					case "malformed-response":
						return []byte(`not-json`), nil
					default:
						return []byte(fmt.Sprintf(`[{"State":{"Running":false},"Config":{"Labels":{"%s":"app"}}}]`, tailscaleOwnerLabel)), nil
					}
				}
				if cmd == "network inspect "+node && failure == "unowned-network" {
					calls = append(calls, cmd)
					return []byte(`[{"Labels":{}}]`), nil
				}
				return base(ctx, args...)
			}}
			previous := ExposureSettings{Mode: "private", PrivateType: "tailscale", PrivatePort: 80, Target: "app:8080", TailscaleNode: node}
			if _, err := svc.syncTailscaleExposure("app", previous, &ExposureSettings{Mode: "public"}); err == nil {
				t.Fatal("ignored ownership or Docker inspection failure")
			}
			for _, call := range calls {
				if strings.HasPrefix(call, "exec ") || strings.HasPrefix(call, "start ") || strings.HasPrefix(call, "stop ") || strings.HasPrefix(call, "network disconnect ") {
					t.Fatalf("changed routing despite failed inspection: %v", calls)
				}
			}
		})
	}
}

func TestCleanupQueueFailureRollsBackExposure(t *testing.T) {
	setup := setupFixture(t)
	svc := &DockerContainers{db: setup.db}
	previous := ExposureSettings{Mode: "private", PrivateType: "tailscale", PrivatePort: 80, Target: "app:8080"}
	if err := svc.saveExposureToDB("app", previous); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.db.Exec(`CREATE TRIGGER reject_tailscale_cleanup BEFORE INSERT ON tailscale_rule_cleanup BEGIN SELECT RAISE(ABORT, 'injected cleanup failure'); END`); err != nil {
		t.Fatal(err)
	}
	next := ExposureSettings{Mode: "public", Hostname: "app.example.com", cleanup: &tailscaleRuleCleanup{App: "app", Node: "docktun-tailscale", Port: 80, Target: "app:8080"}}
	if err := svc.saveExposureToDB("app", next); err == nil || !strings.Contains(err.Error(), "injected cleanup failure") {
		t.Fatalf("expected cleanup insert failure, got %v", err)
	}
	if got := svc.getExposureFromDB("app"); got.Mode != previous.Mode || got.PrivateType != previous.PrivateType || got.Target != previous.Target || got.Hostname != "" {
		t.Fatalf("exposure committed without durable cleanup: %#v", got)
	}
	pending, err := svc.pendingTailscaleRules("docktun-tailscale")
	if err != nil || len(pending) != 0 {
		t.Fatalf("failed transaction retained cleanup: %#v, %v", pending, err)
	}
}

func TestTailscaleUndoRestoresRuleBeforeAppConnectivity(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(fmt.Sprintf("running=%t", running), func(t *testing.T) {
			var calls []string
			node := tailscaleNodeName("app")
			base := fakeNodeRunner("app", &calls)
			svc := &DockerContainers{exposureDocker: func(ctx context.Context, args ...string) ([]byte, error) {
				cmd := strings.Join(args, " ")
				if cmd == "container inspect "+node {
					calls = append(calls, cmd)
					return []byte(fmt.Sprintf(`[{"State":{"Running":%t},"Config":{"Labels":{"%s":"app"}}}]`, running, tailscaleOwnerLabel)), nil
				}
				if cmd == "exec "+node+" tailscale serve status --json" {
					calls = append(calls, cmd)
					return []byte(`{"TCP":{"80":{"TCPForward":"app:8080"}}}`), nil
				}
				return base(ctx, args...)
			}}
			previous := ExposureSettings{Mode: "private", PrivateType: "tailscale", PrivatePort: 80, Target: "app:8080", TailscaleNode: node}
			undo, err := svc.syncTailscaleExposure("app", previous, &ExposureSettings{Mode: "public"})
			if err != nil {
				t.Fatal(err)
			}
			calls = nil
			undo()
			want := []string{"network connect " + node + " app"}
			if running {
				want = append([]string{"start " + node, "exec " + node + " tailscale status --json", "exec " + node + " tailscale serve status --json", "exec " + node + " tailscale serve --bg --tcp=80 tcp://app:8080"}, want...)
			}
			if strings.Join(calls, "\n") != strings.Join(want, "\n") {
				t.Fatalf("unsafe rollback order: got %v, want %v", calls, want)
			}
		})
	}
}

func TestTailscaleUndoWaitsForControlSocket(t *testing.T) {
	var calls []string
	node := tailscaleNodeName("app")
	base := fakeNodeRunner("app", &calls)
	rollingBack := false
	statusAttempts := 0
	svc := &DockerContainers{exposureDocker: func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := strings.Join(args, " ")
		if cmd == "exec "+node+" tailscale serve status --json" {
			calls = append(calls, cmd)
			if rollingBack {
				statusAttempts++
				if statusAttempts == 1 {
					return []byte("failed to connect to local tailscaled; no such file or directory"), fmt.Errorf("exit status 1")
				}
				return []byte(`{}`), nil
			}
			return []byte(`{"TCP":{"80":{"TCPForward":"app:8080"}}}`), nil
		}
		if rollingBack && cmd == "exec "+node+" tailscale serve --bg --tcp=80 tcp://app:8080" && statusAttempts < 2 {
			t.Error("restored Serve before the local control socket became available")
		}
		return base(ctx, args...)
	}}
	previous := ExposureSettings{Mode: "private", PrivateType: "tailscale", PrivatePort: 80, Target: "app:8080", TailscaleNode: node}
	undo, err := svc.syncTailscaleExposure("app", previous, &ExposureSettings{Mode: "public"})
	if err != nil {
		t.Fatal(err)
	}
	calls = nil
	rollingBack = true
	undo()
	want := []string{
		"start " + node,
		"exec " + node + " tailscale status --json",
		"exec " + node + " tailscale serve status --json",
		"exec " + node + " tailscale status --json",
		"exec " + node + " tailscale serve status --json",
		"exec " + node + " tailscale serve --bg --tcp=80 tcp://app:8080",
		"network connect " + node + " app",
	}
	if statusAttempts != 2 || strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rollback did not recover after socket startup: attempts %d, calls %v", statusAttempts, calls)
	}
}

func TestPendingTailscaleCleanupWaitsForPersistedProfile(t *testing.T) {
	setup := setupFixture(t)
	var calls []string
	node := tailscaleNodeName("app")
	base := fakeNodeRunner("app", &calls)
	backendChecks := 0
	svc := &DockerContainers{db: setup.db}
	svc.exposureDocker = func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := strings.Join(args, " ")
		if cmd == "exec "+node+" tailscale status --json" {
			calls = append(calls, cmd)
			backendChecks++
			pending, err := svc.pendingTailscaleRules(node)
			if err != nil || len(pending) != 1 {
				t.Fatalf("cleanup was consumed before the persisted profile loaded: %#v, %v", pending, err)
			}
			if backendChecks == 1 {
				return []byte(`{"BackendState":"Starting"}`), nil
			}
			return []byte(`{"BackendState":"Running"}`), nil
		}
		if cmd == "exec "+node+" tailscale serve status --json" {
			calls = append(calls, cmd)
			if backendChecks < 2 {
				// The real CLI can return this before loading persisted Serve rules.
				return []byte(`{}`), nil
			}
			return []byte(`{"TCP":{"80":{"TCPForward":"app:8080"}}}`), nil
		}
		return base(ctx, args...)
	}
	if err := svc.saveExposureToDB("app", ExposureSettings{Mode: "public", cleanup: &tailscaleRuleCleanup{App: "app", Node: node, Port: 80, Target: "app:8080"}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.waitPendingTailscaleRules(node); err != nil {
		t.Fatal(err)
	}
	if backendChecks < 2 || !strings.Contains(strings.Join(calls, "\n"), "exec "+node+" tailscale serve --bg --tcp=80 off") {
		t.Fatalf("early empty Serve response skipped persisted rule cleanup: %v", calls)
	}
	pending, err := svc.pendingTailscaleRules(node)
	if err != nil || len(pending) != 0 {
		t.Fatalf("loaded profile was not cleaned: %#v, %v", pending, err)
	}
}

func TestSignedOutTailscaleKeepsCleanupAndAppDetachedUntilLogin(t *testing.T) {
	setup := setupFixture(t)
	var calls []string
	node := tailscaleNodeName("app")
	missingNode := tailscaleNodeName("missing-app")
	stoppedNode := "docktun-tailscale"
	backendState := "NeedsLogin"
	base := fakeNodeRunner("app", &calls)
	svc := &DockerContainers{db: setup.db, exposureDocker: func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := strings.Join(args, " ")
		switch cmd {
		case "container inspect app":
			calls = append(calls, cmd)
			return []byte(`[{"State":{"Running":true},"NetworkSettings":{"Networks":{"original":{}}}}]`), nil
		case "container inspect " + missingNode:
			calls = append(calls, cmd)
			return []byte("No such container"), fmt.Errorf("exit status 1")
		case "container inspect " + stoppedNode:
			calls = append(calls, cmd)
			return []byte(`[{"State":{"Running":false}}]`), nil
		case "exec " + node + " tailscale status --json":
			calls = append(calls, cmd)
			return json.Marshal(map[string]string{"BackendState": backendState})
		case "exec " + node + " tailscale serve status --json":
			calls = append(calls, cmd)
			if backendState != "Running" {
				return []byte(`{}`), nil
			}
			return []byte(`{"TCP":{"80":{"TCPForward":"app:8080"},"443":{"TCPForward":"unrelated:443"}}}`), nil
		}
		return base(ctx, args...)
	}}
	for _, rule := range []tailscaleRuleCleanup{
		{App: "app", Node: node, Port: 80, Target: "app:8080"},
		{App: "missing-app", Node: missingNode, Port: 80, Target: "missing-app:8080"},
		{App: "stopped-app", Node: stoppedNode, Port: 80, Target: "stopped-app:8080"},
	} {
		if err := svc.saveExposureToDB(rule.App, ExposureSettings{Mode: "public", cleanup: &rule}); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.ensureNodeResources("app"); err != nil {
		t.Fatalf("signed-out helper was not left available for login: %v", err)
	}
	if strings.Contains(strings.Join(calls, "\n"), "network connect "+node+" app") {
		t.Fatalf("attached app before profile cleanup: %v", calls)
	}
	pending, err := svc.pendingTailscaleRules(node)
	if err != nil || len(pending) != 1 {
		t.Fatalf("signed-out helper lost pending cleanup: %#v, %v", pending, err)
	}
	calls = nil
	svc.ReconcileTailscaleAppNetworks()
	pending, err = svc.pendingTailscaleRules(node)
	if err != nil || len(pending) != 1 {
		t.Fatalf("ticker consumed signed-out profile cleanup: %#v, %v", pending, err)
	}
	backendState = "Running"
	svc.ReconcileTailscaleAppNetworks()
	pending, err = svc.pendingTailscaleRules(node)
	if err != nil || len(pending) != 0 {
		t.Fatalf("ticker failed cleanup after login: %#v, %v", pending, err)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "exec "+node+" tailscale serve --bg --tcp=80 off") {
		t.Fatalf("ticker did not remove selected old rule: %v", calls)
	}
	for _, dormant := range []string{missingNode, stoppedNode} {
		pending, err := svc.pendingTailscaleRules(dormant)
		if err != nil || len(pending) != 1 {
			t.Fatalf("ticker discarded dormant cleanup in %s: %#v, %v", dormant, pending, err)
		}
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "start ") || strings.HasPrefix(call, "run ") || strings.HasPrefix(call, "stop ") || strings.HasPrefix(call, "network connect ") || strings.Contains(call, "--tcp=443") || strings.Contains(call, "exec "+missingNode+" ") || strings.Contains(call, "exec "+stoppedNode+" ") {
			t.Fatalf("ticker enabled a provider or touched unrelated connectivity: %v", calls)
		}
	}
}

func TestTailscaleReconnectCleansBeforeConnectingApp(t *testing.T) {
	for _, target := range []string{"app:8080", "unrelated:8080"} {
		t.Run(target, func(t *testing.T) {
			setup := setupFixture(t)
			var calls []string
			node := tailscaleNodeName("app")
			base := fakeNodeRunner("app", &calls)
			svc := &DockerContainers{db: setup.db, exposureDocker: func(ctx context.Context, args ...string) ([]byte, error) {
				cmd := strings.Join(args, " ")
				if cmd == "container inspect app" {
					calls = append(calls, cmd)
					return []byte(`[{"State":{"Running":true},"NetworkSettings":{"Networks":{"original":{}}}}]`), nil
				}
				if cmd == "exec "+node+" tailscale serve status --json" {
					calls = append(calls, cmd)
					return json.Marshal(map[string]any{"TCP": map[string]any{"80": map[string]string{"TCPForward": target}}})
				}
				return base(ctx, args...)
			}}
			if err := svc.saveExposureToDB("app", ExposureSettings{Mode: "public", cleanup: &tailscaleRuleCleanup{App: "app", Node: node, Port: 80, Target: "app:8080"}}); err != nil {
				t.Fatal(err)
			}
			err := svc.ensureNodeResources("app")
			if target == "unrelated:8080" {
				if err == nil || !strings.Contains(err.Error(), "unrelated Serve rule") {
					t.Fatalf("expected unrelated-rule protection, got %v", err)
				}
				if strings.Contains(strings.Join(calls, "\n"), "network connect "+node+" app") {
					t.Fatalf("connected app despite failed cleanup: %v", calls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			removedAt, connectedAt := -1, -1
			for i, call := range calls {
				if call == "exec "+node+" tailscale serve --bg --tcp=80 off" {
					removedAt = i
				}
				if call == "network connect "+node+" app" {
					connectedAt = i
				}
			}
			if removedAt < 0 || connectedAt <= removedAt {
				t.Fatalf("stale route could resume before cleanup: %v", calls)
			}
			pending, err := svc.pendingTailscaleRules(node)
			if err != nil || len(pending) != 0 {
				t.Fatalf("successful cleanup was not consumed: %#v, %v", pending, err)
			}
		})
	}
}
