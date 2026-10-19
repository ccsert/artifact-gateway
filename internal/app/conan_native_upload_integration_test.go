//go:build integration

package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresRustFSConanNativeClientRestartAndColdInstall(t *testing.T) {
	databaseURL, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_RUSTFS_ENDPOINT")
	access, secret := os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || access == "" || secret == "" || os.Getenv("CONAN_BINARY") == "" {
		if os.Getenv("CONAN_PERSISTENCE_REQUIRED") == "1" {
			t.Fatal("pinned Conan client, PostgreSQL and S3 environment are required")
		}
		t.Skip("pinned Conan client, PostgreSQL and S3 environment are required")
	}
	bucket := "conan-native-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	var current *repository.PostgresStore
	open := func() conanClientBackend {
		t.Helper()
		store, err := repository.NewPostgresStore(databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		current = store
		t.Cleanup(func() { _ = store.Close() })
		objects, err := objectstore.NewRustFSStore(endpoint, access, secret, bucket)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err = objects.EnsureBucket(ctx); err != nil {
			t.Fatal(err)
		}
		return conanClientBackend{store, store, objects}
	}
	reopen := func() conanClientBackend {
		t.Helper()
		_ = current.Close()
		project, file := os.Getenv("TEST_CONAN_COMPOSE_PROJECT"), os.Getenv("TEST_CONAN_COMPOSE_FILE")
		override := os.Getenv("TEST_CONAN_COMPOSE_OVERRIDE")
		if project != "" || os.Getenv("CONAN_PERSISTENCE_REQUIRED") == "1" {
			if !strings.HasPrefix(project, "artifact-gateway-conan-") || file == "" || !filepath.IsAbs(file) || filepath.Base(file) != "compose.integration.yml" || !filepath.IsAbs(override) || filepath.Base(override) != "compose.conan.yml" {
				t.Fatal("refuse to restart an unowned Conan fixture")
			}

			states := map[string]string{}
			for _, service := range []string{"postgres", "rustfs"} {
				states[service] = conanOwnedServiceState(t, project, file, override, service)
			}
			for _, args := range [][]string{{"restart", "postgres", "rustfs"}, {"up", "-d", "--wait", "--wait-timeout", "45", "postgres", "rustfs"}, {"run", "--rm", "--no-deps", "rustfs-ready"}} {
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				command := exec.CommandContext(ctx, "docker", append([]string{"compose", "--env-file", "/dev/null", "--project-name", project, "-f", file, "-f", override}, args...)...)
				output, err := command.CombinedOutput()
				cancel()
				if err != nil {
					t.Fatalf("restart owned Conan dependencies: %v\n%s", err, output)
				}
			}
			for _, service := range []string{"postgres", "rustfs"} {
				after := conanOwnedServiceState(t, project, file, override, service)
				beforeParts, afterParts := strings.Fields(states[service]), strings.Fields(after)
				if len(beforeParts) != 2 || len(afterParts) != 2 || beforeParts[0] != afterParts[0] || beforeParts[1] == afterParts[1] {
					t.Fatalf("owned %s was not restarted in place: before=%q after=%q", service, states[service], after)
				}
				t.Logf("owned %s restart confirmed: container=%s StartedAt=%s -> %s; pinned endpoints preserved", service, afterParts[0], beforeParts[1], afterParts[1])
			}

		}
		return open()
	}
	exerciseConanNativeClient(t, open(), reopen)
}

func conanOwnedServiceState(t *testing.T, project, file, override, service string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", "compose", "--env-file", "/dev/null", "--project-name", project, "-f", file, "-f", override, "ps", "-q", service)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect owned Conan service: %v %s", err, output)
	}
	id := strings.TrimSpace(string(output))
	if len(id) != 64 || strings.ContainsAny(id, " \r\n") {
		t.Fatal("owned Conan service identity is incomplete")
	}
	output, err = exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.Id}} {{.State.StartedAt}}", id).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect owned Conan restart: %v %s", err, output)
	}
	return strings.TrimSpace(string(output))
}
