package middleware

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimiter implements a simple token bucket rate limiter per IP.
type RateLimiter struct {
	mu      sync.Mutex
	clients map[string]*bucket
	rate    int // requests per window
	window  time.Duration
	now     func() time.Time

	lastCleanup time.Time
}

type bucket struct {
	tokens    int
	lastReset time.Time
}

func NewRateLimiter(rate int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		clients: make(map[string]*bucket),
		rate:    rate,
		window:  window,
		now:     time.Now,
	}
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ClientIP(r)

		rl.mu.Lock()
		now := rl.now()
		if rl.lastCleanup.IsZero() || now.Sub(rl.lastCleanup) >= rl.window {
			for clientIP, existing := range rl.clients {
				if now.Sub(existing.lastReset) >= rl.window {
					delete(rl.clients, clientIP)
				}
			}
			rl.lastCleanup = now
		}

		b, exists := rl.clients[ip]
		if !exists {
			b = &bucket{tokens: rl.rate, lastReset: now}
			rl.clients[ip] = b
		}

		// Reset bucket if window has passed
		if now.Sub(b.lastReset) >= rl.window {
			b.tokens = rl.rate
			b.lastReset = now
		}

		if b.tokens <= 0 {
			retryAfter := int(b.lastReset.Add(rl.window).Sub(now).Seconds())
			if b.lastReset.Add(rl.window).After(now.Add(time.Duration(retryAfter) * time.Second)) {
				retryAfter++
			}
			if retryAfter < 1 {
				retryAfter = 1
			}
			rl.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"message":"rate limit exceeded","type":"error","code":"rate_limit"}}`))
			return
		}

		b.tokens--
		rl.mu.Unlock()

		next.ServeHTTP(w, r)
	})
}
