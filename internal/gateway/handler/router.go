package handler

import (
	"io/fs"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/Muditjain13/transport-app/internal/gateway/middleware"
	"github.com/Muditjain13/transport-app/internal/gateway/web"
)

func NewRouter(search *SearchHandler, booking *BookingHandler, seats *SeatsHandler, waitlist *WaitlistHandler, rateLimiter *middleware.RateLimiter, metrics *middleware.HTTPMetrics, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	// /healthz is gateway's own liveness/readiness signal for k8s (see
	// deploy/k8s/gateway-deployment.yaml) - gateway is HTTP-facing, not
	// gRPC, so it can't use the grpc health service every other service
	// registers (pkg/middleware.RegisterHealth).
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("GET /v1/search", search.Search)
	mux.HandleFunc("GET /v1/trips/{tripId}/seats", seats.ListAvailableSeats)
	mux.Handle("POST /v1/bookings", middleware.Auth(http.HandlerFunc(booking.CreateBooking)))
	mux.Handle("POST /v1/bookings/train", middleware.Auth(http.HandlerFunc(booking.CreateTrainBooking)))
	mux.Handle("POST /v1/bookings/flight", middleware.Auth(http.HandlerFunc(booking.CreateFlightBooking)))
	mux.Handle("GET /v1/bookings/{id}", middleware.Auth(http.HandlerFunc(booking.GetBooking)))
	mux.Handle("POST /v1/bookings/{id}/cancel", middleware.Auth(http.HandlerFunc(booking.CancelBooking)))
	mux.Handle("GET /v1/waitlist", middleware.Auth(http.HandlerFunc(waitlist.ListMyWaitlist)))
	mux.Handle("POST /v1/waitlist/{id}/claim", middleware.Auth(http.HandlerFunc(booking.ClaimWaitlistEntry)))

	staticFS, err := fs.Sub(web.Static, "static")
	if err != nil {
		// web.Static is compiled in via go:embed; a missing "static"
		// subdirectory is a build-time packaging bug, not a runtime
		// condition any caller can recover from.
		panic(err)
	}
	fileServer := http.FileServerFS(staticFS)
	mux.Handle("GET /static/", http.StripPrefix("/static/", fileServer))

	// Serve index.html's bytes directly rather than routing through
	// fileServer with a rewritten path: http.FileServer specifically
	// redirects any request path ending in "/index.html" back to "/",
	// which would turn this into an infinite redirect loop with the
	// handler below.
	indexHTML, err := fs.ReadFile(staticFS, "index.html")
	if err != nil {
		panic(err)
	}
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})

	var h http.Handler = mux
	h = rateLimiter.Middleware(h)
	h = metrics.Middleware(h)
	h = middleware.Logging(logger)(h)
	h = otelhttp.NewHandler(h, "gateway")
	return h
}
