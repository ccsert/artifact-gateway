package rubygems

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
)

func yankRequest(h http.Handler, repo, name, version, platform, token string) *httptest.ResponseRecorder {
	form := url.Values{"gem_name": {name}, "version": {version}}
	if platform != "" {
		form.Set("platform", platform)
	}
	r := httptest.NewRequest("DELETE", "/rubygems/"+repo+"/api/v1/gems/yank", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Authorization", token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func governancePath(repo, name, version, platform string) string {
	return "/api/v2/previews/rubygems/repositories/" + repo + "/packages/" + name + "/" + version + "/" + platform + "/governance"
}
func governanceChange(t *testing.T, h http.Handler, repo, name, version, platform, digest, state, action, token string, want int) governanceReceipt {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"expectedDigest": digest, "expectedGovernanceVersion": state, "action": action, "reason": "Owned lifecycle decision"})
	w := request(h, "POST", governancePath(repo, name, version, platform), body, token)
	if w.Code != want {
		t.Fatalf("governance %s expected %d got %d %s", action, want, w.Code, w.Body.String())
	}
	var out governanceReceipt
	if want == 200 && json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal("invalid governance receipt")
	}
	return out
}
func TestRubyGemsYankHidesAllReadsAndRestoresVerifiedIdentity(t *testing.T) {
	for _, version := range []string{"1.0.0", "1.1.0.pre"} {
		t.Run(version, func(t *testing.T) {
			f := setup(t)
			body := gem(t, "owned", version, "ruby", "fixture")
			if w := push(f, body); w.Code != 201 {
				t.Fatal(w.Code)
			}
			p, err := f.store.GetRetained(t.Context(), f.repo.ID, "owned", version, "ruby")
			if err != nil {
				t.Fatal(err)
			}
			paths := []string{"names", "versions", "info/owned", "specs.4.8.gz", "latest_specs.4.8.gz", "prerelease_specs.4.8.gz", "quick/Marshal.4.8/" + p.FullName() + ".gemspec.rz", "gems/" + p.FullName() + ".gem"}
			etags := map[string]string{}
			for _, path := range paths {
				w := request(f.module, "GET", "/rubygems/owned-gems/"+path, nil, "owned-admin")
				if w.Code != 200 {
					t.Fatal(path, w.Code)
				}
				etags[path] = w.Header().Get("ETag")
			}
			for range 2 {
				if w := yankRequest(f.module, f.repo.Name, "owned", version, "", "owned-admin"); w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			for _, path := range paths {
				for _, method := range []string{"GET", "HEAD"} {
					r := httptest.NewRequest(method, "/rubygems/owned-gems/"+path, nil)
					r.Header.Set("Authorization", "owned-admin")
					r.Header.Set("If-None-Match", etags[path])
					w := httptest.NewRecorder()
					f.module.ServeHTTP(w, r)
					asset := strings.HasPrefix(path, "gems/") || strings.HasPrefix(path, "quick/")
					if asset {
						if w.Code != 404 {
							t.Fatalf("yanked %s %s: %d", method, path, w.Code)
						}
						continue
					}
					if w.Code != 200 && w.Code != 304 {
						t.Fatalf("index %s %s: %d", method, path, w.Code)
					}
					data := w.Body.Bytes()
					if method == "GET" && w.Code == 200 && strings.HasSuffix(path, ".gz") {
						z, err := gzip.NewReader(bytes.NewReader(data))
						if err != nil {
							t.Fatal(err)
						}
						data, err = io.ReadAll(z)
						_ = z.Close()
						if err != nil {
							t.Fatal(err)
						}
					}
					if bytes.Contains(data, []byte("owned")) {
						t.Fatalf("yanked entry in %s", path)
					}
				}
			}
			for _, data := range [][]byte{body, gem(t, "owned", version, "ruby", "different")} {
				if w := push(f, data); w.Code != 409 {
					t.Fatal("repush", w.Code)
				}
			}
			maintenance, _ := NewMaintenance(f.store, f.objects)
			if err := maintenance.Collect(t.Context(), time.Now().Add(24*time.Hour)); err != nil {
				t.Fatal(err)
			}
			c, err := f.store.Capacity(t.Context(), f.repo.ID)
			if err != nil || c.UsedBytes != int64(len(body)) || c.ObjectCount != 1 {
				t.Fatal(c, err)
			}
			audits, err := f.repos.ListAudits(t.Context(), repository.AuditQuery{Operation: "rubygems.yank", Limit: 100})
			if err != nil || len(audits) != 1 {
				t.Fatal(audits, err)
			}
			governanceChange(t, f.module.Preview(), f.repo.ID, "owned", version, "ruby", p.Digest, "1", "restore", "Bearer owned-admin", 409)
			governanceChange(t, f.module.Preview(), f.repo.ID, "owned", version, "ruby", "sha256:"+strings.Repeat("0", 64), "2", "restore", "Bearer owned-admin", 409)
			g := governanceChange(t, f.module.Preview(), f.repo.ID, "owned", version, "ruby", p.Digest, "2", "restore", "Bearer owned-admin", 200)
			if g.Yanked || g.GovernanceVersion != "3" {
				t.Fatal(g)
			}
			governanceChange(t, f.module.Preview(), f.repo.ID, "owned", version, "ruby", p.Digest, "3", "restore", "Bearer owned-admin", 200)
			w := request(f.module, "GET", "/rubygems/owned-gems/gems/"+p.FullName()+".gem", nil, "owned-admin")
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) {
				t.Fatal(w.Code)
			}
			audits, err = f.repos.ListAudits(t.Context(), repository.AuditQuery{Operation: "rubygems.restore", Limit: 100})
			if err != nil || len(audits) != 1 {
				t.Fatal(audits, err)
			}
		})
	}
}

