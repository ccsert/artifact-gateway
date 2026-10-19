package raw_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	formatmodule "github.com/ccsert/artifact-gateway/internal/format"
	rawformat "github.com/ccsert/artifact-gateway/internal/format/raw"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

func fixture(t *testing.T) (*repository.MemoryStore, *rawformat.Module, *http.ServeMux) {
	t.Helper()
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "downloads", Name: "downloads", Format: repository.FormatRaw}); err != nil {
		t.Fatal(err)
	}
	auth := authorization.Authenticator{AdminToken: "fixture-admin"}
	module := rawformat.New(rawformat.Options{Store: store, Query: store, Publication: store, Jobs: store, Replications: store, Repositories: store, Groups: store, Objects: objectstore.NewMemoryStore(), Authenticator: auth, Authorizer: authorization.RepositoryAuthorizer{Grants: store, Legacy: auth}, Anonymous: store, Audit: store, ReadPolicies: store, Quarantine: store})
	registry, err := formatmodule.New(module)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	if err = registry.Mount(mux); err != nil {
		t.Fatal(err)
	}
	return store, module, mux
}
func request(handler http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer fixture-admin")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

// This is deliberately constructed without app or a GatewayStore: the module
// must own a usable publication/read/governance seam independently.
func TestRawModulePublishesReadsScansAndRestoresWithoutGateway(t *testing.T) {
	store, module, mux := fixture(t)
	const path = "/raw/downloads/releases/app.bin"
	put := request(mux, http.MethodPut, path, "artifact", nil)
	if put.Code != http.StatusCreated {
		t.Fatalf("put=%d %s", put.Code, put.Body.String())
	}
	if response := request(mux, http.MethodPut, path, "replacement", map[string]string{"If-None-Match": "*"}); response.Code != http.StatusPreconditionFailed {
		t.Fatalf("CAS=%d", response.Code)
	}
	if response := request(mux, http.MethodGet, path, "", map[string]string{"Range": "bytes=1-3"}); response.Code != http.StatusPartialContent || response.Body.String() != "rti" {
		t.Fatalf("range=%d %q", response.Code, response.Body.String())
	}
	asset, err := store.GetRawAsset(context.Background(), "downloads", "releases/app.bin")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("artifact"))
	wantDigest := "sha256:" + hex.EncodeToString(sum[:])
	if asset.Digest != wantDigest || asset.Size != 8 {
		t.Fatalf("published=%#v", asset)
	}
	scanned, err := module.ResolveArtifactScan(context.Background(), "downloads", repository.ArtifactScanPayload{Format: repository.FormatRaw, Coordinate: asset.Path, Digest: wantDigest})
	if err != nil || len(scanned.Assets) != 1 {
		t.Fatalf("scan=%#v %v", scanned, err)
	}
	reader, err := scanned.Assets[0].Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil || closeErr != nil || string(body) != "artifact" {
		t.Fatalf("scan bytes=%q %v %v", body, err, closeErr)
	}
	if _, err := module.ResolveArtifactScan(context.Background(), "downloads", repository.ArtifactScanPayload{Format: repository.FormatRaw, Coordinate: asset.Path, Digest: "stale"}); err == nil {
		t.Fatal("accepted changed scan identity")
	}
	if _, err := module.ResolveArtifactScan(context.Background(), "downloads", repository.ArtifactScanPayload{Format: repository.FormatOCI, Coordinate: asset.Path, Digest: wantDigest}); !errors.Is(err, repository.ErrDisabled) {
		t.Fatalf("wrong scan format=%v", err)
	}
	checksum := request(mux, http.MethodGet, path+".sha256", "", nil)
	if checksum.Code != http.StatusOK || checksum.Body.String() != hex.EncodeToString(sum[:])+"\n" {
		t.Fatalf("checksum=%d %q", checksum.Code, checksum.Body.String())
	}
	if err := module.Tombstone(context.Background(), "downloads", asset.Path); err != nil {
		t.Fatal(err)
	}
	if response := request(mux, http.MethodGet, path, "", nil); response.Code != http.StatusNotFound {
		t.Fatalf("deleted read=%d", response.Code)
	}
	if err := module.Restore(context.Background(), "downloads", asset.Path); err != nil {
		t.Fatal(err)
	}
	if response := request(mux, http.MethodGet, path, "", nil); response.Code != http.StatusOK || response.Body.String() != "artifact" {
		t.Fatalf("restored=%d %q", response.Code, response.Body.String())
	}
}

