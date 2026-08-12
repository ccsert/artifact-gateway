package refscanner

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestTrivyEngineUsesFixedFilesystemAndConvertCommands(t *testing.T) {
	executor := &fakeCommandExecutor{}
	engine, err := NewTrivyEngine(TrivyOptions{Binary: "/usr/local/bin/trivy", Executor: executor, MaxOutputBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	executor.run = func(_ context.Context, binary string, args ...string) ([]byte, error) {
		if binary != "/usr/local/bin/trivy" {
			t.Fatalf("binary=%q", binary)
		}
		executor.calls = append(executor.calls, append([]string{}, args...))
		content := []byte(`{"SchemaVersion":2,"Results":[]}`)
		if len(executor.calls) == 2 {
			content = []byte(`{"bomFormat":"CycloneDX"}`)
		}
		return content, nil
	}
	output, err := engine.Scan(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if string(output.Report) != `{"SchemaVersion":2,"Results":[]}` || string(output.SBOM) != `{"bomFormat":"CycloneDX"}` {
		t.Fatalf("output=%#v", output)
	}
	if len(executor.calls) != 2 {
		t.Fatalf("calls=%#v", executor.calls)
	}
	wantFilesystemPrefix := []string{"filesystem", "--quiet", "--no-progress", "--scanners", "vuln,license", "--format", "json", "--"}
	if !reflect.DeepEqual(executor.calls[0][:len(wantFilesystemPrefix)], wantFilesystemPrefix) || executor.calls[0][len(executor.calls[0])-1] != root {
		t.Fatalf("filesystem args=%#v", executor.calls[0])
	}
	if got := executor.calls[1][0:5]; !reflect.DeepEqual(got, []string{"convert", "--quiet", "--format", "cyclonedx", "--"}) {
		t.Fatalf("convert args=%#v", executor.calls[1])
	}
}

func TestTrivyEngineParsesVersionAndDatabaseHealth(t *testing.T) {
	updatedAt := time.Date(2026, 8, 12, 1, 0, 0, 0, time.UTC)
	executor := &fakeCommandExecutor{output: []byte(`{"Version":"0.70.0","VulnerabilityDB":{"Version":2,"UpdatedAt":"2026-08-12T01:00:00Z"}}`)}
	engine, err := NewTrivyEngine(TrivyOptions{Binary: "trivy", Executor: executor, MaxOutputBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	health, err := engine.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.Version != "0.70.0" || health.DatabaseVersion != "2" || health.DatabaseUpdatedAt != updatedAt {
		t.Fatalf("health=%#v", health)
	}
	if len(executor.calls) != 1 || !reflect.DeepEqual(executor.calls[0], []string{"version", "--format", "json"}) {
		t.Fatalf("calls=%#v", executor.calls)
	}
}

type fakeCommandExecutor struct {
	run    func(context.Context, string, ...string) ([]byte, error)
	output []byte
	calls  [][]string
}

func (e *fakeCommandExecutor) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	if e.run != nil {
		return e.run(ctx, binary, args...)
	}
	e.calls = append(e.calls, append([]string{}, args...))
	return append([]byte{}, e.output...), nil
}
