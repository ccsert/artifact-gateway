package app

import (
	"context"
	"errors"
	"sync"

	"github.com/artifact-gateway/artifact-gateway/internal/operationalog"
)

type rawFailureContextKey struct{}

// The access event is emitted after the response. Forwarded request clones
// share only this bounded diagnostic state, never an upstream error or URL.
type rawFailureState struct {
	mu   sync.Mutex
	code string
}

func (s *rawFailureState) current() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.code
}

func recordRawFailure(ctx context.Context, code string) {
	if operationalog.FailurePhase(code) == "" {
		return
	}
	if state, ok := ctx.Value(rawFailureContextKey{}).(*rawFailureState); ok {
		state.mu.Lock()
		state.code = code
		state.mu.Unlock()
	}
}

type rawFetchError struct {
	code string
	err  error
}

func (e *rawFetchError) Error() string { return e.err.Error() }
func (e *rawFetchError) Unwrap() error { return e.err }

func rawFetchFailureCode(err error) string {
	var failure *rawFetchError
	if errors.As(err, &failure) && operationalog.FailurePhase(failure.code) != "" {
		return failure.code
	}
	return "unknown"
}