func TestRubyGemsYankRejectsAmbiguousFormsAndPreservesPlatform(t *testing.T) {
	f := setup(t)
	for _, platform := range []string{"ruby", "java"} {
		if w := push(f, gem(t, "owned", "1.0.0", platform, "fixture")); w.Code != 201 {
			t.Fatal(w.Code)
		}
	}
	for _, form := range []string{"gem_name=owned&version=1.0.0&platform=ruby&platform=java", "gem_name=owned&version=1.0.0&ruby_abi=3.4", "gem_name=owned&version=1.0.0&extra=yes", "gem_name=owned&version=1.0.0&version=2.0", "gem_name=owned&version=%zz", "gem_name=owned;version=1.0.0", "gem_name=owned&version=1.0.0&platform=current", "gem_name=owned&version=1.0.0&gem_name[]=other", "gem_name=owned&version=1.0.0&platform= "} {
		r := httptest.NewRequest("DELETE", "/rubygems/owned-gems/api/v1/gems/yank", strings.NewReader(form))
		r.Header.Set("Authorization", "owned-admin")
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		f.module.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(form, w.Code)
		}
	}
	for _, tc := range []struct {
		body, ct string
		want     int
	}{{strings.Repeat("x", 4097), "application/x-www-form-urlencoded", 413}, {"gem_name=owned&version=1.0.0", "application/json", 415}} {
		r := httptest.NewRequest("DELETE", "/rubygems/owned-gems/api/v1/gems/yank", strings.NewReader(tc.body))
		r.Header.Set("Authorization", "owned-admin")
		r.Header.Set("Content-Type", tc.ct)
		w := httptest.NewRecorder()
		f.module.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(w.Code)
		}
	}
	if w := yankRequest(f.module, f.repo.Name, "owned", "1.0", "", "owned-admin"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if _, err := f.store.Get(t.Context(), f.repo.ID, "owned-1.0.0-java"); err != nil {
		t.Fatal("default ruby affected java", err)
	}
	if w := yankRequest(f.module, f.repo.Name, "owned", "1.0.0", "java", "owned-admin"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if rows, err := f.store.List(t.Context(), f.repo.ID, 10); err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
}

type retainedCorruption struct {
	domain.Store
	mode string
}

type failingGovernanceAudit struct {
	domain.Auditor
	fail bool
}

func (a *failingGovernanceAudit) RecordAudit(ctx context.Context, r repository.AuditRecord) error {
	if a.fail && (r.Operation == "rubygems.yank" || r.Operation == "rubygems.restore") {
		return errors.New("owned governance audit failure")
	}
	return a.Auditor.RecordAudit(ctx, r)
}

func (s retainedCorruption) GetRetained(ctx context.Context, repo, name, version, platform string) (domain.Publication, error) {
	p, err := s.Store.GetRetained(ctx, repo, name, version, platform)
	if s.mode == "metadata" {
		p.MetadataJSON = []byte(`{}`)
	} else if s.mode == "identity" {
		p.Name = "different"
	}
	return p, err
}

func TestRubyGemsGovernanceCorruptRestoreAndAuditFailureStayClosed(t *testing.T) {
	for _, mode := range []string{"missing", "bytes", "metadata", "identity", "audit"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			body := gem(t, "owned", "1.0.0", "ruby", "fixture")
			audit := &failingGovernanceAudit{Auditor: f.repos}
			store := domain.NewMemory(f.repos, audit)
			f.store = store
			m, err := New(options(f.repos, store, f.objects, f.repos))
			if err != nil {
				t.Fatal(err)
			}
			f.module = m
			if w := push(f, body); w.Code != 201 {
				t.Fatal(w.Code)
			}
			p, err := store.GetRetained(t.Context(), f.repo.ID, "owned", "1.0.0", "ruby")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "audit" {
				audit.fail = true
				if w := yankRequest(m, f.repo.Name, "owned", "1.0.0", "", "owned-admin"); w.Code != 500 {
					t.Fatal(w.Code)
				}
				got, _ := store.GetRetained(t.Context(), f.repo.ID, "owned", "1.0.0", "ruby")
				if got.Governance.Version != "1" || got.Governance.Yanked {
					t.Fatal(got)
				}
				audit.fail = false
			}
			if w := yankRequest(m, f.repo.Name, "owned", "1.0.0", "", "owned-admin"); w.Code != 200 {
				t.Fatal(w.Code)
			}
			switch mode {
			case "missing":
				err = f.objects.Delete(t.Context(), p.ObjectKey)
			case "bytes":
				err = f.objects.Put(t.Context(), p.ObjectKey, []byte("damaged"))
			case "metadata", "identity":
				m, err = New(options(f.repos, retainedCorruption{store, mode}, f.objects, f.repos))
			case "audit":
				audit.fail = true
			}
			if err != nil {
				t.Fatal(err)
			}
			governanceChange(t, m.Preview(), f.repo.ID, "owned", "1.0.0", "ruby", p.Digest, "2", "restore", "Bearer owned-admin", 500)
			got, err := store.GetRetained(t.Context(), f.repo.ID, "owned", "1.0.0", "ruby")
			if err != nil || !got.Governance.Yanked || got.Governance.Version != "2" {
				t.Fatal(got, err)
			}
			rows, err := f.repos.ListAudits(t.Context(), repository.AuditQuery{Operation: "rubygems.restore", Limit: 10})
			if err != nil || len(rows) != 0 {
				t.Fatal(rows, err)
			}
		})
	}
}
