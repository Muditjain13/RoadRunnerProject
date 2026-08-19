package clients

import (
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
)

// grpcToAppError maps a gRPC status error back to booking's sentinel
// errors, so the saga's errors.Is(err, apperrors.ErrConflict) /
// ErrUnavailable checks work the same whether the call was in-process or
// across a gRPC boundary.
func grpcToAppError(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	switch st.Code() {
	case codes.NotFound:
		return fmt.Errorf("%s: %w", st.Message(), apperrors.ErrNotFound)
	case codes.FailedPrecondition:
		return fmt.Errorf("%s: %w", st.Message(), apperrors.ErrConflict)
	case codes.Unavailable:
		return fmt.Errorf("%s: %w", st.Message(), apperrors.ErrUnavailable)
	default:
		return err
	}
}
