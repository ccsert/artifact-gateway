//go:build integration

package backupops

import (
	"context"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresNuGetPublishedBackupReferences(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	store, err := repository.NewPostgresStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	repo, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "nuget-backup-" + uuid.NewString(), Format: repository.FormatNuGet, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, version := range []string{"1.0.0", "2.0.0"} {
		s, _, err := store.ReserveNuGetPublication(t.Context(), repository.NuGetSession{ID: uuid.NewString(), RepositoryID: repo.ID, PackageID: "backup", Version: version, Digest: "sha256:" + strings.Repeat("1", 64), Size: 1, Actor: "operator", ExpiresAt: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		s.MetadataJSON = []byte(`{"id":"backup","version":"` + version + `"}`)
		s.Nuspec = []byte("original nuspec")
		if _, err = store.StageNuGetPublication(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, s.ObjectKey)
		if version == "1.0.0" {
			if _, _, err = store.CommitNuGetPublication(t.Context(), s.ID, s.Actor); err != nil {
				t.Fatal(err)
			}
		}
	}
	seen := map[string]bool{}
	if err = nugetReferenceLoopback(t, dsn).WalkReferences(t.Context(), func(key string) error { seen[key] = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !seen[keys[0]] || seen[keys[1]] {
		t.Fatalf("visible/staged backup references: %t/%t", seen[keys[0]], seen[keys[1]])
	}
}

// Operational backup connections require a literal loopback endpoint. Forward
// to the exact declared integration database, preserving its role, credentials,
// database and options when the fixture runs inside the Go test container.
func nugetReferenceLoopback(t *testing.T, dsn string) Postgres {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.Port() == "" {
		t.Fatal("NuGet reference fixture requires an explicit TEST_DATABASE_URL")
	}
	target := u.Host
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	closing := false
	track := func(conn net.Conn) bool {
		mu.Lock()
		defer mu.Unlock()
		if closing {
			_ = conn.Close()
			return false
		}
		connections[conn] = true
		return true
	}
	forget := func(conn net.Conn) {
		_ = conn.Close()
		mu.Lock()
		delete(connections, conn)
		mu.Unlock()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			incoming, err := listener.Accept()
			if err != nil {
				return
			}
			if !track(incoming) {
				return
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer forget(incoming)
				outgoing, err := (&net.Dialer{}).DialContext(ctx, "tcp", target)
				if err != nil {
					return
				}
				if !track(outgoing) {
					return
				}
				defer forget(outgoing)
				copied := make(chan struct{})
				go func() {
					defer close(copied)
					_, _ = io.Copy(incoming, outgoing)
					_ = incoming.Close()
				}()
				_, _ = io.Copy(outgoing, incoming)
				_ = outgoing.Close()
				<-copied
			}()
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		mu.Lock()
		closing = true
		for conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
		workers.Wait()
	})
	u.Host = listener.Addr().String()
	return Postgres{DSN: u.String()}
}
