package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterIgnoresPortAndForwardedHeaders(t *testing.T) {
	h := NewRateLimiter(1, time.Minute).Limit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	for i, addr := range []string{"192.0.2.1:1000", "192.0.2.1:2000"} {
		r := httptest.NewRequest("POST", "/auth/login", nil)
		r.RemoteAddr = addr
		r.Header.Set("X-Forwarded-For", addr)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 200
		if i == 1 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("got %d want %d", w.Code, want)
		}
	}
}
