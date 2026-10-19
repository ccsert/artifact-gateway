package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBoundariesCannotBeHiddenBehindHelpers(t *testing.T) {
	inv := inventory{Imports: map[string]map[string]bool{
		"internal/protocol/raw": {"example.test/gateway/internal/helper": true},
		"internal/helper":       {"example.test/gateway/internal/app": true, "example.test/gateway/internal/protocol/oci": true, "example.test/gateway/internal/repository/postgres": true},
	}}
	want := []string{"internal/helper -> internal/app", "internal/helper -> internal/protocol/oci", "internal/helper -> internal/repository/postgres"}
	if got := violations(inv, "example.test/gateway"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v; want %v", got, want)
	}
}

func TestDomainImportsAndSameFormatHelpersAreAllowed(t *testing.T) {
	inv := inventory{Imports: map[string]map[string]bool{
		"internal/protocol/raw":            {"example.test/gateway/internal/repository": true, "example.test/gateway/internal/protocol/raw/coordinate": true},
		"internal/protocol/raw/coordinate": {"example.test/gateway/internal/protocol/raw": true},
	}}
	if got := violations(inv, "example.test/gateway"); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestNewStoreMembersAreRejectedEvenWhenCountDoesNotGrow(t *testing.T) {
	if got := additions([]string{"repository.RawStore", "repository.NewStore"}, []string{"repository.RawStore", "repository.OldStore"}); !reflect.DeepEqual(got, []string{"repository.NewStore"}) {
		t.Fatal(got)
	}
}

func TestGateRejectsNewDependencyAndRequiresDebtRemoval(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.test/gateway\n")
	write("internal/app/store.go", "package app\ntype GatewayStore interface { Read() error }\n")
	write("internal/protocol/raw/raw.go", "package raw\n")
	write("tools/architecture/baseline.json", `{"violations":[],"gatewayStoreMembers":["Read func() error"],"effectiveStoreMethods":["internal/app.GatewayStore.Read func() error"]}`)
	if err := run(root, false); err != nil {
		t.Fatal(err)
	}
	write("internal/protocol/raw/raw.go", "package raw\nimport _ \"example.test/gateway/internal/app\"\n")
	if err := run(root, false); err == nil || !strings.Contains(err.Error(), "internal/protocol/raw -> internal/app") {
		t.Fatalf("new dependency accepted: %v", err)
	}
	write("tools/architecture/baseline.json", `{"violations":["internal/protocol/raw -> internal/app"],"gatewayStoreMembers":["Read func() error"],"effectiveStoreMethods":["internal/app.GatewayStore.Read func() error"]}`)
	if err := run(root, false); err != nil {
		t.Fatal(err)
	}
	write("internal/protocol/raw/raw.go", "package raw\n")
	if err := run(root, false); err == nil || !strings.Contains(err.Error(), "remove resolved violation") {
		t.Fatalf("stale allowance accepted: %v", err)
	}
	write("tools/architecture/baseline.json", `{"violations":[],"gatewayStoreMembers":["Read func() error"],"effectiveStoreMethods":["internal/app.GatewayStore.Read func() error"]}`)
	write("internal/app/store.go", "package app\ntype GatewayStore interface { Read() error; Write() error }\n")
	if err := run(root, false); err == nil || !strings.Contains(err.Error(), "GatewayStore adds Write") {
		t.Fatalf("new store method accepted: %v", err)
	}
}

func TestGrandfatheredTargetDoesNotHideNewDependencyEdges(t *testing.T) {
	inv := inventory{Imports: map[string]map[string]bool{
		"internal/protocol/raw": {"example.test/gateway/internal/repository": true, "example.test/gateway/internal/helper": true, "example.test/gateway/internal/protocol/cargo": true},
		"internal/repository":   {"example.test/gateway/internal/protocol/cargo": true},
		"internal/helper":       {"example.test/gateway/internal/protocol/cargo": true},
	}}
	got := additions(violations(inv, "example.test/gateway"), []string{"internal/repository -> internal/protocol/cargo"})
	want := []string{"internal/helper -> internal/protocol/cargo", "internal/protocol/raw -> internal/protocol/cargo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v; want %v", got, want)
	}
}

func TestEmbeddedStoreExpansionIncludesNewMethods(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.test/gateway\n")
	write("internal/app/store.go", "package app\nimport repos \"example.test/gateway/internal/repository\"\ntype GatewayStore interface { repos.Store }\n")
	write("internal/repository/store.go", "package repository\ntype Reader interface { Read() error }\ntype Store = Reader\n")
	inv, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	methods, err := effectiveMembers(inv, "example.test/gateway")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(baseline{Violations: []string{}, GatewayStoreMembers: inv.Members, EffectiveStoreMethods: methods})
	if err != nil {
		t.Fatal(err)
	}
	write("tools/architecture/baseline.json", string(data))
	if err := run(root, false); err != nil {
		t.Fatal(err)
	}
	write("internal/repository/store.go", "package repository\ntype Reader interface { Read() error; Write() error }\ntype Store = Reader\n")
	if err := run(root, false); err == nil || !strings.Contains(err.Error(), "adds effective method internal/repository.Reader.Write") {
		t.Fatalf("embedded method expansion accepted: %v", err)
	}
}

func TestFutureFormatModulesCannotImportOtherFormats(t *testing.T) {
	inv := inventory{Imports: map[string]map[string]bool{
		"internal/format/raw": {"example.test/gateway/internal/protocol/raw": true, "example.test/gateway/internal/format/oci": true},
	}}
	want := []string{"internal/format/raw -> internal/format/oci"}
	if got := violations(inv, "example.test/gateway"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v; want %v", got, want)
	}
}
