package service

import (
	"context"
	"docktun/internal/config"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestNetworkNameIsLiteralAndDuplicateUsesOwnNetwork(t *testing.T) {
	p := NewProviderContainers(&config.Config{})
	p.run = func(ctx context.Context, args ...string) ([]byte, error) {
		if args[0] == "network" && args[1] == "ls" {
			if args[3] != `name=^my\.net$` {
				t.Fatalf("unescaped network filter: %s", args[3])
			}
			return []byte("wrong\tmyXnet\nother\tmy.net\nours\tmy.net"), nil
		}
		if args[0] == "inspect" {
			hostname, _ := os.Hostname()
			return json.Marshal([]any{map[string]any{"Config": map[string]any{"Hostname": hostname}, "State": map[string]any{"Running": true}, "NetworkSettings": map[string]any{"Networks": map[string]any{"my.net": map[string]any{"NetworkID": "ours"}}}}})
		}
		t.Fatalf("unexpected command %v", args)
		return nil, nil
	}
	id, err := p.resolveNetwork(context.Background(), "my.net")
	if err != nil || id != "ours" {
		t.Fatalf("wrong duplicate selected: %q %v", id, err)
	}
}

func TestNetworkDuplicateWithoutMembershipFails(t *testing.T) {
	p := NewProviderContainers(&config.Config{})
	p.run = func(ctx context.Context, args ...string) ([]byte, error) {
		if args[0] == "network" && args[1] == "ls" {
			return []byte("first\tshared\nsecond\tshared"), nil
		}
		if args[0] == "inspect" {
			return nil, fmt.Errorf("no own container")
		}
		t.Fatal("mutated an ambiguous network")
		return nil, nil
	}
	if _, err := p.resolveNetwork(context.Background(), "shared"); err == nil || !strings.Contains(err.Error(), "network by its ID") {
		t.Fatalf("ambiguity hidden: %v", err)
	}
}

func TestNetworkLookupFailureDoesNotCreate(t *testing.T) {
	p := NewProviderContainers(&config.Config{})
	p.run = func(ctx context.Context, args ...string) ([]byte, error) {
		if len(args) < 2 || args[1] != "ls" {
			t.Fatal("created network after failed lookup")
		}
		return nil, fmt.Errorf("daemon unavailable")
	}
	if _, err := p.resolveNetwork(context.Background(), "shared"); err == nil {
		t.Fatal("daemon error ignored")
	}
}

func TestNetworkCreationRaceAndIDSelection(t *testing.T) {
	p := NewProviderContainers(&config.Config{})
	lookups := 0
	p.run = func(ctx context.Context, args ...string) ([]byte, error) {
		switch args[1] {
		case "ls":
			lookups++
			if lookups == 1 {
				return nil, nil
			}
			return []byte("winner\tshared"), nil
		case "create":
			return nil, fmt.Errorf("already exists")
		case "inspect":
			return []byte("abcdef1234567890"), nil
		}
		t.Fatalf("unexpected command: %v", args)
		return nil, nil
	}
	if id, err := p.resolveNetwork(context.Background(), "shared"); err != nil || id != "winner" {
		t.Fatalf("creation race lost: %q %v", id, err)
	}
	if id, err := p.resolveNetwork(context.Background(), "abcdef123456"); err != nil || id != "abcdef1234567890" {
		t.Fatalf("network ID treated as name: %q %v", id, err)
	}
}
