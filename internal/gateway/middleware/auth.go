// Package middleware is gateway's HTTP middleware: auth, rate limiting,
// request logging. This is deliberately separate from pkg's shared gRPC
// interceptors (auth/logging/trace propagation for service-to-service
// calls) - gateway is the only service in this system that terminates
// public HTTP traffic, so its middleware concerns are its own.
package middleware

import (
	"context"
	"net/http"
	"strings"
)

type contextKey int

const userIDKey contextKey = 0

// Auth is a deliberately minimal placeholder: there is no identity
// service in this architecture (the architecture diagram has none),
// so "auth" here is limited to extracting a caller-supplied user ID from
// a bearer token and rejecting requests without one - not verifying a
// real credential. A production gateway would validate a signed token
// against an identity provider; this demonstrates the middleware seam
// that step would plug into, not a substitute for it.
func Auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		userID, ok := strings.CutPrefix(authHeader, "Bearer ")
		if !ok || userID == "" {
			http.Error(w, `{"error":"missing or invalid Authorization header"}`, http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// UserID returns the authenticated caller's user ID from context. Only
// valid on requests that passed through Auth.
func UserID(ctx context.Context) (string, bool) {
	userID, ok := ctx.Value(userIDKey).(string)
	return userID, ok
}

// ContextWithUserID sets the user ID Auth would have set, for callers
// that need an authenticated context without going through the HTTP
// middleware chain (e.g. handler tests).
func ContextWithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}
