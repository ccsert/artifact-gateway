//go:build integration

package pgstore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/app"
	m "github.com/ccsert/artifact-gateway/internal/nexusmigration"
)

func TestPostgresMigrationPublicHTTPContractCASIdempotencyPauseResumeCancel(t *testing.T) {
	f := newFixture(t, "raw", map[string][]byte{"http.bin": []byte("http-content")})
	h, err := app.NewGatewayHandler(app.GatewayOptions{Dependencies: app.Dependencies{MigrationService: f.service, SourceService: f.source}, Store: f.native, Storage: app.SharedObjectStores(f.objects), Adapter: app.TestAdapter{}, Authenticator: app.Authenticator{AdminToken: "owned-admin", AdminActor: "synthetic-admin", ResolverToken: "owned-resolver"}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	call := func(method, path string, body any, version, key, token string) (int, []byte, http.Header) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		r, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if version != "" {
			r.Header.Set("If-Match", version)
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", f.sourceID+"-"+key)
		}
		resp, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Header.Get("Cache-Control") != "no-store" && resp.StatusCode < 400 {
			t.Fatal("management response was cacheable")
		}
		return resp.StatusCode, data, resp.Header
	}
	root := "/api/v2/migrations/nexus/batches"
	for _, auth := range []struct {
		token  string
		status int
	}{{"", 401}, {"owned-resolver", 403}} {
		status, _, _ := call("GET", "/api/v2/migrations/nexus/capabilities", nil, "", "", auth.token)
		if status != auth.status {
			t.Fatalf("authorization status=%d want=%d", status, auth.status)
		}
	}
	status, body, _ := call("GET", "/api/v2/migrations/nexus/capabilities", nil, "", "", "owned-admin")
	if status != 200 || !strings.Contains(string(body), `"capacityEvidenceSource":"operator-reported"`) {
		t.Fatalf("capabilities=%d %s", status, body)
	}
	status, body, headers := call("POST", root, f.input, "", "start-key", "owned-admin")
	if status != 202 || !strings.Contains(string(body), `"totalBytes":null`) {
		t.Fatalf("start=%d %s", status, body)
	}
	var b m.Batch
	if err = json.Unmarshal(body, &b); err != nil {
		t.Fatal(err)
	}
	if headers.Get("ETag") != b.Version {
		t.Fatal("missing batch ETag")
	}
	status, replay, _ := call("POST", root, f.input, "", "start-key", "owned-admin")
	if status != 202 || !bytes.Equal(body, replay) {
		t.Fatalf("start replay=%d %s", status, replay)
	}
	conflict := f.input
	conflict.SourceVersion = "2"
	status, _, _ = call("POST", root, conflict, "", "start-key", "owned-admin")
	if status != 409 {
		t.Fatalf("idempotency conflict=%d", status)
	}
	path := root + "/" + b.ID
	status, body, _ = call("POST", path+"/pause", nil, b.Version, "pause-key", "owned-admin")
	if status != 200 {
		t.Fatalf("pause=%d %s", status, body)
	}
	var paused m.Batch
	_ = json.Unmarshal(body, &paused)
	requireState(t, paused, "paused")
	status, replay, _ = call("POST", path+"/pause", nil, b.Version, "pause-key", "owned-admin")
	if status != 200 || !bytes.Equal(body, replay) {
		t.Fatalf("pause replay=%d %s", status, replay)
	}
	f.run(&paused)
	requireState(t, paused, "paused")
	if paused.ScanComplete {
		t.Fatal("paused batch started scanning")
	}
	status, _, _ = call("POST", path+"/resume", map[string]any{"capacityEvidence": f.input.CapacityEvidence}, paused.Version, "old-evidence", "owned-admin")
	if status != 422 {
		t.Fatalf("old evidence=%d", status)
	}
	evidence := f.evidence()
	status, body, _ = call("POST", path+"/resume", map[string]any{"capacityEvidence": evidence}, paused.Version, "resume-key", "owned-admin")
	if status != 200 {
		t.Fatalf("resume=%d %s", status, body)
	}
	_ = json.Unmarshal(body, &b)
	status, _, _ = call("POST", path+"/cancel", nil, paused.Version, "stale-control", "owned-admin")
	if status != 412 {
		t.Fatalf("stale CAS=%d", status)
	}
	status, body, _ = call("POST", path+"/cancel", nil, b.Version, "cancel-key", "owned-admin")
	if status != 200 {
		t.Fatalf("cancel=%d %s", status, body)
	}
	_ = json.Unmarshal(body, &b)
	requireState(t, b, "cancelled")
	f.run(&b)
	requireState(t, b, "cancelled")
	status, body, _ = call("GET", path+"/entries?limit=1", nil, "", "", "owned-admin")
	if status != 200 || !strings.Contains(string(body), `"items":[]`) {
		t.Fatalf("entries=%d %s", status, body)
	}
}
func TestPostgresMigrationPauseDrainsOneRecordedEntryAndStartsNoNext(t *testing.T) {
	f := newFixture(t, "raw", map[string][]byte{"first.bin": []byte("one"), "second.bin": []byte("two")})
	entered, release := make(chan struct{}), make(chan struct{})
	f.service = f.newService(m.Options{AfterUpload: func(context.Context, m.Entry) error { close(entered); <-release; return nil }})
	b := f.start()
	done := make(chan error, 1)
	go func() { done <- f.service.RunOnce(context.Background(), []string{"raw"}) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("entry did not start")
	}
	current, err := f.service.Get(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	paused, err := f.service.Control(context.Background(), b.ID, current.Version, "synthetic-admin", "pause-inflight", "pause", nil)
	if err != nil {
		t.Fatal(err)
	}
	requireState(t, paused, "pausing")
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	current, err = f.service.Get(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	requireState(t, current, "paused")
	if current.Counts.Committed != 1 || current.Counts.Pending != 1 || current.VisibleIdentities != 1 {
		t.Fatalf("pause did not preserve one in-flight result: %+v", current)
	}
	for _, entry := range f.entries(b.ID) {
		if entry.State == "pending" && entry.Attempts != 0 {
			t.Fatal("paused worker started the next entry")
		}
	}
}

func TestPostgresMigrationIncompleteSourceVerificationRetainsActualProgress(t *testing.T) {
	f := newFixture(t, "raw", map[string][]byte{"partial.bin": []byte("complete-source")})
	f.remote.forceProbe = true
	f.remote.stream = true
	f.remote.change = func(_ string, _ int, data []byte) ([]byte, int, string) { return data[:3], 200, `"fixed"` }
	b := f.start()
	f.run(b)
	requireState(t, *b, "failed")
	if b.ScanComplete || b.TotalBytes != nil || b.EntryCount != 0 || b.MetadataDiscovered != 1 || b.SourceVerificationBytes != 3 {
		t.Fatalf("incomplete scan progress was fabricated/lost: %+v", b)
	}
	keys, err := f.objects.List(context.Background(), "native/")
	if err != nil || len(keys) != 0 {
		t.Fatal("partial source verification wrote target objects")
	}
}

func TestPostgresMigrationRawCanonicalPathsAreReadableAndCompleteMetadataAvoidsExtraTransfer(t *testing.T) {
	path := "dir with space/测试+build.bin"
	body := []byte("canonical-content")
	f := newFixture(t, "raw", map[string][]byte{path: body})
	b := f.start()
	f.run(b)
	requireState(t, *b, "completed")
	entries := f.entries(b.ID)
	if len(entries) != 1 || entries[0].Identity != entries[0].Assets[0].TargetPath || entries[0].Assets[0].SourcePath != path {
		t.Fatal("source and native target addressing were conflated")
	}
	if b.SourceVerificationBytes != 0 || b.TransferredBytes != int64(len(body)) || b.VerifiedBytes != int64(len(body)) {
		t.Fatalf("extra read/counters=%+v", b)
	}
	h, err := app.NewGatewayHandler(app.GatewayOptions{Dependencies: app.Dependencies{}, Store: f.native, Storage: app.SharedObjectStores(f.objects), Adapter: app.TestAdapter{}, Authenticator: app.Authenticator{AdminToken: "owned-admin", AdminActor: "synthetic-admin"}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	r, _ := http.NewRequest("GET", server.URL+"/raw/"+f.repo.Name+"/"+entries[0].Assets[0].TargetPath, nil)
	r.Header.Set("Authorization", "Bearer owned-admin")
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	actual, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || !bytes.Equal(actual, body) {
		t.Fatalf("canonical native GET=%d %q err=%v", response.StatusCode, actual, err)
	}
}
