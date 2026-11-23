package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type RateLimiter struct {
	requests    map[string][]time.Time
	mu          sync.Mutex
	limit       int
	window      time.Duration
	lastCleanup time.Time
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		requests:    make(map[string][]time.Time),
		limit:       limit,
		window:      window,
		lastCleanup: time.Now(),
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

		// Periodic cleanup of expired entries (at most once every 30s or window)
		cleanupInterval := rl.window
		if cleanupInterval < 30*time.Second {
			cleanupInterval = 30 * time.Second
		}
		if now.Sub(rl.lastCleanup) >= cleanupInterval {
			for key, reqs := range rl.requests {
				if len(reqs) == 0 || !reqs[len(reqs)-1].After(cutoff) {
					delete(rl.requests, key)
				}
			}
			rl.lastCleanup = now
		}

		// If table is nearing capacity, prune expired keys immediately
		if _, exists := rl.requests[ip]; !exists && len(rl.requests) >= 10000 {
			for key, reqs := range rl.requests {
				if len(reqs) == 0 || !reqs[len(reqs)-1].After(cutoff) {
					delete(rl.requests, key)
				}
			}
			rl.lastCleanup = now
			// Preserve active throttles. Evicting them lets source-address churn
			// reset authentication limits within the current window.
			if len(rl.requests) >= 10000 {
				rl.mu.Unlock()
				http.Error(w, `{"error":"rate limiter busy"}`, http.StatusTooManyRequests)
				return
			}
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
