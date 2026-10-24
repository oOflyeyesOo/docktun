package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type RateLimiter struct {
	requests map[string][]time.Time
	mu       sync.Mutex
	limit    int
	window   time.Duration
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		requests: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
	}
}

func (rl *RateLimiter) Limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		rl.mu.Lock()

		now := time.Now()
		cutoff := now.Add(-rl.window)
		// Bound memory across clients and discard inactive entries.
		for key, requests := range rl.requests {
			if len(requests) == 0 || !requests[len(requests)-1].After(cutoff) {
				delete(rl.requests, key)
			}
		}
		if _, exists := rl.requests[ip]; !exists && len(rl.requests) >= 10000 {
			rl.mu.Unlock()
			http.Error(w, `{"error":"rate limiter busy"}`, http.StatusTooManyRequests)
			return
		}
		var valid []time.Time
		for _, t := range rl.requests[ip] {
			if t.After(cutoff) {
				valid = append(valid, t)
			}
		}

		if len(valid) >= rl.limit {
			rl.mu.Unlock()
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}

		rl.requests[ip] = append(valid, now)
		rl.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}
