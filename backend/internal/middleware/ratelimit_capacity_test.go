package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterCapacityPreservesActiveAuthenticationLimits(t *testing.T) {
	rl := NewRateLimiter(1, time.Minute)
	now := time.Now()
	for i := range 10000 {
		rl.requests[fmt.Sprintf("2001:db8::%x", i)] = []time.Time{now}
	}
	h := rl.Limit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("saturated or throttled request reached authentication")
	}))
	for _, address := range []string{"[2001:db8:1::1]:1234", "[2001:db8::1]:4321"} {
		r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		r.RemoteAddr = address
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("%s: got %d, want 429", address, w.Code)
		}
	}
	if len(rl.requests) != 10000 {
		t.Fatalf("active throttles were discarded: retained %d", len(rl.requests))
	}
	for client, requests := range rl.requests {
		if len(requests) != 1 || !requests[0].Equal(now) {
			t.Fatalf("active throttle changed for %s", client)
		}
	}
}

func TestRateLimiterCapacityReclaimsExpiredClientsWithoutGlobalLockout(t *testing.T) {
	rl := NewRateLimiter(1, time.Minute)
	for i := range 10000 {
		rl.requests[fmt.Sprintf("2001:db8::%x", i)] = []time.Time{time.Now().Add(-2 * time.Minute)}
	}
	rl.requests["192.0.2.1"] = []time.Time{time.Now()}
	h := rl.Limit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	r.RemoteAddr = "192.0.2.2:1234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent || len(rl.requests) != 2 {
		t.Fatalf("expired clients blocked new admission: status=%d retained=%d", w.Code, len(rl.requests))
	}
	r.RemoteAddr = "192.0.2.1:5678"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("existing active throttle was lost: got %d", w.Code)
	}
}
