package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/localcapacity"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/getkin/kin-openapi/openapi3"
)

func TestDiagnosticsCapacityPreservesAdministratorAndPasswordGates(t *testing.T) {
	var calls atomic.Int32
	o, err := localcapacity.New(map[localcapacity.Alias]string{localcapacity.Temporary: "/synthetic/private-secret"}, localcapacity.Options{Probe: func(string) (localcapacity.Filesystem, error) {
		calls.Add(1)
		return localcapacity.Filesystem{Kind: localcapacity.Supported, Key: "private-device", BlockSize: 4096, Blocks: 10, FreeBlocks: 3, AvailableBlocks: 2}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	_, err = store.CreateUser(context.Background(), repository.User{ID: "00000000-0000-0000-0000-000000000210", Name: "capacity-reset", Role: string(RoleAdmin), SecretHash: "synthetic-hash", MustChangePassword: true})
	if err != nil {
		t.Fatal(err)
	}
	auth := testAuthenticatorWithUsers(store)
	handler := NewGatewayHandler(Dependencies{LocalCapacity: o}, store, TestAdapter{}, auth)
	resetToken := auth.IssueToken("user:capacity-reset")
	for _, token := range []string{"", "reader-secret", resetToken} {
		r := capacityResponse(handler, token)
		if r.Code == http.StatusOK || strings.Contains(r.Body.String(), "localCapacity") || calls.Load() != 0 {
			t.Fatalf("capacity queried across gate: code=%d calls=%d", r.Code, calls.Load())
		}
		if token == resetToken && (r.Code != http.StatusForbidden || !strings.Contains(r.Body.String(), "password_change_required")) {
			t.Fatalf("password gate=%d %s", r.Code, r.Body.String())
		}
	}
	r := capacityResponse(handler, "admin-secret")
	if r.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("administrator snapshot must not be stored by intermediaries")
	}
	if r.Code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("administrator response=%d calls=%d body=%s", r.Code, calls.Load(), r.Body.String())
	}
	var body struct {
		LocalCapacity struct {
			Source, Scope, Unit                                              string
			RefreshIntervalSeconds, MaxSampleAgeSeconds, TimeoutMilliseconds int
			Mounts                                                           []struct {
				Alias, Status, Reason      string
				TotalBytes, AvailableBytes *int64
				SampleAt                   *time.Time
			}
		}
	}
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	c := body.LocalCapacity
	if c.Source != "statfs" || c.Scope != "observer_mount_namespace" || c.Unit != "bytes" || c.RefreshIntervalSeconds != 15 || c.MaxSampleAgeSeconds != 60 || c.TimeoutMilliseconds != 1000 || len(c.Mounts) != 3 {
		t.Fatalf("capacity metadata=%#v", c)
	}
	m := c.Mounts[0]
	if m.Alias != "temporary" || m.Status != "available" || m.Reason != "" || m.TotalBytes == nil || *m.TotalBytes != 40960 || m.AvailableBytes == nil || *m.AvailableBytes != 8192 || m.SampleAt == nil {
		t.Fatalf("capacity sample=%#v", m)
	}
	if strings.Contains(r.Body.String(), "private-") || strings.Contains(r.Body.String(), "/synthetic") {
		t.Fatal("private path or identity escaped diagnostics")
	}
}

func TestDiagnosticsCapacityHasNoFakeZeroForUnconfiguredOrFailedObservation(t *testing.T) {
	for _, failed := range []bool{false, true} {
		deps := Dependencies{}
		if failed {
			var err error
			deps.LocalCapacity, err = localcapacity.New(map[localcapacity.Alias]string{localcapacity.Logs: "/synthetic/private-secret"}, localcapacity.Options{Probe: func(string) (localcapacity.Filesystem, error) {
				return localcapacity.Filesystem{}, errors.New("private-secret raw permission error")
			}})
			if err != nil {
				t.Fatal(err)
			}
		}
		r := capacityResponse(NewGatewayHandler(deps, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator()), "admin-secret")
		if r.Code != http.StatusOK || strings.Contains(r.Body.String(), "totalBytes") || strings.Contains(r.Body.String(), "availableBytes") || strings.Contains(r.Body.String(), "private-secret") {
			t.Fatalf("unknown capacity response=%d %s", r.Code, r.Body.String())
		}
		if failed && !strings.Contains(r.Body.String(), `"reason":"read_failed"`) {
			t.Fatal("failed read was not classified")
		}
		if !strings.Contains(r.Body.String(), `"status":"not_configured"`) {
			t.Fatal("unconfigured aliases not reported")
		}
	}
}

func TestDiagnosticsRepeatedTimeoutsReuseProcessObserverWithoutProbeGrowth(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	o, err := localcapacity.New(map[localcapacity.Alias]string{localcapacity.Temporary: "/synthetic/blocked"}, localcapacity.Options{Timeout: 10 * time.Millisecond, Probe: func(string) (localcapacity.Filesystem, error) {
		calls.Add(1)
		<-release
		return localcapacity.Filesystem{}, errors.New("synthetic read failure")
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{LocalCapacity: o}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	for range 8 {
		r := capacityResponse(handler, "admin-secret")
		if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"reason":"timeout"`) || strings.Contains(r.Body.String(), "availableBytes") {
			t.Fatalf("timeout response=%d %s", r.Code, r.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("repeated diagnostics grew blocked probes: %d", calls.Load())
	}
}

func capacityResponse(handler http.Handler, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/api/v2/diagnostics", nil)
	if token != "" {
		authorize(r, token)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestDiagnosticsLocalCapacityMatchesGeneratedOpenAPIResponse(t *testing.T) {
	spec, err := openapi3.NewLoader().LoadFromFile(filepath.Join("..", "..", "api", "openapi", "management-runtime-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, configured := range []bool{false, true} {
		deps := Dependencies{}
		if configured {
			deps.LocalCapacity, err = localcapacity.New(map[localcapacity.Alias]string{localcapacity.Temporary: "/synthetic/contract"}, localcapacity.Options{Probe: func(string) (localcapacity.Filesystem, error) {
				return localcapacity.Filesystem{Kind: localcapacity.Supported, Key: "private-identity", BlockSize: 4096, Blocks: 10, FreeBlocks: 2, AvailableBlocks: 1}, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
		}
		response := capacityResponse(NewGatewayHandler(deps, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator()), "admin-secret")
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if err := spec.Paths.Find("/diagnostics").Get.Responses.Status(200).Value.Content["application/json"].Schema.Value.VisitJSON(body); err != nil {
			t.Fatalf("diagnostics contract: %v", err)
		}
	}
}
