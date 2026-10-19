// Package format defines explicit format modules and capability-based dispatch.
package format

import (
	"fmt"
	"net/http"
	"reflect"
	"sort"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

// Module is the identity of a format-owned implementation. Capabilities are
// separate small interfaces; a module never needs no-op implementations.
type Module interface{ Format() repository.Format }

// Route is a protocol route installed by the composition root.
type Route struct {
	Pattern string
	Handler http.Handler
}
type NativeRoutes interface{ Routes() []Route }

// Registry is immutable after construction and safe for concurrent lookup.
// Modules are explicitly supplied; there is no global mutable registration.
type Registry struct{ modules map[repository.Format]Module }

func New(modules ...Module) (*Registry, error) {
	registry := &Registry{modules: make(map[repository.Format]Module, len(modules))}
	for _, module := range modules {
		if module == nil || isNil(module) {
			return nil, fmt.Errorf("format module is nil")
		}
		name := module.Format()
		if name == "" {
			return nil, fmt.Errorf("format module has no format")
		}
		if _, exists := registry.modules[name]; exists {
			return nil, fmt.Errorf("format module %q is registered twice", name)
		}
		registry.modules[name] = module
	}
	return registry, nil
}

func isNil(value any) bool {
	switch reflect.ValueOf(value).Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflect.ValueOf(value).IsNil()
	default:
		return false
	}
}

func (r *Registry) Lookup(name repository.Format) (Module, bool) {
	if r == nil {
		return nil, false
	}
	module, ok := r.modules[name]
	return module, ok
}

// Capability returns the optional capability of a registered module.
func Capability[T any](r *Registry, name repository.Format) (T, bool) {
	var zero T
	module, ok := r.Lookup(name)
	if !ok {
		return zero, false
	}
	capability, ok := module.(T)
	return capability, ok
}

func (r *Registry) Formats() []repository.Format {
	if r == nil {
		return nil
	}
	formats := make([]repository.Format, 0, len(r.modules))
	for name := range r.modules {
		formats = append(formats, name)
	}
	sort.Slice(formats, func(i, j int) bool { return formats[i] < formats[j] })
	return formats
}

func (r *Registry) Mount(mux interface{ Handle(string, http.Handler) }) error {
	if mux == nil || isNil(mux) {
		return fmt.Errorf("format route mux is nil")
	}
	routes := []Route{}
	patterns := make(map[string]bool)
	for _, name := range r.Formats() {
		native, ok := Capability[NativeRoutes](r, name)
		if !ok {
			continue
		}
		for _, route := range native.Routes() {
			if route.Pattern == "" || route.Handler == nil || isNil(route.Handler) {
				return fmt.Errorf("format module %q has an invalid route", name)
			}
			if patterns[route.Pattern] {
				return fmt.Errorf("format route %q is registered twice", route.Pattern)
			}
			patterns[route.Pattern] = true
			routes = append(routes, route)
		}
	}
	// Validate all patterns on a private mux first, so a bad module cannot leave
	// half of its routes mounted in the caller's mux.
	if err := validateRoutes(routes); err != nil {
		return err
	}
	for _, route := range routes {
		mux.Handle(route.Pattern, route.Handler)
	}
	return nil
}

func validateRoutes(routes []Route) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("invalid format routes: %v", value)
		}
	}()
	mux := http.NewServeMux()
	for _, route := range routes {
		mux.Handle(route.Pattern, route.Handler)
	}
	return nil
}
