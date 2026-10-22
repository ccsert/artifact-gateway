package helm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	helmprotocol "github.com/ccsert/artifact-gateway/internal/protocol/helm"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type fixture struct {
	module  *Module
	store   *repository.MemoryStore
	objects *objectstore.MemoryStore
	repo    repository.HostedRepository
}

func moduleOptions(store *repository.MemoryStore, objects objectstore.Store) Options {
	auth := authorization.Authenticator{AdminToken: "helm-owned-admin", AdminActor: "operator", ResolverToken: "helm-owned-reader", ResolverActor: "reader", RepositoryReaders: map[string][]string{"reader": {"helm-test"}}}
	return Options{Store: store, Repositories: store, Objects: objects, Authenticator: auth, Authorizer: authorization.RepositoryAuthorizer{Grants: store, Legacy: auth}, Audit: store, Anonymous: store, Capacities: store}
}
func newFixture(t *testing.T) fixture {
	t.Helper()
	store := repository.NewMemoryStore()
	objects := objectstore.NewMemoryStore()
	repo, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "helm-test", Format: repository.FormatHelm, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	module, err := New(moduleOptions(store, objects))
	if err != nil {
		t.Fatal(err)
	}
	return fixture{module, store, objects, repo}
}
func chartFixture(t *testing.T, name, version, description string) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for _, entry := range []struct{ name, body string }{{name + "/Chart.yaml", "apiVersion: v2\nname: " + name + "\nversion: " + version + "\ndescription: " + description + "\n"}, {name + "/templates/owned.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}\ndata:\n  owned: fixture\n"}} {
		if err := tw.WriteHeader(&tar.Header{Name: entry.name, Size: int64(len(entry.body)), Mode: 0600, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func provenanceFixture(t *testing.T, chart []byte) []byte {
	t.Helper()
	in, err := helmprotocol.Inspect(bytes.NewReader(chart))
	if err != nil {
		t.Fatal(err)
	}
	key, err := openpgp.NewEntity("Owned Helm", "", "fixture@example.invalid", &packet.Config{RSABits: 2048})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w, err := clearsign.Encode(&out, key.PrivateKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.WriteString(w, string(in.ChartYAML)+"\n...\nfiles:\n  "+helmprotocol.Filename(in.Metadata.Name, in.Metadata.Version)+": "+digestFor(chart)+"\n")
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func digestFor(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func multipartChart(t *testing.T, name, version string, chart, prov []byte) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, entry := range []struct {
		field, filename string
		data            []byte
	}{{"chart", helmprotocol.Filename(name, version), chart}, {"provenance", helmprotocol.Filename(name, version) + ".prov", prov}} {
		if entry.data == nil {
			continue
		}
		part, err := writer.CreateFormFile(entry.field, entry.filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), writer.FormDataContentType()
}
func request(t *testing.T, h http.Handler, method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func upload(t *testing.T, f fixture, name, version string, chart, prov []byte) *httptest.ResponseRecorder {
	body, content := multipartChart(t, name, version, chart, prov)
	return request(t, f.module.Preview(), "PUT", "/api/v2/previews/helm/repositories/"+f.repo.ID+"/charts/"+name+"/"+version, body, map[string]string{"Authorization": "Bearer helm-owned-admin", "Content-Type": content})
}

var admin = map[string]string{"Authorization": "Bearer helm-owned-admin"}

func TestHelmHostedPublishesImmutableChartProvenanceAndDeterministicIndex(t *testing.T) {
	f := newFixture(t)
	chart := chartFixture(t, "owned", "1.2.3", "complete")
	prov := provenanceFixture(t, chart)
	for range 2 {
		w := upload(t, f, "owned", "1.2.3", chart, prov)
		if w.Code != 201 {
			t.Fatalf("publish=%d %s", w.Code, w.Body.String())
		}
	}
	indexPath := "/helm/helm-test/index.yaml"
	index := request(t, f.module, "GET", indexPath, nil, admin)
	if index.Code != 200 || !strings.Contains(index.Body.String(), "digest: "+strings.TrimPrefix(digestFor(chart), "sha256:")) || !strings.Contains(index.Body.String(), "charts/owned/1.2.3/owned-1.2.3.tgz") {
		t.Fatalf("index=%d %s", index.Code, index.Body.String())
	}
	if repeated := request(t, f.module, "GET", indexPath, nil, admin); !bytes.Equal(repeated.Body.Bytes(), index.Body.Bytes()) || repeated.Header().Get("ETag") != index.Header().Get("ETag") {
		t.Fatal("unchanged index changed bytes")
	}
	if conditional := request(t, f.module, "GET", indexPath, nil, map[string]string{"Authorization": admin["Authorization"], "If-None-Match": index.Header().Get("ETag")}); conditional.Code != 304 || conditional.Body.Len() != 0 {
		t.Fatalf("conditional index=%d", conditional.Code)
	}
	base := "/helm/helm-test/charts/owned/1.2.3/owned-1.2.3.tgz"
	for _, asset := range []struct {
		path string
		body []byte
	}{{base, chart}, {base + ".prov", prov}} {
		w := request(t, f.module, "GET", asset.path, nil, map[string]string{"Authorization": "Basic cmVhZGVyOmhlbG0tb3duZWQtcmVhZGVy"})
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), asset.body) || w.Header().Get("ETag") != `"`+digestFor(asset.body)+`"` {
			t.Fatalf("asset=%d %s", w.Code, w.Body.String())
		}
		head := request(t, f.module, "HEAD", asset.path, nil, admin)
		if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != fmt.Sprint(len(asset.body)) {
			t.Fatalf("head=%d %v", head.Code, head.Header())
		}
		ranged := request(t, f.module, "GET", asset.path, nil, map[string]string{"Authorization": admin["Authorization"], "Range": "bytes=0-3"})
		if ranged.Code != 206 || !bytes.Equal(ranged.Body.Bytes(), asset.body[:4]) {
			t.Fatalf("range=%d", ranged.Code)
		}
	}
	for _, path := range []string{indexPath, base, base + ".prov"} {
		if w := request(t, f.module, "GET", path, nil, nil); w.Code != 401 {
			t.Fatalf("unauthorized %s=%d", path, w.Code)
		}
	}
	if w := upload(t, f, "owned", "1.2.3", chart, nil); w.Code != 409 {
		t.Fatalf("removed provenance replay=%d", w.Code)
	}
	if w := upload(t, f, "owned", "1.2.3", chartFixture(t, "owned", "1.2.3", "changed"), nil); w.Code != 409 {
		t.Fatalf("chart rewrite=%d", w.Code)
	}
	if w := upload(t, f, "other", "1.2.3", chart, nil); w.Code != 400 {
		t.Fatalf("identity mismatch=%d", w.Code)
	}
	if w := upload(t, f, "owned", "1.2.4", []byte("malformed"), nil); w.Code != 400 {
		t.Fatalf("malformed=%d", w.Code)
	}
	capacity, err := f.store.GetRepositoryCapacity(t.Context(), f.repo.ID)
	if err != nil || capacity.UsedBytes != int64(len(chart)+len(prov)) || capacity.ObjectCount != 2 {
		t.Fatalf("capacity=%+v %v", capacity, err)
	}
	audits, err := f.store.ListAudits(t.Context(), repository.AuditQuery{Repository: f.repo.Name, Operation: "helm.publish", Limit: 100})
	if err != nil || len(audits) != 1 {
		t.Fatalf("audit count=%d %v", len(audits), err)
	}
}
func TestHelmIndexFiltersChartScopedGrantsAndWrites(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"allowed", "hidden"} {
		if w := upload(t, f, name, "1.0.0", chartFixture(t, name, "1.0.0", name), nil); w.Code != 201 {
			t.Fatal(w.Body.String())
		}
	}
	if _, err := f.store.ReplaceRepositoryGrants(t.Context(), f.repo.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}, ResourcePrefix: "allowed"}}, "1"); err != nil {
		t.Fatal(err)
	}
	reader := map[string]string{"Authorization": "Bearer helm-owned-reader"}
	w := request(t, f.module, "GET", "/helm/helm-test/index.yaml", nil, reader)
	if w.Code != 200 || strings.Contains(w.Body.String(), "hidden") || !strings.Contains(w.Body.String(), "allowed") {
		t.Fatalf("filtered index=%d %s", w.Code, w.Body.String())
	}
	for _, name := range []string{"allowed", "hidden"} {
		want := 200
		if name == "hidden" {
			want = 403
		}
		path := "/helm/helm-test/charts/" + name + "/1.0.0/" + name + "-1.0.0.tgz"
		if w = request(t, f.module, "GET", path, nil, reader); w.Code != want {
			t.Fatalf("chart %s=%d", name, w.Code)
		}
	}
	body, content := multipartChart(t, "allowed", "2.0.0", chartFixture(t, "allowed", "2.0.0", "denied write"), nil)
	w = request(t, f.module.Preview(), "PUT", "/api/v2/previews/helm/repositories/"+f.repo.ID+"/charts/allowed/2.0.0", body, map[string]string{"Authorization": reader["Authorization"], "Content-Type": content})
	if w.Code != 403 {
		t.Fatalf("reader wrote=%d", w.Code)
	}
}

type interruptedObjects struct {
	objectstore.Store
	fail atomic.Bool
}

func (s *interruptedObjects) PutVerifiedReader(ctx context.Context, key string, reader io.Reader, size int64, digest string) error {
	if strings.Contains(key, "/provenance/") && s.fail.CompareAndSwap(true, false) {
		return errors.New("owned interruption")
	}
	return s.Store.PutVerifiedReader(ctx, key, reader, size, digest)
}
func TestHelmInterruptedClosureResumesAfterRecompositionAndNeverIndexesPending(t *testing.T) {
	f := newFixture(t)
	wrapped := &interruptedObjects{Store: f.objects}
	wrapped.fail.Store(true)
	m, err := New(moduleOptions(f.store, wrapped))
	if err != nil {
		t.Fatal(err)
	}
	f.module = m
	chart := chartFixture(t, "restart", "1.0.0", "restart")
	prov := provenanceFixture(t, chart)
	if w := upload(t, f, "restart", "1.0.0", chart, prov); w.Code != 503 {
		t.Fatalf("interruption=%d", w.Code)
	}
	if w := request(t, f.module, "GET", "/helm/helm-test/index.yaml", nil, admin); w.Code != 200 || strings.Contains(w.Body.String(), "restart:") {
		t.Fatalf("partial index=%d %s", w.Code, w.Body.String())
	}
	f.module, err = New(moduleOptions(f.store, f.objects))
	if err != nil {
		t.Fatal(err)
	}
	if w := upload(t, f, "restart", "1.0.0", chart, prov); w.Code != 201 {
		t.Fatalf("resume=%d %s", w.Code, w.Body.String())
	}
	p, err := f.store.GetHelmPublication(t.Context(), f.repo.ID, "restart", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.objects.Put(t.Context(), p.ObjectKey, []byte("corrupt")); err != nil {
		t.Fatal(err)
	}
	if w := request(t, f.module, "GET", assetPath(f.repo, p, false), nil, admin); w.Code != 503 || bytes.Contains(w.Body.Bytes(), []byte("corrupt")) {
		t.Fatalf("corrupt read=%d %s", w.Code, w.Body.String())
	}
}
func TestHelmConcurrentPublicationsKeepIndexAndObjectIdentity(t *testing.T) {
	f := newFixture(t)
	var wg sync.WaitGroup
	errs := make(chan string, 12)
	for i := range 12 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			version := fmt.Sprintf("1.0.%d", n)
			w := upload(t, f, "parallel", version, chartFixture(t, "parallel", version, "parallel"), nil)
			if w.Code != 201 {
				errs <- w.Body.String()
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var index struct {
		Entries map[string][]struct {
			Version, Digest string
			URLs            []string
		}
	}
	w := request(t, f.module, "GET", "/helm/helm-test/index.yaml", nil, admin)
	if w.Code != 200 || helmprotocol.DecodeYAML(w.Body.Bytes(), &index) != nil || len(index.Entries["parallel"]) != 12 {
		t.Fatalf("concurrent index=%d %s", w.Code, w.Body.String())
	}
	for _, entry := range index.Entries["parallel"] {
		download := request(t, f.module, "GET", "/helm/helm-test/"+entry.URLs[0], nil, admin)
		if download.Code != 200 || digestFor(download.Body.Bytes()) != "sha256:"+entry.Digest {
			t.Fatalf("index and bytes disagree %s", entry.Version)
		}
	}
}
func TestHelmQuotaCoversWholeClosureAndExpiredObjectCollection(t *testing.T) {
	f := newFixture(t)
	chart := chartFixture(t, "quota", "1.0.0", "quota")
	if _, err := f.store.ReplaceRepositoryCapacityQuota(t.Context(), f.repo.ID, int64(len(chart)-1)); err != nil {
		t.Fatal(err)
	}
	if w := upload(t, f, "quota", "1.0.0", chart, nil); w.Code != 409 {
		t.Fatalf("quota bypass=%d", w.Code)
	}
	if _, err := f.store.ReplaceRepositoryCapacityQuota(t.Context(), f.repo.ID, 0); err != nil {
		t.Fatal(err)
	}
	inspection, err := helmprotocol.Inspect(bytes.NewReader(chart))
	if err != nil {
		t.Fatal(err)
	}
	in, _, err := f.store.ReserveHelmPublication(t.Context(), repository.HelmSession{ID: uuid.NewString(), RepositoryID: f.repo.ID, Name: "quota", Version: "1.0.0", Digest: digestFor(chart), Size: int64(len(chart)), Actor: "operator", MetadataJSON: inspection.MetadataJSON, ChartYAML: inspection.ChartYAML, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.objects.Put(t.Context(), in.ObjectKey, chart); err != nil {
		t.Fatal(err)
	}
	maintenance, err := NewMaintenance(f.store, f.objects)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = maintenance.Collect(t.Context(), time.Now().Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.objects.Stat(t.Context(), in.ObjectKey); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("expired upload retained: %v", err)
	}
	if w := upload(t, f, "quota", "1.0.0", chart, nil); w.Code != 201 {
		t.Fatalf("replacement=%d %s", w.Code, w.Body.String())
	}
}
func TestHelmPreviewManagementRejectsUnknownInputAndNonAdminCreation(t *testing.T) {
	f := newFixture(t)
	prefix := "/api/v2/previews/helm/repositories"
	for _, body := range []string{`{"name":"valid","anonymousRead":true}`, `{"name":"UPPER"}`, `{"name":"valid"} {}`} {
		if w := request(t, f.module.Preview(), "POST", prefix, []byte(body), admin); w.Code != 400 {
			t.Fatalf("invalid create=%d %s", w.Code, w.Body.String())
		}
	}
	if w := request(t, f.module.Preview(), "POST", prefix, []byte(`{"name":"valid"}`), map[string]string{"Authorization": "Bearer helm-owned-reader"}); w.Code != 403 {
		t.Fatalf("nonadmin created=%d", w.Code)
	}
	headers := map[string]string{"Authorization": admin["Authorization"], "Idempotency-Key": "owned-create"}
	var prior string
	for range 2 {
		w := request(t, f.module.Preview(), "POST", prefix, []byte(`{"name":"preview-created"}`), headers)
		if w.Code != 201 {
			t.Fatalf("create=%d %s", w.Code, w.Body.String())
		}
		var out struct {
			ID      string
			Preview bool
		}
		if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.ID == "" || !out.Preview {
			t.Fatal("invalid create DTO")
		}
		if prior != "" && prior != out.ID {
			t.Fatal("create replay changed identity")
		}
		prior = out.ID
	}
}