func TestRawModuleResumableUploadIsInvisibleUntilGuardedCompletion(t *testing.T) {
	_, _, mux := fixture(t)
	const path = "/raw/downloads/release.bin"
	started := request(mux, http.MethodPost, path+"?resumable=1", "", nil)
	if started.Code != http.StatusCreated {
		t.Fatalf("start=%d %s", started.Code, started.Body.String())
	}
	location := started.Header().Get("Location")
	if response := request(mux, http.MethodPatch, location, "part", map[string]string{"Upload-Offset": "1"}); response.Code != http.StatusConflict {
		t.Fatalf("wrong offset=%d", response.Code)
	}
	if response := request(mux, http.MethodPatch, location, "artifact", map[string]string{"Upload-Offset": "0"}); response.Code != http.StatusNoContent {
		t.Fatalf("append=%d", response.Code)
	}
	if response := request(mux, http.MethodGet, path, "", nil); response.Code != http.StatusNotFound {
		t.Fatalf("premature visibility=%d", response.Code)
	}
	sum := sha256.Sum256([]byte("artifact"))
	digest := "sha-256=" + base64.StdEncoding.EncodeToString(sum[:])
	completed := request(mux, http.MethodPut, location+"&complete=1", "", map[string]string{"Digest": digest, "If-None-Match": "*"})
	if completed.Code != http.StatusCreated {
		t.Fatalf("complete=%d %s", completed.Code, completed.Body.String())
	}
	if response := request(mux, http.MethodGet, path, "", nil); response.Code != http.StatusOK || response.Body.String() != "artifact" {
		t.Fatalf("read=%d %q", response.Code, response.Body.String())
	}
}

func TestRawModuleRetentionAndDistributionShareNativeIdentity(t *testing.T) {
	store, module, mux := fixture(t)
	for _, path := range []string{"releases/old.bin", "protected/old.bin"} {
		if response := request(mux, http.MethodPut, "/raw/downloads/"+path, "artifact", nil); response.Code != http.StatusCreated {
			t.Fatalf("publish=%d", response.Code)
		}
	}
	policy := repository.RepositoryRetentionPolicy{KeepDays: 7, CoordinatePatterns: []string{"^releases/"}, ProtectedPatterns: []string{"protected"}}
	candidates, err := module.PlanRetention(context.Background(), "downloads", policy, time.Now().Add(10*24*time.Hour))
	if err != nil || len(candidates) != 1 || candidates[0].Coordinate != "releases/old.bin" || candidates[0].VersionType != "asset" || candidates[0].AgeDays < 9 {
		t.Fatalf("retention=%#v err=%v", candidates, err)
	}
	if _, err := module.PlanRetention(context.Background(), "downloads", repository.RepositoryRetentionPolicy{CoordinatePatterns: []string{"["}}, time.Now()); err == nil {
		t.Fatal("accepted invalid retention regex")
	}
	asset, err := store.GetRawAsset(context.Background(), "downloads", "releases/old.bin")
	if err != nil {
		t.Fatal(err)
	}
	checkpoints, err := module.ReplicationCheckpoints(context.Background(), "downloads", asset.Path, asset.Digest)
	if err != nil || len(checkpoints) != 1 || checkpoints[0].ObjectKey != asset.ObjectKey || checkpoints[0].Digest != asset.Digest || checkpoints[0].Size != 8 {
		t.Fatalf("checkpoints=%#v %v", checkpoints, err)
	}
	if _, err := module.ReplicationCheckpoints(context.Background(), "downloads", asset.Path, "changed"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("changed identity=%v", err)
	}
	job, replayed, err := module.EnqueuePromotion(context.Background(), "downloads", "target", asset.Path, asset.Digest, "promote-key")
	if err != nil || replayed || job.Kind != repository.LifecycleJobPromotion || !bytes.Contains(job.Payload, []byte(`"path":"releases/old.bin"`)) {
		t.Fatalf("promotion=%#v replayed=%v err=%v", job, replayed, err)
	}
	replay, replayed, err := module.EnqueuePromotion(context.Background(), "downloads", "target", asset.Path, asset.Digest, "promote-key")
	if err != nil || !replayed || replay.ID != job.ID {
		t.Fatalf("promotion replay=%#v %v %v", replay, replayed, err)
	}
}

