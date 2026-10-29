package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestTailscaleStoppedDuringProviderSwitchDefersCleanup(t *testing.T) {
	for _, dedicated := range []bool{false, true} {
		for _, phase := range []string{"verify", "remove", "after-detach", "stop"} {
			if (phase == "stop" || phase == "after-detach") && !dedicated {
				continue // Leaving an app must never stop the shared provider.
			}
			for _, missing := range []bool{false, true} {
				for _, commit := range []bool{false, true} {
					t.Run(fmt.Sprintf("dedicated=%t/%s/missing=%t/commit=%t", dedicated, phase, missing, commit), func(t *testing.T) {
						setup := setupFixture(t)
						var calls []string
						node := "docktun-tailscale"
						previous := ExposureSettings{Mode: "private", PrivateType: "tailscale", PrivatePort: 80, Target: "app:8080"}
						if dedicated {
							node = tailscaleNodeName("app")
							previous.TailscaleNode = node
						}
						base := fakeNodeRunner("app", &calls)
						stopped := false
						stateChecks := 0
						svc := &DockerContainers{db: setup.db, exposureDocker: func(ctx context.Context, args ...string) ([]byte, error) {
							cmd := strings.Join(args, " ")
							if cmd == "container inspect "+node {
								calls = append(calls, cmd)
								stateChecks++
								if stopped && missing {
									return []byte("Error: No such container: " + node), fmt.Errorf("exit status 1")
								}
								return []byte(fmt.Sprintf(`[{"State":{"Running":%t},"Config":{"Labels":{"%s":"app"}}}]`, !stopped, tailscaleOwnerLabel)), nil
							}
							if cmd == "exec "+node+" tailscale serve status --json" {
								calls = append(calls, cmd)
								if phase == "verify" {
									stopped = true
									return []byte("Error response from daemon: Container fixture-id is not running"), fmt.Errorf("exit status 1")
								}
								return []byte(`{"TCP":{"80":{"TCPForward":"app:8080"},"443":{"TCPForward":"other:443"}}}`), nil
							}
							if cmd == "exec "+node+" tailscale serve --bg --tcp=80 off" || cmd == "stop "+node {
								calls = append(calls, cmd)
								if phase == "remove" || cmd == "stop "+node {
									stopped = true
									return []byte("Error response from daemon: Container fixture-id is not running"), fmt.Errorf("exit status 1")
								}
								return nil, nil
							}
							if phase == "after-detach" && cmd == "network disconnect "+node+" app" {
								stopped = true
							}
							return base(ctx, args...)
						}}
						if err := svc.saveExposureToDB("app", previous); err != nil {
							t.Fatal(err)
						}
						next := ExposureSettings{Mode: "public", Hostname: "app.example.com", Target: "http://app:8080"}
						undo, err := svc.syncTailscaleExposure("app", previous, &next)
						if err != nil {
							t.Fatalf("externally stopped device blocked switching provider: %v", err)
						}
						wantChecks := 2
						if phase == "stop" {
							wantChecks = 3
						}
						if !stopped || stateChecks != wantChecks {
							t.Fatalf("did not confirm the race: stopped=%t, checks=%d, calls=%v", stopped, stateChecks, calls)
						}
						want := tailscaleRuleCleanup{App: "app", Node: node, Port: 80, Target: "app:8080"}
						if next.cleanup == nil || *next.cleanup != want {
							t.Fatalf("wrong deferred route: %#v", next.cleanup)
						}
						if commit {
							if err := svc.saveExposureToDB("app", next); err != nil {
								t.Fatal(err)
							}
							reloaded := &DockerContainers{db: setup.db}
							pending, err := reloaded.pendingTailscaleRules(node)
							if err != nil || len(pending) != 1 || pending[0] != want || reloaded.getExposureFromDB("app").Mode != "public" {
								t.Fatalf("provider and cleanup not committed together: pending=%#v, error=%v", pending, err)
							}
						} else {
							// A later Cloudflare failure must preserve the old settings,
							// without starting a device stopped outside this request.
							undo()
							pending, err := svc.pendingTailscaleRules(node)
							if err != nil || len(pending) != 0 || svc.getExposureFromDB("app").Mode != "private" {
								t.Fatalf("rollback changed saved exposure or queued cleanup: %#v, %v", pending, err)
							}
						}
						all := strings.Join(calls, "\n")
						if dedicated && !strings.Contains(all, "network disconnect "+node+" app") {
							t.Fatalf("inactive owned device retained app connectivity: %v", calls)
						}
						if dedicated && !commit && !strings.Contains(all, "network connect "+node+" app") {
							t.Fatalf("rollback did not restore previous bridge membership: %v", calls)
						}
						for _, call := range calls {
							if strings.HasPrefix(call, "start ") || strings.Contains(call, " reset") || strings.Contains(call, "--tcp=443") || strings.Contains(call, "tcp://app:8080") || phase != "stop" && strings.HasPrefix(call, "stop ") {
								t.Fatalf("modified inactive device or unrelated route: %v", calls)
							}
						}
					})
				}
			}
		}
	}
}

