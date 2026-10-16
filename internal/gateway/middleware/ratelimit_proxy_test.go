package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
}

func serve(h http.Handler, method, remote, xff string) int {
	req := httptest.NewRequest(method, "/", nil)
	req.RemoteAddr = remote
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// Behind a trusted proxy, two real clients must get separate buckets even
// though both arrive from the proxy's address.
func TestRateLimiter_TrustedProxyUsesForwardedClient(t *testing.T) {
	rl, err := NewRateLimiter(1, 1).WithTrustedProxies([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	h := rl.Middleware(okHandler())

	if got := serve(h, http.MethodGet, "10.0.0.2:4000", "203.0.113.5"); got != http.StatusOK {
		t.Fatalf("client A first request = %d, want 200", got)
	}
	if got := serve(h, http.MethodGet, "10.0.0.2:4001", "203.0.113.5"); got != http.StatusTooManyRequests {
		t.Errorf("client A second request = %d, want 429", got)
	}
	if got := serve(h, http.MethodGet, "10.0.0.2:4002", "203.0.113.9"); got != http.StatusOK {
		t.Errorf("client B = %d, want 200 (own bucket, same proxy)", got)
	}
}

// A client that is not a trusted proxy must not be able to pick its own bucket
// by sending X-Forwarded-For.
func TestRateLimiter_IgnoresForwardedHeaderFromUntrustedPeer(t *testing.T) {
	rl, err := NewRateLimiter(1, 1).WithTrustedProxies([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	h := rl.Middleware(okHandler())

	if got := serve(h, http.MethodGet, "198.51.100.7:1", "1.1.1.1"); got != http.StatusOK {
		t.Fatalf("first request = %d, want 200", got)
	}
	if got := serve(h, http.MethodGet, "198.51.100.7:2", "2.2.2.2"); got != http.StatusTooManyRequests {
		t.Errorf("second request with a different spoofed header = %d, want 429 (keyed on the real peer)", got)
	}
}

// The right-most untrusted entry wins, so a client cannot prepend a fake
// address to a header the proxy appended to.
func TestRateLimiter_RightmostUntrustedForwardedAddressWins(t *testing.T) {
	rl, err := NewRateLimiter(1, 1).WithTrustedProxies([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	h := rl.Middleware(okHandler())

	if got := serve(h, http.MethodGet, "10.0.0.2:1", "9.9.9.9, 203.0.113.5"); got != http.StatusOK {
		t.Fatalf("first = %d, want 200", got)
	}
	if got := serve(h, http.MethodGet, "10.0.0.2:2", "8.8.8.8, 203.0.113.5"); got != http.StatusTooManyRequests {
		t.Errorf("second = %d, want 429 (same real client 203.0.113.5, forged left entry ignored)", got)
	}
}

func TestRateLimiter_WriteLimitOnlyAffectsMutatingRequests(t *testing.T) {
	rl := NewRateLimiter(100, 100).WithWriteLimit(1, 1)
	h := rl.Middleware(okHandler())

	if got := serve(h, http.MethodPost, "1.2.3.4:1", ""); got != http.StatusOK {
		t.Fatalf("first POST = %d, want 200", got)
	}
	if got := serve(h, http.MethodPost, "1.2.3.4:2", ""); got != http.StatusTooManyRequests {
		t.Errorf("second POST = %d, want 429 (write bucket exhausted)", got)
	}
	for i := 0; i < 20; i++ {
		if got := serve(h, http.MethodGet, "1.2.3.4:3", ""); got != http.StatusOK {
			t.Fatalf("GET %d = %d, want 200 (reads are not write-limited)", i, got)
		}
	}
}

func TestRateLimiter_EvictsIdleClients(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	rl := NewRateLimiter(100, 100)
	rl.now = func() time.Time { return now }
	h := rl.Middleware(okHandler())

	for i := 0; i < 50; i++ {
		serve(h, http.MethodGet, fmt.Sprintf("192.0.2.%d:1", i), "")
	}
	if rl.size() != 50 {
		t.Fatalf("size = %d, want 50", rl.size())
	}

	now = now.Add(idleTTL + 2*sweepInterval)
	serve(h, http.MethodGet, "198.51.100.1:1", "")
	if rl.size() != 1 {
		t.Errorf("size after idle sweep = %d, want 1 (only the active client)", rl.size())
	}
}

func TestRateLimiter_RejectsBadTrustedProxyCIDR(t *testing.T) {
	if _, err := NewRateLimiter(1, 1).WithTrustedProxies([]string{"not-a-cidr"}); err == nil {
		t.Error("WithTrustedProxies(not-a-cidr) error = nil, want an error")
	}
}