func TestRawModuleBrowseAndCapabilitiesUseCanonicalPaths(t *testing.T) {
	_, module, mux := fixture(t)
	if response := request(mux, http.MethodPut, "/raw/downloads/releases/two%20words.txt", "artifact", nil); response.Code != http.StatusCreated {
		t.Fatalf("publish=%d", response.Code)
	}
	query := module.SearchQuery(repository.ArtifactSearchQuery{Mode: repository.ArtifactSearchByCoordinate, Value: "releases/two words"})
	if query.Value != "releases/two%20words" {
		t.Fatalf("canonical query=%#v", query)
	}
	items, err := module.Search(context.Background(), "downloads", query, 2, "")
	if err != nil || len(items) != 1 || items[0].Coordinate != "releases/two%20words.txt" || items[0].Size != 8 {
		t.Fatalf("search=%#v %v", items, err)
	}
	if module.BrowseName("two%20words.txt") != "two words.txt" || !module.ValidBrowseParent(repository.ArtifactBrowseParent{Kind: repository.BrowseNodeDirectory, Path: "releases/"}) {
		t.Fatal("invalid browse projection")
	}
	if module.ValidPrefix("releases/../escape") || module.ValidCoordinate("") || module.ValidKeyword("danger\u202e", repository.ArtifactSearchContains) {
		t.Fatal("accepted unsafe coordinate")
	}
	if module.Profile().Format != repository.FormatRaw || module.UsageKey("releases/file") != "releases/file" {
		t.Fatal("incorrect profile/usage identity")
	}
}

func TestRawModuleMissingPortsNeverCreateStorage(t *testing.T) {
	module := rawformat.New(rawformat.Options{})
	response := httptest.NewRecorder()
	module.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/raw/downloads/file", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing native ports=%d", response.Code)
	}
	if _, err := module.ResolveArtifactScan(context.Background(), "id", repository.ArtifactScanPayload{Format: repository.FormatRaw}); err == nil {
		t.Fatal("invented scan store")
	}
	if _, err := module.PlanRetention(context.Background(), "id", repository.RepositoryRetentionPolicy{}, time.Now()); !errors.Is(err, repository.ErrDisabled) {
		t.Fatalf("invented retention=%v", err)
	}
	if err := module.Restore(context.Background(), "id", "file"); !errors.Is(err, repository.ErrDisabled) {
		t.Fatalf("invented restore=%v", err)
	}
	if _, _, err := module.EnqueuePromotion(context.Background(), "source", "target", "file", "digest", "key"); !errors.Is(err, repository.ErrDisabled) {
		t.Fatalf("invented promotion=%v", err)
	}
	// Supplying native publication storage does not implicitly grant query access.
	module = rawformat.New(rawformat.Options{Store: repository.NewMemoryStore()})
	if _, err := module.Search(context.Background(), "id", repository.ArtifactSearchQuery{}, 1, ""); !errors.Is(err, repository.ErrDisabled) {
		t.Fatalf("invented query capability=%v", err)
	}
}

func TestRawModuleImmutablePublicationKeepsFailedUploadInvisibleAndPreservesTarget(t *testing.T) {
	_, module, _ := fixture(t)
	sum := sha256.Sum256([]byte("artifact"))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	asset := formatmodule.NativeAsset{RepositoryID: "downloads", Path: "release.bin", Digest: digest, ObjectKey: "native/raw/sha256/" + hex.EncodeToString(sum[:]), Size: 8}
	if err := module.ValidatePublication(asset.Path, []formatmodule.NativeAsset{asset}); err != nil {
		t.Fatal(err)
	}
	if err := module.ValidatePublication("release.bin", []formatmodule.NativeAsset{asset, asset}); !errors.Is(err, formatmodule.ErrPublicationIdentity) {
		t.Fatalf("duplicate publication=%v", err)
	}
	invalid := asset
	invalid.Path = "../escape"
	if err := module.ValidatePublication(invalid.Path, []formatmodule.NativeAsset{invalid}); !errors.Is(err, formatmodule.ErrPublicationPath) {
		t.Fatalf("unsafe publication=%v", err)
	}
	ctx, release, err := module.LockPublication(context.Background(), []formatmodule.NativeAsset{asset})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := module.StageVerifiedPublication(ctx, asset, failingPublicationReader{}); !errors.Is(err, formatmodule.ErrPublicationUpload) {
		t.Fatalf("failed upload=%v", err)
	}
	if _, err := module.ReadPublication(ctx, asset.RepositoryID, asset.Path); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("failed bytes became visible: %v", err)
	}
	if err := module.StageVerifiedPublication(ctx, asset, strings.NewReader("artifact")); err != nil {
		t.Fatal(err)
	}
	if err := module.PublishPublication(ctx, asset); err != nil {
		t.Fatal(err)
	}
	if err := module.PublishPublication(ctx, asset); !errors.Is(err, repository.ErrPreconditionFailed) {
		t.Fatalf("create-only replay=%v", err)
	}
	read, err := module.ReadPublication(ctx, asset.RepositoryID, asset.Path)
	if err != nil || read != asset {
		t.Fatalf("native readback=%#v %v", read, err)
	}
}

type failingPublicationReader struct{}

func (failingPublicationReader) Read([]byte) (int, error) {
	return 0, errors.New("synthetic upload interruption")
}