func TestTailscaleSwitchRaceDoesNotHideOtherFailures(t *testing.T) {
	for _, phase := range []string{"verify", "remove", "after-detach", "stop"} {
		for _, failure := range []string{"still-running", "unowned", "malformed-state", "inspect-denied", "operation-denied"} {
			if phase == "after-detach" && (failure == "still-running" || failure == "operation-denied") {
				continue // This phase exercises an invalid latest-state inspection.
			}
			t.Run(phase+"/"+failure, func(t *testing.T) {
				var calls []string
				var unsafeRestore []string
				node := tailscaleNodeName("app")
				base := fakeNodeRunner("app", &calls)
				failed := false
				svc := &DockerContainers{exposureDocker: func(ctx context.Context, args ...string) ([]byte, error) {
					cmd := strings.Join(args, " ")
					if failed && failure != "still-running" && cmd == "exec "+node+" tailscale serve --bg --tcp=80 tcp://app:8080" {
						unsafeRestore = append(unsafeRestore, cmd)
					}
					if cmd == "container inspect "+node && failed {
						calls = append(calls, cmd)
						switch failure {
						case "still-running":
							return []byte(fmt.Sprintf(`[{"State":{"Running":true},"Config":{"Labels":{"%s":"app"}}}]`, tailscaleOwnerLabel)), nil
						case "unowned":
							return []byte(`[{"State":{"Running":false},"Config":{"Labels":{}}}]`), nil
						case "malformed-state":
							return []byte(fmt.Sprintf(`[{"Config":{"Labels":{"%s":"app"}}}]`, tailscaleOwnerLabel)), nil
						case "inspect-denied":
							return []byte("permission denied accessing Docker socket"), fmt.Errorf("exit status 1")
						default:
							return []byte(fmt.Sprintf(`[{"State":{"Running":false},"Config":{"Labels":{"%s":"app"}}}]`, tailscaleOwnerLabel)), nil
						}
					}
					if phase == "verify" && cmd == "exec "+node+" tailscale serve status --json" || phase == "remove" && cmd == "exec "+node+" tailscale serve --bg --tcp=80 off" || phase == "stop" && cmd == "stop "+node {
						calls = append(calls, cmd)
						failed = true
						message := "Error response from daemon: Container fixture-id is not running"
						if failure == "operation-denied" {
							message = "permission denied on Tailscale control socket"
						}
						return []byte(message), fmt.Errorf("exit status 1")
					}
					if cmd == "exec "+node+" tailscale serve status --json" {
						calls = append(calls, cmd)
						return []byte(`{"TCP":{"80":{"TCPForward":"app:8080"}}}`), nil
					}
					if phase == "after-detach" && cmd == "network disconnect "+node+" app" {
						failed = true
					}
					return base(ctx, args...)
				}}
				previous := ExposureSettings{Mode: "private", PrivateType: "tailscale", PrivatePort: 80, Target: "app:8080", TailscaleNode: node}
				next := ExposureSettings{Mode: "public", Hostname: "app.example.com"}
				if _, err := svc.syncTailscaleExposure("app", previous, &next); err == nil || next.cleanup != nil {
					t.Fatalf("hid %s failure or queued cleanup: error=%v, pending=%#v, calls=%v", failure, err, next.cleanup, calls)
				}
				if len(unsafeRestore) != 0 {
					t.Fatalf("restored a rule without a trusted running device: %v", unsafeRestore)
				}
				for _, call := range calls {
					if strings.HasPrefix(call, "start ") || strings.Contains(call, " reset") {
						t.Fatalf("enabled or reset a provider after failed confirmation: %v", calls)
					}
				}
			})
		}
	}
}

