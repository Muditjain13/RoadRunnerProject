package middleware

import (
	"net"
	"net/http"
	"sync"

	"golang.org/x/time/rate"
)

// RateLimiter is a per-client-IP token bucket. Buckets are created lazily
// and never evicted - acceptable for a portfolio project's traffic
// volume; a production gateway would age out idle entries.
type RateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	rps      rate.Limit
	burst    int
}

func NewRateLimiter(requestsPerSecond float64, burst int) *RateLimiter {
	return &RateLimiter{
		limiters: make(map[string]*rate.Limiter),
		rps:      rate.Limit(requestsPerSecond),
		burst:    burst,
	}
}

func (rl *RateLimiter) limiterFor(clientKey string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	l, ok := rl.limiters[clientKey]
	if !ok {
		l = rate.NewLimiter(rl.rps, rl.burst)
		rl.limiters[clientKey] = l
	}
	return l
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := clientKey(r)
		if !rl.limiterFor(key).Allow() {
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
