package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestIsPortConflictError(t *testing.T) {
	tests := []struct {
		err      error
		expected bool
	}{
		{nil, false},
		{errors.New("some unrelated error"), false},
		{errors.New("Bind for 0.0.0.0:8080 failed: port is already allocated"), true},
		{errors.New("listen tcp 0.0.0.0:3000: bind: address already in use"), true},
		{errors.New("failed to bind port 80: address already in use"), true},
		{errors.New("docker: Error response from daemon: driver failed programming external connectivity on endpoint foo: failed to bind port 0.0.0.0:8080/tcp"), true},
	}

	for _, tt := range tests {
		actual := IsPortConflictError(tt.err)
		if actual != tt.expected {
			t.Errorf("IsPortConflictError(%v) = %v; want %v", tt.err, actual, tt.expected)
		}
	}
}

func TestRetryOnPortConflict_SuccessImmediate(t *testing.T) {
	var calls int
	fn := func() error {
		calls++
		return nil
	}

	err := RetryOnPortConflict(context.Background(), fn, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestRetryOnPortConflict_NonPortError_NoRetry(t *testing.T) {
	var calls int
	nonPortErr := errors.New("container not found")
	fn := func() error {
		calls++
		return nonPortErr
	}

	err := RetryOnPortConflict(context.Background(), fn, 3, 10*time.Millisecond)
	if !errors.Is(err, nonPortErr) {
		t.Fatalf("expected %v, got %v", nonPortErr, err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call (no retry for non-port error), got %d", calls)
	}
}

func TestRetryOnPortConflict_PortConflict_Recovers(t *testing.T) {
	var calls int
	portErr := errors.New("driver failed: port is already allocated")
	fn := func() error {
		calls++
		if calls < 3 {
			return portErr
		}
		return nil
	}

	start := time.Now()
	err := RetryOnPortConflict(context.Background(), fn, 4, 10*time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected recovery, got error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
	if elapsed < 20*time.Millisecond {
		t.Fatalf("expected backoff delay, elapsed: %v", elapsed)
	}
}

func TestRetryOnPortConflict_MaxAttemptsExhausted(t *testing.T) {
	var calls int
	portErr := errors.New("listen tcp: bind: address already in use")
	fn := func() error {
		calls++
		return portErr
	}

	err := RetryOnPortConflict(context.Background(), fn, 3, 5*time.Millisecond)
	if !errors.Is(err, portErr) {
		t.Fatalf("expected %v, got %v", portErr, err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestRetryOnPortConflict_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	portErr := errors.New("failed to bind port 8080: address already in use")

	fn := func() error {
		calls++
		cancel() // Cancel context during retry
		return portErr
	}

	err := RetryOnPortConflict(ctx, fn, 5, 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected context canceled error, got: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call before cancel, got %d", calls)
	}
}

func TestExtractDesiredPortsFromComposeContent(t *testing.T) {
	compose := `
version: '3.8'
services:
  web:
    image: nginx:alpine
    ports:
      - "8080:80"
      - "127.0.0.1:8443:443"
      - target: 53
        published: "5353"
        protocol: udp
        host_ip: "0.0.0.0"
  worker:
    image: redis:alpine
    container_name: custom_redis
    ports:
      - "6379:6379"
`

	ports := ExtractDesiredPortsFromComposeContent(compose)
	if len(ports) != 4 {
		t.Fatalf("expected 4 desired ports, got %d: %+v", len(ports), ports)
	}

	// Verify web 8080:80
	found8080 := false
	found8443 := false
	found5353 := false
	found6379 := false

	for _, p := range ports {
		if p.HostPort == 8080 && p.Protocol == "tcp" && p.ContainerName == "web" {
			found8080 = true
		}
		if p.HostPort == 8443 && p.HostIP == "127.0.0.1" && p.Protocol == "tcp" && p.ContainerName == "web" {
			found8443 = true
		}
		if p.HostPort == 5353 && p.Protocol == "udp" && p.ContainerName == "web" {
			found5353 = true
		}
		if p.HostPort == 6379 && p.Protocol == "tcp" && p.ContainerName == "custom_redis" {
			found6379 = true
		}
	}

	if !found8080 || !found8443 || !found5353 || !found6379 {
		t.Errorf("missing parsed ports: 8080=%v, 8443=%v, 5353=%v, 6379=%v",
			found8080, found8443, found5353, found6379)
	}
}

func TestIpMatchesCollision(t *testing.T) {
	tests := []struct {
		desired  string
		occupied string
		expected bool
	}{
		{"", "", true},
		{"0.0.0.0", "", true},
		{"", "0.0.0.0", true},
		{"::", "0.0.0.0", true},
		{"127.0.0.1", "0.0.0.0", true},
		{"0.0.0.0", "127.0.0.1", true},
		{"127.0.0.1", "127.0.0.1", true},
		{"192.168.1.10", "192.168.1.20", false},
		{"127.0.0.1", "192.168.1.10", false},
	}

	for _, tt := range tests {
		actual := ipMatchesCollision(tt.desired, tt.occupied)
		if actual != tt.expected {
			t.Errorf("ipMatchesCollision(%q, %q) = %v; want %v", tt.desired, tt.occupied, actual, tt.expected)
		}
	}
}
