package format_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	formatmodule "github.com/ccsert/artifact-gateway/internal/format"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

type module struct {
	name   repository.Format
	routes []formatmodule.Route
}

func (m *module) Format() repository.Format    { return m.name }
func (m *module) Routes() []formatmodule.Route { return m.routes }

type identityOnly struct{ name repository.Format }

func (m identityOnly) Format() repository.Format { return m.name }

func TestRegistryRejectsInvalidAndDuplicateModules(t *testing.T) {
	var absent *module
	for _, modules := range [][]formatmodule.Module{{nil}, {absent}, {&module{}}, {identityOnly{"raw"}, identityOnly{"raw"}}} {
		if _, err := formatmodule.New(modules...); err == nil {
			t.Fatalf("accepted invalid registration: %#v", modules)
		}
	}
}
func TestRegistryDispatchesOnlyRegisteredCapabilityAndReturnsIndependentOrder(t *testing.T) {
	called := 0
	registry, err := formatmodule.New(identityOnly{"zeta"}, &module{name: "raw", routes: []formatmodule.Route{{Pattern: "/raw/", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called++; w.WriteHeader(http.StatusNoContent) })}}})
	if err != nil {
		t.Fatal(err)
	}
	got := registry.Formats()
	if !reflect.DeepEqual(got, []repository.Format{"raw", "zeta"}) {
		t.Fatalf("formats=%v", got)
	}
	got[0] = "modified"
	if registry.Formats()[0] != "raw" {
		t.Fatal("mutated registry formats")
	}
	if _, ok := formatmodule.Capability[formatmodule.NativeRoutes](registry, "zeta"); ok {
		t.Fatal("invented native capability")
	}
	if _, ok := formatmodule.Capability[formatmodule.NativeRoutes](registry, "missing"); ok {
		t.Fatal("found absent module")
	}
	mux := http.NewServeMux()
	if err := registry.Mount(mux); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/raw/example/file", nil))
	if response.Code != http.StatusNoContent || called != 1 {
		t.Fatalf("status=%d calls=%d", response.Code, called)
	}
}
func TestRegistryRejectsRoutesBeforeMountingAny(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	var nilHandler *http.ServeMux
	for _, routes := range [][]formatmodule.Route{
		{{Pattern: "/valid/", Handler: handler}, {Pattern: "/bad/", Handler: nilHandler}},
		{{Pattern: "/valid/", Handler: handler}, {Pattern: "/valid/", Handler: handler}},
		{{Pattern: "/valid/", Handler: handler}, {Pattern: "/bad/{", Handler: handler}},
		{{Pattern: "", Handler: handler}},
	} {
		registry, err := formatmodule.New(&module{name: "raw", routes: routes})
		if err != nil {
			t.Fatal(err)
		}
		mux := http.NewServeMux()
		if err := registry.Mount(mux); err == nil {
			t.Fatalf("accepted invalid routes %v", routes)
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/valid/file", nil))
		if response.Code != http.StatusNotFound {
			t.Fatal("partially mounted invalid routes")
		}
	}
}
func TestRegistryZeroValueAndMissingTarget(t *testing.T) {
	var registry *formatmodule.Registry
	if _, ok := registry.Lookup("raw"); ok {
		t.Fatal("nil registry found module")
	}
	if len(registry.Formats()) != 0 {
		t.Fatal("nil registry has formats")
	}
	registry, err := formatmodule.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Mount(nil); err == nil {
		t.Fatal("nil mux accepted")
	}
}
