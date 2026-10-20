//go:build integration

package repository

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresNPMMetadataUpgradePreservesPublication(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	ctx := context.Background()
	admin, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}

	name := "npm_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", "") + "_test"
	if _, err = admin.db.ExecContext(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.db.ExecContext(context.Background(), `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`)
		_ = admin.Close()
	})
	address, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	address.Path = "/" + name
	legacy, err := NewPostgresStore(address.String())
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	files, err := os.ReadDir(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	var upgrades []struct{ name, sql string }
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".sql") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join("..", "..", "migrations", file.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		up := strings.Split(string(data), "-- +goose Down")[0]
		if strings.HasPrefix(file.Name(), "000149_") || len(upgrades) != 0 {
			upgrades = append(upgrades, struct{ name, sql string }{file.Name(), up})
			continue
		}
		if _, err = legacy.db.ExecContext(ctx, up); err != nil {
			t.Fatalf("pre-governance schema %s: %v", file.Name(), err)
		}
	}
	if len(upgrades) == 0 {
		t.Fatal("npm metadata migration is missing")
	}
	repo, err := legacy.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "legacy-npm", Format: FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	published := time.Date(2026, 10, 1, 1, 2, 3, 0, time.UTC)
	_, err = legacy.db.ExecContext(ctx, `INSERT INTO native_npm_packages(repository_id,name,dist_tags) VALUES($1,'legacy','{"latest":"1.0.0"}'::jsonb);
`, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.db.ExecContext(ctx, `INSERT INTO native_npm_versions(repository_id,package_name,version,digest,integrity,shasum,tarball_name,object_key,size,manifest,publisher,created_at,cached_at)
	 VALUES($1,'legacy','1.0.0',$2,'sha512-YQ==',$3,'legacy-1.0.0.tgz','native/npm/sha256/legacy',8,'{"name":"legacy","version":"1.0.0","deprecated":"legacy warning"}'::jsonb,'release-bot',$4,$4)`, repo.ID, "sha256:"+strings.Repeat("a", 64), strings.Repeat("b", 40), published)
	if err != nil {
		t.Fatal(err)
	}
	var manifest string
	if err = legacy.db.QueryRowContext(ctx, `SELECT manifest::text FROM native_npm_versions WHERE repository_id=$1`, repo.ID).Scan(&manifest); err != nil {
		t.Fatal(err)
	}
	var hasSourceVersion bool
	if err = legacy.db.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema='public' AND table_name='native_npm_packages' AND column_name='source_version'
	)`).Scan(&hasSourceVersion); err != nil {
		t.Fatal(err)
	}
	if hasSourceVersion {
		t.Fatal("pre-governance fixture already contains the modern proxy cache binding")
	}
	for _, upgrade := range upgrades {
		if _, err = legacy.db.ExecContext(ctx, upgrade.sql); err != nil {
			t.Fatalf("append-only upgrade %s: %v", upgrade.name, err)
		}
		t.Logf("applied real upgrade %s", upgrade.name)
	}
	upgraded, err := NewPostgresStore(address.String())
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	pkg, err := upgraded.GetNPMPackage(ctx, repo.ID, "legacy")
	if err != nil || pkg.Revision != 1 || len(pkg.Versions) != 1 {
		t.Fatalf("upgraded package=%#v %v", pkg, err)
	}
	version := pkg.Versions[0]
	if version.Deprecated != "legacy warning" || string(version.Manifest) != manifest || !version.CreatedAt.Equal(published) || version.Digest != "sha256:"+strings.Repeat("a", 64) || version.ObjectKey != "native/npm/sha256/legacy" || version.Size != 8 || !version.CollectingAt.IsZero() {
		t.Fatalf("upgrade changed publication identity: %#v", version)
	}
}
