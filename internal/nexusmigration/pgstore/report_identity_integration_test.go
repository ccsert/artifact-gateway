//go:build integration

package pgstore_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/app"
	m "github.com/ccsert/artifact-gateway/internal/nexusmigration"
)

func TestPostgresMigrationUnsupportedReportCannotCollideWithRawIdentity(t *testing.T) {
	for _, test := range []struct{ name, oversized, supported string }{
		{"colon prefix", "large.bin", "unsupported:large.bin"},
		{"relative report prefix", "large.bin", "unsupported/large.bin"},
		{"encoded Unicode", "dir with space/构建+large.bin", "unsupported:dir with space/构建+large.bin"},
		{"multiple colons", "large:build.bin", "unsupported:large:build.bin"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte("legal-small-source-bytes")
			f := newFixture(t, "raw", map[string][]byte{test.oversized: []byte("not-downloaded"), test.supported: body})
			f.remote.metadataSizes = map[string]int64{test.oversized: 1<<30 + 1}
			first := f.start()
			f.run(first)
			if !first.ScanComplete || first.EntryCount != 2 || first.Counts.Unsupported != 1 || first.Counts.Committed != 1 || first.VisibleIdentities != 1 {
				t.Fatalf("unsupported report collided with a legitimate native identity: %+v", *first)
			}
			// Unsupported work keeps the overall batch actionable while the legal
			// identity is still published; it must never be overwritten or dropped.
			requireState(t, *first, "failed")
			var supportedIdentity string
			for _, entry := range f.entries(first.ID) {
				if entry.State == "unsupported" {
					if entry.Identity != "/unsupported/"+test.oversized || entry.LastError != "asset_size_unsupported" {
						t.Fatalf("report namespace lost decoded source path or reason: %+v", entry)
					}
				} else {
					supportedIdentity = entry.Identity
				}
			}
			segments := strings.Split(test.supported, "/")
			for i := range segments {
				segments[i] = url.PathEscape(segments[i])
			}
			if supportedIdentity != strings.Join(segments, "/") || strings.HasPrefix(supportedIdentity, "/") {
				t.Fatalf("legitimate identity was renamed: %q", supportedIdentity)
			}
			h, err := app.NewGatewayHandler(app.GatewayOptions{Store: f.native, Storage: app.SharedObjectStores(f.objects), Adapter: app.TestAdapter{}, Authenticator: app.Authenticator{AdminToken: "owned-admin"}})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodGet, "/raw/"+f.repo.Name+"/"+supportedIdentity, nil)
			r.Header.Set("Authorization", "Bearer owned-admin")
			response := httptest.NewRecorder()
			h.ServeHTTP(response, r)
			if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), body) {
				t.Fatalf("legal path was not natively readable: %d %q", response.Code, response.Body.Bytes())
			}
			second := f.start()
			f.run(second)
			if second.EntryCount != 2 || second.Counts.Unsupported != 1 || second.Counts.Skipped != 1 || second.Counts.SourceDeleted != 0 || second.ManifestSHA256 != first.ManifestSHA256 {
				t.Fatalf("complete rescan duplicated/lost reports or changed the fixed manifest: %+v", *second)
			}
			f.remote.mu.Lock()
			oversizedDownloads := f.remote.gets[test.oversized]
			f.remote.mu.Unlock()
			if oversizedDownloads != 0 {
				t.Fatal("unsupported oversized asset was downloaded")
			}
			var count int
			if err = f.db.QueryRowContext(context.Background(), `SELECT count(*) FROM native_raw_assets WHERE repository_id=$1`, f.repo.ID).Scan(&count); err != nil || count != 1 {
				t.Fatalf("native publication duplicated/lost: count=%d err=%v", count, err)
			}
		})
	}
}

func TestPostgresMigrationUnsupportedReportIsSeparateFromMavenGAV(t *testing.T) {
	oversized := "org/example/large/1.0/large-1.0.jar"
	f := newFixture(t, "maven2", map[string][]byte{
		oversized:                         []byte("not-downloaded"),
		"org/example/lib/1.0/lib-1.0.pom": []byte(`<project><modelVersion>4.0.0</modelVersion><groupId>org.example</groupId><artifactId>lib</artifactId><version>1.0</version></project>`),
		"org/example/lib/1.0/lib-1.0.jar": []byte("owned-legal-release"),
	})
	f.remote.metadataSizes = map[string]int64{oversized: 1<<30 + 1}
	b := f.start()
	f.run(b)
	if !b.ScanComplete || b.EntryCount != 2 || b.Counts.Unsupported != 1 || b.Counts.Committed != 1 {
		t.Fatalf("Maven report damaged the usable release: %+v", *b)
	}
	for _, entry := range f.entries(b.ID) {
		if entry.State == "unsupported" {
			if entry.Identity != "/unsupported/"+oversized {
				t.Fatalf("Maven report lost its source path: %+v", entry)
			}
			continue
		}
		read, err := f.publication.Readback(context.Background(), entry)
		if entry.Identity != "org.example:lib:1.0" || read != m.Identical || err != nil {
			t.Fatalf("native GAV identity/complete bytes changed: identity=%s read=%s err=%v", entry.Identity, read, err)
		}
	}
}

func TestPostgresMigrationDuplicateUnsupportedMetadataIsRejectedBeforeSeal(t *testing.T) {
	f := newFixture(t, "raw", map[string][]byte{"large.bin": []byte("not-downloaded")})
	f.remote.metadataSizes = map[string]int64{"large.bin": 1<<30 + 1}
	f.remote.duplicateMetadata = true
	b := f.start()
	f.run(b)
	if b.ScanComplete || b.EntryCount != 0 || b.TotalBytes != nil || len(f.entries(b.ID)) != 0 || b.LastError != "source_duplicate_asset" {
		t.Fatalf("duplicate metadata was silently dropped or sealed: %+v", *b)
	}
}
