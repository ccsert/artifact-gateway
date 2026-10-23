package rubygems

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

func TestRubyGemsHeadConditionalAndRangeKeepAuthorizedRepresentation(t *testing.T) {
	f := setup(t)
	body := gem(t, "owned", "1.0.0", "ruby", "fixture")
	if w := push(f, body); w.Code != 201 {
		t.Fatal(w.Code)
	}
	for _, path := range []string{"versions", "info/owned", "quick/Marshal.4.8/owned-1.0.0.gemspec.rz", "gems/owned-1.0.0.gem"} {
		get := request(f.module, "GET", "/rubygems/owned-gems/"+path, nil, "owned-admin")
		head := request(f.module, "HEAD", "/rubygems/owned-gems/"+path, nil, "owned-admin")
		if get.Code != 200 || head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("ETag") != get.Header().Get("ETag") || head.Header().Get("Content-Length") != get.Header().Get("Content-Length") {
			t.Fatalf("HEAD %s %d", path, head.Code)
		}
		r := httptest.NewRequest("GET", "/rubygems/owned-gems/"+path, nil)
		r.Header.Set("Authorization", "owned-admin")
		r.Header.Set("If-None-Match", get.Header().Get("ETag"))
		w := httptest.NewRecorder()
		f.module.ServeHTTP(w, r)
		if w.Code != 304 || w.Body.Len() != 0 {
			t.Fatalf("conditional %s %d", path, w.Code)
		}
		r = httptest.NewRequest("GET", "/rubygems/owned-gems/"+path, nil)
		r.Header.Set("Authorization", "owned-admin")
		r.Header.Set("Range", "bytes=0-7")
		w = httptest.NewRecorder()
		f.module.ServeHTTP(w, r)
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), get.Body.Bytes()) {
			t.Fatalf("complete range representation %s %d", path, w.Code)
		}
	}
	audits, e := f.repos.ListAudits(t.Context(), repository.AuditQuery{Operation: "download", Limit: 100})
	if e != nil || len(audits) != 4 {
		t.Fatalf("native download audits %v %v", audits, e)
	}
	for _, a := range audits {
		if a.Status == 304 && a.Bytes != 0 || a.Status == 200 && a.Bytes != 0 && a.Bytes != int64(len(body)) {
			t.Fatalf("wrong response bytes %+v", a)
		}
	}
}
func TestRubyGemsAnonymousRequiresBothPoliciesAndExplicitInvalidKeyNeverFallsBack(t *testing.T) {
	f := setup(t)
	if w := push(f, gem(t, "owned", "1.0.0", "ruby", "fixture")); w.Code != 201 {
		t.Fatal(w.Code)
	}
	f.repo.AnonymousRead = true
	var e error
	f.repo, e = f.repos.UpdateHostedRepository(t.Context(), f.repo, "1")
	if e != nil {
		t.Fatal(e)
	}
	path := "/rubygems/owned-gems/info/owned"
	if w := request(f.module, "GET", path, nil, ""); w.Code != 401 {
		t.Fatalf("global disabled %d", w.Code)
	}
	if _, e = f.repos.ReplaceAnonymousAccessPolicy(t.Context(), repository.AnonymousAccessPolicy{Enabled: true}, "1"); e != nil {
		t.Fatal(e)
	}
	if w := request(f.module, "GET", path, nil, ""); w.Code != 200 {
		t.Fatalf("both enabled %d", w.Code)
	}
	if w := request(f.module, "GET", path, nil, "revoked-owned-key"); w.Code != 401 {
		t.Fatalf("explicit invalid key used anonymous access %d", w.Code)
	}
	if w := request(f.module, "POST", "/rubygems/owned-gems/api/v1/gems", gem(t, "another", "1.0.0", "ruby", "fixture"), ""); w.Code != 401 {
		t.Fatalf("anonymous write %d", w.Code)
	}
}
func TestRubyGemsInvalidAndOversizedUploadsCreateNoObjectsOrVisibleIndex(t *testing.T) {
	f := setup(t)
	if w := push(f, []byte("malformed archive")); w.Code != 400 {
		t.Fatalf("invalid archive %d", w.Code)
	}
	o := options(f.repos, f.store, f.objects, f.repos)
	o.MaxGemBytes = 128
	module, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	if w := request(module, "POST", "/rubygems/owned-gems/api/v1/gems", gem(t, "owned", "1.0.0", "ruby", "fixture"), "owned-admin"); w.Code != 413 {
		t.Fatalf("oversized upload %d", w.Code)
	}
	rows, e := f.store.List(t.Context(), f.repo.ID, 100)
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	keys, e := f.objects.List(t.Context(), "native/rubygems/")
	if e != nil || len(keys) != 0 {
		t.Fatal(keys, e)
	}
}
