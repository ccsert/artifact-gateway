//go:build integration

package repository

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresCargoProxyCacheAcrossConnections(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	first, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPostgresStore(databaseURL)
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	ctx := context.Background()
	repo, err := first.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "cargo-proxy-" + uuid.NewString()[:8],
		Format: FormatCargo, Type: RepositoryTypeProxy, Endpoint: "https://index.crates.io"})
	if err != nil {
		_ = first.Close()
		_ = second.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = first.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, repo.ID)
		_ = first.Close()
		_ = second.Close()
	})
	now := time.Now().UTC()
	config := CargoProxyConfig{RepositoryID: repo.ID, DownloadTemplate: "https://static.crates.io/crates", FetchedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := first.PutCargoProxyConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	if saved, err := second.GetCargoProxyConfig(ctx, repo.ID); err != nil || saved.DownloadTemplate != config.DownloadTemplate {
		t.Fatalf("cross-connection config=%+v err=%v", saved, err)
	}
	checksum := strings.Repeat("a", 64)
	row := `{"name":"demo","vers":"1.0.0","cksum":"` + checksum + `","yanked":false}` + "\n"
	index := CargoProxyIndex{RepositoryID: repo.ID, Name: "demo", Body: []byte(row), Status: 200, FetchedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := first.PutCargoProxyIndex(ctx, index); err != nil {
		t.Fatal(err)
	}
	if saved, err := second.GetCargoProxyIndex(ctx, repo.ID, "demo"); err != nil || !bytes.Equal(saved.Body, index.Body) {
		t.Fatalf("cross-connection index=%s err=%v", saved.Body, err)
	}
	if listed, err := second.SearchCargoProxyCrates(ctx, repo.ID, "dem", 10, ""); err != nil || len(listed) != 1 || listed[0].Name != "demo" || listed[0].MaxVersion != "1.0.0" {
		t.Fatalf("cross-connection proxy browse=%+v err=%v", listed, err)
	}
	crate := CargoProxyCrate{RepositoryID: repo.ID, Name: "demo", Version: "1.0.0", Checksum: checksum,
		ObjectKey: "native/cargo-proxy/sha256/" + checksum, Size: 23, CachedAt: now}
	if err := first.PutCargoProxyCrate(ctx, crate); err != nil {
		t.Fatal(err)
	}
	if saved, err := second.GetCargoProxyCrate(ctx, repo.ID, "demo", "1.0.0"); err != nil || saved.Checksum != checksum {
		t.Fatalf("cross-connection crate=%+v err=%v", saved, err)
	}
	index.Body = []byte(strings.Replace(row, checksum, strings.Repeat("b", 64), 1))
	if err := second.PutCargoProxyIndex(ctx, index); !errors.Is(err, ErrUpstreamChanged) {
		t.Fatalf("index rewrite: %v", err)
	}
	index.Body = []byte(strings.Replace(row, `"yanked":false`, `"yanked":true`, 1))
	if err := second.PutCargoProxyIndex(ctx, index); err != nil {
		t.Fatalf("yank: %v", err)
	}
	index.Status, index.Body = 404, nil
	if err := first.PutCargoProxyIndex(ctx, index); !errors.Is(err, ErrUpstreamChanged) {
		t.Fatalf("index disappearance: %v", err)
	}
	if saved, err := first.GetCargoProxyIndex(ctx, repo.ID, "demo"); err != nil || saved.Status != 200 {
		t.Fatalf("failed transaction changed index=%+v err=%v", saved, err)
	}
}