func TestSwitchingToTailscaleDoesNotDeferFailedLegacyRead(t *testing.T) {
	var calls []string
	base := fakeNodeRunner("app", &calls)
	sharedChecks := 0
	svc := &DockerContainers{exposureDocker: func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := strings.Join(args, " ")
		if cmd == "container inspect docktun-tailscale" {
			sharedChecks++
			if sharedChecks > 1 {
				return []byte(`[{"State":{"Running":false}}]`), nil
			}
		}
		if cmd == "exec docktun-tailscale tailscale serve status --json" {
			calls = append(calls, cmd)
			return []byte("Error response from daemon: Container fixture-id is not running"), fmt.Errorf("exit status 1")
		}
		return base(ctx, args...)
	}}
	previous := ExposureSettings{Mode: "private", PrivateType: "tailscale", PrivatePort: 80, Target: "app:8080"}
	next := ExposureSettings{Mode: "private", PrivateType: "tailscale", PrivatePort: 80, Target: "app:8080"}
	if _, err := svc.syncTailscaleExposure("app", previous, &next); err == nil || next.cleanup != nil || sharedChecks != 1 {
		t.Fatalf("Tailscale migration deferred an unverified rule: error=%v, pending=%#v, checks=%d", err, next.cleanup, sharedChecks)
	}
	for _, call := range calls {
		if strings.Contains(call, "serve --bg") {
			t.Fatalf("changed routing despite failed Tailscale migration: %v", calls)
		}
	}
}

func TestTailscaleServeValidationCannotBecomeDeferredCleanup(t *testing.T) {
	for _, response := range []string{`not-json`, `{"TCP":{"80":{"TCPForward":"other:8080"}}}`} {
		t.Run(response, func(t *testing.T) {
			var calls []string
			node := tailscaleNodeName("app")
			base := fakeNodeRunner("app", &calls)
			stateChecks := 0
			svc := &DockerContainers{exposureDocker: func(ctx context.Context, args ...string) ([]byte, error) {
				cmd := strings.Join(args, " ")
				if cmd == "container inspect "+node {
					stateChecks++
					if stateChecks > 1 {
						return []byte(fmt.Sprintf(`[{"State":{"Running":false},"Config":{"Labels":{"%s":"app"}}}]`, tailscaleOwnerLabel)), nil
					}
				}
				if cmd == "exec "+node+" tailscale serve status --json" {
					calls = append(calls, cmd)
					return []byte(response), nil
				}
				return base(ctx, args...)
			}}
			previous := ExposureSettings{Mode: "private", PrivateType: "tailscale", PrivatePort: 80, Target: "app:8080", TailscaleNode: node}
			next := ExposureSettings{Mode: "public", Hostname: "app.example.com"}
			if _, err := svc.syncTailscaleExposure("app", previous, &next); err == nil || next.cleanup != nil || stateChecks != 1 {
				t.Fatalf("masked invalid or unrelated Serve configuration: error=%v, pending=%#v, checks=%d", err, next.cleanup, stateChecks)
			}
		})
	}
}
