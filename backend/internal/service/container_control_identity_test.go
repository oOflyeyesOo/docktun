package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestDestructiveContainerControlResolvesSelfAliases(t *testing.T) {
	hostname, _ := os.Hostname()
	ownID := strings.Repeat("a", 64)
	appID := strings.Repeat("b", 64)
	for _, configured := range []string{"", "docktun"} {
		t.Run("configured_"+configured, func(t *testing.T) {
			t.Setenv("DOCKTUN_CONTAINER", configured)
			var mutations [][]string
			service := &DockerContainers{controlDocker: func(_ context.Context, args ...string) ([]byte, error) {
				if args[0] != "inspect" {
					mutations = append(mutations, append([]string{}, args...))
					return nil, nil
				}
				id := args[1]
				c := updateContainer{ID: ownID, Name: "/docktun", Config: map[string]any{"Hostname": hostname}}
				c.State.Running = true
				if id == "docktun-neighbor" {
					c.ID = appID
					c.Name = "/docktun-neighbor"
				}
				return json.Marshal([]updateContainer{c})
			}}
			for _, alias := range []string{"docktun", "/docktun", ownID[:12], ownID} {
				if err := service.Stop(alias); err == nil {
					t.Fatalf("self stop allowed through %q", alias)
				}
				if err := service.Remove(alias, true); err == nil {
					t.Fatalf("self removal allowed through %q", alias)
				}
			}
			if len(mutations) != 0 {
				t.Fatalf("destructive self command executed: %v", mutations)
			}
			if err := service.Stop("docktun-neighbor"); err != nil {
				t.Fatal(err)
			}
			if err := service.Remove("docktun-neighbor", true); err != nil {
				t.Fatal(err)
			}
			if len(mutations) != 2 || strings.Join(mutations[0], " ") != "stop "+appID || strings.Join(mutations[1], " ") != "rm -f "+appID {
				t.Fatalf("controls must use verified immutable app ID: %v", mutations)
			}
		})
	}
}

func TestDestructiveContainerControlRefusesUnverifiedSelf(t *testing.T) {
	service := &DockerContainers{controlDocker: func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] != "inspect" {
			t.Fatalf("mutation before identity verification: %v", args)
		}
		return nil, errors.New("cannot inspect self")
	}}
	if service.Stop("app") == nil || service.Remove("app", true) == nil {
		t.Fatal("unverified destructive control accepted")
	}
}
