package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

// Capture the actual records produced by public HTTP. After rotation, replay
// every old public cache-write operation and prove it cannot overwrite the
// newly authorized metadata or object reference, including after a PG reopen.
func privateCacheVersionWitness(t *testing.T, format repository.Format, store GatewayStore, repo repository.HostedRepository, fixture privateNativeAssets) func(GatewayStore, repository.HostedRepository) {
	t.Helper()
	ctx := t.Context()
	assertVersion := func(version string) {
		t.Helper()
		if version != repo.Version {
			t.Fatalf("cache source version=%q want=%q", version, repo.Version)
		}
	}
	unchanged := func(before, after any, err error) {
		t.Helper()
		if !errors.Is(err, repository.ErrVersionConflict) {
			t.Fatalf("late write error=%v, want configuration version conflict", err)
		}
		left, _ := json.Marshal(before)
		right, _ := json.Marshal(after)
		if string(left) != string(right) {
			t.Fatal("late cache write changed current authorization metadata")
		}
	}
	switch format {
	case repository.FormatNPM:
		cache := store.(repository.NativeNPMStore)
		old, err := cache.GetNPMPackage(ctx, repo.ID, "private-demo")
		if err != nil {
			t.Fatal(err)
		}
		assertVersion(old.SourceVersion)
		return func(next GatewayStore, current repository.HostedRepository) {
			cache := next.(repository.NativeNPMStore)
			before, err := cache.GetNPMPackage(ctx, repo.ID, old.Name)
			if err != nil || before.SourceVersion != current.Version {
				t.Fatalf("current npm binding=%q: %v", before.SourceVersion, err)
			}
			_, writeErr := cache.SyncNPMProxyPackage(ctx, old)
			after, _ := cache.GetNPMPackage(ctx, repo.ID, old.Name)
			unchanged(before, after, writeErr)
			writeErr = cache.StoreNPMProxyNegative(ctx, old)
			after, _ = cache.GetNPMPackage(ctx, repo.ID, old.Name)
			unchanged(before, after, writeErr)
			oldTarball := old.Versions[0]
			oldTarball.SourceVersion = old.SourceVersion
			_, writeErr = cache.CacheNPMProxyTarball(ctx, oldTarball)
			after, _ = cache.GetNPMPackage(ctx, repo.ID, old.Name)
			unchanged(before, after, writeErr)
			// An identical digest on an already-cached row still requires the
			// exact metadata snapshot that authorized its original download.
			for _, field := range []string{"integrity", "shasum", "tarball-url", "tarball-name"} {
				stale := before.Versions[0]
				stale.SourceVersion = current.Version
				switch field {
				case "integrity":
					stale.Integrity += "stale"
				case "shasum":
					stale.Shasum += "stale"
				case "tarball-url":
					stale.UpstreamTarball += "?stale"
				case "tarball-name":
					stale.TarballName += ".stale"
				}
				_, writeErr = cache.CacheNPMProxyTarball(ctx, stale)
				if !errors.Is(writeErr, repository.ErrUpstreamChanged) {
					t.Fatalf("stale cached tarball %s metadata accepted: %v", field, writeErr)
				}
				after, _ = cache.GetNPMPackage(ctx, repo.ID, old.Name)
				left, _ := json.Marshal(before)
				right, _ := json.Marshal(after)
				if string(left) != string(right) {
					t.Fatal("stale cached tarball metadata changed the current package")
				}
			}
			old.SourceVersion = ""
			_, writeErr = cache.SyncNPMProxyPackage(ctx, old)
			after, _ = cache.GetNPMPackage(ctx, repo.ID, old.Name)
			unchanged(before, after, writeErr)
			oldTarball.SourceVersion = ""
			_, writeErr = cache.CacheNPMProxyTarball(ctx, oldTarball)
			after, _ = cache.GetNPMPackage(ctx, repo.ID, old.Name)
			unchanged(before, after, writeErr)
		}
	case repository.FormatPyPI:
		cache := store.(repository.NativePyPIStore)
		filename := strings.TrimPrefix(fixture.object, "/packages/")
		old, err := cache.GetPyPIFile(ctx, repo.ID, filename)
		if err != nil {
			t.Fatal(err)
		}
		assertVersion(old.SourceVersion)
		return func(next GatewayStore, current repository.HostedRepository) {
			cache := next.(repository.NativePyPIStore)
			before, err := cache.GetPyPIFile(ctx, repo.ID, filename)
			if err != nil || before.SourceVersion != current.Version {
				t.Fatalf("current PyPI binding=%q: %v", before.SourceVersion, err)
			}
			writeErr := cache.SyncPyPIProxyFiles(ctx, repo.ID, old.Project, []repository.PyPIFile{old})
			after, _ := cache.GetPyPIFile(ctx, repo.ID, filename)
			unchanged(before, after, writeErr)
			_, writeErr = cache.CachePyPIProxyFile(ctx, old)
			after, _ = cache.GetPyPIFile(ctx, repo.ID, filename)
			unchanged(before, after, writeErr)
			writeErr = cache.SyncPyPIProxyFiles(ctx, repo.ID, old.Project, []repository.PyPIFile{before, old})
			after, _ = cache.GetPyPIFile(ctx, repo.ID, filename)
			unchanged(before, after, writeErr)
			old.SourceVersion = ""
			writeErr = cache.SyncPyPIProxyFiles(ctx, repo.ID, old.Project, []repository.PyPIFile{old})
			after, _ = cache.GetPyPIFile(ctx, repo.ID, filename)
			unchanged(before, after, writeErr)
		}
	case repository.FormatCargo:
		cache := store.(repository.CargoProxyStore)
		config, err := cache.GetCargoProxyConfig(ctx, repo.ID)
		if err != nil {
			t.Fatal(err)
		}
		index, err := cache.GetCargoProxyIndex(ctx, repo.ID, "demo")
		if err != nil {
			t.Fatal(err)
		}
		crate, err := cache.GetCargoProxyCrate(ctx, repo.ID, "demo", "1.0.0")
		if err != nil {
			t.Fatal(err)
		}
		assertVersion(config.SourceVersion)
		assertVersion(index.SourceVersion)
		assertVersion(crate.SourceVersion)
		return func(next GatewayStore, current repository.HostedRepository) {
			cache := next.(repository.CargoProxyStore)
			before, err := cache.GetCargoProxyIndex(ctx, repo.ID, "demo")
			if err != nil || before.SourceVersion != current.Version {
				t.Fatalf("current Cargo binding=%q: %v", before.SourceVersion, err)
			}
			writeErr := cache.PutCargoProxyIndex(ctx, index)
			after, _ := cache.GetCargoProxyIndex(ctx, repo.ID, "demo")
			unchanged(before, after, writeErr)
			beforeConfig, _ := cache.GetCargoProxyConfig(ctx, repo.ID)
			writeErr = cache.PutCargoProxyConfig(ctx, config)
			afterConfig, _ := cache.GetCargoProxyConfig(ctx, repo.ID)
			unchanged(beforeConfig, afterConfig, writeErr)
			beforeCrate, _ := cache.GetCargoProxyCrate(ctx, repo.ID, "demo", "1.0.0")
			writeErr = cache.PutCargoProxyCrate(ctx, crate)
			afterCrate, _ := cache.GetCargoProxyCrate(ctx, repo.ID, "demo", "1.0.0")
			unchanged(beforeCrate, afterCrate, writeErr)
			config.SourceVersion, index.SourceVersion, crate.SourceVersion = "", "", ""
			writeErr = cache.PutCargoProxyConfig(ctx, config)
			afterConfig, _ = cache.GetCargoProxyConfig(ctx, repo.ID)
			unchanged(beforeConfig, afterConfig, writeErr)
			writeErr = cache.PutCargoProxyIndex(ctx, index)
			after, _ = cache.GetCargoProxyIndex(ctx, repo.ID, "demo")
			unchanged(before, after, writeErr)
			writeErr = cache.PutCargoProxyCrate(ctx, crate)
			afterCrate, _ = cache.GetCargoProxyCrate(ctx, repo.ID, "demo", "1.0.0")
			unchanged(beforeCrate, afterCrate, writeErr)
		}
	case repository.FormatAPT:
		cache := store.(repository.NativeAPTStore)
		old, err := cache.GetAPTAsset(ctx, repo.ID, strings.TrimPrefix(fixture.object, "/"))
		if err != nil {
			t.Fatal(err)
		}
		assertVersion(old.SourceVersion)
		return func(next GatewayStore, current repository.HostedRepository) {
			cache := next.(repository.NativeAPTStore)
			before, err := cache.GetAPTAsset(ctx, repo.ID, old.Path)
			if err != nil || before.SourceVersion != current.Version {
				t.Fatalf("current APT binding=%q: %v", before.SourceVersion, err)
			}
			_, writeErr := cache.CacheAPTAsset(ctx, old)
			after, _ := cache.GetAPTAsset(ctx, repo.ID, old.Path)
			unchanged(before, after, writeErr)
			old.SourceVersion = ""
			_, writeErr = cache.CacheAPTAsset(ctx, old)
			after, _ = cache.GetAPTAsset(ctx, repo.ID, old.Path)
			unchanged(before, after, writeErr)
		}
	default:
		t.Fatalf("unsupported cache witness %s", format)
		return nil
	}
}
