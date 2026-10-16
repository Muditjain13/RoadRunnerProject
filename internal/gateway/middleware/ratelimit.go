package middleware

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	// idleTTL is how long a client's bucket is kept after its last request.
	// An idle bucket is full again anyway, so dropping it changes nothing for
	// that client and stops the map growing without bound.
	idleTTL       = 10 * time.Minute
	sweepInterval = time.Minute
)

type clientLimiter struct {
	limiter *rate.Limiter
	seen    time.Time
}

// RateLimiter is a per-client-IP token bucket. Buckets are created lazily
// and swept after idleTTL without traffic.
//
// Optional behaviors, all off unless configured:
//   - WithTrustedProxies: behind a reverse proxy every request arrives from the
//     proxy's address, so the real client is read from X-Forwarded-For, but
//     only when the direct peer is a configured trusted proxy.
//   - WithWriteLimit: an extra, stricter bucket for mutating requests (POST,
//     PUT, PATCH, DELETE), on top of the general one.
type RateLimiter struct {
	mu        sync.Mutex
	limiters  map[string]*clientLimiter
	rps       rate.Limit
	burst     int
	lastSweep time.Time

	trusted []*net.IPNet
	write   *RateLimiter

	now func() time.Time
}

func NewRateLimiter(requestsPerSecond float64, burst int) *RateLimiter {
	return &RateLimiter{
		limiters: make(map[string]*clientLimiter),
		rps:      rate.Limit(requestsPerSecond),
		burst:    burst,
		now:      time.Now,
	}
}

// WithTrustedProxies marks the given CIDRs (or single IPs) as reverse proxies
// whose X-Forwarded-For header may be believed.
func (rl *RateLimiter) WithTrustedProxies(cidrs []string) (*RateLimiter, error) {
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if !strings.Contains(c, "/") {
			if ip := net.ParseIP(c); ip != nil && ip.To4() != nil {
				c += "/32"
			} else {
				c += "/128"
			}
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q: %w", c, err)
		}
		rl.trusted = append(rl.trusted, n)
	}
	return rl, nil
}

// WithWriteLimit adds a stricter per-client bucket for mutating requests.
func (rl *RateLimiter) WithWriteLimit(requestsPerSecond float64, burst int) *RateLimiter {
	w := NewRateLimiter(requestsPerSecond, burst)
	w.now = rl.now
	rl.write = w
	return rl
}

func (rl *RateLimiter) allow(clientKey string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()
	if now.Sub(rl.lastSweep) >= sweepInterval {
		for k, c := range rl.limiters {
			if now.Sub(c.seen) > idleTTL {
				delete(rl.limiters, k)
			}
		}
		rl.lastSweep = now
	}
	c, ok := rl.limiters[clientKey]
	if !ok {
		c = &clientLimiter{limiter: rate.NewLimiter(rl.rps, rl.burst)}
		rl.limiters[clientKey] = c
	}
	c.seen = now
	return c.limiter.Allow()
}

func (rl *RateLimiter) size() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.limiters)
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := rl.clientKey(r)
		if !rl.allow(key) || (rl.write != nil && isMutating(r.Method) && !rl.write.allow(key)) {
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isMutating(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

func (rl *RateLimiter) isTrusted(ip net.IP) bool {
	for _, n := range rl.trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// clientKey identifies the caller. Without trusted proxies it is the TCP peer
// address. When the peer is a trusted proxy it is the right-most
// X-Forwarded-For entry that is not itself a trusted proxy, which a client
// cannot forge because it is the address the proxy saw, not text the client sent.
func (rl *RateLimiter) clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if len(rl.trusted) == 0 {
		return host
	}
	peer := net.ParseIP(host)
	if peer == nil || !rl.isTrusted(peer) {
		return host
	}
	parts := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := net.ParseIP(strings.TrimSpace(parts[i]))
		if ip == nil {
			continue
		}
		if !rl.isTrusted(ip) {
			return ip.String()
		}
	}
	return host
}
