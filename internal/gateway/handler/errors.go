// Package handler is gateway's HTTP transport layer: JSON in and out,
// translating to and from gRPC calls into search and booking. No business
// logic lives here.
package handler

import (
	"encoding/json"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func writeJSON(w http.ResponseWriter, statusCode int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	writeJSON(w, grpcCodeToHTTPStatus(statusCodeOf(err)), map[string]string{"error": err.Error()})
}

func statusCodeOf(err error) codes.Code {
	if st, ok := status.FromError(err); ok {
		return st.Code()
	}
	return codes.Internal
}

func grpcCodeToHTTPStatus(c codes.Code) int {
	switch c {
	case codes.OK:
		return http.StatusOK
	case codes.InvalidArgument:
		return http.StatusBadRequest
	case codes.NotFound:
		return http.StatusNotFound
	case codes.FailedPrecondition, codes.AlreadyExists:
		return http.StatusConflict
	case codes.Unavailable:
		return http.StatusServiceUnavailable
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}
