package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func npmMigrationInventory(t *testing.T, repo repository.HostedRepository, name string) (nexusmigration.Entry, map[string][]byte) {
	t.Helper()
	entry := nexusmigration.Entry{ID: uuid.NewString(), BatchID: uuid.NewString(), SourceRepository: "source", TargetRepositoryID: repo.ID, Format: "npm", Identity: name, State: "pending", Actor: "synthetic-admin"}
	versions := map[string]any{}
	bodies := map[string][]byte{}
	for i, version := range []string{"1.0.0", "2.0.0-beta.1"} {
		dependencies := map[string]string{}
		if name == "widget" {
			dependencies["@scope/widget"] = version
		}
		body := npmMigrationTarball(t, name, version, dependencies)
		filename := []string{"archive-without-version.tgz", "different-history.tgz"}[i]
		source := name + "/-/" + filename
		bodies[source] = body
		h1, h256, h512 := sha1.Sum(body), sha256.Sum256(body), sha512.Sum512(body)
		deprecated := ""
		if i == 0 {
			deprecated = "use next"
		}
		versions[version] = map[string]any{"name": name, "version": version, "dependencies": dependencies, "description": "original producer metadata", "deprecated": deprecated, "dist": map[string]string{"tarball": "https://example.com/repository/source/" + source, "shasum": hex.EncodeToString(h1[:]), "integrity": "sha512-" + base64.StdEncoding.EncodeToString(h512[:])}}
		entry.Assets = append(entry.Assets, nexusmigration.Asset{SourcePath: source, TargetPath: version, Size: int64(len(body)), SHA256: hex.EncodeToString(h256[:]), SHA1: hex.EncodeToString(h1[:]), SHA512: hex.EncodeToString(h512[:])})
	}
	raw, _ := json.Marshal(map[string]any{"name": name, "dist-tags": map[string]string{"latest": "1.0.0", "next": "2.0.0-beta.1"}, "versions": versions})
	sum := sha256.Sum256(raw)
	bodies[name] = raw
	entry.Assets = append([]nexusmigration.Asset{{SourcePath: name, TargetPath: nexusmigration.NPMPackumentPath, Size: int64(len(raw)), SHA256: hex.EncodeToString(sum[:]), NPMPackument: raw}}, entry.Assets...)
	return entry, bodies
}
func stageNPMMigration(t *testing.T, ctx context.Context, p *NexusNPMMigrationPublication, e *nexusmigration.Entry, bodies map[string][]byte) {
	t.Helper()
	var err error
	e.SessionID, err = p.Begin(ctx, *e)
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range e.Assets {
		if err = p.Stage(ctx, *e, a, bytes.NewReader(bodies[a.SourcePath])); err != nil {
			t.Fatal(err)
		}
		e.Assets[i].Uploaded = true
	}
	if err = p.Verify(ctx, *e); err != nil {
		t.Fatal(err)
	}
}
func newNPMMigrationMemory(t *testing.T) (*repository.MemoryStore, *objectstore.MemoryStore, repository.HostedRepository, *NexusNPMMigrationPublication) {
	t.Helper()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "npm-migration", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	objects := objectstore.NewMemoryStore()
	return store, objects, repo, NewNexusNPMMigrationPublication(store, objects)
}
func TestNexusNPMMigrationAtomicWholePackageNativeBytes(t *testing.T) {
	for _, name := range []string{"widget", "@scope/widget"} {
		t.Run(name, func(t *testing.T) {
			store, objects, repo, p := newNPMMigrationMemory(t)
			e, bodies := npmMigrationInventory(t, repo, name)
			ctx, release, err := p.Lock(context.Background(), e)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			stageNPMMigration(t, ctx, p, &e, bodies)
			if _, err = store.GetNPMPackage(ctx, repo.ID, name); !errors.Is(err, repository.ErrNotFound) {
				t.Fatal("staging leaked npm metadata")
			}
			if err = p.Publish(ctx, e); err != nil {
				t.Fatal(err)
			}
			if state, err := p.Readback(ctx, e); err != nil || state != nexusmigration.Identical {
				t.Fatalf("readback=%s %v", state, err)
			}
			handler := newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatNPM: objects}})
			for _, a := range e.Assets {
				if a.TargetPath == nexusmigration.NPMPackumentPath {
					continue
				}
				request := httptest.NewRequest(http.MethodGet, "/npm/"+repo.Name+"/"+a.SourcePath, nil)
				request.Header.Set("Authorization", "Bearer resolver-secret")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), bodies[a.SourcePath]) {
					t.Fatalf("actual native bytes %s=%d", a.SourcePath, response.Code)
				}
			}
			public, _ := json.Marshal(e)
			if bytes.Contains(public, []byte("original producer metadata")) || bytes.Contains(public, []byte("npmPackument")) {
				t.Fatal("private source packument escaped management schema")
			}
			if err = p.Publish(ctx, e); err != nil {
				t.Fatal(err)
			}
			audits, _ := store.ListAudits(ctx, repository.AuditQuery{Repository: repo.Name, Operation: "publish", Limit: 100})
			if len(audits) != 1 {
				t.Fatalf("replay duplicated audit=%d", len(audits))
			}
		})
	}
}
func TestNexusNPMMigrationRejectsMutableTagsCorruptionAndUnverifiedPublish(t *testing.T) {
	for _, mode := range []string{"no-verification", "different-tag", "wrong-bytes", "quota", "changed-checksum"} {
		t.Run(mode, func(t *testing.T) {
			store, objects, repo, p := newNPMMigrationMemory(t)
			e, bodies := npmMigrationInventory(t, repo, "widget")
			ctx, release, err := p.Lock(context.Background(), e)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if mode == "no-verification" {
				if err = p.Publish(ctx, e); err == nil {
					t.Fatal("unverified publication accepted")
				}
				return
			}
			stageNPMMigration(t, ctx, p, &e, bodies)
			if mode == "changed-checksum" {
				e.Assets[1].SHA512 = strings.Repeat("a", 128)
				if err = p.Publish(ctx, e); err == nil {
					t.Fatal("changed verified checksum facts were published")
				}
				return
			}
			if mode == "wrong-bytes" {
				a := e.Assets[1]
				if err = objects.Put(ctx, "native/npm/sha256/"+a.SHA256, bytes.Repeat([]byte{'x'}, int(a.Size))); err != nil {
					t.Fatal(err)
				}
				if err = p.Verify(ctx, e); err == nil {
					t.Fatal("same-size target corruption accepted")
				}
				return
			}
			if mode == "quota" {
				if _, err = store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 1); err != nil {
					t.Fatal(err)
				}
				if err = p.Publish(ctx, e); err == nil {
					t.Fatal("quota ignored")
				}
				if _, err = store.GetNPMPackage(ctx, repo.ID, e.Identity); !errors.Is(err, repository.ErrNotFound) {
					t.Fatal("quota failure partially published")
				}
				return
			}
			if err = p.Publish(ctx, e); err != nil {
				t.Fatal(err)
			}
			if _, err = store.SetNPMDistTag(ctx, repository.NPMDistTagMutation{RepositoryID: repo.ID, PackageName: e.Identity, Tag: "latest", Version: "2.0.0-beta.1"}); err != nil {
				t.Fatal(err)
			}
			if state, err := p.Readback(ctx, e); err != nil || state != nexusmigration.Conflict {
				t.Fatalf("mutable tag overwrite allowed=%s %v", state, err)
			}
			if err = p.Publish(ctx, e); err == nil {
				t.Fatal("existing tag was overwritten")
			}
		})
	}
}

func npmMigrationTarball(t *testing.T, name, version string, dependencies map[string]string) []byte {
	t.Helper()
	manifest, _ := json.Marshal(map[string]any{"name": name, "version": version, "dependencies": dependencies})
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tarball := tar.NewWriter(gz)
	if err := tarball.WriteHeader(&tar.Header{Name: "package/package.json", Mode: 0644, Size: int64(len(manifest))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarball.Write(manifest); err != nil {
		t.Fatal(err)
	}
	if err := tarball.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
