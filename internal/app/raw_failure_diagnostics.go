package app

import (
	"context"
	"sync"

	"github.com/ccsert/artifact-gateway/internal/operationalog"
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
