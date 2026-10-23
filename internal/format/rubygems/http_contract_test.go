package rubygems

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
	gemfixture "github.com/ccsert/artifact-gateway/internal/testsupport/rubygemsfixture"
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

func TestRubyGemsInvalidSpecificationIsRejectedBeforePublication(t *testing.T) {
	for _, tail := range []string{"date: definitely-not-a-date\n", "date: ''\n", "date: '2026-13-01'\n", "require_paths: lib\n", "extra_tail: !ruby/object:Gem::Version\n  version: [bad]\n", "extra_tail: !ruby/object:Gem::Specification\n  date: definitely-not-a-date\n", "extra_tail: !ruby/object:Gem::Requirement\n  invalid-key: value\n"} {
		t.Run(strings.TrimSpace(tail), func(t *testing.T) {
			f := setup(t)
			yaml := append(gemfixture.YAML("owned", "1.0.0", "ruby", "fixture", gemfixture.Dependency("leaf", "runtime")), tail...)
			if w := push(f, gemfixture.Build(t, yaml, "lib/owned.rb")); w.Code != 400 {
				t.Fatalf("invalid native specification published: %d %s", w.Code, w.Body.String())
			}
			for _, path := range []string{"names", "versions", "info/owned", "specs.4.8.gz", "latest_specs.4.8.gz", "prerelease_specs.4.8.gz", "quick/Marshal.4.8/owned-1.0.0.gemspec.rz", "gems/owned-1.0.0.gem"} {
				w := request(f.module, "GET", "/rubygems/owned-gems/"+path, nil, "owned-admin")
				if strings.HasPrefix(path, "quick/") || strings.HasPrefix(path, "gems/") {
					if w.Code != 404 {
						t.Fatalf("invalid specification discoverable at %s: %d", path, w.Code)
					}
				} else {
					body := w.Body.Bytes()
					if strings.HasSuffix(path, ".gz") {
						reader, err := gzip.NewReader(bytes.NewReader(body))
						if err != nil {
							t.Fatal(err)
						}
						body, err = io.ReadAll(reader)
						_ = reader.Close()
						if err != nil {
							t.Fatal(err)
						}
					}
					if w.Code != 200 || bytes.Contains(body, []byte("owned")) || strings.HasPrefix(path, "info/") && string(body) != "---\n" {
						t.Fatalf("invalid specification indexed at %s: %d %q", path, w.Code, body)
					}
				}
			}
			rows, err := f.store.List(t.Context(), f.repo.ID, 100)
			if err != nil || len(rows) != 0 {
				t.Fatalf("invalid specification visible: %v %v", rows, err)
			}
			keys, err := f.objects.List(t.Context(), "native/rubygems/")
			if err != nil || len(keys) != 0 {
				t.Fatalf("invalid specification wrote objects: %v %v", keys, err)
			}
			audits, err := f.repos.ListAudits(t.Context(), repository.AuditQuery{Operation: "rubygems.publish", Limit: 100})
			if err != nil || len(audits) != 0 {
				t.Fatalf("invalid specification success audit: %v %v", audits, err)
			}
		})
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
