package app

import (
	"net/http"
	"sync"

	"github.com/artifact-gateway/artifact-gateway/internal/operationalog"
)

// runtimeLogMux records only server registrations. Request paths and mutable
// r.Pattern values never supply a template.
type runtimeLogMux struct {
	*http.ServeMux
	buffer *operationalog.Buffer
}

type runtimeLogRouteContextKey struct{}

type runtimeLogRouteState struct {
	mu      sync.Mutex
	pattern string
}

func (s *runtimeLogRouteState) set(pattern string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pattern = pattern
}

func (s *runtimeLogRouteState) current() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pattern
}

func newRuntimeLogMux(buffer *operationalog.Buffer) *runtimeLogMux {
	return &runtimeLogMux{ServeMux: http.NewServeMux(), buffer: buffer}
}

func (m *runtimeLogMux) Handle(pattern string, handler http.Handler) {
	m.ServeMux.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if state, ok := r.Context().Value(runtimeLogRouteContextKey{}).(*runtimeLogRouteState); ok {
			state.set(pattern)
		}
		handler.ServeHTTP(w, r)
	}))
	m.buffer.RegisterRouteTemplate(pattern)
}

func (m *runtimeLogMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	m.Handle(pattern, http.HandlerFunc(handler))
}
