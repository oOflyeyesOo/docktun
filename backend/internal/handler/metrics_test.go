package handler

import (
	"context"
	"docktun/internal/config"
	"docktun/internal/service"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestParseDiskOutput(t *testing.T) {
	// Case 1: Standard POSIX or unwrapped single-line df output
	standardOutput := `Filesystem     1024-blocks      Used Available Capacity Mounted on
/dev/sda1        102604620  15518720  81861780      16% /app/data`

	total, used, free, pct := parseDiskOutput(standardOutput)
	if total != 102604620*1024 {
		t.Fatalf("expected total %d, got %d", 102604620*1024, total)
	}
	if used != 15518720*1024 {
		t.Fatalf("expected used %d, got %d", 15518720*1024, used)
	}
	if free != 81861780*1024 {
		t.Fatalf("expected free %d, got %d", 81861780*1024, free)
	}
	if math.Abs(pct-15.12) > 0.1 {
		t.Fatalf("expected pct ~15.12, got %f", pct)
	}

	// Case 2: GNU df with long device name wrapped onto next line (Claude's exact reported bug!)
	wrappedOutput := `Filesystem           1K-blocks      Used Available Use% Mounted on
/dev/mapper/syno_vg1-volume_data
                     102604620  15518720  81861780  16% /app/data`

	totalW, usedW, freeW, pctW := parseDiskOutput(wrappedOutput)
	if totalW != 102604620*1024 {
		t.Fatalf("expected total %d, got %d", 102604620*1024, totalW)
	}
	if usedW != 15518720*1024 {
		t.Fatalf("expected used %d, got %d", 15518720*1024, usedW)
	}
	if freeW != 81861780*1024 {
		t.Fatalf("expected free %d, got %d", 81861780*1024, freeW)
	}
	if math.Abs(pctW-15.12) > 0.1 {
		t.Fatalf("expected pct ~15.12, got %f", pctW)
	}

	// Case 3: Empty / invalid
	tE, uE, fE, pE := parseDiskOutput("")
	if tE != 0 || uE != 0 || fE != 0 || pE != 0 {
		t.Fatalf("expected zeroes for empty input, got %d, %d, %d, %f", tE, uE, fE, pE)
	}
}

func TestMetricsSkipsUnusedCloudflareAndReadsRunningTunnel(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write([]byte("cloudflared_tunnel_total_requests 42\ncloudflared_tunnel_active_streams 3\n"))
	}))
	defer server.Close()
	h := NewMetrics(&config.Config{MetricsAddr: server.URL}, nil)
	resp := metricsResponse{ResponseCodes: make(map[string]int64)}
	h.readTunnelMetrics(context.Background(), false, &resp)
	if requests.Load() != 0 {
		t.Fatal("unused provider was contacted")
	}
	h.readTunnelMetrics(context.Background(), true, &resp)
	if requests.Load() != 1 || resp.TotalRequests != 42 || resp.ActiveConns != 3 {
		t.Fatalf("running provider metrics missing: %+v", resp)
	}
}

func TestMetricsRequestCancellationAndHTTPFailure(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("cloudflared_tunnel_total_requests 99"))
	}))
	defer server.Close()
	h := NewMetrics(&config.Config{MetricsAddr: server.URL}, nil)
	resp := metricsResponse{ResponseCodes: make(map[string]int64)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.readTunnelMetrics(ctx, true, &resp)
	if requests.Load() != 0 {
		t.Fatal("canceled request reached metrics server")
	}
	h.readTunnelMetrics(context.Background(), true, &resp)
	if resp.TotalRequests != 0 || resp.Raw != "" {
		t.Fatal("HTTP error body interpreted as metrics")
	}
}

func TestContainerCPUSummaryDistinguishesMissingUsageFromZero(t *testing.T) {
	cases := []struct {
		containers []service.Container
		total      float64
		available  bool
	}{
		{[]service.Container{{Running: true, CPU: "0.00%"}}, 0, true},
		{[]service.Container{{Running: true, CPU: "150%"}, {Running: true, CPU: "50%"}}, 200, true},
		{[]service.Container{{Running: true, CPU: ""}, {Running: true, CPU: "10%"}}, 10, false},
		{[]service.Container{{Running: false, CPU: "old"}}, 0, true},
		{[]service.Container{{Running: true, CPU: "NaN%"}}, 0, false},
		{[]service.Container{{Running: true, CPU: "+Inf%"}}, 0, false},
	}
	for _, tc := range cases {
		total, available := containerCPUSummary(tc.containers)
		if total != tc.total || available != tc.available {
			t.Fatalf("CPU total/sample availability wrong: %v %v", total, available)
		}
	}
}
